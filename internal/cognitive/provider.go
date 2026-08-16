package cognitive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// provider.go — a abstração de Provider (padrão Vercel ProviderV4) + o cliente
// OpenAI-compatível único.
//
// ollama, openai e deepseek falam o MESMO protocolo (/chat/completions com o
// shape de mensagens system/user/assistant). Um único cliente openAICompat
// serve os três — só mudam a base URL e a API key. Isso elimina o código
// duplicado (o antigo ollama.go + openai.go viravam dois caminhos separados).

// Provider é uma fábrica de modelos por modalidade.
type Provider interface {
	Name() string
	LanguageModel(id string) (Model, error)
	EmbeddingModel(id string) (EmbeddingModel, error)
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// openAICompat é o cliente OpenAI-compatível único.
type openAICompat struct {
	name   string
	base   string // "http://localhost:11434/v1" | "https://api.openai.com/v1" | ...
	apiKey string
	httpc  *http.Client
}

// NewOpenAICompatible cria um provider OpenAI-compatível (ollama/openai/deepseek).
func NewOpenAICompatible(name, base, apiKey string) Provider {
	base = strings.TrimRight(base, "/")
	return &openAICompat{
		name:   name,
		base:   base,
		apiKey: apiKey,
		httpc:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *openAICompat) Name() string { return o.name }

// LanguageModel devolve o modelo pelo id. A validação de existência acontece
// no ListModels (não há erro de "modelo desconhecido" no caminho quente — o
// provider tenta e o erro HTTP 404/400 informa).
func (o *openAICompat) LanguageModel(id string) (Model, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%s: model id vazio", o.name)
	}
	return &compatModel{p: o, id: id}, nil
}

// EmbeddingModel devolve um modelo de embedding pelo id (endpoint /embeddings).
func (o *openAICompat) EmbeddingModel(id string) (EmbeddingModel, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%s: embedding model id vazio", o.name)
	}
	return &compatEmbedding{p: o, id: id}, nil
}

// ListModels lista os modelos do provider.
//   - ollama: GET {base}/../api/tags (o base já termina em /v1)
//   - openai/deepseek: catálogo estático (não dependem de chave pra listar o que existe)
func (o *openAICompat) ListModels(ctx context.Context) ([]ModelInfo, error) {
	if o.name == "ollama" {
		return o.listOllama(ctx)
	}
	return o.listStatic(ctx), nil
}

// listOllama consulta /api/tags do ollama.
func (o *openAICompat) listOllama(ctx context.Context) ([]ModelInfo, error) {
	// base = http://host:11434/v1 → tags em http://host:11434/api/tags
	tagsURL := strings.TrimSuffix(o.base, "/v1") + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama HTTP %d", resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name       string `json:"name"`
			ModifiedAt string `json:"modified_at"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	infos := make([]ModelInfo, 0, len(out.Models))
	for _, m := range out.Models {
		infos = append(infos, ModelInfo{
			ID:         m.Name,
			Provider:   o.name,
			Capability: defaultCapability(m.Name),
			Available:  true,
		})
	}
	return infos, nil
}

// listStatic devolve o catálogo estático (openai/deepseek). O usuário só
// consegue usar com a API key; a lista é informativa.
func (o *openAICompat) listStatic(ctx context.Context) []ModelInfo {
	switch o.name {
	case "openai":
		return staticModels(o.name, []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "o3-mini"})
	case "deepseek":
		return staticModels(o.name, []string{"deepseek-chat", "deepseek-reasoner"})
	default:
		return nil
	}
}

func staticModels(provider string, ids []string) []ModelInfo {
	out := make([]ModelInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, ModelInfo{
			ID:         id,
			Provider:   provider,
			Capability: defaultCapability(id),
			Available:  true,
		})
	}
	return out
}

// defaultCapability infere capacidade básica a partir do nome do modelo.
func defaultCapability(id string) Capability {
	c := Capability{ContextWindow: 8192, Modalities: []string{"text"}, Streaming: true}
	switch {
	case strings.Contains(id, "embed"):
		c.Modalities = []string{"embedding"}
		c.Streaming = false
	case strings.Contains(id, "vision") || strings.Contains(id, "llava"):
		c.Vision = true
		c.Modalities = append(c.Modalities, "image")
	case strings.Contains(id, "reason") || strings.Contains(id, "o3") || strings.Contains(id, "o1"):
		c.Reasoning = true
	}
	if strings.Contains(id, "128k") {
		c.ContextWindow = 131072
	} else if strings.Contains(id, "32k") {
		c.ContextWindow = 32768
	} else if strings.Contains(id, "14b") || strings.Contains(id, "gpt-4o") || strings.Contains(id, "deepseek") {
		c.ContextWindow = 32768
	}
	return c
}

// compatModel é o Model concreto do openAICompat.
type compatModel struct {
	p  *openAICompat
	id string
}

func (m *compatModel) Provider() string { return m.p.name }
func (m *compatModel) ID() string       { return m.id }
func (m *compatModel) Capability() Capability {
	return defaultCapability(m.id)
}

func (m *compatModel) Generate(ctx context.Context, system, user string) (string, error) {
	if m.p.name != "ollama" && m.p.apiKey == "" {
		return "", fmt.Errorf("%s: API key não definida", m.p.name)
	}
	payload := map[string]any{
		"model": m.id,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.p.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.p.apiKey)
	}
	resp, err := m.p.httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: %w", m.p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("%s HTTP %d: %s", m.p.name, resp.StatusCode, string(b))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s: resposta vazia", m.p.name)
	}
	return out.Choices[0].Message.Content, nil
}

// compatEmbedding é o EmbeddingModel concreto do openAICompat (endpoint /embeddings).
type compatEmbedding struct {
	p  *openAICompat
	id string
}

func (e *compatEmbedding) Provider() string { return e.p.name }
func (e *compatEmbedding) ID() string       { return e.id }

func (e *compatEmbedding) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.p.name != "ollama" && e.p.apiKey == "" {
		return nil, fmt.Errorf("%s: API key não definida", e.p.name)
	}
	payload := map[string]any{"model": e.id, "input": texts}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.p.base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.p.apiKey)
	}
	resp, err := e.p.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s HTTP %d: %s", e.p.name, resp.StatusCode, string(b))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, 0, len(out.Data))
	for _, d := range out.Data {
		vecs = append(vecs, d.Embedding)
	}
	return vecs, nil
}
