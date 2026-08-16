// Package diagnosis calcula o diagnóstico completo de um ativo: favorável ou
// não, ganho esperado, confiança e estatística completa por estratégia. É a
// "lente científica" que o bot/kernel usa para responder "vale a pena operar
// este ativo agora?".
//
// O diagnóstico roda sobre candles reais (Binance) e usa o backtest real de
// CADA estratégia — win-rate, profit factor, Sharpe, expectância, retorno e o
// benchmark buy-and-hold (para saber se há edge ou só surfe de mercado, a
// lição do Freqtrade/OctoBot).
package diagnosis

import (
	"fmt"
	"sort"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/strategy"
	"github.com/shopspring/decimal"
)

// StrategyStat é a estatística de uma estratégia para o ativo.
type StrategyStat struct {
	Strategy     string  `json:"strategy"`
	WinRate      float64 `json:"win_rate"`      // 0..1
	ProfitFactor float64 `json:"profit_factor"` // grossProfit/|grossLoss|
	Sharpe       float64 `json:"sharpe"`
	Expectancy   float64 `json:"expectancy"`    // PnL médio por trade (decimal → float)
	ReturnPct    float64 `json:"return_pct"`    // netPnL/initial, 0..1
	MaxDD        float64 `json:"max_drawdown_pct"`
	Trades       int     `json:"trades"`
	Edge         float64 `json:"edge"` // bot return − buy-and-hold return (pp)
	Favorable    bool    `json:"favorable"`
}

// AssetDiagnosis é o diagnóstico completo de um ativo.
type AssetDiagnosis struct {
	Symbol        string         `json:"symbol"`
	Favorable     bool           `json:"favorable"`      // vale operar agora?
	Direction     string         `json:"direction"`      // "long" | "short" | "neutral"
	Confidence    float64        `json:"confidence"`     // 0..1
	BestStrategy  string         `json:"best_strategy"`  // estratégia com melhor edge
	ExpectedGainPct float64      `json:"expected_gain_pct"` // retorno esperado do backtest
	Regime        string         `json:"regime"`         // risk-on | risk-off | desconhecido
	Strategies    []StrategyStat `json:"strategies"`     // estatística completa, ordenada por edge
	Summary       string         `json:"summary"`        // explicação humana
}

// Analyze roda o diagnóstico completo: backtest de todas as estratégias sobre
// as candles do ativo e devolve o laudo. `regime` é o macro (risk-on/off) para
// o parecer favorável/desfavorável.
func Analyze(symbol, regime string, candles []domain.Candle) AssetDiagnosis {
	d := AssetDiagnosis{Symbol: symbol, Regime: regime}

	if len(candles) < 30 {
		d.Summary = "dados insuficientes (menos de 30 velas) para um diagnóstico confiável"
		return d
	}

	initial := decimal.NewFromFloat(1000)
	feePct := decimal.NewFromFloat(0.001) // 0.1% por trade

	for _, name := range strategy.Names() {
		s, err := strategy.NewByName(name)
		if err != nil {
			continue
		}
		// AnalyzeBacktest dá Stats + Benchmark (edge vs buy-and-hold). Monte
		// Carlo e walk-forward ficam fora do caminho quente (só o laudo base).
		rep := strategy.AnalyzeBacktest(s, candles, initial, feePct, 0, 0, 0)
		st := rep.Stats
		if st.TotalTrades == 0 {
			continue
		}
		edge := rep.Benchmark.EdgePct // bot − buy-and-hold (fração)
		d.Strategies = append(d.Strategies, StrategyStat{
			Strategy:     name,
			WinRate:      st.WinRate,
			ProfitFactor: st.ProfitFactor,
			Sharpe:       st.Sharpe,
			Expectancy:   st.Expectancy.InexactFloat64(),
			ReturnPct:    st.ReturnPct,
			MaxDD:        st.MaxDrawdownPct,
			Trades:       st.TotalTrades,
			Edge:         edge,
			Favorable:    st.ProfitFactor > 1.0 && edge > 0 && st.Expectancy.IsPositive(),
		})
	}

	if len(d.Strategies) == 0 {
		d.Summary = "nenhuma estratégia gerou trades neste período — sem edge detectável"
		return d
	}

	// ordena por edge desc (a melhor estratégia primeiro)
	sort.Slice(d.Strategies, func(i, j int) bool {
		return d.Strategies[i].Edge > d.Strategies[j].Edge
	})

	best := d.Strategies[0]
	d.BestStrategy = best.Strategy
	d.ExpectedGainPct = best.ReturnPct * 100 // em %
	d.Confidence = confidenceOf(best)

	// direção: o edge positivo vem do lado dominante do backtest (não tem lado
	// no Stats, então inferimos da estratégia — a maioria é bidirecional; aqui
	// usamos o sinal do retorno e o regime como proxy honesto).
	d.Direction = directionOf(best, regime)

	// parecer: favorável se a melhor estratégia tem edge E o regime não bloqueia
	d.Favorable = best.Favorable && !regimeBlocks(regime, best.Strategy)

	d.Summary = summarize(d, best)
	return d
}

// confidenceOf mapeia a qualidade do edge para confiança [0,1].
func confidenceOf(s StrategyStat) float64 {
	c := 0.0
	if s.WinRate > 0.5 {
		c += 0.3
	}
	if s.ProfitFactor > 1.5 {
		c += 0.3
	}
	if s.Sharpe > 1.0 {
		c += 0.2
	}
	if s.Edge > 0 {
		c += 0.2
	}
	if c > 1.0 {
		c = 1.0
	}
	return c
}

// directionOf infere a direção: edge forte + regime risk-on → long;
// regime risk-off → short; sem convicção → neutral.
func directionOf(s StrategyStat, regime string) string {
	if s.Edge <= 0 || s.ProfitFactor <= 1.0 {
		return "neutral"
	}
	switch regime {
	case "risk-on":
		return "long"
	case "risk-off":
		return "short"
	default:
		return "long" // sem regime conhecido, assume a tendência do edge
	}
}

// regimeBlocks aplica o gate macro (L284): estratégia de tendência não opera
// em regime adverso.
func regimeBlocks(regime, stratName string) bool {
	switch regime {
	case "risk-off":
		return stratName == "ema-cross" || stratName == "breakout" || stratName == "momentum"
	case "risk-on":
		return stratName == "bb-reversion" || stratName == "contrarian"
	default:
		return false
	}
}

// summarize monta o resumo humano do laudo.
func summarize(d AssetDiagnosis, best StrategyStat) string {
	verdict := "DESFAVORÁVEL"
	if d.Favorable {
		verdict = "FAVORÁVEL"
	}
	dir := "neutro"
	if d.Direction != "neutral" {
		dir = d.Direction
	}
	base := fmt.Sprintf(
		"%s: %s (%s) — melhor estratégia %s com edge %.2fpp, win-rate %.0f%%, profit factor %.2f, Sharpe %.2f, ganho esperado %.2f%% em %d trades. Regime: %s.",
		d.Symbol, verdict, dir, best.Strategy, best.Edge*100, best.WinRate*100,
		best.ProfitFactor, best.Sharpe, d.ExpectedGainPct, best.Trades, d.Regime)
	// quando a melhor estratégia tem edge mas o regime bloqueia (gate macro)
	if best.Favorable && !d.Favorable {
		base += " (o regime atual bloqueia esta estratégia — aguarde o regime virar)"
	}
	return base
}
