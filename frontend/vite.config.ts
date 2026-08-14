// vite.config.ts — Fase 3C: proxy para o core headless. O CORS do core é
// fail-closed (qualquer Origin bloqueado); o proxy resolve o browser removendo
// o header Origin das requisições — o core vê um request mesmo-origin/cliente
// nativo e aplica apenas a auth Bearer. A porta do core é configurável
// (COSCA_TRADER_CORE_URL); default: 127.0.0.1:24120 (porta ÚNICA e ALTA, fora
// da família 1412x do Cosca — o conflito histórico com o cosca-code na 14126
// está resolvido).
import { defineConfig, type ProxyOptions } from "vite";
import react from "@vitejs/plugin-react";

const CORE_TARGET = process.env.COSCA_TRADER_CORE_URL ?? "http://127.0.0.1:24120";

function coreProxy(): ProxyOptions {
  return {
    target: CORE_TARGET,
    changeOrigin: true,
    configure: (proxy) => {
      proxy.on("proxyReq", (proxyReq) => {
        // mata o vetor CSRF: o core nunca vê a Origin do navegador
        proxyReq.removeHeader("origin");
      });
    },
  };
}

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/health": coreProxy(),
      "/events": coreProxy(),
      "/orders": coreProxy(),
      "/positions": coreProxy(),
      "/balances": coreProxy(),
      "/paper": coreProxy(),
      "/timeline": coreProxy(),
      "/ledger": coreProxy(),
      "/risk": coreProxy(),
      "/backtest": coreProxy(),
      "/convergence": coreProxy(),
      "/markets": coreProxy(),
    },
  },
});
