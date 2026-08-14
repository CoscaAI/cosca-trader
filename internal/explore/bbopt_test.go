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

// TestBBStopOptimization varre o StopMult da bb-reversion no histórico REAL
// (BTCUSDT 1h) e imprime PnL/win rate/PF por configuração — calibração por
// evidência, não por chute.
func TestBBStopOptimization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bc := binance.New()
	candles, err := bc.Klines(ctx, "BTCUSDT", "1h", 500, 500)
	if err != nil { t.Skipf("offline: %v", err) }

	for _, mult := range []float64{0.4, 0.6, 0.8, 1.0, 1.2, 1.5} {
		s := strategy.NewBBReversionWith(strategy.BBConfig{StopMult: mult, ExitAtSMA: true})
		report := strategy.AnalyzeBacktest(s, candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), 300, 300, 42)
		st := report.Stats
		consist := "n"
		if report.WalkForward.Consistent { consist = "s" }
		fmt.Printf("stop=%.2f → pnl=%s win=%.0f%% PF=%.2f trades=%d sharpe=%.2f p=%.3f wf=%s\n",
			mult, st.NetPnL, st.WinRate*100, st.ProfitFactor, st.TotalTrades, st.Sharpe, report.Significance.PValue, consist)
	}
}
