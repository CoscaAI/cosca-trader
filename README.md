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
- **F1** — Conectividade Binance + market data em tempo real (WebSocket).
- **F2** — OMS: ordens, fills, posições, saldos, book de ofertas.
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
frontend/   React + Vite + TypeScript (desktop Wails)
```

## Rodar

```bash
# core headless (porta 14126)
go run . --db .cosca/trader.db

# health
curl http://127.0.0.1:14126/health

# timeline (rastro total)
curl http://127.0.0.1:14126/timeline

# stream SSE
curl http://127.0.0.1:14126/events

# frontend (dev)
cd frontend && npm install && npm run dev

# desktop (Wails)
wails build -tags webkit2_41
```

## Regra de negócio (pesquisa)

Falta conhecimento? Buscar no GitHub por estrelas:
`https://github.com/search?q=($search)&type=repositories&s=stars&o=desc`
(equivalente programático: `api.github.com/search/repositories?q=...&sort=stars&order=desc`)
