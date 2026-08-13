// userdata.go — user data stream da Binance: eventos de conta (ordens, fills,
// saldos) em tempo real. O fluxo exige um listenKey (criado via REST, com
// keepalive a cada 30 min) e entrega executionReport e outboundAccountPosition.
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"nhooyr.io/websocket"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// executionReport espelha o evento de ordem da Binance. Todos os campos com
// pares maiúsculo/minúsculo (e/E, s/S, l/L, ...) são declarados com tipo certo
// para evitar a colisão case-insensitive do encoding/json.
type executionReport struct {
	Event           string  `json:"e"`
	EventTime       int64   `json:"E"`
	Symbol          string  `json:"s"`
	ClientID        string  `json:"c"`
	Side            string  `json:"S"`
	Type            string  `json:"o"`
	TimeInForce     string  `json:"f"`
	Quantity        string  `json:"q"`
	Price           string  `json:"p"`
	StopPrice       string  `json:"P"`
	IcebergQty      string  `json:"F"`
	OrderListID     int64   `json:"g"`
	OrigClientID    string  `json:"C"`
	ExecType        string  `json:"x"`
	Status          string  `json:"X"`
	RejectReason    string  `json:"r"`
	OrderID         int64   `json:"i"`
	LastQty         string  `json:"l"`
	CumFilledQty    string  `json:"z"`
	LastPrice       string  `json:"L"`
	Commission      string  `json:"n"`
	CommissionAsset *string `json:"N"`
	TransactTime    int64   `json:"T"`
	TradeID         int64   `json:"t"`
	IgnoreI         int64   `json:"I"`
	Working         bool    `json:"w"`
	IsMaker         bool    `json:"m"`
	IgnoreM         bool    `json:"M"`
	OrderTime       int64   `json:"O"`
	CumQuoteQty     string  `json:"Z"`
	LastQuoteQty    string  `json:"Y"`
	QuoteOrderQty   string  `json:"Q"`
}

// outboundAccountPosition espelha o evento de saldos.
type outboundAccountPosition struct {
	Event      string `json:"e"`
	EventTime  int64  `json:"E"`
	LastUpdate int64  `json:"u"`
	Balances   []struct {
		Asset  string `json:"a"`
		Free   string `json:"f"`
		Locked string `json:"l"`
	} `json:"B"`
}

// StartUserStream abre o stream de conta (listenKey) com reconexão automática.
func (c *Client) StartUserStream(ctx context.Context, h exchange.Handler) error {
	if !c.TradingEnabled() {
		return fmt.Errorf("binance: credenciais ausentes (use NewTrading)")
	}
	backoff := time.Second
	for {
		err := c.runUserStream(ctx, h)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h.OnStatus(exchange.Status{Exchange: c.name, State: "disconnected", Message: err.Error()})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) runUserStream(ctx context.Context, h exchange.Handler) error {
	listenKey, err := c.createListenKey(ctx)
	if err != nil {
		return err
	}

	keepaliveCtx, cancelKeepalive := context.WithCancel(ctx)
	defer cancelKeepalive()
	go c.keepalive(keepaliveCtx, listenKey)

	conn, _, err := websocket.Dial(ctx, c.url+"/"+listenKey, nil)
	if err != nil {
		return fmt.Errorf("user stream dial: %w", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	h.OnStatus(exchange.Status{Exchange: c.name, State: "connected"})

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("user stream read: %w", err)
		}
		if err := handleUserMessage(data, c.name, h); err != nil {
			h.OnStatus(exchange.Status{Exchange: c.name, State: "error", Message: err.Error()})
		}
	}
}

func (c *Client) createListenKey(ctx context.Context) (string, error) {
	var out struct {
		ListenKey string `json:"listenKey"`
	}
	if err := c.signedRequest(ctx, httpMethodPost, "/api/v3/userDataStream", url.Values{}, &out); err != nil {
		return "", err
	}
	return out.ListenKey, nil
}

// keepalive renova o listenKey a cada 30 min (expira em 60).
func (c *Client) keepalive(ctx context.Context, listenKey string) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			params := url.Values{}
			params.Set("listenKey", listenKey)
			_ = c.signedRequest(ctx, "PUT", "/api/v3/userDataStream", params, nil)
		}
	}
}

// handleUserMessage roteia executionReport / outboundAccountPosition.
func handleUserMessage(data []byte, exchange string, h exchange.Handler) error {
	var probe struct {
		Event     string `json:"e"`
		EventTime int64  `json:"E"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	switch probe.Event {
	case "executionReport":
		return handleExecutionReport(data, exchange, h)
	case "outboundAccountPosition":
		return handleBalances(data, exchange, h)
	default:
		return nil
	}
}

func handleExecutionReport(data []byte, exchange string, h exchange.Handler) error {
	var r executionReport
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}

	ord := domain.Order{
		ID:            strconv.FormatInt(r.OrderID, 10),
		ClientOrderID: r.ClientID,
		Symbol:        r.Symbol,
		Side:          toSide(r.Side),
		Type:          toOrderType(r.Type),
		Price:         parseFloat(r.Price),
		StopPrice:     parseFloat(r.StopPrice),
		Quantity:      parseFloat(r.Quantity),
		FilledQty:     parseFloat(r.CumFilledQty),
		Status:        toStatus(r.Status),
		TimeInForce:   toTimeInForce(r.TimeInForce),
		CreatedAt:     time.UnixMilli(r.OrderTime),
		UpdatedAt:     time.UnixMilli(r.EventTime),
	}
	if h.OnOrderUpdate != nil {
		h.OnOrderUpdate(ord)
	}

	// Um fill aconteceu quando há quantidade e preço de última execução.
	lastQty, lastPrice := parseFloat(r.LastQty), parseFloat(r.LastPrice)
	if lastQty > 0 && lastPrice > 0 && h.OnTrade != nil {
		feeAsset := ""
		if r.CommissionAsset != nil {
			feeAsset = *r.CommissionAsset
		}
		h.OnTrade(domain.Trade{
			ID:        strconv.FormatInt(r.TradeID, 10),
			OrderID:   ord.ID,
			Symbol:    r.Symbol,
			Exchange:  exchange,
			Side:      ord.Side,
			Price:     lastPrice,
			Quantity:  lastQty,
			Fee:       parseFloat(r.Commission),
			FeeAsset:  feeAsset,
			Timestamp: time.UnixMilli(r.TransactTime),
		})
	}
	return nil
}

func handleBalances(data []byte, exchange string, h exchange.Handler) error {
	var m outboundAccountPosition
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if h.OnBalanceUpdate == nil {
		return nil
	}
	for _, b := range m.Balances {
		h.OnBalanceUpdate(domain.Balance{
			Asset:  b.Asset,
			Free:   parseFloat(b.Free),
			Locked: parseFloat(b.Locked),
		})
	}
	return nil
}
