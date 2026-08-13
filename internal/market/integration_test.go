package market

import (
	"context"
	"testing"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// TestMarketDataToEngineStorePipeline valida o fluxo ponta a ponta:
// exchange → MarketData → engine.Emit → event store (rastro total).
func TestMarketDataToEngineStorePipeline(t *testing.T) {
	e := engine.New(nil)
	f := &fakeExchange{}
	md := New(f, e.Emit)

	if err := md.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	f.handler.OnStatus(exchange.Status{Exchange: "fake", State: "connected"})
	f.handler.OnTick(domain.Tick{Symbol: "BTCUSDT", Price: 100})

	all := e.Store.All()
	if len(all) != 2 {
		t.Fatalf("esperava 2 eventos no store (connected + tick), veio %d", len(all))
	}
	if all[0].Type != event.ExchangeConnected {
		t.Errorf("evento 0 = %q, esperava ExchangeConnected", all[0].Type)
	}
	if all[1].Type != event.MarketTick {
		t.Errorf("evento 1 = %q, esperava MarketTick", all[1].Type)
	}
}
