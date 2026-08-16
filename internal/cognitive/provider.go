// Package cognitive é o assistente IA do cosca-trader (Fase 6 — o copiloto,
// não o piloto). O MOTOR (estratégia/risco/OMS) continua Go puro e
// determinístico; a IA só CONVERSA e RACIOCINA — explica decisões, lê o
// estado e responde ao Don. O modelo é plugável: ollama (local, grátis) ou
// OpenAI (nuvem). Sem provider configurado, o assistente fica em modo
// "off" e devolve uma resposta determinística.
package cognitive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Provider completa um chat (system + user) e devolve a resposta.
type Provider interface {
	Complete(ctx context.Context, system, user string) (string, error)
	Name() string
}

// Config seleciona o provider e o modelo.
type Config struct {
	Kind   string // "off" | "ollama" | "openai"
	Model  string
	APIKey string
	Base   string // base URL (ollama default: http://localhost:11434)
}

// New monta o provider conforme a configuração. Kind vazio/"off" → nil (off).
func New(cfg Config) Provider {
	switch cfg.Kind {
	case "openai":
		return &openAI{model: cfg.Model, apiKey: cfg.APIKey, httpc: &http.Client{Timeout: 60 * time.Second}}
	case "ollama":
		base := cfg.Base
		if base == "" {
			base = "http://localhost:11434"
		}
		if cfg.Model == "" {
			cfg.Model = "qwen2.5-coder:7b"
		}
		return &ollama{model: cfg.Model, base: base, httpc: &http.Client{Timeout: 120 * time.Second}}
	default:
		return nil
	}
}

// ── Ollama (local, grátis) ──────────────────────────────────────────────────

type ollama struct {
	model string
	base  string
	httpc *http.Client
}

func (o *ollama) Name() string { return "ollama:" + o.model }

func (o *ollama) Complete(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{
		"model":  o.model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Message.Content, nil
}

// ── OpenAI (nuvem) ─────────────────────────────────────────────────────────

type openAI struct {
	model  string
	apiKey string
	httpc  *http.Client
}

func (o *openAI) Name() string { return "openai:" + o.model }

func (o *openAI) Complete(ctx context.Context, system, user string) (string, error) {
	if o.apiKey == "" {
		return "", fmt.Errorf("openai: OPENAI_API_KEY não definida")
	}
	payload := map[string]any{
		"model": o.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, string(b))
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
		return "", fmt.Errorf("openai: resposta vazia")
	}
	return out.Choices[0].Message.Content, nil
}
