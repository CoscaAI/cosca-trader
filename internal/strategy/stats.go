// stats.go — a camada estatística do COSCA TRADER (Fase 5): métricas
// científicas sobre resultados de estratégia. É isto que separa APOSTAR de
// INVESTIR COM VANTAGEM — não basta "deu lucro", precisa saber "com que
// probabilidade e com que risco".
//
// Design: métricas PURAS sobre []TradeResult (sem I/O). O dinheiro entra como
// decimal.Decimal (fronteira do backtest) e é convertido para float64 SÓ para
// a matemática estatística (desvio padrão, percentis) — nenhum valor monetário
// é calculado em float aqui, apenas estatística descritiva sobre a distribuição.
package strategy

import (
	"math"
	"sort"

	"github.com/shopspring/decimal"
)

// TradeResult é o resultado de UM trade fechado (round-trip).
type TradeResult struct {
	Symbol    string          `json:"symbol"`
	Side      string          `json:"side"` // "buy" | "sell"
	EntryPx   decimal.Decimal `json:"entry_px"`
	ExitPx    decimal.Decimal `json:"exit_px"`
	Quantity  decimal.Decimal `json:"quantity"`
	PnL       decimal.Decimal `json:"pnl"`         // líquido de fees
	FeesPaid  decimal.Decimal `json:"fees_paid"`
	HeldBars  int             `json:"held_bars"`   // nº de velas no trade
	ExitReason string         `json:"exit_reason"` // "signal" | "stop" | "take_profit" | "end"
	// EnterTag é a tag do SINAL de entrada (lição do Freqtrade: enter_tag/
	// exit_tag → análise de desempenho POR SINAL, não só agregado).
	EnterTag string `json:"enter_tag,omitempty"`
}

// EquityPoint é um ponto da curva de equity (após cada trade fechado).
type EquityPoint struct {
	Index int             `json:"index"`
	Equity decimal.Decimal `json:"equity"`
}

// Stats agrega as métricas científicas de um conjunto de trades.
type Stats struct {
	// Trades — contagem e resultado bruto.
	TotalTrades     int             `json:"total_trades"`
	Wins            int             `json:"wins"`
	Losses          int             `json:"losses"`
	Breakeven       int             `json:"breakeven"`
	WinRate         float64         `json:"win_rate"` // 0..1
	GrossProfit     decimal.Decimal `json:"gross_profit"`
	GrossLoss       decimal.Decimal `json:"gross_loss"`
	NetPnL          decimal.Decimal `json:"net_pnl"`
	ProfitFactor    float64         `json:"profit_factor"` // grossProfit/|grossLoss|; ∞ → 9999
	AvgWin          decimal.Decimal `json:"avg_win"`
	AvgLoss         decimal.Decimal `json:"avg_loss"`
	Expectancy      decimal.Decimal `json:"expectancy"` // PnL médio por trade
	FeesPaid        decimal.Decimal `json:"fees_paid"`

	// Retorno — sobre o equity inicial.
	ReturnPct       float64 `json:"return_pct"`  // netPnL/initial, 0..1
	MaxDrawdownPct  float64 `json:"max_drawdown_pct"` // pior queda do pico da equity curve
	MaxDrawdownAbs  decimal.Decimal `json:"max_drawdown_abs"`
	BestTrade       decimal.Decimal `json:"best_trade"`
	WorstTrade      decimal.Decimal `json:"worst_trade"`

	// Risco ajustado.
	Sharpe          float64 `json:"sharpe"`  // média(returns)/std(returns) × √252 (anualizado, assume 1 trade/dia)
	Sortino         float64 `json:"sortino"` // downside-only
	VolatilityPct   float64 `json:"volatility_pct"` // std dos retornos por trade

	// Distribuição — percentis do PnL por trade.
	P5              decimal.Decimal `json:"p5"`
	P25             decimal.Decimal `json:"p25"`
	P50             decimal.Decimal `json:"p50"`
	P75             decimal.Decimal `json:"p75"`
	P95             decimal.Decimal `json:"p95"`

	// PorSinal (lição do Freqtrade enter_tag): desempenho AGRUPADO por sinal
	// de entrada — revela quais sinais dão edge e quais só sangram.
	PorSinal []SignalStats `json:"por_sinal"`
}

// SignalStats é o desempenho de UM sinal (enter_tag) individual.
type SignalStats struct {
	Tag        string          `json:"tag"`
	Trades     int             `json:"trades"`
	Wins       int             `json:"wins"`
	WinRate    float64         `json:"win_rate"`
	NetPnL     decimal.Decimal `json:"net_pnl"`
	AvgPnL     decimal.Decimal `json:"avg_pnl"`
	PF         float64         `json:"pf"`
}

// computeStats calcula todas as métricas sobre os trades (ordenados por
// execução) e o equity inicial. tradeSeq deve estar em ordem cronológica.
func computeStats(trades []TradeResult, initial decimal.Decimal) Stats {
	s := Stats{TotalTrades: len(trades)}
	if len(trades) == 0 {
		return s
	}

	// Passada 1: agregação bruta + curve de equity.
	equity := initial
	pnls := make([]float64, 0, len(trades))
	rets := make([]float64, 0, len(trades)) // retorno por trade (PnL / equity anterior)
	equityCurve := make([]EquityPoint, 0, len(trades))
	peak := equity
	maxDD := decimal.Zero

	for i, t := range trades {
		// Retorno por trade = PnL sobre o capital ANTES do trade (o que foi
		// posto em risco). É esta série que o Sharpe/Sortino/Volatility medem
		// — não o PnL monetário absoluto, que cresce com o equity e não é
		// estacionário.
		if equity.IsPositive() {
			rets = append(rets, t.PnL.Div(equity).InexactFloat64())
		}
		equity = equity.Add(t.PnL)
		pnls = append(pnls, t.PnL.InexactFloat64())
		equityCurve = append(equityCurve, EquityPoint{Index: i, Equity: equity})

		if t.PnL.IsPositive() {
			s.Wins++
			s.GrossProfit = s.GrossProfit.Add(t.PnL)
		} else if t.PnL.IsNegative() {
			s.Losses++
			s.GrossLoss = s.GrossLoss.Add(t.PnL.Neg())
		} else {
			s.Breakeven++
		}
		s.NetPnL = s.NetPnL.Add(t.PnL)
		s.FeesPaid = s.FeesPaid.Add(t.FeesPaid)

		if equity.GreaterThan(peak) {
			peak = equity
		}
		if dd := peak.Sub(equity); dd.GreaterThan(maxDD) {
			maxDD = dd
		}
		if s.BestTrade.IsZero() || t.PnL.GreaterThan(s.BestTrade) {
			s.BestTrade = t.PnL
		}
		if s.WorstTrade.IsZero() || t.PnL.LessThan(s.WorstTrade) {
			s.WorstTrade = t.PnL
		}
	}

	// Win rate e expectância.
	if s.TotalTrades > 0 {
		s.WinRate = float64(s.Wins) / float64(s.TotalTrades)
		s.Expectancy = s.NetPnL.Div(decimal.NewFromInt(int64(s.TotalTrades)))
		if s.Wins > 0 {
			s.AvgWin = s.GrossProfit.Div(decimal.NewFromInt(int64(s.Wins)))
		}
		if s.Losses > 0 {
			s.AvgLoss = s.GrossLoss.Div(decimal.NewFromInt(int64(s.Losses)))
		}
	}

	// Profit factor (∞ protegido).
	if s.GrossLoss.IsPositive() {
		s.ProfitFactor = s.GrossProfit.Div(s.GrossLoss).InexactFloat64()
	} else if s.GrossProfit.IsPositive() {
		s.ProfitFactor = 9999 // sem perdas = PF infinito (representado)
	}

	// Retorno e drawdown.
	if initial.IsPositive() {
		s.ReturnPct = s.NetPnL.Div(initial).InexactFloat64()
	}
	s.MaxDrawdownAbs = maxDD
	if initial.IsPositive() {
		s.MaxDrawdownPct = maxDD.Div(initial).InexactFloat64()
	}

	// Estatística descritiva dos RETORNOS por trade (float apenas aqui). Sharpe
	// e Sortino são razões de retorno (adimensional) — calcular sobre PnL
	// monetário distorce a média/desvio (o PnL absoluto cresce com o equity).
	avg := mean(rets)
	std := stdDev(rets, avg)
	s.VolatilityPct = std * 100 // std dos retornos por trade, em %
	if std > 0 {
		s.Sharpe = (avg / std) * math.Sqrt(252)
		// Sortino: downside deviation dos retornos centrada em 0 (MAR), não na
		// média dos perdedores. dstd = √(Σ min(0, r)² / N).
		var downsideSq float64
		for _, r := range rets {
			d := math.Min(r, 0)
			downsideSq += d * d
		}
		dstd := math.Sqrt(downsideSq / float64(len(rets)))
		if dstd > 0 {
			s.Sortino = (avg / dstd) * math.Sqrt(252)
		}
	}

	// Percentis do PnL por trade.
	sorted := make([]float64, len(pnls))
	copy(sorted, pnls)
	sort.Float64s(sorted)
	s.P5 = decimal.NewFromFloat(percentile(sorted, 0.05))
	s.P25 = decimal.NewFromFloat(percentile(sorted, 0.25))
	s.P50 = decimal.NewFromFloat(percentile(sorted, 0.50))
	s.P75 = decimal.NewFromFloat(percentile(sorted, 0.75))
	s.P95 = decimal.NewFromFloat(percentile(sorted, 0.95))

	// Desempenho por sinal (enter_tag) — a lição do Freqtrade: quais sinais
	// dão edge e quais só sangram.
	s.PorSinal = computeBySignal(trades)

	return s
}

// computeBySignal agrupa os trades por enter_tag e calcula o desempenho de
// cada sinal. Revela sinais vencedores e perdedores individuais.
func computeBySignal(trades []TradeResult) []SignalStats {
	byTag := map[string]*SignalStats{}
	order := []string{}
	for _, t := range trades {
		tag := t.EnterTag
		if tag == "" {
			tag = "sem-tag"
		}
		st, ok := byTag[tag]
		if !ok {
			st = &SignalStats{Tag: tag}
			byTag[tag] = st
			order = append(order, tag)
		}
		st.Trades++
		st.NetPnL = st.NetPnL.Add(t.PnL)
		if t.PnL.IsPositive() {
			st.Wins++
		}
		if t.PnL.IsNegative() {
			// PF por sinal: soma das perdas.
			st.PF += t.PnL.Neg().InexactFloat64()
		}
	}
	out := make([]SignalStats, 0, len(order))
	for _, tag := range order {
		st := byTag[tag]
		if st.Trades > 0 {
			st.WinRate = float64(st.Wins) / float64(st.Trades)
			st.AvgPnL = st.NetPnL.Div(decimal.NewFromInt(int64(st.Trades)))
			// PF: gross profit / gross loss (guard para PF infinito).
			gross := st.NetPnL.Add(decimal.NewFromFloat(st.PF))
			if st.PF > 0 {
				st.PF = gross.Div(decimal.NewFromFloat(st.PF)).InexactFloat64()
			} else if gross.IsPositive() {
				st.PF = 9999
			}
		}
		out = append(out, *st)
	}
	// Ordena por PnL líquido decrescente (os melhores sinais primeiro).
	sort.Slice(out, func(i, j int) bool {
		return out[i].NetPnL.GreaterThan(out[j].NetPnL)
	})
	return out
}

// mean devolve a média aritmética de uma sequência.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// stdDev devolve o desvio padrão amostral (n-1).
func stdDev(xs []float64, m float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sq float64
	for _, x := range xs {
		d := x - m
		sq += d * d
	}
	return math.Sqrt(sq / float64(len(xs)-1))
}

// percentile devolve o valor no percentil p (0..1) via interpolação linear
// (método de Hyndman-Fan tipo 7, o default de numpy).
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}
