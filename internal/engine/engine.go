// Package engine compõe o núcleo headless do COSCA TRADER: o Event Bus
// (reações internas por tipo), o Event Store + SQLite (persistência/rastro),
// e o Hub (streaming em tempo real). Um único método Emit atravessa os três —
// é o ponto único de entrada de todo acontecimento da plataforma.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/store"
	"github.com/CoscaAI/cosca-trader/internal/stream"
)

// Engine é a composição raiz do core.
type Engine struct {
	Bus   *event.Bus
	Store *store.Store
	Hub   *stream.Hub
	DB    *store.DB
}

// New monta o núcleo. db pode ser nil (modo sem persistência, para testes).
func New(db *store.DB) *Engine {
	return &Engine{
		Bus:   event.NewBus(),
		Store: store.New(),
		Hub:   stream.NewHub(),
		DB:    db,
	}
}

// Emit é o caminho único de um evento: preenche defaults, persiste no log
// (memória + SQLite) e publica no hub (streaming) e no bus (handlers internos).
// A ordem é deliberada: persistir ANTES de publicar garante que nenhum
// consumidor observe um evento que não está no rastro.
func (e *Engine) Emit(ev event.Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	if ev.ID == "" {
		ev.ID = NewID()
	}
	if ev.Severity == "" {
		ev.Severity = event.SeverityInfo
	}

	e.Store.Append(ev)
	if e.DB != nil {
		if err := e.DB.PersistEvent(ev); err != nil {
			// Nunca descartar erro de persistência em silêncio: o rastro em
			// memória e o disco podem divergir.
			log.Printf("⚠ engine: persistência de evento %s falhou: %v", ev.Type, err)
		}
	}
	e.Hub.Publish(ev)
	e.Bus.Publish(ev)
}

// NewID gera um identificador único (hex de 8 bytes aleatórios + nanos).
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(b)
}
