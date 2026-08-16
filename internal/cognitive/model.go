package cognitive

import (
	"context"
	"time"
)

// model.go — as abstrações de modelo (padrão Vercel LanguageModelV4).
//
// Um Model é identificado por provider + modelId (a string "provider:modelo").
// A Capability descreve a capacidade do modelo (para o modal de seleção do
// header): contexto, modalidades, streaming, visão, raciocínio.

// Capability descreve a capacidade de um modelo.
type Capability struct {
	ContextWindow int      `json:"context_window"` // tokens de contexto
	Modalities    []string `json:"modalities"`     // "text", "embedding", "image", ...
	Streaming     bool     `json:"streaming"`
	Vision        bool     `json:"vision"`
	Reasoning     bool     `json:"reasoning"`
}

// Has devolve true se o modelo suporta a modalidade dada.
func (c Capability) Has(modality string) bool {
	for _, m := range c.Modalities {
		if m == modality {
			return true
		}
	}
	return false
}

// ModelInfo é o metadado de um modelo listado (para o header).
type ModelInfo struct {
	ID         string        `json:"id"`         // "qwen2.5-coder:14b"
	Provider   string        `json:"provider"`   // "ollama"
	Capability Capability    `json:"capability"` // contexto, modalidades, ...
	Latency    time.Duration `json:"latency"`    // tempo de resposta medido (0 = não medido)
	Available  bool          `json:"available"`  // está respondendo agora?
}

// Model é a interface de um modelo de linguagem.
type Model interface {
	Provider() string
	ID() string
	Generate(ctx context.Context, system, user string) (string, error)
	Capability() Capability
}

// EmbeddingModel é a interface de um modelo de embedding (vetores semânticos).
type EmbeddingModel interface {
	Provider() string
	ID() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
