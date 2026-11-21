// main.go
//
// Generates the top-level main.go of the generated admin panel application.
// The generated program resolves the database DSN at runtime (DATABASE_URL
// env var, then the .ENV file, then an embedded localhost fallback only when
// the config declared no connection), opens the database/sql connection,
// builds the chi router and starts the HTTP server.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/filterexpr"
	"github.com/MichalHerstus/yaga/internal/types"
)

// generateMain writes main.go for the generated app. The app opens TWO
// databases: the business DB (DATABASE_URL, driver per config) and the local
// SQLite system DB holding auth data (AUTH_DB_DSN, default "system.db"). The
// modernc.org/sqlite driver is therefore always imported; the business driver
// is added on top unless the business DB is itself sqlite. Both connections
// are verified (Ping plus a sanity query) BEFORE binding the listen port, so a
// missing/uninitialized DB never occupies the port. The configured DSNs are
// never baked into the binary — they live in the sibling .ENV file (see
// generateEnvFile) and can be overridden by env vars. Returns an error if the
// file cannot be written.
func (g *Generator) generateMain() error {
	driverName := "pgx"
	driverImports := `_ "modernc.org/sqlite"`
	if g.isSQLite() {
		driverName = "sqlite"
	} else if g.isMSSQL() {
		driverName = "mssql"
		driverImports = "_ \"github.com/microsoft/go-mssqldb\"\n\t" + driverImports
	} else {
		driverImports = "_ \"github.com/jackc/pgx/v5/stdlib\"\n\t" + driverImports
	}

	authTable, _, _ := g.systemDBInfo()

	poolCode := ""
	for _, conn := range g.Config.Connections {
		if conn.Pool.MaxOpen > 0 {
			poolCode += fmt.Sprintf("\n\tdb.SetMaxOpenConns(%d)\n", conn.Pool.MaxOpen)
		}
		if conn.Pool.MaxIdle > 0 {
			poolCode += fmt.Sprintf("\n\tdb.SetMaxIdleConns(%d)\n", conn.Pool.MaxIdle)
		}
		if conn.Pool.Lifetime != "" {
			poolCode += fmt.Sprintf("\n\tif d, err := time.ParseDuration(%q); err == nil {\n\t\tdb.SetConnMaxLifetime(d)\n\t}\n", conn.Pool.Lifetime)
		}
		break
	}

	// DSN resolution: DATABASE_URL env var wins, then the .ENV file next to the
	// binary, then — only when the config declared NO connection at all (nothing
	// secret was ever configured) — a localhost postgres default. When a real
	// connection was configured the DSN is NOT embedded here; it lives in .ENV
	// (see generateEnvFile) so credentials never end up compiled into the binary.
	dsnBlock := `
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = envFrom(".ENV", "DATABASE_URL")
	}
`
	if configDSN(g.Config) == "" {
		dsnBlock += `	if dsn == "" {
		dsn = "postgres://localhost:5432/db?sslmode=disable"
	}
`
	} else {
		dsnBlock += `	if dsn == "" {
		log.Fatalf("no DATABASE_URL: set the environment variable or add it to .ENV")
	}
`
	}

	// Auth DB DSN resolution: explicit env var wins, then .ENV, then the
	// system.db created next to the generated app by `yaga generate`.
	authDSNBlock := `
	authDSN := os.Getenv("AUTH_DB_DSN")
	if authDSN == "" {
		authDSN = envFrom(".ENV", "AUTH_DB_DSN")
	}
	if authDSN == "" {
		authDSN = "system.db"
	}
`

	// The auth table always lives in SQLite, so quote its identifiers with the
	// SQLite dialect regardless of the business DB driver.
	authSanityQuery := fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", filterexpr.QuoteIdent("sqlite", authTable))

	// The business sanity query must target a real business table — users/roles
	// no longer exist there. Prefer the first resource's table, then the first
	// schema table; a config with neither only gets a Ping check.
	businessSanity := ""
	if table := g.businessSanityTable(); table != "" {
		q := fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", g.quoteIdent(table))
		if g.isMSSQL() {
			q = fmt.Sprintf("SELECT TOP 1 1 FROM %s", g.quoteIdent(table))
		}
		businessSanity = fmt.Sprintf(`
	var one int
	if err := db.QueryRow(%q).Scan(&one); err != nil && err != sql.ErrNoRows {
		log.Fatalf("database not initialized: %%v", err)
	}
`, q)
	}

	luaImport := ""
	luaInit := ""
	if g.hasAnyScript() {
		luaImport = fmt.Sprintf("\n\tluascript %q", g.moduleImport("internal/panel/luascript"))
		// The runtime defaults to SQLite placeholders, but emit the setter on
		// every driver: with the default (true) on SQLite the import would be
		// otherwise unused and the generated project would fail to build.
		luaInit = fmt.Sprintf("\n\tluascript.SetKeepQuestion(%t)", g.isSQLite())
	}

	code := fmt.Sprintf(`package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	%s%s
	"%s"
	"%s"
)

func main() {
	port := flag.Int("port", 0, "listen port (overrides ADDR env)")
	flag.IntVar(port, "p", 0, "shorthand for --port")
	logLevel := flag.String("log", "full", "log level: full (default) or err (errors only)")
	flag.StringVar(logLevel, "l", "full", "shorthand for --log")
	help := flag.Bool("help", false, "print command line syntax and exit")
	flag.BoolVar(help, "h", false, "shorthand for --help")
	flag.Usage = func() {
		fmt.Fprintf(os.Stdout, "Usage: %%s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *help {
		flag.Usage()
		os.Exit(0)
	}

	auth.Init()%s%s%s

	db, err := sql.Open(%q, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}
%s%s
	authDB, err := sql.Open("sqlite", authDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer authDB.Close()

	if err := authDB.Ping(); err != nil {
		log.Fatal(err)
	}
	var authOne int
	if err := authDB.QueryRow(%q).Scan(&authOne); err != nil && err != sql.ErrNoRows {
		log.Fatalf("system database not initialized: %%v", err)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if *port != 0 {
		addr = fmt.Sprintf(":%%d", *port)
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: panel.NewRouter(db, authDB, *logLevel),
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %%s: %%v (is another dashboard instance already running?)", addr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown: %%v", err)
		}
	}()

	log.Printf("Starting server on %%s", addr)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// envFrom reads a dotenv-style KEY=value file and returns the value of key
// ("" when the file or key is absent). Blank lines and # comments are skipped,
// surrounding single/double quotes are stripped. Keeps the dashboard free of
// external dotenv dependencies.
func envFrom(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		if strings.TrimSpace(line[:eq]) != key {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '\'' || val[0] == '"') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		return val
	}
	return ""
}
`, driverImports, luaImport, g.moduleImport("internal/panel"), g.moduleImport("internal/panel/auth"), dsnBlock, luaInit, authDSNBlock, driverName, poolCode, businessSanity, authSanityQuery)

	return os.WriteFile(filepath.Join(g.OutDir, "main.go"), []byte(code), 0644)
}

// businessSanityTable returns a table to probe in the business DB after Ping,
// so a missing/uninitialized DB fails before the port is bound. Resources come
// first (their tables are guaranteed by the captured schema); otherwise the
// first captured schema table is used. Auth tables are excluded — they now
// live only in the SQLite system DB. Returns "" when there is nothing to probe.
func (g *Generator) businessSanityTable() string {
	for _, r := range g.Config.Resources {
		if name := tableName(r); name != "" && !isAuthTable(name) {
			return name
		}
	}
	if g.Config.Schema != nil {
		for _, t := range g.Config.Schema.Tables {
			if t.Name != "" && !isAuthTable(t.Name) {
				return t.Name
			}
		}
	}
	return ""
}

// isAuthTable reports whether a table belongs to the SQLite system DB rather
// than the business DB. Kept local to the generator package (the CLI has its
// own copy used during introspection).
func isAuthTable(name string) bool {
	l := strings.ToLower(name)
	return l == "users" || l == "roles"
}

// configDSN returns the DSN of the first configured connection, or "" when no
// connection is configured. Only real configured DSNs are returned — the
// localhost fallback is handled by generateMain, never emitted into .ENV.
// Params: cfg (parsed config whose Connections map is inspected).
// Returns: the first configured connection DSN ("" when none).
func configDSN(cfg *types.Config) string {
	for _, conn := range cfg.Connections {
		return conn.DSN
	}
	return ""
}

// generateEnvFile writes the .ENV file into the output directory holding the
// configured database DSN (DATABASE_URL) and the auth DB DSN (AUTH_DB_DSN),
// mode 0600 so credentials are not world-readable. Deployments can edit this
// file to point at a different database without rebuilding; the corresponding
// environment variables override the values at runtime. Emitted only when the
// config declares a connection (a config with zero connections boots from the
// non-secret localhost default baked into main.go, and the auth DB from its
// system.db default). Returns an error on write failure.
func (g *Generator) generateEnvFile() error {
	dsn := g.sqliteDSNForEnv(configDSN(g.Config))
	if dsn == "" {
		return nil
	}
	content := "# Generated by YAGA — database connection strings read at startup.\n" +
		"# Edit to switch databases per deployment (test/prod, …) without rebuilding.\n" +
		"# DATABASE_URL / AUTH_DB_DSN environment variables override these values.\n" +
		"DATABASE_URL=" + dsn + "\n" +
		"AUTH_DB_DSN=" + systemDBFilename + "\n"
	return os.WriteFile(filepath.Join(g.OutDir, ".ENV"), []byte(content), 0600)
}

// sqliteDSNForEnv resolves a relative SQLite file DSN against the directory
// holding the source config, so the generated .ENV keeps working when the
// dashboard is run from its own output directory (e.g. `make run` inside
// admin/). The `dsn:` in yaga.yaml is authored relative to the folder where
// `yaga init`/`yaga generate` ran, but the binary is executed with the output
// directory as its CWD, so a verbatim relative path would resolve to the wrong
// place (this is what produced `unable to open database file (14)`).
//
// Non-sqlite DSNs, empty DSNs, in-memory databases, already-absolute paths and
// `file:` URIs carrying `mode=memory` are returned unchanged; a plain relative
// path (or a relative `file:` URI) is rewritten to an absolute one, preserving
// any query string/fragment.
// Params: dsn (the configured connection DSN).
// Returns: the DSN to write into .ENV.
func (g *Generator) sqliteDSNForEnv(dsn string) string {
	if !g.isSQLite() || dsn == "" {
		return dsn
	}
	prefix, rest := "", dsn
	if strings.HasPrefix(dsn, "file:") {
		prefix, rest = "file:", strings.TrimPrefix(dsn, "file:")
	}
	query := ""
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		query, rest = rest[i:], rest[:i]
	}
	if rest == "" || strings.HasPrefix(rest, ":") || strings.Contains(query, "mode=memory") || filepath.IsAbs(rest) {
		return dsn
	}
	base := g.ConfigDir
	if base == "" {
		base = "."
	}
	abs, err := filepath.Abs(filepath.Join(base, rest))
	if err != nil {
		return dsn
	}
	return prefix + filepath.ToSlash(abs) + query
}
