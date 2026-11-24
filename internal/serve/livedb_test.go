package serve

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
)

// TestDriverForDSN verifies configured driver names are normalized to the
// database/sql drivers registered by the binary, with DSN sniffing as fallback.
func TestDriverForDSN(t *testing.T) {
	cases := []struct {
		driver, dsn, want string
	}{
		{"", "postgres://u:p@h/db", "pgx"},
		{"", "postgresql://u:p@h/db", "pgx"},
		{"", "sqlserver://u:p@h?database=db", "mssql"},
		{"", "mssql://u:p@h?database=db", "mssql"},
		{"", "./data.db", "sqlite"},
		{"postgres", "./data.db", "pgx"},
		{"pgx", "./data.db", "pgx"},
		{"mssql", "./data.db", "mssql"},
		{"sqlserver", "./data.db", "mssql"},
		{"sqlite", "./data.db", "sqlite"},
		{"sqlite3", "./data.db", "sqlite"},
		{"PostgreSQL", "./data.db", "pgx"},
	}
	for _, tc := range cases {
		if got := driverForDSN(tc.driver, tc.dsn); got != tc.want {
			t.Errorf("driverForDSN(%q, %q) = %q, want %q", tc.driver, tc.dsn, got, tc.want)
		}
	}
}

// TestSQLiteDSNFromBase verifies relative sqlite DSNs are anchored at the
// config directory while absolute/in-memory DSNs pass through unchanged.
func TestSQLiteDSNFromBase(t *testing.T) {
	base := t.TempDir()
	if got, want := sqliteDSNFromBase("./data.db", base), filepath.ToSlash(filepath.Join(base, "data.db")); got != want {
		t.Errorf("relative = %q, want %q", got, want)
	}
	if got, want := sqliteDSNFromBase("data.db?cache=shared", base), filepath.ToSlash(filepath.Join(base, "data.db"))+"?cache=shared"; got != want {
		t.Errorf("query preserved = %q, want %q", got, want)
	}
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared", "/abs/data.db"} {
		if got := sqliteDSNFromBase(dsn, base); got != dsn {
			t.Errorf("sqliteDSNFromBase(%q) = %q, want unchanged", dsn, got)
		}
	}
}

// TestSchemaSampleUsesConfigConnection is the regression guard for the wedit
// schema browser: with a DSN in the config (no --db flag) "Show data" must
// query the live database instead of reporting the empty in-memory stub.
func TestSchemaSampleUsesConfigConnection(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.db")

	live, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO items (id, name) VALUES (1, 'alpha'), (2, 'beta')`,
	} {
		if _, err := live.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	live.Close()

	cfg := testConfig()
	// Relative DSN: must resolve against the config directory, not the CWD.
	cfg.Connections = map[string]types.Connection{
		"primary": {Driver: "sqlite", DSN: "data.db"},
	}
	cfg.Schema = &types.Schema{Tables: []types.SchemaTable{{
		Name:    "items",
		Columns: []types.SchemaColumn{{Name: "id", Type: "integer"}, {Name: "name", Type: "string"}},
	}}}

	configPath := filepath.Join(dir, "yaga.yaml")
	if err := os.WriteFile(configPath, []byte("version: \"1.0\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, configPath, Options{Port: 0})

	rec, data := get(t, s.Handler(), "GET", "/api/schema-sample/items?limit=5", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if data["source"] != "live" {
		t.Errorf("source = %v, want live", data["source"])
	}
	rows, ok := data["rows"].([]interface{})
	if !ok || len(rows) != 2 {
		t.Fatalf("rows = %v, want 2 rows", data["rows"])
	}
	first, _ := rows[0].(map[string]interface{})
	if first["name"] != "alpha" {
		t.Errorf("first row name = %v, want alpha", first["name"])
	}
}

// TestLiveConnectionFlagWins verifies an explicit --db DSN overrides the
// config's connection.
func TestLiveConnectionFlagWins(t *testing.T) {
	cfg := testConfig()
	cfg.Connections = map[string]types.Connection{
		"primary": {Driver: "sqlite", DSN: "config.db"},
	}
	s := New(cfg, "yaga.yaml", Options{Port: 0, LiveDSN: "flag.db"})
	dsn, driver := s.liveConnection()
	if dsn != "flag.db" {
		t.Errorf("dsn = %q, want flag.db", dsn)
	}
	if driver != "sqlite" {
		t.Errorf("driver = %q, want sqlite", driver)
	}
}
