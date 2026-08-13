// rest.go — endpoints autenticados da Binance: ordens, conta e user stream.
package binance

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// restOrder espelha a resposta de ordem dos endpoints REST (campos em
// lowerCamelCase, diferente do stream executionReport).
type restOrder struct {
	Symbol        string `json:"symbol"`
	OrderID       int64  `json:"orderId"`
	ClientOrderID string `json:"clientOrderId"`
	Price         string `json:"price"`
	StopPrice     string `json:"stopPrice"`
	OrigQty       string `json:"origQty"`
	ExecutedQty   string `json:"executedQty"`
	Status        string `json:"status"`
	Type          string `json:"type"`
	Side          string `json:"side"`
	TimeInForce   string `json:"timeInForce"`
	TransactTime  int64  `json:"transactTime"`
}

func (r restOrder) toDomain() domain.Order {
	return domain.Order{
		ID:            strconv.FormatInt(r.OrderID, 10),
		ClientOrderID: r.ClientOrderID,
		Symbol:        r.Symbol,
		Side:          toSide(r.Side),
		Type:          toOrderType(r.Type),
		Price:         parseFloat(r.Price),
		StopPrice:     parseFloat(r.StopPrice),
		Quantity:      parseFloat(r.OrigQty),
		FilledQty:     parseFloat(r.ExecutedQty),
		Status:        toStatus(r.Status),
		TimeInForce:   toTimeInForce(r.TimeInForce),
		CreatedAt:     time.UnixMilli(r.TransactTime),
	}
}

// PlaceOrder envia uma ordem (POST /api/v3/order).
func (c *Client) PlaceOrder(ctx context.Context, req exchange.OrderRequest) (domain.Order, error) {
	params := url.Values{}
	params.Set("symbol", req.Symbol)
	params.Set("side", string(req.Side))
	params.Set("type", string(req.Type))
	params.Set("quantity", strconv.FormatFloat(req.Quantity, 'f', -1, 64))
	if req.Price > 0 {
		params.Set("price", strconv.FormatFloat(req.Price, 'f', -1, 64))
	}
	if req.StopPrice > 0 {
		params.Set("stopPrice", strconv.FormatFloat(req.StopPrice, 'f', -1, 64))
	}
	if req.TimeInForce != "" {
		params.Set("timeInForce", string(req.TimeInForce))
	}
	if req.ClientOrderID != "" {
		params.Set("newClientOrderId", req.ClientOrderID)
	}

	var out restOrder
	if err := c.signedRequest(ctx, httpMethodPost, "/api/v3/order", params, &out); err != nil {
		return domain.Order{}, err
	}
	return out.toDomain(), nil
}

// CancelOrder cancela uma ordem (DELETE /api/v3/order).
func (c *Client) CancelOrder(ctx context.Context, symbol, orderID string) error {
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("orderId", orderID)
	return c.signedRequest(ctx, httpMethodDelete, "/api/v3/order", params, nil)
}

// Balances devolve os saldos da conta (GET /api/v3/account).
func (c *Client) Balances(ctx context.Context) ([]domain.Balance, error) {
	var out struct {
		Balances []struct {
			Asset  string `json:"asset"`
			Free   string `json:"free"`
			Locked string `json:"locked"`
		} `json:"balances"`
	}
	if err := c.signedRequest(ctx, httpMethodGet, "/api/v3/account", url.Values{}, &out); err != nil {
		return nil, err
	}
	res := make([]domain.Balance, 0, len(out.Balances))
	for _, b := range out.Balances {
		bal := domain.Balance{Asset: b.Asset, Free: parseFloat(b.Free), Locked: parseFloat(b.Locked)}
		if bal.Free == 0 && bal.Locked == 0 {
			continue // ignora saldos zerados
		}
		res = append(res, bal)
	}
	return res, nil
}

// OpenOrders devolve as ordens ativas de um símbolo (GET /api/v3/openOrders).
func (c *Client) OpenOrders(ctx context.Context, symbol string) ([]domain.Order, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	var out []restOrder
	if err := c.signedRequest(ctx, httpMethodGet, "/api/v3/openOrders", params, &out); err != nil {
		return nil, err
	}
	res := make([]domain.Order, 0, len(out))
	for _, o := range out {
		res = append(res, o.toDomain())
	}
	return res, nil
}

const (
	httpMethodGet    = "GET"
	httpMethodPost   = "POST"
	httpMethodDelete = "DELETE"
)

// toSide converte "BUY"/"SELL" para domain.Side.
func toSide(s string) domain.Side {
	if s == "SELL" {
		return domain.SideSell
	}
	return domain.SideBuy
}

// toOrderType converte o tipo da Binance para domain.OrderType.
func toOrderType(s string) domain.OrderType {
	switch s {
	case "MARKET":
		return domain.OrderMarket
	case "STOP_LOSS":
		return domain.OrderStop
	case "STOP_LOSS_LIMIT", "TAKE_PROFIT_LIMIT":
		return domain.OrderStopLimit
	default:
		return domain.OrderLimit
	}
}

// toStatus converte o status da Binance para domain.OrderStatus.
func toStatus(s string) domain.OrderStatus {
	switch s {
	case "NEW":
		return domain.OrderNew
	case "PARTIALLY_FILLED":
		return domain.OrderPartiallyFilled
	case "FILLED":
		return domain.OrderFilled
	case "CANCELED", "PENDING_CANCEL":
		return domain.OrderCanceled
	case "REJECTED", "EXPIRED":
		return domain.OrderRejected
	default:
		return domain.OrderNew
	}
}

// toTimeInForce converte GTC/IOC/FOK para domain.TimeInForce.
func toTimeInForce(s string) domain.TimeInForce {
	return domain.TimeInForce(s)
}
