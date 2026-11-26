package serve

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
)

// previewCfg builds a config with one custom page (stat + chart widgets), one
// resource, and a navigation group carrying an HTML-ish label used by the
// escaping tests.
func previewCfg() *types.Config {
	cfg := testConfig()
	cfg.Pages = []types.Page{
		{
			Name: "Dashboard",
			Path: "/dashboard",
			Widgets: []types.Widget{
				{Type: "stat", Label: "Total revenue", Icon: "dollar", Prefix: "$"},
				{Type: "chart", Label: "Orders by month", Chart: &types.ChartConfig{Type: "bar"}},
			},
		},
	}
	cfg.Navigation = []types.NavigationGroup{
		{
			Group: "Main",
			Sort:  1,
			Items: []types.NavigationItem{
				{Type: "resource", Resource: "User"},
				{Type: "page", Page: "Dashboard"},
				{Type: "link", URL: "https://example.com", Label: "<script>alert(1)</script>", OpensInNewTab: true},
			},
		},
	}
	return cfg
}

func TestPreviewPage(t *testing.T) {
	s, _ := setupServer(t, previewCfg())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=page&page=Dashboard&theme=light", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<html lang=\"en\" class=\"preview\"",
		"Total revenue",
		"$",
		"text-2xl font-bold",
		"Orders by month",
		`data-chart-type="bar"`,
		`<canvas`,
		"/preview/styles.css",
		"/preview/chart.js",
		"--brand-primary: #6366f1;",
		"--brand-primary-rgb: 99 102 241;",
		"html.classList.remove('dark');",
		"data-width=\"256\"",
		"max-w-none",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page preview missing %q", want)
		}
	}
}

func TestPreviewResource(t *testing.T) {
	s, _ := setupServer(t, previewCfg())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=resource&resource=User&theme=auto", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"User",
		">id</th>",
		">email</th>",
		"Search",
		"View",
		"Edit",
		"Delete",
		"Showing page 1 of 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("resource preview missing %q", want)
		}
	}
}

func TestPreviewThemeForced(t *testing.T) {
	s, _ := setupServer(t, previewCfg())

	rec, _ := get(t, s.Handler(), "GET", "/preview?view=page&theme=dark", nil)
	if !strings.Contains(rec.Body.String(), "html.classList.add('dark');") {
		t.Errorf("dark force did not add the dark class: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "localStorage.getItem('yaga-theme')") {
		t.Errorf("forced dark still reads the saved preference")
	}

	rec, _ = get(t, s.Handler(), "GET", "/preview?view=page&theme=light", nil)
	if !strings.Contains(rec.Body.String(), "html.classList.remove('dark');") {
		t.Errorf("light force did not remove the dark class: %s", rec.Body.String())
	}
}

func TestPreviewThemeAutoFromConfig(t *testing.T) {
	cfg := previewCfg()
	cfg.Panel.Theme.DarkMode = true
	s, _ := setupServer(t, cfg)
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=page&theme=auto", nil)
	if !strings.Contains(rec.Body.String(), "else if (true) { html.classList.add('dark'); }") {
		t.Errorf("auto dark mode did not default to dark: %s", rec.Body.String())
	}
}

func TestPreviewEscapesNavLabel(t *testing.T) {
	s, _ := setupServer(t, previewCfg())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=page", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("nav label was not HTML-escaped")
	}
	if strings.Contains(body, "alert(1)</a>") || strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("raw unescaped nav label leaked into the document")
	}
}

// TestPreviewSidebarLinks verifies the preview sidebar navigates within the
// /preview endpoint instead of emitting real dashboard routes that the wedit
// server would 404 (the generated app's /admin/... handlers do not exist here).
func TestPreviewSidebarLinks(t *testing.T) {
	s, _ := setupServer(t, previewCfg())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=page", nil)
	body := rec.Body.String()
	for _, want := range []string{
		`href="/preview?view=resource&resource=User"`,
		`href="/preview?view=page&page=Dashboard"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("preview sidebar missing %q", want)
		}
	}
	if strings.Contains(body, `href="/admin/user"`) || strings.Contains(body, `href="/admin/dashboard"`) {
		t.Errorf("preview sidebar leaked a real dashboard route (would 404)")
	}
}

func TestPreviewNoTarget(t *testing.T) {
	s, _ := setupServer(t, testConfig())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=page", nil)
	if !strings.Contains(rec.Body.String(), "Select a page from the Preview tab toolbar.") {
		t.Errorf("missing no-target fallback: %s", rec.Body.String())
	}
	rec, _ = get(t, s.Handler(), "GET", "/preview?view=resource", nil)
	if !strings.Contains(rec.Body.String(), "Select a resource from the Preview tab toolbar.") {
		t.Errorf("missing resource no-target fallback: %s", rec.Body.String())
	}
}

func TestPreviewAssets(t *testing.T) {
	s, _ := setupServer(t, previewCfg())

	rec, _ := get(t, s.Handler(), "GET", "/preview/styles.css", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("styles status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("styles content-type = %q, want text/css", ct)
	}
	if !strings.Contains(rec.Body.String(), "--brand-primary") && len(rec.Body.String()) < 1000 {
		t.Errorf("styles.css looks empty/short (len=%d)", rec.Body.Len())
	}

	rec, _ = get(t, s.Handler(), "GET", "/preview/chart.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("chart status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("chart content-type = %q, want application/javascript", ct)
	}
	if len(rec.Body.String()) < 100000 {
		t.Errorf("chart.js unexpectedly small: %d bytes", rec.Body.Len())
	}
}

func TestPreviewReflectsConfigPut(t *testing.T) {
	s, _ := setupServer(t, testConfig())
	// Round-trip the served (YAML-keyed) config through the API, then add a
	// resource — this mirrors exactly what the SPA does before loading the
	// preview iframe.
	_, first := get(t, s.Handler(), "GET", "/api/config", nil)
	tree := first["config"].(map[string]interface{})
	resources := tree["resources"].([]interface{})
	resources = append(resources, map[string]interface{}{"name": "Order", "table": "orders"})
	tree["resources"] = resources
	payload, _ := json.Marshal(tree)

	rec, _ := get(t, s.Handler(), "PUT", "/api/config", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/config = %d: %s", rec.Code, rec.Body.String())
	}

	rec, _ = get(t, s.Handler(), "GET", "/preview?view=resource&resource=Order", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Order") {
		t.Errorf("preview did not reflect the PUT config")
	}
}

// previewCfgWithActions builds a config with a resource that has detail,
// card, form and actions configured.
func previewCfgWithActions() *types.Config {
	cfg := testConfig()
	cfg.Resources[0].Detail = &types.DetailConfig{
		Fields: []types.Field{
			{Name: "id", Type: "integer", Label: "ID"},
			{Name: "email", Type: "string", Label: "Email"},
			{Name: "active", Type: "boolean", Label: "Active"},
		},
	}
	cfg.Resources[0].Card = &types.CardConfig{
		Fields: []types.Field{
			{Name: "id", Type: "integer", Label: "ID"},
			{Name: "email", Type: "string", Label: "Email"},
		},
		Columns: 3,
		Rows:    2,
	}
	cfg.Resources[0].Form = &types.FormConfig{
		Create: &types.FormAction{
			Fields: []types.Field{
				{Name: "email", Type: "string", Label: "Email"},
				{Name: "active", Type: "boolean", Label: "Active"},
			},
		},
		Update: &types.FormAction{
			Fields: []types.Field{
				{Name: "status", Type: "string", Label: "Status"},
			},
		},
	}
	cfg.Resources[0].Actions = []types.Action{
		{Name: "archive", Label: "Archive", Query: "UPDATE users SET status = 'archived' WHERE id = $1", RequiresConfirmation: true},
		{Name: "count_active", Label: "Count active", Query: "SELECT COUNT(*) AS c FROM users WHERE active = true"},
		{Name: "lua_action", Label: "Lua action", Script: "db.exec('UPDATE users SET status = ?', {ctx.values.id})"},
	}
	return cfg
}

func TestPreviewDetail(t *testing.T) {
	s, _ := setupServer(t, previewCfgWithActions())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=detail&resource=User&id=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"User",
		"Email",
		"Active",
		"Edit",
		"Back to list",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail preview missing %q: %s", want, body)
		}
	}
}

func TestPreviewDetailNoID(t *testing.T) {
	s, _ := setupServer(t, previewCfgWithActions())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=detail&resource=User", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "User") {
		t.Errorf("detail preview missing resource title: %s", body)
	}
}

func TestPreviewCard(t *testing.T) {
	s, _ := setupServer(t, previewCfgWithActions())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=card&resource=User", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"User",
		"View",
		"Edit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("card preview missing %q: %s", want, body)
		}
	}
}

func TestPreviewFormCreate(t *testing.T) {
	s, _ := setupServer(t, previewCfgWithActions())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=form&resource=User&mode=create", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Create User",
		"Email",
		"Active",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form create preview missing %q: %s", want, body)
		}
	}
}

func TestPreviewFormUpdate(t *testing.T) {
	s, _ := setupServer(t, previewCfgWithActions())
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=form&resource=User&mode=update&id=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Edit User",
		"Email",
		"Active",
		"Status",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form update preview missing %q: %s", want, body)
		}
	}
}

// TestPreviewActionRunDryRunSQL tests dry-run validation of a mutating SQL action.
func TestPreviewActionRunDryRunSQL(t *testing.T) {
	s, dir := setupServer(t, previewCfgWithActions())
	// Create the users table in the test DB so the dry-run can prepare the statement.
	mustCreateTestTable(t, dir)
	body, _ := json.Marshal(map[string]interface{}{
		"resource":    "User",
		"action_name": "archive",
		"id":          1,
		"dry_run":     true,
	})
	rec, data := post(t, s.Handler(), "/api/preview/action-run", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if data["ok"] != true {
		t.Errorf("ok = %v, want true: %v", data["ok"], data)
	}
	if data["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", data["dryRun"])
	}
}

// TestPreviewActionRunDryRunInvalidSQL tests that dry-run catches invalid SQL.
func TestPreviewActionRunDryRunInvalidSQL(t *testing.T) {
	// Temporarily break the SQL by using a config with invalid SQL.
	cfg := previewCfgWithActions()
	for i := range cfg.Resources {
		for j := range cfg.Resources[i].Actions {
			if cfg.Resources[i].Actions[j].Name == "archive" {
				cfg.Resources[i].Actions[j].Query = "SELECT * FROM nonexistent_table WHERE"
			}
		}
	}
	s, dir := setupServer(t, cfg)
	mustCreateTestTable(t, dir)
	body, _ := json.Marshal(map[string]interface{}{
		"resource":    "User",
		"action_name": "archive",
		"id":          1,
		"dry_run":     true,
	})
	rec, data := post(t, s.Handler(), "/api/preview/action-run", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if data["ok"] != false {
		t.Errorf("ok = %v, want false for invalid SQL", data["ok"])
	}
	if data["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", data["dryRun"])
	}
}

// TestPreviewActionRunSelectQuery tests running a SELECT action.
func TestPreviewActionRunSelectQuery(t *testing.T) {
	s, dir := setupServer(t, previewCfgWithActions())
	mustCreateTestTable(t, dir)
	body, _ := json.Marshal(map[string]interface{}{
		"resource":    "User",
		"action_name": "count_active",
		"id":          0,
		"dry_run":     false,
	})
	rec, data := post(t, s.Handler(), "/api/preview/action-run", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if data["ok"] != true {
		t.Errorf("ok = %v, want true: %v", data["ok"], data)
	}
}

// TestPreviewActionRunLuaDryRun tests dry-run syntax check for a Lua action.
func TestPreviewActionRunLuaDryRun(t *testing.T) {
	s, dir := setupServer(t, previewCfgWithActions())
	mustCreateTestTable(t, dir)
	body, _ := json.Marshal(map[string]interface{}{
		"resource":    "User",
		"action_name": "lua_action",
		"id":          1,
		"dry_run":     true,
	})
	rec, data := post(t, s.Handler(), "/api/preview/action-run", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if data["ok"] != true {
		t.Errorf("ok = %v, want true: %v", data["ok"], data)
	}
	if data["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", data["dryRun"])
	}
}

// TestPreviewActionRunResourceNotFound tests a 404 for unknown resource.
func TestPreviewActionRunResourceNotFound(t *testing.T) {
	s, dir := setupServer(t, previewCfgWithActions())
	mustCreateTestTable(t, dir)
	body, _ := json.Marshal(map[string]interface{}{
		"resource":    "NoSuch",
		"action_name": "archive",
		"id":          1,
		"dry_run":     true,
	})
	rec, _ := post(t, s.Handler(), "/api/preview/action-run", body)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestPreviewActionRunActionNotFound tests a 404 for unknown action.
func TestPreviewActionRunActionNotFound(t *testing.T) {
	s, dir := setupServer(t, previewCfgWithActions())
	mustCreateTestTable(t, dir)
	body, _ := json.Marshal(map[string]interface{}{
		"resource":    "User",
		"action_name": "NoSuchAction",
		"id":          1,
		"dry_run":     true,
	})
	rec, _ := post(t, s.Handler(), "/api/preview/action-run", body)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestPreviewActionRunNoDB tests that action-run reports missing DB config.
func TestPreviewActionRunNoDB(t *testing.T) {
	// config without connections block but with an action
	cfg := &types.Config{
		Version: "1.0",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Resources: []types.Resource{
			{
				Name:    "User",
				Table:   "users",
				Actions: []types.Action{{Name: "archive", Label: "Archive", Query: "UPDATE users SET x = 1 WHERE id = $1"}},
			},
		},
	}
	s, _ := setupServer(t, cfg)
	body, _ := json.Marshal(map[string]interface{}{
		"resource":    "User",
		"action_name": "archive",
		"id":          1,
		"dry_run":     true,
	})
	rec, data := post(t, s.Handler(), "/api/preview/action-run", body)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if data["error"] == nil {
		t.Errorf("expected error message about missing DB")
	}
}

// mustCreateTestTable creates a users table in the test DB at dir/test.db.
func mustCreateTestTable(t *testing.T, dir string) {
	t.Helper()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, email TEXT, active INTEGER, status TEXT)"); err != nil {
		t.Fatalf("creating users table: %v", err)
	}
}

// TestPreviewSelectDriverSyntax guards the driver-specific row-limit syntax:
// MSSQL has no LIMIT clause, so emitting one silently broke every live preview
// query against a SQL Server database.
func TestPreviewSelectDriverSyntax(t *testing.T) {
	cases := []struct {
		driver string
		from   string
		n      int
		want   string
	}{
		{"mssql", "FROM " + previewIdent("mssql", "Zamestnanec"), 8, "SELECT TOP 8 * FROM [Zamestnanec]"},
		{"sqlserver", "FROM " + previewIdent("sqlserver", "x"), 1, "SELECT TOP 1 * FROM [x]"},
		{"sqlite", "FROM " + previewIdent("sqlite", "users"), 8, `SELECT * FROM "users" LIMIT 8`},
		{"postgres", "FROM " + previewIdent("postgres", "users"), 5, `SELECT * FROM "users" LIMIT 5`},
	}
	for _, c := range cases {
		if got := previewSelect(c.driver, "*", c.from, c.n); got != c.want {
			t.Errorf("previewSelect(%q) = %q, want %q", c.driver, got, c.want)
		}
	}
}

// TestPreviewIdentQuoting covers bracket vs double-quote quoting and escaping.
func TestPreviewIdentQuoting(t *testing.T) {
	if got, want := previewIdent("mssql", "a]b"), "[a]]b]"; got != want {
		t.Errorf("previewIdent(mssql) = %q, want %q", got, want)
	}
	if got, want := previewIdent("sqlite", `a"b`), `"a""b"`; got != want {
		t.Errorf("previewIdent(sqlite) = %q, want %q", got, want)
	}
}

// TestPreviewLiteral covers inline value rendering for preview WHERE clauses.
func TestPreviewLiteral(t *testing.T) {
	cases := map[string]string{
		"18":      "18",
		"-3":      "-3",
		"abc":     "'abc'",
		"O'Brien": "'O''Brien'",
		"":        "''",
	}
	for in, want := range cases {
		if got := previewLiteral(in); got != want {
			t.Errorf("previewLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPreviewResourceLiveData verifies that ?live=1 renders real rows from the
// live database (resolved relative to the config file) and never the sample
// rows used when live data is off.
func TestPreviewResourceLiveData(t *testing.T) {
	s, dir := setupServer(t, previewCfgWithActions())
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, active INTEGER, status TEXT)"); err != nil {
		t.Fatalf("creating users table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO users (id, email, active) VALUES (1, 'real.person@corp.cz', 1)"); err != nil {
		t.Fatalf("inserting row: %v", err)
	}
	db.Close()

	rec, _ := get(t, s.Handler(), "GET", "/preview?view=resource&resource=User&live=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "real.person@corp.cz") {
		t.Errorf("live preview does not contain the real row: %s", body)
	}
	for _, fake := range []string{"Alpha", "Bravo", "Charlie", "Delta"} {
		if strings.Contains(body, ">"+fake+"<") {
			t.Errorf("live preview fell back to fake data (%s)", fake)
		}
	}

	// Without ?live=1 the same view must use sample rows.
	rec, _ = get(t, s.Handler(), "GET", "/preview?view=resource&resource=User", nil)
	if strings.Contains(rec.Body.String(), "real.person@corp.cz") {
		t.Errorf("non-live preview should not query the database")
	}
}

// TestPreviewLiveQueryFailureIsVisible ensures a failing live query surfaces a
// hint instead of silently pretending the sample rows are real data.
func TestPreviewLiveQueryFailureIsVisible(t *testing.T) {
	cfg := previewCfgWithActions()
	cfg.Resources[0].Table = "missing_table"
	s, dir := setupServer(t, cfg)
	mustCreateTestTable(t, dir)

	rec, _ := get(t, s.Handler(), "GET", "/preview?view=resource&resource=User&live=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Live query failed") {
		t.Errorf("expected a visible live-query failure hint")
	}
}

// TestPreviewLiveLinksScope guards the "live data is per-resource" rule: live
// stays on when drilling into the same resource (detail/edit) but the sidebar
// (switching resource) drops it, so a new resource always starts with sample
// data until the user opts in again.
func TestPreviewLiveLinksScope(t *testing.T) {
	s, _ := setupServer(t, previewCfgWithActions())

	rec, _ := get(t, s.Handler(), "GET", "/preview?view=resource&resource=User&live=1", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "/preview?view=detail&resource=User&id=1&live=1") {
		t.Errorf("live list should link to a live detail view")
	}
	if !strings.Contains(body, `href="/preview?view=resource&resource=User"`) {
		t.Errorf("sidebar links must not carry live=1 (switching resource resets it)")
	}

	// Detail keeps live for edit/back-to-list.
	rec, _ = get(t, s.Handler(), "GET", "/preview?view=detail&resource=User&id=1&live=1", nil)
	body = rec.Body.String()
	if !strings.Contains(body, "/preview?view=form&resource=User&mode=update&id=1&live=1") {
		t.Errorf("live detail should link to a live edit form")
	}
	if !strings.Contains(body, "/preview?view=resource&resource=User&live=1") {
		t.Errorf("live detail should keep live on back-to-list")
	}

	// With live off no link may leak live=1.
	rec, _ = get(t, s.Handler(), "GET", "/preview?view=resource&resource=User", nil)
	if strings.Contains(rec.Body.String(), "live=1") {
		t.Errorf("non-live preview must not emit live=1 links")
	}
}

// TestPreviewEmbedDisablesClickToSwitch guards the built-in preview rule:
// only the toolbar dropdown may switch resource/page — clicking resources
// in the preview itself is disabled.
func TestPreviewEmbedDisablesClickToSwitch(t *testing.T) {
	s, _ := setupServer(t, previewCfgWithActions())

	// Embedded: sidebar items render as spans, not navigating links.
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=resource&resource=User&embed=1", nil)
	body := rec.Body.String()
	if strings.Contains(body, `href="/preview?view=resource&resource=User"`) {
		t.Errorf("embed preview must not link to other resources")
	}
	if !strings.Contains(body, `<span class="block px-4 py-2 text-sm text-gray-400 dark:text-gray-500 mx-2">User</span>`) {
		t.Errorf("embed preview should render resource items as spans")
	}

	// Standalone: sidebar items remain clickable links.
	rec, _ = get(t, s.Handler(), "GET", "/preview?view=resource&resource=User", nil)
	body = rec.Body.String()
	if !strings.Contains(body, `href="/preview?view=resource&resource=User"`) {
		t.Errorf("standalone preview should keep clickable resource links")
	}
}

// TestPreviewNoLiveDataMessage guards that when live=1 is requested but the
// table is empty, the preview shows "No live data for this resource!" instead
// of silently falling back to sample rows.
func TestPreviewNoLiveDataMessage(t *testing.T) {
	s, dir := setupServer(t, previewCfgWithActions())
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	// Empty users table — no rows at all.
	if _, err := db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, active INTEGER, status TEXT)"); err != nil {
		t.Fatalf("creating users table: %v", err)
	}
	db.Close()

	msg := "No live data for this resource!"

	// List view.
	rec, _ := get(t, s.Handler(), "GET", "/preview?view=resource&resource=User&live=1", nil)
	if !strings.Contains(rec.Body.String(), msg) {
		t.Errorf("list live preview should show %q on empty table", msg)
	}

	// Detail view.
	rec, _ = get(t, s.Handler(), "GET", "/preview?view=detail&resource=User&live=1", nil)
	if !strings.Contains(rec.Body.String(), msg) {
		t.Errorf("detail live preview should show %q on empty table", msg)
	}

	// Card view.
	rec, _ = get(t, s.Handler(), "GET", "/preview?view=card&resource=User&live=1", nil)
	if !strings.Contains(rec.Body.String(), msg) {
		t.Errorf("card live preview should show %q on empty table", msg)
	}

	// Non-empty table must NOT show the message.
	db, _ = sql.Open("sqlite", dbPath)
	if _, err := db.Exec("INSERT INTO users (id, email, active) VALUES (1, 'real.person@corp.cz', 1)"); err != nil {
		t.Fatalf("inserting row: %v", err)
	}
	db.Close()

	rec, _ = get(t, s.Handler(), "GET", "/preview?view=resource&resource=User&live=1", nil)
	if strings.Contains(rec.Body.String(), msg) {
		t.Errorf("non-empty table should not show %q", msg)
	}
	if !strings.Contains(rec.Body.String(), "real.person@corp.cz") {
		t.Errorf("non-empty table live preview should show real rows")
	}
}

// TestPreviewCardNilConfig guards against a nil-pointer panic when a resource
// has no card: section configured.
func TestPreviewCardNilConfig(t *testing.T) {
	cfg := testConfig()
	// Resource has no Card section
	s, _ := setupServer(t, cfg)

	rec, _ := get(t, s.Handler(), "GET", "/preview?view=card&resource=User&embed=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Card view not configured") {
		t.Errorf("card view without config should show 'Card view not configured'")
	}
}

// TestPreviewCardNilConfigLive guards that live=1 with no card config shows
// only the "not configured" banner, not a misleading "No live data" message.
func TestPreviewCardNilConfigLive(t *testing.T) {
	cfg := testConfig()
	s, dir := setupServer(t, cfg)
	mustCreateTestTable(t, dir)

	rec, _ := get(t, s.Handler(), "GET", "/preview?view=card&resource=User&live=1&embed=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Card view not configured") {
		t.Errorf("card view without config should show 'Card view not configured'")
	}
	if strings.Contains(body, "No live data for this resource") {
		t.Errorf("card view without config should not show 'No live data' banner")
	}
}
