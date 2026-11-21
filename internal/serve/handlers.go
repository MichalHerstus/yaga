package serve

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/MichalHerstus/yaga/internal/fixer"
	luasrc "github.com/MichalHerstus/yaga/internal/generator/luasrc"
	"github.com/MichalHerstus/yaga/internal/parser"
	"github.com/MichalHerstus/yaga/internal/schema"
	"github.com/MichalHerstus/yaga/internal/types"
	"gopkg.in/yaml.v3"
)

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// configTree renders the config as a JSON-ready generic tree. The types have
// only YAML tags, so the bridge is yaml.Marshal -> yaml generic tree -> json.
// JSON field names therefore match the YAML field names the SPA renders and
// submits back.
func configTree(cfg *types.Config) (interface{}, error) {
	y, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var tree interface{}
	if err := yaml.Unmarshal(y, &tree); err != nil {
		return nil, err
	}
	return tree, nil
}

// configFromJSON parses a JSON config body into a validated *types.Config,
// returning the list of structural errors (empty when valid) and warnings.
func configFromJSON(data []byte) (*types.Config, []string, []string) {
	var tree interface{}
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, []string{"invalid JSON: " + err.Error()}, nil
	}
	y, err := yaml.Marshal(tree)
	if err != nil {
		return nil, []string{"converting JSON to YAML: " + err.Error()}, nil
	}
	return configFromYAML(y)
}

// configFromYAML parses a YAML config body into a validated *types.Config.
func configFromYAML(data []byte) (*types.Config, []string, []string) {
	var cfg types.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, []string{"parsing config: " + err.Error()}, nil
	}
	var errs, warns []string
	for _, verr := range parser.ValidateAll(&cfg) {
		if _, ok := verr.(parser.Warning); ok {
			warns = append(warns, verr.Error())
		} else {
			errs = append(errs, verr.Error())
		}
	}
	return &cfg, errs, warns
}

// splitErrors partitions ValidateAll errors into errors and warnings.
func splitErrors(errs []error) ([]string, []string) {
	var out, warns []string
	for _, e := range errs {
		if _, ok := e.(parser.Warning); ok {
			warns = append(warns, e.Error())
		} else {
			out = append(out, e.Error())
		}
	}
	return out, warns
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	path := s.configPath
	rev := s.rev
	s.mu.RUnlock()

	tree, err := configTree(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":   path,
		"config": tree,
		"rev":    rev,
	})
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cfg, errs, warns := configFromJSON(body)
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"ok":       false,
			"errors":   errs,
			"warnings": warns,
		})
		return
	}
	rev := s.replaceConfig(cfg)
	if warns == nil {
		warns = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "warnings": warns, "rev": rev})
}

// handleRev reports the current config revision so the SPA can detect changes
// made elsewhere (MCP/agent) without fetching the whole config.
func (s *Server) handleRev(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"rev": s.currentRev()})
}

// saveToDisk writes the in-memory config to disk. Shared by the SPA save
// endpoint and the MCP save tool.
func (s *Server) saveToDisk() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := yaml.Marshal(s.cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(s.configPath, data, 0644)
}

// handleSave writes the in-memory config to disk.
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if err := s.saveToDisk(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleFix applies the auto-repair engine (the `validate --fix` logic) to the
// in-memory config and replaces it with the repaired one; nothing is written to
// disk until a global Save (POST /api/save). Returns the fixed paths and any
// unfixable errors/warnings that remain. Partial repairs still apply — like the
// CLI, fixable problems are fixed even when others cannot be.
func (s *Server) handleFix(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	out, fixed, remaining, err := fixer.Apply(data)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"ok": false, "error": err.Error(), "fixed": []string{}})
		return
	}
	norm := func(ss []string) []string {
		if ss == nil {
			return []string{}
		}
		return ss
	}
	if len(fixed) == 0 {
		errs, warns := splitErrors(remaining)
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "changed": false, "fixed": []string{}, "errors": norm(errs), "warnings": norm(warns)})
		return
	}

	newCfg, errs, warns := configFromYAML(out)
	if newCfg != nil {
		s.replaceConfig(newCfg)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "changed": true, "fixed": fixed, "errors": norm(errs), "warnings": norm(warns)})
}

// findingJSON is one row of the /api/validate screen.
type findingJSON struct {
	Kind   string `json:"kind"` // "error" | "warning"
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

// findingsOf runs the full health check over a config (structural validation of
// a YAML copy so defaults are not injected, plus a schema-block reference
// pass) and returns every finding. The captured `schema:` block is the source
// of truth: a resource's table must exist in it and every referenced column
// must be a column of that table.
func (s *Server) findingsOf(cfg *types.Config) []findingJSON {
	findings := make([]findingJSON, 0)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return append(findings, findingJSON{"error", "yaml.Marshal failed", err.Error()})
	}
	var copyCfg types.Config
	if err := yaml.Unmarshal(data, &copyCfg); err != nil {
		return append(findings, findingJSON{"error", "yaml.Unmarshal failed", err.Error()})
	}
	for _, verr := range parser.ValidateAll(&copyCfg) {
		kind := "error"
		if _, ok := verr.(parser.Warning); ok {
			kind = "warning"
		}
		findings = append(findings, findingJSON{kind, verr.Error(), ""})
	}
	if copyCfg.Schema == nil {
		findings = append(findings, findingJSON{"warning", "no schema block captured (re-run `yaga init --db`)", ""})
		return findings
	}
	refs := schema.CollectReferences(&copyCfg)
	for _, r := range copyCfg.Resources {
		table := refs.Tables[r.Name]
		st := schemaBlockTable(copyCfg, table)
		if st == nil {
			findings = append(findings, findingJSON{"error", r.Name + ": table not found in schema block: " + table, ""})
			continue
		}
		for _, c := range refs.ColumnRefs[r.Name] {
			if !schema.HasColumn(st, c.Column) {
				findings = append(findings, findingJSON{"warning", "missing column: " + r.Name + "." + c.Section + "." + c.Column, ""})
			}
		}
	}
	return findings
}

// handleValidate runs the full health check and returns every finding.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]interface{}{"findings": s.findingsOf(cfg)})
}

// schemaBlockTable returns the captured `schema:` block entry for a table by
// name (case-insensitive), or nil when the block has no such table.
func schemaBlockTable(cfg types.Config, name string) *types.SchemaTable {
	if cfg.Schema == nil {
		return nil
	}
	for i := range cfg.Schema.Tables {
		t := &cfg.Schema.Tables[i]
		if strings.EqualFold(t.Name, name) {
			return t
		}
	}
	return nil
}



// handleRawGet returns the in-memory config serialized to YAML for the raw
// editing tab.
func (s *Server) handleRawGet(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"yaml": string(data)})
}

// handleRawPut accepts a raw YAML config body, validates it and replaces the
// in-memory config. Validation errors are returned (422) and the in-memory
// config is untouched.
func (s *Server) handleRawPut(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cfg, errs, warns := configFromYAML(data)
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{
			"ok": false, "errors": errs, "warnings": warns,
		})
		return
	}
	rev := s.replaceConfig(cfg)
	if warns == nil {
		warns = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "warnings": warns, "rev": rev})
}

// --- E6: Lua check / debug / SQL debug / sample refresh ---

// ensureStub returns the cached in-memory sqlite stub, building it from the
// schema block if it does not yet exist.
func (s *Server) ensureStub() (*sql.DB, error) {
	if s.stubDB != nil {
		return s.stubDB, nil
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	db, err := BuildStubDB(cfg)
	if err != nil {
		return nil, err
	}
	s.stubDB = db
	return db, nil
}

// handleLuaCheck checks a Lua script for syntax errors.
func (s *Server) handleLuaCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Script string `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	errs := luasrc.SyntaxCheck(req.Script)
	if errs == nil {
		errs = []luasrc.SyntaxError{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"errors": errs, "ok": len(errs) == 0})
}

// handleLuaRun runs a Lua script against the seeded in-memory stub.
func (s *Server) handleLuaRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Script string                 `json:"script"`
		ID     int64                  `json:"id"`
		Table  string                 `json:"table"`
		Action string                 `json:"action"`
		Values map[string]interface{} `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	stub, err := s.ensureStub()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": err.Error(),
		})
		return
	}
	rec := NewRecordingExecer(stub)
	scope := luasrc.Scope{
		ID:     req.ID,
		Table:  req.Table,
		Action: req.Action,
		Values: req.Values,
	}
	var out strings.Builder
	runErr := luasrc.RunWithOutput(context.Background(), rec, scope, req.Script, &out)
	result := map[string]interface{}{
		"ok":       runErr == nil,
		"output":   out.String(),
		"captured": rec.Calls,
		"values":   scope.Values,
	}
	if runErr != nil {
		if abort, ok := runErr.(*luasrc.AbortError); ok {
			result["error"] = map[string]interface{}{"line": 0, "message": abort.Msg}
		} else {
			result["error"] = map[string]interface{}{"line": 0, "message": runErr.Error()}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// handleSQLRun executes a SQL body against the seeded in-memory stub.
func (s *Server) handleSQLRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind     string `json:"kind"` // "hook" | "action" | "procedure"
		SQL      string `json:"sql"`
		ID       int64  `json:"id"`
		Table    string `json:"table"`
		Action   string `json:"action"`
		ProcName string `json:"proc_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	stub, err := s.ensureStub()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": err.Error(),
		})
		return
	}

	ctx := context.Background()
	switch req.Kind {
	case "procedure":
		// Resolve body from config procedures block.
		s.mu.RLock()
		cfg := s.cfg
		s.mu.RUnlock()
		body := req.SQL
		if req.ProcName != "" {
			for _, p := range cfg.Procedures {
				if p.Name == req.ProcName {
					body = p.SQL
					break
				}
			}
		}
		results := RunSQLBatch(ctx, stub, body, req.ID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true, "steps": results, "driver": "sqlite",
		})
	default: // "hook" or "action"
		result := RunSQL(ctx, stub, req.SQL, req.ID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": result.Error == "", "steps": []RunSQLResult{result}, "driver": "sqlite",
		})
	}
}

// handleSchemaBrowser returns every table from the captured schema: block with
// its columns and foreign keys. Read-only — no DB connection required.
func (s *Server) handleSchemaBrowser(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	tables := []types.SchemaTable{}
	if cfg.Schema != nil {
		tables = cfg.Schema.Tables
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tables": tables})
}

// handleSchemaSample returns up to `limit` rows from the in-memory stub for
// the given table. When a live --db DSN is configured, it queries the live
// database directly instead (fresh data, no seeding required).
func (s *Server) handleSchemaSample(w http.ResponseWriter, r *http.Request) {
	table := r.PathValue("table")
	if table == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "table required"})
		return
	}

	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	// When a live database is configured, query it directly.
	if s.liveDSN != "" {
		rows, err := queryLiveSample(s.liveDriver, s.liveDSN, table, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"table": table,
			"limit": limit,
			"rows":  rows,
			"count": len(rows),
		})
		return
	}

	stub, err := s.ensureStub()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	query := fmt.Sprintf("SELECT * FROM %s LIMIT %d", quoteIdentStub(table), limit)
	result := RunSQL(r.Context(), stub, query, 0)

	if result.Error != "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": result.Error})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"table": table,
		"limit": limit,
		"rows":  result.Rows,
		"count": len(result.Rows),
	})
}

// queryLiveSample opens a live database connection, selects up to `limit` rows
// from `table`, and returns them as an array of column->value maps. The
// connection is closed before returning.
func queryLiveSample(driver, dsn, table string, limit int) ([]map[string]interface{}, error) {
	driver = driverForDSN(driver, dsn)
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening live database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging live database: %w", err)
	}

	var query string
	var qIdent func(string) string
	switch driver {
	case "mssql":
		query = fmt.Sprintf("SELECT TOP %d * FROM [%s]", limit, table)
		qIdent = func(s string) string { return "[" + strings.ReplaceAll(s, "]", "]]") + "]" }
	default: // sqlite, postgres
		query = fmt.Sprintf("SELECT * FROM %s LIMIT %d", quoteIdentStub(table), limit)
		qIdent = quoteIdentStub
	}
	_ = qIdent // query already built above

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("querying live database: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}

	var out []map[string]interface{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for j := range vals {
			ptrs[j] = &vals[j]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		row := make(map[string]interface{}, len(cols))
		for j, c := range cols {
			row[c] = vals[j]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}
	return out, nil
}

// handleSampleRefresh seeds the in-memory stub with up to 100 rows per schema
// table from the first configured live database connection.
func (s *Server) handleSampleRefresh(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	dsn := ""
	driver := ""
	for _, conn := range cfg.Connections {
		dsn = conn.DSN
		driver = conn.Driver
		if dsn == "" {
			dsn = os.Getenv("DATABASE_URL")
		}
		break
	}
	if driver == "" {
		driver = "pgx"
	}

	// Close existing stub if any.
	if s.stubDB != nil {
		s.stubDB.Close()
		s.stubDB = nil
	}

	stub, err := BuildStubDB(cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": err.Error(),
		})
		return
	}

	rowCount := 0
	if dsn != "" {
		if err := SeedFromDB(dsn, driver, cfg, stub); err == nil {
			// Count rows per table for the response.
			if cfg.Schema != nil {
				for _, t := range cfg.Schema.Tables {
					var n int
					stub.QueryRow("SELECT COUNT(*) FROM " + quoteIdentStub(t.Name)).Scan(&n)
					rowCount += n
				}
			}
		}
	}
	s.stubDB = stub
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"row_count": rowCount,
		"tables":   len(cfg.Schema.Tables),
	})
}
