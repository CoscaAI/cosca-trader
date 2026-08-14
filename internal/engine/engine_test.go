package engine

import (
	"path/filepath"
	"testing"

	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/store"
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

func TestEmitMoneyEventFailStopOnPersistError(t *testing.T) {
	// Fase 2A: evento de DINHEIRO com persistência falha → Emit devolve erro
	// (fail-parado). Evento de market data continua tolerante.
	db, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	db.Close() // força falha de persistência (database is closed)

	e := New(db)

	if err := e.Emit(event.Event{Type: event.OrderCreated, Source: "oms"}); err == nil {
		t.Fatal("OrderCreated sem persistência deveria falhar (fail-parado)")
	}
	if err := e.Emit(event.Event{Type: event.TradeExecuted, Source: "oms"}); err == nil {
		t.Fatal("TradeExecuted sem persistência deveria falhar (fail-parado)")
	}
	// Market data: tolerante — não devolve erro mesmo com o disco falho.
	if err := e.Emit(event.Event{Type: event.MarketTick, Source: "feed"}); err != nil {
		t.Errorf("MarketTick não deveria falhar o Emit (tolerante): %v", err)
	}
}

func TestIsMoneyEventClassification(t *testing.T) {
	money := []event.Type{
		event.OrderCreated, event.OrderSubmitted, event.OrderFilled, event.OrderPartiallyFilled,
		event.OrderCanceled, event.OrderRejected, event.OrderExpired,
		event.TradeExecuted, event.PositionOpened, event.PositionUpdated, event.PositionClosed,
		event.BalanceUpdated, event.RiskBreach, event.RiskWarning,
	}
	for _, ty := range money {
		if !isMoneyEvent(ty) {
			t.Errorf("%q deveria ser evento de dinheiro (fail-stop)", ty)
		}
	}
	tolerant := []event.Type{
		event.MarketTick, event.CandleClosed, event.OrderBookUpdate, event.DepthSnapshot,
		event.ExchangeConnected, event.ExchangeDisconnected, event.ExchangeError,
		event.StrategySignal, event.SystemStarted, event.SystemStopped,
	}
	for _, ty := range tolerant {
		if isMoneyEvent(ty) {
			t.Errorf("%q NÃO deveria ser evento de dinheiro (tolerante)", ty)
		}
	}
}
