// sqlrun.go — E6 debug dry-run infrastructure: in-memory sqlite stub builder,
// live-DB seeder (up to 100 rows per schema table), recording Execer, single
// and batch SQL runners, and copies of splitStatements/containsPlaceholder
// (parity-tested against the generator copy in procs.go).
package serve

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/MichalHerstus/yaga/internal/types"

	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/yuin/gopher-lua"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// stubDBKey is the context key for the in-memory sqlite stub.
type stubDBKey struct{}

// BuildStubDB creates an in-memory sqlite database with tables from the schema
// block. Foreign key enforcement is disabled so row-copy order is irrelevant.
func BuildStubDB(cfg *types.Config) (*sql.DB, error) {
	if cfg.Schema == nil {
		return nil, fmt.Errorf("no schema block")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("opening stub: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		db.Close()
		return nil, err
	}
	for _, t := range cfg.Schema.Tables {
		cols := make([]string, 0, len(t.Columns))
		for _, c := range t.Columns {
			cols = append(cols, fmt.Sprintf("%s %s", quoteIdentStub(c.Name), sqliteType(c.Type)))
		}
		ddl := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", quoteIdentStub(t.Name), strings.Join(cols, ", "))
		if _, err := db.Exec(ddl); err != nil {
			db.Close()
			return nil, fmt.Errorf("creating %s: %w", t.Name, err)
		}
	}
	return db, nil
}

// SeedFromDB opens the live database at dsn with the given driver, selects at
// most maxRows (100) rows per schema table, and inserts them into the stub.
// Tables missing in the live DB are skipped (not fatal). An unreachable DB
// leaves the stub empty.
func SeedFromDB(dsn, driver string, cfg *types.Config, stub *sql.DB) error {
	if cfg.Schema == nil || dsn == "" {
		return nil
	}
	live, err := sql.Open(driver, dsn)
	if err != nil {
		return nil // unreachable DB → empty stub
	}
	defer live.Close()
	if err := live.Ping(); err != nil {
		return nil // unreachable DB → empty stub
	}
	const maxRows = 100
	for _, t := range cfg.Schema.Tables {
		cols := make([]string, 0, len(t.Columns))
		for _, c := range t.Columns {
			cols = append(cols, quoteIdentStub(c.Name))
		}
		query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), quoteIdentStub(t.Name))
		if driver == "mssql" {
			query = fmt.Sprintf("SELECT TOP %d %s FROM %s", maxRows, strings.Join(cols, ", "), quoteIdentStub(t.Name))
		} else {
			query += fmt.Sprintf(" LIMIT %d", maxRows)
		}
		rows, err := live.Query(query)
		if err != nil {
			continue // table might not exist
		}
		colsOut, err := rows.Columns()
		if err != nil {
			rows.Close()
			continue
		}
		placeholders := make([]string, len(colsOut))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		insert := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			quoteIdentStub(t.Name), strings.Join(colsOut, ", "), strings.Join(placeholders, ", "))
		tx, err := stub.Begin()
		if err != nil {
			rows.Close()
			continue
		}
		stmt, err := tx.Prepare(insert)
		if err != nil {
			tx.Rollback()
			rows.Close()
			continue
		}
		for rows.Next() {
			vals := make([]interface{}, len(colsOut))
			ptrs := make([]interface{}, len(colsOut))
			for j := range vals {
				ptrs[j] = &vals[j]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			converted := make([]interface{}, len(vals))
			for j, v := range vals {
				converted[j] = coerceForSQLite(v)
			}
			stmt.Exec(converted...)
		}
		rows.Close()
		stmt.Close()
		tx.Commit()
	}
	return nil
}

// coerceForSQLite converts Go values to sqlite-friendly storage.
func coerceForSQLite(v interface{}) interface{} {
	switch x := v.(type) {
	case time.Time:
		return x.Format(time.RFC3339)
	case []byte:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	default:
		return v
	}
}

// RecordingExecer wraps a *sql.DB and records every SQL call.
type RecordingExecer struct {
	db   *sql.DB
	Calls []RecordedCall
}

// RecordedCall is one captured SQL call.
type RecordedCall struct {
	SQL  string        `json:"sql"`
	Args []interface{} `json:"args"`
}

// NewRecordingExecer wraps db for recording.
func NewRecordingExecer(db *sql.DB) *RecordingExecer {
	return &RecordingExecer{db: db}
}

func (r *RecordingExecer) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	r.Calls = append(r.Calls, RecordedCall{SQL: query, Args: args})
	return r.db.ExecContext(ctx, query, args...)
}

func (r *RecordingExecer) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	r.Calls = append(r.Calls, RecordedCall{SQL: query, Args: args})
	return r.db.QueryContext(ctx, query, args...)
}

func (r *RecordingExecer) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	r.Calls = append(r.Calls, RecordedCall{SQL: query, Args: args})
	return r.db.QueryRowContext(ctx, query, args...)
}

// RunSQLResult holds the result of a single SQL execution.
type RunSQLResult struct {
	SQL           string        `json:"sql"`
	Args          []interface{} `json:"args"`
	Rows          []map[string]interface{} `json:"rows,omitempty"`
	RowsAffected  *int64        `json:"rows_affected,omitempty"`
	LastInsertID  *int64        `json:"last_insert_id,omitempty"`
	Error         string        `json:"error,omitempty"`
	Skipped       bool          `json:"skipped,omitempty"`
}

// RunSQL executes a single SQL statement against the stub with $1 bound to id.
// If the statement contains no placeholder, id is not passed.
func RunSQL(ctx context.Context, stub *sql.DB, sqlText string, id int64) RunSQLResult {
	args := []interface{}{}
	if containsPlaceholder(sqlText) {
		args = append(args, id)
	}
	result := RunSQLResult{SQL: sqlText, Args: args}

	// Try QueryContext first (SELECT-style).
	if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(sqlText)), "SELECT") {
		rows, err := stub.QueryContext(ctx, sqlText, args...)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			result.Error = err.Error()
			return result
		}
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for j := range vals {
				ptrs[j] = &vals[j]
			}
			if err := rows.Scan(ptrs...); err != nil {
				result.Error = err.Error()
				return result
			}
			row := make(map[string]interface{})
			for j, c := range cols {
				row[c] = vals[j]
			}
			result.Rows = append(result.Rows, row)
		}
		if err := rows.Err(); err != nil {
			result.Error = err.Error()
		}
		return result
	}

	// ExecContext for INSERT/UPDATE/DELETE.
	res, err := stub.ExecContext(ctx, sqlText, args...)
	if err != nil {
		// Fallback: try QueryContext (e.g. RETURNING).
		rows, qErr := stub.QueryContext(ctx, sqlText, args...)
		if qErr != nil {
			result.Error = err.Error()
			return result
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			result.Error = err.Error()
			return result
		}
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for j := range vals {
				ptrs[j] = &vals[j]
			}
			if err := rows.Scan(ptrs...); err != nil {
				result.Error = err.Error()
				return result
			}
			row := make(map[string]interface{})
			for j, c := range cols {
				row[c] = vals[j]
			}
			result.Rows = append(result.Rows, row)
		}
		if err := rows.Err(); err != nil {
			result.Error = err.Error()
		}
		return result
	}
	ra, _ := res.RowsAffected()
	li, _ := res.LastInsertId()
	result.RowsAffected = &ra
	if li != 0 {
		result.LastInsertID = &li
	}
	return result
}

// RunSQLBatch splits a procedure body into statements and runs them in one
// transaction, mirroring procs.Exec in the generated app.
func RunSQLBatch(ctx context.Context, stub *sql.DB, body string, id int64) []RunSQLResult {
	stmts := splitStatements(body)
	var results []RunSQLResult
	tx, err := stub.BeginTx(ctx, nil)
	if err != nil {
		for range stmts {
			results = append(results, RunSQLResult{Error: err.Error(), Skipped: true})
		}
		return results
	}
	for _, s := range stmts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		args := []interface{}{}
		if containsPlaceholder(s) {
			args = append(args, id)
		}
		r := RunSQLResult{SQL: s, Args: args}
		res, err := tx.ExecContext(ctx, s, args...)
		if err != nil {
			r.Error = err.Error()
			results = append(results, r)
			tx.Rollback()
			// Remaining stmts are skipped.
			for i := len(results); i < len(stmts); i++ {
				results = append(results, RunSQLResult{SQL: strings.TrimSpace(stmts[i]), Skipped: true})
			}
			return results
		}
		ra, _ := res.RowsAffected()
		li, _ := res.LastInsertId()
		r.RowsAffected = &ra
		if li != 0 {
			r.LastInsertID = &li
		}
		results = append(results, r)
	}
	tx.Commit()
	return results
}

// splitStatements splits a SQL batch on top-level semicolons, respecting
// string literals and identifiers. Copied from internal/generator/procs.go
// (must stay in parity — tested by sqlrun_test.go).
func splitStatements(sql string) []string {
	var stmts []string
	var cur strings.Builder
	i, n := 0, len(sql)
	for i < n {
		c := sql[i]
		switch {
		case c == '\'':
			cur.WriteByte(c)
			i++
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						cur.WriteString("''")
						i += 2
						continue
					}
					cur.WriteByte('\'')
					i++
					break
				}
				cur.WriteByte(sql[i])
				i++
			}
		case c == '"' || c == '[' || c == '`':
			close := byte('"')
			switch c {
			case '[':
				close = ']'
			case '`':
				close = '`'
			}
			cur.WriteByte(c)
			i++
			for i < n && sql[i] != close {
				cur.WriteByte(sql[i])
				i++
			}
			if i < n {
				cur.WriteByte(sql[i])
				i++
			}
		case c == '-' && i+1 < n && sql[i+1] == '-':
			for i < n && sql[i] != '\n' {
				cur.WriteByte(sql[i])
				i++
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				cur.WriteByte(sql[i])
				i++
			}
			if i+1 < n {
				cur.WriteString("*/")
				i += 2
			}
		case c == ';':
			stmts = append(stmts, cur.String())
			cur.Reset()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		stmts = append(stmts, cur.String())
	}
	return stmts
}

// containsPlaceholder reports whether sqlText contains a $N placeholder
// outside string literals and identifiers. Copied from internal/generator/
// procs.go (must stay in parity — tested by sqlrun_test.go).
func containsPlaceholder(sql string) bool {
	i, n := 0, len(sql)
	for i < n {
		c := sql[i]
		switch {
		case c == '\'':
			i++
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case c == '"' || c == '[' || c == '`':
			close := byte('"')
			switch c {
			case '[':
				close = ']'
			case '`':
				close = '`'
			}
			i++
			for i < n && sql[i] != close {
				i++
			}
			if i < n {
				i++
			}
		case c == '-' && i+1 < n && sql[i+1] == '-':
			for i < n && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
		case c == '$' && i+1 < n && sql[i+1] >= '0' && sql[i+1] <= '9':
			return true
		default:
			i++
		}
	}
	return false
}

// quoteIdentStub quotes an identifier for sqlite (double quotes).
func quoteIdentStub(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// sqliteType maps yaga field types to sqlite column types.
func sqliteType(yagaType string) string {
	switch yagaType {
	case "integer":
		return "INTEGER"
	case "string":
		return "TEXT"
	case "boolean":
		return "INTEGER"
	case "datetime":
		return "TEXT"
	case "float":
		return "REAL"
	case "json":
		return "TEXT"
	case "file":
		return "BLOB"
	default:
		return "TEXT"
	}
}

// openLiveDB opens a database connection for the given driver and DSN.
func openLiveDB(driver, dsn string) (*sql.DB, error) {
	return sql.Open(driver, dsn)
}

// driverForDSN detects the driver from the DSN prefix or returns the provided
// driver value.
func driverForDSN(driver, dsn string) string {
	if driver != "" {
		return driver
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return "pgx"
	}
	return "sqlite"
}

// parseIntID safely parses a string to int64.
func parseIntID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
