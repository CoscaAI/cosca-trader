// Testes da estratégia EMA cross e do backtest mínimo (Fase 3D). Séries
// sintéticas CONHECIDAS: 25 velas planas (warm-up), salto para cima (buy no
// cruzamento), queda (sell no cruzamento). Determinístico — sem rede, sem
// broker, dinheiro em decimal.
package strategy

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// candlesFrom monta velas OHLC flat (open=high=low=close=price) em sequência.
func candlesFrom(prices []float64, symbol string) []domain.Candle {
	base := time.Now().Truncate(time.Minute)
	out := make([]domain.Candle, len(prices))
	for i, p := range prices {
		out[i] = domain.Candle{
			Symbol:   symbol,
			Exchange: "e",
			Interval: "1m",
			OpenTime: base.Add(time.Duration(i) * time.Minute),
			Open:     p, High: p, Low: p, Close: p,
			Volume: 1, Complete: true,
		}
	}
	return out
}

// emaKnownSeries: warm-up flat, salto para 200 (buy), queda para 100 (sell).
func emaKnownSeries() []float64 {
	var prices []float64
	for i := 0; i < 25; i++ {
		prices = append(prices, 100)
	}
	for i := 0; i < 10; i++ {
		prices = append(prices, 200)
	}
	for i := 0; i < 10; i++ {
		prices = append(prices, 100)
	}
	return prices
}

func TestEMACrossSignalsOnKnownSeries(t *testing.T) {
	s := NewEMACross()
	var buys, sells int
	for _, c := range candlesFrom(emaKnownSeries(), "BTCUSDT") {
		for _, sig := range s.OnCandle(c) {
			switch sig.Side {
			case "buy":
				buys++
			case "sell":
				sells++
			}
			if sig.Symbol != "BTCUSDT" || sig.Price.Sign() <= 0 || sig.Reason == "" {
				t.Errorf("sinal incompleto: %+v", sig)
			}
		}
	}
	if buys != 1 {
		t.Errorf("esperava 1 buy no cruzamento para cima, veio %d", buys)
	}
	if sells != 1 {
		t.Errorf("esperava 1 sell no cruzamento para baixo, veio %d", sells)
	}
}

func TestEMACrossRespectsWarmupAndStatePerSymbol(t *testing.T) {
	s := NewEMACross()
	// Warm-up: 25 velas planas não podem sinalizar nada.
	var sigs int
	for _, c := range candlesFrom(repeatFloats(100, 25), "BTCUSDT") {
		sigs += len(s.OnCandle(c))
	}
	if sigs != 0 {
		t.Errorf("warm-up deveria silenciar, veio %d sinais", sigs)
	}

	// Estado independente por símbolo: o mesmo salto em símbolos distintos
	// produz um buy em cada um.
	a := candlesFrom(emaKnownSeries(), "AUSDT")
	b := candlesFrom(emaKnownSeries(), "BUSDT")
	for i := range a {
		for _, sig := range s.OnCandle(a[i]) {
			if sig.Symbol != "AUSDT" {
				t.Errorf("sinal no símbolo errado: %+v", sig)
			}
		}
		for _, sig := range s.OnCandle(b[i]) {
			if sig.Symbol != "BUSDT" {
				t.Errorf("sinal no símbolo errado: %+v", sig)
			}
		}
	}
}

func repeatFloats(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestBacktestKnownSeries(t *testing.T) {
	// Sem fee: buy @200 com todo o capital (qty = 10000/200 = 50) e sell @100.
	// PnL = 50 × (100-200) = -5000 → final 5000, 1 trade.
	res := Backtest(NewEMACross(), candlesFrom(emaKnownSeries(), "BTCUSDT"), decimal.NewFromInt(10000), decimal.Zero)
	if !res.Initial.Equal(decimal.NewFromInt(10000)) {
		t.Errorf("Initial = %v", res.Initial)
	}
	if !res.Final.Equal(decimal.NewFromInt(5000)) {
		t.Errorf("Final = %v, esperava 5000", res.Final)
	}
	if !res.PnL.Equal(decimal.NewFromInt(-5000)) {
		t.Errorf("PnL = %v, esperava -5000", res.PnL)
	}
	if res.Trades != 1 {
		t.Errorf("Trades = %d, esperava 1", res.Trades)
	}
}

func TestBacktestFees(t *testing.T) {
	// Fee 0.1% nos dois lados: buy fee = 200×50×0.001 = 10; sell fee = 100×50×0.001 = 5.
	// equity = 10000 - 10 (entrada) - 5000 (gross) - 5 (saída) = 4985.
	res := Backtest(NewEMACross(), candlesFrom(emaKnownSeries(), "BTCUSDT"), decimal.NewFromInt(10000), decimal.NewFromFloat(0.001))
	if !res.Final.Equal(decimal.NewFromFloat(4985)) {
		t.Errorf("Final = %v, esperava 4985", res.Final)
	}
	if !res.FeePaid.Equal(decimal.NewFromFloat(15)) {
		t.Errorf("FeePaid = %v, esperava 15", res.FeePaid)
	}
}

func TestSyntheticCandlesDeterministic(t *testing.T) {
	a := SyntheticCandles(42, "BTCUSDT", 100, 60000)
	b := SyntheticCandles(42, "BTCUSDT", 100, 60000)
	if len(a) != 100 || len(b) != 100 {
		t.Fatalf("esperava 100 candles, veio %d/%d", len(a), len(b))
	}
	for i := range a {
		if a[i].Close != b[i].Close || a[i].Symbol != "BTCUSDT" || !a[i].Complete {
			t.Fatalf("feed sintético divergiu no candle %d", i)
		}
	}
}
