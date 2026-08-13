package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Position é a exposição acumulada em um ativo (soma dos fills da mesma
// direção). O controle de risco opera sobre ela. Dinheiro em decimal.Decimal.
//
// RealizedPnL é o PnL realizado ACUMULADO do par (símbolo+exchange) — sobrevive
// ao fechamento e reabertura de posição (nunca é zerado).
type Position struct {
	Symbol        string          `json:"symbol"`
	Exchange      string          `json:"exchange"`
	Side          Side            `json:"side"`
	Quantity      decimal.Decimal `json:"quantity"`
	AvgEntryPrice decimal.Decimal `json:"avg_entry_price"`
	MarkPrice     decimal.Decimal `json:"mark_price"`
	RealizedPnL   decimal.Decimal `json:"realized_pnl"`
	OpenedAt      time.Time       `json:"opened_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// IsOpen devolve se há quantidade em posição.
func (p Position) IsOpen() bool {
	return p.Quantity.Sign() > 0
}

// UnrealizedPnLAt calcula o PnL não realizado dado um preço de mercado.
// Para posição comprada (long): (mark - entry) * qty. Vendida (short): inverso.
func (p Position) UnrealizedPnLAt(mark decimal.Decimal) decimal.Decimal {
	if p.Side == SideSell {
		return p.AvgEntryPrice.Sub(mark).Mul(p.Quantity)
	}
	return mark.Sub(p.AvgEntryPrice).Mul(p.Quantity)
}
