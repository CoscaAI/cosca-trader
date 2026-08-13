package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestOrderIsOpen(t *testing.T) {
	cases := []struct {
		status OrderStatus
		want   bool
	}{
		{OrderNew, true},
		{OrderPartiallyFilled, true},
		{OrderFilled, false},
		{OrderCanceled, false},
		{OrderRejected, false},
	}
	for _, c := range cases {
		o := Order{Status: c.status}
		if o.IsOpen() != c.want {
			t.Errorf("IsOpen(%q) = %v, esperava %v", c.status, o.IsOpen(), c.want)
		}
		if o.IsClosed() == c.want {
			t.Errorf("IsClosed(%q) inconsistente com IsOpen", c.status)
		}
	}
}

func TestPositionUnrealizedPnL(t *testing.T) {
	// long: (mark - entry) * qty
	long := Position{Side: SideBuy, Quantity: d("2"), AvgEntryPrice: d("100")}
	if got := long.UnrealizedPnLAt(d("110")); !got.Equal(d("20")) {
		t.Errorf("long PnL = %v, esperava 20", got)
	}

	// short: (entry - mark) * qty
	short := Position{Side: SideSell, Quantity: d("2"), AvgEntryPrice: d("100")}
	if got := short.UnrealizedPnLAt(d("90")); !got.Equal(d("20")) {
		t.Errorf("short PnL = %v, esperava 20", got)
	}
}

func TestPositionIsOpen(t *testing.T) {
	if (Position{Quantity: d("0")}).IsOpen() {
		t.Error("posição zerada não deveria estar aberta")
	}
	if !(Position{Quantity: d("1")}).IsOpen() {
		t.Error("posição com quantidade deveria estar aberta")
	}
}

func TestInstrumentValid(t *testing.T) {
	if (Instrument{}).Valid() {
		t.Error("instrumento vazio não deveria ser válido")
	}
	if !(Instrument{Symbol: "BTCUSDT", Exchange: "binance", BaseAsset: "BTC", QuoteAsset: "USDT"}).Valid() {
		t.Error("instrumento completo deveria ser válido")
	}
}

func TestOrderTypeValid(t *testing.T) {
	if !OrderLimit.Valid() || !OrderMarket.Valid() {
		t.Error("tipos válidos deveriam passar")
	}
	if OrderType("lixo").Valid() {
		t.Error("tipo desconhecido deveria ser inválido")
	}
}

func TestTimeInForceValid(t *testing.T) {
	if !TIFGTC.Valid() || !TIFIOC.Valid() {
		t.Error("TIF válidos deveriam passar")
	}
	if TimeInForce("XYZ").Valid() {
		t.Error("TIF desconhecido deveria ser inválido")
	}
}
