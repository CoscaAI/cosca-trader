package explore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/exchange/binance"
)

func sqrt(f float64) float64 {
	if f <= 0 { return 0 }
	x := f
	for i := 0; i < 20; i++ { x = (x + f/x) / 2 }
	return x
}

// TestMarketEvidence minera o histórico REAL da Binance e imprime evidências
// de mercado para desenhar estratégias baseadas em dados, não em chute.
func TestMarketEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bc := binance.New()
	candles, err := bc.Klines(ctx, "BTCUSDT", "1h", 500, 500)
	if err != nil { t.Skipf("offline: %v", err) }

	closes := make([]float64, len(candles))
	for i, c := range candles { closes[i] = c.Close }
	first, last := closes[0], closes[len(closes)-1]
	fmt.Printf("PERÍODO: %s → %s (%d velas 1h)\n", candles[0].OpenTime.Format("2006-01-02"), candles[len(candles)-1].OpenTime.Format("2006-01-02"), len(closes))
	fmt.Printf("PREÇO: %.2f → %.2f (%.2f%%)\n", first, last, (last-first)/first*100)

	fmt.Println("\nREGIME POR SEGMENTO (120 velas ≈ 5 dias):")
	for i := 0; i+120 <= len(closes); i += 120 {
		seg := closes[i : i+120]
		chg := (seg[119] - seg[0]) / seg[0] * 100
		var sum float64
		ret := make([]float64, 119)
		for j := 1; j < 120; j++ { ret[j-1] = (seg[j] - seg[j-1]) / seg[j-1]; sum += ret[j-1] }
		mean := sum / 119
		var sq float64
		for _, r := range ret { d := r - mean; sq += d*d }
		vol := sqrt(sq/119) * 100
		trend := "lateral"
		if chg > 3 { trend = "ALTA" } else if chg < -3 { trend = "BAIXA" }
		fmt.Printf("  %s→%s: %+.2f%% vol %.3f%% → %s\n", candles[i].OpenTime.Format("01-02"), candles[i+119].OpenTime.Format("01-02"), chg, vol, trend)
	}

	// Continuidade de tendência: dado que fechou +x%, qual a prob de fechar
	// +x% na próxima? (evidência de momentum/serie temporal)
	fmt.Println("\nPERSISTÊNCIA DE DIREÇÃO (evidência de momentum):")
	upFollow, upCount, dnFollow, dnCount := 0, 0, 0, 0
	for i := 2; i < len(closes); i++ {
		up1 := closes[i-1] > closes[i-2]
		up2 := closes[i] > closes[i-1]
		if up1 { upCount++; if up2 { upFollow++ } }
		if !up1 { dnCount++; if !up2 { dnFollow++ } }
	}
	fmt.Printf("  após vela ALTA: próxima ALTA em %.0f%% (%d/%d)\n", float64(upFollow)/float64(upCount)*100, upFollow, upCount)
	fmt.Printf("  após vela BAIXA: próxima BAIXA em %.0f%% (%d/%d)\n", float64(dnFollow)/float64(dnCount)*100, dnFollow, dnCount)

	// Fecho acima de range: breakout de N velas tem continuidade?
	fmt.Println("\nCONTINUIDADE DE BREAKOUT (evidência de breakout):")
	for _, n := range []int{10, 20} {
		okUp, totUp, okDn, totDn := 0, 0, 0, 0
		for i := n; i+3 < len(closes); i++ {
			maxP, minP := closes[i-n], closes[i-n]
			for j := i - n; j < i; j++ {
				if closes[j] > maxP { maxP = closes[j] }
				if closes[j] < minP { minP = closes[j] }
			}
			if closes[i] > maxP {
				totUp++
				up2 := closes[i+2] > closes[i]
				if up2 { okUp++ }
			}
			if closes[i] < minP {
				totDn++
				dn2 := closes[i+2] < closes[i]
				if dn2 { okDn++ }
			}
		}
		fmt.Printf("  quebra máxima %d velas: continua subindo em 2 velas = %.0f%% (%d/%d) | quebra mínima: continua caindo = %.0f%% (%d/%d)\n",
			n, float64(okUp)/float64(totUp)*100, okUp, totUp, float64(okDn)/float64(totDn)*100, okDn, totDn)
	}
}
