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
- **F3** — Gráficos + indicadores + painel de trading (frontend). ✅
- **F4** — Risco (sizing, stop/target, drawdown) + opções.
- **F5** — Estratégias + backtest + paper trading. (o **modo paper** já existe desde a Fase 2B; a Fase 3D deixou a **semente** da estratégia + backtest)
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
  paper/    Paper trading engine (broker simulado + candles sintéticos)
  strategy/ Estratégias (Fase 3D — semente do F5): EMA cross + backtest
frontend/   React + Vite + TypeScript (desktop Wails) — painel de trading F3
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

## Modo paper (Fase 2B — operar junto com o kernel, sem corretora)

O **paper trading engine** emula uma corretora em memória (fills simulados,
saldos virtuais, fees) — o kernel inteiro (OMS, event bus, HTTP, journal)
roda do mesmo jeito, mas o dinheiro é de mentira. Ideal para validar
estratégia, UX e os controles antes de testnet/live.

```bash
# PAPER com candles SINTÉTICOS (sem internet, sem chaves):
go run . --paper

# PAPER com market data REAL da Binance (os fills usam o preço ao vivo):
go run . --paper --binance --symbol BTCUSDT --interval 1m
```

`--paper` é **exclusivo** com `--live`/`--testnet` (conflito = erro). No modo
paper não há exigência de chaves Binance.

O que acontece ao operar:

1. `POST /orders` → o OMS roda a saga normal (intent durável → broker →
   `OrderCreated`), mas quem executa é o **paper broker**.
2. **market** preenche na hora no preço corrente; **limit** só preenche se o
   preço cruzar o limite; **stop** dispara no cruzamento do `stop_price`.
   Ordens não preenchidas ficam `NEW` com fundos travados (como em corretora
   real) e preenchem quando o preço se move.
3. Cada fill gera os mesmos eventos (`TradeExecuted`, `PositionOpened/Closed`,
   `BalanceUpdated`) e o PnL é calculado pelo OMS com `decimal` exato.
4. `GET /paper` (autenticado) devolve o resumo: capital inicial, capital
   atual, PnL total, ordens abertas e trades simulados.

### Env vars do modo paper

| Variável | Default | Efeito |
|---|---|---|
| `COSCA_TRADER_PAPER_BALANCE` | `10000` | Caixa inicial em USDT (+ BTC/ETH/BNB de demonstração). |
| `COSCA_TRADER_PAPER_FEE_PCT` | `0.001` | Taxa por preenchimento (0.1%), debitada no ativo recebido. |
| `COSCA_TRADER_PAPER_SLIPPAGE` | `0` | Deslizamento de preço de execução (ex.: `0.01` = 1%). |
| `COSCA_TRADER_PAPER_SEED` | aleatório | Semente do feed de candles sintéticos — definir = demo reproduzível. |
| `COSCA_TRADER_PAPER_LIMIT_FILL_FRACTION` | `0.5` | Fase 3B: fração da quantidade preenchida por avaliação de preço em ordens limit. `0.5` = metade por vez (fills parciais); `0` = all-or-nothing. Market é sempre all-or-nothing. |
| `COSCA_TRADER_PAPER_FAST` | — | `1` = demo acelerada: velas sintéticas de 5s (a estratégia sinaliza em minutos, não em horas). Mesmo seed = mesmo random walk. |

## Fase 3 — MarkPrice vivo, fills parciais, painel de trading e estratégia

### 3A · MarkPrice vivo (PnL não realizado real)

Antes da Fase 3A, `Position.MarkPrice` ficava em `0` — o `/positions` mostrava a
posição, mas o PnL **não realizado** (o que o operador enxerga na tela) nunca
era calculado. Agora:

- O OMS ganhou `ApplyMarkPrice(symbol, exchange, mark)`: alimenta o `MarkPrice`
  da posição a partir dos ticks de mercado e emite `PositionUpdated` (rate
  limit: no máximo 1× por segundo por posição). O mark é **market data** — o
  estado monetário de liquidação continua intocado.
- `main.go` conecta `MarketTick` → `ApplyMarkPrice` em **todo** modo de
  execução (paper com DemoFeed inclui — o tick alimenta o mark igual).
- `/positions` agora devolve `unrealized_pnl` e `unrealized_pnl_pct`
  calculados na fronteira de view com `UnrealizedPnLAt(mark)` (o domínio
  permanece intacto). Sem mark alimentado, o PnL não realizado é `0`.

### 3B · Fills parciais no paper broker

O paper broker preenchia tudo-ou-nada. Desde a Fase 3B, ordens **limit
grandes** preenchem em **múltiplos fills parciais** conforme o preço evolui:

- A cada `SetPrice`, uma ordem limit favorável preenche uma **fração** da
  quantidade original (`COSCA_TRADER_PAPER_LIMIT_FILL_FRACTION`, default
  `0.5`) — cada fatia emite `TradeExecuted` e a ordem avança de
  `new` → `partially_filled` → `filled`.
- **Market permanece all-or-nothing** (igual corretora real).
- Determinismo preservado: o fluxo de preços é o mesmo (seed + sequência
  injetável); o desbloqueio usa exatamente o montante **reservado** no lock
  (dinheiro exato, nunca aproximado por preço corrente).

### 3C · Frontend — painel de trading real

O frontend deixou de ser um health-check e virou o **painel de operação**
(React + Vite + TypeScript estrito + `lightweight-charts`):

- **Gráfico candlestick** ao vivo, alimentado pelo SSE `/events`
  (`MarketTick` reflete o preço na vela em formação; `CandleClosed` fecha a vela).
- **Topbar**: status do core, modo (paper/testnet/live), e o campo de **token**
  Bearer (persistido em `localStorage`, enviado em todas as chamadas).
- **Posições** (com PnL não realizado e %), **saldos** (livre/travado),
  **ordens** (formulário market/limit + lista recente), **paper**
  (capital/equity/PnL) e **timeline** compacta dos eventos (ordem/fill/posição).
- O `vite.config.ts` **proxya** o core (o CORS fail-closed do core continua
  fechado — o proxy remove o header `Origin`; a auth Bearer segue intacta).
- Tipos estritos em `src/types.ts`; cliente tipado em `src/api.ts`.

### 3D · Estratégia demonstrativa + backtest mínimo (semente do F5)

Para o operador ver uma estratégia operar no paper:

- `internal/strategy/`: interface `Strategy{ OnCandle(domain.Candle) []Signal; Name() }`
  + **`ema-cross`** (EMA 9/21 no fechamento; compra quando a rápida cruza
  acima da lenta, vende quando cruza abaixo; estado por símbolo).
- No modo paper, `CandleClosed` → estratégia → `OMS.PlaceOrder` (market),
  com limite de **1 ordem por sinal por direção** (sem spam). Eventos
  `StrategyStarted` / `StrategySignal` / `StrategyStopped` (este emitido no
  desligamento via handler de sinal).
- **Backtest mínimo** (`--backtest`): roda a estratégia sobre candles sem
  broker real e devolve PnL final + nº de trades (+ fees). Candles do rastro
  persistido se houver; senão sintéticos seedáveis. É CLI-only — semente do F5.

```bash
# backtest (CLI, sem HTTP):
go run . --backtest --symbol BTCUSDT

# estratégia ema-cross no paper com demo acelerada (sinal em minutos):
COSCA_TRADER_PAPER_FAST=1 COSCA_TRADER_PAPER_SEED=9 go run . --paper --strategy ema-cross

# estratégia com candles reais da Binance:
go run . --paper --binance --symbol BTCUSDT --interval 1m --strategy ema-cross
```

> A quantidade por sinal é `COSCA_TRADER_STRATEGY_QTY` (default `0.001` do
> ativo base). A estratégia só opera no **modo paper** — nunca em live.

### Como rodar o frontend com proxy

```bash
# 1. core no ar (modo paper de exemplo) com token:
COSCA_TRADER_TOKEN=meu-token go run . --paper

# 2. frontend (dev) — o proxy aponta para o core em 127.0.0.1:14126:
cd frontend && npm install && npm run dev

# 3. abra http://localhost:5173 e cole o token no campo da topbar.
#    Porta do core diferente? aponte o proxy:
#    COSCA_TRADER_CORE_URL=http://127.0.0.1:14127 npm run dev
```

O painel fala com o core **só via proxy** (mesma origem): o CORS fail-closed
permanece fechado, e a autenticação Bearer é exigida nos endpoints sensíveis
(painel mostra 401 sem token válido).

## Rastreamento de intent (Fase 2A — nunca mais ordem órfã)

Antes da Fase 2A, a ordem só virava registro no journal quando `OrderCreated`
era emitido DEPOIS do sucesso do broker. Se o processo caísse entre o broker
aceitar e o emit, a ordem existia na Binance mas não no rastro — ficava órfã
até o `Reconcile` achá-la (e ele só varria símbolos conhecidos).

A Fase 2A fecha essa janela com um **order intent**: registro durável do
`client_order_id` + request, persistido **ANTES** de tocar o broker.

- **Fase 1 (pré-broker):** o intent é gravado como `pending`. Se a
  persistência falhar, a ordem **nunca** é enviada (falha limpa — sem ordem
  fantasma).
- **Fase 2 (broker):** envio à exchange.
- **Fase 3 (pós-broker):** sucesso → `OrderCreated` + intent `submitted`;
  erro → saga consulta a exchange por `origClientOrderId` — adota se existir
  (`submitted`), marca `failed` se confirmadamente não existir.

O **`Reconcile`** (startup/reconexão) consulta **todos** os intents
`pending`/`submitted` e pergunta à exchange por `origClientOrderId`: se a
ordem existir, adota (registra + emite `OrderCreated`); se não, marca
`failed`. Mesmo sem símbolo conhecido, a ordem do crash window é recuperada.

Intents que chegam a estado terminal (`filled`/`canceled`/`expired`) são
marcados `done` e deixam de ser varridos.

**Fail-parado (dinheiro):** eventos de dinheiro (`OrderCreated`,
`OrderSubmitted`, `TradeExecuted`, `PositionOpened/Closed`, `BalanceUpdated`)
que não persistirem fazem o caminho síncrono **retornar erro** em vez de
fingir sucesso — o `PlaceOrder` devolve erro (a ordem existe na exchange e o
intent `pending` garante a recuperação). Eventos de market data
(tick/candle) continuam tolerantes a falha de persistência.

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
2. **Paper** — `go run . --paper` (ou com `--binance` para preços reais);
   valide estratégia, journal, ledger e controles de risco com dinheiro
   fictício, no núcleo real.
3. **Live** — `COSCA_TRADER_TOKEN=... BINANCE_API_KEY=... BINANCE_API_SECRET=... go run . --live`
   com ordens pequenas e `COSCA_TRADER_MAX_ORDER_USDT` conservador. Chaves com
   permissão mínima (sem retirada) na Binance.

**Invariantes de integridade (P0):**

- **Anti double-trade:** toda ordem carrega `client_order_id` (gerado se
  ausente) e a chave é única por requisição. Erro ambíguo do broker trava a
  chave até a reconciliação — o `Reconcile` adota ordens órfãs pelo
  `origClientOrderId` persistido no journal **e nos intents duráveis**
  (ver "Rastreamento de intent").
- **Metadados de instrumento (P1-1):** o core carrega o `/exchangeInfo` no
  startup (revalidado a cada 24h) e valida/arredonda a ordem contra
  `step_size`, `tick_size`, `min_qty` e `min_notional` **antes** de assinar —
  quantidade para baixo, preço para o tick mais próximo.
- **Stop-loss automático (P1-2):** com `COSCA_TRADER_STOP_LOSS_PCT`, ao abrir
  posição o OMS coloca um STOP no lado oposto a `pct%` do preço médio de
  entrada.

## Fase 4 — Camada de risco (proteção do capital)

A camada de risco (`internal/risk`) protege o capital antes de cada ordem e
continuamente durante a operação. Vale em **todos** os modos (paper, testnet,
live). Regras:

| Regra | Env | Default |
|-------|-----|---------|
| **Exposição por símbolo** — notional da ordem ≤ % do equity | `COSCA_TRADER_MAX_EXPOSURE_PCT` | 0.20 (20%) |
| **Exposição total** — soma das posições + ordem ≤ % do equity | `COSCA_TRADER_MAX_TOTAL_EXPOSURE_PCT` | 0.50 (50%) |
| **Drawdown máximo** — do pico de equity; ao atingir, trading é PAUSADO | `COSCA_TRADER_MAX_DRAWDOWN_PCT` | 0.10 (10%) |
| **Ordens abertas** — máximo simultâneo | (fixo) | 10 |
| **Rate limit local** — máx. ordens por minuto | (fixo) | 30 |

Como funciona:

1. **Fail-closed:** sem equity disponível, novas ordens são bloqueadas
   (`ErrEquityUnavailable`) — a casa nunca opera às cegas.
2. **Antes de cada ordem** o `PlaceOrder` consulta o risk manager: exposição
   pós-ordem, drawdown, ordens abertas e rate limit. Violou → ordem rejeitada
   com o erro da regra, **sem gastar client_order_id nem tocar a exchange**.
3. **A cada tick de mercado** o equity é reavaliado (pico e drawdown). Ao
   cruzar o drawdown máximo, o trading é **pausado** (`RiskBreach` no rastro)
   e só volta com `Resume` manual (ou se o equity se recuperar e o operador
   destravar).
4. **`GET /risk`** (autenticado) devolve o estado: equity, pico, drawdown %,
   trading pausado + razão, ordens/min.

O frontend mostra o card **Risco** com o drawdown % (em amarelo acima de 50%
do limite) e um **banner vermelho "TRADING PAUSADO"** quando o risco trava a
operação.

> **Nota (fail-open corrigido):** ordem market sem preço de referência era
> medida como exposição zero (furo). Agora, sem preço corrente, o sistema
> **bloqueia** a ordem — melhor parar do que arriscar às cegas.

## Fase 5 — A ferramenta científica de probabilidade (laudo estatístico)

Um backtest entrega **1 número**. A ciência entrega a **distribuição e a
probabilidade**. A Fase 5 transforma o motor em instrumento científico:

```bash
# CLI — laudo completo no terminal:
go run . --backtest --symbol BTCUSDT

# HTTP — o mesmo laudo via API (autenticado):
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:14126/backtest?symbol=BTCUSDT"
```

O laudo (`ScientificReport`) tem 5 camadas:

| Camada | O que responde |
|--------|----------------|
| **Stats** | win rate, profit factor, expectância, Sharpe, Sortino, drawdown máx, percentis P5/P25/P50/P75/P95 do PnL por trade |
| **Monte Carlo** (10k sims) | embaralha a ORDEM dos trades → P(perder), P(ruína), equity P5/P50/P95 — o risco de sequência |
| **Significância** (bootstrap) | p-value: a vantagem é real ou é o mesmo que sorteio de moeda? p≤0.05 = significativa (95% de confiança) |
| **Walk-forward** | treina em 70% do histórico, valida nos 30% out-of-sample — vantagem que sobrevive fora da amostra é provável de ser real |
| **Equity curve** | a curva completa para o gráfico no painel |

**Regra da casa:** nenhuma estratégia vai para testnet/live sem o laudo
aprovando — `significant=true`, `walk_forward.consistent=true` e
`monte_carlo.prob_of_loss < 0.5`. A fé não opera aqui; a probabilidade medida
opera.

O frontend mostra o card **"Ciência — laudo estatístico"** com o veredito
(VANTAGEM REAL / SEM VANTAGEM), as métricas e o resultado do Monte Carlo.

## Laboratório vivo — shadow trading + convergência (a resposta ao "o mercado muda")

O Don levantou o problema fundamental: *"parece funcionar mas ao entrar a ação
muda"*. Um backtest valida o passado; o mercado real pode ter mudado de regime.
A resposta científica é o **LABORATÓRIO VIVO** (`--shadow`): o sistema observa
o mercado REAL em tempo real (sem chave, sem dinheiro), registra cada sinal da
estratégia como uma **PREVISÃO**, e mede se a previsão está **CONVERGINDO** com
a realidade.

```bash
# Com dados REAIS da Binance (WebSocket público, sem chave):
COSCA_TRADER_TOKEN=meu-token go run . --shadow --symbol BTCUSDT --interval 1m --shadow-strategy ema-cross

# Com candles sintéticos ACELERADOS (velas de 5s) — ver funcionando em minutos:
COSCA_TRADER_TOKEN=meu-token go run . --shadow --shadow-demo --shadow-strategy breakout

# Acompanhar a convergência:
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:14126/convergence
```

### Como a convergência funciona

1. **Previsão** — cada sinal da estratégia vira uma previsão (direção + preço
   de entrada + horizonte de N velas).
2. **Realização** — quando o horizonte passa, o mercado REAL decide: a direção
   acertou? (hit/miss + retorno).
3. **Convergência** — o win rate OBSERVADO (janela deslizante) é comparado ao
   win rate ESPERADO (do laudo científico). O **z-score** mede os desvios:
   - `z ≥ -2` → **convergindo** (a previsão acompanha a realidade)
   - `z < -2` → **DIVERGENTE** (regime mudou; o sistema erra MUITO mais que o
     esperado)
4. **Reajuste automático** — na divergência, o monitor dispara o re-scan com
   dados frescos e aponta a melhor estratégia do momento. **A chave só entra
   quando uma estratégia está convergindo E passa no portão científico.**

O endpoint `/convergence` devolve: estratégia, esperado vs observado, z-score,
estado (convergindo/divergente), nº de reajustes.

## Regra de negócio (pesquisa)

Falta conhecimento? Buscar no GitHub por estrelas:
`https://github.com/search?q=($search)&type=repositories&s=stars&o=desc`
(equivalente programático: `api.github.com/search/repositories?q=...&sort=stars&order=desc`)

## Operação mínima (min_notional) e otimização

### Entrar com o MÍNIMO (o sizing do Don)

O sistema conhece as regras REAIS do instrumento (exchangeInfo) e calcula a
operação mínima viável — para operar com o menor capital possível:

- `risk.SizeMinNotional(rules, price)` → a menor quantidade que respeita
  `min_notional`, `min_qty` e `step_size` da exchange.
- O `PlaceOrder` valida o MÍNIMO (notional < min_notional → rejeita) e o MÁXIMO
  (maxOrderUSDT) antes de gastar client_order_id.
- **Exemplo real (BTCUSDT, filtro NOTIONAL novo):** min_notional = 5 USDT,
  preço 62945 → operação mínima = 0.00008 BTC (≈5 USDT).

> Nota: a Binance renomeou o filtro `MIN_NOTIONAL` para `NOTIONAL` — o parsing
> aceita ambos (o antigo foi pego retornando zero).

### Otimizador de acertividade (`strategy.Optimize`)

O otimizador varre combinações de parâmetros da estratégia no histórico real e
devolve a melhor por score (win rate 50% + Sharpe 30% + PF 20%). Exemplo real
na contrarian: win rate subiu de 38% (default) para **54%** (calibrado: RSI
sobrecomprado 80, sobrevendido 30, stop 1.5×ATR). A calibração vira varredura
sistemática, não tentativa-e-erro.

## Lições mineradas (Freqtrade × OctoBot × Jesse × Superalgos)

Referência: `.cosca/knowledge/mining-freqtrade-octobot-jesse-superalgos.md` —
mineração das 4 plataformas líderes (2026-08-14). Três lições já aplicadas:

### 1. Benchmark buy-and-hold (o EDGE contra o mercado)
O laudo agora reporta `benchmark`: o que comprar-e-segurar teria feito no
período, o retorno do bot, e o **EDGE** (diferença). Sem isso, um bot que
"lucra" num bull market parece bom quando na verdade perdeu do buy-and-hold.
**Regra: edge positivo é o que importa, não lucro absoluto.**

### 2. Análise por sinal (enter_tag)
Cada trade registra a TAG do sinal que o abriu. O laudo mostra o desempenho
POR SINAL: quantos trades, win rate, PnL e profit factor de cada sinal —
revelando quais sinais dão edge e quais só sangram (ex.: "EMA cruzou acima"
com 0% de win = o lado comprador é o problema).

### 3. Slippage paramétrico no backtest
`ExecConfig{SlippagePct}` degrada a execução (compra mais cara, venda mais
barata, stop no lado pessimista). A lição do Superalgos: "sem fees/slippage,
funciona no teste e quebra ao vivo". O backtest agora é pessimista por padrão.

### 4. Filtro NOTIONAL novo + operação mínima
A Binance renomeou `MIN_NOTIONAL` → `NOTIONAL`; o parsing aceita ambos. A
operação mínima real (ex.: BTCUSDT 5 USDT) é calculada e validada no
`PlaceOrder` (mínimo E máximo).

### 5. Proteções de mercado (StoplossGuard + Cooldown)

Lição do Freqtrade aplicada: circuit-breakers que pausam novas entradas quando
o mercado está adverso.

```bash
COSCA_TRADER_GUARD_MAX_STOPS=3      # N stops recentes → pausa (0 = off)
COSCA_TRADER_GUARD_LOOKBACK=12      # janela de velas do guard
COSCA_TRADER_GUARD_PAUSE_CANDLES=6  # velas de pausa ao disparar
COSCA_TRADER_COOLDOWN_CANDLES=5     # velas de cooldown por par após fechar
```

- **StoplossGuard**: 3 stops na janela → pausa entradas por 6 velas (o mercado
  está "armado" contra a estratégia; parar é a decisão lucrativa).
- **CooldownPeriod**: após fechar um trade, o par fica bloqueado por N velas —
  evita re-entrada imediata no mesmo movimento.

### 6. Testing farm (otimizador paralelo)

Lição do Superalgos aplicada: a varredura de parâmetros roda em worker pool
(tantos workers quanto CPUs), casos independentes em paralelo — resultado
IDÊNTICO ao serial (determinístico por seed e índice), mas N× mais rápido.
Ideal para varrer ativos × parâmetros × timeframes em escala.

### 7. Smart ordering (Jesse) + scanner multi-símbolo

- **Smart ordering**: o tipo da ordem é inferido do preço-alvo vs o preço
  corrente (lição do Jesse) — alvo < corrente → limit, alvo > corrente → stop,
  igual → market. A estratégia pode fixar o tipo (`sig.Type`) ou delegar.
- **Scanner multi-símbolo**: `--scan-symbols "BTCUSDT,ETHUSDT,SOLUSDT"`
  `--scan-intervals "1h,4h"` — varre combinações e rankeia globalmente. A
  testing farm do Superalgos em escala.

### 🏆 Primeira estratégia APROVADA no portão

A caçada multi-símbolo encontrou **bb-reversion em ETHUSDT 1h** (stop 0.6×ATR)
aprovada com p=0.016, PF=2.82, P(perder)=0%, walk-forward consistente e edge
+6.8% vs buy-and-hold. Candidata à chave da testnet.

## Proteção macro — monitor dos mercados GLOBAIS (S&P, NASDAQ, VIX, ouro, dólar)

O Don pediu: *"precisamos nos proteger de mercado estrangeiro... monitorar
junto pra saber o que acontece"*. O cripto não vive numa bolha — quando o S&P
cai forte, o BTC segue (risk-off). O `internal/marketindex` monitora os
mercados globais via Yahoo Finance (API pública, SEM chave):

```bash
# Ver o radar global agora:
go run . --markets
```

```
═══ MERCADOS GLOBAIS ═══
  S&P 500          7782.42  5d:+0.38%  10d:+2.39%
  NASDAQ           26692.07 5d:+0.33%  10d:+3.00%
  VIX (medo)         14.42  5d:-6.73%  10d:-9.08%
  Ouro             4432.70  5d:+1.63%  10d:+8.24%
  Dólar (DXY)        99.65  5d:-0.16%  10d:-0.24%
REGIME: risk-on — ✅ RISK-ON: mercados globais favoráveis
```

### Como protege

1. **Regime macro** calculado: risk-on / cautela / risk-off (S&P 5d, VIX > 25,
   ouro 5d, dólar 5d).
2. **O risk manager consulta o regime a cada ordem**: em **risk-off**, o limite
   de exposição cai pela METADE automaticamente — o mundo está em aversão ao
   risco, não se entra pesado no cripto.
3. Cache de 5min (sem custo por ordem); falha do radar = regime "desconhecido"
   (usa o limite normal — fail-safe conservador).

A proteção macro é automática: o Don vê o radar e o sistema se ajusta sozinho
quando o mundo vira contra o risco.
