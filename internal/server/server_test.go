package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"proxysql/internal/config"
)

func testCfg() *config.Config {
	return &config.Config{
		Server: config.Server{
			BasePath:     "",
			MaxBodyBytes: 1 << 20,
		},
		Query: config.Query{
			DefaultTimeout:       config.Dur{Duration: 5 * time.Second},
			MaxTimeout:           config.Dur{Duration: 10 * time.Second},
			MaxRows:              100,
			AllowRawSQL:          false,
			AllowMultiStatements: false,
			AllowedStatements:    []string{"SELECT", "INSERT", "UPDATE", "CREATE"},
		},
	}
}

func post(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/query/sync", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleQuerySync(w, r)
	return w
}

func TestDeleteDeniedByPreset(t *testing.T) {
	s := New(testCfg(), nil)
	w := post(t, s, `{"sql":"DELETE FROM t WHERE id=$1","params":[1]}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 got %d: %s", w.Code, w.Body.String())
	}
}

func TestStringLiteralBlockedInStrictMode(t *testing.T) {
	s := New(testCfg(), nil)
	w := post(t, s, `{"sql":"SELECT * FROM t WHERE name='alice'"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "literals") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestStringLiteralAllowedWhenRawEnabled(t *testing.T) {
	cfg := testCfg()
	cfg.Query.AllowRawSQL = true
	// still blocked at pool (nil) only if it got past guard; use a DELETE
	// which is denied by allowlist regardless -> proves guard passed literal check
	w := post(t, New(cfg, nil), `{"sql":"DELETE FROM t WHERE name='alice'"}`)
	if w.Code != http.StatusForbidden { // allowlist, not literal error
		t.Fatalf("want 403 got %d: %s", w.Code, w.Body.String())
	}
}

func TestMethodAndJSONValidation(t *testing.T) {
	s := New(testCfg(), nil)
	r := httptest.NewRequest(http.MethodGet, "/v1/query/sync", nil)
	w := httptest.NewRecorder()
	s.handleQuerySync(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", w.Code)
	}
	w = post(t, s, `not-json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", w.Code)
	}
}

func TestNormalizeParam(t *testing.T) {
	if got := normalizeParam("abc"); got != "abc" {
		t.Fatal(got)
	}
	if got := normalizeParam(true); got != "true" {
		t.Fatal(got)
	}
	if got := normalizeParam(nil); got != nil {
		t.Fatal(got)
	}
}
