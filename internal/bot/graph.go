package bot

// graph.go — o grafo de conhecimento do bot (node-edge), todo em memória.
//
// O grafo codifica as relações de trading que o bot consulta no caminho quente:
//
//	símbolo → estratégia  ("supports")   ex: BTC → bb-reversion
//	estratégia → sinal    ("triggers")   ex: bb-reversion → buy
//	sinal → ação          ("executes")   ex: buy → long
//	estado → estratégia   ("gates")      ex: risk-on → ema-cross (regime)
//
// É uma matriz de adjacência em mapa — traversal em microssegundos.

// Node é um vértice do grafo.
type Node struct {
	ID   string // identificador único (ex: "BTC", "bb-reversion", "buy", "risk-on")
	Kind string // "symbol" | "strategy" | "signal" | "action" | "state"
	Data map[string]string
}

// Edge é uma aresta direcionada com relação tipada.
type Edge struct {
	From string // ID do nó origem
	To   string // ID do nó destino
	Rel  string // relação: "supports" | "triggers" | "executes" | "gates"
}

// Graph é o grafo de conhecimento em memória.
type Graph struct {
	nodes map[string]Node
	edges map[string][]Edge // adjacência: nodeID → arestas de saída
}

// NewGraph cria um grafo vazio.
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]Node),
		edges: make(map[string][]Edge),
	}
}

// AddNode insere (ou substitui) um nó.
func (g *Graph) AddNode(n Node) {
	g.nodes[n.ID] = n
}

// AddEdge insere uma aresta direcionada.
func (g *Graph) AddEdge(from, to, rel string) {
	g.edges[from] = append(g.edges[from], Edge{From: from, To: to, Rel: rel})
}

// Node devolve um nó pelo ID.
func (g *Graph) Node(id string) (Node, bool) {
	n, ok := g.nodes[id]
	return n, ok
}

// Out devolve as arestas de saída de um nó (traversal rápido).
func (g *Graph) Out(id string) []Edge {
	return g.edges[id]
}

// Neighbors devolve os IDs vizinhos por relação. Se rel == "", todas.
func (g *Graph) Neighbors(id, rel string) []string {
	var out []string
	for _, e := range g.edges[id] {
		if rel == "" || e.Rel == rel {
			out = append(out, e.To)
		}
	}
	return out
}

// Path devolve o caminho mais curto (BFS) entre dois nós, ou nil se não existe.
// O grafo do bot é pequeno (dezenas de nós), então BFS é instantâneo.
func (g *Graph) Path(from, to string) []Edge {
	if from == to {
		return nil
	}
	prev := map[string]Edge{}
	visited := map[string]bool{from: true}
	queue := []string{from}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			// reconstrói o caminho de trás pra frente
			var path []Edge
			for cur != from {
				e := prev[cur]
				path = append([]Edge{e}, path...)
				cur = e.From
			}
			return path
		}
		for _, e := range g.edges[cur] {
			if !visited[e.To] {
				visited[e.To] = true
				prev[e.To] = e
				queue = append(queue, e.To)
			}
		}
	}
	return nil
}

// NodeCount e EdgeCount reportam o tamanho do grafo.
func (g *Graph) NodeCount() int { return len(g.nodes) }
func (g *Graph) EdgeCount() int {
	n := 0
	for _, es := range g.edges {
		n += len(es)
	}
	return n
}
