// Package oms é o Order Management System — o coração operacional do COSCA
// TRADER. Gerencia ordens (ciclo de vida), posições (acumuladas de fills) e
// saldos. Recebe pedidos do usuário, envia ao broker (exchange) e consolida o
// estado a partir dos eventos de execução. Toda mudança relevante vira um
// evento no rastro total (persistir-before-publish, via Emit).
package oms

import (
	"context"
	"errors"
	"sync"

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

	mu        sync.RWMutex
	orders    map[string]*domain.Order    // por ID da exchange
	positions map[string]*domain.Position // por símbolo
	balances  map[string]domain.Balance   // por ativo
}

// New cria o OMS sobre um broker, emitindo eventos via emit.
func New(broker exchange.Broker, emit func(event.Event)) *OMS {
	return &OMS{
		broker:    broker,
		emit:      emit,
		orders:    make(map[string]*domain.Order),
		positions: make(map[string]*domain.Position),
		balances:  make(map[string]domain.Balance),
	}
}

// PlaceOrder valida, envia à exchange, registra e emite OrderCreated.
func (o *OMS) PlaceOrder(ctx context.Context, req exchange.OrderRequest) (*domain.Order, error) {
	if err := validateOrder(req); err != nil {
		return nil, err
	}
	ord, err := o.broker.PlaceOrder(ctx, req)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	o.orders[ord.ID] = &ord
	o.mu.Unlock()

	o.emit(event.Event{
		Type:    event.OrderCreated,
		Source:  "oms",
		Payload: ord,
	})
	return &ord, nil
}

// CancelOrder cancela uma ordem ativa e emite OrderCanceled.
func (o *OMS) CancelOrder(ctx context.Context, symbol, orderID string) error {
	if err := o.broker.CancelOrder(ctx, symbol, orderID); err != nil {
		return err
	}
	o.emit(event.Event{
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
	o.emit(event.Event{Type: t, Source: "oms", Severity: sev, Payload: ord})
}

// ApplyTrade consolida um fill: atualiza a posição do símbolo e emite eventos.
func (o *OMS) ApplyTrade(t domain.Trade) {
	o.emit(event.Event{Type: event.TradeExecuted, Source: "oms", Payload: t})

	o.mu.Lock()
	pos := o.positions[t.Symbol]
	if pos == nil {
		pos = &domain.Position{}
	}
	opened, closed := applyFill(pos, t)
	o.positions[t.Symbol] = pos
	o.mu.Unlock()

	switch {
	case closed:
		o.emit(event.Event{Type: event.PositionClosed, Source: "oms", Payload: t.Symbol})
	case opened:
		o.emit(event.Event{Type: event.PositionOpened, Source: "oms", Payload: t.Symbol})
	default:
		o.emit(event.Event{Type: event.PositionUpdated, Source: "oms", Payload: t.Symbol})
	}
}

// ApplyBalance consolida um saldo.
func (o *OMS) ApplyBalance(b domain.Balance) {
	o.mu.Lock()
	o.balances[b.Asset] = b
	o.mu.Unlock()
	o.emit(event.Event{Type: event.BalanceUpdated, Source: "oms", Payload: b})
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
		if p.Quantity > 0 {
			out = append(out, *p)
		}
	}
	return out
}

// Position devolve a posição de um símbolo (aberta ou zerada) e se existe.
func (o *OMS) Position(symbol string) (domain.Position, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	p, ok := o.positions[symbol]
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

// validateOrder aplica as regras mínimas antes de enviar à exchange.
func validateOrder(req exchange.OrderRequest) error {
	if req.Symbol == "" {
		return errors.Join(ErrInvalidOrder, errors.New("símbolo obrigatório"))
	}
	if req.Side != domain.SideBuy && req.Side != domain.SideSell {
		return errors.Join(ErrInvalidOrder, errors.New("lado inválido"))
	}
	if req.Quantity <= 0 {
		return errors.Join(ErrInvalidOrder, errors.New("quantidade deve ser positiva"))
	}
	if req.Type == domain.OrderLimit && req.Price <= 0 {
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
// realiza PnL proporcional ao preço médio de entrada.
func applyFill(pos *domain.Position, t domain.Trade) (opened, closed bool) {
	if pos.Quantity == 0 {
		*pos = domain.Position{
			Symbol:        t.Symbol,
			Exchange:      t.Exchange,
			Side:          t.Side,
			Quantity:      t.Quantity,
			AvgEntryPrice: t.Price,
			OpenedAt:      t.Timestamp,
			UpdatedAt:     t.Timestamp,
		}
		return true, false
	}

	if pos.Side == t.Side {
		// Aumenta a posição: média ponderada de entrada.
		newQty := pos.Quantity + t.Quantity
		pos.AvgEntryPrice = (pos.AvgEntryPrice*pos.Quantity + t.Price*t.Quantity) / newQty
		pos.Quantity = newQty
		pos.UpdatedAt = t.Timestamp
		return false, false
	}

	// Reduz/fecha/flipa posição: realiza PnL na parcela fechada.
	closeQty := pos.Quantity
	if t.Quantity < closeQty {
		closeQty = t.Quantity
	}
	if pos.Side == domain.SideBuy {
		pos.RealizedPnL += (t.Price - pos.AvgEntryPrice) * closeQty
	} else {
		pos.RealizedPnL += (pos.AvgEntryPrice - t.Price) * closeQty
	}
	pos.Quantity -= closeQty
	pos.UpdatedAt = t.Timestamp

	if t.Quantity > closeQty {
		// Flip: o excesso abre a posição no lado oposto.
		excess := t.Quantity - closeQty
		pos.Side = t.Side
		pos.Quantity = excess
		pos.AvgEntryPrice = t.Price
		pos.OpenedAt = t.Timestamp
		return true, false
	}
	if pos.Quantity == 0 {
		return false, true
	}
	return false, false
}
