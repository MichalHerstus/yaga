package main

import (
	"database/sql"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

// fkSchemaTables returns the introspected tables for a schema where
// sklad_zasoby.pn references sklad_zbozi.pn (a non-PK column) and sklad_zbozi
// is itself a user-visible resource. This mirrors the real-world case that
// exposed the FK join and options generation bugs.
func fkSchemaTables() []TableInfo {
	return []TableInfo{
		{
			Name: "sklad_zasoby",
			Columns: []ColumnInfo{
				{Name: "id", DBType: "integer", IsPrimaryKey: true},
				{Name: "pn", DBType: "character varying"},
				{Name: "mnozstvi", DBType: "numeric"},
				{Name: "created_at", DBType: "timestamp without time zone"},
			},
			ForeignKeys: []ForeignKeyInfo{
				{Column: "pn", ForeignTable: "sklad_zbozi", ForeignColumn: "pn"},
			},
		},
		{
			Name: "sklad_zbozi",
			Columns: []ColumnInfo{
				{Name: "id", DBType: "integer", IsPrimaryKey: true},
				{Name: "pn", DBType: "character varying"},
				{Name: "pn_nazev", DBType: "character varying"},
			},
		},
	}
}

// TestConvertSchemaFKLabel ensures the captured schema block records each FK's
// foreign table + label column (used to derive option SQL at generation time).
func TestConvertSchemaFKLabel(t *testing.T) {
	s := convertSchema(fkSchemaTables(), "postgres")

	if len(s.Tables) != 2 {
		t.Fatalf("expected 2 schema tables, got %d", len(s.Tables))
	}
	zasoby := s.Tables[0]
	if zasoby.Name != "sklad_zasoby" || zasoby.PK != "id" {
		t.Fatalf("sklad_zasoby wrong: %+v", zasoby)
	}
	if len(zasoby.ForeignKeys) != 1 {
		t.Fatalf("expected 1 FK, got %d", len(zasoby.ForeignKeys))
	}
	fk := zasoby.ForeignKeys[0]
	if fk.Column != "pn" || fk.ForeignTable != "sklad_zbozi" || fk.ForeignColumn != "pn" {
		t.Fatalf("FK wrong: %+v", fk)
	}
	// sklad_zbozi has no "name"/"title"/"label" column; the label falls back to
	// its first non-PK string column (pn).
	if fk.Label != "pn" {
		t.Fatalf("FK label should fall back to pn, got %q", fk.Label)
	}
}

// TestConvertSchemaColumnTypes ensures yaga field types come from the DB types
// via mapDBTypeToFieldType.
func TestConvertSchemaColumnTypes(t *testing.T) {
	s := convertSchema(fkSchemaTables(), "postgres")
	zasoby := s.Tables[0]

	got := map[string]string{}
	for _, c := range zasoby.Columns {
		got[c.Name] = c.Type
	}
	if got["mnozstvi"] != "float" {
		t.Fatalf("numeric must map to float, got %q", got["mnozstvi"])
	}
	if got["created_at"] != "datetime" {
		t.Fatalf("timestamp must map to datetime, got %q", got["created_at"])
	}
}

// TestGenerateYAMLHasSchemaBlock ensures the introspected config carries the
// `schema:` block (the sole schema source) and no longer a `sqlc:` block.
func TestGenerateYAMLHasSchemaBlock(t *testing.T) {
	out := generateYAML(fkSchemaTables(), "postgres", "postgres://x/x")

	if !strings.Contains(out, "schema:") {
		t.Fatalf("yaga.yaml must contain a schema: block:\n%s", out)
	}
	if !strings.Contains(out, "  tables:") {
		t.Fatalf("schema block must list tables:\n%s", out)
	}
	if !strings.Contains(out, "  - name: sklad_zasoby") {
		t.Fatalf("schema block must contain sklad_zasoby:\n%s", out)
	}
	if strings.Contains(out, "sqlc:") {
		t.Fatal("yaga.yaml must not contain a sqlc: block")
	}
	if strings.Contains(out, "queries_dir") {
		t.Fatal("yaga.yaml must not reference queries_dir")
	}
}

// TestGenerateYAMLSchemaParses ensures the emitted schema block round-trips
// through the yaml parser used by the generator (Field.OptionsSQL tags parse,
// schema table/FK structure intact).
func TestGenerateYAMLSchemaParses(t *testing.T) {
	out := generateYAML(fkSchemaTables(), "postgres", "postgres://x/x")

	var doc struct {
		Schema struct {
			Tables []struct {
				Name        string `yaml:"name"`
				PK          string `yaml:"pk"`
				ForeignKeys []struct {
					Column        string `yaml:"column"`
					ForeignTable  string `yaml:"foreign_table"`
					ForeignColumn string `yaml:"foreign_column"`
					Label         string `yaml:"label"`
				} `yaml:"foreign_keys"`
			} `yaml:"tables"`
		} `yaml:"schema"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("schema block does not parse: %v\n%s", err, out)
	}
	if len(doc.Schema.Tables) != 2 {
		t.Fatalf("expected 2 schema tables, got %d", len(doc.Schema.Tables))
	}
	if len(doc.Schema.Tables[0].ForeignKeys) != 1 {
		t.Fatalf("expected 1 FK in first table, got %d", len(doc.Schema.Tables[0].ForeignKeys))
	}
	fk := doc.Schema.Tables[0].ForeignKeys[0]
	if fk.Column != "pn" || fk.ForeignTable != "sklad_zbozi" || fk.ForeignColumn != "pn" || fk.Label != "pn" {
		t.Fatalf("FK structure wrong after round-trip: %+v", fk)
	}
}

// TestWriteResourceYAMLRelationField ensures the relation field carries
// options_value/options_label (option SQL is derived from the schema block)
// and no longer an options_query reference.
func TestWriteResourceYAMLRelationField(t *testing.T) {
	var b strings.Builder
	tables := fkSchemaTables()
	writeResourceYAML(&b, tables[0], tables, "postgres")
	out := b.String()

	if strings.Contains(out, "options_query:") {
		t.Fatalf("relation field must not reference options_query, got:\n%s", out)
	}
	if !strings.Contains(out, "options_value: pn") {
		t.Fatalf("options_value must be the referenced foreign column pn:\n%s", out)
	}
	if !strings.Contains(out, "options_label: pn") {
		t.Fatalf("options_label must be the FK target's label column:\n%s", out)
	}
}

// TestWriteResourceYAMLChildren ensures a table with children who FK back to
// its primary key emits a `children:` block (D14) with the reverse-FK column,
// and that FK columns pointing at a NON-key column are ignored.
func TestWriteResourceYAMLChildren(t *testing.T) {
	var b strings.Builder
	tables := []TableInfo{
		{
			Name: "orders",
			Columns: []ColumnInfo{
				{Name: "id", DBType: "integer", IsPrimaryKey: true},
				{Name: "customer_name", DBType: "character varying"},
			},
		},
		{
			Name: "order_lines",
			Columns: []ColumnInfo{
				{Name: "id", DBType: "integer", IsPrimaryKey: true},
				{Name: "order_id", DBType: "integer"},
				{Name: "qty", DBType: "integer"},
			},
			ForeignKeys: []ForeignKeyInfo{{Column: "order_id", ForeignTable: "orders", ForeignColumn: "id"}},
		},
	}
	writeResourceYAML(&b, tables[0], tables, "postgres")
	out := b.String()

	if !strings.Contains(out, "    children:\n") {
		t.Fatalf("orders must emit a children block, got:\n%s", out)
	}
	if !strings.Contains(out, "      - name: OrderLines\n") {
		t.Fatalf("children must list the child by its plural name, got:\n%s", out)
	}
	if !strings.Contains(out, "        resource: OrderLine\n") {
		t.Fatalf("children must reference the child resource name, got:\n%s", out)
	}
	if !strings.Contains(out, "        column: order_id\n") {
		t.Fatalf("children must record the reverse FK column, got:\n%s", out)
	}

	// sklad_zasoby.pn references sklad_zbozi.pn which is NOT the parent's key,
	// so sklad_zbozi gets no children entry.
	var b2 strings.Builder
	writeResourceYAML(&b2, fkSchemaTables()[1], fkSchemaTables(), "postgres")
	if strings.Contains(b2.String(), "children:") {
		t.Fatalf("non-PK FK target must not emit a children block, got:\n%s", b2.String())
	}
}

// viewTables returns tables for a schema where order_summary is a database view
// (no primary key, no foreign keys) alongside a real orders table.
func viewTables() []TableInfo {
	return []TableInfo{
		{
			Name: "orders",
			Columns: []ColumnInfo{
				{Name: "id", DBType: "integer", IsPrimaryKey: true},
				{Name: "customer_name", DBType: "character varying"},
				{Name: "total", DBType: "numeric"},
			},
		},
		{
			Name:   "order_summary",
			IsView: true,
			Columns: []ColumnInfo{
				{Name: "customer_name", DBType: "character varying"},
				{Name: "total", DBType: "numeric"},
				{Name: "created_at", DBType: "timestamp without time zone"},
			},
		},
	}
}

// intGridView returns a view whose resolved key column is an integer literal id,
// so it is eligible for a detail ("view form").
func intGridView() TableInfo {
	return TableInfo{
		Name:   "active_customers",
		IsView: true,
		Columns: []ColumnInfo{
			{Name: "id", DBType: "integer"},
			{Name: "name", DBType: "character varying"},
		},
	}
}

// TestConvertSchemaView ensures views are marked with View in the schema block
// and keep their fallback key column.
func TestConvertSchemaView(t *testing.T) {
	s := convertSchema(viewTables(), "postgres")

	summary := s.Tables[1]
	if !summary.View {
		t.Fatal("order_summary must be marked as a view in the schema block")
	}
	if summary.PK != "customer_name" {
		t.Fatalf("view key must fall back to its first column, got %q", summary.PK)
	}
}

// TestWriteResourceYAMLViewReadOnly ensures a text-keyed view is emitted as a
// read-only resource: list + card present, no form section, and no detail (the
// non-integer key column cannot feed the int-casting detail handler).
func TestWriteResourceYAMLViewReadOnly(t *testing.T) {
	var b strings.Builder
	tables := viewTables()
	writeResourceYAML(&b, tables[1], tables, "postgres")
	out := b.String()

	if !strings.Contains(out, "list:") || !strings.Contains(out, "card:") {
		t.Fatalf("view resource must have list/card sections, got:\n%s", out)
	}
	if strings.Contains(out, "form:") {
		t.Fatalf("view resource must not have a form section, got:\n%s", out)
	}
	if strings.Contains(out, "detail:") {
		t.Fatalf("text-keyed view must not emit a detail section, got:\n%s", out)
	}
	if !strings.Contains(out, "id_column: customer_name") {
		t.Fatalf("view key column must fall back to the first column (customer_name), got:\n%s", out)
	}
}

// TestWriteResourceYAMLViewDetail ensures an integer-keyed view still gets the
// detail ("view form") section.
func TestWriteResourceYAMLViewDetail(t *testing.T) {
	var b strings.Builder
	writeResourceYAML(&b, intGridView(), []TableInfo{intGridView()}, "postgres")
	out := b.String()

	if !strings.Contains(out, "detail:") {
		t.Fatalf("integer-keyed view must emit a detail section, got:\n%s", out)
	}
	if !strings.Contains(out, "card:") {
		t.Fatalf("view must emit a card section, got:\n%s", out)
	}
	if strings.Contains(out, "form:") {
		t.Fatalf("view must not emit a form section, got:\n%s", out)
	}
}

// TestIntrospectSQLiteKeywordTable ensures introspecting a SQLite database that
// contains a table named with a SQL keyword (e.g. "Order") succeeds. PRAGMA
// arguments must quote the identifier or SQLite errors with
// `near "Order": syntax error`.
func TestIntrospectSQLiteKeywordTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE "Order" (id INTEGER PRIMARY KEY AUTOINCREMENT, total REAL, status TEXT)`,
		`CREATE TABLE Customer (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, order_id INTEGER REFERENCES "Order"(id))`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}

	tables, err := introspectSQLite(db)
	if err != nil {
		t.Fatalf("introspectSQLite: %v", err)
	}

	var order *TableInfo
	var customer *TableInfo
	for i := range tables {
		switch tables[i].Name {
		case "Order":
			order = &tables[i]
		case "Customer":
			customer = &tables[i]
		}
	}
	if order == nil {
		t.Fatal("Order table not introspected")
	}
	if len(order.Columns) != 3 || !order.Columns[0].IsPrimaryKey {
		t.Fatalf("Order columns wrong: %+v", order.Columns)
	}
	if customer == nil {
		t.Fatal("Customer table not introspected")
	}
	if len(customer.ForeignKeys) != 1 || customer.ForeignKeys[0].ForeignTable != "Order" {
		t.Fatalf("Customer FK should reference Order, got %+v", customer.ForeignKeys)
	}
}

// TestMergeResourcesAddNew tests that new tables are added to existing config.
func TestMergeResourcesAddNew(t *testing.T) {
	// Existing config with one resource (User)
	existingYAML := `
version: "1.0"
panel:
  id: admin
  path: /admin
  name: "My Admin"
connections:
  default:
    driver: sqlite
    dsn: "file:test.db"
schema:
  tables:
    - name: users
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
    - name: posts
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
        - name: title
          type: string
resources:
  - name: Post
    label: Posts
    table: posts
    list:
      query: ListPosts
      count_query: CountPosts
      columns:
        - name: id
          type: integer
          sortable: true
        - name: title
          type: string
          searchable: true
      default_sort: -created_at
`

	// Introspected tables: users, posts, AND new table "comments"
	tables := []TableInfo{
		{Name: "users", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}}},
		{Name: "posts", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}, {Name: "title", DBType: "text"}, {Name: "created_at", DBType: "timestamp"}}},
		{Name: "comments", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}, {Name: "post_id", DBType: "integer"}, {Name: "body", DBType: "text"}}, ForeignKeys: []ForeignKeyInfo{{Column: "post_id", ForeignTable: "posts", ForeignColumn: "id"}}},
	}

	mergedYAML, added, orphaned, err := mergeResources([]byte(existingYAML), tables, "sqlite", "file:test.db")
	if err != nil {
		t.Fatalf("mergeResources failed: %v", err)
	}

	// Should have added Comment resource
	if len(added) != 1 || added[0] != "Comment" {
		t.Fatalf("expected added=[Comment], got %v", added)
	}

	// Should have no orphaned
	if len(orphaned) != 0 {
		t.Fatalf("expected no orphaned, got %v", orphaned)
	}

	// Verify merged YAML has Comment resource
	var merged struct {
		Resources []struct {
			Name string `yaml:"name"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal(mergedYAML, &merged); err != nil {
		t.Fatalf("merged YAML parse failed: %v", err)
	}

	foundComment := false
	for _, r := range merged.Resources {
		if r.Name == "Comment" {
			foundComment = true
			break
		}
	}
	if !foundComment {
		t.Fatal("Comment resource not found in merged YAML")
	}

	// Verify Post resource still exists (preserved)
	foundPost := false
	for _, r := range merged.Resources {
		if r.Name == "Post" {
			foundPost = true
			break
		}
	}
	if !foundPost {
		t.Fatal("Post resource was not preserved")
	}
}

// TestMergeResourcesPreserveCustomizations tests that existing resource customizations are preserved.
func TestMergeResourcesPreserveCustomizations(t *testing.T) {
	// Existing config with customized Post resource (custom label, extra column, action)
	existingYAML := `
version: "1.0"
panel:
  id: admin
  path: /admin
  name: "My Admin"
connections:
  default:
    driver: sqlite
    dsn: "file:test.db"
schema:
  tables:
    - name: users
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
    - name: posts
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
        - name: title
          type: string
resources:
  - name: Post
    label: "Blog Posts"  # Custom label
    table: posts
    list:
      query: ListPosts
      count_query: CountPosts
      columns:
        - name: id
          type: integer
          sortable: true
        - name: title
          type: string
          searchable: true
          label: "Post Title"  # Custom label
        - name: custom_field  # Extra field added by user
          type: string
      default_sort: -created_at
    actions:
      - name: publish
        label: "Publish"
        query: "UPDATE posts SET published = true WHERE id = $1"
`

	// Introspected tables: users, posts (no new tables)
	tables := []TableInfo{
		{Name: "users", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}}},
		{Name: "posts", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}, {Name: "title", DBType: "text"}, {Name: "created_at", DBType: "timestamp"}}},
	}

	mergedYAML, added, orphaned, err := mergeResources([]byte(existingYAML), tables, "sqlite", "file:test.db")
	if err != nil {
		t.Fatalf("mergeResources failed: %v", err)
	}

	// No new tables
	if len(added) != 0 {
		t.Fatalf("expected no added, got %v", added)
	}
	if len(orphaned) != 0 {
		t.Fatalf("expected no orphaned, got %v", orphaned)
	}

	// Verify customizations preserved
	var merged struct {
		Resources []struct {
			Name   string `yaml:"name"`
			Label  string `yaml:"label"`
			List   struct {
				Columns []struct {
					Name  string `yaml:"name"`
					Label string `yaml:"label"`
				} `yaml:"columns"`
			} `yaml:"list"`
			Actions []struct {
				Name  string `yaml:"name"`
				Label string `yaml:"label"`
			} `yaml:"actions"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal(mergedYAML, &merged); err != nil {
		t.Fatalf("merged YAML parse failed: %v", err)
	}

	if len(merged.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(merged.Resources))
	}
	res := merged.Resources[0]
	if res.Name != "Post" {
		t.Fatalf("expected Post, got %s", res.Name)
	}
	if res.Label != "Blog Posts" {
		t.Fatalf("custom label not preserved: %s", res.Label)
	}
	if len(res.List.Columns) != 3 {
		t.Fatalf("expected 3 columns (including custom_field), got %d", len(res.List.Columns))
	}
	// Check custom label preserved
	foundCustomLabel := false
	for _, c := range res.List.Columns {
		if c.Name == "title" && c.Label == "Post Title" {
			foundCustomLabel = true
		}
		if c.Name == "custom_field" {
			// Custom field should be preserved
		}
	}
	if !foundCustomLabel {
		t.Fatal("custom column label not preserved")
	}
	// Check action preserved
	if len(res.Actions) != 1 || res.Actions[0].Name != "publish" {
		t.Fatal("custom action not preserved")
	}
}

// TestMergeResourcesOrphaned tests that resources for dropped tables are marked as orphaned.
func TestMergeResourcesOrphaned(t *testing.T) {
	// Existing config with Post and Comment resources
	existingYAML := `
version: "1.0"
panel:
  id: admin
  path: /admin
  name: "My Admin"
connections:
  default:
    driver: sqlite
    dsn: "file:test.db"
schema:
  tables:
    - name: users
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
    - name: posts
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
        - name: title
          type: string
    - name: comments
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
        - name: post_id
          type: integer
        - name: body
          type: string
resources:
  - name: Post
    label: Posts
    table: posts
    list:
      query: ListPosts
      count_query: CountPosts
      columns:
        - name: id
          type: integer
          sortable: true
        - name: title
          type: string
          searchable: true
  - name: Comment
    label: Comments
    table: comments
    list:
      query: ListComments
      count_query: CountComments
      columns:
        - name: id
          type: integer
          sortable: true
        - name: body
          type: string
`

	// Introspected tables: users, posts only (comments table dropped)
	tables := []TableInfo{
		{Name: "users", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}}},
		{Name: "posts", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}, {Name: "title", DBType: "text"}, {Name: "created_at", DBType: "timestamp"}}},
	}

	mergedYAML, added, orphaned, err := mergeResources([]byte(existingYAML), tables, "sqlite", "file:test.db")
	if err != nil {
		t.Fatalf("mergeResources failed: %v", err)
	}

	if len(added) != 0 {
		t.Fatalf("expected no added, got %v", added)
	}
	if len(orphaned) != 1 || orphaned[0] != "Comment" {
		t.Fatalf("expected orphaned=[Comment], got %v", orphaned)
	}

	// Verify Comment resource has orphaned comment
	var merged struct {
		Resources []yaml.Node `yaml:"resources"`
	}
	if err := yaml.Unmarshal(mergedYAML, &merged); err != nil {
		t.Fatalf("merged YAML parse failed: %v", err)
	}

	foundOrphanedComment := false
	for _, r := range merged.Resources {
		var res struct {
			Name string `yaml:"name"`
		}
		if err := r.Decode(&res); err != nil {
			continue
		}
		if res.Name == "Comment" {
			if r.HeadComment != "" && strings.Contains(r.HeadComment, "ORPHANED") {
				foundOrphanedComment = true
			}
			break
		}
	}
	if !foundOrphanedComment {
		t.Fatal("Comment resource not marked as orphaned")
	}
}

// TestMergeResourcesSchemaReplaced tests that schema block is fully replaced.
func TestMergeResourcesSchemaReplaced(t *testing.T) {
	existingYAML := `
version: "1.0"
panel:
  id: admin
  path: /admin
  name: "My Admin"
connections:
  default:
    driver: sqlite
    dsn: "file:old.db"
schema:
  tables:
    - name: users
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
    - name: posts
      pk: id
      columns:
        - name: id
          type: integer
          primary_key: true
        - name: title
          type: string
resources:
  - name: Post
    label: Posts
    table: posts
    list:
      query: ListPosts
      count_query: CountPosts
      columns:
        - name: id
          type: integer
          sortable: true
        - name: title
          type: string
          searchable: true
`

	// Introspected tables with NEW column on posts (body) and new table comments
	tables := []TableInfo{
		{Name: "users", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}}},
		{Name: "posts", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}, {Name: "title", DBType: "text"}, {Name: "body", DBType: "text"}, {Name: "created_at", DBType: "timestamp"}}},
		{Name: "comments", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}, {Name: "post_id", DBType: "integer"}, {Name: "body", DBType: "text"}}, ForeignKeys: []ForeignKeyInfo{{Column: "post_id", ForeignTable: "posts", ForeignColumn: "id"}}},
	}

	mergedYAML, _, _, err := mergeResources([]byte(existingYAML), tables, "sqlite", "file:new.db")
	if err != nil {
		t.Fatalf("mergeResources failed: %v", err)
	}

	// Verify schema block has new column "body" on posts and new table comments
	var merged struct {
		Schema struct {
			Tables []struct {
				Name    string `yaml:"name"`
				Columns []struct {
					Name string `yaml:"name"`
				} `yaml:"columns"`
			} `yaml:"tables"`
		} `yaml:"schema"`
		Connections struct {
			Default struct {
				DSN string `yaml:"dsn"`
			} `yaml:"default"`
		} `yaml:"connections"`
	}
	if err := yaml.Unmarshal(mergedYAML, &merged); err != nil {
		t.Fatalf("merged YAML parse failed: %v", err)
	}

	// Check posts table has body column
	foundBody := false
	for _, t := range merged.Schema.Tables {
		if t.Name == "posts" {
			for _, c := range t.Columns {
				if c.Name == "body" {
					foundBody = true
				}
			}
		}
	}
	if !foundBody {
		t.Fatal("schema block not updated: posts.body column missing")
	}

	// Check comments table exists
	foundComments := false
	for _, t := range merged.Schema.Tables {
		if t.Name == "comments" {
			foundComments = true
		}
	}
	if !foundComments {
		t.Fatal("schema block not updated: comments table missing")
	}

	// Check DSN updated
	if merged.Connections.Default.DSN != "file:new.db" {
		t.Fatalf("DSN not updated: got %s", merged.Connections.Default.DSN)
	}
}

// TestMergeResourcesNoConfig tests fallback to full init when config doesn't exist.
// This is tested via cmdInitUpdate but we can test the mergeResources behavior with empty YAML.
func TestMergeResourcesEmptyConfig(t *testing.T) {
	// Empty/minimal YAML
	existingYAML := `
version: "1.0"
panel:
  id: admin
  path: /admin
  name: "My Admin"
connections:
  default:
    driver: sqlite
    dsn: "file:test.db"
schema:
  tables: []
resources: []
`

	tables := []TableInfo{
		{Name: "users", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}}},
		{Name: "posts", Columns: []ColumnInfo{{Name: "id", DBType: "integer", IsPrimaryKey: true}, {Name: "title", DBType: "text"}}},
	}

	mergedYAML, added, orphaned, err := mergeResources([]byte(existingYAML), tables, "sqlite", "file:test.db")
	if err != nil {
		t.Fatalf("mergeResources failed: %v", err)
	}

	// Should add Post resource
	if len(added) != 1 || added[0] != "Post" {
		t.Fatalf("expected added=[Post], got %v", added)
	}
	if len(orphaned) != 0 {
		t.Fatalf("expected no orphaned, got %v", orphaned)
	}

	var merged struct {
		Resources []struct {
			Name string `yaml:"name"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal(mergedYAML, &merged); err != nil {
		t.Fatalf("merged YAML parse failed: %v", err)
	}

	if len(merged.Resources) != 1 || merged.Resources[0].Name != "Post" {
		t.Fatalf("expected 1 Post resource, got %v", merged.Resources)
	}
}
