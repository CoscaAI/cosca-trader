package risk

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func TestMacroRiskOffHalvesExposure(t *testing.T) {
	// Em risk-off, a exposição permitida (20%) cai para 10%.
	m := New(Config{
		MaxExposurePct: decimal.NewFromFloat(0.20),
		EquityProvider: func() decimal.Decimal { return decimal.NewFromInt(10000) },
		MacroRegime:    func() string { return "risk-off" },
	})
	// Notional 1500 = 15% > 10% (limite reduzido) → bloqueia.
	err := m.Check(decimal.NewFromInt(1500), nil, 0)
	if !errors.Is(err, ErrExposureExceeded) {
		t.Fatalf("risk-off deveria bloquear 15%% (limite 10%%), got %v", err)
	}
	// Notional 800 = 8% < 10% → passa.
	if err := m.Check(decimal.NewFromInt(800), nil, 0); err != nil {
		t.Fatalf("8%% em risk-off deveria passar, got %v", err)
	}
}

func TestMacroRiskOnKeepsExposure(t *testing.T) {
	// Em risk-on, 15% está dentro do limite normal de 20%.
	m := New(Config{
		MaxExposurePct: decimal.NewFromFloat(0.20),
		EquityProvider: func() decimal.Decimal { return decimal.NewFromInt(10000) },
		MacroRegime:    func() string { return "risk-on" },
	})
	if err := m.Check(decimal.NewFromInt(1500), nil, 0); err != nil {
		t.Fatalf("15%% em risk-on deveria passar (limite 20%%), got %v", err)
	}
}

func TestMacroUnknownUsesDefault(t *testing.T) {
	// Regime desconhecido (falha do radar) → usa o limite normal (fail-safe
	// conservador: não reduz, mas não aumenta).
	m := New(Config{
		MaxExposurePct: decimal.NewFromFloat(0.20),
		EquityProvider: func() decimal.Decimal { return decimal.NewFromInt(10000) },
		MacroRegime:    func() string { return "desconhecido" },
	})
	if err := m.Check(decimal.NewFromInt(1500), nil, 0); err != nil {
		t.Fatalf("regime desconhecido usa limite normal (20%%), got %v", err)
	}
}
