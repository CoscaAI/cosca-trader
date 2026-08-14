// Package exchange define a camada de abstração de corretoras — o Provider
// Engine de exchanges (padrão do cosca-code aplicado a corretoras em vez de
// LLMs). Cada exchange (Binance, Coinbase, CoinEx, B3/MT5) é um adapter que
// implementa esta interface e entrega dados de mercado NORMALIZADOS em
// domain.Tick / domain.Candle. O núcleo nunca conhece o protocolo específico
// de cada corretora.
package exchange

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Status é uma notificação de estado da conexão com a exchange.
type Status struct {
	Exchange string `json:"exchange"`
	State    string `json:"state"` // connected | disconnected | error
	Message  string `json:"message,omitempty"`
}

// Handler recebe dados normalizados de uma exchange (mercado + conta).
type Handler struct {
	OnTick   func(domain.Tick)
	OnCandle func(domain.Candle)
	OnStatus func(Status)
	// F2 — conta/execução (user data stream)
	OnOrderUpdate   func(domain.Order)
	OnTrade         func(domain.Trade)
	OnBalanceUpdate func(domain.Balance)
}

// Exchange é a abstração de uma corretora. As implementações lidam com o
// protocolo (REST/WebSocket) e entregam tipos de domínio.
type Exchange interface {
	// Name devolve o identificador da corretora ("binance", "coinbase"...).
	Name() string

	// Connect abre a conexão e começa a entregar dados via Handler até o
	// contexto ser cancelado. Deve reconectar automaticamente com backoff.
	Connect(ctx context.Context, h Handler) error

	// SubscribeCandles assina candles (OHLCV) de um símbolo + intervalo.
	SubscribeCandles(symbol, interval string) error

	// SubscribeTrades assina o fluxo de trades (ticks) de um símbolo.
	SubscribeTrades(symbol string) error

	// Close encerra a conexão.
	Close() error
}

// OrderRequest é a ordem a ser enviada à exchange (payload de execução).
// As tags JSON alimentam o endpoint POST /orders (a API aceita client_order_id
// para idempotência — a chave nunca é opcional no caminho de liquidação).
type OrderRequest struct {
	Symbol        string          `json:"symbol"`
	Side          domain.Side     `json:"side"`
	Type          domain.OrderType `json:"type"`
	Quantity      decimal.Decimal `json:"quantity"`
	Price         decimal.Decimal `json:"price"`
	StopPrice     decimal.Decimal `json:"stop_price"`
	TimeInForce   domain.TimeInForce `json:"time_in_force,omitempty"`
	ClientOrderID string          `json:"client_order_id"`
}

// Broker é a interface de execução autenticada (ordens + conta). Separada da
// Exchange (market data) porque exige credenciais e opera sobre a conta do
// usuário — riscos diferentes, contratos diferentes.
type Broker interface {
	// PlaceOrder envia uma ordem e devolve o estado confirmado pela exchange.
	PlaceOrder(ctx context.Context, req OrderRequest) (domain.Order, error)
	// CancelOrder cancela uma ordem ativa.
	CancelOrder(ctx context.Context, symbol, orderID string) error
	// Balances devolve os saldos da conta.
	Balances(ctx context.Context) ([]domain.Balance, error)
	// OpenOrders devolve as ordens ativas de um símbolo.
	OpenOrders(ctx context.Context, symbol string) ([]domain.Order, error)
	// StartUserStream abre o stream de eventos da conta (ordens, fills, saldo)
	// até o contexto ser cancelado.
	StartUserStream(ctx context.Context, h Handler) error
}

// PriceProvider é uma interface OPCIONAL de brokers que conseguem devolver o
// preço corrente de um símbolo — usada pelo OMS para estimar o notional de
// ordens market (limite COSCA_TRADER_MAX_ORDER_USDT).
type PriceProvider interface {
	Price(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// OrderRecoverer é uma interface OPCIONAL de brokers que conseguem consultar
// uma ordem pelo clientOrderID (origClientOrderId na Binance). Usada pela saga
// de recuperação do OMS: quando PlaceOrder falha de forma ambígua (timeout,
// conexão perdida), o OMS pergunta ao broker se a ordem foi aceita — evitando
// reenvio (double trade) ou perda silenciosa.
type OrderRecoverer interface {
	// OrderByClientOrderID devolve a ordem do clientOrderID no símbolo (a API
	// da Binance exige o símbolo), ou uma ordem com ID vazio se ela não existir.
	// Erro = estado ambíguo (não confirmado).
	OrderByClientOrderID(ctx context.Context, symbol, clientOrderID string) (domain.Order, error)
}

// InstrumentRules são as regras de negociação de um símbolo (exchangeInfo) —
// a mesma estrutura que o sizer do risk usa. Declarada aqui para o OMS e o
// broker compartilharem sem acoplar ao pacote risk.
type InstrumentRules struct {
	MinNotional decimal.Decimal // valor mínimo da ordem em quote (ex.: 10 USDT)
	MinQty      decimal.Decimal // quantidade mínima do ativo base
	StepSize    decimal.Decimal // incremento da quantidade
	TickSize    decimal.Decimal // incremento do preço
}

// InstrumentInfoProvider é uma interface OPCIONAL de brokers que conhecem as
// regras de negociação dos instrumentos (exchangeInfo). O OMS usa para validar
// o MÍNIMO da operação (min_notional) além do máximo — o "entrar com o mínimo"
// que o Don pediu.
type InstrumentInfoProvider interface {
	// InstrumentRules devolve as regras do símbolo e se estão disponíveis.
	InstrumentRules(ctx context.Context, symbol string) (InstrumentRules, bool)
}
