// procs.go
//
// Implements D6: SQLite "stored procedures" as named SQL-batch bodies stored
// in the sql_procedures table. SQLite has no real stored procedures, so the
// config's `procedures:` block declares a name, description and a multi-
// statement SQL body; the generator emits the sql_procedures DDL + INSERT OR
// IGNORE seeds into sql/migrations/procedures.sql and a shared
// internal/panel/procs package whose Exec(db, name, id) looks the body up at
// call time, splits it into individual statements (sqlite only runs the first
// statement of a multi-statement string) and executes them inside one
// transaction. Existing `proc:` fields on actions and hooks reference these
// names — the same YAML config works on all three drivers with three execution
// strategies: CALL on postgres, EXEC on mssql, procs.Exec on sqlite. On
// postgres/mssql the `procedures:` block is ignored entirely.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
)

// procPackageSrc is the full source of the generated internal/panel/procs
// package. splitStatements and containsPlaceholder must stay byte-identical to
// the testable copies below (the emitted file is the runtime source of truth
// for the generated app; the copies in this file let the generator unit tests
// exercise the logic without building the generated module).
const procPackageSrc = `package procs

import (
    "database/sql"
    "strings"
)

func Exec(db *sql.DB, name string, id int64) error {
    var body string
    if err := db.QueryRow("SELECT body FROM sql_procedures WHERE name = ?", name).Scan(&body); err != nil {
        return err
    }
    tx, err := db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()
    for _, stmt := range splitStatements(body) {
        stmt = strings.TrimSpace(stmt)
        if stmt == "" {
            continue
        }
        var rows *sql.Rows
        if containsPlaceholder(stmt) {
            rows, err = tx.Query(stmt, id)
        } else {
            rows, err = tx.Query(stmt)
        }
        if err != nil {
            return err
        }
        for rows.Next() {
        }
        if err := rows.Err(); err != nil {
            rows.Close()
            return err
        }
        rows.Close()
    }
    return tx.Commit()
}

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
        case c == '"' || c == '[':
            close := byte(']')
            switch c {
            case '"':
                close = '"'
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
        case c == '"' || c == '[':
            close := byte(']')
            switch c {
            case '"':
                close = '"'
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
`

// splitStatements splits a SQL batch into individual statements on top-level
// semicolons, ignoring semicolons inside single-quoted strings ('...', ” ),
// quoted identifiers ("..." / `...` / [...]), -- line comments and /* */
// block comments. This is the unit-testable copy of the function embedded in
// procPackageSrc — keep both in sync.
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
		case c == '"' || c == '[':
			close := byte(']')
			switch c {
			case '"':
				close = '"'
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

// containsPlaceholder reports whether a SQL statement contains a numbered
// placeholder ($1, $2, ...) outside of string literals, quoted identifiers and
// comments. The sqlite drivers (modernc.org/sqlite and mattn/go-sqlite3) bind
// $N positionally but error when more arguments are passed than the statement
// has placeholders, so the generated procs executor only binds the record id
// for statements that reference it.
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
		case c == '"' || c == '[':
			close := byte(']')
			switch c {
			case '"':
				close = '"'
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

// procedureByName returns the declared procedure matching name, or nil when it
// is undeclared or name is empty.
// Params: name (the procedure identifier referenced by a proc: field).
// Returns: the matching *types.Procedure, or nil.
func (g *Generator) procedureByName(name string) *types.Procedure {
	if name == "" {
		return nil
	}
	for i := range g.Config.Procedures {
		if g.Config.Procedures[i].Name == name {
			return &g.Config.Procedures[i]
		}
	}
	return nil
}

// usesProcedures reports whether the generator should emit the sqlite
// sql_procedures machinery: the driver must be sqlite and at least one
// procedure must be declared. Postgres and mssql ignore the block entirely.
// Returns: true when sqlite and len(Procedures) > 0.
func (g *Generator) usesProcedures() bool {
	return g.isSQLite() && len(g.Config.Procedures) > 0
}

// procImport returns the procs package import line for a handler that emits
// procs.Exec calls, or "" when nothing is emitted for the configured driver.
// The generator only emits sqlite procs.Exec for declared procedures, so this
// is gated on the emitted-dir having sqlite procedures.
// Returns: the "    procs <module>/internal/panel/procs" line, or "".
func (g *Generator) procImport() string {
	if !g.usesProcedures() {
		return ""
	}
	return fmt.Sprintf("    procs %q\n", g.moduleImport("internal/panel/procs"))
}

// hookUsesProc reports whether a Hooks block contains a proc hook that is
// emitted as a procs.Exec call: only declared procedures on sqlite (postgres
// and mssql proc hooks emit CALL/EXEC and never reference the procs package).
// Params: h (the hooks block; nil is valid).
// Returns: true when a proc hook emits procs.Exec.
func (g *Generator) hookUsesProc(h *types.Hooks) bool {
	if h == nil {
		return false
	}
	for _, list := range [][]types.Hook{h.Before, h.After} {
		for _, hook := range list {
			if hook.Proc != "" && g.procedureByName(hook.Proc) != nil {
				return true
			}
		}
	}
	return false
}

// actionProcExec returns the Go snippet that runs a declared SQLite SQL-batch
// procedure for an action, or "" when the action is not a sqlite proc. The
// snippet is indented to sit inside an action switch case (12 spaces) or a
// bulk id loop (12 spaces). Postgres/mssql proc actions go through
// actionExecSQL instead.
// Params: a (the action definition), idExpr (the Go expression holding the
// record id, e.g. "int64(id)" in actions.go or "id" in bulk.go).
// Returns: the procs.Exec call with error handling, or "".
func (g *Generator) actionProcExec(a types.Action, idExpr string) string {
	if a.Proc == "" || g.procedureByName(a.Proc) == nil {
		return ""
	}
	return fmt.Sprintf(`            if err := procs.Exec(db, %q, %s); err != nil {
                httperr.Internal(w, err)
                return
            }`, a.Proc, idExpr)
}

// generateProcedures writes the sqlite sql_procedures DDL + INSERT OR IGNORE
// seeds into sql/migrations/procedures.sql and the internal/panel/procs
// package (procs.Exec + the statement splitter) into the output directory,
// when the driver is sqlite and at least one procedure is declared. The seeds
// are idempotent: an existing row (e.g. one applied by a previous migration
// run with a newer body) is never overwritten.
// Returns: an error on write failure.
func (g *Generator) generateProcedures() error {
	if !g.usesProcedures() {
		return nil
	}
	var seeds strings.Builder
	for _, p := range g.Config.Procedures {
		body := strings.ReplaceAll(p.SQL, "'", "''")
		desc := strings.ReplaceAll(p.Description, "'", "''")
		seeds.WriteString(fmt.Sprintf("INSERT OR IGNORE INTO sql_procedures (name, body, description) VALUES ('%s', '%s', '%s');\n", p.Name, body, desc))
	}
	code := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS sql_procedures (
    name TEXT PRIMARY KEY,
    body TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

%s`, seeds.String())
	dir := filepath.Join(g.OutDir, "sql", "migrations")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "procedures.sql"), []byte(code), 0644); err != nil {
		return err
	}
	dir = filepath.Join(g.OutDir, "internal/panel/procs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "procs.go"), []byte(procPackageSrc), 0644)
}
