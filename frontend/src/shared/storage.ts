// shared/storage.ts — serviço tipado de localStorage (padrão extraído do
// react-redux-boilerplate): chaves com namespace, JSON serializado e seguro.
// Substitui o acesso cru a localStorage espalhado pelo App.tsx.

// StorageKeys é o registro único das chaves persistidas.
export const StorageKeys = {
  token: "cosca_trader_token",
  layout: "cosca_trader_layout",
} as const;

export type StorageKey = (typeof StorageKeys)[keyof typeof StorageKeys];

function ns(key: string, version?: string): string {
  return version ? `${key}:${version}` : key;
}

// get lê e desserializa (null se ausente ou corrompido).
function get<T>(key: string): T | null {
  try {
    return JSON.parse(globalThis.localStorage.getItem(key) ?? "null") as T | null;
  } catch {
    return null;
  }
}

// set serializa e grava.
function set<T>(key: string, value: T): void {
  globalThis.localStorage.setItem(key, JSON.stringify(value));
}

// remove apaga uma chave.
function remove(key: string): void {
  globalThis.localStorage.removeItem(key);
}

// Storage é o serviço público, com versão opcional para chaves versionadas
// (ex.: o layout do grid, que invalida versões antigas).
export const Storage = {
  get: <T>(key: StorageKey, version?: string): T | null =>
    get<T>(ns(key, version)),
  set: <T>(key: StorageKey, value: T, version?: string): void =>
    set(ns(key, version), value),
  remove: (key: StorageKey, version?: string): void =>
    remove(ns(key, version)),
};
