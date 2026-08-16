// types.ts — contratos tipados com o core cosca-trader (Fase 3C). Dinheiro
// sempre chega como string (o core serializa decimal.Decimal como string —
// nunca float no caminho de liquidação).

export type Mode = "observe" | "paper" | "testnet" | "live";

export type Health = {
  ok: boolean;
  service: string;
  mode: Mode;
  port: string;
  events: number;
  first_event?: string;
  last_event?: string;
  subscribers: number;
  trading: boolean;
};

export type Side = "buy" | "sell";

export type OrderType = "market" | "limit" | "stop" | "stop_limit" | "stop_market";

export type OrderStatus =
  | "new"
  | "partially_filled"
  | "filled"
  | "canceled"
  | "rejected"
  | "expired";

export type Order = {
  id: string;
  client_order_id?: string;
  symbol: string;
  exchange: string;
  side: Side;
  type: OrderType;
  price: string;
  stop_price: string;
  quantity: string;
  filled_qty: string;
  avg_fill_price: string;
  status: OrderStatus;
  time_in_force: string;
  created_at: string;
  updated_at: string;
};

export type Position = {
  symbol: string;
  exchange: string;
  side: Side;
  quantity: string;
  avg_entry_price: string;
  mark_price: string;
  realized_pnl: string;
  unrealized_pnl: string;
  unrealized_pnl_pct: string;
  opened_at: string;
  updated_at: string;
};

export type Balance = {
  asset: string;
  free: string;
  locked: string;
};

export type Trade = {
  id: string;
  order_id: string;
  symbol: string;
  exchange: string;
  side: Side;
  price: string;
  quantity: string;
  quote_qty: string;
  fee: string;
  fee_asset: string;
  timestamp: string;
};

export type PaperSummary = {
  mode: string;
  initial_capital: string;
  equity: string;
  total_pnl: string;
  open_orders: number;
  trades: Trade[];
};

// RiskState é o estado da camada de risco (F4) — /risk.
export type RiskState = {
  equity: string;
  peak_equity: string;
  drawdown_pct: string;
  trading_halted: boolean;
  halt_reason?: string;
  open_orders: number;
  orders_last_min: number;
  max_drawdown_pct: string;
  max_exposure_pct: string;
  max_total_exposure_pct: string;
  max_open_orders: number;
};

// ── Fase 5 — laudo científico (estatística de estratégia) ──────────────────

// Stats: métricas estatísticas do backtest.
export type BacktestStats = {
  total_trades: number;
  wins: number;
  losses: number;
  breakeven: number;
  win_rate: number;
  gross_profit: string;
  gross_loss: string;
  net_pnl: string;
  profit_factor: number;
  avg_win: string;
  avg_loss: string;
  expectancy: string;
  fees_paid: string;
  return_pct: number;
  max_drawdown_pct: number;
  max_drawdown_abs: string;
  best_trade: string;
  worst_trade: string;
  sharpe: number;
  sortino: number;
  volatility_pct: number;
  p5: string;
  p25: string;
  p50: string;
  p75: string;
  p95: string;
};

export type MonteCarloResult = {
  simulations: number;
  initial: string;
  p5: string;
  p50: string;
  p95: string;
  prob_of_loss: number;
  prob_ruin: number;
  worst_equity: string;
  best_equity: string;
};

export type SignificanceResult = {
  trials: number;
  observed_pnl: string;
  mean_null_pnl: string;
  p_value: number;
  significant: boolean;
  z_score: number;
};

export type WalkForwardResult = {
  train_bars: number;
  test_bars: number;
  train_pnl: string;
  test_pnl: string;
  test_return_pct: number;
  train_trades: number;
  test_trades: number;
  consistent: boolean;
};

export type EquityPoint = {
  index: number;
  equity: string;
};

export type BacktestReport = {
  strategy: string;
  symbol: string;
  periods: number;
  source: string;
  initial: string;
  final: string;
  trade_count: number;
  stats: BacktestStats;
  equity_curve: EquityPoint[];
  monte_carlo: MonteCarloResult;
  significance: SignificanceResult;
  walk_forward: WalkForwardResult;
};

// StreamEvent é a unidade do SSE /events — o mesmo Event do rastro do core.
export type StreamEvent = {
  id: string;
  type: string;
  timestamp: string;
  source: string;
  payload?: unknown;
  correlation_id?: string;
  causation_id?: string;
  severity: string;
};

// Payloads tipados dos eventos que o painel consome.
export type TickPayload = {
  symbol: string;
  exchange: string;
  price: number;
  quantity: number;
  time: string;
};

export type CandlePayload = {
  symbol: string;
  exchange: string;
  interval: string;
  open_time: string;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
  trades?: number;
  complete: boolean;
};

export type OrderRequest = {
  symbol: string;
  side: Side;
  type: OrderType;
  quantity: string;
  price?: string;
  stop_price?: string;
};

// ── Proteção macro (L287/L288): mercados globais + divergência ─────────────

export type MarketQuote = {
  symbol: string;
  name: string;
  price: number;
  change_5d_pct: number;
  change_10d_pct: number;
  fetched_at: string;
};

export type DivergenceSignal = {
  action: "buy" | "sell" | "hold";
  strength: number;
  reason: string;
  global_change_pct: number;
  crypto_change_pct: number;
  generated_at: string;
};

export type MarketsSnapshot = {
  regime: "risk-on" | "risk-off" | "cautela" | string;
  signal: string;
  quotes: MarketQuote[];
  fetched_at: string;
  divergence: DivergenceSignal;
};

export type ConvergenceState = {
  strategy: string;
  expected_hit: number;
  observed_hit: number;
  resolved: number;
  pending: number;
  z_score: number;
  converging: boolean;
  diverged: boolean;
  last_check: string;
  diverged_at?: string;
  readjustments: number;
  last_reason: string;
};

// Coin — um ativo operável no sidebar (lista estilo TradingView).
export type Coin = {
  symbol: string; // "BTCUSDT"
  base: string; // "BTC"
  name: string; // "Bitcoin"
  price: number;
  change_24h: number; // % variação 24h
  volume_24h: number; // volume 24h em USDT
  market_cap: number; // market cap em USD
};
