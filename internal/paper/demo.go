// demo.go — feed de candles SINTÉTICOS para o modo paper sem internet/chaves
// (Fase 2B). Um random walk simples e seedável (COSCA_TRADER_PAPER_SEED) gera
// ticks e velas como qualquer exchange real — o núcleo não distingue. O Don
// vê o sistema inteiro funcionando offline: ticks ao vivo, velas fechando,
// OMS executando no paper broker com preços que se movem.
package paper

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// DemoFeed gera market data sintético e entrega via exchange.Handler.
type DemoFeed struct {
	name     string
	symbol   string
	interval time.Duration // duração da vela
	tickStep time.Duration // cadência de ticks
	start    float64       // preço inicial
	vol      float64       // volatilidade por tick
	rng      *rand.Rand
	fast     bool // demo acelerada (Fase 3D: velas de 5s p/ a estratégia sinalizar em minutos)

	mu      sync.Mutex
	handler exchange.Handler
}

// NewDemoFeed cria o feed com seed fixo (determinismo) e preço inicial.
func NewDemoFeed(seed int64, symbol string, start float64) *DemoFeed {
	return &DemoFeed{
		name:     "paper",
		symbol:   symbol,
		interval: time.Minute,
		tickStep: time.Second,
		start:    start,
		vol:      0.002, // 0.2% por tick (ruído diário plausível)
		rng:      rand.New(rand.NewSource(seed)),
	}
}

// FastDemo acelera a cadência do feed (velas de 5s) e AUMENTA a volatilidade
// (10× o default) para demonstrações da estratégia (Fase 3D): o cruzamento de
// médias sinaliza em MINUTOS, não em horas. O seed não muda — a sequência de
// choques é a mesma; só a magnitude (vol) e o ritmo de entrega sobem, para o
// Don VER a máquina operar.
func (d *DemoFeed) FastDemo() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fast = true
	d.interval = 5 * time.Second
	d.tickStep = 250 * time.Millisecond
	d.vol = 0.02 // 2% por tick — 10× o default, para o random walk cruzar as EMAs rápido
}

// Name devolve o identificador do feed.
func (d *DemoFeed) Name() string { return d.name }

// Connect roda o gerador até o contexto ser cancelado, entregando
// OnTick (a cada tickStep) e OnCandle (vela fechada a cada interval).
func (d *DemoFeed) Connect(ctx context.Context, h exchange.Handler) error {
	d.mu.Lock()
	d.handler = h
	d.mu.Unlock()

	h.OnStatus(exchange.Status{Exchange: d.name, State: "connected", Message: "market data sintético (paper demo)"})

	price := d.start
	candleOpen := time.Now().Truncate(d.interval)
	open, high, low := price, price, price
	ticker := time.NewTicker(d.tickStep)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			price = d.nextPrice(price)
			if price > high {
				high = price
			}
			if price < low {
				low = price
			}
			now := time.Now()
			h.OnTick(domain.Tick{
				Symbol:   d.symbol,
				Exchange: d.name,
				Price:    price,
				Quantity: 0,
				Time:     now,
			})
			if now.After(candleOpen.Add(d.interval)) {
				h.OnCandle(domain.Candle{
					Symbol:   d.symbol,
					Exchange: d.name,
					Interval: intervalName(d.interval),
					OpenTime: candleOpen,
					Open:     open,
					High:     high,
					Low:      low,
					Close:    price,
					Volume:   d.rng.Float64() * 100,
					Complete: true,
				})
				candleOpen = candleOpen.Add(d.interval)
				open, high, low = price, price, price
			}
		}
	}
}

// SubscribeCandles guarda o intervalo da vela (parse simples). Na demo
// acelerada o intervalo curto já foi definido no FastDemo — não sobrescreve.
func (d *DemoFeed) SubscribeCandles(_ string, interval string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.fast {
		d.interval = parseInterval(interval)
	}
	return nil
}

// SubscribeTrades é no-op (os ticks já são o fluxo de trades).
func (d *DemoFeed) SubscribeTrades(string) error { return nil }

// Close encerra o feed.
func (d *DemoFeed) Close() error { return nil }

// nextPrice aplica um passo de random walk ao preço (log-normal pequeno).
func (d *DemoFeed) nextPrice(p float64) float64 {
	shock := d.rng.NormFloat64() * d.vol
	if math.Abs(shock) > 4*d.vol { // clampa o raro e extremo
		shock = 4 * d.vol * math.Copysign(1, shock)
	}
	return math.Max(p*(1+shock), 1e-6)
}

// parseInterval converte "1m"|"5m"|"1h"|"1d" em duração (default 1m).
func parseInterval(s string) time.Duration {
	switch strings.ToLower(s) {
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "1h":
		return time.Hour
	case "4h":
		return 4 * time.Hour
	case "1d":
		return 24 * time.Hour
	default:
		return time.Minute
	}
}

// intervalName devolve o nome do intervalo a partir da duração.
func intervalName(d time.Duration) string {
	switch d {
	case time.Minute:
		return "1m"
	case 5 * time.Minute:
		return "5m"
	case 15 * time.Minute:
		return "15m"
	case time.Hour:
		return "1h"
	case 4 * time.Hour:
		return "4h"
	case 24 * time.Hour:
		return "1d"
	default:
		return "1m"
	}
}
