package config

import (
	"os"
	"path/filepath"
	"testing"
)

const testYAML = `
server:
  listen: ":8080"
  base_path: "/proxysql"
  read_timeout: 65s
  write_timeout: 65s
  max_body_bytes: 1048576
db:
  pool_min: 2
  pool_max: 5
  acquire_timeout: 5s
  idle_timeout: 5m
  max_lifetime: 30m
  health_check_period: 30s
query:
  default_timeout: 60s
  max_timeout: 120s
  max_rows: 100
  allow_raw_sql: false
  allow_multi_statements: false
  allowed_statements: ["SELECT", "INSERT"]
auth:
  api_keys: []
log:
  level: info
  log_sql_text: false
`

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsFromYAML(t *testing.T) {
	t.Setenv("PROXYSQL_DB__DSN", "postgres://u:pw@host:5432/db?sslmode=require")
	cfg, err := Load(writeYAML(t, testYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Query.AllowRawSQL {
		t.Fatal("want allow_raw_sql=false from yaml")
	}
	if cfg.Server.BasePath != "/proxysql" {
		t.Fatalf("want base_path from yaml, got %q", cfg.Server.BasePath)
	}
}

func TestDSNEnvOverridesYAML(t *testing.T) {
	t.Setenv("PROXYSQL_DB__DSN", "postgres://env:secret@envhost:5432/e?sslmode=require")
	cfg, err := Load(writeYAML(t, testYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := "postgres://env:secret@envhost:5432/e?sslmode=require"
	if cfg.DB.DSN != want {
		t.Fatalf("want DSN from env, got %q", cfg.DB.DSN)
	}
}

func TestOtherEnvKeysAreIgnored(t *testing.T) {
	// Single home per setting: only DSN has an env binding.
	t.Setenv("PROXYSQL_DB__DSN", "postgres://u:pw@host:5432/db?sslmode=require")
	t.Setenv("PROXYSQL_QUERY__ALLOW_RAW_SQL", "true")
	t.Setenv("PROXYSQL_SERVER__BASE_PATH", "/ignored")
	cfg, err := Load(writeYAML(t, testYAML))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Query.AllowRawSQL {
		t.Fatal("ALLOW_RAW_SQL env must no longer override yaml")
	}
	if cfg.Server.BasePath != "/proxysql" {
		t.Fatalf("BASE_PATH env must no longer override yaml, got %q", cfg.Server.BasePath)
	}
}

func TestMissingDSNFailsFast(t *testing.T) {
	// DSN is env-only: neither yaml default nor empty env is accepted.
	os.Unsetenv("PROXYSQL_DB__DSN")
	if _, err := Load(writeYAML(t, testYAML)); err == nil {
		t.Fatal("want startup failure without PROXYSQL_DB__DSN")
	}
	t.Setenv("PROXYSQL_DB__DSN", "   ")
	if _, err := Load(writeYAML(t, testYAML)); err == nil {
		t.Fatal("want startup failure with blank PROXYSQL_DB__DSN")
	}
}

func TestRedactedDSN(t *testing.T) {
	got := RedactedDSN("postgres://u:pw@host:5432/db?sslmode=require")
	want := "postgres://u:***@host:5432/db?sslmode=require"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if g := RedactedDSN("not-a-dsn"); g != "not-a-dsn" {
		t.Fatalf("non-URL must pass through, got %q", g)
	}
}
