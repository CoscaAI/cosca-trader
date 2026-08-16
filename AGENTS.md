# AGENTS.md — Contrato de Arquitetura do COSCA TRADER

> Instruções para agentes de IA (e desenvolvedores) que trabalham neste repositório.
> Este documento é a FONTE DA VERDADE da arquitetura. Leia antes de mexer.

## 1. Visão Geral

O **COSCA TRADER** é uma plataforma de trading de cripto com um **cérebro de duas
velocidades**:

```
Don → Kernel (chat/LLM, estratégico, segundos) → Bot HFT (tático, microssegundos)
                                                    │
                                                    ├─ caminho EXATO     (mapa)        → 631ns
                                                    └─ caminho SEMÂNTICO (vetor→conceito) → 6µs
```

- **O MOTOR (estratégia/risco/OMS) é Go puro e determinístico.** A IA NÃO executa
  ordens — ela conversa, raciocina e orienta o bot.
- **O bot HFT** (`internal/bot/`) NUNCA chama LLM no caminho quente. Tudo é
  memória: vetores semânticos + grafo node-edge + operações pré-compiladas.
- **Latência alvo**: load < 1ms · decisão < 10µs · busca semântica < 10µs.

## 2. Stack

| Camada | Tecnologia |
|--------|-----------|
| Backend (core) | **Go 1.25** — módulo `github.com/CoscaAI/cosca-trader` |
| Frontend | **React 19 + TypeScript 5 + Vite 7** |
| Gráfico | `lightweight-charts` |
| Grid drag & drop | `react-grid-layout` (v2 — API `gridConfig`/`dragConfig`/`resizeConfig`) |
| Ícones | `lucide-react` (PADRÃO — nunca emoji/símbolo solto) |
| Classes | `clsx` via `shared/cn.ts` (sem Tailwind) |
| Persistência | `shared/storage.ts` (localStorage tipado) |
| Estilo | **CSS puro** (`style.css` com tokens) — sem Tailwind |

## 3. Estrutura de Diretórios

```
cosca-trader/
├── main.go              → entry point (modo paper/shadow/live, serve)
├── serve.go             → REST API + SSE (todos os endpoints)
├── internal/
│   ├── bot/             → 🧠 o kernel HFT (semantic.go + graph.go + bot.go)
│   ├── cognitive/       → Provider/Model/Registry (padrão Vercel) + Assistant
│   ├── diagnosis/       → laudo completo de ativo (backtest real + estatística)
│   ├── domain/          → modelos (Candle, Order, Position, Balance, ...)
│   ├── strategy/        → as 6 estratégias + backtest + stats + scanner
│   ├── engine/          → orquestração do motor
│   ├── exchange/        → adaptador (binance/) — market data + execução
│   ├── oms/             → order management (ordens/fills/posições)
│   ├── risk/            → risk manager (drawdown, fail-closed)
│   ├── paper/           → broker simulado (paper trading)
│   ├── ledger/          → WAL + MVCC (append-only)
│   ├── market/          → radar de moedas (coins.go — sidebar)
│   ├── marketindex/     → radar macro (regime, divergência, gate)
│   └── clock/event/stream/quantize/ratelimit/explore/store → infra
├── frontend/
│   └── src/
│       ├── App.tsx          → painel (cards + grid) — AINDA monolítico
│       ├── CoinsSidebar.tsx → sidebar de ativos (estilo TradingView)
│       ├── api.ts           → cliente tipado (CoreClient)
│       ├── types.ts         → tipos compartilhados
│       ├── shared/          → cn.ts + storage.ts (utilitários)
│       └── style.css        → design system (tokens)
└── tradingview.html     → referência de estrutura do TradingView (download do Don)
```

## 4. Comandos

```bash
# Backend
go build ./...
go test ./...
go vet ./...
go test ./internal/bot/ -bench . -benchmem   # benchmark do bot HFT

# Frontend (cd frontend)
npm run build      # tsc + vite build
npm run dev        # dev server em 5273 (strictPort)
npx tsc --noEmit   # typecheck

# Rodar
go run . --paper                    # modo paper (seguro)
COSCA_TRADER_TOKEN=xyz go run . --paper
```

## 5. Portas (contrato de endereçamento)

| Serviço | Porta | Nota |
|---------|-------|------|
| Core (API) | **24120** | `COSCA_TRADER_CORE_URL` sobrescreve |
| Frontend (Vite) | **5273** | strictPort — nunca "pula" de porta |
| Ollama (local) | 11434 | provider de LLM/embedding |

A 24120 é ÚNICA e ALTA — FORA da família 1412x do Cosca (14120 serve, 14123 runtime).

## 6. Estratégias (o catálogo — `internal/strategy/registry.go`)

`ema-cross` · `momentum` · `rsi-reversion` · `breakout` · `bb-reversion` · `contrarian`

Adicionar estratégia = criar o arquivo + registrar no `registry` (um único lugar).
Toda estratégia implementa: `Name() string` + `OnCandle(c domain.Candle) []Signal`.

## 7. Convenções de Estilo

### Go
- **Decimal para dinheiro**: SEMPRE `github.com/shopspring/decimal`. NUNCA `float64`
  para valores monetários (lição dura: precisão binária destrói o ledger).
- **Erros explícitos**: nunca ignore com `_`. Propague com contexto (`fmt.Errorf("x: %w", err)`).
- **Fail-closed**: segurança e risco travam por padrão; nunca "fail-open" silencioso.
- **Regra da família**: nunca `pkill` — sempre `kill -TERM <pid>`.

### React/TypeScript
- **Classes**: use `cn(...)` de `shared/cn.ts` (nunca template string com ternário).
- **Ícones**: `lucide-react` (nunca emoji). Ex.: `<Gem />`, `<TrendingUp />`.
- **Persistência**: `Storage.get/set/remove` de `shared/storage.ts` (nunca `localStorage` cru).
- **Tipos de API**: declare em `types.ts` (nunca `any` nos contratos).

## 8. Contrato de Arquitetura (decisões-chave)

### 8.1 O bot HFT (`internal/bot/`)
- `semantic.go` — vetores determinísticos (feature hashing) + `Enrich()` para embedding real.
- `graph.go` — grafo node-edge (símbolo→estratégia→sinal→ação→regime).
- `bot.go` — `Execute(cmd)` em 2 caminhos: exato (mapa) + semântico (vetor).
- **NUNCA chama LLM no hot path.** O kernel compila; o bot executa.

### 8.2 O cognitive (`internal/cognitive/`)
- `Provider`/`Model`/`Registry` (padrão Vercel AI SDK).
- **Um cliente `openAICompat`** serve ollama + openai + deepseek (mesmo protocolo).
- `Assistant` é o chat (cérebro lento) — nunca fica mudo (offlineReply determinístico).

### 8.3 O diagnosis (`internal/diagnosis/`)
- `Analyze(symbol, regime, candles)` → laudo: favorável, direção, confiança, ganho
  esperado + estatística completa (win-rate, profit factor, Sharpe, edge vs buy-and-hold).
- Aplica o **gate macro**: estratégia com edge mas regime contrário → bloqueada.

### 8.4 A API (`serve.go`)
- Auth **fail-closed** (Bearer obrigatório nos endpoints sensíveis) + CORS fechado.
- Endpoints: `/health` `/ticker` `/orders` `/positions` `/balances` `/ledger` `/paper`
  `/risk` `/backtest` `/convergence` `/markets` `/chat` `/candles` `/models`
  `/models/active` `/diagnosis` `/coins` `/events` (SSE) `/timeline`.
- Respostas: preferir structs tipadas com `json` tags (não `map[string]any` solto).

### 8.5 O frontend
- `App.tsx` ainda é monolítico (~1170 linhas) — ao tocar, EXTRAIA componentes
  para vertical slices (`features/<feature>/components|hooks|services`), o padrão
  do `react-redux-boilerplate` do Don. NÃO migre para Redux/Tailwind sem ordem.

## 9. Regras de Ouro (nunca viole)

1. **Motor é determinístico; IA não executa.** O LLM orienta, o bot opera.
2. **Dinheiro é `decimal.Decimal`.** Nunca float.
3. **Bot HFT = memória pura.** Nunca rede/LLM no hot path.
4. **Segurança fail-closed.** Auth, risco e regime travam por padrão.
5. **Ícone = lucide · classe = cn() · storage = Storage.** Padrões do projeto.
