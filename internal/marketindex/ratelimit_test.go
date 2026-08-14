package marketindex

import (
	"testing"
	"time"
)

func TestRateLimiterThrottles(t *testing.T) {
	r := NewRateLimiter(100*time.Millisecond, time.Minute)
	if !r.Allow() {
		t.Fatal("primeira chamada deveria passar")
	}
	if r.Allow() {
		t.Fatal("segunda chamada imediata deveria ser bloqueada")
	}
	time.Sleep(120 * time.Millisecond)
	if !r.Allow() {
		t.Fatal("após o intervalo deveria passar")
	}
}

func TestRateLimiterFresh(t *testing.T) {
	r := NewRateLimiter(time.Minute, time.Minute)
	if !r.Fresh(time.Second) {
		t.Fatal("dados de 1s deveriam estar frescos")
	}
	if r.Fresh(2 * time.Minute) {
		t.Fatal("dados de 2min deveriam estar stale")
	}
}

func TestIsDataEnough(t *testing.T) {
	r := NewRateLimiter(time.Minute, time.Minute)
	// Snapshot completo e fresco → dados suficientes.
	snap := Snapshot{
		FetchedAt: time.Now(),
		Quotes: []Quote{
			quote("^GSPC", "S&P 500", 7800),
			quote("^VIX", "VIX", 14),
		},
	}
	if !IsDataEnough(snap, r) {
		t.Fatal("snapshot completo e fresco deveria ser suficiente")
	}
	// Snapshot velho → NÃO suficiente (proteção por falta de dados).
	stale := Snapshot{
		FetchedAt: time.Now().Add(-2 * time.Minute),
		Quotes: []Quote{
			quote("^GSPC", "S&P 500", 7800),
			quote("^VIX", "VIX", 14),
		},
	}
	if IsDataEnough(stale, r) {
		t.Fatal("dados velhos não deveriam ser suficientes")
	}
	// Sem VIX → NÃO suficiente.
	incomplete := Snapshot{
		FetchedAt: time.Now(),
		Quotes:    []Quote{quote("^GSPC", "S&P 500", 7800)},
	}
	if IsDataEnough(incomplete, r) {
		t.Fatal("sem VIX não deveria ser suficiente")
	}
}
