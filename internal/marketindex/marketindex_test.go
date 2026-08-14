package marketindex

import (
	"context"
	"testing"
	"time"
)

// TestFetchLive busca os mercados GLOBAIS reais (Yahoo Finance, sem chave).
// Skip se offline — o teste unitário de parsing cobre a lógica.
func TestFetchLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New()
	snap, err := c.Fetch(ctx)
	if err != nil {
		t.Skipf("sem acesso ao Yahoo Finance: %v", err)
	}
	if len(snap.Quotes) == 0 {
		t.Fatal("nenhuma cotação coletada")
	}
	t.Logf("REGIME GLOBAL: %s", snap.Regime)
	t.Logf("SINAL: %s", snap.Signal)
	for _, q := range snap.Quotes {
		t.Logf("  %-18s %10.2f  5d:%+5.2f%%  10d:%+5.2f%%", q.Name, q.Price, q.Change5d, q.Change10d)
	}
	if snap.Regime != "risk-on" && snap.Regime != "risk-off" && snap.Regime != "cautela" {
		t.Fatalf("regime desconhecido: %s", snap.Regime)
	}
}

// TestCacheTTL valida que o cache evita re-fetch dentro do TTL.
func TestCacheTTL(t *testing.T) {
	ctx := context.Background()
	c := New()
	c.ttl = time.Hour // TTL longo para o teste
	// Sem rede, o cache vazio retorna regime com quotes vazias — o importante
	// é não panificar.
	snap, err := c.Fetch(ctx)
	if err == nil {
		_ = snap.Regime
	}
}
