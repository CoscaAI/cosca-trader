package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTicker24hAll_ParsesStringFields(t *testing.T) {
	// simula a resposta da Binance (campos numéricos como STRING)
	body := `[
		{"symbol":"BTCUSDT","priceChangePercent":"2.5","lastPrice":"50000.10","quoteVolume":"500000000.0"},
		{"symbol":"ETHUSDT","priceChangePercent":"-1.2","lastPrice":"3000.50","quoteVolume":"300000000.0"},
		{"symbol":"DOGEBTC","priceChangePercent":"3.3","lastPrice":"0.000001","quoteVolume":"1000.0"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/ticker/24hr" {
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New()
	c.restBase = srv.URL

	tickers, err := c.Ticker24hAll(context.Background())
	if err != nil {
		t.Fatalf("Ticker24hAll: %v", err)
	}
	if len(tickers) != 3 {
		t.Fatalf("esperava 3 tickers, veio %d", len(tickers))
	}
	btc := tickers[0]
	if btc.Symbol != "BTCUSDT" || btc.PriceChangePercent != 2.5 || btc.LastPrice != 50000.10 || btc.QuoteVolume != 500000000.0 {
		t.Fatalf("BTC parseado errado: %+v", btc)
	}
	if tickers[2].Symbol != "DOGEBTC" || !strings.HasSuffix(tickers[2].Symbol, "USDT") {
		t.Logf("DOGEBTC não é par USDT (esperado, filtrado depois)")
	}
}
