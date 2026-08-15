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
// Price é o preço de referência (fechamento da vela); Stop é o stop-loss
// sugerido pela estratégia (opcional — usado pelo sizing por risco; zero =
// a estratégia não propõe stop e o motor usa o stop-loss global). Dinheiro
// em decimal.
//
// Fase Insight (padrão Lean: Insight = direção + magnitude + confiança):
// Magnitude e Confidence enriquecem o sinal com a força da convicção — o
// scanner/seletor adaptativo e o módulo de significância medem a QUALIDADE do
// alpha sobre esses campos, não só o acerto binário.
type Signal struct {
	Symbol string          `json:"symbol"`
	Side   string          `json:"side"` // "buy" | "sell"
	Type   string          `json:"type,omitempty"` // "market"|"limit"|"stop"; vazio = smart order
	Price  decimal.Decimal `json:"price"`
	Stop   decimal.Decimal `json:"stop,omitempty"`
	Reason string          `json:"reason"`
	// Magnitude é a variação % prevista (ex.: 0.03 = +3%). Zero = não declarada.
	Magnitude float64 `json:"magnitude,omitempty"`
	// Confidence é a convicção do sinal em [0,1]. Zero = não declarada.
	Confidence float64 `json:"confidence,omitempty"`
}

// SmartOrderType infere o tipo de ordem a partir do preço-alvo vs o preço
// corrente (lição do Jesse: "smart ordering"). Um sinal de COMPRA:
//   - preço-alvo == corrente  → market (entrar já)
//   - preço-alvo < corrente   → limit (comprar mais barato que o mercado)
//   - preço-alvo > corrente   → stop (comprar só se romper acima)
//
// E o espelho para VENDA. Retorna "market", "limit" ou "stop".
func SmartOrderType(side string, target, current decimal.Decimal) string {
	if target.IsZero() || current.IsZero() || target.Equal(current) {
		return "market"
	}
	if side == "buy" {
		if target.LessThan(current) {
			return "limit"
		}
		return "stop"
	}
	// sell
	if target.GreaterThan(current) {
		return "limit"
	}
	return "stop"
}
