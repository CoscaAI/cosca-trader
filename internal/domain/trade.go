package domain

import "time"

// Trade é um fill executado — a menor unidade de liquidez do journal. Cada
// ordem preenchida gera um ou mais trades.
type Trade struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"order_id"`
	Symbol    string    `json:"symbol"`
	Exchange  string    `json:"exchange"`
	Side      Side      `json:"side"`
	Price     float64   `json:"price"`
	Quantity  float64   `json:"quantity"`
	QuoteQty  float64   `json:"quote_qty,omitempty"`
	Fee       float64   `json:"fee"`
	FeeAsset  string    `json:"fee_asset,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
