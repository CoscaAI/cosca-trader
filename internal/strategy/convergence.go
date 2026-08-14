// convergence.go — o MONITOR DE CONVERGÊNCIA (Fase 5): a resposta direta ao
// Don ("é possível identificar se o cálculo está em convergência e reajustar
// automaticamente?").
//
// Cada estratégia tem uma taxa de acerto ESPERADA (do laudo científico —
// win rate validado no backtest). O monitor mede a taxa de acerto OBSERVADA
// nas previsões realizadas em tempo real e calcula se a previsão está
// convergindo com o mercado. Quando a divergência é estatisticamente
// significativa (z-score abaixo de -2, ou seja, o sistema está errando MUITO
// mais que o esperado), o regime mudou → dispara o callback de reajuste
// automático (o AdaptiveSelector troca a estratégia).
package strategy

import (
	"math"
	"sync"
	"time"
)

// ConvergenceState é o estado corrente do monitor (para o painel).
type ConvergenceState struct {
	Strategy      string    `json:"strategy"`
	ExpectedHit   float64   `json:"expected_hit"`   // win rate do laudo
	ObservedHit   float64   `json:"observed_hit"`   // win rate real na janela
	Resolved      int       `json:"resolved"`       // previsões resolvidas
	Pending       int       `json:"pending"`        // previsões aguardando
	ZScore        float64   `json:"z_score"`        // desvios do esperado (negativo = pior que esperado)
	Converging    bool      `json:"converging"`     // dentro do intervalo de confiança
	Diverged      bool      `json:"diverged"`       // regime mudou (z < threshold)
	LastCheck     time.Time `json:"last_check"`
	DivergedAt    time.Time `json:"diverged_at,omitempty"`
	Readjustments int       `json:"readjustments"` // nº de reajustes disparados
	LastReason    string    `json:"last_reason,omitempty"`
}

// ConvergenceConfig parametriza o monitor.
type ConvergenceConfig struct {
	// MinResolved é o mínimo de previsões resolvidas antes de julgar
	// convergência (amostra pequena não prova nada — a lição do F5).
	MinResolved int
	// ZThreshold é o z-score que dispara divergência (default -2.0).
	ZThreshold float64
	// OnDivergence é chamado quando o regime muda (reajuste automático).
	OnDivergence func(state ConvergenceState)
}

// ConvergenceMonitor acompanha a convergência da previsão com a realidade.
type ConvergenceMonitor struct {
	cfg       ConvergenceConfig
	expected  float64
	tracker   *PredictionTracker
	mu        sync.Mutex
	diverged  bool
	divergedAt time.Time
	adjusts   int
	lastZ     float64
	lastCheck time.Time
}

// NewConvergence cria o monitor. expectedHit é o win rate do laudo científico.
func NewConvergence(expectedHit float64, tracker *PredictionTracker, cfg ConvergenceConfig) *ConvergenceMonitor {
	if cfg.MinResolved <= 0 {
		cfg.MinResolved = 20
	}
	if cfg.ZThreshold == 0 {
		cfg.ZThreshold = -2.0
	}
	return &ConvergenceMonitor{
		cfg:      cfg,
		expected: expectedHit,
		tracker:  tracker,
	}
}

// State devolve o estado corrente do monitor.
func (c *ConvergenceMonitor) State() ConvergenceState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}

func (c *ConvergenceMonitor) stateLocked() ConvergenceState {
	st := ConvergenceState{
		Strategy:    c.tracker.strategy,
		ExpectedHit: c.expected,
		ObservedHit: c.tracker.HitRate(),
		Resolved:    len(c.tracker.Results()),
		Pending:     c.tracker.PendingCount(),
		ZScore:      c.lastZ,
		Converging:  !c.diverged,
		Diverged:    c.diverged,
		LastCheck:   c.lastCheck,
		DivergedAt:  c.divergedAt,
		Readjustments: c.adjusts,
		LastReason:  c.lastReason(),
	}
	return st
}

// Check reavalia a convergência com os resultados mais recentes. Chamado a
// cada vela (o tracker resolve previsões → re-checamos).
func (c *ConvergenceMonitor) Check() ConvergenceState {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastCheck = time.Now()
	results := c.tracker.Results()
	n := len(results)
	observed := c.tracker.HitRate()

	if n < c.cfg.MinResolved {
		// Amostra insuficiente — ainda não dá para julgar convergência.
		c.lastZ = 0
		return c.stateLocked()
	}

	// Z-score para proporção: z = (p_obs − p_esp) / sqrt(p_esp(1−p_esp)/n)
	p := c.expected
	if p <= 0 || p >= 1 {
		p = 0.5 // sem referência, assume 50% (sorte)
	}
	se := math.Sqrt(p * (1 - p) / float64(n))
	if se == 0 {
		c.lastZ = 0
		return c.stateLocked()
	}
	z := (observed - p) / se
	c.lastZ = z

	if z < c.cfg.ZThreshold && !c.diverged {
		// REGIME MUDOU: o sistema erra significativamente mais que o esperado.
		c.diverged = true
		c.divergedAt = time.Now()
		c.adjusts++
		st := c.stateLocked()
		if c.cfg.OnDivergence != nil {
			go c.cfg.OnDivergence(st) // reajuste em background — nunca bloqueia o feed
		}
		return st
	}
	if z >= c.cfg.ZThreshold {
		c.diverged = false // voltou a convergir
	}
	return c.stateLocked()
}

func (c *ConvergenceMonitor) lastReason() string {
	if c.diverged {
		return "z-score abaixo do limiar — a previsão divergiu do mercado (regime mudou); reajuste disparado"
	}
	return "previsão dentro do intervalo de confiança — convergindo"
}
