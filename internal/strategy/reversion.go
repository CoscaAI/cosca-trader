// reversion.go — estratégia de MEAN-REVERSION via RSI com filtro de
// tendência (Fase 5): compra sobrevendido (RSI < 30) apenas quando o mercado
// NÃO está em queda forte (preço acima da SMA de longo prazo); vende
// sobrecomprado (RSI > 70) apenas quando não está em alta forte. O filtro
// evita "pegar faca caindo".
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Parâmetros do mean-reversion.
const (
	revRSIPeriod   = 14
	revOversold    = 30.0
	revOverbought  = 70.0
	revSMAPeriod   = 50
	revATRPeriod   = 14
	revStopMult    = 1.5
)

// ReversionState mantém o estado por símbolo.
type ReversionState struct {
	closes []float64
	gains  []float64
	losses []float64
	ranges []float64
	rsi    float64
	side   string
}

// Reversion é a estratégia mean-reversion com filtro de regime.
type Reversion struct {
	state map[string]*ReversionState
}

// NewReversion cria a estratégia.
func NewReversion() *Reversion {
	return &Reversion{state: make(map[string]*ReversionState)}
}

// Name identifica a estratégia.
func (r *Reversion) Name() string { return "rsi-reversion" }

// OnCandle processa a vela e emite sinais.
func (r *Reversion) OnCandle(c domain.Candle) []Signal {
	if c.Close <= 0 {
		return nil
	}
	st := r.state[c.Symbol]
	if st == nil {
		st = &ReversionState{}
		r.state[c.Symbol] = st
	}

	// RSI (Wilder smoothing simples).
	if len(st.closes) > 0 {
		chg := c.Close - st.closes[len(st.closes)-1]
		if chg >= 0 {
			st.gains = append(st.gains, chg)
			st.losses = append(st.losses, 0)
		} else {
			st.gains = append(st.gains, 0)
			st.losses = append(st.losses, -chg)
		}
		if len(st.gains) > revRSIPeriod {
			st.gains = st.gains[1:]
			st.losses = st.losses[1:]
		}
	}
	st.closes = append(st.closes, c.Close)
	if len(st.closes) > revSMAPeriod {
		st.closes = st.closes[1:]
	}
	st.ranges = append(st.ranges, c.High-c.Low)
	if len(st.ranges) > revATRPeriod {
		st.ranges = st.ranges[1:]
	}

	// RSI.
	if len(st.gains) >= revRSIPeriod {
		avgGain, avgLoss := 0.0, 0.0
		for _, g := range st.gains {
			avgGain += g
		}
		for _, l := range st.losses {
			avgLoss += l
		}
		avgGain /= float64(revRSIPeriod)
		avgLoss /= float64(revRSIPeriod)
		if avgLoss == 0 {
			st.rsi = 100
		} else {
			rs := avgGain / avgLoss
			st.rsi = 100 - 100/(1+rs)
		}
	}

	if len(st.closes) < revSMAPeriod || st.rsi == 0 {
		return nil
	}

	price := decimal.NewFromFloat(c.Close)

	// Filtro de tendência: SMA de longo prazo.
	total := 0.0
	for _, cl := range st.closes {
		total += cl
	}
	sma := total / float64(len(st.closes))

	// ATR para o stop.
	atr := 0.0
	if len(st.ranges) >= revATRPeriod {
		for _, rr := range st.ranges {
			atr += rr
		}
		atr /= float64(len(st.ranges))
	}

	var out []Signal
	// Compra: sobrevendido + mercado não em queda forte (preço ≥ SMA).
	if st.rsi < revOversold && c.Close >= sma && st.side != "long" {
		st.side = "long"
		stop := decimal.Zero
		if atr > 0 {
			stop = price.Sub(decimal.NewFromFloat(atr * revStopMult))
		}
		out = append(out, Signal{Symbol: c.Symbol, Side: "buy", Price: price, Stop: stop,
			Reason: "rsi-reversion long: RSI sobrevendido com tendência neutra/alta"})
		// Vende (fecha long) quando RSI volta à zona neutra.
	} else if st.side == "long" && st.rsi >= 50 {
		st.side = ""
		out = append(out, Signal{Symbol: c.Symbol, Side: "sell", Price: price,
			Reason: "rsi-reversion: saiu da zona de sobrevenda"})
	}
	// Venda: sobrecomprado + mercado não em alta forte (preço ≤ SMA).
	if st.rsi > revOverbought && c.Close <= sma && st.side != "short" {
		st.side = "short"
		stop := decimal.Zero
		if atr > 0 {
			stop = price.Add(decimal.NewFromFloat(atr * revStopMult))
		}
		out = append(out, Signal{Symbol: c.Symbol, Side: "sell", Price: price, Stop: stop,
			Reason: "rsi-reversion short: RSI sobrecomprado com tendência neutra/baixa"})
		// Compra de volta quando RSI volta à zona neutra.
	} else if st.side == "short" && st.rsi <= 50 {
		st.side = ""
		out = append(out, Signal{Symbol: c.Symbol, Side: "buy", Price: price,
			Reason: "rsi-reversion: saiu da zona de sobrecompra"})
	}
	return out
}
