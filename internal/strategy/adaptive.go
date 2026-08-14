// adaptive.go — o SELETOR ADAPTATIVO (Fase 5): a resposta ao Don sobre "o
// mercado muda o tempo todo, como validar o que muda?". O AdaptiveSelector
// mantém uma janela deslizante de velas, reavalia as estratégias a cada N
// velas (revalidation window) e TROCA a estratégia ativa quando outra passa
// no portão com score melhor. É o "kernel do kernel" das estratégias: opera
// a melhor do momento, revalidando continuamente.
package strategy

import (
	"sync"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// AdaptiveConfig parametriza o seletor.
type AdaptiveConfig struct {
	// Window é o tamanho da janela deslizante de velas para avaliação
	// (ex.: 500 velas de 1h ≈ 3 semanas).
	Window int
	// RevalidateEvery é de quantas em quantas velas reavaliar (ex.: 50 →
	// a cada 50 velas novas, re-roda o scan). 0 = reavaliar em toda vela.
	RevalidateEvery int
	// Gate é o portão científico (DefaultGate se zero).
	Gate GateConfig
	// InitialCapital e FeePct do backtest de avaliação.
	InitialCapital decimal.Decimal
	FeePct         decimal.Decimal
	// MonteCarloSims e SigTrials da avaliação (menores para velocidade no
	// live; ex.: 500/500).
	MonteCarloSims int
	SigTrials      int
	// SeedBase para reprodutibilidade.
	SeedBase int64
}

// AdaptiveSelector troca a estratégia ativa conforme o mercado.
type AdaptiveSelector struct {
	cfg AdaptiveConfig

	mu       sync.Mutex
	active   Strategy   // estratégia corrente (operando)
	activeName string
	candles  []domain.Candle // janela deslizante
	sinceScan int
	lastScan  *ScanResult
	// scans é o histórico de trocas (para o painel).
	switches []AdaptiveSwitch
}

// AdaptiveSwitch registra uma troca de estratégia.
type AdaptiveSwitch struct {
	AtBar      int    `json:"at_bar"`
	From       string `json:"from"`
	To         string `json:"to"`
	Reason     string `json:"reason"`
	FromScore  float64 `json:"from_score"`
	ToScore    float64 `json:"to_score"`
}

// NewAdaptive cria o seletor e registra a primeira estratégia do registry
// como ativa (o scan inicial decide a melhor na primeira revalidação).
func NewAdaptive(cfg AdaptiveConfig) *AdaptiveSelector {
	if cfg.Gate == (GateConfig{}) {
		cfg.Gate = DefaultGate()
	}
	if cfg.MonteCarloSims <= 0 {
		cfg.MonteCarloSims = 500
	}
	if cfg.SigTrials <= 0 {
		cfg.SigTrials = 500
	}
	names := Names()
	if len(names) == 0 {
		return nil
	}
	first, _ := NewByName(names[0])
	return &AdaptiveSelector{
		cfg:        cfg,
		active:     first,
		activeName: names[0],
	}
}

// ActiveName devolve o nome da estratégia operando agora.
func (a *AdaptiveSelector) ActiveName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeName
}

// Switches devolve o histórico de trocas.
func (a *AdaptiveSelector) Switches() []AdaptiveSwitch {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AdaptiveSwitch, len(a.switches))
	copy(out, a.switches)
	return out
}

// LastScan devolve o último resultado do scan (para o painel).
func (a *AdaptiveSelector) LastScan() *ScanResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastScan
}

// OnCandle alimenta a janela e, quando a revalidation window chega, re-scan
// e possivelmente troca a estratégia ativa. Os sinais vêm da estratégia ATIVA.
func (a *AdaptiveSelector) OnCandle(c domain.Candle) []Signal {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Janela deslizante.
	a.candles = append(a.candles, c)
	if len(a.candles) > a.cfg.Window {
		a.candles = a.candles[len(a.candles)-a.cfg.Window:]
	}

	// Sinais da estratégia ativa (sempre).
	var out []Signal
	if a.active != nil {
		out = a.active.OnCandle(c)
	}

	// Revalidação.
	a.sinceScan++
	if a.cfg.RevalidateEvery <= 0 || a.sinceScan >= a.cfg.RevalidateEvery {
		a.sinceScan = 0
		a.maybeSwitchLocked()
	}
	return out
}

// maybeSwitchLocked re-roda o scan e troca a ativa se outra estratégia passar
// no portão com score melhor. Deve ser chamado com o lock garantido.
func (a *AdaptiveSelector) maybeSwitchLocked() {
	if len(a.candles) < a.cfg.Window {
		return // janela ainda não cheia
	}
	scan := Scan(a.candles, a.cfg.InitialCapital, a.cfg.FeePct,
		a.cfg.Gate, a.cfg.MonteCarloSims, a.cfg.SigTrials, a.cfg.SeedBase+int64(a.sinceScan))
	a.lastScan = &scan

	if scan.Best == nil || scan.Best.Name == a.activeName {
		return // nada melhor ou já estamos na melhor
	}

	// Troca: cria a estratégia nova (estado zerado) e registra a troca.
	from := a.activeName
	newStrat, err := NewByName(scan.Best.Name)
	if err != nil {
		return
	}
	a.active = newStrat
	a.activeName = scan.Best.Name
	a.switches = append(a.switches, AdaptiveSwitch{
		AtBar:     len(a.candles),
		From:      from,
		To:        scan.Best.Name,
		Reason:    "scan revalidou e encontrou vantagem melhor",
		FromScore: scan.Best.Report.Stats.ProfitFactor, // pragmático: score real abaixo
		ToScore:   scan.Best.Score,
	})
}

// Score devolve o score da estratégia ativa no último scan (0 se nunca scan).
func (a *AdaptiveSelector) Score() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastScan == nil {
		return 0
	}
	for _, s := range a.lastScan.Strategies {
		if s.Name == a.activeName {
			return s.Score
		}
	}
	return 0
}
