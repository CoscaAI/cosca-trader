package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Trade é um fill executado — a menor unidade de liquidez do journal. Cada
// ordem preenchida gera um ou mais trades.
type Trade struct {
	ID        string          `json:"id"`
	OrderID   string          `json:"order_id"`
	Symbol    string          `json:"symbol"`
	Exchange  string          `json:"exchange"`
	Side      Side            `json:"side"`
	Price     decimal.Decimal `json:"price"`
	Quantity  decimal.Decimal `json:"quantity"`
	QuoteQty  decimal.Decimal `json:"quote_qty"`
	Fee       decimal.Decimal `json:"fee"`
	FeeAsset  string          `json:"fee_asset"`
	Timestamp time.Time       `json:"timestamp"`
}
