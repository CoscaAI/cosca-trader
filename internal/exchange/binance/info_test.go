// info_test.go — testes dos metadados de instrumento (P1-1): parsing do
// exchangeInfo, arredondamento (step_size/tick_size) e validação
// (min_qty/min_notional) antes do envio.
package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// exchangeInfoCanned é uma fatia do exchangeInfo real da Binance (BTCUSDT).
const exchangeInfoCanned = `{
  "timezone": "UTC",
  "serverTime": 1565246363776,
  "symbols": [
    {
      "symbol": "BTCUSDT",
      "status": "TRADING",
      "baseAsset": "BTC",
      "quoteAsset": "USDT",
      "filters": [
        {"filterType":"PRICE_FILTER","minPrice":"0.01","maxPrice":"10000000","tickSize":"0.01"},
        {"filterType":"LOT_SIZE","minQty":"0.00001","maxQty":"9000","stepSize":"0.00001"},
        {"filterType":"MIN_NOTIONAL","minNotional":"10","applyToMarket":true}
      ]
    }
  ]
}`

func newInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newInfoServerCaptured(t, nil)
}

// newInfoServerCaptured expõe o query string assinado do último POST /order.
func newInfoServerCaptured(t *testing.T, captured *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/exchangeInfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(exchangeInfoCanned))
	})
	mux.HandleFunc("/api/v3/order", func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			*captured = r.URL.RawQuery
		}
		w.Write([]byte(`{"symbol":"BTCUSDT","orderId":123,"clientOrderId":"cli-1","price":"50000.01","origQty":"0.00101000","executedQty":"0","status":"NEW","type":"LIMIT","side":"BUY","timeInForce":"GTC","stopPrice":"0","transactTime":1499405658657}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestInfoLoadsExchangeInfo(t *testing.T) {
	srv := newInfoServer(t)
	info := newInfo(srv.URL)
	if err := info.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	si, ok := info.Get("BTCUSDT")
	if !ok {
		t.Fatal("BTCUSDT não carregado do exchangeInfo")
	}
	if !si.TickSize.Equal(d("0.01")) || !si.StepSize.Equal(d("0.00001")) ||
		!si.MinQty.Equal(d("0.00001")) || !si.MinNotional.Equal(d("10")) {
		t.Errorf("regras do símbolo erradas: %+v", si)
	}
	if si.BaseAsset != "BTC" || si.QuoteAsset != "USDT" {
		t.Errorf("ativos errados: %+v", si)
	}
}

func TestRoundQtyAndPrice(t *testing.T) {
	// quantidade SEMPRE arredondada para baixo (nunca excede o lote).
	if q := roundQty(d("0.000015"), d("0.00001")); !q.Equal(d("0.00001")) {
		t.Errorf("roundQty para baixo falhou: %v", q)
	}
	if q := roundQty(d("0.00002"), d("0.00001")); !q.Equal(d("0.00002")) {
		t.Errorf("roundQty exato falhou: %v", q)
	}
	// preço arredondado para o tick mais próximo.
	if p := roundPrice(d("50000.005"), d("0.01")); !p.Equal(d("50000.01")) {
		t.Errorf("roundPrice falhou: %v", p)
	}
}

func TestPlaceOrderRoundsAndSendsClientOrderID(t *testing.T) {
	var captured string
	srv := newInfoServerCaptured(t, &captured)
	c := &Client{restBase: srv.URL, apiKey: "k", secret: "s"}

	// qty 0.001019 → arredonda para 0.00101 (múltiplo do step 0.00001, para
	// baixo); preço 50000.005 → 50000.01 (tick 0.01). Notional resultante
	// 0.00101 × 50000.01 = 50.5 ≥ min_notional 10 → envia.
	ord, err := c.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: d("0.001019"), Price: d("50000.005"), ClientOrderID: "cli-1",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.ID != "123" || ord.ClientOrderID != "cli-1" {
		t.Errorf("ordem devolvida errada: %+v", ord)
	}
	if !strings.Contains(captured, "quantity=0.00101") {
		t.Errorf("quantidade não arredondada para o step_size: %s", captured)
	}
	if !strings.Contains(captured, "price=50000.01") {
		t.Errorf("preço não arredondado para o tick_size: %s", captured)
	}
	if !strings.Contains(captured, "type=LIMIT") {
		t.Errorf("tipo não mapeado para a API Binance: %s", captured)
	}
	if !strings.Contains(captured, "newClientOrderId=cli-1") {
		t.Errorf("client_order_id ausente na requisição assinada: %s", captured)
	}
}

func TestPlaceOrderRejectsBelowMinimum(t *testing.T) {
	srv := newInfoServer(t)
	c := &Client{restBase: srv.URL, apiKey: "k", secret: "s"}

	// quantidade abaixo do min_qty (0.00001) → rejeitado antes de enviar.
	_, err := c.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("0.000001"), Price: d("50000"),
	})
	if err == nil || !strings.Contains(err.Error(), "abaixo do mínimo") {
		t.Fatalf("esperava rejeição por min_qty, veio %v", err)
	}

	// notional abaixo do min_notional (50000 × 0.00001 = 0.5 < 10) → rejeitado.
	_, err = c.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("0.00001"), Price: d("50000"),
	})
	if err == nil || !strings.Contains(err.Error(), "notional") {
		t.Fatalf("esperava rejeição por min_notional, veio %v", err)
	}

	// símbolo sem metadados → fail-closed.
	_, err = c.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "NOPEUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1"), Price: d("1"),
	})
	if err == nil || !strings.Contains(err.Error(), "sem metadados") {
		t.Fatalf("esperava fail-closed sem metadados, veio %v", err)
	}
}

func TestToStatusExpired(t *testing.T) {
	// P1-2: EXPIRED é estado terminal próprio, não rejeição.
	if toStatus("EXPIRED") != domain.OrderExpired {
		t.Errorf("EXPIRED deveria mapear para OrderExpired, veio %v", toStatus("EXPIRED"))
	}
	if toStatus("REJECTED") != domain.OrderRejected {
		t.Errorf("REJECTED deveria mapear para OrderRejected, veio %v", toStatus("REJECTED"))
	}
}

func TestToBinanceTypeMapping(t *testing.T) {
	cases := map[domain.OrderType]string{
		domain.OrderLimit:      "LIMIT",
		domain.OrderMarket:     "MARKET",
		domain.OrderStop:       "STOP_LOSS",
		domain.OrderStopLimit:  "STOP_LOSS_LIMIT",
		domain.OrderStopMarket: "STOP_LOSS",
	}
	for in, want := range cases {
		if got := toBinanceType(in); got != want {
			t.Errorf("toBinanceType(%s) = %q, esperava %q", in, got, want)
		}
	}
}

func TestClientPrice(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/ticker/price", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"symbol":"BTCUSDT","price":"50000.01"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{restBase: srv.URL}

	px, err := c.Price(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("Price: %v", err)
	}
	if !px.Equal(decimal.RequireFromString("50000.01")) {
		t.Errorf("preço errado: %v", px)
	}
}
