// gate.go — o FILTRO MACRO da execução (a missão do Don):
//
//	"gap de preco muito grande em ativo que vai influenciar no que estamos
//	operando" — o cripto não vive numa bolha. Antes de executar um sinal da
//	estratégia, o regime global decide se o trade vai A FAVOR ou CONTRA o
//	mercado. Operar contra o regime é remar contra a maré.
package marketindex

// GateTrade decide se um trade (side "buy"/"sell") é permitido sob o regime
// macro corrente. Retorna (allow, reason): allow=false bloqueia o sinal —
// o kernel loga o motivo e a ordem NÃO é enviada à exchange.
//
// Regra (seguir a tendência global, nunca remar contra):
//   - risk-off  + buy  → bloqueia (aversão global; não comprar topo de medo)
//   - risk-on   + sell → bloqueia (mercado construtivo; não vender alta de
//     confiança)
//   - cautela   → permite (sinais mistos; o risk manager segura os stops)
//   - desconhecido → permite (Yahoo fora do ar não pode travar a operação;
//     a rede de segurança real é o risk manager + stop-loss)
func GateTrade(regime, side string) (bool, string) {
	switch {
	case regime == "risk-off" && side == "buy":
		return false, "risk-off: segurar compra (aversão ao risco global)"
	case regime == "risk-on" && side == "sell":
		return false, "risk-on: segurar venda (mercado construtivo)"
	default:
		return true, ""
	}
}
