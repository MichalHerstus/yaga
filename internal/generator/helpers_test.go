package generator

import (
	"strings"
	"testing"

	"github.com/MichalHerstus/yaga/internal/types"
)

// helperGen builds a Generator with the given driver configured so the helper
// expansion tests can exercise every driver. expandHelpers never writes to
// OutDir, so an empty out dir is fine.
func helperGen(driver string) *Generator {
	return New(&types.Config{
		Version: "1",
		Panel:   types.Panel{ID: "admin", Path: "/admin", Name: "Admin"},
		Connections: map[string]types.Connection{
			"main": {Driver: driver, DSN: "ignored"},
		},
	}, "")
}

type expandCase struct {
	driver string
	in     string
	want   string
}

func TestExpandHelpers(t *testing.T) {
	cases := []expandCase{
		// plain identifier pass-through
		{"postgres", "created_at > now()", "created_at > now()"}, // not a helpers.* call
		// date_diff
		{"postgres", "helpers.date_diff(now(), created_at)",
			"EXTRACT(DAY FROM (now())::timestamp - (created_at)::timestamp)"},
		{"sqlite", "helpers.date_diff(now(), created_at)",
			"(julianday(now()) - julianday(created_at))"},
		{"mssql", "helpers.date_diff(now(), created_at)",
			"DATEDIFF(DAY, created_at, now())"},
		// year_diff / month_diff
		{"postgres", "helpers.year_diff(now(), birth_date)",
			"EXTRACT(YEAR FROM age((now())::timestamp, (birth_date)::timestamp))"},
		{"sqlite", "helpers.year_diff(now(), birth_date)",
			"CAST((julianday(now()) - julianday(birth_date)) / 365.25 AS INTEGER)"},
		{"mssql", "helpers.month_diff(now(), created_at)",
			"DATEDIFF(MONTH, created_at, now())"},
		// coalesce / ifnull / round / now
		{"postgres", "helpers.coalesce(display_name, email)", "COALESCE(display_name, email)"},
		{"sqlite", "helpers.ifnull(a, 0)", "IFNULL(a, 0)"},
		{"mssql", "helpers.ifnull(a, 0)", "ISNULL(a, 0)"},
		{"postgres", "helpers.ifnull(a, 0)", "COALESCE(a, 0)"},
		{"postgres", "helpers.round(total, 2)", "ROUND(total::numeric, 2)"},
		{"sqlite", "helpers.round(total, 2)", "ROUND(total, 2)"},
		{"sqlite", "helpers.now()", "datetime('now')"},
		{"mssql", "helpers.now()", "GETDATE()"},
		{"postgres", "helpers.now()", "NOW()"},
		// nested: inner helper expanded first
		{"postgres", "helpers.coalesce(helpers.year_diff(now(), created_at), 0)",
			"COALESCE(EXTRACT(YEAR FROM age((now())::timestamp, (created_at)::timestamp)), 0)"},
		// nested inside an arg with commas in the inner call
		{"sqlite", "helpers.round(helpers.date_diff(warranty_until, sold_at), 1)",
			"ROUND((julianday(warranty_until) - julianday(sold_at)), 1)"},
		// unknown helper left verbatim
		{"postgres", "helpers.frobnicate(a, b)", "helpers.frobnicate(a, b)"},
		// wrong arity left verbatim
		{"postgres", "helpers.date_diff(a)", "helpers.date_diff(a)"},
		// arg splitting reserves quoted commas
		{"postgres", `helpers.coalesce(first_name, 'A, B')`, `COALESCE(first_name, 'A, B')`},
	}
	for _, c := range cases {
		g := helperGen(c.driver)
		got := g.expandHelpers(c.in)
		if got != c.want {
			t.Errorf("expandHelpers(%q driver=%q) = %q, want %q", c.in, c.driver, got, c.want)
		}
	}
}

// TestExpandHelpersIntactFeature promotes the "computed fields are opt-in"
// guarantee at the helper-expansion level: expressions that contain no
// helpers.* token are byte-for-byte unchanged.
func TestExpandHelpersIntactFeature(t *testing.T) {
	expr := "sold_at + 30 || ' days'"
	for _, driver := range []string{"postgres", "sqlite", "mssql"} {
		g := helperGen(driver)
		if got := g.expandHelpers(expr); got != expr {
			t.Errorf("driver=%q: expression without helpers mutated: %q", driver, got)
		}
	}
}

// TestSplitHelperArgs exercises the comma splitter against nested parens and
// quoted commas.
func TestSplitHelperArgs(t *testing.T) {
	got := splitHelperArgs("a, b , c")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("simple split = %#v", got)
	}
	got = splitHelperArgs("")
	if got != nil {
		t.Fatalf("empty args = %#v, want nil", got)
	}
	got = splitHelperArgs("EXTRACT(DAY FROM x), 2")
	if len(got) != 2 || got[0] != "EXTRACT(DAY FROM x)" || got[1] != "2" {
		t.Fatalf("paren split = %#v", got)
	}
	got = splitHelperArgs("'a,b', c")
	if len(got) != 2 || got[0] != "'a,b'" || got[1] != "c" {
		t.Fatalf("quoted split = %#v", got)
	}
}

// TestFindHelperCalls verifies balanced-span discovery over nested calls.
func TestFindHelperCalls(t *testing.T) {
	in := "helpers.coalesce(helpers.date_diff(now(), a), helpers.now())"
	spans := findHelperCalls(in)
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3 (%#v)", len(spans), spans)
	}
	want := map[string]bool{"coalesce": true, "date_diff": true, "now": true}
	for _, s := range spans {
		if !want[s.fn] {
			t.Errorf("unexpected span fn %q", s.fn)
		}
		delete(want, s.fn)
	}
	if len(want) != 0 {
		t.Errorf("missing spans for %#v", want)
	}
}

func TestExpandHelpersBlankToken(t *testing.T) {
	g := helperGen("postgres")
	expr := strings.Repeat("helpers.coalesce(a, ", 40) + "b" + strings.Repeat(")", 40)
	_ = g.expandHelpers(expr) // must terminate despite deep nesting
}