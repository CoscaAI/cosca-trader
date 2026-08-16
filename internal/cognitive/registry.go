package cognitive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// registry.go — o registro de providers e modelos (padrão createProviderRegistry).
//
// Resolve a string "provider:modelo" → Model e lista os modelos de todos os
// providers. Também MEDE a latência de cada modelo (o "analisar capacidade"
// do header): um ping curto que reporta o tempo de resposta real.

// Registry resolve "provider:modelo" e lista modelos.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	order     []string
	active    string // "provider:modelo" ativo (para o chat)
}

// NewRegistry cria um registry vazio.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register registra um provider (substitui se o nome já existir).
func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[p.Name()]; !ok {
		r.order = append(r.order, p.Name())
	}
	r.providers[p.Name()] = p
}

// Providers devolve os nomes dos providers registrados, em ordem.
func (r *Registry) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Provider devolve um provider pelo nome.
func (r *Registry) Provider(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Resolve resolve "provider:modelo" (ou "provider/modelo") → Model.
func (r *Registry) Resolve(id string) (Model, error) {
	provider, modelID, err := splitModelID(id)
	if err != nil {
		return nil, err
	}
	p, ok := r.Provider(provider)
	if !ok {
		return nil, fmt.Errorf("provider desconhecido: %q (disponíveis: %s)", provider, strings.Join(r.Providers(), ", "))
	}
	return p.LanguageModel(modelID)
}

// ResolveEmbedding resolve um modelo de embedding pelo id.
func (r *Registry) ResolveEmbedding(id string) (EmbeddingModel, error) {
	provider, modelID, err := splitModelID(id)
	if err != nil {
		return nil, err
	}
	p, ok := r.Provider(provider)
	if !ok {
		return nil, fmt.Errorf("provider desconhecido: %q", provider)
	}
	return p.EmbeddingModel(modelID)
}

// SetActive define o modelo ativo (para o chat) por id.
func (r *Registry) SetActive(id string) error {
	if _, err := r.Resolve(id); err != nil {
		return err
	}
	r.mu.Lock()
	r.active = id
	r.mu.Unlock()
	return nil
}

// Active devolve o id do modelo ativo ("" se nenhum).
func (r *Registry) Active() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// ListModels lista os modelos de todos os providers, com latência medida.
// O ping é leve (uma pergunta de 2 tokens) e paralelo.
func (r *Registry) ListModels(ctx context.Context, measureLatency bool) []ModelInfo {
	names := r.Providers()
	var (
		mu   sync.Mutex
		all  []ModelInfo
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 8) // limita concorrência
	)
	for _, name := range names {
		p, ok := r.Provider(name)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			infos, err := p.ListModels(ctx)
			if err != nil {
				mu.Lock()
				all = append(all, ModelInfo{ID: "(erro)", Provider: p.Name(), Available: false})
				mu.Unlock()
				return
			}
			if measureLatency {
				for i := range infos {
					infos[i].Latency = measureOne(ctx, p, infos[i].ID)
				}
			}
			mu.Lock()
			all = append(all, infos...)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	sort.Slice(all, func(i, j int) bool {
		if all[i].Provider != all[j].Provider {
			return all[i].Provider < all[j].Provider
		}
		return all[i].ID < all[j].ID
	})
	return all
}

// measureOne mede a latência de um modelo com um ping mínimo ("ping").
func measureOne(ctx context.Context, p Provider, modelID string) time.Duration {
	m, err := p.LanguageModel(modelID)
	if err != nil {
		return 0
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := time.Now()
	_, err = m.Generate(cctx, "Responda com uma única palavra: pong", "ping")
	if err != nil {
		return 0 // 0 = não medido (erro/timeout)
	}
	return time.Since(start)
}

// splitModelID separa "provider:modelo" em (provider, modelo). Aceita ":" ou "/".
func splitModelID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	sep := strings.IndexAny(id, ":/")
	if sep < 0 {
		return "", "", fmt.Errorf("id de modelo inválido %q — use \"provider:modelo\"", id)
	}
	provider := id[:sep]
	modelID := strings.TrimPrefix(id[sep+1:], "/")
	if provider == "" || modelID == "" {
		return "", "", fmt.Errorf("id de modelo inválido %q", id)
	}
	return provider, modelID, nil
}
