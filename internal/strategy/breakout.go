// breakout.go — estratégia de BREAKOUT (Donchian, Fase 5): compra quando o
// preço fecha ACIMA da máxima de N velas; vende quando fecha ABAIXO da mínima
// de N velas. Captura rompimentos de range; o canal de Donchian também serve
// de stop natural (sair quando o preço reverte para o canal oposto).
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Parâmetros do breakout.
const (
	boChannel     = 20   // canal de Donchian (máx/mín de N velas)
	boExitChannel = 10   // canal curto para saída (trailing do canal)
	boATRPeriod   = 14
	boStopMult    = 1.0  // stop inicial = 1×ATR além do canal
)

// BreakoutState mantém o estado por símbolo.
type BreakoutState struct {
	highs []float64
	lows  []float64
	ranges []float64
	side  string
}

// Breakout é a estratégia de rompimento de canal.
type Breakout struct {
	state map[string]*BreakoutState
}

// NewBreakout cria a estratégia.
func NewBreakout() *Breakout {
	return &Breakout{state: make(map[string]*BreakoutState)}
}

// Name identifica a estratégia.
func (b *Breakout) Name() string { return "breakout" }

// OnCandle processa a vela e emite sinais.
func (b *Breakout) OnCandle(c domain.Candle) []Signal {
	if c.Close <= 0 {
		return nil
	}
	st := b.state[c.Symbol]
	if st == nil {
		st = &BreakoutState{}
		b.state[c.Symbol] = st
	}
	st.highs = append(st.highs, c.High)
	st.lows = append(st.lows, c.Low)
	// Mantém boChannel velas ANTERIORES + a vela atual (para comparar o
	// rompimento). Sem isso, o canal e a comparação colidem.
	if len(st.highs) > boChannel+1 {
		st.highs = st.highs[1:]
		st.lows = st.lows[1:]
	}
	st.ranges = append(st.ranges, c.High-c.Low)
	if len(st.ranges) > boATRPeriod {
		st.ranges = st.ranges[1:]
	}
	if len(st.highs) < boChannel+1 {
		return nil // precisa da vela atual + o canal anterior
	}

	price := decimal.NewFromFloat(c.Close)

	// Canal de Donchian das velas ANTERIORES (exclui a vela atual — senão o
	// close nunca supera o high da própria vela e o rompimento é impossível).
	maxHigh, minLow := 0.0, 1e18
	for i := 0; i < boChannel; i++ {
		if st.highs[i] > maxHigh {
			maxHigh = st.highs[i]
		}
		if st.lows[i] < minLow {
			minLow = st.lows[i]
		}
	}

	// Canal curto para saída (velas anteriores, excluindo a atual).
	shortHigh, shortLow := 0.0, 1e18
	start := len(st.highs) - 1 - boExitChannel
	if start < 0 {
		start = 0
	}
	for i := start; i < len(st.highs)-1; i++ {
		if st.highs[i] > shortHigh {
			shortHigh = st.highs[i]
		}
		if st.lows[i] < shortLow {
			shortLow = st.lows[i]
		}
	}

	// ATR para o stop.
	atr := 0.0
	if len(st.ranges) >= boATRPeriod {
		for _, rr := range st.ranges {
			atr += rr
		}
		atr /= float64(len(st.ranges))
	}

	var out []Signal
	// Compra no rompimento da máxima.
	if c.Close > maxHigh && st.side != "long" {
		st.side = "long"
		stop := decimal.NewFromFloat(minLow - atr*boStopMult)
		out = append(out, Signal{Symbol: c.Symbol, Side: "buy", Price: price, Stop: stop,
			Reason: "breakout long: fechamento acima da máxima de N velas"})
		// Sai do long quando o preço reverte abaixo do canal curto.
	} else if st.side == "long" && c.Close < shortLow {
		st.side = ""
		out = append(out, Signal{Symbol: c.Symbol, Side: "sell", Price: price,
			Reason: "breakout: reverteu abaixo do canal curto (trailing)"})
	}
	// Venda no rompimento da mínima.
	if c.Close < minLow && st.side != "short" {
		st.side = "short"
		stop := decimal.NewFromFloat(maxHigh + atr*boStopMult)
		out = append(out, Signal{Symbol: c.Symbol, Side: "sell", Price: price, Stop: stop,
			Reason: "breakout short: fechamento abaixo da mínima de N velas"})
		// Sai do short quando o preço reverte acima do canal curto.
	} else if st.side == "short" && c.Close > shortHigh {
		st.side = ""
		out = append(out, Signal{Symbol: c.Symbol, Side: "buy", Price: price,
			Reason: "breakout: reverteu acima do canal curto (trailing)"})
	}
	return out
}
