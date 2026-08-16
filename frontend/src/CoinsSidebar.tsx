// CoinsSidebar.tsx — o sidebar de ativos (à direita, estilo TradingView).
// Lista as moedas da Binance com volume diário consistente e market cap
// suficiente: sigla, nome, preço e variação 24h (verde/vermelho). Ao clicar,
// seleciona o ativo (o painel e o diagnóstico recalculam para ele).

import { useEffect, useState } from "react";
import { TrendingDown, TrendingUp } from "lucide-react";
import type { Coin } from "./types";
import { CoreClient } from "./api";

type Props = {
  client: CoreClient;
  onSelect: (symbol: string) => void;
};

// fmtCompact formata números grandes de forma compacta (1.2B, 340M).
function fmtCompact(n: number): string {
  if (!n || !isFinite(n)) return "—";
  const abs = Math.abs(n);
  if (abs >= 1e9) return `$${(n / 1e9).toFixed(2)}B`;
  if (abs >= 1e6) return `$${(n / 1e6).toFixed(1)}M`;
  if (abs >= 1e3) return `$${(n / 1e3).toFixed(1)}K`;
  return `$${n.toFixed(2)}`;
}

function fmtPrice(p: number): string {
  if (!p || !isFinite(p)) return "—";
  if (p >= 1000) return p.toLocaleString("en-US", { maximumFractionDigits: 2 });
  if (p >= 1) return p.toLocaleString("en-US", { maximumFractionDigits: 4 });
  return p.toLocaleString("en-US", { maximumFractionDigits: 6 });
}

export default function CoinsSidebar({ client, onSelect }: Props) {
  const [coins, setCoins] = useState<Coin[]>([]);
  const [err, setErr] = useState<string>("");

  useEffect(() => {
    let alive = true;
    const load = () => {
      client
        .coins()
        .then((c) => {
          if (alive) {
            setCoins(c);
            setErr("");
          }
        })
        .catch((e) => {
          if (alive) setErr(String(e?.message ?? e));
        });
    };
    load();
    const t = window.setInterval(load, 30_000); // atualiza a cada 30s
    return () => {
      alive = false;
      window.clearInterval(t);
    };
  }, [client]);

  return (
    <aside className="coin-sidebar">
      <div className="coin-sidebar-head">
        <span className="coin-sidebar-title">Ativos</span>
        <span className="coin-sidebar-count">{coins.length}</span>
      </div>
      <div className="coin-list">
        {err && <div className="coin-empty">{err}</div>}
        {!err && coins.length === 0 && <div className="coin-empty">carregando…</div>}
        {coins.map((c) => {
          const up = c.change_24h >= 0;
          return (
            <button
              key={c.symbol}
              className="coin-row"
              onClick={() => onSelect(c.symbol)}
              title={`${c.name} — volume 24h ${fmtCompact(c.volume_24h)} · market cap ${fmtCompact(c.market_cap)}`}
            >
              <div className="coin-id">
                <span className="coin-sigla">{c.base}</span>
                <span className="coin-nome">{c.name || "—"}</span>
              </div>
              <div className="coin-metric">
                <span className="coin-price">{fmtPrice(c.price)}</span>
                <span className={`coin-chg ${up ? "up" : "down"}`}>
                  {up ? <TrendingUp size={13} /> : <TrendingDown size={13} />}{" "}
                  {Math.abs(c.change_24h).toFixed(2)}%
                </span>
              </div>
            </button>
          );
        })}
      </div>
    </aside>
  );
}
