package sqlguard_test

import "testing"
import "proxysql/internal/sqlguard"

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":                  "SELECT",
		"  -- c\nselect * from t":   "SELECT",
		"INSERT INTO t VALUES ($1)": "INSERT",
		"insert into t values(1) on conflict do nothing": "INSERT",
		"UPDATE t SET a=$1":                     "UPDATE",
		"DELETE FROM t":                         "DELETE",
		"CREATE TABLE t (id int)":               "CREATE",
		"ALTER TABLE t ADD COLUMN x int":        "ALTER",
		"DROP TABLE t":                          "DROP",
		"WITH x AS (SELECT 1) SELECT * FROM x":  "SELECT",
		"WITH x AS (SELECT 1) UPDATE t SET a=1": "UPDATE",
		"EXPLAIN SELECT 1":                      "EXPLAIN",
		"BEGIN":                                 "UTILITY",
	}
	for sql, want := range cases {
		if got := sqlguard.Classify(sql); got != want {
			t.Errorf("Classify(%q)=%q want %q", sql, got, want)
		}
	}
}

func TestAllowlistPreset(t *testing.T) {
	allow := []string{"SELECT", "INSERT", "UPDATE", "CREATE"}
	for _, s := range []string{"SELECT", "INSERT", "UPDATE", "CREATE"} {
		if !sqlguard.Allowed(s, allow) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if sqlguard.Allowed("DELETE", allow) {
		t.Errorf("DELETE should be denied by preset")
	}
}

func TestRawSQL(t *testing.T) {
	if err := sqlguard.CheckRawSQL("SELECT * FROM t WHERE id=$1", 1, false); err != nil {
		t.Fatal(err)
	}
	if err := sqlguard.CheckRawSQL("SELECT * FROM t WHERE id=1", 0, false); err != nil {
		t.Fatal(err) // no placeholders + no params is fine even in strict mode
	}
	if err := sqlguard.CheckRawSQL("SELECT * FROM t WHERE id=1", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := sqlguard.CheckRawSQL("SELECT $1", 0, false); err == nil {
		t.Fatal("expected placeholder mismatch error")
	}
	// raw allowed: bare sql ok
	if err := sqlguard.CheckRawSQL("SELECT * FROM t WHERE id=1", 0, true); err != nil {
		t.Fatal(err)
	}
	// $1 inside string literal must not count
	if err := sqlguard.CheckRawSQL("SELECT '$1'", 0, false); err != nil {
		t.Fatalf("literal $1 should not count: %v", err)
	}
}

func TestStringLiteral(t *testing.T) {
	yes := []string{
		"SELECT * FROM t WHERE name='alice'",
		"SELECT 'hello'",
		"INSERT INTO t (a) VALUES ('x')",
		`SELECT $$dollar$$`,
		`SELECT $tag$hi$tag$`,
		"SELECT * FROM t WHERE name='a''b'", // escaped quote still a literal
	}
	for _, sql := range yes {
		if !sqlguard.ContainsStringLiteral(sql) {
			t.Errorf("want literal detected: %q", sql)
		}
	}
	no := []string{
		"SELECT * FROM t WHERE id=$1",
		"SELECT 1",
		`SELECT "it""s" FROM t`, // double-quoted identifier, not a literal
		"-- don't count this\nSELECT 1",
		"/* 'quoted' */ SELECT 1",
		"CREATE TABLE t (id serial primary key)",
	}
	for _, sql := range no {
		if sqlguard.ContainsStringLiteral(sql) {
			t.Errorf("false positive: %q", sql)
		}
	}
}

func TestMulti(t *testing.T) {
	if !sqlguard.HasMultiStatements("SELECT 1; SELECT 2") {
		t.Fatal("should detect multi")
	}
	if sqlguard.HasMultiStatements("SELECT 1") {
		t.Fatal("single should not be multi")
	}
	if sqlguard.HasMultiStatements("SELECT ';'") {
		t.Fatal("semicolon in literal should not split")
	}
	if sqlguard.HasMultiStatements("SELECT 1;") {
		t.Fatal("trailing semicolon is still single")
	}
}
