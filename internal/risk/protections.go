// protections.go — PROTEÇÕES de mercado (lição minerada do Freqtrade):
// circuit-breakers que pausam novas entradas quando o mercado está adverso.
// Duas proteções de maior custo-benefício:
//
//   - StoplossGuard: se N trades recentes terminaram em STOP (perda), pausa
//     novas entradas por um período. O mercado está "armado" contra a
//     estratégia — parar é a decisão lucrativa.
//   - CooldownPeriod: após FECHAR um trade, o par fica em cooldown por X velas
//     — evita re-entrada imediata no mesmo movimento (o padrão do Freqtrade).
package risk

import (
	"errors"
	"sync"
)

// StoplossGuard pausa novas entradas quando trades recentes terminaram em stop.
type StoplossGuard struct {
	// MaxStops é o nº de stops que dispara a proteção (ex.: 3).
	MaxStops int
	// Lookback é a janela de velas analisada (ex.: 12 velas).
	Lookback int
	// PauseCandles é por quantas velas pausar ao disparar (ex.: 6).
	PauseCandles int
	// MaxPauses limita quantas vezes a proteção pode disparar seguidas
	// (0 = ilimitado; previne "proteção eterna").
	MaxPauses int
}

// CooldownPeriod evita re-entrada imediata em um par após fechar trade.
type CooldownPeriod struct {
	// CooldownCandles é o nº de velas de espera após fechar um trade no par.
	CooldownCandles int
}

// stopEvent registra um trade que terminou em stop (para o guard).
type stopEvent struct {
	atCandle int
}

// ProtectionState é o estado das proteções (para o /risk).
type ProtectionState struct {
	StoplossTriggered bool   `json:"stoploss_triggered"`
	StopsInWindow     int    `json:"stops_in_window"`
	PauseRemaining    int    `json:"pause_remaining"`
	PausesUsed        int    `json:"pauses_used"`
	Cooldowns         int    `json:"active_cooldowns"`
}

// Protections agrega as proteções ativas.
type Protections struct {
	guard   *StoplossGuard
	cooldown *CooldownPeriod

	mu            sync.Mutex
	recentStops   []stopEvent
	pauseUntil    int // vela até a qual novas entradas são bloqueadas
	pausesUsed    int
	cooldownUntil map[string]int // símbolo → vela até a qual está em cooldown
	currentBar    int
}

// NewProtections cria o conjunto de proteções (nil = desativada).
func NewProtections(guard *StoplossGuard, cooldown *CooldownPeriod) *Protections {
	p := &Protections{guard: guard, cooldown: cooldown, cooldownUntil: make(map[string]int)}
	return p
}

// OnBar avança o contador de velas (chame a cada CandleClosed).
func (p *Protections) OnBar() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentBar++
}

// OnStopLoss registra um trade que terminou em stop.
func (p *Protections) OnStopLoss() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.guard == nil {
		return
	}
	p.recentStops = append(p.recentStops, stopEvent{atCandle: p.currentBar})
	// Janela deslizante.
	cutoff := p.currentBar - p.guard.Lookback
	kept := p.recentStops[:0]
	for _, e := range p.recentStops {
		if e.atCandle >= cutoff {
			kept = append(kept, e)
		}
	}
	p.recentStops = kept
	// Dispara o guard se o limite de stops na janela foi atingido.
	if len(p.recentStops) >= p.guard.MaxStops {
		if p.guard.MaxPauses <= 0 || p.pausesUsed < p.guard.MaxPauses {
			p.pauseUntil = p.currentBar + p.guard.PauseCandles
			p.pausesUsed++
		}
	}
}

// OnTradeClosed registra o fechamento de um trade no par (cooldown).
func (p *Protections) OnTradeClosed(symbol string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cooldown == nil || p.cooldown.CooldownCandles <= 0 {
		return
	}
	p.cooldownUntil[symbol] = p.currentBar + p.cooldown.CooldownCandles
}

// Check pre-trade: bloqueia se o guard está ativo OU o par está em cooldown.
func (p *Protections) Check(symbol string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.guard != nil && p.currentBar < p.pauseUntil {
		remaining := p.pauseUntil - p.currentBar
		return errors.Join(ErrDrawdownBreach,
			errors.New("stoploss guard ativo: N stops recentes — pausa de novas entradas por "+itoa(remaining)+" velas"))
	}
	if p.cooldown != nil {
		if until, ok := p.cooldownUntil[symbol]; ok && p.currentBar < until {
			remaining := until - p.currentBar
			return errors.Join(ErrTooManyOpenOrders,
				errors.New("cooldown do par "+symbol+": aguarde "+itoa(remaining)+" velas antes de re-entrar"))
		}
	}
	return nil
}

// State devolve o estado das proteções.
func (p *Protections) State() ProtectionState {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := ProtectionState{}
	if p.guard != nil {
		st.StopsInWindow = len(p.recentStops)
		st.StoplossTriggered = p.currentBar < p.pauseUntil
		st.PauseRemaining = p.pauseUntil - p.currentBar
		if st.PauseRemaining < 0 {
			st.PauseRemaining = 0
		}
		st.PausesUsed = p.pausesUsed
	}
	if p.cooldown != nil {
		for _, until := range p.cooldownUntil {
			if p.currentBar < until {
				st.Cooldowns++
			}
		}
	}
	return st
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
