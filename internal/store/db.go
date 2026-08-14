// SQLite persistence para o COSCA TRADER: schema de domínio (instrumentos,
// candles, ordens, trades, posições, saldos) + log de eventos durável.
// Usa modernc.org/sqlite (pure-Go, sem CGO) — portátil para o desktop Wails.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/shopspring/decimal"

	"github.com/CoscaAI/cosca-trader/internal/domain"
	"github.com/CoscaAI/cosca-trader/internal/event"
)

// schemaDDL é a migração idempotente de F0 (CREATE TABLE IF NOT EXISTS).
const schemaDDL = `
CREATE TABLE IF NOT EXISTS events (
    seq            INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id       TEXT NOT NULL,
    type           TEXT NOT NULL,
    timestamp      TEXT NOT NULL,
    source         TEXT,
    payload        TEXT,
    correlation_id TEXT,
    causation_id   TEXT,
    severity       TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);

CREATE TABLE IF NOT EXISTS instruments (
    symbol      TEXT NOT NULL,
    exchange    TEXT NOT NULL,
    base_asset  TEXT NOT NULL,
    quote_asset TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT 'spot',
    status      TEXT NOT NULL DEFAULT 'trading',
    tick_size   REAL,
    step_size   REAL,
    min_qty     REAL,
    precision   INTEGER,
    PRIMARY KEY (symbol, exchange)
);

CREATE TABLE IF NOT EXISTS candles (
    symbol    TEXT NOT NULL,
    exchange  TEXT NOT NULL,
    interval  TEXT NOT NULL,
    open_time INTEGER NOT NULL,
    open      REAL, high REAL, low REAL, close REAL,
    volume    REAL,
    trades    INTEGER,
    complete  INTEGER DEFAULT 1,
    PRIMARY KEY (symbol, exchange, interval, open_time)
);

CREATE TABLE IF NOT EXISTS orders (
    id              TEXT PRIMARY KEY,
    client_order_id TEXT,
    symbol          TEXT NOT NULL,
    exchange        TEXT NOT NULL,
    side            TEXT NOT NULL,
    type            TEXT NOT NULL,
    price           TEXT,
    stop_price      TEXT,
    quantity        TEXT,
    filled_qty      TEXT,
    avg_fill_price  TEXT,
    status          TEXT NOT NULL,
    time_in_force   TEXT,
    created_at      INTEGER,
    updated_at      INTEGER
);

CREATE TABLE IF NOT EXISTS trades (
    id        TEXT PRIMARY KEY,
    order_id  TEXT,
    symbol    TEXT NOT NULL,
    exchange  TEXT NOT NULL,
    side      TEXT NOT NULL,
    price     TEXT,
    quantity  TEXT,
    quote_qty TEXT,
    fee       TEXT,
    fee_asset TEXT,
    timestamp INTEGER
);

CREATE TABLE IF NOT EXISTS positions (
    symbol          TEXT NOT NULL,
    exchange        TEXT NOT NULL,
    side            TEXT NOT NULL,
    quantity        TEXT,
    avg_entry_price TEXT,
    realized_pnl    TEXT,
    opened_at       INTEGER,
    updated_at      INTEGER,
    PRIMARY KEY (symbol, exchange)
);

CREATE TABLE IF NOT EXISTS balances (
    asset  TEXT PRIMARY KEY,
    free   TEXT,
    locked TEXT
);

-- order_intents (Fase 2A): registro durável do client_order_id + request
-- ANTES do envio ao broker — a âncora que fecha a janela de crash (ordem
-- aceita na exchange sem OrderCreated no rastro). status:
--   pending   → persistido, resultado do envio desconhecido
--   submitted → confirmado na exchange (OrderCreated emitido)
--   done      → ordem atingiu estado terminal (filled/canceled/expired)
--   failed    → confirmado que a ordem NÃO existe na exchange
CREATE TABLE IF NOT EXISTS order_intents (
    client_order_id TEXT PRIMARY KEY,
    symbol          TEXT NOT NULL,
    side            TEXT NOT NULL,
    type            TEXT NOT NULL,
    price           TEXT NOT NULL DEFAULT '0',
    quantity        TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_order_intents_status ON order_intents(status);
`

// DB encapsula a conexão SQLite com o schema de domínio aplicado.
type DB struct {
	*sql.DB
}

// Open abre (ou cria) o banco em path e aplica a migração idempotente.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("abrir sqlite: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, err
	}
	// Vários produtores (market data + user stream + OMS) escrevem no mesmo
	// SQLite — aguardar o lock concorrente evita SQLITE_BUSY espúrio em WAL.
	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrar schema: %w", err)
	}
	return &DB{db}, nil
}

// PersistEvent grava um evento no log durável.
func (d *DB) PersistEvent(ev event.Event) error {
	payload := ""
	if ev.Payload != nil {
		b, err := json.Marshal(ev.Payload)
		if err != nil {
			return fmt.Errorf("serializar payload: %w", err)
		}
		payload = string(b)
	}
	_, err := d.Exec(
		`INSERT INTO events (event_id, type, timestamp, source, payload, correlation_id, causation_id, severity)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, string(ev.Type), ev.Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00"),
		ev.Source, payload, ev.CorrelationID, ev.CausationID, ev.Severity,
	)
	if err != nil {
		return fmt.Errorf("inserir evento: %w", err)
	}
	return nil
}

// SaveIntent grava (ou atualiza, idempotente pela chave) um order intent.
// É a FASE 1 da saga Fase 2A — o registro durável que precede o envio ao
// broker. Falhar aqui NUNCA deve enviar a ordem para a exchange.
func (d *DB) SaveIntent(i Intent) error {
	_, err := d.Exec(
		`INSERT INTO order_intents (client_order_id, symbol, side, type, price, quantity, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(client_order_id) DO UPDATE SET
			symbol = excluded.symbol, side = excluded.side, type = excluded.type,
			price = excluded.price, quantity = excluded.quantity, status = excluded.status,
			updated_at = excluded.updated_at`,
		i.ClientOrderID, i.Symbol, string(i.Side), string(i.Type), i.Price.String(),
		i.Quantity.String(), string(i.Status), timeFmt(i.CreatedAt), timeFmt(i.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("salvar intent %q: %w", i.ClientOrderID, err)
	}
	return nil
}

// UpdateIntentStatus avança o ciclo de vida de um intent (pending → submitted
// → done / failed).
func (d *DB) UpdateIntentStatus(clientOrderID string, status IntentStatus) error {
	res, err := d.Exec(
		`UPDATE order_intents SET status = ?, updated_at = ? WHERE client_order_id = ?`,
		string(status), timeFmt(time.Now()), clientOrderID,
	)
	if err != nil {
		return fmt.Errorf("atualizar intent %q para %q: %w", clientOrderID, status, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("intent %q não encontrado para atualizar", clientOrderID)
	}
	return nil
}

// PendingIntents devolve todos os intents ainda não resolvidos
// ('pending'/'submitted') — a fonte do Reconcile para adotar ordens órfãs.
func (d *DB) PendingIntents() ([]Intent, error) {
	rows, err := d.Query(
		`SELECT client_order_id, symbol, side, type, price, quantity, status, created_at, updated_at
		 FROM order_intents WHERE status IN ('pending','submitted') ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Intent
	for rows.Next() {
		var it Intent
		var side, typ, price, qty, status, created, updated string
		if err := rows.Scan(&it.ClientOrderID, &it.Symbol, &side, &typ, &price, &qty, &status, &created, &updated); err != nil {
			return nil, err
		}
		it.Side = domain.Side(side)
		it.Type = domain.OrderType(typ)
		it.Price, _ = parseDecimal(price)
		it.Quantity, _ = parseDecimal(qty)
		it.Status = IntentStatus(status)
		it.CreatedAt, _ = parseTime(created)
		it.UpdatedAt, _ = parseTime(updated)
		out = append(out, it)
	}
	return out, rows.Err()
}

// parseDecimal converte a string persistida em decimal exato; string vazia
// vira zero (nunca float no caminho de dinheiro).
func parseDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// timeFmt formata timestamps no mesmo formato do log de eventos.
func timeFmt(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("2006-01-02T15:04:05.999999999Z07:00")
}

// AllEvents devolve todos os eventos do log durável, em ordem de seq.
func (d *DB) AllEvents() ([]event.Event, error) {
	rows, err := d.Query(
		`SELECT event_id, type, timestamp, source, payload, correlation_id, causation_id, severity
		 FROM events ORDER BY seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []event.Event
	for rows.Next() {
		var ev event.Event
		var ts, payload string
		if err := rows.Scan(&ev.ID, &ev.Type, &ts, &ev.Source, &payload,
			&ev.CorrelationID, &ev.CausationID, &ev.Severity); err != nil {
			return nil, err
		}
		ev.Timestamp, _ = parseTime(ts)
		if payload != "" {
			ev.Payload = json.RawMessage(payload)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
