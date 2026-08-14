package risk

import (
	"errors"

	"github.com/shopspring/decimal"
)

// Erros do sizer.
var (
	ErrSizeInvalidParams = errors.New("sizing: parâmetros inválidos (equity/risco/entrada/stop devem ser positivos)")
	ErrSizeStopTooClose  = errors.New("sizing: distância entrada→stop é zero — não dá para dimensionar")
)

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
