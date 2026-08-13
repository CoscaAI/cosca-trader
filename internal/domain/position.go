package domain

import "time"

// Position é a exposição acumulada em um ativo (soma dos fills da mesma
// direção). O controle de risco opera sobre ela.
type Position struct {
	Symbol        string    `json:"symbol"`
	Exchange      string    `json:"exchange"`
	Side          Side      `json:"side"`
	Quantity      float64   `json:"quantity"`
	AvgEntryPrice float64   `json:"avg_entry_price"`
	MarkPrice     float64   `json:"mark_price,omitempty"`
	UnrealizedPnL float64   `json:"unrealized_pnl"`
	RealizedPnL   float64   `json:"realized_pnl"`
	OpenedAt      time.Time `json:"opened_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// IsOpen devolve se há quantidade em posição.
func (p Position) IsOpen() bool {
	return p.Quantity > 0
}

// UnrealizedPnLAt calcula o PnL não realizado dado um preço de mercado.
// Para posição comprada (long): (mark - entry) * qty. Vendida (short): inverso.
func (p Position) UnrealizedPnLAt(mark float64) float64 {
	if p.Side == SideSell {
		return (p.AvgEntryPrice - mark) * p.Quantity
	}
	return (mark - p.AvgEntryPrice) * p.Quantity
}
