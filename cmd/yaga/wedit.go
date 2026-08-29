// wedit.go
//
// `yaga wedit` — the web-based YAML config editor (E4). Starts a local HTTP
// server exposing a JSON REST API over the same Go logic the TUI editor uses
// (parser.ValidateAll, schema.CollectReferences) plus an
// embedded vanilla-JS single-page app. The
// command is named `wedit` (not the E4-drafted `serve`) so it is clearly the
// web version of the YAML editor rather than a running generated dashboard.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/MichalHerstus/yaga/internal/parser"
	"github.com/MichalHerstus/yaga/internal/serve"
)

// cmdWedit parses the wedit-specific flags (--port/--open) from os.Args[2:],
// loads the config via the shared --config flag, and runs the editor server
// until interrupted.
func cmdWedit() {
	configPath, _, _, _, _, _, _, _ := parseGlobalFlags()
	port, open := parseWeditFlags(os.Args[2:])

	cfg, err := parser.ParseFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config: %v\n", err)
		os.Exit(1)
	}

	srv := serve.New(cfg, configPath, serve.Options{Port: port, OpenBrowser: open})
	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "WEdit error: %v\n", err)
		os.Exit(1)
	}
}

// parseWeditFlags scans args for the wedit-only flags. Unknown flags are
// skipped so this runs independently of parseGlobalFlags. --config is handled
// by parseGlobalFlags (the `-c` short form), so wedit only adds --port and
// --open.
func parseWeditFlags(args []string) (port int, open bool) {
	port = serve.DefaultPort
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 < len(args) {
				if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
					port = v
				}
				i++
			}
		case "--open":
			open = true
		}
	}
	return
}
