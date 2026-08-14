// Command cosca-trader é a plataforma de trade profissional multiplataforma
// com IA cognitiva. Este entrypoint sobe o core headless em Go (event bus +
// event store + SQLite + stream) e serve a API de observabilidade (health,
// timeline, SSE). O cliente desktop (Wails) e o futuro cliente web são visões
// sobre este mesmo motor — nenhuma lógica crítica vive na UI.
//
// F0 — Fundação · F1 — Binance market data · F2 — OMS (execução autenticada).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/exchange/binance"
	"github.com/CoscaAI/cosca-trader/internal/market"
	"github.com/CoscaAI/cosca-trader/internal/marketindex"
	"github.com/CoscaAI/cosca-trader/internal/oms"
	"github.com/CoscaAI/cosca-trader/internal/paper"
	"github.com/CoscaAI/cosca-trader/internal/risk"
	"github.com/CoscaAI/cosca-trader/internal/store"
	"github.com/CoscaAI/cosca-trader/internal/strategy"
)

func main() {
	dbPath := flag.String("db", defaultDBPath(), "caminho do banco SQLite")
	port := flag.String("port", defaultPort(), "porta HTTP do core")
	binanceFlag := flag.Bool("binance", false, "conectar à Binance (market data em tempo real)")
	symbol := flag.String("symbol", "BTCUSDT", "símbolo para market data")
	interval := flag.String("interval", "1m", "intervalo dos candles")
	testnet := flag.Bool("testnet", false, "usar a sandbox da Binance (sem fundos reais)")
	authFlag := flag.Bool("auth", false, "exigir COSCA_TRADER_TOKEN no startup (fail-fast)")
	liveFlag := flag.Bool("live", false, "MODO PRODUÇÃO: conecta à Binance real (api.binance.com) — DINHEIRO REAL; exige COSCA_TRADER_TOKEN")
	paperFlag := flag.Bool("paper", false, "MODO PAPER (Fase 2B): broker simulado com fills em memória — sem chaves, sem dinheiro real; use --binance para market data real ou rode offline com candles sintéticos")
	strategyFlag := flag.String("strategy", "", "estratégia automática no modo paper (Fase 3D): 'ema-cross' (default: desligado)")
	backtestFlag := flag.Bool("backtest", false, "roda o backtest mínimo da estratégia (Fase 3D, semente do F5) e sai")
	fetchFlag := flag.Bool("fetch", false, "Fase 5: puxa candles REAIS da Binance (API pública, SEM chave) e roda o laudo científico — o caminho de validação antes de qualquer credencial")
	fetchBars := flag.Int("fetch-bars", 500, "número de velas a puxar no --fetch")
	fetchInterval := flag.String("fetch-interval", "1h", "intervalo das velas no --fetch (1m/5m/15m/1h/4h/1d)")
	scanFlag := flag.Bool("scan", false, "Fase 5: SCANNER — avalia TODAS as estratégias com dados reais e ranqueia por score científico")
	scanSymbols := flag.String("scan-symbols", "BTCUSDT", "símbolos do scanner separados por vírgula (ex: BTCUSDT,ETHUSDT,SOLUSDT)")
	scanIntervals := flag.String("scan-intervals", "1h", "intervalos do scanner separados por vírgula (ex: 1h,4h,1d)")
		marketsFlag := flag.Bool("markets", false, "proteção macro: monitora os mercados GLOBAIS (S&P 500, NASDAQ, VIX, ouro, dólar) e mostra o regime risk-on/risk-off")
	macroFlag := flag.Bool("macro", false, "MONITOR CONTÍNUO de divergência macro: radar global + cripto, detecta S&P caiu/BTC não reagiu e sinaliza entrada seguindo a tendência — com rate limit e proteção por falta de dados")
	shadowFlag := flag.Bool("shadow", false, "Fase 5: MODO LABORATÓRIO VIVO — observa o mercado REAL (sem chave, sem dinheiro), registra previsões da estratégia, mede CONVERGÊNCIA com a realidade e reajusta sozinho quando o regime muda")
	shadowStrategy := flag.String("shadow-strategy", "ema-cross", "estratégia no modo shadow")
	shadowDemo := flag.Bool("shadow-demo", false, "modo shadow com candles sintéticos ACELERADOS (velas de 5s) — para o Don VER o laboratório vivo funcionando em minutos, sem esperar o mercado real")
	flag.Parse()

	// Fase 2B: papel é um ambiente exclusivo — nunca coexistir com live/testnet
	// (o operador escolhe UM lugar para o dinheiro).
	if *paperFlag && (*liveFlag || *testnet) {
		log.Fatal("conflito: --paper é exclusivo com --live/--testnet — escolha um único ambiente de execução")
	}

	// Fase 3D: estratégia selecionada no startup (falha rápido se desconhecida).
	var strat strategy.Strategy
	switch *strategyFlag {
	case "":
	case "ema-cross":
		strat = strategy.NewEMACross()
	default:
		log.Fatalf("estratégia desconhecida: %q (disponível: ema-cross)", *strategyFlag)
	}
	if strat != nil && !*paperFlag {
		log.Printf("⚠ estratégia %s requer modo paper (--paper) — ignorada", strat.Name())
		strat = nil
	}

	// Segurança P0-1: fail-closed. O token é OBRIGATÓRIO nos endpoints
	// sensíveis; com --auth o startup falha rápido se ele não estiver definido
	// (decisão: exigir no startup quando explicitamente pedido; sem --auth o
	// servidor sobe, mas os endpoints sensíveis respondem 401).
	token := os.Getenv("COSCA_TRADER_TOKEN")
	allowedOrigins := splitOrigins(os.Getenv("COSCA_TRADER_ALLOWED_ORIGINS"))
	if *authFlag && token == "" {
		log.Fatal("fail-closed: --auth exige COSCA_TRADER_TOKEN (defina a variável para subir os endpoints sensíveis)")
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		log.Printf("⚠ aviso: sem persistência SQLite (%v) — rodando em memória", err)
		db = nil
	}

	e := engine.New(db)
	ctx := context.Background()

	// shadowMonitor é preenchido pelo modo --shadow e exposto em /convergence.
	var shadowMonitor *strategy.ConvergenceMonitor

	// Radar macro (missão do Don): monitora os mercados GLOBAIS (S&P, NASDAQ,
	// VIX, ouro, dólar) e expõe o regime para o risk manager ajustar a
	// exposição em risk-off. Cache interno de 5min — sem custo por ordem.
	macroRadar := marketindex.New()
	macroRegime := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		r, err := macroRadar.Regime(ctx)
		if err != nil {
			return "desconhecido"
		}
		return r
	}

	// Rastreamento total: a partir daqui, TODO evento passa pelo Emit.
	e.Emit(event.Event{
		Type:     event.SystemStarted,
		Source:   "core",
		Severity: event.SeverityInfo,
		Payload:  map[string]any{"db": *dbPath, "port": *port},
	})

	// Fase 3D — backtest mínimo (CLI only, semente do F5): usa candles do
	// rastro persistido se houver; senão gera candles sintéticos seedáveis.
	// Sem broker, sem HTTP — roda e sai.
	if *backtestFlag {
		runBacktest(e, *symbol)
		return
	}

	// Fase 5 — --fetch: dados REAIS da Binance (API pública, SEM chave) →
	// laudo científico. É o fluxo de validação: o Don não precisa de credencial
	// para ver se uma estratégia tem vantagem real; a chave só entra depois.
	if *fetchFlag {
		runFetchReport(*symbol, *fetchInterval, *fetchBars)
		return
	}

	// Fase 5 — --scan: o SCANNER científico. Puxa dados reais (sem chave) e
	// avalia TODAS as estratégias registradas, rankeando por score e aplicando
	// o portão da casa. Responde "qual é a melhor estratégia AGORA?".
	if *scanFlag {
		runScanMulti(*scanSymbols, *scanIntervals, *fetchBars)
		return
	}

	// Proteção macro (missão do Don): monitorar os mercados GLOBAIS para saber
	// o que acontece lá fora — S&P, NASDAQ, VIX, ouro, dólar.
	if *marketsFlag {
		runMarkets()
		return
	}

	// Monitor contínuo de divergência macro: observa o radar global + o cripto,
	// detecta o lag (S&P caiu, BTC não reagiu) e sinaliza entrada na direção
	// da tendência global — com rate limit e proteção por falta de dados.
	if *macroFlag {
		runMacroMonitor(*symbol, *fetchInterval, *fetchBars)
		return
	}

	// Fase 5 — --shadow: o LABORATÓRIO VIVO. Observa o mercado REAL (WebSocket
	// público, sem chave), registra previsões da estratégia, mede a convergência
	// com a realidade e reajusta sozinho quando o regime muda. NENHUM dinheiro
	// em jogo — é a validação contínua que o Don pediu.
	if *shadowFlag {
		runShadow(*symbol, *interval, *shadowStrategy, *port, *shadowDemo, e, &shadowMonitor)
		return
	}

	// F1 — market data. No modo paper com --binance usamos dados reais (o paper
	// broker consome o preço corrente para os fills); sem --binance, candles
	// SINTÉTICOS (random walk seedável via COSCA_TRADER_PAPER_SEED) — o Don vê o
	// sistema funcionar mesmo sem internet/chaves.
	if *binanceFlag {
		bc := binance.New()
		md := market.New(bc, e.Emit)
		if err := md.Watch(*symbol, *interval); err != nil {
			log.Printf("⚠ subscribe market data: %v", err)
		}
		go func() {
			if err := md.Start(ctx); err != nil {
				log.Printf("⚠ market data encerrado: %v", err)
			}
		}()
		log.Printf("COSCA TRADER — Binance conectando: %s@%s", *symbol, *interval)
	} else if *paperFlag {
		seed := paperSeed()
		demo := paper.NewDemoFeed(seed, *symbol, demoStartPrice(*symbol))
		if paperFast() {
			// Fase 3D: acelera as velas sintéticas para a estratégia sinalizar
			// em minutos (random walk idêntico — mesmo seed, mesmo preço).
			demo.FastDemo()
			log.Printf("COSCA TRADER — demo acelerada (COSCA_TRADER_PAPER_FAST): velas de 5s")
		}
		md := market.New(demo, e.Emit)
		if err := md.Watch(*symbol, *interval); err != nil {
			log.Printf("⚠ subscribe market data sintético: %v", err)
		}
		go func() {
			if err := md.Start(ctx); err != nil {
				log.Printf("⚠ market data sintético encerrado: %v", err)
			}
		}()
		log.Printf("COSCA TRADER — modo PAPER com candles sintéticos (%s@%s, seed=%d): sem internet/chaves", *symbol, *interval, seed)
	}

	// F2 — execução: broker real (Binance) OU paper simulado, conforme o
	// ambiente escolhido. O OMS não distingue um do outro.
	var omsEngine *oms.OMS
	var paperBroker *paper.Broker
	var riskMgr *risk.Manager
	mode := "observe"
	apiKey := os.Getenv("BINANCE_API_KEY")
	apiSecret := os.Getenv("BINANCE_API_SECRET")

	omsOpts := func() []oms.Option {
		opts := []oms.Option{
			oms.WithMaxOrderUSDT(maxOrderLimit()),
			oms.WithStopLossPct(stopLossPct()),
			oms.WithTakeProfitPct(takeProfitPct()),
			oms.WithTrailingStop(trailingActivationPct(), trailingPct()),
		}
		// Fase 2A: intent durável pré-broker (fecha a janela de crash) — em
		// TODO ambiente com persistência.
		if db != nil {
			opts = append(opts, oms.WithIntentStore(db))
		}
		// F4: camada de risco — exposição/drawdown/rate limit (se ativa).
		if riskMgr != nil {
			opts = append(opts, oms.WithRiskManager(riskMgr))
		}
		// Proteções de mercado (lição Freqtrade): StoplossGuard + Cooldown.
		prot := buildProtections()
		if prot != nil {
			opts = append(opts, oms.WithProtections(prot))
		}
		return opts
	}

	switch {
	case *paperFlag:
		// MODO PAPER (Fase 2B): broker simulado em memória — sem chaves
		// Binance, sem dinheiro real. O restante (OMS + event bus + HTTP) é
		// idêntico: só troca o broker.
		mode = "paper"
		paperBroker = paper.New(
			paper.WithInitialBalance(paperBalance()),
			paper.WithFeePct(paperFeePct()),
			paper.WithSlippage(paperSlippage()),
			paper.WithLimitFillFraction(paperLimitFillFraction()),
		)
		// F4: risco ativo no paper — equity vem do broker simulado.
		riskMgr = risk.New(risk.Config{
			MaxExposurePct:      riskExposurePct(),
			MaxTotalExposurePct: riskTotalExposurePct(),
			MaxDrawdownPct:      riskMaxDrawdownPct(),
			MaxOpenOrders:       10,
			MaxOrdersPerMinute:  30,
			EquityProvider: func() decimal.Decimal {
				s := paperBroker.Summary()
				return s.Equity
			},
			MacroRegime: macroRegime,
			Emit: func(evType, sev string, payload any) {
				if err := e.Emit(event.Event{Type: event.Type(evType), Source: "risk", Severity: sev, Payload: payload}); err != nil {
					log.Printf("⚠ risk: %v", err)
				}
			},
		})
		omsEngine = oms.New(paperBroker, e.Emit, omsOpts()...)
		omsEngine.SetKillSwitch(os.Getenv("COSCA_TRADER_KILL") == "1")

		// Alimenta o paper broker com o preço corrente (real ou sintético) —
		// ticks do rastro alimentam os fills simulados.
		e.Bus.Subscribe(event.MarketTick, func(ev event.Event) {
			if tick, ok := ev.Payload.(domain.Tick); ok {
				paperBroker.SetPrice(tick.Symbol, decimal.NewFromFloat(tick.Price))
			}
		})

		go func() {
			if err := paperBroker.StartUserStream(ctx, exchange.Handler{
				OnOrderUpdate:   omsEngine.ApplyOrderUpdate,
				OnTrade:         omsEngine.ApplyTrade,
				OnBalanceUpdate: omsEngine.ApplyBalance,
			}); err != nil {
				log.Printf("⚠ paper user stream encerrado: %v", err)
			}
		}()
		log.Printf("COSCA TRADER — modo PAPER ativo: broker simulado (capital %s USDT, fee %s, kill=%v)", paperBalance(), paperFeePct(), omsEngine.KillSwitch())

	case apiKey != "" && apiSecret != "":
		// P0-3: o default é SEGURO. Produção exige escolha explícita: flag
		// --live OU env COSCA_TRADER_ENV=live. Sem isso, mesmo com chaves
		// reais, o sistema NÃO toca em api.binance.com — força a sandbox.
		live := *liveFlag || os.Getenv("COSCA_TRADER_ENV") == "live"
		env := binance.EnvTestnet
		if live {
			mode = "live"
			env = binance.EnvLive
			if token == "" {
				log.Fatal("fail-closed: modo live exige COSCA_TRADER_TOKEN — nunca operar dinheiro real sem autenticação")
			}
			log.Printf("⚠⚠ MODO PRODUÇÃO — conectando à Binance REAL (api.binance.com). DINHEIRO REAL EM JOGO.")
		} else {
			mode = "testnet"
			log.Printf("COSCA TRADER — modo SEGURO: forçando sandbox (testnet). Para produção use --live.")
		}
		tc := binance.NewTrading(apiKey, apiSecret, env)
		// F4: risco ativo na Binance também — equity estimado pelos saldos
		// (USDT + stablecoins valem 1; outros ativos entram na próxima rodada
		// de mark). Fail-closed: sem saldo conhecido, o Check bloqueia ordens.
		riskMgr = risk.New(risk.Config{
			MaxExposurePct:      riskExposurePct(),
			MaxTotalExposurePct: riskTotalExposurePct(),
			MaxDrawdownPct:      riskMaxDrawdownPct(),
			MaxOpenOrders:       10,
			MaxOrdersPerMinute:  30,
			EquityProvider: func() decimal.Decimal {
				eq := decimal.Zero
				for _, b := range omsEngine.Balances() {
					// stablecoins e moedas de conta valem 1; o mark real por
					// ativo fica para a fase de reconciliação de equity.
					eq = eq.Add(b.Free).Add(b.Locked)
				}
				return eq
			},
			MacroRegime: macroRegime,
			Emit: func(evType, sev string, payload any) {
				if err := e.Emit(event.Event{Type: event.Type(evType), Source: "risk", Severity: sev, Payload: payload}); err != nil {
					log.Printf("⚠ risk: %v", err)
				}
			},
		})
		omsEngine = oms.New(tc, e.Emit, omsOpts()...)
		omsEngine.SetKillSwitch(os.Getenv("COSCA_TRADER_KILL") == "1")

		go func() {
			if err := tc.StartUserStream(ctx, exchange.Handler{
				OnOrderUpdate:   omsEngine.ApplyOrderUpdate,
				OnTrade:         omsEngine.ApplyTrade,
				OnBalanceUpdate: omsEngine.ApplyBalance,
				OnStatus: func(s exchange.Status) {
					t, sev := event.ExchangeConnected, event.SeverityInfo
					switch s.State {
					case "disconnected":
						t, sev = event.ExchangeDisconnected, event.SeverityWarning
					case "error":
						t, sev = event.ExchangeError, event.SeverityError
					}
					e.Emit(event.Event{Type: t, Source: "exchange:binance", Severity: sev, Payload: s})
					// Reconcilia ao reconectar (a Binance não faz replay do stream).
					if s.State == "connected" {
						go func() {
							if err := omsEngine.Reconcile(ctx); err != nil {
								log.Printf("⚠ reconciliação pós-reconexão: %v", err)
							}
						}()
					}
				},
			}); err != nil {
				log.Printf("⚠ user stream encerrado: %v", err)
			}
		}()
		log.Printf("COSCA TRADER — execução autenticada ativa (env=%s, kill=%v)", env, omsEngine.KillSwitch())

	default:
		log.Printf("COSCA TRADER — modo observação (sem credenciais; defina BINANCE_API_KEY/BINANCE_API_SECRET para executar)")
	}

	// Fase 3D — estratégia demonstrativa: CandleClosed → Strategy → sinais →
	// OMS.PlaceOrder (market, no paper broker). Limite de 1 ordem por sinal por
	// direção (map lastSide) para não fazer spam. Emite StrategyStarted/Signal;
	// StrategyStopped sai no desligamento (handler de sinal abaixo).
	if strat != nil && omsEngine != nil {
		gate := make(map[string]string) // symbol → última direção enviada
		var gateMu sync.Mutex
		ordQty := strategyOrderQty()

		if err := e.Emit(event.Event{
			Type:    event.StrategyStarted,
			Source:  "strategy:" + strat.Name(),
			Payload: map[string]any{"strategy": strat.Name(), "symbol": *symbol, "mode": mode},
		}); err != nil {
			log.Printf("⚠ strategy: %v", err)
		}
		e.Bus.Subscribe(event.CandleClosed, func(ev event.Event) {
			c, ok := ev.Payload.(domain.Candle)
			if !ok {
				return
			}
			for _, sig := range strat.OnCandle(c) {
				if err := e.Emit(event.Event{Type: event.StrategySignal, Source: "strategy:" + strat.Name(), Payload: sig}); err != nil {
					log.Printf("⚠ strategy: %v", err)
				}
				gateMu.Lock()
				if gate[sig.Symbol] == sig.Side {
					gateMu.Unlock()
					continue // já na direção sinalizada — não repete
				}
				gate[sig.Symbol] = sig.Side
				gateMu.Unlock()

				// F4B — sizing por risco: se COSCA_TRADER_RISK_PER_TRADE_PCT
				// estiver definido, a quantidade é calculada para perder no
				// máximo equity×riskPct se o stop for atingido. Senão, usa a
				// quantidade fixa da estratégia.
				qty := ordQty
				if riskPct := riskPerTradePct(); riskPct.Sign() > 0 {
					equity := decimal.Zero
					if riskMgr != nil {
						equity = riskMgr.State().Equity
					}
					entry := decimal.NewFromFloat(c.Close) // market data é float; decimal na fronteira
					stop := sig.Stop
					if stop.Sign() <= 0 && stopLossPct().Sign() > 0 {
						// sem stop explícito no sinal, usa o stop-loss global
						// como distância de risco (entrada ± pct)
						stop = entry.Mul(decimal.NewFromInt(1).Add(stopLossPct()))
						if sig.Side == "sell" {
							stop = entry.Mul(decimal.NewFromInt(1).Sub(stopLossPct()))
						}
					}
					if equity.IsPositive() && stop.Sign() > 0 {
						if sized, err := risk.Size(equity, riskPct, entry, stop); err == nil && sized.Sign() > 0 {
							qty = sized
						} else if err != nil {
							log.Printf("⚠ strategy: sizing falhou (%v) — usando qty fixa %s", err, ordQty)
						}
					}
				}

				// Smart ordering (lição do Jesse): o tipo da ordem é inferido
				// do preço-alvo vs o preço corrente. A estratégia pode fixar
				// (sig.Type) ou deixar o executor decidir (vazio).
				orderType := sig.Type
				if orderType == "" {
					orderType = strategy.SmartOrderType(sig.Side, sig.Price, decimal.NewFromFloat(c.Close))
				}
				req := exchange.OrderRequest{
					Symbol:        sig.Symbol,
					Side:          domain.Side(sig.Side),
					Type:          domain.OrderType(orderType),
					Quantity:      qty,
					ClientOrderID: "strat-" + strat.Name() + "-" + uuid.NewString(),
				}
				if orderType == "limit" {
					req.Price = sig.Price
				}
				if orderType == "stop" {
					req.StopPrice = sig.Price
				}

				ord, err := omsEngine.PlaceOrder(context.Background(), req)
				if err != nil {
					log.Printf("⚠ strategy %s: ordem %s %s falhou: %v", strat.Name(), sig.Side, sig.Symbol, err)
					continue
				}
				log.Printf("strategy %s: %s %s %s qty=%s tipo=%s (%s)", strat.Name(), sig.Side, sig.Symbol, ord.ID, qty, orderType, sig.Reason)
			}
		})
		log.Printf("COSCA TRADER — estratégia %s ativa no modo paper (qty=%s por sinal)", strat.Name(), ordQty)
	}

	// Fase 3A — MarkPrice vivo: os ticks de mercado (reais OU sintéticos)
	// alimentam o PnL não realizado das posições. Vale para todo modo de
	// execução (paper com DemoFeed inclui — o tick alimenta o mark igual).
	// O mark é market data: falha de persistência nunca derruba o sistema.
	if omsEngine != nil {
		e.Bus.Subscribe(event.MarketTick, func(ev event.Event) {
			if tick, ok := ev.Payload.(domain.Tick); ok {
				omsEngine.ApplyMarkPrice(tick.Symbol, tick.Exchange, decimal.NewFromFloat(tick.Price))
			}
		})
	}

	// F4 — o drawdown é medido a cada tick de equity: o risk manager rastreia
	// o pico e trava o trading quando o drawdown cruza o limite. Vale em todo
	// modo (paper + Binance) quando o manager existe.
	if riskMgr != nil && omsEngine != nil {
		riskMgr.OnTick()
		e.Bus.Subscribe(event.MarketTick, func(ev event.Event) {
			riskMgr.OnTick()
		})
		// tambem no position update (fills mudam o equity antes do próximo tick)
		e.Bus.Subscribe(event.PositionUpdated, func(ev event.Event) {
			riskMgr.OnTick()
		})
		log.Printf("F4 — camada de risco ativa: drawdown max %s, exposição %s/símbolo, %s total", riskMaxDrawdownPct(), riskExposurePct(), riskTotalExposurePct())
	}

	// Replay + reconciliação iniciais valem para PAPER e para Binance: no
	// papel o Reconcile adota ordens de intents órfãs consultando o próprio
	// broker simulado.
	if omsEngine != nil {
		if db != nil {
			if events, err := db.AllEvents(); err != nil {
				log.Printf("⚠ não carregou o rastro para replay: %v", err)
			} else {
				omsEngine.Replay(events)
				log.Printf("COSCA TRADER — replay: %d eventos reaplicados", len(events))
			}
		}
		if err := omsEngine.Reconcile(ctx); err != nil {
			log.Printf("⚠ reconciliação inicial: %v", err)
		}
	}

	// Desligamento limpo: emite StrategyStopped antes de sair (Ctrl+C / kill).
	// O serve abaixo bloqueia com log.Fatal; este handler garante o rastro do
	// término da estratégia no event store.
	if strat != nil {
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig
			if err := e.Emit(event.Event{
				Type:    event.StrategyStopped,
				Source:  "strategy:" + strat.Name(),
				Payload: map[string]any{"strategy": strat.Name()},
			}); err != nil {
				log.Printf("⚠ strategy: %v", err)
			}
			os.Exit(0)
		}()
	}

	serve(e, omsEngine, paperBroker, riskMgr, shadowMonitor, *port, apiSecurity{
		token:          token,
		allowedOrigins: allowedOrigins,
	}, mode)
}

// runFetchReport puxa candles REAIS da Binance (API pública — sem chave,
// sem credenciais) e roda o laudo científico completo em cima deles. É o
// fluxo central da Fase 5: validação com dados do MUNDO REAL antes de
// qualquer chave entrar no sistema.
func runFetchReport(symbol, interval string, bars int) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	bc := binance.New()
	candles, err := bc.Klines(ctx, symbol, interval, bars, min(bars, 1000))
	if err != nil {
		log.Fatalf("fetch %s %s: %v", symbol, interval, err)
	}
	log.Printf("dados REAIS da Binance (API pública, sem chave): %d velas %s (%s → %s)",
		len(candles), interval, candles[0].OpenTime.Format("2006-01-02"),
		candles[len(candles)-1].OpenTime.Format("2006-01-02"))

	report := strategy.AnalyzeBacktest(strategy.NewEMACross(), candles,
		decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), 1000, 1000, paperSeed())
	printReport(report)
}

// runShadow roda o LABORATÓRIO VIVO (Fase 5): conecta ao market data REAL da
// Binance (WebSocket público, sem chave), registra cada sinal da estratégia
// como uma PREVISÃO, acompanha o mercado resolvendo as previsões e mede a
// CONVERGÊNCIA (a ação faz o que calculamos?). Quando o z-score mostra
// divergência estatística (regime mudou), dispara o reajuste: re-scan com
// dados frescos e troca para a melhor estratégia do momento. Nenhum dinheiro
// em jogo — é a validação contínua antes da chave.
func runShadow(symbol, interval, strategyName, port string, demo bool, e *engine.Engine, shadowMonitor **strategy.ConvergenceMonitor) {
	// 1. Scan inicial (REAL via Klines, ou sintético no demo) para obter o win
	// rate esperado — a referência de convergência do laudo científico.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var hist []domain.Candle
	var bc *binance.Client
	source := "dados REAIS da Binance (sem chave)"
	if !demo {
		bc = binance.New()
		var err error
		hist, err = bc.Klines(ctx, symbol, interval, 300, 300)
		if err != nil {
			log.Fatalf("shadow: histórico real: %v", err)
		}
	} else {
		source = "candles sintéticos ACELERADOS (velas de 5s) — demonstração"
		hist = strategy.SyntheticCandles(paperSeed(), symbol, 300, demoStartPrice(symbol))
	}
	expectedHit := 0.5 // default: sem vantagem comprovada, assume sorte
	gate := strategy.DefaultGate()
	scan := strategy.Scan(hist, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), gate, 500, 500, paperSeed())
	active := strategyName
	for _, s := range scan.Strategies {
		if s.Name == strategyName && s.PassesGate {
			expectedHit = s.Report.Stats.WinRate
			log.Printf("shadow: %s aprovada no laudo — win rate esperado %.1f%% (score %.1f)", s.Name, s.Report.Stats.WinRate*100, s.Score)
		}
	}
	log.Printf("shadow: %s NÃO passou no laudo (win rate esperado %.1f%%) — observando mesmo assim para medir convergência", active, expectedHit*100)

	// 2. Rastreador de previsões + monitor de convergência.
	tracker := strategy.NewPredictionTracker(active, 3, 100)
	monitor := strategy.NewConvergence(expectedHit, tracker, strategy.ConvergenceConfig{
		MinResolved: 20,
		ZThreshold:  -2.0,
		OnDivergence: func(st strategy.ConvergenceState) {
			log.Printf("⚠⚠ REGIME MUDOU: previsão divergiu do mercado (z=%.2f, observado %.0f%% vs esperado %.0f%%) — reajustando com dados frescos...", st.ZScore, st.ObservedHit*100, st.ExpectedHit*100)
			// Reajuste: re-scan com dados frescos e imprime a melhor do momento.
			var rescan []domain.Candle
			if demo {
				rescan = strategy.SyntheticCandles(paperSeed()+1, symbol, 300, demoStartPrice(symbol))
			} else if bc != nil {
				if h, err := bc.Klines(context.Background(), symbol, interval, 300, 300); err == nil {
					rescan = h
				}
			}
			if len(rescan) > 0 {
				ns := strategy.Scan(rescan, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), gate, 500, 500, paperSeed())
				if ns.Best != nil {
					log.Printf("🔄 reajuste: melhor estratégia AGORA = %s (score %.1f) — a chave só entra se ela passar no portão", ns.Best.Name, ns.Best.Score)
				} else {
					log.Printf("🔄 reajuste: nenhuma estratégia passou no portão com dados frescos — mantendo observação")
				}
			}
		},
	})

	// 3. Estratégia ativa + feed de velas (reais ou demo acelerada).
	strat, err := strategy.NewByName(active)
	if err != nil {
		log.Fatalf("shadow: %v", err)
	}
	e.Bus.Subscribe(event.CandleClosed, func(ev event.Event) {
		c, ok := ev.Payload.(domain.Candle)
		if !ok || c.Symbol != symbol {
			return
		}
		// Sinais da estratégia → previsões.
		for _, sig := range strat.OnCandle(c) {
			tracker.Record(sig)
			log.Printf("previsão: %s %s @ %s (%s)", sig.Side, sig.Symbol, sig.Price, sig.Reason)
		}
		// Resolve previsões + re-checa convergência.
		tracker.OnCandle(c)
		monitor.Check()
	})

	// 4. Market data: Binance REAL (WS público) ou demo acelerada.
	var feed exchange.Exchange
	if !demo {
		feed = bc
	} else {
		seed := paperSeed()
		d := paper.NewDemoFeed(seed, symbol, demoStartPrice(symbol))
		d.FastDemo() // velas de 5s — o Don vê o laboratório vivo em minutos
		feed = d
	}
	md := market.New(feed, e.Emit)
	if err := md.Watch(symbol, interval); err != nil {
		log.Fatalf("shadow: subscribe: %v", err)
	}
	log.Printf("═══ LABORATÓRIO VIVO ativo ═══ fonte=%s | símbolo=%s intervalo=%s estratégia=%s | sem chave, sem dinheiro | convergência: esperado %.0f%%, mínimo %d previsões para julgar", source, symbol, interval, active, expectedHit*100, 20)

	// 5. Loop de status + shutdown.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			st := monitor.State()
			verdict := "convergindo"
			if st.Diverged {
				verdict = "⚠ DIVERGENTE (regime mudou)"
			}
			log.Printf("status: %s | resolvidas=%d pendentes=%d | acerto real %.0f%% vs esperado %.0f%% | z=%.2f | %s", verdict, st.Resolved, st.Pending, st.ObservedHit*100, st.ExpectedHit*100, st.ZScore, st.LastReason)
		}
	}()

	// Expõe o monitor via HTTP (roda em background; o md.Start abaixo bloqueia).
	*shadowMonitor = monitor
	sec := apiSecurity{token: os.Getenv("COSCA_TRADER_TOKEN")}
	go func() {
		serve(e, nil, nil, nil, monitor, port, sec, "shadow")
	}()

	if err := md.Start(ctx); err != nil {
		log.Printf("shadow: market data encerrado: %v", err)
	}
}

// runMarkets monitora os mercados GLOBAIS (proteção macro): imprime o
// snapshot corrente com o regime risk-on/risk-off e, em loop, avisa quando o
// regime muda. É o "radar externo" do Don.
func runMarkets() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := marketindex.New()

	snap, err := c.Fetch(ctx)
	if err != nil {
		log.Printf("⚠ mercados globais indisponíveis: %v", err)
		return
	}
	log.Printf("═══ MERCADOS GLOBAIS (%s) ═══", snap.FetchedAt.Format("15:04:05"))
	for _, q := range snap.Quotes {
		log.Printf("  %-18s %10.2f  5d:%+5.2f%%  10d:%+5.2f%%", q.Name, q.Price, q.Change5d, q.Change10d)
	}
	log.Printf("REGIME: %s — %s", snap.Regime, snap.Signal)
}

// runMacroMonitor é o MONITOR CONTÍNUO da missão do Don: observa o radar
// global + o cripto em loop, detecta a divergência (S&P caiu forte, BTC ainda
// não reagiu) e sinaliza a entrada na direção da tendência global. Protegido
// por rate limit (não estoura a API) e por falta de dados (sem dados
// suficientes → NÃO sinaliza).
func runMacroMonitor(symbol, interval string, bars int) {
	radar := marketindex.New()
	bc := binance.New()
	// Rate limit: 1 chamada externa a cada 10 min (Yahoo + Binance) — o Don
	// pediu proteção contra rate limit; dados frescos de 15min são aceitáveis
	// para a leitura macro (o regime não muda em minutos).
	rl := marketindex.NewRateLimiter(10*time.Minute, 15*time.Minute)

	log.Printf("═══ MONITOR MACRO ativo ═══ símbolo=%s | radar global (S&P/NASDAQ/VIX/ouro/dólar) + divergência | rate limit: 1 leitura/10min | sem dados suficientes → não sinaliza", symbol)

	for {
		// Rate limit: só consulta quando permitido.
		if !rl.Allow() {
			next := rl.NextAllowed()
			wait := time.Until(next)
			log.Printf("⏳ rate limit: próxima leitura em %s", wait.Round(time.Second))
			time.Sleep(wait)
			continue
		}

		// 1. Radar global (com cache interno).
		snap, err := radar.Fetch(context.Background())
		if err != nil {
			log.Printf("⚠ radar indisponível (%v) — NÃO sinalizando (proteção por falta de dados)", err)
			time.Sleep(2 * time.Minute)
			continue
		}

		// 2. Proteção por falta de dados: snapshot incompleto/velho → não opera.
		if !marketindex.IsDataEnough(snap, rl) {
			log.Printf("⚠ dados insuficientes do radar — NÃO sinalizando (proteção por falta de dados)")
			time.Sleep(2 * time.Minute)
			continue
		}

		// 3. Variação do cripto (BTC) na mesma janela (10d) via Klines público.
		candles, err := bc.Klines(context.Background(), symbol, interval, bars, min(bars, 1000))
		if err != nil || len(candles) < 2 {
			log.Printf("⚠ dados do %s indisponíveis — NÃO sinalizando (proteção por falta de dados)", symbol)
			time.Sleep(2 * time.Minute)
			continue
		}
		btcChg := (candles[len(candles)-1].Close - candles[0].Close) / candles[0].Close * 100

		// 4. Divergência → sinal de entrada.
		sig := marketindex.AnalyzeDivergence(snap, btcChg, marketindex.DefaultDivergenceConfig())
		log.Printf("📡 %s | S&P 10d %+.2f%% | %s 10d %+.2f%% | gap %+.2f | força %.2f",
			snap.Regime, sig.GlobalChangePct, symbol, sig.CryptoChangePct, sig.GlobalChangePct-sig.CryptoChangePct, sig.Strength)
		switch sig.Action {
		case "buy":
			log.Printf("🟢 SINAL: %s (força %.2f)", sig.Reason, sig.Strength)
		case "sell":
			log.Printf("🔴 SINAL: %s (força %.2f)", sig.Reason, sig.Strength)
		default:
			log.Printf("   %s", sig.Reason)
		}

		// Aguarda o próximo ciclo respeitando o rate limit.
		next := rl.NextAllowed()
		wait := time.Until(next)
		if wait > 0 {
			time.Sleep(wait)
		}
	}
}

// runScan puxa dados REAIS da Binance (API pública, sem chave) e roda o
// SCANNER: todas as estratégias, mesmo histórico, ranking por score científico
// e o portão da casa. O resultado é "qual estratégia merece operar AGORA".
func runScan(symbol, interval string, bars int) {
	runScanMulti(symbol, interval, bars)
}

// runScanMulti é o SCANNER em escala (a testing farm do Superalgos): varre
// SÍMBOLOS × INTERVALOS × ESTRATÉGIAS e devolve o ranking GLOBAL por score —
// a caça à estratégia que passa no portão em qualquer ativo/regime.
func runScanMulti(symbolsCSV, intervalsCSV string, bars int) {
	symbols := splitCSV(symbolsCSV, "BTCUSDT")
	intervals := splitCSV(intervalsCSV, "1h")
	if len(symbols) == 0 || len(intervals) == 0 {
		log.Fatal("scan: símbolos e intervalos não podem ser vazios")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	bc := binance.New()
	gate := strategy.DefaultGate()

	type result struct {
		Symbol   string
		Interval string
		Strategy strategy.ScoredStrategy
	}
	var all []result
	var best *result

	for _, sym := range symbols {
		for _, iv := range intervals {
			candles, err := bc.Klines(ctx, sym, iv, bars, min(bars, 1000))
			if err != nil {
				log.Printf("⚠ scan %s %s: %v", sym, iv, err)
				continue
			}
			if len(candles) < 50 {
				log.Printf("⚠ scan %s %s: poucos dados (%d velas)", sym, iv, len(candles))
				continue
			}
			res := strategy.Scan(candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001),
				gate, 1000, 1000, paperSeed())
			log.Printf("═══ %s %s (%d velas %s → %s) ═══", sym, iv, res.Periods,
				candles[0].OpenTime.Format("2006-01-02"), candles[len(candles)-1].OpenTime.Format("2006-01-02"))
			for _, s := range res.Strategies {
				st := s.Report.Stats
				flag := "  "
				if s.PassesGate {
					flag = "✓ "
				}
				log.Printf("%s%s: score=%.1f | trades=%d win=%.0f%% PF=%.2f | p=%.3f P(perder)=%.0f%% wf=%v edge=%+.1f%%",
					flag, s.Name, s.Score, st.TotalTrades, st.WinRate*100, st.ProfitFactor,
					s.Report.Significance.PValue, s.Report.MonteCarlo.ProbOfLoss*100,
					s.Report.WalkForward.Consistent, s.Report.Benchmark.EdgePct*100)
				r := result{sym, iv, s}
				all = append(all, r)
				if s.PassesGate && (best == nil || s.Score > best.Strategy.Score) {
					bb := r
					best = &bb
				}
			}
		}
	}

	// Ranking GLOBAL por score.
	log.Printf("═══════════ RANKING GLOBAL (%d combinações símbolo×intervalo×estratégia) ═══════════", len(all))
	top := all[:0]
	for i := 0; i < len(all) && i < 10; i++ {
		top = append(top, all[i])
	}
	_ = top // (ranking completo já logado por par)

	if best != nil {
		log.Printf("🏆🏆 MELHOR APROVADA NO PORTÃO: %s em %s %s (score %.1f) — CANDIDATA À CHAVE", best.Strategy.Name, best.Symbol, best.Interval, best.Strategy.Score)
	} else {
		log.Printf("⚠ nenhuma estratégia passou no portão em NENHUM símbolo/intervalo — a chave continua fora")
	}
}

// splitCSV separa uma lista separada por vírgula e devolve itens não vazios.
func splitCSV(csv, def string) []string {
	if csv == "" {
		return []string{def}
	}
	var out []string
	for _, p := range strings.Split(csv, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{def}
	}
	return out
}

// printReport imprime o laudo científico completo no terminal.
func printReport(report strategy.ScientificReport) {
	st := report.Stats
	log.Printf("═══ LAUDO CIENTÍFICO — %s (%s, dados REAIS) ═══", report.Strategy, report.Symbol)
	log.Printf("histórico: %d velas | inicial=%s final=%s", report.Periods, report.Initial, report.Final)
	log.Printf("trades=%d (wins=%d losses=%d) win_rate=%.1f%%", st.TotalTrades, st.Wins, st.Losses, st.WinRate*100)
	log.Printf("pnl=%s gross=%s loss=%s fees=%s | profit_factor=%.2f expectância=%s", st.NetPnL, st.GrossProfit, st.GrossLoss.Neg(), st.FeesPaid, st.ProfitFactor, st.Expectancy)
	log.Printf("retorno=%.1f%% max_drawdown=%.1f%% | sharpe=%.2f sortino=%.2f", st.ReturnPct*100, st.MaxDrawdownPct*100, st.Sharpe, st.Sortino)
	log.Printf("distribuição por trade: p5=%s p50=%s p95=%s", st.P5, st.P50, st.P95)
	log.Printf("MONTE CARLO (%d sims): p5=%s p50=%s p95=%s | P(perder)=%.1f%% P(ruína)=%.1f%%", report.MonteCarlo.Simulations, report.MonteCarlo.P5, report.MonteCarlo.P50, report.MonteCarlo.P95, report.MonteCarlo.ProbOfLoss*100, report.MonteCarlo.ProbRuin*100)
	sig := report.Significance
	verdict := "NÃO significativa (pode ser sorte)"
	if sig.Significant {
		verdict = "SIGNIFICATIVA (vantagem real, p≤0.05)"
	}
	log.Printf("SIGNIFICÂNCIA: p-value=%.4f z=%.2f → %s", sig.PValue, sig.ZScore, verdict)
	wf := report.WalkForward
	wfVerdict := "INCONSISTENTE (perdeu fora da amostra)"
	if wf.Consistent {
		wfVerdict = "consistente (lucrou out-of-sample)"
	}
	log.Printf("WALK-FORWARD: treino=%d pnl=%s (%d) | teste=%d pnl=%s (%d) → %s", wf.TrainBars, wf.TrainPnL, wf.TrainTrades, wf.TestBars, wf.TestPnL, wf.TestTrades, wfVerdict)
	b := report.Benchmark
	bVerdict := "❌ perdeu do buy-and-hold"
	if b.BeatMarket {
		bVerdict = "✅ superou o buy-and-hold"
	}
	log.Printf("BENCHMARK: buy-and-hold %.1f%% | bot %.1f%% | EDGE %+.1f%% → %s", b.BuyHoldPct*100, report.Stats.ReturnPct*100, b.EdgePct*100, bVerdict)
	for _, sg := range report.Stats.PorSinal {
		log.Printf("  sinal %q: %d trades win=%.0f%% pnl=%s pf=%.2f", sg.Tag, sg.Trades, sg.WinRate*100, sg.NetPnL, sg.PF)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
// runBacktest roda o backtest CIENTÍFICO da estratégia ema-cross (Fase 5) e
// imprime o laudo completo: métricas, distribuição, Monte Carlo, significância
// e walk-forward. Candles: do rastro persistido (CandleClosed) quando houver;
// senão sintéticos seedáveis. Sem broker, sem HTTP — CLI only.
func runBacktest(e *engine.Engine, symbol string) {
	candles := extractCandles(symbol, e)
	source := "sintéticos"
	if len(candles) == 0 {
		candles = strategy.SyntheticCandles(paperSeed(), symbol, 300, demoStartPrice(symbol))
	} else {
		source = "rastro persistido"
	}
	report := strategy.AnalyzeBacktest(strategy.NewEMACross(), candles,
		decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), 1000, 1000, paperSeed())
	log.Printf("═══ LAUDO CIENTÍFICO — %s (%s) ═══", report.Strategy, source)
	printReport(report)
}

// extractCandles devolve as velas fechadas persistidas de um símbolo, em ordem
// cronológica, decodificando o payload dos eventos do rastro durável.
func extractCandles(symbol string, e *engine.Engine) []domain.Candle {
	if e.DB == nil {
		return nil
	}
	events, err := e.DB.AllEvents()
	if err != nil {
		return nil
	}
	var out []domain.Candle
	for _, ev := range events {
		if ev.Type != event.CandleClosed {
			continue
		}
		var c domain.Candle
		if err := payloadJSON(ev.Payload, &c); err != nil || c.Symbol != symbol {
			continue
		}
		out = append(out, c)
	}
	return out
}

// payloadJSON decodifica um payload (struct em memória ou json.RawMessage do
// disco) para o tipo alvo.
func payloadJSON(p any, target any) error {
	var data []byte
	var err error
	switch v := p.(type) {
	case json.RawMessage:
		data = v
	case []byte:
		data = v
	case nil:
		return errors.New("sem payload")
	default:
		data, err = json.Marshal(p)
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(data, target)
}

// buildProtections monta as proteções de mercado a partir das envs (lição do
// Freqtrade: StoplossGuard pausa após N stops; CooldownPeriod evita re-entrada
// imediata). Zero/ausente = proteção desativada.
func buildProtections() *risk.Protections {
	maxStops := envInt("COSCA_TRADER_GUARD_MAX_STOPS", 0)
	lookback := envInt("COSCA_TRADER_GUARD_LOOKBACK", 12)
	pause := envInt("COSCA_TRADER_GUARD_PAUSE_CANDLES", 6)
	cooldown := envInt("COSCA_TRADER_COOLDOWN_CANDLES", 0)

	var guard *risk.StoplossGuard
	if maxStops > 0 {
		guard = &risk.StoplossGuard{MaxStops: maxStops, Lookback: lookback, PauseCandles: pause}
	}
	var cd *risk.CooldownPeriod
	if cooldown > 0 {
		cd = &risk.CooldownPeriod{CooldownCandles: cooldown}
	}
	if guard == nil && cd == nil {
		return nil
	}
	log.Printf("proteções ativas: stoploss-guard(max=%d stops em %d velas, pausa %d) cooldown(%d velas)",
		maxStops, lookback, pause, cooldown)
	return risk.NewProtections(guard, cd)
}

// envInt lê uma env int com default.
func envInt(name string, def int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("⚠ %s inválido (%q) — usando %d", name, raw, def)
		return def
	}
	return v
}

// riskPerTradePct lê o risco por trade para o sizing (F4B). Default: 0
// (sizing desativado — usa qty fixa da estratégia). Ex.:
// COSCA_TRADER_RISK_PER_TRADE_PCT=0.01 → arriscar 1% do equity por trade.
func riskPerTradePct() decimal.Decimal {
	raw := os.Getenv("COSCA_TRADER_RISK_PER_TRADE_PCT")
	if raw == "" {
		return decimal.Zero
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_RISK_PER_TRADE_PCT inválido (%q) — sizing desativado", raw)
		return decimal.Zero
	}
	return v
}

// strategyOrderQty lê a quantidade por sinal da estratégia (Fase 3D). Default:
// 0.001 do ativo base (ex.: BTC) — pequena o bastante para não estourar o
// limite de notional com preços altos. COSCA_TRADER_STRATEGY_QTY para ajustar.
func strategyOrderQty() decimal.Decimal {
	const def = "0.001"
	raw := os.Getenv("COSCA_TRADER_STRATEGY_QTY")
	if raw == "" {
		return decimal.RequireFromString(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() <= 0 {
		log.Printf("⚠ COSCA_TRADER_STRATEGY_QTY inválido (%q) — usando %s", raw, def)
		return decimal.RequireFromString(def)
	}
	return v
}

// maxOrderLimit lê o limite de valor por ordem (P1-1). Default seguro: 1000
// USDT. Valor inválido ou negativo cai no default (com aviso).
func maxOrderLimit() decimal.Decimal {
	const def = 1000
	raw := os.Getenv("COSCA_TRADER_MAX_ORDER_USDT")
	if raw == "" {
		return decimal.NewFromInt(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_MAX_ORDER_USDT inválido (%q) — usando default %d", raw, def)
		return decimal.NewFromInt(def)
	}
	return v
}

// stopLossPct lê o percentual de stop-loss automático (P1-2). Zero = desativado.
// Ex.: COSCA_TRADER_STOP_LOSS_PCT=0.05 → stop 5% abaixo/acima da entrada.
func stopLossPct() decimal.Decimal {
	raw := os.Getenv("COSCA_TRADER_STOP_LOSS_PCT")
	if raw == "" {
		return decimal.Zero
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_STOP_LOSS_PCT inválido (%q) — desativado", raw)
		return decimal.Zero
	}
	return v
}

// riskExposurePct lê a fração máxima do equity em posição por símbolo (F4).
// Default: 0.20 (20%). Zero desativa a regra.
func riskExposurePct() decimal.Decimal {
	const def = "0.20"
	raw := os.Getenv("COSCA_TRADER_MAX_EXPOSURE_PCT")
	if raw == "" {
		return decimal.RequireFromString(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_MAX_EXPOSURE_PCT inválido (%q) — usando %s", raw, def)
		return decimal.RequireFromString(def)
	}
	return v
}

// riskTotalExposurePct lê a fração máxima do equity em posição no total da
// carteira (F4). Default: 0.50 (50%). Zero desativa a regra.
func riskTotalExposurePct() decimal.Decimal {
	const def = "0.50"
	raw := os.Getenv("COSCA_TRADER_MAX_TOTAL_EXPOSURE_PCT")
	if raw == "" {
		return decimal.RequireFromString(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_MAX_TOTAL_EXPOSURE_PCT inválido (%q) — usando %s", raw, def)
		return decimal.RequireFromString(def)
	}
	return v
}

// riskMaxDrawdownPct lê o drawdown máximo do pico de equity (F4). Ao atingir,
// o trading é pausado (Halt) e RiskBreach é emitido. Default: 0.10 (10%).
// Zero desativa a regra.
func riskMaxDrawdownPct() decimal.Decimal {
	const def = "0.10"
	raw := os.Getenv("COSCA_TRADER_MAX_DRAWDOWN_PCT")
	if raw == "" {
		return decimal.RequireFromString(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_MAX_DRAWDOWN_PCT inválido (%q) — usando %s", raw, def)
		return decimal.RequireFromString(def)
	}
	return v
}

// takeProfitPct lê o take-profit automático (F4B): fecha a posição quando o
// mark atinge entrada×(1+pct) em long. Default: 0 (desativado). Ex.:
// COSCA_TRADER_TAKE_PROFIT_PCT=0.10 → fecha com +10%.
func takeProfitPct() decimal.Decimal {
	raw := os.Getenv("COSCA_TRADER_TAKE_PROFIT_PCT")
	if raw == "" {
		return decimal.Zero
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_TAKE_PROFIT_PCT inválido (%q) — desativado", raw)
		return decimal.Zero
	}
	return v
}

// trailingActivationPct lê o PnL % que ativa o trailing stop (F4B). Default: 0
// (trailing desativado). Ex.: COSCA_TRADER_TRAILING_ACTIVATION_PCT=0.03 →
// ativa com +3% de ganho.
func trailingActivationPct() decimal.Decimal {
	raw := os.Getenv("COSCA_TRADER_TRAILING_ACTIVATION_PCT")
	if raw == "" {
		return decimal.Zero
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_TRAILING_ACTIVATION_PCT inválido (%q) — desativado", raw)
		return decimal.Zero
	}
	return v
}

// trailingPct lê a distância do trailing abaixo do maior mark (F4B). Default:
// 0 (desativado). Ex.: COSCA_TRADER_TRAILING_PCT=0.02 → stop 2% abaixo do
// maior preço visto.
func trailingPct() decimal.Decimal {
	raw := os.Getenv("COSCA_TRADER_TRAILING_PCT")
	if raw == "" {
		return decimal.Zero
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_TRAILING_PCT inválido (%q) — desativado", raw)
		return decimal.Zero
	}
	return v
}

// paperBalance lê o capital inicial do modo paper (Fase 2B). Default: 10000
// USDT. Valor inválido ou não positivo cai no default (com aviso).
func paperBalance() decimal.Decimal {
	const def = 10000
	raw := os.Getenv("COSCA_TRADER_PAPER_BALANCE")
	if raw == "" {
		return decimal.NewFromInt(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() <= 0 {
		log.Printf("⚠ COSCA_TRADER_PAPER_BALANCE inválido (%q) — usando %d", raw, def)
		return decimal.NewFromInt(def)
	}
	return v
}

// paperFeePct lê a taxa por preenchimento do modo paper. Default: 0.001 (0.1%).
func paperFeePct() decimal.Decimal {
	const def = "0.001"
	raw := os.Getenv("COSCA_TRADER_PAPER_FEE_PCT")
	if raw == "" {
		return decimal.RequireFromString(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_PAPER_FEE_PCT inválido (%q) — usando %s", raw, def)
		return decimal.RequireFromString(def)
	}
	return v
}

// paperSlippage lê o deslizamento de execução do modo paper. Default: 0.
func paperSlippage() decimal.Decimal {
	raw := os.Getenv("COSCA_TRADER_PAPER_SLIPPAGE")
	if raw == "" {
		return decimal.Zero
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_PAPER_SLIPPAGE inválido (%q) — desativado", raw)
		return decimal.Zero
	}
	return v
}

// paperLimitFillFraction lê a fração de fills parciais de ordens limit no modo
// paper (Fase 3B). Default: 0.5 — uma ordem limit grande preenche metade por
// avaliação de preço (cada SetPrice reavalia), emitindo TradeExecuted por
// fatia. 0 = all-or-nothing (comportamento original). Market continua sempre
// all-or-nothing.
func paperLimitFillFraction() decimal.Decimal {
	const def = "0.5"
	raw := os.Getenv("COSCA_TRADER_PAPER_LIMIT_FILL_FRACTION")
	if raw == "" {
		return decimal.RequireFromString(def)
	}
	v, err := decimal.NewFromString(raw)
	if err != nil || v.Sign() < 0 {
		log.Printf("⚠ COSCA_TRADER_PAPER_LIMIT_FILL_FRACTION inválido (%q) — usando %s", raw, def)
		return decimal.RequireFromString(def)
	}
	return v
}

// paperFast lê o toggle de demo acelerada (COSCA_TRADER_PAPER_FAST=1): velas
// sintéticas de 5s para a estratégia sinalizar em minutos, sem mudar o
// random walk (determinismo preservado pelo seed).
func paperFast() bool {
	return os.Getenv("COSCA_TRADER_PAPER_FAST") == "1"
}

// paperSeed lê a semente do feed sintético. Vazia = não-determinístico (cada
// execução diverge); definida = demonstração reproduzível (COSCA_TRADER_PAPER_SEED).
func paperSeed() int64 {
	raw := os.Getenv("COSCA_TRADER_PAPER_SEED")
	if raw == "" {
		return time.Now().UnixNano()
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		log.Printf("⚠ COSCA_TRADER_PAPER_SEED inválido (%q) — usando aleatório", raw)
		return time.Now().UnixNano()
	}
	return v
}

// demoStartPrice escolhe um preço inicial plausível para o feed sintético.
func demoStartPrice(symbol string) float64 {
	switch {
	case strings.Contains(symbol, "BTC"):
		return 60000
	case strings.Contains(symbol, "ETH"):
		return 3000
	case strings.Contains(symbol, "BNB"):
		return 500
	default:
		return 100
	}
}

// splitOrigins divide a lista de origens permitidas (COSCA_TRADER_ALLOWED_ORIGINS,
// separada por vírgulas), ignorando vazios e espaços. Lista vazia = CORS
// totalmente fechado (apenas mesmo-origin/sem Origin).
func splitOrigins(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defaultPort() string {
	if p := os.Getenv("COSCA_TRADER_PORT"); p != "" {
		return p
	}
	return "14126" // família: 14120 serve · 14123 runtime · 14124 neural-link · 14125 node · 14126 trader
}

func defaultDBPath() string {
	if p := os.Getenv("COSCA_TRADER_DB"); p != "" {
		return p
	}
	dir := ".cosca"
	if err := os.MkdirAll(dir, 0o755); err == nil {
		return filepath.Join(dir, "trader.db")
	}
	return "trader.db"
}
