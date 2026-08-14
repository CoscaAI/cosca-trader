# COSCA TRADER

Plataforma de trade profissional **multiplataforma com IA cognitiva** — opera
cripto (Binance, Coinbase, CoinEx) e bolsa brasileira (B3). Nível enterprise:
gráficos elegantes, sistema de plugins, OMS completo, controle de risco,
indicadores, assistente IA e rastreamento total.

Construída sobre os padrões da família Cosca: core headless em Go orientado a
eventos, clientes finos (desktop Wails + web), event store append-only para
rastro total, e IA cognitiva (padrões do cosca-neural-link).

## Arquitetura

```
CLIENTES: Desktop (Wails+React) · Web · CLI
   ▲ RPC tipado (gRPC/HTTP)    ▲ stream (SSE/WS)
CORE (Go) — headless, event-driven
   Market Data · Execution (OMS) · Exchanges · Strategy
   Risk · Backtest · Cognitive AI
   Event Bus · Event Store (append-only) · SQLite · Stream
```

## Fases

- **F0** — Fundação: event bus, event store, schema de domínio, rastro total. ✅
- **F1** — Conectividade Binance + market data em tempo real (WebSocket). ✅
- **F2** — OMS: ordens, fills, posições, saldos, book de ofertas. ✅
- **F3** — Gráficos + indicadores (plugins).
- **F4** — Risco (sizing, stop/target, drawdown) + opções.
- **F5** — Estratégias + backtest + paper trading.
- **F6** — Assistente IA cognitiva.
- **F7** — B3 (MetaTrader5/API de corretora) + Coinbase/CoinEx + empacotamento enterprise.

## Estrutura

```
internal/
  event/    Event Model + Event Bus (sistema nervoso)
  domain/   Tipos de negócio (instrumento, candle, ordem, posição, saldo, trade)
  store/    Event Store append-only + SQLite (persistência/rastro)
  stream/   Pub/sub em tempo real (SSE/WS)
  engine/   Composição raiz (Emit = persistir + publicar)
  exchange/ Abstração de corretoras (Provider Engine) + adapter Binance
  market/   Market Data Engine (exchange → eventos de mercado)
  oms/      Order Management System (ordens, posições, saldos, PnL)
frontend/   React + Vite + TypeScript (desktop Wails)
```

## Rodar

```bash
# core headless (porta 14126)
go run . --db .cosca/trader.db

# core + market data real da Binance
go run . --binance --symbol BTCUSDT --interval 1m

# execução autenticada (ordens/conta) — SEGURO (sandbox forçada):
BINANCE_API_KEY=... BINANCE_API_SECRET=... go run . --binance

# MODO PRODUÇÃO — dinheiro real (exige COSCA_TRADER_TOKEN; nunca sem ele):
COSCA_TRADER_TOKEN=... BINANCE_API_KEY=... BINANCE_API_SECRET=... go run . --live

# endpoints (todos os sensíveis exigem Bearer token)
curl http://127.0.0.1:14126/health
curl -H "Authorization: Bearer $COSCA_TRADER_TOKEN" http://127.0.0.1:14126/positions
curl -H "Authorization: Bearer $COSCA_TRADER_TOKEN" http://127.0.0.1:14126/balances
curl -X POST -H "Authorization: Bearer $COSCA_TRADER_TOKEN" http://127.0.0.1:14126/orders \
  -d '{"symbol":"BTCUSDT","side":"buy","type":"limit","quantity":1,"price":50000}'

# timeline (rastro total)
curl -H "Authorization: Bearer $COSCA_TRADER_TOKEN" http://127.0.0.1:14126/timeline

# stream SSE
curl -H "Authorization: Bearer $COSCA_TRADER_TOKEN" http://127.0.0.1:14126/events

# frontend (dev)
cd frontend && npm install && npm run dev

# desktop (Wails)
wails build -tags webkit2_41
```

## Segurança e operação (Fase 1 — blindagem)

O core opera **fail-closed**: nada sensível (ordens, posições, saldos, timeline,
SSE) é servido sem autenticação, e o modo de produção é **explícito**. Dinheiro
real exige decisão consciente do operador.

| Variável | Default | Efeito |
|---|---|---|
| `COSCA_TRADER_TOKEN` | — | Token Bearer **obrigatório** nos endpoints sensíveis. Sem token, eles respondem `401`. |
| `COSCA_TRADER_ALLOWED_ORIGINS` | vazio | Origens cross-origin permitidas (CSV). Vazio = CORS fechado: qualquer header `Origin` é bloqueado (`403`). Mata o CSRF localhost. |
| `COSCA_TRADER_KILL` | — | `1` = kill switch: `PlaceOrder`/`PlaceStopLoss` recusam novas ordens (`ErrKillSwitch`). Cancelamento continua permitido para desmontar posição. |
| `COSCA_TRADER_MAX_ORDER_USDT` | `1000` | Limite de notional (preço × quantidade) por ordem. Ordens market estimam o notional pelo preço corrente. |
| `COSCA_TRADER_STOP_LOSS_PCT` | `0` (desativado) | Stop-loss automático por posição (ex.: `0.05` = 5% abaixo/acima da entrada). |
| `COSCA_TRADER_ENV` | — | `live` alternativo à flag `--live`. |
| `COSCA_TRADER_PORT` / `COSCA_TRADER_DB` | `14126` / `.cosca/trader.db` | Porta HTTP e caminho do SQLite. |

**Modo produção é opt-in.** Por padrão, mesmo com `BINANCE_API_KEY`/
`BINANCE_API_SECRET` reais, o sistema **força a sandbox** (testnet). Só conecta em
`api.binance.com` com `--live` (ou `COSCA_TRADER_ENV=live`), que também exige
`COSCA_TRADER_TOKEN` no startup (fail-fast).

**Fluxo seguro (nunca pule etapas):**

1. **Testnet** — `BINANCE_API_KEY=... BINANCE_API_SECRET=... go run . --binance`
   (sandbox, zero risco). Use chaves geradas no painel de testnet.
2. **Paper** — rode o core em observação (`go run .`) com o frontend; valide o
   journal, o ledger e os controles de risco.
3. **Live** — `COSCA_TRADER_TOKEN=... BINANCE_API_KEY=... BINANCE_API_SECRET=... go run . --live`
   com ordens pequenas e `COSCA_TRADER_MAX_ORDER_USDT` conservador. Chaves com
   permissão mínima (sem retirada) na Binance.

**Invariantes de integridade (P0):**

- **Anti double-trade:** toda ordem carrega `client_order_id` (gerado se
  ausente) e a chave é única por requisição. Erro ambíguo do broker trava a
  chave até a reconciliação — o `Reconcile` adota ordens órfãs pelo
  `origClientOrderId` persistido no journal.
- **Metadados de instrumento (P1-1):** o core carrega o `/exchangeInfo` no
  startup (revalidado a cada 24h) e valida/arredonda a ordem contra
  `step_size`, `tick_size`, `min_qty` e `min_notional` **antes** de assinar —
  quantidade para baixo, preço para o tick mais próximo.
- **Stop-loss automático (P1-2):** com `COSCA_TRADER_STOP_LOSS_PCT`, ao abrir
  posição o OMS coloca um STOP no lado oposto a `pct%` do preço médio de
  entrada.

## Regra de negócio (pesquisa)

Falta conhecimento? Buscar no GitHub por estrelas:
`https://github.com/search?q=($search)&type=repositories&s=stars&o=desc`
(equivalente programático: `api.github.com/search/repositories?q=...&sort=stars&order=desc`)
