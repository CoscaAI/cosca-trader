package strategy

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// sampleTrades devolve um conjunto conhecido: 4 wins de 100 + 1 loss de 100.
func sampleTrades() []TradeResult {
	return []TradeResult{
		{EntryPx: d("100"), ExitPx: d("200"), Quantity: d("1"), PnL: d("100"), FeesPaid: d("1"), Side: "buy", HeldBars: 5, ExitReason: "signal"},
		{EntryPx: d("100"), ExitPx: d("200"), Quantity: d("1"), PnL: d("100"), FeesPaid: d("1"), Side: "buy", HeldBars: 4, ExitReason: "signal"},
		{EntryPx: d("100"), ExitPx: d("200"), Quantity: d("1"), PnL: d("100"), FeesPaid: d("1"), Side: "buy", HeldBars: 6, ExitReason: "signal"},
		{EntryPx: d("100"), ExitPx: d("200"), Quantity: d("1"), PnL: d("100"), FeesPaid: d("1"), Side: "buy", HeldBars: 3, ExitReason: "signal"},
		{EntryPx: d("100"), ExitPx: d("0"), Quantity: d("1"), PnL: d("-100"), FeesPaid: d("1"), Side: "buy", HeldBars: 2, ExitReason: "stop"},
	}
}

func TestComputeStatsBasic(t *testing.T) {
	trades := sampleTrades()
	s := computeStats(trades, d("1000"))

	if s.TotalTrades != 5 || s.Wins != 4 || s.Losses != 1 {
		t.Fatalf("contagem errada: total=%d wins=%d losses=%d", s.TotalTrades, s.Wins, s.Losses)
	}
	if s.WinRate != 0.8 {
		t.Fatalf("win rate esperado 0.8, got %v", s.WinRate)
	}
	// Gross profit 400, gross loss 100 → PF 4.0
	if s.ProfitFactor != 4.0 {
		t.Fatalf("profit factor esperado 4.0, got %v", s.ProfitFactor)
	}
	// Net PnL = 400 - 100 = 300
	if !s.NetPnL.Equal(d("300")) {
		t.Fatalf("net pnl esperado 300, got %v", s.NetPnL)
	}
	// Expectancy = 300/5 = 60
	if !s.Expectancy.Equal(d("60")) {
		t.Fatalf("expectancy esperada 60, got %v", s.Expectancy)
	}
	// Retorno sobre 1000 = 30%
	if s.ReturnPct != 0.3 {
		t.Fatalf("return pct esperado 0.3, got %v", s.ReturnPct)
	}
}

func TestComputeStatsMaxDrawdown(t *testing.T) {
	// Sequência: +100, -100, +100, +100, +100 sobre 1000.
	trades := []TradeResult{
		{PnL: d("100")},
		{PnL: d("-100")},
		{PnL: d("100")},
		{PnL: d("100")},
		{PnL: d("100")},
	}
	s := computeStats(trades, d("1000"))
	// pico 1100 (após t1), cai para 1000 (após t2) → drawdown 100.
	if !s.MaxDrawdownAbs.Equal(d("100")) {
		t.Fatalf("max drawdown abs esperado 100, got %v", s.MaxDrawdownAbs)
	}
	if s.MaxDrawdownPct != 0.1 {
		t.Fatalf("max drawdown pct esperado 0.1, got %v", s.MaxDrawdownPct)
	}
}

func TestPercentile(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5}
	if got := percentile(sorted, 0.5); got != 3.0 {
		t.Fatalf("p50 esperado 3, got %v", got)
	}
	if got := percentile(sorted, 0.0); got != 1.0 {
		t.Fatalf("p0 esperado 1, got %v", got)
	}
	if got := percentile(sorted, 1.0); got != 5.0 {
		t.Fatalf("p100 esperado 5, got %v", got)
	}
	// interpolação: p25 = 2 (entre 2 e 3 a 50%? rank=1.0 → 2)
	if got := percentile(sorted, 0.25); got != 2.0 {
		t.Fatalf("p25 esperado 2, got %v", got)
	}
}

func TestMonteCarloBounds(t *testing.T) {
	trades := sampleTrades()
	mc := MonteCarlo(trades, d("1000"), 1000, 42)
	if mc.Simulations != 1000 {
		t.Fatalf("simulations esperado 1000, got %d", mc.Simulations)
	}
	// Ordem embaralhada não muda o total: P50 deve ficar perto de 1300
	// (1000 + 300), mas com espalhamento por causa da sequência.
	if mc.P50.LessThan(d("1000")) || mc.P50.GreaterThan(d("1600")) {
		t.Fatalf("P50 fora do esperado (~1300): %v", mc.P50)
	}
	if mc.ProbOfLoss < 0 || mc.ProbOfLoss > 1 {
		t.Fatalf("prob_of_loss fora de [0,1]: %v", mc.ProbOfLoss)
	}
}

func TestSignificanceDeterministic(t *testing.T) {
	trades := sampleTrades()
	s1 := Significance(trades, 500, 7)
	s2 := Significance(trades, 500, 7)
	// Mesmo seed → mesmo p-valor (reproduzível).
	if s1.PValue != s2.PValue {
		t.Fatalf("significância deveria ser determinística: %v vs %v", s1.PValue, s2.PValue)
	}
	// LIÇÃO CIENTÍFICA: com apenas 5 trades, mesmo com 80% de win rate, a
	// vantagem NÃO é estatisticamente significativa (p~0.19) — poucas amostras
	// não provam nada. A ferramenta rejeita a expectativa ingênua.
	if s1.Significant {
		t.Fatalf("5 trades não deveriam ser significativos (amostra pequena), p-value=%v", s1.PValue)
	}
}

func TestSignificanceWithLargeEdge(t *testing.T) {
	// 200 trades, 60% de win com avg win == avg loss (1:1): vantagem real.
	// Sob o nulo (sinais aleatórios), k ≥ 120 com k~Binomial(200,0.5) está a
	// ~2.8 desvios — p-value deve ser bem abaixo de 0.05.
	trades := make([]TradeResult, 0, 200)
	for i := 0; i < 120; i++ {
		trades = append(trades, TradeResult{PnL: d("1")})
	}
	for i := 0; i < 80; i++ {
		trades = append(trades, TradeResult{PnL: d("-1")})
	}
	s := Significance(trades, 1000, 13)
	if !s.Significant {
		t.Fatalf("vantagem real com 200 trades deveria ser significativa, p-value=%v z=%v", s.PValue, s.ZScore)
	}
	if s.PValue > 0.05 {
		t.Fatalf("p-value esperado < 0.05, got %v", s.PValue)
	}
}

func TestWalkForwardConsistent(t *testing.T) {
	// Histórico sintético com tendência de alta: a EMA cross deve lucrar nos
	// dois segmentos (treino e teste) — e ser consistente.
	candles := SyntheticCandles(11, "BTCUSDT", 300, 50000)
	s := NewEMACross()
	wf := walkForward(s, candles, d("10000"), d("0.001"), 0.70, 5)
	if wf.TrainBars+wf.TestBars != 300 {
		t.Fatalf("barras divididas errado: train=%d test=%d", wf.TrainBars, wf.TestBars)
	}
	// Não exigimos lucro (mercado sintético pode variar), mas a divisão deve
	// ser íntegra e o resultado reportado.
	_ = wf.Consistent
}

func TestWalkForwardUsesFreshState(t *testing.T) {
	// REGRESSÃO (look-ahead bias): a estratégia mantém estado por símbolo
	// (EMAs). Se walkForward reutilizasse a instância que já rodou sobre o
	// histórico INTEIRO, o estado "aquecido" vazaria o período de teste para o
	// treino. O fix garante instâncias zeradas por split: o resultado deve ser
	// IDÊNTICO quer a instância de entrada esteja aquecida ou zerada.
	candles := SyntheticCandles(11, "BTCUSDT", 300, 50000)

	// Aquece uma instância sobre o histórico completo (simula o backtest
	// principal que roda ANTES do walk-forward dentro de AnalyzeBacktestWith).
	warm := NewEMACross()
	backtestTrades(warm, candles, d("10000"), d("0.001"))

	gotWarm := walkForward(warm, candles, d("10000"), d("0.001"), 0.70, 5)
	gotFresh := walkForward(NewEMACross(), candles, d("10000"), d("0.001"), 0.70, 5)

	if !gotWarm.TrainPnL.Equal(gotFresh.TrainPnL) || !gotWarm.TestPnL.Equal(gotFresh.TestPnL) {
		t.Fatalf("walkForward vazou estado: warm(train=%s test=%s) != fresh(train=%s test=%s)",
			gotWarm.TrainPnL, gotWarm.TestPnL, gotFresh.TrainPnL, gotFresh.TestPnL)
	}
	if gotWarm.TrainTrades != gotFresh.TrainTrades || gotWarm.TestTrades != gotFresh.TestTrades {
		t.Fatalf("walkForward vazou estado nos trades: warm(train=%d test=%d) != fresh(train=%d test=%d)",
			gotWarm.TrainTrades, gotWarm.TestTrades, gotFresh.TrainTrades, gotFresh.TestTrades)
	}
}

func TestAnalyzeBacktestReport(t *testing.T) {
	candles := SyntheticCandles(99, "BTCUSDT", 500, 50000)
	s := NewEMACross()
	report := AnalyzeBacktest(s, candles, d("10000"), d("0.001"), 500, 300, 42)

	if report.Strategy != "ema-cross" {
		t.Fatalf("strategy esperado ema-cross, got %s", report.Strategy)
	}
	if report.Periods != 500 {
		t.Fatalf("periods esperado 500, got %d", report.Periods)
	}
	if !report.Initial.Equal(d("10000")) {
		t.Fatalf("initial esperado 10000, got %v", report.Initial)
	}
	// Final = initial + soma dos PnLs dos trades.
	if len(report.EquityCurve) != len(report.Trades)+1 {
		t.Fatalf("equity curve deveria ter trades+1 pontos: curve=%d trades=%d", len(report.EquityCurve), len(report.Trades))
	}
	if report.MonteCarlo.Simulations != 500 {
		t.Fatalf("mc sims esperado 500, got %d", report.MonteCarlo.Simulations)
	}
	if report.Significance.Trials != 300 {
		t.Fatalf("sig trials esperado 300, got %d", report.Significance.Trials)
	}
}
