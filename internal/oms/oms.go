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
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/ledger"
)

// Erros de validação do OMS.
var (
	ErrInvalidOrder   = errors.New("ordem inválida")
	ErrOrderNotFound  = errors.New("ordem não encontrada")
	ErrDuplicateOrder = errors.New("client_order_id duplicado")
	ErrKillSwitch     = errors.New("kill switch ativo: novas ordens bloqueadas")
	ErrOrderTooLarge  = errors.New("ordem acima do limite de valor")
)

// OMS é o motor de ordens/posições/saldos.
type OMS struct {
	broker exchange.Broker
	emit   func(event.Event)

	mu           sync.RWMutex
	orders       map[string]*domain.Order    // por ID da exchange
	clientOrder  map[string]string           // clientOrderID → orderID (idempotência)
	pending      map[string]string           // clientOrderID → symbol (estado ambíguo/saga P0-2)
	positions    map[string]*domain.Position // por symbol:exchange
	balances     map[string]domain.Balance   // por ativo
	ledger       *ledger.Ledger              // double-entry (fees + PnL)
	seenTrades   map[string]struct{}         // dedup de fills por Trade.ID
	replaying    bool                        // true durante Replay (suprime emissão)
	kill         bool                        // kill switch local (COSCA_TRADER_KILL=1)
	maxOrderUSDT decimal.Decimal             // limite de notional por ordem (P1-1)
}

// Option configura o OMS no New.
type Option func(*OMS)

// WithMaxOrderUSDT define o limite de valor (notional) por ordem. Zero/negativo
// desativa a trava. O default do OMS como biblioteca é permissivo (0); o
// default de PRODUÇÃO é 1000 USDT, aplicado na composição raiz (main.go lê
// COSCA_TRADER_MAX_ORDER_USDT). Assim o guardrail vive onde o dinheiro vive.
func WithMaxOrderUSDT(v decimal.Decimal) Option {
	return func(o *OMS) { o.maxOrderUSDT = v }
}

// New cria o OMS sobre um broker, emitindo eventos via emit.
func New(broker exchange.Broker, emit func(event.Event), opts ...Option) *OMS {
	o := &OMS{
		broker:       broker,
		emit:         emit,
		orders:       make(map[string]*domain.Order),
		clientOrder:  make(map[string]string),
		pending:      make(map[string]string),
		positions:    make(map[string]*domain.Position),
		balances:     make(map[string]domain.Balance),
		ledger:       ledger.New(),
		seenTrades:   make(map[string]struct{}),
		maxOrderUSDT: decimal.Zero, // permissivo como biblioteca; main.go impõe 1000
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// SetKillSwitch liga/desliga o kill switch local. Quando ativo, PlaceOrder
// (e PlaceStopLoss) recusam novas ordens. CancelOrder NÃO é bloqueado — em uma
// emergência o operador precisa conseguir reduzir a exposição, nunca ficar
// preso com a posição aberta.
func (o *OMS) SetKillSwitch(active bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.kill = active
}

// KillSwitch devolve o estado atual do kill switch.
func (o *OMS) KillSwitch() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.kill
}

// PlaceOrder valida, envia à exchange, registra e emite OrderCreated.
//
// P0-3: kill switch é verificado no topo — com COSCA_TRADER_KILL=1 nenhuma
// nova ordem sai para o mercado.
//
// P0-2 (anti double-trade):
//  1. A chave client_order_id é OBRIGATÓRIA — se o cliente não mandou, geramos
//     uma UUID. NUNCA se envia ordem à exchange sem a chave (âncora da
//     idempotência e da saga de recuperação).
//  2. Reenvio de uma ordem JÁ confirmada devolve a existente (at-most-once:
//     3 cliques ≠ 3 ordens). Reenvio de uma chave em estado AMBÍGUO (tentativa
//     anterior cujo resultado é desconhecido) é rejeitado com 409.
//  3. Se o broker falhar, a saga consulta pelo client_order_id para descobrir
//     se a ordem foi aceita — adota se foi, reporta falha real se confirmado,
//     e trava a chave se o estado seguir ambíguo.
func (o *OMS) PlaceOrder(ctx context.Context, req exchange.OrderRequest) (*domain.Order, error) {
	if o.KillSwitch() {
		return nil, errors.Join(ErrKillSwitch, errors.New("COSCA_TRADER_KILL=1 — nenhuma nova ordem até desativar"))
	}
	if err := validateOrder(req); err != nil {
		return nil, err
	}

	if req.ClientOrderID == "" {
		req.ClientOrderID = uuid.NewString()
	}

	o.mu.Lock()
	if id, ok := o.clientOrder[req.ClientOrderID]; ok {
		ord := o.orders[id]
		o.mu.Unlock()
		return ord, nil
	}
	if sym, ambiguous := o.pending[req.ClientOrderID]; ambiguous {
		o.mu.Unlock()
		return nil, errors.Join(ErrDuplicateOrder, fmt.Errorf("client_order_id %q em recuperação no símbolo %s — resolva o estado antes de reenviar", req.ClientOrderID, sym))
	}
	o.pending[req.ClientOrderID] = req.Symbol
	o.mu.Unlock()

	// P1-1: limite de valor por ordem (fail antes de chegar na exchange).
	if err := o.checkNotional(ctx, req); err != nil {
		o.mu.Lock()
		delete(o.pending, req.ClientOrderID)
		o.mu.Unlock()
		return nil, err
	}

	ord, err := o.broker.PlaceOrder(ctx, req)
	if err != nil {
		return o.recoverAfterError(ctx, req, err)
	}

	o.registerOrder(req, ord)
	o.emitEvent(event.Event{Type: event.OrderCreated, Source: "oms", Payload: ord})
	return &ord, nil
}

// checkNotional aplica o limite de valor por ordem (P1-1). Notional = preço ×
// quantidade; para ordens market sem preço, estima pelo preço corrente via
// PriceProvider ou usa a quantidade como proxy conservador. Dinheiro em
// decimal.Decimal — nunca float.
func (o *OMS) checkNotional(ctx context.Context, req exchange.OrderRequest) error {
	if o.maxOrderUSDT.Sign() <= 0 {
		return nil // trava desativada (0 = ilimitado)
	}
	notional := req.Quantity // proxy para ordens market sem preço
	if req.Price.Sign() > 0 {
		notional = req.Price.Mul(req.Quantity)
	} else if p, ok := o.broker.(exchange.PriceProvider); ok {
		if px, err := p.Price(ctx, req.Symbol); err == nil && px.Sign() > 0 {
			notional = px.Mul(req.Quantity)
		}
	}
	if notional.GreaterThan(o.maxOrderUSDT) {
		return errors.Join(ErrOrderTooLarge, fmt.Errorf("notional %s USDT excede o limite %s USDT por ordem (%s)", notional, o.maxOrderUSDT, req.Symbol))
	}
	return nil
}

// recoverAfterError é a saga de recuperação P0-2: após um erro do broker
// (timeout, conexão perdida), consulta a exchange pelo client_order_id para
// descobrir se a ordem foi aceita antes de reportar falha.
func (o *OMS) recoverAfterError(ctx context.Context, req exchange.OrderRequest, origErr error) (*domain.Order, error) {
	// 1. Broker com OrderRecoverer: consulta direta pelo origClientOrderId.
	if rec, ok := o.broker.(exchange.OrderRecoverer); ok {
		ord, rerr := rec.OrderByClientOrderID(ctx, req.Symbol, req.ClientOrderID)
		if rerr != nil {
			// Ambíguo: nem o envio nem a consulta confirmaram — trava a chave
			// para impedir double trade; Reconcile adota a ordem depois.
			return nil, errors.Join(origErr, fmt.Errorf("estado ambíguo: client_order_id %q travado até reconciliação", req.ClientOrderID))
		}
		if ord.ID != "" {
			o.adoptRecovered(req, ord)
			return &ord, nil
		}
	}
	// 2. Fallback: varre ordens abertas dos símbolos conhecidos.
	ord, found, oerr := o.recoverFromOpenOrders(ctx, req)
	if oerr != nil {
		return nil, errors.Join(origErr, fmt.Errorf("estado ambíguo: client_order_id %q travado até reconciliação", req.ClientOrderID))
	}
	if found {
		o.adoptRecovered(req, ord)
		return &ord, nil
	}
	// 3. Confirmado que a ordem NÃO existe → falha real; libera a trava.
	o.mu.Lock()
	delete(o.pending, req.ClientOrderID)
	o.mu.Unlock()
	return nil, origErr
}

// adoptRecovered registra uma ordem que a saga descobriu na exchange (foi
// aceita mesmo com a resposta perdida) e emite OrderCreated — o cliente recebe
// a ordem criada em vez de um erro falso.
func (o *OMS) adoptRecovered(req exchange.OrderRequest, ord domain.Order) {
	o.mu.Lock()
	if _, known := o.orders[ord.ID]; !known {
		o.orders[ord.ID] = &ord
	}
	clientID := ord.ClientOrderID
	if clientID == "" {
		clientID = req.ClientOrderID
	}
	if clientID != "" {
		o.clientOrder[clientID] = ord.ID
	}
	delete(o.pending, req.ClientOrderID)
	o.mu.Unlock()
	o.emitEvent(event.Event{Type: event.OrderCreated, Source: "oms", Payload: ord})
}

// recoverFromOpenOrders procura a ordem em ordens abertas dos símbolos
// conhecidos (fallback sem OrderRecoverer). Erro = consulta ambígua.
func (o *OMS) recoverFromOpenOrders(ctx context.Context, req exchange.OrderRequest) (domain.Order, bool, error) {
	for _, s := range o.symbols() {
		orders, err := o.broker.OpenOrders(ctx, s)
		if err != nil {
			return domain.Order{}, false, err
		}
		for _, ord := range orders {
			if ord.ClientOrderID == req.ClientOrderID {
				return ord, true, nil
			}
		}
	}
	return domain.Order{}, false, nil
}

// registerOrder grava a ordem confirmada nos índices do OMS (idempotência).
func (o *OMS) registerOrder(req exchange.OrderRequest, ord domain.Order) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.orders[ord.ID] = &ord
	clientID := ord.ClientOrderID
	if clientID == "" {
		clientID = req.ClientOrderID
	}
	if clientID != "" {
		o.clientOrder[clientID] = ord.ID
	}
	delete(o.pending, req.ClientOrderID)
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

	// Taxa em double-entry: débito na conta de fee (expense), crédito no ativo.
	if t.Fee.Sign() > 0 {
		o.ledger.Post(
			ledger.Entry{ID: t.ID + ":fee", Account: ledger.FeeAccount(t.FeeAsset), Debit: t.Fee, Ref: t.ID, Timestamp: t.Timestamp},
			ledger.Entry{ID: t.ID + ":asset", Account: ledger.AssetAccount(t.FeeAsset), Credit: t.Fee, Ref: t.ID, Timestamp: t.Timestamp},
		)
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
	if closed {
		o.emitEvent(event.Event{Type: event.PositionClosed, Source: "oms", Payload: *pos})
	}
	if opened {
		o.emitEvent(event.Event{Type: event.PositionOpened, Source: "oms", Payload: *pos})
	}
	if !opened && !closed {
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

// Fees devolve o total de taxas pagas por ativo (derivado do ledger).
func (o *OMS) Fees() map[string]decimal.Decimal {
	out := make(map[string]decimal.Decimal)
	for account, bal := range o.ledger.Balances() {
		if strings.HasPrefix(account, ledger.FeePrefix) {
			out[strings.TrimPrefix(account, ledger.FeePrefix)] = bal
		}
	}
	return out
}

// Ledger devolve o livro-razão double-entry do OMS.
func (o *OMS) Ledger() *ledger.Ledger {
	return o.ledger
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
//
// P0-2: além dos símbolos, consulta as ordens pelo origClientOrderId PERSISTIDO
// no journal (e pelas chaves ainda ambíguas) e adota qualquer ordem que exista
// na exchange mas não no rastro local — cobre o crash entre broker.PlaceOrder e
// a emissão de OrderCreated, e fills cujo estado terminal foi perdido.
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

	// Saga pós-crash: adota ordens órfãs consultando por origClientOrderId.
	rec, ok := o.broker.(exchange.OrderRecoverer)
	if !ok {
		return nil
	}
	for clientID, sym := range o.clientOrderIDs() {
		if clientID == "" {
			continue
		}
		ord, err := rec.OrderByClientOrderID(ctx, sym, clientID)
		if err != nil || ord.ID == "" {
			continue
		}
		o.mu.RLock()
		_, known := o.orders[ord.ID]
		o.mu.RUnlock()
		if known {
			continue
		}
		o.ApplyOrderUpdate(ord) // adota a ordem órfã
	}
	return nil
}

// clientOrderIDs devolve client_order_id → symbol das chaves conhecidas no
// journal e das ainda ambíguas (pending) — fonte da reconciliação por
// origClientOrderId.
func (o *OMS) clientOrderIDs() map[string]string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make(map[string]string, len(o.clientOrder)+len(o.pending))
	for clientID, orderID := range o.clientOrder {
		if ord, ok := o.orders[orderID]; ok {
			out[clientID] = ord.Symbol
		}
	}
	for clientID, sym := range o.pending {
		out[clientID] = sym
	}
	return out
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
		// Flip: o excesso abre a posição no lado oposto. O lado anterior foi
		// fechado (PnL realizado) E um novo foi aberto — sinaliza ambos.
		excess := t.Quantity.Sub(closeQty)
		pos.Side = t.Side
		pos.Quantity = excess
		pos.AvgEntryPrice = t.Price
		pos.OpenedAt = t.Timestamp
		return true, true
	}
	if pos.Quantity.IsZero() {
		return false, true
	}
	return false, false
}
