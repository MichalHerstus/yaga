// introspect.go
//
// Implements `yaga init --db {dsn}`: connects to an existing database,
// introspects its schema (tables, columns, primary keys, foreign keys),
// generates yaga.yaml — including the captured `schema:` block, the sole
// source of schema truth for the generator (D11, no sqlc). Creates the
// users/roles auth tables + admin user when missing.
package main

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

// ColumnInfo describes a single column as discovered by schema introspection.
type ColumnInfo struct {
	Name         string
	DBType       string
	Nullable     bool
	Default      string
	IsPrimaryKey bool
}

// ForeignKeyInfo describes a foreign key constraint discovered on a table.
type ForeignKeyInfo struct {
	Column        string
	ForeignTable  string
	ForeignColumn string
}

// TableInfo describes a database table (or view) as discovered by schema
// introspection.
type TableInfo struct {
	Name        string
	Columns     []ColumnInfo
	ForeignKeys []ForeignKeyInfo
	// IsView marks an object discovered from the database's view catalog
	// rather than its base tables. Views are surfaced as read-only resources
	// (list/card/detail only, no create/update/delete forms).
	IsView bool
}

// detectDriver determines the database driver from a DSN string. Postgres DSNs
// start with "postgres://" or "postgresql://", MSSQL DSNs with "sqlserver://"
// or "mssql://"; everything else is treated as sqlite (file path, :memory:,
// etc.).
func detectDriver(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return "postgres"
	}
	if strings.HasPrefix(dsn, "sqlserver://") || strings.HasPrefix(dsn, "mssql://") {
		return "mssql"
	}
	return "sqlite"
}

// openDB opens a database connection using the appropriate driver for the DSN.
// MSSQL uses the "mssql" driver name so the driver's loose placeholder parsing
// accepts the $N placeholders that the postgres-flavored SQL uses.
func openDB(dsn, driver string) (*sql.DB, error) {
	if driver == "postgres" {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, fmt.Errorf("opening postgres connection: %w", err)
		}
		return db, nil
	}
	if driver == "mssql" {
		db, err := sql.Open("mssql", dsn)
		if err != nil {
			return nil, fmt.Errorf("opening mssql connection: %w", err)
		}
		return db, nil
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite connection: %w", err)
	}
	return db, nil
}

// introspectSchema discovers all user tables in the database along with their
// columns, primary keys and foreign keys.
func introspectSchema(db *sql.DB, driver string) ([]TableInfo, error) {
	if driver == "postgres" {
		return introspectPostgres(db)
	}
	if driver == "mssql" {
		return introspectMSSQL(db)
	}
	return introspectSQLite(db)
}

// introspectPostgres queries information_schema to discover tables and views,
// their columns, primary keys and foreign keys in a PostgreSQL database.
// Views carry no primary keys or foreign keys (both discovered only for base
// tables); they are marked IsView so they surface as read-only resources.
func introspectPostgres(db *sql.DB) ([]TableInfo, error) {
	rows, err := db.Query(`
		SELECT table_name, table_type FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type IN ('BASE TABLE', 'VIEW')
		ORDER BY table_name`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tableNames []string
	tableViews := map[string]bool{}
	for rows.Next() {
		var name, tt string
		if err := rows.Scan(&name, &tt); err != nil {
			return nil, err
		}
		tableNames = append(tableNames, name)
		tableViews[name] = tt == "VIEW"
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var tables []TableInfo
	for _, name := range tableNames {
		ti := TableInfo{Name: name, IsView: tableViews[name]}

		colRows, err := db.Query(`
			SELECT column_name, data_type, is_nullable, column_default
			FROM information_schema.columns
			WHERE table_name = $1 AND table_schema = 'public'
			ORDER BY ordinal_position`, name)
		if err != nil {
			return nil, fmt.Errorf("listing columns for %s: %w", name, err)
		}
		for colRows.Next() {
			var c ColumnInfo
			var nullable string
			var defaultVal sql.NullString
			if err := colRows.Scan(&c.Name, &c.DBType, &nullable, &defaultVal); err != nil {
				colRows.Close()
				return nil, err
			}
			c.Nullable = nullable == "YES"
			if defaultVal.Valid {
				c.Default = defaultVal.String
			}
			ti.Columns = append(ti.Columns, c)
		}
		colRows.Close()
		if err := colRows.Err(); err != nil {
			return nil, err
		}

		pkRows, err := db.Query(`
			SELECT kcu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			WHERE tc.table_name = $1
				AND tc.constraint_type = 'PRIMARY KEY'
				AND tc.table_schema = 'public'
			ORDER BY kcu.ordinal_position`, name)
		if err != nil {
			return nil, fmt.Errorf("listing PKs for %s: %w", name, err)
		}
		if !ti.IsView {
			for pkRows.Next() {
				var colName string
				if err := pkRows.Scan(&colName); err != nil {
					pkRows.Close()
					return nil, err
				}
				for i := range ti.Columns {
					if ti.Columns[i].Name == colName {
						ti.Columns[i].IsPrimaryKey = true
						break
					}
				}
			}
		}
		pkRows.Close()
		if err := pkRows.Err(); err != nil {
			return nil, err
		}

		fkRows, err := db.Query(`
			SELECT kcu.column_name, ccu.table_name, ccu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
				ON tc.constraint_name = kcu.constraint_name
				AND tc.table_schema = kcu.table_schema
			JOIN information_schema.constraint_column_usage ccu
				ON tc.constraint_name = ccu.constraint_name
				AND tc.table_schema = ccu.table_schema
			WHERE tc.table_name = $1
				AND tc.constraint_type = 'FOREIGN KEY'
				AND tc.table_schema = 'public'`, name)
		if err != nil {
			return nil, fmt.Errorf("listing FKs for %s: %w", name, err)
		}
		if !ti.IsView {
			for fkRows.Next() {
				var fk ForeignKeyInfo
				if err := fkRows.Scan(&fk.Column, &fk.ForeignTable, &fk.ForeignColumn); err != nil {
					fkRows.Close()
					return nil, err
				}
				ti.ForeignKeys = append(ti.ForeignKeys, fk)
			}
		}
		fkRows.Close()
		if err := fkRows.Err(); err != nil {
			return nil, err
		}

		tables = append(tables, ti)
	}
	return tables, nil
}

// introspectMSSQL queries INFORMATION_SCHEMA and sys views to discover tables,
// columns, primary keys and foreign keys in a SQL Server database. Table
// discovery is restricted to the current schema (SCHEMA_NAME()). The $N
// placeholders work because the connection uses the mssql driver name with
// loose placeholder parsing.
func introspectMSSQL(db *sql.DB) ([]TableInfo, error) {
	rows, err := db.Query(`
		SELECT table_name, table_type FROM INFORMATION_SCHEMA.TABLES
		WHERE table_type IN ('BASE TABLE', 'VIEW') AND table_schema = SCHEMA_NAME()
		ORDER BY table_name`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tableNames []string
	tableViews := map[string]bool{}
	for rows.Next() {
		var name, tt string
		if err := rows.Scan(&name, &tt); err != nil {
			return nil, err
		}
		tableNames = append(tableNames, name)
		tableViews[name] = tt == "VIEW"
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var tables []TableInfo
	for _, name := range tableNames {
		ti := TableInfo{Name: name, IsView: tableViews[name]}

		colRows, err := db.Query(`
			SELECT column_name, data_type, is_nullable, column_default
			FROM INFORMATION_SCHEMA.COLUMNS
			WHERE table_name = $1 AND table_schema = SCHEMA_NAME()
			ORDER BY ordinal_position`, name)
		if err != nil {
			return nil, fmt.Errorf("listing columns for %s: %w", name, err)
		}
		for colRows.Next() {
			var c ColumnInfo
			var nullable string
			var defaultVal sql.NullString
			if err := colRows.Scan(&c.Name, &c.DBType, &nullable, &defaultVal); err != nil {
				colRows.Close()
				return nil, err
			}
			c.Nullable = nullable == "YES"
			if defaultVal.Valid {
				c.Default = defaultVal.String
			}
			ti.Columns = append(ti.Columns, c)
		}
		colRows.Close()
		if err := colRows.Err(); err != nil {
			return nil, err
		}

		if !ti.IsView {
			pkRows, err := db.Query(`
				SELECT kcu.column_name
				FROM INFORMATION_SCHEMA.table_constraints tc
				JOIN INFORMATION_SCHEMA.key_column_usage kcu
					ON tc.constraint_name = kcu.constraint_name
					AND tc.table_schema = kcu.table_schema
				WHERE tc.table_name = $1
					AND tc.constraint_type = 'PRIMARY KEY'
					AND tc.table_schema = SCHEMA_NAME()
				ORDER BY kcu.ordinal_position`, name)
			if err != nil {
				return nil, fmt.Errorf("listing PKs for %s: %w", name, err)
			}
			for pkRows.Next() {
				var colName string
				if err := pkRows.Scan(&colName); err != nil {
					pkRows.Close()
					return nil, err
				}
				for i := range ti.Columns {
					if ti.Columns[i].Name == colName {
						ti.Columns[i].IsPrimaryKey = true
						break
					}
				}
			}
			pkRows.Close()
			if err := pkRows.Err(); err != nil {
				return nil, err
			}

			// Tables without a declared PRIMARY KEY still often have an IDENTITY
			// column that acts as the row key (e.g. legacy MSSQL schemas). Treat
			// identity columns as the primary key so yaga keys routes on them
			// and omits them from INSERT/UPDATE. A declared PK takes precedence.
			hasPK := false
			for i := range ti.Columns {
				if ti.Columns[i].IsPrimaryKey {
					hasPK = true
					break
				}
			}
			if !hasPK {
				idRows, err := db.Query(`
					SELECT c.name
					FROM sys.columns c
					JOIN sys.tables t ON t.object_id = c.object_id
					WHERE t.name = $1 AND c.is_identity = 1`, name)
				if err != nil {
					return nil, fmt.Errorf("listing identity columns for %s: %w", name, err)
				}
				for idRows.Next() {
					var colName string
					if err := idRows.Scan(&colName); err != nil {
						idRows.Close()
						return nil, err
					}
					for i := range ti.Columns {
						if ti.Columns[i].Name == colName {
							ti.Columns[i].IsPrimaryKey = true
							break
						}
					}
				}
				idRows.Close()
				if err := idRows.Err(); err != nil {
					return nil, err
				}
			}

			fkRows, err := db.Query(`
				SELECT fk_col.name AS column_name, rt.name AS foreign_table, rc.name AS foreign_column
				FROM sys.foreign_keys fk
				JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
				JOIN sys.tables t ON t.object_id = fk.parent_object_id
				JOIN sys.columns fk_col ON fk_col.object_id = fkc.parent_object_id AND fk_col.column_id = fkc.parent_column_id
				JOIN sys.tables rt ON rt.object_id = fk.referenced_object_id
				JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
				WHERE t.name = $1`, name)
			if err != nil {
				return nil, fmt.Errorf("listing FKs for %s: %w", name, err)
			}
			for fkRows.Next() {
				var fk ForeignKeyInfo
				if err := fkRows.Scan(&fk.Column, &fk.ForeignTable, &fk.ForeignColumn); err != nil {
					fkRows.Close()
					return nil, err
				}
				ti.ForeignKeys = append(ti.ForeignKeys, fk)
			}
			fkRows.Close()
			if err := fkRows.Err(); err != nil {
				return nil, err
			}
		}

		tables = append(tables, ti)
	}
	return tables, nil
}

// sqliteIdent double-quotes an identifier for use inside a PRAGMA argument,
// escaping embedded double quotes (SQLite doubles them inside "" identifiers).
// Required because a table may be named with a SQL keyword (e.g. "Order").
func sqliteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// introspectSQLite queries sqlite_master and PRAGMA statements to discover
// tables and views, their columns, primary keys and foreign keys in a SQLite
// database. Views carry no primary keys or foreign keys (both discovered only
// for base tables); they are marked IsView so they surface as read-only.
func introspectSQLite(db *sql.DB) ([]TableInfo, error) {
	rows, err := db.Query(`SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var tableNames []string
	tableViews := map[string]bool{}
	for rows.Next() {
		var name, tt string
		if err := rows.Scan(&name, &tt); err != nil {
			return nil, err
		}
		tableNames = append(tableNames, name)
		tableViews[name] = tt == "view"
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var tables []TableInfo
	for _, name := range tableNames {
		ti := TableInfo{Name: name, IsView: tableViews[name]}

		colRows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, sqliteIdent(name)))
		if err != nil {
			return nil, fmt.Errorf("listing columns for %s: %w", name, err)
		}
		for colRows.Next() {
			var c ColumnInfo
			var cid int
			var notnull int
			var pk int
			var defaultVal sql.NullString
			if err := colRows.Scan(&cid, &c.Name, &c.DBType, &notnull, &defaultVal, &pk); err != nil {
				colRows.Close()
				return nil, err
			}
			c.Nullable = notnull == 0
			c.IsPrimaryKey = pk > 0
			if defaultVal.Valid {
				c.Default = defaultVal.String
			}
			ti.Columns = append(ti.Columns, c)
		}
		colRows.Close()
		if err := colRows.Err(); err != nil {
			return nil, err
		}

		if !ti.IsView {
			fkRows, err := db.Query(fmt.Sprintf(`PRAGMA foreign_key_list(%s)`, sqliteIdent(name)))
			if err != nil {
				return nil, fmt.Errorf("listing FKs for %s: %w", name, err)
			}
			for fkRows.Next() {
				var fk ForeignKeyInfo
				var id, seq int
				var onUpdate, onDelete, match string
				if err := fkRows.Scan(&id, &seq, &fk.ForeignTable, &fk.Column, &fk.ForeignColumn, &onUpdate, &onDelete, &match); err != nil {
					fkRows.Close()
					return nil, err
				}
				ti.ForeignKeys = append(ti.ForeignKeys, fk)
			}
			fkRows.Close()
			if err := fkRows.Err(); err != nil {
				return nil, err
			}
		}

		tables = append(tables, ti)
	}
	return tables, nil
}

// mapDBTypeToFieldType converts a database column type string to a yaga
// field type. It strips parenthesised size modifiers and matches against
// known type prefixes.
func mapDBTypeToFieldType(dbType string) string {
	t := strings.ToLower(dbType)
	if idx := strings.Index(t, "("); idx != -1 {
		t = t[:idx]
	}
	t = strings.TrimSpace(t)

	switch {
	case strings.Contains(t, "int") || strings.Contains(t, "serial") || strings.Contains(t, "bigserial") || strings.Contains(t, "smallserial"):
		return "integer"
	case strings.Contains(t, "varchar") || strings.Contains(t, "text") || strings.Contains(t, "char") || strings.Contains(t, "character") || strings.Contains(t, "uniqueidentifier") || strings.Contains(t, "xml"):
		return "string"
	case strings.Contains(t, "bool") || t == "bit":
		return "boolean"
	case strings.Contains(t, "timestamp") || strings.Contains(t, "datetime") || strings.Contains(t, "date") || t == "time":
		return "datetime"
	case strings.Contains(t, "real") || strings.Contains(t, "float") || strings.Contains(t, "double") || strings.Contains(t, "numeric") || strings.Contains(t, "decimal") || strings.Contains(t, "money"):
		return "float"
	case strings.Contains(t, "json"):
		return "json"
	case strings.Contains(t, "bytea") || strings.Contains(t, "blob") || strings.Contains(t, "varbinary") || strings.Contains(t, "binary") || strings.Contains(t, "image"):
		return "file"
	default:
		return "string"
	}
}

// singularize converts a plural table name to a singular resource name.
// It handles common English plurals: "s", "ies", "es", "ses".
func singularize(tableName string) string {
	lower := strings.ToLower(tableName)
	switch {
	case strings.HasSuffix(lower, "ies"):
		return tableName[:len(tableName)-3] + "y"
	case strings.HasSuffix(lower, "ses") || strings.HasSuffix(lower, "xes") || strings.HasSuffix(lower, "zes") || strings.HasSuffix(lower, "ches") || strings.HasSuffix(lower, "shes"):
		return tableName[:len(tableName)-2]
	case strings.HasSuffix(lower, "s") && !strings.HasSuffix(lower, "ss"):
		return tableName[:len(tableName)-1]
	default:
		return tableName
	}
}

// toPascalCase converts a snake_case or lowercase string to PascalCase.
func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// toSingularPascal converts a table name to singular PascalCase.
// e.g. "user_roles" -> "UserRole", "order_items" -> "OrderItem"
func toSingularPascal(tableName string) string {
	return toPascalCase(singularize(tableName))
}

// findLabelColumn picks the "best" column to display as a human-readable label
// for a table. It prefers columns named "name", "title", or "label", then
// falls back to the first non-PK text column, then the PK.
func findLabelColumn(ti TableInfo) string {
	for _, c := range ti.Columns {
		if strings.ToLower(c.Name) == "name" {
			return c.Name
		}
	}
	for _, c := range ti.Columns {
		if strings.ToLower(c.Name) == "title" {
			return c.Name
		}
	}
	for _, c := range ti.Columns {
		if strings.ToLower(c.Name) == "label" {
			return c.Name
		}
	}
	for _, c := range ti.Columns {
		if !c.IsPrimaryKey && mapDBTypeToFieldType(c.DBType) == "string" {
			return c.Name
		}
	}
	for _, c := range ti.Columns {
		if c.IsPrimaryKey {
			return c.Name
		}
	}
	if len(ti.Columns) > 0 {
		return ti.Columns[0].Name
	}
	return ""
}

// findLabelColumnByTable finds the label column for a table name in a list of tables.
func findLabelColumnByTable(tables []TableInfo, tableName string) string {
	for _, ti := range tables {
		if ti.Name == tableName {
			return findLabelColumn(ti)
		}
	}
	return "name"
}

// findTableByName finds a table by name in a list of tables.
func findTableByName(tables []TableInfo, name string) *TableInfo {
	for i, ti := range tables {
		if ti.Name == name {
			return &tables[i]
		}
	}
	return nil
}

// hasTable reports whether a real (non-view) table with the given name exists.
// Views named e.g. "users" / "roles" must never be mistaken for the auth tables.
func hasTable(tables []TableInfo, name string) bool {
	for _, ti := range tables {
		if ti.Name == name && !ti.IsView {
			return true
		}
	}
	return false
}

// findPKColumn returns the primary key column name for a table. Returns "id"
// as a fallback. Views carry no primary keys, so they fall back to a column
// literally named "id" when present, else their first column.
func findPKColumn(ti TableInfo) string {
	for _, c := range ti.Columns {
		if c.IsPrimaryKey {
			return c.Name
		}
	}
	if ti.IsView {
		for _, c := range ti.Columns {
			if strings.EqualFold(c.Name, "id") {
				return c.Name
			}
		}
		if len(ti.Columns) > 0 {
			return ti.Columns[0].Name
		}
	}
	return "id"
}

// idColumnName returns the actual column name yaga should treat as the row
// key for a table: the primary key column when declared, otherwise the column
// conventionally named "id" (any case). Returns "" when neither exists. Unlike
// findPKColumn, this preserves the real column case so the key matches the
// names in list row maps (e.g. "ID" on MSSQL).
func idColumnName(ti TableInfo) string {
	pk := findPKColumn(ti)
	for _, c := range ti.Columns {
		if strings.EqualFold(c.Name, pk) {
			return c.Name
		}
	}
	return ""
}

// viewKeyIsInt reports whether a view's resolved key column maps to an integer
// field type. The generated detail handler casts the key to an int, so only
// integer-keyed views can get a detail ("view form") that compiles.
func viewKeyIsInt(ti TableInfo) bool {
	key := findPKColumn(ti)
	for _, c := range ti.Columns {
		if c.Name == key {
			return mapDBTypeToFieldType(c.DBType) == "integer"
		}
	}
	return false
}

// ensureAuthTables checks whether "users" and "roles" tables exist in the
// database. If either is missing, both are created with a driver-appropriate
// DDL, default roles are seeded, and an admin user is inserted.
func ensureAuthTables(db *sql.DB, driver string, tables []TableInfo) error {
	hasUsers := hasTable(tables, "users")
	hasRoles := hasTable(tables, "roles")

	if hasUsers && hasRoles {
		return nil
	}

	if driver == "postgres" {
		if !hasRoles {
			if _, err := db.Exec(`
				CREATE TABLE roles (
					id SERIAL PRIMARY KEY,
					name VARCHAR(100) NOT NULL
				)`); err != nil {
				return fmt.Errorf("creating roles table: %w", err)
			}
			if _, err := db.Exec(`INSERT INTO roles (name) VALUES ('admin'), ('manager'), ('user')`); err != nil {
				return fmt.Errorf("seeding roles: %w", err)
			}
		}
		if !hasUsers {
			if _, err := db.Exec(`
				CREATE TABLE users (
					id SERIAL PRIMARY KEY,
					name VARCHAR(255) NOT NULL,
					email VARCHAR(255) UNIQUE NOT NULL,
					password VARCHAR(255) NOT NULL,
					role_id INT REFERENCES roles(id),
					role_name VARCHAR(100) DEFAULT 'user',
					status VARCHAR(20) DEFAULT 'active',
					created_at TIMESTAMPTZ DEFAULT NOW()
				)`); err != nil {
				return fmt.Errorf("creating users table: %w", err)
			}
		}
	} else if driver == "mssql" {
		if !hasRoles {
			if _, err := db.Exec(`
				CREATE TABLE roles (
					id INT IDENTITY(1,1) PRIMARY KEY,
					name NVARCHAR(100) NOT NULL
				)`); err != nil {
				return fmt.Errorf("creating roles table: %w", err)
			}
			if _, err := db.Exec(`INSERT INTO roles (name) VALUES ('admin'), ('manager'), ('user')`); err != nil {
				return fmt.Errorf("seeding roles: %w", err)
			}
		}
		if !hasUsers {
			if _, err := db.Exec(`
				CREATE TABLE users (
					id INT IDENTITY(1,1) PRIMARY KEY,
					name NVARCHAR(255) NOT NULL,
					email NVARCHAR(255) UNIQUE NOT NULL,
					password NVARCHAR(255) NOT NULL,
					role_id INT REFERENCES roles(id),
					role_name NVARCHAR(100) DEFAULT 'user',
					status NVARCHAR(20) DEFAULT 'active',
					created_at DATETIME2 DEFAULT SYSUTCDATETIME()
				)`); err != nil {
				return fmt.Errorf("creating users table: %w", err)
			}
		}
	} else {
		if !hasRoles {
			if _, err := db.Exec(`
				CREATE TABLE roles (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					name TEXT NOT NULL
				)`); err != nil {
				return fmt.Errorf("creating roles table: %w", err)
			}
			if _, err := db.Exec(`INSERT INTO roles (name) VALUES ('admin'), ('manager'), ('user')`); err != nil {
				return fmt.Errorf("seeding roles: %w", err)
			}
		}
		if !hasUsers {
			if _, err := db.Exec(`
				CREATE TABLE users (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					name TEXT NOT NULL,
					email TEXT UNIQUE NOT NULL,
					password TEXT NOT NULL,
					role_id INTEGER REFERENCES roles(id),
					role_name TEXT DEFAULT 'user',
					status TEXT DEFAULT 'active',
					created_at TEXT DEFAULT (datetime('now'))
				)`); err != nil {
				return fmt.Errorf("creating users table: %w", err)
			}
		}
	}

	return nil
}

// insertAdminUser inserts a default admin user into the users table if it is
// empty. Credentials: admin@admin.test / password (an empty password makes the
// caller generate and print a random one). Returns whether a user was inserted.
func insertAdminUser(db *sql.DB, driver, password string) (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, fmt.Errorf("counting users: %w", err)
	}
	if count > 0 {
		return false, nil
	}

	hash, err := bcryptHash(password)
	if err != nil {
		return false, fmt.Errorf("hashing admin password: %w", err)
	}

	var adminRoleID int
	if driver == "postgres" {
		err = db.QueryRow(`SELECT id FROM roles WHERE name = 'admin'`).Scan(&adminRoleID)
	} else {
		err = db.QueryRow(`SELECT id FROM roles WHERE name = 'admin'`).Scan(&adminRoleID)
	}
	if err != nil {
		return false, fmt.Errorf("finding admin role: %w", err)
	}

	if driver == "postgres" {
		_, err = db.Exec(`
			INSERT INTO users (name, email, password, role_id, role_name, status)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			"Admin User", "admin@admin.test", hash, adminRoleID, "admin", "active")
	} else {
		_, err = db.Exec(`
			INSERT INTO users (name, email, password, role_id, role_name, status)
			VALUES (?, ?, ?, ?, ?, ?)`,
			"Admin User", "admin@admin.test", hash, adminRoleID, "admin", "active")
	}
	if err != nil {
		return false, fmt.Errorf("inserting admin user: %w", err)
	}
	return true, nil
}

// bcryptHash produces a bcrypt hash of the given plaintext password.
func bcryptHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// randomPassword returns a cryptographically random 14-character password built
// from an unambiguous alphabet (no 0/O, 1/l/I). It is used as the one-time
// admin password for --demo and --db scaffolding when --admin-password is not
// given, and is printed to the user instead of being embedded anywhere.
func randomPassword() string {
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

// convertSchema converts the introspected []TableInfo into the *types.Schema
// captured as the `schema:` block of yaga.yaml. Each column's yaga field type
// is derived via mapDBTypeToFieldType; each foreign key records the label
// column of its target table (used to build option SQL and list/detail label
// joins offline).
// Params: tables (introspected tables/views), driver (postgres/sqlite/mssql).
// Returns: the schema block value.
func convertSchema(tables []TableInfo, driver string) *types.Schema {
	s := &types.Schema{}
	for _, ti := range tables {
		st := types.SchemaTable{
			Name:        ti.Name,
			PK:          findPKColumn(ti),
			View:        ti.IsView,
			Columns:     []types.SchemaColumn{},
			ForeignKeys: []types.SchemaFK{},
		}
		for _, c := range ti.Columns {
			st.Columns = append(st.Columns, types.SchemaColumn{
				Name:       c.Name,
				Type:       mapDBTypeToFieldType(c.DBType),
				PrimaryKey: c.IsPrimaryKey,
			})
		}
		for _, fk := range ti.ForeignKeys {
			st.ForeignKeys = append(st.ForeignKeys, types.SchemaFK{
				Column:        fk.Column,
				ForeignTable:  fk.ForeignTable,
				ForeignColumn: fk.ForeignColumn,
				Label:         findLabelColumnByTable(tables, fk.ForeignTable),
			})
		}
		s.Tables = append(s.Tables, st)
	}
	return s
}

// writeSchemaBlock appends the captured `schema:` block to the YAML builder,
// indenting the marshaled schema under the top-level key.
func writeSchemaBlock(b *strings.Builder, tables []TableInfo, driver string) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(convertSchema(tables, driver))
	_ = enc.Close()
	b.WriteString("schema:\n")
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
}

// generateYAML builds a yaga.yaml config string from the introspected
// schema. It creates a resource for each table (excluding users/roles) with
// list, detail and form sections, plus the captured `schema:` block (the sole
// schema source for the generator). Foreign keys become relation fields;
// their option SQL is derived from the schema block at generation time.
func generateYAML(tables []TableInfo, driver, dsn string) string {
	var b strings.Builder

	b.WriteString(`version: "1.0"

panel:
  id: admin
  path: /admin
  name: "My Admin"

connections:
  default:
    driver: `)
	b.WriteString(driver)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf(`    dsn: %q
`, dsn))

	writeSchemaBlock(&b, tables, driver)

	b.WriteString(`
auth:
  guard: web
  provider: session
  table: users
  login:
    fields: [email, password]
    redirect: /admin/dashboard

resources:
`)

	for _, ti := range tables {
		if ti.Name == "users" || ti.Name == "roles" {
			continue
		}
		writeResourceYAML(&b, ti, tables, driver)
	}

	b.WriteString(`
pages:
  - name: Dashboard
    path: /dashboard
    default: true
    widgets:
      - type: stats_grid
        columns: 2
        widgets:
          - type: stat
            label: "Total Users"
            query: SELECT COUNT(*) FROM users
            icon: users
          - type: stat
            label: "Active Users"
            query: SELECT COUNT(*) FROM users WHERE status = 'active'
            icon: check

navigation:
  - group: "Management"
    icon: all
    sort: 1
    items:
`)

	for _, ti := range tables {
		if ti.Name == "users" || ti.Name == "roles" {
			continue
		}
		resourceName := toSingularPascal(ti.Name)
		b.WriteString(fmt.Sprintf("      - resource: %s\n", resourceName))
	}

	return b.String()
}

// writeResourceYAML writes a single resource definition in YAML format to the
// builder. It configures list columns, detail fields and form fields based on
// the introspected columns and foreign keys.
func writeResourceYAML(b *strings.Builder, ti TableInfo, allTables []TableInfo, driver string) {
	resourceName := toSingularPascal(ti.Name)
	pluralPascal := toPascalCase(ti.Name)
	pk := findPKColumn(ti)

	b.WriteString(fmt.Sprintf("  - name: %s\n", resourceName))
	b.WriteString(fmt.Sprintf("    label: %s\n", pluralPascal))

	if strings.ToLower(resourceName)+"s" != ti.Name {
		b.WriteString(fmt.Sprintf("    table: %s\n", ti.Name))
	}

	if idCol := idColumnName(ti); idCol != "" && idCol != "id" {
		b.WriteString(fmt.Sprintf("    id_column: %s\n", idCol))
	}

	pkGo := pkGoType(ti, driver)
	defaultPKGo := "int32"
	if driver == "sqlite" {
		defaultPKGo = "int64"
	}
	if pkGo != "" && pkGo != defaultPKGo {
		b.WriteString(fmt.Sprintf("    id_type: %s\n", pkGo))
	}

	defaultSort := findDefaultSort(ti)

	// list section
	b.WriteString("    list:\n")
	b.WriteString(fmt.Sprintf("      query: List%s\n", pluralPascal))
	b.WriteString(fmt.Sprintf("      count_query: Count%s\n", pluralPascal))
	b.WriteString("      columns:\n")

	for _, c := range ti.Columns {
		isFK := false
		for _, fk := range ti.ForeignKeys {
			if fk.Column == c.Name {
				isFK = true
				break
			}
		}
		if isFK {
			continue
		}
		writeColumnYAML(b, c)
	}

	// FK label columns in the list
	for _, fk := range ti.ForeignKeys {
		foreignTable := findTableByName(allTables, fk.ForeignTable)
		if foreignTable == nil {
			continue
		}
		labelCol := findLabelColumn(*foreignTable)
		colName := fk.Column + "_label"
		b.WriteString(fmt.Sprintf("        - name: %s\n", colName))
		b.WriteString(fmt.Sprintf("          label: %s\n", toPascalCase(singularize(fk.ForeignTable))))
		b.WriteString("          type: string\n")
		_ = labelCol
	}

	if defaultSort != "" {
		b.WriteString(fmt.Sprintf("      default_sort: -%s\n", defaultSort))
	}

	// detail section. Views are read-only, so their detail ("view form") is
	// only emitted when the key column is integer-typed — the generated detail
	// handler casts the key to an int, so a text/non-integer key column would
	// not compile against the generated data Get query.
	emitDetail := !ti.IsView || viewKeyIsInt(ti)
	if emitDetail {
		b.WriteString("    detail:\n")
		b.WriteString(fmt.Sprintf("      query: Get%s\n", toSingularPascal(ti.Name)))
		b.WriteString("      params:\n")
		b.WriteString(fmt.Sprintf("        id: \"{record.%s}\"\n", pk))
		b.WriteString("      fields:\n")
		for _, c := range ti.Columns {
			writeFieldYAML(b, c, ti, allTables, driver, false, "        ")
		}
	}

	if ti.IsView {
		// Views are surfaced as read-only resources: a card view is provided,
		// but no create/update/delete form (the view is not writable).
		b.WriteString("    card:\n")
		b.WriteString("      fields:\n")
		for _, c := range ti.Columns {
			writeFieldYAML(b, c, ti, allTables, driver, true, "        ")
		}
		return
	}

	// form section
	b.WriteString("    form:\n")

	// create
	b.WriteString("      create:\n")
	b.WriteString(fmt.Sprintf("        query: Create%s\n", toSingularPascal(ti.Name)))
	b.WriteString("        fields:\n")
	for _, c := range ti.Columns {
		if c.IsPrimaryKey {
			continue
		}
		if c.Default != "" && c.Nullable {
			continue
		}
		writeFieldYAML(b, c, ti, allTables, driver, true, "          ")
	}

	// update
	b.WriteString("      update:\n")
	b.WriteString(fmt.Sprintf("        query: Update%s\n", toSingularPascal(ti.Name)))
	b.WriteString(fmt.Sprintf("        populate_query: Get%s\n", toSingularPascal(ti.Name)))
	b.WriteString("        fields:\n")
	for _, c := range ti.Columns {
		if c.IsPrimaryKey {
			continue
		}
		writeFieldYAML(b, c, ti, allTables, driver, true, "          ")
	}

	// children: every child table whose FK points back at this table's key is
	// a potential master-detail section (D14). The FK column is recorded; the
	// generator derives the rest (child lines SELECT, pre-bound add/edit) from
	// the child resource and the captured schema block.
	var children []TableInfo
	for _, other := range allTables {
		if other.IsView || other.Name == ti.Name {
			continue
		}
		for _, fk := range other.ForeignKeys {
			if strings.EqualFold(fk.ForeignTable, ti.Name) && strings.EqualFold(fk.ForeignColumn, pk) {
				children = append(children, other)
				break
			}
		}
	}
	if len(children) > 0 {
		b.WriteString("    children:\n")
		for _, other := range children {
			b.WriteString(fmt.Sprintf("      - name: %s\n", toPascalCase(other.Name)))
			b.WriteString(fmt.Sprintf("        resource: %s\n", toSingularPascal(other.Name)))
			for _, fk := range other.ForeignKeys {
				if strings.EqualFold(fk.ForeignTable, ti.Name) && strings.EqualFold(fk.ForeignColumn, pk) {
					b.WriteString(fmt.Sprintf("        column: %s\n", fk.Column))
					break
				}
			}
		}
	}
}

// writeColumnYAML writes a list column definition.
func writeColumnYAML(b *strings.Builder, c ColumnInfo) {
	ft := mapDBTypeToFieldType(c.DBType)
	b.WriteString(fmt.Sprintf("        - name: %s\n", c.Name))
	b.WriteString(fmt.Sprintf("          type: %s\n", ft))
	if c.IsPrimaryKey || ft == "integer" {
		b.WriteString("          sortable: true\n")
	}
	if ft == "string" || ft == "email" {
		b.WriteString("          searchable: true\n")
	}
}

// writeFieldYAML writes a detail/form field definition with the given
// indentation prefix. For foreign key columns in forms, it writes a relation
// field carrying options_value/options_label; the option SQL itself is derived
// from the captured schema block at generation time (no options_query, D11).
func writeFieldYAML(b *strings.Builder, c ColumnInfo, ti TableInfo, allTables []TableInfo, driver string, isForm bool, indent string) {
	for _, fk := range ti.ForeignKeys {
		if fk.Column == c.Name {
			if isForm {
				b.WriteString(fmt.Sprintf("%s- name: %s\n", indent, c.Name))
				b.WriteString(indent + "  type: relation\n")
				b.WriteString(fmt.Sprintf("%s  options_value: %s\n", indent, fk.ForeignColumn))
				labelCol := findLabelColumnByTable(allTables, fk.ForeignTable)
				b.WriteString(fmt.Sprintf("%s  options_label: %s\n", indent, labelCol))
			} else {
				b.WriteString(fmt.Sprintf("%s- name: %s\n", indent, c.Name))
				b.WriteString(fmt.Sprintf("%s  type: %s\n", indent, mapDBTypeToFieldType(c.DBType)))
			}
			return
		}
	}

	ft := mapDBTypeToFieldType(c.DBType)
	b.WriteString(fmt.Sprintf("%s- name: %s\n", indent, c.Name))
	b.WriteString(fmt.Sprintf("%s  type: %s\n", indent, ft))
	if c.Name == "password" {
		b.WriteString(indent + "  type: password\n")
	}
}

// findDefaultSort picks a sensible default sort column: the first datetime
// column, or the primary key.
func findDefaultSort(ti TableInfo) string {
	for _, c := range ti.Columns {
		ft := mapDBTypeToFieldType(c.DBType)
		if ft == "datetime" {
			return c.Name
		}
	}
	return findPKColumn(ti)
}

// pkGoType returns the Go type used for the primary key column of a table,
// derived from its database type: sqlite INTEGER ids map to int64, BIGINT to
// int64, SMALLINT to int16, everything else to int32. This drives the
// `id_type` override emitted into the resource YAML so the generator's
// detail/update/data handlers cast the row key correctly. Returns "" when no
// matching column is found.
func pkGoType(ti TableInfo, driver string) string {
	pk := findPKColumn(ti)
	for _, c := range ti.Columns {
		if !strings.EqualFold(c.Name, pk) {
			continue
		}
		if driver == "sqlite" {
			return "int64"
		}
		switch strings.ToLower(c.DBType) {
		case "bigint":
			return "int64"
		case "smallint":
			return "int16"
		default:
			return "int32"
		}
	}
	return ""
}

// cmdInitFromDB is the main entry point for `yaga init --db {dsn}`. It
// connects to the database, introspects the schema, creates auth tables if
// missing, inserts an admin user when the users table is empty, then generates
// yaga.yaml — including the captured `schema:` block. No SQL files are
// written: the generator runs offline from the schema block (D11).
// If updateMode is true, merges new tables into existing config instead of overwriting.
func cmdInitFromDB(configPath, outDir, dsn, adminPassword string, force, updateMode bool) error {
	if !force && !updateMode {
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("%s already exists. Use --force to overwrite.", configPath)
		}
		if _, err := os.Stat(outDir); err == nil {
			return fmt.Errorf("%s already exists. Use --force to overwrite.", outDir)
		}
	}

	driver := detectDriver(dsn)

	db, err := openDB(dsn, driver)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}

	tables, err := introspectSchema(db, driver)
	if err != nil {
		return fmt.Errorf("introspecting schema: %w", err)
	}

	if len(tables) == 0 {
		fmt.Println("No tables found in database.")
	}

	hasRoles := hasTable(tables, "roles")
	hasUsers := hasTable(tables, "users")

	if err := ensureAuthTables(db, driver, tables); err != nil {
		return fmt.Errorf("ensuring auth tables: %w", err)
	}

	adminPass := adminPassword
	if adminPass == "" {
		adminPass = randomPassword()
	}
	inserted, err := insertAdminUser(db, driver, adminPass)
	if err != nil {
		return fmt.Errorf("inserting admin user: %w", err)
	}

	if !hasRoles || !hasUsers {
		fmt.Println("Created auth tables (users, roles) and seeded admin user.")
		if inserted {
			fmt.Printf("Admin login: admin@admin.test / %s\n", adminPass)
		}
	}

	// Re-introspect after creating auth tables so the full schema is available
	// for YAML/SQL generation (needed for FK references to roles, etc.)
	tables, err = introspectSchema(db, driver)
	if err != nil {
		return fmt.Errorf("re-introspecting schema: %w", err)
	}

	if updateMode {
		return cmdInitUpdate(configPath, dsn, driver, tables)
	}

	// Write yaga.yaml (includes the captured schema block)
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(configPath, []byte(generateYAML(tables, driver, dsn)), 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Println("Introspected database and generated config:", configPath)
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  1. Review", configPath, "(the schema: block is the sole schema source)")
	fmt.Println("  2. Run 'yaga generate --config", configPath, "--out", outDir, "'")
	return nil
}

// cmdInitUpdate handles the --update mode: merges newly introspected tables
// into an existing yaga.yaml while preserving user customizations.
func cmdInitUpdate(configPath, dsn, driver string, tables []TableInfo) error {
	// Read existing config
	existingYAML, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Config doesn't exist, fall back to full init
			fmt.Println("Config file not found, creating new config...")
			return os.WriteFile(configPath, []byte(generateYAML(tables, driver, dsn)), 0644)
		}
		return fmt.Errorf("reading config file: %w", err)
	}

	// Merge resources
	mergedYAML, added, orphaned, err := mergeResources(existingYAML, tables, driver, dsn)
	if err != nil {
		return fmt.Errorf("merging resources: %w", err)
	}

	// Write merged config
	if err := os.WriteFile(configPath, mergedYAML, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Println("Introspected database and updated config:", configPath)
	fmt.Println("Schema block updated")
	if len(added) > 0 {
		fmt.Printf("Added %d new resource(s): %s\n", len(added), strings.Join(added, ", "))
	} else {
		fmt.Println("No new tables found")
	}
	if len(orphaned) > 0 {
		fmt.Printf("Orphaned resources (table dropped): %s (marked with comment)\n", strings.Join(orphaned, ", "))
	}
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  1. Review", configPath, "(the schema: block is the sole schema source)")
	fmt.Println("  2. Run 'yaga generate --config", configPath, "--out ./admin'")
	return nil
}

// mergeResources merges introspected tables into existing YAML config.
// Returns merged YAML bytes, added resource names, orphaned resource names, error.
func mergeResources(existingYAML []byte, tables []TableInfo, driver, dsn string) ([]byte, []string, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(existingYAML, &doc); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing existing yaml: %w", err)
	}
	root, err := mappingOf(&doc)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing existing yaml: %w", err)
	}

	// Build map of existing resources by table name
	existingByTable := buildExistingResourceMap(root)

	// Build map of introspected tables by name
	introspectedByName := make(map[string]TableInfo)
	for _, ti := range tables {
		if ti.Name == "users" || ti.Name == "roles" {
			continue
		}
		introspectedByName[ti.Name] = ti
	}

	// Find new tables (in DB but not in config)
	var added []string
	for tableName, ti := range introspectedByName {
		if _, exists := existingByTable[tableName]; !exists {
			// Generate resource node for new table
			resNode := writeResourceYAMLNode(ti, tables, driver)
			// Insert into resources sequence
			insertResourceNode(root, resNode)
			added = append(added, toSingularPascal(ti.Name))
		}
	}

	// Find orphaned resources (in config but not in DB)
	var orphaned []string
	for tableName, resNode := range existingByTable {
		if _, exists := introspectedByName[tableName]; !exists {
			// Mark as orphaned with comment
			addOrphanedComment(resNode, tableName)
			orphaned = append(orphaned, toSingularPascal(tableName))
		}
	}

	// Replace schema block entirely (source of truth)
	replaceSchemaBlock(root, tables, driver)

	// Update default connection DSN
	updateConnectionDSN(root, dsn)

	// Marshal back to YAML
	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encoding yaml: %w", err)
	}

	return out, added, orphaned, nil
}

// buildExistingResourceMap builds a map of table name -> resource node from the existing config.
func buildExistingResourceMap(root *yaml.Node) map[string]*yaml.Node {
	result := make(map[string]*yaml.Node)
	ri := mappingIndex(root, "resources")
	if ri < 0 {
		return result
	}
	ress := root.Content[ri+1]
	if ress.Kind != yaml.SequenceNode {
		return result
	}
	for _, res := range ress.Content {
		if res.Kind != yaml.MappingNode {
			continue
		}
		// Get table name from resource (explicit table: or derived from name)
		tableName := mappingValue(res, "table")
		if tableName == "" {
			// Derive from resource name (pluralize)
			resName := mappingValue(res, "name")
			tableName = pluralize(resName)
		}
		result[tableName] = res
	}
	return result
}

// pluralize converts a singular PascalCase resource name to plural table name.
// Reverse of singularize/toSingularPascal. Matches the convention in generateYAML:
// resource "Product" -> table "products" (lowercase + s)
func pluralize(name string) string {
	return strings.ToLower(name) + "s"
}

// writeResourceYAMLNode generates a yaml.Node for a resource (like writeResourceYAML but node-based).
func writeResourceYAMLNode(ti TableInfo, allTables []TableInfo, driver string) *yaml.Node {
	resourceName := toSingularPascal(ti.Name)
	pluralPascal := toPascalCase(ti.Name)
	pk := findPKColumn(ti)

	res := &yaml.Node{Kind: yaml.MappingNode}

	// name
	res.Content = append(res.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: resourceName})

	// label
	res.Content = append(res.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "label"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: pluralPascal})

	// table (if different from pluralized name)
	if strings.ToLower(resourceName)+"s" != ti.Name {
		res.Content = append(res.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "table"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: ti.Name})
	}

	// id_column (if not "id")
	if idCol := idColumnName(ti); idCol != "" && idCol != "id" {
		res.Content = append(res.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "id_column"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: idCol})
	}

	// id_type (if non-default)
	pkGo := pkGoType(ti, driver)
	defaultPKGo := "int32"
	if driver == "sqlite" {
		defaultPKGo = "int64"
	}
	if pkGo != "" && pkGo != defaultPKGo {
		res.Content = append(res.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "id_type"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: pkGo})
	}

	defaultSort := findDefaultSort(ti)

	// list section
	listNode := &yaml.Node{Kind: yaml.MappingNode}
	listNode.Content = append(listNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "query"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("List%s", pluralPascal)})
	listNode.Content = append(listNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "count_query"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("Count%s", pluralPascal)})

	// columns
	colsNode := &yaml.Node{Kind: yaml.SequenceNode}
	for _, c := range ti.Columns {
		isFK := false
		for _, fk := range ti.ForeignKeys {
			if fk.Column == c.Name {
				isFK = true
				break
			}
		}
		if isFK {
			continue
		}
		colNode := createColumnNode(c)
		colsNode.Content = append(colsNode.Content, colNode)
	}

	// FK label columns in the list
	for _, fk := range ti.ForeignKeys {
		foreignTable := findTableByName(allTables, fk.ForeignTable)
		if foreignTable == nil {
			continue
		}
		labelCol := findLabelColumn(*foreignTable)
		colName := fk.Column + "_label"
		colNode := &yaml.Node{Kind: yaml.MappingNode}
		colNode.Content = append(colNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: colName})
		colNode.Content = append(colNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "label"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: toPascalCase(singularize(fk.ForeignTable))})
		colNode.Content = append(colNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "string"})
		_ = labelCol
		colsNode.Content = append(colsNode.Content, colNode)
	}

	listNode.Content = append(listNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "columns"},
		colsNode)

	if defaultSort != "" {
		listNode.Content = append(listNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "default_sort"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "-" + defaultSort})
	}

	res.Content = append(res.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "list"},
		listNode)

	// detail section (views: only if integer key)
	emitDetail := !ti.IsView || viewKeyIsInt(ti)
	if emitDetail {
		detailNode := &yaml.Node{Kind: yaml.MappingNode}
		detailNode.Content = append(detailNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "query"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("Get%s", toSingularPascal(ti.Name))})
		paramsNode := &yaml.Node{Kind: yaml.MappingNode}
		paramsNode.Content = append(paramsNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "id"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("{record.%s}", pk)})
		detailNode.Content = append(detailNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "params"},
			paramsNode)

		fieldsNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, c := range ti.Columns {
			fieldNode := createFieldNode(c, ti, allTables, driver, false)
			fieldsNode.Content = append(fieldsNode.Content, fieldNode)
		}
		detailNode.Content = append(detailNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "fields"},
			fieldsNode)
		res.Content = append(res.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "detail"},
			detailNode)
	}

	if ti.IsView {
		// Views are read-only: card only, no form
		cardNode := &yaml.Node{Kind: yaml.MappingNode}
		fieldsNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, c := range ti.Columns {
			fieldNode := createFieldNode(c, ti, allTables, driver, true)
			fieldsNode.Content = append(fieldsNode.Content, fieldNode)
		}
		cardNode.Content = append(cardNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "fields"},
			fieldsNode)
		res.Content = append(res.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "card"},
			cardNode)
		return res
	}

	// form section
	formNode := &yaml.Node{Kind: yaml.MappingNode}

	// create
	createNode := &yaml.Node{Kind: yaml.MappingNode}
	createNode.Content = append(createNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "query"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("Create%s", toSingularPascal(ti.Name))})
	createFieldsNode := &yaml.Node{Kind: yaml.SequenceNode}
	for _, c := range ti.Columns {
		if c.IsPrimaryKey {
			continue
		}
		if c.Default != "" && c.Nullable {
			continue
		}
		fieldNode := createFieldNode(c, ti, allTables, driver, true)
		createFieldsNode.Content = append(createFieldsNode.Content, fieldNode)
	}
	createNode.Content = append(createNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "fields"},
		createFieldsNode)
	formNode.Content = append(formNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "create"},
		createNode)

	// update
	updateNode := &yaml.Node{Kind: yaml.MappingNode}
	updateNode.Content = append(updateNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "query"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("Update%s", toSingularPascal(ti.Name))})
	updateNode.Content = append(updateNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "populate_query"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("Get%s", toSingularPascal(ti.Name))})
	updateFieldsNode := &yaml.Node{Kind: yaml.SequenceNode}
	for _, c := range ti.Columns {
		if c.IsPrimaryKey {
			continue
		}
		fieldNode := createFieldNode(c, ti, allTables, driver, true)
		updateFieldsNode.Content = append(updateFieldsNode.Content, fieldNode)
	}
	updateNode.Content = append(updateNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "fields"},
		updateFieldsNode)
	formNode.Content = append(formNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "update"},
		updateNode)

	res.Content = append(res.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "form"},
		formNode)

	// children: reverse FKs
	var children []TableInfo
	for _, other := range allTables {
		if other.IsView || other.Name == ti.Name {
			continue
		}
		for _, fk := range other.ForeignKeys {
			if strings.EqualFold(fk.ForeignTable, ti.Name) && strings.EqualFold(fk.ForeignColumn, pk) {
				children = append(children, other)
				break
			}
		}
	}
	if len(children) > 0 {
		childrenNode := &yaml.Node{Kind: yaml.SequenceNode}
		for _, other := range children {
			childNode := &yaml.Node{Kind: yaml.MappingNode}
			childNode.Content = append(childNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: toPascalCase(other.Name)})
			childNode.Content = append(childNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "resource"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: toSingularPascal(other.Name)})
			for _, fk := range other.ForeignKeys {
				if strings.EqualFold(fk.ForeignTable, ti.Name) && strings.EqualFold(fk.ForeignColumn, pk) {
					childNode.Content = append(childNode.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "column"},
						&yaml.Node{Kind: yaml.ScalarNode, Value: fk.Column})
					break
				}
			}
			childrenNode.Content = append(childrenNode.Content, childNode)
		}
		res.Content = append(res.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "children"},
			childrenNode)
	}

	return res
}

// createColumnNode creates a list column node from ColumnInfo.
func createColumnNode(c ColumnInfo) *yaml.Node {
	ft := mapDBTypeToFieldType(c.DBType)
	colNode := &yaml.Node{Kind: yaml.MappingNode}
	colNode.Content = append(colNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: c.Name})
	colNode.Content = append(colNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: ft})
	if c.IsPrimaryKey || ft == "integer" {
		colNode.Content = append(colNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "sortable"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "true"})
	}
	if ft == "string" || ft == "email" {
		colNode.Content = append(colNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "searchable"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "true"})
	}
	return colNode
}

// createFieldNode creates a detail/form field node from ColumnInfo.
func createFieldNode(c ColumnInfo, ti TableInfo, allTables []TableInfo, driver string, isForm bool) *yaml.Node {
	for _, fk := range ti.ForeignKeys {
		if fk.Column == c.Name {
			fieldNode := &yaml.Node{Kind: yaml.MappingNode}
			fieldNode.Content = append(fieldNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: c.Name})
			if isForm {
				fieldNode.Content = append(fieldNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: "relation"})
				fieldNode.Content = append(fieldNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "options_value"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: fk.ForeignColumn})
				labelCol := findLabelColumnByTable(allTables, fk.ForeignTable)
				fieldNode.Content = append(fieldNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "options_label"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: labelCol})
			} else {
				ft := mapDBTypeToFieldType(c.DBType)
				fieldNode.Content = append(fieldNode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: ft})
			}
			return fieldNode
		}
	}

	ft := mapDBTypeToFieldType(c.DBType)
	fieldNode := &yaml.Node{Kind: yaml.MappingNode}
	fieldNode.Content = append(fieldNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "name"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: c.Name})
	fieldNode.Content = append(fieldNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "type"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: ft})
	if c.Name == "password" {
		// Override type for password field
		for i := 0; i < len(fieldNode.Content); i += 2 {
			if fieldNode.Content[i].Value == "type" {
				fieldNode.Content[i+1].Value = "password"
				break
			}
		}
	}
	return fieldNode
}

// insertResourceNode inserts a new resource node into the resources sequence.
func insertResourceNode(root *yaml.Node, resNode *yaml.Node) {
	ri := mappingIndex(root, "resources")
	if ri < 0 {
		// Create resources sequence if it doesn't exist
		resourcesSeq := &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "resources"},
			resourcesSeq)
		resourcesSeq.Content = append(resourcesSeq.Content, resNode)
		return
	}
	ress := root.Content[ri+1]
	if ress.Kind != yaml.SequenceNode {
		return
	}
	ress.Content = append(ress.Content, resNode)
}

// addOrphanedComment adds a comment to mark a resource as orphaned.
func addOrphanedComment(resNode *yaml.Node, tableName string) {
	comment := fmt.Sprintf(" ORPHANED: table '%s' no longer exists in database", tableName)
	// Add line comment to the resource mapping node
	if resNode.HeadComment == "" {
		resNode.HeadComment = comment
	} else {
		resNode.HeadComment += "\n" + comment
	}
}

// replaceSchemaBlock replaces the entire schema: block with fresh introspection.
func replaceSchemaBlock(root *yaml.Node, tables []TableInfo, driver string) {
	// Remove existing schema key if present
	si := mappingIndex(root, "schema")
	if si >= 0 {
		root.Content = append(root.Content[:si], root.Content[si+2:]...)
	}

	// Build new schema node
	schema := convertSchema(tables, driver)
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(schema)
	_ = enc.Close()

	var schemaDoc yaml.Node
	if err := yaml.Unmarshal([]byte(buf.String()), &schemaDoc); err != nil {
		return // Should not happen
	}
	schemaRoot, _ := mappingOf(&schemaDoc)

	// Insert at the beginning (after version, before connections)
	// Find insertion point: after version, panel, before connections
	insertIdx := 0
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "connections" {
			insertIdx = i
			break
		}
	}

	// Insert schema key and value
	newContent := make([]*yaml.Node, 0, len(root.Content)+2)
	newContent = append(newContent, root.Content[:insertIdx]...)
	newContent = append(newContent,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "schema"},
		schemaRoot)
	newContent = append(newContent, root.Content[insertIdx:]...)
	root.Content = newContent
}

// updateConnectionDSN updates the default connection DSN.
func updateConnectionDSN(root *yaml.Node, dsn string) {
	ci := mappingIndex(root, "connections")
	if ci < 0 {
		return
	}
	conns := root.Content[ci+1]
	if conns.Kind != yaml.MappingNode {
		return
	}
	di := mappingIndex(conns, "default")
	if di < 0 {
		return
	}
	def := conns.Content[di+1]
	if def.Kind != yaml.MappingNode {
		return
	}
	dsi := mappingIndex(def, "dsn")
	if dsi >= 0 {
		def.Content[dsi+1].Value = dsn
	} else {
def.Content = append(def.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "dsn"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: dsn})
	}
}
