// Package paper implementa o paper trading engine do COSCA TRADER (Fase 2B):
// um Broker SIMULADO que implementa a interface exchange.Broker com fills
// executados em memória — para o operador validar estratégia e UX com o kernel
// rodando "ao lado do mercado", sem corretora real. O OMS não distingue papel
// de corretora real: mesma saga, mesmo rastro, mesmos eventos.
//
// Execução simulada:
//   - market: fill IMEDIATO ao preço corrente (via SetPrice do market data).
//   - limit:  fill condicional — buy só preenche se ask ≤ price (market ≤
//     limit), sell se bid ≥ price. Senão fica NEW até o preço cruzar.
//   - stop/stop_market: fill quando o preço cruza o stopPrice.
//   - stop_limit: cruza o stop E respeita o preço limite.
//
// Determinismo e teste: slippage (default 0) e latência de fill (default 0)
// são injetáveis via options. Balances começam com capital virtual
// (COSCA_TRADER_PAPER_BALANCE, default 10000 USDT) + BTC/ETH/BNB de
// demonstração. Fees (default 0.1%) são debitadas no ativo RECEBIDO.
//
// DINHEIRO É SEMPRE decimal.Decimal — o preço de mercado (candle/tick, float64
// na fronteira da exchange) é convertido para decimal em SetPrice/PlaceOrder.
package paper

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// Suffixos de cotação conhecidos (para derivar base/quote de um símbolo spot).
var quoteSuffixes = []string{"USDT", "USDC", "BUSD", "FDUSD", "TUSD", "DAI", "BNB", "BTC", "ETH"}

// stableCoins: ativos que valem 1 na conta de equity (moedas de margem).
var stableCoins = []string{"USDT", "USDC", "BUSD", "FDUSD", "TUSD", "DAI"}

// Holdings iniciais de demonstração (quantidade @ preço de custo para o
// cálculo de equity/PnL quando não há preço de mercado ao vivo).
var demoHoldings = []struct {
	Asset  string
	Qty    string
	CostPx string
}{
	{"BTC", "0.5", "60000"},
	{"ETH", "5", "3000"},
	{"BNB", "10", "500"},
}

// Broker é a corretora simulada de papel.
type Broker struct {
	mu sync.Mutex

	initial     decimal.Decimal // capital inicial (equity em USDT)
	feePct      decimal.Decimal // taxa por preenchimento (default 0.001)
	slippage    decimal.Decimal // slippage aplicado ao preço de execução
	fillLatency time.Duration   // latência simulada de fill (0 = síncrono)

	balances    map[string]domain.Balance  // ativo → {free, locked}
	orders      map[string]domain.Order    // por ID da exchange (paper-<n>)
	clientIndex map[string]string          // clientOrderID → orderID
	prices      map[string]decimal.Decimal // preço corrente por símbolo
	avgPrice    map[string]decimal.Decimal // preço médio de custo por ativo
	trades      []domain.Trade

	handler     exchange.Handler
	nextOrderID int64
	nextTradeID int64
}

// Option configura o Broker no New.
type Option func(*Broker)

// WithInitialBalance define o caixa inicial em USDT (COSCA_TRADER_PAPER_BALANCE).
func WithInitialBalance(v decimal.Decimal) Option {
	return func(b *Broker) {
		b.balances["USDT"] = domain.Balance{Asset: "USDT", Free: v}
	}
}

// WithFeePct define a taxa por preenchimento (COSCA_TRADER_PAPER_FEE_PCT).
func WithFeePct(v decimal.Decimal) Option {
	return func(b *Broker) { b.feePct = v }
}

// WithSlippage define o deslizamento de preço de execução (default 0).
func WithSlippage(v decimal.Decimal) Option {
	return func(b *Broker) { b.slippage = v }
}

// WithFillLatency simula a latência de execução (default 0 = fill síncrono).
func WithFillLatency(d time.Duration) Option {
	return func(b *Broker) { b.fillLatency = d }
}

// New cria o broker de papel com o capital virtual default.
func New(opts ...Option) *Broker {
	b := &Broker{
		feePct:      decimal.NewFromFloat(0.001),
		balances:    make(map[string]domain.Balance),
		orders:      make(map[string]domain.Order),
		clientIndex: make(map[string]string),
		prices:      make(map[string]decimal.Decimal),
		avgPrice:    make(map[string]decimal.Decimal),
	}
	b.balances["USDT"] = domain.Balance{Asset: "USDT", Free: decimal.NewFromInt(10000)}
	for _, h := range demoHoldings {
		qty, _ := decimal.NewFromString(h.Qty)
		px, _ := decimal.NewFromString(h.CostPx)
		b.balances[h.Asset] = domain.Balance{Asset: h.Asset, Free: qty}
		b.avgPrice[h.Asset] = px
	}
	for _, opt := range opts {
		opt(b)
	}
	// Capital inicial = caixa + valor das participações iniciais ao custo.
	initial := b.balances["USDT"].Free
	for _, h := range demoHoldings {
		qty, _ := decimal.NewFromString(h.Qty)
		px, _ := decimal.NewFromString(h.CostPx)
		initial = initial.Add(qty.Mul(px))
	}
	b.initial = initial
	return b
}

// InitialCapital devolve o capital inicial (equity em USDT).
func (b *Broker) InitialCapital() decimal.Decimal {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.initial
}

// PlaceOrder valida, simula o fill e devolve a ordem confirmada.
func (b *Broker) PlaceOrder(ctx context.Context, req exchange.OrderRequest) (domain.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	base, quote, ok := splitSymbol(req.Symbol)
	if !ok {
		return domain.Order{}, fmt.Errorf("paper: símbolo %q não reconhecido (sem par base/quote)", req.Symbol)
	}
	if req.Quantity.Sign() <= 0 {
		return domain.Order{}, fmt.Errorf("paper: quantidade deve ser positiva")
	}
	if req.Type == domain.OrderLimit && req.Price.Sign() <= 0 {
		return domain.Order{}, fmt.Errorf("paper: ordem limit exige preço")
	}
	if (req.Type == domain.OrderStop || req.Type == domain.OrderStopLimit || req.Type == domain.OrderStopMarket) && req.StopPrice.Sign() <= 0 {
		return domain.Order{}, fmt.Errorf("paper: ordem stop exige stop_price")
	}
	if req.ClientOrderID == "" {
		req.ClientOrderID = "paper-" + uuid.NewString()
	}
	// Idempotência (at-most-once): reenvio da mesma chave devolve a existente.
	if id, dup := b.clientIndex[req.ClientOrderID]; dup {
		return b.orders[id], nil
	}

	price, err := b.currentPrice(req.Symbol)
	if err != nil {
		return domain.Order{}, err
	}

	ord := domain.Order{
		ID:            b.newOrderID(),
		ClientOrderID: req.ClientOrderID,
		Symbol:        req.Symbol,
		Exchange:      "paper",
		Side:          req.Side,
		Type:          req.Type,
		Price:         req.Price,
		StopPrice:     req.StopPrice,
		Quantity:      req.Quantity,
		Status:        domain.OrderNew,
		TimeInForce:   req.TimeInForce,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	b.clientIndex[ord.ClientOrderID] = ord.ID

	// Saldo suficiente para abrir a posição/ordem.
	if err := b.checkFunds(ord, base, quote, price); err != nil {
		delete(b.clientIndex, ord.ClientOrderID)
		return domain.Order{}, err
	}

	if b.fillLatency > 0 {
		// Latência simulada: fica NEW e preenche em background após o delay.
		b.lockFunds(ord, base, quote, price)
		b.orders[ord.ID] = ord
		b.mu.Unlock()
		go func(id string) {
			time.Sleep(b.fillLatency)
			b.fillOrderAsync(id)
		}(ord.ID)
		b.mu.Lock()
		return b.orders[ord.ID], nil
	}

	if b.shouldFillNow(ord, price) {
		emitters := b.fillLocked(ord, base, quote, price, false)
		b.mu.Unlock()
		b.runEmitters(emitters)
		b.mu.Lock()
		return b.orders[ord.ID], nil
	}

	// Fica aberta (limit/stop fora do preço corrente).
	b.lockFunds(ord, base, quote, price)
	b.orders[ord.ID] = ord
	return b.orders[ord.ID], nil
}

// CancelOrder cancela uma ordem aberta, devolve os fundos travados e notifica.
func (b *Broker) CancelOrder(_ context.Context, symbol, orderID string) error {
	b.mu.Lock()
	ord, ok := b.orders[orderID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("paper: ordem %q não encontrada", orderID)
	}
	if !ord.IsOpen() {
		b.mu.Unlock()
		return fmt.Errorf("paper: ordem %q não está aberta", orderID)
	}
	base, quote, _ := splitSymbol(ord.Symbol)
	b.unlockFunds(ord, base, quote)
	ord.Status = domain.OrderCanceled
	ord.UpdatedAt = time.Now()
	b.orders[orderID] = ord
	emitters := []func(){b.emitOrder(ord), b.emitBalances()}
	b.mu.Unlock()
	b.runEmitters(emitters)
	return nil
}

// OrderByClientOrderID devolve a ordem do client_order_id (OrderRecoverer).
func (b *Broker) OrderByClientOrderID(_ context.Context, _ string, clientOrderID string) (domain.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id, ok := b.clientIndex[clientOrderID]; ok {
		return b.orders[id], nil
	}
	return domain.Order{}, nil
}

// OpenOrders devolve as ordens abertas de um símbolo.
func (b *Broker) OpenOrders(_ context.Context, symbol string) ([]domain.Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []domain.Order
	for _, ord := range b.orders {
		if ord.Symbol == symbol && ord.IsOpen() {
			out = append(out, ord)
		}
	}
	return out, nil
}

// Balances devolve os saldos não zerados.
func (b *Broker) Balances(_ context.Context) ([]domain.Balance, error) {
	return b.snapshotBalances(), nil
}

// StartUserStream grava o handler de eventos da conta (como o user stream da
// Binance). Todos os fills/cancelamentos/saldos são entregues por ele.
func (b *Broker) StartUserStream(_ context.Context, h exchange.Handler) error {
	b.mu.Lock()
	b.handler = h
	b.mu.Unlock()
	return nil
}

// Price devolve o preço corrente de um símbolo (PriceProvider).
func (b *Broker) Price(_ context.Context, symbol string) (decimal.Decimal, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentPrice(symbol)
}

// SetPrice atualiza o preço corrente e avalia fills condicionais (limit/stop)
// das ordens abertas. O preço de mercado chega como decimal — o float da
// fronteira é convertido ANTES (em quem alimenta o broker).
func (b *Broker) SetPrice(symbol string, price decimal.Decimal) {
	if price.Sign() <= 0 {
		return
	}
	b.mu.Lock()
	b.prices[symbol] = price
	base, quote, ok := splitSymbol(symbol)
	var emitters []func()
	for _, ord := range b.orders {
		if ord.Symbol != symbol || !ord.IsOpen() {
			continue
		}
		if ok && b.shouldFillNow(ord, price) {
			emitters = append(emitters, b.fillLocked(ord, base, quote, price, true)...)
		}
	}
	b.mu.Unlock()
	b.runEmitters(emitters)
}

// Summary devolve o resumo do modo paper (GET /paper).
type Summary struct {
	Mode           string          `json:"mode"`
	InitialCapital decimal.Decimal `json:"initial_capital"`
	Equity         decimal.Decimal `json:"equity"`
	TotalPnL       decimal.Decimal `json:"total_pnl"`
	OpenOrders     int             `json:"open_orders"`
	Trades         []domain.Trade  `json:"trades"`
}

// Summary calcula equity/PnL em USDT: soma cada ativo × seu preço (corrente
// se houver, senão custo médio); stablecoins valem 1. Dinheiro em decimal.
func (b *Broker) Summary() Summary {
	b.mu.Lock()
	defer b.mu.Unlock()
	equity := decimal.Zero
	for asset, bal := range b.balances {
		equity = equity.Add(bal.Total().Mul(b.assetPrice(asset)))
	}
	open := 0
	for _, ord := range b.orders {
		if ord.IsOpen() {
			open++
		}
	}
	return Summary{
		Mode:           "paper",
		InitialCapital: b.initial,
		Equity:         equity,
		TotalPnL:       equity.Sub(b.initial),
		OpenOrders:     open,
		Trades:         append([]domain.Trade(nil), b.trades...),
	}
}

// ── helpers internos ────────────────────────────────────────────────────────

// splitSymbol deriva base/quote de um símbolo spot ("BTCUSDT" → BTC/USDT).
func splitSymbol(symbol string) (base, quote string, ok bool) {
	for _, q := range quoteSuffixes {
		if strings.HasSuffix(symbol, q) && len(symbol) > len(q) {
			return strings.TrimSuffix(symbol, q), q, true
		}
	}
	return "", "", false
}

// currentPrice devolve o preço corrente do símbolo. Sem preço → erro claro
// (nunca inventa valor para o caminho de dinheiro).
func (b *Broker) currentPrice(symbol string) (decimal.Decimal, error) {
	if p, ok := b.prices[symbol]; ok && p.Sign() > 0 {
		return p, nil
	}
	return decimal.Zero, fmt.Errorf("paper: sem preço de mercado para %s (aguardando ticks do market data)", symbol)
}

// assetPrice devolve o valor de 1 unidade do ativo em USDT: moeda estável → 1;
// senão preço corrente do par (asset+USDT primeiro), fallback custo médio.
func (b *Broker) assetPrice(asset string) decimal.Decimal {
	for _, q := range stableCoins {
		if asset == q {
			return decimal.NewFromInt(1)
		}
	}
	if p, ok := b.prices[asset+"USDT"]; ok && p.Sign() > 0 {
		return p
	}
	for _, q := range quoteSuffixes {
		if p, ok := b.prices[asset+q]; ok && p.Sign() > 0 {
			return p
		}
	}
	return b.avgPrice[asset]
}

// checkFunds valida saldo suficiente: buy exige quote (USDT) para pagar o
// notional; sell exige base para entregar.
func (b *Broker) checkFunds(ord domain.Order, base, quote string, price decimal.Decimal) error {
	if ord.Side == domain.SideBuy {
		need := ord.Quantity.Mul(price)
		if ord.Type == domain.OrderLimit && ord.Price.Sign() > 0 {
			need = ord.Quantity.Mul(ord.Price)
		}
		if ord.Quantity.Sign() > 0 && need.Sign() > 0 && b.freeOf(quote).LessThan(need) {
			return fmt.Errorf("paper: saldo insuficiente de %s (precisa %s, tem %s)", quote, need, b.freeOf(quote))
		}
		return nil
	}
	if b.freeOf(base).LessThan(ord.Quantity) {
		return fmt.Errorf("paper: saldo insuficiente de %s (precisa %s, tem %s)", base, ord.Quantity, b.freeOf(base))
	}
	return nil
}

// lockFunds trava os fundos de uma ordem aberta (buy trava quote, sell trava
// base).
func (b *Broker) lockFunds(ord domain.Order, base, quote string, price decimal.Decimal) {
	if ord.Side == domain.SideBuy {
		amt := ord.Quantity.Mul(price)
		if ord.Type == domain.OrderLimit && ord.Price.Sign() > 0 {
			amt = ord.Quantity.Mul(ord.Price)
		}
		b.moveFree(quote, amt.Neg(), amt)
	} else {
		b.moveFree(base, ord.Quantity.Neg(), ord.Quantity)
	}
}

// unlockFunds devolve os fundos travados de uma ordem aberta (cancel/fill).
func (b *Broker) unlockFunds(ord domain.Order, base, quote string) {
	if ord.Side == domain.SideBuy {
		amt := ord.Quantity.Mul(ord.Price)
		b.moveFree(quote, amt, amt.Neg())
	} else {
		b.moveFree(base, ord.Quantity, ord.Quantity.Neg())
	}
}

// shouldFillNow decide se a ordem preenche com o preço corrente.
func (b *Broker) shouldFillNow(ord domain.Order, price decimal.Decimal) bool {
	switch ord.Type {
	case domain.OrderMarket:
		return true
	case domain.OrderLimit:
		if ord.Side == domain.SideBuy {
			return price.LessThanOrEqual(ord.Price)
		}
		return price.GreaterThanOrEqual(ord.Price)
	case domain.OrderStop, domain.OrderStopMarket:
		if ord.Side == domain.SideBuy {
			return price.GreaterThanOrEqual(ord.StopPrice)
		}
		return price.LessThanOrEqual(ord.StopPrice)
	case domain.OrderStopLimit:
		trigger := price.GreaterThanOrEqual(ord.StopPrice)
		if ord.Side == domain.SideSell {
			trigger = price.LessThanOrEqual(ord.StopPrice)
		}
		if !trigger {
			return false
		}
		if ord.Side == domain.SideBuy {
			return price.LessThanOrEqual(ord.Price)
		}
		return price.GreaterThanOrEqual(ord.Price)
	}
	return false
}

// fillLocked executa o fill (chamado sob lock): aplica slippage, movimenta
// saldos, registra o trade e devolve as notificações para emitir FORA do lock.
func (b *Broker) fillLocked(ord domain.Order, base, quote string, price decimal.Decimal, wasLocked bool) []func() {
	if wasLocked {
		b.unlockFunds(ord, base, quote)
	}

	exec := price
	if b.slippage.Sign() > 0 {
		one := decimal.NewFromInt(1)
		if ord.Side == domain.SideBuy {
			exec = price.Mul(one.Add(b.slippage))
		} else {
			exec = price.Mul(one.Sub(b.slippage))
		}
	}
	// Para ordens limit, executa no melhor entre mercado e limite (nunca pior).
	if ord.Type == domain.OrderLimit {
		if ord.Side == domain.SideBuy && exec.GreaterThan(ord.Price) {
			exec = ord.Price
		}
		if ord.Side == domain.SideSell && exec.LessThan(ord.Price) {
			exec = ord.Price
		}
	}

	quoteQty := exec.Mul(ord.Quantity)
	ord.Status = domain.OrderFilled
	ord.FilledQty = ord.Quantity
	ord.AvgFillPrice = exec
	ord.UpdatedAt = time.Now()

	var fee decimal.Decimal
	var feeAsset string
	if ord.Side == domain.SideBuy {
		held := b.freeOf(base) // quantidade detida ANTES do fill (média ponderada)
		fee = ord.Quantity.Mul(b.feePct)
		feeAsset = base
		b.addFree(base, ord.Quantity.Sub(fee)) // recebe base líquida da taxa
		b.addFree(quote, quoteQty.Neg())       // paga o notional em quote
		b.updateAvgPrice(base, exec, ord.Quantity, held)
	} else {
		fee = quoteQty.Mul(b.feePct)
		feeAsset = quote
		b.addFree(base, ord.Quantity.Neg()) // entrega a base
		b.addFree(quote, quoteQty.Sub(fee)) // recebe quote líquida da taxa
	}

	t := domain.Trade{
		ID:        b.newTradeID(),
		OrderID:   ord.ID,
		Symbol:    ord.Symbol,
		Exchange:  "paper",
		Side:      ord.Side,
		Price:     exec,
		Quantity:  ord.Quantity,
		QuoteQty:  quoteQty,
		Fee:       fee,
		FeeAsset:  feeAsset,
		Timestamp: time.Now(),
	}
	b.trades = append(b.trades, t)
	b.orders[ord.ID] = ord
	b.clientIndex[ord.ClientOrderID] = ord.ID

	return []func(){b.emitOrder(ord), b.emitTrade(t), b.emitBalances()}
}

// fillOrderAsync preenche uma ordem com latência (background, fora do PlaceOrder).
func (b *Broker) fillOrderAsync(id string) {
	b.mu.Lock()
	ord, ok := b.orders[id]
	if !ok || !ord.IsOpen() {
		b.mu.Unlock()
		return
	}
	price, ok := b.prices[ord.Symbol]
	if !ok || price.Sign() <= 0 {
		b.mu.Unlock()
		return
	}
	base, quote, splitOK := splitSymbol(ord.Symbol)
	if !splitOK || !b.shouldFillNow(ord, price) {
		b.mu.Unlock()
		return
	}
	emitters := b.fillLocked(ord, base, quote, price, true)
	b.mu.Unlock()
	b.runEmitters(emitters)
}

// updateAvgPrice recalcula o custo médio de um ativo após uma compra
// (média ponderada: (avg_antigo × detido + exec × comprado) / novo detido).
func (b *Broker) updateAvgPrice(asset string, exec, qty, heldBefore decimal.Decimal) {
	newQty := heldBefore.Add(qty)
	if newQty.Sign() <= 0 {
		b.avgPrice[asset] = exec
		return
	}
	oldAvg := b.avgPrice[asset]
	b.avgPrice[asset] = oldAvg.Mul(heldBefore).Add(exec.Mul(qty)).Div(newQty)
}

// addFree soma (ou subtrai) do saldo livre de um ativo.
func (b *Broker) addFree(asset string, delta decimal.Decimal) {
	bal := b.balances[asset]
	bal.Free = bal.Free.Add(delta)
	b.balances[asset] = bal
}

// moveFree transfere entre livre e travado (lock/unlock).
func (b *Broker) moveFree(asset string, freeDelta, lockedDelta decimal.Decimal) {
	bal := b.balances[asset]
	bal.Free = bal.Free.Add(freeDelta)
	bal.Locked = bal.Locked.Add(lockedDelta)
	b.balances[asset] = bal
}

// freeOf devolve o saldo livre de um ativo.
func (b *Broker) freeOf(asset string) decimal.Decimal {
	return b.balances[asset].Free
}

// snapshotBalances devolve os saldos não zerados (cópia).
func (b *Broker) snapshotBalances() []domain.Balance {
	var out []domain.Balance
	for _, bal := range b.balances {
		if bal.Free.IsZero() && bal.Locked.IsZero() {
			continue
		}
		out = append(out, bal)
	}
	return out
}

// emitOrder/emitTrade/emitBalances constroem notificações para o handler.
func (b *Broker) emitOrder(ord domain.Order) func() {
	return func() {
		if b.handler.OnOrderUpdate != nil {
			b.handler.OnOrderUpdate(ord)
		}
	}
}

func (b *Broker) emitTrade(t domain.Trade) func() {
	return func() {
		if b.handler.OnTrade != nil {
			b.handler.OnTrade(t)
		}
	}
}

func (b *Broker) emitBalances() func() {
	bs := b.snapshotBalances()
	return func() {
		if b.handler.OnBalanceUpdate == nil {
			return
		}
		for _, bal := range bs {
			b.handler.OnBalanceUpdate(bal)
		}
	}
}

// runEmitters executa as notificações FORA do lock (evita deadlock quando o
// handler reentra no broker, ex.: SetPrice alimentado pelo bus de eventos).
func (b *Broker) runEmitters(emitters []func()) {
	for _, em := range emitters {
		em()
	}
}

func (b *Broker) newOrderID() string {
	b.nextOrderID++
	return "paper-" + strconv.FormatInt(b.nextOrderID, 10)
}

func (b *Broker) newTradeID() string {
	b.nextTradeID++
	return "paper-t" + strconv.FormatInt(b.nextTradeID, 10)
}
