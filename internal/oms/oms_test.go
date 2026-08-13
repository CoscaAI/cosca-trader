package oms

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

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
		Quantity: d("1"), Price: d("50000"),
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

func TestPlaceOrderIdempotentByClientID(t *testing.T) {
	b := &fakeBroker{}
	var events []event.Event
	o := New(b, func(e event.Event) { events = append(events, e) })

	req := exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: d("1"), Price: d("50000"), ClientOrderID: "cliente-1",
	}
	first, _ := o.PlaceOrder(context.Background(), req)
	events = nil
	second, _ := o.PlaceOrder(context.Background(), req)

	if first.ID != second.ID {
		t.Errorf("idempotência violada: %q vs %q", first.ID, second.ID)
	}
	if len(events) != 0 {
		t.Errorf("reenvio não deveria emitir novo OrderCreated, veio %+v", events)
	}
}

func TestPlaceOrderValidates(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	cases := []exchange.OrderRequest{
		{Symbol: "", Side: domain.SideBuy, Quantity: d("1")},                                        // sem símbolo
		{Symbol: "BTCUSDT", Side: "hold", Quantity: d("1")},                                         // lado inválido
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Quantity: d("0")},                                 // qty zero
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1")},        // limit sem preço
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderType("lixo"), Quantity: d("1")}, // tipo inválido
	}
	for i, req := range cases {
		if _, err := o.PlaceOrder(context.Background(), req); err == nil {
			t.Errorf("caso %d deveria falhar", i)
		}
	}
}

func TestApplyTradeOpensPosition(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy, Price: d("100"), Quantity: d("2"), Timestamp: time.Now()})

	pos, ok := o.Position("BTCUSDT", "binance")
	if !ok {
		t.Fatal("posição não criada")
	}
	if pos.Side != domain.SideBuy || !pos.Quantity.Equal(d("2")) || !pos.AvgEntryPrice.Equal(d("100")) {
		t.Errorf("posição errada: %+v", pos)
	}
}

func TestApplyTradeAveragesUp(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("1"), Timestamp: ts})
	o.ApplyTrade(domain.Trade{ID: "t2", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("200"), Quantity: d("1"), Timestamp: ts})

	pos, _ := o.Position("X", "e")
	if !pos.Quantity.Equal(d("2")) || !pos.AvgEntryPrice.Equal(d("150")) {
		t.Errorf("média ponderada errada: qty=%v avg=%v", pos.Quantity, pos.AvgEntryPrice)
	}
}

func TestApplyTradeRealizesPnL(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	// compra 2 @ 100
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("2"), Timestamp: ts})
	// vende 2 @ 150 → fecha, PnL = (150-100)*2 = 100
	o.ApplyTrade(domain.Trade{ID: "t2", Symbol: "X", Exchange: "e", Side: domain.SideSell, Price: d("150"), Quantity: d("2"), Timestamp: ts})

	pos, _ := o.Position("X", "e")
	if !pos.Quantity.IsZero() {
		t.Errorf("posição deveria estar fechada, qty=%v", pos.Quantity)
	}
	if !pos.RealizedPnL.Equal(d("100")) {
		t.Errorf("PnL realizado = %v, esperava 100", pos.RealizedPnL)
	}
}

func TestApplyTradePreservesRealizedPnLOnReopen(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	// ciclo 1: compra 1 @ 100, vende 1 @ 200 → PnL 100
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("1"), Timestamp: ts})
	o.ApplyTrade(domain.Trade{ID: "t2", Symbol: "X", Exchange: "e", Side: domain.SideSell, Price: d("200"), Quantity: d("1"), Timestamp: ts})
	// ciclo 2: reabre compra 1 @ 300 — o PnL 100 NÃO pode ser perdido
	o.ApplyTrade(domain.Trade{ID: "t3", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("300"), Quantity: d("1"), Timestamp: ts})

	pos, _ := o.Position("X", "e")
	if !pos.RealizedPnL.Equal(d("100")) {
		t.Errorf("PnL realizado foi APAGADO no reabrir: %v, esperava 100", pos.RealizedPnL)
	}
}

func TestApplyTradeFlipsPosition(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	// long 1 @ 100
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("1"), Timestamp: ts})
	// vende 3 @ 120 → fecha 1 (PnL 20) + abre short 2
	o.ApplyTrade(domain.Trade{ID: "t2", Symbol: "X", Exchange: "e", Side: domain.SideSell, Price: d("120"), Quantity: d("3"), Timestamp: ts})

	pos, _ := o.Position("X", "e")
	if pos.Side != domain.SideSell || !pos.Quantity.Equal(d("2")) || !pos.AvgEntryPrice.Equal(d("120")) {
		t.Errorf("flip errado: %+v", pos)
	}
	if !pos.RealizedPnL.Equal(d("20")) {
		t.Errorf("PnL realizado = %v, esperava 20", pos.RealizedPnL)
	}
}

func TestApplyTradeDedupsByID(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	ts := time.Now()
	trade := domain.Trade{ID: "dup-1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("2"), Timestamp: ts}

	o.ApplyTrade(trade)
	o.ApplyTrade(trade) // retransmissão — deve ser ignorada

	pos, _ := o.Position("X", "e")
	if !pos.Quantity.Equal(d("2")) {
		t.Errorf("fill duplicado aplicado duas vezes: qty=%v", pos.Quantity)
	}
}

func TestApplyTradeTracksFees(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) {})
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("1"), Fee: d("0.5"), FeeAsset: "USDT", Timestamp: time.Now()})

	fees := o.Fees()
	if !fees["USDT"].Equal(d("0.5")) {
		t.Errorf("fee não rastreada: %v", fees)
	}
}

func TestApplyOrderUpdate(t *testing.T) {
	b := &fakeBroker{}
	var events []event.Event
	o := New(b, func(e event.Event) { events = append(events, e) })

	ord, _ := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1"), Price: d("100"),
	})
	events = nil

	o.ApplyOrderUpdate(domain.Order{ID: ord.ID, Status: domain.OrderFilled, FilledQty: d("1")})

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
	o.ApplyBalance(domain.Balance{Asset: "USDT", Free: d("1000"), Locked: d("500")})

	b := o.Balances()
	if len(b) != 1 || b[0].Asset != "USDT" || !b[0].Total().Equal(d("1500")) {
		t.Errorf("saldo errado: %+v", b)
	}
}

func TestReplayReconstructsState(t *testing.T) {
	var emitted []event.Event
	o := New(&fakeBroker{}, func(e event.Event) { emitted = append(emitted, e) })

	ts := time.Now()
	events := []event.Event{
		{Type: event.TradeExecuted, Payload: domain.Trade{ID: "t1", Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy, Price: d("100"), Quantity: d("2"), Timestamp: ts}},
		{Type: event.BalanceUpdated, Payload: domain.Balance{Asset: "USDT", Free: d("5000")}},
		{Type: event.OrderCreated, Payload: domain.Order{ID: "o1", Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy, Type: domain.OrderLimit, Price: d("100"), Quantity: d("2"), Status: domain.OrderNew}},
	}

	o.Replay(events)

	// estado reconstruído
	if pos, ok := o.Position("BTCUSDT", "binance"); !ok || !pos.Quantity.Equal(d("2")) {
		t.Errorf("posição não reconstruída no replay: %+v", pos)
	}
	if b := o.Balances(); len(b) != 1 || b[0].Asset != "USDT" {
		t.Errorf("saldo não reconstruído no replay: %+v", b)
	}
	if ords := o.Orders(); len(ords) != 1 || ords[0].ID != "o1" {
		t.Errorf("ordem não reconstruída no replay: %+v", ords)
	}

	// replay NÃO pode reemitir eventos (já estão no rastro)
	if len(emitted) != 0 {
		t.Errorf("Replay reemitiu %d eventos — deveria ser silencioso", len(emitted))
	}
}

func TestReplayDecodesRawMessage(t *testing.T) {
	// Simula payload vindo do disco (json.RawMessage, como o db.AllEvents devolve).
	var emitted []event.Event
	o := New(&fakeBroker{}, func(e event.Event) { emitted = append(emitted, e) })

	raw := json.RawMessage(`{"id":"t1","symbol":"BTCUSDT","exchange":"binance","side":"buy","price":"100","quantity":"2","timestamp":"2026-08-13T00:00:00Z"}`)
	o.Replay([]event.Event{{Type: event.TradeExecuted, Payload: raw}})

	pos, ok := o.Position("BTCUSDT", "binance")
	if !ok || !pos.Quantity.Equal(d("2")) {
		t.Errorf("replay de RawMessage falhou: %+v", pos)
	}
	if len(emitted) != 0 {
		t.Errorf("replay de RawMessage reemitiu eventos")
	}
}

func TestReconcileSyncsBalancesAndOrders(t *testing.T) {
	b := &reconcileBroker{
		balances: []domain.Balance{{Asset: "USDT", Free: d("9000")}},
		orders:   []domain.Order{{ID: "o9", Symbol: "BTCUSDT", Exchange: "binance", Status: domain.OrderNew, Side: domain.SideBuy}},
	}
	o := New(b, func(event.Event) {})
	// registra um símbolo conhecido para a reconciliação de ordens
	o.ApplyOrderUpdate(domain.Order{ID: "seed", Symbol: "BTCUSDT", Exchange: "binance", Status: domain.OrderNew})

	if err := o.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	bl := o.Balances()
	if len(bl) != 1 || bl[0].Asset != "USDT" || !bl[0].Free.Equal(d("9000")) {
		t.Errorf("reconciliação de saldos falhou: %+v", bl)
	}
	if ords := o.Orders(); len(ords) != 2 {
		t.Errorf("reconciliação de ordens falhou: %+v", ords)
	}
}

// reconcileBroker devolve saldos e ordens fixos para teste de Reconcile.
type reconcileBroker struct {
	fakeBroker
	balances []domain.Balance
	orders   []domain.Order
}

func (r *reconcileBroker) Balances(_ context.Context) ([]domain.Balance, error) {
	return r.balances, nil
}

func (r *reconcileBroker) OpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return r.orders, nil
}
