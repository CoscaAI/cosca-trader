package engine

import (
	"testing"

	"github.com/CoscaAI/cosca-trader/internal/event"
)

func TestEmitFillsDefaults(t *testing.T) {
	e := New(nil)

	var got event.Event
	e.Bus.Subscribe(event.MarketTick, func(ev event.Event) { got = ev })
	e.Hub.Subscribe() // consumir para o hub não bloquear

	e.Emit(event.Event{Type: event.MarketTick, Source: "feed"})

	if got.ID == "" {
		t.Error("Emit deveria preencher ID")
	}
	if got.Timestamp.IsZero() {
		t.Error("Emit deveria preencher Timestamp")
	}
	if got.Severity != event.SeverityInfo {
		t.Errorf("Severity = %q, esperava info", got.Severity)
	}
}

func TestEmitPersistsAndPublishes(t *testing.T) {
	e := New(nil)
	sub := e.Hub.Subscribe()

	e.Emit(event.Event{Type: event.OrderCreated, Source: "oms"})

	if e.Store.Len() != 1 {
		t.Fatalf("store Len = %d, esperava 1", e.Store.Len())
	}

	select {
	case ev := <-sub:
		if ev.Type != event.OrderCreated {
			t.Errorf("hub recebeu %q, esperava OrderCreated", ev.Type)
		}
	default:
		t.Error("hub não recebeu o evento")
	}
}

func TestEmitBusOrdering(t *testing.T) {
	// O evento deve estar persistido ANTES de ser publicado no bus.
	e := New(nil)
	var storeLenAtDispatch int
	e.Bus.Subscribe(event.OrderFilled, func(ev event.Event) {
		storeLenAtDispatch = e.Store.Len()
	})

	e.Emit(event.Event{Type: event.OrderFilled, Source: "oms"})

	if storeLenAtDispatch != 1 {
		t.Errorf("no dispatch o store tinha %d eventos; persistir-before-publish violado", storeLenAtDispatch)
	}
}
