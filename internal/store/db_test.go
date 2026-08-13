package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/event"
)

func TestDBRoundTripEvent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "trader.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	ev := event.Event{
		ID:            "ev-1",
		Type:          event.OrderFilled,
		Timestamp:     now,
		Source:        "oms",
		CorrelationID: "order-42",
		CausationID:   "order-created-1",
		Severity:      event.SeverityInfo,
		Payload:       map[string]any{"symbol": "BTCUSDT", "qty": 0.01},
	}

	if err := db.PersistEvent(ev); err != nil {
		t.Fatalf("PersistEvent: %v", err)
	}

	all, err := db.AllEvents()
	if err != nil {
		t.Fatalf("AllEvents: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("esperava 1 evento, veio %d", len(all))
	}

	got := all[0]
	if got.ID != "ev-1" || got.Type != event.OrderFilled || got.Source != "oms" {
		t.Errorf("metadados não sobreviveram ao round-trip: %+v", got)
	}
	if got.CorrelationID != "order-42" || got.CausationID != "order-created-1" {
		t.Errorf("correlation/causation não sobreviveram: %+v", got)
	}
	if !got.Timestamp.Equal(now) {
		t.Errorf("timestamp divergiu: %v vs %v", got.Timestamp, now)
	}
}

func TestDBSchemaIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trader.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("primeira Open: %v", err)
	}
	db1.Close()

	// Segunda abertura aplica a migração de novo (idempotente) sem erro.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("segunda Open (idempotência): %v", err)
	}
	defer db2.Close()
}
