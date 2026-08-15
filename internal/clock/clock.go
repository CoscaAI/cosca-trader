// Package clock injeta o tempo no motor de trading (padrão EngineClock do
// barter-rs / paridade backtest-live do Lean). Toda lógica que depende de
// "agora" (time-in-force, expiração, PnL marcado a mercado, rate limit de mark)
// usa um Clock em vez de time.Now() direto — assim o MESMO código roda em live
// (RealClock) e em backtest (FixedClock avançado a cada evento histórico).
package clock

import "time"

// Clock é a fonte de tempo do motor. Implementações: Real() para live,
// Fixed(t) para backtest/replay determinístico.
type Clock interface {
	// Now devolve o instante corrente (real ou histórico).
	Now() time.Time
}

// realClock devolve time.Now() — o clock de produção.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Real devolve o clock de parede (produção/live).
func Real() Clock { return realClock{} }

// fixedClock devolve sempre o mesmo instante — útil em testes e em replay
// determinístico onde o tempo vem do último evento processado.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// Fixed devolve um clock congelado em t.
func Fixed(t time.Time) Clock { return fixedClock{t: t} }
