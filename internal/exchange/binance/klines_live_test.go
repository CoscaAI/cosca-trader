package binance

import (
	"context"
	"testing"
	"time"
)

// TestKlinesLive busca candles REAIS da Binance (API pública, SEM chave) e
// valida o formato. Skip se não houver rede/API disponível — o teste unitário
// de parsing cobre a lógica; este prova a integração com o mundo real.
func TestKlinesLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := New()
	candles, err := c.Klines(ctx, "BTCUSDT", "1h", 500, 500)
	if err != nil {
		t.Skipf("sem acesso à Binance (offline?): %v", err)
	}
	if len(candles) < 100 {
		t.Fatalf("esperava ≥100 candles reais, got %d", len(candles))
	}
	// Preços devem ser positivos e coerentes.
	for _, c := range candles[:10] {
		if c.Close <= 0 || c.High < c.Low {
			t.Fatalf("candle inválido: %+v", c)
		}
	}
	// Ordem cronológica crescente.
	for i := 1; i < len(candles); i++ {
		if !candles[i].OpenTime.After(candles[i-1].OpenTime) {
			t.Fatalf("candles fora de ordem em %d", i)
		}
	}
	t.Logf("OK: %d candles reais BTCUSDT 1h (%s → %s), close %.2f → %.2f",
		len(candles), candles[0].OpenTime.Format("2006-01-02"),
		candles[len(candles)-1].OpenTime.Format("2006-01-02"),
		candles[0].Close, candles[len(candles)-1].Close)
}
