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
}

// AnalyzeBacktest roda o backtest (com curva de equity e trades) e agrega
// todas as camadas científicas. Monte Carlo e significância são seedáveis
// para reprodução.
func AnalyzeBacktest(s Strategy, candles []domainCandle, initial, feePct decimal.Decimal, mcSims, sigTrials int, seed int64) ScientificReport {
	trades := backtestTrades(s, candles, initial, feePct)
	final := initial
	for _, t := range trades {
		final = final.Add(t.PnL)
	}
	stats := computeStats(trades, initial)

	// Walk-forward: treina a estratégia nos primeiros 70% e valida nos 30%.
	wf := walkForward(s, candles, initial, feePct, 0.70, seed)

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
	}
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
// fees — a matéria-prima de toda a análise estatística. Segue as mesmas
// regras do Backtest original (long-only all-in), mas registra cada round-trip.
func backtestTrades(s Strategy, candles []domainCandle, initial, feePct decimal.Decimal) []TradeResult {
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
		})
		position = decimal.Zero
		entry = decimal.Zero
		activeStop = decimal.Zero
		held = 0
		_ = barIdx
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
				finish(exit, "stop", idx)
			}
		}

		// 2. Sinais da estratégia.
		for _, sig := range s.OnCandle(c) {
			price := sig.Price
			if price.Sign() <= 0 {
				price = closePx
			}
			switch sig.Side {
			case "buy":
				if position.IsZero() {
					position = equity.Div(price)
					entry = price
					entrySide = "buy"
					fee := price.Mul(position).Mul(feePct)
					equity = equity.Sub(fee)
					held = 0
					activeStop = sig.Stop // stop do sinal de entrada
				}
			case "sell":
				if position.Sign() > 0 {
					finish(price, "signal", idx)
				}
			}
		}
		idx++
		held++
	}

	// Liquida posição remanescente no último preço.
	if position.Sign() > 0 {
		last := decimal.NewFromFloat(candles[len(candles)-1].Close)
		finish(last, "end", len(candles))
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
