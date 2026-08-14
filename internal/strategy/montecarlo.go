// montecarlo.go — simulação de Monte Carlo e teste de significância (Fase 5).
// Um backtest entrega UMA curva de equity. Monte Carlo responde: "se a sorte
// tivesse embaralhado a ORDEM dos trades, quantas curvas teriam dado prejuízo?"
// E o p-valor responde: "a vantagem desta estratégia é real, ou é o mesmo que
// um sorteio de moedas com a mesma frequência de acertos?"
package strategy

import (
	"math"
	"math/rand"

	"github.com/shopspring/decimal"
)

// MonteCarloResult resume a simulação.
type MonteCarloResult struct {
	Simulations int             `json:"simulations"`
	Initial     decimal.Decimal `json:"initial"`
	// Percentis da equity FINAL após N simulações de ordem embaralhada.
	P5   decimal.Decimal `json:"p5"`
	P50  decimal.Decimal `json:"p50"`
	P95  decimal.Decimal `json:"p95"`
	// ProbOfLoss é a fração de simulações que terminou ABAIXO do capital
	// inicial — o "risco de perder dinheiro" da estratégia na ordem dos trades.
	ProbOfLoss float64 `json:"prob_of_loss"`
	// ProbBelowZeroEquity é a fração de simulações que tocou equity ≤ 0
	// (ruína) em algum ponto — o risco de quebra.
	ProbRuin float64 `json:"prob_ruin"`
	// WorstEquity é o pior resultado final entre todas as simulações.
	WorstEquity decimal.Decimal `json:"worst_equity"`
	BestEquity  decimal.Decimal `json:"best_equity"`
}

// MonteCarlo embaralha a ORDEM dos trades (preservando o conjunto de PnLs —
// mesmo win rate, mesmo tamanho médio) e recomputa a equity curve. Isso mede
// o risco de SEQUÊNCIA: uma estratégia lucrativa pode quebrar se os losses
// vierem em rajada. Determinístico via seed (reproduzível).
func MonteCarlo(trades []TradeResult, initial decimal.Decimal, sims int, seed int64) MonteCarloResult {
	res := MonteCarloResult{Simulations: sims, Initial: initial, WorstEquity: initial, BestEquity: initial}
	if len(trades) == 0 || sims <= 0 {
		return res
	}

	pnls := make([]float64, len(trades))
	for i, t := range trades {
		pnls[i] = t.PnL.InexactFloat64()
	}

	rng := rand.New(rand.NewSource(seed))
	finals := make([]float64, 0, sims)
	ruins := 0
	losses := 0
	worst := math.Inf(1)
	best := math.Inf(-1)

	for s := 0; s < sims; s++ {
		// Embaralha o vetor de PnLs (Fisher-Yates).
		shuffled := append([]float64(nil), pnls...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		eq := initial.InexactFloat64()
		ruined := false
		for _, p := range shuffled {
			eq += p
			if eq <= 0 {
				ruined = true
			}
		}
		finals = append(finals, eq)
		if ruined {
			ruins++
		}
		if eq < initial.InexactFloat64() {
			losses++
		}
		if eq < worst {
			worst = eq
		}
		if eq > best {
			best = eq
		}
	}

	sortFloats(finals)
	res.P5 = decimal.NewFromFloat(percentile(finals, 0.05))
	res.P50 = decimal.NewFromFloat(percentile(finals, 0.50))
	res.P95 = decimal.NewFromFloat(percentile(finals, 0.95))
	res.ProbOfLoss = float64(losses) / float64(sims)
	res.ProbRuin = float64(ruins) / float64(sims)
	res.WorstEquity = decimal.NewFromFloat(worst)
	res.BestEquity = decimal.NewFromFloat(best)
	return res
}

// SignificanceResult é o teste de significância estatística (bootstrap de
// permutação). Compara o PnL real da estratégia contra N permutações
// aleatórias dos MESMOS PnLs com SINAIS embaralhados — o nulo é "a estratégia
// não tem vantagem; qualquer sequência de + e − seria igualmente provável".
type SignificanceResult struct {
	// Trials é o número de permutações nulas.
	Trials int `json:"trials"`
	// ObservedPnL é o PnL real da estratégia.
	ObservedPnL decimal.Decimal `json:"observed_pnl"`
	// MeanNullPnL é o PnL médio das permutações nulas (deve ser ~0).
	MeanNullPnL decimal.Decimal `json:"mean_null_pnl"`
	// PValue é a fração de nulos com PnL ≥ o observado. P-value ≤ 0.05 =
	// a vantagem dificilmente é sorte (significância de 95%).
	PValue float64 `json:"p_value"`
	// Significant é true se p-value ≤ 0.05.
	Significant bool `json:"significant"`
	// ZScore quantas desvios padrão o PnL observado está do nulo.
	ZScore float64 `json:"z_score"`
}

// Significance testa se a vantagem da estratégia é real (p-valor via
// bootstrap de permutação). Seedável e determinístico.
func Significance(trades []TradeResult, trials int, seed int64) SignificanceResult {
	res := SignificanceResult{Trials: trials}
	if len(trades) == 0 || trials <= 0 {
		return res
	}

	// Observed: soma dos PnLs reais.
	observed := decimal.Zero
	absPnls := make([]float64, len(trades))
	for i, t := range trades {
		observed = observed.Add(t.PnL)
		absPnls[i] = t.PnL.Abs().InexactFloat64()
	}
	res.ObservedPnL = observed
	obs := observed.InexactFloat64()

	rng := rand.New(rand.NewSource(seed))
	var nullSum float64
	var nullSq float64
	ge := 0 // nulos ≥ observado

	for t := 0; t < trials; t++ {
		// Permutação nula: cada |PnL| recebe sinal aleatório (±).
		var s float64
		for _, a := range absPnls {
			if rng.Intn(2) == 0 {
				s += a
			} else {
				s -= a
			}
		}
		nullSum += s
		nullSq += s * s
		if s >= obs {
			ge++
		}
	}

	res.PValue = float64(ge) / float64(trials)
	res.Significant = res.PValue <= 0.05
	res.MeanNullPnL = decimal.NewFromFloat(nullSum / float64(trials))

	if trials > 1 {
		m := nullSum / float64(trials)
		v := nullSq/float64(trials) - m*m
		if v > 0 {
			res.ZScore = (obs - m) / math.Sqrt(v)
		}
	}
	return res
}

func sortFloats(xs []float64) {
	// insertion sort simples — vetores de simulação são grandes mas ordenar
	// 10k floats é trivial; evita import extra.
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
