package explore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/exchange/binance"
	"github.com/CoscaAI/cosca-trader/internal/risk"
)

// TestMinNotionalReal lê as regras REAIS do BTCUSDT na Binance e mostra o
// tamanho da operação mínima — o que o Don vai usar para entrar.
func TestMinNotionalReal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bc := binance.New()
	rules, ok := bc.InstrumentRules(ctx, "BTCUSDT")
	if !ok {
		t.Skipf("sem acesso à Binance: %v", ok)
	}
	fmt.Printf("BTCUSDT — min_notional=%s min_qty=%s step=%s tick=%s\n",
		rules.MinNotional, rules.MinQty, rules.StepSize, rules.TickSize)
	// Preço atual aproximado
	candles, err := bc.Klines(ctx, "BTCUSDT", "1m", 1, 1)
	if err == nil && len(candles) > 0 {
		price := decimal.NewFromFloat(candles[0].Close)
		r := risk.InstrumentRules{
			MinNotional: rules.MinNotional,
			MinQty:      rules.MinQty,
			StepSize:    rules.StepSize,
			TickSize:    rules.TickSize,
		}
		qty, err := risk.SizeMinNotional(r, price)
		if err != nil {
			t.Fatalf("SizeMinNotional: %v", err)
		}
		fmt.Printf("preço atual ≈ %s → OPERAÇÃO MÍNIMA: qty=%s (notional %s)\n",
			price, qty, qty.Mul(price))
	}
}
