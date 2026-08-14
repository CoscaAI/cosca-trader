// momentum.go — estratégia de MOMENTUM com confirmação de tendência (Fase 5):
// compra quando o preço está ACIMA da média móvel de longo prazo E a média
// está SUBINDO (slope positivo); vende quando o preço está abaixo e a média
// caindo. O filtro duplo (preço + slope) reduz whipsaw em mercado lateral.
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Parâmetros do momentum.
const (
	momSlowPeriod = 50  // SMA de longo prazo (regime)
	momSlopeBars  = 10  // janela do slope (média está subindo/descendo?)
	momATRPeriod  = 20  // ATR para o stop sugerido
	momStopMult   = 1.5 // distância do stop = 1.5×ATR
)

// MomentumState mantém o estado por símbolo.
type MomentumState struct {
	closes     []float64 // fechamentos recentes (para SMA/slope)
	smaPrev    float64
	slope      float64
	side       string // "long" | "short" | ""
	signaled   string // última direção sinalizada
	ranges     []float64 // ranges (high-low) para ATR
}

// Momentum é a estratégia de momentum com confirmação de tendência.
type Momentum struct {
	state map[string]*MomentumState
}

// NewMomentum cria a estratégia.
func NewMomentum() *Momentum {
	return &Momentum{state: make(map[string]*MomentumState)}
}

// Name identifica a estratégia.
func (m *Momentum) Name() string { return "momentum" }

// OnCandle processa a vela e emite sinais.
func (m *Momentum) OnCandle(c domain.Candle) []Signal {
	if c.Close <= 0 {
		return nil
	}
	st := m.state[c.Symbol]
	if st == nil {
		st = &MomentumState{}
		m.state[c.Symbol] = st
	}

	// Acumula fechamentos e ranges.
	st.closes = append(st.closes, c.Close)
	if len(st.closes) > momSlowPeriod+momSlopeBars {
		st.closes = st.closes[1:]
	}
	st.ranges = append(st.ranges, c.High-c.Low)
	if len(st.ranges) > momATRPeriod {
		st.ranges = st.ranges[1:]
	}

	// Precisamos de dados suficientes.
	if len(st.closes) < momSlowPeriod+momSlopeBars {
		return nil
	}

	price := decimal.NewFromFloat(c.Close)

	// SMA de longo prazo (janela total).
	total := 0.0
	for _, cl := range st.closes {
		total += cl
	}
	smaNow := total / float64(len(st.closes))

	// Slope: SMA da primeira metade vs segunda metade da janela.
	half := len(st.closes) / 2
	sum1, sum2 := 0.0, 0.0
	for i := 0; i < half; i++ {
		sum1 += st.closes[i]
	}
	for i := half; i < len(st.closes); i++ {
		sum2 += st.closes[i]
	}
	avg1 := sum1 / float64(half)
	avg2 := sum2 / float64(len(st.closes)-half)
	st.slope = avg2 - avg1
	st.smaPrev = smaNow

	// ATR para o stop.
	atr := 0.0
	if len(st.ranges) >= momATRPeriod {
		for _, r := range st.ranges {
			atr += r
		}
		atr /= float64(len(st.ranges))
	}

	above := c.Close > smaNow
	slopeUp := st.slope > 0
	slopeDown := st.slope < 0

	// Direção de regime: long se preço acima + slope up; short se abaixo + slope down.
	wantLong := above && slopeUp
	wantShort := !above && slopeDown

	var out []Signal
	// Sinal só no FLIP de regime (evita spam a cada vela).
	if wantLong && st.side != "long" {
		st.side = "long"
		st.signaled = "long"
		stop := decimal.Zero
		if atr > 0 {
			stop = price.Sub(decimal.NewFromFloat(atr * momStopMult))
		}
		out = append(out, Signal{Symbol: c.Symbol, Side: "buy", Price: price, Stop: stop,
			Reason: "momentum long: preço acima da SMA e slope positivo"})
	} else if wantShort && st.side != "short" {
		st.side = "short"
		st.signaled = "short"
		stop := decimal.Zero
		if atr > 0 {
			stop = price.Add(decimal.NewFromFloat(atr * momStopMult))
		}
		out = append(out, Signal{Symbol: c.Symbol, Side: "sell", Price: price, Stop: stop,
			Reason: "momentum short: preço abaixo da SMA e slope negativo"})
	}
	return out
}
