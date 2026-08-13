package store

import (
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/event"
)

func TestStoreAppendAndAll(t *testing.T) {
	s := New()
	now := time.Now()
	s.Append(event.Event{Type: event.MarketTick, Timestamp: now})
	s.Append(event.Event{Type: event.OrderCreated, Timestamp: now.Add(time.Second)})

	if s.Len() != 2 {
		t.Fatalf("Len = %d, esperava 2", s.Len())
	}

	all := s.All()
	if len(all) != 2 {
		t.Fatalf("All len = %d, esperava 2", len(all))
	}
	if all[0].Type != event.MarketTick {
		t.Errorf("primeiro evento = %q, esperava MarketTick", all[0].Type)
	}
}

func TestStoreAllIsCopy(t *testing.T) {
	s := New()
	s.Append(event.Event{Type: event.MarketTick})

	all := s.All()
	all[0].Type = event.OrderFilled // mutar a cópia

	if s.All()[0].Type != event.MarketTick {
		t.Error("All devolveu a fatia interna — deve devolver cópia")
	}
}

func TestStoreTimeRange(t *testing.T) {
	s := New()
	if f, l := s.TimeRange(); f != "" || l != "" {
		t.Errorf("store vazio devia ter range vazio, veio %q/%q", f, l)
	}

	base := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	s.Append(event.Event{Type: event.MarketTick, Timestamp: base})
	s.Append(event.Event{Type: event.MarketTick, Timestamp: base.Add(time.Hour)})

	first, last := s.TimeRange()
	if first == "" || last == "" {
		t.Fatal("esperava range preenchido")
	}
	if first == last {
		t.Error("first e last deveriam diferir")
	}
}
