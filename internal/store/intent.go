package store

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// IntentStatus é o ciclo de vida de um order intent (Fase 2A).
type IntentStatus string

const (
	// IntentPending: persistido antes do envio ao broker; resultado desconhecido.
	IntentPending IntentStatus = "pending"
	// IntentSubmitted: confirmado na exchange (OrderCreated emitido).
	IntentSubmitted IntentStatus = "submitted"
	// IntentDone: a ordem atingiu estado terminal (filled/canceled/expired).
	IntentDone IntentStatus = "done"
	// IntentFailed: confirmado que a ordem NÃO existe na exchange.
	IntentFailed IntentStatus = "failed"
)

// Intent é o registro durável de uma intenção de ordem (client_order_id +
// request) persistido ANTES do envio ao broker. É a âncora da saga Fase 2A:
// se o processo cair entre o broker aceitar e o OrderCreated entrar no rastro,
// o Reconcile consulta a exchange por origClientOrderId e adota a ordem.
type Intent struct {
	ClientOrderID string           `json:"client_order_id"`
	Symbol        string           `json:"symbol"`
	Side          domain.Side      `json:"side"`
	Type          domain.OrderType `json:"type"`
	Price         decimal.Decimal  `json:"price"`
	Quantity      decimal.Decimal  `json:"quantity"`
	Status        IntentStatus     `json:"status"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

// Pending devolve se o intent ainda precisa de resolução na exchange.
func (i Intent) Pending() bool {
	return i.Status == IntentPending || i.Status == IntentSubmitted
}
