package ledger

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestLedgerPostAndBalance(t *testing.T) {
	l := New()
	l.Post(
		Entry{ID: "1", Account: "fee:BNB", Debit: d("0.5"), Ref: "t1"},
		Entry{ID: "2", Account: "asset:BNB", Credit: d("0.5"), Ref: "t1"},
	)

	if !l.Balance("fee:BNB").Equal(d("0.5")) {
		t.Errorf("fee:BNB = %v, esperava 0.5", l.Balance("fee:BNB"))
	}
	if !l.Balance("asset:BNB").Equal(d("-0.5")) {
		t.Errorf("asset:BNB = %v, esperava -0.5", l.Balance("asset:BNB"))
	}
	if l.Len() != 2 {
		t.Errorf("Len = %d, esperava 2", l.Len())
	}
}

func TestLedgerBalancesAndEntries(t *testing.T) {
	l := New()
	l.Post(Entry{ID: "1", Account: "fee:USDT", Debit: d("1.25"), Ref: "a"})

	bals := l.Balances()
	if !bals["fee:USDT"].Equal(d("1.25")) {
		t.Errorf("balances: %v", bals)
	}
	if len(l.Entries()) != 1 {
		t.Errorf("entries: %d", len(l.Entries()))
	}
}

func TestAssetAccountHelpers(t *testing.T) {
	if AssetAccount("BTC") != "asset:BTC" {
		t.Errorf("AssetAccount = %q", AssetAccount("BTC"))
	}
	if FeeAccount("BNB") != "fee:BNB" {
		t.Errorf("FeeAccount = %q", FeeAccount("BNB"))
	}
}
