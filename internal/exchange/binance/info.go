// info.go — metadados de instrumento (P1-1): carrega o /api/v3/exchangeInfo e
// valida/arredonda ordens contra as regras da exchange (step_size, tick_size,
// min_qty, min_notional) ANTES de assinar e enviar. Valores sempre em
// decimal.Decimal — nunca float no caminho de liquidação.
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// SymbolInfo são as regras de negociação de um símbolo, derivadas do
// exchangeInfo da Binance.
type SymbolInfo struct {
	Symbol      string
	BaseAsset   string
	QuoteAsset  string
	Status      string
	TickSize    decimal.Decimal // PRICE_FILTER
	StepSize    decimal.Decimal // LOT_SIZE
	MinQty      decimal.Decimal // LOT_SIZE
	MinNotional decimal.Decimal // MIN_NOTIONAL
}

// Info é o cache local do exchangeInfo, revalidado periodicamente.
type Info struct {
	mu       sync.RWMutex
	base     string
	symbols  map[string]SymbolInfo
	loadedAt time.Time
}

func newInfo(restBase string) *Info {
	return &Info{base: restBase, symbols: make(map[string]SymbolInfo)}
}

// exchangeInfoResponse espelha a resposta do /api/v3/exchangeInfo (só os
// campos que o OMS precisa para validar ordens).
type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol     string `json:"symbol"`
		Status     string `json:"status"`
		BaseAsset  string `json:"baseAsset"`
		QuoteAsset string `json:"quoteAsset"`
		Filters    []struct {
			FilterType  string `json:"filterType"`
			TickSize    string `json:"tickSize"`
			MinQty      string `json:"minQty"`
			StepSize    string `json:"stepSize"`
			MinNotional string `json:"minNotional"`
		} `json:"filters"`
	} `json:"symbols"`
}

// Load busca o exchangeInfo (endpoint público) e popula o cache.
func (i *Info) Load(ctx context.Context) error {
	u := i.base + "/api/v3/exchangeInfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("binance exchangeInfo: requisição falhou")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("binance exchangeInfo: %d %s", resp.StatusCode, string(body))
	}
	var out exchangeInfoResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("binance exchangeInfo: parse: %w", err)
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	next := make(map[string]SymbolInfo, len(out.Symbols))
	for _, s := range out.Symbols {
		si := SymbolInfo{Symbol: s.Symbol, BaseAsset: s.BaseAsset, QuoteAsset: s.QuoteAsset, Status: s.Status}
		for _, f := range s.Filters {
			switch f.FilterType {
			case "PRICE_FILTER":
				si.TickSize, _ = parseDecimal(f.TickSize)
			case "LOT_SIZE":
				si.StepSize, _ = parseDecimal(f.StepSize)
				si.MinQty, _ = parseDecimal(f.MinQty)
			case "MIN_NOTIONAL":
				// Filtro antigo (deprecado): minNotional direto.
				si.MinNotional, _ = parseDecimal(f.MinNotional)
			case "NOTIONAL":
				// Filtro novo (2024+): NOTIONAL com minNotional — a Binance
				// renomeou. minNotional=0 significa "sem mínimo" (mercados
				// isentos); o valor real (ex.: 5.0) é o mínimo da operação.
				si.MinNotional, _ = parseDecimal(f.MinNotional)
			}
		}
		next[s.Symbol] = si
	}
	i.symbols = next
	i.loadedAt = time.Now()
	return nil
}

// Get devolve as regras de um símbolo.
func (i *Info) Get(symbol string) (SymbolInfo, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	si, ok := i.symbols[symbol]
	return si, ok
}

// Stale devolve true se o cache está mais velho que maxAge (ou nunca carregado).
func (i *Info) Stale(maxAge time.Duration) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.loadedAt.IsZero() || time.Since(i.loadedAt) > maxAge
}

// RefreshIfStale recarrega o exchangeInfo se o cache estiver velho ou ausente.
func (i *Info) RefreshIfStale(ctx context.Context, maxAge time.Duration) error {
	if !i.Stale(maxAge) {
		return nil
	}
	return i.Load(ctx)
}

// applySymbolRules valida e arredonda a ordem contra as regras do símbolo
// (P1-1). Fail-closed: sem metadados ou abaixo do mínimo → erro claro, nunca
// envia ordem inválida.
func (c *Client) applySymbolRules(ctx context.Context, req *exchange.OrderRequest) error {
	info, ok := c.symbolInfo(req.Symbol)
	if !ok {
		return fmt.Errorf("binance: símbolo %q sem metadados de instrumento (exchangeInfo)", req.Symbol)
	}
	if info.Status != "" && info.Status != "TRADING" {
		return fmt.Errorf("binance: símbolo %q fora de negociação (status %s)", req.Symbol, info.Status)
	}
	// Arredonda para os lotes/preços válidos da exchange.
	req.Quantity = roundQty(req.Quantity, info.StepSize)
	if req.Price.Sign() > 0 {
		req.Price = roundPrice(req.Price, info.TickSize)
	}
	if info.MinQty.Sign() > 0 && req.Quantity.LessThan(info.MinQty) {
		return fmt.Errorf("binance: quantidade %s abaixo do mínimo %s de %s", req.Quantity, info.MinQty, req.Symbol)
	}
	if info.MinNotional.Sign() > 0 {
		// P1: notional é preço×quantidade em QUOTE. Em ordem MARKET o preço é
		// zero — comparar quantidade (base) com notional (quote) rejeitaria
		// ordens market válidas (0.0005 BTC < 5 USDT). Estima pelo preço
		// corrente; se indisponível, deixa a exchange decidir (a camada OMS já
		// validou o mínimo com o mesmo critério).
		notional := req.Price.Mul(req.Quantity)
		if req.Price.Sign() <= 0 {
			if px, err := c.Price(ctx, req.Symbol); err == nil && px.Sign() > 0 {
				notional = px.Mul(req.Quantity)
			} else {
				return nil // sem preço de referência → não false-rejeitar
			}
		}
		if notional.LessThan(info.MinNotional) {
			return fmt.Errorf("binance: notional %s abaixo do mínimo %s de %s", notional, info.MinNotional, req.Symbol)
		}
	}
	return nil
}

// roundQty arredonda a quantidade para múltiplo de step_size, SEMPRE para
// baixo (nunca excede o lote permitido pela exchange).
func roundQty(qty, step decimal.Decimal) decimal.Decimal {
	if step.Sign() <= 0 {
		return qty
	}
	return qty.Div(step).Floor().Mul(step)
}

// roundPrice arredonda o preço para múltiplo de tick_size (arredondamento
// comercial — sempre um preço válido na exchange).
func roundPrice(price, tick decimal.Decimal) decimal.Decimal {
	if tick.Sign() <= 0 {
		return price
	}
	return price.Div(tick).Round(0).Mul(tick)
}

// Price devolve o preço corrente de um símbolo (GET /api/v3/ticker/price —
// endpoint público, sem assinatura). Usado pelo OMS para estimar o notional
// de ordens market (limite COSCA_TRADER_MAX_ORDER_USDT).
func (c *Client) Price(ctx context.Context, symbol string) (decimal.Decimal, error) {
	params := url.Values{}
	params.Set("symbol", symbol)
	u := c.restBase + "/api/v3/ticker/price?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return decimal.Zero, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return decimal.Zero, fmt.Errorf("binance ticker: requisição falhou")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return decimal.Zero, err
	}
	if resp.StatusCode >= 400 {
		return decimal.Zero, fmt.Errorf("binance ticker %s: %d %s", symbol, resp.StatusCode, string(body))
	}
	var out struct {
		Price string `json:"price"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return decimal.Zero, fmt.Errorf("binance ticker: parse: %w", err)
	}
	return parseDecimal(out.Price)
}
