// backtest.go — backtest MÍNIMO (Fase 3D, semente do F5): roda uma Strategy
// sobre candles históricos SEM broker real e devolve PnL final + número de
// trades. Long-only e all-in por simplicidade; fees aplicadas nos dois lados.
// É a base do F5 — aqui a régua é a corretude (decimal exato), não a riqueza
// de cenários.
package strategy

import (
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// BacktestResult é o resumo do backtest.
type BacktestResult struct {
	Initial decimal.Decimal `json:"initial"`
	Final   decimal.Decimal `json:"final"`
	PnL     decimal.Decimal `json:"pnl"`
	Trades  int             `json:"trades"`
	FeePaid decimal.Decimal `json:"fee_paid"`
}

// Backtest roda a estratégia sobre candles históricos. Regras (documentadas
// como semente do F5):
//   - long-only: "buy" abre posição com TODO o equity; "sell" fecha.
//   - preço de referência: o Price do sinal (fechamento da vela que o gerou).
//   - feePct aplicada sobre o notional nos dois lados; dinheiro sempre decimal.
//   - posição em aberto ao final é liquidada no último preço (PnL honesto).
func Backtest(s Strategy, candles []domain.Candle, initial, feePct decimal.Decimal) BacktestResult {
	equity := initial
	position := decimal.Zero
	entry := decimal.Zero
	trades := 0
	feePaid := decimal.Zero

	for _, c := range candles {
		for _, sig := range s.OnCandle(c) {
			price := sig.Price
			if price.Sign() <= 0 {
				price = decimal.NewFromFloat(c.Close)
			}
			switch sig.Side {
			case "buy":
				if position.IsZero() {
					position = equity.Div(price)
					entry = price
					fee := price.Mul(position).Mul(feePct)
					equity = equity.Sub(fee) // fee de entrada
					feePaid = feePaid.Add(fee)
				}
			case "sell":
				if position.Sign() > 0 {
					gross := price.Sub(entry).Mul(position)
					fee := price.Mul(position).Mul(feePct)
					equity = equity.Add(gross).Sub(fee) // realiza + fee de saída
					feePaid = feePaid.Add(fee)
					position = decimal.Zero
					trades++
				}
			}
		}
	}

	// Liquida posição remanescente no último preço disponível.
	if position.Sign() > 0 && len(candles) > 0 {
		last := decimal.NewFromFloat(candles[len(candles)-1].Close)
		gross := last.Sub(entry).Mul(position)
		fee := last.Mul(position).Mul(feePct)
		equity = equity.Add(gross).Sub(fee)
		feePaid = feePaid.Add(fee)
		trades++
	}

	return BacktestResult{
		Initial: initial,
		Final:   equity,
		PnL:     equity.Sub(initial),
		Trades:  trades,
		FeePaid: feePaid,
	}
}

// SyntheticCandles gera candles sintéticos determinísticos (random walk
// seedável, mesma família do DemoFeed do paper) para o backtest quando não há
// histórico persistido — demonstração reproduzível sem corretora.
func SyntheticCandles(seed int64, symbol string, n int, start float64) []domain.Candle {
	rng := rand.New(rand.NewSource(seed))
	price := start
	openTime := time.Now().Truncate(time.Minute).Add(-time.Duration(n) * time.Minute)
	out := make([]domain.Candle, 0, n)
	for i := 0; i < n; i++ {
		open := price
		// Um passo de random walk por vela (OHLC coerente em torno do close).
		close := open * (1 + rng.NormFloat64()*0.01)
		if close <= 0 {
			close = 1e-6
		}
		high := max(open, close) * (1 + rng.Float64()*0.003)
		low := min(open, close) * (1 - rng.Float64()*0.003)
		out = append(out, domain.Candle{
			Symbol:   symbol,
			Exchange: "synthetic",
			Interval: "1m",
			OpenTime: openTime.Add(time.Duration(i) * time.Minute),
			Open:     open,
			High:     high,
			Low:      low,
			Close:    close,
			Volume:   rng.Float64() * 100,
			Complete: true,
		})
		price = close
	}
	return out
}
