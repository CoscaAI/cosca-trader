package bot

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// bot.go — o kernel cognitivo HFT: orquestra semantic store + grafo + operações.
//
// A cadeia de comando (arquitetura de duas velocidades):
//
//	Don → Kernel (chat/LLM, lento, estratégico) → Bot (rápido, tático)
//	                                              │
//	                                              ├─ caminho exato  : comando → op (µs)
//	                                              └─ caminho semântico: comando → vetor → conceito → ação (µs)
//
// O bot NÃO chama LLM no caminho quente. Tudo é memória. A latência é µs.

// Result é a resposta do bot a um comando.
type Result struct {
	OK      bool
	Action  string // o que foi feito ("start ema-cross on DOGE", "status", ...)
	Detail  string // explicação humana
	Latency time.Duration
}

// Op é uma operação pré-compilada (comando → ação).
type Op struct {
	Command string                      // o verbo: "start", "stop", "risk", "status", "analyze", "help"
	Intent  string                      // descrição semântica (para busca por similaridade)
	Run     func(b *Bot, args []string) Result
}

// Bot é o kernel HFT.
type Bot struct {
	semantic *SemanticStore
	graph    *Graph
	ops      map[string]Op
	// estado operacional do bot (mutável, mas só o kernel escreve)
	state map[string]string
}

// New cria um bot vazio (sem conhecimento). Chame Load() para pré-carregar.
func New() *Bot {
	return &Bot{
		semantic: NewSemanticStore(),
		graph:    NewGraph(),
		ops:      make(map[string]Op),
		state:    make(map[string]string),
	}
}

// Load pré-carrega o conhecimento do bot (semântica + grafo + operações) em
// memória. É O caminho de inicialização: deve terminar em milissegundos.
func (b *Bot) Load() error {
	b.seed()
	return nil
}

// Execute processa um comando e devolve a ação. Caminho quente — µs.
//
//  1. tentativa exata  (mapa de ops)
//  2. tentativa semântica (vetor do comando → conceito mais próximo → op)
func (b *Bot) Execute(cmd string) Result {
	start := time.Now()
	finish := func(r Result) Result {
		r.Latency = time.Since(start)
		return r
	}

	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return finish(Result{OK: false, Action: "noop", Detail: "comando vazio"})
	}

	fields := strings.Fields(strings.ToLower(cmd))
	verb := fields[0]
	args := fields[1:]

	// 1. caminho exato
	if op, ok := b.ops[verb]; ok {
		return finish(op.Run(b, args))
	}

	// 2. caminho semântico: acha o conceito de operação mais próximo
	if c, ok := b.semantic.best(featureVector(verb)); ok && c.Kind == "op" {
		if op, ok := b.ops[c.ID]; ok {
			return finish(op.Run(b, args))
		}
	}

	return finish(Result{
		OK:     false,
		Action: "noop",
		Detail: fmt.Sprintf("comando não reconhecido: %q — diga 'help'", cmd),
	})
}

// Embedder embute uma lista de textos em vetores (adaptador para o modelo de
// embedding real — ex: nomic-embed-text via ollama). O bot NÃO depende do
// pacote cognitive; o chamador injeta esta função.
type Embedder func(ctx context.Context, texts []string) ([][]float32, error)

// Enrich substitui os vetores determinísticos (feature hashing) por embeddings
// reais — semântica cross-linguagem (consulta em português casa com conceito
// em inglês). Roda UMA vez no startup (não no caminho quente); se falhar, o
// bot mantém os vetores determinísticos e segue operando.
func (b *Bot) Enrich(ctx context.Context, embed Embedder) error {
	texts := make([]string, 0, b.semantic.Size())
	for i := range b.semantic.concepts {
		texts = append(texts, b.semantic.concepts[i].Text)
	}
	if len(texts) == 0 {
		return nil
	}
	vecs, err := embed(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(texts) {
		return fmt.Errorf("embedding devolveu %d vetores para %d textos", len(vecs), len(texts))
	}
	for i := range b.semantic.concepts {
		if vecs[i] != nil {
			normalize(vecs[i])
			b.semantic.concepts[i].Vector = vecs[i]
		}
	}
	return nil
}

// Status é o resumo operacional do bot (para o painel / chat).
func (b *Bot) Status() map[string]string {
	out := make(map[string]string, len(b.state)+3)
	for k, v := range b.state {
		out[k] = v
	}
	out["semantic.concepts"] = fmt.Sprintf("%d", b.semantic.Size())
	out["graph.nodes"] = fmt.Sprintf("%d", b.graph.NodeCount())
	out["graph.edges"] = fmt.Sprintf("%d", b.graph.EdgeCount())
	return out
}

// ── seed: o conhecimento pré-compilado do bot ──────────────────────────────

func (b *Bot) seed() {
	b.seedStrategies()
	b.seedSymbols()
	b.seedSignals()
	b.seedStates()
	b.seedOps()
	b.state["active_strategy"] = "ema-cross"
	b.state["risk"] = "low"
}

func (b *Bot) seedStrategies() {
	strats := []struct{ name, desc string }{
		{"ema-cross", "exponential moving average crossover trend following"},
		{"momentum", "price momentum breaking out with strength"},
		{"rsi-reversion", "mean reversion when RSI is overbought or oversold"},
		{"breakout", "channel breakout closing above N-bar high"},
		{"bb-reversion", "bollinger band mean reversion lateral market"},
		{"contrarian", "trade against the market at sentiment extremes"},
	}
	for _, s := range strats {
		b.graph.AddNode(Node{ID: s.name, Kind: "strategy"})
		b.semantic.Add("strategy:"+s.name, "strategy", s.name+" "+s.desc)
	}
}

func (b *Bot) seedSymbols() {
	symbols := []string{"BTC", "ETH", "DOGE", "SOL", "BNB"}
	for _, s := range symbols {
		b.graph.AddNode(Node{ID: s, Kind: "symbol"})
		b.semantic.Add("symbol:"+s, "symbol", s+" cryptocurrency")
		// todas as estratégias suportam todos os símbolos (o scanner testa)
		for _, st := range strategyNames() {
			b.graph.AddEdge(s, st, "supports")
		}
	}
}

func (b *Bot) seedSignals() {
	signals := []struct{ name, desc string }{
		{"buy", "buy signal enter long position"},
		{"sell", "sell signal enter short position"},
		{"flat", "no position stay out of the market"},
	}
	for _, s := range signals {
		b.graph.AddNode(Node{ID: s.name, Kind: "signal"})
		b.semantic.Add("signal:"+s.name, "signal", s.desc)
	}
	// sinal → ação
	b.graph.AddEdge("buy", "long", "executes")
	b.graph.AddEdge("sell", "short", "executes")
	b.graph.AddEdge("flat", "hold", "executes")

	// estratégias → sinais (que sinal cada uma gera)
	b.graph.AddEdge("bb-reversion", "buy", "triggers")
	b.graph.AddEdge("bb-reversion", "sell", "triggers")
	b.graph.AddEdge("ema-cross", "buy", "triggers")
	b.graph.AddEdge("ema-cross", "sell", "triggers")
	b.graph.AddEdge("breakout", "buy", "triggers")
	b.graph.AddEdge("breakout", "sell", "triggers")
	b.graph.AddEdge("momentum", "buy", "triggers")
	b.graph.AddEdge("rsi-reversion", "buy", "triggers")
	b.graph.AddEdge("rsi-reversion", "sell", "triggers")
	b.graph.AddEdge("contrarian", "buy", "triggers")
	b.graph.AddEdge("contrarian", "sell", "triggers")

	// ações como nós
		for _, a := range []string{"long", "short", "hold"} {
			b.graph.AddNode(Node{ID: a, Kind: "action"})
			b.semantic.Add("action:"+a, "action", a+" market position")
		}
}

func (b *Bot) seedStates() {
	states := []struct{ name, desc string }{
		{"risk-on", "market risk on appetite buying"},
		{"risk-off", "market risk off aversion selling"},
		{"trending", "market in a defined trend"},
		{"lateral", "sideways market no direction"},
		{"volatile", "volatile market large swings"},
	}
	for _, s := range states {
		b.graph.AddNode(Node{ID: s.name, Kind: "state"})
		b.semantic.Add("state:"+s.name, "state", s.desc)
	}
	// regime → estratégia (gate macro, L284: sinal só passa se o regime confirmar)
	b.graph.AddEdge("risk-on", "ema-cross", "gates")
	b.graph.AddEdge("risk-on", "breakout", "gates")
	b.graph.AddEdge("trending", "ema-cross", "gates")
	b.graph.AddEdge("trending", "breakout", "gates")
	b.graph.AddEdge("lateral", "bb-reversion", "gates")
	b.graph.AddEdge("lateral", "rsi-reversion", "gates")
	b.graph.AddEdge("volatile", "contrarian", "gates")
}

func (b *Bot) seedOps() {
	reg := func(name, intent string, run func(b *Bot, args []string) Result) {
		b.ops[name] = Op{Command: name, Intent: intent, Run: run}
		b.semantic.Add(name, "op", name+" "+intent)
	}

	reg("start", "start activate a strategy on a symbol", func(b *Bot, args []string) Result {
		if len(args) == 0 {
			return Result{OK: false, Action: "start", Detail: "uso: start <estratégia> on <símbolo>"}
		}
		strat := args[0]
		symbol := ""
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "on" {
				symbol = strings.ToUpper(args[i+1])
			}
		}
		if !hasStrategy(strat) {
			return Result{OK: false, Action: "start", Detail: fmt.Sprintf("estratégia desconhecida: %q", strat)}
		}
		if symbol == "" {
			symbol = "BTC"
		}
		b.state["active_strategy"] = strat
		b.state["symbol"] = symbol
		return Result{OK: true, Action: fmt.Sprintf("start %s on %s", strat, symbol),
			Detail: fmt.Sprintf("estratégia %s ativada em %s — sinais fluindo", strat, symbol)}
	})

	reg("stop", "stop deactivate the active strategy", func(b *Bot, args []string) Result {
		cur := b.state["active_strategy"]
		b.state["active_strategy"] = "flat"
		return Result{OK: true, Action: "stop", Detail: fmt.Sprintf("estratégia %s desativada — flat", cur)}
	})

	reg("risk", "adjust the risk level", func(b *Bot, args []string) Result {
		lvl := "low"
		if len(args) > 0 {
			lvl = args[0]
		}
		b.state["risk"] = lvl
		return Result{OK: true, Action: "risk " + lvl, Detail: fmt.Sprintf("risco ajustado para %s", lvl)}
	})

	reg("analyze", "analyze a symbol using the graph and vectors", func(b *Bot, args []string) Result {
		symbol := "BTC"
		if len(args) > 0 {
			symbol = strings.ToUpper(args[0])
		}
		// caminho: símbolo → estratégias suportadas → sinais → ações
		strats := b.graph.Neighbors(symbol, "supports")
		var lines []string
		for _, st := range strats {
			sigs := b.graph.Neighbors(st, "triggers")
			lines = append(lines, fmt.Sprintf("%s → %v", st, sigs))
		}
		return Result{OK: true, Action: "analyze " + symbol,
			Detail: fmt.Sprintf("%s: %s", symbol, strings.Join(lines, "; "))}
	})

	reg("status", "report the current bot state", func(b *Bot, args []string) Result {
		return Result{OK: true, Action: "status",
			Detail: fmt.Sprintf("estratégia=%s símbolo=%s risco=%s conceitos=%d nós=%d arestas=%d",
				b.state["active_strategy"], b.state["symbol"], b.state["risk"],
				b.semantic.Size(), b.graph.NodeCount(), b.graph.EdgeCount())}
	})

	reg("help", "list the available commands", func(b *Bot, args []string) Result {
		var cmds []string
		for k := range b.ops {
			cmds = append(cmds, k)
		}
		sort.Strings(cmds)
		return Result{OK: true, Action: "help", Detail: "comandos: " + strings.Join(cmds, ", ")}
	})
}

// ── helpers ────────────────────────────────────────────────────────────────

func strategyNames() []string {
	return []string{"ema-cross", "momentum", "rsi-reversion", "breakout", "bb-reversion", "contrarian"}
}

func hasStrategy(name string) bool {
	for _, s := range strategyNames() {
		if s == name {
			return true
		}
	}
	return false
}
