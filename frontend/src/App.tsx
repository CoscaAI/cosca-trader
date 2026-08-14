// App.tsx — painel de trading do COSCA TRADER (Fase 3C): gráfico candlestick
// ao vivo (SSE /events), posições com PnL não realizado, saldos, envio de
// ordens e timeline compacta. Tudo falado com o core headless via proxy do
// Vite; o token Bearer é configurado na topbar e persistido no localStorage.

import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import GridLayout from "react-grid-layout";
import { WidthProvider } from "react-grid-layout/legacy";

// O @types do react-grid-layout está defasado (não reconhece props). O
// GridLayout padrão (ESM) NÃO exporta WidthProvider — o subpath /legacy tem.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const Grid = WidthProvider(GridLayout) as React.ComponentType<any>;

// Shape de um item do layout (x/y/w/h + mínimos) — o array do painel.
type GridLayoutItem = {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
  minW?: number;
  minH?: number;
};
import {
  CandlestickSeries,
  ColorType,
  CrosshairMode,
  createChart,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
} from "lightweight-charts";

import { CoreClient, streamEvents, UnavailableError } from "./api";
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
  TickPayload,
} from "./types";

const TOKEN_KEY = "cosca_trader_token";
const TIMELINE_MAX = 40;
const CANDLE_MAX = 500;
const LAYOUT_KEY = "cosca_trader_layout";

// Layout default do painel (grid de 12 colunas, linhas de 30px). O Don pode
// arrastar e redimensionar cada card — o layout fica salvo no navegador.
const DEFAULT_LAYOUT: GridLayoutItem[] = [
  { i: "chart", x: 0, y: 0, w: 8, h: 14, minW: 5, minH: 10 },
  { i: "positions", x: 8, y: 0, w: 4, h: 8, minW: 3, minH: 5 },
  { i: "balances", x: 8, y: 8, w: 4, h: 7, minW: 3, minH: 4 },
  { i: "science", x: 0, y: 14, w: 4, h: 12, minW: 3, minH: 8 },
  { i: "markets", x: 4, y: 14, w: 4, h: 12, minW: 3, minH: 8 },
  { i: "convergence", x: 8, y: 15, w: 4, h: 9, minW: 3, minH: 6 },
  { i: "paper", x: 8, y: 24, w: 4, h: 6, minW: 3, minH: 4 },
  { i: "risk", x: 0, y: 26, w: 4, h: 7, minW: 3, minH: 5 },
  { i: "orders", x: 4, y: 26, w: 4, h: 10, minW: 3, minH: 6 },
  { i: "timeline", x: 8, y: 30, w: 4, h: 8, minW: 3, minH: 5 },
];

// Carrega o layout salvo (ou o default) do localStorage.
function loadLayout(): GridLayoutItem[] {
  try {
    const raw = localStorage.getItem(LAYOUT_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as GridLayoutItem[];
      if (Array.isArray(parsed) && parsed.length > 0) return parsed;
    }
  } catch {
    /* layout corrompido → default */
  }
  return DEFAULT_LAYOUT;
}

// Timeframes do gráfico (o Don pediu a lista completa). 1s não tem histórico
// na Binance (mínimo 1m) — fica no seletor para o fluxo ao vivo do SSE.
const TF_OPTIONS = ["1m", "5m", "10m", "15m", "30m", "1h", "2h", "4h", "1d", "1w", "1M"];

// ── formatação ──────────────────────────────────────────────────────────────

function fmt(v: string | number | undefined | null, digits = 2): string {
  if (v === undefined || v === null || v === "") return "—";
  const n = typeof v === "number" ? v : Number(v);
  // Infinity/NaN derrubavam o painel (maximumFractionDigits out of range).
  if (!Number.isFinite(n)) return "—";
  const d = Math.min(Math.max(Math.trunc(digits), 0), 20);
  return n.toLocaleString("pt-BR", { maximumFractionDigits: d });
}

function fmtMoney(v: string | number | undefined | null, digits = 2): string {
  if (v === undefined || v === null || v === "") return "—";
  const n = typeof v === "number" ? v : Number(v);
  // Valida finitude (NaN OU Infinity): um tick malformado derrubava o painel
  // com "maximumFractionDigits value is out of range" no toLocaleString.
  if (!Number.isFinite(n)) return "—";
  // Garante dígitos válidos (inteiro 0-20; o toLocaleString estoura fora disso).
  const d = Math.min(Math.max(Math.trunc(digits), 0), 20);
  const sign = n < 0 ? "-" : "";
  return `${sign}$${Math.abs(n).toLocaleString("pt-BR", {
    minimumFractionDigits: d,
    maximumFractionDigits: d,
  })}`;
}

function pnlClass(v: string | number | undefined | null): string {
  const n = typeof v === "number" ? v : Number(v ?? 0);
  return n > 0 ? "pos" : n < 0 ? "neg" : "";
}

function fmtTime(iso: string | undefined | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString("pt-BR", { hour12: false });
}

// ── estado do core ──────────────────────────────────────────────────────────

function useHealth() {
  const [health, setHealth] = useState<Health | null>(null);
  const [healthErr, setHealthErr] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    const poll = () => {
      fetch("/health")
        .then((r) => r.json())
        .then((h: Health) => alive && setHealth(h))
        .catch(() => alive && setHealthErr("core offline"));
    };
    poll();
    const t = setInterval(poll, 3000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);
  return { health, healthErr };
}

// ── gráfico candlestick (lightweight-charts) ────────────────────────────────

function CandlestickChart({
  candles,
  lastPrice,
  timeframe,
}: {
  candles: CandlePayload[];
  lastPrice: TickPayload | null;
  timeframe: string;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);

  useEffect(() => {
    if (!ref.current) return;
    const chart = createChart(ref.current, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: "transparent" },
        textColor: "#8b96a5",
      },
      grid: {
        vertLines: { color: "#1e2636" },
        horzLines: { color: "#1e2636" },
      },
      rightPriceScale: { borderColor: "#1e2636" },
      timeScale: {
        borderColor: "#1e2636",
        timeVisible: true,
        secondsVisible: false,
        // formato de data/hora visível e legível (o Don pediu data e hora)
        tickMarkFormatter: (time: UTCTimestamp) => {
          const d = new Date((time as number) * 1000);
          const hh = String(d.getHours()).padStart(2, "0");
          const mm = String(d.getMinutes()).padStart(2, "0");
          const dd = String(d.getDate()).padStart(2, "0");
          const mo = String(d.getMonth() + 1).padStart(2, "0");
          // timeframes curtos (≤4h) mostram hora; longos (≥1d) mostram data
          if (timeframe && ["1d", "1w", "1M"].includes(timeframe)) {
            return `${dd}/${mo}`;
          }
          return `${dd}/${mo} ${hh}:${mm}`;
        },
      },
      crosshair: { mode: CrosshairMode.Normal },
    });
    const series = chart.addSeries(CandlestickSeries, {
      upColor: "#22c55e",
      downColor: "#ef4444",
      borderUpColor: "#22c55e",
      borderDownColor: "#ef4444",
      wickUpColor: "#22c55e",
      wickDownColor: "#ef4444",
    });
    chartRef.current = chart;
    seriesRef.current = series;
    return () => {
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
    };
  }, [timeframe]);

  // velas fechadas: redesenha o histórico
  useEffect(() => {
    const series = seriesRef.current;
    if (!series || candles.length === 0) return;
    series.setData(
      candles.map((c) => ({
        time: Math.floor(Date.parse(c.open_time) / 1000) as UTCTimestamp,
        open: c.open,
        high: c.high,
        low: c.low,
        close: c.close,
      })),
    );
  }, [candles]);

  // tick ao vivo: reflete o último preço na vela em formação
  useEffect(() => {
    const series = seriesRef.current;
    if (!series || !lastPrice || candles.length === 0) return;
    const last = candles[candles.length - 1];
    series.update({
      time: Math.floor(Date.parse(last.open_time) / 1000) as UTCTimestamp,
      open: last.open,
      high: Math.max(last.high, lastPrice.price),
      low: Math.min(last.low, lastPrice.price),
      close: lastPrice.price,
    });
  }, [lastPrice, candles]);

  return <div ref={ref} className="chart" />;
}

// ── componentes de painel ───────────────────────────────────────────────────

function ModeBadge({ mode, hasToken }: { mode?: string; hasToken: boolean }) {
  const label = (mode ?? "observe").toUpperCase();
  return (
    <span className={`badge mode-${mode ?? "observe"}`}>
      {label}
      {!hasToken && <span className="hint">· sem token</span>}
    </span>
  );
}

function PositionsTable({ positions }: { positions: Position[] }) {
  if (positions.length === 0) {
    return <p className="empty">sem posições abertas</p>;
  }
  return (
    <table className="data">
      <thead>
        <tr>
          <th>símbolo</th>
          <th>lado</th>
          <th>qty</th>
          <th>entrada</th>
          <th>mark</th>
          <th>PnL não realizado</th>
          <th>%</th>
        </tr>
      </thead>
      <tbody>
        {positions.map((p) => (
          <tr key={`${p.exchange}:${p.symbol}`}>
            <td>{p.symbol}</td>
            <td className={`side-${p.side}`}>{p.side}</td>
            <td>{fmt(p.quantity, 6)}</td>
            <td>{fmtMoney(p.avg_entry_price, 1)}</td>
            <td>{fmtMoney(p.mark_price, 1)}</td>
            <td className={pnlClass(p.unrealized_pnl)}>{fmtMoney(p.unrealized_pnl)}</td>
            <td className={pnlClass(p.unrealized_pnl_pct)}>{fmt(p.unrealized_pnl_pct, 2)}%</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function BalancesTable({ balances }: { balances: Balance[] }) {
  if (balances.length === 0) {
    return <p className="empty">sem saldos</p>;
  }
  return (
    <table className="data">
      <thead>
        <tr>
          <th>ativo</th>
          <th>livre</th>
          <th>travado</th>
        </tr>
      </thead>
      <tbody>
        {balances.map((b) => (
          <tr key={b.asset}>
            <td>{b.asset}</td>
            <td>{fmt(b.free, 6)}</td>
            <td>{fmt(b.locked, 6)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function PaperCard({ paper }: { paper: PaperSummary | null }) {
  if (!paper) {
    return <p className="empty">modo paper não ativo (rode o core com --paper)</p>;
  }
  return (
    <div className="metrics-grid">
      <div className="metric">
        <span className="k">capital inicial</span>
        <span className="v">{fmtMoney(paper.initial_capital)}</span>
      </div>
      <div className="metric">
        <span className="k">equity</span>
        <span className="v">{fmtMoney(paper.equity)}</span>
      </div>
      <div className="metric">
        <span className="k">PnL total</span>
        <span className={`v ${pnlClass(paper.total_pnl)}`}>{fmtMoney(paper.total_pnl)}</span>
      </div>
      <div className="metric">
        <span className="k">ordens abertas</span>
        <span className="v">{paper.open_orders}</span>
      </div>
    </div>
  );
}

function RiskCard({ risk }: { risk: RiskState | null }) {
  if (!risk) {
    return <p className="empty">camada de risco não ativa</p>;
  }
  const dd = pct(risk.drawdown_pct);
  const maxDd = pct(risk.max_drawdown_pct);
  const ddWarn = dd >= maxDd * 0.5;
  return (
    <div className="metrics-grid">
      {risk.trading_halted && (
        <div className="risk-banner">
          ⛔ TRADING PAUSADO — {risk.halt_reason ?? "regra de risco"}
        </div>
      )}
      <div className="metric">
        <span className="k">drawdown</span>
        <span className={`v ${ddWarn ? "warn" : ""}`}>
          {fmtPct(dd)} <small>de {fmtPct(maxDd)}</small>
        </span>
      </div>
      <div className="metric">
        <span className="k">exposição máx/símbolo</span>
        <span className="v">{fmtPct(pct(risk.max_exposure_pct))}</span>
      </div>
      <div className="metric">
        <span className="k">exposição máx total</span>
        <span className="v">{fmtPct(pct(risk.max_total_exposure_pct))}</span>
      </div>
      <div className="metric">
        <span className="k">ordens/min</span>
        <span className="v">
          {risk.orders_last_min}
          <small> máx {risk.max_open_orders} abertas</small>
        </span>
      </div>
    </div>
  );
}

// fmtPct formata uma fração decimal (0.10) como percentual (10%).
function fmtPct(f: number): string {
  return `${(f * 100).toFixed(1)}%`;
}

// pct converte string decimal do core ("0.10") em número.
function pct(s: string): number {
  const n = parseFloat(s);
  return Number.isFinite(n) ? n : 0;
}

// ScienceCard — o laudo científico (F5): a ferramenta de probabilidade.
// Mostra win rate, profit factor, Sharpe, Monte Carlo e o veredito de
// significância — para o Don NUNCA levar estratégia sem vantagem pra frente.
function ScienceCard({ report }: { report: BacktestReport | null }) {
  if (!report) {
    return <p className="empty">laudo científico indisponível</p>;
  }
  const st = report.stats;
  const mc = report.monte_carlo;
  const sig = report.significance;
  const wf = report.walk_forward;

  const sigClass = sig.significant ? "pos" : "neg";
  const wfClass = wf.consistent ? "pos" : "neg";
  const pnlClassV = st.net_pnl.startsWith("-") ? "neg" : "pos";

  return (
    <div className="science">
      <div className="science-verdict">
        <span className={`v ${sigClass}`}>
          {sig.significant ? "VANTAGEM REAL" : "SEM VANTAGEM"}
        </span>
        <span className="k">
          p-value {sig.p_value.toFixed(3)} · {st.total_trades} trades ·{" "}
          {fmtPct(st.win_rate)} win rate
        </span>
      </div>
      <div className="metrics-grid">
        <div className="metric">
          <span className="k">PnL líquido</span>
          <span className={`v ${pnlClassV}`}>{fmtMoney(st.net_pnl)}</span>
        </div>
        <div className="metric">
          <span className="k">profit factor</span>
          <span className="v">{st.profit_factor.toFixed(2)}</span>
        </div>
        <div className="metric">
          <span className="k">expectância/trade</span>
          <span className={`v ${st.expectancy.startsWith("-") ? "neg" : "pos"}`}>
            {fmtMoney(st.expectancy)}
          </span>
        </div>
        <div className="metric">
          <span className="k">Sharpe</span>
          <span className={`v ${st.sharpe < 1 ? "neg" : "pos"}`}>{st.sharpe.toFixed(2)}</span>
        </div>
        <div className="metric">
          <span className="k">max drawdown</span>
          <span className="v">{fmtPct(st.max_drawdown_pct)}</span>
        </div>
        <div className="metric">
          <span className="k">P(perder) MC</span>
          <span className={`v ${mc.prob_of_loss > 0.5 ? "neg" : "pos"}`}>
            {fmtPct(mc.prob_of_loss)}
          </span>
        </div>
      </div>
      <div className="science-line">
        <span className="k">Monte Carlo ({mc.simulations} sims):</span>{" "}
        <span className="v small">
          P5 {fmtMoney(mc.p5)} · P50 {fmtMoney(mc.p50)} · P95 {fmtMoney(mc.p95)}
        </span>
      </div>
      <div className="science-line">
        <span className="k">Walk-forward:</span>{" "}
        <span className={`v small ${wfClass}`}>
          {wf.consistent ? "consistente (lucrou out-of-sample)" : "inconsistente"} — teste{" "}
          {fmtMoney(wf.test_pnl)} em {wf.test_bars} velas
        </span>      </div>
      <div className="science-line">
        <span className="k">Distribuição P5/P50/P95:</span>{" "}
        <span className="v small">
          {fmtMoney(st.p5)} / {fmtMoney(st.p50)} / {fmtMoney(st.p95)}
        </span>
      </div>
    </div>
  );
}

// MarketsCard — o radar macro (L287/L288): regime global risk-on/off, cotações
// de S&P/NASDAQ/VIX/ouro/dólar e a divergência com o cripto (sinal de entrada
// seguindo a tendência global).
function MarketsCard({ markets }: { markets: MarketsSnapshot | null }) {
  if (!markets) {
    return <p className="empty">radar global indisponível (rode o core com acesso à rede)</p>;
  }
  const div = markets.divergence;
  const regimeCls =
    markets.regime === "risk-off" ? "neg" : markets.regime === "risk-on" ? "pos" : "warn";
  const actCls = div.action === "buy" ? "pos" : div.action === "sell" ? "neg" : "";
  return (
    <div className="markets">
      <div className="science-verdict">
        <span className={`v ${regimeCls}`}>
          {markets.regime === "risk-on" ? "RISK-ON" : markets.regime === "risk-off" ? "RISK-OFF" : markets.regime}
        </span>
        <span className="k">{markets.signal}</span>
      </div>
      <table className="mini-table">
        <thead>
          <tr>
            <th>Ativo</th>
            <th>Preço</th>
            <th>5d</th>
            <th>10d</th>
          </tr>
        </thead>
        <tbody>
          {markets.quotes.map((q) => (
            <tr key={q.symbol}>
              <td>{q.name}</td>
              <td className="num">{fmtMoney(q.price, 2)}</td>
              <td className={`num ${q.change_5d_pct >= 0 ? "pos" : "neg"}`}>
                {q.change_5d_pct.toFixed(2)}%
              </td>
              <td className={`num ${q.change_10d_pct >= 0 ? "pos" : "neg"}`}>
                {q.change_10d_pct.toFixed(2)}%
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {div.action !== "hold" && (
        <div className={`science-line div-signal ${actCls}`}>
          <span className="v">
            {div.action === "buy" ? "🟢 SINAL DE ALTA" : "🔴 SINAL DE QUEDA"}
          </span>{" "}
          <span className="k">força {div.strength.toFixed(2)}</span>
          <p>{div.reason}</p>
        </div>
      )}
      {div.action === "hold" && (
        <p className="empty">{div.reason}</p>
      )}
    </div>
  );
}

// ConvergenceCard — o laboratório vivo (L281): a previsão da estratégia está
// convergindo com o mercado? z-score, acerto observado vs esperado, divergência.
function ConvergenceCard({ conv }: { conv: ConvergenceState | null }) {
  if (!conv) {
    return <p className="empty">laboratório vivo inativo (rode o core com --shadow)</p>;
  }
  const cls = conv.diverged ? "neg" : "pos";
  return (
    <div className="convergence">
      <div className="science-verdict">
        <span className={`v ${cls}`}>
          {conv.diverged ? "⚠ DIVERGENTE — regime mudou" : "CONVERGINDO"}
        </span>
        <span className="k">
          {conv.strategy} · z={conv.z_score.toFixed(2)} · {conv.resolved} previsões
        </span>
      </div>
      <div className="metrics-grid">
        <div className="metric">
          <span className="k">acerto observado</span>
          <span className="v">{(conv.observed_hit * 100).toFixed(0)}%</span>
        </div>
        <div className="metric">
          <span className="k">acerto esperado</span>
          <span className="v">{(conv.expected_hit * 100).toFixed(0)}%</span>
        </div>
        <div className="metric">
          <span className="k">pendentes</span>
          <span className="v">{conv.pending}</span>
        </div>
        <div className="metric">
          <span className="k">reajustes</span>
          <span className="v">{conv.readjustments}</span>
        </div>
      </div>
      <p className="empty">{conv.last_reason}</p>
    </div>
  );
}

function OrdersPanel({
  client,
  orders,
  onSubmitted,
}: {
  client: CoreClient;
  orders: Order[];
  onSubmitted: () => void;
}) {
  const [form, setForm] = useState<OrderRequest>({
    symbol: "BTCUSDT",
    side: "buy",
    type: "market",
    quantity: "0.01",
  });
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const set = (k: keyof OrderRequest, v: string) => setForm((f) => ({ ...f, [k]: v }));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      await client.placeOrder(form);
      onSubmitted();
    } catch (err) {
      setErr(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <form className="order-form" onSubmit={submit}>
        <div className="row">
          <label>
            símbolo
            <input
              value={form.symbol}
              onChange={(e) => set("symbol", e.target.value.toUpperCase())}
              placeholder="BTCUSDT"
            />
          </label>
          <label>
            lado
            <select value={form.side} onChange={(e) => set("side", e.target.value)}>
              <option value="buy">comprar</option>
              <option value="sell">vender</option>
            </select>
          </label>
        </div>
        <div className="row">
          <label>
            tipo
            <select value={form.type} onChange={(e) => set("type", e.target.value)}>
              <option value="market">market</option>
              <option value="limit">limit</option>
            </select>
          </label>
          <label>
            quantidade
            <input
              value={form.quantity}
              onChange={(e) => set("quantity", e.target.value)}
              placeholder="0.01"
              inputMode="decimal"
            />
          </label>
        </div>
        {form.type === "limit" && (
          <div className="row">
            <label>
              preço (limit)
              <input
                value={form.price ?? ""}
                onChange={(e) => set("price", e.target.value)}
                placeholder="60000"
                inputMode="decimal"
              />
            </label>
          </div>
        )}
        <button className="btn primary" disabled={busy} type="submit">
          {busy ? "enviando…" : "enviar ordem"}
        </button>
        {err && <p className="form-error">{err}</p>}
      </form>

      <div className="table-wrap">
        <table className="data compact">
          <thead>
            <tr>
              <th>id</th>
              <th>símbolo</th>
              <th>lado</th>
              <th>tipo</th>
              <th>qty</th>
              <th>preço</th>
              <th>status</th>
            </tr>
          </thead>
          <tbody>
            {[...orders].reverse().slice(0, 8).map((o) => (
              <tr key={o.id}>
                <td className="mono">{o.id}</td>
                <td>{o.symbol}</td>
                <td className={`side-${o.side}`}>{o.side}</td>
                <td>{o.type}</td>
                <td>{fmt(o.quantity, 6)}</td>
                <td>{fmtMoney(o.price || o.avg_fill_price, 1)}</td>
                <td>
                  <span className={`badge st-${o.status}`}>{o.status}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

function Timeline({ events }: { events: StreamEvent[] }) {
  if (events.length === 0) {
    return <p className="empty">aguardando eventos (SSE)…</p>;
  }
  return (
    <ul className="timeline">
      {[...events].reverse().map((ev) => (
        <li key={ev.id}>
          <span className={`dot t-${ev.severity}`} />
          <code>{fmtTime(ev.timestamp)}</code>
          <span className="t-type">{ev.type}</span>
          <span className="t-src">{ev.source}</span>
        </li>
      ))}
    </ul>
  );
}

// ── App ─────────────────────────────────────────────────────────────────────

export default function App() {
  const { health, healthErr } = useHealth();
  const [token, setToken] = useState<string>(
    () => localStorage.getItem(TOKEN_KEY) ?? "",
  );
  const [positions, setPositions] = useState<Position[]>([]);
  const [balances, setBalances] = useState<Balance[]>([]);
  const [orders, setOrders] = useState<Order[]>([]);
  const [paper, setPaper] = useState<PaperSummary | null>(null);
  const [risk, setRisk] = useState<RiskState | null>(null);
  const [science, setScience] = useState<BacktestReport | null>(null);
  const [markets, setMarkets] = useState<MarketsSnapshot | null>(null);
  const [convergence, setConvergence] = useState<ConvergenceState | null>(null);
  const [candles, setCandles] = useState<CandlePayload[]>([]);
  const [lastPrice, setLastPrice] = useState<TickPayload | null>(null);
  const [timeline, setTimeline] = useState<StreamEvent[]>([]);
  const [timeframe, setTimeframe] = useState<string>("1h");
  const [chartSymbol, setChartSymbol] = useState<string>("BTCUSDT");
  const [layout, setLayout] = useState<GridLayoutItem[]>(() => loadLayout());

  // Persiste o layout quando o Don reorganiza/redimensiona o painel.
  // Nota: o @types do react-grid-layout tipa onLayoutChange como (layout) =>
  // void com "Layout" sendo o ITEM — mas o runtime entrega o ARRAY completo.
  // Usamos GridLayoutItem[] (o shape real) e alinhamos com as any na prop.
  const onLayoutChange = useCallback((next: GridLayoutItem[]) => {
    setLayout(next);
    try {
      localStorage.setItem(LAYOUT_KEY, JSON.stringify(next));
    } catch {
      /* storage cheio — layout só em memória */
    }
  }, []);

  const resetLayout = useCallback(() => {
    setLayout(DEFAULT_LAYOUT);
    try {
      localStorage.removeItem(LAYOUT_KEY);
    } catch {
      /* */
    }
  }, []);

  // Refs de "recurso inativo": após um 503 (convergence sem --shadow, etc.),
  // o painel DESISTE de re-tentar no polling — não martela o servidor.
  const convGivenUp = useRef(false);
  const mktGivenUp = useRef(false);

  const client = useMemo(() => new CoreClient(token), [token]);

  const onTokenChange = (v: string) => {
    setToken(v);
    localStorage.setItem(TOKEN_KEY, v);
  };

  // refresh: carrega posições/saldos/ordens/paper do core
  const refresh = useCallback(async () => {
    if (!token) return;
    try {
      setPositions(await client.positions());
    } catch {
      /* painel segue com o último estado */
    }
    try {
      setBalances(await client.balances());
    } catch {
      /* */
    }
    try {
      setOrders(await client.orders());
    } catch {
      /* */
    }
    try {
      const pp = await client.paper();
      setPaper(pp.mode === "paper" ? pp : null);
    } catch {
      setPaper(null);
    }
    try {
      setRisk(await client.risk());
    } catch {
      setRisk(null);
    }
    try {
      setScience(await client.backtest("BTCUSDT"));
    } catch {
      setScience(null);
    }
    try {
      if (!convGivenUp.current) {
        setConvergence(await client.convergence());
      }
    } catch (err) {
      if (err instanceof UnavailableError) convGivenUp.current = true;
      setConvergence(null);
    }
    try {
      if (!mktGivenUp.current) {
        setMarkets(await client.markets());
      }
    } catch (err) {
      if (err instanceof UnavailableError) mktGivenUp.current = true;
      setMarkets(null);
    }
  }, [client, token]);

  // Histórico do gráfico: carrega UMA vez ao montar / mudar token (não no
  // polling — a Binance é consultada 1x; o SSE alimenta os candles por cima).
  useEffect(() => {
    if (!token) return;
    let alive = true;
    (async () => {
      try {
        const hist = await client.candles(chartSymbol, timeframe, 300);
        if (alive && hist.length > 0) setCandles(hist);
      } catch {
        /* offline: o gráfico usa só os candles do SSE */
      }
    })();
    return () => {
      alive = false;
    };
  }, [client, token, chartSymbol, timeframe]);

  const refreshRef = useRef(refresh);
  useEffect(() => {
    refreshRef.current = refresh;
  }, [refresh]);

  // polling: dados das tabelas a cada 3s (fallback robusto)
  useEffect(() => {
    if (!token) return;
    refresh();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, [refresh, token]);

  // SSE /events: candles + tick ao vivo + timeline + refresh incremental
  useEffect(() => {
    if (!token) return;
    const ac = new AbortController();
    let stopped = false;
    let timer: number | undefined;

    const onEvent = (ev: StreamEvent) => {
      if (ev.type === "market.tick") {
        const t = ev.payload as TickPayload | null;
        if (t && typeof t?.price === "number") setLastPrice(t);
        return;
      }
      if (ev.type === "market.candle_closed") {
        const c = ev.payload as CandlePayload | null;
        if (c && typeof c?.open === "number") {
          setCandles((prev) =>
            prev.length && prev[prev.length - 1].open_time === c.open_time
              ? prev
              : [...prev, c].slice(-CANDLE_MAX),
          );
        }
        return;
      }
      const relevant =
        ev.type.startsWith("order.") ||
        ev.type.startsWith("trade.") ||
        ev.type.startsWith("position.") ||
        ev.type.startsWith("balance.") ||
        ev.type.startsWith("strategy.");
      if (relevant) {
        setTimeline((prev) => [...prev, ev].slice(-TIMELINE_MAX));
        if (timer !== undefined) window.clearTimeout(timer);
        timer = window.setTimeout(() => refreshRef.current(), 300);
      }
    };

    const run = async () => {
      while (!stopped) {
        try {
          await streamEvents(token, onEvent, ac.signal);
        } catch {
          // conexão caiu: reconecta após pausa (nunca derruba o painel)
        }
        if (stopped) break;
        await new Promise((r) => setTimeout(r, 1500));
      }
    };
    run();

    return () => {
      stopped = true;
      ac.abort();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [token]);

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span className="logo">◆</span>
          <h1>COSCA&nbsp;TRADER</h1>
          <span className="tag">F3 — painel de trading</span>
          <ModeBadge mode={health?.mode} hasToken={token !== ""} />
        </div>
        <div className="topbar-right">
          <input
            className="token-input"
            type="password"
            placeholder="token (COSCA_TRADER_TOKEN)"
            value={token}
            onChange={(e) => onTokenChange(e.target.value)}
            autoComplete="off"
          />
          <div className={`status ${health?.ok ? "ok" : "off"}`}>
            <span className="dot" />
            {health?.ok ? "core online" : healthErr ?? "conectando…"}
          </div>
          <button className="btn reset-layout" onClick={resetLayout} title="restaurar layout">
            ⟲ layout
          </button>
        </div>
      </header>

      <main className="grid-main">
        <Grid
          className="layout"
          layout={layout}
          cols={12}
          rowHeight={34}
          margin={[14, 14]}
          containerPadding={[4, 4]}
          draggableHandle=".card-drag"
          onDragStop={onLayoutChange}
          onResizeStop={onLayoutChange}
          isResizable
          isDraggable
          useCSSTransforms
        >
          <div key="chart" className="card chart-card">
            <div className="card-drag chart-head">
              <h2>
                {chartSymbol} — {fmtMoney(lastPrice?.price, 1) ?? "—"}
              </h2>
              <div className="chart-controls">
                <input
                  className="sym-input"
                  value={chartSymbol}
                  onChange={(e) => setChartSymbol(e.target.value.toUpperCase())}
                  placeholder="BTCUSDT"
                  spellCheck={false}
                />
                <div className="tf-group">
                  {TF_OPTIONS.map((tf) => (
                    <button
                      key={tf}
                      className={`tf-btn ${timeframe === tf ? "active" : ""}`}
                      onClick={() => setTimeframe(tf)}
                    >
                      {tf}
                    </button>
                  ))}
                </div>
              </div>
            </div>
            <CandlestickChart candles={candles} lastPrice={lastPrice} timeframe={timeframe} />
          </div>

          <div key="positions" className="card">
            <div className="card-drag">
              <h2>Posições</h2>
            </div>
            <PositionsTable positions={positions} />
          </div>

          <div key="balances" className="card">
            <div className="card-drag">
              <h2>Saldos</h2>
            </div>
            <BalancesTable balances={balances} />
          </div>

          <div key="paper" className="card">
            <div className="card-drag">
              <h2>Paper</h2>
            </div>
            <PaperCard paper={paper} />
          </div>

          <div key="risk" className="card">
            <div className="card-drag">
              <h2>Risco</h2>
            </div>
            <RiskCard risk={risk} />
          </div>

          <div key="science" className="card science-card">
            <div className="card-drag">
              <h2>Ciência — laudo estatístico</h2>
            </div>
            <ScienceCard report={science} />
          </div>

          <div key="markets" className="card markets-card">
            <div className="card-drag">
              <h2>Mercados globais — radar macro</h2>
            </div>
            <MarketsCard markets={markets} />
          </div>

          <div key="convergence" className="card">
            <div className="card-drag">
              <h2>Convergência — laboratório vivo</h2>
            </div>
            <ConvergenceCard conv={convergence} />
          </div>

          <div key="orders" className="card orders-card">
            <div className="card-drag">
              <h2>Ordens</h2>
            </div>
            <OrdersPanel client={client} orders={orders} onSubmitted={refresh} />
          </div>

          <div key="timeline" className="card timeline-card">
            <div className="card-drag">
              <h2>Timeline</h2>
            </div>
            <Timeline events={timeline} />
          </div>
        </Grid>
      </main>
    </div>
  );
}
