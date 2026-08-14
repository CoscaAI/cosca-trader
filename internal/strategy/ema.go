// ema.go — estratégia demonstrativa de cruzamento de médias exponenciais
// (Fase 3D, semente do F5): EMA rápida (9) × EMA lenta (21) sobre o preço de
// fechamento das velas. Compra quando a rápida cruza ACIMA da lenta; vende
// quando cruza ABAIXO. É baseada em preço (nunca no tempo): basta receber as
// velas em sequência — o estado é mantido por símbolo.
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Parâmetros default do cruzamento (rápida/lenta).
const (
	emaFastPeriod = 9
	emaSlowPeriod = 21
)

// EMACross é a estratégia de cruzamento de médias exponenciais.
type EMACross struct {
	fast  int
	slow  int
	state map[string]*emaState // estado por símbolo
}

// emaState guarda as EMAs e o lado corrente (fast acima/abaixo da slow) por
// símbolo. O lado é rastreado desde a PRIMEIRA vela — assim um cruzamento que
// aconteça durante o warm-up não se perde: quando o warm-up termina, o lado
// atual já está gravado e só os flips a partir daí sinalizam.
type emaState struct {
	fastEMA    decimal.Decimal
	slowEMA    decimal.Decimal
	candles    int
	fastAbove  bool   // fast acima da slow (lado atual)
	lastSignal string // última direção sinalizada — evita repetir o mesmo lado
	atrSum     decimal.Decimal // soma dos ranges (para o stop sugerido, F4B)
}

// NewEMACross cria a estratégia com os períodos default (9/21).
func NewEMACross() *EMACross {
	return &EMACross{fast: emaFastPeriod, slow: emaSlowPeriod, state: make(map[string]*emaState)}
}

// Name identifica a estratégia.
func (e *EMACross) Name() string { return "ema-cross" }

// OnCandle avança as EMAs com a vela fechada e emite sinal no flip de lado.
func (e *EMACross) OnCandle(c domain.Candle) []Signal {
	if c.Close <= 0 {
		return nil
	}
	st := e.state[c.Symbol]
	if st == nil {
		st = &emaState{}
		e.state[c.Symbol] = st
	}
	price := decimal.NewFromFloat(c.Close)
	if st.candles == 0 {
		// Primeira vela: semeia as EMAs com o próprio preço (as EMAs "nascem"
		// no preço corrente e convergem à medida que as velas chegam).
		st.fastEMA = price
		st.slowEMA = price
	}
	st.candles++
	st.fastEMA = ema(st.fastEMA, price, e.fast)
	st.slowEMA = ema(st.slowEMA, price, e.slow)
	// F4B — acumula o range da vela (high−low) para o stop sugerido (ATR simples).
	st.atrSum = st.atrSum.Add(decimal.NewFromFloat(c.High - c.Low))
	fastAbove := st.fastEMA.GreaterThan(st.slowEMA)

	// Warm-up: as EMAs ainda estão "nascendo" — só grava o lado, não sinaliza.
	if st.candles <= e.slow {
		st.fastAbove = fastAbove
		return nil
	}

	// Flip de lado: fast cruza acima → buy; abaixo → sell.
	if fastAbove == st.fastAbove {
		return nil // sem cruzamento
	}
	st.fastAbove = fastAbove
	side := "sell"
	reason := "EMA rápida cruzou abaixo da lenta"
	if fastAbove {
		side = "buy"
		reason = "EMA rápida cruzou acima da lenta"
	}
	if st.lastSignal == side {
		return nil
	}
	st.lastSignal = side

	// F4B — stop sugerido: distância de 1.5× o range médio das velas (ATR
	// simples). Em buy, o stop fica ABAIXO da entrada; em sell, ACIMA.
	atr := st.atrSum.Div(decimal.NewFromInt(int64(st.candles)))
	stopDist := atr.Mul(decimal.NewFromFloat(1.5))
	stop := price.Sub(stopDist)
	if side == "sell" {
		stop = price.Add(stopDist)
	}
	return []Signal{{Symbol: c.Symbol, Side: side, Price: price, Stop: stop, Reason: reason}}
}

// ema calcula o valor exponencial: k = 2/(periodo+1); ema' = preço×k + ema×(1-k).
func ema(prev, price decimal.Decimal, period int) decimal.Decimal {
	k := decimal.NewFromInt(2).Div(decimal.NewFromInt(int64(period) + 1))
	return price.Mul(k).Add(prev.Mul(decimal.NewFromInt(1).Sub(k)))
}
