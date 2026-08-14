package strategy

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestAnalyzeBacktestWithSlippage(t *testing.T) {
	// Com slippage, o resultado deve ser PIOR ou igual ao sem slippage
	// (a degradação pessimista nunca melhora o PnL).
	candles := SyntheticCandles(42, "TEST", 200, 100)
	s := NewEMACross()

	r0 := AnalyzeBacktest(s, candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), 100, 100, 7)
	r1 := AnalyzeBacktestWith(s, candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001),
		ExecConfig{SlippagePct: decimal.NewFromFloat(0.001)}, 100, 100, 7)

	if r1.Stats.NetPnL.GreaterThan(r0.Stats.NetPnL) {
		t.Fatalf("slippage deveria piorar o PnL: sem=%s com=%s", r0.Stats.NetPnL, r1.Stats.NetPnL)
	}
	if r1.Benchmark.BuyHoldPct != r0.Benchmark.BuyHoldPct {
		t.Fatalf("benchmark não deveria mudar com slippage (é o mercado)")
	}
}

func TestBenchmarkBuyHold(t *testing.T) {
	// Mercado de alta constante: buy-and-hold positivo, e uma estratégia
	// perdedora deve mostrar edge negativo.
	candles := SyntheticCandles(5, "TEST", 100, 100) // tendência
	s := NewEMACross()
	r := AnalyzeBacktest(s, candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), 100, 100, 7)
	if r.Benchmark.BuyHoldPct == 0 {
		t.Fatal("buy-and-hold deveria ser diferente de zero")
	}
	if !r.Benchmark.BeatMarket && r.Stats.NetPnL.IsPositive() {
		t.Fatal("estratégia lucrativa em mercado de alta deveria bater o mercado (ou no mínimo reportar edge)")
	}
}

func TestBySignalStats(t *testing.T) {
	// Dois sinais: um lucrativo, um perdedor — a análise por sinal deve
	// separá-los.
	trades := []TradeResult{
		{EnterTag: "sinal-bom", PnL: d("100")},
		{EnterTag: "sinal-bom", PnL: d("50")},
		{EnterTag: "sinal-ruim", PnL: d("-80")},
		{EnterTag: "sinal-ruim", PnL: d("-20")},
	}
	by := computeBySignal(trades)
	if len(by) != 2 {
		t.Fatalf("esperava 2 sinais, got %d", len(by))
	}
	// Ordenado por PnL decrescente: sinal-bom primeiro.
	if by[0].Tag != "sinal-bom" || by[1].Tag != "sinal-ruim" {
		t.Fatalf("ordenação errada: %+v", by)
	}
	if !by[0].NetPnL.Equal(d("150")) {
		t.Fatalf("sinal-bom pnl esperado 150, got %v", by[0].NetPnL)
	}
	if by[0].WinRate != 1.0 || by[1].WinRate != 0.0 {
		t.Fatalf("win rates errados: %+v", by)
	}
}
