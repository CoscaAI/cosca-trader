// serve.go — API do core (health, timeline, SSE, ordens, posições, saldos).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/oms"
)

func serve(e *engine.Engine, o *oms.OMS, port string) {
	mux := http.NewServeMux()

	// /health — estado do core.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		first, last := e.Store.TimeRange()
		writeJSON(w, map[string]any{
			"ok":          true,
			"service":     "cosca-trader",
			"port":        port,
			"events":      e.Store.Len(),
			"first_event": first,
			"last_event":  last,
			"subscribers": e.Hub.SubscriberCount(),
			"trading":     o != nil,
		})
	})

	// /timeline — rastro total (todos os eventos em ordem).
	mux.HandleFunc("/timeline", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"count":  e.Store.Len(),
			"events": e.Store.All(),
		})
	})

	// /events — stream SSE de eventos em tempo real.
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming não suportado", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Sem CORS aberto: o stream expõe ordens/saldos/posições — restringir
		// a origens confiáveis quando houver cliente web.

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
	})

	// /orders — GET lista ordens · POST envia uma ordem.
	mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			http.Error(w, "execução não configurada (defina BINANCE_API_KEY/BINANCE_API_SECRET)", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, o.Orders())
		case http.MethodPost:
			if !authorized(r) {
				http.Error(w, "não autorizado", http.StatusUnauthorized)
				return
			}
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
			if !authorized(r) {
				http.Error(w, "não autorizado", http.StatusUnauthorized)
				return
			}
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
	})

	// /positions — posições abertas.
	mux.HandleFunc("/positions", func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			writeJSON(w, []any{})
			return
		}
		writeJSON(w, o.Positions())
	})

	// /balances — saldos.
	mux.HandleFunc("/balances", func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			writeJSON(w, []any{})
			return
		}
		writeJSON(w, o.Balances())
	})

	// /ledger — livro-razão double-entry (saldos por conta + entradas).
	mux.HandleFunc("/ledger", func(w http.ResponseWriter, r *http.Request) {
		if o == nil {
			writeJSON(w, map[string]any{"balances": map[string]any{}, "entries": []any{}})
			return
		}
		writeJSON(w, map[string]any{
			"balances": o.Ledger().Balances(),
			"entries":  o.Ledger().Entries(),
		})
	})

	log.Printf("COSCA TRADER — core no ar em 127.0.0.1:%s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, mux))
}

// authorized verifica o token de API (COSCA_TRADER_TOKEN). Sem token
// configurado, o modo é desenvolvimento (permissivo, bind em 127.0.0.1).
func authorized(r *http.Request) bool {
	token := os.Getenv("COSCA_TRADER_TOKEN")
	if token == "" {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
