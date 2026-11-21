// systemdb.go
//
// Creates and seeds the SQLite "system" database that holds the dashboard's
// auth data (users table + roles). It lives in the generated dashboard folder
// (system.db) and is completely separate from the working --db database that
// holds all business data. Generation stays offline with respect to the
// working DB: this step only writes a local SQLite file.
package generator

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichalHerstus/yaga/internal/authbootstrap"
	"github.com/MichalHerstus/yaga/internal/filterexpr"
	_ "modernc.org/sqlite"
)

const systemDBFilename = "system.db"

// systemDBInfo returns the auth table and login column names used both for the
// system DB schema and by the generated login handler.
func (g *Generator) systemDBInfo() (table, identity, password string) {
	table = g.Config.Auth.Table
	if table == "" {
		table = "users"
	}
	identity, password = "email", "password"
	fields := g.Config.Auth.Login.Fields
	if len(fields) >= 2 {
		identity, password = fields[0], fields[1]
	}
	return table, identity, password
}

// generateSystemDB creates <OutDir>/system.db if missing and makes sure the
// roles table and the bootstrap admin account are seeded. An existing file is
// never replaced or deleted, so regenerating a dashboard (even with --force)
// preserves its users. The effective admin password is printed when a row was
// inserted or reset. Returns the console lines to show the operator.
func (g *Generator) generateSystemDB() ([]string, error) {
	path := filepath.Join(g.OutDir, systemDBFilename)
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)
	if statErr != nil && !created {
		return nil, fmt.Errorf("checking system database: %w", statErr)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening system database: %w", err)
	}
	defer db.Close()

	table, identity, passwordField := g.systemDBInfo()
	q := func(name string) string { return filterexpr.QuoteIdent("sqlite", name) }

	if _, err := db.Exec(fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS roles (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)",
	)); err != nil {
		return nil, fmt.Errorf("creating roles table: %w", err)
	}
	if _, err := db.Exec("INSERT OR IGNORE INTO roles (name) VALUES ('admin'), ('manager'), ('user')"); err != nil {
		return nil, fmt.Errorf("seeding roles: %w", err)
	}

	columns := fmt.Sprintf(
		"id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, %s TEXT UNIQUE NOT NULL, %s TEXT NOT NULL, role_id INTEGER REFERENCES roles(id), role_name TEXT DEFAULT 'user', status TEXT DEFAULT 'active', created_at TEXT DEFAULT (datetime('now'))",
		q(identity), q(passwordField),
	)
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", q(table), columns)); err != nil {
		return nil, fmt.Errorf("creating auth table: %w", err)
	}

	password := g.AdminPassword
	if password == "" {
		password = authbootstrap.RandomPassword()
	}
	inserted, updated, err := authbootstrap.EnsureAdmin(db, "sqlite", table, identity, passwordField, password, g.AdminPasswordSet)
	if err != nil {
		return nil, fmt.Errorf("seeding admin user: %w", err)
	}
	return authbootstrap.Message(created, inserted, updated, password), nil
}
