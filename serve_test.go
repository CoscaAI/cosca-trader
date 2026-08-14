// serve_test.go — testes da camada HTTP do core (P0-1): auth fail-closed e
// CORS fechado. Exercita o roteador via httptest, sem subir listener.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/paper"
	"github.com/CoscaAI/cosca-trader/internal/store"
)

func newTestMux(t *testing.T, sec apiSecurity) *http.ServeMux {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	e := engine.New(db)
	e.Emit(event.Event{Type: event.SystemStarted, Source: "test"})
	return newMux(e, nil, nil, "0", sec, "observe")
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
	mux := newMux(e, nil, nil, "0", apiSecurity{token: "segredo"}, "observe")
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
	mux := newMux(e, nil, pb, "0", apiSecurity{token: "segredo"}, "paper")
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
