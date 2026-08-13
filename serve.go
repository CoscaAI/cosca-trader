// serve.go — API do core (health, timeline, SSE, ordens, posições, saldos).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

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
		w.Header().Set("Access-Control-Allow-Origin", "*")

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

	log.Printf("COSCA TRADER — core no ar em 127.0.0.1:%s", port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, mux))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
