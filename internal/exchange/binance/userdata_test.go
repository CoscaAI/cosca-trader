package binance

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// payload REAL da Binance (executionReport de um fill), com todos os pares
// case-colliding (e/E, s/S, l/L, z/Z, ...) — regression da colisão.
const executionReportFill = `{
  "e": "executionReport", "E": 1499405658658, "s": "ETHBTC",
  "c": "mUvoqJxFIILMdfAW5iGSOW", "S": "BUY", "o": "LIMIT", "f": "GTC",
  "q": "1.00000000", "p": "0.10264410", "P": "0", "F": "0", "g": -1, "C": "",
  "x": "TRADE", "X": "FILLED", "r": "NONE", "i": 4293153,
  "l": "1.00000000", "z": "1.00000000", "L": "0.10264410",
  "n": "0.00001000", "N": "BNB", "T": 1499405658657, "t": 1499405658658,
  "I": 8641984, "w": false, "m": false, "M": false, "O": 1499405658657,
  "Z": "0.10264410", "Y": "0.10264410", "Q": "0"
}`

func TestHandleExecutionReport(t *testing.T) {
	var order domain.Order
	var trade domain.Trade
	err := handleUserMessage([]byte(executionReportFill), "binance", exchange.Handler{
		OnOrderUpdate: func(o domain.Order) { order = o },
		OnTrade:       func(tr domain.Trade) { trade = tr },
	})
	if err != nil {
		t.Fatalf("handleUserMessage: %v", err)
	}

	if order.ID != "4293153" || order.Symbol != "ETHBTC" || order.Side != domain.SideBuy {
		t.Errorf("ordem errada: %+v", order)
	}
	if order.Status != domain.OrderFilled || order.Type != domain.OrderLimit {
		t.Errorf("status/tipo errados: %+v", order)
	}
	if order.FilledQty.Equal(d("1")) == false || order.Price.Equal(d("0.10264410")) == false {
		t.Errorf("preço/qty errados: %+v", order)
	}

	// fill deve gerar um trade com fee asset BNB
	if trade.Symbol != "ETHBTC" || !trade.Price.Equal(d("0.10264410")) || !trade.Quantity.Equal(d("1")) {
		t.Errorf("trade errado: %+v", trade)
	}
	if trade.FeeAsset != "BNB" {
		t.Errorf("fee asset = %q, esperava BNB", trade.FeeAsset)
	}
}

func TestHandleExecutionReportNoFill(t *testing.T) {
	// execução NEW (sem fill): não deve gerar trade.
	payload := `{"e":"executionReport","E":1,"s":"ETHBTC","S":"BUY","o":"LIMIT","f":"GTC","q":"1","p":"0.1","P":"0","F":"0","g":-1,"C":"","x":"NEW","X":"NEW","r":"NONE","i":42,"l":"0","z":"0","L":"0","n":"0","T":1,"t":-1,"I":0,"w":true,"m":false,"M":false,"O":1,"Z":"0","Y":"0","Q":"0"}`

	var order domain.Order
	tradeCalled := false
	err := handleUserMessage([]byte(payload), "binance", exchange.Handler{
		OnOrderUpdate: func(o domain.Order) { order = o },
		OnTrade:       func(domain.Trade) { tradeCalled = true },
	})
	if err != nil {
		t.Fatalf("handleUserMessage: %v", err)
	}
	if order.ID != "42" || order.Status != domain.OrderNew {
		t.Errorf("ordem NEW errada: %+v", order)
	}
	if tradeCalled {
		t.Error("execução NEW não deveria gerar trade")
	}
}

func TestHandleBalances(t *testing.T) {
	payload := `{"e":"outboundAccountPosition","E":1564034571105,"u":1564034571073,"B":[{"a":"ETH","f":"10000.000000","l":"0.000000"},{"a":"BNB","f":"0.000000","l":"0.000000"}]}`

	var balances []domain.Balance
	err := handleUserMessage([]byte(payload), "binance", exchange.Handler{
		OnBalanceUpdate: func(b domain.Balance) { balances = append(balances, b) },
	})
	if err != nil {
		t.Fatalf("handleUserMessage: %v", err)
	}
	if len(balances) != 2 {
		t.Fatalf("esperava 2 saldos, veio %d", len(balances))
	}
	if balances[0].Asset != "ETH" || !balances[0].Free.Equal(d("10000")) {
		t.Errorf("saldo ETH errado: %+v", balances[0])
	}
}

func TestConversionHelpers(t *testing.T) {
	if toSide("SELL") != domain.SideSell || toSide("BUY") != domain.SideBuy {
		t.Error("toSide errado")
	}
	if toOrderType("MARKET") != domain.OrderMarket || toOrderType("LIMIT") != domain.OrderLimit {
		t.Error("toOrderType errado")
	}
	if toStatus("FILLED") != domain.OrderFilled || toStatus("PARTIALLY_FILLED") != domain.OrderPartiallyFilled {
		t.Error("toStatus errado")
	}
}

func TestCommissionAssetNull(t *testing.T) {
	// `N` pode vir null — o campo é *string e deve tolerar.
	payload := `{"e":"executionReport","E":1,"s":"X","S":"BUY","o":"LIMIT","f":"GTC","q":"1","p":"1","P":"0","F":"0","g":-1,"C":"","x":"NEW","X":"NEW","r":"NONE","i":1,"l":"0","z":"0","L":"0","n":"0","N":null,"T":1,"t":-1,"I":0,"w":true,"m":false,"M":false,"O":1,"Z":"0","Y":"0","Q":"0"}`
	var order domain.Order
	if err := handleUserMessage([]byte(payload), "binance", exchange.Handler{
		OnOrderUpdate: func(o domain.Order) { order = o },
	}); err != nil {
		t.Fatalf("N null deveria ser tolerado: %v", err)
	}
	if order.ID != "1" {
		t.Errorf("ordem errada: %+v", order)
	}
}
