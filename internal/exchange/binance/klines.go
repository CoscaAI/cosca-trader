// klines.go — histórico de candles da Binance via REST PÚBLICO (sem chave).
// A API /api/v3/klines não exige autenticação: é o caminho da Fase 5 para
// coletar dados REAIS e rodar o laudo científico ANTES de qualquer credencial.
// A chave do Don só entra quando o laudo aprovar a estratégia.
//
// Paginação: a Binance devolve no máximo 1000 klines por chamada; para
// históricos maiores, usa startTime para andar para trás (loop até ter o
// número pedido ou esgotar o intervalo).
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// KlineResponse é UM elemento do array devolvido por /api/v3/klines.
// A Binance devolve um array de arrays: [openTime, open, high, low, close,
// volume, closeTime, ...]. Os preços vêm como STRING (precisão exata — a
// doutrina fintech da casa: nunca float no caminho do dinheiro; aqui é market
// data, mas mantemos o parse via string para fidelidade).
type klineResponse []struct {
	OpenTime  int64  `json:"open_time"`
	Open      string `json:"open"`
	High      string `json:"high"`
	Low       string `json:"low"`
	Close     string `json:"close"`
	Volume    string `json:"volume"`
	CloseTime int64  `json:"close_time"`
	// raw é o array original; usamos apenas os campos acima via unmarshal
	// manual em array — ver Klines.
}

// Klines busca candles históricos reais de um símbolo+intervalo, da mais
// antiga para a mais recente. Sem credenciais — endpoint público.
// `limit` é o máximo de velas por página (max 1000); `bars` é o total pedido
// (paginação automática quando > 1000).
func (c *Client) Klines(ctx context.Context, symbol, interval string, bars, limit int) ([]domain.Candle, error) {
	if limit < 1 || limit > 1000 {
		limit = 1000
	}
	if bars < 1 {
		bars = 1
	}

	// A API pública funciona em QUALQUER ambiente (o REST base do client é
	// api.binance.com no default; testnet também expõe klines).
	restBase := c.restBase
	if restBase == "" {
		restBase = "https://api.binance.com"
	}

	var out []domain.Candle
	// Começa pelo presente e anda para trás (startTime decrescente) — a
	// Binance devolve velas de startTime em diante; coletando do fim para o
	// começo garantimos o histórico mais RECENTE primeiro.
	endTime := time.Now().UnixMilli()
	first := true

	for len(out) < bars {
		params := url.Values{}
		params.Set("symbol", symbol)
		params.Set("interval", interval)
		params.Set("limit", strconv.Itoa(limit))
		params.Set("endTime", strconv.FormatInt(endTime, 10))
		// startTime = endTime - limit*intervalMs (uma janela para trás).
		startTime := endTime - int64(limit)*intervalMillis(interval)
		params.Set("startTime", strconv.FormatInt(startTime, 10))

		u := restBase + "/api/v3/klines?" + params.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("klines: montar requisição: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("klines: %s %s: %w", symbol, interval, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("klines: %s %s: HTTP %d", symbol, interval, resp.StatusCode)
		}

		var raw []json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("klines: decodificar: %w", err)
		}
		resp.Body.Close()

		if len(raw) == 0 {
			break // sem mais dados
		}

		// Cada kline é um array posicional:
		// [0]=openTime [1]=open [2]=high [3]=low [4]=close [5]=volume
		var page []domain.Candle
		for _, r := range raw {
			var arr []json.RawMessage
			if err := json.Unmarshal(r, &arr); err != nil || len(arr) < 6 {
				continue
			}
			// openTime é um NÚMERO (ms epoch); preços/volume são STRINGS.
			var openTime int64
			if err := json.Unmarshal(arr[0], &openTime); err != nil {
				continue
			}
			var fields [5]string // open, high, low, close, volume
			ok := true
			for i := 0; i < 5; i++ {
				if err := json.Unmarshal(arr[i+1], &fields[i]); err != nil {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			c := domain.Candle{
				Symbol:    symbol,
				Exchange:  "binance",
				Interval:  interval,
				OpenTime:  time.UnixMilli(openTime),
				Open:      parseF(fields[0]),
				High:      parseF(fields[1]),
				Low:       parseF(fields[2]),
				Close:     parseF(fields[3]),
				Volume:    parseF(fields[4]),
				Complete:  true,
			}
			page = append(page, c)
		}

		if len(page) == 0 {
			break
		}

		// A página vem do mais antigo para o mais recente dentro da janela.
		// Acumula e avança a janela para trás.
		out = append(page, out...)
		oldest := page[0].OpenTime.UnixMilli()
		if oldest == endTime && first {
			// janela sem avanço — evita loop infinito
			break
		}
		endTime = oldest - 1
		first = false

		if len(page) < limit {
			break // última página
		}
	}

	// Corta o excesso (bars é o total pedido).
	if len(out) > bars {
		out = out[:bars]
	}
	return out, nil
}

// intervalMillis devolve a duração de um intervalo Binance em ms (os comuns).
func intervalMillis(interval string) int64 {
	switch interval {
	case "1m":
		return 60_000
	case "3m":
		return 3 * 60_000
	case "5m":
		return 5 * 60_000
	case "15m":
		return 15 * 60_000
	case "30m":
		return 30 * 60_000
	case "1h":
		return 60 * 60_000
	case "4h":
		return 4 * 60 * 60_000
	case "1d":
		return 24 * 60 * 60_000
	case "1w":
		return 7 * 24 * 60 * 60_000
	default:
		return 60_000 // default 1m
	}
}

// parseF converte string numérica (ex.: "50000.12") em float64 para market
// data. O caminho do DINHEIRO continua decimal — aqui é display/análise.
func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
