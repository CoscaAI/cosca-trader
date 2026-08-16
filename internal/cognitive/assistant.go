package cognitive

import (
	"context"
	"fmt"
	"strings"
)

// Snapshot é o estado corrente do trader que o assistente "lê" para responder.
// O MOTOR continua Go puro; o snapshot é só o resumo textual que o copiloto
// recebe para raciocinar.
type Snapshot struct {
	Mode          string // paper | testnet | live | observe
	Strategy      string // nome da estratégia ativa (ou "desligada")
	Equity        string // equity corrente (formatado)
	Drawdown      string // drawdown % (formatado)
	Paused        bool   // risk manager pausou o trading?
	Positions     string // resumo das posições abertas (ou "nenhuma")
	MacroRegime   string // risk-on | risk-off | cautela | desconhecido
	RecentSignals string // últimos sinais da estratégia (ou "nenhum")
}

// Assistant é o copiloto cognitivo: lê o snapshot, monta o contexto e pergunta
// ao modelo. Sem modelo (off), devolve uma resposta determinística — nunca
// trava a operação por falta de LLM.
type Assistant struct {
	model    Model
	registry *Registry
}

// NewAssistant cria o copiloto com um modelo (nil = off).
func NewAssistant(m Model) *Assistant {
	return &Assistant{model: m}
}

// WithRegistry anexa o registry (para o endpoint /models do header).
func (a *Assistant) WithRegistry(r *Registry) *Assistant {
	a.registry = r
	return a
}

// Registry devolve o registry anexado (nil se não houver).
func (a *Assistant) Registry() *Registry { return a.registry }

// Model devolve o modelo ativo (nil = off) — para o /chat reportar qual
// modelo está respondendo.
func (a *Assistant) Model() Model { return a.model }

// SetModel troca o modelo ativo (nil = off) — o header usa para trocar.
func (a *Assistant) SetModel(m Model) { a.model = m }

// Ask responde a pergunta do Don sobre o estado corrente.
func (a *Assistant) Ask(ctx context.Context, snap Snapshot, question string) (string, error) {
	if a.model == nil {
		return offlineReply(snap, question), nil
	}
	return a.model.Generate(ctx, buildSystemPrompt(snap), question)
}

// buildSystemPrompt monta o contexto do copiloto — identidade + estado vivo.
// O modelo NÃO executa nada; só raciocina sobre o texto.
func buildSystemPrompt(s Snapshot) string {
	var b strings.Builder
	b.WriteString("Você é o copiloto cognitivo do COSCA TRADER, uma plataforma de trade ")
	b.WriteString("profissional de cripto. Você NÃO executa ordens — apenas explica e ")
	b.WriteString("raciocina sobre o estado do sistema. Responda em português do Brasil, ")
	b.WriteString("de forma direta e honesta, sem enrolação. Se não souber, diga que não sabe.\n\n")
	b.WriteString("ESTADO ATUAL DO SISTEMA:\n")
	fmt.Fprintf(&b, "- Modo: %s\n", s.Mode)
	fmt.Fprintf(&b, "- Estratégia: %s\n", s.Strategy)
	fmt.Fprintf(&b, "- Equity: %s\n", s.Equity)
	fmt.Fprintf(&b, "- Drawdown: %s\n", s.Drawdown)
	if s.Paused {
		b.WriteString("- Trading: PAUSADO (risk manager travou)\n")
	} else {
		b.WriteString("- Trading: ativo\n")
	}
	fmt.Fprintf(&b, "- Posições: %s\n", s.Positions)
	fmt.Fprintf(&b, "- Regime macro: %s\n", s.MacroRegime)
	fmt.Fprintf(&b, "- Últimos sinais: %s\n", s.RecentSignals)
	return b.String()
}

// offlineReply é a resposta determinística quando não há LLM configurado —
// o copiloto nunca fica mudo, só menos eloquente.
func offlineReply(s Snapshot, question string) string {
	q := strings.ToLower(question)
	switch {
	case strings.Contains(q, "risco") || strings.Contains(q, "drawdown"):
		if s.Paused {
			return fmt.Sprintf("O trading está PAUSADO pelo risk manager. Drawdown atual: %s. Para destravar, revise o risco (GET /risk) e retome manualmente.", s.Drawdown)
		}
		return fmt.Sprintf("Trading ativo. Drawdown: %s. Equity: %s. O risk manager trava sozinho se o drawdown cruzar o limite.", s.Drawdown, s.Equity)
	case strings.Contains(q, "posi") || strings.Contains(q, "posição"):
		return fmt.Sprintf("Posições abertas: %s", s.Positions)
	case strings.Contains(q, "regime") || strings.Contains(q, "macro") || strings.Contains(q, "mercado"):
		return fmt.Sprintf("Regime macro atual: %s.", s.MacroRegime)
	case strings.Contains(q, "estrat") || strings.Contains(q, "sinal"):
		return fmt.Sprintf("Estratégia ativa: %s. Últimos sinais: %s", s.Strategy, s.RecentSignals)
	default:
		return fmt.Sprintf("Copiloto em modo OFF (nenhum LLM configurado). Para ligar, escolha um provider e modelo no header. Estado atual: modo %s, estratégia %s, equity %s, drawdown %s, regime %s.", s.Mode, s.Strategy, s.Equity, s.Drawdown, s.MacroRegime)
	}
}
