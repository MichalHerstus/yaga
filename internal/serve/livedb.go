// livedb.go — resolves the live business-database connection used by the wedit
// schema browser ("Show data") and the sample-data stub seeder.
//
// An explicit --db flag always wins. When wedit is started without it (the
// common case — the DSN already lives in yaga.yaml's connections: block) the
// first configured connection is used, so "Show data" queries the real
// database instead of falling back to the empty in-memory stub.
package serve

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
)

// liveConnection returns the DSN and database/sql driver for the live business
// database. The --db flag (with its optional driver override) wins; otherwise
// the first configured connection is used, falling back to the DATABASE_URL
// environment variable and then the `.ENV` file next to the config.
func (s *Server) liveConnection() (dsn, driver string) {
	s.mu.RLock()
	cfg, configPath := s.cfg, s.configPath
	flagDSN, flagDriver := s.liveDSN, s.liveDriver
	s.mu.RUnlock()

	if flagDSN != "" {
		// A DSN typed on the command line is relative to the process working
		// directory, so it is passed through unchanged.
		return flagDSN, driverForDSN(flagDriver, flagDSN)
	}
	return configLiveConnection(cfg, configPath)
}

// configLiveConnection derives the live DSN from the config's first
// connections: entry. A relative SQLite file DSN is resolved against the
// directory holding the config, because the `dsn:` in yaga.yaml is authored
// relative to the config folder (the same rule the generator applies when it
// writes .ENV).
func configLiveConnection(cfg *types.Config, configPath string) (dsn, driver string) {
	baseDir := "."
	if configPath != "" {
		baseDir = filepath.Dir(configPath)
	}

	if cfg != nil {
		for _, conn := range cfg.Connections {
			driver, dsn = conn.Driver, conn.DSN
			break
		}
	}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = envFileValue(filepath.Join(baseDir, ".ENV"), "DATABASE_URL")
	}
	if dsn == "" {
		return "", ""
	}

	resolved := driverForDSN(driver, dsn)
	if resolved == "sqlite" {
		dsn = sqliteDSNFromBase(dsn, baseDir)
	}
	return dsn, resolved
}

// sqliteDSNFromBase resolves a relative SQLite file DSN against baseDir.
// In-memory databases, absolute paths and `mode=memory` URIs pass through
// unchanged; a query string/fragment is preserved.
func sqliteDSNFromBase(dsn, baseDir string) string {
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
	if baseDir == "" {
		baseDir = "."
	}
	abs, err := filepath.Abs(filepath.Join(baseDir, rest))
	if err != nil {
		return dsn
	}
	return prefix + filepath.ToSlash(abs) + query
}

// envFileValue reads KEY=value from a dotenv-style file, returning "" when the
// file or the key is absent. Used as a last-resort DSN source so a wedit
// session also works when the connection string lives in .ENV (where
// `yaga generate` writes it).
func envFileValue(path, key string) string {
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
		return strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
	}
	return ""
}
