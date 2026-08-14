// divergence.go — o DETECTOR DE DIVERGÊNCIA MACRO (a missão do Don):
//
//	"gap de preco muito grande em ativo que vai influenciar no que estamos
//	operando, por exemplo sp500 caiu bastante e bitcoin ainda nao reagiu —
//	entrar na melhor forma possivel seguindo a tendencia de queda e vice-versa"
//
// Quando o S&P 500 (ou o ouro/dólar) move FORTE e o cripto ainda NÃO reagiu,
// há uma janela de oportunidade: o cripto tende a seguir o movimento global
// com atraso (lag de correlação). O detector mede essa divergência e emite
// um sinal de ENTRADA na direção da tendência global — usando toda a
// evidência que temos (regime + correlação medida).
package marketindex

import (
	"time"
)

// DivergenceSignal é a decisão de entrada gerada pela divergência macro.
type DivergenceSignal struct {
	// Action: "buy" (seguir a alta global) | "sell" (seguir a queda global)
	// | "hold" (sem divergência relevante).
	Action string `json:"action"`
	// Strength 0..1: quão forte é a divergência (magnitude do gap × confiança).
	Strength float64 `json:"strength"`
	// Reason explica a leitura (para o log e o painel).
	Reason string `json:"reason"`
	// GlobalChangePct é o movimento do mercado global (S&P 5d).
	GlobalChangePct float64 `json:"global_change_pct"`
	// CryptoChangePct é o movimento do cripto (BTC 5d).
	CryptoChangePct float64 `json:"crypto_change_pct"`
	// GeneratedAt marca o momento da leitura.
	GeneratedAt time.Time `json:"generated_at"`
}

// DivergenceConfig parametriza o detector.
type DivergenceConfig struct {
	// MinGapPct é a divergência mínima (global − cripto) para sinalizar.
	// Ex.: 2.0 = S&P −2% e BTC +0% → gap de 2 pontos.
	MinGapPct float64
	// MinGlobalMove é o movimento global mínimo para considerar o evento.
	// Ex.: 1.0 = S&P precisa ter movido ≥1% para ser "evento".
	MinGlobalMove float64
	// Confiança do regime (risk-off aumenta a confiança de seguir queda).
	// [0..1]
	RegimeBoost float64
}

// DefaultDivergenceConfig é a calibração default da casa.
func DefaultDivergenceConfig() DivergenceConfig {
	return DivergenceConfig{
		MinGapPct:     2.0,
		MinGlobalMove: 1.0,
		RegimeBoost:   0.5,
	}
}

// AnalyzeDivergence mede a divergência entre o mercado global (S&P como
// proxy do risk) e o cripto (BTC), e devolve o sinal de entrada. Usa o
// snapshot JÁ coletado (sem novo fetch — o radar alimenta).
func AnalyzeDivergence(snap Snapshot, btcChange10d float64, cfg DivergenceConfig) DivergenceSignal {
	now := time.Now()
	if cfg.MinGapPct <= 0 {
		cfg = DefaultDivergenceConfig()
	}

	// Pega o S&P (proxy global) e o VIX (confiança).
	sp := findQuote(snap, "^GSPC")
	if sp == nil {
		return DivergenceSignal{Action: "hold", Reason: "sem dados do S&P", GeneratedAt: now}
	}
	global := sp.Change10d // movimento global (10d, mais estável que 5d)

	// Gap = movimento global − movimento do cripto (na mesma janela).
	gap := global - btcChange10d

	// Confiança: reforçada pelo regime.
	confidence := 0.6
	if snap.Regime == "risk-off" && global < 0 {
		confidence += cfg.RegimeBoost // risk-off + queda global = seguir a queda com força
	}
	if snap.Regime == "risk-on" && global > 0 {
		confidence += cfg.RegimeBoost // risk-on + alta global = seguir a alta
	}
	if confidence > 1 {
		confidence = 1
	}

	// Ação: o gap grande em MÓDULO + movimento global relevante.
	sig := DivergenceSignal{GeneratedAt: now, GlobalChangePct: global, CryptoChangePct: btcChange10d}
	absGlobal := global
	if absGlobal < 0 {
		absGlobal = -absGlobal
	}
	absGap := gap
	if absGap < 0 {
		absGap = -absGap
	}

	switch {
	case absGlobal < cfg.MinGlobalMove:
		sig.Action = "hold"
		sig.Reason = "movimento global insuficiente (S&P " + f2(global) + "%)"
	case gap > cfg.MinGapPct:
		// Global subiu, cripto ficou para trás → seguir a ALTA (comprar).
		sig.Action = "buy"
		sig.Strength = (absGap / (cfg.MinGapPct * 2)) * confidence
		if sig.Strength > 1 {
			sig.Strength = 1
		}
		sig.Reason = "S&P subiu " + f2(global) + "% mas cripto só " + f2(btcChange10d) + "% — gap de " + f2(gap) + " pontos → seguir a alta"
	case gap < -cfg.MinGapPct:
		// Global caiu, cripto ainda não reagiu → seguir a QUEDA (vender).
		sig.Action = "sell"
		sig.Strength = (absGap / (cfg.MinGapPct * 2)) * confidence
		if sig.Strength > 1 {
			sig.Strength = 1
		}
		sig.Reason = "S&P caiu " + f2(global) + "% e o cripto ainda não reagiu (só " + f2(btcChange10d) + "%) — gap de " + f2(gap) + " pontos → seguir a queda"
	default:
		sig.Action = "hold"
		sig.Reason = "sem divergência relevante (gap " + f2(gap) + " < " + f2(cfg.MinGapPct) + ")"
	}
	return sig
}

// findQuote devolve a cotação de um símbolo no snapshot (nil se ausente).
func findQuote(snap Snapshot, symbol string) *Quote {
	for i := range snap.Quotes {
		if snap.Quotes[i].Symbol == symbol {
			return &snap.Quotes[i]
		}
	}
	return nil
}

// f2 formata float com 2 casas para mensagens (sem import de fmt no hot path
// — usamos strconv-style manual simples).
func f2(v float64) string {
	neg := ""
	if v < 0 {
		neg = "-"
		v = -v
	}
	i := int(v)
	frac := int((v - float64(i)) * 100 + 0.5)
	if frac >= 100 {
		i++
		frac = 0
	}
	// conversão manual int→string
	return neg + itoaF(i) + "." + itoaF2(frac)
}

func itoaF(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func itoaF2(n int) string {
	if n < 10 {
		return "0" + itoaF(n)
	}
	return itoaF(n)
}
