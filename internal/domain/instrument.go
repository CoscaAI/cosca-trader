// Package domain define os tipos de negócio do COSCA TRADER — o vocabulário
// do trading (instrumentos, candles, ordens, posições, saldos, trades). São
// estruturas puras, sem lógica de I/O: o que a exchange envia é normalizado
// para estas formas pela camada de adaptação, e o OMS opera sobre elas.
package domain

// InstrumentType classifica o tipo de ativo.
type InstrumentType string

const (
	InstrumentSpot   InstrumentType = "spot"
	InstrumentFuture InstrumentType = "future"
	InstrumentOption InstrumentType = "option"
)

// Instrument é um ativo negociável (ex.: BTCUSDT na Binance, PETR4 na B3).
type Instrument struct {
	Symbol     string         `json:"symbol"`      // "BTCUSDT"
	Exchange   string         `json:"exchange"`    // "binance"
	BaseAsset  string         `json:"base_asset"`  // "BTC"
	QuoteAsset string         `json:"quote_asset"` // "USDT"
	Type       InstrumentType `json:"type"`
	Status     string         `json:"status"` // "trading" | "delisted"
	TickSize   float64        `json:"tick_size"`
	StepSize   float64        `json:"step_size"`
	MinQty     float64        `json:"min_qty"`
	Precision  int            `json:"precision"` // casas decimais do preço
}

// Valid devolve se o instrumento tem os campos mínimos preenchidos.
func (i Instrument) Valid() bool {
	return i.Symbol != "" && i.Exchange != "" && i.BaseAsset != "" && i.QuoteAsset != ""
}
