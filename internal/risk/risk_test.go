package risk

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// helper: equity fixa e provider.
func equityProvider(e decimal.Decimal) func() decimal.Decimal {
	return func() decimal.Decimal { return e }
}

func TestCheckExposurePerSymbol(t *testing.T) {
	m := New(Config{
		MaxExposurePct: decimal.NewFromFloat(0.20),
		EquityProvider: equityProvider(decimal.NewFromInt(10000)),
	})
	// notional 3000 em 10000 = 30% > 20% → bloqueia
	err := m.Check(decimal.NewFromInt(3000), nil, 0)
	if !errors.Is(err, ErrExposureExceeded) {
		t.Fatalf("esperava ErrExposureExceeded, got %v", err)
	}
	// notional 1500 = 15% → ok
	if err := m.Check(decimal.NewFromInt(1500), nil, 0); err != nil {
		t.Fatalf("esperava nil, got %v", err)
	}
}

func TestCheckTotalExposure(t *testing.T) {
	m := New(Config{
		MaxExposurePct:      decimal.NewFromFloat(0.50),
		MaxTotalExposurePct: decimal.NewFromFloat(0.50),
		EquityProvider:      equityProvider(decimal.NewFromInt(10000)),
	})
	positions := []domain.Position{{
		Symbol:        "BTCUSDT",
		Exchange:      "paper",
		Side:          domain.SideBuy,
		Quantity:      decimal.NewFromFloat(0.05),
		AvgEntryPrice: decimal.NewFromInt(50000), // 2500 exposição
	}}
	// 2500 existente + 3000 novo = 5500 = 55% > 50%
	err := m.Check(decimal.NewFromInt(3000), positions, 0)
	if !errors.Is(err, ErrTotalExposureLimit) {
		t.Fatalf("esperava ErrTotalExposureLimit, got %v", err)
	}
}

func TestCheckOpenOrders(t *testing.T) {
	m := New(Config{
		MaxOpenOrders: 2,
		EquityProvider: equityProvider(decimal.NewFromInt(10000)),
	})
	if err := m.Check(decimal.NewFromInt(100), nil, 1); err != nil {
		t.Fatalf("1 ordem aberta de limite 2: esperava nil, got %v", err)
	}
	if err := m.Check(decimal.NewFromInt(100), nil, 2); !errors.Is(err, ErrTooManyOpenOrders) {
		t.Fatalf("2 ordens de limite 2: esperava ErrTooManyOpenOrders, got %v", err)
	}
}

func TestCheckRateLimit(t *testing.T) {
	m := New(Config{
		MaxOrdersPerMinute: 2,
		EquityProvider:     equityProvider(decimal.NewFromInt(10000)),
	})
	if err := m.Check(decimal.NewFromInt(100), nil, 0); err != nil {
		t.Fatal(err)
	}
	m.RegisterOrder()
	if err := m.Check(decimal.NewFromInt(100), nil, 0); err != nil {
		t.Fatal(err)
	}
	m.RegisterOrder()
	// terceira ordem no mesmo minuto → rate limited
	err := m.Check(decimal.NewFromInt(100), nil, 0)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("esperava ErrRateLimited, got %v", err)
	}
}

func TestDrawdownBreachHalts(t *testing.T) {
	var emitted []string
	m := New(Config{
		MaxDrawdownPct: decimal.NewFromFloat(0.10),
		EquityProvider: equityProvider(decimal.NewFromInt(10000)),
		Emit:           func(t, sev string, p any) { emitted = append(emitted, t) },
	})
	// pico 10000, equity cai para 8500 → drawdown 15% ≥ 10% → halt + breach
	st := m.OnEquityUpdate(decimal.NewFromInt(8500))
	if !st.TradingHalted {
		t.Fatal("esperava trading halted após drawdown breach")
	}
	found := false
	for _, e := range emitted {
		if e == "risk.breach" {
			found = true
		}
	}
	if !found {
		t.Fatalf("esperava risk.breach emitido, got %v", emitted)
	}
	// depois de halt, Check bloqueia
	err := m.Check(decimal.NewFromInt(100), nil, 0)
	if !errors.Is(err, ErrDrawdownBreach) {
		t.Fatalf("após halt esperava ErrDrawdownBreach, got %v", err)
	}
	// Resume destrava
	m.Resume()
	if err := m.Check(decimal.NewFromInt(100), nil, 0); err != nil {
		t.Fatalf("após Resume esperava nil, got %v", err)
	}
}

func TestEquityUnavailableFailClosed(t *testing.T) {
	// sem provider → fail-closed bloqueia
	m := New(Config{})
	err := m.Check(decimal.NewFromInt(100), nil, 0)
	if !errors.Is(err, ErrEquityUnavailable) {
		t.Fatalf("sem provider esperava ErrEquityUnavailable, got %v", err)
	}
}

func TestOnEquityUpdateTracksPeak(t *testing.T) {
	m := New(Config{
		MaxDrawdownPct: decimal.NewFromFloat(0.10),
		EquityProvider: equityProvider(decimal.NewFromInt(10000)),
	})
	m.OnEquityUpdate(decimal.NewFromInt(12000)) // novo pico
	st := m.OnEquityUpdate(decimal.NewFromInt(11000))
	if !st.PeakEquity.Equal(decimal.NewFromInt(12000)) {
		t.Fatalf("pico esperado 12000, got %v", st.PeakEquity)
	}
	// drawdown = (12000-11000)/12000 = 8.33%
	want := decimal.NewFromInt(1000).Div(decimal.NewFromInt(12000))
	if !st.DrawdownPct.Equal(want) {
		t.Fatalf("drawdown esperado %v, got %v", want, st.DrawdownPct)
	}
}
