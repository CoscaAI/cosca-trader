// Package bot implementa o kernel cognitivo de alta frequência (HFT) do
// cosca-trader. É o "cérebro rápido" da arquitetura de duas velocidades:
//
//   - Kernel (chat/LLM)  = estratégia — compila conhecimento, orienta (lento, raro)
//   - Bot (este pacote)  = tática   — executa em milissegundos (rápido, contínuo)
//
// O bot NUNCA chama LLM no caminho quente. Tudo é pré-computado e mantido em
// memória: vetores semânticos (similaridade por cosseno), grafo node-edge e
// operações (comando → ação). A latência é medida em milissegundos, não segundos.
package bot

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

// dim é a dimensão do vetor de feature hashing. Para o espaço dedicado do bot
// (dezenas de conceitos de trading), 256 dimensões dão boa separação sem custo.
const dim = 256

// Vector é um vetor de embedding pré-normalizado (norma unitária).
type Vector []float32

// featureVector constrói um vetor determinístico a partir de texto usando o
// "hashing trick" (n-gramas → bucket de hash → contagem com sinal). É
// determinístico, sem rede e captura sobreposição de tokens para similaridade
// por cosseno — o fallback de embedding quando não há modelo disponível.
//
// Para embeddings ricos (semântica real), o kernel compila vetores via o
// modelo de embedding (nomic-embed-text) e chama AddVector — este método é só
// o caminho offline determinístico.
func featureVector(text string) Vector {
	v := make(Vector, dim)
	text = strings.ToLower(text)

	words := strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '.'
	})

	// 1-gramas (tokens) + 2-gramas (frases) para contexto.
	for _, w := range words {
		addFeature(v, w)
	}
	for i := 0; i+1 < len(words); i++ {
		addFeature(v, words[i]+" "+words[i+1])
	}

	normalize(v)
	return v
}

// addFeature soma uma feature assinada no bucket correspondente. O sinal vem
// de um segundo hash — evita colisões destrutivas (o "hashing trick" clássico).
func addFeature(v Vector, feat string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(feat))
	idx := h.Sum32() % uint32(len(v))

	s := fnv.New32a()
	_, _ = s.Write([]byte(feat + "#sign"))
	sign := float32(1)
	if s.Sum32()&1 == 1 {
		sign = -1
	}
	v[idx] += sign
}

// normalize normaliza o vetor para norma unitária (cosseno = produto escalar).
func normalize(v Vector) {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	if sum == 0 {
		return
	}
	n := float32(math.Sqrt(float64(sum)))
	for i := range v {
		v[i] /= n
	}
}

// dot é o produto escalar de dois vetores unitários (= similaridade cosseno).
func dot(a, b Vector) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Concept é uma unidade semântica do conhecimento do bot.
type Concept struct {
	ID     string // ex: "strategy:bb-reversion", "signal:buy", "state:risk-on"
	Kind   string // "strategy" | "signal" | "action" | "state" | "symbol"
	Text   string // texto humano (embedding + explicação)
	Vector Vector // embedding pré-computado (norma unitária)
}

// Hit é um resultado de busca semântica.
type Hit struct {
	Concept Concept
	Score   float32 // similaridade cosseno em [0,1]
}

// SemanticStore é o índice de vetores em memória do bot. Para o espaço dedicado
// (poucas centenas de conceitos), a varredura linear com produto escalar é a
// opção MAIS rápida — sem overhead de índice (HNSW só compensa a partir de
// ~10k vetores), e cache-friendly.
type SemanticStore struct {
	concepts []Concept
}

// NewSemanticStore cria um store vazio.
func NewSemanticStore() *SemanticStore { return &SemanticStore{} }

// Add adiciona um conceito com embedding determinístico (feature hashing).
func (s *SemanticStore) Add(id, kind, text string) {
	s.concepts = append(s.concepts, Concept{
		ID:     id,
		Kind:   kind,
		Text:   text,
		Vector: featureVector(text),
	})
}

// AddVector adiciona um conceito com vetor pré-computado (ex: embedding real).
func (s *SemanticStore) AddVector(c Concept) {
	if c.Vector != nil {
		normalize(c.Vector)
	}
	s.concepts = append(s.concepts, c)
}

// Search devolve os k conceitos mais similares a q (cosseno), ordenados.
// Zero alocação no caminho quente além do resultado.
func (s *SemanticStore) Search(q Vector, k int) []Hit {
	if k <= 0 || len(s.concepts) == 0 {
		return nil
	}
	if k > len(s.concepts) {
		k = len(s.concepts)
	}

	hits := make([]Hit, 0, k)
	for i := range s.concepts {
		score := dot(q, s.concepts[i].Vector)
		if len(hits) < k {
			hits = append(hits, Hit{Concept: s.concepts[i], Score: score})
			// mantém ordenado desc por score (inserção simples; k é pequeno)
			for j := len(hits) - 1; j > 0 && hits[j].Score > hits[j-1].Score; j-- {
				hits[j], hits[j-1] = hits[j-1], hits[j]
			}
			continue
		}
		// só substitui se for melhor que o pior atual (top-k)
		if score > hits[len(hits)-1].Score {
			hits[len(hits)-1] = Hit{Concept: s.concepts[i], Score: score}
			for j := len(hits) - 1; j > 0 && hits[j].Score > hits[j-1].Score; j-- {
				hits[j], hits[j-1] = hits[j-1], hits[j]
			}
		}
	}
	return hits
}

// SearchText embute o texto (determinístico) e busca.
func (s *SemanticStore) SearchText(text string, k int) []Hit {
	return s.Search(featureVector(text), k)
}

// Size devolve quantos conceitos o store contém.
func (s *SemanticStore) Size() int { return len(s.concepts) }

// best devolve o conceito mais similar a q, ou ok=false se vazio.
func (s *SemanticStore) best(q Vector) (Concept, bool) {
	if len(s.concepts) == 0 {
		return Concept{}, false
	}
	best := s.concepts[0]
	bestScore := dot(q, best.Vector)
	for i := 1; i < len(s.concepts); i++ {
		if sc := dot(q, s.concepts[i].Vector); sc > bestScore {
			best, bestScore = s.concepts[i], sc
		}
	}
	return best, true
}

// byScore ordena hits desc por score (usado pelo benchmark/teste).
type byScore []Hit

func (h byScore) Len() int           { return len(h) }
func (h byScore) Less(i, j int) bool { return h[i].Score > h[j].Score }
func (h byScore) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

var _ = sort.Sort // referência estática (byScore usado em testes)
