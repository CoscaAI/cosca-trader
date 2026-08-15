package quantize

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestTruncateToStep(t *testing.T) {
	// 0.000015 com step 0.00001 → 0.00001 (para baixo, nunca excede o lote)
	if got := TruncateToStep(d("0.000015"), d("0.00001")); !got.Equal(d("0.00001")) {
		t.Fatalf("truncate 0.000015 → %v, esperava 0.00001", got)
	}
	// exato permanece
	if got := TruncateToStep(d("0.00002"), d("0.00001")); !got.Equal(d("0.00002")) {
		t.Fatalf("truncate exato → %v, esperava 0.00002", got)
	}
	// step zero = sem restrição → inalterado
	if got := TruncateToStep(d("1.234"), decimal.Zero); !got.Equal(d("1.234")) {
		t.Fatalf("step zero → %v, esperava 1.234", got)
	}
}

func TestRoundToTick(t *testing.T) {
	// 50000.005 com tick 0.01 → 50000.01 (meio-afasta-de-zero)
	if got := RoundToTick(d("50000.005"), d("0.01")); !got.Equal(d("50000.01")) {
		t.Fatalf("round 50000.005 → %v, esperava 50000.01", got)
	}
	// 50000.004 → 50000.00
	if got := RoundToTick(d("50000.004"), d("0.01")); !got.Equal(d("50000.00")) {
		t.Fatalf("round 50000.004 → %v, esperava 50000.00", got)
	}
	// tick zero = sem restrição
	if got := RoundToTick(d("123.456"), decimal.Zero); !got.Equal(d("123.456")) {
		t.Fatalf("tick zero → %v, esperava 123.456", got)
	}
}

func TestToStepModes(t *testing.T) {
	// Truncate em direção a zero: -0.000015 → -0.00001 (não -0.00002)
	if got := ToStep(d("-0.000015"), d("0.00001"), Truncate); !got.Equal(d("-0.00001")) {
		t.Fatalf("truncate negativo → %v, esperava -0.00001", got)
	}
	// Round meio-afasta: -50000.005 → -50000.01
	if got := ToStep(d("-50000.005"), d("0.01"), Round); !got.Equal(d("-50000.01")) {
		t.Fatalf("round negativo → %v, esperava -50000.01", got)
	}
}
