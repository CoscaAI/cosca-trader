// ratelimit.go — proteção de RATE LIMIT do monitor macro (a preocupação do
// Don: "so nao esquece que pode ter limite rate"). Protege contra:
//  1. Estourar o limite da API do Yahoo/Binance (429) — throttling interno.
//  2. Operar com DADOS INSUFICIENTES — se o radar falhou ou os dados estão
//     velhos demais, o sistema NÃO opera (fail-safe).
package marketindex

import (
	"sync"
	"time"
)

// RateLimiter controla a frequência de chamadas externas e a frescura dos
// dados. Determinístico e thread-safe.
type RateLimiter struct {
	mu          sync.Mutex
	minInterval time.Duration
	lastCall    time.Time
	// MaxDataAge: dados mais velhos que isto são considerados STALE — o
	// sistema não opera com eles (proteção por falta de dados).
	MaxDataAge time.Duration
}

// NewRateLimiter cria o limitador. minInterval entre chamadas externas;
// maxDataAge é o limite de idade dos dados.
func NewRateLimiter(minInterval, maxDataAge time.Duration) *RateLimiter {
	if minInterval <= 0 {
		minInterval = 5 * time.Minute
	}
	if maxDataAge <= 0 {
		maxDataAge = 15 * time.Minute
	}
	return &RateLimiter{minInterval: minInterval, MaxDataAge: maxDataAge}
}

// Allow decide se uma chamada externa pode ocorrer agora (throttle).
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.lastCall.IsZero() || now.Sub(r.lastCall) >= r.minInterval {
		r.lastCall = now
		return true
	}
	return false
}

// NextAllowed devolve quando a próxima chamada pode ocorrer (para o loop).
func (r *RateLimiter) NextAllowed() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastCall.IsZero() {
		return time.Now()
	}
	return r.lastCall.Add(r.minInterval)
}

// Fresh decide se dados com a idade dada ainda são utilizáveis.
func (r *RateLimiter) Fresh(age time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return age <= r.MaxDataAge
}

// IsDataEnough valida se temos dados SUFICIENTES para operar: o snapshot tem
// o S&P E o VIX, e não está velho. Sem dados suficientes → NÃO opera.
func IsDataEnough(snap Snapshot, limiter *RateLimiter) bool {
	if limiter == nil {
		// Sem limitador, exige que o snapshot não esteja vazio.
		return len(snap.Quotes) >= 2
	}
	if !limiter.Fresh(time.Since(snap.FetchedAt)) {
		return false // dados velhos demais
	}
	sp := findQuote(snap, "^GSPC")
	vix := findQuote(snap, "^VIX")
	return sp != nil && vix != nil
}
