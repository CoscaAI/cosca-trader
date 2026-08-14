// Package engine compõe o núcleo headless do COSCA TRADER: o Event Bus
// (reações internas por tipo), o Event Store + SQLite (persistência/rastro),
// e o Hub (streaming em tempo real). Um único método Emit atravessa os três —
// é o ponto único de entrada de todo acontecimento da plataforma.
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
//
// Retorna erro SOMENTE para eventos de DINHEIRO cuja persistência falhou —
// o chamador (OMS) falha-parado: nunca publica um evento de dinheiro que não
// está no rastro durável. Eventos de market data (tick/candle) e de sistema
// continuam tolerantes à falha de persistência (logam e seguem), porque um
// tick perdido não compromete a corretude do dinheiro.
func (e *Engine) Emit(ev event.Event) error {
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
			if isMoneyEvent(ev.Type) {
				return fmt.Errorf("persistir evento de dinheiro %s: %w", ev.Type, err)
			}
			// Nunca descartar erro de persistência em silêncio: o rastro em
			// memória e o disco podem divergir.
			log.Printf("⚠ engine: persistência de evento %s falhou: %v", ev.Type, err)
		}
	}
	e.Hub.Publish(ev)
	e.Bus.Publish(ev)
	return nil
}

// isMoneyEvent classifica os eventos cuja persistência é OBRIGATÓRIA: se o
// rastro durável falhar, o sistema falha-parado em vez de fingir que o evento
// aconteceu. Market data fica de fora (tolerante a perda de ticks/velas).
func isMoneyEvent(t event.Type) bool {
	switch t {
	case event.OrderCreated, event.OrderSubmitted, event.OrderPartiallyFilled,
		event.OrderFilled, event.OrderCanceled, event.OrderRejected, event.OrderExpired,
		event.TradeExecuted,
		event.PositionOpened, event.PositionUpdated, event.PositionClosed,
		event.BalanceUpdated,
		event.RiskBreach, event.RiskWarning:
		return true
	}
	return false
}

// NewID gera um identificador único (hex de 8 bytes aleatórios + nanos).
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(b)
}
