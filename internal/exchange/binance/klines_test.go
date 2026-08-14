package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestKlinesParsing valida o parsing de um payload REAL de /api/v3/klines
// (formato de array posicional da Binance) via servidor mock — sem rede.
func TestKlinesParsing(t *testing.T) {
	payload := `[
	  [1752000000000,"60000.10","60500.00","59800.00","60200.50","1234.56000000",1752000059999,"74000000.0",1000,"500.0","30000000.0","0"],
	  [1752000060000,"60200.50","60800.00","60100.00","60750.25","900.00000000",1752000119999,"54600000.0",900,"450.0","27300000.0","0"]
	]`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	c := New()
	c.restBase = srv.URL // aponta o mock (só para o teste)

	candles, err := c.Klines(context.Background(), "BTCUSDT", "1m", 2, 10)
	if err != nil {
		t.Fatalf("Klines: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("esperava 2 candles, got %d", len(candles))
	}
	if candles[0].Close != 60200.50 {
		t.Fatalf("primeiro close esperado 60200.50, got %v", candles[0].Close)
	}
	if candles[1].Close != 60750.25 {
		t.Fatalf("segundo close esperado 60750.25, got %v", candles[1].Close)
	}
	if candles[0].OpenTime.UnixMilli() != 1752000000000 {
		t.Fatalf("openTime errado: %v", candles[0].OpenTime)
	}
}

// TestKlinesPagination valida que o loop de paginação para corretamente quando
// a API devolve uma página menor que o limite (fim do histórico).
func TestKlinesPagination(t *testing.T) {
	// Primeira chamada devolve 5, segunda devolve 0 (fim).
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		if calls == 1 {
			// 5 klines sintéticas — array de arrays posicionais.
			var arr []json.RawMessage
			for i := 0; i < 5; i++ {
				arr = append(arr, json.RawMessage(`[1752000000000,"100","101","99","100.5","1.0",1752000059999,"100",100,"50","50","0"]`))
			}
			b, _ := json.Marshal(arr)
			w.Write(b)
			return
		}
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c := New()
	c.restBase = srv.URL

	candles, err := c.Klines(context.Background(), "BTCUSDT", "1m", 100, 100)
	if err != nil {
		t.Fatalf("Klines: %v", err)
	}
	if len(candles) != 5 {
		t.Fatalf("esperava 5 candles (fim do histórico), got %d", len(candles))
	}
}

func TestIntervalMillis(t *testing.T) {
	if intervalMillis("1m") != 60_000 {
		t.Fatal("1m errado")
	}
	if intervalMillis("1h") != 3_600_000 {
		t.Fatal("1h errado")
	}
	if intervalMillis("1d") != 86_400_000 {
		t.Fatal("1d errado")
	}
	_ = time.Now() // time já importado (sem warning)
}
