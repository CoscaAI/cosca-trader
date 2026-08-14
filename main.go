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
	"flag"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/exchange/binance"
	"github.com/CoscaAI/cosca-trader/internal/market"
	"github.com/CoscaAI/cosca-trader/internal/oms"
	"github.com/CoscaAI/cosca-trader/internal/paper"
	"github.com/CoscaAI/cosca-trader/internal/store"
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
	flag.Parse()

	// Fase 2B: papel é um ambiente exclusivo — nunca coexistir com live/testnet
	// (o operador escolhe UM lugar para o dinheiro).
	if *paperFlag && (*liveFlag || *testnet) {
		log.Fatal("conflito: --paper é exclusivo com --live/--testnet — escolha um único ambiente de execução")
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
	mode := "observe"
	apiKey := os.Getenv("BINANCE_API_KEY")
	apiSecret := os.Getenv("BINANCE_API_SECRET")

	omsOpts := func() []oms.Option {
		opts := []oms.Option{
			oms.WithMaxOrderUSDT(maxOrderLimit()),
			oms.WithStopLossPct(stopLossPct()),
		}
		// Fase 2A: intent durável pré-broker (fecha a janela de crash) — em
		// TODO ambiente com persistência.
		if db != nil {
			opts = append(opts, oms.WithIntentStore(db))
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
		)
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

	serve(e, omsEngine, paperBroker, *port, apiSecurity{
		token:          token,
		allowedOrigins: allowedOrigins,
	}, mode)
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
