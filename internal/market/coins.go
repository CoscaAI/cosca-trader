// Package market — o radar de moedas: a lista de ativos operáveis (sidebar à
// direita, estilo TradingView) com sigla, nome, variação 24h, volume e market
// cap. Combina a Binance (ticker 24h real + símbolos operáveis) com o
// CoinGecko (nome + market cap). Filtra por volume diário consistente e
// market cap suficiente para operar — sem lixo, só ativos líquidos.
package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/exchange/binance"
)

// Coin é um ativo da lista do sidebar.
type Coin struct {
	Symbol     string  `json:"symbol"`      // "BTCUSDT"
	Base       string  `json:"base"`        // "BTC"
	Name       string  `json:"name"`        // "Bitcoin"
	Price      float64 `json:"price"`       // preço em USDT
	Change24h  float64 `json:"change_24h"`  // % variação 24h (Binance)
	Volume24h  float64 `json:"volume_24h"`  // volume 24h em USDT (Binance)
	MarketCap  float64 `json:"market_cap"`  // market cap em USD (CoinGecko)
}

// Filtros de operabilidade (o Don: "volume diário consistente" + "bastante
// market cap pra operações"). Limiares conservadores — só ativos líquidos.
const (
	minQuoteVolume = 20_000_000.0  // US$ 20M de volume 24h (liquidez)
	minMarketCap   = 100_000_000.0 // US$ 100M de market cap
)

// FetchCoins monta a lista de ativos operáveis: ticker 24h da Binance (só
// pares USDT) cruzado com o CoinGecko (nome + market cap), filtrado e ordenado
// por volume. Se o CoinGecko falhar, devolve com nome=vazio (só Binance).
func FetchCoins(ctx context.Context, bc *binance.Client) ([]Coin, error) {
	tickers, err := bc.Ticker24hAll(ctx)
	if err != nil {
		return nil, err
	}

	// 1. pares USDT com volume consistente
	byBase := map[string]binance.Ticker24h{}
	for _, t := range tickers {
		if !strings.HasSuffix(t.Symbol, "USDT") {
			continue
		}
		base := strings.TrimSuffix(t.Symbol, "USDT")
		if t.QuoteVolume < minQuoteVolume {
			continue
		}
		// mantém o par com MAIOR volume se houver duplicata (ex: stablecoins)
		if prev, ok := byBase[base]; !ok || t.QuoteVolume > prev.QuoteVolume {
			byBase[base] = t
		}
	}

	// 2. CoinGecko: nome + market cap (top 250 por market cap)
	gecko := fetchGecko(ctx)

	// 3. junta + filtra por market cap
	coins := make([]Coin, 0, len(byBase))
	for base, t := range byBase {
		c := Coin{
			Symbol:    t.Symbol,
			Base:      base,
			Price:     t.LastPrice,
			Change24h: t.PriceChangePercent,
			Volume24h: t.QuoteVolume,
		}
		if g, ok := gecko[strings.ToLower(base)]; ok {
			c.Name = g.name
			c.MarketCap = g.marketCap
		}
		if c.MarketCap > 0 && c.MarketCap < minMarketCap {
			continue // market cap conhecido mas insuficiente → fora
		}
		coins = append(coins, c)
	}

	sort.Slice(coins, func(i, j int) bool { return coins[i].Volume24h > coins[j].Volume24h })
	return coins, nil
}

// geckoCoin é o item relevante do CoinGecko /coins/markets.
type geckoCoin struct {
	name      string
	marketCap float64
}

// fetchGecko busca o CoinGecko (nome + market cap). Nunca falha o fluxo — em
// erro devolve mapa vazio e o sidebar segue só com dados da Binance.
func fetchGecko(ctx context.Context) map[string]geckoCoin {
	out := map[string]geckoCoin{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=250&page=1", nil)
	if err != nil {
		return out
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return out
	}
	defer resp.Body.Close()
	var raw []struct {
		Symbol    string  `json:"symbol"`
		Name      string  `json:"name"`
		MarketCap float64 `json:"market_cap"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return out
	}
	for _, c := range raw {
		if c.MarketCap > 0 {
			out[c.Symbol] = geckoCoin{name: c.Name, marketCap: c.MarketCap}
		}
	}
	return out
}

// String devolve uma representação legível (para debug/teste).
func (c Coin) String() string {
	return fmt.Sprintf("%s (%s): $%.2f | %.2f%% | vol $%.0f | cap $%.0f",
		c.Symbol, c.Name, c.Price, c.Change24h, c.Volume24h, c.MarketCap)
}
