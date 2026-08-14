// serve_test.go — testes da camada HTTP do core (P0-1): auth fail-closed e
// CORS fechado. Exercita o roteador via httptest, sem subir listener.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/oms"
	"github.com/CoscaAI/cosca-trader/internal/paper"
	"github.com/CoscaAI/cosca-trader/internal/store"
)

// fakeHTTPBroker é um broker inerte para exercitar o roteador HTTP do OMS.
type fakeHTTPBroker struct{}

func (fakeHTTPBroker) PlaceOrder(_ context.Context, _ exchange.OrderRequest) (domain.Order, error) {
	return domain.Order{}, nil
}
func (fakeHTTPBroker) CancelOrder(_ context.Context, _, _ string) error     { return nil }
func (fakeHTTPBroker) Balances(_ context.Context) ([]domain.Balance, error) { return nil, nil }
func (fakeHTTPBroker) OpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return nil, nil
}
func (fakeHTTPBroker) StartUserStream(_ context.Context, _ exchange.Handler) error { return nil }

// d constrói um decimal exato (dinheiro nunca é float no caminho de teste).
func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func newTestMux(t *testing.T, sec apiSecurity) *http.ServeMux {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	e := engine.New(db)
	e.Emit(event.Event{Type: event.SystemStarted, Source: "test"})
	return newMux(e, nil, nil, nil, nil, nil, "0", sec, "observe")
}

// doGET executa uma requisição GET com cabeçalhos opcionais.
func doGET(mux *http.ServeMux, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHealthIsPublic(t *testing.T) {
	mux := newTestMux(t, apiSecurity{})
	rec := doGET(mux, "/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/health público deveria ser 200, veio %d", rec.Code)
	}
}

func TestSensitiveEndpointsRequireToken(t *testing.T) {
	for _, path := range []string{"/timeline", "/orders", "/positions", "/balances", "/ledger", "/paper"} {
		// Fail-closed: sem token configurado → 401.
		mux := newTestMux(t, apiSecurity{})
		if rec := doGET(mux, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s sem token deveria ser 401, veio %d", path, rec.Code)
		}
		// Com token configurado, mas sem Authorization → 401.
		mux = newTestMux(t, apiSecurity{token: "segredo"})
		if rec := doGET(mux, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s sem Bearer deveria ser 401, veio %d", path, rec.Code)
		}
		// Bearer errado → 401.
		mux = newTestMux(t, apiSecurity{token: "segredo"})
		rec := doGET(mux, path, map[string]string{"Authorization": "Bearer errado"})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s Bearer errado deveria ser 401, veio %d", path, rec.Code)
		}
		// Bearer correto → passa da auth (200/404 ok: auth aplicada antes do handler).
		mux = newTestMux(t, apiSecurity{token: "segredo"})
		rec = doGET(mux, path, map[string]string{"Authorization": "Bearer segredo"})
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Errorf("%s com Bearer válido não deveria ser bloqueado, veio %d", path, rec.Code)
		}
	}
}

func TestOriginBlockedByDefault(t *testing.T) {
	// CORS fail-closed: lista vazia → qualquer Origin é bloqueado (403), mesmo
	// com token válido. Mata o CSRF localhost (Simple Request text/plain).
	mux := newTestMux(t, apiSecurity{token: "segredo"})
	rec := doGET(mux, "/positions", map[string]string{
		"Authorization": "Bearer segredo",
		"Origin":        "http://evil.example.com",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Origin não permitida deveria ser 403, veio %d", rec.Code)
	}
}

func TestOriginAllowedWhenListed(t *testing.T) {
	mux := newTestMux(t, apiSecurity{token: "segredo", allowedOrigins: []string{"http://localhost:5173"}})
	rec := doGET(mux, "/positions", map[string]string{
		"Authorization": "Bearer segredo",
		"Origin":        "http://localhost:5173",
	})
	if rec.Code == http.StatusForbidden {
		t.Fatal("Origin na lista permitida não deveria ser bloqueada")
	}
	// outra origem fora da lista → 403
	rec = doGET(mux, "/positions", map[string]string{
		"Authorization": "Bearer segredo",
		"Origin":        "http://outra.example.com",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Origin fora da lista deveria ser 403, veio %d", rec.Code)
	}
}

func TestNoOriginRequestPasses(t *testing.T) {
	// Clientes nativos/desktop sem header Origin (curl, Wails) passam.
	mux := newTestMux(t, apiSecurity{token: "segredo"})
	rec := doGET(mux, "/orders", map[string]string{"Authorization": "Bearer segredo"})
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("request mesmo-origin (sem Origin) não deveria ser bloqueado, veio %d", rec.Code)
	}
}

func TestPositionViewComputesUnrealizedPnL(t *testing.T) {
	pos := domain.Position{
		Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy,
		Quantity: d("2"), AvgEntryPrice: d("100"), MarkPrice: d("120"),
	}
	views := positionViews([]domain.Position{pos})
	if len(views) != 1 {
		t.Fatalf("esperava 1 view, veio %d", len(views))
	}
	if !views[0].UnrealizedPnL.Equal(d("40")) {
		t.Errorf("unrealized_pnl = %v, esperava 40", views[0].UnrealizedPnL)
	}
	// pct = 40 / (100×2) × 100 = 20%.
	if !views[0].UnrealizedPnLPct.Equal(d("20")) {
		t.Errorf("unrealized_pnl_pct = %v, esperava 20", views[0].UnrealizedPnLPct)
	}
	// Short: (entry - mark) × qty.
	views = positionViews([]domain.Position{{
		Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideSell,
		Quantity: d("2"), AvgEntryPrice: d("100"), MarkPrice: d("90"),
	}})
	if !views[0].UnrealizedPnL.Equal(d("20")) {
		t.Errorf("short unrealized_pnl = %v, esperava 20", views[0].UnrealizedPnL)
	}
	// Sem mark (zero) → PnL não realizado zero, pct zero.
	views = positionViews([]domain.Position{{
		Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy,
		Quantity: d("2"), AvgEntryPrice: d("100"),
	}})
	if !views[0].UnrealizedPnL.IsZero() || !views[0].UnrealizedPnLPct.IsZero() {
		t.Errorf("sem mark deveria zerar pnl: %+v", views[0])
	}
}

func TestPositionsEndpointIncludesUnrealizedPnL(t *testing.T) {
	db, _ := store.Open(t.TempDir() + "/t.db")
	defer db.Close()
	e := engine.New(db)
	omsEngine := oms.New(&fakeHTTPBroker{}, func(event.Event) error { return nil })
	omsEngine.ApplyTrade(domain.Trade{ID: "t1", Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy, Price: d("100"), Quantity: d("2"), Timestamp: time.Now()})
	omsEngine.ApplyMarkPrice("BTCUSDT", "binance", d("120"))
	mux := newMux(e, omsEngine, nil, nil, nil, nil, "0", apiSecurity{token: "segredo"}, "observe")
	rec := doGET(mux, "/positions", map[string]string{"Authorization": "Bearer segredo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("/positions deveria ser 200, veio %d", rec.Code)
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("/positions inválido: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("esperava 1 posição, veio %d", len(body))
	}
	if body[0]["unrealized_pnl"] == nil || body[0]["unrealized_pnl_pct"] == nil {
		t.Errorf("unrealized_pnl/pct ausentes no /positions: %+v", body[0])
	}
}

func TestHealthReportsMode(t *testing.T) {
	mux := newTestMux(t, apiSecurity{})
	rec := doGET(mux, "/health", nil)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health inválido: %v", err)
	}
	if body["mode"] != "observe" {
		t.Errorf("mode = %v, esperava observe", body["mode"])
	}
}

func TestPaperEndpointWithoutPaperMode(t *testing.T) {
	// /paper existe mas responde 503 quando o modo paper não está ativo.
	db, _ := store.Open(t.TempDir() + "/t.db")
	defer db.Close()
	e := engine.New(db)
	mux := newMux(e, nil, nil, nil, nil, nil, "0", apiSecurity{token: "segredo"}, "observe")
	rec := doGET(mux, "/paper", map[string]string{"Authorization": "Bearer segredo"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/paper sem modo paper deveria ser 503, veio %d", rec.Code)
	}
}

func TestPaperEndpointWithPaperMode(t *testing.T) {
	db, _ := store.Open(t.TempDir() + "/t.db")
	defer db.Close()
	e := engine.New(db)
	pb := paper.New()
	pb.SetPrice("BTCUSDT", decimal.NewFromFloat(60000))
	mux := newMux(e, nil, pb, nil, nil, nil, "0", apiSecurity{token: "segredo"}, "paper")
	rec := doGET(mux, "/paper", map[string]string{"Authorization": "Bearer segredo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("/paper com modo paper deveria ser 200, veio %d", rec.Code)
	}
	var s struct {
		Mode           string `json:"mode"`
		InitialCapital string `json:"initial_capital"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("/paper inválido: %v", err)
	}
	if s.Mode != "paper" {
		t.Errorf("mode = %q, esperava paper", s.Mode)
	}
	if s.InitialCapital == "" {
		t.Error("initial_capital ausente no /paper")
	}
}
