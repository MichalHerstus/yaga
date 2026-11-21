package generator

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichalHerstus/yaga/internal/authbootstrap"
	"github.com/MichalHerstus/yaga/internal/types"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// systemDBConfig is a minimal config whose only business table is NOT an auth
// table, so the generated main.go sanity query has something real to probe.
func systemDBConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "postgres://user:pass@host:5432/db"},
		},
		Schema: &types.Schema{Tables: []types.SchemaTable{{Name: "products", PK: "id"}}},
		Resources: []types.Resource{
			{Name: "Product", Table: "products", List: &types.ListConfig{Columns: []types.Column{{Name: "name"}}}},
		},
	}
}

// readSystemDB opens the generated system.db for assertions.
func readSystemDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, systemDBFilename))
	if err != nil {
		t.Fatalf("open system.db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestGenerateSystemDB verifies the local SQLite auth database is created and
// seeded with the default roles and an admin whose password is a valid bcrypt
// hash of the one printed by generate.
func TestGenerateSystemDB(t *testing.T) {
	dir := t.TempDir()
	g := New(systemDBConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, systemDBFilename)); err != nil {
		t.Fatalf("system.db missing: %v", err)
	}
	if len(g.BootstrapLines) == 0 || !strings.Contains(strings.Join(g.BootstrapLines, "\n"), authbootstrap.AdminEmail) {
		t.Fatalf("bootstrap lines should surface the admin login: %v", g.BootstrapLines)
	}
	password := strings.TrimSpace(strings.Split(g.BootstrapLines[len(g.BootstrapLines)-1], "/")[1])

	db := readSystemDB(t, dir)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM roles").Scan(&count); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if count != 3 {
		t.Errorf("roles count = %d, want 3", count)
	}
	for _, role := range []string{"admin", "manager", "user"} {
		if err := db.QueryRow("SELECT COUNT(*) FROM roles WHERE name = ?", role).Scan(&count); err != nil || count != 1 {
			t.Errorf("role %q missing (count %d, err %v)", role, count, err)
		}
	}

	var email, hash, roleName string
	if err := db.QueryRow(`SELECT "email", "password", role_name FROM "users" WHERE "email" = ?`, authbootstrap.AdminEmail).
		Scan(&email, &hash, &roleName); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if email != authbootstrap.AdminEmail || roleName != "admin" {
		t.Errorf("admin row = %q / %q", email, roleName)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		t.Errorf("admin password hash does not match the printed password: %v", err)
	}
}

// TestGenerateSystemDBCustomAuthFields checks that custom auth.table and
// auth.login.fields names are honoured by the system DB schema. They must be
// double-quoted regardless of the business driver (this fixture uses MSSQL to
// catch a driver-quoting regression).
func TestGenerateSystemDBCustomAuthFields(t *testing.T) {
	cfg := systemDBConfig()
	cfg.Connections["default"] = types.Connection{Driver: "mssql", DSN: "sqlserver://host/db"}
	cfg.Auth.Table = "Accounts"
	cfg.Auth.Login.Fields = []string{"login", "secret"}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	db := readSystemDB(t, dir)
	var hash string
	if err := db.QueryRow(`SELECT "secret" FROM "Accounts" WHERE "login" = ?`, authbootstrap.AdminEmail).Scan(&hash); err != nil {
		t.Fatalf("custom auth table/fields not seeded: %v", err)
	}
	if len(hash) < 10 {
		t.Errorf("password hash looks invalid: %q", hash)
	}
}

// TestGenerateSystemDBIdempotent checks that regeneration preserves the admin
// password, an explicit --admin-password resets it, and --force never deletes
// or recreates the system database.
func TestGenerateSystemDBIdempotent(t *testing.T) {
	dir := t.TempDir()
	g := New(systemDBConfig(), dir)
	g.AdminPassword = "first-password"
	g.AdminPasswordSet = true
	if err := g.Generate(); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, systemDBFilename))
	if err != nil {
		t.Fatalf("stat system.db: %v", err)
	}
	modTime := info.ModTime()

	firstHash := adminHash(t, dir)

	// Omitting --admin-password on regeneration must leave existing users alone.
	g2 := New(systemDBConfig(), dir)
	if err := g2.Generate(); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if got := adminHash(t, dir); got != firstHash {
		t.Error("regeneration without --admin-password changed the admin hash")
	}
	if len(g2.BootstrapLines) != 0 {
		t.Errorf("no bootstrap lines expected when nothing was seeded: %v", g2.BootstrapLines)
	}

	// An explicit password resets the existing admin.
	g3 := New(systemDBConfig(), dir)
	g3.AdminPassword = "second-password"
	g3.AdminPasswordSet = true
	if err := g3.Generate(); err != nil {
		t.Fatalf("reset generate: %v", err)
	}
	newHash := adminHash(t, dir)
	if newHash == firstHash {
		t.Error("explicit --admin-password did not reset the admin password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(newHash), []byte("second-password")); err != nil {
		t.Errorf("reset password hash mismatch: %v", err)
	}
	if info2, err := os.Stat(filepath.Join(dir, systemDBFilename)); err != nil || info2.ModTime().Before(modTime) {
		t.Errorf("system.db must be preserved across regeneration: %v", err)
	}
}

// adminHash reads the bootstrap admin's stored password hash.
func adminHash(t *testing.T, dir string) string {
	t.Helper()
	db := readSystemDB(t, dir)
	var hash string
	if err := db.QueryRow(`SELECT "password" FROM "users" WHERE "email" = ?`, authbootstrap.AdminEmail).Scan(&hash); err != nil {
		t.Fatalf("read admin hash: %v", err)
	}
	return hash
}

// TestGenerateSystemDBRuntimeWiring checks the generated app reads auth from
// the system DB (AUTH_DB_DSN) while all business handlers keep the working DB.
func TestGenerateSystemDBRuntimeWiring(t *testing.T) {
	dir := t.TempDir()
	g := New(systemDBConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	main, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	mainStr := string(main)
	for _, want := range []string{
		`_ "modernc.org/sqlite"`,
		`authDSN := os.Getenv("AUTH_DB_DSN")`,
		`authDSN = envFrom(".ENV", "AUTH_DB_DSN")`,
		`authDSN = "system.db"`,
		`sql.Open("sqlite", authDSN)`,
		`SELECT 1 FROM \"users\" LIMIT 1`,
		`SELECT 1 FROM \"products\" LIMIT 1`,
		`panel.NewRouter(db, authDB, *logLevel)`,
	} {
		if !strings.Contains(mainStr, want) {
			t.Errorf("main.go missing %q", want)
		}
	}

	router, err := os.ReadFile(filepath.Join(dir, "internal/panel/router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	if !strings.Contains(string(router), "func NewRouter(db *sql.DB, authDB *sql.DB, logLevel string)") {
		t.Error("router must accept both databases")
	}
	if !strings.Contains(string(router), "auth.LoginHandler(authDB)") {
		t.Error("login must read from the auth DB")
	}

	authHandler, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth/handler.go"))
	if err != nil {
		t.Fatalf("read auth handler: %v", err)
	}
	handlerStr := string(authHandler)
	if !strings.Contains(handlerStr, `FROM \"users\" WHERE \"email\" = ?`) {
		t.Error("login SQL must be SQLite-native")
	}
	if strings.Contains(handlerStr, "= $1") {
		t.Error("login SQL must not use $N placeholders against SQLite")
	}

	env, err := os.ReadFile(filepath.Join(dir, ".ENV"))
	if err != nil {
		t.Fatalf("read .ENV: %v", err)
	}
	if !strings.Contains(string(env), "AUTH_DB_DSN=system.db\n") {
		t.Errorf(".ENV missing AUTH_DB_DSN:\n%s", env)
	}

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if strings.Count(string(gomod), "modernc.org/sqlite v1.55.0") != 1 {
		t.Errorf("go.mod must pin sqlite exactly once:\n%s", gomod)
	}
}
