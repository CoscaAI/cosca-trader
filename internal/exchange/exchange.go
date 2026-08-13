// Package exchange define a camada de abstração de corretoras — o Provider
// Engine de exchanges (padrão do cosca-code aplicado a corretoras em vez de
// LLMs). Cada exchange (Binance, Coinbase, CoinEx, B3/MT5) é um adapter que
// implementa esta interface e entrega dados de mercado NORMALIZADOS em
// domain.Tick / domain.Candle. O núcleo nunca conhece o protocolo específico
// de cada corretora.
package exchange

import (
	"context"

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
type OrderRequest struct {
	Symbol        string
	Side          domain.Side
	Type          domain.OrderType
	Quantity      float64
	Price         float64
	StopPrice     float64
	TimeInForce   domain.TimeInForce
	ClientOrderID string
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
