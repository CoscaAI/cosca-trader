// Package ledger implementa a contabilidade double-entry do COSCA TRADER
// (Fintech #2: "toda transação tem débito e crédito; o sistema nunca perde ou
// cria dinheiro"). Cada fill gera entradas de débito/crédito por conta de ativo
// — o saldo de cada ativo é débito − crédito, sempre explicável.
package ledger

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// Entry é uma entrada de ledger. Debit incrementa a conta; Credit decrementa.
// Por convenção, cada movimentação de ativo é expressa em débito (entra) e
// crédito (sai) na MOEDA do ativo.
type Entry struct {
	ID        string          `json:"id"`
	Account   string          `json:"account"` // "asset:BTC", "asset:USDT", "fee:BNB"
	Debit     decimal.Decimal `json:"debit"`
	Credit    decimal.Decimal `json:"credit"`
	Ref       string          `json:"ref"` // trade/order ID de origem
	Timestamp time.Time       `json:"timestamp"`
}

// Account helpers.
const (
	AssetPrefix = "asset:"
	FeePrefix   = "fee:"
)

// AssetAccount devolve o nome da conta de um ativo.
func AssetAccount(asset string) string { return AssetPrefix + asset }

// FeeAccount devolve o nome da conta de taxa de um ativo.
func FeeAccount(asset string) string { return FeePrefix + asset }

// Ledger é um livro-razão double-entry thread-safe.
type Ledger struct {
	mu       sync.RWMutex
	entries  []Entry
	balances map[string]decimal.Decimal // conta → débito − crédito
}

// New cria o ledger vazio.
func New() *Ledger {
	return &Ledger{balances: make(map[string]decimal.Decimal)}
}

// Post registra entradas (uma transação = uma ou mais entradas). A convenção
// double-entry é preservada por construção nas entradas geradas pelo OMS.
func (l *Ledger) Post(entries ...Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range entries {
		l.entries = append(l.entries, e)
		bal := l.balances[e.Account]
		bal = bal.Add(e.Debit).Sub(e.Credit)
		l.balances[e.Account] = bal
	}
}

// Balance devolve o saldo de uma conta (débito − crédito).
func (l *Ledger) Balance(account string) decimal.Decimal {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.balances[account]
}

// Balances devolve uma cópia de todos os saldos por conta.
func (l *Ledger) Balances() map[string]decimal.Decimal {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[string]decimal.Decimal, len(l.balances))
	for k, v := range l.balances {
		out[k] = v
	}
	return out
}

// Entries devolve uma cópia das entradas em ordem.
func (l *Ledger) Entries() []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Len devolve o número de entradas.
func (l *Ledger) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.entries)
}
