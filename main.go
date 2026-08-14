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

				ord, err := omsEngine.PlaceOrder(context.Background(), exchange.OrderRequest{
					Symbol:        sig.Symbol,
					Side:          domain.Side(sig.Side),
					Type:          domain.OrderMarket,
					Quantity:      qty,
					ClientOrderID: "strat-" + strat.Name() + "-" + uuid.NewString(),
				})
				if err != nil {
					log.Printf("⚠ strategy %s: ordem %s %s falhou: %v", strat.Name(), sig.Side, sig.Symbol, err)
					continue
				}
				log.Printf("strategy %s: %s %s %s qty=%s (%s)", strat.Name(), sig.Side, sig.Symbol, ord.ID, qty, sig.Reason)
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

	serve(e, omsEngine, paperBroker, riskMgr, *port, apiSecurity{
		token:          token,
		allowedOrigins: allowedOrigins,
	}, mode)
}

// runBacktest roda o backtest mínimo da estratégia ema-cross (Fase 3D, semente
// do F5) e imprime o resultado. Candles: do rastro persistido (CandleClosed)
// quando houver; senão sintéticos seedáveis. Sem broker, sem HTTP — CLI only.
func runBacktest(e *engine.Engine, symbol string) {
	candles := extractCandles(symbol, e)
	source := "sintéticos"
	if len(candles) == 0 {
		candles = strategy.SyntheticCandles(paperSeed(), symbol, 300, demoStartPrice(symbol))
	} else {
		source = "rastro persistido"
	}
	res := strategy.Backtest(strategy.NewEMACross(), candles, decimal.NewFromInt(10000), decimal.NewFromFloat(0.001))
	log.Printf("backtest ema-cross %s (%d candles, %s): inicial=%s final=%s pnl=%s trades=%d fee=%s",
		symbol, len(candles), source, res.Initial, res.Final, res.PnL, res.Trades, res.FeePaid)
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
