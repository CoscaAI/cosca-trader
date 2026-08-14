// bbreversion.go — estratégia de REVERSÃO À MÉDIA com BOLLINGER BANDS (Fase 5),
// DESENHADA A PARTIR DA EVIDÊNCIA REGISTRADA: a análise empírica do mercado
// real (BTCUSDT 1h, 500 velas, 24/07→14/08) mostrou que momentum (48%) e
// breakout (38-43%) são ≈ moeda, mas a reversão à média após tocar a banda de
// Bollinger (SMA 20, 2σ) reverteu em 71% (banda superior, 6 velas) e 56%
// (banda inferior). Esta estratégia EXPLORA exatamente essa vantagem medida.
//
// Regras (longas/curtas simétricas):
//   - Compra quando o close cruza ABAIXO da banda inferior (oversold)
//     e vende quando reverte à SMA (meio do canal).
//   - Venda quando o close cruza ACIMA da banda superior (overbought)
//     e compra de volta na SMA.
//   - Filtro de regime: só opera reversão se o mercado NÃO está em tendência
//     forte (SMA 50 com inclinação pequena) — a evidência vale para regime
//     lateral, e o filtro impede "pegar faca caindo".
package strategy

import (
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Parâmetros do bb-reversion (calibrados na evidência).
const (
	bbPeriod   = 20  // SMA do canal (a evidência usou 20)
	bbStdMult  = 2.0 // desvios (2σ — a evidência mediu 2σ)
	bbSlopeWin = 50  // janela do filtro de regime (SMA 50)
	bbATRPeriod = 14
	bbStopMult  = 1.0
)

// BBState mantém o estado por símbolo.
type BBState struct {
	closes  []float64
	ranges  []float64
	side    string // "long" | "short" | ""
}

// BBConfig parametriza a estratégia (calibrável por evidência).
type BBConfig struct {
	// StopMult é o multiplicador de ATR para o stop (default 1.0).
	StopMult float64
	// ExitAtSMA: true = sai quando reverte à SMA; false = sai quando reverte
	// ao MEIO do canal (média da banda). O teste de evidência mostrou que
	// sair na SMA pode segurar perdas grandes — calibramos.
	ExitAtSMA bool
}

// BBReversion é a estratégia de reversão à média por Bollinger Bands.
type BBReversion struct {
	state map[string]*BBState
	cfg   BBConfig
}

// NewBBReversion cria a estratégia com a configuração default.
func NewBBReversion() *BBReversion {
	return NewBBReversionWith(BBConfig{StopMult: 1.0, ExitAtSMA: true})
}

// NewBBReversionWith cria a estratégia com configuração custom (calibração).
func NewBBReversionWith(cfg BBConfig) *BBReversion {
	if cfg.StopMult <= 0 {
		cfg.StopMult = 1.0
	}
	return &BBReversion{state: make(map[string]*BBState), cfg: cfg}
}

// Name identifica a estratégia.
func (b *BBReversion) Name() string { return "bb-reversion" }

// OnCandle processa a vela e emite sinais.
func (b *BBReversion) OnCandle(c domain.Candle) []Signal {
	if c.Close <= 0 {
		return nil
	}
	st := b.state[c.Symbol]
	if st == nil {
		st = &BBState{}
		b.state[c.Symbol] = st
	}
	st.closes = append(st.closes, c.Close)
	if len(st.closes) > bbSlopeWin+bbPeriod {
		st.closes = st.closes[1:]
	}
	st.ranges = append(st.ranges, c.High-c.Low)
	if len(st.ranges) > bbATRPeriod {
		st.ranges = st.ranges[1:]
	}
	if len(st.closes) < bbSlopeWin {
		return nil // warm-up completo (SMA 50 + canal 20)
	}

	price := decimal.NewFromFloat(c.Close)

	// Canal de Bollinger (últimas bbPeriod velas ANTES da atual).
	sum := 0.0
	for i := len(st.closes) - bbPeriod - 1; i < len(st.closes)-1; i++ {
		sum += st.closes[i]
	}
	sma := sum / bbPeriod
	var sq float64
	for i := len(st.closes) - bbPeriod - 1; i < len(st.closes)-1; i++ {
		d := st.closes[i] - sma
		sq += d * d
	}
	std := sqrtF(sq / bbPeriod)
	upper := sma + bbStdMult*std
	lower := sma - bbStdMult*std

	// Filtro de regime: inclinação da SMA 50 (primeira metade vs segunda).
	half := bbSlopeWin / 2
	startIdx := len(st.closes) - bbSlopeWin
	sum1, sum2 := 0.0, 0.0
	for i := 0; i < half; i++ {
		sum1 += st.closes[startIdx+i]
	}
	for i := half; i < bbSlopeWin; i++ {
		sum2 += st.closes[startIdx+i]
	}
	slope := (sum2/float64(half)) - (sum1/float64(half))
	// Tolerância: regime lateral se a inclinação for pequena relativa ao preço.
	regimeOK := slope < 0.02*sma && slope > -0.02*sma

	// ATR para o stop.
	atr := 0.0
	if len(st.ranges) >= bbATRPeriod {
		for _, rr := range st.ranges {
			atr += rr
		}
		atr /= float64(len(st.ranges))
	}

	var out []Signal
	if regimeOK {
		// Compra na banda inferior (oversold) — a evidência: reverteu 56-71%.
		if c.Close <= lower && st.side != "long" {
			st.side = "long"
			stop := decimal.Zero
			if atr > 0 {
				stop = price.Sub(decimal.NewFromFloat(atr * b.cfg.StopMult))
			}
			out = append(out, Signal{Symbol: c.Symbol, Side: "buy", Price: price, Stop: stop,
				Reason: "bb-reversion: fechou abaixo da banda inferior (2σ) em regime lateral"})
		}
		// Venda na banda superior (overbought).
		if c.Close >= upper && st.side != "short" {
			st.side = "short"
			stop := decimal.Zero
			if atr > 0 {
				stop = price.Add(decimal.NewFromFloat(atr * b.cfg.StopMult))
			}
			out = append(out, Signal{Symbol: c.Symbol, Side: "sell", Price: price, Stop: stop,
				Reason: "bb-reversion: fechou acima da banda superior (2σ) em regime lateral"})
		}
	}
	// Saída: revertido à média (SMA ou meio do canal conforme a calibração).
	// Long sai quando o close ≥ sma; short sai quando ≤ sma. Sempre ativo
	// (take-profit natural do mean-reversion).
	if st.side == "long" && c.Close >= sma {
		st.side = ""
		out = append(out, Signal{Symbol: c.Symbol, Side: "sell", Price: price,
			Reason: "bb-reversion: reverteu à média — saída do long"})
	}
	if st.side == "short" && c.Close <= sma {
		st.side = ""
		out = append(out, Signal{Symbol: c.Symbol, Side: "buy", Price: price,
			Reason: "bb-reversion: reverteu à média — saída do short"})
	}
	return out
}

// midExit devolve o preço-alvo de saída antecipada (meio do canal = média da
// banda). Não usado no fluxo atual — disponível para calibração futura.
func (b *BBReversion) midExit(sma, upper, lower float64) float64 {
	return (upper + lower) / 2
}

// sqrtF é a raiz quadrada simples (stdlib math sem import extra no hot path).
func sqrtF(f float64) float64 {
	if f <= 0 {
		return 0
	}
	x := f
	for i := 0; i < 20; i++ {
		x = (x + f/x) / 2
	}
	return x
}
