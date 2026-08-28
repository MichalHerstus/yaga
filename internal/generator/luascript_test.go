package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
)

// scriptConfig returns a minimal config exercising script: hooks (create
// before, delete after) and one bulk script: action, on the given driver.
func scriptConfig(driver string) *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: driver, DSN: "x"},
		},
		Resources: []types.Resource{
			{
				Name:  "User",
				Label: "User",
				List: &types.ListConfig{
					Columns: []types.Column{{Name: "name", Label: "Name"}},
				},
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "text"},
							{Name: "status", Type: "text"},
						},
						Hooks: &types.Hooks{
							Before: []types.Hook{{
								Name:   "default_status",
								Script: "if ctx.values[\"status\"] == nil then\n  ctx.values[\"status\"] = \"draft\"\nend",
							}},
						},
					},
					Delete: &types.FormAction{
						Hooks: &types.Hooks{
							After: []types.Hook{{
								Name:   "log_delete",
								Script: `db.exec("INSERT INTO events (msg) VALUES ('deleted')", ctx.id)`,
							}},
						},
					},
				},
				Actions: []types.Action{
					{
						Name: "archive",
						Bulk: true,
						Script: `if ctx.values["status"] == "archived" then
  abort("Already archived")
end
db.exec("UPDATE users SET status = 'archived' WHERE id = ?", ctx.id)`,
					},
					{Name: "deactivate", Query: "UPDATE users SET status = 'inactive' WHERE id = $1"},
				},
			},
		},
	}
}

// TestGenerateScriptPostgres ensures a postgres config with script: hooks and a
// scripted action emits the luascript runtime (keepQuestion false), the
// hook/action call sites (with abort flash + BadRequest paths, ctx.values
// write-back), the conditional gopher-lua go.mod dep, auth.RoleName and
// httperr.BadRequest, and that everything parses.
func TestGenerateScriptPostgres(t *testing.T) {
	dir := t.TempDir()
	g := New(scriptConfig("postgres"), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	main, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(main), `luascript.SetKeepQuestion(false)`) {
		t.Errorf("postgres main.go missing luascript.SetKeepQuestion(false)\n--- generated:\n%s", main)
	}

	lua, err := os.ReadFile(filepath.Join(dir, "internal/panel/luascript/luascript.go"))
	if err != nil {
		t.Fatalf("read luascript.go: %v", err)
	}
	luaStr := string(lua)
	for _, want := range []string{
		"func Run(ctx context.Context, db Execer, scope Scope, code string) error",
		"var keepQuestion = true",
		"func renumber(sqlText string) string",
		"type AbortError struct{ Msg string }",
		"func IsAbort(err error) bool",
		"L.SetContext(lctx)",
		`RawSetString("exec", L.NewFunction(func(L *lua.LState) int {`,
		`RawSetString("query_one", L.NewFunction(func(L *lua.LState) int {`,
		"last_insert_id",
	} {
		if !strings.Contains(luaStr, want) {
			t.Errorf("luascript.go missing %q\n--- generated:\n%s", want, luaStr)
		}
	}

	create := readResourceFile(t, dir, "user", "create.go")
	for _, want := range []string{
		`luascript "`,
		`if err := luascript.Run(r.Context(), db, luascript.Scope{`,
		`auth.RoleName(r),`,
		`Values: scope.Values,`,
		`for i, c := range []string{"name", "status"} {`,
		`vals[i] = v`,
	} {
		if !strings.Contains(create, want) {
			t.Errorf("create.go missing %q\n--- generated:\n%s", want, create)
		}
	}

	deleteStr := readResourceFile(t, dir, "user", "delete.go")
	for _, want := range []string{
		`luascript "`,
		`auth "`,
		`httperr.BadRequest(w, err.Error())`,
	} {
		if !strings.Contains(deleteStr, want) {
			t.Errorf("delete.go missing %q\n--- generated:\n%s", want, deleteStr)
		}
	}

	actions := readResourceFile(t, dir, "user", "actions.go")
	for _, want := range []string{
		`luascript "`,
		`"net/url"`,
		`luascript.Run(r.Context(), db, luascript.Scope{`,
		`http.Redirect(w, r, "/admin/user"+"?flash="+url.QueryEscape(err.Error()), http.StatusFound)`,
		`db.ExecContext(r.Context(), "UPDATE users SET status = 'inactive' WHERE id = $1", int64(id))`,
	} {
		if !strings.Contains(actions, want) {
			t.Errorf("actions.go missing %q\n--- generated:\n%s", want, actions)
		}
	}

	bulk := readResourceFile(t, dir, "user", "bulk.go")
	for _, want := range []string{
		`luascript "`,
		`for _, id := range ids {`,
		`luascript.Run(r.Context(), db, luascript.Scope{`,
		`ID:     id,`,
	} {
		if !strings.Contains(bulk, want) {
			t.Errorf("bulk.go missing %q\n--- generated:\n%s", want, bulk)
		}
	}

	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(mod), "github.com/yuin/gopher-lua v1.1.1") {
		t.Errorf("go.mod missing gopher-lua dep\n--- generated:\n%s", mod)
	}

	middleware, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth/middleware.go"))
	if err != nil {
		t.Fatalf("read middleware.go: %v", err)
	}
	if !strings.Contains(string(middleware), "func RoleName(r *http.Request) string") {
		t.Errorf("middleware.go missing RoleName\n--- generated:\n%s", middleware)
	}

	httperrFile, err := os.ReadFile(filepath.Join(dir, "internal/panel/httperr/httperr.go"))
	if err != nil {
		t.Fatalf("read httperr.go: %v", err)
	}
	if !strings.Contains(string(httperrFile), "func BadRequest(w http.ResponseWriter, msg string)") {
		t.Errorf("httperr.go missing BadRequest\n--- generated:\n%s", httperrFile)
	}
}

// TestGenerateScriptSQLite verifies the sqlite driver keeps "?" placeholders
// (keepQuestion = true) in the emitted luascript runtime.
func TestGenerateScriptSQLite(t *testing.T) {
	dir := t.TempDir()
	g := New(scriptConfig("sqlite"), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	lua, err := os.ReadFile(filepath.Join(dir, "internal/panel/luascript/luascript.go"))
	if err != nil {
		t.Fatalf("read luascript.go: %v", err)
	}
	if !strings.Contains(string(lua), "var keepQuestion = true") {
		t.Errorf("sqlite luascript.go must keep ? placeholders\n--- generated:\n%s", lua)
	}
}

// TestGenerateScriptFeatureOff verifies a config without any script: emits no
// luascript package, no gopher-lua dependency, no RoleName and no BadRequest —
// the generated tree stays byte-identical to the pre-E2 output.
func TestGenerateScriptFeatureOff(t *testing.T) {
	dir := t.TempDir()
	g := New(hookConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/panel/luascript")); err == nil {
		t.Fatal("luascript dir must not exist without scripts")
	}
	badTokens := []string{"luascript", "gopher-lua", "RoleName", "httperr.BadRequest", "func BadRequest"}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, tok := range badTokens {
			if strings.Contains(string(b), tok) {
				t.Errorf("%s must not contain %q", path, tok)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
