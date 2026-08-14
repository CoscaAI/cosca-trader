// serve.go — API do core (health, timeline, SSE, ordens, posições, saldos).
// Segurança P0-1: auth FAIL-CLOSED (token obrigatório nos endpoints sensíveis)
// + CORS fechado (origem não permitida → 403). Isso mata o vetor de CSRF
// localhost (Simple Request text/plain de sites maliciosos).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/marketindex"
	"github.com/CoscaAI/cosca-trader/internal/oms"
	"github.com/CoscaAI/cosca-trader/internal/paper"
	"github.com/CoscaAI/cosca-trader/internal/risk"
	"github.com/CoscaAI/cosca-trader/internal/strategy"
)

// apiSecurity carrega a política de segurança HTTP do core. Preenchida no
// startup a partir do ambiente (COSCA_TRADER_TOKEN / COSCA_TRADER_ALLOWED_ORIGINS).
type apiSecurity struct {
	token          string
	allowedOrigins []string
}

func serve(e *engine.Engine, o *oms.OMS, pb *paper.Broker, rm *risk.Manager, conv *strategy.ConvergenceMonitor, radar *marketindex.Client, port string, sec apiSecurity, mode string) {
	mux := newMux(e, o, pb, rm, conv, radar, port, sec, mode)
	log.Printf("COSCA TRADER — core no ar em 127.0.0.1:%s (auth fail-closed: %v)", port, sec.token != "")
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, mux))
}

// newMux monta o roteador HTTP do core — extraído para ser testável (httptest)
// sem subir o listener. /health é público; todos os demais endpoints passam
// pela política de segurança (origem + token).
func newMux(e *engine.Engine, o *oms.OMS, pb *paper.Broker, rm *risk.Manager, conv *strategy.ConvergenceMonitor, radar *marketindex.Client, port string, sec apiSecurity, mode string) *http.ServeMux {
	mux := http.NewServeMux()

	// /health — estado do core. Público (liveness, sem dados sensíveis).
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		first, last := e.Store.TimeRange()
		writeJSON(w, map[string]any{
			"ok":          true,
			"service":     "cosca-trader",
			"mode":        mode, // "live" | "testnet" | "paper" | "observe"
			"port":        port,
			"events":      e.Store.Len(),
			"first_event": first,
			"last_event":  last,
			"subscribers": e.Hub.SubscriberCount(),
			"trading":     o != nil,
		})
	})

	// /timeline — rastro total (sensível: ordens, saldos, posições, PnL).
	mux.HandleFunc("/timeline", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"count":  e.Store.Len(),
			"events": e.Store.All(),
		})
	}))

	// /events — stream SSE de eventos em tempo real (sensível: expõe fills,
	// ordens, saldos e posições — CORS totalmente fechado por padrão).
	mux.HandleFunc("/events", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming não suportado", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		sub := e.Hub.Subscribe()
		defer e.Hub.Unsubscribe(sub)

		for {
			select {
			case ev := <-sub:
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}))

	// /orders — GET lista ordens · POST envia uma ordem · DELETE cancela.
	mux.HandleFunc("/orders", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			http.Error(w, "execução não configurada (defina BINANCE_API_KEY/BINANCE_API_SECRET)", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, o.Orders())
		case http.MethodPost:
			var req exchange.OrderRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "JSON inválido", http.StatusBadRequest)
				return
			}
			ord, err := o.PlaceOrder(r.Context(), req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			writeJSON(w, ord)
		case http.MethodDelete:
			symbol := r.URL.Query().Get("symbol")
			orderID := r.URL.Query().Get("order_id")
			if symbol == "" || orderID == "" {
				http.Error(w, "symbol e order_id obrigatórios", http.StatusBadRequest)
				return
			}
			if err := o.CancelOrder(r.Context(), symbol, orderID); err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "order_id": orderID})
		default:
			http.Error(w, "método não suportado", http.StatusMethodNotAllowed)
		}
	}))

	// /positions — posições abertas (sensível). Desde a Fase 3A o JSON inclui o
	// PnL NÃO realizado (unrealized_pnl) e o percentual em relação ao notional
	// de entrada — calculados na fronteira de view usando o MarkPrice vivo.
	mux.HandleFunc("/positions", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			writeJSON(w, []any{})
			return
		}
		writeJSON(w, positionViews(o.Positions()))
	}))

	// /balances — saldos (sensível).
	mux.HandleFunc("/balances", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			writeJSON(w, []any{})
			return
		}
		writeJSON(w, o.Balances())
	}))

	// /ledger — livro-razão double-entry (sensível).
	mux.HandleFunc("/ledger", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			writeJSON(w, map[string]any{"balances": map[string]any{}, "entries": []any{}})
			return
		}
		writeJSON(w, map[string]any{
			"balances": o.Ledger().Balances(),
			"entries":  o.Ledger().Entries(),
		})
	}))

	// /paper — resumo do modo paper (Fase 2B): capital inicial, capital atual,
	// PnL total e trades simulados. Protegido por auth como os demais.
	mux.HandleFunc("/paper", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if pb == nil {
			http.Error(w, "modo paper não ativo (rode com --paper)", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, pb.Summary())
	}))

	// /risk — estado da camada de risco (F4): equity, pico, drawdown, exposição,
	// trading pausado e a razão. O frontend usa para o painel de risco.
	mux.HandleFunc("/risk", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if rm == nil {
			http.Error(w, "camada de risco não ativa", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, rm.State())
	}))

	// /backtest — o laudo científico (F5): métricas estatísticas, distribuição,
	// Monte Carlo, significância e walk-forward da estratégia sobre o histórico
	// persistido (ou sintético seedável). É a "ferramenta de probabilidade".
	mux.HandleFunc("/backtest", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		sym := r.URL.Query().Get("symbol")
		if sym == "" {
			sym = "BTCUSDT"
		}
		candles := extractCandles(sym, e)
		source := "sintéticos"
		if len(candles) == 0 {
			candles = strategy.SyntheticCandles(paperSeed(), sym, 300, demoStartPrice(sym))
		} else {
			source = "rastro persistido"
		}
		report := strategy.AnalyzeBacktest(strategy.NewEMACross(), candles,
			decimal.NewFromInt(10000), decimal.NewFromFloat(0.001), 1000, 1000, paperSeed())
		writeJSON(w, map[string]any{
			"strategy":      report.Strategy,
			"symbol":        report.Symbol,
			"periods":       report.Periods,
			"source":        source,
			"initial":       report.Initial,
			"final":         report.Final,
			"stats":         report.Stats,
			"equity_curve":  report.EquityCurve,
			"monte_carlo":   report.MonteCarlo,
			"significance":  report.Significance,
			"walk_forward":  report.WalkForward,
			"trade_count":   len(report.Trades),
		})
	}))

	// /convergence — o estado do LABORATÓRIO VIVO (Fase 5): a previsão da
	// estratégia está convergindo com o mercado real? z-score, acerto
	// observado vs esperado, divergência (regime mudou) e reajustes.
	mux.HandleFunc("/convergence", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if conv == nil {
			http.Error(w, "monitor de convergência não ativo (rode com --shadow)", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, conv.State())
	}))

	// /markets — o radar GLOBAL (proteção macro L287 + divergência L288):
	// cotações de S&P/NASDAQ/VIX/ouro/dólar, o regime risk-on/off e a
	// divergência com o cripto (S&P caiu/BTC não reagiu → sinal).
	mux.HandleFunc("/markets", sec.secure(func(w http.ResponseWriter, r *http.Request) {
		if radar == nil {
			http.Error(w, "radar global não ativo", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		snap, err := radar.Fetch(ctx)
		if err != nil {
			http.Error(w, "radar indisponível: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		// Divergência com o cripto (BTC 10d) — se o Klines estiver acessível.
		sig := marketindex.AnalyzeDivergence(snap, 0, marketindex.DefaultDivergenceConfig())
		writeJSON(w, map[string]any{
			"regime":     snap.Regime,
			"signal":     snap.Signal,
			"quotes":     snap.Quotes,
			"fetched_at": snap.FetchedAt,
			"divergence": sig,
		})
	}))

	return mux
}

// secure envolve um handler sensível: aplica a política de origem (CORS) e a
// autenticação fail-closed ANTES de delegar. Qualquer falha → erro HTTP, nunca
// chega ao handler.
func (s apiSecurity) secure(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.originAllowed(r) {
			http.Error(w, "origem bloqueada (CORS)", http.StatusForbidden)
			return
		}
		if !authorized(r, s.token) {
			http.Error(w, "não autorizado", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// originAllowed aplica a política CORS fail-closed: com a lista de origens
// permitidas vazia (default), QUALQUER header Origin é bloqueado — apenas
// requests mesmo-origin ou sem Origin (curl, clientes nativos/desktop) passam.
// Mata o vetor de Simple Request text/plain do CSRF localhost.
func (s apiSecurity) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // mesmo-origin / cliente sem origem
	}
	for _, allowed := range s.allowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// authorized é FAIL-CLOSED: sem token configurado ou sem Bearer válido → 401.
// O token (COSCA_TRADER_TOKEN) é OBRIGATÓRIO em todos os endpoints sensíveis —
// o modo "desenvolvimento permissivo" foi eliminado.
func authorized(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	return r.Header.Get("Authorization") == "Bearer "+token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// positionView é a representação HTTP de uma posição (Fase 3A): embrulha a
// Position do domínio e acrescenta o PnL não realizado calculado no mark. O
// domínio permanece intacto — a view computa na fronteira de apresentação.
type positionView struct {
	domain.Position
	UnrealizedPnL    decimal.Decimal `json:"unrealized_pnl"`
	UnrealizedPnLPct decimal.Decimal `json:"unrealized_pnl_pct"`
}

// positionViews projeta posições do OMS em views com PnL não realizado.
// Sem mark alimentado (MarkPrice zero) o PnL não realizado é zero (e o pct
// zero) — nunca inventa valor no caminho de dinheiro. O pct é relativo ao
// notional de entrada (avg_entry_price × quantity).
func positionViews(positions []domain.Position) []positionView {
	out := make([]positionView, 0, len(positions))
	for _, p := range positions {
		v := positionView{Position: p}
		if p.MarkPrice.Sign() > 0 {
			v.UnrealizedPnL = p.UnrealizedPnLAt(p.MarkPrice)
			if notional := p.AvgEntryPrice.Mul(p.Quantity); notional.Sign() > 0 {
				v.UnrealizedPnLPct = v.UnrealizedPnL.Div(notional).Mul(decimal.NewFromInt(100))
			}
		}
		out = append(out, v)
	}
	return out
}
