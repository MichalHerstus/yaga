package schema

import (
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
)

func TestIsInlineSQL(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"ListCustomers", false},
		{"CountUsers", false},
		{"GetUserByEmail", false},
		{"SELECT COUNT(*) FROM users", true},
		{"UPDATE orders SET status = 'completed' WHERE id = $1", true},
		{"DELETE FROM t WHERE id = ?", true},
		{"", false},
	} {
		if got := isInlineSQL(tc.in); got != tc.want {
			t.Errorf("isInlineSQL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestCollectReferencesInlineTagging verifies that inline SQL (action/widget
// queries) is tagged Inline while real SQLC query names are not.
func TestCollectReferencesInlineTagging(t *testing.T) {
	cfg := &types.Config{
		Resources: []types.Resource{{
			Name: "User",
			List: &types.ListConfig{Query: "ListUsers"},
			Actions: []types.Action{
				{Name: "archive", Query: "UPDATE users SET archived = 1 WHERE id = ?"},
			},
		}},
	}
	refs := CollectReferences(cfg)
	var gotName, gotInline bool
	for _, q := range refs.Queries {
		if q.Name == "ListUsers" && !q.Inline {
			gotName = true
		}
		if q.Name == "UPDATE users SET archived = 1 WHERE id = ?" && q.Inline {
			gotInline = true
		}
	}
	if !gotName {
		t.Error("ListUsers should be a non-inline reference")
	}
	if !gotInline {
		t.Error("action SQL should be tagged Inline")
	}
}

// TestCollectReferences verifies the full reference sweep (queries, tables,
// columns and per-section column locations).
func TestCollectReferences(t *testing.T) {
	cfg := &types.Config{
		Resources: []types.Resource{
			{
				Name: "User",
				List: &types.ListConfig{Query: "ListUsers", CountQuery: "CountUsers",
					Columns: []types.Column{{Name: "id"}, {Name: "email"}}},
				Detail: &types.DetailConfig{Query: "GetUser",
					Fields: []types.Field{{Name: "name"}, {Name: "role_id", OptionsQuery: "ListRoles"}}},
				Card: &types.CardConfig{
					Fields:      []types.Field{{Name: "email"}},
					KanbanField: "status",
					Searchable:  []string{"email"},
					DefaultSort: "-created_at",
				},
				Form: &types.FormConfig{
					Create: &types.FormAction{Query: "CreateUser"},
					Update: &types.FormAction{Query: "UpdateUser", PopulateQuery: "GetUser"},
				},
				Actions: []types.Action{{Name: "archive", Query: "ArchiveUser"}},
			},
		},
		Pages: []types.Page{{
			Name: "Dashboard",
			Widgets: []types.Widget{
				{Type: "stat", Label: "x", Query: "CountUsers"},
				{Type: "chart", Label: "y", Query: "ChartData", Chart: &types.ChartConfig{Type: "line"}},
				{Type: "stats_grid", Widgets: []types.Widget{{Type: "stat", Label: "z", Query: "CountUsers"}}},
			},
		}},
	}
	refs := CollectReferences(cfg)
	wantQueries := []string{"ListUsers", "CountUsers", "GetUser", "ListRoles",
		"CreateUser", "UpdateUser", "ArchiveUser", "ChartData"}
	got := map[string]bool{}
	for _, q := range refs.Queries {
		got[q.Name] = true
	}
	for _, w := range wantQueries {
		if !got[w] {
			t.Errorf("missing query ref %s", w)
		}
	}
	if len(got) != len(wantQueries) {
		t.Errorf("unexpected extra refs: %v", got)
	}
	if refs.Tables["User"] != "users" {
		t.Errorf("table for User: %q", refs.Tables["User"])
	}
	// Columns is the deduplicated summary: id, email (list+card), status,
	// created_at, name, role_id = 6 unique names.
	if len(refs.Columns["User"]) != 6 {
		t.Errorf("columns for User: %v", refs.Columns["User"])
	}
	// ColumnRefs pins each reference to its section + index.
	wantRefs := []ColumnRef{
		{"id", "list.columns", 0},
		{"email", "list.columns", 1},
		{"email", "card.fields", 0},
		{"email", "card.searchable", 0},
		{"status", "card.kanban_field", 0},
		{"created_at", "card.default_sort", 0},
		{"name", "detail.fields", 0},
		{"role_id", "detail.fields", 1},
	}
	gotRefs := refs.ColumnRefs["User"]
	if len(gotRefs) != len(wantRefs) {
		t.Fatalf("ColumnRefs for User: got %d, want %d: %+v", len(gotRefs), len(wantRefs), gotRefs)
	}
	for i, w := range wantRefs {
		if gotRefs[i] != w {
			t.Errorf("ColumnRefs[%d] = %+v, want %+v", i, gotRefs[i], w)
		}
	}
}

func TestTableNameFor(t *testing.T) {
	if TableNameFor(types.Resource{Name: "User"}) != "users" {
		t.Error("pluralize failed")
	}
	if TableNameFor(types.Resource{Name: "User", Table: "accounts"}) != "accounts" {
		t.Error("explicit table failed")
	}
}

// TestHasColumn verifies that real columns, case-insensitive matches and
// FK-label virtual columns (pn_label) are all recognised, while unknown
// columns and label columns without a matching FK are rejected.
func TestHasColumn(t *testing.T) {
	st := &types.SchemaTable{
		Name: "sklad_zasoby",
		PK:   "id",
		Columns: []types.SchemaColumn{
			{Name: "id", Type: "integer"},
			{Name: "pn", Type: "string"},
			{Name: "mnozstvi", Type: "float"},
		},
		ForeignKeys: []types.SchemaFK{{
			Column:        "pn",
			ForeignTable:  "sklad_zbozi",
			ForeignColumn: "pn",
			Label:         "pn",
		}},
	}
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"id", true},                             // real column exact
		{"ID", true},                             // real column case-insensitive
		{"pn", true},                             // real column = FK base
		{"pn_label", true},                       // FK-label virtual column
		{"nonexistent", false},                   // unknown column
		{"pn_label", true},                       // _label with matching FK
		{"mnozstvi", true},                       // real column
		{"mnozstvi_label", false},                // _label without matching FK
		{"fk_label", false},                      // _label, no FK on "fk"
		{"name", false},                          // real column absent, no FK
		{"name_label", false},                    // _label, no FK on "name"
	} {
		got := HasColumn(st, tc.name)
		if got != tc.want {
			t.Errorf("HasColumn(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
