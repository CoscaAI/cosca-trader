// Package event é o Event Model + Event Bus do COSCA TRADER — o sistema
// nervoso da plataforma. Todo acontecimento relevante (tick, candle, ordem,
// fill, posição, risco, estratégia) vira um Event tipado com correlation e
// causation. É a fundação da observabilidade ("rastreamento total") e da
// extensibilidade (plugins reagem a eventos, não a callbacks espalhados).
//
// Padrão herdado do cosca-node (event model com causation/severity) e do
// cosca-code (event bus tipado com pub/sub) — reaplicado ao domínio de trade.
package event

import "time"

// Type identifica o tipo de evento.
type Type string

const (
	// ── Market Data ───────────────────────────────────────────────
	MarketTick      Type = "market.tick"
	CandleClosed    Type = "market.candle_closed"
	OrderBookUpdate Type = "market.order_book_update"
	DepthSnapshot   Type = "market.depth_snapshot"

	// ── Conectividade (exchanges) ─────────────────────────────────
	ExchangeConnected    Type = "exchange.connected"
	ExchangeDisconnected Type = "exchange.disconnected"
	ExchangeError        Type = "exchange.error"

	// ── Ordens ────────────────────────────────────────────────────
	OrderCreated         Type = "order.created"
	OrderSubmitted       Type = "order.submitted"
	OrderPartiallyFilled Type = "order.partially_filled"
	OrderFilled          Type = "order.filled"
	OrderCanceled        Type = "order.canceled"
	OrderRejected        Type = "order.rejected"
	OrderExpired         Type = "order.expired"

	// ── Trades / Posições ─────────────────────────────────────────
	TradeExecuted   Type = "trade.executed"
	PositionOpened  Type = "position.opened"
	PositionUpdated Type = "position.updated"
	PositionClosed  Type = "position.closed"

	// ── Balanço / Risco ───────────────────────────────────────────
	BalanceUpdated Type = "balance.updated"
	RiskBreach     Type = "risk.breach"
	RiskWarning    Type = "risk.warning"

	// ── Estratégia ────────────────────────────────────────────────
	StrategyStarted Type = "strategy.started"
	StrategySignal  Type = "strategy.signal"
	StrategyStopped Type = "strategy.stopped"

	// ── Sistema ───────────────────────────────────────────────────
	JournalAppended Type = "journal.appended"
	SystemStarted   Type = "system.started"
	SystemStopped   Type = "system.stopped"
)

// Severity classifica a importância do evento.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityError    = "error"
	SeverityCritical = "critical"
)

// Event é a unidade do barramento. Source identifica o emissor (ex.:
// "exchange:binance", "oms", "risk", "strategy:rsi-cross"); CorrelationID
// agrupa eventos de uma mesma unidade de trabalho (uma ordem e seus fills);
// CausationID aponta para o evento que causou este (ordem → fill → posição).
type Event struct {
	ID            string    `json:"id"`
	Type          Type      `json:"type"`
	Timestamp     time.Time `json:"timestamp"`
	Source        string    `json:"source"`
	Payload       any       `json:"payload,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	CausationID   string    `json:"causation_id,omitempty"`
	Severity      string    `json:"severity"`
}
