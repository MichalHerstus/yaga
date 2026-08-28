// Package luascript implements the request-time Lua runtime (gopher-lua) that
// executes script: hook bodies and script actions. It is used by the generated
// dashboard app at runtime and by the yaga binary for syntax checking and debug
// dry-runs (E6).
//
// The generated app embeds this file verbatim into internal/panel/luascript/.
package luascript

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const scriptTimeout = 5 * time.Second

// keepQuestion controls placeholder renumbering. When true (sqlite), "?" is
// kept as-is; when false (postgres/mssql), "?" is renumbered to $N outside
// string literals. Default is true; the generated app sets it via
// SetKeepQuestion based on the configured driver.
var keepQuestion = true

// SetKeepQuestion controls whether "?" placeholders are kept as-is (true,
// sqlite) or renumbered to $N (false, postgres/mssql). Must be called before
// Run.
func SetKeepQuestion(v bool) { keepQuestion = v }

// Scope holds the execution context passed to a Lua script.
type Scope struct {
	ID     int64
	Table  string
	Action string
	User   string
	Role   string
	Values map[string]interface{}
}

// Execer is the database interface satisfied by *sql.DB and *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// AbortError is raised by the Lua abort() host function.
type AbortError struct{ Msg string }

func (e *AbortError) Error() string { return e.Msg }

// IsAbort reports whether err (or any error in its chain) is an AbortError.
func IsAbort(err error) bool {
	for err != nil {
		if _, ok := err.(*AbortError); ok {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

const abortPrefix = "\x00yaga-abort:"

// Run executes the Lua code as the body of a run(ctx) function with the given
// scope. The db Execer is exposed to the script as db.exec, db.query and
// db.query_one. print output is written to the process stdout (the gopher-lua
// base print behaviour). Log output is written via the standard log package.
// Mutated ctx.values are written back into scope.Values in place.
func Run(ctx context.Context, db Execer, scope Scope, code string) error {
	return RunWithOutput(ctx, db, scope, code, os.Stdout)
}

// RunWithOutput runs a script like Run, but redirects the print output to out.
// The printed text accumulates exactly as the base print would write it
// (arguments joined with tabs, one line per call), so a caller can capture it
// instead of letting it reach the process stdout.
func RunWithOutput(ctx context.Context, db Execer, scope Scope, code string, out io.Writer) error {
	lctx, cancel := context.WithTimeout(ctx, scriptTimeout)
	defer cancel()

	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	L.SetContext(lctx)
	openLib(L, lua.OpenBase, lua.BaseLibName)
	openLib(L, lua.OpenTable, lua.TabLibName)
	openLib(L, lua.OpenString, lua.StringLibName)
	openLib(L, lua.OpenMath, lua.MathLibName)

	L.SetGlobal("ctx", newCtxTable(L, scope))
	dbTbl := L.NewTable()
	dbTbl.RawSetString("exec", L.NewFunction(func(L *lua.LState) int {
		query, args := luaQueryArgs(L)
		res, err := db.ExecContext(lctx, renumber(query), args...)
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		out := L.NewTable()
		if n, err := res.RowsAffected(); err == nil {
			L.SetField(out, "rows_affected", lua.LNumber(n))
		}
		if id, err := res.LastInsertId(); err == nil {
			L.SetField(out, "last_insert_id", lua.LNumber(id))
		}
		L.Push(out)
		return 1
	}))
	dbTbl.RawSetString("query", L.NewFunction(func(L *lua.LState) int {
		query, args := luaQueryArgs(L)
		rows, err := db.QueryContext(lctx, renumber(query), args...)
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		out := L.NewTable()
		i := 0
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for j := range vals {
				ptrs[j] = &vals[j]
			}
			if err := rows.Scan(ptrs...); err != nil {
				L.RaiseError("%s", err.Error())
			}
			row := L.NewTable()
			for j, c := range cols {
				L.SetField(row, c, goToLua(L, vals[j]))
			}
			i++
			L.SetTable(out, lua.LNumber(i), row)
		}
		if err := rows.Err(); err != nil {
			L.RaiseError("%s", err.Error())
		}
		L.Push(out)
		return 1
	}))
	dbTbl.RawSetString("query_one", L.NewFunction(func(L *lua.LState) int {
		query, args := luaQueryArgs(L)
		rows, err := db.QueryContext(lctx, renumber(query), args...)
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				L.RaiseError("%s", err.Error())
			}
			L.Push(lua.LNil)
			return 1
		}
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for j := range vals {
			ptrs[j] = &vals[j]
		}
		if err := rows.Scan(ptrs...); err != nil {
			L.RaiseError("%s", err.Error())
		}
		row := L.NewTable()
		for j, c := range cols {
			L.SetField(row, c, goToLua(L, vals[j]))
		}
		L.Push(row)
		return 1
	}))
	L.SetGlobal("db", dbTbl)
	L.SetGlobal("abort", L.NewFunction(func(L *lua.LState) int {
		L.RaiseError("%s%s", abortPrefix, L.CheckString(1))
		return 0
	}))
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		var sb strings.Builder
		top := L.GetTop()
		for i := 1; i <= top; i++ {
			if i > 1 {
				sb.WriteByte('\t')
			}
			sb.WriteString(L.ToStringMeta(L.Get(i)).String())
		}
		sb.WriteByte('\n')
		_, _ = io.WriteString(out, sb.String())
		return 0
	}))
	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int {
		log.Printf("[lua] %s", L.CheckString(1))
		return 0
	}))

	fn, err := L.LoadString("function run(ctx) " + code + "\nend")
	if err != nil {
		return err
	}
	if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
		return err
	}
	if err := L.CallByParam(lua.P{Fn: L.GetGlobal("run"), NRet: 0, Protect: true}, L.GetGlobal("ctx")); err != nil {
		if idx := strings.Index(err.Error(), abortPrefix); idx >= 0 {
			msg := err.Error()[idx+len(abortPrefix):]
			if nl := strings.IndexByte(msg, '\n'); nl >= 0 {
				msg = msg[:nl]
			}
			return &AbortError{Msg: msg}
		}
		return err
	}
	ct, ok := L.GetGlobal("ctx").(*lua.LTable)
	if !ok {
		return nil
	}
	if v, ok := luaToGo(L.GetField(ct, "values")).(map[string]interface{}); ok && scope.Values != nil {
		for k := range scope.Values {
			delete(scope.Values, k)
		}
		for k, val := range v {
			scope.Values[k] = val
		}
	}
	return nil
}

// SyntaxCheck parses code as a Lua run(ctx) function body without executing it,
// returning every syntax error with its line number.
func SyntaxCheck(code string) []SyntaxError {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	fn, err := L.LoadString("function run(ctx) " + code + "\nend")
	if err == nil {
		_ = fn
		return nil
	}
	msg := err.Error()
	var errs []SyntaxError
	for msg != "" {
		line := 0
		rest := msg
		if idx := strings.Index(msg, ":"); idx > 0 {
			if n, e := fmt.Sscanf(msg[idx+1:], "%d", &line); e == nil && n == 1 {
				rest = msg[idx+1:]
				if idx2 := strings.Index(rest, ":"); idx2 >= 0 {
					rest = rest[idx2+1:]
				}
			}
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			break
		}
		errs = append(errs, SyntaxError{Line: line, Message: rest})
		break
	}
	return errs
}

// SyntaxError is one Lua parse error from SyntaxCheck.
type SyntaxError struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

func openLib(L *lua.LState, fn lua.LGFunction, name string) {
	L.Push(L.NewFunction(fn))
	L.Push(lua.LString(name))
	L.Call(1, 0)
}

func newCtxTable(L *lua.LState, scope Scope) *lua.LTable {
	t := L.NewTable()
	L.SetField(t, "id", lua.LNumber(scope.ID))
	L.SetField(t, "table", lua.LString(scope.Table))
	L.SetField(t, "action", lua.LString(scope.Action))
	L.SetField(t, "user", lua.LString(scope.User))
	L.SetField(t, "role", lua.LString(scope.Role))
	values := L.NewTable()
	for k, v := range scope.Values {
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		L.SetField(values, k, goToLua(L, v))
	}
	L.SetField(t, "values", values)
	return t
}

func luaQueryArgs(L *lua.LState) (string, []interface{}) {
	query := L.CheckString(1)
	var args []interface{}
	for i := 2; i <= L.GetTop(); i++ {
		args = append(args, luaToGo(L.Get(i)))
	}
	return query, args
}

func luaToGo(v lua.LValue) interface{} {
	switch n := v.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(n)
	case lua.LString:
		return string(n)
	case lua.LNumber:
		f := float64(n)
		if f == float64(int64(f)) {
			return int64(f)
		}
		return f
	case *lua.LTable:
		m := map[string]interface{}{}
		n.ForEach(func(k, val lua.LValue) {
			m[lua.LVAsString(k)] = luaToGo(val)
		})
		return m
	default:
		return v.String()
	}
}

func goToLua(L *lua.LState, v interface{}) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(x)
	case string:
		return lua.LString(x)
	case int:
		return lua.LNumber(x)
	case int64:
		return lua.LNumber(x)
	case int32:
		return lua.LNumber(x)
	case float64:
		return lua.LNumber(x)
	case []byte:
		return lua.LString(string(x))
	case time.Time:
		return lua.LString(x.Format("2006-01-02T15:04:05"))
	case map[string]interface{}:
		t := L.NewTable()
		for k, val := range x {
			L.SetField(t, k, goToLua(L, val))
		}
		return t
	default:
		return lua.LString(fmt.Sprintf("%v", v))
	}
}

func renumber(sqlText string) string {
	if keepQuestion {
		return sqlText
	}
	var out strings.Builder
	n := 0
	i := 0
	ln := len(sqlText)
	for i < ln {
		c := sqlText[i]
		switch {
		case c == '\'':
			out.WriteByte(c)
			i++
			for i < ln {
				if sqlText[i] == '\'' {
					if i+1 < ln && sqlText[i+1] == '\'' {
						out.WriteString("''")
						i += 2
						continue
					}
					out.WriteByte('\'')
					i++
					break
				}
				out.WriteByte(sqlText[i])
				i++
			}
		case c == '"' || c == '[':
			close := byte(']')
			if c == '"' {
				close = '"'
			}
			out.WriteByte(c)
			i++
			for i < ln && sqlText[i] != close {
				out.WriteByte(sqlText[i])
				i++
			}
			if i < ln {
				out.WriteByte(sqlText[i])
				i++
			}
		case c == '-':
			if i+1 < ln && sqlText[i+1] == '-' {
				for i < ln && sqlText[i] != '\n' {
					out.WriteByte(sqlText[i])
					i++
				}
			} else {
				out.WriteByte(c)
				i++
			}
		case c == '/' && i+1 < ln && sqlText[i+1] == '*':
			for i+1 < ln && !(sqlText[i] == '*' && sqlText[i+1] == '/') {
				out.WriteByte(sqlText[i])
				i++
			}
			if i+1 < ln {
				out.WriteString("*/")
				i += 2
			}
		case c == '`':
			out.WriteByte(c)
			i++
			for i < ln && sqlText[i] != '`' {
				out.WriteByte(sqlText[i])
				i++
			}
			if i < ln {
				out.WriteByte(sqlText[i])
				i++
			}
		case c == '?':
			n++
			out.WriteString(fmt.Sprintf("$%d", n))
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}
