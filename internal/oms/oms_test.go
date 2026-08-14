package oms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/store"
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
		StopPrice:     req.StopPrice,
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
	o := New(b, func(e event.Event) error { events = append(events, e); return nil })

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
	o := New(b, func(e event.Event) error { events = append(events, e); return nil })

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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
	ts := time.Now()
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("1"), Timestamp: ts})
	o.ApplyTrade(domain.Trade{ID: "t2", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("200"), Quantity: d("1"), Timestamp: ts})

	pos, _ := o.Position("X", "e")
	if !pos.Quantity.Equal(d("2")) || !pos.AvgEntryPrice.Equal(d("150")) {
		t.Errorf("média ponderada errada: qty=%v avg=%v", pos.Quantity, pos.AvgEntryPrice)
	}
}

func TestApplyTradeRealizesPnL(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "X", Exchange: "e", Side: domain.SideBuy, Price: d("100"), Quantity: d("1"), Fee: d("0.5"), FeeAsset: "USDT", Timestamp: time.Now()})

	fees := o.Fees()
	if !fees["USDT"].Equal(d("0.5")) {
		t.Errorf("fee não rastreada: %v", fees)
	}
}

func TestApplyOrderUpdate(t *testing.T) {
	b := &fakeBroker{}
	var events []event.Event
	o := New(b, func(e event.Event) error { events = append(events, e); return nil })

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
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
	o.ApplyBalance(domain.Balance{Asset: "USDT", Free: d("1000"), Locked: d("500")})

	b := o.Balances()
	if len(b) != 1 || b[0].Asset != "USDT" || !b[0].Total().Equal(d("1500")) {
		t.Errorf("saldo errado: %+v", b)
	}
}

func TestReplayReconstructsState(t *testing.T) {
	var emitted []event.Event
	o := New(&fakeBroker{}, func(e event.Event) error { emitted = append(emitted, e); return nil })

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
	o := New(&fakeBroker{}, func(e event.Event) error { emitted = append(emitted, e); return nil })

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
	o := New(b, func(event.Event) error { return nil })
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

// sagaBroker simula um broker com PlaceOrder instável e OrderRecoverer
// configurável — usado nos testes da saga de recuperação (P0-2).
type sagaBroker struct {
	failPlaceOrder bool
	found          domain.Order // preenchido = a ordem existe na exchange
	queries        []string
}

func (s *sagaBroker) PlaceOrder(_ context.Context, req exchange.OrderRequest) (domain.Order, error) {
	if s.failPlaceOrder {
		return domain.Order{}, errors.New("timeout simulado")
	}
	return domain.Order{
		ID:            "o1",
		ClientOrderID: req.ClientOrderID,
		Symbol:        req.Symbol,
		Side:          req.Side,
		Type:          req.Type,
		Quantity:      req.Quantity,
		Status:        domain.OrderNew,
	}, nil
}

func (s *sagaBroker) OrderByClientOrderID(_ context.Context, _ string, clientOrderID string) (domain.Order, error) {
	s.queries = append(s.queries, clientOrderID)
	if s.found.ID != "" {
		return s.found, nil
	}
	return domain.Order{}, nil
}

func (s *sagaBroker) CancelOrder(_ context.Context, _, _ string) error { return nil }
func (s *sagaBroker) Balances(_ context.Context) ([]domain.Balance, error) {
	return nil, nil
}
func (s *sagaBroker) OpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return nil, nil
}
func (s *sagaBroker) StartUserStream(_ context.Context, _ exchange.Handler) error { return nil }

// recoverBroker é um fakeBroker com OrderRecoverer (para Reconcile por
// origClientOrderId).
type recoverBroker struct {
	fakeBroker
	found domain.Order
}

func (r *recoverBroker) OrderByClientOrderID(_ context.Context, _ string, _ string) (domain.Order, error) {
	if r.found.ID != "" {
		return r.found, nil
	}
	return domain.Order{}, nil
}

// ambiguousBroker sempre falha, inclusive na consulta de recuperação — o
// estado vira ambíguo e a chave deve ficar travada.
type ambiguousBroker struct {
	placed []string
}

func (a *ambiguousBroker) PlaceOrder(_ context.Context, req exchange.OrderRequest) (domain.Order, error) {
	a.placed = append(a.placed, req.ClientOrderID)
	return domain.Order{}, errors.New("timeout simulado")
}
func (a *ambiguousBroker) OrderByClientOrderID(_ context.Context, _, _ string) (domain.Order, error) {
	return domain.Order{}, errors.New("consulta de recuperação também falhou")
}
func (a *ambiguousBroker) CancelOrder(_ context.Context, _, _ string) error { return nil }
func (a *ambiguousBroker) Balances(_ context.Context) ([]domain.Balance, error) {
	return nil, nil
}
func (a *ambiguousBroker) OpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return nil, nil
}
func (a *ambiguousBroker) StartUserStream(_ context.Context, _ exchange.Handler) error { return nil }

// priceBroker é um fakeBroker com PriceProvider (para o limite de notional de
// ordens market).
type priceBroker struct {
	fakeBroker
	price decimal.Decimal
}

func (p *priceBroker) Price(_ context.Context, _ string) (decimal.Decimal, error) {
	return p.price, nil
}

// ── P1-1: limite de valor por ordem ────────────────────────────────────────

func TestPlaceOrderNotionalLimit(t *testing.T) {
	b := &priceBroker{price: d("50000")}
	o := New(b, func(event.Event) error { return nil }, WithMaxOrderUSDT(d("1000")))

	// limit: 50000 × 1 = 50000 > 1000 → rejeitado
	_, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1"), Price: d("50000"),
	})
	if !errors.Is(err, ErrOrderTooLarge) {
		t.Fatalf("esperava ErrOrderTooLarge, veio %v", err)
	}
	if len(b.placed) != 0 {
		t.Errorf("ordem acima do limite não deveria chegar ao broker")
	}

	// dentro do limite (50000 × 0.01 = 500 ≤ 1000) → passa
	ord, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("0.01"), Price: d("50000"),
	})
	if err != nil {
		t.Fatalf("ordem dentro do limite falhou: %v", err)
	}
	if ord.ID == "" {
		t.Error("ordem válida não registrada")
	}

	// market sem preço: estima notional pelo preço corrente (50000 × 0.1 = 5000 > 1000)
	_, err = o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket, Quantity: d("0.1"),
	})
	if !errors.Is(err, ErrOrderTooLarge) {
		t.Fatalf("market acima do limite deveria falhar, veio %v", err)
	}
	// market pequeno passa (50000 × 0.001 = 50)
	if _, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket, Quantity: d("0.001"),
	}); err != nil {
		t.Fatalf("market dentro do limite falhou: %v", err)
	}
}

func TestPlaceOrderNoLimitWhenDisabled(t *testing.T) {
	// limite 0 = desativado: ordem grande passa (padrão de biblioteca).
	b := &fakeBroker{}
	o := New(b, func(event.Event) error { return nil }, WithMaxOrderUSDT(decimal.Zero))
	ord, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("100"), Price: d("50000"),
	})
	if err != nil {
		t.Fatalf("com trava desativada a ordem deveria passar: %v", err)
	}
	if ord.ID == "" {
		t.Error("ordem não registrada")
	}
}

// trackingBroker é thread-safe (o stop-loss automático roda em goroutine —
// precisa tolerar -race).
type trackingBroker struct {
	mu     sync.Mutex
	placed []exchange.OrderRequest
}

func (t *trackingBroker) PlaceOrder(_ context.Context, req exchange.OrderRequest) (domain.Order, error) {
	t.mu.Lock()
	t.placed = append(t.placed, req)
	ord := domain.Order{
		ID:            "ord-" + itoa(len(t.placed)),
		ClientOrderID: req.ClientOrderID,
		Symbol:        req.Symbol,
		Side:          req.Side,
		Type:          req.Type,
		Price:         req.Price,
		StopPrice:     req.StopPrice,
		Quantity:      req.Quantity,
		Status:        domain.OrderNew,
	}
	t.mu.Unlock()
	return ord, nil
}

func (t *trackingBroker) requests() []exchange.OrderRequest {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]exchange.OrderRequest(nil), t.placed...)
}

func (t *trackingBroker) CancelOrder(_ context.Context, _, _ string) error { return nil }
func (t *trackingBroker) Balances(_ context.Context) ([]domain.Balance, error) {
	return nil, nil
}
func (t *trackingBroker) OpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return nil, nil
}
func (t *trackingBroker) StartUserStream(_ context.Context, _ exchange.Handler) error { return nil }

// ── P1-2: stop-loss automático + validação de stop orders ──────────────────

func TestPlaceOrderRequiresStopPrice(t *testing.T) {
	o := New(&fakeBroker{}, func(event.Event) error { return nil })
	cases := []exchange.OrderRequest{
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderStop, Quantity: d("1")},
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderStopMarket, Quantity: d("1")},
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderStopLimit, Quantity: d("1"), Price: d("90")},
		{Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderStopLimit, Quantity: d("1"), StopPrice: d("95")},
	}
	for i, req := range cases {
		if _, err := o.PlaceOrder(context.Background(), req); err == nil {
			t.Errorf("caso %d deveria falhar (stop sem campos obrigatórios)", i)
		}
	}
	if _, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderStop, Quantity: d("1"), StopPrice: d("95"),
	}); err != nil {
		t.Errorf("stop válido falhou: %v", err)
	}
}

func TestPlaceStopLoss(t *testing.T) {
	b := &fakeBroker{}
	o := New(b, func(event.Event) error { return nil })

	// long 2 @ 100, pct 5% → SELL stop a 95, quantidade 2.
	ord, err := o.PlaceStopLoss(context.Background(), domain.Position{
		Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy, Quantity: d("2"), AvgEntryPrice: d("100"),
	}, d("0.05"))
	if err != nil {
		t.Fatalf("PlaceStopLoss: %v", err)
	}
	if ord.Side != domain.SideSell || !ord.StopPrice.Equal(d("95")) || !ord.Quantity.Equal(d("2")) {
		t.Errorf("stop long errado: %+v", ord)
	}
	if ord.ClientOrderID == "" {
		t.Error("stop sem client_order_id")
	}

	// short 2 @ 100, pct 5% → BUY stop a 105.
	ord2, err := o.PlaceStopLoss(context.Background(), domain.Position{
		Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideSell, Quantity: d("2"), AvgEntryPrice: d("100"),
	}, d("0.05"))
	if err != nil {
		t.Fatalf("PlaceStopLoss short: %v", err)
	}
	if ord2.Side != domain.SideBuy || !ord2.StopPrice.Equal(d("105")) {
		t.Errorf("stop short errado: %+v", ord2)
	}

	// posição sem quantidade → erro.
	if _, err := o.PlaceStopLoss(context.Background(), domain.Position{
		Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy, Quantity: decimal.Zero, AvgEntryPrice: d("100"),
	}, d("0.05")); err == nil {
		t.Error("posição sem quantidade deveria falhar")
	}
}

func TestAutoStopLossOnPositionOpen(t *testing.T) {
	b := &trackingBroker{}
	o := New(b, func(event.Event) error { return nil }, WithStopLossPct(d("0.05")))

	o.ApplyTrade(domain.Trade{
		ID: "t1", Symbol: "BTCUSDT", Exchange: "binance", Side: domain.SideBuy,
		Price: d("100"), Quantity: d("2"), Timestamp: time.Now(),
	})

	// o stop-loss é assíncrono (user stream não pode bloquear) — aguarda.
	var stop *exchange.OrderRequest
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for i := range b.requests() {
			if b.requests()[i].Type == domain.OrderStop {
				cp := b.requests()[i]
				stop = &cp
				break
			}
		}
		if stop != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stop == nil {
		t.Fatal("stop-loss automático não foi enviado após abrir posição")
	}
	if stop.Side != domain.SideSell || !stop.Quantity.Equal(d("2")) || !stop.StopPrice.Equal(d("95")) {
		t.Errorf("stop automático errado: %+v", stop)
	}
}

func TestApplyOrderUpdateOrderExpired(t *testing.T) {
	var events []event.Event
	o := New(&fakeBroker{}, func(e event.Event) error { events = append(events, e); return nil })

	o.ApplyOrderUpdate(domain.Order{ID: "o1", Symbol: "BTCUSDT", Status: domain.OrderExpired})

	if len(events) != 1 || events[0].Type != event.OrderExpired {
		t.Fatalf("EXPIRED deveria emitir OrderExpired, veio %+v", events)
	}
	if events[0].Severity != event.SeverityWarning {
		t.Errorf("EXPIRED deveria ser warning, veio %q", events[0].Severity)
	}
}

// ── P0-3: kill switch ──────────────────────────────────────────────────────

func TestKillSwitchBlocksNewOrders(t *testing.T) {
	b := &fakeBroker{}
	o := New(b, func(event.Event) error { return nil })
	o.SetKillSwitch(true)

	req := exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1"), Price: d("50000"),
	}
	if _, err := o.PlaceOrder(context.Background(), req); !errors.Is(err, ErrKillSwitch) {
		t.Errorf("esperava ErrKillSwitch com kill ativo, veio %v", err)
	}
	if len(b.placed) != 0 {
		t.Errorf("kill switch deveria impedir qualquer envio ao broker, veio %d", len(b.placed))
	}

	o.SetKillSwitch(false)
	if _, err := o.PlaceOrder(context.Background(), req); err != nil {
		t.Errorf("com kill desativado a ordem deveria passar: %v", err)
	}
}

// ── P0-2: client_order_id obrigatório + saga de recuperação ────────────────

func TestPlaceOrderAutoGeneratesClientOrderID(t *testing.T) {
	b := &fakeBroker{}
	o := New(b, func(event.Event) error { return nil })
	ord, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1"), Price: d("50000"),
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.ClientOrderID == "" {
		t.Error("client_order_id não foi gerado automaticamente")
	}
	if len(b.placed) != 1 || b.placed[0].ClientOrderID == "" {
		t.Error("ordem enviada ao broker sem client_order_id — double trade possível")
	}
}

func TestPlaceOrderRejectsAmbiguousDuplicate(t *testing.T) {
	b := &ambiguousBroker{}
	o := New(b, func(event.Event) error { return nil })
	req := exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket, Quantity: d("1"), ClientOrderID: "dup-amb",
	}
	if _, err := o.PlaceOrder(context.Background(), req); err == nil {
		t.Fatal("primeira tentativa deveria falhar (estado ambíguo)")
	}
	// estado ambíguo → chave travada → reenvio com o MESMO client_order_id
	// deve ser rejeitado (409) em vez de gerar double trade.
	_, err := o.PlaceOrder(context.Background(), req)
	if !errors.Is(err, ErrDuplicateOrder) {
		t.Errorf("esperava ErrDuplicateOrder, veio %v", err)
	}
	if len(b.placed) != 1 {
		t.Errorf("double trade: broker recebeu %d envios, esperava 1", len(b.placed))
	}
}

func TestPlaceOrderSagaRecoversAcceptedOrder(t *testing.T) {
	b := &sagaBroker{
		failPlaceOrder: true,
		found:          domain.Order{ID: "o-rec", ClientOrderID: "cli-1", Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket, Quantity: d("1"), Status: domain.OrderNew},
	}
	var events []event.Event
	o := New(b, func(e event.Event) error { events = append(events, e); return nil })

	req := exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket, Quantity: d("1"), ClientOrderID: "cli-1",
	}
	ord, err := o.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("saga deveria recuperar a ordem aceita: %v", err)
	}
	if ord.ID != "o-rec" {
		t.Errorf("ordem adotada errada: %+v", ord)
	}
	if len(events) != 1 || events[0].Type != event.OrderCreated {
		t.Errorf("ordem adotada deveria emitir OrderCreated, veio %+v", events)
	}
	// a ordem recuperada entra no índice → reenvio devolve a mesma (at-most-once)
	again, _ := o.PlaceOrder(context.Background(), req)
	if again.ID != "o-rec" {
		t.Errorf("reenvio após recuperação deveria ser idempotente: %+v", again)
	}
}

func TestPlaceOrderConfirmedFailureUnlocksKey(t *testing.T) {
	b := &sagaBroker{failPlaceOrder: true} // found vazio = confirmado que NÃO existe
	o := New(b, func(event.Event) error { return nil })
	req := exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket, Quantity: d("1"), ClientOrderID: "cli-2",
	}
	if _, err := o.PlaceOrder(context.Background(), req); err == nil {
		t.Fatal("falha real deveria ser reportada")
	}
	// chave liberada: uma nova tentativa (broker agora ok) é legítima.
	b.failPlaceOrder = false
	ord, err := o.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("reenvio legítimo falhou: %v", err)
	}
	if ord.ID != "o1" {
		t.Errorf("ordem errada após reenvio legítimo: %+v", ord)
	}
}

func TestReconcileAdoptsOrphanByClientOrderID(t *testing.T) {
	b := &recoverBroker{found: domain.Order{
		ID: "orphan", ClientOrderID: "cli-x", Symbol: "BTCUSDT", Exchange: "binance",
		Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1"), Price: d("100"), Status: domain.OrderNew,
	}}
	o := New(b, func(event.Event) error { return nil })
	// simula uma chave ambígua persistida (tentativa cujo resultado se perdeu)
	o.mu.Lock()
	o.pending["cli-x"] = "BTCUSDT"
	o.mu.Unlock()

	if err := o.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	found := false
	for _, ord := range o.Orders() {
		if ord.ID == "orphan" {
			found = true
		}
	}
	if !found {
		t.Errorf("ordem órfã não foi adotada pelo Reconcile: %+v", o.Orders())
	}
}

// ── Fase 2A: intent durável pré-broker ──────────────────────────────────────

// intentStoreMem é uma implementação em memória de IntentStore para testes
// (o store.DB real é coberto em store/intent_test.go).
type intentStoreMem struct {
	mu       sync.Mutex
	byID     map[string]store.Intent
	failSave bool
}

func newIntentStoreMem() *intentStoreMem {
	return &intentStoreMem{byID: make(map[string]store.Intent)}
}

func (m *intentStoreMem) SaveIntent(i store.Intent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSave {
		return errors.New("disco cheio (simulado)")
	}
	m.byID[i.ClientOrderID] = i
	return nil
}

func (m *intentStoreMem) UpdateIntentStatus(id string, status store.IntentStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("intent %q não existe", id)
	}
	it.Status = status
	m.byID[id] = it
	return nil
}

func (m *intentStoreMem) PendingIntents() ([]store.Intent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []store.Intent
	for _, it := range m.byID {
		if it.Pending() {
			out = append(out, it)
		}
	}
	return out, nil
}

func (m *intentStoreMem) get(id string) (store.Intent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.byID[id]
	return it, ok
}

func TestPlaceOrderPersistsIntentBeforeBroker(t *testing.T) {
	b := &fakeBroker{}
	is := newIntentStoreMem()
	var events []event.Event
	o := New(b, func(e event.Event) error { events = append(events, e); return nil }, WithIntentStore(is))

	ord, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: d("1"), Price: d("50000"), ClientOrderID: "fase2a-1",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.ID == "" {
		t.Fatal("ordem não criada")
	}
	// O intent existe e foi avançado para 'submitted'.
	it, ok := is.get("fase2a-1")
	if !ok {
		t.Fatal("intent não persistido")
	}
	if it.Status != store.IntentSubmitted {
		t.Errorf("intent status = %q, esperava submitted", it.Status)
	}
	if it.Symbol != "BTCUSDT" || !it.Quantity.Equal(d("1")) || it.Side != domain.SideBuy {
		t.Errorf("intent com request errado: %+v", it)
	}
}

func TestPlaceOrderDoesNotSendWhenIntentSaveFails(t *testing.T) {
	// Fase 1 falha → a ordem NUNCA chega ao broker (sem ordem fantasma).
	b := &fakeBroker{}
	is := newIntentStoreMem()
	is.failSave = true
	o := New(b, func(event.Event) error { return nil }, WithIntentStore(is))

	_, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: d("1"), Price: d("50000"), ClientOrderID: "fase2a-fail",
	})
	if err == nil {
		t.Fatal("falha de intent deveria falhar o PlaceOrder")
	}
	if len(b.placed) != 0 {
		t.Errorf("ordem enviada ao broker mesmo com intent falho: %d envios", len(b.placed))
	}
	// A chave é liberada: o reenvio legítimo funciona.
	is.failSave = false
	if _, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: d("1"), Price: d("50000"), ClientOrderID: "fase2a-fail",
	}); err != nil {
		t.Fatalf("reenvio após liberar a trava falhou: %v", err)
	}
	if len(b.placed) != 1 {
		t.Errorf("reenvio legítimo deveria enviar 1 ordem, veio %d", len(b.placed))
	}
}

func TestPlaceOrderFailStopWhenOrderCreatedNotPersisted(t *testing.T) {
	// Fase 3 falha de persistência → PlaceOrder retorna erro (fail-parado),
	// e o intent fica 'pending' para o Reconcile adotar depois.
	b := &fakeBroker{}
	is := newIntentStoreMem()
	o := New(b, func(event.Event) error { return errors.New("sqlite falhou") }, WithIntentStore(is))

	_, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: d("1"), Price: d("50000"), ClientOrderID: "fase2a-pend",
	})
	if err == nil {
		t.Fatal("OrderCreated não persistido deveria falhar o PlaceOrder")
	}
	// O intent permanece 'pending' → a próxima reconciliação adota.
	it, _ := is.get("fase2a-pend")
	if it.Status != store.IntentPending {
		t.Errorf("intent status = %q, esperava pending (âncora da recuperação)", it.Status)
	}
}

func TestPlaceOrderSagaMarksIntentFailed(t *testing.T) {
	// Broker falha E confirma que a ordem não existe → intent 'failed'.
	b := &sagaBroker{failPlaceOrder: true}
	is := newIntentStoreMem()
	o := New(b, func(event.Event) error { return nil }, WithIntentStore(is))

	req := exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: d("1"), ClientOrderID: "fase2a-failed",
	}
	if _, err := o.PlaceOrder(context.Background(), req); err == nil {
		t.Fatal("falha real deveria ser reportada")
	}
	it, _ := is.get("fase2a-failed")
	if it.Status != store.IntentFailed {
		t.Errorf("intent status = %q, esperava failed", it.Status)
	}
}

func TestReconcileAdoptsOrphanViaIntentStore(t *testing.T) {
	// Crash window: intent 'pending' persistido + ordem existente na exchange
	// (sem OrderCreated no rastro) → Reconcile adota + emite + marca submitted.
	b := &recoverBroker{found: domain.Order{
		ID: "orphan-intent", ClientOrderID: "cli-orphan", Symbol: "BTCUSDT", Exchange: "binance",
		Side: domain.SideBuy, Type: domain.OrderLimit, Quantity: d("1"), Price: d("100"), Status: domain.OrderNew,
	}}
	is := newIntentStoreMem()
	var events []event.Event
	o := New(b, func(e event.Event) error { events = append(events, e); return nil }, WithIntentStore(is))

	// Simula o intent deixado pelo crash (nada em memória — só o disco).
	_ = is.SaveIntent(store.Intent{
		ClientOrderID: "cli-orphan", Symbol: "BTCUSDT", Side: domain.SideBuy,
		Type: domain.OrderLimit, Price: d("100"), Quantity: d("1"),
		Status: store.IntentPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	if err := o.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	found := false
	for _, ord := range o.Orders() {
		if ord.ID == "orphan-intent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ordem órfã não adotada pelo Reconcile via intent: %+v", o.Orders())
	}
	// OrderCreated emitido durante a adoção (o rastro estava sem ele).
	created := false
	for _, ev := range events {
		if ev.Type == event.OrderCreated {
			created = true
		}
	}
	if !created {
		t.Errorf("adoção deveria emitir OrderCreated, eventos: %+v", events)
	}
	// Intent avançado para 'submitted'.
	it, _ := is.get("cli-orphan")
	if it.Status != store.IntentSubmitted {
		t.Errorf("intent status = %q, esperava submitted", it.Status)
	}
}

func TestReconcileMarksIntentFailedWhenOrderAbsent(t *testing.T) {
	// Intent 'pending' cuja ordem NÃO existe na exchange → Reconcile marca
	// 'failed' (nenhuma adoção, nenhum evento).
	b := &recoverBroker{} // found vazio = não existe
	is := newIntentStoreMem()
	o := New(b, func(event.Event) error { return nil }, WithIntentStore(is))

	_ = is.SaveIntent(store.Intent{
		ClientOrderID: "cli-fantasma", Symbol: "BTCUSDT", Side: domain.SideBuy,
		Type: domain.OrderMarket, Quantity: d("1"),
		Status: store.IntentPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	if err := o.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	it, _ := is.get("cli-fantasma")
	if it.Status != store.IntentFailed {
		t.Errorf("intent status = %q, esperava failed", it.Status)
	}
	if len(o.Orders()) != 0 {
		t.Errorf("ordem inexistente não deveria ser adotada: %+v", o.Orders())
	}
}

func TestApplyOrderUpdateMarksIntentDone(t *testing.T) {
	// Ordem atinge estado terminal → intent 'done' (Reconcile para de varrer).
	b := &fakeBroker{}
	is := newIntentStoreMem()
	o := New(b, func(event.Event) error { return nil }, WithIntentStore(is))

	// Registra intent 'submitted' + ordem em memória.
	_ = is.SaveIntent(store.Intent{
		ClientOrderID: "cli-done", Symbol: "BTCUSDT", Side: domain.SideBuy,
		Type: domain.OrderMarket, Quantity: d("1"),
		Status: store.IntentSubmitted, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	o.ApplyOrderUpdate(domain.Order{
		ID: "o-done", ClientOrderID: "cli-done", Symbol: "BTCUSDT", Side: domain.SideBuy,
		Type: domain.OrderMarket, Quantity: d("1"), Status: domain.OrderFilled, FilledQty: d("1"),
	})

	it, _ := is.get("cli-done")
	if it.Status != store.IntentDone {
		t.Errorf("intent status = %q, esperava done após terminal", it.Status)
	}
}
