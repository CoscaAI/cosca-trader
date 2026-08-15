package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestTokenBucketCapacity(t *testing.T) {
	b := NewTokenBucket(2, 0.1) // capacity 2, refill 0.1/s (1 token a cada 10s)
	ctx := context.Background()
	if err := b.Wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// terceiro deve bloquear (sem token; refill é lento demais)
	ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := b.Wait(ctx2, 1); err == nil {
		t.Fatal("terceiro Wait deveria ter expirado (bucket esvaziado)")
	}
}

func TestTokenBucketCost(t *testing.T) {
	b := NewTokenBucket(3, 0.1)
	ctx := context.Background()
	// consome 3 de uma vez (custo ponderado — request caro)
	if err := b.Wait(ctx, 3); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := b.Wait(ctx2, 1); err == nil {
		t.Fatal("custo 3 deveria esvaziar a capacity 3")
	}
}

func TestTokenBucketZeroCostNoop(t *testing.T) {
	b := NewTokenBucket(1, 1)
	if err := b.Wait(context.Background(), 0); err != nil {
		t.Fatal("custo zero não deveria bloquear")
	}
}

func TestSlidingWindowLimit(t *testing.T) {
	w := NewSlidingWindow(time.Second, 2)
	ctx := context.Background()
	if err := w.Wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := w.Wait(ctx, 1); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := w.Wait(ctx2, 1); err == nil {
		t.Fatal("terceiro Wait deveria ter expirado (janela cheia)")
	}
}

func TestSlidingWindowDisabled(t *testing.T) {
	// max 0 = desativado
	w := NewSlidingWindow(time.Second, 0)
	for i := 0; i < 100; i++ {
		if err := w.Wait(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
}
