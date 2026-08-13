package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Side é a direção de uma ordem ou posição.
type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// OrderType classifica a ordem.
type OrderType string

const (
	OrderMarket     OrderType = "market"
	OrderLimit      OrderType = "limit"
	OrderStop       OrderType = "stop"
	OrderStopLimit  OrderType = "stop_limit"
	OrderStopMarket OrderType = "stop_market"
)

// Valid devolve se o tipo de ordem é conhecido.
func (t OrderType) Valid() bool {
	switch t {
	case OrderMarket, OrderLimit, OrderStop, OrderStopLimit, OrderStopMarket:
		return true
	}
	return false
}

// OrderStatus é o estado de vida de uma ordem.
type OrderStatus string

const (
	OrderNew             OrderStatus = "new"
	OrderPartiallyFilled OrderStatus = "partially_filled"
	OrderFilled          OrderStatus = "filled"
	OrderCanceled        OrderStatus = "canceled"
	OrderRejected        OrderStatus = "rejected"
	OrderExpired         OrderStatus = "expired"
)

// TimeInForce define a validade temporal da ordem.
type TimeInForce string

const (
	TIFGTC TimeInForce = "GTC" // good till canceled
	TIFIOC TimeInForce = "IOC" // immediate or cancel
	TIFFOK TimeInForce = "FOK" // fill or kill
)

// Valid devolve se o TimeInForce é conhecido.
func (t TimeInForce) Valid() bool {
	switch t {
	case TIFGTC, TIFIOC, TIFFOK:
		return true
	}
	return false
}

// Order é uma ordem de compra/venda. É a entidade central do OMS.
// Dinheiro (preço/quantidade) é decimal.Decimal — nunca float (Fintech #1).
type Order struct {
	ID            string          `json:"id"`
	ClientOrderID string          `json:"client_order_id"`
	Symbol        string          `json:"symbol"`
	Exchange      string          `json:"exchange"`
	Side          Side            `json:"side"`
	Type          OrderType       `json:"type"`
	Price         decimal.Decimal `json:"price"`
	StopPrice     decimal.Decimal `json:"stop_price"`
	Quantity      decimal.Decimal `json:"quantity"`
	FilledQty     decimal.Decimal `json:"filled_qty"`
	AvgFillPrice  decimal.Decimal `json:"avg_fill_price"`
	Status        OrderStatus     `json:"status"`
	TimeInForce   TimeInForce     `json:"time_in_force"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// IsOpen devolve se a ordem ainda está ativa (não terminou).
func (o Order) IsOpen() bool {
	return o.Status == OrderNew || o.Status == OrderPartiallyFilled
}

// IsClosed devolve se a ordem atingiu um estado terminal.
func (o Order) IsClosed() bool {
	return !o.IsOpen()
}
