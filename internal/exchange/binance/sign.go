// sign.go — assinatura HMAC-SHA256 e requisições autenticadas da Binance.
// Endpoints autenticados exigem o parâmetro `timestamp` e a `signature`
// (HMAC-SHA256 do query string com a chave secreta), além do header
// X-MBX-APIKEY.
package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

// signedRequest monta e executa uma requisição autenticada. Os parâmetros
// (incluindo timestamp e signature) vão no query string — a Binance aceita
// isso para GET, POST e DELETE.
func (c *Client) signedRequest(ctx context.Context, method, path string, params url.Values, out any) error {
	if !c.TradingEnabled() {
		return fmt.Errorf("binance: credenciais ausentes (use NewTrading)")
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	params.Set("recvWindow", "10000") // janela de 10s: tolera leve deriva de relógio
	qs := sign(params, c.secret)

	u := c.restBase + path + "?" + qs
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-MBX-APIKEY", c.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Não incluir a URL (que carrega a signature) na mensagem de erro.
		// Rede/timeout → transitório (retryable) — a saga de recuperação usa isso.
		return exchange.Wrap(exchange.KindTransient, fmt.Errorf("binance: requisição %s %s falhou", method, path))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchange.Wrap(exchange.KindTransient, err)
	}
	if resp.StatusCode >= 400 {
		return exchange.Wrap(classifyStatus(resp.StatusCode), fmt.Errorf("binance %s %s: %d %s", method, path, resp.StatusCode, string(body)))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("binance: parse %s: %w", path, err)
		}
	}
	return nil
}

// sign assina um query string com HMAC-SHA256 e devolve o query string final.
func sign(params url.Values, secret string) string {
	qs := params.Encode() // url.Values.Encode() ordena as chaves (exigência da Binance)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(qs))
	return qs + "&signature=" + hex.EncodeToString(mac.Sum(nil))
}
