package domain

import "testing"

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
	long := Position{Side: SideBuy, Quantity: 2, AvgEntryPrice: 100}
	if got := long.UnrealizedPnLAt(110); got != 20 {
		t.Errorf("long PnL = %v, esperava 20", got)
	}

	// short: (entry - mark) * qty
	short := Position{Side: SideSell, Quantity: 2, AvgEntryPrice: 100}
	if got := short.UnrealizedPnLAt(90); got != 20 {
		t.Errorf("short PnL = %v, esperava 20", got)
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
