// livedb.go — resolves the live business-database connection used by the wedit
// schema browser ("Show data") and the sample-data stub seeder.
//
// An explicit --db flag always wins. When wedit is started without it (the
// common case — the DSN already lives in yaga.yaml's connections: block) the
// first configured connection is used, so "Show data" queries the real
// database instead of falling back to the empty in-memory stub.
package serve

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
)

// liveConnection returns the DSN and database/sql driver for the live business
// database. The --db flag (with its optional driver override) wins; otherwise
// the first configured connection is used, falling back to the DATABASE_URL
// environment variable and then the `.ENV` file next to the config.
func (s *Server) liveConnection() (dsn, driver string) {
	s.mu.RLock()
	cfg, configPath := s.cfg, s.configPath
	flagDSN, flagDriver := s.liveDSN, s.liveDriver
	s.mu.RUnlock()

	if flagDSN != "" {
		// A DSN typed on the command line is relative to the process working
		// directory, so it is passed through unchanged.
		return flagDSN, driverForDSN(flagDriver, flagDSN)
	}
	return configLiveConnection(cfg, configPath)
}

// configLiveConnection derives the live DSN from the config's first
// connections: entry. A relative SQLite file DSN is resolved against the
// directory holding the config, because the `dsn:` in yaga.yaml is authored
// relative to the config folder (the same rule the generator applies when it
// writes .ENV).
func configLiveConnection(cfg *types.Config, configPath string) (dsn, driver string) {
	baseDir := "."
	if configPath != "" {
		baseDir = filepath.Dir(configPath)
	}

	if cfg != nil {
		for _, conn := range cfg.Connections {
			driver, dsn = conn.Driver, conn.DSN
			break
		}
	}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = envFileValue(filepath.Join(baseDir, ".ENV"), "DATABASE_URL")
	}
	if dsn == "" {
		return "", ""
	}

	resolved := driverForDSN(driver, dsn)
	if resolved == "sqlite" {
		dsn = sqliteDSNFromBase(dsn, baseDir)
	}
	return dsn, resolved
}

// sqliteDSNFromBase resolves a relative SQLite file DSN against baseDir.
// In-memory databases, absolute paths and `mode=memory` URIs pass through
// unchanged; a query string/fragment is preserved.
func sqliteDSNFromBase(dsn, baseDir string) string {
	prefix, rest := "", dsn
	if strings.HasPrefix(dsn, "file:") {
		prefix, rest = "file:", strings.TrimPrefix(dsn, "file:")
	}
	query := ""
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		query, rest = rest[i:], rest[:i]
	}
	if rest == "" || strings.HasPrefix(rest, ":") || strings.Contains(query, "mode=memory") || filepath.IsAbs(rest) {
		return dsn
	}
	if baseDir == "" {
		baseDir = "."
	}
	abs, err := filepath.Abs(filepath.Join(baseDir, rest))
	if err != nil {
		return dsn
	}
	return prefix + filepath.ToSlash(abs) + query
}

// envFileValue reads KEY=value from a dotenv-style file, returning "" when the
// file or the key is absent. Used as a last-resort DSN source so a wedit
// session also works when the connection string lives in .ENV (where
// `yaga generate` writes it).
func envFileValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		if strings.TrimSpace(line[:eq]) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
	}
	return ""
}

// queryLive runs an arbitrary SELECT against the live database and returns rows
// as an array of column->value maps. The connection is closed before returning.
// Used by the preview for widget data and resource rows.
func queryLive(driver, dsn, sqlText string) ([]map[string]interface{}, error) {
	driver = driverForDSN(driver, dsn)
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening live database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging live database: %w", err)
	}

	rows, err := db.Query(sqlText)
	if err != nil {
		return nil, fmt.Errorf("querying live database: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}

	var out []map[string]interface{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for j := range vals {
			ptrs[j] = &vals[j]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make(map[string]interface{}, len(cols))
		for j, c := range cols {
			row[c] = vals[j]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}
	return out, nil
}

// queryLiveExec runs an arbitrary SQL statement against the live database.
// If the statement is a SELECT it returns rows; otherwise it returns rows
// affected. The connection is closed before returning. When `id` is non-zero
// and the SQL contains a $1 placeholder, id is bound to it.
func queryLiveExec(driver, dsn, sqlText string, id int64) (*ResultWrapper, error) {
	driver = driverForDSN(driver, dsn)
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening live database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging live database: %w", err)
	}

	trimmed := strings.TrimSpace(sqlText)
	isSelect := strings.HasPrefix(strings.ToUpper(trimmed), "SELECT")

	var args []interface{}
	if id != 0 && strings.Contains(sqlText, "$1") {
		args = append(args, id)
	}

	if isSelect {
		rows, err := db.Query(sqlText, args...)
		if err != nil {
			return nil, fmt.Errorf("querying live database: %w", err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("reading columns: %w", err)
		}
		var rowsOut []map[string]interface{}
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for j := range vals {
				ptrs[j] = &vals[j]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, fmt.Errorf("scanning row: %w", err)
			}
			row := make(map[string]interface{}, len(cols))
			for j, c := range cols {
				row[c] = vals[j]
			}
			rowsOut = append(rowsOut, row)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating rows: %w", err)
		}
		return &ResultWrapper{Rows: rowsOut}, nil
	}

	res, err := db.ExecContext(context.Background(), sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("executing live SQL: %w", err)
	}
	ra, _ := res.RowsAffected()
	li, _ := res.LastInsertId()
	return &ResultWrapper{
		RowsAffected: ra,
		LastInsertID: li,
	}, nil
}

// queryLiveDryRun validates that a SQL statement can be prepared against the
// live database without executing it. Returns nil when the statement is valid.
func queryLiveDryRun(driver, dsn, sqlText string) error {
	driver = driverForDSN(driver, dsn)
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return fmt.Errorf("opening live database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("pinging live database: %w", err)
	}
	_, err = db.Prepare(sqlText)
	if err != nil {
		return fmt.Errorf("preparing SQL: %w", err)
	}
	return nil
}

// ResultWrapper mirrors RunSQLResult shape for JSON responses.
type ResultWrapper struct {
	Rows         []map[string]interface{} `json:"rows,omitempty"`
	RowsAffected int64                    `json:"rows_affected,omitempty"`
	LastInsertID int64                    `json:"last_insert_id,omitempty"`
}
