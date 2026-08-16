package marketindex

import "testing"

func TestGateTrade(t *testing.T) {
	cases := []struct {
		regime, side string
		allow        bool
	}{
		{"risk-off", "buy", false},  // comprar no medo → bloqueia
		{"risk-off", "sell", true},  // vender no medo → permite
		{"risk-on", "sell", false},  // vender na confiança → bloqueia
		{"risk-on", "buy", true},    // comprar na confiança → permite
		{"cautela", "buy", true},    // cautela → permite compra
		{"cautela", "sell", true},   // cautela → permite venda
		{"desconhecido", "buy", true},  // sem dados → permite (fail-open na rede, fail-closed no risco)
		{"desconhecido", "sell", true},
		{"neutral", "buy", true},
	}

	for _, c := range cases {
		allow, reason := GateTrade(c.regime, c.side)
		if allow != c.allow {
			t.Errorf("GateTrade(%q, %q) = %v (reason=%q), want %v", c.regime, c.side, allow, reason, c.allow)
		}
		if !allow && reason == "" {
			t.Errorf("GateTrade(%q, %q) bloqueou sem motivo", c.regime, c.side)
		}
	}
}
