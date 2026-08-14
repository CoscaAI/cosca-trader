package market

import (
	"context"
	"testing"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// fakeExchange implementa exchange.Exchange em memória para testes do motor.
type fakeExchange struct {
	handler    exchange.Handler
	candleSubs []string
	tradeSubs  []string
}

func (f *fakeExchange) Name() string { return "fake" }
func (f *fakeExchange) Connect(_ context.Context, h exchange.Handler) error {
	f.handler = h
	return nil
}
func (f *fakeExchange) SubscribeCandles(symbol, interval string) error {
	f.candleSubs = append(f.candleSubs, symbol+"@"+interval)
	return nil
}
func (f *fakeExchange) SubscribeTrades(symbol string) error {
	f.tradeSubs = append(f.tradeSubs, symbol)
	return nil
}
func (f *fakeExchange) Close() error { return nil }

func TestMarketEmitsEvents(t *testing.T) {
	f := &fakeExchange{}
	var events []event.Event
	m := New(f, func(e event.Event) error { events = append(events, e); return nil })

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	f.handler.OnStatus(exchange.Status{Exchange: "fake", State: "connected"})
	f.handler.OnTick(domain.Tick{Symbol: "BTCUSDT", Price: 100})
	f.handler.OnCandle(domain.Candle{Symbol: "BTCUSDT", Complete: true})
	f.handler.OnCandle(domain.Candle{Symbol: "BTCUSDT", Complete: false}) // vela em formação → ignorada

	if len(events) != 3 {
		t.Fatalf("esperava 3 eventos, veio %d: %+v", len(events), events)
	}
	if events[0].Type != event.ExchangeConnected {
		t.Errorf("evento 0 = %q, esperava ExchangeConnected", events[0].Type)
	}
	if events[1].Type != event.MarketTick {
		t.Errorf("evento 1 = %q, esperava MarketTick", events[1].Type)
	}
	if events[2].Type != event.CandleClosed {
		t.Errorf("evento 2 = %q, esperava CandleClosed", events[2].Type)
	}
}

func TestMarketWatchSubscribes(t *testing.T) {
	f := &fakeExchange{}
	m := New(f, func(event.Event) error { return nil })

	if err := m.Watch("BTCUSDT", "1m"); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	if len(f.candleSubs) != 1 || f.candleSubs[0] != "BTCUSDT@1m" {
		t.Errorf("candleSubs = %v", f.candleSubs)
	}
	if len(f.tradeSubs) != 1 || f.tradeSubs[0] != "BTCUSDT" {
		t.Errorf("tradeSubs = %v", f.tradeSubs)
	}
}

func TestMarketStatusSeverity(t *testing.T) {
	f := &fakeExchange{}
	var events []event.Event
	m := New(f, func(e event.Event) error { events = append(events, e); return nil })
	_ = m.Start(context.Background())

	f.handler.OnStatus(exchange.Status{Exchange: "fake", State: "error", Message: "boom"})

	if len(events) != 1 {
		t.Fatalf("esperava 1 evento, veio %d", len(events))
	}
	if events[0].Type != event.ExchangeError || events[0].Severity != event.SeverityError {
		t.Errorf("evento = %q/%q, esperava ExchangeError/error", events[0].Type, events[0].Severity)
	}
}
