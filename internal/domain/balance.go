package domain

import "github.com/shopspring/decimal"

// Balance é o saldo de um ativo na conta (moeda de margem ou do próprio ativo).
type Balance struct {
	Asset  string          `json:"asset"`
	Free   decimal.Decimal `json:"free"`
	Locked decimal.Decimal `json:"locked"`
}

// Total devolve o saldo total (livre + travado em ordens abertas).
func (b Balance) Total() decimal.Decimal {
	return b.Free.Add(b.Locked)
}
