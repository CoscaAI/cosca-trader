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

// Handler recebe dados de mercado normalizados de uma exchange.
type Handler struct {
	OnTick   func(domain.Tick)
	OnCandle func(domain.Candle)
	OnStatus func(Status)
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
