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

	"github.com/MichalHerstus/yaga/internal/types"
)

// generateMain writes main.go for the generated app: it imports the driver
// package (pgx stdlib for postgres, mattn/go-sqlite3 for sqlite, go-mssqldb
// for mssql), opens the database connection resolved at runtime (see the DSN
// resolution block), verifies the database is usable (Ping plus a sanity query
// against the auth table) BEFORE binding the listen port, then serves on a
// port chosen by the --port flag (or the ADDR env var, default ":8080") with
// graceful shutdown on SIGINT/SIGTERM. The configured DSN is never baked into
// the binary — it is emitted into the sibling .ENV file (see generateEnvFile)
// which deployments can edit. Returns an error if the file cannot be written.
func (g *Generator) generateMain() error {
	driverName := "postgres"
	driverImport := fmt.Sprintf("_ %q", g.moduleImport(g.Config.SQLC.OutputPkg))
	if g.isSQLite() {
		driverName = "sqlite3"
		driverImport = `_ "github.com/mattn/go-sqlite3"`
	} else if g.isMSSQL() {
		driverName = "mssql"
		driverImport = `_ "github.com/microsoft/go-mssqldb"`
	} else {
		driverName = "pgx"
		driverImport = `_ "github.com/jackc/pgx/v5/stdlib"`
	}

	authTable := g.Config.Auth.Table
	if authTable == "" {
		authTable = "users"
	}

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

	sanityQuery := fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", g.quoteIdent(authTable))
	if g.isMSSQL() {
		sanityQuery = fmt.Sprintf("SELECT TOP 1 1 FROM %s", g.quoteIdent(authTable))
	}

	luaImport := ""
	luaInit := ""
	if g.hasAnyScript() {
		luaImport = fmt.Sprintf("\n\tluascript %q", g.moduleImport("internal/panel/luascript"))
		if !g.isSQLite() {
			luaInit = "\n\tluascript.SetKeepQuestion(false)"
		}
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

	auth.Init()%s%s

	db, err := sql.Open(%q, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}
%s
	var one int
	if err := db.QueryRow(%q).Scan(&one); err != nil && err != sql.ErrNoRows {
		log.Fatalf("database not initialized: %%v", err)
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
		Handler: panel.NewRouter(db, *logLevel),
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
`, driverImport, luaImport, g.moduleImport("internal/panel"), g.moduleImport("internal/panel/auth"), dsnBlock, luaInit, driverName, poolCode, sanityQuery)

	return os.WriteFile(filepath.Join(g.OutDir, "main.go"), []byte(code), 0644)
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
// configured database DSN (DATABASE_URL), mode 0600 so credentials are not
// world-readable. Deployments can edit this file to point at a different
// database without rebuilding; the DATABASE_URL environment variable
// overrides it at runtime. Emitted only when the config declares a connection
// (a config with zero connections boots from the non-secret localhost default
// baked into main.go). Returns an error on write failure.
func (g *Generator) generateEnvFile() error {
	dsn := configDSN(g.Config)
	if dsn == "" {
		return nil
	}
	content := "# Generated by YAGA — database connection string read at startup.\n" +
		"# Edit to switch databases per deployment (test/prod, …) without rebuilding.\n" +
		"# The DATABASE_URL environment variable overrides this value at runtime.\n" +
		"DATABASE_URL=" + dsn + "\n"
	return os.WriteFile(filepath.Join(g.OutDir, ".ENV"), []byte(content), 0600)
}
