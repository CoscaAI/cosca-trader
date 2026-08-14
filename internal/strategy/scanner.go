// scanner.go — o SCANNER de estratégias (Fase 5): avalia TODAS as estratégias
// registradas contra o mesmo histórico (dados reais via Klines) e devolve um
// ranking por score científico. É como o Don "encontra as melhores": a
// ciência rankeia, não a opinião.
package strategy

import (
	"sort"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// ScoredStrategy é uma estratégia avaliada com seu laudo e score.
type ScoredStrategy struct {
	Name        string             `json:"name"`
	Report      ScientificReport   `json:"report"`
	Score       float64            `json:"score"`
	PassesGate  bool               `json:"passes_gate"` // candidata a operar
}

// ScanResult é o resultado do scanner: ranking completo + as aprovadas.
type ScanResult struct {
	Symbol    string             `json:"symbol"`
	Periods   int                `json:"periods"`
	Strategies []ScoredStrategy  `json:"strategies"`
	// Best é a melhor estratégia que PASSOU no portão científico (ou nil).
	Best *ScoredStrategy `json:"best,omitempty"`
}

// GateConfig são os critérios mínimos para uma estratégia ser aprovada.
type GateConfig struct {
	// MaxPValue — vantagem significativa (p ≤ 0.05 por padrão).
	MaxPValue float64
	// MinProfitFactor — lucro bruto ≥ X × perda bruta.
	MinProfitFactor float64
	// MaxProbOfLoss — risco de perder dinheiro no Monte Carlo.
	MaxProbOfLoss float64
	// RequireConsistent — walk-forward deve lucrar out-of-sample.
	RequireConsistent bool
	// MinTrades — amostra mínima para estatística significar algo.
	MinTrades int
}

// DefaultGate é o portão científico da casa.
func DefaultGate() GateConfig {
	return GateConfig{
		MaxPValue:         0.05,
		MinProfitFactor:   1.2,
		MaxProbOfLoss:     0.50,
		RequireConsistent: true,
		MinTrades:         10,
	}
}

// Scan avalia todas as estratégias registradas contra os candles (mesmo
// histórico, mesmo capital, mesmas fees) e devolve o ranking ordenado por
// score. Reproduzível via seed (Monte Carlo + significância).
func Scan(candles []domain.Candle, initial, feePct decimal.Decimal, gate GateConfig, mcSims, sigTrials int, seed int64) ScanResult {
	res := ScanResult{Symbol: symbolOf(candles), Periods: len(candles)}
	names := Names()
	res.Strategies = make([]ScoredStrategy, 0, len(names))

	for _, name := range names {
		f, ok := Get(name)
		if !ok {
			continue
		}
		s := f()
		report := AnalyzeBacktest(s, candles, initial, feePct, mcSims, sigTrials, seed)
		score := scoreStrategy(report, gate)
		passes := passesGate(report, gate)

		res.Strategies = append(res.Strategies, ScoredStrategy{
			Name:       name,
			Report:     report,
			Score:      score,
			PassesGate: passes,
		})
	}

	// Ranking por score (decrescente).
	sort.Slice(res.Strategies, func(i, j int) bool {
		return res.Strategies[i].Score > res.Strategies[j].Score
	})

	// Melhor aprovada.
	for i := range res.Strategies {
		if res.Strategies[i].PassesGate {
			best := res.Strategies[i]
			res.Best = &best
			break
		}
	}
	return res
}

// scoreStrategy pontua uma estratégia de 0 a 100 com base no laudo. O score
// NÃO é o gate (uma estratégia pode ter score alto e ainda não passar por
// ter amostra pequena, por exemplo); é o ordenador do ranking.
func scoreStrategy(r ScientificReport, g GateConfig) float64 {
	st := r.Stats
	if st.TotalTrades == 0 {
		return 0
	}

	score := 0.0
	// 1. Vantagem: significância (p menor = melhor). 0..40
	pScore := (1 - r.Significance.PValue) * 40
	if r.Significance.Significant {
		pScore = 40
	}
	score += pScore

	// 2. Consistência out-of-sample: 0..20
	if r.WalkForward.Consistent {
		score += 20
	} else {
		score += 5 // ainda pontua um pouco por ter testado
	}

	// 3. Qualidade do retorno ajustado: Sharpe. -∞..1 → 0..20
	sharpeScore := (st.Sharpe + 2) / 4 * 20 // sharpe -2 → 0, +2 → 20
	if sharpeScore < 0 {
		sharpeScore = 0
	}
	if sharpeScore > 20 {
		sharpeScore = 20
	}
	score += sharpeScore

	// 4. Risco de perda (Monte Carlo): 0..20. P(perder)=0 → 20; 1 → 0.
	score += (1 - r.MonteCarlo.ProbOfLoss) * 20

	return score
}

// passesGate aplica o portão científico da casa.
func passesGate(r ScientificReport, g GateConfig) bool {
	st := r.Stats
	if st.TotalTrades < g.MinTrades {
		return false // amostra insuficiente
	}
	if !r.Significance.Significant || r.Significance.PValue > g.MaxPValue {
		return false
	}
	if g.MinProfitFactor > 0 && st.ProfitFactor < g.MinProfitFactor {
		return false
	}
	if g.MaxProbOfLoss > 0 && r.MonteCarlo.ProbOfLoss > g.MaxProbOfLoss {
		return false
	}
	if g.RequireConsistent && !r.WalkForward.Consistent {
		return false
	}
	return true
}
