package stream

import (
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/event"
)

func TestHubPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()

	h.Publish(event.Event{Type: event.MarketTick, Source: "feed"})

	select {
	case ev := <-ch:
		if ev.Type != event.MarketTick || ev.Source != "feed" {
			t.Errorf("evento errado: %+v", ev)
		}
	default:
		t.Error("assinante não recebeu o evento")
	}
}

func TestHubUnsubscribe(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()
	if h.SubscriberCount() != 1 {
		t.Errorf("SubscriberCount = %d, esperava 1", h.SubscriberCount())
	}
	h.Unsubscribe(ch)
	if h.SubscriberCount() != 0 {
		t.Errorf("SubscriberCount = %d após unsubscribe, esperava 0", h.SubscriberCount())
	}
}

func TestHubDropSlowSubscriber(t *testing.T) {
	h := NewHub()
	h.Subscribe() // assinante lento: ninguém consome o canal

	// Buffer é 1024; publicar muito mais deve DESCARTAR sem bloquear.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5000; i++ {
			h.Publish(event.Event{Type: event.MarketTick})
		}
		close(done)
	}()

	select {
	case <-done:
		// sucesso: não bloqueou no assinante lento
	case <-time.After(2 * time.Second):
		t.Fatal("Publish bloqueou com assinante lento")
	}
}
