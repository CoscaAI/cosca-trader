// messages.go — parsing das mensagens WebSocket da Binance (kline + trade)
// para domain.Candle / domain.Tick. Funções puras, testáveis sem rede.
package binance

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// klineMsg espelha o payload `kline` do stream público da Binance.
//
// Importante: a Binance manda `e` (tipo, string) E `E` (event time, número),
// e dentro de `k` manda `l` (low) vs `L` (last trade id) e `v` (volume) vs
// `V` (taker volume). O encoding/json do Go casa campos case-insensitive —
// declarar TODOS os campos com o tipo certo elimina a colisão.
type klineMsg struct {
	Event     string `json:"e"`
	EventTime int64  `json:"E"`
	Symbol    string `json:"s"`
	Kline     struct {
		StartTime   int64  `json:"t"`
		CloseTime   int64  `json:"T"`
		Symbol      string `json:"s"`
		Interval    string `json:"i"`
		FirstTrade  int64  `json:"f"`
		LastTrade   int64  `json:"L"`
		Open        string `json:"o"`
		High        string `json:"h"`
		Low         string `json:"l"`
		Close       string `json:"c"`
		Volume      string `json:"v"`
		Trades      int    `json:"n"`
		Closed      bool   `json:"x"`
		QuoteVolume string `json:"q"`
		TakerVolume string `json:"V"`
		TakerQuote  string `json:"Q"`
		Ignore      string `json:"B"`
	} `json:"k"`
}

// tradeMsg espelha o payload `trade` do stream público da Binance.
// (mesma regra do klineMsg: `E` explícito para não colidir com `e`)
type tradeMsg struct {
	Event     string `json:"e"`
	EventTime int64  `json:"E"`
	Symbol    string `json:"s"`
	Price     string `json:"p"`
	Qty       string `json:"q"`
	Time      int64  `json:"T"`
	Trade     int64  `json:"t"`
}

// handleMessage roteia uma mensagem pelo campo `e` e despacha para o handler.
func handleMessage(data []byte, exchange string, h exchange.Handler) error {
	var probe struct {
		Event     string `json:"e"`
		EventTime int64  `json:"E"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.Event {
	case "kline":
		return handleKline(data, exchange, h)
	case "trade":
		return handleTrade(data, exchange, h)
	default:
		// resposta de SUBSCRIBE/UNSUBSCRIBE e mensagens sem `e` são ignoradas.
		return nil
	}
}

func handleKline(data []byte, exchange string, h exchange.Handler) error {
	var m klineMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if h.OnCandle == nil {
		return nil
	}
	c := domain.Candle{
		Symbol:   m.Symbol,
		Exchange: exchange,
		Interval: m.Kline.Interval,
		OpenTime: time.UnixMilli(m.Kline.StartTime),
		Open:     parseFloat(m.Kline.Open),
		High:     parseFloat(m.Kline.High),
		Low:      parseFloat(m.Kline.Low),
		Close:    parseFloat(m.Kline.Close),
		Volume:   parseFloat(m.Kline.Volume),
		Trades:   m.Kline.Trades,
		Complete: m.Kline.Closed,
	}
	h.OnCandle(c)
	return nil
}

func handleTrade(data []byte, exchange string, h exchange.Handler) error {
	var m tradeMsg
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if h.OnTick == nil {
		return nil
	}
	h.OnTick(domain.Tick{
		Symbol:   m.Symbol,
		Exchange: exchange,
		Price:    parseFloat(m.Price),
		Quantity: parseFloat(m.Qty),
		Time:     time.UnixMilli(m.Time),
	})
	return nil
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
