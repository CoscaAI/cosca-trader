package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

func TestIntentRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "trader.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	it := Intent{
		ClientOrderID: "cli-1",
		Symbol:        "BTCUSDT",
		Side:          domain.SideBuy,
		Type:          domain.OrderLimit,
		Price:         decimal.NewFromInt(50000),
		Quantity:      decimal.NewFromFloat(0.01),
		Status:        IntentPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := db.SaveIntent(it); err != nil {
		t.Fatalf("SaveIntent: %v", err)
	}

	pending, err := db.PendingIntents()
	if err != nil {
		t.Fatalf("PendingIntents: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("esperava 1 intent pendente, veio %d", len(pending))
	}
	got := pending[0]
	if got.ClientOrderID != "cli-1" || got.Symbol != "BTCUSDT" || got.Side != domain.SideBuy ||
		got.Type != domain.OrderLimit || !got.Price.Equal(it.Price) || !got.Quantity.Equal(it.Quantity) {
		t.Errorf("intent não sobreviveu ao round-trip: %+v", got)
	}
	if got.Status != IntentPending {
		t.Errorf("status = %q, esperava pending", got.Status)
	}
}

func TestIntentSaveIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "trader.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	base := Intent{
		ClientOrderID: "cli-dup",
		Symbol:        "BTCUSDT",
		Side:          domain.SideBuy,
		Type:          domain.OrderMarket,
		Quantity:      decimal.NewFromInt(1),
		Status:        IntentPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := db.SaveIntent(base); err != nil {
		t.Fatalf("primeiro SaveIntent: %v", err)
	}
	// Re-save (mesma chave) não pode duplicar a linha (ON CONFLICT).
	upd := base
	upd.Status = IntentSubmitted
	upd.Symbol = "ETHUSDT"
	if err := db.SaveIntent(upd); err != nil {
		t.Fatalf("segundo SaveIntent: %v", err)
	}
	pending, _ := db.PendingIntents()
	if len(pending) != 1 {
		t.Fatalf("SaveIntent idempotente deveria manter 1 linha, veio %d", len(pending))
	}
	if pending[0].Symbol != "ETHUSDT" || pending[0].Status != IntentSubmitted {
		t.Errorf("upsert não aplicou a atualização: %+v", pending[0])
	}
}

func TestUpdateIntentStatusAndPendingFilter(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "trader.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	mk := func(id, status string) Intent {
		return Intent{
			ClientOrderID: id, Symbol: "BTCUSDT", Side: domain.SideBuy,
			Type: domain.OrderLimit, Quantity: decimal.NewFromInt(1),
			Status: IntentStatus(status), CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	for _, it := range []Intent{mk("a", string(IntentPending)), mk("b", string(IntentSubmitted)), mk("c", string(IntentDone)), mk("d", string(IntentFailed))} {
		if err := db.SaveIntent(it); err != nil {
			t.Fatalf("SaveIntent %s: %v", it.ClientOrderID, err)
		}
	}

	// PendingIntents só devolve pending/submitted.
	pending, err := db.PendingIntents()
	if err != nil {
		t.Fatalf("PendingIntents: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("esperava 2 pendentes (a,b), veio %d: %+v", len(pending), pending)
	}

	// Avança "a" → submitted; "b" → done; "d" já é terminal.
	if err := db.UpdateIntentStatus("a", IntentSubmitted); err != nil {
		t.Fatalf("UpdateIntentStatus: %v", err)
	}
	if err := db.UpdateIntentStatus("b", IntentDone); err != nil {
		t.Fatalf("UpdateIntentStatus: %v", err)
	}
	pending, _ = db.PendingIntents()
	if len(pending) != 1 || pending[0].ClientOrderID != "a" {
		t.Fatalf("após avanço só 'a' deveria restar pendente: %+v", pending)
	}

	// Atualizar intent inexistente → erro (nunca criar em silêncio).
	if err := db.UpdateIntentStatus("nao-existe", IntentFailed); err == nil {
		t.Error("UpdateIntentStatus de intent inexistente deveria falhar")
	}
}
