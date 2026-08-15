// status.go — classificação de status HTTP da Binance para a hierarquia de
// erros da camada exchange (retryable vs determinístico). O padrão é do ccxt:
// 429/418 = rate limit (retry com backoff); 5xx = transitório (retry); o resto
// (4xx) = determinístico (falha rápida).
package binance

import "github.com/CoscaAI/cosca-trader/internal/exchange"

// classifyStatus mapeia um status HTTP para o ErrorKind da exchange.
func classifyStatus(code int) exchange.ErrorKind {
	switch {
	case code == 429 || code == 418:
		return exchange.KindRateLimit
	case code >= 500:
		return exchange.KindTransient
	default:
		return exchange.KindDeterministic
	}
}
