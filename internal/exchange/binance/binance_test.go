// binance_test.go — testes do adapter Binance (ambiente de execução P0-3).
package binance

import "testing"

// TestNewTradingEnvSelection valida a semântica P0-3: o default do sistema é
// SEGURO (testnet) — a Binance real (api.binance.com) só é atingida com a
// escolha explícita de EnvLive.
func TestNewTradingEnvSelection(t *testing.T) {
	live := NewTrading("k", "s", EnvLive)
	if live.Env() != EnvLive {
		t.Errorf("EnvLive deveria conectar na Binance real, veio %q", live.Env())
	}
	if live.restBase != "https://api.binance.com" {
		t.Errorf("EnvLive deveria usar api.binance.com, veio %q", live.restBase)
	}

	tn := NewTrading("k", "s", EnvTestnet)
	if tn.Env() != EnvTestnet {
		t.Errorf("EnvTestnet deveria usar a sandbox, veio %q", tn.Env())
	}
	if tn.restBase != "https://testnet.binance.vision" {
		t.Errorf("EnvTestnet deveria usar testnet.binance.vision, veio %q", tn.restBase)
	}

	// default implícito: qualquer env não-live é seguro (nunca api.binance.com).
	safe := NewTrading("k", "s", "qualquer-coisa")
	if safe.Env() != EnvTestnet || safe.restBase == "https://api.binance.com" {
		t.Errorf("env não-live deve ser seguro (testnet), veio %q", safe.Env())
	}
}
