// serve_test.go — testes da camada HTTP do core (P0-1): auth fail-closed e
// CORS fechado. Exercita o roteador via httptest, sem subir listener.
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CoscaAI/cosca-trader/internal/engine"
	"github.com/CoscaAI/cosca-trader/internal/event"
	"github.com/CoscaAI/cosca-trader/internal/store"
)

func newTestMux(t *testing.T, sec apiSecurity) *http.ServeMux {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	e := engine.New(db)
	e.Emit(event.Event{Type: event.SystemStarted, Source: "test"})
	return newMux(e, nil, "0", sec)
}

// doGET executa uma requisição GET com cabeçalhos opcionais.
func doGET(mux *http.ServeMux, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHealthIsPublic(t *testing.T) {
	mux := newTestMux(t, apiSecurity{})
	rec := doGET(mux, "/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/health público deveria ser 200, veio %d", rec.Code)
	}
}

func TestSensitiveEndpointsRequireToken(t *testing.T) {
	for _, path := range []string{"/timeline", "/orders", "/positions", "/balances", "/ledger"} {
		// Fail-closed: sem token configurado → 401.
		mux := newTestMux(t, apiSecurity{})
		if rec := doGET(mux, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s sem token deveria ser 401, veio %d", path, rec.Code)
		}
		// Com token configurado, mas sem Authorization → 401.
		mux = newTestMux(t, apiSecurity{token: "segredo"})
		if rec := doGET(mux, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s sem Bearer deveria ser 401, veio %d", path, rec.Code)
		}
		// Bearer errado → 401.
		mux = newTestMux(t, apiSecurity{token: "segredo"})
		rec := doGET(mux, path, map[string]string{"Authorization": "Bearer errado"})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s Bearer errado deveria ser 401, veio %d", path, rec.Code)
		}
		// Bearer correto → passa da auth (200/404 ok: auth aplicada antes do handler).
		mux = newTestMux(t, apiSecurity{token: "segredo"})
		rec = doGET(mux, path, map[string]string{"Authorization": "Bearer segredo"})
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Errorf("%s com Bearer válido não deveria ser bloqueado, veio %d", path, rec.Code)
		}
	}
}

func TestOriginBlockedByDefault(t *testing.T) {
	// CORS fail-closed: lista vazia → qualquer Origin é bloqueado (403), mesmo
	// com token válido. Mata o CSRF localhost (Simple Request text/plain).
	mux := newTestMux(t, apiSecurity{token: "segredo"})
	rec := doGET(mux, "/positions", map[string]string{
		"Authorization": "Bearer segredo",
		"Origin":        "http://evil.example.com",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Origin não permitida deveria ser 403, veio %d", rec.Code)
	}
}

func TestOriginAllowedWhenListed(t *testing.T) {
	mux := newTestMux(t, apiSecurity{token: "segredo", allowedOrigins: []string{"http://localhost:5173"}})
	rec := doGET(mux, "/positions", map[string]string{
		"Authorization": "Bearer segredo",
		"Origin":        "http://localhost:5173",
	})
	if rec.Code == http.StatusForbidden {
		t.Fatal("Origin na lista permitida não deveria ser bloqueada")
	}
	// outra origem fora da lista → 403
	rec = doGET(mux, "/positions", map[string]string{
		"Authorization": "Bearer segredo",
		"Origin":        "http://outra.example.com",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Origin fora da lista deveria ser 403, veio %d", rec.Code)
	}
}

func TestNoOriginRequestPasses(t *testing.T) {
	// Clientes nativos/desktop sem header Origin (curl, Wails) passam.
	mux := newTestMux(t, apiSecurity{token: "segredo"})
	rec := doGET(mux, "/orders", map[string]string{"Authorization": "Bearer segredo"})
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("request mesmo-origin (sem Origin) não deveria ser bloqueado, veio %d", rec.Code)
	}
}
