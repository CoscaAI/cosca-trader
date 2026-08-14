package risk

import (
	"errors"
	"testing"
)

func TestStoplossGuardTriggersAndPauses(t *testing.T) {
	p := NewProtections(&StoplossGuard{MaxStops: 3, Lookback: 10, PauseCandles: 5}, nil)

	// 3 stops em sequência → guard dispara.
	for i := 0; i < 3; i++ {
		p.OnBar()
		p.OnStopLoss()
	}
	if err := p.Check("BTCUSDT"); !errors.Is(err, ErrDrawdownBreach) {
		t.Fatalf("esperava ErrDrawdownBreach (guard ativo), got %v", err)
	}
	st := p.State()
	if !st.StoplossTriggered || st.PausesUsed != 1 {
		t.Fatalf("guard deveria estar ativo: %+v", st)
	}
}

func TestStoplossGuardExpires(t *testing.T) {
	p := NewProtections(&StoplossGuard{MaxStops: 2, Lookback: 10, PauseCandles: 3}, nil)
	for i := 0; i < 2; i++ {
		p.OnBar()
		p.OnStopLoss()
	}
	if err := p.Check("BTCUSDT"); err == nil {
		t.Fatal("guard deveria bloquear após 2 stops")
	}
	// Passa a pausa (3 velas + margem).
	for i := 0; i < 5; i++ {
		p.OnBar()
	}
	if err := p.Check("BTCUSDT"); err != nil {
		t.Fatalf("guard deveria ter expirado, got %v", err)
	}
}

func TestCooldownPerSymbol(t *testing.T) {
	p := NewProtections(nil, &CooldownPeriod{CooldownCandles: 5})
	p.OnTradeClosed("BTCUSDT")
	if err := p.Check("BTCUSDT"); err == nil {
		t.Fatal("cooldown deveria bloquear BTCUSDT")
	}
	// Outro símbolo NÃO está em cooldown.
	if err := p.Check("ETHUSDT"); err != nil {
		t.Fatalf("ETHUSDT não deveria estar em cooldown, got %v", err)
	}
	// Passa o cooldown.
	for i := 0; i < 6; i++ {
		p.OnBar()
	}
	if err := p.Check("BTCUSDT"); err != nil {
		t.Fatalf("cooldown deveria ter expirado, got %v", err)
	}
}

func TestStoplossGuardMaxPauses(t *testing.T) {
	// MaxPauses=1: o guard dispara uma vez, expira, e NÃO re-dispara.
	p := NewProtections(&StoplossGuard{MaxStops: 2, Lookback: 10, PauseCandles: 3, MaxPauses: 1}, nil)
	for i := 0; i < 2; i++ {
		p.OnBar()
		p.OnStopLoss()
	}
	if p.State().PausesUsed != 1 {
		t.Fatalf("primeira pausa deveria ter usado, got %d", p.State().PausesUsed)
	}
	// Expira.
	for i := 0; i < 6; i++ {
		p.OnBar()
	}
	// Novos stops → guard NÃO re-dispara (max atingido).
	for i := 0; i < 2; i++ {
		p.OnBar()
		p.OnStopLoss()
	}
	if p.State().PausesUsed != 1 {
		t.Fatalf("MaxPauses=1 deveria limitar a 1 pausa, got %d", p.State().PausesUsed)
	}
}
