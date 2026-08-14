// prediction.go — o RASTREADOR DE PREVISÕES (Fase 5, "laboratório vivo").
//
// O problema que o Don descreveu: "parece funcionar mas ao entrar a ação
// muda". Um backtest valida contra o passado; o mercado real pode ter mudado
// de regime. A resposta científica: CADA sinal da estratégia vira uma
// PREVISÃO (direção + preço de entrada), e o mercado REAL (velas que chegam
// depois) decide se a previsão acertou. Com uma janela de previsões
// realizadas, medimos se o cálculo está CONVERGINDO com a realidade.
package strategy

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Prediction é UMA previsão da estratégia, pendente de realização.
type Prediction struct {
	ID         string          `json:"id"`
	Strategy   string          `json:"strategy"`
	Symbol     string          `json:"symbol"`
	Side       string          `json:"side"` // "buy" | "sell"
	EntryPx    decimal.Decimal `json:"entry_px"`
	CreatedAt  time.Time       `json:"created_at"`
	// Horizon é de quantas velas em diante validar a direção (ex.: 5).
	Horizon int `json:"horizon"`
	// Resolvido — preenchido quando o horizonte passa.
	Resolved     bool            `json:"resolved"`
	ExitPx       decimal.Decimal `json:"exit_px,omitempty"`
	Hit          *bool           `json:"hit,omitempty"` // acertou a direção?
	ReturnPct    float64         `json:"return_pct,omitempty"` // retorno no horizonte
	ResolvedAt   time.Time       `json:"resolved_at,omitempty"`
}

// PredictionResult é o resultado resolvido de uma previsão (para estatística).
type PredictionResult struct {
	Side      string          `json:"side"`
	EntryPx   decimal.Decimal `json:"entry_px"`
	ExitPx    decimal.Decimal `json:"exit_px"`
	Hit       bool            `json:"hit"`
	ReturnPct float64         `json:"return_pct"`
	ResolvedAt time.Time      `json:"resolved_at"`
}

// PredictionTracker registra previsões e as resolve quando o horizonte passa.
// Thread-safe (o mercado alimenta em goroutines).
type PredictionTracker struct {
	mu         sync.Mutex
	strategy   string
	horizon    int
	pending    []*Prediction
	results    []PredictionResult
	maxResults int // janela deslizante de resultados
	nextID     int
}

// NewPredictionTracker cria o rastreador. horizon = velas até validar;
// maxResults = tamanho da janela deslizante de resultados.
func NewPredictionTracker(strategy string, horizon, maxResults int) *PredictionTracker {
	if horizon < 1 {
		horizon = 1
	}
	if maxResults < 10 {
		maxResults = 50
	}
	return &PredictionTracker{
		strategy:   strategy,
		horizon:    horizon,
		results:    make([]PredictionResult, 0, maxResults),
		maxResults: maxResults,
	}
}

// Record registra uma previsão a partir de um sinal.
func (p *PredictionTracker) Record(sig Signal) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	id := "pred-" + itoa(p.nextID)
	p.pending = append(p.pending, &Prediction{
		ID:        id,
		Strategy:  p.strategy,
		Symbol:    sig.Symbol,
		Side:      sig.Side,
		EntryPx:   sig.Price,
		CreatedAt: time.Now(),
		Horizon:   p.horizon,
	})
	return id
}

// OnCandle avança cada previsão pendente em uma vela do seu símbolo e resolve
// as que atingiram o horizonte. Chamado a cada CandleClosed do símbolo.
func (p *PredictionTracker) OnCandle(c domain.Candle) {
	p.mu.Lock()
	defer p.mu.Unlock()

	price := decimal.NewFromFloat(c.Close)
	var still []*Prediction
	for _, pred := range p.pending {
		if pred.Symbol != c.Symbol {
			still = append(still, pred)
			continue
		}
		pred.Horizon--
		if pred.Horizon > 0 {
			still = append(still, pred)
			continue
		}
		// Resolve: a direção acertou?
		hit := false
		ret := 0.0
		if pred.Side == "buy" {
			hit = price.GreaterThan(pred.EntryPx)
			if pred.EntryPx.IsPositive() {
				ret = price.Sub(pred.EntryPx).Div(pred.EntryPx).InexactFloat64()
			}
		} else {
			hit = price.LessThan(pred.EntryPx)
			if pred.EntryPx.IsPositive() {
				ret = pred.EntryPx.Sub(price).Div(pred.EntryPx).InexactFloat64()
			}
		}
		pred.Resolved = true
		pred.ExitPx = price
		pred.Hit = &hit
		pred.ReturnPct = ret
		pred.ResolvedAt = time.Now()

		p.results = append(p.results, PredictionResult{
			Side: pred.Side, EntryPx: pred.EntryPx, ExitPx: price,
			Hit: hit, ReturnPct: ret, ResolvedAt: time.Now(),
		})
		if len(p.results) > p.maxResults {
			p.results = p.results[len(p.results)-p.maxResults:]
		}
	}
	p.pending = still
}

// PendingCount devolve quantas previsões ainda aguardam resolução.
func (p *PredictionTracker) PendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// Results devolve a janela de resultados resolvidos (cópia).
func (p *PredictionTracker) Results() []PredictionResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PredictionResult, len(p.results))
	copy(out, p.results)
	return out
}

// HitRate devolve a taxa de acerto observada na janela (0..1). 0 se vazia.
func (p *PredictionTracker) HitRate() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.results) == 0 {
		return 0
	}
	hits := 0
	for _, r := range p.results {
		if r.Hit {
			hits++
		}
	}
	return float64(hits) / float64(len(p.results))
}

// itoa é um mini-formatador de inteiros (evita import extra no hot path).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
