package domain

// Balance é o saldo de um ativo na conta (moeda de margem ou do próprio ativo).
type Balance struct {
	Asset  string  `json:"asset"`
	Free   float64 `json:"free"`
	Locked float64 `json:"locked"`
}

// Total devolve o saldo total (livre + travado em ordens abertas).
func (b Balance) Total() float64 {
	return b.Free + b.Locked
}
