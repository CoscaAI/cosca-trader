package diagnosis

import (
	"strings"
	"testing"

	"github.com/CoscaAI/cosca-trader/internal/strategy"
)

// TestAnalyze verifica que o diagnóstico produz estatística completa e um
// parecer (favorável/desfavorável) coerente sobre candles sintéticas.
func TestAnalyze(t *testing.T) {
	candles := strategy.SyntheticCandles(42, "BTCUSDT", 300, 50000)
	d := Analyze("BTCUSDT", "risk-on", candles)

	if d.Symbol != "BTCUSDT" {
		t.Fatalf("symbol = %q", d.Symbol)
	}
	if len(d.Strategies) == 0 {
		t.Fatal("esperava estatística de pelo menos uma estratégia")
	}
	if d.BestStrategy == "" {
		t.Fatal("best_strategy vazio")
	}
	// toda estratégia listada deve ter campos preenchidos
	for _, s := range d.Strategies {
		if s.Trades <= 0 {
			t.Fatalf("estratégia %s sem trades", s.Strategy)
		}
		if s.WinRate < 0 || s.WinRate > 1 {
			t.Fatalf("estratégia %s win-rate fora de [0,1]: %f", s.Strategy, s.WinRate)
		}
	}
	if d.Summary == "" || !strings.Contains(d.Summary, d.Symbol) {
		t.Fatalf("summary incoerente: %q", d.Summary)
	}
	t.Logf("diagnóstico: %s", d.Summary)
	t.Logf("estratégias avaliadas: %d | melhor: %s (edge %.4f)", len(d.Strategies), d.BestStrategy, d.Strategies[0].Edge)
}

// TestAnalyzeInsufficient verifica o caso sem dados suficientes.
func TestAnalyzeInsufficient(t *testing.T) {
	d := Analyze("BTCUSDT", "risk-on", strategy.SyntheticCandles(1, "BTCUSDT", 10, 50000))
	if d.Favorable {
		t.Fatal("sem dados suficientes não deveria ser favorável")
	}
}
