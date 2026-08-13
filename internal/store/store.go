// Package store é o Event Store do COSCA TRADER — um log append-only de todos
// os eventos, a fonte de verdade para reconstruir o estado (posições, ordens,
// journal) em QUALQUER ponto do tempo. "Como a plataforma chegou aqui?" =
// fold(events ≤ T). O SQLite (db.go) persiste o mesmo log em disco.
//
// Padrão herdado do cosca-node (event store append-only + time travel).
package store

import (
	"sync"

	"github.com/CoscaAI/cosca-trader/internal/event"
)

// Store é um log append-only de eventos em ordem cronológica.
type Store struct {
	mu     sync.RWMutex
	events []event.Event
}

// New cria o store vazio.
func New() *Store {
	return &Store{}
}

// Append adiciona um evento ao fim do log.
func (s *Store) Append(ev event.Event) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
}

// All devolve uma cópia ordenada de todos os eventos.
func (s *Store) All() []event.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]event.Event, len(s.events))
	copy(out, s.events)
	return out
}

// Len devolve o nº de eventos armazenados.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.events)
}

// TimeRange devolve o timestamp do primeiro e do último evento.
func (s *Store) TimeRange() (first, last string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return "", ""
	}
	return s.events[0].Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00"),
		s.events[len(s.events)-1].Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00")
}
