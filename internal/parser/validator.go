// validator.go
//
// Validates a parsed configuration and fills in defaults for missing optional
// fields (panel id, sqlc paths, resource labels, page paths). The generator
// relies on these defaults being applied before generation.
package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	luasrc "github.com/MichalHerstus/yaga/internal/generator/luasrc"
	"github.com/MichalHerstus/yaga/internal/types"
)

// Warning is a non-fatal validation notice, e.g. a value silently clamped to a
// supported range (grid columns [1,12]) or a max_content_width fallback. It
// renders as a yellow row on the editor's Validate screen and never blocks a
// config save or generation (Validate skips warnings).
type Warning struct{ msg string }

// Error implements the error interface.
func (w Warning) Error() string { return w.msg }

// warn builds a Warning.
func warn(format string, a ...interface{}) error {
	return Warning{msg: fmt.Sprintf(format, a...)}
}

// maxWidths is the allowlist of max_content_width values that map onto a real
// Tailwind max-w-{V} class. Anything else (including empty) falls back to
// "none" (max-w-none) with a warning — the pre-built stylesheet only safelists
// these values.
var maxWidths = []string{
	"none", "xs", "sm", "md", "lg", "xl", "2xl", "3xl", "4xl", "5xl", "6xl", "7xl",
	"full", "min", "max", "fit", "prose",
	"screen-sm", "screen-md", "screen-lg", "screen-xl", "screen-2xl",
}

func inMaxWidths(v string) bool {
	for _, m := range maxWidths {
		if m == v {
			return true
		}
	}
	return false
}

// paramRefRE matches runtime $N tokens in a filter where expression. The N is
// used to check that every referenced param position has a matching declaration.
var paramRefRE = regexp.MustCompile(`\$(\d+)`)

// Validate checks a parsed config for required fields and applies defaults. It
// returns the first validation problem (or nil), ignoring non-fatal warnings;
// callers that need every problem at once (e.g. the editor's Validate screen)
// use ValidateAll.
// Params: cfg (the config to validate; may be mutated to set defaults).
// Returns: an error describing the first validation problem, or nil.
func Validate(cfg *types.Config) error {
	errs := ValidateAll(cfg)
	for _, e := range errs {
		if _, ok := e.(Warning); !ok {
			return e
		}
	}
	return nil
}

// ValidateAll checks a parsed config for required fields and applies defaults
// (see the Validate doc comment), collecting every problem found instead of
// stopping at the first. Defaulting still runs even when errors are present so
// a follow-up pass sees the same normalized config.
// Params: cfg (the config to validate; may be mutated to set defaults).
// Returns: the validation problems found (empty when the config is valid).
func ValidateAll(cfg *types.Config) []error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if cfg.Version == "" {
		add(fmt.Errorf("version is required"))
	}
	if cfg.Panel.Name == "" {
		add(fmt.Errorf("panel.name is required"))
	}
	if cfg.Panel.Path == "" {
		add(fmt.Errorf("panel.path is required"))
	}
	if cfg.Panel.ID == "" {
		cfg.Panel.ID = "admin"
	}
	if cfg.Panel.Layout.MaxContentWidth != "" && !inMaxWidths(cfg.Panel.Layout.MaxContentWidth) {
		add(warn("panel.layout.max_content_width %q is not a supported width, falling back to \"none\"", cfg.Panel.Layout.MaxContentWidth))
		cfg.Panel.Layout.MaxContentWidth = "none"
	}
	if cfg.SQLC.Config == "" {
		cfg.SQLC.Config = "sqlc.yaml"
	}
	if cfg.SQLC.QueriesDir == "" {
		cfg.SQLC.QueriesDir = "./sql/queries"
	}
	if cfg.SQLC.SchemaDir == "" {
		cfg.SQLC.SchemaDir = "./sql/migrations"
	}
	if cfg.SQLC.OutputPkg == "" {
		cfg.SQLC.OutputPkg = "internal/data"
	}
	if len(cfg.Resources) == 0 && len(cfg.Pages) == 0 {
		add(fmt.Errorf("at least one resource or page is required"))
	}
	for i, pl := range cfg.Plugins {
		if pl.Name == "" {
			add(fmt.Errorf("plugins[%d].name is required", i))
		}
		if pl.Source == "" {
			add(fmt.Errorf("plugins[%d].source is required", i))
		}
		for j := 0; j < i; j++ {
			if cfg.Plugins[j].Name == pl.Name {
				add(fmt.Errorf("plugins[%d].name %q is duplicated", i, pl.Name))
			}
		}
	}
	if cfg.Audit != nil {
		if cfg.Audit.Table == "" {
			cfg.Audit.Table = "audit_log"
		}
		for _, ex := range cfg.Audit.ExcludeResources {
			found := false
			for _, r := range cfg.Resources {
				if r.Name == ex {
					found = true
					break
				}
			}
			if !found {
				add(fmt.Errorf("audit.exclude_resources references unknown resource %q", ex))
			}
		}
	}
	for i, r := range cfg.Resources {
		if r.Name == "" {
			add(fmt.Errorf("resources[%d].name is required", i))
		}
		if r.Label == "" {
			cfg.Resources[i].Label = r.Name
		}
		if r.Card != nil {
			if r.Card.Columns < 1 || r.Card.Columns > 12 {
				add(warn("resources[%d] (%s) card.columns %d is out of range [1,12], clamping to %d", i, r.Name, r.Card.Columns, clampColumns(r.Card.Columns)))
				r.Card.Columns = clampColumns(r.Card.Columns)
			}
			if r.Card.Rows < 1 {
				r.Card.Rows = 4
			}
		}
		if r.List != nil && r.List.PerPage < 1 {
			r.List.PerPage = 20
		}
		if r.List != nil {
			for _, ex := range r.List.Export {
				found := false
				for _, c := range r.List.Columns {
					if c.Name == ex {
						found = true
						break
					}
				}
				if !found {
					add(fmt.Errorf("resources[%d] (%s) list.export references unknown column %q", i, r.Name, ex))
				}
			}
		}
		if r.ImportCSV && (r.Form == nil || r.Form.Create == nil) {
			add(fmt.Errorf("resources[%d] (%s) import_csv requires a form.create section", i, r.Name))
		}
		if r.List != nil {
			for _, fe := range validateFilter(r.List.Filter) {
				add(fmt.Errorf("resources[%d] (%s) list.filter: %w", i, r.Name, fe))
			}
		}
		if r.Card != nil {
			for _, fe := range validateFilter(r.Card.Filter) {
				add(fmt.Errorf("resources[%d] (%s) card.filter: %w", i, r.Name, fe))
			}
		}
		if r.List != nil {
			for _, fe := range validateComputedFields(r.List.Computed, columnNamesOf(r.List.Columns), fmt.Sprintf("resources[%d] (%s) list", i, r.Name)) {
				add(fe)
			}
		}
		if r.Card != nil {
			for _, fe := range validateComputedFields(r.Card.Computed, fieldNamesOf(r.Card.Fields), fmt.Sprintf("resources[%d] (%s) card", i, r.Name)) {
				add(fe)
			}
		}
		if r.Detail != nil {
			for _, fe := range validateComputedFields(r.Detail.Computed, fieldNamesOf(r.Detail.Fields), fmt.Sprintf("resources[%d] (%s) detail", i, r.Name)) {
				add(fe)
			}
		}
		if err := validateResourceHooks(r); err != nil {
			add(fmt.Errorf("resources[%d]: %w", i, err))
		}
		for _, e := range validateCopies(r, i) {
			add(e)
		}
		for _, e := range validateChildren(cfg, r, i) {
			add(e)
		}
	}
	validateProcedures(cfg, add)
	validateScripts(cfg, add)
	for i, p := range cfg.Pages {
		if p.Name == "" {
			add(fmt.Errorf("pages[%d].name is required", i))
		}
		if p.Path == "" {
			cfg.Pages[i].Path = "/" + p.Name
		}
		clampWidgetColumns(&cfg.Pages[i], i, add)
	}
	return errs
}

// clampColumns clamps a grid column count into the supported [1,12] range that
// the pre-built stylesheet safelists (lg:grid-cols-1..12). Values >12 previously
// emitted arbitrary tailwind classes.
func clampColumns(v int) int {
	if v < 1 {
		return 1
	}
	if v > 12 {
		return 12
	}
	return v
}

// clampWidgetColumns clamps every stats_grid widget's Columns into [1,12] (see
// clampColumns), recursing into nested widgets. Each out-of-range value is
// recorded as a non-fatal warning.
func clampWidgetColumns(p *types.Page, pageIdx int, add func(error)) {
	var walk func(w *types.Widget, path string)
	walk = func(w *types.Widget, path string) {
		if w == nil {
			return
		}
		if w.Type == "stats_grid" && (w.Columns < 1 || w.Columns > 12) {
			add(warn("pages[%d] %s columns %d is out of range [1,12], clamping to %d", pageIdx, path, w.Columns, clampColumns(w.Columns)))
			w.Columns = clampColumns(w.Columns)
		}
		for j := range w.Widgets {
			walk(&w.Widgets[j], fmt.Sprintf("%s[%d]", path, j))
		}
	}
	for j := range p.Widgets {
		walk(&p.Widgets[j], fmt.Sprintf("widgets[%d]", j))
	}
}

// validateResourceHooks checks that every hook declared on a resource's form
// actions and custom actions is well-formed: each hook needs a name and
// exactly one of fn/sql/proc/script set, and each custom action must not mix
// query/proc/script.
// Params: r (the resource definition).
// Returns: an error describing the first invalid hook, or nil.
func validateResourceHooks(r types.Resource) error {
	if r.Form != nil {
		for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update, r.Form.Delete} {
			if fa == nil {
				continue
			}
			if err := validateHooks(fa.Hooks); err != nil {
				return err
			}
		}
	}
	for _, a := range r.Actions {
		if err := validateAction(a); err != nil {
			return fmt.Errorf("actions: %w", err)
		}
		if err := validateHooks(a.Hooks); err != nil {
			return fmt.Errorf("actions: %w", err)
		}
	}
	return nil
}

// validateAction checks that a custom action does not mix the query, proc and
// script execution modes (they are mutually exclusive). All empty is allowed so
// an action can run hooks only.
// Params: a (the action definition).
// Returns: an error when two or more of query/proc/script are set, or nil.
func validateAction(a types.Action) error {
	kinds := 0
	if a.Query != "" {
		kinds++
	}
	if a.Proc != "" {
		kinds++
	}
	if a.Script != "" {
		kinds++
	}
	if kinds > 1 {
		return fmt.Errorf("%q: query, proc and script are mutually exclusive", a.Name)
	}
	return nil
}

// driver returns the database driver of the first configured connection,
// defaulting to "postgres" when no connections are configured (mirrors the
// generator's driver()).
// Params: cfg (the config to inspect).
// Returns: the driver name.
func driver(cfg *types.Config) string {
	for _, conn := range cfg.Connections {
		if conn.Driver != "" {
			return conn.Driver
		}
	}
	return "postgres"
}

// validateProcedures checks the `procedures:` block: every entry needs a name
// and names must be unique. When the driver is sqlite, every proc: reference
// on an action or hook must match a declared procedure — on sqlite proc:
// execution is driven by the declared body, so an undeclared reference is a
// fatal config error (mirroring the plugin-load-failure semantics). Postgres
// and mssql ignore the block entirely (real procedures come from user DDL).
// Params: cfg (the config to validate), add (collects a validation problem).
func validateProcedures(cfg *types.Config, add func(error)) {
	names := map[string]bool{}
	for i, p := range cfg.Procedures {
		if p.Name == "" {
			add(fmt.Errorf("procedures[%d].name is required", i))
			continue
		}
		if names[p.Name] {
			add(fmt.Errorf("procedures[%d].name %q is duplicated", i, p.Name))
		}
		names[p.Name] = true
	}
	d := driver(cfg)
	if d != "sqlite" && d != "sqlite3" {
		return
	}
	for i, r := range cfg.Resources {
		for label, proc := range procRefs(r) {
			if !names[proc] {
				add(fmt.Errorf("resources[%d] (%s) %s references undeclared procedure %q - add a matching procedures: entry", i, r.Name, label, proc))
			}
		}
	}
}

// validateScripts walks every Lua script body in the config and emits a
// non-blocking Warning for each syntax error found by the Lua parser.
func validateScripts(cfg *types.Config, add func(error)) {
	for i, r := range cfg.Resources {
		checkHookScripts := func(hooks *types.Hooks, label string) {
			if hooks == nil {
				return
			}
			for j, h := range hooks.Before {
				if h.Script != "" {
					for _, e := range luasrc.SyntaxCheck(h.Script) {
						add(warn("resources[%d] (%s) %s before[%d] %s script: %d: %s", i, r.Name, label, j, h.Name, e.Line, e.Message))
					}
				}
			}
			for j, h := range hooks.After {
				if h.Script != "" {
					for _, e := range luasrc.SyntaxCheck(h.Script) {
						add(warn("resources[%d] (%s) %s after[%d] %s script: %d: %s", i, r.Name, label, j, h.Name, e.Line, e.Message))
					}
				}
			}
		}
		if r.Form != nil {
			if r.Form.Create != nil {
				checkHookScripts(r.Form.Create.Hooks, "create")
			}
			if r.Form.Update != nil {
				checkHookScripts(r.Form.Update.Hooks, "update")
			}
			if r.Form.Delete != nil {
				checkHookScripts(r.Form.Delete.Hooks, "delete")
			}
		}
		for j, a := range r.Actions {
			if a.Script != "" {
				for _, e := range luasrc.SyntaxCheck(a.Script) {
					add(warn("resources[%d] (%s) action[%d] %s script: %d: %s", i, r.Name, j, a.Name, e.Line, e.Message))
				}
			}
			checkHookScripts(a.Hooks, "action "+a.Name)
		}
	}
}

// procRefs returns every proc: reference on a resource as a map keyed by a
// human-readable "action <name>" / "action <name> hook <name>" label so
// validation errors name the exact site.
// Params: r (the resource definition).
// Returns: a map of site label to procedure name.
func procRefs(r types.Resource) map[string]string {
	refs := map[string]string{}
	collect := func(label string, h *types.Hooks) {
		if h == nil {
			return
		}
		for _, list := range [][]types.Hook{h.Before, h.After} {
			for _, hook := range list {
				if hook.Proc != "" {
					refs[label+" hook "+hook.Name] = hook.Proc
				}
			}
		}
	}
	for _, a := range r.Actions {
		if a.Proc != "" {
			refs["action "+a.Name] = a.Proc
		}
		collect("action "+a.Name, a.Hooks)
	}
	if r.Form != nil {
		for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update, r.Form.Delete} {
			if fa == nil {
				continue
			}
			collect("form", fa.Hooks)
		}
	}
	return refs
}

// validateHooks validates a single Hooks block, walking the before and after
// lists and checking each hook's name and fn/sql/proc combination.
// Params: h (the hooks block; nil is valid).
// Returns: an error describing the first invalid hook, or nil.
// validateFilter checks a list/card filter config:
//   - where is required (non-empty) when a filter config is present;
//   - every $N token referenced by where must have a matching runtime param:
//     the highest $N must not exceed len(params) — a where expression using $N
//     with no (or too few) declared params is an error;
//   - every param must have a non-empty, unique name.
//
// Params: f (the filter config; nil is valid).
// Returns: the validation problems found (empty when valid).
func validateFilter(f *types.FilterConfig) []error {
	if f == nil {
		return nil
	}
	var errs []error
	if f.Where == "" {
		errs = append(errs, fmt.Errorf("where is required"))
	} else {
		maxN := 0
		for _, m := range paramRefRE.FindAllStringSubmatch(f.Where, -1) {
			if n, _ := strconv.Atoi(m[1]); n > maxN {
				maxN = n
			}
		}
		if maxN > len(f.Params) {
			if len(f.Params) == 0 {
				errs = append(errs, fmt.Errorf("where references $%d but no params are declared", maxN))
			} else {
				errs = append(errs, fmt.Errorf("where references $%d but only %d param(s) are declared", maxN, len(f.Params)))
			}
		}
	}
	seen := map[string]bool{}
	for i, p := range f.Params {
		if p.Name == "" {
			errs = append(errs, fmt.Errorf("params[%d].name is required", i))
			continue
		}
		if seen[p.Name] {
			errs = append(errs, fmt.Errorf("params[%d].name %q is duplicated", i, p.Name))
		}
		seen[p.Name] = true
	}
	return errs
}

// columnNamesOf extracts the names of a list's columns.
func columnNamesOf(cols []types.Column) []string {
	var names []string
	for _, c := range cols {
		names = append(names, c.Name)
	}
	return names
}

// fieldNamesOf extracts the names of a section's fields.
func fieldNamesOf(fields []types.Field) []string {
	var names []string
	for _, f := range fields {
		names = append(names, f.Name)
	}
	return names
}

// validateComputedFields checks a section's virtual computed fields (E7).
// Each entry needs a name, a non-empty expression and no collisions with a real
// column/field of the same section — a duplicate alias breaks both the SELECT
// and the emitted Go scan source. Unsupported types are a non-fatal warning
// (renderers already fall back to plain text).
func validateComputedFields(computed []types.ComputedField, realNames []string, path string) []error {
	var errs []error
	seen := map[string]bool{}
	for i, c := range computed {
		if c.Name == "" {
			errs = append(errs, fmt.Errorf("%s[%d] computed name is required", path, i))
			continue
		}
		if seen[c.Name] {
			errs = append(errs, fmt.Errorf("%s[%d] computed name %q is duplicated", path, i, c.Name))
		}
		seen[c.Name] = true
		if c.Expression == "" {
			errs = append(errs, fmt.Errorf("%s[%d] computed %q expression is required", path, i, c.Name))
		}
		if c.Type != "" {
			if _, ok := types.FieldTypes[c.Type]; !ok {
				errs = append(errs, warn("%s[%d] computed %q type %q is not a supported field type, rendering as string", path, i, c.Name, c.Type))
			}
		}
		for _, rn := range realNames {
			if rn == c.Name {
				errs = append(errs, fmt.Errorf("%s[%d] computed name %q collides with a real column", path, i, c.Name))
			}
		}
	}
	return errs
}

func validateHooks(h *types.Hooks) error {
	if h == nil {
		return nil
	}
	for _, list := range []struct {
		name string
		h    []types.Hook
	}{{"before", h.Before}, {"after", h.After}} {
		for j, hook := range list.h {
			if hook.Name == "" {
				return fmt.Errorf("%s[%d].name is required", list.name, j)
			}
			kindCount := 0
			if hook.Fn != "" {
				kindCount++
			}
			if hook.SQL != "" {
				kindCount++
			}
			if hook.Proc != "" {
				kindCount++
			}
			if hook.Script != "" {
				kindCount++
			}
			if kindCount != 1 {
				return fmt.Errorf("%s[%d]: exactly one of fn, sql, proc or script is required", list.name, j)
			}
		}
	}
	return nil
}

// validateCopies checks a resource's `copies:` auto-fill mappings. Copies only
// make sense on a select/relation picker field, and every copy target must be a
// real field of the SAME form (the create+update union) other than the picker
// itself. Problems are non-fatal warnings — a missing target simply no-ops at
// runtime (the JS finds no element).
func validateCopies(r types.Resource, idx int) []error {
	var errs []error
	var formNames []string
	if r.Form != nil {
		for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update} {
			if fa == nil {
				continue
			}
			for _, f := range fa.Fields {
				formNames = append(formNames, f.Name)
			}
		}
	}
	inForm := func(name string) bool {
		for _, n := range formNames {
			if n == name {
				return true
			}
		}
		return false
	}
	checkAction := func(section string, fields []types.Field) {
		for _, f := range fields {
			if len(f.Copies) == 0 {
				continue
			}
			if f.Type != "select" && f.Type != "relation" {
				errs = append(errs, warn("resources[%d] (%s) %s field %q sets copies but is not a select/relation picker field", idx, r.Name, section, f.Name))
				continue
			}
			for target := range f.Copies {
				if target == f.Name {
					errs = append(errs, warn("resources[%d] (%s) %s field %q copies into itself", idx, r.Name, section, f.Name))
					continue
				}
				if !inForm(target) {
					errs = append(errs, warn("resources[%d] (%s) %s field %q copies into %q which is not a field of the same form", idx, r.Name, section, f.Name, target))
				}
			}
		}
	}
	if r.Form != nil && r.Form.Create != nil {
		checkAction("form.create", r.Form.Create.Fields)
	}
	if r.Form != nil && r.Form.Update != nil {
		checkAction("form.update", r.Form.Update.Fields)
	}
	return errs
}

// validateChildren checks the `children:` block of a header resource (D14):
// every child.resource must exist, and the FK used to fetch the child lines
// must resolve — either the explicit column is a real column of the child's
// table, or (when empty) it is derived by scanning the `schema:` block for a
// foreign key pointing at the parent's table/key. Column overrides must
// reference child schema columns. When the schema block is absent the check is
// skipped (the generator degrades for hand-written configs).
func validateChildren(cfg *types.Config, r types.Resource, idx int) []error {
	if len(r.Children) == 0 {
		return nil
	}
	var errs []error
	childByName := map[string]types.Resource{}
	for _, cr := range cfg.Resources {
		childByName[cr.Name] = cr
	}
	parentTable := tableName(r)
	parentKey := resourceKey(r)
	for j, ch := range r.Children {
		if ch.Resource == "" {
			errs = append(errs, fmt.Errorf("resources[%d] (%s) children[%d].resource is required", idx, r.Name, j))
			continue
		}
		child, ok := childByName[ch.Resource]
		if !ok {
			errs = append(errs, fmt.Errorf("resources[%d] (%s) children[%d].resource %q is not a defined resource", idx, r.Name, j, ch.Resource))
			continue
		}
		st := schemaTableByName(cfg, tableName(child))
		if st == nil {
			continue
		}
		fkCol := ch.Column
		if fkCol == "" {
			for _, fk := range st.ForeignKeys {
				if strings.EqualFold(fk.ForeignTable, parentTable) && strings.EqualFold(fk.ForeignColumn, parentKey) {
					fkCol = fk.Column
					break
				}
			}
			if fkCol == "" {
				errs = append(errs, fmt.Errorf("resources[%d] (%s) children[%d]: no FK from %s to %s in the schema block; set column explicitly", idx, r.Name, j, tableName(child), parentTable))
				continue
			}
		} else if !schemaHasColumn(st, fkCol) {
			errs = append(errs, fmt.Errorf("resources[%d] (%s) children[%d].column %q is not a column of %s", idx, r.Name, j, fkCol, tableName(child)))
		}
		for _, c := range ch.Columns {
			if !schemaHasColumn(st, c.Name) {
				errs = append(errs, fmt.Errorf("resources[%d] (%s) children[%d].columns %q is not a column of %s", idx, r.Name, j, c.Name, tableName(child)))
			}
		}
	}
	return errs
}

// tableName mirrors the generator's tableName() so validation matches the table
// the generated handlers will hit: the explicit "table" override, else the
// lowercase plural of the resource name.
func tableName(r types.Resource) string {
	if r.Table != "" {
		return r.Table
	}
	return strings.ToLower(r.Name) + "s"
}

// resourceKey is the row-key column of a resource: the "id_column" override if
// set, else "id" (matches the generator's idColumn()).
func resourceKey(r types.Resource) string {
	if r.IDColumn != "" {
		return r.IDColumn
	}
	return "id"
}

// schemaTableByName returns the schema block entry for a table, comparing exact
// then case-insensitive (MSSQL PascalCase names). nil when the config carries
// no schema block or the table is absent.
func schemaTableByName(cfg *types.Config, name string) *types.SchemaTable {
	if cfg.Schema == nil {
		return nil
	}
	for i := range cfg.Schema.Tables {
		t := &cfg.Schema.Tables[i]
		if t.Name == name || strings.EqualFold(t.Name, name) {
			return t
		}
	}
	return nil
}

// schemaHasColumn reports whether a schema table carries a column with the
// given name (exact or case-insensitive).
func schemaHasColumn(st *types.SchemaTable, name string) bool {
	if st == nil {
		return false
	}
	for _, c := range st.Columns {
		if c.Name == name || strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}
