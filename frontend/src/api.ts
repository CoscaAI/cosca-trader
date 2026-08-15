// api.ts — cliente tipado do core cosca-trader (Fase 3C). Todas as chamadas
// passam pelo proxy do Vite (resolve o CORS fechado do core) e, quando há
// token configurado, enviam Authorization: Bearer.

import type {
  BacktestReport,
  Balance,
  CandlePayload,
  ConvergenceState,
  Health,
  MarketsSnapshot,
  Order,
  OrderRequest,
  PaperSummary,
  Position,
  RiskState,
  StreamEvent,
} from "./types";

// UnavailableError: o endpoint existe mas o recurso NÃO está ativo no modo
// atual (ex.: /convergence sem --shadow → 503). O painel deve DESISTIR de
// re-tentar neste recurso (não martelar o servidor a cada polling), em vez
// de tratá-lo como erro transitório.
export class UnavailableError extends Error {
  status: number;
  constructor(status: number, path: string) {
    super(`${path} ${status} (recurso inativo)`);
    this.name = "UnavailableError";
    this.status = status;
  }
}

export class CoreClient {
  constructor(private token: string) {}

  // authHeaders monta os headers comuns. Token vazio = sem Authorization
  // (os endpoints sensíveis respondem 401 — a UI deixa claro o estado).
  private authHeaders(): Record<string, string> {
    const h: Record<string, string> = { "Content-Type": "application/json" };
    if (this.token) h.Authorization = `Bearer ${this.token}`;
    return h;
  }

  async health(): Promise<Health> {
    const r = await fetch("/health");
    if (!r.ok) throw new Error(`/health ${r.status}`);
    return (await r.json()) as Health;
  }

  // ticker busca o preço corrente de um símbolo (Binance REST público — sem
  // auth, funciona mesmo sem token configurado).
  async ticker(symbol: string): Promise<{ symbol: string; price: string }> {
    const r = await fetch(`/ticker?symbol=${encodeURIComponent(symbol)}`);
    if (!r.ok) throw new Error(`/ticker ${r.status}`);
    return (await r.json()) as { symbol: string; price: string };
  }

  async positions(): Promise<Position[]> {
    return this.get<Position[]>("/positions");
  }

  async balances(): Promise<Balance[]> {
    return this.get<Balance[]>("/balances");
  }

  async orders(): Promise<Order[]> {
    return this.get<Order[]>("/orders");
  }

  async paper(): Promise<PaperSummary> {
    return this.get<PaperSummary>("/paper");
  }

  async risk(): Promise<RiskState> {
    return this.get<RiskState>("/risk");
  }

  async markets(): Promise<MarketsSnapshot> {
    return this.get<MarketsSnapshot>("/markets");
  }

  async convergence(): Promise<ConvergenceState> {
    return this.get<ConvergenceState>("/convergence");
  }

  async candles(symbol: string, interval: string, bars: number): Promise<CandlePayload[]> {
    const r = await fetch(
      `/candles?symbol=${encodeURIComponent(symbol)}&interval=${encodeURIComponent(interval)}&bars=${bars}`,
      { headers: this.authHeaders() },
    );
    if (!r.ok) throw new Error(`/candles ${r.status}`);
    const d = (await r.json()) as { candles: CandlePayload[] };
    return d.candles ?? [];
  }

  async backtest(symbol: string): Promise<BacktestReport> {
    return this.get<BacktestReport>(`/backtest?symbol=${encodeURIComponent(symbol)}`);
  }

  async placeOrder(body: OrderRequest): Promise<Order> {
    const r = await fetch("/orders", {
      method: "POST",
      headers: this.authHeaders(),
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error(await r.text());
    return (await r.json()) as Order;
  }

  private async get<T>(path: string): Promise<T> {
    const r = await fetch(path, { headers: this.authHeaders() });
    if (r.status === 503) throw new UnavailableError(503, path);
    if (!r.ok) throw new Error(`${path} ${r.status}`);
    return (await r.json()) as T;
  }
}

// streamEvents conecta ao SSE /events via fetch (EventSource do browser não
// envia headers de Authorization) e entrega cada evento decodificado. Reconecta
// automaticamente enquanto o AbortSignal não for disparado.
export async function streamEvents(
  token: string,
  onEvent: (ev: StreamEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const res = await fetch("/events", {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    signal,
  });
  if (!res.ok || !res.body) {
    throw new Error(`/events ${res.status}`);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let idx: number;
    while ((idx = buf.indexOf("\n\n")) >= 0) {
      const frame = buf.slice(0, idx);
      buf = buf.slice(idx + 2);
      const data = frame
        .split("\n")
        .filter((l) => l.startsWith("data:"))
        .map((l) => l.slice(5).trim())
        .join("\n");
      if (!data) continue;
      try {
        onEvent(JSON.parse(data) as StreamEvent);
      } catch {
        // frame malformado: ignora e segue (nunca derruba o painel)
      }
    }
  }
}
