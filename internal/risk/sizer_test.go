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
