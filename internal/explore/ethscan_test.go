package explore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/exchange/binance"
	"github.com/CoscaAI/cosca-trader/internal/strategy"
)

// TestEthOptimization varre parâmetros da bb-reversion em ETHUSDT 1h (a
// combinação candidata) para maximizar score E passar no portão.
func TestEthOptimization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bc := binance.New()
	candles, err := bc.Klines(ctx, "ETHUSDT", "1h", 500, 500)
	if err != nil { t.Skipf("offline: %v", err) }

	// Varre o multiplicador do stop da bb-reversion (calibração fina).
	for _, mult := range []float64{0.4, 0.6, 0.8, 1.0, 1.2} {
		s := strategy.NewBBReversionWith(strategy.BBConfig{StopMult: mult, ExitAtSMA: true})
		r := strategy.AnalyzeBacktest(s, candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), 1000, 1000, 42)
		st := r.Stats
		g := "❌"
		if r.Significance.Significant && st.ProfitFactor >= 1.2 && r.WalkForward.Consistent {
			g = "🏆 PASSOU"
		}
		fmt.Printf("stop=%.1f → win=%.0f%% PF=%.2f trades=%d p=%.3f P(perder)=%.0f%% wf=%v edge=%+.1f%% %s\n",
			mult, st.WinRate*100, st.ProfitFactor, st.TotalTrades, r.Significance.PValue,
			r.MonteCarlo.ProbOfLoss*100, r.WalkForward.Consistent, r.Benchmark.EdgePct*100, g)
	}
}
