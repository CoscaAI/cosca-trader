package risk

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func TestSizeLong(t *testing.T) {
	// equity 10000, risco 1% = 100; entrada 100, stop 95 (dist 5) → qty 20.
	equity := decimal.NewFromInt(10000)
	risk := decimal.NewFromFloat(0.01)
	qty, err := Size(equity, risk, decimal.NewFromInt(100), decimal.NewFromInt(95))
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if !qty.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("qty esperada 20, got %v", qty)
	}
	// Risco real: (100-95)×20 = 100 = 1% do equity ✓
	loss := decimal.NewFromInt(5).Mul(qty)
	if !loss.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("perda no stop deveria ser 100, got %v", loss)
	}
}

func TestSizeShort(t *testing.T) {
	// equity 5000, risco 2% = 100; entrada 50, stop 55 (dist 5) → qty 20.
	equity := decimal.NewFromInt(5000)
	risk := decimal.NewFromFloat(0.02)
	qty, err := Size(equity, risk, decimal.NewFromInt(50), decimal.NewFromInt(55))
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if !qty.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("qty esperada 20, got %v", qty)
	}
}

func TestSizeTruncatesDown(t *testing.T) {
	// 10000 × 0.01 = 100; dist 3 → 33.333... → trunca para 33 (nunca arredonda
	// para cima, que aumentaria o risco).
	equity := decimal.NewFromInt(10000)
	qty, err := Size(equity, decimal.NewFromFloat(0.01), decimal.NewFromInt(100), decimal.NewFromInt(97))
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if !qty.Equal(decimal.NewFromFloat(33.33333333)) {
		t.Fatalf("qty esperada 33.33333333 (truncada), got %v", qty)
	}
}

func TestSizeInvalidParams(t *testing.T) {
	// equity zero → erro
	if _, err := Size(decimal.Zero, decimal.NewFromFloat(0.01), decimal.NewFromInt(100), decimal.NewFromInt(95)); !errors.Is(err, ErrSizeInvalidParams) {
		t.Fatalf("esperava ErrSizeInvalidParams, got %v", err)
	}
	// risco ≥ 1 → erro
	if _, err := Size(decimal.NewFromInt(1000), decimal.NewFromInt(1), decimal.NewFromInt(100), decimal.NewFromInt(95)); err == nil {
		t.Fatal("risco 100% deveria ser erro")
	}
	// entrada == stop → erro
	if _, err := Size(decimal.NewFromInt(1000), decimal.NewFromFloat(0.01), decimal.NewFromInt(100), decimal.NewFromInt(100)); !errors.Is(err, ErrSizeStopTooClose) {
		t.Fatalf("esperava ErrSizeStopTooClose, got %v", err)
	}
}

func TestSizeMinNotional(t *testing.T) {
	// BTCUSDT: min_notional 10 USDT, preço 60000 → qty mínima = 10/60000
	// = 0.00016666..., arredondada para cima ao step 0.00001 → 0.00017.
	rules := InstrumentRules{
		MinNotional: decimal.NewFromInt(10),
		MinQty:      decimal.NewFromFloat(0.00001),
		StepSize:    decimal.NewFromFloat(0.00001),
	}
	qty, err := SizeMinNotional(rules, decimal.NewFromInt(60000))
	if err != nil {
		t.Fatalf("SizeMinNotional: %v", err)
	}
	// 10/60000 = 0.0001666... → ceil ao step 0.00001 = 0.00017
	if !qty.Equal(decimal.NewFromFloat(0.00017)) {
		t.Fatalf("qty mínima esperada 0.00017, got %v", qty)
	}
	// Notional real = 0.00017 × 60000 = 10.2 ≥ min 10 ✓
	notional := qty.Mul(decimal.NewFromInt(60000))
	if notional.LessThan(decimal.NewFromInt(10)) {
		t.Fatalf("notional %v abaixo do mínimo", notional)
	}
}

func TestSizeMinNotionalRespectsMinQty(t *testing.T) {
	// Se o min_qty (0.001) > qty_por_notional (10/60000), o min_qty vence.
	rules := InstrumentRules{
		MinNotional: decimal.NewFromInt(10),
		MinQty:      decimal.NewFromFloat(0.001),
		StepSize:    decimal.NewFromFloat(0.001),
	}
	qty, err := SizeMinNotional(rules, decimal.NewFromInt(60000))
	if err != nil {
		t.Fatalf("SizeMinNotional: %v", err)
	}
	if !qty.Equal(decimal.NewFromFloat(0.001)) {
		t.Fatalf("qty mínima esperada 0.001 (min_qty vence), got %v", qty)
	}
}

func TestValidarPreTrade(t *testing.T) {
	rules := InstrumentRules{
		MinNotional: decimal.NewFromInt(10),
		MinQty:      decimal.NewFromFloat(0.00001),
		StepSize:    decimal.NewFromFloat(0.00001),
	}
	// Abaixo do min_notional → erro.
	if _, err := ValidarPreTrade(rules, decimal.NewFromInt(60000), decimal.NewFromFloat(0.00005)); !errors.Is(err, ErrSizeBelowMinimum) {
		t.Fatalf("esperava ErrSizeBelowMinimum, got %v", err)
	}
	// Válido e arredondado ao step: 0.0005 × 60000 = 30 ≥ 10 ✓
	qty, err := ValidarPreTrade(rules, decimal.NewFromInt(60000), decimal.NewFromFloat(0.00055))
	if err != nil {
		t.Fatalf("ValidarPreTrade: %v", err)
	}
	if !qty.Equal(decimal.NewFromFloat(0.00055)) {
		t.Fatalf("qty esperada 0.00055 (floor ao step), got %v", qty)
	}
}
