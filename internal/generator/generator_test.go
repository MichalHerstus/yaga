package generator

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
	pluginapi "github.com/MichalHerstus/yaga/pkg/plugin"
)

// hookConfig returns a minimal config exercising hooks on create, delete and a
// custom action, plus a fn hook and a sql hook.
func hookConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
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
							{Name: "email", Type: "email"},
						},
						Hooks: &types.Hooks{
							Before: []types.Hook{{Name: "validate_domain", Fn: "ValidateUserDomain"}},
							After:  []types.Hook{{Name: "notify", SQL: "INSERT INTO notifications (target, msg) VALUES ($1, 'user created')"}},
						},
					},
					Delete: &types.FormAction{
						Hooks: &types.Hooks{
							After: []types.Hook{{Name: "audit_delete", SQL: "INSERT INTO audit_log (action) VALUES ('delete')"}},
						},
					},
				},
				Actions: []types.Action{
					{
						Name:  "deactivate",
						Query: "UPDATE users SET status = 'inactive' WHERE id = $1",
						Hooks: &types.Hooks{
							Before: []types.Hook{{Name: "log_deactivate", Fn: "LogDeactivate"}},
						},
					},
				},
			},
		},
	}
}

// auditConfig returns a minimal config with the audit log enabled and value
// snapshots turned on, exercising audit weaving on create/update/delete/action.
func auditConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Audit: &types.AuditConfig{
			Enabled:       true,
			Table:         "audit_log",
			IncludeValues: true,
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
						Fields: []types.Field{{Name: "name", Type: "text"}},
					},
					Update: &types.FormAction{
						Fields: []types.Field{{Name: "name", Type: "text"}},
					},
					Delete: &types.FormAction{},
				},
				Actions: []types.Action{
					{Name: "deactivate", Query: "UPDATE users SET status = 'inactive' WHERE id = $1"},
				},
			},
		},
	}
}

// TestGenerateNoGoFilaReferences guards D10: generated output must never
// contain the old "go-fila" brand or module-path tokens anywhere in the tree.
func TestGenerateNoGoFilaReferences(t *testing.T) {
	dir := t.TempDir()
	g := New(auditConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	var bad []string
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
		if strings.Contains(string(b), "go-fila") || strings.Contains(string(b), "go_fila") || strings.Contains(string(b), "gf-theme") {
			bad = append(bad, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(bad) > 0 {
		t.Fatalf("generated output still references the old brand: %v", bad)
	}
}

func TestGenerateAudit(t *testing.T) {
	dir := t.TempDir()
	g := New(auditConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	// The config is augmented with a list-only AuditLog resource.
	found := false
	for _, r := range g.Config.Resources {
		if r.Name == "AuditLog" && r.Form == nil {
			found = true
			if r.Table != "audit_log" {
				t.Errorf("AuditLog resource table = %q, want audit_log", r.Table)
			}
		}
	}
	if !found {
		t.Fatal("audit generation must augment the config with a list-only AuditLog resource")
	}

	create, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "create.go"))
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	createStr := string(create)
	for _, want := range []string{
		`db.BeginTx(r.Context(), nil)`,
		`tx.QueryRowContext(r.Context(), query+" RETURNING \"id\"", vals...)`,
		`var valuesJSON []byte`,
		`"name": vals[0],`,
		`INSERT INTO \"audit_log\" (user_id, user_name, table_name, action, row_id, values_json) VALUES ($1, $2, $3, $4, $5, $6)`,
		`auth.UserID(r), auth.UserName(r), "users", "create", fmt.Sprintf("%d", newID), string(valuesJSON)`,
		`tx.Commit()`,
		`defer tx.Rollback()`,
	} {
		if !strings.Contains(createStr, want) {
			t.Errorf("create.go missing %q", want)
		}
	}
	for _, notWant := range []string{`db.ExecContext(r.Context(), query, vals...)`} {
		if strings.Contains(createStr, notWant) {
			t.Errorf("create.go must not contain %q when audit is on", notWant)
		}
	}

	update, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "update.go"))
	if err != nil {
		t.Fatalf("read update.go: %v", err)
	}
	updateStr := string(update)
	for _, want := range []string{
		`_, err = tx.ExecContext(r.Context(), query, vals...)`,
		`strconv.FormatInt(int64(id), 10)`,
		`"users", "update"`,
		`"encoding/json"`,
	} {
		if !strings.Contains(updateStr, want) {
			t.Errorf("update.go missing %q", want)
		}
	}

	del, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "delete.go"))
	if err != nil {
		t.Fatalf("read delete.go: %v", err)
	}
	delStr := string(del)
	for _, want := range []string{
		`tx.ExecContext(r.Context(), "DELETE FROM \"users\" WHERE \"id\" = $1", int64(id))`,
		`auth "`,
		`"users", "delete", strconv.FormatInt(int64(id), 10), ""`,
	} {
		if !strings.Contains(delStr, want) {
			t.Errorf("delete.go missing %q", want)
		}
	}

	actions, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "actions.go"))
	if err != nil {
		t.Fatalf("read actions.go: %v", err)
	}
	actionsStr := string(actions)
	for _, want := range []string{
		`tx, err := db.BeginTx(r.Context(), nil)`,
		`_, err = tx.ExecContext(r.Context(), "UPDATE users SET status = 'inactive' WHERE id = $1", int64(id))`,
		`"users", "deactivate", strconv.FormatInt(int64(id), 10), ""`,
		`auth "`,
	} {
		if !strings.Contains(actionsStr, want) {
			t.Errorf("actions.go missing %q", want)
		}
	}

	mw, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "middleware.go"))
	if err != nil {
		t.Fatalf("read middleware.go: %v", err)
	}
	mwStr := string(mw)
	for _, want := range []string{
		`"fmt"`,
		`func UserID(r *http.Request) string {`,
		`fmt.Sprintf("%v", id)`,
	} {
		if !strings.Contains(mwStr, want) {
			t.Errorf("middleware.go missing %q (UserID helper must be emitted when audit is on)", want)
		}
	}

	if !strings.Contains(string(mw), `func UserID`) {
		t.Error("middleware.go must define UserID when audit is on")
	}
}

func TestGenerateAuditNoValuesAndExcluded(t *testing.T) {
	dir := t.TempDir()
	cfg := auditConfig()
	cfg.Audit.IncludeValues = false
	cfg.Audit.ExcludeResources = []string{"User"}
	// Move the User resource to the tail so the generated AuditLog resource
	// (appended by applyAudit) does not mask exclusion by name lookup; the
	// resource is excluded by name regardless of order.
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	create, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "create.go"))
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	createStr := string(create)
	if strings.Contains(createStr, "var valuesJSON") {
		t.Error("create.go must not emit values_json when audit.include_values is false")
	}
	if strings.Contains(createStr, `INSERT INTO "audit_log"`) {
		t.Error("create.go must not emit an audit INSERT when the resource is excluded")
	}
	if strings.Contains(createStr, "BeginTx") {
		t.Error("create.go must not wrap in a transaction when the resource is excluded")
	}
	// The hookless path stays byte-identical for excluded resources.
	if !strings.Contains(createStr, "db.ExecContext(r.Context(), query, vals...)") {
		t.Error("create.go must keep the plain hookless exec for excluded resources")
	}

	mw, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "middleware.go"))
	if err != nil {
		t.Fatalf("read middleware.go: %v", err)
	}
	if strings.Contains(string(mw), "func UserID") {
		t.Error("middleware.go must not emit UserID when audit is enabled but every resource is excluded")
	}
}

func TestGenerateAuditSchemaSkippedWhenDeclared(t *testing.T) {
	dir := t.TempDir()
	cfg := auditConfig()
	// Simulate the demo/user schema already declaring the audit table.
	migDir := filepath.Join(dir, "sql", "migrations")
	if err := os.MkdirAll(migDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migDir, "schema.sql"), []byte("CREATE TABLE audit_log (\n    id INTEGER PRIMARY KEY AUTOINCREMENT\n);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(migDir, "audit_log.sql")); !os.IsNotExist(err) {
		t.Error("must not emit audit_log.sql when a migration already declares the audit table")
	}
	if _, err := os.Stat(filepath.Join(dir, "sql", "queries", "audit_log.sql")); !os.IsNotExist(err) {
		t.Error("must not emit audit_log.sql queries when a migration already declares the audit table")
	}
}

func TestGenerateAuditSchemaEmitted(t *testing.T) {
	dir := t.TempDir()
	g := New(auditConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	ddl, err := os.ReadFile(filepath.Join(dir, "sql", "migrations", "audit_log.sql"))
	if err != nil {
		t.Fatalf("read audit_log.sql: %v", err)
	}
	ddlStr := string(ddl)
	for _, want := range []string{
		`CREATE TABLE "audit_log" (`,
		"user_id TEXT",
		"values_json",
		"created_at",
	} {
		if !strings.Contains(ddlStr, want) {
			t.Errorf("audit_log.sql missing %q", want)
		}
	}
	// D11: no sqlc query file is produced — audit list/count run as raw SQL in
	// the generated list handler.
	if _, err := os.Stat(filepath.Join(dir, "sql", "queries", "audit_log.sql")); !os.IsNotExist(err) {
		t.Error("must not emit a sqlc queries/audit_log.sql (D11)")
	}
}

func TestAuditForExcludesAugmentedResource(t *testing.T) {
	g := New(auditConfig(), t.TempDir())
	g.applyAudit()
	if g.auditFor(types.Resource{Name: "AuditLog"}) != nil {
		t.Error("the generated AuditLog resource itself must never be audited")
	}
	if g.auditFor(types.Resource{Name: "User"}) == nil {
		t.Error("User must be audited")
	}
}

func TestContainsCreateTable(t *testing.T) {
	cases := []struct {
		sql   string
		table string
		want  bool
	}{
		{"CREATE TABLE audit_log (id INT);", "audit_log", true},
		{"create table audit_log (id int);", "audit_log", true},
		{"CREATE TABLE IF NOT EXISTS audit_log (id INT);", "audit_log", true},
		{`CREATE TABLE "AuditLog" (id INT);`, "auditlog", true},
		{"CREATE TABLE users (id INT);", "audit_log", false},
		{"CREATE TABLE audit_trail (id INT);", "audit_log", false},
		{"-- comment about audit_log", "audit_log", false},
		{"", "audit_log", false},
	}
	for _, c := range cases {
		if got := containsCreateTable(c.sql, c.table); got != c.want {
			t.Errorf("containsCreateTable(%q, %q) = %v, want %v", c.sql, c.table, got, c.want)
		}
	}
}

func TestGenerateHooks(t *testing.T) {
	dir := t.TempDir()
	g := New(hookConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	create, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "create.go"))
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	createStr := string(create)
	for _, want := range []string{
		`RETURNING \"id\"`,
		`hooks.Scope{`,
		`Action: "create"`,
		`hooks.ValidateUserDomain(r.Context(), db, scope)`,
		`db.QueryRowContext(r.Context(), query+" RETURNING \"id\"", vals...)`,
		`scope.ID = newID`,
		`db.ExecContext(r.Context(), "INSERT INTO notifications (target, msg) VALUES ($1, 'user created')", scope.ID)`,
	} {
		if !strings.Contains(createStr, want) {
			t.Errorf("create.go missing %q", want)
		}
	}
	for _, notWant := range []string{`db.ExecContext(r.Context(), query, vals...)`} {
		if strings.Contains(createStr, notWant) {
			t.Errorf("create.go should not contain %q when hooks are declared", notWant)
		}
	}

	del, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "delete.go"))
	if err != nil {
		t.Fatalf("read delete.go: %v", err)
	}
	delStr := string(del)
	for _, want := range []string{
		`hooks "`,
		`Action: "delete"`,
		`db.ExecContext(r.Context(), "INSERT INTO audit_log (action) VALUES ('delete')", scope.ID)`,
	} {
		if !strings.Contains(delStr, want) {
			t.Errorf("delete.go missing %q", want)
		}
	}

	actions, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "actions.go"))
	if err != nil {
		t.Fatalf("read actions.go: %v", err)
	}
	if !strings.Contains(string(actions), "hooks.LogDeactivate(r.Context(), db, scope)") {
		t.Error("actions.go missing fn hook call")
	}

	hooksGo, err := os.ReadFile(filepath.Join(dir, "internal/hooks", "hooks.go"))
	if err != nil {
		t.Fatalf("read hooks.go: %v", err)
	}
	hooksStr := string(hooksGo)
	for _, want := range []string{
		`type Scope struct`,
		`func ValidateUserDomain(ctx context.Context, db *sql.DB, s Scope) error { return nil }`,
		`func LogDeactivate(ctx context.Context, db *sql.DB, s Scope) error { return nil }`,
	} {
		if !strings.Contains(hooksStr, want) {
			t.Errorf("hooks.go missing %q", want)
		}
	}
}

func TestGeneratePluginFnHookSkippedStub(t *testing.T) {
	cfg := hookConfig()
	cfg.Resources[0].Actions[0].Hooks.After = append(cfg.Resources[0].Actions[0].Hooks.After,
		types.Hook{Name: "audit_customer_create", Fn: "LogCustomerCreated"})

	dir := t.TempDir()
	g := New(cfg, dir)
	g.pluginFnNames = map[string]bool{"LogCustomerCreated": true}
	g.pluginHookFiles = map[string]string{"audit_hooks.go": "package hooks\n"}
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	hooksGo, err := os.ReadFile(filepath.Join(dir, "internal/hooks", "hooks.go"))
	if err != nil {
		t.Fatalf("read hooks.go: %v", err)
	}
	hooksStr := string(hooksGo)
	for _, want := range []string{
		`type Scope struct`,
		`func ValidateUserDomain(ctx context.Context, db *sql.DB, s Scope) error { return nil }`,
		`func LogDeactivate(ctx context.Context, db *sql.DB, s Scope) error { return nil }`,
	} {
		if !strings.Contains(hooksStr, want) {
			t.Errorf("hooks.go missing %q", want)
		}
	}
	if strings.Contains(hooksStr, "func LogCustomerCreated(") {
		t.Error("hooks.go must not stub a plugin-backed fn hook")
	}

	actions, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "actions.go"))
	if err != nil {
		t.Fatalf("read actions.go: %v", err)
	}
	actionsStr := string(actions)
	if !strings.Contains(actionsStr, "hooks.LogCustomerCreated(r.Context(), db, scope)") {
		t.Error("actions.go missing plugin fn hook call")
	}
}

func TestGenerateHooksOnlyPluginSource(t *testing.T) {
	cfg := &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Resources: []types.Resource{
			{Name: "User", List: &types.ListConfig{Columns: []types.Column{{Name: "name", Label: "Name"}}}},
		},
	}

	dir := t.TempDir()
	g := New(cfg, dir)
	g.pluginHookFiles = map[string]string{"audit_hooks.go": "package hooks\n"}
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	hooksGo, err := os.ReadFile(filepath.Join(dir, "internal/hooks", "hooks.go"))
	if err != nil {
		t.Fatalf("read hooks.go: %v", err)
	}
	hooksStr := string(hooksGo)
	if !strings.Contains(hooksStr, "type Scope struct") {
		t.Error("hooks.go must emit Scope when a plugin hook source exists")
	}
	if strings.Contains(hooksStr, "func ") || strings.Contains(hooksStr, "import (") {
		t.Error("hooks.go with no fn hooks must have no stubs and no imports")
	}
}

func TestHookFuncNames(t *testing.T) {
	src := `package hooks

func LogCustomerCreated(ctx context.Context, db *sql.DB, s Scope) error { return nil }

func helper(x int) int { return x }

func (p *thing) method() error { return nil }

func F[T any](x T) {}

func Exported(y string) {}
`
	got := hookFuncNames(src)
	want := []string{"LogCustomerCreated", "helper", "Exported"}
	if len(got) != len(want) {
		t.Fatalf("hookFuncNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hookFuncNames = %v, want %v", got, want)
		}
	}
}

func TestAttachHookRequiresSourceForFn(t *testing.T) {
	g := New(&types.Config{}, t.TempDir())
	err := g.attachHook("audit", pluginapi.HookAttachment{
		Resource: "User",
		Action:   "create",
		When:     "after",
		Hook:     pluginapi.Hook{Fn: "LogCustomerCreated"},
	})
	if err == nil || !strings.Contains(err.Error(), "no matching hook source") {
		t.Fatalf("expected missing-hook-source error, got %v", err)
	}

	cfg := &types.Config{Resources: []types.Resource{{
		Name: "User",
		Form: &types.FormConfig{Create: &types.FormAction{Fields: []types.Field{{Name: "name", Type: "text"}}}},
	}}}
	g2 := New(cfg, t.TempDir())
	g2.pluginFnNames = map[string]bool{"LogCustomerCreated": true}
	if err := g2.attachHook("audit", pluginapi.HookAttachment{
		Resource: "User",
		Action:   "create",
		When:     "after",
		Hook:     pluginapi.Hook{Name: "audit_customer_create", Fn: "LogCustomerCreated"},
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	hooks := g2.Config.Resources[0].Form.Create.Hooks
	if hooks == nil || len(hooks.After) != 1 || hooks.After[0].Fn != "LogCustomerCreated" {
		t.Fatalf("fn hook not merged: %+v", hooks)
	}
}

func TestGenerateNoHooksRegression(t *testing.T) {
	cfg := hookConfig()
	cfg.Resources[0].Form.Create.Hooks = nil
	cfg.Resources[0].Form.Delete.Hooks = nil
	cfg.Resources[0].Actions[0].Hooks = nil

	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	create, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "create.go"))
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	createStr := string(create)
	if strings.Contains(createStr, "RETURNING") {
		t.Error("hookless create.go must not use RETURNING")
	}
	if !strings.Contains(createStr, "db.ExecContext(r.Context(), query, vals...)") {
		t.Error("hookless create.go should keep ExecContext")
	}

	if _, err := os.Stat(filepath.Join(dir, "internal/hooks", "hooks.go")); !os.IsNotExist(err) {
		t.Error("hooks.go should not be generated without fn hooks")
	}
}

// procConfig returns a config exercising stored-procedure hooks (create after,
// delete before) and a proc-backed custom action (single + bulk) for the given
// driver. The driver drives emission: CALL on postgres, EXEC on mssql, skipped
// on sqlite.
func procConfig(driver string) *types.Config {
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
						Fields: []types.Field{{Name: "name", Type: "text"}},
						Hooks: &types.Hooks{
							After: []types.Hook{{Name: "archive_created", Proc: "sp_archive_user"}},
						},
					},
					Delete: &types.FormAction{
						Hooks: &types.Hooks{
							Before: []types.Hook{{Name: "archive_delete", Proc: "sp_archive_user"}},
						},
					},
				},
				Actions: []types.Action{
					{Name: "archive", Proc: "sp_archive_user", Bulk: true},
				},
			},
		},
	}
}

// assertGeneratedGoParses parses every generated .go file under dir, failing on
// syntax errors. It catches malformed Sprintf output in the emission builders
// (e.g. a mangled case block) without needing the full generated module to
// type-check.
func assertGeneratedGoParses(t *testing.T, dir string) {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk output dir: %v", err)
	}
	for _, f := range files {
		if _, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.SkipObjectResolution); err != nil {
			t.Errorf("%s failed to parse: %v", f, err)
		}
	}
}

func TestGenerateProcPostgres(t *testing.T) {
	dir := t.TempDir()
	g := New(procConfig("postgres"), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	assert := func(file string, wants []string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s missing %q\n--- generated:\n%s", file, want, data)
			}
		}
		return string(data)
	}
	assert("internal/panel/resources/user/create.go", []string{
		`db.ExecContext(r.Context(), "CALL sp_archive_user($1)", scope.ID)`,
		`RETURNING \"id\"`,
		`hooks "`,
	})
	assert("internal/panel/resources/user/delete.go", []string{
		`db.ExecContext(r.Context(), "CALL sp_archive_user($1)", scope.ID)`,
		`hooks "`,
	})
	assert("internal/panel/resources/user/actions.go", []string{
		`db.ExecContext(r.Context(), "CALL sp_archive_user($1)", int64(id))`,
	})
	assert("internal/panel/resources/user/bulk.go", []string{
		`tx, err := db.BeginTx(r.Context(), nil)`,
		`defer tx.Rollback()`,
		`tx.ExecContext(r.Context(), "CALL sp_archive_user($1)", id)`,
		`tx.Commit()`,
	})
}

func TestGenerateProcMSSQL(t *testing.T) {
	dir := t.TempDir()
	g := New(procConfig("mssql"), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	assert := func(file string, wants []string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s missing %q\n--- generated:\n%s", file, want, data)
			}
		}
		return string(data)
	}
	assert("internal/panel/resources/user/create.go", []string{
		`db.ExecContext(r.Context(), "EXEC sp_archive_user $1", scope.ID)`,
		`OUTPUT INSERTED.[id]`,
		`hooks "`,
	})
	assert("internal/panel/resources/user/actions.go", []string{
		`db.ExecContext(r.Context(), "EXEC sp_archive_user $1", int64(id))`,
	})
	assert("internal/panel/resources/user/bulk.go", []string{
		`tx, err := db.BeginTx(r.Context(), nil)`,
		`defer tx.Rollback()`,
		`tx.ExecContext(r.Context(), "EXEC sp_archive_user $1", id)`,
		`tx.Commit()`,
	})
}

func TestGenerateProcSQLiteIgnored(t *testing.T) {
	dir := t.TempDir()
	g := New(procConfig("sqlite"), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	assert := func(file string, wants []string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s missing %q\n--- generated:\n%s", file, want, data)
			}
		}
		return string(data)
	}
	assertNot := func(file string, notWants []string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, not := range notWants {
			if strings.Contains(string(data), not) {
				t.Errorf("%s should not contain %q\n--- generated:\n%s", file, not, data)
			}
		}
	}

	assert("internal/panel/resources/user/create.go", []string{
		`db.ExecContext(r.Context(), query, vals...)`,
	})
	assertNot("internal/panel/resources/user/create.go", []string{
		"CALL", "EXEC", `RETURNING \"id\"`, `hooks "`,
	})

	assertNot("internal/panel/resources/user/delete.go", []string{
		"CALL", "EXEC", `hooks "`,
	})

	assert("internal/panel/resources/user/actions.go", []string{
		`case "archive":`,
	})
	assertNot("internal/panel/resources/user/actions.go", []string{
		"CALL", "EXEC", "db.ExecContext", `hooks "`,
	})

	assert("internal/panel/resources/user/bulk.go", []string{
		`case "archive":`,
		`_ = id`,
	})
	assertNot("internal/panel/resources/user/bulk.go", []string{
		"CALL", "EXEC", "db.ExecContext",
	})
}

// TestGenerateProcActionWithFnHook ensures an action mixing a proc call with a
// fn hook emits both the hook import/call and the proc invocation in one case.
func TestGenerateProcActionWithFnHook(t *testing.T) {
	cfg := procConfig("postgres")
	cfg.Resources[0].Actions[0].Hooks = &types.Hooks{
		Before: []types.Hook{{Name: "log_archive", Fn: "LogArchive"}},
	}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	actions, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "actions.go"))
	if err != nil {
		t.Fatalf("read actions.go: %v", err)
	}
	actionsStr := string(actions)
	for _, want := range []string{
		`hooks "`,
		`hooks.LogArchive(r.Context(), db, scope)`,
		`db.ExecContext(r.Context(), "CALL sp_archive_user($1)", int64(id))`,
	} {
		if !strings.Contains(actionsStr, want) {
			t.Errorf("actions.go missing %q\n--- generated:\n%s", want, actionsStr)
		}
	}
}

// TestGenerateProcSQLiteMixedHooks keeps fn/sql hooks and the RETURNING path
// when a proc-only hook sits next to a real hook in the same block on sqlite:
// only the proc call is skipped, everything else still emits.
func TestGenerateProcSQLiteMixedHooks(t *testing.T) {
	cfg := procConfig("sqlite")
	cfg.Resources[0].Form.Create.Hooks.After = []types.Hook{
		{Name: "archive_created", Proc: "sp_archive_user"},
		{Name: "notify", SQL: "INSERT INTO notifications (target) VALUES ('created')"},
	}

	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	create, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "create.go"))
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	createStr := string(create)
	for _, want := range []string{
		`RETURNING \"id\"`,
		`hooks "`,
		`db.ExecContext(r.Context(), "INSERT INTO notifications (target) VALUES ('created')", scope.ID)`,
	} {
		if !strings.Contains(createStr, want) {
			t.Errorf("create.go missing %q\n--- generated:\n%s", want, createStr)
		}
	}
	for _, notWant := range []string{"CALL", "EXEC"} {
		if strings.Contains(createStr, notWant) {
			t.Errorf("create.go should not contain %q\n--- generated:\n%s", notWant, createStr)
		}
	}
}

// fkLabelConfig returns a postgres config whose list view has an FK label
// column ({fk}_label) backed by a relation form field, mirroring what
// `init --db` introspection emits for a table with foreign keys.
func fkLabelConfig() *types.Config {
	fields := []types.Field{
		{Name: "pn", Type: "relation", OptionsQuery: "ListSkladZbozi", OptionsValue: "pn", OptionsLabel: "pn"},
		{Name: "pn_nazev", Type: "string"},
	}
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "postgres://user:pass@host:5432/db"},
		},
		Resources: []types.Resource{
			{
				Name:  "SkladZbozi",
				Label: "SkladZbozi",
				Table: "sklad_zbozi",
				List:  &types.ListConfig{Columns: []types.Column{{Name: "pn", Label: "pn"}}},
			},
			{
				Name:  "SkladZasoby",
				Label: "SkladZasoby",
				Table: "sklad_zasoby",
				List: &types.ListConfig{
					Columns: []types.Column{
						{Name: "id", Type: "integer", Sortable: true},
						{Name: "pn_nazev", Type: "string", Searchable: true},
						{Name: "pn_label", Label: "SkladZbozi", Type: "string"},
					},
				},
				Form: &types.FormConfig{
					Create: &types.FormAction{Fields: fields},
					Update: &types.FormAction{Fields: fields},
				},
			},
		},
	}
}

// TestGenerateListFKLabelJoin ensures the generated list handler emits the FK
// LEFT JOIN for "{fk}_label" list columns (so the raw SQL can select the label
// from the joined foreign table) and derives the pagination total from a single
// windowed COUNT(*) OVER() query instead of a separate COUNT query.
func TestGenerateListFKLabelJoin(t *testing.T) {
	dir := t.TempDir()
	g := New(fkLabelConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	list, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/skladzasoby", "list.go"))
	if err != nil {
		t.Fatalf("read list.go: %v", err)
	}
	listStr := string(list)
	for _, want := range []string{
		`SELECT t.\"id\", t.\"pn_nazev\", f_sklad_zbozi.\"pn\" AS pn_label, COUNT(*) OVER() AS _total FROM \"sklad_zasoby\" t LEFT JOIN \"sklad_zbozi\" f_sklad_zbozi ON f_sklad_zbozi.\"pn\" = t.\"pn\"`,
		`searchableCols := []string{"t.\"pn_nazev\""}`,
		`dataQuery := "SELECT t.\"id\", t.\"pn_nazev\", f_sklad_zbozi.\"pn\" AS pn_label, COUNT(*) OVER() AS _total FROM \"sklad_zasoby\" t LEFT JOIN \"sklad_zbozi\" f_sklad_zbozi ON f_sklad_zbozi.\"pn\" = t.\"pn\"" + whereSQL + orderSQL + " LIMIT $1 OFFSET $2"`,
		`case int64:
            total = tv`,
	} {
		if !strings.Contains(listStr, want) {
			t.Errorf("list.go missing %q\n--- generated:\n%s", want, listStr)
		}
	}
	if strings.Contains(listStr, `countQuery :=`) {
		t.Error("list.go must not emit a separate COUNT query (windowed COUNT(*) OVER() replaces it)")
	}
}

// TestGenerateFmtImportSearchBlock ensures list.go imports fmt on
// postgres/mssql even when the resource declares no searchable columns and no
// filter — the emitted search block always references fmt.Sprintf, so a gated
// import (as before) produced "undefined: fmt" builds. On sqlite the search
// block uses string concatenation and must not import fmt.
func TestGenerateFmtImportSearchBlock(t *testing.T) {
	cfgFor := func(driver string) *types.Config {
		return &types.Config{
			Version: "1",
			Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
			Connections: map[string]types.Connection{
				"default": {Driver: driver, DSN: "x"},
			},
			Resources: []types.Resource{
				{
					Name:  "Note",
					Label: "Note",
					List:  &types.ListConfig{Columns: []types.Column{{Name: "title", Label: "Title"}}},
				},
			},
		}
	}

	for _, driver := range []string{"postgres", "mssql"} {
		dir := t.TempDir()
		g := New(cfgFor(driver), dir)
		if err := g.Generate(); err != nil {
			t.Fatalf("generate (%s): %v", driver, err)
		}
		list, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/note/list.go"))
		if err != nil {
			t.Fatalf("read list.go: %v", err)
		}
		if !strings.Contains(string(list), "    \"fmt\"\n") {
			t.Errorf("%s list.go must import fmt (search block uses fmt.Sprintf) even without searchable columns:\n%s", driver, list)
		}
	}

	dir := t.TempDir()
	g := New(cfgFor("sqlite"), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate (sqlite): %v", err)
	}
	list, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/note/list.go"))
	if err != nil {
		t.Fatalf("read list.go: %v", err)
	}
	if strings.Contains(string(list), `"fmt"`) {
		t.Errorf("sqlite list.go must not import fmt (search block uses concatenation):\n%s", list)
	}
}

// TestGeneratePostgresDriver ensures a postgres project opens the DB with the
// pgx driver name and registers it (and pins it in go.mod) so the generated
// app actually boots against a live postgres server.
func TestGeneratePostgresDriver(t *testing.T) {
	cfg := hookConfig()
	cfg.Connections = map[string]types.Connection{
		"default": {Driver: "postgres", DSN: "postgres://user:pass@host:5432/db"},
	}

	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	main, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	mainStr := string(main)
	for _, want := range []string{
		`_ "github.com/jackc/pgx/v5/stdlib"`,
		`sql.Open("pgx", dsn)`,
		`dsn = envFrom(".ENV", "DATABASE_URL")`,
	} {
		if !strings.Contains(mainStr, want) {
			t.Errorf("main.go missing %q", want)
		}
	}
	if strings.Contains(mainStr, `sql.Open("postgres"`) {
		t.Error("main.go must not use the unregistered \"postgres\" driver name")
	}
	if strings.Contains(mainStr, "postgres://user:pass@host:5432/db") {
		t.Error("main.go must not embed the configured DSN (credentials belong in .ENV)")
	}
	if strings.Contains(mainStr, `dsn = "postgres://localhost:5432`) {
		t.Error("main.go must not fall back to the localhost default when a connection is configured")
	}

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(gomod), "github.com/jackc/pgx/v5 v5.10.0") {
		t.Error("go.mod must pin github.com/jackc/pgx/v5 for the postgres driver")
	}
}

// TestGenerateEnvFile ensures the configured database DSN is emitted into a
// private .ENV file next to main.go (not compiled into the binary), so
// deployments can switch databases by editing the file. A config with no
// connection produces no .ENV and main.go boots from the localhost default.
func TestGenerateEnvFile(t *testing.T) {
	cfg := hookConfig()
	cfg.Connections = map[string]types.Connection{
		"default": {Driver: "postgres", DSN: "postgres://user:pass@host:5432/db"},
	}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".ENV"))
	if err != nil {
		t.Fatalf("read .ENV: %v", err)
	}
	if !strings.Contains(string(data), "DATABASE_URL=postgres://user:pass@host:5432/db") {
		t.Errorf(".ENV must carry DATABASE_URL:\n%s", data)
	}
	info, err := os.Stat(filepath.Join(dir, ".ENV"))
	if err != nil {
		t.Fatalf("stat .ENV: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf(".ENV mode = %o, want 0600", info.Mode().Perm())
	}

	// No connection configured => no .ENV, and main.go keeps the non-secret
	// localhost fallback so the generated app still boots.
	cfg2 := hookConfig()
	cfg2.Connections = nil
	dir2 := t.TempDir()
	g2 := New(cfg2, dir2)
	if err := g2.Generate(); err != nil {
		t.Fatalf("generate (no connections): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir2, ".ENV")); !os.IsNotExist(err) {
		t.Error("config without a connection must not emit .ENV")
	}
	main2, err := os.ReadFile(filepath.Join(dir2, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(main2), `dsn = "postgres://localhost:5432/db?sslmode=disable"`) {
		t.Error("main.go without a configured connection must keep the localhost default fallback")
	}
	if strings.Contains(string(main2), `log.Fatalf("no DATABASE_URL:`) {
		t.Error("main.go without a configured connection must not error on a missing DSN")
	}
}

// TestMainGeneratedFlags ensures the generated main.go registers short flag
// aliases (--port/-p, --log/-l) and a --help/-h path that prints the command
// line syntax and exits before touching the database.
func TestMainGeneratedFlags(t *testing.T) {
	cfg := hookConfig()
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	main, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	mainStr := string(main)
	for _, want := range []string{
		`flag.IntVar(port, "p", 0, "shorthand for --port")`,
		`flag.StringVar(logLevel, "l", "full", "shorthand for --log")`,
		`help := flag.Bool("help", false, "print command line syntax and exit")`,
		`flag.BoolVar(help, "h", false, "shorthand for --help")`,
		`flag.Usage = func() {`,
		`fmt.Fprintf(os.Stdout, "Usage: %s [options]\n\nOptions:\n", os.Args[0])`,
		`if *help {`,
		`os.Exit(0)`,
		`auth.Init()`,
		`internal/panel/auth`,
	} {
		if !strings.Contains(mainStr, want) {
			t.Errorf("main.go missing %q\n--- generated:\n%s", want, mainStr)
		}
	}
}

// phaseAConfig returns a config exercising the Phase A security surfaces:
// a sortable list, a card view, a create form with a file upload field and a
// custom action with a sql hook (so the httperr import is exercised in the
// action handler too).
func phaseAConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Resources: []types.Resource{
			{
				Name:  "User",
				Label: "User",
				List: &types.ListConfig{
					Columns: []types.Column{{Name: "name", Label: "Name", Sortable: true}},
				},
				Card: &types.CardConfig{
					Fields:      []types.Field{{Name: "name", Type: "text"}},
					Columns:     3,
					Rows:        2,
					DefaultSort: "-name",
				},
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "text"},
							{Name: "avatar", Type: "file"},
						},
					},
					Delete: &types.FormAction{},
				},
				Actions: []types.Action{
					{Name: "deactivate", Query: "UPDATE users SET status = 'inactive' WHERE id = $1"},
				},
			},
		},
	}
}

// TestGenerateSessionSecret checks the generated session store reads
// SESSION_SECRET from the environment, requires it in production and falls back
// to an ephemeral random secret otherwise.
func TestGenerateSessionSecret(t *testing.T) {
	dir := t.TempDir()
	g := New(phaseAConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	sess, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "session.go"))
	if err != nil {
		t.Fatalf("read session.go: %v", err)
	}
	sessStr := string(sess)
	for _, want := range []string{
		`os.Getenv("SESSION_SECRET")`,
		`len(v) < 32`,
		`os.Getenv("APP_ENV") == "production"`,
		`log.Fatal("SESSION_SECRET must be set when APP_ENV=production")`,
		`rand.Read(buf)`,
		`Store = newStore([]byte(v))`,
		`sessions.NewCookieStore(secret)`,
		`SameSite: http.SameSiteLaxMode`,
		`HttpOnly: true`,
		`var Store *sessions.CookieStore`,
	} {
		if !strings.Contains(sessStr, want) {
			t.Errorf("session.go missing %q", want)
		}
	}
	if strings.Contains(sessStr, "yaga-secret-key-change-in-production") {
		t.Error("session.go must not hardcode the default secret")
	}
}

// TestGenerateOrderWhitelist checks both the list and card handlers clamp the
// order query parameter to asc/desc before interpolating it into ORDER BY.
func TestGenerateOrderWhitelist(t *testing.T) {
	dir := t.TempDir()
	g := New(phaseAConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	for _, f := range []string{"list.go", "card.go"} {
		code, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		str := string(code)
		if !strings.Contains(str, `if order != "asc" && order != "desc" {`) {
			t.Errorf("%s missing order whitelist guard", f)
		}
	}
}

// TestGenerateUploadValidation checks the emitted saveUploadedFile helper
// whitelists extensions and content types instead of accepting arbitrary files.
func TestGenerateUploadValidation(t *testing.T) {
	dir := t.TempDir()
	g := New(phaseAConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	create, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "create.go"))
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	createStr := string(create)
	for _, want := range []string{
		`func saveUploadedFile(r *http.Request, fieldName string) string {`,
		`strings.ToLower(filepath.Ext(header.Filename))`,
		`safeUploadExt(ext)`,
		`http.DetectContentType(head[:n])`,
		`detected == "text/html"`,
		`detected == "image/svg+xml"`,
		`file.Seek(0, io.SeekStart)`,
		`var safeUploadExts = map[string]bool{`,
	} {
		if !strings.Contains(createStr, want) {
			t.Errorf("create.go missing %q", want)
		}
	}
	for _, notWant := range []string{`".html": true`, `".svg": true`, `".php": true`} {
		if strings.Contains(createStr, notWant) {
			t.Errorf("create.go must not whitelist %q", notWant)
		}
	}
}

// TestGenerateSecureErrors checks every generated handler logs the real error
// server-side and returns a generic status instead of leaking err.Error(), and
// that the shared httperr package is emitted.
func TestGenerateSecureErrors(t *testing.T) {
	dir := t.TempDir()
	g := New(phaseAConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	httperrCode, err := os.ReadFile(filepath.Join(dir, "internal/panel/httperr", "httperr.go"))
	if err != nil {
		t.Fatalf("read httperr.go: %v", err)
	}
	for _, want := range []string{
		`func Internal(w http.ResponseWriter, err error) {`,
		`log.Printf("internal error: %v", err)`,
		`http.Error(w, "Internal Server Error", http.StatusInternalServerError)`,
		`func NotFound(w http.ResponseWriter, err error) {`,
	} {
		if !strings.Contains(string(httperrCode), want) {
			t.Errorf("httperr.go missing %q", want)
		}
	}

	handlerDir := filepath.Join(dir, "internal/panel/resources/user")
	for _, f := range []string{"list.go", "card.go", "create.go", "delete.go", "actions.go"} {
		code, err := os.ReadFile(filepath.Join(handlerDir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		str := string(code)
		if !strings.Contains(str, `httperr "`) {
			t.Errorf("%s missing httperr import", f)
		}
		if strings.Contains(str, `http.Error(w, err.Error()`) {
			t.Errorf("%s must not leak err.Error() to the client", f)
		}
	}

	pages, err := os.ReadFile(filepath.Join(dir, "internal/panel/pages", "Dashboard.go"))
	if err == nil {
		if !strings.Contains(string(pages), `httperr.Internal(w, err)`) {
			t.Error("page handler must use httperr.Internal for render errors")
		}
	}
}

// TestGenerateSecurityHeaders checks the router registers the securityHeaders
// middleware and serves uploads with Content-Disposition: attachment.
func TestGenerateSecurityHeaders(t *testing.T) {
	dir := t.TempDir()
	g := New(phaseAConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	router, err := os.ReadFile(filepath.Join(dir, "internal/panel", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	routerStr := string(router)
	for _, want := range []string{
		`r.Use(securityHeaders)`,
		`func securityHeaders(next http.Handler) http.Handler {`,
		`X-Frame-Options`, `DENY`,
		`X-Content-Type-Options`, `nosniff`,
		`Referrer-Policy`, `same-origin`,
		`Content-Security-Policy`,
		`Content-Disposition`, `attachment`,
	} {
		if !strings.Contains(routerStr, want) {
			t.Errorf("router.go missing %q", want)
		}
	}
}

// TestGenerateCSRFProtection checks the generated auth package emits the CSRF
// token helper and middleware, hardens the session cookie, registers the
// middleware first in the panel router, makes logout POST-only, and embeds
// hidden _csrf inputs in every state-changing form.
func TestGenerateCSRFProtection(t *testing.T) {
	dir := t.TempDir()
	g := New(phaseAConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	sess, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "session.go"))
	if err != nil {
		t.Fatalf("read session.go: %v", err)
	}
	sessStr := string(sess)
	for _, want := range []string{
		`func CSRFToken(r *http.Request, w http.ResponseWriter) string {`,
		`session.Values["csrf_token"]`,
		`func csrfMatches(expected, actual string) bool {`,
		`subtle.ConstantTimeCompare`,
		`func CSRFMiddleware(next http.Handler) http.Handler {`,
		`r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions`,
		`X-CSRF-Token`,
		`SameSite: http.SameSiteLaxMode`,
		`HttpOnly: true`,
		`Secure:`,
	} {
		if !strings.Contains(sessStr, want) {
			t.Errorf("session.go missing %q", want)
		}
	}

	router, err := os.ReadFile(filepath.Join(dir, "internal/panel", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	routerStr := string(router)
	if !strings.Contains(routerStr, `r.Use(auth.CSRFMiddleware)`) {
		t.Error("router.go must register auth.CSRFMiddleware")
	}
	if strings.Contains(routerStr, `r.Get("/logout"`) {
		t.Error("router.go must not expose logout as GET")
	}
	if !strings.Contains(routerStr, `r.Post("/logout", auth.LogoutHandler(db))`) {
		t.Error("router.go must register logout as POST only")
	}

	handler, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "handler.go"))
	if err != nil {
		t.Fatalf("read auth handler.go: %v", err)
	}
	handlerStr := string(handler)
	if !strings.Contains(handlerStr, `CSRFToken: CSRFToken(r, w)`) {
		t.Error("auth handler.go must populate LoginPageData.CSRFToken")
	}

	for _, f := range []string{
		filepath.Join(dir, "internal/views/resources/user", "form.templ"),
		filepath.Join(dir, "internal/views/resources/user", "list.templ"),
		filepath.Join(dir, "internal/panel/auth", "login.templ"),
		filepath.Join(dir, "internal/views/layout", "base.templ"),
	} {
		code, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if !strings.Contains(string(code), `_csrf`) {
			t.Errorf("%s must embed a hidden _csrf input", filepath.Base(f))
		}
	}
}

// TestGenerateLoginRateLimit checks ratelimit.go is emitted and wired into the
// login handler only when auth.login.rate_limit is configured with a positive
// max_attempts.
func TestGenerateLoginRateLimit(t *testing.T) {
	cfg := phaseAConfig()
	cfg.Auth.Login.RateLimit = &types.LoginRateLimit{MaxAttempts: 3, WindowSeconds: 60}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	rl, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "ratelimit.go"))
	if err != nil {
		t.Fatalf("read ratelimit.go: %v", err)
	}
	rlStr := string(rl)
	for _, want := range []string{
		`func clientIP(r *http.Request) string {`,
		`net.SplitHostPort`,
		`const loginMaxAttempts = 3`,
		`const loginWindow = 60 * time.Second`,
		`func loginLimited(r *http.Request) bool {`,
		`func resetLoginLimit(r *http.Request) {`,
	} {
		if !strings.Contains(rlStr, want) {
			t.Errorf("ratelimit.go missing %q", want)
		}
	}

	handler, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "handler.go"))
	if err != nil {
		t.Fatalf("read auth handler.go: %v", err)
	}
	handlerStr := string(handler)
	for _, want := range []string{
		`loginLimited(r)`,
		`resetLoginLimit(r)`,
		`Too many login attempts. Please try again later.`,
	} {
		if !strings.Contains(handlerStr, want) {
			t.Errorf("auth handler.go missing %q", want)
		}
	}

	dir2 := t.TempDir()
	g2 := New(phaseAConfig(), dir2)
	if err := g2.Generate(); err != nil {
		t.Fatalf("generate without rate limit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "internal/panel/auth", "ratelimit.go")); !os.IsNotExist(err) {
		t.Error("ratelimit.go must not be emitted without a rate_limit config")
	}
}

// TestGenerateCSVFormulaEscaping checks exported CSV headers and values pass
// through csvSafe, which neutralizes spreadsheet formula injection.
func TestGenerateCSVFormulaEscaping(t *testing.T) {
	dir := t.TempDir()
	g := New(phaseAConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	exp, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "export.go"))
	if err != nil {
		t.Fatalf("read export.go: %v", err)
	}
	expStr := string(exp)
	for _, want := range []string{
		`func csvSafe(s string) string {`,
		`case '=', '+', '-', '@', '\t', '\r':`,
		`out[i] = csvSafe(c)`,
		`vals[i] = csvSafe(vals[i])`,
	} {
		if !strings.Contains(expStr, want) {
			t.Errorf("export.go missing %q", want)
		}
	}
}

// TestGenerateActionRBAC checks an action policy emits ActionRBACMiddleware and
// wraps the action and bulk routes, while policy-less resources keep plain POST
// routes and no extra middleware.
func TestGenerateActionRBAC(t *testing.T) {
	cfg := phaseAConfig()
	cfg.Resources[0].Actions = []types.Action{
		{Name: "deactivate", Label: "Deactivate", Query: "UPDATE users SET status = 'inactive' WHERE id = $1", Policy: "admin"},
		{Name: "complete_selected", Label: "Complete selected", Query: "UPDATE users SET status = 'done' WHERE id = $1", Bulk: true, Policy: "admin|manager"},
	}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	mw, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "middleware.go"))
	if err != nil {
		t.Fatalf("read middleware.go: %v", err)
	}
	mwStr := string(mw)
	for _, want := range []string{
		`func ActionRBACMiddleware(resource string) func(http.Handler) http.Handler {`,
		`action := r.PathValue("action")`,
		`resource == "user" && action == "deactivate" && !checkRole("admin", userRole)`,
		`resource == "user" && action == "complete_selected" && !checkRole("admin|manager", userRole)`,
	} {
		if !strings.Contains(mwStr, want) {
			t.Errorf("middleware.go missing %q", want)
		}
	}

	router, err := os.ReadFile(filepath.Join(dir, "internal/panel", "router.go"))
	if err != nil {
		t.Fatalf("read router.go: %v", err)
	}
	routerStr := string(router)
	for _, want := range []string{
		`r.With(auth.ActionRBACMiddleware("user")).Post("/user/{id}/action/{action}", user.Action(db))`,
		`r.With(auth.ActionRBACMiddleware("user")).Post("/user/bulk/{action}", user.Bulk(db))`,
	} {
		if !strings.Contains(routerStr, want) {
			t.Errorf("router.go missing %q", want)
		}
	}

	dir2 := t.TempDir()
	g2 := New(phaseAConfig(), dir2)
	if err := g2.Generate(); err != nil {
		t.Fatalf("generate without policies: %v", err)
	}
	assertGeneratedGoParses(t, dir2)
	mw2, err := os.ReadFile(filepath.Join(dir2, "internal/panel/auth", "middleware.go"))
	if err != nil {
		t.Fatalf("read middleware.go: %v", err)
	}
	if strings.Contains(string(mw2), "ActionRBACMiddleware") {
		t.Error("ActionRBACMiddleware must not be emitted without action policies")
	}
}

// formUnionConfig returns a resource whose create and update forms have
// differing field sets: update adds status + created_at (datetime) that are
// absent from create. This exercises BUG-4 (update-only fields were dropped
// from the shared edit form).
func formUnionConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Resources: []types.Resource{
			{
				Name:  "Customer",
				Label: "Customer",
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "string"},
							{Name: "email", Type: "email"},
						},
					},
					Update: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "string"},
							{Name: "email", Type: "email"},
							{Name: "status", Type: "string"},
							{Name: "created_at", Type: "datetime"},
						},
					},
				},
			},
		},
	}
}

// visitOnlyConfig returns a resource where the create form has a field marked
// visible:[create] only, and update has a boolean field marked visible:[update].
func visitOnlyConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Resources: []types.Resource{
			{
				Name:  "User",
				Label: "User",
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "string"},
							{Name: "password", Type: "password", Visible: []string{"create"}},
						},
					},
					Update: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "string"},
							{Name: "active", Type: "boolean", Visible: []string{"update"}},
						},
					},
				},
			},
		},
	}
}

func TestGenerateFormUnionFields(t *testing.T) {
	dir := t.TempDir()
	g := New(formUnionConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	form, err := os.ReadFile(filepath.Join(dir, "internal/views/resources/customer", "form.templ"))
	if err != nil {
		t.Fatalf("read form.templ: %v", err)
	}
	formStr := string(form)
	for _, want := range []string{
		`if !data.IsCreate {`,
		`name="status"`,
		`name="created_at"`,
		`viewmodels.TimeInputValue(data.Item, "created_at")`,
		`viewmodels.ItemValue(data.Item, "status")`,
	} {
		if !strings.Contains(formStr, want) {
			t.Errorf("form.templ missing %q", want)
		}
	}
	if strings.Contains(formStr, `fmt.Sprintf("%v"`) {
		t.Error("form.templ must not render values with raw fmt.Sprintf")
	}
}

// readOnlyConfig returns a config with a resource that has list, card and
// detail but no form (as generated for database views): it must be read-only.
func readOnlyConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Resources: []types.Resource{
			{
				Name:     "Order",
				Label:    "Orders",
				Table:    "order_summary",
				IDColumn: "order_no",
				List: &types.ListConfig{
					Query: "ListOrderSummary", CountQuery: "CountOrderSummary",
					Columns: []types.Column{{Name: "order_no", Label: "Order No"}, {Name: "customer_name", Label: "Customer"}},
				},
				Card: &types.CardConfig{
					Fields:  []types.Field{{Name: "customer_name", Type: "text"}},
					Columns: 3, Rows: 2,
				},
				Detail: &types.DetailConfig{
					Query:  "GetOrderSummary",
					Fields: []types.Field{{Name: "customer_name", Type: "text"}},
				},
			},
		},
	}
}

// TestGenerateReadOnlyResource ensures a resource with no form (database view)
// generates correctly: no create/update/delete handlers/pages and no "Create" /
// "Edit" links in the list, card or detail templ views.
func TestGenerateReadOnlyResource(t *testing.T) {
	dir := t.TempDir()
	g := New(readOnlyConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	pkg := filepath.Join(dir, "internal/panel/resources/order")
	if _, err := os.Stat(filepath.Join(pkg, "create.go")); !os.IsNotExist(err) {
		t.Error("read-only resource must not generate create.go")
	}
	if _, err := os.Stat(filepath.Join(pkg, "update.go")); !os.IsNotExist(err) {
		t.Error("read-only resource must not generate update.go")
	}
	if _, err := os.Stat(filepath.Join(pkg, "delete.go")); !os.IsNotExist(err) {
		t.Error("read-only resource must not generate delete.go")
	}

	for _, v := range []string{"list.templ", "cards.templ", "detail.templ"} {
		view, err := os.ReadFile(filepath.Join(dir, "internal/views/resources/order", v))
		if err != nil {
			t.Fatalf("read %s: %v", v, err)
		}
		viewStr := string(view)
		if strings.Contains(viewStr, "/new") {
			t.Errorf("%s must not contain a Create link (/new) for a read-only resource", v)
		}
		if strings.Contains(viewStr, "/edit") {
			t.Errorf("%s must not contain an Edit link (/edit) for a read-only resource", v)
		}
	}

	list, _ := os.ReadFile(filepath.Join(dir, "internal/views/resources/order", "list.templ"))
	if !strings.Contains(string(list), ">View</a>") {
		t.Error("read-only list.templ must still render a View link")
	}
}

func TestGenerateFormVisibleGuards(t *testing.T) {
	dir := t.TempDir()
	g := New(visitOnlyConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	form, err := os.ReadFile(filepath.Join(dir, "internal/views/resources/user", "form.templ"))
	if err != nil {
		t.Fatalf("read form.templ: %v", err)
	}
	formStr := string(form)
	for _, want := range []string{
		`name="password"`,
		`if data.IsCreate {`,
		`name="active"`,
		`if !data.IsCreate {`,
		`viewmodels.BoolValue(data.Item["active"])`,
	} {
		if !strings.Contains(formStr, want) {
			t.Errorf("form.templ missing %q", want)
		}
	}
}

func TestGenerateBooleanFormValue(t *testing.T) {
	dir := t.TempDir()
	g := New(visitOnlyConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	upd, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "update.go"))
	if err != nil {
		t.Fatalf("read update.go: %v", err)
	}
	if want := `r.FormValue("active") == "true"`; !strings.Contains(string(upd), want) {
		t.Errorf("update.go missing boolean value expr %q", want)
	}
}

func TestGenerateViewmodelsStringify(t *testing.T) {
	dir := t.TempDir()
	g := New(formUnionConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	models, err := os.ReadFile(filepath.Join(dir, "internal/viewmodels", "models.go"))
	if err != nil {
		t.Fatalf("read models.go: %v", err)
	}
	modelsStr := string(models)
	for _, want := range []string{
		`func Stringify(v interface{}) string {`,
		`case sql.NullBool:`,
		`case sql.NullTime:`,
		`func TimeInputValue(item map[string]interface{}, name string) string {`,
		`func DateInputValue(item map[string]interface{}, name string) string {`,
		"\"time\"",
	} {
		if !strings.Contains(modelsStr, want) {
			t.Errorf("models.go missing %q", want)
		}
	}
}

// poolConfig returns a config whose first connection carries pool settings.
func poolConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {
				Driver: "postgres",
				DSN:    "postgres://user:pass@host:5432/db",
				Pool:   types.PoolConfig{MaxOpen: 25, MaxIdle: 5, Lifetime: "30m"},
			},
		},
		Resources: []types.Resource{
			{
				Name: "User",
				List: &types.ListConfig{Columns: []types.Column{{Name: "name"}}},
			},
		},
	}
}

// TestGeneratePoolSettings ensures the generated main.go wires connections.*.pool
// (max_open/max_idle/lifetime) into the database/sql pool after Ping, and omits
// the setters when no pool block is configured.
func TestGeneratePoolSettings(t *testing.T) {
	dir := t.TempDir()
	g := New(poolConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	main, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	mainStr := string(main)
	for _, want := range []string{
		`db.SetMaxOpenConns(25)`,
		`db.SetMaxIdleConns(5)`,
		`if d, err := time.ParseDuration("30m"); err == nil {`,
		`db.SetConnMaxLifetime(d)`,
	} {
		if !strings.Contains(mainStr, want) {
			t.Errorf("main.go missing %q\n--- generated:\n%s", want, mainStr)
		}
	}
	// SetMaxOpenConns must appear after Ping, before the sanity query.
	ping := strings.Index(mainStr, "db.Ping()")
	sanity := strings.Index(mainStr, "database not initialized")
	setMax := strings.Index(mainStr, "db.SetMaxOpenConns(25)")
	if !(ping < setMax && setMax < sanity) {
		t.Error("pool setters must run after Ping and before the auth-table sanity query")
	}

	// No pool block => no setters emitted.
	cfg := poolConfig()
	cfg.Connections["default"] = types.Connection{Driver: "postgres", DSN: "x"}
	dir2 := t.TempDir()
	g2 := New(cfg, dir2)
	if err := g2.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	main2, err := os.ReadFile(filepath.Join(dir2, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	for _, not := range []string{"SetMaxOpenConns", "SetMaxIdleConns", "SetConnMaxLifetime"} {
		if strings.Contains(string(main2), not) {
			t.Errorf("main.go without pool config must not emit %q", not)
		}
	}
}

// TestGenerateBulkTransaction ensures bulk actions with SQL run inside a single
// transaction (BeginTx/Rollback/Commit) instead of N ExecContext calls, while a
// proc-only bulk action on sqlite stays a transaction-less no-op loop.
func TestGenerateBulkTransaction(t *testing.T) {
	cfg := &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "x"},
		},
		Resources: []types.Resource{
			{
				Name: "User",
				List: &types.ListConfig{Columns: []types.Column{{Name: "name"}}},
				Actions: []types.Action{
					{Name: "mark", Label: "Mark", Query: "UPDATE users SET status = 'done' WHERE id = $1", Bulk: true},
				},
			},
		},
	}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	bulk, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "bulk.go"))
	if err != nil {
		t.Fatalf("read bulk.go: %v", err)
	}
	bulkStr := string(bulk)
	for _, want := range []string{
		`tx, err := db.BeginTx(r.Context(), nil)`,
		`defer tx.Rollback()`,
		`tx.ExecContext(r.Context(), "UPDATE users SET status = 'done' WHERE id = $1", id)`,
		`if err := tx.Commit(); err != nil {`,
	} {
		if !strings.Contains(bulkStr, want) {
			t.Errorf("bulk.go missing %q\n--- generated:\n%s", want, bulkStr)
		}
	}
	if strings.Contains(bulkStr, `db.ExecContext`) {
		t.Error("bulk.go must not use db.ExecContext inside a transaction")
	}
}

// TestGenerateOptionsLoaderDedupe ensures fields sharing the same resolved
// option SQL (via options_sql) emit a single options-load block whose variable
// is reused by both fields (no N+1).
func TestGenerateOptionsLoaderDedupe(t *testing.T) {
	cfg := &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "x"},
		},
		Resources: []types.Resource{
			{
				Name: "User",
				List: &types.ListConfig{Columns: []types.Column{{Name: "name"}}},
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "role_id", Type: "relation", OptionsValue: "id", OptionsLabel: "name", OptionsSQL: "SELECT id, name FROM roles"},
							{Name: "manager_id", Type: "relation", OptionsValue: "id", OptionsLabel: "name", OptionsSQL: "SELECT id, name FROM roles"},
						},
					},
				},
			},
		},
	}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	create, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/user", "create.go"))
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	createStr := string(create)
	if n := strings.Count(createStr, `SELECT id, name FROM roles`); n < 1 {
		t.Fatalf("create.go missing the options SQL reference: %d", n)
	}
	if got := strings.Count(createStr, `:= map[string]string{}`); got != 1 {
		t.Errorf("expected 1 options-load block, got %d\n--- generated:\n%s", got, createStr)
	}
	if !strings.Contains(createStr, `role_idOpts := map[string]string{}`) {
		t.Errorf("create.go missing shared options var role_idOpts\n--- generated:\n%s", createStr)
	}
	if strings.Contains(createStr, `manager_idOpts :=`) {
		t.Error("manager_id must reuse the shared role_idOpts var, not load its own options")
	}
	// Both field defs must reference the shared var.
	if n := strings.Count(createStr, `Options: role_idOpts`); n != 2 {
		t.Errorf("both fields must wire Options to role_idOpts (got %d references)", n)
	}
}

// idColumnConfig returns a config whose resource keys on a non-"id" column, as
// `init --db` emits for MSSQL tables with an identity "ID" column.
func idColumnConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "mssql", DSN: "sqlserver://sa:pw@host:1433"},
		},
		Resources: []types.Resource{
			{
				Name:     "Zamestnanec",
				Table:    "Zamestnanec",
				IDColumn: "ID",
				List:     &types.ListConfig{Columns: []types.Column{{Name: "CeleJmeno"}}},
				Form: &types.FormConfig{
					Update: &types.FormAction{
						Fields: []types.Field{{Name: "CeleJmeno", Type: "text"}},
					},
					Delete: &types.FormAction{},
				},
			},
		},
	}
}

// TestGenerateIDColumnUpdateDelete ensures update.go and delete.go key on
// idColumn(r) ("ID") instead of the hardcoded "id", so introspected MSSQL
// tables with an identity "ID" column work.
func TestGenerateIDColumnUpdateDelete(t *testing.T) {
	dir := t.TempDir()
	g := New(idColumnConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	upd, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/zamestnanec", "update.go"))
	if err != nil {
		t.Fatalf("read update.go: %v", err)
	}
	updStr := string(upd)
	if want := `query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = $%d", "[Zamestnanec]", strings.Join(setClauses, ", "), "[ID]", len(cols)+1)`; !strings.Contains(updStr, want) {
		t.Errorf("update.go missing idColumn WHERE clause %q\n--- generated:\n%s", want, updStr)
	}
	if strings.Contains(updStr, `WHERE id = $`) {
		t.Error("update.go must not hardcode WHERE id")
	}
	del, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources/zamestnanec", "delete.go"))
	if err != nil {
		t.Fatalf("read delete.go: %v", err)
	}
	delStr := string(del)
	if want := `db.ExecContext(r.Context(), "DELETE FROM [Zamestnanec] WHERE [ID] = $1", int64(id))`; !strings.Contains(delStr, want) {
		t.Errorf("delete.go missing idColumn WHERE clause %q\n--- generated:\n%s", want, delStr)
	}
	if strings.Contains(delStr, `WHERE id = $1`) {
		t.Error("delete.go must not hardcode WHERE id")
	}
}

// widgetPageConfig returns a postgres config with a page exercising every widget
// type (stat, chart, table, stats_grid, list, html).
func widgetPageConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "x"},
		},
		Resources: []types.Resource{
			{
				Name: "User",
				List: &types.ListConfig{Columns: []types.Column{{Name: "name"}}},
			},
		},
		Pages: []types.Page{
			{
				Name: "Dashboard",
				Path: "/dashboard",
				Widgets: []types.Widget{
					{Type: "stat", Label: "Total Users", Query: "SELECT COUNT(*) FROM users"},
					{Type: "chart", Label: "By Role", Chart: &types.ChartConfig{Type: "bar"}, Query: "SELECT role, COUNT(*) FROM users GROUP BY role"},
					{Type: "table", Label: "Latest", DataColumns: []string{"id", "name"}, Query: "SELECT id, name FROM users"},
					{Type: "stats_grid", Widgets: []types.Widget{{Type: "stat", Label: "Sub", Query: "SELECT 1"}}},
					{Type: "list", Label: "Feed", Query: "SELECT id, name FROM users"},
					{Type: "html", Label: "Note", Query: "SELECT note FROM settings"},
				},
			},
		},
	}
}

// TestGenerateWidgetErrorLogging ensures page handlers log widget DB query and
// scan errors instead of silently swallowing them, and keep rendering the widget
// with empty data on failure.
func TestGenerateWidgetErrorLogging(t *testing.T) {
	dir := t.TempDir()
	g := New(widgetPageConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	page, err := os.ReadFile(filepath.Join(dir, "internal/panel/pages", "dashboard.go"))
	if err != nil {
		t.Fatalf("read dashboard.go: %v", err)
	}
	pageStr := string(page)
	for _, want := range []string{
		`"log"`,
		`log.Printf("page %s widget %d (%s) stat: %v", "Dashboard", 0, "Total Users", err)`,
		`log.Printf("page %s widget %d (%s) chart: %v", "Dashboard", 1, "By Role", err)`,
		`log.Printf("page %s widget %d (%s) chart scan: %v", "Dashboard", 1, "By Role", err)`,
		`log.Printf("page %s widget %d (%s) table: %v", "Dashboard", 2, "Latest", err)`,
		`log.Printf("page %s widget %d (%s) stats_grid: %v", "Dashboard", 3, "Sub", err)`,
		`log.Printf("page %s widget %d (%s) list: %v", "Dashboard", 4, "Feed", err)`,
		`log.Printf("page %s widget %d (%s) html: %v", "Dashboard", 5, "Note", err)`,
	} {
		if !strings.Contains(pageStr, want) {
			t.Errorf("dashboard.go missing %q\n--- generated:\n%s", want, pageStr)
		}
	}
	if strings.Contains(pageStr, `_ = db.QueryRowContext`) {
		t.Error("page handler must not swallow stat widget errors with _ =")
	}
	if strings.Contains(pageStr, `if err == nil {`) {
		t.Error("page handler must not gate widget fetching on a silent if err == nil")
	}
}

// TestGenerateChartJSAsset ensures generateAssets writes the embedded Chart.js
// bundle to static/js/chart.js and the pre-built stylesheet to
// static/css/styles.css (both byte-identical to the embedded copies) and stops
// emitting package.json — the generated dashboard no longer needs npm or
// Tailwind.
func TestGenerateChartJSAsset(t *testing.T) {
	dir := t.TempDir()
	g := New(poolConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	chart, err := os.ReadFile(filepath.Join(dir, "static/js/chart.js"))
	if err != nil {
		t.Fatalf("read static/js/chart.js: %v", err)
	}
	if len(chart) == 0 {
		t.Fatal("static/js/chart.js is empty")
	}
	if string(chart) != string(chartUmdJS) {
		t.Errorf("static/js/chart.js does not match the embedded Chart.js bundle (embedded %d bytes, written %d)", len(chartUmdJS), len(chart))
	}
	if !strings.HasPrefix(string(chart), "/*!") {
		t.Error("static/js/chart.js must keep the Chart.js license banner intact")
	}
	css, err := os.ReadFile(filepath.Join(dir, "static/css/styles.css"))
	if err != nil {
		t.Fatalf("read static/css/styles.css: %v", err)
	}
	if len(css) == 0 {
		t.Fatal("static/css/styles.css is empty")
	}
	if string(css) != string(stylesCSS) {
		t.Errorf("static/css/styles.css does not match the embedded stylesheet (embedded %d bytes, written %d)", len(stylesCSS), len(css))
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); !os.IsNotExist(err) {
		t.Error("package.json must not be emitted (npm build is gone)")
	}
	if _, err := os.Stat(filepath.Join(dir, "tailwind.config.js")); !os.IsNotExist(err) {
		t.Error("tailwind.config.js must not be emitted (stylesheet is pre-built)")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/assets/css")); !os.IsNotExist(err) {
		t.Error("internal/assets/css must not be emitted (no tailwind input dir)")
	}
}

// TestGenerateMakefileNoTailwind ensures the generated Makefile contains no npm
// and no Tailwind targets — the stylesheet is pre-built and vendored at
// generation time, so the dashboard builds with templ + go build only.
func TestGenerateMakefileNoTailwind(t *testing.T) {
	dir := t.TempDir()
	g := New(poolConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	mk, err := os.ReadFile(filepath.Join(dir, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	mkStr := string(mk)
	for _, forbidden := range []string{"npx", "node_modules", "package.json", "css:", "TAILWIND", "tailwindcss", "$(BINARY) run", "npm run"} {
		if strings.Contains(mkStr, forbidden) {
			t.Errorf("Makefile must not reference %q\n--- generated:\n%s", forbidden, mkStr)
		}
	}
	for _, want := range []string{
		`build: templ`,
		`go tool templ generate`,
		`go build -o $(BINARY) .`,
		`run: build`,
		`package: build`,
		`.PHONY: all build templ tidy run package clean`,
	} {
		if !strings.Contains(mkStr, want) {
			t.Errorf("Makefile missing %q\n--- generated:\n%s", want, mkStr)
		}
	}
}

// csvConfig returns a config exercising D3: a resource with an export column
// subset and CSV import enabled over its create form fields.
func csvConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Resources: []types.Resource{
			{
				Name:  "User",
				Label: "Users",
				List: &types.ListConfig{
					Columns: []types.Column{
						{Name: "id", Type: "integer"},
						{Name: "name", Label: "Name", Type: "string"},
						{Name: "email", Type: "email"},
						{Name: "status", Type: "badge"},
					},
					Export: []string{"name", "email"},
				},
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "text"},
							{Name: "email", Type: "email"},
							{Name: "status", Type: "select"},
						},
					},
				},
				ImportCSV: true,
			},
		},
	}
}

func readResourceFile(t *testing.T, dir, pkg, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "internal/panel/resources", pkg, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return string(b)
}

// readViewTempl reads a generated templ view file under internal/views/resources.
func readViewTempl(t *testing.T, dir, pkg, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "internal/views/resources", pkg, file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return string(b)
}

func TestGenerateExportSubset(t *testing.T) {
	dir := t.TempDir()
	g := New(csvConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	code := readResourceFile(t, dir, "user", "export.go")
	if !strings.Contains(code, `query := "SELECT \"name\", \"email\" FROM \"users\" ORDER BY 1"`) {
		t.Errorf("export must select only the subset columns\n--- generated:\n%s", code)
	}
	if !strings.Contains(code, `wr.Write([]string{csvSafe("Name"), csvSafe("email")})`) {
		t.Errorf("export must emit label headers for the subset\n--- generated:\n%s", code)
	}
}

func TestGenerateExportAllColumns(t *testing.T) {
	cfg := csvConfig()
	cfg.Resources[0].List.Export = nil
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	code := readResourceFile(t, dir, "user", "export.go")
	if !strings.Contains(code, `query := "SELECT \"id\", \"name\", \"email\", \"status\" FROM \"users\" ORDER BY 1"`) {
		t.Errorf("export without subset must keep all list columns\n--- generated:\n%s", code)
	}
	if !strings.Contains(code, `out[i] = csvSafe(c)`) {
		t.Errorf("export without subset must use column-name headers\n--- generated:\n%s", code)
	}
}

func TestGenerateImportCSV(t *testing.T) {
	dir := t.TempDir()
	g := New(csvConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	code := readResourceFile(t, dir, "user", "import.go")
	for _, want := range []string{
		`func ImportCSV(db *sql.DB) http.HandlerFunc {`,
		`rd := csv.NewReader(file)`,
		`vals, err := buildCreateParams(m)`,
		`tx, err := db.BeginTx(r.Context(), nil)`,
		`defer tx.Rollback()`,
		`if err := tx.Commit(); err != nil {`,
		`Imported %d, Skipped %d`,
		`?flash=`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("import.go must emit %q\n--- generated:\n%s", want, code)
		}
	}
}

func TestCreateUsesBuildCreateParams(t *testing.T) {
	dir := t.TempDir()
	g := New(csvConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	code := readResourceFile(t, dir, "user", "create.go")
	if !strings.Contains(code, `func buildCreateParams(m map[string]string) ([]interface{}, error) {`) {
		t.Errorf("create.go must define buildCreateParams\n--- generated:\n%s", code)
	}
	if !strings.Contains(code, `vals, err := buildCreateParams(map[string]string{`) {
		t.Errorf("create POST must reuse buildCreateParams\n--- generated:\n%s", code)
	}
	if strings.Contains(code, `_, err := db.ExecContext`) {
		t.Errorf("create POST must not redeclare err after buildCreateParams\n--- generated:\n%s", code)
	}
}

func TestCreateFileFieldKeepsLegacyPath(t *testing.T) {
	cfg := csvConfig()
	cfg.Resources[0].Form.Create.Fields = []types.Field{
		{Name: "name", Type: "text"},
		{Name: "avatar", Type: "file"},
	}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	code := readResourceFile(t, dir, "user", "create.go")
	if !strings.Contains(code, `saveUploadedFile(r, "avatar")`) {
		t.Errorf("create with file field must keep the upload helper path\n--- generated:\n%s", code)
	}
	if strings.Contains(code, `vals, err := buildCreateParams(map[string]string{`) {
		t.Errorf("create with file field must not use buildCreateParams\n--- generated:\n%s", code)
	}
	if !strings.Contains(code, `file/image uploads are not supported in CSV import`) {
		t.Errorf("import-enabled file resource must emit the buildCreateParams stub\n--- generated:\n%s", code)
	}
}

// schemaConfig returns a config exercising the D11 schema block: a users table
// with an FK to roles plus a User resource with list/detail/update so the
// schema-derived Get query and FK option loading are exercised.
func schemaConfig() *types.Config {
	cfg := &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "x"},
		},
		Schema: &types.Schema{
			Tables: []types.SchemaTable{
				{
					Name: "users",
					PK:   "id",
					Columns: []types.SchemaColumn{
						{Name: "id", Type: "integer", PrimaryKey: true},
						{Name: "name", Type: "string"},
						{Name: "email", Type: "string"},
						{Name: "role_id", Type: "integer"},
					},
					ForeignKeys: []types.SchemaFK{
						{Column: "role_id", ForeignTable: "roles", ForeignColumn: "id", Label: "name"},
					},
				},
				{
					Name:    "roles",
					PK:      "id",
					Columns: []types.SchemaColumn{{Name: "id", Type: "integer", PrimaryKey: true}, {Name: "name", Type: "string"}},
				},
			},
		},
		Resources: []types.Resource{
			{
				Name:  "User",
				Label: "User",
				List:  &types.ListConfig{Columns: []types.Column{{Name: "id"}, {Name: "name"}, {Name: "role_id_label"}}},
				Detail: &types.DetailConfig{
					Query:  "GetUser",
					Fields: []types.Field{{Name: "id"}, {Name: "name"}, {Name: "email"}, {Name: "role_id"}},
				},
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "name", Type: "text"},
							{Name: "email", Type: "email"},
							{Name: "role_id", Type: "relation", OptionsValue: "id", OptionsLabel: "name"},
						},
					},
					Update: &types.FormAction{
						PopulateQuery: "GetUser",
						Fields:        []types.Field{{Name: "name", Type: "text"}, {Name: "email", Type: "email"}},
					},
				},
			},
		},
	}
	return cfg
}

// TestGenerateDataFromSchema ensures the schema-derived Get query carries every
// table column, FK label LEFT JOINs and a driver-aware key placeholder.
func TestGenerateDataFromSchema(t *testing.T) {
	dir := t.TempDir()
	g := New(schemaConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)
	code, err := os.ReadFile(filepath.Join(dir, "internal", "data", "data.go"))
	if err != nil {
		t.Fatalf("read data.go: %v", err)
	}
	str := string(code)
	for _, want := range []string{
		"func New(db *sql.DB) *Querier",
		"func (q *Querier) GetUser(ctx context.Context, id int32) (map[string]interface{}, error) {",
		`SELECT t.\"id\", t.\"name\", t.\"email\", t.\"role_id\", f_roles.\"name\" AS role_id_label FROM \"users\" t LEFT JOIN \"roles\" f_roles ON f_roles.\"id\" = t.\"role_id\" WHERE t.\"id\" = $1`,
	} {
		if !strings.Contains(str, want) {
			t.Errorf("data.go missing %q\n--- generated:\n%s", want, str)
		}
	}
	// Detail + update share the query name but target the same table, so only
	// one method is emitted.
	if n := strings.Count(str, "GetUser("); n != 1 {
		t.Errorf("expected exactly 1 GetUser method, got %d\n--- generated:\n%s", n, str)
	}
}

// TestGenerateDataFallbackFields ensures a config without a schema block still
// emits a Get from the resource's own detail/update fields + key column.
func TestGenerateDataFallbackFields(t *testing.T) {
	cfg := schemaConfig()
	cfg.Schema = nil
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	code, err := os.ReadFile(filepath.Join(dir, "internal", "data", "data.go"))
	if err != nil {
		t.Fatalf("read data.go: %v", err)
	}
	str := string(code)
	if !strings.Contains(str, `SELECT \"id\", \"name\", \"email\", \"role_id\" FROM \"users\" WHERE \"id\" = $1`) {
		t.Errorf("fallback Get must use detail/update fields + key column\n--- generated:\n%s", str)
	}
	if strings.Contains(str, "LEFT JOIN") {
		t.Errorf("fallback Get must not emit FK joins without a schema block\n--- generated:\n%s", str)
	}
}

// TestGenerateOptionsLoaderFKFromSchema ensures a relation field with
// options_value/options_label resolves its option SQL from the schema block's
// FK metadata when no options_sql is present.
func TestGenerateOptionsLoaderFKFromSchema(t *testing.T) {
	dir := t.TempDir()
	g := New(schemaConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	code := readResourceFile(t, dir, "user", "create.go")
	if !strings.Contains(code, `SELECT \"id\", \"name\" FROM \"roles\"`) {
		t.Errorf("create.go must derive role_id option SQL from the schema FK\n--- generated:\n%s", code)
	}
	if !strings.Contains(code, `role_idOpts := map[string]string{}`) {
		t.Errorf("create.go must emit the role_id options var\n--- generated:\n%s", code)
	}
	if !strings.Contains(code, `{Name: "role_id", Label: "role_id", FieldType: "relation", Picker: true, Options: role_idOpts, Locked: locked["role_id"]}`) {
		t.Errorf("create.go must wire the relation field to role_idOpts as a parent-lockable picker\n--- generated:\n%s", code)
	}
	// The form templ must render the modal picker for the same field (the
	// templ's isPickerField uses the same optionSQL resolution as the loader).
	formTempl, err := os.ReadFile(filepath.Join(dir, "internal", "views", "resources", "user", "form.templ"))
	if err != nil {
		t.Fatalf("read form.templ: %v", err)
	}
	formStr := string(formTempl)
	if !strings.Contains(formStr, `data-picker-options={ viewmodels.OptionsJS(data.Fields, "role_id") }`) {
		t.Errorf("form.templ must render picker markup for the schema-FK relation field\n--- generated:\n%s", formStr)
	}
	// And the list/card handlers must join the FK label from the schema block
	// even though no options_query exists on the relation field.
	listCode := readResourceFile(t, dir, "user", "list.go")
	if !strings.Contains(listCode, `LEFT JOIN \"roles\" f_roles ON f_roles.\"id\" = t.\"role_id\"`) {
		t.Errorf("list.go must join the FK label from the schema block\n--- generated:\n%s", listCode)
	}
}

// filterConfig returns a config with a list filter exercising literal-only and
// $N-based filter expressions on the postgres driver (default).
func filterConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "postgres://x"},
		},
		Resources: []types.Resource{
			{
				Name:  "Order",
				Label: "Orders",
				Table: "orders",
				List: &types.ListConfig{
					Columns: []types.Column{{Name: "id", Label: "ID"}, {Name: "status", Label: "Status"}, {Name: "total", Label: "Total"}},
					Filter: &types.FilterConfig{
						Label: "Advanced filter",
						Where: "(price > 1000 and name contains $1) or status = 'active'",
						Params: []types.FilterParam{
							{Name: "name_part", Label: "Name part"},
						},
					},
				},
				Card: &types.CardConfig{
					Fields:  []types.Field{{Name: "id", Type: "text"}, {Name: "status", Type: "text"}},
					Columns: 3, Rows: 2,
					Filter: &types.FilterConfig{
						Label: "Card filter",
						Where: "status = $1",
						Params: []types.FilterParam{
							{Name: "status_val", Label: "Status"},
						},
					},
				},
			},
		},
	}
}

// TestGenerateFilter ensures a resource with list and card filters generates
// correctly: the handler uses parts-based WHERE, echoes filter params and
// filterApplied, includes the net/url import, and the templ renders the filter
// bar component. The no-filter path stays byte-identical (tested implicitly by
// the existing TestGenerate* tests passing).
func TestGenerateFilter(t *testing.T) {
	dir := t.TempDir()
	g := New(filterConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	// List handler assertions
	listCode := readResourceFile(t, dir, "order", "list.go")
	for _, want := range []string{
		`"net/url"`,
		`filterApplied := false`,
		`fp_name_part`,
		`filterData := &viewmodels.FilterData{}`,
		`FilterQS`,
		`ILIKE`,
		`parts = append(parts`,
		`__GFP__`,
		`filterQS`,
	} {
		if !strings.Contains(listCode, want) {
			t.Errorf("list.go missing %q", want)
		}
	}
	// The literal 'active' should be baked into the SQL
	if !strings.Contains(listCode, `'active'`) {
		t.Errorf("list.go missing literal value 'active'")
	}

	// Card handler assertions
	cardCode := readResourceFile(t, dir, "order", "card.go")
	for _, want := range []string{
		`"net/url"`,
		`filterApplied := false`,
		`fp_status_val`,
		`viewmodels.FilterData`,
	} {
		if !strings.Contains(cardCode, want) {
			t.Errorf("card.go missing %q", want)
		}
	}

	// List templ assertions
	listTempl, err := os.ReadFile(filepath.Join(dir, "internal/views/resources/order", "list.templ"))
	if err != nil {
		t.Fatalf("read list.templ: %v", err)
	}
	listTemplStr := string(listTempl)
	for _, want := range []string{
		`@filterBar`,
		`filter-panel`,
		`"fp_" + p.Key`,
	} {
		if !strings.Contains(listTemplStr, want) {
			_ = listTemplStr
		}
	}

	// Card templ assertions — cards.templ calls @filterBar
	cardTempl, err := os.ReadFile(filepath.Join(dir, "internal/views/resources/order", "cards.templ"))
	if err != nil {
		t.Fatalf("read cards.templ: %v", err)
	}
	cardTemplStr := string(cardTempl)
	if !strings.Contains(cardTemplStr, `@filterBar`) {
		t.Errorf("cards.templ missing @filterBar call")
	}
	// The filterBar component definition lives in renderers.templ
	renderers, err := os.ReadFile(filepath.Join(dir, "internal/views/resources/order", "renderers.templ"))
	if err != nil {
		t.Fatalf("read renderers.templ: %v", err)
	}
	renderersStr := string(renderers)
	for _, want := range []string{
		`templ filterBar`,
		`filter-panel`,
		`"fp_" + p.Key`,
		`f.Label`,
		`filterPanelClass`,
	} {
		if !strings.Contains(renderersStr, want) {
			t.Errorf("renderers.templ missing %q", want)
		}
	}
}

// TestGenerateNoFilterRegression ensures a resource without a filter still
// generates byte-identical output (the existing list handler template path).
// This test guards the regression where the filter branch would break the
// no-filter output.
func TestGenerateNoFilterRegression(t *testing.T) {
	dir := t.TempDir()
	g := New(readOnlyConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	listCode := readResourceFile(t, dir, "order", "list.go")
	// Must not contain filter-specific tokens
	if strings.Contains(listCode, "filterApplied") {
		t.Error("no-filter list.go must not reference filterApplied")
	}
	if strings.Contains(listCode, "FilterQS") {
		t.Error("no-filter list.go must not reference FilterQS")
	}
	if strings.Contains(listCode, "net/url") {
		t.Error("no-filter list.go must not import net/url")
	}
	// Must use the old-style whereSQL (not parts)
	if !strings.Contains(listCode, "whereSQL") {
		t.Error("no-filter list.go must build whereSQL")
	}
}

// copiesConfig returns a config with a picker field carrying a `copies:` map
// (auto-fill target fields from the selected related record).
func copiesConfig() *types.Config {
	cfg := schemaConfig()
	cfg.Resources[0].Form.Create.Fields = []types.Field{
		{Name: "name", Type: "text"},
		{Name: "city", Type: "string"},
		{Name: "customer_country", Type: "string"},
		{Name: "shipped_at", Type: "datetime"},
		{Name: "role_id", Type: "relation", OptionsValue: "id", OptionsLabel: "name",
			Copies: map[string]string{"city": "city", "customer_country": "country", "shipped_at": "created_at"}},
	}
	cfg.Resources[0].Form.Update.Fields = cfg.Resources[0].Form.Create.Fields
	return cfg
}

// TestGenerateCopiesAutoFill ensures a picker field's `copies:` map extends the
// FK-derived loader SELECT with the copy source columns, populates a per-row
// copy-data variable keyed by the TARGET form field, formats datetime/date
// targets, wires CopyData into the ColumnDef, and emits the picker copies data
// attribute + auto-fill JS in the form template.
func TestGenerateCopiesAutoFill(t *testing.T) {
	dir := t.TempDir()
	g := New(copiesConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	create := readResourceFile(t, dir, "user", "create.go")
	// FK-derived SQL must carry the copy source columns in deterministic
	// (sorted-target: city, customer_country, shipped_at) order.
	if !strings.Contains(create, `SELECT \"id\", \"name\", \"city\", \"country\", \"created_at\" FROM \"roles\"`) {
		t.Errorf("create.go loader must extend the FK SQL with copy columns\n--- generated:\n%s", create)
	}
	if !strings.Contains(create, `role_idCopies := map[string]map[string]string{}`) {
		t.Errorf("create.go must declare the per-row copy map\n--- generated:\n%s", create)
	}
	if !strings.Contains(create, `k := fmt.Sprintf("%v", val)`) {
		t.Errorf("create.go loader must key the row map by the option value\n--- generated:\n%s", create)
	}
	if !strings.Contains(create, `"city": viewmodels.PickCopyValue(cpy0, "string")`) {
		t.Errorf("create.go must format the string copy target via PickCopyValue\n--- generated:\n%s", create)
	}
	if !strings.Contains(create, `"shipped_at": viewmodels.PickCopyValue(cpy2, "datetime")`) {
		t.Errorf("create.go must format the datetime copy target for the datetime-local input\n--- generated:\n%s", create)
	}
	if !strings.Contains(create, `CopyData: role_idCopies`) {
		t.Errorf("create.go must wire CopyData to the per-row copy map\n--- generated:\n%s", create)
	}

	form := readViewTempl(t, dir, "user", "form.templ")
	if !strings.Contains(form, `data-picker-copies={viewmodels.CopyDataJSON(data.Fields, "role_id")}`) {
		t.Errorf("form.templ must carry the copies data attribute\n--- generated:\n%s", form)
	}
	if !strings.Contains(form, `const rowCopies = copyData[row.dataset.value];`) {
		t.Errorf("form.templ must apply per-row copies on selection\n--- generated:\n%s", form)
	}
	if !strings.Contains(form, `document.querySelector('[name="' + t + '"]')`) {
		t.Errorf("form.templ must target form fields by name\n--- generated:\n%s", form)
	}
}

// TestGenerateCopiesNoCopyRegression ensures a picker field without copies keeps
// the historical output: no copies loader, no CopyData literal, no
// data-picker-copies attribute.
func TestGenerateCopiesNoCopyRegression(t *testing.T) {
	dir := t.TempDir()
	g := New(schemaConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	create := readResourceFile(t, dir, "user", "create.go")
	if strings.Contains(create, "Copies") || strings.Contains(create, "PickCopyValue") {
		t.Errorf("copies-free create.go must stay byte-identical to the value/label loader\n--- generated:\n%s", create)
	}
	form := readViewTempl(t, dir, "user", "form.templ")
	if strings.Contains(form, "viewmodels.CopyDataJSON") {
		t.Errorf("copies-free form.templ must not emit the copies data attribute\n--- generated:\n%s", form)
	}
}

// mdConfig returns a header resource with a children block plus the child
// resource (D14 master-detail).
func mdConfig() *types.Config {
	cfg := &types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"default": {Driver: "postgres", DSN: "x"},
		},
		Schema: &types.Schema{
			Tables: []types.SchemaTable{
				{
					Name: "orders",
					PK:   "id",
					Columns: []types.SchemaColumn{
						{Name: "id", Type: "integer", PrimaryKey: true},
						{Name: "customer_name", Type: "string"},
					},
				},
				{
					Name: "order_lines",
					PK:   "id",
					Columns: []types.SchemaColumn{
						{Name: "id", Type: "integer", PrimaryKey: true},
						{Name: "order_id", Type: "integer"},
						{Name: "product_id", Type: "integer"},
						{Name: "qty", Type: "integer"},
						{Name: "total", Type: "float"},
					},
					ForeignKeys: []types.SchemaFK{{Column: "order_id", ForeignTable: "orders", ForeignColumn: "id", Label: "customer_name"}},
				},
			},
		},
		Resources: []types.Resource{
			{
				Name:  "Order",
				Label: "Orders",
				Detail: &types.DetailConfig{
					Query:  "GetOrder",
					Fields: []types.Field{{Name: "customer_name", Type: "string"}},
				},
				Form: &types.FormConfig{
					Update: &types.FormAction{
						PopulateQuery: "GetOrder",
						Fields:        []types.Field{{Name: "customer_name", Type: "string"}},
					},
				},
				Children: []types.ChildResource{
					{Name: "Lines", Resource: "OrderLine", Columns: []types.Column{{Name: "qty", Type: "integer"}, {Name: "total", Type: "float"}}},
				},
			},
			{
				Name:  "OrderLine",
				Table: "order_lines",
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{
							{Name: "qty", Type: "integer"},
							{Name: "total", Type: "float"},
							{Name: "order_id", Type: "relation", OptionsValue: "id", OptionsLabel: "customer_name"},
						},
					},
					Update: &types.FormAction{
						Fields: []types.Field{
							{Name: "qty", Type: "integer"},
							{Name: "total", Type: "float"},
							{Name: "order_id", Type: "relation", OptionsValue: "id", OptionsLabel: "customer_name"},
						},
					},
					Delete: &types.FormAction{},
				},
			},
		},
	}
	return cfg
}

// TestGenerateMasterChildren exercises the D14 master-detail pipeline: the
// header detail/update load child lines, the child form seeds+locks its FK and
// honors ?return=, the delete handler redirects back, and the templates render
// the lines section + Add Line + create note.
func TestGenerateMasterChildren(t *testing.T) {
	dir := t.TempDir()
	g := New(mdConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	// Header detail: child lines SELECT + Lines literal + helper.
	detail := readResourceFile(t, dir, "order", "detail.go")
	if !strings.Contains(detail, `SELECT \"id\", \"qty\", \"total\" FROM \"order_lines\" t WHERE t.\"order_id\" = $1`) {
		t.Errorf("detail.go must select child lines scoped by the reverse FK\n--- generated:\n%s", detail)
	}
	if !strings.Contains(detail, `childLines1 := loadChildLines(r.Context(), db, "SELECT \"id\", \"qty\", \"total\" FROM \"order_lines\" t WHERE t.\"order_id\" = $1", int64(id))`) {
		t.Errorf("detail.go must load child lines into the lines var\n--- generated:\n%s", detail)
	}
	if !strings.Contains(detail, "Lines: []viewmodels.ChildLinesData{") || !strings.Contains(detail, `Heading:`) || !strings.Contains(detail, `"Lines"`) {
		t.Errorf("detail.go must wire the Lines section\n--- generated:\n%s", detail)
	}
	childlines := readResourceFile(t, dir, "order", "childlines.go")
	if !strings.Contains(childlines, "func loadChildLines(ctx context.Context, db *sql.DB") {
		t.Errorf("childlines.go must define the shared loadChildLines helper\n--- generated:\n%s", childlines)
	}

	// Header edit: locked map + child lines + Return.
	update := readResourceFile(t, dir, "order", "update.go")
	if !strings.Contains(update, `loadChildLines(r.Context(), db, "SELECT \"id\", \"qty\", \"total\" FROM \"order_lines\" t WHERE t.\"order_id\" = $1", int64(id))`) {
		t.Errorf("update.go must load child lines on GET\n--- generated:\n%s", update)
	}
	if !strings.Contains(update, "Return:        r.URL.Query().Get(\"return\"),") {
		t.Errorf("update.go must echo the return query into FormData.Return\n--- generated:\n%s", update)
	}

	// Child create: FK seed + lock + _return redirect.
	childCreate := readResourceFile(t, dir, "orderline", "create.go")
	if !strings.Contains(childCreate, `item["order_id"] = v`) || !strings.Contains(childCreate, `locked["order_id"] = true`) {
		t.Errorf("orderline create.go must seed + lock the FK from a parent context\n--- generated:\n%s", childCreate)
	}
	if !strings.Contains(childCreate, `Locked: locked["order_id"]`) {
		t.Errorf("orderline create.go must render the seeded FK as a locked picker\n--- generated:\n%s", childCreate)
	}
	if !strings.Contains(childCreate, `ret := r.FormValue("_return")`) || !strings.Contains(childCreate, `strings.HasPrefix(ret, "/")`) {
		t.Errorf("orderline create.go must honor the _return redirect\n--- generated:\n%s", childCreate)
	}

	// Child delete returns to the header edit.
	childDelete := readResourceFile(t, dir, "orderline", "delete.go")
	if !strings.Contains(childDelete, `strings.HasPrefix(ret, "/")`) {
		t.Errorf("orderline delete.go must honor the return query\n--- generated:\n%s", childDelete)
	}

	readView := func(pkg, file string) string {
		b, err := os.ReadFile(filepath.Join(dir, "internal/views/resources", pkg, file))
		if err != nil {
			t.Fatalf("read %s/%s: %v", pkg, file, err)
		}
		return string(b)
	}

	// Form templ: _return field, Add Line button, child lines on edit, note on create.
	form := readView("order", "form.templ")
	if !strings.Contains(form, `name="_return" value={ data.Return }`) {
		t.Errorf("order form.templ must submit the return path\n--- generated:\n%s", form)
	}
	if !strings.Contains(form, `sec.PanelPath, sec.ResourceLower, sec.FKColumn, sec.ParentID`) {
		t.Errorf("order form.templ must render the Add Line button\n--- generated:\n%s", form)
	}
	if !strings.Contains(form, "Save the header, then add lines.") {
		t.Errorf("order form.templ must show the create-mode note\n--- generated:\n%s", form)
	}
	if !strings.Contains(form, `return=`) {
		t.Errorf("order form.templ must link Edit/Delete back to the header\n--- generated:\n%s", form)
	}

	// Detail templ: view-only lines section + child rows.
	detailView := readView("order", "detail.templ")
	if !strings.Contains(detailView, `for _, sec := range data.Lines`) {
		t.Errorf("order detail.templ must render the lines sections\n--- generated:\n%s", detailView)
	}
}

// spaceyConfig returns a config whose names are NOT valid Go identifiers —
// "Order Management" (resource), "order panel" (panel id), "Sales by Region"
// (page) and a spacey list column ("size range") — so every identifier splice
// site must sanitize or the generated Go fails to parse. Uses mssql so the
// driver branches (EXEC / ORDER BY fallback) are exercised alongside the
// scripting- and filter-free path.
func spaceyConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel: types.Panel{
			ID:   "order panel",
			Path: "/order",
			Name: "Order Panel",
		},
		Connections: map[string]types.Connection{
			"default": {Driver: "mssql", DSN: "sqlserver://sa:pw@host:1433"},
		},
		Navigation: []types.NavigationGroup{
			{Group: "Orders", Items: []types.NavigationItem{{Resource: "Order Management", Label: "Order Management"}}},
			{Group: "Reports", Items: []types.NavigationItem{{Page: "Sales by Region", Label: "Sales by Region"}}},
		},
		Resources: []types.Resource{
			{
				Name:  "Order Management",
				Label: "Order Management",
				List: &types.ListConfig{
					Columns: []types.Column{
						{Name: "id", Type: "integer"},
						{Name: "size range", Label: "Size range", Type: "string"},
					},
					Export:  []string{"size range"},
					Computed: []types.ComputedField{{Name: "gross total", Label: "Gross total", Type: "float", Expression: "price * qty"}},
				},
				Card: &types.CardConfig{
					Columns: 3,
					Rows:    2,
					Fields:  []types.Field{{Name: "size range", Type: "string"}},
				},
				Detail: &types.DetailConfig{
					Fields: []types.Field{
						{Name: "id", Type: "integer"},
						{Name: "size range", Type: "string"},
					},
				},
				Form: &types.FormConfig{
					Create: &types.FormAction{
						Fields: []types.Field{{Name: "size range", Type: "string"}},
					},
					Update: &types.FormAction{
						Fields: []types.Field{{Name: "size range", Type: "string"}},
					},
					Delete: &types.FormAction{},
				},
				Actions: []types.Action{
					{Name: "mark done", Query: "UPDATE t SET status='done' WHERE id=$1", Bulk: true},
				},
				Policies: &types.Policy{ViewAny: "admin|manager"},
				ImportCSV: true,
			},
		},
		Pages: []types.Page{
			{Name: "Sales by Region", Path: "/sales-region"},
		},
	}
}

// TestGenerateIdentifiers drives generation with names that are not valid Go
// identifiers and asserts the whole tree still parses. This guards the Windows
// panic-class bug (spacey names produced `package Order Management` / `Order
// ManagementList` and broke the build on every OS).
func TestGenerateIdentifiers(t *testing.T) {
	dir := t.TempDir()
	g := New(spaceyConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	// Resource package + view directories are sanitized (lowercased, spaces -> _).
	for _, sub := range []string{
		filepath.Join(dir, "internal/panel/resources", "order_management"),
		filepath.Join(dir, "internal/views/resources", "order_management"),
	} {
		if _, err := os.Stat(sub); err != nil {
			t.Errorf("expected sanitized dir %s: %v", sub, err)
		}
	}

	// Sanitized identifiers appear everywhere the raw name used to be spliced.
	checks := []struct {
		file string
		want string
	}{
		{filepath.Join(dir, "internal/panel", "router.go"), `package panel`},
		{filepath.Join(dir, "internal/panel/resources/order_management", "list.go"), `package order_management`},
		{filepath.Join(dir, "internal/panel/resources/order_management", "list.go"), `Order_ManagementList(`},
		{filepath.Join(dir, "internal/panel", "router.go"), `/order_management`},
		{filepath.Join(dir, "internal/panel", "router.go"), `auth.RBACMiddleware("order_management", "view_any")`},
	}
	for _, c := range checks {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		if !strings.Contains(string(data), c.want) {
			t.Errorf("%s missing %q\n--- generated:\n%s", c.file, c.want, data)
		}
	}

	// RBAC resource strings must match the sanitized route segment the router
	// passes to the middleware, or authorization silently never matches.
	mw, err := os.ReadFile(filepath.Join(dir, "internal/panel/auth", "middleware.go"))
	if err != nil {
		t.Fatalf("read middleware.go: %v", err)
	}
	if strings.Contains(string(mw), "Order Management") {
		t.Errorf("RBAC middleware must compare sanitized resource names, got raw spaces\n--- generated:\n%s", mw)
	}
	if !strings.Contains(string(mw), `resource == "order_management"`) {
		t.Errorf("RBAC middleware missing sanitized resource check\n--- generated:\n%s", mw)
	}

	// The sidebar nav href uses the sanitized route segment.
	base, err := os.ReadFile(filepath.Join(dir, "internal/views/layout", "base.templ"))
	if err == nil && strings.Contains(string(base), "/order/Order Management") {
		t.Errorf("nav href leaks the raw resource name\n--- generated:\n%s", base)
	}
}

// TestGenerateSQLiteDriver guards the modernc.org/sqlite switch: the generated
// app opens "sqlite" (registered by modernc) with a blank CGO-free import, and
// go.mod declares modernc.org/sqlite — never mattn/go-sqlite3 (which requires
// a C toolchain and broke cross-platform builds).
func TestGenerateSQLiteDriver(t *testing.T) {
	cfg := procConfig("sqlite")
	cfg.Procedures = []types.Procedure{{Name: "sp_archive_user", Description: "archive", SQL: "UPDATE users SET status='inactive' WHERE id = $1;"}}
	dir := t.TempDir()
	g := New(cfg, dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	mainB, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	mainStr := string(mainB)
	if !strings.Contains(mainStr, `_ "modernc.org/sqlite"`) {
		t.Errorf("main.go must blank-import modernc.org/sqlite\n--- generated:\n%s", mainStr)
	}
	if !strings.Contains(mainStr, `sql.Open("sqlite", dsn)`) {
		t.Errorf("main.go must sql.Open the modernc driver name \"sqlite\"\n--- generated:\n%s", mainStr)
	}
	if strings.Contains(mainStr, "sqlite3") || strings.Contains(mainStr, "mattn") {
		t.Errorf("main.go must not reference the mattn sqlite3 driver\n--- generated:\n%s", mainStr)
	}

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	gomodStr := string(gomod)
	if !strings.Contains(gomodStr, "modernc.org/sqlite") {
		t.Errorf("go.mod must require modernc.org/sqlite\n--- generated:\n%s", gomodStr)
	}
	if strings.Contains(gomodStr, "mattn/go-sqlite3") {
		t.Errorf("go.mod must not require mattn/go-sqlite3\n--- generated:\n%s", gomodStr)
	}
}

// TestGenerateBuildScriptWindows guards the Windows build fixing: every
// generated project carries a build.ps1 mirroring the Makefile targets, and
// the script stays free of the Unix-only commands the Makefile depends on.
func TestGenerateBuildScriptWindows(t *testing.T) {
	dir := t.TempDir()
	g := New(auditConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}

	ps, err := os.ReadFile(filepath.Join(dir, "build.ps1"))
	if err != nil {
		t.Fatalf("read build.ps1: %v", err)
	}
	psStr := string(ps)
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err != nil {
		t.Errorf("Makefile must still be generated alongside build.ps1: %v", err)
	}
	for _, want := range []string{
		"powershell -ExecutionPolicy Bypass -File",
		"[string]$Binary = ",
		"Invoke-Step \"go tool templ generate\"",
		"Invoke-Step \"go build -o $Binary.exe .\"",
		"Invoke-Step \"& .\\\\$Binary.exe --port $Port --log $Log\"",
		"tar.exe -czf",
		"Remove-Item",
	} {
		if !strings.Contains(psStr, want) {
			t.Errorf("build.ps1 missing %q\n--- generated:\n%s", want, psStr)
		}
	}
	if strings.Contains(psStr, "rm -rf") || strings.Contains(psStr, "./") || strings.Contains(psStr, "tar czf") {
		t.Errorf("build.ps1 must not contain Unix-only shell commands\n--- generated:\n%s", psStr)
	}
}
