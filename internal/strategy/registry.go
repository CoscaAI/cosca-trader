// registry.go — o catálogo de estratégias (Fase 5): nome → fábrica. O scanner
// usa o registry para avaliar TODAS as estratégias; o seletor adaptativo usa
// para trocar a ativa quando o mercado muda. Adicionar uma estratégia = criar
// o arquivo + registrar aqui (um único lugar).
package strategy

import (
	"fmt"
	"sort"
)

// Factory cria uma instância nova da estratégia (estado zerado).
type Factory func() Strategy

// registry é o catálogo interno nome → fábrica.
var registry = map[string]Factory{
	"ema-cross":     func() Strategy { return NewEMACross() },
	"momentum":      func() Strategy { return NewMomentum() },
	"rsi-reversion": func() Strategy { return NewReversion() },
	"breakout":      func() Strategy { return NewBreakout() },
}

// Register adiciona uma estratégia ao catálogo (permite plugins).
func Register(name string, f Factory) {
	if f != nil {
		registry[name] = f
	}
}

// Get devolve a fábrica de uma estratégia pelo nome.
func Get(name string) (Factory, bool) {
	f, ok := registry[name]
	return f, ok
}

// NewByName cria uma instância nova da estratégia pelo nome.
func NewByName(name string) (Strategy, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("estratégia desconhecida: %q (disponíveis: %s)", name, Names())
	}
	return f(), nil
}

// Names devolve os nomes das estratégias registradas, ordenados.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
