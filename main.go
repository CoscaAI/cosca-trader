// Command cosca-trader é a plataforma de trade profissional multiplataforma
// com IA cognitiva. Este entrypoint sobe o core headless em Go (event bus +
// event store + SQLite + stream) e serve a API de observabilidade (health,
// timeline, SSE). O cliente desktop (Wails) e o futuro cliente web são visões
// sobre este mesmo motor — nenhuma lógica crítica vive na UI.
//
// F0 — Fundação: event bus, event store, schema de domínio, rastro total.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange/binance"
	"github.com/CoscaAI/cosca-trader/internal/market"
	"github.com/CoscaAI/cosca-trader/internal/store"
)

func main() {
	dbPath := flag.String("db", defaultDBPath(), "caminho do banco SQLite")
	port := flag.String("port", defaultPort(), "porta HTTP do core")
	binanceFlag := flag.Bool("binance", false, "conectar à Binance (market data em tempo real)")
	symbol := flag.String("symbol", "BTCUSDT", "símbolo para market data")
	interval := flag.String("interval", "1m", "intervalo dos candles")
	flag.Parse()

	// SQLite (nil se não conseguir abrir — o core roda mesmo sem disco).
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Printf("⚠ aviso: sem persistência SQLite (%v) — rodando em memória", err)
		db = nil
	}

	e := engine.New(db)

	// Rastreamento total: a partir daqui, TODO evento passa pelo Emit.
	e.Emit(event.Event{
		Type:     event.SystemStarted,
		Source:   "core",
		Severity: event.SeverityInfo,
		Payload:  map[string]any{"db": *dbPath, "port": *port},
	})

	// F1 — conectividade Binance: market data em tempo real → eventos.
	if *binanceFlag {
		ctx := context.Background()
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
	}

	serve(e, *port)
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
