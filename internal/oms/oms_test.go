package oms

import (
	"context"
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// fakeBroker implementa exchange.Broker em memória.
type fakeBroker struct {
	placed []exchange.OrderRequest
	nextID int
}

func (f *fakeBroker) PlaceOrder(_ context.Context, req exchange.OrderRequest) (domain.Order, error) {
	f.placed = append(f.placed, req)
	f.nextID++
	return domain.Order{
		ID:            "ord-" + itoa(f.nextID),
		ClientOrderID: req.ClientOrderID,
		Symbol:        req.Symbol,
		Side:          req.Side,
		Type:          req.Type,
		Price:         req.Price,
		Quantity:      req.Quantity,
		Status:        domain.OrderNew,
		TimeInForce:   req.TimeInForce,
		CreatedAt:     time.Now(),
	}, nil
}

func (f *fakeBroker) CancelOrder(_ context.Context, _, _ string) error { return nil }
func (f *fakeBroker) Balances(_ context.Context) ([]domain.Balance, error) {
	return nil, nil
}
func (f *fakeBroker) OpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return nil, nil
}
func (f *fakeBroker) StartUserStream(_ context.Context, _ exchange.Handler) error { return nil }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestPlaceOrderEmitsCreated(t *testing.T) {
	b := &fakeBroker{}
	var events []event.Event
	o := New(b, func(e event.Event) { events = append(events, e) })

	ord, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: 1, Price: 50000,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderNew || ord.ID == "" {
		t.Errorf("ordem errada: %+v", ord)
	}
	if len(events) != 1 || events[0].Type != event.OrderCreated {
		t.Errorf("esperava OrderCreated, veio %+v", events)
	}
	if len(o.Orders()) != 1 {
		t.Errorf("OMS devia registrar 1 ordem")
	}
}

func TestPlaceOrderValidates(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	cases := []exchange.OrderRequest{
		{Symbol: "", Side: domain.SideBuy, Quantity: 1},                                 // sem símbolo
		{Symbol: "BTCUSDT", Side: "hold", Quantity: 1},                                  // lado inválido
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Quantity: 0},                          // qty zero
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: 1}, // limit sem preço
	}
	for i, req := range cases {
		if _, err := o.PlaceOrder(context.Background(), req); err == nil {
			t.Errorf("caso %d deveria falhar", i)
		}
	}
}

func TestApplyTradeOpensPosition(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	o.ApplyTrade(domain.Trade{Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy, Price: 100, Quantity: 2, Timestamp: time.Now()})

	pos, ok := o.Position("BTCUSDT")
	if !ok {
		t.Fatal("posição não criada")
	}
	if pos.Side != domain.SideBuy || pos.Quantity != 2 || pos.AvgEntryPrice != 100 {
		t.Errorf("posição errada: %+v", pos)
	}
}

func TestApplyTradeAveragesUp(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	o.ApplyTrade(domain.Trade{Symbol: "X", Side: domain.SideBuy, Price: 100, Quantity: 1, Timestamp: ts})
	o.ApplyTrade(domain.Trade{Symbol: "X", Side: domain.SideBuy, Price: 200, Quantity: 1, Timestamp: ts})

	pos, _ := o.Position("X")
	if pos.Quantity != 2 || pos.AvgEntryPrice != 150 {
		t.Errorf("média ponderada errada: qty=%v avg=%v", pos.Quantity, pos.AvgEntryPrice)
	}
}

func TestApplyTradeRealizesPnL(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	// compra 2 @ 100
	o.ApplyTrade(domain.Trade{Symbol: "X", Side: domain.SideBuy, Price: 100, Quantity: 2, Timestamp: ts})
	// vende 2 @ 150 → fecha, PnL = (150-100)*2 = 100
	o.ApplyTrade(domain.Trade{Symbol: "X", Side: domain.SideSell, Price: 150, Quantity: 2, Timestamp: ts})

	pos, _ := o.Position("X")
	if pos.Quantity != 0 {
		t.Errorf("posição deveria estar fechada, qty=%v", pos.Quantity)
	}
	if pos.RealizedPnL != 100 {
		t.Errorf("PnL realizado = %v, esperava 100", pos.RealizedPnL)
	}
}

func TestApplyTradeFlipsPosition(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	// long 1 @ 100
	o.ApplyTrade(domain.Trade{Symbol: "X", Side: domain.SideBuy, Price: 100, Quantity: 1, Timestamp: ts})
	// vende 3 @ 120 → fecha 1 (PnL 20) + abre short 2
	o.ApplyTrade(domain.Trade{Symbol: "X", Side: domain.SideSell, Price: 120, Quantity: 3, Timestamp: ts})

	pos, _ := o.Position("X")
	if pos.Side != domain.SideSell || pos.Quantity != 2 || pos.AvgEntryPrice != 120 {
		t.Errorf("flip errado: %+v", pos)
	}
	if pos.RealizedPnL != 20 {
		t.Errorf("PnL realizado = %v, esperava 20", pos.RealizedPnL)
	}
}

func TestApplyOrderUpdate(t *testing.T) {
	b := &fakeBroker{}
	var events []event.Event
	o := New(b, func(e event.Event) { events = append(events, e) })

	// registra a ordem via PlaceOrder
	ord, _ := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: 1, Price: 100,
	})
	events = nil

	// atualização de fill
	o.ApplyOrderUpdate(domain.Order{ID: ord.ID, Status: domain.OrderFilled, FilledQty: 1})

	if len(events) != 1 || events[0].Type != event.OrderFilled {
		t.Errorf("esperava OrderFilled, veio %+v", events)
	}
	stored := o.Orders()[0]
	if stored.Status != domain.OrderFilled {
		t.Errorf("estado não atualizado: %+v", stored)
	}
	if stored.Symbol != "BTCUSDT" {
		t.Errorf("mergeOrder perdeu o símbolo: %+v", stored)
	}
}

func TestApplyBalance(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	o.ApplyBalance(domain.Balance{Asset: "USDT", Free: 1000, Locked: 500})

	b := o.Balances()
	if len(b) != 1 || b[0].Asset != "USDT" || b[0].Total() != 1500 {
		t.Errorf("saldo errado: %+v", b)
	}
}
