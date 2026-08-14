package strategy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// rapidFeed entrega velas sintéticas RÁPIDAS (50ms) — um stub de Exchange para
// o teste do laboratório vivo sem depender do ritmo do mercado.
type rapidFeed struct {
	candles []domain.Candle
}

func (r *rapidFeed) Name() string { return "rapid" }

func (r *rapidFeed) Connect(ctx context.Context, h exchange.Handler) error {
	go func() {
		for _, c := range r.candles {
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
				h.OnCandle(c)
			}
		}
	}()
	return nil
}

func (r *rapidFeed) SubscribeCandles(string, string) error { return nil }
func (r *rapidFeed) SubscribeTrades(string) error          { return nil }
func (r *rapidFeed) Close() error                          { return nil }

// TestShadowFullFlowDemo prova o LABORATÓRIO VIVO de ponta a ponta com um
// feed de velas sintéticas acelerado: a estratégia gera sinais, os sinais
// viram previsões, o tracker resolve e o monitor mede a convergência.
func TestShadowFullFlowDemo(t *testing.T) {
	tracker := NewPredictionTracker("ema-cross", 3, 100)
	monitor := NewConvergence(0.50, tracker, ConvergenceConfig{MinResolved: 10, ZThreshold: -2.0})
	strat := NewEMACross()

	// 200 velas sintéticas com tendência oscilante — a EMA cruza várias vezes.
	candles := SyntheticCandles(42, "BTCUSDT", 200, 60000)
	feed := &rapidFeed{candles: candles}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var mu sync.Mutex
	signals := 0
	resolved := 0

	go func() {
		_ = feed.Connect(ctx, exchange.Handler{
			OnTick:   func(domain.Tick) {},
			OnStatus: func(exchange.Status) {},
			OnCandle: func(c domain.Candle) {
				for _, sig := range strat.OnCandle(c) {
					mu.Lock()
					signals++
					mu.Unlock()
					tracker.Record(sig)
				}
				tracker.OnCandle(c)
				monitor.Check()
				mu.Lock()
				resolved = len(tracker.Results())
				mu.Unlock()
			},
		})
	}()

	// Aguarda sinais E resoluções (ou timeout).
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s, r := signals, resolved
		mu.Unlock()
		if s >= 3 && r >= 5 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	mu.Lock()
	s, r := signals, resolved
	mu.Unlock()

	if s == 0 {
		t.Fatal("a estratégia deveria ter gerado sinais com o feed")
	}
	if r == 0 {
		t.Fatal("o tracker deveria ter resolvido previsões")
	}
	st := monitor.State()
	if st.Resolved == 0 {
		t.Fatalf("monitor deveria ter resultados, got %d", st.Resolved)
	}
	t.Logf("laboratório vivo OK: %d sinais, %d previsões resolvidas, hit rate %.0f%%, z=%.2f, divergiu=%v",
		s, r, st.ObservedHit*100, st.ZScore, st.Diverged)
}
