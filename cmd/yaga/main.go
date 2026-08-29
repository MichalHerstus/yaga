// main.go
//
// CLI entry point for the yaga admin panel generator. Parses the
// subcommand (init, generate, validate, version) plus the global flags
// (--config, --out, --force, --verbose) and delegates to the parser and
// generator packages.
package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichalHerstus/yaga/internal/generator"
	"github.com/MichalHerstus/yaga/internal/parser"
)

// version is the current yaga release version.
const version = "2.1.0"

//go:embed AGENTS_for_generated_dashboard.md
var agentsForGeneratedDashboard string

// main is the CLI entry point. It requires at least one argument and
// dispatches to cmdInit, cmdGenerate or cmdValidate, or prints the version.
// Missing or unknown arguments print the usage text and exit with code 1.
func main() {
	ensureAgentGuide()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		cmdInit()
	case "edit":
		cmdEdit()
	case "wedit":
		cmdWedit()
	case "generate":
		cmdGenerate()
	case "validate":
		cmdValidate()
	case "version":
		fmt.Printf("yaga version %s\n", version)
	default:
		printUsage()
		os.Exit(1)
	}
}

// ensureAgentGuide writes the embedded AGENTS.md guide into the current
// working directory when an AGENTS.md file does not already exist there. It
// runs on every invocation so a freshly cloned project (or a directory without
// the guide) always gets the agent instructions. Failures are reported to
// stderr but do not abort the CLI.
func ensureAgentGuide() {
	target := filepath.Join(".", "AGENTS.md")
	if _, err := os.Stat(target); err == nil {
		return
	}
	if err := os.WriteFile(target, []byte(agentsForGeneratedDashboard), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write %s: %v\n", target, err)
		return
	}
	fmt.Printf("Created %s (agent guide for generated dashboards)\n", target)
}

// printUsage prints the CLI help text to stdout, listing the available
// subcommands and their flags.
func printUsage() {
	fmt.Println(`YAGA —> Yaml Advanced Generator for Admin panels
(c) 2026, White Dog Software, MIT license	

Usage:
  yaga init --db DSN  Introspect an existing database and generate yaga.yaml
                      (the captured schema: block is the sole schema source)
  yaga init --db DSN --update
                      Merge new tables from database into existing yaga.yaml
  yaga edit           Interactive YAML config editor (TUI)
  yaga wedit          Web-based YAML config editor (browser, local HTTP server)
  yaga generate       Generate the admin panel Go application (offline, no sqlc)
  yaga validate       Validate the YAML configuration
  yaga version        Print version information

Flags:
  --config, -c   Path to YAML config file (default: yaga.yaml)
  --out, -o      Output directory (default: ./admin)
  --db, -d DSN   Introspect database (postgres://..., sqlserver://... or sqlite file path)
  --force, -f    Overwrite existing files
  --verbose, -v  Enable verbose logging
  --skip-plugins, -s
                 Skip loading declared plugins (generate cannot use them)
  --update       Merge new tables into existing config (init only)
  --fix          Auto-repair known-fixable validation problems (e.g. an inert
                 list/card filter block) and rewrite the config
  --dry-run      With validate: show what --fix would apply without writing
  --admin-password, -p PASSWORD
                 Set the initial admin password for --db scaffolding
                 (a random one is generated and printed when omitted)

AI-assisted edit (experimental feature only):
  --prompt TEXT  Edit yaga.yaml via AI instead of the TUI
                 (the full config is sent to the AI provider)
                 file://PATH reads the prompt from a file (~ expands to home)
  --apikey KEY   OpenRouter API key (fallback: OPENROUTER_API_KEY env, then .ENV)
  --model MODEL  Model id (fallback: .ENV, then openrouter/auto);
                 "lmstudio" uses a local LM Studio server (127.0.0.1:1234, no key)
  --dry-run      Print proposed YAML + diff without writing

WEdit (wedit only):
  --port N       Web editor listen port (default: 9090)
  --open         Open the editor in the default browser after binding`)
}

// parseGlobalFlags scans os.Args[2:] for the global flags shared by all
// subcommands. Flags that take a value (--config/-c, --out/-o, --db/-d,
// --admin-password/-p) consume the following argument.
// Returns: configPath (YAML config file path, default "yaga.yaml"),
// outDir (output directory, default "./admin"),
// db (connection string for --db/-d introspection mode, required by init),
// adminPassword (initial admin password for --db scaffolding, or ""),
// force (overwrite existing files), verbose (enable verbose logging),
// skipPlugins (skip loading declared plugins),
// updateMode (merge new tables into existing config instead of overwriting).
func parseGlobalFlags() (configPath, outDir, db, adminPassword string, force, verbose, skipPlugins, updateMode bool) {
	configPath = "yaga.yaml"
	outDir = "./admin"
	db = ""
	adminPassword = ""
	force = false
	verbose = false
	skipPlugins = false
	updateMode = false

	args := os.Args[2:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config", "-c":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			}
		case "--out", "-o":
			if i+1 < len(args) {
				outDir = args[i+1]
				i++
			}
		case "--db", "-d":
			if i+1 < len(args) {
				db = args[i+1]
				i++
			}
		case "--admin-password", "-p":
			if i+1 < len(args) {
				adminPassword = args[i+1]
				i++
			}
		case "--force", "-f":
			force = true
		case "--verbose", "-v":
			verbose = true
		case "--skip-plugins", "-s":
			skipPlugins = true
		case "--update":
			updateMode = true
		}
	}
	return
}

// cmdInit scaffolds a project from an existing database: it requires --db,
// connects to the database, introspects its schema and generates yaga.yaml
// (including the captured `schema:` block, the sole schema source for the
// generator) plus the admin auth tables when missing. The plain starter
// scaffold and --demo were removed in D11 — the database is the only source
// of truth.
// With --update, merges newly introspected tables into an existing yaga.yaml
// while preserving user customizations (resources, navigation, pages, etc.).
func cmdInit() {
	configPath, outDir, dbDSN, adminPassword, force, _, _, updateMode := parseGlobalFlags()

	if dbDSN == "" {
		fmt.Fprintln(os.Stderr, "Error: init requires a database connection string: yaga init --db DSN")
		fmt.Fprintln(os.Stderr, "  (postgres://..., sqlserver://... or a sqlite file path)")
		os.Exit(1)
	}

	if err := cmdInitFromDB(configPath, outDir, dbDSN, adminPassword, force, updateMode); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// cmdValidate parses and validates the YAML config file, printing whether it
// is valid. With --verbose it also prints a short summary of the panel, the
// number of resources, pages and navigation groups. With --fix it first
// auto-repairs known-fixable problems (e.g. an inert list/card filter block)
// and rewrites the file; --dry-run shows what --fix would apply without
// writing.
func cmdValidate() {
	configPath, _, _, _, _, verbose, _, _ := parseGlobalFlags()
	fix, dryRun := wantFixFlags()

	if fix || dryRun {
		fixed, _, remaining, err := autoFixFile(configPath, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Validation failed: %v\n", err)
			os.Exit(1)
		}
		if len(fixed) > 0 {
			if dryRun {
				fmt.Println("Dry run — the following changes would be applied:")
			} else {
				fmt.Println("Auto-fixed:")
				fmt.Printf("Backup saved to %s.bak\n", configPath)
			}
			for _, f := range fixed {
				fmt.Printf("  %s\n", f)
			}
		}
		if len(remaining) > 0 {
			fmt.Fprintln(os.Stderr, "Validation failed:")
			for _, e := range remaining {
				fmt.Fprintf(os.Stderr, "  %v\n", e)
			}
			os.Exit(1)
		}
		fmt.Println("Configuration is valid!")
		if verbose {
			cfg, err := parser.ParseFile(configPath)
			if err == nil {
				fmt.Printf("  Panel: %s (path: %s)\n", cfg.Panel.Name, cfg.Panel.Path)
				fmt.Printf("  Resources: %d\n", len(cfg.Resources))
				fmt.Printf("  Pages: %d\n", len(cfg.Pages))
				fmt.Printf("  Navigation groups: %d\n", len(cfg.Navigation))
			}
		}
		return
	}

	cfg, err := parser.ParseFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Validation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Configuration is valid!")
	if verbose {
		fmt.Printf("  Panel: %s (path: %s)\n", cfg.Panel.Name, cfg.Panel.Path)
		fmt.Printf("  Resources: %d\n", len(cfg.Resources))
		fmt.Printf("  Pages: %d\n", len(cfg.Pages))
		fmt.Printf("  Navigation groups: %d\n", len(cfg.Navigation))
	}
}

// cmdGenerate parses the YAML config and generates the admin panel
// application into outDir, fully offline: the schema comes from the config's
// `schema:` block (no sqlc). Afterwards it attempts to run the Tailwind CSS
// build; failure there is reported as a warning instead of being fatal, since
// the user can re-run it manually.
func cmdGenerate() {
	configPath, outDir, _, _, _, verbose, skipPlugins, _ := parseGlobalFlags()

	cfg, err := parser.ParseFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config: %v\n", err)
		os.Exit(1)
	}

	if verbose {
		fmt.Println("Configuration parsed successfully")
	}

	gen := generator.New(cfg, outDir)
	gen.ConfigDir = filepath.Dir(configPath)
	gen.Verbose = verbose
	gen.SkipPlugins = skipPlugins
	if err := gen.Generate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Admin panel generated in", outDir)
	fmt.Println("")

	fmt.Println("Next steps:")
	fmt.Println("  1. cd", outDir)
	fmt.Println("  2. go mod tidy")
	fmt.Println("  3. go tool templ generate")
	fmt.Println("  4. go build ./...")
}
