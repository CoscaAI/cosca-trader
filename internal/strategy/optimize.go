// optimize.go — o OTIMIZADOR de estratégias (Fase 5): varre combinações de
// parâmetros de uma estratégia contra o histórico real e devolve a melhor por
// ACERTIVIDADE (win rate) E por retorno ajustado. É a resposta ao Don
// "aumente ao máximo a acertividade": a calibração vira varredura sistemática,
// não tentativa-e-erro manual. Executa em PARALELO (worker pool) — a lição da
// testing farm do Superalgos aplicada ao Go: casos independentes rodam
// simultaneamente e o resultado é idêntico ao serial (determinístico).
package strategy

import (
	"runtime"
	"sort"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// ParamRange é um intervalo de valores a varrer para um parâmetro.
type ParamRange struct {
	Name  string
	Min   float64
	Max   float64
	Step  float64
}

// OptimizeResult é o resultado da otimização.
type OptimizeResult struct {
	Strategy string             `json:"strategy"`
	Params   map[string]float64 `json:"params"`
	Score    float64            `json:"score"`
	WinRate  float64            `json:"win_rate"`
	PnL      decimal.Decimal    `json:"pnl"`
	ProfitFactor float64        `json:"profit_factor"`
	Trades   int                `json:"trades"`
	// Ranked são as combinações testadas, ordenadas por score (top N).
	Ranked []OptimizeResult `json:"ranked"`
}

// ParamSetter aplica um parâmetro a uma fábrica de estratégia. O otimizador
// usa este hook para variar os parâmetros da estratégia genérica.
type ParamSetter func(s Strategy, name string, value float64)

// Optimize varre os ranges de parâmetros sobre uma estratégia (criada por
// factory e ajustada por setter) e devolve as combinações ordenadas por score.
// score = winRate*0.5 + sharpeTransform*0.3 + profitability*0.2 — prioriza
// ACERTIVIDADE (o que o Don pediu) sem ignorar o retorno.
func Optimize(factory Factory, setter ParamSetter, candles []domain.Candle, initial, feePct decimal.Decimal, ranges []ParamRange, mcSims, sigTrials int, seed int64, top int) OptimizeResult {
	res := OptimizeResult{Strategy: "optimize", Params: map[string]float64{}, Ranked: []OptimizeResult{}}

	// Gera todas as combinações via cartesian product dos ranges.
	combos := [][]float64{{}}
	for _, r := range ranges {
		var values []float64
		for v := r.Min; v <= r.Max+1e-9; v += r.Step {
			values = append(values, v)
		}
		var next [][]float64
		for _, combo := range combos {
			for _, v := range values {
				c := append([]float64(nil), combo...)
				c = append(c, v)
				next = append(next, c)
			}
		}
		combos = next
	}

	// Worker pool: cada combinação é um caso independente (testing farm).
	// Resultados são indexados por posição → determinístico (mesma ordem do
	// serial), independente da ordem de conclusão das goroutines.
	results := make([]OptimizeResult, len(combos))
	workers := runtime.GOMAXPROCS(0)
	if workers > len(combos) {
		workers = len(combos)
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				combo := combos[i]
				s := factory()
				params := map[string]float64{}
				for j, r := range ranges {
					val := combo[j]
					params[r.Name] = val
					if setter != nil {
						setter(s, r.Name, val)
					}
				}
				report := AnalyzeBacktest(s, candles, initial, feePct, mcSims, sigTrials, seed)
				st := report.Stats
				score := st.WinRate*0.5 + transformSharpe(st.Sharpe)*0.3 + transformPF(st.ProfitFactor)*0.2
				results[i] = OptimizeResult{
					Strategy: "optimize", Params: params, Score: score,
					WinRate: st.WinRate, PnL: st.NetPnL, ProfitFactor: st.ProfitFactor, Trades: st.TotalTrades,
				}
			}
		}()
	}
	for i := range combos {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	res.Ranked = results

	// Ordena por score decrescente e guarda o melhor.
	sort.Slice(res.Ranked, func(i, j int) bool {
		return res.Ranked[i].Score > res.Ranked[j].Score
	})
	if len(res.Ranked) > 0 {
		best := res.Ranked[0]
		res.Params = best.Params
		res.Score = best.Score
		res.WinRate = best.WinRate
		res.PnL = best.PnL
		res.ProfitFactor = best.ProfitFactor
		res.Trades = best.Trades
	}
	if top > 0 && len(res.Ranked) > top {
		res.Ranked = res.Ranked[:top]
	}
	return res
}

// transformSharpe mapeia Sharpe para [0,1]: -2 → 0, +2 → 1.
func transformSharpe(s float64) float64 {
	t := (s + 2) / 4
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}

// transformPF mapeia profit factor para [0,1]: 0 → 0, 2 → 1.
func transformPF(pf float64) float64 {
	t := pf / 2
	if t < 0 {
		return 0
	}
	if t > 1 {
		return 1
	}
	return t
}
