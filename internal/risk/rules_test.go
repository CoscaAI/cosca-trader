package risk

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// TestRulesDirect valida as regras STATELESS isoladas (fora do Manager).
func TestExposureRuleDirect(t *testing.T) {
	r := ExposureRule{MaxPct: d("0.20")}
	in := Input{Notional: d("3000"), Equity: d("10000")}
	if !errors.Is(r.Check(in), ErrExposureExceeded) {
		t.Fatalf("exposição 30%% deveria estourar 20%%")
	}
	in.Notional = d("1500")
	if err := r.Check(in); err != nil {
		t.Fatalf("exposição 15%% deveria passar: %v", err)
	}
	// risk-off: 15% > 10% (metade) → bloqueia
	in.Notional = d("1500")
	in.Regime = "risk-off"
	if !errors.Is(r.Check(in), ErrExposureExceeded) {
		t.Fatalf("risk-off deveria reduzir o limite pela metade (10%%)")
	}
	// desativado (zero) → passa tudo
	r2 := ExposureRule{MaxPct: decimal.Zero}
	if err := r2.Check(Input{Notional: d("999999"), Equity: d("10000")}); err != nil {
		t.Fatalf("regra desativada deveria passar: %v", err)
	}
}

func TestTotalExposureRuleDirect(t *testing.T) {
	r := TotalExposureRule{MaxPct: d("0.50")}
	positions := []domain.Position{{
		Symbol: "BTCUSDT", Exchange: "paper", Side: domain.SideBuy,
		Quantity: d("0.05"), AvgEntryPrice: d("50000"), // 2500
	}}
	in := Input{Notional: d("3000"), Positions: positions, Equity: d("10000")}
	// 2500 + 3000 = 5500 = 55% > 50%
	if !errors.Is(r.Check(in), ErrTotalExposureLimit) {
		t.Fatalf("55%% deveria estourar 50%%")
	}
}

func TestOpenOrdersRuleDirect(t *testing.T) {
	r := OpenOrdersRule{Max: 2}
	if err := r.Check(Input{OpenOrders: 1}); err != nil {
		t.Fatalf("1 de 2 deveria passar: %v", err)
	}
	if !errors.Is(r.Check(Input{OpenOrders: 2}), ErrTooManyOpenOrders) {
		t.Fatalf("2 de 2 deveria estourar")
	}
}

// TestCustomRules garante que Config.Rules substitui as regras padrão.
func TestCustomRules(t *testing.T) {
	m := New(Config{
		MaxExposurePct: decimal.NewFromFloat(0.90), // permissivo por padrão
		EquityProvider: func() decimal.Decimal { return decimal.NewFromInt(10000) },
		Rules:          []Rule{denyRule{}},
	})
	if err := m.Check(decimal.NewFromInt(100), nil, 0); err == nil {
		t.Fatal("regra custom deveria bloquear")
	}
	// Sem Rules → regras padrão aplicam (exposição 90%).
	m2 := New(Config{
		MaxExposurePct: decimal.NewFromFloat(0.90),
		EquityProvider: func() decimal.Decimal { return decimal.NewFromInt(10000) },
	})
	if err := m2.Check(decimal.NewFromInt(100), nil, 0); err != nil {
		t.Fatalf("sem Rules deveria usar as regras padrão: %v", err)
	}
}

// denyRule bloqueia qualquer ordem (regra custom para testar Config.Rules).
type denyRule struct{}

func (denyRule) Name() string      { return "deny_all" }
func (denyRule) Check(Input) error { return errors.New("bloqueado por teste") }

func TestRulesListed(t *testing.T) {
	m := New(Config{
		MaxExposurePct:      decimal.NewFromFloat(0.20),
		MaxTotalExposurePct: decimal.NewFromFloat(0.50),
		MaxOpenOrders:       5,
		EquityProvider:      func() decimal.Decimal { return decimal.NewFromInt(10000) },
	})
	if len(m.Rules()) != 3 {
		t.Fatalf("esperava 3 regras padrão, got %d", len(m.Rules()))
	}
}
