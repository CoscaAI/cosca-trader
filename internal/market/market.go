// Package market é o Market Data Engine: a ponte entre as exchanges (que
// entregam domain.Tick/Candle) e o rastro de eventos do núcleo. Normaliza
// dados de mercado em eventos tipados (MarketTick, CandleClosed,
// ExchangeConnected/Disconnected/Error) — é a camada de adaptação, o núcleo
// nunca conhece o protocolo da corretora.
package market

import (
	"context"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// MarketData observa uma exchange e emite eventos de mercado.
type MarketData struct {
	ex   exchange.Exchange
	emit func(event.Event)
}

// New cria o motor sobre uma exchange, com a função de emissão de eventos.
func New(ex exchange.Exchange, emit func(event.Event)) *MarketData {
	return &MarketData{ex: ex, emit: emit}
}

// Start conecta a exchange e roteia os dados para eventos até ctx cancelar.
func (m *MarketData) Start(ctx context.Context) error {
	return m.ex.Connect(ctx, exchange.Handler{
		OnTick: func(t domain.Tick) {
			m.emit(event.Event{
				Type:    event.MarketTick,
				Source:  "exchange:" + m.ex.Name(),
				Payload: t,
			})
		},
		OnCandle: func(c domain.Candle) {
			// Só velas fechadas entram no rastro histórico; a vela em formação
			// é refletida pelo fluxo de ticks (preço ao vivo).
			if !c.Complete {
				return
			}
			m.emit(event.Event{
				Type:    event.CandleClosed,
				Source:  "exchange:" + m.ex.Name(),
				Payload: c,
			})
		},
		OnStatus: func(s exchange.Status) {
			t := event.ExchangeConnected
			sev := event.SeverityInfo
			switch s.State {
			case "disconnected":
				t = event.ExchangeDisconnected
				sev = event.SeverityWarning
			case "error":
				t = event.ExchangeError
				sev = event.SeverityError
			}
			m.emit(event.Event{
				Type:     t,
				Source:   "exchange:" + s.Exchange,
				Severity: sev,
				Payload:  s,
			})
		},
	})
}

// Watch assina candles + trades de um símbolo.
func (m *MarketData) Watch(symbol, interval string) error {
	if err := m.ex.SubscribeCandles(symbol, interval); err != nil {
		return err
	}
	return m.ex.SubscribeTrades(symbol)
}
