# MARKETINDEX — Monitor de Mercados Globais (proteção macro)

> Missão do Don (2026-08-14): "precisamos nos proteger de mercado estrangeiro
> ex: nasdaq sp500 ouro etc... monitorar junto pra saber o que acontece"

O cripto não vive numa bolha. Quando o S&P 500 cai forte, o BTC segue
(risk-off); quando o ouro dispara, o dinheiro sai do risco. Este pacote
coleta os mercados GLOBAIS em tempo real (Yahoo Finance, sem chave) para o
sistema saber o que acontece lá fora — a proteção macro.

## Ativos monitorados

| Símbolo | Ativo | Por que importa |
|---------|-------|-----------------|
| ^GSPC | S&P 500 | O termômetro do risk-on/risk-off global |
| ^IXIC | NASDAQ | O tech (correlacionado ao cripto) |
| ^VIX | Índice de Volatilidade | O "medidor de medo" — VIX alto = pânico |
| GC=F | Ouro | O refúgio — sobe quando o risco desce |
| DX-Y.NYB | Dólar (DXY) | Liquidez global — DXY forte = pressão no cripto |

## Regimes de proteção

- **Risk-ON**: S&P subindo + VIX baixo + ouro caindo → mercado favorável ao
  cripto.
- **Risk-OFF**: S&P caindo + VIX disparando + ouro subindo → proteção: reduzir
  exposição cripto (o StoplossGuard/cooldown são acionados).
- **DXY forte**: dólar subindo forte → pressão de venda em ativos de risco.
