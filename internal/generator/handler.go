// handler.go
//
// Generates the HTTP handlers for each resource in the admin panel
// (internal/panel/resources/{resource}/): list with dynamic WHERE/ORDER BY/
// LIMIT, detail via SQLC, create/update with raw SQL, delete, named actions
// and CSV export. It also holds shared helpers for building column/field
// definitions, scanning rows, and converting snake_case to PascalCase.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/MichalHerstus/yaga/internal/filterexpr"
	"github.com/MichalHerstus/yaga/internal/types"
)

// generateResource writes all handler files for a single resource into its
// package directory: list.go, detail.go, create.go/update.go/delete.go,
// export.go and actions.go, depending on which sections the resource declares.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error if any handler file fails to write.
func (g *Generator) generateResource(r types.Resource) error {
	dir := filepath.Join(g.OutDir, "internal/panel/resources", strings.ToLower(r.Name))

	if r.List != nil {
		if err := g.generateListHandler(dir, r); err != nil {
			return err
		}
	}
	if r.Card != nil {
		if err := g.generateCardHandler(dir, r); err != nil {
			return err
		}
	}
	if r.Detail != nil {
		if err := g.generateDetailHandler(dir, r); err != nil {
			return err
		}
	}
	if r.Form != nil {
		if err := g.generateFormHandlers(dir, r); err != nil {
			return err
		}
	}
	if r.List != nil {
		if err := g.generateCSVHandler(dir, r); err != nil {
			return err
		}
	}
	if len(r.Actions) > 0 {
		if err := g.generateActionHandler(dir, r); err != nil {
			return err
		}
	}
	if hasBulkActions(r) {
		if err := g.generateBulkHandler(dir, r); err != nil {
			return err
		}
	}
	// D14: the loadChildLines helper is package-scoped, so it lives in its own
	// file (shared by detail.go/update.go) instead of being duplicated.
	if len(r.Children) > 0 {
		if err := g.generateChildLinesHelper(dir, r); err != nil {
			return err
		}
	}
	return nil
}

// generateChildLinesHelper writes childlines.go with the package-level
// loadChildLines helper used by the detail and update handlers to fetch a
// header's child lines (D14).
func (g *Generator) generateChildLinesHelper(dir string, r types.Resource) error {
	code := fmt.Sprintf(`package %s

import (
    "context"
    "database/sql"
)

func loadChildLines(ctx context.Context, db *sql.DB, query string, parentID int64) []map[string]interface{} {
    rows, err := db.QueryContext(ctx, query, parentID)
    if err != nil {
        return nil
    }
    defer rows.Close()
    cols, _ := rows.Columns()
    var out []map[string]interface{}
    for rows.Next() {
        vals := make([]interface{}, len(cols))
        ptrs := make([]interface{}, len(cols))
        for i := range vals {
            ptrs[i] = &vals[i]
        }
        if err := rows.Scan(ptrs...); err != nil {
            continue
        }
        m := make(map[string]interface{}, len(cols))
        for i, c := range cols {
            m[c] = vals[i]
        }
        out = append(out, m)
    }
    return out
}
`, strings.ToLower(r.Name))
	return os.WriteFile(filepath.Join(dir, "childlines.go"), []byte(code), 0644)
}

// hasBulkActions reports whether any action on the resource declares the bulk
// flag, which enables the bulk route and the bulk UI on the list view.
// Params: r (the resource definition).
// Returns: true when at least one action is bulk-enabled.
func hasBulkActions(r types.Resource) bool {
	for _, a := range r.Actions {
		if a.Bulk {
			return true
		}
	}
	return false
}

// hasActionPolicies reports whether any custom action on the resource declares
// a policy, which wraps the action and bulk routes in auth.ActionRBACMiddleware.
// Params: r (the resource definition).
// Returns: true when at least one action carries a role policy.
func hasActionPolicies(r types.Resource) bool {
	for _, a := range r.Actions {
		if a.Policy != "" {
			return true
		}
	}
	return false
}

// colDefsStr renders the []viewmodels.ColumnDef literal for a list of
// columns, filling in the label (defaults to the column name), field type,
// sortable/searchable flags and the static options map (nil when empty).
// Params: cols (list columns from the YAML config).
// Returns: a comma-joined Go source string for the column defs.
func colDefsStr(cols []types.Column) string {
	var defs []string
	for _, c := range cols {
		label := c.Label
		if label == "" {
			label = c.Name
		}
		opts := "nil"
		if len(c.Options) > 0 {
			opts = "map[string]string{"
			for k, v := range c.Options {
				opts += fmt.Sprintf("%q: %q, ", k, v)
			}
			opts += "}"
		}
		defs = append(defs, fmt.Sprintf("{Name: %q, Label: %q, FieldType: %q, Sortable: %t, Searchable: %t, Options: %s}", c.Name, label, c.Type, c.Sortable, c.Searchable, opts))
	}
	return strings.Join(defs, ",\n")
}

// fieldDefsStr renders the []viewmodels.ColumnDef literal for a list of form
// or detail fields, defaulting the label to the field name and the field type
// to "string".
// Params: fields (field definitions from the YAML config).
// Returns: a comma-joined Go source string for the field defs.
func fieldDefsStr(fields []types.Field) string {
	var defs []string
	for _, f := range fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		opts := "nil"
		if len(f.Options) > 0 {
			opts = "map[string]string{"
			for k, v := range f.Options {
				opts += fmt.Sprintf("%q: %q, ", k, v)
			}
			opts += "}"
		}
		ft := f.Type
		if ft == "" {
			ft = "string"
		}
		defs = append(defs, fmt.Sprintf("{Name: %q, Label: %q, FieldType: %q, Options: %s}", f.Name, label, ft, opts))
	}
	return strings.Join(defs, ",\n")
}

// fieldDefsFromDetail renders the field defs for a detail view; it is a thin
// wrapper around fieldDefsStr kept for readability at call sites.
// Params: fields (detail field definitions).
// Returns: the rendered Go source string.
func fieldDefsFromDetail(fields []types.Field) string {
	return fieldDefsStr(fields)
}

// tableName derives the SQL table name for a resource: the explicit "table"
// override when set, otherwise the lowercase resource name plus a plural "s"
// (e.g. "User" -> "users"). Introspected projects emit "table" whenever the
// convention does not match the real table (e.g. "Zamestnanec" -> table
// "Zamestnanec", not "zamestnanecs").
// Params: r (the resource definition).
// Returns: the SQL table name.
func tableName(r types.Resource) string {
	if r.Table != "" {
		return r.Table
	}
	return strings.ToLower(r.Name) + "s"
}

// idColumn returns the name of the row-key column for a resource: the explicit
// "id_column" override when set, otherwise "id". Row maps in list/card/detail
// views are keyed by the real column name, so MSSQL tables with an "ID" column
// must emit id_column to keep View/Edit/action links working.
// Params: r (the resource definition).
// Returns: the row-key column name.
func idColumn(r types.Resource) string {
	if r.IDColumn != "" {
		return r.IDColumn
	}
	return "id"
}

// resourceTitle returns the page title used for a resource view: the YAML
// label when set, falling back to the resource name.
// Params: r (the resource definition).
// Returns: the display title string.
func resourceTitle(r types.Resource) string {
	if r.Label != "" {
		return r.Label
	}
	return r.Name
}

// fkLabelJoin describes a LEFT JOIN a generated list/card/export handler must
// emit so an FK label column ({fk}_label) can select the foreign table's label
// column. It mirrors the JOIN the introspector writes into the SQLC list/detail
// queries.
type fkLabelJoin struct {
	colName    string
	selectPart string
	fromPart   string
}

// labelJoins reconstructs the LEFT JOINs the SQLC queries use for FK label
// columns: for every view column named "{fk}_label" with a matching relation
// form field (options_query "List{Foreign}", options_value, options_label) it
// produces the aliased SELECT fragment and the JOIN clause. Columns without a
// matching relation field are skipped so the emitted SQL keeps the historical
// (unjoined) behavior.
// Params: r (the resource definition), colNames (the view's column/field names).
// Returns: the join specs, possibly empty.
func (g *Generator) labelJoins(r types.Resource, colNames []string) []fkLabelJoin {
	var joins []fkLabelJoin
	st := g.schemaTable(tableName(r))
	for _, c := range colNames {
		if !strings.HasSuffix(c, "_label") {
			continue
		}
		base := strings.TrimSuffix(c, "_label")
		// Prefer the schema block (D11): the FK metadata carries the foreign
		// table, key column and label column directly.
		if st != nil {
			joined := false
			for _, fk := range st.ForeignKeys {
				if !strings.EqualFold(fk.Column, base) {
					continue
				}
				ftable := fk.ForeignTable
				joins = append(joins, fkLabelJoin{
					colName:    c,
					selectPart: fmt.Sprintf("f_%s.%s AS %s", ftable, g.quoteIdent(fk.Label), c),
					fromPart:   fmt.Sprintf("LEFT JOIN %s f_%s ON f_%s.%s = t.%s", g.quoteIdent(ftable), ftable, ftable, g.quoteIdent(fk.ForeignColumn), g.quoteIdent(fk.Column)),
				})
				joined = true
				break
			}
			if joined {
				continue
			}
		}
		// Legacy fallback: a relation form field with options_query.
		f := relationFormField(r, base)
		if f == nil || f.OptionsQuery == "" {
			continue
		}
		foreignName := strings.TrimPrefix(f.OptionsQuery, "List")
		var foreign *types.Resource
		for i := range g.Config.Resources {
			if g.Config.Resources[i].Name == foreignName {
				foreign = &g.Config.Resources[i]
				break
			}
		}
		if foreign == nil {
			continue
		}
		ftable := tableName(*foreign)
		joins = append(joins, fkLabelJoin{
			colName:    c,
			selectPart: fmt.Sprintf("f_%s.%s AS %s", ftable, g.quoteIdent(f.OptionsLabel), c),
			fromPart:   fmt.Sprintf("LEFT JOIN %s f_%s ON f_%s.%s = t.%s", g.quoteIdent(ftable), ftable, ftable, g.quoteIdent(f.OptionsValue), g.quoteIdent(base)),
		})
	}
	return joins
}

// relationFormField returns the relation-typed form field with the given name,
// searching the create then the update form action. nil when absent.
// Params: r (the resource definition), name (the field name to find).
// Returns: the matching field, or nil.
func relationFormField(r types.Resource, name string) *types.Field {
	var fields []types.Field
	if r.Form != nil {
		if r.Form.Create != nil {
			fields = append(fields, r.Form.Create.Fields...)
		}
		if r.Form.Update != nil {
			fields = append(fields, r.Form.Update.Fields...)
		}
	}
	for i := range fields {
		if fields[i].Name == name && fields[i].Type == "relation" {
			return &fields[i]
		}
	}
	return nil
}

// listSelectFrom renders the SELECT column list and FROM fragment for the raw
// list/card/export queries. When the view has FK label columns backed by
// relation form fields, real columns are qualified with the "t" alias, label
// columns select the joined foreign table's column, and the FROM carries the
// LEFT JOINs; colPrefix ("t.") and tableRef ("{table} t") are returned so the
// WHERE/ORDER BY clauses stay unambiguous. Without joins the historical
// unqualified fragments are returned so generated output stays unchanged.
// Params: r (the resource definition), tName (the SQL table name), colNames
// (the view's column/field names).
// Returns: the SELECT list, the FROM fragment (alias + JOINs), the column
// prefix, the table reference, and whether any JOINs were emitted.
func (g *Generator) listSelectFrom(r types.Resource, tName string, colNames []string) (selectFrag, fromFrag, colPrefix, tableRef string, hasJoins bool) {
	selectFrag, fromFrag, colPrefix, tableRef, hasJoins = g.listSelectFromParts(r, tName, colNames)
	return embedSQL(selectFrag), embedSQL(fromFrag), colPrefix, tableRef, hasJoins
}

// listSelectFromParts is listSelectFrom returning raw (un-embedSQL'd)
// fragments. The E7 derived-table wrapper needs the raw text to nest one
// SELECT inside another before a single embedSQL pass.
func (g *Generator) listSelectFromParts(r types.Resource, tName string, colNames []string) (selectFrag, fromFrag, colPrefix, tableRef string, hasJoins bool) {
	joins := g.labelJoins(r, colNames)
	if len(joins) == 0 {
		qn := make([]string, len(colNames))
		for i, c := range colNames {
			qn[i] = g.quoteIdent(c)
		}
		return strings.Join(qn, ", "), g.quoteIdent(tName), "", tName, false
	}
	labelCols := map[string]bool{}
	for _, j := range joins {
		labelCols[j.colName] = true
	}
	sel := make([]string, 0, len(colNames))
	for _, c := range colNames {
		if labelCols[c] {
			continue
		}
		sel = append(sel, "t."+g.quoteIdent(c))
	}
	for _, j := range joins {
		sel = append(sel, j.selectPart)
	}
	fromParts := []string{g.quoteIdent(tName) + " t"}
	for _, j := range joins {
		fromParts = append(fromParts, j.fromPart)
	}
	return strings.Join(sel, ", "), strings.Join(fromParts, " "), "t.", tName + " t", true
}

// computedSelectItems renders the raw ", <expr> AS "name"" SELECT items for a
// block of computed fields (E7). Helpers.* tokens are expanded to
// driver-correct SQL here. The returned text is NOT embedSQL'd; callers embed
// it according to how they splice it into emitted Go literals.
// Params: computed (the resource view's computed fields).
// Returns: a comma-separated list of "<expandhelpers(expr)> AS "<ident>"" items.
func (g *Generator) computedSelectItems(computed []types.ComputedField) string {
	var items []string
	for _, c := range computed {
		items = append(items, g.expandHelpers(c.Expression)+" AS "+g.quoteIdent(c.Name))
	}
	return strings.Join(items, ", ")
}

// computedDefsStr renders the Go ColumnDef literals for a section's computed
// fields, prefixed with the separator that the caller's composite literal
// needs between the last real def and the first computed def. It appends no
// trailing separator (the call site's literal adds the final comma) and
// returns "" when there are no computed fields, so feature-off output stays
// byte-identical.
func computedDefsStr(computed []types.ComputedField) string {
	if len(computed) == 0 {
		return ""
	}
	var defs []string
	for _, c := range computed {
		label := c.Label
		if label == "" {
			label = c.Name
		}
		t := c.Type
		if t == "" {
			t = "string"
		}
		defs = append(defs, fmt.Sprintf("{Name: %q, Label: %q, FieldType: %q}", c.Name, label, t))
	}
	return ", " + strings.Join(defs, ", ")
}

// computedColumns converts computed fields into synthetic list columns carrying
// just name/label/type, so the list templ can render headers and cells for a
// virtual column exactly like a real one. Sortable/searchable stay false.
func computedColumns(computed []types.ComputedField) []types.Column {
	var cols []types.Column
	for _, c := range computed {
		label := c.Label
		if label == "" {
			label = c.Name
		}
		t := c.Type
		if t == "" {
			t = "string"
		}
		cols = append(cols, types.Column{Name: c.Name, Label: label, Type: t})
	}
	return cols
}

// computedFields converts computed fields into synthetic form fields for the
// read-only card and detail views (E7). Only name/label/type are meaningful;
// computed fields never post back through a form.
func computedFields(computed []types.ComputedField) []types.Field {
	var flds []types.Field
	for _, c := range computed {
		label := c.Label
		if label == "" {
			label = c.Name
		}
		t := c.Type
		if t == "" {
			t = "string"
		}
		flds = append(flds, types.Field{Name: c.Name, Label: label, Type: t})
	}
	return flds
}

// filterReferencesComputed reports whether a compiled filter's WHERE fragment
// references any computed field (E7). Such a filter can only run against the
// derived-table wrapper (the plain query has no such column), so the list/card
// handlers switch the SELECT to "(SELECT ... ) _base" when it returns true.
// Params: compiled (the compiled filter), computed (the view's computed fields).
// Returns: true when at least one computed name appears in the compiled WHERE.
func (g *Generator) filterReferencesComputed(compiled *filterexpr.Compiled, computed []types.ComputedField) bool {
	if compiled == nil {
		return false
	}
	for _, c := range computed {
		if strings.Contains(compiled.Frag, filterexpr.QuoteIdent(g.driver(), c.Name)) {
			return true
		}
	}
	return false
}

// filterCompile parses and compiles a list/card filter's `where` expression
// into a dialect-correct SQL fragment for the configured driver.
// Params: f (the filter config), colPrefix (table alias prefix, "" or "t.").
// Returns: the compiled fragment + bindings.
func (g *Generator) filterCompile(f *types.FilterConfig, colPrefix string) (*filterexpr.Compiled, error) {
	expr, err := filterexpr.Parse(f.Where)
	if err != nil {
		return nil, err
	}
	return expr.SQL(g.driver(), colPrefix)
}

// filterParamName returns the URL query param name for a $N-referenced filter
// param (0-based index). It honours the declared params list, falling back to
// the p<N> convention.
// Params: f (the filter config), idx (0-based param index).
// Returns: the param name used in the fp_<name> query key.
func filterParamName(f *types.FilterConfig, idx int) string {
	if idx < len(f.Params) && f.Params[idx].Name != "" {
		return f.Params[idx].Name
	}
	return fmt.Sprintf("p%d", idx+1)
}

// filterParamLabel returns the displayed label for a $N-referenced filter
// param (0-based index), falling back to "Value N".
// Params: f (the filter config), idx (0-based param index).
// Returns: the label text.
func filterParamLabel(f *types.FilterConfig, idx int) string {
	if idx < len(f.Params) && f.Params[idx].Label != "" {
		return f.Params[idx].Label
	}
	return fmt.Sprintf("Value %d", idx+1)
}

// refParamIndices returns the distinct 0-based param indices referenced by a
// compiled filter's bindings, sorted ascending. Used to drive the form inputs
// and the filterQS echo.
// Params: compiled (the compiled filter fragment).
// Returns: a sorted slice of referenced param indices.
func refParamIndices(compiled *filterexpr.Compiled) []int {
	seen := map[int]bool{}
	var out []int
	for _, b := range compiled.Bindings {
		if !seen[b.Param] {
			seen[b.Param] = true
			out = append(out, b.Param)
		}
	}
	sort.Ints(out)
	return out
}

// filterRuntimeBlock emits the Go statements that read the filter's $N params
// from the request and, when every referenced param is non-empty, append the
// bound args (in SQL-text order) and add the compiled WHERE fragment to the
// `parts` slice while setting `filterApplied`. The surrounding handler core
// declares `parts`, `filterApplied`, `args` and (on pg/mssql) `argIdx`.
// Params: f (the filter config), compiled (the compiled fragment + bindings).
// Returns: the runtime filter-apply Go source block.
func (g *Generator) filterRuntimeBlock(f *types.FilterConfig, compiled *filterexpr.Compiled) string {
	var sb strings.Builder
	binds := compiled.Bindings
	if len(binds) == 0 {
		// Literal-only filter: always applied, no runtime args.
		fmt.Fprintf(&sb, "        frag := %s\n", strconv.Quote(compiled.Frag))
		sb.WriteString("        parts = append(parts, \"(\"+frag+\")\")\n")
		sb.WriteString("        filterApplied = true\n")
		return sb.String()
	}
	for i, b := range binds {
		fmt.Fprintf(&sb, "        f%d := r.URL.Query().Get(\"fp_%s\")\n", i, filterParamName(f, b.Param))
	}
	var conds []string
	for i := range binds {
		conds = append(conds, fmt.Sprintf("f%d != \"\"", i))
	}
	fmt.Fprintf(&sb, "        if %s {\n", strings.Join(conds, " && "))
	fmt.Fprintf(&sb, "            frag := %s\n", strconv.Quote(compiled.Frag))
	for i, b := range binds {
		if g.isSQLite() {
			sb.WriteString("            frag = strings.Replace(frag, \"__GFP__\", \"?\", 1)\n")
		} else {
			sb.WriteString("            frag = strings.Replace(frag, \"__GFP__\", fmt.Sprintf(\"$%d\", argIdx), 1)\n")
			sb.WriteString("            argIdx++\n")
		}
		sb.WriteString("            {\n")
		fmt.Fprintf(&sb, "                v := f%d\n", i)
		if b.Contains {
			sb.WriteString("                v = \"%\" + v + \"%\"\n")
		}
		sb.WriteString("                args = append(args, v)\n")
		sb.WriteString("            }\n")
	}
	sb.WriteString("            parts = append(parts, \"(\"+frag+\")\")\n")
	sb.WriteString("            filterApplied = true\n")
	sb.WriteString("        }\n")
	return sb.String()
}

// filterViewmodelCode emits the Go statements that build the viewmodels
// FilterData (echoing current param values) and the filterQS string used by
// pagination, given the declared filter and the already-computed
// `filterApplied`. It declares `filterData` and `filterQS`.
// Params: f (the filter config), compiled (the compiled fragment + bindings).
// Returns: the Go source block, or "" when the resource has no filter.
func (g *Generator) filterViewmodelCode(f *types.FilterConfig, compiled *filterexpr.Compiled) string {
	if f == nil {
		return ""
	}
	refIdx := refParamIndices(compiled)
	var sb strings.Builder
	sb.WriteString("        filterQS := \"\"\n")
	sb.WriteString("        if filterApplied {\n")
	sb.WriteString("            qs := \"filter=1\"\n")
	for _, idx := range refIdx {
		name := filterParamName(f, idx)
		sb.WriteString(fmt.Sprintf("            if v := r.URL.Query().Get(\"fp_%s\"); v != \"\" {\n", name))
		sb.WriteString(fmt.Sprintf("                qs += \"&fp_%s=\" + url.QueryEscape(v)\n", name))
		sb.WriteString("            }\n")
	}
	sb.WriteString("            filterQS = \"&\" + qs\n")
	sb.WriteString("        }\n")
	var params []string
	for _, idx := range refIdx {
		name := filterParamName(f, idx)
		params = append(params, fmt.Sprintf("viewmodels.FilterParamData{Key: %q, Label: %q, Value: r.URL.Query().Get(\"fp_%s\")}",
			name, filterParamLabel(f, idx), name))
	}
	sb.WriteString("        filterData := &viewmodels.FilterData{}\n")
	sb.WriteString(fmt.Sprintf("        filterData = &viewmodels.FilterData{Label: %q, Applied: filterApplied, Params: []viewmodels.FilterParamData{%s}}\n",
		f.Label, strings.Join(params, ", ")))
	sb.WriteString("        _ = filterData\n")
	return sb.String()
}

// filterListCore builds the args/WHERE/ORDER/query region of a filtered list
// handler. The filter block is emitted first on sqlite (positional ? binding
// matches WHERE text order) and numbered first on pg/mssql (argIdx continues
// into the search block). A `parts` slice holds the ANDed WHERE operands,
// degrading to the plain search-only WHERE when the filter is not applied.
// Params: searchableColsLiteral, colPrefix, selectFrag, fromFrag (query parts),
// filterRT (the runtime filter block).
// Returns: the handler core source for the configured driver.
func (g *Generator) filterListCore(searchableColsLiteral, colPrefix, selectFrag, fromFrag, filterRT string) string {
	if g.isSQLite() {
		return fmt.Sprintf(`        var args []interface{}
        var parts []string
        filterApplied := false

%[1]s
        var whereClauses []string
        if search != "" {
            searchableCols := []string{%[2]s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, col+" LIKE ?")
                args = append(args, "%%"+search+"%%")
            }
            parts = append(parts, "("+strings.Join(whereClauses, " OR ")+")")
        }

        whereSQL := ""
        if len(parts) > 0 {
            whereSQL = " WHERE " + strings.Join(parts, " AND ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %[3]s + sqlutil.Ident(sort) + " " + order
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %[4]s, COUNT(*) OVER() AS _total FROM %[5]s" + whereSQL + orderSQL + " LIMIT ? OFFSET ?"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        fullArgs = append(fullArgs, perPage, offset)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()
`, filterRT, searchableColsLiteral, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	}
	if g.isMSSQL() {
		return fmt.Sprintf(`        var args []interface{}
        args = append(args, perPage, offset)
        argIdx := 3
        var parts []string
        filterApplied := false

%[1]s
        var whereClauses []string
        if search != "" {
            searchableCols := []string{%[2]s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, fmt.Sprintf("%%s LIKE $%%d", col, argIdx))
                args = append(args, "%%"+search+"%%")
                argIdx++
            }
            parts = append(parts, "("+strings.Join(whereClauses, " OR ")+")")
        }

        whereSQL := ""
        if len(parts) > 0 {
            whereSQL = " WHERE " + strings.Join(parts, " AND ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %[3]s + sqlutil.Ident(sort) + " " + order
        }
        if orderSQL == "" {
            orderSQL = " ORDER BY (SELECT NULL)"
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %[4]s, COUNT(*) OVER() AS _total FROM %[5]s" + whereSQL + orderSQL + " OFFSET $2 ROWS FETCH NEXT $1 ROWS ONLY"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()
`, filterRT, searchableColsLiteral, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	}
	return fmt.Sprintf(`        var args []interface{}
        args = append(args, perPage, offset)
        argIdx := 3
        var parts []string
        filterApplied := false

%[1]s
        var whereClauses []string
        if search != "" {
            searchableCols := []string{%[2]s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, fmt.Sprintf("%%s ILIKE $%%d", col, argIdx))
                args = append(args, "%%"+search+"%%")
                argIdx++
            }
            parts = append(parts, "("+strings.Join(whereClauses, " OR ")+")")
        }

        whereSQL := ""
        if len(parts) > 0 {
            whereSQL = " WHERE " + strings.Join(parts, " AND ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %[3]s + sqlutil.Ident(sort) + " " + order
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %[4]s, COUNT(*) OVER() AS _total FROM %[5]s" + whereSQL + orderSQL + " LIMIT $1 OFFSET $2"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()
`, filterRT, searchableColsLiteral, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
}

// generateListHandler writes list.go for a resource: a List(db) handler that
// reads page/search/sort/order query parameters, builds a dynamic WHERE/ORDER
// BY/LIMIT query against the plural table name, counts the total rows for
// pagination, scans the listed columns and renders the resource list view.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateListHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	tName := tableName(r)

	var searchCols []string
	var sortableCols []string
	var colNames []string
	for _, c := range r.List.Columns {
		colNames = append(colNames, c.Name)
		if c.Searchable {
			searchCols = append(searchCols, c.Name)
		}
		if c.Sortable {
			sortableCols = append(sortableCols, c.Name)
		}
	}
	scanNames := make([]string, len(colNames))
	copy(scanNames, colNames)
	for _, c := range r.List.Computed {
		scanNames = append(scanNames, c.Name)
	}

	computedItems := g.computedSelectItems(r.List.Computed)
	hasComputed := len(r.List.Computed) > 0

	selectFrag, fromFrag, colPrefix, _, _ := g.listSelectFrom(r, tName, colNames)

	perPage := r.List.PerPage
	if perPage < 1 {
		perPage = 20
	}

	var sb strings.Builder

	// Compile the optional filter once so the runtime block, viewmodel and
	// imports can all be derived from it.
	var compiled *filterexpr.Compiled
	hasFilter := r.List.Filter != nil
	if hasFilter {
		var cerr error
		compiled, cerr = g.filterCompile(r.List.Filter, colPrefix)
		if cerr != nil {
			return cerr
		}
	}
	// E7: when the filter references a computed column the plain query has no
	// such column, so the SELECT is wrapped in a derived table exposing the
	// computed columns to the outer WHERE. The outer query has no join aliases,
	// so the filter is recompiled unprefixed.
	useDerived := hasComputed && hasFilter && g.filterReferencesComputed(compiled, r.List.Computed)
	if useDerived {
		innerSelect, innerFrom, _, _, _ := g.listSelectFromParts(r, tName, colNames)
		derived := "(SELECT " + innerSelect
		if hasComputed {
			derived += ", " + computedItems
		}
		derived += " FROM " + innerFrom + ") _base"
		var outerCols []string
		for _, n := range scanNames {
			outerCols = append(outerCols, g.quoteIdent(n))
		}
		selectFrag = embedSQL(strings.Join(outerCols, ", "))
		fromFrag = embedSQL(derived)
		colPrefix = ""
		var cerr error
		compiled, cerr = g.filterCompile(r.List.Filter, "")
		if cerr != nil {
			return cerr
		}
	} else if hasComputed {
		selectFrag += ", " + embedSQL(computedItems)
	}
	var filterRT string
	if hasFilter {
		filterRT = g.filterRuntimeBlock(r.List.Filter, compiled)
	}
	urlImport := ""
	if hasFilter {
		urlImport = "    \"net/url\"\n"
	}
	fmtImport := ""
	if !g.isSQLite() {
		// On postgres/mssql the emitted search block (and filter block when
		// $N bindings exist) always reference fmt.Sprintf, so fmt is needed
		// even when the resource declares no searchable columns.
		fmtImport = "    \"fmt\"\n"
	}

	// Package declaration and imports
	sb.WriteString(fmt.Sprintf(`package %s

import (
    "database/sql"
%s    "math"
    "net/http"
    "strconv"
    "strings"
%s
    %q
    %q
    auth %q
    httperr %q
    sqlutil %q
    layoutviews %q
)

func List(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        page, _ := strconv.Atoi(r.URL.Query().Get("page"))
        if page < 1 {
            page = 1
        }
        perPage := %d
        offset := (page - 1) * perPage

        search := r.URL.Query().Get("search")
        sort := r.URL.Query().Get("sort")
        order := r.URL.Query().Get("order")
        if order == "" {
            order = "asc"
        }

        if sort == "" {`+func() string {
		sortField := strings.TrimPrefix(r.List.DefaultSort, "-")
		sortOrder := "asc"
		if r.List.DefaultSort != sortField {
			sortOrder = "desc"
		}
		return fmt.Sprintf(`
            sort = %q
            order = %q`, sortField, sortOrder)
	}()+`
        }

        if order != "asc" && order != "desc" {
            order = "asc"
        }

        validSorts := map[string]bool{`, pkgName, fmtImport, urlImport,
		g.moduleImport("internal/viewmodels"), g.moduleImport("internal/views/resources/"+pkgName),
		g.moduleImport("internal/panel/auth"), g.moduleImport("internal/panel/httperr"), g.moduleImport("internal/panel/sqlutil"), g.moduleImport("internal/views/layout"), perPage))

	// Valid sort columns
	for i, c := range sortableCols {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%q: true", c))
	}
	sb.WriteString(`}
        if sort != "" && !validSorts[sort] {
            sort = ""
        }

`)
	searchableColsLiteral := g.quoteSQLList(searchCols, colPrefix)

	// Build the args + WHERE/ORDER/LIMIT construction. Sqlite binds ? args
	// positionally in SQL text order, so search args must come before the
	// LIMIT/OFFSET args; postgres uses numbered $N so order does not matter.
	var listCore string
	if hasFilter {
		listCore = g.filterListCore(searchableColsLiteral, colPrefix, selectFrag, fromFrag, filterRT)
	} else if g.isSQLite() {
		listCore = fmt.Sprintf(`        var args []interface{}

        var whereClauses []string
        if search != "" {
            searchableCols := []string{%s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, col+" LIKE ?")
                args = append(args, "%%"+search+"%%")
            }
        }

        whereSQL := ""
        if len(whereClauses) > 0 {
            whereSQL = " WHERE " + strings.Join(whereClauses, " OR ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %s + sqlutil.Ident(sort) + " " + order
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %s, COUNT(*) OVER() AS _total FROM %s" + whereSQL + orderSQL + " LIMIT ? OFFSET ?"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        fullArgs = append(fullArgs, perPage, offset)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()

`, searchableColsLiteral, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	} else if g.isMSSQL() {
		listCore = fmt.Sprintf(`        var args []interface{}
        args = append(args, perPage, offset)
        argIdx := 3

        var whereClauses []string
        if search != "" {
            searchableCols := []string{%s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, fmt.Sprintf("%%s LIKE $%%d", col, argIdx))
                args = append(args, "%%"+search+"%%")
                argIdx++
            }
        }

        whereSQL := ""
        if len(whereClauses) > 0 {
            whereSQL = " WHERE " + strings.Join(whereClauses, " OR ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %s + sqlutil.Ident(sort) + " " + order
        }
        if orderSQL == "" {
            orderSQL = " ORDER BY (SELECT NULL)"
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %s, COUNT(*) OVER() AS _total FROM %s" + whereSQL + orderSQL + " OFFSET $2 ROWS FETCH NEXT $1 ROWS ONLY"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()

`, searchableColsLiteral, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	} else {
		listCore = fmt.Sprintf(`        var args []interface{}
        args = append(args, perPage, offset)
        argIdx := 3

        var whereClauses []string
        if search != "" {
            searchableCols := []string{%s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, fmt.Sprintf("%%s ILIKE $%%d", col, argIdx))
                args = append(args, "%%"+search+"%%")
                argIdx++
            }
        }

        whereSQL := ""
        if len(whereClauses) > 0 {
            whereSQL = " WHERE " + strings.Join(whereClauses, " OR ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %s + sqlutil.Ident(sort) + " " + order
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %s, COUNT(*) OVER() AS _total FROM %s" + whereSQL + orderSQL + " LIMIT $1 OFFSET $2"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()

`, searchableColsLiteral, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	}
	sb.WriteString(listCore)

	sb.WriteString(`        var items []map[string]interface{}
        for rows.Next() {
            ` + scanFields(scanNames, true) + `
            items = append(items, item)
        }
        if !totalSet {
            total = int64(page * perPage)
        }

        totalPages := int(math.Ceil(float64(total) / float64(perPage)))

`)
	if hasFilter {
		sb.WriteString(g.filterViewmodelCode(r.List.Filter, compiled))
		sb.WriteString("\n")
	}
	filterVdFields := ""
	if hasFilter {
		filterVdFields = `            FilterQS:  filterQS,
            Filter:    filterData,
            Applied:   filterApplied,
`
	}
	sb.WriteString(`        vd := &viewmodels.ListData{
            Items:      items,
            Page:       page,
            PerPage:    perPage,
            Total:      int(total),
            TotalPages: totalPages,
            Search:     search,
            Sort:       sort,
            Order:      order,
            Columns: []viewmodels.ColumnDef{
                ` + colDefsStr(r.List.Columns) + computedDefsStr(r.List.Computed) + `,
            },
            Resource:  ` + fmt.Sprintf("%q", r.Name) + `,
            PanelPath: ` + fmt.Sprintf("%q", g.Config.Panel.Path) + `,
            CSRFToken: auth.CSRFToken(r, w),
` + filterVdFields + `        }

        ` + fmt.Sprintf("layoutviews.Base(%q, %q, viewmodels.DefaultTheme(), auth.UserName(r), auth.CSRFToken(r, w), views.%sList(vd)).Render(r.Context(), w)", resourceTitle(r), g.Config.Panel.Path, r.Name) + `
    }
}
`)

	return os.WriteFile(filepath.Join(dir, "list.go"), []byte(sb.String()), 0644)
}

// validSortsMapStr renders the Go source for a map of sortable column names
// (all mapping to true), used to whitelist sort parameters in the generated
// list handler.
// Params: cols (names of the sortable columns).
// Returns: the Go literal string for the map.
func validSortsMapStr(cols []string) string {
	var parts []string
	for _, c := range cols {
		parts = append(parts, fmt.Sprintf("%q: true", c))
	}
	return strings.Join(parts, ", ")
}

// scanFields generates the Go source that scans a database row into a
// map[string]interface{}: it declares one interface{} variable per column,
// appends their addresses to a scan slice and populates the item map. When
// withTotal is true the emitted code additionally scans the trailing
// COUNT(*) OVER() AS _total column into the outer `total` variable.
// Params: cols (the column names to scan), withTotal (also scan _total).
// Returns: the multi-line Go source string to inline in the generated handler.
func scanFields(cols []string, withTotal bool) string {
	var scans []string
	scans = append(scans, `        item := make(map[string]interface{})`)
	scans = append(scans, `        var scanArgs []interface{}`)
	for _, c := range cols {
		scans = append(scans, fmt.Sprintf(`        var val_%s interface{}`, c))
		scans = append(scans, fmt.Sprintf(`        scanArgs = append(scanArgs, &val_%s)`, c))
	}
	if withTotal {
		scans = append(scans, `        var totalVal interface{}`)
		scans = append(scans, `        scanArgs = append(scanArgs, &totalVal)`)
	}
	scans = append(scans, `        if err := rows.Scan(scanArgs...); err != nil {`)
	scans = append(scans, `            httperr.Internal(w, err)`)
	scans = append(scans, `            return`)
	scans = append(scans, `        }`)
	if withTotal {
		scans = append(scans, `        switch tv := totalVal.(type) {`)
		scans = append(scans, `        case int64:`)
		scans = append(scans, `            total = tv`)
		scans = append(scans, `        case float64:`)
		scans = append(scans, `            total = int64(tv)`)
		scans = append(scans, `        }`)
		scans = append(scans, `        totalSet = true`)
	}
	for _, c := range cols {
		scans = append(scans, fmt.Sprintf(`        item[%q] = val_%s`, c, c))
	}
	return strings.Join(scans, "\n")
}

// quoteList renders a comma-separated list of double-quoted Go string literals
// for the given words, each optionally prefixed (the prefix is used to qualify
// searchable columns with the table alias when the list/card query has FK LEFT
// JOINs).
// Params: words (the strings to quote), prefix (optional column prefix).
// Returns: a comma-separated list of quoted Go literals.
func quoteList(words []string, prefix string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = fmt.Sprintf("%q", prefix+w)
	}
	return strings.Join(q, ", ")
}

// generateCardHandler writes card.go for a resource: a Cards(db) handler that
// paginates and searches the resource exactly like the list handler (LIMIT =
// card Rows * Columns) scanning the card fields. When KanbanField names a
// select field, the fetched rows are grouped into columns keyed by that field's
// option values and rendered as a kanban board; otherwise the rows render as a
// Columns x Rows card grid.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateCardHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	tName := tableName(r)
	card := r.Card
	panelPath := g.Config.Panel.Path

	var fieldNames []string
	var searchCols []string
	for _, f := range card.Fields {
		fieldNames = append(fieldNames, f.Name)
	}
	for _, s := range card.Searchable {
		searchCols = append(searchCols, s)
	}
	scanNames := make([]string, len(fieldNames))
	copy(scanNames, fieldNames)
	for _, c := range card.Computed {
		scanNames = append(scanNames, c.Name)
	}
	computedItems := g.computedSelectItems(card.Computed)
	hasComputed := len(card.Computed) > 0
	selectFrag, fromFrag, colPrefix, _, _ := g.listSelectFrom(r, tName, fieldNames)

	hasFilter := card.Filter != nil
	var compiled *filterexpr.Compiled
	if hasFilter {
		var cerr error
		compiled, cerr = g.filterCompile(card.Filter, colPrefix)
		if cerr != nil {
			return cerr
		}
	}
	// E7: same derived-table switch as the list handler when the filter
	// references a computed card field.
	useDerived := hasComputed && hasFilter && g.filterReferencesComputed(compiled, card.Computed)
	if useDerived {
		innerSelect, innerFrom, _, _, _ := g.listSelectFromParts(r, tName, fieldNames)
		derived := "(SELECT " + innerSelect
		if hasComputed {
			derived += ", " + computedItems
		}
		derived += " FROM " + innerFrom + ") _base"
		var outerCols []string
		for _, n := range scanNames {
			outerCols = append(outerCols, g.quoteIdent(n))
		}
		selectFrag = embedSQL(strings.Join(outerCols, ", "))
		fromFrag = embedSQL(derived)
		colPrefix = ""
		var cerr error
		compiled, cerr = g.filterCompile(card.Filter, "")
		if cerr != nil {
			return cerr
		}
	} else if hasComputed {
		selectFrag += ", " + embedSQL(computedItems)
	}
	var filterRT string
	if hasFilter {
		filterRT = g.filterRuntimeBlock(card.Filter, compiled)
	}
	searchable := g.quoteSQLList(searchCols, colPrefix)
	urlImport := ""
	if hasFilter {
		urlImport = "    \"net/url\"\n"
	}
	fmtImport := ""
	if !g.isSQLite() {
		// Same as the list handler: the emitted search/filter blocks on
		// postgres/mssql always reference fmt.Sprintf.
		fmtImport = "    \"fmt\"\n"
	}

	kanban := card.KanbanField != ""
	perPage, rows, cols := card.Rows*card.Columns, card.Rows, card.Columns

	sortStmt := ""
	if card.DefaultSort != "" {
		sortField := strings.TrimPrefix(card.DefaultSort, "-")
		sortOrder := "asc"
		if card.DefaultSort != sortField {
			sortOrder = "desc"
		}
		sortStmt = fmt.Sprintf(`        if sort == "" {
            sort = %q
            order = %q
        }
`, sortField, sortOrder)
	}

	var queryCore string
	if hasFilter {
		queryCore = g.filterListCore(searchable, colPrefix, selectFrag, fromFrag, filterRT)
	} else if g.isSQLite() {
		queryCore = fmt.Sprintf(`        var args []interface{}

        var whereClauses []string
        if search != "" {
            searchableCols := []string{%s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, col+" LIKE ?")
                args = append(args, "%%"+search+"%%")
            }
        }

        whereSQL := ""
        if len(whereClauses) > 0 {
            whereSQL = " WHERE " + strings.Join(whereClauses, " OR ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %s + sqlutil.Ident(sort) + " " + order
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %s, COUNT(*) OVER() AS _total FROM %s" + whereSQL + orderSQL + " LIMIT ? OFFSET ?"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        fullArgs = append(fullArgs, perPage, offset)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()
`, searchable, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	} else if g.isMSSQL() {
		queryCore = fmt.Sprintf(`        var args []interface{}
        args = append(args, perPage, offset)
        argIdx := 3

        var whereClauses []string
        if search != "" {
            searchableCols := []string{%s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, fmt.Sprintf("%%s LIKE $%%d", col, argIdx))
                args = append(args, "%%"+search+"%%")
                argIdx++
            }
        }

        whereSQL := ""
        if len(whereClauses) > 0 {
            whereSQL = " WHERE " + strings.Join(whereClauses, " OR ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %s + sqlutil.Ident(sort) + " " + order
        }
        if orderSQL == "" {
            orderSQL = " ORDER BY (SELECT NULL)"
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %s, COUNT(*) OVER() AS _total FROM %s" + whereSQL + orderSQL + " OFFSET $2 ROWS FETCH NEXT $1 ROWS ONLY"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()
`, searchable, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	} else {
		queryCore = fmt.Sprintf(`        var args []interface{}
        args = append(args, perPage, offset)
        argIdx := 3

        var whereClauses []string
        if search != "" {
            searchableCols := []string{%s}
            for _, col := range searchableCols {
                whereClauses = append(whereClauses, fmt.Sprintf("%%s ILIKE $%%d", col, argIdx))
                args = append(args, "%%"+search+"%%")
                argIdx++
            }
        }

        whereSQL := ""
        if len(whereClauses) > 0 {
            whereSQL = " WHERE " + strings.Join(whereClauses, " OR ")
        }

        orderSQL := ""
        if sort != "" {
            orderSQL = " ORDER BY " + %s + sqlutil.Ident(sort) + " " + order
        }

        var total int64
        totalSet := false
        dataQuery := "SELECT %s, COUNT(*) OVER() AS _total FROM %s" + whereSQL + orderSQL + " LIMIT $1 OFFSET $2"
        var fullArgs []interface{}
        fullArgs = append(fullArgs, args...)
        rows, err := db.QueryContext(r.Context(), dataQuery, fullArgs...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()
`, searchable, fmt.Sprintf("%q", colPrefix), selectFrag, fromFrag)
	}

	kanbanCode := ""
	kanbanColumnsExpr := "nil"
	if kanban {
		var optKeys []string
		var optLabelMap []string
		for _, f := range card.Fields {
			if f.Name == card.KanbanField {
				for k := range f.Options {
					optKeys = append(optKeys, k)
				}
				sort.Strings(optKeys)
				for _, k := range optKeys {
					optLabelMap = append(optLabelMap, fmt.Sprintf("%q: %q", k, f.Options[k]))
				}
			}
		}
		kanbanCode = fmt.Sprintf(`        var kanbanColumns []viewmodels.CardColumnData
        bucket := map[string]*viewmodels.CardColumnData{}
        var bucketOrder []string
        {
            optLabels := map[string]string{%s}
            for _, k := range []string{%s} {
                bucket[k] = &viewmodels.CardColumnData{Key: k, Label: optLabels[k]}
                bucketOrder = append(bucketOrder, k)
            }
        }
        for _, item := range items {
            key := viewmodels.OptionValue(item[%q])
            if bucket[key] == nil {
                bucket[key] = &viewmodels.CardColumnData{Key: key, Label: key}
                bucketOrder = append(bucketOrder, key)
            }
            bucket[key].Items = append(bucket[key].Items, item)
        }
        for _, k := range bucketOrder {
            kanbanColumns = append(kanbanColumns, *bucket[k])
        }
`, strings.Join(optLabelMap, ", "), quoteList(optKeys, ""), card.KanbanField)
		kanbanColumnsExpr = "kanbanColumns"
	}

	itemsAssignment := `        for rows.Next() {
            ` + scanFields(scanNames, true) + `
            items = append(items, item)
        }
        if !totalSet {
            total = int64(page * perPage)
        }
`

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
%s    "math"
    "net/http"
    "strconv"
    "strings"
%s
    %q
    %q
    auth %q
    httperr %q
    sqlutil %q
    layoutviews %q
)

func Cards(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        page, _ := strconv.Atoi(r.URL.Query().Get("page"))
        if page < 1 {
            page = 1
        }
        perPage := %d
        offset := (page - 1) * perPage

        search := r.URL.Query().Get("search")
        sort := r.URL.Query().Get("sort")
        order := r.URL.Query().Get("order")
        if order == "" {
            order = "asc"
        }

%s
        if order != "asc" && order != "desc" {
            order = "asc"
        }
%s
        var items []map[string]interface{}
%s
%s
        totalPages := int(math.Ceil(float64(total) / float64(perPage)))

%s
        vd := &viewmodels.CardData{
            Items:          items,
            Page:           page,
            PerPage:        perPage,
            Total:          int(total),
            TotalPages:     totalPages,
            Search:         search,
            Sort:           sort,
            Order:          order,
            Fields: []viewmodels.ColumnDef{
%s,
            },
            Columns:       %d,
            Rows:          %d,
            Kanban:        %t,
            KanbanField:   %q,
            KanbanColumns: %s,
            Resource:      %q,
            PanelPath:     %q,
%s        }

        layoutviews.Base(%q, %q, viewmodels.DefaultTheme(), auth.UserName(r), auth.CSRFToken(r, w), views.%sCards(vd)).Render(r.Context(), w)
    }
}
`, pkgName, fmtImport, urlImport,
		g.moduleImport("internal/viewmodels"), g.moduleImport("internal/views/resources/"+pkgName),
		g.moduleImport("internal/panel/auth"), g.moduleImport("internal/panel/httperr"), g.moduleImport("internal/panel/sqlutil"), g.moduleImport("internal/views/layout"),
		perPage,
		sortStmt,
		queryCore,
		itemsAssignment,
		kanbanCode,
		func() string {
			if hasFilter {
				return g.filterViewmodelCode(card.Filter, compiled) + "\n"
			}
			return ""
		}(),
		fieldDefsStr(card.Fields) + computedDefsStr(card.Computed),
		cols, rows, kanban, card.KanbanField,
		kanbanColumnsExpr,
		r.Name, panelPath,
		func() string {
			if hasFilter {
				return "            FilterQS: filterQS,\n            Filter:   filterData,\n            Applied:  filterApplied,\n"
			}
			return ""
		}(),
		resourceTitle(r), panelPath, r.Name)

	return os.WriteFile(filepath.Join(dir, "card.go"), []byte(code), 0644)
}

// computeRowCode renders the package-level compute<Resource>Row helper that a
// detail handler calls to materialize its detail.computed fields (E7). A single
// query selects every computed expression from the resource table by id and
// stores the results in the item map, so the detail templ renders them exactly
// like real fields. Returns "" when the resource declares no computed fields.
func (g *Generator) computeRowCode(r types.Resource) string {
	computed := r.Detail.Computed
	if len(computed) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "func compute%sRow(db *sql.DB, ctx context.Context, item map[string]interface{}, id %s) error {\n", r.Name, g.idGoTypeForResource(r))
	for _, c := range computed {
		fmt.Fprintf(&sb, "    var val_%s interface{}\n", c.Name)
	}
	var vars []string
	for _, c := range computed {
		vars = append(vars, "&val_"+c.Name)
	}
	querySQL := "SELECT " + g.computedSelectItems(computed) +
		" FROM " + g.quoteIdent(tableName(r)) +
		" WHERE " + g.quoteIdent(idColumn(r)) + " = " + g.placeholder(1)
	fmt.Fprintf(&sb, "    err := db.QueryRowContext(ctx, %s, id).Scan(%s)\n", strconv.Quote(querySQL), strings.Join(vars, ", "))
	sb.WriteString("    if err != nil {\n        return err\n    }\n")
	for _, c := range computed {
		fmt.Fprintf(&sb, "    item[%q] = val_%s\n", c.Name, c.Name)
	}
	sb.WriteString("    return nil\n}\n")
	return sb.String()
}

// generateDetailHandler writes detail.go for a resource: a Detail(db) handler
// that parses the :id path parameter, calls the generated data.Get query
// (which returns a column-keyed map) and renders the detail view.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateDetailHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	queryName := r.Detail.Query
	if queryName == "" {
		queryName = "GetByID"
	}
	idCol := idColumn(r)
	parentIDExpr := fmt.Sprintf("fmt.Sprintf(\"%%v\", item[%q])", idCol)
	childLoad, childLines, hasChildren := g.childLinesParts(r, parentIDExpr, "int64(id)")
	fmtImport := ""
	if hasChildren {
		fmtImport = "    \"fmt\"\n"
	}
	contextImport := ""
	computeRow := g.computeRowCode(r)
	computeCall := ""
	if computeRow != "" {
		contextImport = "    \"context\"\n"
		computeCall = fmt.Sprintf(`        if err := compute%sRow(db, r.Context(), item, %s(id)); err != nil {
            httperr.Internal(w, err)
            return
        }

`, r.Name, g.idGoTypeForResource(r))
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "net/http"
    "strconv"
    %s%s
    %q
    %q
    %q
    auth %q
    httperr %q
    layoutviews %q
)

%sfunc Detail(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        idStr := r.PathValue("id")
        id, err := strconv.Atoi(idStr)
        if err != nil {
            http.Error(w, "invalid id", http.StatusBadRequest)
            return
        }

        item, err := data.New(db).%s(r.Context(), %s(id))
        if err != nil {
            httperr.NotFound(w, err)
            return
        }

%s%s
        vd := &viewmodels.DetailData{
            Item: item,
            Fields: []viewmodels.ColumnDef{
                %s,
            },
            Resource:  %q,
            PanelPath: %q,
            CSRFToken: auth.CSRFToken(r, w),
%s        }

        layoutviews.Base(%q, %q, viewmodels.DefaultTheme(), auth.UserName(r), auth.CSRFToken(r, w), views.%sDetail(vd)).Render(r.Context(), w)
    }
}
`, pkgName,
		fmtImport, contextImport,
		g.moduleImport("internal/data"), g.moduleImport("internal/viewmodels"), g.moduleImport("internal/views/resources/"+pkgName),
		g.moduleImport("internal/panel/auth"), g.moduleImport("internal/panel/httperr"), g.moduleImport("internal/views/layout"),
		computeRow,
		queryName,
		g.idGoTypeForResource(r),
		childLoad,
		computeCall,
		fieldDefsFromDetail(r.Detail.Fields)+computedDefsStr(r.Detail.Computed),
		r.Name,
		g.Config.Panel.Path,
		childLines,
		resourceTitle(r),
		g.Config.Panel.Path,
		r.Name)

	return os.WriteFile(filepath.Join(dir, "detail.go"), []byte(code), 0644)
}

// snakeToPascal converts a column name to the PascalCase struct field name
// that sqlc generates. sqlc lowercases the whole identifier, splits only on
// underscores, and maps the "id" segment to "ID" (e.g. "user_role_id" ->
// "UserRoleID", "CeleJmeno" -> "Celejmeno", "ZamestnanecID" -> "Zamestnanecid").
// Params: s (a column name, e.g. from a YAML field definition).
// Returns: the PascalCase variant.
func snakeToPascal(s string) string {
	parts := strings.Split(strings.ToLower(s), "_")
	for i, p := range parts {
		if p == "id" {
			parts[i] = "ID"
		} else if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// generateFormHandlers dispatches to the create, update and delete handler
// generators based on which form sections the resource declares.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error if any generated handler fails.
func (g *Generator) generateFormHandlers(dir string, r types.Resource) error {
	if r.Form.Create != nil {
		if err := g.generateCreateHandler(dir, r); err != nil {
			return err
		}
	}
	if r.ImportCSV {
		if err := g.generateImportHandler(dir, r); err != nil {
			return err
		}
	}
	if r.Form.Update != nil {
		if err := g.generateUpdateHandler(dir, r); err != nil {
			return err
		}
	}
	if r.Form.Delete != nil {
		if err := g.generateDeleteHandler(dir, r); err != nil {
			return err
		}
	}
	return nil
}

// resourceHasPicker reports whether any create or update form field of a
// resource renders as a modal record picker. Used to gate master-detail return
// navigation on the delete handler (a child deleted from a parent's edit screen
// posts ?return= back to it, D14).
func (g *Generator) resourceHasPicker(r types.Resource) bool {
	if r.Form == nil {
		return false
	}
	for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update} {
		if fa == nil {
			continue
		}
		for _, f := range fa.Fields {
			if g.isPickerField(r, f) {
				return true
			}
		}
	}
	return false
}

// generateDeleteHandler writes delete.go: a Delete(db) handler that parses the
// :id path parameter, runs "DELETE FROM {table} WHERE id = $1" via ExecContext
// and redirects back to the resource list.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateDeleteHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	listPath := fmt.Sprintf("%s/%s", g.Config.Panel.Path, pkgName)
	tName := tableName(r)

	hooksImport := ""
	if g.hookBlockEmits(r.Form.Delete.Hooks) {
		hooksImport = fmt.Sprintf("    hooks %q\n", g.moduleImport("internal/hooks"))
	}
	procsImport := ""
	if g.hookUsesProc(r.Form.Delete.Hooks) {
		procsImport = g.procImport()
	}
	authImport := ""
	if g.auditFor(r) != nil || g.hooksUseScript(r.Form.Delete.Hooks) {
		authImport = fmt.Sprintf("    auth %q\n", g.moduleImport("internal/panel/auth"))
	}
	luaImport := ""
	if g.hooksUseScript(r.Form.Delete.Hooks) {
		luaImport = g.luaImport()
	}
	// Return navigation (D14): a child deleted from a parent's lines section
	// carries ?return=/panel/... and redirects back instead of to the list.
	returnRet := ""
	stringsImport := ""
	if g.resourceHasPicker(r) {
		returnRet = `        if ret := r.URL.Query().Get("return"); ret != "" && strings.HasPrefix(ret, "/") {
            http.Redirect(w, r, ret, http.StatusFound)
            return
        }
`
		stringsImport = "    \"strings\"\n"
	}

	hasHooks := g.hookBlockEmits(r.Form.Delete.Hooks)
	middle := ""
	if hasHooks {
		middle += fmt.Sprintf(`        scope := hooks.Scope{
            Table:  %q,
            Action: "delete",
            ID:     int64(id),
        }
`, tName)
		middle += g.hookCallsStr(r.Form.Delete.Hooks.Before, "scope", "        ") + "\n"
	}
	if g.auditFor(r) != nil {
		middle += auditTxBeginStr("        ")
		middle += fmt.Sprintf(`        _, err = tx.ExecContext(r.Context(), "DELETE FROM %s WHERE %s = $1", int64(id))
        if err != nil {
            httperr.Internal(w, err)
            return
        }
`, embedSQL(g.quoteIdent(tName)), embedSQL(g.quoteIdent(idColumn(r))))
		middle += g.auditInsertStr(r, "delete", "strconv.FormatInt(int64(id), 10)", `""`, "        ") + "\n"
		middle += auditTxCommitStr("        ")
	} else {
		middle += fmt.Sprintf(`        _, err = db.ExecContext(r.Context(), "DELETE FROM %s WHERE %s = $1", int64(id))
        if err != nil {
            httperr.Internal(w, err)
            return
        }
`, embedSQL(g.quoteIdent(tName)), embedSQL(g.quoteIdent(idColumn(r))))
	}
	if hasHooks {
		middle += g.hookCallsStr(r.Form.Delete.Hooks.After, "scope", "        ") + "\n"
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "net/http"
    "strconv"
    %s%s%s%s
    httperr %q
%s)

func Delete(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        idStr := r.PathValue("id")
        id, err := strconv.Atoi(idStr)
        if err != nil {
            http.Error(w, "invalid id", http.StatusBadRequest)
            return
        }

%s
%s        http.Redirect(w, r, %q, http.StatusFound)
    }
}
`, pkgName, stringsImport, authImport, hooksImport, luaImport, g.moduleImport("internal/panel/httperr"), procsImport, middle, returnRet, listPath)

	return os.WriteFile(filepath.Join(dir, "delete.go"), []byte(code), 0644)
}

// generateCSVHandler writes export.go: an ExportCSV(db) handler that selects
// the exported columns ordered by the first column and streams them as an
// attachment CSV file using encoding/csv. When list.export is set it exports
// only that subset (with Label headers); otherwise all list columns with
// raw column-name headers (historical behavior).
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateCSVHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	tName := tableName(r)

	exportCols := r.List.Columns
	useLabels := false
	if len(r.List.Export) > 0 {
		var filtered []types.Column
		for _, want := range r.List.Export {
			for _, c := range r.List.Columns {
				if c.Name == want {
					filtered = append(filtered, c)
					break
				}
			}
		}
		exportCols = filtered
		useLabels = true
	}

	var colNames []string
	for _, c := range exportCols {
		colNames = append(colNames, c.Name)
	}
	selectFrag, fromFrag, _, _, _ := g.listSelectFrom(r, tName, colNames)

	headerCode := `        out := make([]string, len(cols))
        for i, c := range cols {
            out[i] = csvSafe(c)
        }
        wr.Write(out)
`
	if useLabels {
		var labels []string
		for _, c := range exportCols {
			lbl := c.Label
			if lbl == "" {
				lbl = c.Name
			}
			labels = append(labels, fmt.Sprintf("csvSafe(%q)", lbl))
		}
		headerCode = fmt.Sprintf("        wr.Write([]string{%s})\n", strings.Join(labels, ", "))
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "encoding/csv"
    "net/http"
    httperr %q
)

func ExportCSV(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        query := "SELECT %s FROM %s ORDER BY 1"
        rows, err := db.QueryContext(r.Context(), query)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer rows.Close()

        w.Header().Set("Content-Type", "text/csv")
        w.Header().Set("Content-Disposition", "attachment; filename=%s_export.csv")
        wr := csv.NewWriter(w)
        defer wr.Flush()

        cols, _ := rows.Columns()
%s
        vals := make([]string, len(cols))
        ptrs := make([]interface{}, len(cols))
        for i := range vals {
            ptrs[i] = &vals[i]
        }
        for rows.Next() {
            rows.Scan(ptrs...)
            for i := range vals {
                vals[i] = csvSafe(vals[i])
            }
            wr.Write(vals)
        }
    }
}

// csvSafe neutralizes spreadsheet formula injection by prefixing a single
// quote when a value begins with a formula trigger character (=, +, -, @, tab
// or carriage return), which Excel/Sheets would otherwise evaluate.
func csvSafe(s string) string {
    if len(s) > 0 {
        switch s[0] {
        case '=', '+', '-', '@', '\t', '\r':
            return "'" + s
        }
    }
    return s
}
`, pkgName, g.moduleImport("internal/panel/httperr"), selectFrag, fromFrag, tName, headerCode)

	return os.WriteFile(filepath.Join(dir, "export.go"), []byte(code), 0644)
}

// actionExecSQL returns the SQL text an action executes at request time: the
// raw Query when set, otherwise the driver-appropriate stored procedure call
// when Proc is set (and the driver is not sqlite). Empty when the action has
// neither (a sqlite proc-only action, or an action that only runs hooks).
// Params: a (the action definition).
// Returns: the SQL to execute, or "".
func (g *Generator) actionExecSQL(a types.Action) string {
	if a.Query != "" {
		return a.Query
	}
	if a.Proc != "" && !g.isSQLite() {
		return g.procSQL(a.Proc)
	}
	return ""
}

// actionScriptBlock renders the Go source that runs a script: action body via
// luascript.Run against the given executor (db, or tx when the action is
// audited). An abort() call redirects to the resource list with the message as
// a ?flash= query param; any real runtime error aborts with a 500.
// Params: executor ("db" or "tx"), listPath (the resource list path literal),
// a (the action definition), tName (the table name), indent (leading whitespace).
// Returns: the Go source lines.
func actionScriptBlock(executor, listPath string, a types.Action, tName, indent string) string {
	return fmt.Sprintf(`%sif err := luascript.Run(r.Context(), %s, luascript.Scope{
%s    ID:     int64(id),
%s    Table:  %q,
%s    Action: %q,
%s    User:   auth.UserName(r),
%s    Role:   auth.RoleName(r),
%s}, %q); err != nil {
%s    if luascript.IsAbort(err) {
%s        http.Redirect(w, r, %q+"?flash="+url.QueryEscape(err.Error()), http.StatusFound)
%s        return
%s    }
%s    httperr.Internal(w, err)
%s    return
%s}`, indent, executor, indent, indent, tName, indent, a.Name, indent, indent, indent, a.Script, indent, indent, listPath, indent, indent, indent, indent, indent)
}

// bulkScriptBlock renders the Go source that runs a bulk script: action body
// via luascript.Run against db for one selected id (no outer transaction,
// mirroring proc bulk actions). An abort() call redirects to the resource list
// with the message as a ?flash= query param; any real runtime error aborts
// with a 500.
// Params: listPath (the resource list path literal), a (the action definition),
// tName (the table name), indent (leading whitespace).
// Returns: the Go source lines.
func bulkScriptBlock(listPath string, a types.Action, tName, indent string) string {
	return fmt.Sprintf(`%sif err := luascript.Run(r.Context(), db, luascript.Scope{
%s    ID:     id,
%s    Table:  %q,
%s    Action: %q,
%s    User:   auth.UserName(r),
%s    Role:   auth.RoleName(r),
%s}, %q); err != nil {
%s    if luascript.IsAbort(err) {
%s        http.Redirect(w, r, %q+"?flash="+url.QueryEscape(err.Error()), http.StatusFound)
%s        return
%s    }
%s    httperr.Internal(w, err)
%s    return
%s}`, indent, indent, indent, tName, indent, a.Name, indent, indent, indent, a.Script, indent, indent, listPath, indent, indent, indent, indent, indent)
}

// generateActionHandler writes actions.go: an Action(db) handler that parses
// the :id and :action path parameters and switches over the configured action
// names, executing each action's SQL via ExecContext, then redirecting to the
// resource list. Unknown action names return 404.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateActionHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	listPath := fmt.Sprintf("%s/%s", g.Config.Panel.Path, pkgName)
	tName := tableName(r)

	hasHooks := false
	hasProcs := false
	hasLua := false
	hasScriptAction := false
	auditCfg := g.auditFor(r)
	auditAny := false
	var dispatch []string
	for _, a := range r.Actions {
		useHooks := g.hookBlockEmits(a.Hooks)
		if useHooks {
			hasHooks = true
		}
		if g.hookUsesProc(a.Hooks) {
			hasProcs = true
		}
		if g.hooksUseScript(a.Hooks) {
			hasLua = true
		}
		exec := g.actionExecSQL(a)
		procExec := g.actionProcExec(a, "int64(id)")
		if procExec != "" {
			hasProcs = true
		}
		useScript := a.Script != ""
		if useScript {
			hasLua = true
			hasScriptAction = true
		}
		auditAction := auditCfg != nil && (exec != "" || useScript) && procExec == ""
		if auditAction {
			auditAny = true
		}
		var body []string
		if useHooks {
			body = append(body, fmt.Sprintf(`            scope := hooks.Scope{
                Table:  %q,
                Action: %q,
                ID:     int64(id),
            }`, tName, a.Name))
			if before := g.hookCallsStr(a.Hooks.Before, "scope", "            "); before != "" {
				body = append(body, before)
			}
		}
		if auditAction {
			body = append(body, auditTxBeginStr("            "))
			if useScript {
				body = append(body, actionScriptBlock("tx", listPath, a, tName, "            "))
			} else {
				body = append(body, fmt.Sprintf(`            _, err = tx.ExecContext(r.Context(), %q, int64(id))
            if err != nil {
                httperr.Internal(w, err)
                return
            }`, exec))
			}
			body = append(body, g.auditInsertStr(r, a.Name, "strconv.FormatInt(int64(id), 10)", `""`, "            "))
			body = append(body, auditTxCommitStr("            "))
		} else if procExec != "" {
			body = append(body, procExec)
		} else if exec != "" {
			body = append(body, fmt.Sprintf(`            _, err := db.ExecContext(r.Context(), %q, int64(id))
            if err != nil {
                httperr.Internal(w, err)
                return
            }`, exec))
		} else if useScript {
			body = append(body, actionScriptBlock("db", listPath, a, tName, "            "))
		}
		if useHooks {
			if after := g.hookCallsStr(a.Hooks.After, "scope", "            "); after != "" {
				body = append(body, after)
			}
		}
		dispatch = append(dispatch, fmt.Sprintf(`    case %q:
        {
%s
        }
`, a.Name, strings.Join(body, "\n")))
	}

	hooksImport := ""
	if hasHooks {
		hooksImport = fmt.Sprintf("    hooks %q\n", g.moduleImport("internal/hooks"))
	}
	procsImport := ""
	if hasProcs {
		procsImport = g.procImport()
	}
	authImport := ""
	if auditAny || hasLua {
		authImport = fmt.Sprintf("    auth %q\n", g.moduleImport("internal/panel/auth"))
	}
	luaImport := ""
	if hasLua {
		luaImport = g.luaImport()
	}
	urlImport := ""
	if hasScriptAction {
		urlImport = "    \"net/url\"\n"
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "net/http"
    "strconv"
    httperr %q
%s%s%s%s%s)

func Action(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        idStr := r.PathValue("id")
        actionName := r.PathValue("action")
        id, err := strconv.Atoi(idStr)
        if err != nil {
            http.Error(w, "invalid id", http.StatusBadRequest)
            return
        }

        switch actionName {
%s    default:
            http.Error(w, "unknown action", http.StatusNotFound)
            return
        }

        http.Redirect(w, r, %q, http.StatusFound)
    }
}
`, pkgName, g.moduleImport("internal/panel/httperr"), authImport, hooksImport, luaImport, urlImport, procsImport, strings.Join(dispatch, "\n"), listPath)

	return os.WriteFile(filepath.Join(dir, "actions.go"), []byte(code), 0644)
}

// generateBulkHandler writes bulk.go: a Bulk(db) handler that parses the
// :action path parameter and the repeated "ids" form values, switching over the
// configured bulk actions and executing each action's SQL once per selected id,
// then redirecting to the resource list. Unknown action names return 404.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateBulkHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	listPath := fmt.Sprintf("%s/%s", g.Config.Panel.Path, pkgName)

	hasExec := false
	hasProcs := false
	hasLua := false
	hasScriptAction := false
	for _, a := range r.Actions {
		if !a.Bulk {
			continue
		}
		if g.actionExecSQL(a) != "" {
			hasExec = true
		}
		if g.actionProcExec(a, "id") != "" {
			hasProcs = true
		}
		if a.Script != "" {
			hasLua = true
			hasScriptAction = true
		}
	}

	var dispatch []string
	tName := tableName(r)
	for _, a := range r.Actions {
		if !a.Bulk {
			continue
		}
		executor := "db"
		if hasExec {
			executor = "tx"
		}
		if procExec := g.actionProcExec(a, "id"); procExec != "" {
			dispatch = append(dispatch, fmt.Sprintf(`    case %q:
        for _, id := range ids {
%s
        }
`, a.Name, procExec))
		} else if exec := g.actionExecSQL(a); exec != "" {
			dispatch = append(dispatch, fmt.Sprintf(`    case %q:
        for _, id := range ids {
            _, err := %s.ExecContext(r.Context(), %q, id)
            if err != nil {
                httperr.Internal(w, err)
                return
            }
        }
`, a.Name, executor, exec))
		} else if a.Script != "" {
			dispatch = append(dispatch, fmt.Sprintf(`    case %q:
        for _, id := range ids {
%s
        }
`, a.Name, bulkScriptBlock(listPath, a, tName, "            ")))
		} else {
			dispatch = append(dispatch, fmt.Sprintf(`    case %q:
        for _, id := range ids {
            _ = id
        }
`, a.Name))
		}
	}

	txCode := ""
	if hasExec {
		txCode = fmt.Sprintf(`
        tx, err := db.BeginTx(r.Context(), nil)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
        defer tx.Rollback()
`)
	}
	commitCode := ""
	if hasExec {
		commitCode = `
        if err := tx.Commit(); err != nil {
            httperr.Internal(w, err)
            return
        }
`
	}
	procsImport := ""
	if hasProcs {
		procsImport = g.procImport()
	}
	authImport := ""
	if hasScriptAction {
		authImport = fmt.Sprintf("    auth %q\n", g.moduleImport("internal/panel/auth"))
	}
	luaImport := ""
	if hasLua {
		luaImport = g.luaImport()
	}
	urlImport := ""
	if hasScriptAction {
		urlImport = "    \"net/url\"\n"
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "net/http"
    "strconv"
    httperr %q
%s%s%s%s)

func Bulk(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        actionName := r.PathValue("action")

        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }

        ids := make([]int64, 0)
        for _, raw := range r.Form["ids"] {
            if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
                ids = append(ids, id)
            }
        }

%s
        switch actionName {
%s    default:
            http.Error(w, "unknown action", http.StatusNotFound)
            return
        }
%s
        http.Redirect(w, r, %q, http.StatusFound)
    }
}
`, pkgName, g.moduleImport("internal/panel/httperr"), procsImport, luaImport, urlImport, authImport, txCode, strings.Join(dispatch, "\n"), commitCode, listPath)

	return os.WriteFile(filepath.Join(dir, "bulk.go"), []byte(code), 0644)
}

// generateCreateHandler writes create.go: a Create(db) handler serving the
// create form on GET and inserting a new row on POST. It builds the INSERT
// statement from the create form fields, bcrypt-hashes password fields, saves
// uploaded files (file/image fields) via the saveUploadedFile helper and loads
// dynamic select options declared with options_query.
// Params: dir (resource package directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateCreateHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	listPath := fmt.Sprintf("%s/%s", g.Config.Panel.Path, pkgName)
	tName := tableName(r)

	paramFields := r.Form.Create.Fields
	create := r.Form.Create

	var optLoadCode string
	var optVars, copyVars map[string]string
	optVars, copyVars, optLoadCode = g.buildOptionsLoader(r, paramFields)

	var colNames []string
	var valExprs []string
	var preHashLines []string
	hasPassword := false
	hasFile := false
	for _, f := range paramFields {
		colNames = append(colNames, f.Name)
		if f.Type == "password" {
			hasPassword = true
			valExprs = append(valExprs, fmt.Sprintf("string(%sBytes)", f.Name))
			preHashLines = append(preHashLines, fmt.Sprintf(`        %sBytes, _ := bcrypt.GenerateFromPassword([]byte(r.FormValue(%q)), bcrypt.DefaultCost)`, f.Name, f.Name))
		} else if f.Type == "file" || f.Type == "image" {
			hasFile = true
			valExprs = append(valExprs, fmt.Sprintf("saveUploadedFile(r, %q)", f.Name))
		} else if f.Type == "boolean" {
			valExprs = append(valExprs, fmt.Sprintf("r.FormValue(%q) == \"true\"", f.Name))
		} else {
			valExprs = append(valExprs, fmt.Sprintf("r.FormValue(%q)", f.Name))
		}
	}

	// buildCreateParams is the shared INSERT-value constructor used by both the
	// Create POST and the CSV import handler: it maps a field-name -> value map
	// onto the create column order, bcrypt-hashes password fields and coerces
	// booleans. File/image fields are rejected (uploads are request-bound, the
	// CSV path cannot carry them), so the create POST only uses it when the
	// resource has no such fields (legacy inline construction otherwise).
	usesBuildParams := !hasFile
	needBuildParams := !hasFile || r.ImportCSV

	var buildParamsCode string
	var formMapCode string
	if needBuildParams {
		if hasFile {
			buildParamsCode = `func buildCreateParams(m map[string]string) ([]interface{}, error) {
    return nil, fmt.Errorf("file/image uploads are not supported in CSV import")
}
`
		} else {
			var bpPre []string
			var bpVals []string
			for _, f := range paramFields {
				if f.Type == "password" {
					bpPre = append(bpPre, fmt.Sprintf(`    %sBytes, err := bcrypt.GenerateFromPassword([]byte(m[%q]), bcrypt.DefaultCost)
    if err != nil {
        return nil, err
    }`, f.Name, f.Name))
					bpVals = append(bpVals, fmt.Sprintf("string(%sBytes)", f.Name))
				} else if f.Type == "boolean" {
					bpVals = append(bpVals, fmt.Sprintf("m[%q] == \"true\"", f.Name))
				} else {
					bpVals = append(bpVals, fmt.Sprintf("m[%q]", f.Name))
				}
			}
			buildParamsCode = fmt.Sprintf(`func buildCreateParams(m map[string]string) ([]interface{}, error) {
%s
    vals := []interface{}{%s}
    return vals, nil
}
`, strings.Join(bpPre, "\n"), strings.Join(bpVals, ", "))
		}
		if usesBuildParams {
			var entries []string
			for _, f := range paramFields {
				entries = append(entries, fmt.Sprintf("%q: r.FormValue(%q)", f.Name, f.Name))
			}
			formMapCode = fmt.Sprintf(`        vals, err := buildCreateParams(map[string]string{%s})
        if err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }
`, strings.Join(entries, ", "))
		}
	}

	bcryptImport := ""
	if hasPassword {
		bcryptImport = `    "golang.org/x/crypto/bcrypt"
`
	}

	hooksImport := ""
	if g.hookBlockEmits(create.Hooks) {
		hooksImport = fmt.Sprintf("    hooks %q\n", g.moduleImport("internal/hooks"))
	}
	procsImport := ""
	if g.hookUsesProc(create.Hooks) {
		procsImport = g.procImport()
	}
	luaImport := ""
	if g.hooksUseScript(create.Hooks) {
		luaImport = g.luaImport()
	}

	fileImport := ""
	uploadHelper := ""
	if hasFile {
		fileImport = `    "io"
    "os"
    "path/filepath"
    "time"
`
		uploadHelper = `
func saveUploadedFile(r *http.Request, fieldName string) string {
    file, header, err := r.FormFile(fieldName)
    if err != nil {
        return ""
    }
    defer file.Close()

    ext := strings.ToLower(filepath.Ext(header.Filename))
    if !safeUploadExt(ext) {
        return ""
    }

    head := make([]byte, 512)
    n, _ := io.ReadFull(file, head)
    detected := http.DetectContentType(head[:n])
    if n > 0 && (detected == "text/html" || detected == "image/svg+xml") {
        return ""
    }
    if _, err := file.Seek(0, io.SeekStart); err != nil {
        return ""
    }

    dir := "static/uploads/" + fieldName
    os.MkdirAll(dir, 0755)
    outPath := dir + "/" + fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
    out, err := os.Create(outPath)
    if err != nil {
        return ""
    }
    defer out.Close()
    io.Copy(out, file)
    return "/" + outPath
}

var safeUploadExts = map[string]bool{
    ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
    ".pdf": true, ".txt": true, ".csv": true, ".zip": true,
    ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
}

func safeUploadExt(ext string) bool {
    return safeUploadExts[ext]
}
`
	}

	preHashCode := strings.Join(preHashLines, "\n")

	formParseCode := "r.ParseForm()"
	if hasFile {
		formParseCode = "r.ParseMultipartForm(32 << 20)"
	}

	valsLine := ""
	if !usesBuildParams {
		valsLine = fmt.Sprintf("        vals := []interface{}{%s}\n", strings.Join(valExprs, ", "))
	}
	postCode := fmt.Sprintf(`        cols := []string{%s}
%s        placeholders := make([]string, len(cols))
        for i := range cols {
            placeholders[i] = fmt.Sprintf("$%%d", i+1)
        }
        query := fmt.Sprintf("INSERT INTO %%s (%%s) VALUES (%%s)", %q, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
`, g.colsLiteralQuoted(colNames), valsLine, g.quoteIdent(tName))
	hasHooks := g.hookBlockEmits(create.Hooks)
	audit := g.auditFor(r)
	jsonImport := ""
	if audit != nil && audit.IncludeValues {
		jsonImport = "    \"encoding/json\"\n"
	}
	if hasHooks || audit != nil {
		if hasHooks {
			postCode += fmt.Sprintf(`        scope := hooks.Scope{
            Table:  %q,
            Action: "create",
            Values: map[string]interface{}{
%s        },
        }
`, tName, scopeValuesStr(colNames))
			postCode += g.hookCallsStr(create.Hooks.Before, "scope", "        ") + "\n"
			if g.hooksUseScript(create.Hooks) {
				postCode += valuesWriteBack(colNames, "        ") + "\n"
			}
		}
		if audit != nil {
			postCode += auditTxBeginStr("        ")
			postCode += fmt.Sprintf(`        var newID int64
        if err := tx.QueryRowContext(r.Context(), query+%q, vals...).Scan(&newID); err != nil {
            httperr.Internal(w, err)
            return
        }
`, g.returningClause(r))
			if hasHooks {
				postCode += "        scope.ID = newID\n"
			}
			valuesArg := `""`
			if audit.IncludeValues {
				postCode += auditValuesStr(colNames, "        ") + "\n"
				valuesArg = "string(valuesJSON)"
			}
			postCode += g.auditInsertStr(r, "create", `fmt.Sprintf("%d", newID)`, valuesArg, "        ") + "\n"
			postCode += auditTxCommitStr("        ")
		} else {
			postCode += fmt.Sprintf(`        var newID int64
        if err := db.QueryRowContext(r.Context(), query+%q, vals...).Scan(&newID); err != nil {
            httperr.Internal(w, err)
            return
        }
        scope.ID = newID
`, g.returningClause(r))
		}
		if hasHooks {
			postCode += g.hookCallsStr(create.Hooks.After, "scope", "        ") + "\n"
		}
	} else {
		execAssign := ":="
		if usesBuildParams {
			execAssign = "="
		}
		postCode += fmt.Sprintf(`        _, err %s db.ExecContext(r.Context(), query, vals...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
`, execAssign)
	}

	preInsertCode := preHashCode
	if usesBuildParams {
		preInsertCode = formMapCode
	}

	// Parent-context FK seeding (D14): when the create form is opened as a
	// child from a header (e.g. "Add Line" carrying ?order_id=<header id>), the
	// matching picker field is pre-seeded and locked. The runtime `locked` map
	// drives the ColumnDef.Locked flags emitted by formFieldDefsWithOpts.
	seedPickers := g.seedPickersOf(r, paramFields)
	seedCode := ""
	itemExpr := "make(map[string]interface{})"
	if len(seedPickers) > 0 {
		var seedLines []string
		for f := range seedPickers {
			seedLines = append(seedLines, fmt.Sprintf(`        if v := r.URL.Query().Get(%q); v != "" {
            item[%q] = v
            locked[%q] = true
        }
`, f, f, f))
		}
		seedCode = "        item := make(map[string]interface{})\n        locked := map[string]bool{}\n" + strings.Join(seedLines, "")
		itemExpr = "item"
	}

	// Return navigation (D14): a child POST may carry ?return=/panel/... to
	// redirect back to the header's edit screen instead of the child list.
	// Only same-site (path-only) returns are honored to avoid an open redirect.
	redirectRet := ""
	returnField := ""
	if len(r.Children) > 0 || len(seedPickers) > 0 {
		redirectRet = `        ret := r.FormValue("_return")
        if ret == "" {
            ret = r.URL.Query().Get("return")
        }
        if ret != "" && strings.HasPrefix(ret, "/") {
            http.Redirect(w, r, ret, http.StatusFound)
            return
        }
`
		returnField = fmt.Sprintf(`Return:        r.URL.Query().Get("return"),
`)
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "fmt"
    "net/http"
    "strings"
    %s%s%s%s%s%s
    %q
    %q
    auth %q
    httperr %q
    layoutviews %q
)

func Create(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet {
            %s
%s
            vd := &viewmodels.FormData{
                Item: %s,
                Fields: []viewmodels.ColumnDef{
                    %s,
                },
                Action:    %q,
                Method:    "POST",
                Resource:  %q,
                PanelPath: %q,
                IsCreate:  true,
                CSRFToken: auth.CSRFToken(r, w),
%s            }
            layoutviews.Base(%q, %q, viewmodels.DefaultTheme(), auth.UserName(r), auth.CSRFToken(r, w), views.%sForm(vd)).Render(r.Context(), w)
            return
        }

        if err := %s; err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }

%s
%s
%s        http.Redirect(w, r, %q, http.StatusFound)
    }
}
%s`, pkgName,
		jsonImport,
		bcryptImport,
		fileImport,
		hooksImport,
		procsImport,
		luaImport,
		g.moduleImport("internal/viewmodels"), g.moduleImport("internal/views/resources/"+pkgName),
		g.moduleImport("internal/panel/auth"), g.moduleImport("internal/panel/httperr"), g.moduleImport("internal/views/layout"),
		optLoadCode,
		seedCode,
		itemExpr,
		formFieldDefsWithOpts(paramFields, optVars, copyVars, seedPickers),
		fmt.Sprintf("%s/%s/new", g.Config.Panel.Path, pkgName),
		r.Name,
		g.Config.Panel.Path,
		returnField,
		resourceTitle(r),
		g.Config.Panel.Path,
		r.Name,
		formParseCode,
		preInsertCode,
		postCode,
		redirectRet,
		listPath,
		uploadHelper+buildParamsCode)

	return os.WriteFile(filepath.Join(dir, "create.go"), []byte(code), 0644)
}

// buildOptionsLoader generates code to load dynamic select options from DB.
// Returns: fieldName→goVarName map for the value→label options, a second
// fieldName→varName map for the per-field `copies:` auto-fill data (key → map
// of {target form field: string value}), and the code to load them at request
// time by running "SELECT value, label [, copy columns…] FROM (rawSQL) AS _opt".
// The SQL per field resolves in order: options_sql (explicit), FK-derived SQL
// from the schema block (a field matching a foreign key with
// options_value/options_label), else nothing. Fields sharing the same resolved
// SQL AND the same copies mapping reuse a single options variable (batched
// loader). For FK-derived SQL the copies' source columns are appended to the
// loader SELECT; a custom options_sql is wrapped verbatim and must expose them
// itself. Each copy value is formatted for its target field type via
// viewmodels.PickCopyValue.
func (g *Generator) buildOptionsLoader(r types.Resource, fields []types.Field) (optVars, copyVars map[string]string, loadCode string) {
	optVars = make(map[string]string)
	copyVars = make(map[string]string)
	var loads []string
	loaded := map[string]string{}
	for _, f := range fields {
		sql := g.optionSQL(r, f)
		if sql == "" {
			continue
		}
		targets := sortedCopyTargets(f)
		inner := sql
		if len(targets) > 0 && f.OptionsSQL == "" && f.OptionsValue != "" && f.OptionsLabel != "" {
			if st := g.schemaTable(tableName(r)); st != nil {
				for _, fk := range st.ForeignKeys {
					if strings.EqualFold(fk.Column, f.Name) {
						var srcs []string
						for _, t := range targets {
							srcs = append(srcs, f.Copies[t])
						}
						var qsrcs []string
						for _, s := range srcs {
							qsrcs = append(qsrcs, g.quoteIdent(s))
						}
						inner = fmt.Sprintf("SELECT %s, %s, %s FROM %s", g.quoteIdent(f.OptionsValue), g.quoteIdent(f.OptionsLabel), strings.Join(qsrcs, ", "), g.quoteIdent(fk.ForeignTable))
						break
					}
				}
			}
		}
		dedupKey := inner
		if len(targets) > 0 {
			dedupKey += "|" + strings.Join(targets, ",") + "|" + strings.Join(scanSources(f, targets), ",")
		}
		if varName, ok := loaded[dedupKey]; ok {
			optVars[f.Name] = varName
			if len(targets) > 0 {
				copyVars[f.Name] = copyVarsOf(varName)
			}
			continue
		}
		varName := f.Name + "Opts"
		optVars[f.Name] = varName
		loaded[dedupKey] = varName
		optField := f.OptionsValue
		if optField == "" {
			optField = "id"
		}
		optLabel := f.OptionsLabel
		if optLabel == "" {
			optLabel = "name"
		}

		if len(targets) > 0 {
			copyVar := f.Name + "Copies"
			copyVars[f.Name] = copyVar
			srcs := scanSources(f, targets)
			var qsrcs []string
			for _, s := range srcs {
				qsrcs = append(qsrcs, embedSQL(g.quoteIdent(s)))
			}
			var scanVars []string
			var ampVars []string
			var copyExprs []string
			for i, t := range targets {
				vname := fmt.Sprintf("cpy%d", i)
				scanVars = append(scanVars, vname)
				ampVars = append(ampVars, "&"+vname)
				ft := fieldTypeOf(fields, t)
				copyExprs = append(copyExprs, fmt.Sprintf("%q: viewmodels.PickCopyValue(%s, %q)", t, vname, ft))
			}
			loads = append(loads, fmt.Sprintf(`        %s := map[string]string{}
        %s := map[string]map[string]string{}
        { optRows, err := db.QueryContext(r.Context(), "SELECT %s, %s, %s FROM ("+%q+") AS _opt"); if err == nil { defer optRows.Close(); for optRows.Next() { var val, label, %s interface{}; if err := optRows.Scan(&val, &label, %s); err == nil { k := fmt.Sprintf("%%v", val); %s[k] = fmt.Sprintf("%%v", label); %s[k] = map[string]string{%s} } } } }`,
				varName, copyVar, embedSQL(g.quoteIdent(optField)), embedSQL(g.quoteIdent(optLabel)), strings.Join(qsrcs, ", "), inner, strings.Join(scanVars, ", "), strings.Join(ampVars, ", "), varName, copyVar, strings.Join(copyExprs, ", ")))
			continue
		}
		loads = append(loads, fmt.Sprintf(`        %s := map[string]string{}
        { optRows, err := db.QueryContext(r.Context(), "SELECT %s, %s FROM ("+%q+") AS _opt"); if err == nil { defer optRows.Close(); for optRows.Next() { var val, label interface{}; if err := optRows.Scan(&val, &label); err == nil { %s[fmt.Sprintf("%%v", val)] = fmt.Sprintf("%%v", label) } } } }`, varName, embedSQL(g.quoteIdent(optField)), embedSQL(g.quoteIdent(optLabel)), inner, varName))
	}
	return optVars, copyVars, strings.Join(loads, "\n")
}

// sortedCopyTargets returns the sorted target form-field names from a field's
// copies mapping (deterministic SQL/scan order in the emitted loader).
func sortedCopyTargets(f types.Field) []string {
	if len(f.Copies) == 0 {
		return nil
	}
	out := make([]string, 0, len(f.Copies))
	for t := range f.Copies {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// scanSources returns the distinct source columns of a copies mapping in the
// order of the given sorted targets (deduped, order preserved).
func scanSources(f types.Field, targets []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range targets {
		s := f.Copies[t]
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// fieldTypeOf returns the field type of a named form field, or "string" when
// the field is unknown (a cross-form target falls back to plain string copy).
func fieldTypeOf(fields []types.Field, name string) string {
	for _, f := range fields {
		if f.Name == name {
			if f.Type == "" {
				return "string"
			}
			return f.Type
		}
	}
	return "string"
}

// copyVarsOf derives the copy-data variable name from an options var name.
func copyVarsOf(optVar string) string {
	return optVar[:len(optVar)-len("Opts")] + "Copies"
}

// childRel is a resolved master-detail relation from a header resource's
// `children:` block: the section heading, the child's URL/package segment,
// its row-key column, the FK column scoping the lines, the display columns and
// the child table name, plus a per-child load var name for the emitted handler.
type childRel struct {
	heading    string
	resName    string
	childLower string
	childTable string
	childID    string
	fkCol      string
	cols       []types.Column
	varName    string
}

// childRels resolves the `children:` block of a header resource into concrete
// relations (D14). The child FK column is taken from the entry or derived by
// scanning the `schema:` block for a reverse FK (a foreign key on the child's
// table pointing at the parent's table/pk). Display columns default to the
// child resource's list columns, filtered to columns present in the child's
// schema table (label-join columns like {fk}_label are dropped — they cannot
// be selected from the child table directly). Entries whose child resource or
// schema table cannot be resolved are skipped (the parser already flags them).
func (g *Generator) childRels(r types.Resource) []childRel {
	var out []childRel
	byName := map[string]types.Resource{}
	for _, cr := range g.Config.Resources {
		byName[cr.Name] = cr
	}
	parentTable := tableName(r)
	parentPK := idColumn(r)
	if st := g.schemaTable(parentTable); st != nil && st.PK != "" {
		parentPK = st.PK
	}
	for i, ch := range r.Children {
		cr, ok := byName[ch.Resource]
		if !ok {
			continue
		}
		childTable := tableName(cr)
		fkCol := ch.Column
		if fkCol == "" {
			cst := g.schemaTable(childTable)
			if cst == nil {
				continue
			}
			for _, fk := range cst.ForeignKeys {
				if strings.EqualFold(fk.ForeignTable, parentTable) && strings.EqualFold(fk.ForeignColumn, parentPK) {
					fkCol = fk.Column
					break
				}
			}
			if fkCol == "" {
				continue
			}
		}
		cols := ch.Columns
		if len(cols) == 0 && cr.List != nil {
			cols = g.childTableColumns(cr, cr.List.Columns)
		}
		heading := ch.Name
		if heading == "" {
			heading = cr.Label
			if heading == "" {
				heading = cr.Name
			}
		}
		out = append(out, childRel{
			heading:    heading,
			resName:    cr.Name,
			childLower: strings.ToLower(cr.Name),
			childTable: childTable,
			childID:    idColumn(cr),
			fkCol:      fkCol,
			cols:       cols,
			varName:    fmt.Sprintf("childLines%d", i+1),
		})
	}
	return out
}

// childTableColumns filters a column list down to the real columns of a
// resource's schema table (case-insensitive). Columns not present in the
// schema (e.g. FK label columns like role_id_label) are dropped so a raw
// SELECT on the child table stays valid. The row-key column is always kept.
func (r *Generator) childTableColumns(cr types.Resource, cols []types.Column) []types.Column {
	st := r.schemaTable(tableName(cr))
	if st == nil {
		return cols
	}
	key := idColumn(cr)
	var out []types.Column
	seen := map[string]bool{key: true}
	out = append(out, types.Column{Name: key})
	for _, c := range cols {
		if strings.EqualFold(c.Name, key) || seen[c.Name] {
			continue
		}
		if !schemaColPresent(st, c.Name) {
			continue
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out
}

// schemaColPresent reports whether a schema table has a column with the given
// name (exact or case-insensitive).
func schemaColPresent(st *types.SchemaTable, name string) bool {
	for _, c := range st.Columns {
		if c.Name == name || strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

// childLinesParts emits the Go source that loads a header resource's child
// lines and the `Lines: []viewmodels.ChildLinesData{…}` literal for a detail or
// update handler. parentIDExpr is the Go expression for the parent key string,
// and parentIDArg the Go expression bound to the child FK placeholder (both
// derive from the same value). Returns the loadCode (childLinesN :=
// loadChildLines(…)), the Lines literal block, and whether the loadChildLines
// helper must be emitted into the handler file.
func (g *Generator) childLinesParts(r types.Resource, parentIDExpr, parentIDArg string) (loadCode, linesCode string, has bool) {
	rels := g.childRels(r)
	if len(rels) == 0 {
		return "", "", false
	}
	parentLower := strings.ToLower(r.Name)
	panelPath := g.Config.Panel.Path
	var loads []string
	var parts []string
	for _, rel := range rels {
		// SELECT child key + display columns (deduped), driver-aware FK param.
		var sel []string
		seen := map[string]bool{}
		add := func(c string) {
			if !seen[c] {
				seen[c] = true
				sel = append(sel, g.quoteIdent(c))
			}
		}
		add(rel.childID)
		for _, c := range rel.cols {
			add(c.Name)
		}
		query := fmt.Sprintf("SELECT %s FROM %s t WHERE t.%s = %s", strings.Join(sel, ", "), g.quoteIdent(rel.childTable), g.quoteIdent(rel.fkCol), g.placeholder(1))
		loads = append(loads, fmt.Sprintf(`        %s := loadChildLines(r.Context(), db, %q, %s)`, rel.varName, query, parentIDArg))

		var colDefs []string
		for _, c := range rel.cols {
			label := c.Label
			if label == "" {
				label = c.Name
			}
			ft := c.Type
			if ft == "" {
				ft = "string"
			}
			colDefs = append(colDefs, fmt.Sprintf("{Name: %q, Label: %q, FieldType: %q}", c.Name, label, ft))
		}
		parts = append(parts, fmt.Sprintf(`        {
            Heading:       %q,
            Resource:      %q,
            ResourceLower: %q,
            IDColumn:      %q,
            FKColumn:      %q,
            ParentID:      %s,
            PanelPath:     %q,
            CSRFToken:     auth.CSRFToken(r, w),
            ReturnURL:     fmt.Sprintf("%%s/%%s/%%s/edit", %q, %q, %s),
            Fields: []viewmodels.ColumnDef{
                %s,
            },
            Rows:   %s,
            Count:  len(%s),
        },`, rel.heading, rel.resName, rel.childLower, rel.childID, rel.fkCol, parentIDExpr, g.Config.Panel.Path,
			panelPath, parentLower, parentIDExpr, strings.Join(colDefs, ",\n"), rel.varName, rel.varName))
	}
	return strings.Join(loads, "\n"), "Lines: []viewmodels.ChildLinesData{\n" + strings.Join(parts, "\n") + "\n        },", true
}

// optionSQL resolves the option-list SQL for a form field: options_sql wins,
// then FK-derived SQL from the schema block (when the field names a foreign
// key of the resource's table and carries options_value/options_label).
// Returns "" when neither applies.
func (g *Generator) optionSQL(r types.Resource, f types.Field) string {
	if f.OptionsSQL != "" {
		return strings.TrimRight(strings.TrimSpace(f.OptionsSQL), ";")
	}
	if f.OptionsValue == "" || f.OptionsLabel == "" {
		return ""
	}
	st := g.schemaTable(tableName(r))
	if st == nil {
		return ""
	}
	for _, fk := range st.ForeignKeys {
		if !strings.EqualFold(fk.Column, f.Name) {
			continue
		}
		return fmt.Sprintf("SELECT %s, %s FROM %s", g.quoteIdent(f.OptionsValue), g.quoteIdent(f.OptionsLabel), g.quoteIdent(fk.ForeignTable))
	}
	return ""
}

// formFieldDefsWithOpts renders the []viewmodels.ColumnDef literal for form
// fields, wiring each field's Options to the runtime-loaded variable when the
// field has an option loader, or to the inline static options map otherwise.
// copyVars adds the runtime-loaded `copies:` auto-fill data
// (CopyData: <var>). seedPickers lists picker fields that may be locked from a
// parent context (D14: the handler declares a `locked map[string]bool` and the
// emitted def references it, so the same form renders the picker locked only
// when opened with ?<fk>=<parent id>).
// Params: fields (form field definitions), optVars/copyVars (field name to
// generated option/copy variables from buildOptionsLoader), seedPickers (names
// of picker fields participating in parent-context FK lock).
// Returns: the comma-joined Go source string for the field defs.
func formFieldDefsWithOpts(fields []types.Field, optVars, copyVars map[string]string, seedPickers map[string]bool) string {
	var defs []string
	for _, f := range fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		opts := "nil"
		if varName, ok := optVars[f.Name]; ok {
			opts = varName
		} else if len(f.Options) > 0 {
			opts = "map[string]string{"
			for k, v := range f.Options {
				opts += fmt.Sprintf("%q: %q, ", k, v)
			}
			opts += "}"
		}
		picker := false
		if _, ok := optVars[f.Name]; ok && (f.Type == "select" || f.Type == "relation") {
			picker = true
		}
		extra := ""
		if copyName, ok := copyVars[f.Name]; ok {
			extra += fmt.Sprintf(", CopyData: %s", copyName)
		}
		if picker && seedPickers[f.Name] {
			extra += fmt.Sprintf(", Locked: locked[%q]", f.Name)
		}
		defs = append(defs, fmt.Sprintf("{Name: %q, Label: %q, FieldType: %q, Picker: %t, Options: %s%s}", f.Name, label, f.Type, picker, opts, extra))
	}
	return strings.Join(defs, ",\n")
}

// seedPickersOf returns the picker field names of a field list whose value can
// be seeded and locked from the URL (a parent context in master-detail, D14).
func (g *Generator) seedPickersOf(r types.Resource, fields []types.Field) map[string]bool {
	out := map[string]bool{}
	for _, f := range fields {
		if g.isPickerField(r, f) {
			out[f.Name] = true
		}
	}
	return out
}

// colsLiteral renders a list of double-quoted Go string literals for the given
// column names, used to build the cols []string literals in the generated
// create/update handlers.
// Params: cols (the column names).
// Returns: a comma-separated list of quoted Go literals.
func colsLiteral(cols []string) string {
	var q []string
	for _, c := range cols {
		q = append(q, fmt.Sprintf("%q", c))
	}
	return strings.Join(q, ", ")
}

// generateUpdateHandler writes update.go: an Update(db) handler that renders
// the populated edit form on GET (via the SQLC populate query) and performs a
// raw SQL UPDATE on POST. It builds the SET clauses from the update form
// fields, saves uploaded files, appends the record id as the last placeholder
// and loads dynamic select options. Returns an error on write failure.
// Params: dir (resource package directory), r (the resource definition).
func (g *Generator) generateUpdateHandler(dir string, r types.Resource) error {
	pkgName := strings.ToLower(r.Name)
	listPath := fmt.Sprintf("%s/%s", g.Config.Panel.Path, pkgName)
	tName := tableName(r)
	populateQuery := r.Form.Update.PopulateQuery
	if populateQuery == "" {
		populateQuery = "GetByID"
	}

	paramFields := r.Form.Update.Fields
	update := r.Form.Update

	var optLoadCode string
	var optVars, copyVars map[string]string
	optVars, copyVars, optLoadCode = g.buildOptionsLoader(r, paramFields)

	var colNames []string
	var valExprs []string
	hasFile := false
	for _, f := range paramFields {
		colNames = append(colNames, f.Name)
		if f.Type == "file" || f.Type == "image" {
			hasFile = true
			valExprs = append(valExprs, fmt.Sprintf("saveUploadedFile(r, %q)", f.Name))
		} else if f.Type == "boolean" {
			valExprs = append(valExprs, fmt.Sprintf("r.FormValue(%q) == \"true\"", f.Name))
		} else {
			valExprs = append(valExprs, fmt.Sprintf("r.FormValue(%q)", f.Name))
		}
	}

	hooksImport := ""
	if g.hookBlockEmits(update.Hooks) {
		hooksImport = fmt.Sprintf("    hooks %q\n", g.moduleImport("internal/hooks"))
	}
	procsImport := ""
	if g.hookUsesProc(update.Hooks) {
		procsImport = g.procImport()
	}
	luaImport := ""
	if g.hooksUseScript(update.Hooks) {
		luaImport = g.luaImport()
	}

	fileImport := ""
	uploadHelper := ""
	if hasFile {
		fileImport = `    "io"
    "os"
    "path/filepath"
    "time"
`
		uploadHelper = `
func saveUploadedFile(r *http.Request, fieldName string) string {
    file, header, err := r.FormFile(fieldName)
    if err != nil {
        return ""
    }
    defer file.Close()

    ext := strings.ToLower(filepath.Ext(header.Filename))
    if !safeUploadExt(ext) {
        return ""
    }

    head := make([]byte, 512)
    n, _ := io.ReadFull(file, head)
    detected := http.DetectContentType(head[:n])
    if n > 0 && (detected == "text/html" || detected == "image/svg+xml") {
        return ""
    }
    if _, err := file.Seek(0, io.SeekStart); err != nil {
        return ""
    }

    dir := "static/uploads/" + fieldName
    os.MkdirAll(dir, 0755)
    outPath := dir + "/" + fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
    out, err := os.Create(outPath)
    if err != nil {
        return ""
    }
    defer out.Close()
    io.Copy(out, file)
    return "/" + outPath
}

var safeUploadExts = map[string]bool{
    ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
    ".pdf": true, ".txt": true, ".csv": true, ".zip": true,
    ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
}

func safeUploadExt(ext string) bool {
    return safeUploadExts[ext]
}
`
	}

	formParseCode := "r.ParseForm()"
	if hasFile {
		formParseCode = "r.ParseMultipartForm(32 << 20)"
	}

	postCode := fmt.Sprintf(`        cols := []string{%s}
        vals := []interface{}{%s}
        setClauses := make([]string, len(cols))
        for i, col := range cols {
            setClauses[i] = fmt.Sprintf("%%s = $%%d", col, i+1)
        }
        vals = append(vals, int64(id))
        query := fmt.Sprintf("UPDATE %%s SET %%s WHERE %%s = $%%d", %q, strings.Join(setClauses, ", "), %q, len(cols)+1)
`, g.colsLiteralQuoted(colNames), strings.Join(valExprs, ", "), g.quoteIdent(tName), g.quoteIdent(idColumn(r)))
	hasHooks := g.hookBlockEmits(update.Hooks)
	audit := g.auditFor(r)
	jsonImport := ""
	if audit != nil && audit.IncludeValues {
		jsonImport = "    \"encoding/json\"\n"
	}
	if hasHooks {
		postCode += fmt.Sprintf(`        scope := hooks.Scope{
            Table:  %q,
            Action: "update",
            ID:     int64(id),
            Values: map[string]interface{}{
%s        },
        }
`, tName, scopeValuesStr(colNames))
		postCode += g.hookCallsStr(update.Hooks.Before, "scope", "        ") + "\n"
		if g.hooksUseScript(update.Hooks) {
			postCode += valuesWriteBack(colNames, "        ") + "\n"
		}
	}
	if audit != nil {
		postCode += auditTxBeginStr("        ")
		postCode += `        _, err = tx.ExecContext(r.Context(), query, vals...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
`
		valuesArg := `""`
		if audit.IncludeValues {
			postCode += auditValuesStr(colNames, "        ") + "\n"
			valuesArg = "string(valuesJSON)"
		}
		postCode += g.auditInsertStr(r, "update", "strconv.FormatInt(int64(id), 10)", valuesArg, "        ") + "\n"
		postCode += auditTxCommitStr("        ")
	} else {
		postCode += `        _, err = db.ExecContext(r.Context(), query, vals...)
        if err != nil {
            httperr.Internal(w, err)
            return
        }
`
	}
	if hasHooks {
		postCode += g.hookCallsStr(update.Hooks.After, "scope", "        ") + "\n"
	}

	// D14: parent-context FK seeding/locking, child-lines loading and return
	// navigation for the edit form. seedPickers lists picker fields that can be
	// locked from a parent context (?fk=<parent id>); the runtime `locked` map
	// drives the ColumnDef.Locked flags.
	seedPickers := g.seedPickersOf(r, paramFields)
	gateCode := ""
	if len(seedPickers) > 0 {
		var seedLines []string
		for f := range seedPickers {
			seedLines = append(seedLines, fmt.Sprintf(`        if v := r.URL.Query().Get(%q); v != "" {
            item[%q] = v
            locked[%q] = true
        }
`, f, f, f))
		}
		gateCode = "        locked := map[string]bool{}\n" + strings.Join(seedLines, "")
	}
	childLoad, childLines, hasChildren := g.childLinesParts(r, `fmt.Sprintf("%d", id)`, "int64(id)")
	gateCode += childLoad
	linesField := childLines
	derivable := len(seedPickers) > 0 || hasChildren
	returnField := ""
	if derivable {
		returnField = fmt.Sprintf(`Return:        r.URL.Query().Get("return"),
`)
	}
	redirectRet := ""
	if derivable {
		redirectRet = `        ret := r.FormValue("_return")
        if ret == "" {
            ret = r.URL.Query().Get("return")
        }
        if ret != "" && strings.HasPrefix(ret, "/") {
            http.Redirect(w, r, ret, http.StatusFound)
            return
        }
`
	}

	code := fmt.Sprintf(`package %s

import (
    "database/sql"
    "fmt"
    "net/http"
    "strconv"
    "strings"
%s%s%s%s%s
    %q
    %q
    %q
    auth %q
    httperr %q
    layoutviews %q
)

func Update(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        idStr := r.PathValue("id")
        id, err := strconv.Atoi(idStr)
        if err != nil {
            http.Error(w, "invalid id", http.StatusBadRequest)
            return
        }

        if r.Method == http.MethodGet {
            item, err := data.New(db).%s(r.Context(), %s(id))
            if err != nil {
                httperr.NotFound(w, err)
                return
            }

            %s
%s
            vd := &viewmodels.FormData{
                Item: item,
%s                Fields: []viewmodels.ColumnDef{
                    %s,
                },
                Action:    %s,
                Method:    "POST",
                Resource:  %q,
                PanelPath: %q,
                IsCreate:  false,
                CSRFToken: auth.CSRFToken(r, w),
%s            }
            layoutviews.Base(%q, %q, viewmodels.DefaultTheme(), auth.UserName(r), auth.CSRFToken(r, w), views.%sForm(vd)).Render(r.Context(), w)
            return
        }

        if err := %s; err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }

%s
%s        http.Redirect(w, r, %q, http.StatusFound)
    }
}
%s`, pkgName,
		jsonImport,
		fileImport,
		hooksImport,
		procsImport,
		luaImport,
		g.moduleImport("internal/data"), g.moduleImport("internal/viewmodels"), g.moduleImport("internal/views/resources/"+pkgName),
		g.moduleImport("internal/panel/auth"), g.moduleImport("internal/panel/httperr"), g.moduleImport("internal/views/layout"),
		populateQuery,
		g.idGoTypeForResource(r),
		optLoadCode,
		gateCode,
		linesField,
		formFieldDefsWithOpts(paramFields, optVars, copyVars, seedPickers),
		fmt.Sprintf("fmt.Sprintf(\"%%s/%%s/%%d\", %q, %q, id)", g.Config.Panel.Path, pkgName),
		r.Name,
		g.Config.Panel.Path,
		returnField,
		resourceTitle(r),
		g.Config.Panel.Path,
		r.Name,
		formParseCode,
		postCode,
		redirectRet,
		listPath,
		uploadHelper)

	return os.WriteFile(filepath.Join(dir, "update.go"), []byte(code), 0644)
}
