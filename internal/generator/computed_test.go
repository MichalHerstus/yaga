// computed_test.go
//
// E7: virtual computed fields. These tests drive the generator with
// `computed:` blocks on list/card/detail and assert the emitted handlers and
// templ render them: synthetic SELECT aliases, scan targets and ColumnDefs,
// the helpers.* expansion, the derived-table wrapper when a filter references
// a computed column, and the detail compute<Resource>Row helper.
package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
)

// computedConfig returns a postgres resource exercising computed fields on all
// three views plus a list filter referencing a computed column (forces the E7
// derived-table wrapper on the list query).
func computedConfig() *types.Config {
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
					Columns: []types.Column{
						{Name: "id", Label: "ID"},
						{Name: "status", Label: "Status"},
						{Name: "total", Label: "Total"},
					},
					Computed: []types.ComputedField{
						{Name: "total_gross", Label: "Total gross", Type: "float", Expression: "helpers.round(total * 1.21, 2)"},
						{Name: "age_days", Label: "Age days", Type: "integer", Expression: "helpers.date_diff(helpers.now(), created_at)"},
					},
					Filter: &types.FilterConfig{
						Label: "Gross filter",
						Where: "total_gross > $1",
						Params: []types.FilterParam{
							{Name: "min_gross", Label: "Min gross"},
						},
					},
				},
				Card: &types.CardConfig{
					Fields:  []types.Field{{Name: "id", Type: "text"}, {Name: "total", Type: "float"}},
					Columns: 3, Rows: 2,
					Computed: []types.ComputedField{
						{Name: "total_gross", Label: "Total gross", Type: "float", Expression: "helpers.round(total * 1.21, 2)"},
					},
					Filter: &types.FilterConfig{
						Label: "Card gross filter",
						Where: "total_gross > $1",
						Params: []types.FilterParam{
							{Name: "min_gross", Label: "Min gross"},
						},
					},
				},
				Detail: &types.DetailConfig{
					Fields: []types.Field{{Name: "id", Label: "ID"}, {Name: "total", Label: "Total"}},
					Computed: []types.ComputedField{
						{Name: "total_gross", Label: "Total gross", Type: "float", Expression: "helpers.round(total * 1.21, 2)"},
					},
				},
			},
		},
	}
}

// TestGenerateComputedList ensures the list handler concatenates the computed
// SELECT items, scans them into the item map and emits their ColumnDefs, and
// that the list templ renders a header and cell for each computed column.
func TestGenerateComputedList(t *testing.T) {
	dir := t.TempDir()
	g := New(computedConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	listCode := readResourceFile(t, dir, "order", "list.go")
	for _, want := range []string{
		`ROUND(total * 1.21::numeric, 2) AS \"total_gross\"`,
		`EXTRACT(DAY FROM (NOW())::timestamp - (created_at)::timestamp) AS \"age_days\"`,
		`var val_total_gross interface{}`,
		`var val_age_days interface{}`,
		`item["total_gross"] = val_total_gross`,
		`item["age_days"] = val_age_days`,
		`{Name: "total_gross", Label: "Total gross", FieldType: "float"}`,
		`{Name: "age_days", Label: "Age days", FieldType: "integer"}`,
		`(SELECT `,
		`) _base`,
		`_total`,
	} {
		if !strings.Contains(listCode, want) {
			t.Errorf("list.go missing %q", want)
		}
	}

	listTempl, err := os.ReadFile(filepath.Join(dir, "internal/views/resources/order", "list.templ"))
	if err != nil {
		t.Fatalf("read list.templ: %v", err)
	}
	listTemplStr := string(listTempl)
	for _, want := range []string{
		`Total gross`,
		`item["total_gross"]`,
		`item["age_days"]`,
	} {
		if !strings.Contains(listTemplStr, want) {
			t.Errorf("list.templ missing %q", want)
		}
	}
}

// TestGenerateComputedFiltered asserts the E7 derived-table wrapper: when the
// list filter references a computed column, the emitted query nests the real +
// computed SELECT inside `FROM (SELECT ...) _base` and selects only the quoted
// plain identifiers from the OUTER query (read from the derived table), so the
// filter can reference the computed alias.
func TestGenerateComputedFiltered(t *testing.T) {
	dir := t.TempDir()
	g := New(computedConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	listCode := readResourceFile(t, dir, "order", "list.go")
	for _, want := range []string{
		`FROM (SELECT`,
		`) _base`,
		`SELECT \"id\", \"status\", \"total\", \"total_gross\", \"age_days\", COUNT(*) OVER() AS _total FROM (SELECT`,
		`\"total_gross\" >`,
	} {
		if !strings.Contains(listCode, want) {
			t.Errorf("list.go missing %q", want)
		}
	}
}

// TestGenerateComputedDetail asserts the detail handler emits the
// compute<Resource>Row helper, calls it after the GetByID fetch, imports
// context, and appends the computed fields to the DetailData defs.
func TestGenerateComputedDetail(t *testing.T) {
	dir := t.TempDir()
	g := New(computedConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	detailCode := readResourceFile(t, dir, "order", "detail.go")
	for _, want := range []string{
		`"context"`,
		`func computeOrderRow(db *sql.DB, ctx context.Context, item map[string]interface{}, id int32) error`,
		`ROUND(total * 1.21::numeric, 2) AS \"total_gross\"`,
		`if err := computeOrderRow(db, r.Context(), item, int32(id)); err != nil {`,
		`item["total_gross"] = val_total_gross`,
		`{Name: "total_gross", Label: "Total gross", FieldType: "float"}`,
	} {
		if !strings.Contains(detailCode, want) {
			t.Errorf("detail.go missing %q", want)
		}
	}
}

// TestGenerateComputedMSSQL ensures the mssql driver still works: computed
// items use mssql identifier quoting (brackets) and helper expansions.
func TestGenerateComputedMSSQL(t *testing.T) {
	dir := t.TempDir()
	g := New(computedConfig(), dir)
	g.Config.Connections["default"] = types.Connection{Driver: "mssql", DSN: "sqlserver://x"}
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	listCode := readResourceFile(t, dir, "order", "list.go")
	for _, want := range []string{
		`ROUND(total * 1.21, 2) AS [total_gross]`,
		`ORDER BY (SELECT NULL)`,
	} {
		if !strings.Contains(listCode, want) {
			t.Errorf("list.go missing %q", want)
		}
	}
}

// TestGenerateComputedFeatureOff guards the byte-identical regression: a
// config without any computed block must not reference computed plumbing.
func TestGenerateComputedFeatureOff(t *testing.T) {
	dir := t.TempDir()
	g := New(readOnlyConfig(), dir)
	if err := g.Generate(); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertGeneratedGoParses(t, dir)

	for _, file := range []string{"list.go", "card.go", "detail.go"} {
		code := readResourceFile(t, dir, "order", file)
		if strings.Contains(code, "compute") {
			t.Errorf("%s must not emit computeRow plumbing", file)
		}
	}
	listCode := readResourceFile(t, dir, "order", "list.go")
	if strings.Contains(listCode, "_base") {
		t.Errorf("feature-off list.go must not emit a derived table")
	}
}