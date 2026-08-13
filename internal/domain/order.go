package domain

import "time"

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

// Order é uma ordem de compra/venda. É a entidade central do OMS.
type Order struct {
	ID            string      `json:"id"`
	ClientOrderID string      `json:"client_order_id"`
	Symbol        string      `json:"symbol"`
	Exchange      string      `json:"exchange"`
	Side          Side        `json:"side"`
	Type          OrderType   `json:"type"`
	Price         float64     `json:"price"`
	StopPrice     float64     `json:"stop_price,omitempty"`
	Quantity      float64     `json:"quantity"`
	FilledQty     float64     `json:"filled_qty"`
	AvgFillPrice  float64     `json:"avg_fill_price"`
	Status        OrderStatus `json:"status"`
	TimeInForce   TimeInForce `json:"time_in_force"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// IsOpen devolve se a ordem ainda está ativa (não terminou).
func (o Order) IsOpen() bool {
	return o.Status == OrderNew || o.Status == OrderPartiallyFilled
}

// IsClosed devolve se a ordem atingiu um estado terminal.
func (o Order) IsClosed() bool {
	return !o.IsOpen()
}
