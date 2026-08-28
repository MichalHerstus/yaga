package editor

import (
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
	"github.com/rivo/tview"
)

// testConfig builds a config with one resource, one page and navigation.
func testConfig() *types.Config {
	return &types.Config{
		Version: "1",
		Panel: types.Panel{
			Name: "Admin",
			Path: "/admin",
			Brand: types.Brand{Colors: types.BrandColors{
				Primary: "#6366f1", Secondary: "#8b5cf6",
			}},
			Layout: types.Layout{Sidebar: types.SidebarLayout{
				Collapsible: true, Width: 256, CollapsedWidth: 64,
			}},
		},
		Connections: map[string]types.Connection{
			"default": {Driver: "sqlite", DSN: "file:demo.db"},
		},
		SQLC: types.SQLCConfig{
			Config:     "sqlc.yaml",
			QueriesDir: "./sql/queries",
			SchemaDir:  "./sql/migrations",
			OutputPkg:  "internal/data",
		},
		Auth: types.AuthConfig{Table: "users", Login: types.LoginConfig{
			Fields: []string{"email", "password"}, Redirect: "/admin/dashboard",
		}},
		Navigation: []types.NavigationGroup{{
			Group: "Sales",
			Items: []types.NavigationItem{{Resource: "User", Type: "resource"}},
		}},
		Resources: []types.Resource{{
			Name:  "User",
			Label: "Users",
			List: &types.ListConfig{
				Query:      "ListUsers",
				CountQuery: "CountUsers",
				Columns:    []types.Column{{Name: "id", Label: "ID", Type: "integer", Sortable: true}},
				Computed:   []types.ComputedField{{Name: "total_gross", Type: "float", Expression: "helpers.round(total * 1.21, 2)"}},
			},
			Detail: &types.DetailConfig{
				Query:  "GetUser",
				Params: map[string]string{"id": "{record.id}"},
				Fields: []types.Field{{Name: "email", Type: "email"}},
			},
			Form: &types.FormConfig{
				Create: &types.FormAction{Query: "CreateUser", Fields: []types.Field{{Name: "email", Type: "email"}}},
			},
			Actions: []types.Action{{Name: "archive", Label: "Archive", Query: "UPDATE users SET archived = 1 WHERE id = ?"}},
		}},
		Pages: []types.Page{{
			Name:    "Dashboard",
			Path:    "/dashboard",
			Default: true,
			Widgets: []types.Widget{
				{Type: "stat", Label: "Users", Query: "SELECT COUNT(*) FROM users"},
				{Type: "chart", Label: "Revenue", Chart: &types.ChartConfig{Type: "line"}},
				{Type: "stats_grid", Label: "Grid", Columns: 2,
					Widgets: []types.Widget{{Type: "stat", Label: "A"}}},
			},
		}},
	}
}

// TestPageBuilders builds every page without running the app, ensuring no
// nil-pointer panics and non-nil primitives.
func TestPageBuilders(t *testing.T) {
	e := New(testConfig(), "testdata/yaga.yaml")
	builders := map[string]func() tview.Primitive{
		"home":        e.homePage,
		"panel":       e.panelPage,
		"brand":       e.brandPage,
		"layout":      e.layoutPage,
		"theme":       e.themePage,
		"connections": e.connectionsPage,
		"auth":        e.authPage,
		"audit":       e.auditPage,
		"procedures":  e.proceduresPage,
		"plugins":     e.pluginsPage,
		"navigation":  e.navGroupsPage,
		"pages":       e.pagesPage,
		"resources":   e.resourcesPage,
		"validate":    e.validatePage,
		"preview":     e.previewPage,
	}
	for name, build := range builders {
		if p := build(); p == nil {
			t.Errorf("%s: nil primitive", name)
		}
	}
}

// TestResourcePages exercises the nested resource editors.
func TestResourcePages(t *testing.T) {
	e := New(testConfig(), "testdata/yaga.yaml")
	for _, fn := range []func() tview.Primitive{
		func() tview.Primitive { return e.resourcePage(0) },
		func() tview.Primitive { return e.listPage(0) },
		func() tview.Primitive { return e.columnsPage(0) },
		func() tview.Primitive { return e.cardPage(0) },
		func() tview.Primitive { return e.detailPage(0) },
		func() tview.Primitive { return e.formPage(0) },
		func() tview.Primitive { return e.formActionPage(0, "create") },
		func() tview.Primitive { return e.formFieldsPage(0, "create") },
		func() tview.Primitive { return e.actionsPage(0) },
		func() tview.Primitive { return e.policiesPage(0) },
	} {
		if fn() == nil {
			t.Error("nil primitive")
		}
	}
}

// TestProcPages exercises the hook/action editors with proc-configured items,
// ensuring the three-way kind picker and the Proc field render without panic.
func TestProcPages(t *testing.T) {
	cfg := testConfig()
	cfg.Resources[0].Form.Create.Hooks = &types.Hooks{
		After: []types.Hook{{Name: "archive_created", Proc: "sp_archive_user"}},
	}
	cfg.Resources[0].Actions[0].Proc = "sp_archive_user"
	cfg.Resources[0].Actions[0].Query = ""
	e := New(cfg, "testdata/yaga.yaml")

	hs := cfg.Resources[0].Form.Create.Hooks
	get := func() *[]types.Hook { return &hs.After }
	if p := e.hookListPage("Resources/User/Form/Create/Hooks/After", &cfg.Resources[0].Form.Create.Hooks, false); p == nil {
		t.Error("hookListPage: nil primitive")
	}
	if p := e.hookPage(get, 0); p == nil {
		t.Error("hookPage: nil primitive")
	}
	if p := e.actionsPage(0); p == nil {
		t.Error("actionsPage: nil primitive")
	}
	if p := e.actionPage(0, 0); p == nil {
		t.Error("actionPage: nil primitive")
	}
}
