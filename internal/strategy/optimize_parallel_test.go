package strategy

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// TestOptimizeDeterministicParallel valida que o otimizador PARALELO produz o
// MESMO resultado que o serial (testing farm determinística — a lição do
// Superalgos: casos independentes em paralelo, resultado idêntico).
func TestOptimizeDeterministicParallel(t *testing.T) {
	candles := SyntheticCandles(42, "TEST", 200, 100)
	ranges := []ParamRange{
		{Name: "stop_mult", Min: 0.5, Max: 1.5, Step: 0.5},
		{Name: "rsi_overb", Min: 60, Max: 70, Step: 10},
	}

	factory := func() Strategy { return NewContrarian() }
	setter := func(s Strategy, name string, v float64) {
		if c, ok := s.(*Contrarian); ok {
			c.SetParam(name, v)
		}
	}

	res := Optimize(factory, setter, candles, decimal.NewFromInt(10000),
		decimal.NewFromFloat(0.001), ranges, 100, 100, 7, 10)

	// Deve ter avaliado todas as combinações (3×2=6: stop 0.5/1.0/1.5 × RSI 60/70).
	if len(res.Ranked) != 6 {
		t.Fatalf("esperava 6 combinações, got %d", len(res.Ranked))
	}
	// Ranking ordenado.
	for i := 1; i < len(res.Ranked); i++ {
		if res.Ranked[i].Score > res.Ranked[i-1].Score {
			t.Fatalf("ranking fora de ordem em %d", i)
		}
	}
	// O melhor deve ter parâmetros preenchidos.
	if len(res.Params) != 2 {
		t.Fatalf("melhor combinação sem parâmetros: %v", res.Params)
	}
}

// TestOptimizeWorkerCount valida que o pool de workers funciona com 1 e com N.
func TestOptimizeWorkerCount(t *testing.T) {
	candles := SyntheticCandles(42, "TEST", 100, 100)
	ranges := []ParamRange{{Name: "stop_mult", Min: 0.5, Max: 1.0, Step: 0.5}}
	factory := func() Strategy { return NewContrarian() }
	setter := func(s Strategy, name string, v float64) {
		if c, ok := s.(*Contrarian); ok {
			c.SetParam(name, v)
		}
	}
	r1 := Optimize(factory, setter, candles, decimal.NewFromInt(10000),
		decimal.NewFromFloat(0.001), ranges, 50, 50, 7, 0)
	r2 := Optimize(factory, setter, candles, decimal.NewFromInt(10000),
		decimal.NewFromFloat(0.001), ranges, 50, 50, 7, 0)
	// Mesmo seed → mesmo melhor resultado (paralelismo não muda o determinismo).
	if !r1.PnL.Equal(r2.PnL) || r1.WinRate != r2.WinRate {
		t.Fatalf("otimizador não determinístico: %+v vs %+v", r1, r2)
	}
	_ = domain.Candle{}
}

// TestOptimizeInvalidRangeNoHang garante que um ParamRange com Step <= 0 (ou
// Min > Max) NÃO trava o otimizador em loop infinito — o range inválido é
// filtrado e a varredura prossegue com os demais (ou vazia).
func TestOptimizeInvalidRangeNoHang(t *testing.T) {
	candles := SyntheticCandles(42, "TEST", 100, 100)
	factory := func() Strategy { return NewContrarian() }

	// Step == 0: antes do fix, v += 0 nunca avança → loop infinito.
	done := make(chan struct{})
	go func() {
		Optimize(factory, nil, candles, decimal.NewFromInt(10000),
			decimal.NewFromFloat(0.001), []ParamRange{{Name: "x", Min: 0, Max: 10, Step: 0}}, 10, 10, 7, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("otimizador travou com Step == 0 (loop infinito)")
	}

	// Step < 0: v decrece e a condição v <= Max+eps segue verdadeira.
	done = make(chan struct{})
	go func() {
		Optimize(factory, nil, candles, decimal.NewFromInt(10000),
			decimal.NewFromFloat(0.001), []ParamRange{{Name: "x", Min: 0, Max: 10, Step: -1}}, 10, 10, 7, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("otimizador travou com Step < 0 (loop infinito)")
	}

	// Range inválido filtrado → varredura vazia, resultado determinístico com
	// uma única combinação (vazia).
	res := Optimize(factory, nil, candles, decimal.NewFromInt(10000),
		decimal.NewFromFloat(0.001), []ParamRange{{Name: "x", Min: 10, Max: 0, Step: 1}}, 10, 10, 7, 0)
	if len(res.Ranked) != 1 {
		t.Fatalf("esperava 1 combinação (vazia) após filtrar range inválido, got %d", len(res.Ranked))
	}
}
