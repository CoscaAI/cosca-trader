// Package marketindex monitora os mercados GLOBAIS (S&P 500, NASDAQ, VIX,
// ouro, dólar) via Yahoo Finance — API pública, SEM chave. É a proteção macro
// do Don: o cripto não vive numa bolha; saber o que acontece lá fora antecipa
// movimentos aqui dentro (risk-off / risk-on).
package marketindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Asset define um ativo global monitorado.
type Asset struct {
	Symbol string `json:"symbol"`
	Name   string `json:"name"`
}

// Quote é a cotação corrente de um ativo global.
type Quote struct {
	Symbol  string  `json:"symbol"`
	Name    string  `json:"name"`
	Price   float64 `json:"price"`
	Change5d float64 `json:"change_5d_pct"` // variação em 5 dias (%)
	Change10d float64 `json:"change_10d_pct"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Snapshot é o estado corrente de todos os mercados monitorados.
type Snapshot struct {
	Quotes   []Quote     `json:"quotes"`
	Regime   string      `json:"regime"` // "risk-on" | "risk-off" | "neutral"
	Signal   string      `json:"signal"` // texto da leitura macro
	FetchedAt time.Time  `json:"fetched_at"`
}

// Client coleta cotações do Yahoo Finance (sem chave).
type Client struct {
	base   string
	assets []Asset
	mu     sync.RWMutex
	cache  []Quote
	last   time.Time
	ttl    time.Duration
	httpc  *http.Client
}

// New cria o cliente com os mercados default.
func New() *Client {
	return &Client{
		base: "https://query1.finance.yahoo.com/v8/finance/chart",
		assets: []Asset{
			{Symbol: "^GSPC", Name: "S&P 500"},
			{Symbol: "^IXIC", Name: "NASDAQ"},
			{Symbol: "^VIX", Name: "VIX (medo)"},
			{Symbol: "GC=F", Name: "Ouro"},
			{Symbol: "DX-Y.NYB", Name: "Dólar (DXY)"},
		},
		ttl:   5 * time.Minute,
		httpc: &http.Client{Timeout: 15 * time.Second},
	}
}

// Assets devolve a lista de ativos monitorados.
func (c *Client) Assets() []Asset {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Asset, len(c.assets))
	copy(out, c.assets)
	return out
}

// chartResponse espelha a resposta do Yahoo Finance chart API.
type chartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol          string  `json:"symbol"`
				RegularPrice    float64 `json:"regularMarketPrice"`
			} `json:"meta"`
			Indicators struct {
				Quote []struct {
					Close []*float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
	} `json:"chart"`
}

// Fetch coleta as cotações de todos os ativos (com cache por TTL).
func (c *Client) Fetch(ctx context.Context) (Snapshot, error) {
	c.mu.RLock()
	if !c.last.IsZero() && time.Since(c.last) < c.ttl {
		snap := c.buildSnapshotLocked()
		c.mu.RUnlock()
		return snap, nil
	}
	c.mu.RUnlock()

	quotes := make([]Quote, 0, len(c.assets))
	for _, a := range c.assets {
		q, err := c.fetchOne(ctx, a)
		if err != nil {
			continue // um ativo fora do ar não derruba o monitor
		}
		quotes = append(quotes, q)
	}

	c.mu.Lock()
	c.cache = quotes
	c.last = time.Now()
	snap := c.buildSnapshotLocked()
	c.mu.Unlock()
	return snap, nil
}

// fetchOne busca a cotação de UM ativo (10 dias para a variação).
func (c *Client) fetchOne(ctx context.Context, a Asset) (Quote, error) {
	url := fmt.Sprintf("%s/%s?interval=1d&range=10d", c.base, urlEncode(a.Symbol))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Quote{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return Quote{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("%s HTTP %d", a.Symbol, resp.StatusCode)
	}

	var out chartResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Quote{}, err
	}
	if len(out.Chart.Result) == 0 {
		return Quote{}, fmt.Errorf("%s sem resultado", a.Symbol)
	}
	r := out.Chart.Result[0]
	q := Quote{
		Symbol:    a.Symbol,
		Name:      a.Name,
		Price:     r.Meta.RegularPrice,
		FetchedAt: time.Now(),
	}
	// Variação do range (5d e 10d) a partir dos closes.
	if len(r.Indicators.Quote) > 0 {
		closes := r.Indicators.Quote[0].Close
		var vals []float64
		for _, c := range closes {
			if c != nil {
				vals = append(vals, *c)
			}
		}
		if len(vals) > 1 {
			q.Change10d = (vals[len(vals)-1] - vals[0]) / vals[0] * 100
		}
		if len(vals) > 5 {
			start := vals[len(vals)-5]
			q.Change5d = (vals[len(vals)-1] - start) / start * 100
		}
	}
	return q, nil
}

// buildSnapshotLocked monta o snapshot com o regime macro (lock garantido).
func (c *Client) buildSnapshotLocked() Snapshot {
	snap := Snapshot{Quotes: c.cache, FetchedAt: c.last}
	by := map[string]Quote{}
	for _, q := range c.cache {
		by[q.Symbol] = q
	}

	sp := by["^GSPC"]
	vix := by["^VIX"]
	gold := by["GC=F"]
	dxy := by["DX-Y.NYB"]

	// Leitura macro: regime dominante pelas últimas 5d.
	riskOff := 0
	if sp.Price > 0 && sp.Change5d < -1.0 {
		riskOff++
	}
	if vix.Price > 25 {
		riskOff++ // VIX alto = medo
	}
	if gold.Price > 0 && gold.Change5d > 2.0 {
		riskOff++ // ouro disparando = refúgio = aversão ao risco
	}
	if dxy.Price > 0 && dxy.Change5d > 1.0 {
		riskOff++ // dólar forte = pressão no risco
	}

	switch {
	case riskOff >= 3:
		snap.Regime = "risk-off"
		snap.Signal = "⚠ RISK-OFF: mercados globais em aversão ao risco — reduzir exposição cripto"
	case riskOff == 2:
		snap.Regime = "cautela"
		snap.Signal = "⚡ CAUTELA: sinais mistos nos mercados globais — manter stops apertados"
	default:
		snap.Regime = "risk-on"
		snap.Signal = "✅ RISK-ON: mercados globais favoráveis — ambiente construtivo para o cripto"
	}
	return snap
}

// Regime devolve apenas o regime corrente (curto).
func (c *Client) Regime(ctx context.Context) (string, error) {
	snap, err := c.Fetch(ctx)
	if err != nil {
		return "desconhecido", err
	}
	return snap.Regime, nil
}

// urlEncode codifica um símbolo para a URL (^ → %5E).
func urlEncode(sym string) string {
	out := ""
	for _, r := range sym {
		if r == '^' {
			out += "%5E"
		} else {
			out += string(r)
		}
	}
	return out
}

// SortQuotes ordena as cotações por nome (determinístico).
func SortQuotes(qs []Quote) {
	sort.Slice(qs, func(i, j int) bool { return qs[i].Name < qs[j].Name })
}
