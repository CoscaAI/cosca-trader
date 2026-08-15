package oms

import (
	"context"
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
)

// TestSetTargetPosition valida a ordem delta-based idempotente (padrão smart
// order do OpenAlgo): processar o mesmo target produz a mesma posição final.
func TestSetTargetPosition(t *testing.T) {
	ctx := context.Background()
	o := New(&fakeBroker{}, func(event.Event) error { return nil })

	// Abre uma posição long de 0.01 BTC.
	o.ApplyTrade(domain.Trade{ID: "t1", Symbol: "BTCUSDT", Exchange: "binance",
		Side: domain.SideBuy, Price: d("60000"), Quantity: d("0.01"), Timestamp: time.Now()})

	// Alvo 0.03 → delta +0.02 → compra 0.02.
	ord, err := o.SetTargetPosition(ctx, "BTCUSDT", "binance", d("0.03"))
	if err != nil {
		t.Fatalf("SetTargetPosition: %v", err)
	}
	if ord == nil || ord.Side != domain.SideBuy || !ord.Quantity.Equal(d("0.02")) {
		t.Fatalf("esperava buy 0.02, got %+v", ord)
	}

	// Simula o fill da ordem (fakeBroker não preenche sozinho).
	o.ApplyTrade(domain.Trade{ID: "t2", Symbol: "BTCUSDT", Exchange: "binance",
		Side: domain.SideBuy, Price: d("60000"), Quantity: d("0.02"), Timestamp: time.Now()})

	// Mesmo alvo de novo → delta zero → no-op (idempotente).
	ord, err = o.SetTargetPosition(ctx, "BTCUSDT", "binance", d("0.03"))
	if err != nil {
		t.Fatalf("SetTargetPosition idempotente: %v", err)
	}
	if ord != nil {
		t.Fatalf("alvo já atingido deveria ser no-op, got %+v", ord)
	}

	// Reduz para 0.01 → delta -0.02 → vende 0.02.
	ord, err = o.SetTargetPosition(ctx, "BTCUSDT", "binance", d("0.01"))
	if err != nil {
		t.Fatalf("SetTargetPosition redução: %v", err)
	}
	if ord == nil || ord.Side != domain.SideSell || !ord.Quantity.Equal(d("0.02")) {
		t.Fatalf("esperava sell 0.02, got %+v", ord)
	}

	// Zera → flat.
	o.ApplyTrade(domain.Trade{ID: "t3", Symbol: "BTCUSDT", Exchange: "binance",
		Side: domain.SideSell, Price: d("60000"), Quantity: d("0.02"), Timestamp: time.Now()})
	ord, err = o.SetTargetPosition(ctx, "BTCUSDT", "binance", d("0"))
	if err != nil {
		t.Fatalf("SetTargetPosition flat: %v", err)
	}
	if ord == nil || ord.Side != domain.SideSell || !ord.Quantity.Equal(d("0.01")) {
		t.Fatalf("esperava sell 0.01 para zerar, got %+v", ord)
	}
}

// TestSetTargetPositionShort valida alvo curto (short).
func TestSetTargetPositionShort(t *testing.T) {
	ctx := context.Background()
	o := New(&fakeBroker{}, func(event.Event) error { return nil })

	// Alvo -0.02 (short) a partir do flat.
	ord, err := o.SetTargetPosition(ctx, "BTCUSDT", "binance", d("-0.02"))
	if err != nil {
		t.Fatalf("SetTargetPosition short: %v", err)
	}
	if ord == nil || ord.Side != domain.SideSell || !ord.Quantity.Equal(d("0.02")) {
		t.Fatalf("esperava sell 0.02 (abrir short), got %+v", ord)
	}
}
