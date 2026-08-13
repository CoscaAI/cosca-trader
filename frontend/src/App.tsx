import { useEffect, useState } from "react";

type Health = {
  ok: boolean;
  service: string;
  port: string;
  events: number;
  subscribers: number;
  first_event?: string;
  last_event?: string;
};

const CORE_URL = "http://127.0.0.1:14126";

function useCoreHealth() {
  const [health, setHealth] = useState<Health | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const poll = () => {
      fetch(`${CORE_URL}/health`)
        .then((r) => r.json())
        .then((h: Health) => alive && setHealth(h))
        .catch(() => alive && setError("core offline"));
    };
    poll();
    const t = setInterval(poll, 3000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, []);

  return { health, error };
}

export default function App() {
  const { health, error } = useCoreHealth();

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span className="logo">◆</span>
          <h1>COSCA&nbsp;TRADER</h1>
          <span className="tag">F0 — Fundação</span>
        </div>
        <div className={`status ${health?.ok ? "ok" : "off"}`}>
          <span className="dot" />
          {health?.ok ? "core online" : error ?? "conectando…"}
        </div>
      </header>

      <main className="grid">
        <section className="card">
          <h2>Core headless (Go)</h2>
          <p>Event bus · event store · SQLite · stream — o motor da plataforma.</p>
          {health && (
            <ul className="metrics">
              <li><b>{health.events}</b> eventos no rastro</li>
              <li><b>{health.subscribers}</b> assinantes ativos</li>
              <li>porta <b>{health.port}</b></li>
            </ul>
          )}
        </section>

        <section className="card">
          <h2>Roadmap</h2>
          <ul className="roadmap">
            <li className="done">F0 · Fundação (event bus, store, schema)</li>
            <li>F1 · Conectividade Binance + market data</li>
            <li>F2 · OMS (ordens, fills, posições, saldos)</li>
            <li>F3 · Gráficos + indicadores (plugins)</li>
            <li>F4 · Risco + opções</li>
            <li>F5 · Estratégias + backtest</li>
            <li>F6 · Assistente IA cognitiva</li>
            <li>F7 · B3 + multiplataforma</li>
          </ul>
        </section>
      </main>
    </div>
  );
}
