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

func (r restOrder) toDomain() (domain.Order, error) {
	price, err := parseDecimal(r.Price)
	if err != nil {
		return domain.Order{}, err
	}
	stopPrice, err := parseDecimal(r.StopPrice)
	if err != nil {
		return domain.Order{}, err
	}
	quantity, err := parseDecimal(r.OrigQty)
	if err != nil {
		return domain.Order{}, err
	}
	filledQty, err := parseDecimal(r.ExecutedQty)
	if err != nil {
		return domain.Order{}, err
	}
	return domain.Order{
		ID:            strconv.FormatInt(r.OrderID, 10),
		ClientOrderID: r.ClientOrderID,
		Symbol:        r.Symbol,
		Side:          toSide(r.Side),
		Type:          toOrderType(r.Type),
		Price:         price,
		StopPrice:     stopPrice,
		Quantity:      quantity,
		FilledQty:     filledQty,
		Status:        toStatus(r.Status),
		TimeInForce:   toTimeInForce(r.TimeInForce),
		CreatedAt:     time.UnixMilli(r.TransactTime),
	}, nil
}

// PlaceOrder envia uma ordem (POST /api/v3/order).
func (c *Client) PlaceOrder(ctx context.Context, req exchange.OrderRequest) (domain.Order, error) {
	params := url.Values{}
	params.Set("symbol", req.Symbol)
	params.Set("side", string(req.Side))
	params.Set("type", string(req.Type))
	params.Set("quantity", req.Quantity.String())
	if req.Price.Sign() > 0 {
		params.Set("price", req.Price.String())
	}
	if req.StopPrice.Sign() > 0 {
		params.Set("stopPrice", req.StopPrice.String())
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
	return out.toDomain()
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
		free, err := parseDecimal(b.Free)
		if err != nil {
			return nil, err
		}
		locked, err := parseDecimal(b.Locked)
		if err != nil {
			return nil, err
		}
		bal := domain.Balance{Asset: b.Asset, Free: free, Locked: locked}
		if bal.Free.IsZero() && bal.Locked.IsZero() {
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
		ord, err := o.toDomain()
		if err != nil {
			return nil, err
		}
		res = append(res, ord)
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
