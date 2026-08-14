package strategy

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

func TestPredictionTrackerResolvesHorizon(t *testing.T) {
	tr := NewPredictionTracker("ema-cross", 3, 50)

	// Previsão buy a 100.
	tr.Record(Signal{Symbol: "TEST", Side: "buy", Price: decimal.NewFromInt(100)})
	if tr.PendingCount() != 1 {
		t.Fatalf("deveria haver 1 previsão pendente, got %d", tr.PendingCount())
	}

	// Velas seguintes: 101, 102, 103 — na terceira, resolve (buy acertou).
	tr.OnCandle(domain.Candle{Symbol: "TEST", Close: 101})
	tr.OnCandle(domain.Candle{Symbol: "TEST", Close: 102})
	if tr.PendingCount() != 1 {
		t.Fatal("ainda dentro do horizonte")
	}
	tr.OnCandle(domain.Candle{Symbol: "TEST", Close: 103})
	if tr.PendingCount() != 0 {
		t.Fatalf("previsão deveria ter resolvido, ainda %d pendentes", tr.PendingCount())
	}
	results := tr.Results()
	if len(results) != 1 {
		t.Fatalf("esperava 1 resultado, got %d", len(results))
	}
	if !results[0].Hit {
		t.Fatal("buy a 100 com close 103 deveria acertar")
	}
	if results[0].ReturnPct <= 0 {
		t.Fatalf("retorno deveria ser positivo, got %v", results[0].ReturnPct)
	}
}

func TestPredictionTrackerMiss(t *testing.T) {
	tr := NewPredictionTracker("momentum", 2, 50)
	// Previsão sell a 100; o preço SOBE → erra.
	tr.Record(Signal{Symbol: "TEST", Side: "sell", Price: decimal.NewFromInt(100)})
	tr.OnCandle(domain.Candle{Symbol: "TEST", Close: 101})
	tr.OnCandle(domain.Candle{Symbol: "TEST", Close: 105})
	results := tr.Results()
	if len(results) != 1 || results[0].Hit {
		t.Fatalf("sell a 100 com close 105 deveria ERRAR: %+v", results)
	}
}

func TestPredictionTrackerSlidingWindow(t *testing.T) {
	tr := NewPredictionTracker("test", 1, 10)
	// Registra e resolve 15 previsões → janela deve ter só as 10 últimas.
	for i := 0; i < 15; i++ {
		tr.Record(Signal{Symbol: "TEST", Side: "buy", Price: decimal.NewFromInt(100)})
		tr.OnCandle(domain.Candle{Symbol: "TEST", Close: float64(100 + i)})
	}
	if n := len(tr.Results()); n != 10 {
		t.Fatalf("janela deslizante deveria ter 10 resultados, got %d", n)
	}
	if tr.HitRate() != 1.0 {
		t.Fatalf("todas acertaram (preço subiu), hit rate deveria ser 1, got %v", tr.HitRate())
	}
}

func TestConvergenceDiverges(t *testing.T) {
	// Estratégia com win rate esperado 60% mas que na prática erra quase
	// tudo → z-score negativo → divergência → reajuste disparado.
	tr := NewPredictionTracker("test", 1, 100)
	diverged := make(chan bool, 1)
	m := NewConvergence(0.60, tr, ConvergenceConfig{
		MinResolved: 20,
		ZThreshold:  -2.0,
		OnDivergence: func(ConvergenceState) {
			diverged <- true
		},
	})

	// 30 previsões buy a 100 com o preço CAINDO → erram quase todas.
	for i := 0; i < 30; i++ {
		tr.Record(Signal{Symbol: "TEST", Side: "buy", Price: decimal.NewFromInt(100)})
		tr.OnCandle(domain.Candle{Symbol: "TEST", Close: float64(100 - i*2)})
		m.Check()
	}

	select {
	case <-diverged:
		st := m.State()
		if !st.Diverged {
			t.Fatal("estado deveria marcar divergência")
		}
		if st.ZScore > -2.0 {
			t.Fatalf("z-score deveria ser muito negativo, got %v", st.ZScore)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("divergência deveria ter disparado o reajuste")
	}
}

func TestConvergenceConverges(t *testing.T) {
	// Estratégia com win rate esperado 50% que acerta ~50% → z-score ~0 →
	// convergindo, sem divergência.
	tr := NewPredictionTracker("test", 1, 100)
	m := NewConvergence(0.50, tr, ConvergenceConfig{MinResolved: 20, ZThreshold: -2.0})

	// Alterna: buy a 100 com close 101 (acerta), depois buy a 100 com close 99
	// (erra) — 50/50.
	for i := 0; i < 40; i++ {
		tr.Record(Signal{Symbol: "TEST", Side: "buy", Price: decimal.NewFromInt(100)})
		if i%2 == 0 {
			tr.OnCandle(domain.Candle{Symbol: "TEST", Close: 101})
		} else {
			tr.OnCandle(domain.Candle{Symbol: "TEST", Close: 99})
		}
		m.Check()
	}
	st := m.State()
	if st.Diverged {
		t.Fatalf("50%% de acerto com esperado 50%% não deveria divergir, z=%v", st.ZScore)
	}
	if !st.Converging {
		t.Fatal("deveria estar convergindo")
	}
	if st.Resolved != 40 {
		t.Fatalf("esperava 40 resolvidas, got %d", st.Resolved)
	}
}
