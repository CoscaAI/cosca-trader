// Testes do paper broker: fills simulados (market/limit/stop), saldos, fees,
// cancelamento, idempotência e summary/PnL. Determinísticos — sem rede.
package paper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

func dd(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// recBroker captura os eventos do handler do broker (fills, trades, saldos).
type recBroker struct {
	mu      sync.Mutex
	orders  []domain.Order
	trades  []domain.Trade
	balance []domain.Balance
}

func (r *recBroker) handler() exchange.Handler {
	return exchange.Handler{
		OnOrderUpdate: func(o domain.Order) {
			r.mu.Lock()
			r.orders = append(r.orders, o)
			r.mu.Unlock()
		},
		OnTrade: func(t domain.Trade) {
			r.mu.Lock()
			r.trades = append(r.trades, t)
			r.mu.Unlock()
		},
		OnBalanceUpdate: func(b domain.Balance) {
			r.mu.Lock()
			r.balance = append(r.balance, b)
			r.mu.Unlock()
		},
	}
}

func (r *recBroker) snapshot() (orders []domain.Order, trades []domain.Trade) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.Order(nil), r.orders...), append([]domain.Trade(nil), r.trades...)
}

// newTestBroker cria um broker com preço de BTCUSDT e handler capturado.
func newTestBroker(t *testing.T) (*Broker, *recBroker) {
	t.Helper()
	b := New()
	b.SetPrice("BTCUSDT", dd("100"))
	rec := &recBroker{}
	if err := b.StartUserStream(context.Background(), rec.handler()); err != nil {
		t.Fatalf("StartUserStream: %v", err)
	}
	return b, rec
}

func balanceOf(t *testing.T, b *Broker, asset string) domain.Balance {
	t.Helper()
	for _, bal := range b.snapshotBalances() {
		if bal.Asset == asset {
			return bal
		}
	}
	return domain.Balance{Asset: asset}
}

func TestMarketBuyFillsImmediately(t *testing.T) {
	b, rec := newTestBroker(t)

	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-1",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderFilled {
		t.Fatalf("market deveria preencher na hora, status=%s", ord.Status)
	}
	if !ord.AvgFillPrice.Equal(dd("100")) {
		t.Errorf("fill a %v, esperava 100", ord.AvgFillPrice)
	}
	// USDT: 10000 - 0.01*100 = 9999. BTC: 0.5 + 0.01 - fee(0.01*0.001).
	usdt := balanceOf(t, b, "USDT")
	btc := balanceOf(t, b, "BTC")
	if !usdt.Free.Equal(dd("9999")) {
		t.Errorf("USDT livre = %v, esperava 9999", usdt.Free)
	}
	if !btc.Free.Equal(dd("0.50999")) {
		t.Errorf("BTC livre = %v, esperava 0.50999", btc.Free)
	}
	_, trades := rec.snapshot()
	if len(trades) != 1 {
		t.Fatalf("esperava 1 trade, veio %d", len(trades))
	}
	if !trades[0].Fee.Equal(dd("0.00001")) || trades[0].FeeAsset != "BTC" {
		t.Errorf("fee = %v %s, esperava 0.00001 BTC", trades[0].Fee, trades[0].FeeAsset)
	}
}

func TestMarketSellCreditsQuoteNetOfFee(t *testing.T) {
	b, _ := newTestBroker(t)
	b.SetPrice("BTCUSDT", dd("20000"))

	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideSell, Type: domain.OrderMarket,
		Quantity: dd("0.5"), ClientOrderID: "cli-sell",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderFilled {
		t.Fatalf("market sell deveria preencher, status=%s", ord.Status)
	}
	// Vendeu 0.5 BTC e recebeu 0.5*20000 = 10000 USDT, taxa 10000*0.001 = 10.
	usdt := balanceOf(t, b, "USDT")
	btc := balanceOf(t, b, "BTC")
	if !usdt.Free.Equal(dd("19990")) {
		t.Errorf("USDT livre = %v, esperava 19990", usdt.Free)
	}
	if !btc.Free.Equal(dd("0")) {
		t.Errorf("BTC livre = %v, esperava 0", btc.Free)
	}
}

func TestLimitBuyFillsWhenMarketBelow(t *testing.T) {
	b, _ := newTestBroker(t) // BTCUSDT = 100
	// buy limit a 120 com mercado 100 → preenche na hora (no mercado).
	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: dd("0.01"), Price: dd("120"), ClientOrderID: "cli-l1",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderFilled {
		t.Fatalf("limit dentro do preço deveria preencher, status=%s", ord.Status)
	}
	if !ord.AvgFillPrice.Equal(dd("100")) {
		t.Errorf("fill limit a %v, esperava mercado 100 (nunca pior que o limite)", ord.AvgFillPrice)
	}
}

func TestLimitBuyStaysOpenAndFillsOnPriceDrop(t *testing.T) {
	b, _ := newTestBroker(t) // BTCUSDT = 100
	// buy limit a 90 com mercado 100 → fica NEW e trava 0.9 USDT.
	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: dd("0.01"), Price: dd("90"), ClientOrderID: "cli-l2",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderNew {
		t.Fatalf("limit fora do preço deveria ficar NEW, status=%s", ord.Status)
	}
	usdt := balanceOf(t, b, "USDT")
	if !usdt.Locked.Equal(dd("0.9")) || !usdt.Free.Equal(dd("9999.1")) {
		t.Errorf("fundos travados errados: free=%v locked=%v", usdt.Free, usdt.Locked)
	}

	// Preço cai para 85 → a ordem preenche e destrava.
	b.SetPrice("BTCUSDT", dd("85"))
	open, _ := b.OpenOrders(context.Background(), "BTCUSDT")
	if len(open) != 0 {
		t.Errorf("ordem deveria ter preenchido, ainda aberta: %+v", open)
	}
	usdt = balanceOf(t, b, "USDT")
	if !usdt.Free.Equal(dd("9999.15")) || !usdt.Locked.IsZero() {
		t.Errorf("pós-fill: free=%v locked=%v, esperava free 9999.15 locked 0", usdt.Free, usdt.Locked)
	}
}

func TestInsufficientBalanceRejected(t *testing.T) {
	b, _ := newTestBroker(t)
	b.SetPrice("BTCUSDT", dd("60000"))

	_, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("1"), ClientOrderID: "cli-broke",
	})
	if err == nil {
		t.Fatal("compra acima do saldo deveria ser rejeitada")
	}
	if _, ok := b.clientIndex["cli-broke"]; ok {
		t.Error("chave rejeitada não deveria ficar indexada")
	}
}

func TestCancelOrderReleasesFunds(t *testing.T) {
	b, _ := newTestBroker(t) // BTCUSDT = 100
	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: dd("0.01"), Price: dd("90"), ClientOrderID: "cli-cancel",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	if err := b.CancelOrder(context.Background(), "BTCUSDT", ord.ID); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	usdt := balanceOf(t, b, "USDT")
	if !usdt.Free.Equal(dd("10000")) || !usdt.Locked.IsZero() {
		t.Errorf("cancel deveria devolver 10000 USDT livres: free=%v locked=%v", usdt.Free, usdt.Locked)
	}
	cancelled, _ := b.OrderByClientOrderID(context.Background(), "BTCUSDT", "cli-cancel")
	if cancelled.Status != domain.OrderCanceled {
		t.Errorf("status = %s, esperava canceled", cancelled.Status)
	}
}

func TestOrderByClientOrderIDAndIdempotency(t *testing.T) {
	b, _ := newTestBroker(t)
	req := exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-dup",
	}
	first, err := b.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	second, err := b.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("reenvio: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("idempotência violada: %q vs %q", first.ID, second.ID)
	}
	byID, err := b.OrderByClientOrderID(context.Background(), "BTCUSDT", "cli-dup")
	if err != nil || byID.ID != first.ID {
		t.Errorf("OrderByClientOrderID falhou: %+v, err=%v", byID, err)
	}
	// Chave desconhecida → ordem vazia, sem erro (contrato do OrderRecoverer).
	missing, err := b.OrderByClientOrderID(context.Background(), "BTCUSDT", "nao-existe")
	if err != nil || missing.ID != "" {
		t.Errorf("chave inexistente deveria vir vazia sem erro: %+v, err=%v", missing, err)
	}
}

func TestSlippageApplied(t *testing.T) {
	b := New(WithSlippage(dd("0.01"))) // 1%
	b.SetPrice("BTCUSDT", dd("100"))
	rec := &recBroker{}
	_ = b.StartUserStream(context.Background(), rec.handler())

	// buy market: execução piora (100 * 1.01 = 101).
	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-slip",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if !ord.AvgFillPrice.Equal(dd("101")) {
		t.Errorf("slippage buy = %v, esperava 101", ord.AvgFillPrice)
	}
	// sell market: execução piora (100 * 0.99 = 99).
	b.SetPrice("BTCUSDT", dd("200"))
	ord2, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideSell, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-slip2",
	})
	if err != nil {
		t.Fatalf("PlaceOrder sell: %v", err)
	}
	if !ord2.AvgFillPrice.Equal(dd("198")) {
		t.Errorf("slippage sell = %v, esperava 198", ord2.AvgFillPrice)
	}
}

func TestMarketOrderRequiresPrice(t *testing.T) {
	b := New() // nenhum preço alimentado
	if _, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-nopx",
	}); err == nil {
		t.Fatal("ordem sem preço de mercado deveria falhar (nunca inventar valor)")
	}
}

// ── Fase 3B: fills parciais em ordens limit grandes ─────────────────────────

// TestLimitOrderPartialFills prova o ciclo de fills parciais (Fase 3B): ordem
// limit grande preenche em fatias conforme o preço evolui (cada SetPrice
// reavalia), emitindo um TradeExecuted por fatia e avançando o estado da ordem
// de new → partially_filled → filled. Determinístico (seed de preços injetado).
func TestLimitOrderPartialFills(t *testing.T) {
	b := New(WithLimitFillFraction(dd("0.5"))) // 50% por avaliação
	b.SetPrice("BTCUSDT", dd("100"))
	rec := &recBroker{}
	if err := b.StartUserStream(context.Background(), rec.handler()); err != nil {
		t.Fatalf("StartUserStream: %v", err)
	}

	// buy limit 2 BTC @ 90 com mercado 100 → fica NEW, trava 180 USDT.
	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: dd("2"), Price: dd("90"), ClientOrderID: "cli-pf",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderNew {
		t.Fatalf("limit fora do preço deveria ficar NEW, status=%s", ord.Status)
	}
	usdt := balanceOf(t, b, "USDT")
	if !usdt.Locked.Equal(dd("180")) || !usdt.Free.Equal(dd("9820")) {
		t.Fatalf("travamento inicial errado: free=%v locked=%v", usdt.Free, usdt.Locked)
	}

	// Preço cai para 85 → preenche a 1ª fatia (1 BTC = 50% de 2).
	b.SetPrice("BTCUSDT", dd("85"))
	open, _ := b.OpenOrders(context.Background(), "BTCUSDT")
	if len(open) != 1 || open[0].Status != domain.OrderPartiallyFilled {
		t.Fatalf("após 1ª fatia a ordem deveria estar partially_filled: %+v", open)
	}
	if !open[0].FilledQty.Equal(dd("1")) {
		t.Errorf("FilledQty = %v, esperava 1", open[0].FilledQty)
	}
	if !open[0].AvgFillPrice.Equal(dd("85")) {
		t.Errorf("AvgFillPrice = %v, esperava 85", open[0].AvgFillPrice)
	}
	usdt = balanceOf(t, b, "USDT")
	// 10000 - 180 (lock) + 90 (unlock 1×90) - 85 (paga 1×85) = 9825; travado 90.
	if !usdt.Free.Equal(dd("9825")) || !usdt.Locked.Equal(dd("90")) {
		t.Errorf("pós-1ª fatia: free=%v locked=%v, esperava free 9825 locked 90", usdt.Free, usdt.Locked)
	}

	// Preço cai para 84 → preenche a 2ª e última fatia → filled.
	b.SetPrice("BTCUSDT", dd("84"))
	open, _ = b.OpenOrders(context.Background(), "BTCUSDT")
	if len(open) != 0 {
		t.Fatalf("ordem deveria ter completado: %+v", open)
	}
	done, _ := b.OrderByClientOrderID(context.Background(), "BTCUSDT", "cli-pf")
	if done.Status != domain.OrderFilled || !done.FilledQty.Equal(dd("2")) {
		t.Errorf("pós-fill total: %+v", done)
	}
	if !done.AvgFillPrice.Equal(dd("84.5")) {
		t.Errorf("AvgFillPrice = %v, esperava 84.5 (média 85/84)", done.AvgFillPrice)
	}
	usdt = balanceOf(t, b, "USDT")
	// 9825 + 90 - 84 = 9831; nada travado.
	if !usdt.Free.Equal(dd("9831")) || !usdt.Locked.IsZero() {
		t.Errorf("pós-fill total: free=%v locked=%v, esperava free 9831 locked 0", usdt.Free, usdt.Locked)
	}

	// Rastro: 2 trades (um por fatia) e a sequência partially_filled → filled.
	_, trades := rec.snapshot()
	if len(trades) != 2 {
		t.Fatalf("esperava 2 trades (um por fatia), veio %d", len(trades))
	}
	if !trades[0].Quantity.Equal(dd("1")) || !trades[1].Quantity.Equal(dd("1")) {
		t.Errorf("quantidades das fatias erradas: %+v", trades)
	}
	orders, _ := rec.snapshot()
	if len(orders) != 2 || orders[0].Status != domain.OrderPartiallyFilled || orders[1].Status != domain.OrderFilled {
		t.Errorf("sequência de estados errada: %+v", orders)
	}
}

// TestMarketOrderStaysAllOrNothing garante que ordens market NUNCA preenchem
// parcialmente, mesmo com fração configurada.
func TestMarketOrderStaysAllOrNothing(t *testing.T) {
	b := New(WithLimitFillFraction(dd("0.5")))
	b.SetPrice("BTCUSDT", dd("100"))
	rec := &recBroker{}
	_ = b.StartUserStream(context.Background(), rec.handler())

	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("2"), ClientOrderID: "cli-mk",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderFilled || !ord.FilledQty.Equal(dd("2")) {
		t.Fatalf("market deveria preencher tudo de uma vez: %+v", ord)
	}
	_, trades := rec.snapshot()
	if len(trades) != 1 || !trades[0].Quantity.Equal(dd("2")) {
		t.Errorf("market all-or-nothing quebrado: %d trades %+v", len(trades), trades)
	}
}

// TestPartialFillCancelReleasesRemaining verifica que o cancelamento de uma
// ordem parcialmente preenchida devolve APENAS o remanescente travado.
func TestPartialFillCancelReleasesRemaining(t *testing.T) {
	b := New(WithLimitFillFraction(dd("0.5")))
	b.SetPrice("BTCUSDT", dd("100"))
	_ = b.StartUserStream(context.Background(), (&recBroker{}).handler())

	if _, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderLimit,
		Quantity: dd("2"), Price: dd("90"), ClientOrderID: "cli-pc",
	}); err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	b.SetPrice("BTCUSDT", dd("85")) // 1ª fatia → parcialmente preenchida
	open, _ := b.OpenOrders(context.Background(), "BTCUSDT")
	if len(open) != 1 {
		t.Fatalf("esperava ordem parcialmente preenchida aberta: %+v", open)
	}
	if err := b.CancelOrder(context.Background(), "BTCUSDT", open[0].ID); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	usdt := balanceOf(t, b, "USDT")
	// Após 1ª fatia: free 9825, locked 90. Cancelar devolve os 90 restantes.
	if !usdt.Free.Equal(dd("9915")) || !usdt.Locked.IsZero() {
		t.Errorf("cancel parcial: free=%v locked=%v, esperava free 9915 locked 0", usdt.Free, usdt.Locked)
	}
}

// TestLimitOrderPartialSell cobre o lado vendedor: fatias de venda entregam a
// base e recebem quote líquida da taxa a cada avaliação.
func TestLimitOrderPartialSell(t *testing.T) {
	b := New(WithLimitFillFraction(dd("0.5")))
	b.SetPrice("BTCUSDT", dd("100"))
	_ = b.StartUserStream(context.Background(), (&recBroker{}).handler())

	// Acumula 2 BTC para ter base para vender.
	if _, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("2"), ClientOrderID: "cli-acc",
	}); err != nil {
		t.Fatalf("acúmulo de base: %v", err)
	}
	// sell limit 2 @ 110 com mercado 100 → fica NEW, trava 2 BTC.
	if _, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideSell, Type: domain.OrderLimit,
		Quantity: dd("2"), Price: dd("110"), ClientOrderID: "cli-ps",
	}); err != nil {
		t.Fatalf("PlaceOrder sell: %v", err)
	}
	btc := balanceOf(t, b, "BTC")
	if !btc.Locked.Equal(dd("2")) {
		t.Fatalf("venda deveria travar 2 BTC, veio %+v", btc)
	}

	b.SetPrice("BTCUSDT", dd("115")) // cruza → 1ª fatia de 1 BTC @ 115
	open, _ := b.OpenOrders(context.Background(), "BTCUSDT")
	if len(open) != 1 || open[0].Status != domain.OrderPartiallyFilled {
		t.Fatalf("esperava partially_filled: %+v", open)
	}
	b.SetPrice("BTCUSDT", dd("116")) // 2ª e última fatia
	open, _ = b.OpenOrders(context.Background(), "BTCUSDT")
	if len(open) != 0 {
		t.Fatalf("venda deveria ter completado: %+v", open)
	}
	done, _ := b.OrderByClientOrderID(context.Background(), "BTCUSDT", "cli-ps")
	if done.Status != domain.OrderFilled || !done.FilledQty.Equal(dd("2")) {
		t.Errorf("venda parcial não completou: %+v", done)
	}
	if !done.AvgFillPrice.Equal(dd("115.5")) {
		t.Errorf("AvgFillPrice = %v, esperava 115.5 (média 115/116)", done.AvgFillPrice)
	}
}

func TestStopOrderFillsOnCross(t *testing.T) {
	b, _ := newTestBroker(t) // BTCUSDT = 100
	// sell stop a 95: não cruza agora → NEW, trava 0.01 BTC.
	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideSell, Type: domain.OrderStop,
		Quantity: dd("0.01"), StopPrice: dd("95"), ClientOrderID: "cli-stop",
	})
	if err != nil {
		t.Fatalf("PlaceOrder stop: %v", err)
	}
	if ord.Status != domain.OrderNew {
		t.Fatalf("stop fora do preço deveria ficar NEW, status=%s", ord.Status)
	}
	// Preço cai para 90 → stop dispara.
	b.SetPrice("BTCUSDT", dd("90"))
	open, _ := b.OpenOrders(context.Background(), "BTCUSDT")
	if len(open) != 0 {
		t.Fatalf("stop deveria ter disparado: %+v", open)
	}
	btc := balanceOf(t, b, "BTC")
	if !btc.Free.Equal(dd("0.49")) {
		t.Errorf("BTC pós-stop = %v, esperava 0.49", btc.Free)
	}
}

func TestFillLatencyAsync(t *testing.T) {
	b := New(WithFillLatency(20 * time.Millisecond))
	b.SetPrice("BTCUSDT", dd("100"))
	rec := &recBroker{}
	_ = b.StartUserStream(context.Background(), rec.handler())

	ord, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-lat",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if ord.Status != domain.OrderNew {
		t.Fatalf("com latência a ordem começa NEW, status=%s", ord.Status)
	}
	// Aguarda o fill em background.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		filled, _ := b.OrderByClientOrderID(context.Background(), "BTCUSDT", "cli-lat")
		if filled.Status == domain.OrderFilled {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fill com latência não completou a tempo")
}

func TestSummaryPnLRoundTrip(t *testing.T) {
	// Preços iniciais = custo das participações → equity = capital inicial.
	b, _ := newTestBroker(t)
	b.SetPrice("BTCUSDT", dd("60000"))
	b.SetPrice("ETHUSDT", dd("3000"))
	b.SetPrice("BNBUSDT", dd("500"))
	s0 := b.Summary()
	if !s0.InitialCapital.Equal(dd("60000")) || !s0.TotalPnL.IsZero() {
		t.Fatalf("baseline errado: initial=%v equity=%v pnl=%v", s0.InitialCapital, s0.Equity, s0.TotalPnL)
	}

	// Compra 0.01 BTC @ 60000 e vende @ 66000 (round trip lucrativo).
	if _, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideBuy, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-rt1",
	}); err != nil {
		t.Fatalf("compra: %v", err)
	}
	b.SetPrice("BTCUSDT", dd("66000"))
	if _, err := b.PlaceOrder(context.Background(), exchange.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.SideSell, Type: domain.OrderMarket,
		Quantity: dd("0.01"), ClientOrderID: "cli-rt2",
	}); err != nil {
		t.Fatalf("venda: %v", err)
	}

	s := b.Summary()
	if !s.Equity.Equal(dd("63058.68")) {
		t.Errorf("equity = %v, esperava 63058.68", s.Equity)
	}
	if !s.TotalPnL.Equal(dd("3058.68")) {
		t.Errorf("PnL total = %v, esperava 3058.68", s.TotalPnL)
	}
	if len(s.Trades) != 2 {
		t.Errorf("esperava 2 trades simulados, veio %d", len(s.Trades))
	}
}

func TestDemoFeedDeterministic(t *testing.T) {
	// Mesmo seed → mesma sequência de preços (random walk seedável).
	d1 := NewDemoFeed(42, "BTCUSDT", 100)
	d2 := NewDemoFeed(42, "BTCUSDT", 100)
	for i := 0; i < 50; i++ {
		if d1.nextPrice(100) != d2.nextPrice(100) {
			t.Fatalf("feed com mesmo seed divergiu no passo %d", i)
		}
	}
}
