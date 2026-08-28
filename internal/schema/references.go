// references.go
//
// Walks a *types.Config and collects every query name referenced from YAML
// (list/detail/form/delete/action/options_query/widget queries) plus the table
// and column names each resource references. The sync tool compares this set
// against what is actually defined in sql/queries and sql/migrations.
package schema

import (
	"strings"

	"github.com/MichalHerstus/yaga/internal/filterexpr"
	"github.com/MichalHerstus/yaga/internal/types"
)

// QueryRef is a single query name referenced from the YAML config.
type QueryRef struct {
	Name   string
	Origin string // human-readable location, e.g. "User > list.query"
	Inline bool   // true when the reference is inline SQL, not a SQLC query name
}

// ColumnRef pins a column reference to its exact location in the config so the
// editor's Validate screen can jump straight to the offending row.
type ColumnRef struct {
	Column  string // the referenced column/field name
	Section string // list.columns / card.fields / detail.fields / form.create.fields / ...
	Index   int    // index of the column/field within that section
}

// References is the full set of YAML-side references extracted from a config.
type References struct {
	Queries    []QueryRef
	Tables     map[string]string      // resource name -> table name (lowercased)
	Columns    map[string][]string    // resource name -> column/field names referenced
	ColumnRefs map[string][]ColumnRef // resource name -> column references with location
}

// CollectReferences walks cfg and returns all query/table/column references.
func CollectReferences(cfg *types.Config) *References {
	refs := &References{
		Tables:     map[string]string{},
		Columns:    map[string][]string{},
		ColumnRefs: map[string][]ColumnRef{},
	}
	for _, r := range cfg.Resources {
		name := r.Name
		refs.Tables[name] = TableNameFor(r)
		if r.List != nil {
			refs.addQuery(r.List.Query, name+".list.query")
			refs.addQuery(r.List.CountQuery, name+".list.count_query")
			for i, c := range r.List.Columns {
				refs.addColumnRef(name, "list.columns", i, c.Name)
			}
			if r.List.DefaultSort != "" {
				refs.addColumnRef(name, "list.default_sort", 0, sortColumn(r.List.DefaultSort))
			}
			if r.List.Filter != nil && r.List.Filter.Where != "" {
				if expr, err := filterexpr.Parse(r.List.Filter.Where); err == nil {
					for _, col := range expr.Columns() {
						refs.addColumnRef(name, "list.filter", 0, col)
					}
				}
			}
		}
		if r.Card != nil {
			for i, f := range r.Card.Fields {
				refs.addFieldRefs(name, "card.fields", i, f, name+".card.fields")
			}
			for i, col := range r.Card.Searchable {
				refs.addColumnRef(name, "card.searchable", i, col)
			}
			if r.Card.KanbanField != "" {
				refs.addColumnRef(name, "card.kanban_field", 0, r.Card.KanbanField)
			}
			if r.Card.DefaultSort != "" {
				refs.addColumnRef(name, "card.default_sort", 0, sortColumn(r.Card.DefaultSort))
			}
			if r.Card.Filter != nil && r.Card.Filter.Where != "" {
				if expr, err := filterexpr.Parse(r.Card.Filter.Where); err == nil {
					for _, col := range expr.Columns() {
						refs.addColumnRef(name, "card.filter", 0, col)
					}
				}
			}
		}
		if r.Detail != nil {
			refs.addQuery(r.Detail.Query, name+".detail.query")
			for i, f := range r.Detail.Fields {
				refs.addFieldRefs(name, "detail.fields", i, f, name+".detail.fields")
			}
		}
		if r.Form != nil {
			for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update, r.Form.Delete} {
				if fa == nil {
					continue
				}
				section := "form"
				switch {
				case fa == r.Form.Create:
					section = "form.create"
				case fa == r.Form.Update:
					section = "form.update"
				case fa == r.Form.Delete:
					section = "form.delete"
				}
				refs.addQuery(fa.Query, name+"."+section+".query")
				refs.addQuery(fa.PopulateQuery, name+"."+section+".populate_query")
				for i, f := range fa.Fields {
					refs.addFieldRefs(name, section+".fields", i, f, name+"."+section+".fields")
				}
			}
		}
		for _, a := range r.Actions {
			refs.addQuery(a.Query, name+".actions."+a.Name)
		}
	}
	for _, p := range cfg.Pages {
		collectWidgetQueries(refs, p.Widgets, "page "+p.Name)
	}
	return refs
}

// sortColumn strips a leading "-" from a default_sort value (a "-" prefix means
// descending order; the referenced column is the rest).
func sortColumn(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "-")
}

func (refs *References) addQuery(name, origin string) {
	if name == "" {
		return
	}
	for _, q := range refs.Queries {
		if q.Name == name && q.Origin == origin {
			return
		}
	}
	refs.Queries = append(refs.Queries, QueryRef{Name: name, Origin: origin, Inline: isInlineSQL(name)})
}

// isInlineSQL reports whether a YAML query value is literal SQL rather than a
// SQLC query name. Query names are single identifiers without whitespace;
// inline SQL always contains spaces (SELECT/UPDATE/INSERT/DELETE/WITH …).
func isInlineSQL(s string) bool {
	return strings.ContainsAny(s, " \t\n")
}

func (refs *References) addColumn(resource, col string) {
	if col == "" {
		return
	}
	for _, c := range refs.Columns[resource] {
		if c == col {
			return
		}
	}
	refs.Columns[resource] = append(refs.Columns[resource], col)
}

// addColumnRef records a column reference together with its exact location
// (section + index) and keeps the deduplicated Columns summary in sync.
func (refs *References) addColumnRef(resource, section string, index int, col string) {
	if col == "" {
		return
	}
	refs.addColumn(resource, col)
	for _, c := range refs.ColumnRefs[resource] {
		if c.Column == col && c.Section == section && c.Index == index {
			return
		}
	}
	refs.ColumnRefs[resource] = append(refs.ColumnRefs[resource], ColumnRef{Column: col, Section: section, Index: index})
}

func (refs *References) addFieldRefs(resource, section string, index int, f types.Field, origin string) {
	refs.addColumnRef(resource, section, index, f.Name)
	if f.OptionsQuery != "" {
		refs.addQuery(f.OptionsQuery, origin+"."+f.Name)
	}
}

// collectWidgetQueries recurses into widget trees collecting query references.
func collectWidgetQueries(refs *References, widgets []types.Widget, origin string) {
	for _, w := range widgets {
		refs.addQuery(w.Query, origin+".widgets."+w.Label)
		if w.Chart != nil {
			refs.addQuery(w.Chart.Query, origin+".widgets."+w.Label+".chart")
		}
		if len(w.Widgets) > 0 {
			collectWidgetQueries(refs, w.Widgets, origin+".widgets."+w.Label)
		}
	}
}

// TableNameFor returns the table a resource maps to: the explicit table: field
// when set, otherwise the lowercased pluralised resource name (e.g. "User" ->
// "users"). Matching in the sync tool is case-insensitive and also tries a
// snake_case variant for multi-word names.
func TableNameFor(r types.Resource) string {
	if r.Table != "" {
		return r.Table
	}
	return strings.ToLower(Pluralize(r.Name))
}

// FindTableByName looks up a table in a slice by name (case-insensitive).
func FindTableByName(tables []Table, name string) *Table {
	for i := range tables {
		if strings.EqualFold(tables[i].Name, name) {
			return &tables[i]
		}
	}
	return nil
}

// Driver returns the driver of the first connection, defaulting to postgres.
func Driver(cfg *types.Config) string {
	for _, c := range cfg.Connections {
		if c.Driver != "" {
			return c.Driver
		}
	}
	return "postgres"
}

// HasColumn reports whether a schema-block table carries a real column with
// the given name (exact or case-insensitive), or when name ends with
// "_label", whether the table has a foreign key whose Column matches the
// base part (exact or case-insensitive).  This mirrors the generator's
// labelJoins resolution so that FK-label virtual columns like "pn_label"
// are not flagged as missing during validation.
func HasColumn(st *types.SchemaTable, name string) bool {
	for _, c := range st.Columns {
		if c.Name == name || strings.EqualFold(c.Name, name) {
			return true
		}
	}
	if strings.HasSuffix(name, "_label") {
		base := strings.TrimSuffix(name, "_label")
		for _, fk := range st.ForeignKeys {
			if fk.Column == base || strings.EqualFold(fk.Column, base) {
				return true
			}
		}
	}
	return false
}
