package event

import (
	"testing"
	"time"
)

func TestBusPublishByType(t *testing.T) {
	b := NewBus()
	var got []Event
	b.Subscribe(OrderCreated, func(e Event) { got = append(got, e) })

	b.Publish(Event{Type: OrderCreated, Source: "oms"})
	b.Publish(Event{Type: OrderFilled, Source: "oms"})

	if len(got) != 1 {
		t.Fatalf("esperava 1 evento OrderCreated, recebeu %d", len(got))
	}
	if got[0].Source != "oms" {
		t.Errorf("source = %q, esperava %q", got[0].Source, "oms")
	}
}

func TestBusSubscribeAll(t *testing.T) {
	b := NewBus()
	var got []Event
	b.SubscribeAll(func(e Event) { got = append(got, e) })

	b.Publish(Event{Type: MarketTick})
	b.Publish(Event{Type: TradeExecuted})

	if len(got) != 2 {
		t.Fatalf("esperava 2 eventos globais, recebeu %d", len(got))
	}
}

func TestBusUnsubscribe(t *testing.T) {
	b := NewBus()
	var got []Event
	cancel := b.Subscribe(OrderCreated, func(e Event) { got = append(got, e) })

	b.Publish(Event{Type: OrderCreated})
	cancel()
	b.Publish(Event{Type: OrderCreated})

	if len(got) != 1 {
		t.Fatalf("esperava 1 evento após unsubscribe, recebeu %d", len(got))
	}
}

func TestBusFillsTimestamp(t *testing.T) {
	b := NewBus()
	var got Event
	b.Subscribe(MarketTick, func(e Event) { got = e })
	b.Publish(Event{Type: MarketTick})

	if got.Timestamp.IsZero() {
		t.Error("esperava Timestamp preenchido pelo Publish")
	}
	if got.Timestamp.After(time.Now().Add(time.Second)) {
		t.Error("timestamp no futuro — clock inconsistente")
	}
}
