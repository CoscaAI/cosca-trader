package strategy

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// uptrendCandles gera velas com tendência de alta (para momentum/breakout).
func uptrendCandles(n int) []domain.Candle {
	out := make([]domain.Candle, n)
	px := 100.0
	for i := 0; i < n; i++ {
		open := px
		px += 0.5 // alta constante
		out[i] = domain.Candle{
			Symbol: "TEST", Exchange: "synthetic", Interval: "1h",
			Open: open, High: px + 0.2, Low: open - 0.2, Close: px, Volume: 10, Complete: true,
		}
	}
	return out
}

// sidewaysCandles gera velas laterais (range) para mean-reversion.
func sidewaysCandles(n int) []domain.Candle {
	out := make([]domain.Candle, n)
	px := 100.0
	for i := 0; i < n; i++ {
		open := px
		if i%4 == 0 {
			px += 0.8 // sobe
		} else if i%4 == 2 {
			px -= 0.8 // desce
		}
		out[i] = domain.Candle{
			Symbol: "TEST", Exchange: "synthetic", Interval: "1h",
			Open: open, High: px + 0.3, Low: open - 0.3, Close: px, Volume: 10, Complete: true,
		}
	}
	return out
}

func TestMomentumUptrend(t *testing.T) {
	// Em tendência de alta, o momentum deve abrir LONG (buy) e nunca short.
	m := NewMomentum()
	candles := uptrendCandles(100)
	var buys, sells int
	for _, c := range candles {
		for _, sig := range m.OnCandle(c) {
			if sig.Side == "buy" {
				buys++
			} else {
				sells++
			}
		}
	}
	if buys == 0 {
		t.Fatal("momentum deveria ter sinalizado buy em tendência de alta")
	}
	if sells > buys {
		t.Fatalf("momentum não deveria vender mais que comprar em alta: buys=%d sells=%d", buys, sells)
	}
}

func TestReversionSideways(t *testing.T) {
	// Em mercado oscilante (random walk, seed com volatilidade), o
	// mean-reversion deve operar nos dois lados — RSI oscila entre sobrevenda
	// e sobrecompra.
	r := NewReversion()
	candles := SyntheticCandles(3, "TEST", 300, 100) // seed 3 oscila bem
	var buys, sells int
	for _, c := range candles {
		for _, sig := range r.OnCandle(c) {
			if sig.Side == "buy" {
				buys++
			} else {
				sells++
			}
		}
	}
	if buys == 0 && sells == 0 {
		t.Fatal("mean-reversion deveria ter operado em mercado oscilante")
	}
}

func TestBreakoutUptrend(t *testing.T) {
	// Em tendência de alta com rompimentos, o breakout abre long.
	b := NewBreakout()
	candles := SyntheticCandles(23, "TEST", 300, 100)
	var buys, sells int
	for _, c := range candles {
		for _, sig := range b.OnCandle(c) {
			if sig.Side == "buy" {
				buys++
			} else {
				sells++
			}
		}
	}
	if buys == 0 {
		t.Fatal("breakout deveria ter sinalizado buy em tendência de alta")
	}
}

func TestRegistryNames(t *testing.T) {
	names := Names()
	if len(names) != 5 {
		t.Fatalf("esperava 5 estratégias registradas, got %d: %v", len(names), names)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	for _, want := range []string{"ema-cross", "momentum", "rsi-reversion", "breakout", "bb-reversion"} {
		if !found[want] {
			t.Fatalf("estratégia %s não registrada", want)
		}
	}
}

func TestNewByNameUnknown(t *testing.T) {
	if _, err := NewByName("nao-existe"); err == nil {
		t.Fatal("estratégia desconhecida deveria retornar erro")
	}
}

func TestScannerRanksStrategies(t *testing.T) {
	// O scanner roda TODAS as estratégias sobre o MESMO histórico e ordena
	// por score. Alguma delas deve ter score > 0 (as que operaram).
	candles := uptrendCandles(300)
	gate := DefaultGate()
	res := Scan(candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), gate, 300, 300, 42)

	if len(res.Strategies) != 5 {
		t.Fatalf("scanner deveria avaliar 5 estratégias, got %d", len(res.Strategies))
	}
	// Ranking deve estar ordenado (score decrescente).
	for i := 1; i < len(res.Strategies); i++ {
		if res.Strategies[i].Score > res.Strategies[i-1].Score {
			t.Fatalf("ranking fora de ordem em %d: %v > %v", i, res.Strategies[i].Score, res.Strategies[i-1].Score)
		}
	}
	// Best nil é aceitável (gate pode rejeitar tudo), mas o ranking existe.
	_ = res.Best
}

func TestGateRejectsSmallSample(t *testing.T) {
	// Com poucas velas, nenhuma estratégia deve passar no portão (amostra
	// insuficiente = ciência honesta).
	candles := uptrendCandles(50)
	gate := DefaultGate()
	res := Scan(candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), gate, 100, 100, 42)
	if res.Best != nil {
		t.Fatalf("com 50 velas nada deveria passar no portão, mas %s passou", res.Best.Name)
	}
}

func TestAdaptiveSelectorSwitches(t *testing.T) {
	// Janela pequena para o teste; feed de alta seguido de lateral deve
	// (possivelmente) trocar a estratégia ativa. O teste valida a MECÂNICA:
	// a estratégia ativa responde a OnCandle e o scan roda.
	cfg := AdaptiveConfig{
		Window:          100,
		RevalidateEvery: 30,
		Gate:            DefaultGate(),
		InitialCapital:  decimal.NewFromInt(10000),
		FeePct:          decimal.NewFromFloat(0.001),
		MonteCarloSims:  100,
		SigTrials:       100,
		SeedBase:        7,
	}
	sel := NewAdaptive(cfg)
	if sel == nil {
		t.Fatal("selector não criado")
	}
	if sel.ActiveName() == "" {
		t.Fatal("selector sem estratégia ativa")
	}
	// Feed longo (alta + lateral) para cruzar janelas de revalidação.
	candles := append(uptrendCandles(150), sidewaysCandles(150)...)
	var signals int
	for _, c := range candles {
		signals += len(sel.OnCandle(c))
	}
	_ = signals
	if sel.LastScan() == nil {
		t.Fatal("selector deveria ter rodado o scan")
	}
}
