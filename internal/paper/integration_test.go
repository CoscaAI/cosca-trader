// Teste de integração OMS + paper broker (Fase 2B): ordem → fill → posição →
// PnL, pelo caminho completo de eventos (OrderCreated → OrderFilled →
// TradeExecuted → PositionOpened/Closed → BalanceUpdated). Prova que o OMS
// não distingue o broker de papel de uma corretora real.
package paper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
	"github.com/CoscaAI/cosca-trader/internal/oms"
)

func TestOMSAndPaperBrokerEndToEnd(t *testing.T) {
	b := New()
	b.SetPrice("BTCUSDT", dd("100"))

	// Event bus capturando o rastro (thread-safe: fills podem chegar por
	// caminhos distintos no futuro).
	var mu sync.Mutex
	var events []event.Event
	emit := func(ev event.Event) error {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		return nil
	}

	o := oms.New(b, emit)
	if err := b.StartUserStream(context.Background(), exchange.Handler{
		OnOrderUpdate:   o.ApplyOrderUpdate,
		OnTrade:         o.ApplyTrade,
		OnBalanceUpdate: o.ApplyBalance,
	}); err != nil {
		t.Fatalf("StartUserStream: %v", err)
	}

	// 1. Compra market 0.01 BTC @ 100 → fill imediato → posição aberta.
	buy, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "it-buy",
	})
	if err != nil {
		t.Fatalf("PlaceOrder compra: %v", err)
	}
	if buy.Status != domain.OrderFilled {
		t.Fatalf("compra deveria vir FILLED do paper broker, veio %s", buy.Status)
	}
	pos, ok := o.Position("BTCUSDT", "paper")
	if !ok {
		t.Fatal("posição não aberta após fill")
	}
	if pos.Side != domain.SideBuy || !pos.Quantity.Equal(dd("0.01")) || !pos.AvgEntryPrice.Equal(dd("100")) {
		t.Errorf("posição errada: %+v", pos)
	}

	// 2. Mercado sobe para 150; vende market 0.01 → fecha posição com PnL.
	b.SetPrice("BTCUSDT", dd("150"))
	if _, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideSell, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "it-sell",
	}); err != nil {
		t.Fatalf("PlaceOrder venda: %v", err)
	}
	pos, _ = o.Position("BTCUSDT", "paper")
	if pos.IsOpen() {
		t.Errorf("posição deveria estar fechada: %+v", pos)
	}
	// PnL realizado = (150-100)*0.01 = 0.5 (fees ficam no ledger, não no PnL).
	if !pos.RealizedPnL.Equal(dd("0.5")) {
		t.Errorf("PnL realizado = %v, esperava 0.5", pos.RealizedPnL)
	}

	// 3. Rastro de eventos: OrderCreated, OrderFilled, TradeExecuted,
	//    PositionOpened/Closed e BalanceUpdated presentes.
	mu.Lock()
	defer mu.Unlock()
	seen := make(map[event.Type]int)
	for _, ev := range events {
		seen[ev.Type]++
	}
	for _, want := range []event.Type{
		event.OrderCreated, event.OrderFilled, event.TradeExecuted,
		event.PositionOpened, event.PositionClosed, event.BalanceUpdated,
	} {
		if seen[want] == 0 {
			t.Errorf("evento %q não emitido no fluxo OMS+paper: %d eventos no total", want, len(events))
		}
	}
}

func TestOMSStopLossThroughPaperBroker(t *testing.T) {
	// Stop-loss automático (P1-2) operando em cima do paper broker: abertura
	// de posição dispara o STOP assíncrono; quando o preço cruza, ele preenche.
	b := New()
	b.SetPrice("BTCUSDT", dd("100"))
	o := oms.New(b, func(event.Event) error { return nil }, oms.WithStopLossPct(dd("0.05")))
	_ = b.StartUserStream(context.Background(), exchange.Handler{
		OnOrderUpdate:   o.ApplyOrderUpdate,
		OnTrade:         o.ApplyTrade,
		OnBalanceUpdate: o.ApplyBalance,
	})

	// Abre long 0.01 BTC @ 100 pelo próprio paper broker (tem saldo para a
	// proteção: 0.5 BTC iniciais).
	if _, err := o.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "it-sl-buy",
	}); err != nil {
		t.Fatalf("abertura de posição: %v", err)
	}

	// O stop-loss (SELL stop a 95) é colocado no paper broker (assíncrono).
	deadline := time.Now().Add(2 * time.Second)
	var stopID string
	for time.Now().Before(deadline) {
		open, err := b.OpenOrders(context.Background(), "BTCUSDT")
		if err == nil && len(open) > 0 {
			stopID = open[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stopID == "" {
		t.Fatal("stop-loss automático não foi colocado no paper broker")
	}

	// Preço cai para 90 → o stop dispara e reduz a exposição.
	b.SetPrice("BTCUSDT", dd("90"))
	pos, _ := o.Position("BTCUSDT", "paper")
	if !pos.Quantity.Equal(dd("0")) {
		t.Fatalf("stop-loss não fechou a posição: qty=%v", pos.Quantity)
	}
}
