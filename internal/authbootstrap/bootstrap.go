// bootstrap.go
//
// Shared admin-user seeding used by `yaga generate` when it creates the SQLite
// system database. The logic used to live in cmd/yaga/introspect.go alongside
// `init --db`; with the auth split it belongs to the generator instead. Both
// the CLI tests and the generator use this package so the seed/reset semantics
// cannot drift apart.
package authbootstrap

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"

	"github.com/MichalHerstus/yaga/internal/filterexpr"
	"golang.org/x/crypto/bcrypt"
)

// AdminEmail is the fixed login of the seeded bootstrap account.
const AdminEmail = "admin@admin.test"

// AdminName is the display name of the seeded bootstrap account.
const AdminName = "Admin User"

// RandomPassword returns a cryptographically random 14-character password from
// an unambiguous alphabet. It is printed once, never stored in plaintext.
func RandomPassword() string {
	const chars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 14)
	if _, err := rand.Read(buf); err != nil {
		return "changeme"
	}
	for i, c := range buf {
		buf[i] = chars[int(c)%len(chars)]
	}
	return string(buf)
}

// Hash returns a bcrypt hash of the given plaintext password.
func Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// EnsureAdmin guarantees the bootstrap admin account exists in the auth table.
// With reset=false an existing user set is left untouched; with reset=true an
// existing admin row's password is updated (and a new one inserted if absent).
// driver selects the placeholder/quoting dialect; table, identityField and
// passwordField name the auth table and its identity/password columns.
// Returns whether a row was inserted, whether a password was updated, and any
// error.
func EnsureAdmin(db *sql.DB, driver, table, identityField, passwordField, password string, reset bool) (inserted, updated bool, err error) {
	quote := func(s string) string { return filterexpr.QuoteIdent(driver, s) }
	placeholder := "?, ?, ?, ?, ?, ?"
	updatePlaceholders := "?, ?"
	if driver == "postgres" {
		placeholder = "$1, $2, $3, $4, $5, $6"
		updatePlaceholders = "$1, $2"
	}

	var count int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", quote(table))).Scan(&count); err != nil {
		return false, false, fmt.Errorf("counting users: %w", err)
	}
	if count > 0 && !reset {
		return false, false, nil
	}

	hash, err := Hash(password)
	if err != nil {
		return false, false, fmt.Errorf("hashing admin password: %w", err)
	}

	if count > 0 {
		upPH := strings.Split(updatePlaceholders, ", ")
		res, err := db.Exec(
			fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s = %s", quote(table), quote(passwordField), upPH[0], quote(identityField), upPH[1]),
			hash, AdminEmail,
		)
		if err != nil {
			return false, false, fmt.Errorf("updating admin password: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return false, true, nil
		}
	}

	var adminRoleID int
	if err := db.QueryRow("SELECT id FROM roles WHERE name = 'admin'").Scan(&adminRoleID); err != nil {
		return false, false, fmt.Errorf("finding admin role: %w", err)
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (name, %s, %s, role_id, role_name, status) VALUES (%s)",
		quote(table), quote(identityField), quote(passwordField), placeholder,
	)
	if _, err := db.Exec(query, AdminName, AdminEmail, hash, adminRoleID, "admin", "active"); err != nil {
		return false, false, fmt.Errorf("inserting admin user: %w", err)
	}
	return true, false, nil
}

// Message returns the console lines to print after a bootstrap operation. The
// credentials are shown whenever a row was inserted or its password changed —
// the password is either a one-time random value or an explicit flag value,
// neither of which is stored in plaintext.
func Message(createdTables, inserted, updated bool, password string) []string {
	var lines []string
	if createdTables {
		lines = append(lines, "Created auth tables (users, roles).")
	}
	if inserted {
		lines = append(lines, "Seeded admin user.")
	}
	if updated {
		lines = append(lines, "Updated admin password.")
	}
	if inserted || updated {
		lines = append(lines, fmt.Sprintf("Admin login: %s / %s", AdminEmail, password))
	}
	return lines
}
