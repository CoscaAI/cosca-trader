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
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/ledger"
	"github.com/CoscaAI/cosca-trader/internal/store"
)

// Erros de validação do OMS.
var (
	ErrInvalidOrder   = errors.New("ordem inválida")
	ErrOrderNotFound  = errors.New("ordem não encontrada")
	ErrDuplicateOrder = errors.New("client_order_id duplicado")
	ErrKillSwitch     = errors.New("kill switch ativo: novas ordens bloqueadas")
	ErrOrderTooLarge  = errors.New("ordem acima do limite de valor")
)

// IntentStore é o repositório durável de order intents (Fase 2A) — a âncora
// pré-broker que fecha a janela de crash. Implementada por *store.DB.
type IntentStore interface {
	SaveIntent(store.Intent) error
	UpdateIntentStatus(clientOrderID string, status store.IntentStatus) error
	PendingIntents() ([]store.Intent, error)
}

// OMS é o motor de ordens/posições/saldos.
type OMS struct {
	broker  exchange.Broker
	emit    func(event.Event) error
	intents IntentStore // nil = intent durável desativado (saga em memória apenas)

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
	stopLossPct  decimal.Decimal             // stop-loss automático por posição (P1-2)
	lastMarkEmit map[string]time.Time        // rate limit do PositionUpdated de mark (1x/s por posição)
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

// WithStopLossPct habilita o stop-loss automático (P1-2): quando uma posição
// abre, coloca uma ordem de proteção no lado oposto a pct% do preço médio de
// entrada (ex.: 0.05 = 5%). Zero desativa.
func WithStopLossPct(v decimal.Decimal) Option {
	return func(o *OMS) { o.stopLossPct = v }
}

// WithIntentStore liga o intent durável pré-broker (Fase 2A): cada ordem
// persiste um registro ANTES do envio à exchange, e o Reconcile adota ordens
// órfãs consultando a exchange por origClientOrderId — fechando 100% a janela
// de crash. Sem o store, a saga fica em memória (comportamento anterior).
func WithIntentStore(s IntentStore) Option {
	return func(o *OMS) { o.intents = s }
}

// New cria o OMS sobre um broker, emitindo eventos via emit. O emit devolve
// erro (ex.: engine.Emit) — para eventos de dinheiro, falha = fail-stop.
func New(broker exchange.Broker, emit func(event.Event) error, opts ...Option) *OMS {
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
		lastMarkEmit: make(map[string]time.Time),
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

// PlaceOrder valida, envia à exchange, registra e emite OrderCreated. Desde a
// Fase 2A é uma SAGA com 3 fases, fechando 100% a janela de crash:
//
//	a. FASE 1 (pré-broker): persiste o order intent com status 'pending'. Se a
//	   persistência falhar, NÃO envia à exchange (falha limpa, sem ordem
//	   fantasma nem chave órfã).
//	b. FASE 2 (broker): envia à exchange.
//	c. FASE 3 (pós-broker): sucesso → registra + emite OrderCreated + marca o
//	   intent 'submitted'. Erro → saga de recuperação (recoverAfterError), que
//	   adota se a ordem existir e marca 'failed' se confirmado que não.
//
// Fail-parado (Fase 2A): se o OrderCreated não persistir no rastro durável, o
// PlaceOrder RETORNA ERRO em vez de reportar sucesso — mas a ordem existe na
// exchange e o intent fica 'pending', então o Reconcile adota e reemite no
// próximo ciclo. O operador nunca é enganado por um "sucesso" não durável.
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

	// FASE 1 (pré-broker): intent durável — a âncora que fecha a janela de
	// crash. Se falhar, NÃO enviamos à exchange: falha limpa, sem ordem
	// fantasma.
	if o.intents != nil {
		if err := o.intents.SaveIntent(store.Intent{
			ClientOrderID: req.ClientOrderID,
			Symbol:        req.Symbol,
			Side:          req.Side,
			Type:          req.Type,
			Price:         req.Price,
			Quantity:      req.Quantity,
			Status:        store.IntentPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}); err != nil {
			o.mu.Lock()
			delete(o.pending, req.ClientOrderID)
			o.mu.Unlock()
			return nil, errors.Join(ErrInvalidOrder, fmt.Errorf("falha ao persistir intent %q — ordem NÃO enviada à exchange: %w", req.ClientOrderID, err))
		}
	}

	// FASE 2 (broker): envio à exchange.
	ord, err := o.broker.PlaceOrder(ctx, req)
	if err != nil {
		return o.recoverAfterError(ctx, req, err)
	}

	// FASE 3 (pós-broker): sucesso → adota + rastro + intent 'submitted'.
	o.registerOrder(req, ord)
	if err := o.emitEvent(event.Event{Type: event.OrderCreated, Source: "oms", Payload: ord}); err != nil {
		// Fail-parado: a ordem EXISTE na exchange, mas o rastro durável falhou.
		// Não reportamos sucesso; o intent permanece 'pending' e o Reconcile
		// adota + reemite o OrderCreated no próximo ciclo.
		return nil, errors.Join(err, fmt.Errorf("ordem %s aceita na exchange, mas OrderCreated não persistiu — intent %q fica 'pending' para o Reconcile adotar", ord.ID, req.ClientOrderID))
	}
	if err := o.markIntentStatus(req.ClientOrderID, store.IntentSubmitted); err != nil {
		log.Printf("⚠ oms: %v", err) // 'pending' só faz o Reconcile reconsultar — não é fatal
	}
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

// PlaceStopLoss coloca uma ordem de proteção (STOP) no lado oposto da posição,
// a pct% do preço médio de entrada. Ex.: long a 100 com pct 0.05 → SELL stop a
// 95. A ordem passa pelo fluxo completo de PlaceOrder (validação, notional,
// saga de recuperação, client_order_id obrigatório).
func (o *OMS) PlaceStopLoss(ctx context.Context, pos domain.Position, pct decimal.Decimal) (*domain.Order, error) {
	if o.KillSwitch() {
		return nil, errors.Join(ErrKillSwitch, errors.New("kill switch ativo: sem novas ordens (inclui stop-loss)"))
	}
	if pos.Quantity.Sign() <= 0 {
		return nil, errors.Join(ErrInvalidOrder, errors.New("posição sem quantidade para proteger"))
	}
	if pct.Sign() <= 0 {
		return nil, errors.Join(ErrInvalidOrder, errors.New("pct de stop-loss deve ser positivo"))
	}
	side := domain.SideSell
	if pos.Side == domain.SideSell {
		side = domain.SideBuy
	}
	return o.PlaceOrder(ctx, exchange.OrderRequest{
		Symbol:        pos.Symbol,
		Side:          side,
		Type:          domain.OrderStop, // STOP_LOSS (stop-market) na Binance spot
		Quantity:      pos.Quantity,
		StopPrice:     stopTrigger(pos, pct),
		ClientOrderID: "sl-" + uuid.NewString(),
	})
}

// stopTrigger calcula o preço gatilho do stop: abaixo da entrada em posição
// long, acima em posição short.
func stopTrigger(pos domain.Position, pct decimal.Decimal) decimal.Decimal {
	one := decimal.NewFromInt(1)
	if pos.Side == domain.SideSell {
		return pos.AvgEntryPrice.Mul(one.Add(pct))
	}
	return pos.AvgEntryPrice.Mul(one.Sub(pct))
}

// placeStopLossAsync dispara o stop-loss automático em segundo plano — a
// abertura de posição vem do user stream e não pode bloquear o handler.
func (o *OMS) placeStopLossAsync(pos domain.Position) {
	pct := o.stopLossPct
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := o.PlaceStopLoss(ctx, pos, pct); err != nil {
		log.Printf("⚠ oms: stop-loss automático de %s falhou: %v", pos.Symbol, err)
	}
}

// recoverAfterError é a saga de recuperação P0-2/P0-2A: após um erro do broker
// (timeout, conexão perdida), consulta a exchange pelo client_order_id para
// descobrir se a ordem foi aceita antes de reportar falha. Em Fase 2A, o
// resultado também avança o intent durável: adotada → 'submitted'; confirmada
// inexistente → 'failed'.
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
			if err := o.adoptRecovered(req, ord); err != nil {
				return nil, errors.Join(origErr, err)
			}
			return &ord, nil
		}
	}
	// 2. Fallback: varre ordens abertas dos símbolos conhecidos.
	ord, found, oerr := o.recoverFromOpenOrders(ctx, req)
	if oerr != nil {
		return nil, errors.Join(origErr, fmt.Errorf("estado ambíguo: client_order_id %q travado até reconciliação", req.ClientOrderID))
	}
	if found {
		if err := o.adoptRecovered(req, ord); err != nil {
			return nil, errors.Join(origErr, err)
		}
		return &ord, nil
	}
	// 3. Confirmado que a ordem NÃO existe → falha real; libera a trava e
	//    marca o intent 'failed' (só resta como histórico).
	o.mu.Lock()
	delete(o.pending, req.ClientOrderID)
	o.mu.Unlock()
	if err := o.markIntentStatus(req.ClientOrderID, store.IntentFailed); err != nil {
		log.Printf("⚠ oms: %v", err)
	}
	return nil, origErr
}

// adoptRecovered registra uma ordem que a saga descobriu na exchange (foi
// aceita mesmo com a resposta perdida), emite OrderCreated — o cliente recebe
// a ordem criada em vez de um erro falso — e avança o intent para 'submitted'.
// Devolve erro se o OrderCreated não persistir (fail-parado; o intent fica
// 'pending' e o Reconcile reemite depois).
func (o *OMS) adoptRecovered(req exchange.OrderRequest, ord domain.Order) error {
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
	if err := o.emitEvent(event.Event{Type: event.OrderCreated, Source: "oms", Payload: ord}); err != nil {
		return err
	}
	return o.markIntentStatus(req.ClientOrderID, store.IntentSubmitted)
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

// CancelOrder cancela uma ordem ativa e emite OrderCanceled. Falha-parado se
// o evento não persistir (o cancelamento foi executado, mas o rastro falhou —
// o operador é informado em vez de ver um sucesso não durável).
func (o *OMS) CancelOrder(ctx context.Context, symbol, orderID string) error {
	if err := o.broker.CancelOrder(ctx, symbol, orderID); err != nil {
		return err
	}
	if err := o.emitEvent(event.Event{
		Type:    event.OrderCanceled,
		Source:  "oms",
		Payload: map[string]any{"symbol": symbol, "order_id": orderID},
	}); err != nil {
		return err
	}
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
	case domain.OrderExpired:
		// P1-2: EXPIRED caía em OrderSubmitted (rótulo errado) — agora tem
		// evento próprio e severidade de atenção.
		t = event.OrderExpired
		sev = event.SeverityWarning
	}
	if err := o.emitEvent(event.Event{Type: t, Source: "oms", Severity: sev, Payload: ord}); err != nil {
		// Path de stream (user data) não pode falhar-parado no meio da
		// entrega — loga alto e segue; a reconciliação + intent recuperam o
		// estado durável no próximo ciclo.
		log.Printf("⚠ oms: persistência de %s falhou: %v", t, err)
	}

	// Fase 2A: estado terminal (filled/canceled/expired) → intent 'done',
	// para o Reconcile parar de varrer a chave.
	if ord.IsClosed() && ord.ClientOrderID != "" {
		if err := o.markIntentStatus(ord.ClientOrderID, store.IntentDone); err != nil {
			log.Printf("⚠ oms: %v", err)
		}
	}
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
	// Path de stream: falha de persistência loga alto (não pode travar o user
	// stream) — o intent/reconciliação recupera o estado durável depois.
	if err := o.emitEvent(event.Event{Type: event.TradeExecuted, Source: "oms", Payload: t}); err != nil {
		log.Printf("⚠ oms: persistência de TradeExecuted falhou: %v", err)
	}
	if closed {
		if err := o.emitEvent(event.Event{Type: event.PositionClosed, Source: "oms", Payload: *pos}); err != nil {
			log.Printf("⚠ oms: persistência de PositionClosed falhou: %v", err)
		}
	}
	if opened {
		if err := o.emitEvent(event.Event{Type: event.PositionOpened, Source: "oms", Payload: *pos}); err != nil {
			log.Printf("⚠ oms: persistência de PositionOpened falhou: %v", err)
		}
		// P1-2: stop-loss automático — protege a posição assim que ela abre
		// (nunca durante Replay, que não pode tocar na exchange).
		if o.stopLossPct.Sign() > 0 && !o.replaying {
			guard := *pos
			go o.placeStopLossAsync(guard)
		}
	}
	if !opened && !closed {
		if err := o.emitEvent(event.Event{Type: event.PositionUpdated, Source: "oms", Payload: *pos}); err != nil {
			log.Printf("⚠ oms: persistência de PositionUpdated falhou: %v", err)
		}
	}
}

// ApplyBalance consolida um saldo.
func (o *OMS) ApplyBalance(b domain.Balance) {
	o.mu.Lock()
	o.balances[b.Asset] = b
	o.mu.Unlock()
	if err := o.emitEvent(event.Event{Type: event.BalanceUpdated, Source: "oms", Payload: b}); err != nil {
		log.Printf("⚠ oms: persistência de BalanceUpdated falhou: %v", err)
	}
}

// ApplyMarkPrice atualiza o MarkPrice da posição (Fase 3A) a partir do market
// data — é o que torna o PnL NÃO realizado real na tela. O mark NUNCA é dinheiro
// de liquidação (é mercado), então: converte float→decimal na fronteira (quem
// alimenta), e falha de persistência loga e segue — nunca falha-parado num tick.
//
// Não emite a cada tick: há um rate limit interno de 1 PositionUpdated por
// segundo por posição (map de último emit), para não inundar o rastro sem
// valor. Mudança de preço abaixo da relevância (igual ao mark atual) também
// não emite.
func (o *OMS) ApplyMarkPrice(symbol, exchange string, mark decimal.Decimal) {
	if mark.Sign() <= 0 {
		return // preço inválido nunca toca o estado monetário
	}
	key := posKey(symbol, exchange)
	now := time.Now()

	o.mu.Lock()
	pos := o.positions[key]
	if pos == nil || !pos.IsOpen() {
		o.mu.Unlock()
		return
	}
	if pos.MarkPrice.Equal(mark) {
		o.mu.Unlock()
		return
	}
	// O mark SEMPRE atualiza (o /positions lê o estado, o PnL fica fresco);
	// a EMISSÃO é que é rate-limita a 1x/s por posição.
	pos.MarkPrice = mark
	pos.UpdatedAt = now
	if last, ok := o.lastMarkEmit[key]; ok && now.Sub(last) < time.Second {
		o.mu.Unlock()
		return
	}
	o.lastMarkEmit[key] = now
	snap := *pos
	o.mu.Unlock()

	if err := o.emitEvent(event.Event{Type: event.PositionUpdated, Source: "oms:mark", Payload: snap}); err != nil {
		log.Printf("⚠ oms: persistência de PositionUpdated (mark) falhou: %v", err)
	}
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
// já estão no rastro — reemitir duplicaria). O erro devolve o falha-parado do
// emit (engine.Emit) para eventos de dinheiro cuja persistência falhou.
func (o *OMS) emitEvent(ev event.Event) error {
	if o.replaying {
		return nil
	}
	return o.emit(ev)
}

// markIntentStatus avança o intent durável de um client_order_id. Sem intent
// store (modo em memória) é no-op.
func (o *OMS) markIntentStatus(clientOrderID string, status store.IntentStatus) error {
	if o.intents == nil || clientOrderID == "" {
		return nil
	}
	if err := o.intents.UpdateIntentStatus(clientOrderID, status); err != nil {
		return fmt.Errorf("atualizar intent %q para %q: %w", clientOrderID, status, err)
	}
	return nil
}

// orderKnown devolve se o OMS já conhece uma ordem pelo ID da exchange.
func (o *OMS) orderKnown(id string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	_, ok := o.orders[id]
	return ok
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
		if o.orderKnown(ord.ID) {
			continue
		}
		o.ApplyOrderUpdate(ord) // adota a ordem órfã
	}

	// Fase 2A: intents duráveis — fecha a janela de crash MESMO SEM símbolos
	// conhecidos. Todo intent 'pending'/'submitted' é consultado na exchange
	// por origClientOrderId: se existir, adota (register + OrderCreated se
	// ainda não emitido); se confirmadamente não existir, marca 'failed'.
	if o.intents != nil {
		intents, err := o.intents.PendingIntents()
		if err != nil {
			return fmt.Errorf("listar intents pendentes: %w", err)
		}
		for _, it := range intents {
			if it.ClientOrderID == "" {
				continue
			}
			ord, err := rec.OrderByClientOrderID(ctx, it.Symbol, it.ClientOrderID)
			if err != nil {
				continue // ambíguo — mantém para a próxima reconciliação
			}
			if ord.ID == "" {
				// Confirmado que a ordem não existe na exchange → intent 'failed'.
				if err := o.markIntentStatus(it.ClientOrderID, store.IntentFailed); err != nil {
					log.Printf("⚠ oms: %v", err)
				}
				continue
			}
			if !o.orderKnown(ord.ID) {
				// Órfã do crash window: adota + emite OrderCreated.
				if err := o.adoptRecovered(exchange.OrderRequest{ClientOrderID: it.ClientOrderID, Symbol: it.Symbol}, ord); err != nil {
					log.Printf("⚠ oms: adoção da ordem órfã %q falhou: %v", it.ClientOrderID, err)
					continue // intent continua 'pending' — tenta de novo
				}
			}
			if err := o.markIntentStatus(it.ClientOrderID, store.IntentSubmitted); err != nil {
				log.Printf("⚠ oms: %v", err)
			}
		}
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
	// P1-2: ordens stop EXIGEM stop_price (antes aceitavam e a Binance
	// rejeitava com erro enigmático).
	switch req.Type {
	case domain.OrderLimit:
		if req.Price.Sign() <= 0 {
			return errors.Join(ErrInvalidOrder, errors.New("ordem limit exige preço positivo"))
		}
	case domain.OrderStop, domain.OrderStopMarket:
		if req.StopPrice.Sign() <= 0 {
			return errors.Join(ErrInvalidOrder, errors.New("ordem stop exige stop_price positivo"))
		}
	case domain.OrderStopLimit:
		if req.StopPrice.Sign() <= 0 {
			return errors.Join(ErrInvalidOrder, errors.New("ordem stop_limit exige stop_price positivo"))
		}
		if req.Price.Sign() <= 0 {
			return errors.Join(ErrInvalidOrder, errors.New("ordem stop_limit exige preço positivo"))
		}
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
