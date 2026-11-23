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
// roles table and the bootstrap demo accounts are seeded. An existing file is
// never replaced or deleted, so regenerating a dashboard (even with --force)
// preserves its users. Demo user credentials are printed when a row was
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

	// Roles come from auth.roles; fall back to the built-in defaults when the
	// block is absent. Every configured role is seeded into the roles table.
	roles := g.authRoles()
	for i, role := range roles {
		if _, err := db.Exec("INSERT OR IGNORE INTO roles (name) VALUES (?)", role); err != nil {
			return nil, fmt.Errorf("seeding role %q: %w", role, err)
		}
		_ = i
	}

	columns := fmt.Sprintf(
		"id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, %s TEXT UNIQUE NOT NULL, %s TEXT NOT NULL, role_id INTEGER REFERENCES roles(id), role_name TEXT DEFAULT 'user', status TEXT DEFAULT 'active', created_at TEXT DEFAULT (datetime('now'))",
		q(identity), q(passwordField),
	)
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", q(table), columns)); err != nil {
		return nil, fmt.Errorf("creating auth table: %w", err)
	}

	// One demo user per role: {role}@demo.test. When --admin-password is set it
	// applies to all demo users; otherwise each gets a random password.
	password := g.AdminPassword
	var demoSeeded [][2]string
	for _, role := range roles {
		pw := password
		if pw == "" {
			pw = authbootstrap.RandomPassword()
		}
		seeded, err := authbootstrap.SeedUsers(db, "sqlite", table, identity, passwordField, []string{role}, pw, g.AdminPasswordSet)
		if err != nil {
			return nil, fmt.Errorf("seeding demo user for role %q: %w", role, err)
		}
		demoSeeded = append(demoSeeded, seeded...)
	}
	return authbootstrap.MessageMulti(created, demoSeeded), nil
}

// authRoles returns the configured role names from auth.roles, or the built-in
// defaults (admin, manager, user) when the block is absent.
func (g *Generator) authRoles() []string {
	if len(g.Config.Auth.Roles) > 0 {
		names := make([]string, 0, len(g.Config.Auth.Roles))
		for _, r := range g.Config.Auth.Roles {
			if r.Name != "" {
				names = append(names, r.Name)
			}
		}
		if len(names) > 0 {
			return names
		}
	}
	return []string{"admin", "manager", "user"}
}
