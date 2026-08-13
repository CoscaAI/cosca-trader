// Package stream é o barramento de tempo real do COSCA TRADER: um pub/sub em
// memória que transmite eventos para os assinantes (SSE no HTTP, WebSocket
// depois). A UI assina e recebe cada tick/ordem/fill no instante em que ocorre.
//
// Padrão herdado do cosca-node (hub pub/sub com descarte para assinante lento).
package stream

import (
	"sync"

	"github.com/CoscaAI/cosca-trader/internal/event"
)

// Hub é o barramento pub/sub de eventos.
type Hub struct {
	mu   sync.Mutex
	subs map[chan event.Event]struct{}
}

// NewHub cria o hub.
func NewHub() *Hub {
	return &Hub{subs: map[chan event.Event]struct{}{}}
}

// Subscribe registra um assinante e devolve seu canal de eventos.
func (h *Hub) Subscribe() chan event.Event {
	ch := make(chan event.Event, 1024) // buffer para não bloquear o produtor
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe remove um assinante.
func (h *Hub) Unsubscribe(ch chan event.Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// Publish transmite um evento a todos os assinantes (não-bloqueante).
func (h *Hub) Publish(ev event.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// assinante lento: descarta (a UI não pode travar o sistema)
		}
	}
}

// SubscriberCount devolve o nº de assinantes ativos.
func (h *Hub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
