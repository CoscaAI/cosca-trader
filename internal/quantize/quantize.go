// Package quantize implementa arredondamento de preço/quantidade para as
// regras de precisão da exchange (padrão decimal_to_precision do ccxt). A
// distinção é crítica:
//
//   - QUANTIDADE → TRUNCATAR (nunca arredondar para cima: uma ordem maior que
//     o saldo é rejeitada pela exchange).
//   - PREÇO → ARREDONDAR ao tick (meio-afasta-de-zero: preço sempre válido).
//
// O "step" é o incremento mínimo: step_size (LOT_SIZE) para quantidade e
// tick_size (PRICE_FILTER) para preço. Dinheiro é decimal.Decimal — nunca
// float no caminho de liquidação.
package quantize

import "github.com/shopspring/decimal"

// RoundingMode escolhe a direção do arredondamento.
type RoundingMode int

const (
	// Truncate arredonda em direção a zero (chão para positivos) — o modo
	// seguro para QUANTIDADE (nunca excede o lote permitido).
	Truncate RoundingMode = iota
	// Round arredonda meio-afasta-de-zero — o modo para PREÇO (tick).
	Round
)

// ToStep quantiza n para o múltiplo mais próximo de step, conforme mode.
// Se step for zero/negativo (sem restrição), devolve n inalterado.
func ToStep(n, step decimal.Decimal, mode RoundingMode) decimal.Decimal {
	if step.Sign() <= 0 || n.IsZero() {
		return n
	}
	q := n.Div(step)
	switch mode {
	case Truncate:
		q = q.Truncate(0) // em direção a zero: chão para positivo, teto para negativo
	case Round:
		q = q.Round(0) // meio-afasta-de-zero
	}
	return q.Mul(step)
}

// TruncateToStep arredonda quantidade para baixo (múltiplo de step_size).
// É a forma canônica de nunca enviar ordem acima do lote permitido.
func TruncateToStep(quantity, stepSize decimal.Decimal) decimal.Decimal {
	return ToStep(quantity, stepSize, Truncate)
}

// RoundToTick arredonda preço para o tick_size (meio-afasta-de-zero).
func RoundToTick(price, tickSize decimal.Decimal) decimal.Decimal {
	return ToStep(price, tickSize, Round)
}
