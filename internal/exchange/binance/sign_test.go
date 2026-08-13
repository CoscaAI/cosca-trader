package binance

import (
	"net/url"
	"strings"
	"testing"
)

func TestSignDeterministicAndHex(t *testing.T) {
	params := url.Values{}
	params.Set("symbol", "BTCUSDT")
	params.Set("side", "BUY")

	out := sign(params, "secret")
	if !strings.Contains(out, "signature=") {
		t.Fatalf("assinatura ausente: %q", out)
	}
	sig := strings.TrimPrefix(out[strings.Index(out, "signature="):], "signature=")
	if len(sig) != 64 {
		t.Errorf("assinatura HMAC-SHA256 deve ter 64 hex chars, veio %d", len(sig))
	}
	// determinístico
	if sign(params, "secret") != out {
		t.Error("sign() não é determinístico")
	}
}

func TestSignSortsKeys(t *testing.T) {
	// url.Values.Encode() ordena as chaves — a Binance exige ordenação.
	params := url.Values{}
	params.Set("symbol", "BTCUSDT")
	params.Set("side", "BUY")

	out := sign(params, "s")
	idxSide := strings.Index(out, "side=")
	idxSym := strings.Index(out, "symbol=")
	if idxSide == -1 || idxSym == -1 || idxSide > idxSym {
		t.Errorf("chaves deveriam estar ordenadas (side antes de symbol): %q", out)
	}
}

func TestSignedRequestRequiresCredentials(t *testing.T) {
	c := New() // sem credenciais
	err := c.signedRequest(nil, "GET", "/api/v3/account", url.Values{}, nil)
	if err == nil {
		t.Error("esperava erro por credenciais ausentes")
	}
}
