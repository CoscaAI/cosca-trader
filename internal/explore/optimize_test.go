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

// TestOptimizeContrarian varre os parâmetros da contrarian no histórico REAL
// para maximizar a acertividade (win rate) — o que o Don pediu.
func TestOptimizeContrarian(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bc := binance.New()
	candles, err := bc.Klines(ctx, "BTCUSDT", "1h", 500, 500)
	if err != nil { t.Skipf("offline: %v", err) }

	ranges := []strategy.ParamRange{
		{Name: "rsi_overb", Min: 60, Max: 80, Step: 10},
		{Name: "rsi_overs", Min: 20, Max: 40, Step: 10},
		{Name: "stop_mult", Min: 0.5, Max: 1.5, Step: 0.5},
	}

	res := strategy.Optimize(
		func() strategy.Strategy { return strategy.NewContrarian() },
		func(s strategy.Strategy, name string, v float64) {
			if c, ok := s.(*strategy.Contrarian); ok {
				c.SetParam(name, v)
			}
		},
		candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001),
		ranges, 200, 200, 42, 10,
	)

	fmt.Printf("═══ OTIMIZAÇÃO CONTRARIAN (dados reais) ═══\n")
	fmt.Printf("MELHOR: params=%v | win rate %.0f%% | pnl=%s | PF=%.2f | trades=%d\n",
		res.Params, res.WinRate*100, res.PnL, res.ProfitFactor, res.Trades)
	fmt.Println("TOP 5:")
	for i, r := range res.Ranked[:minInt(5, len(res.Ranked))] {
		fmt.Printf("  #%d params=%v win=%.0f%% pnl=%s PF=%.2f trades=%d score=%.3f\n",
			i+1, r.Params, r.WinRate*100, r.PnL, r.ProfitFactor, r.Trades, r.Score)
	}
}

func minInt(a, b int) int {
	if a < b { return a }
	return b
}
