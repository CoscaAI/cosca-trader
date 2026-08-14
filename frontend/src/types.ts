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
