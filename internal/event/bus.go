package event

import (
	"sync"
	"time"
)

// Handler recebe eventos.
type Handler func(Event)

// subscription é uma assinatura identificada por ID (comparar func values em
// Go é não-confiável — o ID é a identidade estável para unsubscribe).
type subscription struct {
	id uint64
	h  Handler
}

// Bus é um barramento pub/sub tipado e thread-safe. Assinaturas por tipo
// exato e um canal global ("subscribe all") para observabilidade transversal.
// O dispatch é síncrono e na ordem de publicação — handlers devem ser rápidos
// e nunca bloquear (trabalho pesado vai para goroutines próprias).
type Bus struct {
	mu     sync.RWMutex
	nextID uint64
	subs   map[Type][]subscription
	all    []subscription
}

// NewBus cria um barramento vazio.
func NewBus() *Bus {
	return &Bus{subs: make(map[Type][]subscription)}
}

// Subscribe registra um handler para um tipo específico. Retorna um
// cancelador idempotente para remover a assinatura.
func (b *Bus) Subscribe(t Type, h Handler) func() {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.subs[t] = append(b.subs[t], subscription{id: id, h: h})
	b.mu.Unlock()
	return func() { b.unsubscribe(t, id) }
}

// SubscribeAll registra um handler que recebe TODOS os eventos.
func (b *Bus) SubscribeAll(h Handler) func() {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.all = append(b.all, subscription{id: id, h: h})
	b.mu.Unlock()
	return func() { b.unsubscribeAll(id) }
}

// Publish entrega o evento aos assinantes do tipo e aos globais, na ordem de
// registro. Preenche Timestamp se zero.
func (b *Bus) Publish(ev Event) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	b.mu.RLock()
	handlers := append([]subscription(nil), b.subs[ev.Type]...)
	all := append([]subscription(nil), b.all...)
	b.mu.RUnlock()

	for _, s := range handlers {
		s.h(ev)
	}
	for _, s := range all {
		s.h(ev)
	}
}

// SubscriberCount devolve o total de assinaturas (por tipo + globais).
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := len(b.all)
	for _, hs := range b.subs {
		n += len(hs)
	}
	return n
}

func (b *Bus) unsubscribe(t Type, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	hs := b.subs[t]
	for i, s := range hs {
		if s.id == id {
			b.subs[t] = append(hs[:i], hs[i+1:]...)
			return
		}
	}
}

func (b *Bus) unsubscribeAll(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.all {
		if s.id == id {
			b.all = append(b.all[:i], b.all[i+1:]...)
			return
		}
	}
}
