// generator.go
//
// Orchestrates the code generation pipeline for the admin panel application.
// The Generator type holds the parsed configuration and the output directory,
// and Generate() runs each code-generation step in order (main, router, auth,
// resource handlers, data queries, page handlers, views, go.mod, view models,
// assets). It also creates the directory layout of the generated project.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/filterexpr"
	"github.com/MichalHerstus/yaga/internal/types"
)

// Generator drives the code generation of one admin panel application.
// Config is the parsed yaga configuration; OutDir is where the generated
// project is written; ConfigDir is the directory containing the source
// yaga.yaml (used to locate the user's sql/ tree to copy into the output).
type Generator struct {
	Config    *types.Config
	OutDir    string
	ConfigDir string
	// SkipPlugins disables the plugin loader (escape hatch for --skip-plugins).
	SkipPlugins bool
	// Verbose enables per-plugin load summaries.
	Verbose bool
	// pluginFnNames tracks fn hook names whose implementation is provided by a
	// plugin hook source, so generateHooks skips stub generation for them.
	pluginFnNames map[string]bool
	// pluginHookFiles holds every plugin-provided hooks-package source file
	// (name -> content), used to gate Scope emission even when no YAML hook
	// block is declared.
	pluginHookFiles map[string]string
}

// New creates a Generator for the given parsed configuration and output
// directory.
// Params: cfg (parsed YAML config), outDir (destination directory).
// Returns: a ready-to-use *Generator.
func New(cfg *types.Config, outDir string) *Generator {
	return &Generator{
		Config: cfg,
		OutDir: outDir,
	}
}

// moduleImport returns the module-qualified import path for a project-relative
// package path, e.g. ("internal/panel") -> "admin/internal/panel". The module
// name is the base name of the output directory, matching the go.mod generated
// by generateGoMod.
// Params: pkg (project-relative package path).
// Returns: the full import path used by the generated code.
func (g *Generator) moduleImport(pkg string) string {
	return filepath.Base(g.OutDir) + "/" + pkg
}

// driver returns the database driver of the first configured connection,
// defaulting to "postgres" when no connections are configured.
// Returns: the driver name ("postgres" or "sqlite").
func (g *Generator) driver() string {
	for _, conn := range g.Config.Connections {
		if conn.Driver != "" {
			return conn.Driver
		}
	}
	return "postgres"
}

// isSQLite reports whether the configured database driver is sqlite.
// Returns: true when the driver is "sqlite" or "sqlite3".
func (g *Generator) isSQLite() bool {
	d := g.driver()
	return d == "sqlite" || d == "sqlite3"
}

// isMSSQL reports whether the configured database driver is Microsoft SQL
// Server. MSSQL connections use the go-mssqldb "mssql" driver name so the
// driver's loose placeholder parsing accepts the $N placeholders required by
// the emitted SQL.
// Returns: true when the driver is "mssql" or "sqlserver".
func (g *Generator) isMSSQL() bool {
	d := g.driver()
	return d == "mssql" || d == "sqlserver"
}

// placeholder returns the SQL bind placeholder for the given 1-based argument
// index. Postgres uses numbered placeholders ($1, $2, ...) while sqlite uses
// a positional "?". MSSQL keeps $N placeholders because the mssql driver name
// loosely parses them into named T-SQL parameters.
// Params: n (1-based argument position).
// Returns: the placeholder token for the configured driver.
func (g *Generator) placeholder(n int) string {
	if g.isSQLite() {
		return "?"
	}
	return fmt.Sprintf("$%d", n)
}

// likeOp returns the case-insensitive LIKE operator for the configured driver.
// Postgres uses ILIKE; sqlite's LIKE is already case-insensitive for ASCII and
// MSSQL's default collations are case-insensitive (LIKE).
// Returns: "ILIKE" for postgres, "LIKE" for sqlite and mssql.
func (g *Generator) likeOp() string {
	if g.isSQLite() || g.isMSSQL() {
		return "LIKE"
	}
	return "ILIKE"
}

// quoteIdent returns a driver-correct quoted SQL identifier for the configured
// driver: `"Order"` (double quotes, embedded " doubled) for postgres/sqlite and
// `[Order]` (brackets, embedded ] doubled) for mssql. Every generated SQL
// identifier — table names, column names, schema columns — is routed through
// this helper so keyword/mixed-case names (e.g. an "Order" table) survive.
func (g *Generator) quoteIdent(name string) string {
	return filterexpr.QuoteIdent(g.driver(), name)
}

// quoteSQLList renders a comma-separated list of Go string literals that hold
// SQL-quoted identifiers, each optionally prefixed with a table alias (the
// prefix itself is NOT quoted: "t."). It feeds the searchable-columns slice in
// the generated list/card handlers, where the value is concatenated with
// " LIKE ?" at runtime, so the identifiers must be quoted (they cannot be
// pre-escaped into an emitted SQL literal).
// Params: words (the column names), prefix (optional "t."-style alias prefix).
// Returns: a comma-separated list of quoted Go literals.
func (g *Generator) quoteSQLList(words []string, prefix string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = fmt.Sprintf("%q", prefix+g.quoteIdent(w))
	}
	return strings.Join(q, ", ")
}

// colsLiteralQuoted renders a comma-separated list of Go string literals that
// hold SQL-quoted column names, used for the cols []string literals in the
// generated create/update/import handlers (the slice values are joined into the
// INSERT/SET SQL at runtime).
// Params: cols (the column names).
// Returns: a comma-separated list of quoted Go literals.
func (g *Generator) colsLiteralQuoted(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = fmt.Sprintf("%q", g.quoteIdent(c))
	}
	return strings.Join(q, ", ")
}

// embedSQL escapes SQL text so it can be injected verbatim into an emitted Go
// string literal (double quotes become \"). Only sites that splice SQL into a
// generated "..." literal with %s (rather than %q) need this.
// Params: s (SQL text already identifier-quoted).
// Returns: the same text with quotes escaped for Go string-literal embedding.
func embedSQL(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// idGoType returns the Go type used for primary key ids in the generated data
// queries: sqlite INTEGER ids map to int64 while postgres INTEGER ids map to
// int32.
// Returns: "int32" for postgres, "int64" for sqlite.
func (g *Generator) idGoType() string {
	if g.isSQLite() {
		return "int64"
	}
	return "int32"
}

// idGoTypeForResource returns the Go type used to cast the :id path parameter
// when reading a row. It honours the resource's optional id_type override
// (emitted by init --db for e.g. BIGINT/SMALLINT identity primary keys) and
// falls back to the driver default otherwise.
// Params: r (the resource definition).
// Returns: the Go type name, e.g. "int32", "int64" or "int16".
func (g *Generator) idGoTypeForResource(r types.Resource) string {
	if r.IDType != "" {
		return r.IDType
	}
	return g.idGoType()
}

// Generate runs the full generation pipeline in dependency order: it ensures
// the directory layout exists, then writes internal/data (Get queries derived
// from the captured schema block), main.go, the router, the auth package, one
// handler file set per resource, one page handler per page, all templ views,
// go.mod, the view models and the static assets. Runs fully offline: the
// schema comes from the config's `schema:` block, never from sqlc. Returns an
// error if any step fails.
func (g *Generator) Generate() error {
	if err := g.ensureDirs(); err != nil {
		return fmt.Errorf("creating directories: %w", err)
	}

	// Plugins run before any resource/page generation. The audit log augments
	// the config after plugins (the AuditLog resource is itself list-only) but
	// before the second ensureDirs so its handler/view dirs are created. The
	// second ensureDirs call creates handler/view dirs for plugin- and
	// audit-contributed resources.
	if err := g.loadPlugins(); err != nil {
		return fmt.Errorf("loading plugins: %w", err)
	}
	g.applyAudit()
	if err := g.ensureDirs(); err != nil {
		return fmt.Errorf("creating resource directories: %w", err)
	}
	if err := g.generateAuditSchema(); err != nil {
		return fmt.Errorf("generating audit schema: %w", err)
	}
	if err := g.generateProcedures(); err != nil {
		return fmt.Errorf("generating procedures: %w", err)
	}

	if err := g.generateData(); err != nil {
		return fmt.Errorf("generating data package: %w", err)
	}

	if err := g.generateMain(); err != nil {
		return fmt.Errorf("generating main.go: %w", err)
	}

	if err := g.generateEnvFile(); err != nil {
		return fmt.Errorf("generating .ENV: %w", err)
	}

	if err := g.generateRouter(); err != nil {
		return fmt.Errorf("generating router: %w", err)
	}

	if err := g.generateAuth(); err != nil {
		return fmt.Errorf("generating auth: %w", err)
	}

	if err := g.generateHTTPErr(); err != nil {
		return fmt.Errorf("generating httperr: %w", err)
	}

	if err := g.generateSQLUtil(); err != nil {
		return fmt.Errorf("generating sqlutil: %w", err)
	}

	if err := g.generateLuascript(); err != nil {
		return fmt.Errorf("generating luascript: %w", err)
	}

	for _, r := range g.Config.Resources {
		if err := g.generateResource(r); err != nil {
			return fmt.Errorf("generating resource %s: %w", r.Name, err)
		}
	}

	for _, p := range g.Config.Pages {
		if err := g.generatePage(p); err != nil {
			return fmt.Errorf("generating page %s: %w", p.Name, err)
		}
	}

	if err := g.generateViews(); err != nil {
		return fmt.Errorf("generating views: %w", err)
	}

	if err := g.generateHooks(); err != nil {
		return fmt.Errorf("generating hooks: %w", err)
	}

	if err := g.generateGoMod(); err != nil {
		return fmt.Errorf("generating go.mod: %w", err)
	}

	if err := g.generateMakefile(); err != nil {
		return fmt.Errorf("generating Makefile: %w", err)
	}

	if err := g.generateBuildScript(); err != nil {
		return fmt.Errorf("generating build.ps1: %w", err)
	}

	if err := g.generateViewModels(); err != nil {
		return fmt.Errorf("generating view models: %w", err)
	}

	if err := g.generateAssets(); err != nil {
		return fmt.Errorf("generating assets: %w", err)
	}

	return nil
}

// ensureDirs creates the fixed directory layout of the generated project
// (internal/panel, internal/data, internal/views, internal/viewmodels,
// internal/assets, static, sql) plus one resource handler and view
// subdirectory per resource. Returns an error if a directory cannot be
// created.
func (g *Generator) ensureDirs() error {
	dirs := []string{
		"internal/panel/auth",
		"internal/panel/httperr",
		"internal/panel/resources",
		"internal/panel/pages",
		"internal/data",
		"internal/hooks",
		"internal/views/layout",
		"internal/views/resources",
		"internal/views/pages",
		"internal/views/widgets",
		"internal/views/components",
		"internal/viewmodels",
		"static/css",
		"static/js",
		"sql/migrations",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(g.OutDir, d), 0755); err != nil {
			return err
		}
	}

	for _, r := range g.Config.Resources {
		resDir := filepath.Join("internal/panel/resources", resourcePkgName(r.Name))
		if err := os.MkdirAll(filepath.Join(g.OutDir, resDir), 0755); err != nil {
			return err
		}
		viewDir := filepath.Join("internal/views/resources", resourcePkgName(r.Name))
		if err := os.MkdirAll(filepath.Join(g.OutDir, viewDir), 0755); err != nil {
			return err
		}
	}

	return nil
}
