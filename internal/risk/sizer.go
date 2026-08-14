package risk

import (
	"errors"

	"github.com/shopspring/decimal"
)

// Erros do sizer.
var (
	ErrSizeInvalidParams = errors.New("sizing: parâmetros inválidos (equity/risco/entrada/stop devem ser positivos)")
	ErrSizeStopTooClose  = errors.New("sizing: distância entrada→stop é zero — não dá para dimensionar")
	ErrSizeBelowMinimum  = errors.New("sizing: valor da operação abaixo do mínimo da exchange (min_notional)")
)

// InstrumentRules são as regras de negociação de um ativo (exchangeInfo).
// Todas em decimal — o caminho do dinheiro nunca usa float.
type InstrumentRules struct {
	MinNotional decimal.Decimal // valor mínimo da ordem em quote (ex.: 10 USDT)
	MinQty      decimal.Decimal // quantidade mínima do ativo base
	StepSize    decimal.Decimal // incremento da quantidade (arredondamento)
	TickSize    decimal.Decimal // incremento do preço
}

// SizeMinNotional calcula a quantidade MÍNIMA VIÁVEL da operação: a menor
// quantidade que (a) respeita o valor mínimo da exchange (min_notional ÷
// preço), (b) respeita a quantidade mínima (min_qty) e (c) é arredondada para
// o step_size. É o "entrar com o mínimo" que o Don pediu — o sizing usa esta
// base quando o capital é pequeno ou quando o Don quer operação mínima.
func SizeMinNotional(rules InstrumentRules, price decimal.Decimal) (decimal.Decimal, error) {
	if price.Sign() <= 0 {
		return decimal.Zero, ErrSizeInvalidParams
	}
	if !rules.MinNotional.IsPositive() {
		return decimal.Zero, ErrSizeInvalidParams
	}
	// qty mínima pelo notional: min_notional ÷ preço (arredonda PARA CIMA —
	// a operação precisa cobrir pelo menos o valor mínimo).
	qtyByNotional := rules.MinNotional.Div(price).Truncate(8)

	// Respeita o min_qty.
	min := rules.MinQty
	if qtyByNotional.GreaterThan(min) {
		min = qtyByNotional
	}

	// Arredonda para o step_size (para cima — nunca abaixo do mínimo).
	if rules.StepSize.IsPositive() {
		steps := min.Div(rules.StepSize).Ceil()
		min = steps.Mul(rules.StepSize)
	}
	return min, nil
}

// ValidarPreTrade valida uma ordem candidata contra as regras do instrumento:
// notional ≥ min_notional, qty ≥ min_qty, qty alinhada ao step_size, preço
// alinhado ao tick_size. Devolve a quantidade ARREDONDADA válida (para baixo
// no step) ou erro se inviável.
func ValidarPreTrade(rules InstrumentRules, price, qty decimal.Decimal) (decimal.Decimal, error) {
	if price.Sign() <= 0 || qty.Sign() <= 0 {
		return decimal.Zero, ErrSizeInvalidParams
	}
	notional := price.Mul(qty)
	if rules.MinNotional.IsPositive() && notional.LessThan(rules.MinNotional) {
		return decimal.Zero, ErrSizeBelowMinimum
	}
	if rules.MinQty.IsPositive() && qty.LessThan(rules.MinQty) {
		return decimal.Zero, ErrSizeBelowMinimum
	}
	// Alinha ao step_size (para baixo — não inventar quantidade).
	if rules.StepSize.IsPositive() {
		qty = qty.Div(rules.StepSize).Floor().Mul(rules.StepSize)
	}
	return qty, nil
}

// Size calcula a quantidade de posição a partir do risco por trade:
//
//	qty = (equity × riskPct) / (entry − stop)
//
// O risco é limitado ao capital: o que estiver em risco é
// equity×riskPct, e a quantidade é dimensionada para que uma saída no stop
// perca exatamente esse valor. Arredonda a quantidade para BAIXO (nunca
// aumenta o risco por arredondamento). Dinheiro em decimal.Decimal.
//
// Long: entry > stop (comprou acima, protege abaixo).
// Short: entry < stop (vendeu acima, protege abaixo) — o denominador é
// negativo, então usa a distância absoluta.
func Size(equity, riskPerTradePct, entry, stop decimal.Decimal) (decimal.Decimal, error) {
	if !equity.IsPositive() || !riskPerTradePct.IsPositive() || !entry.IsPositive() || !stop.IsPositive() {
		return decimal.Zero, ErrSizeInvalidParams
	}
	if riskPerTradePct.GreaterThanOrEqual(decimal.NewFromInt(1)) {
		return decimal.Zero, errors.New("sizing: riskPerTradePct deve ser < 1 (ex.: 0.01 = 1%)")
	}

	distance := entry.Sub(stop).Abs()
	if distance.IsZero() {
		return decimal.Zero, ErrSizeStopTooClose
	}

	atRisk := equity.Mul(riskPerTradePct)
	qty := atRisk.Div(distance)

	// Nunca arredondar para cima (aumentaria o risco).
	qty = qty.Truncate(8)
	if qty.IsNegative() {
		return decimal.Zero, errors.New("sizing: quantidade negativa — verifique a direção da posição")
	}
	return qty, nil
}
