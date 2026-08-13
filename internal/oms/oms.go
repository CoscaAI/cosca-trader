// Package oms é o Order Management System — o coração operacional do COSCA
// TRADER. Gerencia ordens (ciclo de vida), posições (acumuladas de fills) e
// saldos. Recebe pedidos do usuário, envia ao broker (exchange) e consolida o
// estado a partir dos eventos de execução. Toda mudança relevante vira um
// evento no rastro total (persistir-before-publish, via Emit).
//
// Corretude (doutrina Fintech): dinheiro em decimal.Decimal (nunca float),
// PnL realizado acumulado que sobrevive a fechar/reabrir, fees rastreados,
// fills idempotentes (dedup por Trade.ID), posições indexadas por
// símbolo+exchange.
package oms

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// Erros de validação do OMS.
var (
	ErrInvalidOrder  = errors.New("ordem inválida")
	ErrOrderNotFound = errors.New("ordem não encontrada")
)

// OMS é o motor de ordens/posições/saldos.
type OMS struct {
	broker exchange.Broker
	emit   func(event.Event)

	mu          sync.RWMutex
	orders      map[string]*domain.Order    // por ID da exchange
	clientOrder map[string]string           // clientOrderID → orderID (idempotência)
	positions   map[string]*domain.Position // por symbol:exchange
	balances    map[string]domain.Balance   // por ativo
	fees        map[string]decimal.Decimal  // taxas pagas, por ativo
	seenTrades  map[string]struct{}         // dedup de fills por Trade.ID
	replaying   bool                        // true durante Replay (suprime emissão)
}

// New cria o OMS sobre um broker, emitindo eventos via emit.
func New(broker exchange.Broker, emit func(event.Event)) *OMS {
	return &OMS{
		broker:      broker,
		emit:        emit,
		orders:      make(map[string]*domain.Order),
		clientOrder: make(map[string]string),
		positions:   make(map[string]*domain.Position),
		balances:    make(map[string]domain.Balance),
		fees:        make(map[string]decimal.Decimal),
		seenTrades:  make(map[string]struct{}),
	}
}

// PlaceOrder valida, envia à exchange, registra e emite OrderCreated.
// Idempotente por ClientOrderID: reenviar o mesmo client ID devolve a ordem
// existente em vez de criar uma nova (3 cliques ≠ 3 ordens).
func (o *OMS) PlaceOrder(ctx context.Context, req exchange.OrderRequest) (*domain.Order, error) {
	if err := validateOrder(req); err != nil {
		return nil, err
	}

	o.mu.Lock()
	if req.ClientOrderID != "" {
		if id, ok := o.clientOrder[req.ClientOrderID]; ok {
			ord := o.orders[id]
			o.mu.Unlock()
			return ord, nil
		}
	}
	o.mu.Unlock()

	ord, err := o.broker.PlaceOrder(ctx, req)
	if err != nil {
		return nil, err
	}

	o.mu.Lock()
	o.orders[ord.ID] = &ord
	if ord.ClientOrderID != "" {
		o.clientOrder[ord.ClientOrderID] = ord.ID
	}
	o.mu.Unlock()

	o.emitEvent(event.Event{Type: event.OrderCreated, Source: "oms", Payload: ord})
	return &ord, nil
}

// CancelOrder cancela uma ordem ativa e emite OrderCanceled.
func (o *OMS) CancelOrder(ctx context.Context, symbol, orderID string) error {
	if err := o.broker.CancelOrder(ctx, symbol, orderID); err != nil {
		return err
	}
	o.emitEvent(event.Event{
		Type:    event.OrderCanceled,
		Source:  "oms",
		Payload: map[string]any{"symbol": symbol, "order_id": orderID},
	})
	return nil
}

// ApplyOrderUpdate consolida uma mudança de estado de ordem (do user stream).
func (o *OMS) ApplyOrderUpdate(ord domain.Order) {
	o.mu.Lock()
	if old, ok := o.orders[ord.ID]; ok {
		ord = mergeOrder(*old, ord)
	}
	o.orders[ord.ID] = &ord
	if ord.ClientOrderID != "" {
		o.clientOrder[ord.ClientOrderID] = ord.ID
	}
	o.mu.Unlock()

	t := event.OrderSubmitted
	sev := event.SeverityInfo
	switch ord.Status {
	case domain.OrderFilled:
		t = event.OrderFilled
	case domain.OrderPartiallyFilled:
		t = event.OrderPartiallyFilled
	case domain.OrderCanceled:
		t = event.OrderCanceled
	case domain.OrderRejected:
		t = event.OrderRejected
		sev = event.SeverityWarning
	}
	o.emitEvent(event.Event{Type: t, Source: "oms", Severity: sev, Payload: ord})
}

// ApplyTrade consolida um fill (idempotente por Trade.ID): rastreia fee,
// atualiza a posição e emite TradeExecuted + Position* com o estado monetário.
func (o *OMS) ApplyTrade(t domain.Trade) {
	o.mu.Lock()

	// Idempotência: um fill já aplicado não pode ser aplicado de novo.
	if t.ID != "" {
		if _, seen := o.seenTrades[t.ID]; seen {
			o.mu.Unlock()
			return
		}
		o.seenTrades[t.ID] = struct{}{}
	}

	// Rastreia taxas por ativo (nunca ignorar fee).
	if t.Fee.Sign() > 0 {
		o.fees[t.FeeAsset] = o.fees[t.FeeAsset].Add(t.Fee)
	}

	key := posKey(t.Symbol, t.Exchange)
	pos := o.positions[key]
	if pos == nil {
		pos = &domain.Position{}
	}
	opened, closed := applyFill(pos, t)
	o.positions[key] = pos
	o.mu.Unlock()

	// Emite DEPOIS de mutar (persist-before-publish + estado consistente).
	o.emitEvent(event.Event{Type: event.TradeExecuted, Source: "oms", Payload: t})
	switch {
	case closed:
		o.emitEvent(event.Event{Type: event.PositionClosed, Source: "oms", Payload: *pos})
	case opened:
		o.emitEvent(event.Event{Type: event.PositionOpened, Source: "oms", Payload: *pos})
	default:
		o.emitEvent(event.Event{Type: event.PositionUpdated, Source: "oms", Payload: *pos})
	}
}

// ApplyBalance consolida um saldo.
func (o *OMS) ApplyBalance(b domain.Balance) {
	o.mu.Lock()
	o.balances[b.Asset] = b
	o.mu.Unlock()
	o.emitEvent(event.Event{Type: event.BalanceUpdated, Source: "oms", Payload: b})
}

// Orders devolve uma cópia das ordens conhecidas.
func (o *OMS) Orders() []domain.Order {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]domain.Order, 0, len(o.orders))
	for _, ord := range o.orders {
		out = append(out, *ord)
	}
	return out
}

// Positions devolve uma cópia das posições abertas.
func (o *OMS) Positions() []domain.Position {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]domain.Position, 0, len(o.positions))
	for _, p := range o.positions {
		if p.IsOpen() {
			out = append(out, *p)
		}
	}
	return out
}

// Position devolve a posição de um símbolo+exchange e se existe.
func (o *OMS) Position(symbol, exchange string) (domain.Position, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p, ok := o.positions[posKey(symbol, exchange)]
	if !ok {
		return domain.Position{}, false
	}
	return *p, true
}

// Balances devolve uma cópia dos saldos.
func (o *OMS) Balances() []domain.Balance {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]domain.Balance, 0, len(o.balances))
	for _, b := range o.balances {
		out = append(out, b)
	}
	return out
}

// Fees devolve o total de taxas pagas por ativo.
func (o *OMS) Fees() map[string]decimal.Decimal {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make(map[string]decimal.Decimal, len(o.fees))
	for a, f := range o.fees {
		out[a] = f
	}
	return out
}

// emitEvent emite um evento, suprimindo a emissão durante o Replay (os eventos
// já estão no rastro — reemitir duplicaria).
func (o *OMS) emitEvent(ev event.Event) {
	if o.replaying {
		return
	}
	o.emit(ev)
}

// Replay reconstrói o estado do OMS a partir do rastro persistido. Deve ser
// chamado ANTES de o OMS começar a receber eventos ao vivo (startup).
func (o *OMS) Replay(events []event.Event) {
	o.replaying = true
	defer func() { o.replaying = false }()

	for _, ev := range events {
		switch ev.Type {
		case event.OrderCreated, event.OrderSubmitted, event.OrderFilled,
			event.OrderPartiallyFilled, event.OrderCanceled, event.OrderRejected:
			var ord domain.Order
			if decodePayload(ev.Payload, &ord) {
				o.ApplyOrderUpdate(ord)
			}
		case event.TradeExecuted:
			var t domain.Trade
			if decodePayload(ev.Payload, &t) {
				o.ApplyTrade(t)
			}
		case event.BalanceUpdated:
			var b domain.Balance
			if decodePayload(ev.Payload, &b) {
				o.ApplyBalance(b)
			}
		}
	}
}

// Reconcile sincroniza o estado com a exchange via REST (Balances + OpenOrders
// dos símbolos conhecidos). Usado no startup e na reconexão do user stream.
func (o *OMS) Reconcile(ctx context.Context) error {
	if o.broker == nil {
		return nil
	}
	balances, err := o.broker.Balances(ctx)
	if err != nil {
		return err
	}
	for _, b := range balances {
		o.ApplyBalance(b)
	}
	for _, s := range o.symbols() {
		orders, err := o.broker.OpenOrders(ctx, s)
		if err != nil {
			continue
		}
		for _, ord := range orders {
			o.ApplyOrderUpdate(ord)
		}
	}
	return nil
}

// symbols devolve os símbolos distintos que o OMS conhece.
func (o *OMS) symbols() []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	set := make(map[string]struct{})
	for _, ord := range o.orders {
		set[ord.Symbol] = struct{}{}
	}
	for _, p := range o.positions {
		set[p.Symbol] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	return out
}

// decodePayload decodifica um payload (struct em memória ou json.RawMessage do
// disco) para o tipo alvo.
func decodePayload(payload any, target any) bool {
	var data []byte
	var err error
	switch p := payload.(type) {
	case json.RawMessage:
		data = p
	case []byte:
		data = p
	case nil:
		return false
	default:
		data, err = json.Marshal(payload)
		if err != nil {
			return false
		}
	}
	return json.Unmarshal(data, target) == nil
}

// posKey identifica uma posição por símbolo + exchange.
func posKey(symbol, exchange string) string {
	return exchange + ":" + symbol
}

// validateOrder aplica as regras mínimas antes de enviar à exchange.
func validateOrder(req exchange.OrderRequest) error {
	if req.Symbol == "" {
		return errors.Join(ErrInvalidOrder, errors.New("símbolo obrigatório"))
	}
	if req.Side != domain.SideBuy && req.Side != domain.SideSell {
		return errors.Join(ErrInvalidOrder, errors.New("lado inválido"))
	}
	if !req.Type.Valid() {
		return errors.Join(ErrInvalidOrder, errors.New("tipo de ordem inválido: "+string(req.Type)))
	}
	if req.TimeInForce != "" && !req.TimeInForce.Valid() {
		return errors.Join(ErrInvalidOrder, errors.New("time-in-force inválido: "+string(req.TimeInForce)))
	}
	if req.Quantity.Sign() <= 0 {
		return errors.Join(ErrInvalidOrder, errors.New("quantidade deve ser positiva"))
	}
	if req.Type == domain.OrderLimit && req.Price.Sign() <= 0 {
		return errors.Join(ErrInvalidOrder, errors.New("ordem limit exige preço positivo"))
	}
	return nil
}

// mergeOrder preserva campos que a atualização parcial não traz.
func mergeOrder(old, upd domain.Order) domain.Order {
	if upd.ClientOrderID == "" {
		upd.ClientOrderID = old.ClientOrderID
	}
	if upd.Symbol == "" {
		upd.Symbol = old.Symbol
	}
	if upd.Side == "" {
		upd.Side = old.Side
	}
	if upd.Type == "" {
		upd.Type = old.Type
	}
	if upd.CreatedAt.IsZero() {
		upd.CreatedAt = old.CreatedAt
	}
	return upd
}

// applyFill aplica um fill a uma posição. Devolve (opened, closed) para a
// emissão do evento correto. A lógica de aumento/redução/flip de posição é o
// coração do PnL — long soma em compra, short soma em venda, e a redução
// realiza PnL proporcional ao preço médio de entrada. O RealizedPnL acumulado
// é PRESERVADO ao fechar e reabrir (nunca zerado).
func applyFill(pos *domain.Position, t domain.Trade) (opened, closed bool) {
	if pos.Quantity.IsZero() {
		realized := pos.RealizedPnL // preserva PnL acumulado de ciclos anteriores
		*pos = domain.Position{
			Symbol:        t.Symbol,
			Exchange:      t.Exchange,
			Side:          t.Side,
			Quantity:      t.Quantity,
			AvgEntryPrice: t.Price,
			RealizedPnL:   realized,
			OpenedAt:      t.Timestamp,
			UpdatedAt:     t.Timestamp,
		}
		return true, false
	}

	if pos.Side == t.Side {
		// Aumenta a posição: média ponderada de entrada (decimal exato).
		newQty := pos.Quantity.Add(t.Quantity)
		pos.AvgEntryPrice = pos.AvgEntryPrice.Mul(pos.Quantity).
			Add(t.Price.Mul(t.Quantity)).Div(newQty)
		pos.Quantity = newQty
		pos.UpdatedAt = t.Timestamp
		return false, false
	}

	// Reduz/fecha/flipa posição: realiza PnL na parcela fechada.
	closeQty := decimal.Min(pos.Quantity, t.Quantity)
	var pnl decimal.Decimal
	if pos.Side == domain.SideBuy {
		pnl = t.Price.Sub(pos.AvgEntryPrice).Mul(closeQty)
	} else {
		pnl = pos.AvgEntryPrice.Sub(t.Price).Mul(closeQty)
	}
	pos.RealizedPnL = pos.RealizedPnL.Add(pnl)
	pos.Quantity = pos.Quantity.Sub(closeQty)
	pos.UpdatedAt = t.Timestamp

	if t.Quantity.GreaterThan(closeQty) {
		// Flip: o excesso abre a posição no lado oposto.
		excess := t.Quantity.Sub(closeQty)
		pos.Side = t.Side
		pos.Quantity = excess
		pos.AvgEntryPrice = t.Price
		pos.OpenedAt = t.Timestamp
		return true, false
	}
	if pos.Quantity.IsZero() {
		return false, true
	}
	return false, false
}
