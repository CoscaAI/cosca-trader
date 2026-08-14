// Package risk é a camada de controle de risco do COSCA TRADER (Fase 4).
// Protege o capital quando o sistema opera com dinheiro real: exposição por
// símbolo e total, drawdown máximo, limite de ordens abertas e rate limit
// local de ordens. Quando uma regra é violada, novas ordens são bloqueadas e
// eventos RiskBreach/RiskWarning são emitidos para o rastro.
//
// Princípio da casa: a camada de risco NUNCA deixa de proteger — se o
// EquityProvider falhar, o comportamento é fail-closed (trava trading).
package risk

import (
	"errors"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
)

// Erros tipados de regra violada — o chamador pode distinguir a causa.
var (
	ErrExposureExceeded   = errors.New("exposição máxima excedida")
	ErrTotalExposureLimit = errors.New("exposição total da carteira excedida")
	ErrDrawdownBreach     = errors.New("drawdown máximo excedido — trading pausado")
	ErrTooManyOpenOrders  = errors.New("limite de ordens abertas excedido")
	ErrRateLimited        = errors.New("rate limit local de ordens excedido")
	ErrEquityUnavailable  = errors.New("equity indisponível — fail-closed: trading bloqueado")
)

// State é o estado corrente do gerenciador de risco — exposto via GET /risk
// e usado pelo frontend para o painel de risco.
type State struct {
	Equity          decimal.Decimal `json:"equity"`
	PeakEquity      decimal.Decimal `json:"peak_equity"`
	DrawdownPct     decimal.Decimal `json:"drawdown_pct"`
	TradingHalted   bool            `json:"trading_halted"`
	HaltReason      string          `json:"halt_reason,omitempty"`
	OpenOrders      int             `json:"open_orders"`
	ExposurePct     decimal.Decimal `json:"exposure_pct"`
	TotalExposure   decimal.Decimal `json:"total_exposure_pct"`
	OrdersLastMin   int             `json:"orders_last_min"`
	MaxDrawdownPct  decimal.Decimal `json:"max_drawdown_pct"`
	MaxExposurePct  decimal.Decimal `json:"max_exposure_pct"`
	MaxTotalExpoPct decimal.Decimal `json:"max_total_exposure_pct"`
	MaxOpenOrders   int             `json:"max_open_orders"`
}

// Config carrega as regras do gerenciador.
type Config struct {
	// MaxExposurePct é a fração máxima do equity em posição por símbolo
	// (ex.: 0.20 = 20%). Zero desativa.
	MaxExposurePct decimal.Decimal
	// MaxTotalExposurePct é a fração máxima do equity em posição no total
	// da carteira (ex.: 0.50). Zero desativa.
	MaxTotalExposurePct decimal.Decimal
	// MaxDrawdownPct é o drawdown máximo do pico de equity (ex.: 0.10).
	// Ao atingir, trading é pausado (Halt). Zero desativa.
	MaxDrawdownPct decimal.Decimal
	// MaxOpenOrders limita ordens abertas simultâneas. Zero = ilimitado.
	MaxOpenOrders int
	// MaxOrdersPerMinute é o rate limit local de envio de ordens. Zero = ilimitado.
	MaxOrdersPerMinute int
	// EquityProvider devolve o equity corrente da carteira. Obrigatório
	// para as regras de exposição/drawdown. nil = fail-closed (bloqueia).
	EquityProvider func() decimal.Decimal
	// Emit envia eventos de risco (RiskBreach/RiskWarning) ao rastro.
	Emit func(evType string, severity string, payload any)
}

// Manager é o gerenciador de risco thread-safe.
type Manager struct {
	cfg Config

	mu         sync.Mutex
	peakEquity decimal.Decimal
	halted     bool
	haltReason string
	orderTimes []time.Time // janela deslizante de 1 min
	riskWarned bool        // evita emitir RiskWarning a cada equity update
}

// New cria o Manager com as regras dadas.
func New(cfg Config) *Manager {
	m := &Manager{cfg: cfg}
	if cfg.EquityProvider != nil {
		if eq := cfg.EquityProvider(); eq.IsPositive() {
			m.peakEquity = eq
		}
	}
	return m
}

// Check valida UMA ordem candidata contra todas as regras, ANTES do envio.
// Deve ser chamada no topo do PlaceOrder (fail rápido, sem gastar
// client_order_id). notional é o valor estimado da ordem (o OMS o calcula
// via PriceProvider para ordens market; para limit é price×quantity).
// positions é o snapshot atual de posições; openOrders o número de ordens
// abertas correntes.
func (m *Manager) Check(notional decimal.Decimal, positions []domain.Position, openOrders int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Fail-closed: sem equity provider, não há como medir risco → bloqueia.
	if m.cfg.EquityProvider == nil {
		return ErrEquityUnavailable
	}
	if m.halted {
		return errors.Join(ErrDrawdownBreach, errors.New(m.haltReason))
	}

	// Rate limit local de ordens.
	if m.cfg.MaxOrdersPerMinute > 0 {
		now := time.Now()
		cutoff := now.Add(-time.Minute)
		kept := m.orderTimes[:0]
		for _, t := range m.orderTimes {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		m.orderTimes = kept
		if len(m.orderTimes) >= m.cfg.MaxOrdersPerMinute {
			return ErrRateLimited
		}
	}

	equity := m.cfg.EquityProvider()
	if !equity.IsPositive() {
		return errors.Join(ErrEquityUnavailable, errors.New("equity <= 0"))
	}

	// Exposição pós-ordem por símbolo.
	if m.cfg.MaxExposurePct.IsPositive() && notional.IsPositive() {
		symExpo := notional.Div(equity)
		if symExpo.GreaterThan(m.cfg.MaxExposurePct) {
			return errors.Join(ErrExposureExceeded,
				errors.New("exposição "+symExpo.String()+" excede "+m.cfg.MaxExposurePct.String()+" do equity"))
		}
	}

	// Exposição total da carteira (pós-ordem).
	if m.cfg.MaxTotalExposurePct.IsPositive() {
		total := decimal.Zero
		for _, p := range positions {
			if !p.IsOpen() {
				continue
			}
			total = total.Add(p.AvgEntryPrice.Mul(p.Quantity))
		}
		total = total.Add(notional)
		if total.Div(equity).GreaterThan(m.cfg.MaxTotalExposurePct) {
			return ErrTotalExposureLimit
		}
	}

	// Ordens abertas.
	if m.cfg.MaxOpenOrders > 0 && openOrders >= m.cfg.MaxOpenOrders {
		return ErrTooManyOpenOrders
	}

	return nil
}

// OnEquityUpdate atualiza o pico de equity, calcula o drawdown e dispara
// RiskWarning/RiskBreach quando os limiares cruzam. Chame a cada atualização
// de mark/equity (o main alimenta). Devolve o State atualizado.
func (m *Manager) OnEquityUpdate(equity decimal.Decimal) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onEquityUpdateLocked(equity)
}

// OnTick alimenta o gerenciador com o equity corrente do provider (F4):
// chamado a cada MarketTick/PositionUpdated pelo main. Se o provider não
// existir, não faz nada (o Check já bloqueia por fail-closed).
func (m *Manager) OnTick() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.EquityProvider == nil {
		return m.stateLocked(decimal.Zero)
	}
	return m.onEquityUpdateLocked(m.cfg.EquityProvider())
}

func (m *Manager) onEquityUpdateLocked(equity decimal.Decimal) State {
	// Atualiza o pico.
	if equity.GreaterThan(m.peakEquity) {
		m.peakEquity = equity
	}

	st := m.stateLocked(equity)

	// Drawdown: trava trading quando o limite é atingido.
	if m.cfg.MaxDrawdownPct.IsPositive() && st.DrawdownPct.GreaterThanOrEqual(m.cfg.MaxDrawdownPct) {
		if !m.halted {
			m.halted = true
			m.haltReason = "drawdown " + st.DrawdownPct.StringFixed(2) + " ≥ limite " + m.cfg.MaxDrawdownPct.StringFixed(2)
			m.emit("risk.breach", "critical", map[string]any{
				"rule":         "max_drawdown",
				"drawdown_pct": st.DrawdownPct.String(),
				"limit":        m.cfg.MaxDrawdownPct.String(),
				"equity":       equity.String(),
				"peak":         m.peakEquity.String(),
			})
		}
		st.TradingHalted = true
		st.HaltReason = m.haltReason
		return st
	}

	// Warning em 50% do limite de drawdown (uma vez, até reverter).
	if m.cfg.MaxDrawdownPct.IsPositive() {
		half := m.cfg.MaxDrawdownPct.Div(decimal.NewFromInt(2))
		if st.DrawdownPct.GreaterThanOrEqual(half) && !m.riskWarned {
			m.riskWarned = true
			m.emit("risk.warning", "warning", map[string]any{
				"rule":         "drawdown_approaching",
				"drawdown_pct": st.DrawdownPct.String(),
				"limit":        m.cfg.MaxDrawdownPct.String(),
			})
		} else if st.DrawdownPct.LessThan(half) {
			m.riskWarned = false
		}
	}

	return st
}

// RegisterOrder marca uma ordem enviada (para o rate limit local).
func (m *Manager) RegisterOrder() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orderTimes = append(m.orderTimes, time.Now())
}

// Halt trava manualmente o trading.
func (m *Manager) Halt(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.halted = true
	m.haltReason = reason
}

// Resume destrava o trading (reset manual após breach).
func (m *Manager) Resume() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.halted = false
	m.haltReason = ""
	m.riskWarned = false
}

// State devolve o estado corrente.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	equity := decimal.Zero
	if m.cfg.EquityProvider != nil {
		equity = m.cfg.EquityProvider()
	}
	return m.stateLocked(equity)
}

// stateLocked monta o State com base no equity corrente. O drawdown e as
// exposições são sempre calculados aqui para o /risk estar fresco.
func (m *Manager) stateLocked(equity decimal.Decimal) State {
	st := State{
		Equity:          equity,
		PeakEquity:      m.peakEquity,
		MaxDrawdownPct:  m.cfg.MaxDrawdownPct,
		MaxExposurePct:  m.cfg.MaxExposurePct,
		MaxTotalExpoPct: m.cfg.MaxTotalExposurePct,
		MaxOpenOrders:   m.cfg.MaxOpenOrders,
		OrdersLastMin:   len(m.orderTimes),
		TradingHalted:   m.halted,
		HaltReason:      m.haltReason,
	}
	if equity.IsPositive() && m.peakEquity.IsPositive() {
		dd := m.peakEquity.Sub(equity).Div(m.peakEquity)
		st.DrawdownPct = dd
	}
	return st
}

func (m *Manager) emit(evType, severity string, payload any) {
	if m.cfg.Emit != nil {
		m.cfg.Emit(evType, severity, payload)
	}
}
