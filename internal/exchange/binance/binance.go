// Package binance implementa o adapter da Binance (WebSocket de market data
// público: kline + trade). Converte as mensagens da corretora para domain.Candle
// e domain.Tick. O parsing é isolado em funções puras (messages.go) para ser
// testado sem rede.
package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/CoscaAI/cosca-trader/internal/exchange"
)

const defaultWSURL = "wss://stream.binance.com:9443/ws"

// Ambiente de execução (P0-3): modo SEGURO é sempre o default — conectar à
// Binance real exige escolha explícita do operador (--live / COSCA_TRADER_ENV).
const (
	EnvTestnet = "testnet"
	EnvLive    = "live"
)

// Client é o adapter Binance (market data + execução autenticada).
type Client struct {
	name     string
	url      string
	restBase string
	apiKey   string
	secret   string

	mu   sync.Mutex
	conn *websocket.Conn
	subs []string
}

// New cria o adapter de market data (sem credenciais — só streams públicos).
func New() *Client {
	return &Client{name: "binance", url: defaultWSURL, restBase: "https://api.binance.com"}
}

// NewTrading cria o adapter com credenciais para execução (ordens/conta).
// env ∈ {EnvTestnet, EnvLive}. P0-3: o ambiente é EXPLÍCITO — o default de
// todo o sistema é seguro; produção (api.binance.com) só é atingida quando o
// operador escolhe EnvLive no startup.
func NewTrading(apiKey, secret, env string) *Client {
	c := New()
	c.apiKey = apiKey
	c.secret = secret
	if env == EnvLive {
		// api.binance.com já é o default de New() — nada a trocar.
		return c
	}
	c.url = "wss://testnet.binance.vision/ws"
	c.restBase = "https://testnet.binance.vision"
	return c
}

// Env devolve o ambiente de execução do adapter (testnet | live).
func (c *Client) Env() string {
	if c.restBase == "https://api.binance.com" && c.url == defaultWSURL {
		return EnvLive
	}
	return EnvTestnet
}

// TradingEnabled devolve se as credenciais de execução estão configuradas.
func (c *Client) TradingEnabled() bool {
	return c.apiKey != "" && c.secret != ""
}

// Name devolve "binance".
func (c *Client) Name() string { return c.name }

// Connect abre o WebSocket e roda o loop de leitura com reconexão automática
// (backoff exponencial, teto de 15s). Bloqueia até o contexto ser cancelado.
func (c *Client) Connect(ctx context.Context, h exchange.Handler) error {
	backoff := time.Second
	for {
		err := c.run(ctx, h)
		if err == nil {
			return nil // fechamento limpo
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

func (c *Client) run(ctx context.Context, h exchange.Handler) error {
	conn, _, err := websocket.Dial(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	subs := append([]string(nil), c.subs...)
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	h.OnStatus(exchange.Status{Exchange: c.name, State: "connected"})

	// Re-assina os streams após reconexão.
	for _, s := range subs {
		if err := c.write(ctx, subscribeMsg(s)); err != nil {
			return err
		}
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if err := handleMessage(data, c.name, h); err != nil {
			h.OnStatus(exchange.Status{Exchange: c.name, State: "error", Message: err.Error()})
		}
	}
}

// SubscribeCandles assina o stream kline de um símbolo + intervalo.
func (c *Client) SubscribeCandles(symbol, interval string) error {
	return c.subscribe(fmt.Sprintf("%s@kline_%s", strings.ToLower(symbol), interval))
}

// SubscribeTrades assina o stream de trades de um símbolo.
func (c *Client) SubscribeTrades(symbol string) error {
	return c.subscribe(fmt.Sprintf("%s@trade", strings.ToLower(symbol)))
}

func (c *Client) subscribe(stream string) error {
	c.mu.Lock()
	for _, s := range c.subs {
		if s == stream {
			c.mu.Unlock()
			return nil // já assinado
		}
	}
	c.subs = append(c.subs, stream)
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return nil // ainda não conectado — será enviado na conexão/reconexão
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.write(ctx, subscribeMsg(stream))
}

func (c *Client) write(ctx context.Context, msg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("binance: conexão fechada")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
}

// Close encerra a conexão ativa.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

// subscribeMsg monta a mensagem de assinatura do protocolo Binance.
func subscribeMsg(stream string) map[string]any {
	return map[string]any{
		"method": "SUBSCRIBE",
		"params": []string{stream},
		"id":     1,
	}
}
