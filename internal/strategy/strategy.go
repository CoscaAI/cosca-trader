// Package strategy é o núcleo de estratégias do COSCA TRADER (Fase 3D — semente
// do F5): uma interface mínima que transforma velas fechadas em sinais. O
// motor de execução (OMS + paper broker) fica fora daqui — a estratégia é
// pura (preço → sinal), o que a torna testável e reutilizável no backtest.
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Strategy transforma uma vela fechada em sinais de operação. Mantém estado
// interno por símbolo (indisponível para a fronteira: nada de globals).
type Strategy interface {
	// Name identifica a estratégia ("ema-cross", "rsi-cross", ...).
	Name() string
	// OnCandle devolve os sinais gerados por uma vela fechada (pode ser vazio).
	OnCandle(c domain.Candle) []Signal
}

// Signal é um sinal de operação. Side é "buy" ou "sell" (direção da ordem);
// Price é o preço de referência (fechamento da vela). Dinheiro em decimal.
type Signal struct {
	Symbol string          `json:"symbol"`
	Side   string          `json:"side"` // "buy" | "sell"
	Price  decimal.Decimal `json:"price"`
	Reason string          `json:"reason"`
}
