// contrarian.go — estratégia CONTRARIAN completa (Fase 5): opera CONTRA o
// mercado nos DOIS lados, com três confirmações para maximizar a acertividade:
//
//	1. Banda de Bollinger (2σ) — o preço exagerou (evidência: reverteu 71%)
//	2. RSI extremo — confirma o exagero (sobrecomprado/sobrevendido)
//	3. Regime LATERAL — a reversão só vale onde não há tendência forte
//
// Long (compra contra a queda): close ≤ banda inferior E RSI ≤ 35 E regime
// lateral → compra; saída quando reverte à SMA ou RSI volta ao neutro.
// Short (venda contra a alta): close ≥ banda superior E RSI ≥ 65 E regime
// lateral → vende; saída na reversão. Este é o "operar contra o mercado" que
// o Don pediu: vender a euforia, comprar o pânico.
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Parâmetros do contrarian.
const (
	ctPeriod     = 20  // Bollinger
	ctStdMult    = 2.0
	ctRSIPeriod  = 14
	ctRSIOverB   = 65.0 // vender (overbought)
	ctRSIOverS   = 35.0 // comprar (oversold)
	ctSlopeWin   = 50
	ctATRPeriod  = 14
	ctStopMult   = 0.8
)

// CTState mantém o estado por símbolo.
type CTState struct {
	closes  []float64
	gains   []float64
	losses  []float64
	ranges  []float64
	rsi     float64
	side    string
}

// Contrarian é a estratégia bidirecional contra o mercado.
type Contrarian struct {
	state map[string]*CTState
	cfg   CTConfig
}

// CTConfig parametriza a contrarian (calibrável pelo otimizador).
type CTConfig struct {
	StopMult float64
	RSIOverB float64 // vender (overbought)
	RSIOverS float64 // comprar (oversold)
}

// NewContrarian cria a estratégia com a configuração default.
func NewContrarian() *Contrarian {
	return NewContrarianWith(CTConfig{StopMult: ctStopMult, RSIOverB: ctRSIOverB, RSIOverS: ctRSIOverS})
}

// NewContrarianWith cria a estratégia com configuração custom (calibração).
func NewContrarianWith(cfg CTConfig) *Contrarian {
	if cfg.StopMult <= 0 {
		cfg.StopMult = ctStopMult
	}
	if cfg.RSIOverB <= 0 {
		cfg.RSIOverB = ctRSIOverB
	}
	if cfg.RSIOverS <= 0 {
		cfg.RSIOverS = ctRSIOverS
	}
	return &Contrarian{state: make(map[string]*CTState), cfg: cfg}
}

// SetParam aplica um parâmetro pelo nome (hook do otimizador).
func (c *Contrarian) SetParam(name string, v float64) {
	switch name {
	case "stop_mult":
		c.cfg.StopMult = v
	case "rsi_overb":
		c.cfg.RSIOverB = v
	case "rsi_overs":
		c.cfg.RSIOverS = v
	}
}

// Name identifica a estratégia.
func (c *Contrarian) Name() string { return "contrarian" }

// OnCandle processa a vela e emite sinais nos dois lados.
func (c *Contrarian) OnCandle(v domain.Candle) []Signal {
	if v.Close <= 0 {
		return nil
	}
	st := c.state[v.Symbol]
	if st == nil {
		st = &CTState{}
		c.state[v.Symbol] = st
	}

	// RSI (Wilder smoothing).
	if len(st.closes) > 0 {
		chg := v.Close - st.closes[len(st.closes)-1]
		if chg >= 0 {
			st.gains = append(st.gains, chg)
			st.losses = append(st.losses, 0)
		} else {
			st.gains = append(st.gains, 0)
			st.losses = append(st.losses, -chg)
		}
		if len(st.gains) > ctRSIPeriod {
			st.gains = st.gains[1:]
			st.losses = st.losses[1:]
		}
	}
	st.closes = append(st.closes, v.Close)
	if len(st.closes) > ctSlopeWin+ctPeriod {
		st.closes = st.closes[1:]
	}
	st.ranges = append(st.ranges, v.High-v.Low)
	if len(st.ranges) > ctATRPeriod {
		st.ranges = st.ranges[1:]
	}
	if len(st.closes) < ctSlopeWin+1 {
		return nil
	}

	// RSI.
	if len(st.gains) >= ctRSIPeriod {
		ag, al := 0.0, 0.0
		for _, g := range st.gains {
			ag += g
		}
		for _, l := range st.losses {
			al += l
		}
		ag /= float64(ctRSIPeriod)
		al /= float64(ctRSIPeriod)
		if al == 0 {
			st.rsi = 100
		} else {
			rs := ag / al
			st.rsi = 100 - 100/(1+rs)
		}
	}

	price := decimal.NewFromFloat(v.Close)

	// Bollinger (últimas ctPeriod antes da atual).
	sum := 0.0
	for i := len(st.closes) - ctPeriod - 1; i < len(st.closes)-1; i++ {
		sum += st.closes[i]
	}
	sma := sum / ctPeriod
	var sq float64
	for i := len(st.closes) - ctPeriod - 1; i < len(st.closes)-1; i++ {
		d := st.closes[i] - sma
		sq += d * d
	}
	std := sqrtF(sq / ctPeriod)
	upper := sma + ctStdMult*std
	lower := sma - ctStdMult*std

	// Regime lateral: inclinação da SMA 50 pequena.
	half := ctSlopeWin / 2
	startIdx := len(st.closes) - ctSlopeWin
	sum1, sum2 := 0.0, 0.0
	for i := 0; i < half; i++ {
		sum1 += st.closes[startIdx+i]
	}
	for i := half; i < ctSlopeWin; i++ {
		sum2 += st.closes[startIdx+i]
	}
	slope := sum2/float64(half) - sum1/float64(half)
	lateral := slope < 0.02*sma && slope > -0.02*sma

	// ATR para o stop.
	atr := 0.0
	if len(st.ranges) >= ctATRPeriod {
		for _, rr := range st.ranges {
			atr += rr
		}
		atr /= float64(len(st.ranges))
	}

	var out []Signal
	if lateral {
		// COMPRA contra a queda: banda inferior + RSI sobrevendido.
		if v.Close <= lower && st.rsi <= c.cfg.RSIOverS && st.side != "long" {
			st.side = "long"
			stop := decimal.Zero
			if atr > 0 {
				stop = price.Sub(decimal.NewFromFloat(atr * c.cfg.StopMult))
			}
			out = append(out, Signal{Symbol: v.Symbol, Side: "buy", Price: price, Stop: stop,
				Reason: "contrarian long: banda inferior + RSI sobrevendido (comprando o pânico)"})
		}
		// VENDA contra a alta: banda superior + RSI sobrecomprado.
		if v.Close >= upper && st.rsi >= c.cfg.RSIOverB && st.side != "short" {
			st.side = "short"
			stop := decimal.Zero
			if atr > 0 {
				stop = price.Add(decimal.NewFromFloat(atr * c.cfg.StopMult))
			}
			out = append(out, Signal{Symbol: v.Symbol, Side: "sell", Price: price, Stop: stop,
				Reason: "contrarian short: banda superior + RSI sobrecomprado (vendendo a euforia)"})
		}
	}
	// Saídas: reverteu à SMA.
	if st.side == "long" && v.Close >= sma {
		st.side = ""
		out = append(out, Signal{Symbol: v.Symbol, Side: "sell", Price: price,
			Reason: "contrarian: reverteu à média — saída do long"})
	}
	if st.side == "short" && v.Close <= sma {
		st.side = ""
		out = append(out, Signal{Symbol: v.Symbol, Side: "buy", Price: price,
			Reason: "contrarian: reverteu à média — saída do short"})
	}
	return out
}
