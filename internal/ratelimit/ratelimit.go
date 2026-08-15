// Package ratelimit fornece limitadores de taxa para o caminho de execução
// (padrão Throttler do ccxt + rate limiting do OpenAlgo). Duas implementações:
//
//   - TokenBucket: reabastecimento contínuo por segundo — para limites do tipo
//     "N requests/segundo" com custo variável por request (kline grande = custo
//     maior).
//   - SlidingWindow: janela deslizante de timestamps — para limites "M requests
//     por janela" (ex.: 1000/min da Binance).
//
// Ambos expõem Wait(ctx, cost) que bloqueia até liberar ou o contexto cancelar.
// Sem dependências externas — puro mutex + relógio injetável.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// TokenBucket é um leaky/token bucket simples: tokens reabastecem a refillRate
// por segundo até a capacity. Cada Wait(cost) consome `cost` tokens.
type TokenBucket struct {
	mu         sync.Mutex
	capacity   float64
	tokens     float64
	refillRate float64 // tokens por segundo
	last       time.Time
	now        func() time.Time
}

// NewTokenBucket cria um bucket com a capacidade e a taxa (tokens/segundo)
// dados. capacity também é o número inicial de tokens.
func NewTokenBucket(capacity, refillRate float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: refillRate,
		last:       time.Now(),
		now:        time.Now,
	}
}

// Wait bloqueia até haver `cost` tokens disponíveis ou ctx cancelar.
func (b *TokenBucket) Wait(ctx context.Context, cost float64) error {
	if cost <= 0 {
		return nil
	}
	for {
		b.mu.Lock()
		now := b.now()
		elapsed := now.Sub(b.last).Seconds()
		b.last = now
		b.tokens += elapsed * b.refillRate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		if b.tokens >= cost {
			b.tokens -= cost
			b.mu.Unlock()
			return nil
		}
		// tempo até reunir os tokens que faltam
		missing := cost - b.tokens
		wait := time.Duration(missing / b.refillRate * float64(time.Second))
		b.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// SlidingWindow limita a `max` unidades de custo dentro de `window`. Mantém
// os timestamps de cada consumo; ao estourar, espera até a janela liberar.
type SlidingWindow struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	times  []time.Time
	now    func() time.Time
	after  func(time.Duration) <-chan time.Time
}

// NewSlidingWindow cria um limitador de `max` unidades por `window`.
func NewSlidingWindow(window time.Duration, max int) *SlidingWindow {
	return &SlidingWindow{window: window, max: max, now: time.Now, after: time.After}
}

// Wait bloqueia até caber `cost` unidades na janela ou ctx cancelar.
func (w *SlidingWindow) Wait(ctx context.Context, cost int) error {
	if cost <= 0 || w.max <= 0 {
		return nil
	}
	for {
		w.mu.Lock()
		now := w.now()
		cutoff := now.Add(-w.window)
		kept := w.times[:0]
		for _, t := range w.times {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		w.times = kept
		if len(w.times)+cost <= w.max {
			for i := 0; i < cost; i++ {
				w.times = append(w.times, now)
			}
			w.mu.Unlock()
			return nil
		}
		// espera até o mais antigo sair da janela
		oldest := w.times[0]
		wait := oldest.Add(w.window).Sub(now)
		w.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.after(wait):
		}
	}
}
