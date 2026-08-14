// report.go — o relatório científico completo (Fase 5): une o backtest com
// trades individuais, as métricas estatísticas, o Monte Carlo e o teste de
// significância em UM pacote que o /backtest devolve e o painel mostra.
// Este é o "laudo" da ferramenta científica: não 1 número, mas a distribuição
// e a probabilidade.
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// domainCandle é um alias local para manter as assinaturas concisas.
type domainCandle = domain.Candle

// ScientificReport é o laudo completo de uma estratégia sobre um histórico.
type ScientificReport struct {
	Strategy   string          `json:"strategy"`
	Symbol     string          `json:"symbol"`
	Periods    int             `json:"periods"`     // nº de velas
	Initial    decimal.Decimal `json:"initial"`
	Final      decimal.Decimal `json:"final"`
	Stats      Stats           `json:"stats"`
	EquityCurve []EquityPoint  `json:"equity_curve"`
	Trades     []TradeResult   `json:"trades"`
	MonteCarlo MonteCarloResult `json:"monte_carlo"`
	Significance SignificanceResult `json:"significance"`
	WalkForward WalkForwardResult `json:"walk_forward"`
	// Benchmark (lição do Freqtrade/OctoBot): o que o BUY-AND-HOLD teria feito
	// no mesmo período — sem isso, não sabemos se o bot tem edge ou surfou o
	// mercado. Edge = bot retorno − buy-and-hold retorno.
	Benchmark BenchmarkResult `json:"benchmark"`
}

// BenchmarkResult é o buy-and-hold do ativo no período do backtest.
type BenchmarkResult struct {
	BuyHoldPct float64 `json:"buy_hold_pct"` // retorno % do ativo (close→close, sem fees)
	EdgePct    float64 `json:"edge_pct"`     // retorno do bot − buy-and-hold (edge real)
	// BeatMarket: true se o bot superou o buy-and-hold no período.
	BeatMarket bool `json:"beat_market"`
}

// ExecConfig é a configuração de execução do backtest (lição Superalgos/
// OctoBot: sem slippage, "funciona no teste, quebra ao vivo").
type ExecConfig struct {
	// SlippagePct é a degradação de preço por lado (ex.: 0.001 = 0.1%).
	// Compra executa MAIS caro; venda executa MAIS barato. Pessimista, como
	// a casa. 0 = sem slippage (otimista).
	SlippagePct decimal.Decimal
}

// AnalyzeBacktest roda o backtest (com curva de equity e trades) e agrega
// todas as camadas científicas. Monte Carlo e significância são seedáveis
// para reprodução. Slippage/fees configuráveis via ExecConfig.
func AnalyzeBacktest(s Strategy, candles []domainCandle, initial, feePct decimal.Decimal, mcSims, sigTrials int, seed int64) ScientificReport {
	return AnalyzeBacktestWith(s, candles, initial, feePct, ExecConfig{}, mcSims, sigTrials, seed)
}

// AnalyzeBacktestWith é o AnalyzeBacktest com configuração de execução.
func AnalyzeBacktestWith(s Strategy, candles []domainCandle, initial, feePct decimal.Decimal, exec ExecConfig, mcSims, sigTrials int, seed int64) ScientificReport {
	trades := backtestTradesWith(s, candles, initial, feePct, exec)
	final := initial
	for _, t := range trades {
		final = final.Add(t.PnL)
	}
	stats := computeStats(trades, initial)

	// Walk-forward: treina a estratégia nos primeiros 70% e valida nos 30%.
	wf := walkForward(s, candles, initial, feePct, 0.70, seed)

	// Benchmark buy-and-hold (lição Freqtrade/OctoBot/Superalgos): o edge é
	// medido CONTRA o mercado, nunca no absoluto.
	bench := computeBenchmark(candles, initial, final)

	return ScientificReport{
		Strategy:     s.Name(),
		Symbol:       symbolOf(candles),
		Periods:      len(candles),
		Initial:      initial,
		Final:        final,
		Stats:        stats,
		EquityCurve:  equityCurveOf(trades, initial),
		Trades:       trades,
		MonteCarlo:   MonteCarlo(trades, initial, mcSims, seed),
		Significance: Significance(trades, sigTrials, seed+1),
		WalkForward:  wf,
		Benchmark:    bench,
	}
}

// computeBenchmark calcula o buy-and-hold do ativo no período e o edge do bot.
func computeBenchmark(candles []domainCandle, initial, final decimal.Decimal) BenchmarkResult {
	res := BenchmarkResult{}
	if len(candles) < 2 || !initial.IsPositive() {
		return res
	}
	first := candles[0].Close
	last := candles[len(candles)-1].Close
	if first <= 0 {
		return res
	}
	// Buy-and-hold: compra no primeiro close, vende no último (sem fees — é o
	// benchmark bruto do mercado).
	res.BuyHoldPct = (last - first) / first

	// Retorno do bot.
	botPct := final.Sub(initial).Div(initial).InexactFloat64()
	// Edge = o que o bot fez A MAIS que o mercado.
	res.EdgePct = botPct - res.BuyHoldPct
	res.BeatMarket = res.EdgePct > 0
	return res
}

// WalkForwardResult — treino/validação out-of-sample: a estratégia é avaliada
// na parte que ela NÃO "viu" durante o treino. Vantagem que sobrevive ao
// out-of-sample é muito mais provável de ser real (anti-overfitting).
type WalkForwardResult struct {
	TrainBars    int             `json:"train_bars"`
	TestBars     int             `json:"test_bars"`
	TrainPnL     decimal.Decimal `json:"train_pnl"`
	TestPnL      decimal.Decimal `json:"test_pnl"`
	TestReturnPct float64        `json:"test_return_pct"`
	TrainTrades  int             `json:"train_trades"`
	TestTrades   int             `json:"test_trades"`
	// Consistent é true se o TEST (out-of-sample) também lucrou — a
	// estratégia não é um artefato do treino.
	Consistent bool `json:"consistent"`
}

// walkForward divide o histórico em treino (trainFrac) e teste, roda o
// backtest em cada parte e compara. O "train" aqui usa a MESMA estratégia
// (sem parâmetros otimizados — o overfitting clássico seria otimizar no
// treino; nesta semente, medimos se a lógica sobrevive fora da amostra).
func walkForward(s Strategy, candles []domainCandle, initial, feePct decimal.Decimal, trainFrac float64, seed int64) WalkForwardResult {
	res := WalkForwardResult{}
	n := len(candles)
	if n == 0 {
		return res
	}
	cut := int(float64(n) * trainFrac)
	if cut < 1 {
		cut = 1
	}
	if cut >= n {
		cut = n - 1
	}

	trainCandles := candles[:cut]
	testCandles := candles[cut:]

	// Treino.
	trainTrades := backtestTrades(s, trainCandles, initial, feePct)
	res.TrainBars = len(trainCandles)
	res.TrainTrades = len(trainTrades)
	res.TrainPnL = sumPnL(trainTrades)

	// Teste (out-of-sample): o equity inicial do teste é o final do treino —
	// capital contínuo, como na operação real.
	capital := initial.Add(res.TrainPnL)
	testTrades := backtestTrades(s, testCandles, capital, feePct)
	res.TestBars = len(testCandles)
	res.TestTrades = len(testTrades)
	res.TestPnL = sumPnL(testTrades)
	if capital.IsPositive() {
		res.TestReturnPct = res.TestPnL.Div(capital).InexactFloat64()
	}
	res.Consistent = res.TestPnL.IsPositive()
	return res
}

// backtestTrades roda a estratégia e devolve os trades FECHADOS com PnL e
// fees — a matéria-prima de toda a análise estatística. Opera nos DOIS lados
// (long E short): "buy" abre long (ou fecha short), "sell" abre short (ou
// fecha long) — o flip automático acontece quando o sinal é contrário à
// posição aberta. O campo Stop do sinal é respeitado (execução intra-vela,
// pessimista, como a casa). Fees aplicadas nos dois lados.
func backtestTrades(s Strategy, candles []domainCandle, initial, feePct decimal.Decimal) []TradeResult {
	return backtestTradesWith(s, candles, initial, feePct, ExecConfig{})
}

// backtestTradesWith é o backtestTrades com configuração de execução
// (slippage). Aplica degradação de preço pessimista: compra executa mais
// cara, venda mais barata — a lição do Superalgos ("sem fees/slippage,
// funciona no teste e quebra ao vivo").
func backtestTradesWith(s Strategy, candles []domainCandle, initial, feePct decimal.Decimal, exec ExecConfig) []TradeResult {
	var out []TradeResult
	if len(candles) == 0 {
		return out
	}
	equity := initial
	position := decimal.Zero
	entry := decimal.Zero
	entrySide := ""
	held := 0
	idx := 0
	activeStop := decimal.Zero // stop-loss do trade aberto (do sinal de entrada)
	pendingTag := ""           // tag do sinal que abriu o trade atual

	// slip aplica a degradação pessimista ao preço de execução.
	slip := func(px decimal.Decimal, isBuy bool) decimal.Decimal {
		if exec.SlippagePct.Sign() <= 0 || !px.IsPositive() {
			return px
		}
		if isBuy {
			return px.Mul(decimal.NewFromInt(1).Add(exec.SlippagePct))
		}
		return px.Mul(decimal.NewFromInt(1).Sub(exec.SlippagePct))
	}

	finish := func(exit decimal.Decimal, reason string, barIdx int) {
		gross := decimal.Zero
		if entrySide == "buy" {
			gross = exit.Sub(entry).Mul(position)
		} else {
			gross = entry.Sub(exit).Mul(position)
		}
		fee := exit.Mul(position).Mul(feePct)
		pnl := gross.Sub(fee)
		equity = equity.Add(pnl)
		out = append(out, TradeResult{
			Symbol:     candles[0].Symbol,
			Side:       entrySide,
			EntryPx:    entry,
			ExitPx:     exit,
			Quantity:   position,
			PnL:        pnl,
			FeesPaid:   fee,
			HeldBars:   held,
			ExitReason: reason,
			EnterTag:   pendingTag,
		})
		position = decimal.Zero
		entry = decimal.Zero
		activeStop = decimal.Zero
		pendingTag = ""
		held = 0
		_ = barIdx
	}

	// open abre posição na direção pedida (fechando a oposta se houver flip).
	// O preço de ENTRADA executa com slippage pessimista (compra mais cara,
	// venda mais barata).
	open := func(side string, price, stop decimal.Decimal, tag string) {
		execPx := slip(price, side == "buy")
		if position.Sign() > 0 && entrySide != side {
			// FLIP: fecha a posição atual (com slip no lado oposto) e abre.
			finish(slip(price, entrySide != "buy"), "flip", idx)
		}
		if position.IsZero() {
			position = equity.Div(execPx)
			entry = execPx
			entrySide = side
			fee := execPx.Mul(position).Mul(feePct)
			equity = equity.Sub(fee)
			held = 0
			activeStop = stop
			pendingTag = tag
		}
	}

	for _, c := range candles {
		closePx := decimal.NewFromFloat(c.Close)
		lowPx := decimal.NewFromFloat(c.Low)
		highPx := decimal.NewFromFloat(c.High)

		// 1. STOP-LOSS primeiro (o preço pode ter varado o stop dentro da
		// vela — executamos no stop, pessimista e conservador como a casa).
		if position.Sign() > 0 && activeStop.Sign() > 0 {
			stopped := false
			exit := activeStop
			if entrySide == "buy" && lowPx.LessThanOrEqual(activeStop) {
				stopped = true
			}
			if entrySide == "sell" && highPx.GreaterThanOrEqual(activeStop) {
				stopped = true
			}
			if stopped {
				// Saída no stop executa com slippage no lado da venda.
				finish(slip(exit, entrySide != "buy"), "stop", idx)
			}
		}

		// 2. Sinais da estratégia — dois lados: buy abre/fecha long,
		// sell abre/fecha short.
		for _, sig := range s.OnCandle(c) {
			price := sig.Price
			if price.Sign() <= 0 {
				price = closePx
			}
			switch sig.Side {
			case "buy":
				open("buy", price, sig.Stop, sig.Reason)
			case "sell":
				open("sell", price, sig.Stop, sig.Reason)
			}
		}
		idx++
		held++
	}

	// Liquida posição remanescente no último preço.
	if position.Sign() > 0 {
		last := decimal.NewFromFloat(candles[len(candles)-1].Close)
		finish(slip(last, entrySide != "buy"), "end", len(candles))
	}
	return out
}

func sumPnL(trades []TradeResult) decimal.Decimal {
	sum := decimal.Zero
	for _, t := range trades {
		sum = sum.Add(t.PnL)
	}
	return sum
}

func symbolOf(candles []domainCandle) string {
	if len(candles) > 0 {
		return candles[0].Symbol
	}
	return ""
}

// equityCurveOf monta a curva de equity a partir dos trades fechados.
func equityCurveOf(trades []TradeResult, initial decimal.Decimal) []EquityPoint {
	curve := make([]EquityPoint, 0, len(trades)+1)
	curve = append(curve, EquityPoint{Index: 0, Equity: initial})
	eq := initial
	for i, t := range trades {
		eq = eq.Add(t.PnL)
		curve = append(curve, EquityPoint{Index: i + 1, Equity: eq})
	}
	return curve
}
