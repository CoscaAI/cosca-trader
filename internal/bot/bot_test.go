package bot

import (
	"strings"
	"testing"
	"time"
)

// TestLoadAndExecute verifica que o bot carrega o conhecimento e executa
// comandos exatos e semânticos corretamente.
func TestLoadAndExecute(t *testing.T) {
	b := New()
	if err := b.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if b.semantic.Size() == 0 || b.graph.NodeCount() == 0 || len(b.ops) == 0 {
		t.Fatalf("conhecimento vazio: concepts=%d nodes=%d ops=%d",
			b.semantic.Size(), b.graph.NodeCount(), len(b.ops))
	}

	t.Run("caminho exato", func(t *testing.T) {
		r := b.Execute("start bb-reversion on DOGE")
		if !r.OK || !strings.Contains(r.Action, "bb-reversion") {
			t.Fatalf("start exato falhou: %+v", r)
		}
	})

	t.Run("caminho semântico (verbo próximo)", func(t *testing.T) {
		// "begin" não é comando exato, mas é semanticamente próximo de "start"
		r := b.Execute("begin ema-cross on ETH")
		if !r.OK {
			t.Fatalf("caminho semântico deveria resolver 'begin'→'start': %+v", r)
		}
	})

	t.Run("analyze usa o grafo", func(t *testing.T) {
		r := b.Execute("analyze BTC")
		if !r.OK || !strings.Contains(r.Detail, "ema-cross") {
			t.Fatalf("analyze deveria listar estratégias do grafo: %+v", r)
		}
	})

	t.Run("comando desconhecido", func(t *testing.T) {
		r := b.Execute("xyzzy")
		if r.OK {
			t.Fatalf("comando desconhecido não deveria ser OK: %+v", r)
		}
	})
}

// TestSemanticSearch verifica que a busca por cosseno recupera o conceito certo.
func TestSemanticSearch(t *testing.T) {
	b := New()
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}

	// "bollinger band mean reversion" deve bater em bb-reversion
	hits := b.semantic.SearchText("bollinger band mean reversion lateral", 3)
	if len(hits) == 0 {
		t.Fatal("sem hits")
	}
	if hits[0].Concept.Kind != "strategy" {
		t.Fatalf("esperava strategy, veio %s (%s)", hits[0].Concept.Kind, hits[0].Concept.ID)
	}
	if hits[0].Score <= 0 {
		t.Fatalf("score deve ser positivo: %f", hits[0].Score)
	}
}

// TestGraphPath verifica o BFS (ex: símbolo → estratégia → sinal → ação).
func TestGraphPath(t *testing.T) {
	b := New()
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	path := b.graph.Path("DOGE", "long")
	if len(path) == 0 {
		t.Fatal("deveria haver caminho DOGE → ... → long")
	}
}

// ── Benchmarks: provar a latência (µs, não segundos) ───────────────────────

func BenchmarkLoad(b *testing.B) {
	for i := 0; i < b.N; i++ {
		bot := New()
		_ = bot.Load()
	}
}

func BenchmarkExecuteExact(b *testing.B) {
	bot := New()
	_ = bot.Load()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bot.Execute("start bb-reversion on DOGE")
	}
}

func BenchmarkExecuteSemantic(b *testing.B) {
	bot := New()
	_ = bot.Load()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bot.Execute("begin ema-cross on ETH")
	}
}

func BenchmarkSemanticSearch(b *testing.B) {
	bot := New()
	_ = bot.Load()
	q := featureVector("bollinger band mean reversion lateral")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bot.semantic.Search(q, 3)
	}
}

// TestLatencyReport é um teste informativo que loga os tempos reais.
func TestLatencyReport(t *testing.T) {
	b := New()
	start := time.Now()
	_ = b.Load()
	loadMs := time.Since(start).Microseconds()

	// aquece
	for i := 0; i < 1000; i++ {
		_ = b.Execute("status")
	}
	start = time.Now()
	for i := 0; i < 10000; i++ {
		_ = b.Execute("status")
	}
	execUs := time.Since(start).Nanoseconds() / 10000 // ns por op

	start = time.Now()
	for i := 0; i < 10000; i++ {
		_ = b.semantic.SearchText("bollinger band mean reversion", 3)
	}
	searchUs := time.Since(start).Nanoseconds() / 10000

	t.Logf("load=%dµs  exec=%dns/op (~%dµs)  semantic_search=%dns/op (~%dµs)",
		loadMs, execUs, execUs/1000, searchUs, searchUs/1000)

	if loadMs > 50_000 { // 50ms
		t.Errorf("load lento demais: %dµs", loadMs)
	}
	if execUs > 100_000 { // 100µs
		t.Errorf("execute lento demais: %dns", execUs)
	}
}
