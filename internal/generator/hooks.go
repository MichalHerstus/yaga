// hooks.go
//
// Generates the shared internal/hooks package and the Go source snippets that
// invoke lifecycle hooks from the resource handlers. Each declared fn hook
// gets a compile-ready stub in internal/hooks/hooks.go that the user fills in;
// sql hooks are inlined by the handler generators as db.ExecContext calls.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
)

// generateHooks writes internal/hooks/hooks.go into the output directory when
// any hook is declared anywhere in the config (fn, sql or script) or when a
// plugin contributed a hook source file (which references Scope from the same
// package, so the struct must exist). The file defines the Scope struct shared
// by all hook calls (handlers reference hooks.Scope for every hook block,
// script/sql-only included) plus one stub per unique fn hook name that is not
// backed by a plugin hook source; script/sql-only hooks produce the Scope
// struct with no stubs. Nothing is written when no hooks are declared and no
// plugin hook sources exist.
// Returns: an error on write failure.
func (g *Generator) generateHooks() error {
	if !g.hasAnyHooks() && len(g.pluginHookFiles) == 0 {
		return nil
	}

	fns := g.collectFnHooks()
	var stubs strings.Builder
	for _, fn := range fns {
		stubs.WriteString(fmt.Sprintf("\n// %s - one stub per declared fn hook; user implements.\n", fn))
		stubs.WriteString(fmt.Sprintf("func %s(ctx context.Context, db *sql.DB, s Scope) error { return nil }\n", fn))
	}

	// The context/database/sql imports are only needed when fn-hook stubs are
	// emitted; a sql-only hooks.go must stay import-free to compile.
	imports := ""
	if len(fns) > 0 {
		imports = `
import (
    "context"
    "database/sql"
)
`
	}

	code := fmt.Sprintf(`package hooks
%s
type Scope struct {
    ID     int64
    Table  string
    Action string // create|update|delete|<actionName>
    Values map[string]interface{}
}
%s`, imports, stubs.String())

	dir := filepath.Join(g.OutDir, "internal/hooks")
	return os.WriteFile(filepath.Join(dir, "hooks.go"), []byte(code), 0644)
}

// hasAnyHooks reports whether any hook block (before or after, fn or sql) is
// declared on any form action or custom action anywhere in the config.
// Returns: true when at least one Hooks block is non-nil.
func (g *Generator) hasAnyHooks() bool {
	for _, r := range g.Config.Resources {
		if r.Form != nil {
			for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update, r.Form.Delete} {
				if fa != nil && fa.Hooks != nil {
					return true
				}
			}
		}
		for _, a := range r.Actions {
			if a.Hooks != nil {
				return true
			}
		}
	}
	return false
}

// collectFnHooks walks every resource in config order, gathering the unique,
// non-empty fn hook names from the form actions (create/update/delete) and the
// custom actions that are NOT backed by a plugin hook source (those skip stub
// generation). Order is preserved and duplicates are dropped.
// Returns: the deduplicated list of fn hook names needing stubs.
func (g *Generator) collectFnHooks() []string {
	seen := map[string]bool{}
	var fns []string
	for _, r := range g.Config.Resources {
		if r.Form != nil {
			for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update, r.Form.Delete} {
				if fa == nil || fa.Hooks == nil {
					continue
				}
				for _, h := range fa.Hooks.Before {
					if h.Fn != "" && !g.pluginFnNames[h.Fn] && !seen[h.Fn] {
						seen[h.Fn] = true
						fns = append(fns, h.Fn)
					}
				}
				for _, h := range fa.Hooks.After {
					if h.Fn != "" && !g.pluginFnNames[h.Fn] && !seen[h.Fn] {
						seen[h.Fn] = true
						fns = append(fns, h.Fn)
					}
				}
			}
		}
		for _, a := range r.Actions {
			if a.Hooks == nil {
				continue
			}
			for _, h := range a.Hooks.Before {
				if h.Fn != "" && !g.pluginFnNames[h.Fn] && !seen[h.Fn] {
					seen[h.Fn] = true
					fns = append(fns, h.Fn)
				}
			}
			for _, h := range a.Hooks.After {
				if h.Fn != "" && !g.pluginFnNames[h.Fn] && !seen[h.Fn] {
					seen[h.Fn] = true
					fns = append(fns, h.Fn)
				}
			}
		}
	}
	return fns
}

// hookCallsStr renders the Go source that runs a before/after hook list against
// a Scope variable. fn hooks call the generated stub in the hooks package; sql
// hooks are inlined as db.ExecContext binding the current scope id ($1); script
// hooks run the Lua body via luascript.Run against db with the scope's
// id/table/action plus the authenticated user and role; proc hooks call the
// stored procedure driver-appropriately — CALL on postgres, EXEC on mssql, and
// procs.Exec(db, name, scope.ID) on sqlite when the procedure is declared under
// procedures: (undeclared sqlite proc hooks are skipped, so a config without a
// procedures: block stays byte-identical). A hook error aborts the request with
// a 500 response; a script abort() returns a 400 with its message; a script
// redirect() / redirect_to() redirects to the returned path.
// Params: hooks (the before or after list), scopeVar (name of the Scope var),
// indent (the leading whitespace for each emitted line).
// Returns: the Go source lines (empty when the list is empty or all hooks are
// proc hooks on sqlite that are skipped).
func (g *Generator) hookCallsStr(hooks []types.Hook, scopeVar, indent string) string {
	var lines []string
	for _, h := range hooks {
		switch {
		case h.Fn != "":
			lines = append(lines, fmt.Sprintf(`%sif err := hooks.%s(r.Context(), db, %s); err != nil {
%s    httperr.Internal(w, err)
%s    return
%s}`, indent, h.Fn, scopeVar, indent, indent, indent))
		case h.SQL != "":
			lines = append(lines, fmt.Sprintf(`%sif _, err := db.ExecContext(r.Context(), %q, %s.ID); err != nil {
%s    httperr.Internal(w, err)
%s    return
%s}`, indent, h.SQL, scopeVar, indent, indent, indent))
		case h.Script != "":
			call := fmt.Sprintf(`if err := luascript.Run(r.Context(), db, luascript.Scope{
    ID:     %s.ID,
    Table:  %s.Table,
    Action: %s.Action,
    User:   auth.UserName(r),
    Role:   auth.RoleName(r),
    Values: %s.Values,
}, %q); err != nil {
    if luascript.IsRedirect(err) {
        http.Redirect(w, r, err.URL, http.StatusFound)
        return
    }
    if luascript.IsAbort(err) {
        httperr.BadRequest(w, err.Error())
        return
    }
    httperr.Internal(w, err)
    return
}`, scopeVar, scopeVar, scopeVar, scopeVar, h.Script)
			lines = append(lines, indentBlock(call, indent))
		case h.Proc != "" && g.isSQLite() && g.procedureByName(h.Proc) != nil:
			lines = append(lines, fmt.Sprintf(`%sif err := procs.Exec(db, %q, %s.ID); err != nil {
%s    httperr.Internal(w, err)
%s    return
%s}`, indent, h.Proc, scopeVar, indent, indent, indent))
		case h.Proc != "" && !g.isSQLite():
			lines = append(lines, fmt.Sprintf(`%sif _, err := db.ExecContext(r.Context(), %q, %s.ID); err != nil {
%s    httperr.Internal(w, err)
%s    return
%s}`, indent, g.procSQL(h.Proc), scopeVar, indent, indent, indent))
		}
	}
	return strings.Join(lines, "\n")
}

// indentBlock prefixes every non-empty line of a Go source block with indent.
// Params: block (multi-line Go source), indent (the leading whitespace).
// Returns: the re-indented block.
func indentBlock(block, indent string) string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		if line == "" {
			out = append(out, "")
		} else {
			out = append(out, indent+line)
		}
	}
	return strings.Join(out, "\n")
}

// hookBlockEmits reports whether a Hooks block emits any code for the
// configured driver. fn and sql hooks always emit; proc hooks emit when the
// driver is postgres or mssql, or on sqlite when the procedure is declared
// under procedures: (a proc-only block with no matching body on sqlite emits
// nothing). This is the driver-aware replacement for the "Hooks != nil" checks:
// a proc-only block on sqlite with no declared body must not force the hooks
// import, the Scope literal or the RETURNING id capture, or the generated
// handler fails to compile with an unused import.
// Params: h (the hooks block; nil is valid).
// Returns: true when at least one hook produces a generated call.
func (g *Generator) hookBlockEmits(h *types.Hooks) bool {
	if h == nil {
		return false
	}
	for _, list := range [][]types.Hook{h.Before, h.After} {
		for _, hook := range list {
			if hook.Fn != "" || hook.SQL != "" || hook.Script != "" {
				return true
			}
			if hook.Proc != "" && (!g.isSQLite() || g.procedureByName(hook.Proc) != nil) {
				return true
			}
		}
	}
	return false
}

// procSQL renders the driver-specific stored procedure invocation used to run
// a proc hook or action, binding the record id as the single argument.
// Postgres emits the SQL-standard "CALL name($1)"; mssql emits "EXEC name $1"
// (the go-mssqldb loose placeholder mode rewrites $1 to a bound @p1 passed
// positionally to the proc's first parameter). SQLite has no stored procedures
// and returns "" — callers skip the emission entirely.
// Params: name (the stored procedure name, optionally schema-qualified).
// Returns: the SQL call text, or "" on sqlite.
func (g *Generator) procSQL(name string) string {
	if g.isMSSQL() {
		return "EXEC " + name + " $1"
	}
	return "CALL " + name + "($1)"
}

// scopeValuesStr renders the Values map literal for a create/update Scope,
// referencing the handler's vals slice by index so password/file expressions
// are not duplicated. The id column is skipped (it is not part of the form
// params).
// Params: cols (form column names in vals order).
// Returns: the "key: vals[n]," entries (empty for an empty cols list).
func scopeValuesStr(cols []string) string {
	var entries []string
	for i, c := range cols {
		entries = append(entries, fmt.Sprintf("            %q: vals[%d],", c, i))
	}
	return strings.Join(entries, "\n")
}

// valuesWriteBack renders the loop that copies a before-script's ctx.values
// mutations back into the handler's vals slice by column name, so a script can
// set defaults or transform form values before the INSERT/UPDATE executes. The
// scope.Values map is shared with the Lua runtime (in-place), so reading it
// after the Run call yields the script's output. It is only emitted when the
// create/update hook block declares a script: hook (feature-off output stays
// byte-identical).
// Params: cols (form column names in vals order), indent (the leading whitespace).
// Returns: the loop source (empty for an empty cols list).
func valuesWriteBack(cols []string, indent string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	return fmt.Sprintf(`%sfor i, c := range []string{%s} {
%s    if v, ok := scope.Values[c]; ok {
%s        vals[i] = v
%s    }
%s}`, indent, strings.Join(quoted, ", "), indent, indent, indent, indent)
}

// returningClause returns the SQL fragment appended to the create query to
// capture the inserted row key: " RETURNING <id>" for postgres/sqlite and the
// T-SQL " OUTPUT INSERTED.<id>" for mssql (which has no RETURNING clause).
// Params: r (the resource definition; used for its id column override).
// Returns: the SQL fragment including its leading space.
func (g *Generator) returningClause(r types.Resource) string {
	col := g.quoteIdent(idColumn(r))
	if g.isMSSQL() {
		return " OUTPUT INSERTED." + col
	}
	return " RETURNING " + col
}
