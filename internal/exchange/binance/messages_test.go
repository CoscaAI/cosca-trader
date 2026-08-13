package binance

import (
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

func TestHandleKline(t *testing.T) {
	// Formato REAL da Binance: `e`+`E` no topo e `l`+`L`, `v`+`V` dentro de `k`.
	payload := []byte(`{
		"e": "kline", "E": 1750000000000, "s": "BTCUSDT",
		"k": {
			"t": 1750000000000, "T": 1750000059999, "s": "BTCUSDT", "i": "1m",
			"f": 100, "L": 200,
			"o": "100.5", "h": "101.2", "l": "99.8", "c": "100.9",
			"v": "1234.5", "n": 42, "x": true,
			"q": "123450.0", "V": "500.0", "Q": "50000.0", "B": "0"
		}
	}`)

	var got domain.Candle
	err := handleMessage(payload, "binance", exchange.Handler{
		OnCandle: func(c domain.Candle) { got = c },
	})
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	if got.Symbol != "BTCUSDT" || got.Exchange != "binance" || got.Interval != "1m" {
		t.Errorf("metadados errados: %+v", got)
	}
	if got.Open != 100.5 || got.High != 101.2 || got.Low != 99.8 || got.Close != 100.9 {
		t.Errorf("OHLC errado: %+v", got)
	}
	if got.Volume != 1234.5 || got.Trades != 42 || !got.Complete {
		t.Errorf("volume/trades/complete errados: %+v", got)
	}
	if !got.OpenTime.Equal(time.UnixMilli(1750000000000)) {
		t.Errorf("OpenTime errado: %v", got.OpenTime)
	}
}

func TestHandleTrade(t *testing.T) {
	payload := []byte(`{"e": "trade", "E": 1750000100000, "s": "ETHUSDT", "p": "2500.75", "q": "0.5", "T": 1750000100000, "t": 999}`)

	var got domain.Tick
	err := handleMessage(payload, "binance", exchange.Handler{
		OnTick: func(tk domain.Tick) { got = tk },
	})
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	if got.Symbol != "ETHUSDT" || got.Price != 2500.75 || got.Quantity != 0.5 {
		t.Errorf("tick errado: %+v", got)
	}
	if !got.Time.Equal(time.UnixMilli(1750000100000)) {
		t.Errorf("Time errado: %v", got.Time)
	}
}

func TestHandleUnknownIgnored(t *testing.T) {
	// Resposta de SUBSCRIBE não tem `e` — deve ser ignorada sem erro e sem dispatch.
	called := false
	err := handleMessage([]byte(`{"result": null, "id": 1}`), "binance", exchange.Handler{
		OnCandle: func(domain.Candle) { called = true },
		OnTick:   func(domain.Tick) { called = true },
	})
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	if called {
		t.Error("mensagem de subscribe não deveria disparar handlers")
	}
}

func TestHandleMalformed(t *testing.T) {
	if err := handleMessage([]byte(`{not json`), "binance", exchange.Handler{}); err == nil {
		t.Error("esperava erro para JSON malformado")
	}
}

// Regression: a Binance manda `e` (string) E `E` (número) no mesmo payload.
// Sem o campo `E` explícito, o matching case-insensitive do encoding/json
// estoura ao tentar casar o número com o campo string `e`.
func TestEventTypeVsEventTimeNoCollision(t *testing.T) {
	payload := []byte(`{"e":"trade","E":1786648572340,"s":"BTCUSDT","t":1,"p":"1.0","q":"1.0","T":1786648572340,"m":false,"M":true}`)

	var got domain.Tick
	err := handleMessage(payload, "binance", exchange.Handler{
		OnTick: func(tk domain.Tick) { got = tk },
	})
	if err != nil {
		t.Fatalf("colisão e/E não deveria gerar erro: %v", err)
	}
	if got.Symbol != "BTCUSDT" || got.Price != 1.0 {
		t.Errorf("tick errado: %+v", got)
	}
}
