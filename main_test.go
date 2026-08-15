// main_test.go — testes das funções utilitárias de configuração do entrypoint
// (leitura de env, parse/fallback, helpers puros). Cobre o caminho de
// configuração que compõe a raiz do processo — sem subir listener nem tocar em
// rede.
package main

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/strategy"
)

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in, def string
		want    []string
	}{
		{"", "BTCUSDT", []string{"BTCUSDT"}},
		{"BTCUSDT,ETHUSDT,SOLUSDT", "X", []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"}},
		{"BTCUSDT, ETHUSDT , SOLUSDT", "X", []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"}},
		{",,", "X", []string{"X"}},
	}
	for _, c := range cases {
		got := splitCSV(c.in, c.def)
		if len(got) != len(c.want) {
			t.Fatalf("splitCSV(%q) = %v, esperava %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitCSV(%q)[%d] = %q, esperava %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestSplitOrigins(t *testing.T) {
	if got := splitOrigins(""); got != nil {
		t.Fatalf("splitOrigins(vazio) = %v, esperava nil", got)
	}
	got := splitOrigins("http://a.com, http://b.com ,")
	if len(got) != 2 || got[0] != "http://a.com" || got[1] != "http://b.com" {
		t.Fatalf("splitOrigins = %v", got)
	}
}

func TestDemoStartPrice(t *testing.T) {
	cases := map[string]float64{
		"BTCUSDT": 60000,
		"ETHUSDT": 3000,
		"BNBUSDT": 500,
		"SOLUSDT": 100,
	}
	for sym, want := range cases {
		if got := demoStartPrice(sym); got != want {
			t.Errorf("demoStartPrice(%q) = %v, esperava %v", sym, got, want)
		}
	}
}

func TestMin(t *testing.T) {
	if min(3, 5) != 3 || min(5, 3) != 3 || min(4, 4) != 4 {
		t.Fatal("min() quebrado")
	}
}

func TestEnvInt(t *testing.T) {
	t.Setenv("COSCA_TRADER_GUARD_MAX_STOPS", "")
	if got := envInt("COSCA_TRADER_GUARD_MAX_STOPS", 5); got != 5 {
		t.Fatalf("envInt default = %d, esperava 5", got)
	}
	t.Setenv("COSCA_TRADER_GUARD_MAX_STOPS", "42")
	if got := envInt("COSCA_TRADER_GUARD_MAX_STOPS", 5); got != 42 {
		t.Fatalf("envInt válido = %d, esperava 42", got)
	}
	t.Setenv("COSCA_TRADER_GUARD_MAX_STOPS", "nao-numero")
	if got := envInt("COSCA_TRADER_GUARD_MAX_STOPS", 5); got != 5 {
		t.Fatalf("envInt inválido = %d, esperava fallback 5", got)
	}
}

func TestBuildProtections(t *testing.T) {
	t.Setenv("COSCA_TRADER_GUARD_MAX_STOPS", "")
	t.Setenv("COSCA_TRADER_COOLDOWN_CANDLES", "")
	if got := buildProtections(); got != nil {
		t.Fatalf("sem env deveria ser nil, got %v", got)
	}
	t.Setenv("COSCA_TRADER_GUARD_MAX_STOPS", "3")
	if got := buildProtections(); got == nil {
		t.Fatal("com guard max_stops>0 deveria criar proteções")
	}
	t.Setenv("COSCA_TRADER_GUARD_MAX_STOPS", "")
	t.Setenv("COSCA_TRADER_COOLDOWN_CANDLES", "5")
	if got := buildProtections(); got == nil {
		t.Fatal("com cooldown>0 deveria criar proteções")
	}
}

// decimalEnvCases testa os helpers de env decimal (válido/inválido/vazio).
func TestDecimalEnvHelpers(t *testing.T) {
	type tc struct {
		set   func(string)
		get   func() decimal.Decimal
		name  string
		want  string
		empty string
	}
	cases := []tc{
		{func(s string) { t.Setenv("COSCA_TRADER_STRATEGY_QTY", s) }, strategyOrderQty, "COSCA_TRADER_STRATEGY_QTY", "0.25", "0.001"},
		{func(s string) { t.Setenv("COSCA_TRADER_MAX_ORDER_USDT", s) }, maxOrderLimit, "COSCA_TRADER_MAX_ORDER_USDT", "500", "1000"},
		{func(s string) { t.Setenv("COSCA_TRADER_STOP_LOSS_PCT", s) }, stopLossPct, "COSCA_TRADER_STOP_LOSS_PCT", "0.05", "0"},
		{func(s string) { t.Setenv("COSCA_TRADER_MAX_EXPOSURE_PCT", s) }, riskExposurePct, "COSCA_TRADER_MAX_EXPOSURE_PCT", "0.30", "0.20"},
		{func(s string) { t.Setenv("COSCA_TRADER_MAX_TOTAL_EXPOSURE_PCT", s) }, riskTotalExposurePct, "COSCA_TRADER_MAX_TOTAL_EXPOSURE_PCT", "0.60", "0.50"},
		{func(s string) { t.Setenv("COSCA_TRADER_MAX_DRAWDOWN_PCT", s) }, riskMaxDrawdownPct, "COSCA_TRADER_MAX_DRAWDOWN_PCT", "0.15", "0.10"},
		{func(s string) { t.Setenv("COSCA_TRADER_TAKE_PROFIT_PCT", s) }, takeProfitPct, "COSCA_TRADER_TAKE_PROFIT_PCT", "0.10", "0"},
		{func(s string) { t.Setenv("COSCA_TRADER_TRAILING_ACTIVATION_PCT", s) }, trailingActivationPct, "COSCA_TRADER_TRAILING_ACTIVATION_PCT", "0.03", "0"},
		{func(s string) { t.Setenv("COSCA_TRADER_TRAILING_PCT", s) }, trailingPct, "COSCA_TRADER_TRAILING_PCT", "0.02", "0"},
		{func(s string) { t.Setenv("COSCA_TRADER_PAPER_BALANCE", s) }, paperBalance, "COSCA_TRADER_PAPER_BALANCE", "20000", "10000"},
		{func(s string) { t.Setenv("COSCA_TRADER_PAPER_FEE_PCT", s) }, paperFeePct, "COSCA_TRADER_PAPER_FEE_PCT", "0.002", "0.001"},
		{func(s string) { t.Setenv("COSCA_TRADER_PAPER_SLIPPAGE", s) }, paperSlippage, "COSCA_TRADER_PAPER_SLIPPAGE", "0.001", "0"},
		{func(s string) { t.Setenv("COSCA_TRADER_PAPER_LIMIT_FILL_FRACTION", s) }, paperLimitFillFraction, "COSCA_TRADER_PAPER_LIMIT_FILL_FRACTION", "0.8", "0.5"},
		{func(s string) { t.Setenv("COSCA_TRADER_RISK_PER_TRADE_PCT", s) }, riskPerTradePct, "COSCA_TRADER_RISK_PER_TRADE_PCT", "0.01", "0"},
	}
	for _, c := range cases {
		c.set("")
		if got := c.get(); !got.Equal(decimal.RequireFromString(c.empty)) {
			t.Errorf("%s vazio = %s, esperava %s", c.name, got, c.empty)
		}
		c.set("invalido")
		if got := c.get(); !got.Equal(decimal.RequireFromString(c.empty)) {
			t.Errorf("%s inválido = %s, esperava fallback %s", c.name, got, c.empty)
		}
		c.set(c.want)
		if got := c.get(); !got.Equal(decimal.RequireFromString(c.want)) {
			t.Errorf("%s válido = %s, esperava %s", c.name, got, c.want)
		}
	}
}

func TestPaperFast(t *testing.T) {
	t.Setenv("COSCA_TRADER_PAPER_FAST", "")
	if paperFast() {
		t.Fatal("paperFast default deveria ser false")
	}
	t.Setenv("COSCA_TRADER_PAPER_FAST", "1")
	if !paperFast() {
		t.Fatal("paperFast com 1 deveria ser true")
	}
}

func TestDefaultPortAndDBPath(t *testing.T) {
	t.Setenv("COSCA_TRADER_PORT", "")
	if got := defaultPort(); got != "24120" {
		t.Fatalf("defaultPort = %q, esperava 24120", got)
	}
	t.Setenv("COSCA_TRADER_PORT", "9999")
	if got := defaultPort(); got != "9999" {
		t.Fatalf("defaultPort env = %q, esperava 9999", got)
	}
	t.Setenv("COSCA_TRADER_DB", "/tmp/x.db")
	if got := defaultDBPath(); got != "/tmp/x.db" {
		t.Fatalf("defaultDBPath env = %q, esperava /tmp/x.db", got)
	}
}

func TestPayloadJSON(t *testing.T) {
	type obj struct {
		A string `json:"a"`
	}
	// Struct em memória.
	var out obj
	if err := payloadJSON(obj{A: "x"}, &out); err != nil || out.A != "x" {
		t.Fatalf("payloadJSON(struct) = %+v, %v", out, err)
	}
	// json.RawMessage.
	out = obj{}
	if err := payloadJSON(json.RawMessage(`{"a":"y"}`), &out); err != nil || out.A != "y" {
		t.Fatalf("payloadJSON(raw) = %+v, %v", out, err)
	}
	// []byte.
	out = obj{}
	if err := payloadJSON([]byte(`{"a":"z"}`), &out); err != nil || out.A != "z" {
		t.Fatalf("payloadJSON(bytes) = %+v, %v", out, err)
	}
	// nil → erro.
	if err := payloadJSON(nil, &out); err == nil {
		t.Fatal("payloadJSON(nil) deveria errar")
	}
}

func TestPrintReportNoPanic(t *testing.T) {
	// printReport apenas loga — não deve panificar com um laudo zerado (todos
	// os campos vazios/zero).
	printReport(strategy.ScientificReport{})
}
