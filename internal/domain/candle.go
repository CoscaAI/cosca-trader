package domain

import "time"

// Candle é uma vela OHLCV agregada por intervalo (1m, 5m, 1h, 1d...).
type Candle struct {
	Symbol   string    `json:"symbol"`
	Exchange string    `json:"exchange"`
	Interval string    `json:"interval"` // "1m" | "5m" | "1h" | "1d"
	OpenTime time.Time `json:"open_time"`
	Open     float64   `json:"open"`
	High     float64   `json:"high"`
	Low      float64   `json:"low"`
	Close    float64   `json:"close"`
	Volume   float64   `json:"volume"`
	Trades   int       `json:"trades,omitempty"`
	Complete bool      `json:"complete"` // false = vela ainda em formação
}

// Key identifica unicamente uma vela no histórico.
func (c Candle) Key() string {
	return c.Symbol + ":" + c.Exchange + ":" + c.Interval
}

// Tick é o preço de mercado em um instante (menor granularidade que a vela).
type Tick struct {
	Symbol   string    `json:"symbol"`
	Exchange string    `json:"exchange"`
	Price    float64   `json:"price"`
	Quantity float64   `json:"quantity"`
	Time     time.Time `json:"time"`
}
