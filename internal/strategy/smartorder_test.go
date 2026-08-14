package strategy

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestSmartOrderType(t *testing.T) {
	cur := decimal.NewFromInt(100)
	// Buy com alvo == corrente → market.
	if got := SmartOrderType("buy", decimal.NewFromInt(100), cur); got != "market" {
		t.Fatalf("buy 100==100 esperava market, got %s", got)
	}
	// Buy com alvo < corrente (90) → limit (comprar mais barato).
	if got := SmartOrderType("buy", decimal.NewFromInt(90), cur); got != "limit" {
		t.Fatalf("buy 90<100 esperava limit, got %s", got)
	}
	// Buy com alvo > corrente (110) → stop (romper acima).
	if got := SmartOrderType("buy", decimal.NewFromInt(110), cur); got != "stop" {
		t.Fatalf("buy 110>100 esperava stop, got %s", got)
	}
	// Sell com alvo > corrente → limit (vender mais caro).
	if got := SmartOrderType("sell", decimal.NewFromInt(110), cur); got != "limit" {
		t.Fatalf("sell 110>100 esperava limit, got %s", got)
	}
	// Sell com alvo < corrente → stop (romper abaixo).
	if got := SmartOrderType("sell", decimal.NewFromInt(90), cur); got != "stop" {
		t.Fatalf("sell 90<100 esperava stop, got %s", got)
	}
	// Alvo zero → market (fallback).
	if got := SmartOrderType("buy", decimal.Zero, cur); got != "market" {
		t.Fatalf("alvo zero esperava market, got %s", got)
	}
}
