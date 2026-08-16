package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ticker.go — o ticker de 24h (endpoint público /api/v3/ticker/24hr).
// Alimenta o sidebar de moedas: símbolo, preço, variação 24h e volume.

// Ticker24h é um item do ticker de 24h da Binance.
type Ticker24h struct {
	Symbol             string  `json:"symbol"`
	PriceChangePercent float64 `json:"priceChangePercent,string"`
	LastPrice          float64 `json:"lastPrice,string"`
	QuoteVolume        float64 `json:"quoteVolume,string"` // volume 24h em USDT (liquidez)
}

// Ticker24hAll busca o ticker de 24h de TODOS os símbolos (endpoint público).
func (c *Client) Ticker24hAll(ctx context.Context) ([]Ticker24h, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.restBase+"/api/v3/ticker/24hr", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("binance ticker24h: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance ticker24h HTTP %d", resp.StatusCode)
	}
	// a Binance devolve um array plano de objetos; o campo priceChangePercent
	// vem como STRING ("-95.960"), então fazemos o unmarshal manual para o float.
	var raw []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Ticker24h, 0, len(raw))
	for _, r := range raw {
		t := Ticker24h{Symbol: str(r["symbol"])}
		if v, ok := r["priceChangePercent"].(string); ok {
			t.PriceChangePercent, _ = strconv.ParseFloat(v, 64)
		}
		if v, ok := r["lastPrice"].(string); ok {
			t.LastPrice, _ = strconv.ParseFloat(v, 64)
		}
		if v, ok := r["quoteVolume"].(string); ok {
			t.QuoteVolume, _ = strconv.ParseFloat(v, 64)
		}
		out = append(out, t)
	}
	return out, nil
}

// str converte um any para string (vazio se não for string).
func str(v any) string {
	s, _ := v.(string)
	return s
}
