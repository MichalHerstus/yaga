# Future Enhancements — Security, Optimization & Roadmap

Review date: 2026-08-07. Status: in progress. Phase A (security hardening) is
implemented; the remaining items are proposed. File references point at the
yaga generator sources that emit the affected generated-app code.

## 1. Security findings

### Critical

- **Hardcoded session secret → auth bypass** ✅ implemented (Phase A)
  Generated `internal/panel/auth/session.go` uses
  `sessions.NewCookieStore([]byte("yaga-secret-key-change-in-production"))`.
  The secret is public in every generated app, so an attacker can forge a signed
  session cookie claiming any `user_id` and log in as any user.
  Fix: read `SESSION_SECRET` env var; generate a random one and fail fast when
  missing in production.
  Source: `internal/generator/auth.go:261`

- **SQL injection via unvalidated `order` param** ✅ implemented (Phase A)
  (list + card handlers)
  `sort` is whitelisted against `validSorts`, but `order` is interpolated raw into
  `ORDER BY %s %s`. On postgres, stacked statements execute, e.g.
  `?sort=created_at&order=desc;DROP TABLE x--`.
  Fix: whitelist `order` to `asc`/`desc`.
  Source: `internal/generator/handler.go:235,299,351,397`

- **Arbitrary file upload → stored XSS** ✅ implemented (Phase A)
  `saveUploadedFile` accepts any extension (`.html`, `.svg`, `.php`) and serves it
  from `/uploads/*` inline via `http.FileServer`. Uploaded HTML runs scripts in
  the admin origin.
  Fix: extension + content-type whitelist; serve uploads as `Content-Disposition:
  attachment` or store outside the web root.
  Source: `internal/generator/handler.go:1264-1282`

### High

- **No CSRF protection** on any state-changing POST (create/update/delete/action/
  bulk/logout). Admin panels are prime CSRF targets.
  Fix: `SameSite=Lax` session cookie + CSRF token middleware on state-changing routes.
  ✅ implemented (Phase B)

- **Error responses leak DB internals** ✅ implemented (Phase A) —
  `http.Error(w, err.Error(), ...)` on all
  handlers exposes SQL, table names and host details to clients.
  Fix: log server-side, return a generic 500.
  Source: throughout `internal/generator/handler.go`

- **Known default admin credentials** ✅ implemented (Phase A) —
  `init --db` ships `admin@admin.test / admin`;
  `init --demo` ships `admin@demo.test / admin`.
  Fix: `--admin-password` flag or generate + print a random one-time password.
  Source: `cmd/yaga/introspect.go:725-767`, `cmd/yaga/demo.go:1278-1296`

### Medium

- **Session cookie hardening** — cookie `Options` never set (no `Secure`, no
  `SameSite`; gorilla default 30-day `MaxAge`), no session ID rotation after login,
  logout exposed as GET (CSRF-able).
  Fix: explicit cookie options, rotate session on login, POST-only logout.
  ✅ implemented (Phase B)

- **No login rate limiting** — brute-forceable.
  Fix: per-IP throttling (configurable).
  ✅ implemented (Phase B)

- **CSV formula injection** — exported values starting `=`, `+`, `-`, `@` execute
  as formulas in Excel.
  Fix: escape with a leading `'` or tab.
  Source: `internal/generator/handler.go:1000-1050` (export.go)
  ✅ implemented (Phase B)

- **No security headers** ✅ implemented (Phase A) — CSP, `X-Frame-Options`,
  `X-Content-Type-Options`,
  `Referrer-Policy` unset.
  Fix: configurable security-headers middleware.
  Source: `internal/generator/router.go` (`securityHeaders`, registered on every
  generated router; a configurable variant is Phase C roadmap).

### Low / correctness

- `html` widget renders DB output via `templ.Raw` (untrusted data = stored XSS) —
  documented as trusted-input-required (`SPEC.md`); `stat`/`stats_grid` values are
  numeric-only (safe by construction). ✅ documented (Phase C, 2026-08-13)
  Source: `internal/generator/templ.go:1071`, `router.go:383`
- Action + bulk routes skip RBAC entirely (documented design) — consider optional
  enforcement.
- `update.go` / `delete.go` hardcode `WHERE id` instead of `idColumn(r)` — breaks
  introspected MSSQL tables with an `ID` key column.
  Source: `internal/generator/handler.go:959,1568`

## 2. Optimization findings

1. **Two queries per list** (COUNT + SELECT) → use `COUNT(*) OVER()` window function
   for a single round trip.
2. **`per_page` hardcoded to 20** → make configurable per resource.
3. **Connection pool config unused** — `connections.*.pool` (max_open/max_idle/
   lifetime) is parsed but never applied; wire into generated `main.go`.
   Source: `internal/generator/main.go` (no `SetMaxOpenConns` etc.), `internal/types/config.go:85`
4. **Bulk actions run N queries with no transaction** → wrap in one transaction
   (rollback on error).
5. **Options loader runs one query per `options_query` field** per form GET (N+1) →
   batch lookups.
   Source: `internal/generator/handler.go:1400-1425`
6. **Widget DB errors silently swallowed** (`_ = db.QueryRowContext`) → log errors.
   Source: `internal/generator/router.go:157-315`
7. **`SessionMiddleware` is a no-op** → remove or implement (e.g. security headers).
   Source: `internal/generator/auth.go:286-294`
8. Error-only logger exists (`--log err`); add request-id + timing for ops.

## 3. Enhancement roadmap

**Phase A — Security hardening (small surface, high priority)** ✅ done
Session secret via env, `order` whitelist, upload validation, safe error responses,
admin password handling, security headers.
- Session secret: generated `session.go` reads `SESSION_SECRET` (min 32 chars),
  fails fast on `APP_ENV=production` when unset, otherwise uses an ephemeral
  random secret with a warning.
- `order` whitelist: list/card handlers clamp to `asc`/`desc` after the
  default-sort block.
- Upload validation: `saveUploadedFile` whitelists extensions and rejects
  `text/html` / `image/svg+xml` by magic bytes; `/uploads/*` is served with
  `Content-Disposition: attachment`.
- Safe errors: generated `internal/panel/httperr` (Internal/NotFound) logs
  server-side and returns generic status text; all handlers use it.
- Admin password: `--admin-password` flag on `init --demo` / `init --db`;
  random 14-char one-time password generated + printed when omitted.
- Security headers: `securityHeaders` middleware on every generated router
  (CSP, X-Frame-Options DENY, nosniff, Referrer-Policy, Permissions-Policy).

**Phase B — CSRF & auth robustness** ✅ done
CSRF tokens + SameSite cookies, session rotation, login rate limiting, CSV escaping,
optional row-level RBAC enforcement.
- CSRF: generated `session.go` adds `CSRFToken(r, w)` (session-bound 32-byte
  random token) + `CSRFMiddleware` (skips GET/HEAD/OPTIONS and `/static/`,
  `/uploads/`; accepts `_csrf` form field or `X-CSRF-Token` header;
  `subtle.ConstantTimeCompare`; 403 on mismatch). Registered first in the panel
  `Route` block. Every state-changing form (create/update/delete/action/bulk/
  logout/login) embeds a hidden `_csrf`; `ListData`/`DetailData`/`FormData`/
  `LoginPageData` carry the token.
- SameSite cookies: `newStore` helper sets `Path: "/"`, `MaxAge: 0` (session
  cookie), `HttpOnly: true`, `Secure: os.Getenv("APP_ENV") == "production"`,
  `SameSite: http.SameSiteLaxMode`.
- Session rotation: successful login expires the old session (`MaxAge=-1` +
  `Save`) then mints a fresh one via `Store.New`; `resetLoginLimit(r)` clears
  the IP counter.
- Logout is POST-only (GET is 405); login POSTs are CSRF-protected too
  (prevents login CSRF).
- Login rate limiting: optional `auth.login.rate_limit` (`max_attempts`,
  `window_seconds`) emits `ratelimit.go` (per-IP map, mutex, windowed counter,
  `net.SplitHostPort`) and a re-render with "Too many login attempts" when
  exceeded. Absent/zero config → legacy behavior.
- CSV escaping: exported headers and values pass through `csvSafe`, which
  prefixes a `'` when a value starts with `=`, `+`, `-`, `@`, tab or CR.
- Row-level action/bulk RBAC: `Action.Policy` (pipe-separated roles) generates
  `ActionRBACMiddleware`; action and bulk routes are wrapped with
  `r.With(auth.ActionRBACMiddleware(res))` only when a policy exists
  (`hasActionPolicies`).

**Phase C — Performance & correctness** (status 2026-08-13: C.0 all done, C.2 done,
C.3 done, **C.1 deferred**)
Windowed COUNT, configurable `per_page`, pool settings wiring, transactional bulk,
batched options loader, `idColumn(r)` in update/delete, widget error logging, request
logging with request-id + timing.

**C.0 — Original items (all implemented; verified 2026-08-13):**
1. **Windowed COUNT** ✅ — the list/card data query emits
   `SELECT {cols}, COUNT(*) OVER() AS _total FROM …` and scans `_total` per row — a
   single round trip; when the page is empty `totalSet` stays false and the handler
   falls back to `total = page*perPage`. The old two-query + `countClauses`/$N-renumber
   hack is gone. Source: `internal/generator/handler.go` (list/card).
2. **Configurable `per_page`** ✅ — `ListConfig.PerPage` (default 20 applied in
   `internal/parser/validator.go:95`), an editor "Per page" field
   (`cmd/yaga/editor/resource.go:166`), and the handler reads `r.List.PerPage` for
   `LIMIT/OFFSET` (`handler.go:326`). Sources: `internal/types/resource.go:33`.
3. **Pool settings wiring** ✅ — `connections.*.pool` (`max_open_conns`/`max_idle_conns`/
   `conn_max_lifetime`) is emitted as `db.SetMaxOpenConns`/`SetMaxIdleConns`/
   `SetConnMaxLifetime` right after `Ping()` and before the sanity query; no setters
   when the block is absent. Source: `internal/generator/main.go`
   (`TestGeneratePoolSettings`).
4. **Transactional bulk** ✅ — the bulk id-loop runs inside a single `db.BeginTx`;
   `Commit()` only when every Exec succeeded, `defer Rollback()` otherwise. Source:
   `internal/generator/bulk.go`.
5. **Batched options loader** ✅ — distinct `options_query` values load once per resource
   into a shared `{name}Opts := map[string]string{}`; no N queries for N fields.
   Source: `internal/generator/handler.go` (`TestGenerateOptionsLoaderDedupe`).
6. **Widget error logging** ✅ — every widget `Query*`/`Scan` error is
   `log.Printf`'d and the widget renders with whatever rows it got. Source:
   `internal/generator/router.go`.
7. **`idColumn(r)` in update/delete** ✅ — DELETE/UPDATE use `idColumn(r)` (honoring
   `id_column:` overrides) instead of a hardcoded `WHERE id`. Source:
   `internal/generator/handler.go:1069,1822`; regression tests at
   `generator_test.go:1360-1389`.

**C.2 — Stored-XSS documentation (done 2026-08-13):** only the `html` widget is a real
raw-HTML vector — it casts a query *string* result to `template.HTML` and renders via
`templ.Raw` (`internal/generator/router.go:383`, `templ.go:1071`); the query is
config-authored, so its *result* must be trusted input. `stat`/`stats_grid` values are
safe by construction: they scan into `int64` and wrap `fmt.Sprintf("%d", …)`, so only
config-authored numbers ever reach `statWidget`'s `templ.Raw` (`templ.go:1090`). No code
change; documented in `SPEC.md` and the §1 Low finding is marked ✅ documented.

**C.1 — Request logging with request-id + timing (deferred, not implemented):** remove
the dead `SessionMiddleware` (emitted `auth.go:574-582`, registered at `router.go:52` —
its `/static/` branch no-ops then falls through to `next`) and replace the
`if logLevel == "err" { errorOnlyLogger } else { middleware.Logger }` split
(`router.go:46-50`) and the `errorOnlyLogger` literal (`router.go:139-148`, no timing /
request id) with a single generated `requestLogger`: `r.Use(middleware.RequestID)` then
`r.Use(requestLogger(logLevel == "err"))`, wrapping `middleware.NewWrapResponseWriter`,
`time.Since(start)`, logging `[<reqid>] <method> <uri> <status> <duration>` (err mode
skips status < 400). `--log full|err` flag values unchanged; generated router imports
gain `"time"`. This is a global emitter change (all configs), so the byte-identical
regression guard does not apply — assert via `assertGeneratedGoParses` + snippet tests.

**C.3 — Cross-cutting (done 2026-08-13):** version 0.9.0 → 0.10.0
(`cmd/yaga/main.go`); AGENTS.md hardening notes; gates `go build ./...`,
`go vet ./...`, `go test ./...`, `gofmt -l .`.

**Phase D — Feature roadmap**

Status: partially implemented (2026-08-13). Implementation order D2 → D3 →
D5 → D6 (D1 — auth features — and D4 — API mode — are excluded from the plan).
D2 (audit log), D3 (CSV import + export column selection), D5 (plugin fn hooks)
and D6 (SQLite stored procedures) are done. Mobile device support and Lua
scripting moved to **Phase E** below as **E1** and **E2** (their former D14/D15
numbers were freed so **D14** is now the master-detail roadmap item below).
Decisions already taken: sqlite procedures are **YAML-seeded only** (no runtime
editor UI). Assumptions flagged ⚠️ below are open to veto before implementation.

| Item | Status |
|---|---|
| Plugin system (`SPECv05plus.md` M4) | **Done (D5)** — loader, `pkg/plugin`, `--skip-plugins`, plus `AddHookSource` + plugin fn hooks |
| Audit log resource | **Done (D2)** — config `audit` block, generator-implicit INSERTs on create/update/delete/action in one tx, augmented list-only AuditLog resource + nav, driver-aware DDL/queries, demo-enabled |
| CSV import + export column selection | **Done (D3)** — `list.export` subset (Label headers) + `import_csv` (import.go, shared `buildCreateParams`, transactional, ?flash topbar, modal) |
| SQLite stored procedures (batch-in-table) | **Done (D6)** — YAML `procedures:` block, `sql_procedures` DDL + `INSERT OR IGNORE` seeds, `internal/panel/procs` package (`Exec(db,name,id)` + tokenizer statement split), sqlite proc emission flips (actions/hooks/bulk/create RETURNING), validator rejects undeclared sqlite proc refs |
| AI-assisted `yaga edit` (OpenRouter / LM Studio) | **Done (D7)** — `edit --prompt/--apikey/--model/--dry-run` (`cmd/yaga/ai.go`, embedded `ai_spec.md`, spinner progress, fragment-then-merge (keyed-item), single retry, `.ENV` credential persistence, path+value diff output (`changedPaths`), local LM Studio provider via `--model "lmstudio"`, httptest stub (OpenRouter + LM Studio) + `mergeYAML`/`changedPaths` suites) |
| Drop Node.js/npm from the dashboard build | **Done (D8)** |
| Editor Validate (main menu → results list → jump-to-fix) | **Done (D9)** |
| Rename project to YAGA (binary, module path, repo, docs) | Done |
| Drop sqlc & make the DB the sole schema source (`schema:` block, mandatory `--db`) | Planned (D11) |
| Embed pre-built CSS into the yaga binary (drop the Tailwind build step) | Planned (D12) |
| List/Card filter section (`list.filter` / `card.filter`, collapsible, `$N` params) | Planned (D13) |
| Mobile device support (always-on REST/JSON CRUD API on the dashboard + generated React Native/Expo app driven by a spec-derived manifest; `visible_on_mobile` nav filter consumed by the app) | Moved to Phase E (E1) |
| Lua scripting for actions & hooks (gopher-lua, `script:` body, `ctx` scope, `db.*`/`abort`/`log` host API) | Moved to Phase E (E2) |

---

### D2 — Audit log resource

**Status: implemented (2026-08-13).** Config block `audit` (`enabled`, `table` default
`audit_log`, `include_values`, `policy`, `exclude_resources`) implemented in
`internal/generator/audit.go`. `applyAudit()` runs after plugins in `Generate()` and
appends a list-only `AuditLog` resource (`default_sort: -created_at`, `values_json`
column only when `include_values`, RBAC from `policy`) + an "Audit Log" nav group.
`generateAuditSchema()` emits driver-aware `audit_log` DDL into `sql/migrations/` and a
sqlc List/Count file into `sql/queries/`, both skipped when a migration already declares
the audit table (`auditTableInMigrations`/`containsCreateTable` — otherwise sqlc fails
with "relation already exists"). `auditFor(r)` honors `exclude_resources` and never
audits the generated AuditLog resource. `auditInsertStr`/`auditTxBeginStr`/
`auditTxCommitStr`/`auditValuesStr` weave
`INSERT INTO audit_log (user_id, user_name, table_name, action, row_id, values_json)
VALUES ($1,$2,$3,$4,$5,$6)` into create/update/delete/action handlers, with the op + audit
insert wrapped in one transaction (`tx, err := db.BeginTx` / `defer tx.Rollback()` /
`tx.Commit()`; the hookless `_, err := db.ExecContext` path and byte-identical output are
relaxed for audited resources). Actor id comes from a generated `auth.UserID(r)` helper
(gated on `auditAnyResource()`); create needs the `RETURNING <id>` capture path even
without hooks (`fmt.Sprintf("%d", newID)`); delete/action store `row_id` only.
`values_json` contains bcrypt output (documented). Demo enables audit
(`include_values: true, policy: "admin"`) with `audit_log` in `demoSchema()`. Tests:
generator snippets (create/update/delete/action/middleware, no-values+excluded keeps
hookless path, schema skipped-when-declared, `containsCreateTable` table), parser
(defaults/validation), plus a full HTTP e2e against the generated demo (login → create/
update/action/delete → rows appear in `audit_log` with correct actor/action/row_id/values
JSON; curl's naive cookie jar 403s on the two-cookie session-rotation response — use an
RFC 6265 jar).

**Design (recommended: generator-implicit audit on every mutating op)**
```yaml
audit:
  enabled: true
  table: audit_log            # default
  include_values: true        # store JSON of submitted form values
  policy: "admin"             # optional RBAC on the audit resource
  exclude_resources: []
```
The generator:
1. **Augments config at generation time** (same technique as plugin merging): appends an
   `AuditLog` resource (list-only over `audit_log`, `default_sort: -created_at`) + an
   "Audit Log" navigation group, unless excluded — reuses all existing resource generation.
2. **Emits `audit_log` schema** into `sql/migrations/` (driver-aware DDL) +
   `sql/queries/audit.sql` (sqlc List/Count).
3. **Weaves audit INSERTs** into create/update/delete/action handlers, after the DB op,
   before the redirect:
   `INSERT INTO audit_log (user_id, user_name, table_name, action, row_id, values_json)
   VALUES ($1,$2,$3,$4,$5,$6)` — actor from `auth.UserName(r)`.

**Key design consequence:** audit requires the `RETURNING <id>` capture path on create
even when no user hooks exist — the "hookless path stays byte-identical" invariant is
relaxed for create/update/delete/action handlers when `audit.enabled` (snippet builder
`auditInsertStr` in a new `internal/generator/audit.go`, reusing `g.returningClause(r)`,
`scopeValuesStr`). Delete/action store `row_id` only (no pre-delete snapshot in v1).
Wrap op + audit insert in one transaction (folds in optimization finding #4 for single-row
ops; transactional bulk stays in D3). Respects `exclude_resources`; RBAC via the existing
`policies:` when `audit.policy` set. `values_json` contains bcrypt output (already what
`scope.Values` holds) — documented, no plaintext passwords.

**Tests / exit criteria:** snippet assertions — create.go has `RETURNING <id>` + audit
INSERT when audit on with no user hooks; delete/update/action carry the INSERT; e2e:
create/update/delete/action rows appear in the audit_log resource with correct
actor/action/row_id/values JSON.

---

### D3 — CSV import + export column selection

**Status: implemented (2026-08-13).** `ListConfig.Export []string` (optional subset;
when set the CSV export emits only those columns with `Label` headers, else the
historical all-list-columns + raw-header behavior) and `resource.import_csv: true`
(generates `import.go` + `POST /{res}/import/csv`, CSRF-protected and RBAC-wrapped
with the create permission). The create INSERT value construction was factored out of
`create.go` into a package-level `buildCreateParams(m map[string]string)
([]interface{}, error)` shared by the Create POST and the import handler (bcrypt-hashes
password fields, coerces booleans, returns a clear error for `file`/`image` fields —
the create POST keeps the legacy inline path only when the resource has such fields,
where `buildCreateParams` becomes a stub for import). Import parses a multipart CSV,
maps header cells (trimmed) to the create field names, runs every row's
`buildCreateParams` + INSERT inside ONE transaction, and redirects to the list with a
`?flash=...` message ("Imported N, Skipped M: row R: error..."). The flash is
middleware-stashed into the request context (`flashHandler` + `viewmodels.SetFlash`/
`FlashMessage`) and rendered as a topbar bar in `Base`. The list view gains an "Import
CSV" button + modal (outside the bulk `<form>`) when `import_csv` is set. Parser
rejects unknown `list.export` columns and `import_csv` without a create form. Demo
enables both on Customer (`export: [id, name, email, status]`, `import_csv: true`).
Editor: "Import CSV" toggle on the resource page + "Export" string-list editor on the
list page (`Resources/<res>/List/Export`). Known limitation: imports are NOT audited
(the audit weaving only covers the create/update/delete/action handlers). Tests:
generator snippets (export subset/all-columns, import.go reuse + transaction + flash,
create POST reuses buildCreateParams, file-field resource keeps the upload path +
stub), parser validation, and a live e2e (3 valid + 1 duplicate-email row →
"Imported 3, Skipped 1" flash; export returns only the selected columns).

**Export column selection:** `ListConfig` gains `Export []string` (optional subset).
`generateCSVHandler` uses those column names + `Label` headers when set (falls back to
today's all-list-columns behavior). No UI change (config-driven); a request-time column
picker deferred.

**CSV import:** config `resource.import_csv: true` → generates `import.go` + route
`POST /{res}/import/csv` (CSRF-protected; RBAC-wrapped when policies exist).
- **Refactor to avoid SQL drift:** factor the create INSERT construction out of `create.go`
  POST into a package-level `func buildCreateParams(m map[string]string) ([]interface{}, error)`
  (bcrypt-hashes password fields; skips/errors on `file`/`image` fields in v1). Both
  `Create` POST and `Import` call it.
- Import handler: `r.ParseMultipartForm`, `encoding/csv`, map header → create-field,
  per-row `buildCreateParams`, **one transaction** around all inserts, report
  `Imported N, Skipped M` (row numbers + errors).
- List templ: "Import CSV" button (only when `import_csv`) opening a modal
  (`enctype="multipart/form-data"`); POST → redirect with `?flash=...` shown in the topbar.
- Sprintf risk: `import.go` is a new emitter — every `%s`/`%d` counted (AGENTS.md #1).

**Tests / exit criteria:** unit — export uses subset columns; import.go emits
`buildCreateParams` reuse + transaction; e2e — `curl -F file=...` with 3 valid + 1 bad row
→ 3 inserted, 1 reported; export returns only selected columns.

---

### D5 — Plugin M5 (fn hooks)

Completes the already-built plugin system (`SPECv05plus.md` §6.7):
- `pkg/plugin`: `Panel.AddHookSource(name, content)` writes a `package hooks` Go file into
  the manifest (`HookSources map[string]string`); loader writes them to
  `OutDir/internal/hooks/`.
- `attachHook` in `internal/generator/plugin.go`: stop rejecting `fn` hooks — track
  plugin-provided fn names; `generateHooks.collectFnHooks` skips stub generation for names
  backed by a plugin hook source (or a user stub).
- Merge validation: a plugin fn hook must have a matching hook source, else fatal.
- Deliverable: extend the plugin example with an fn hook; regression — no plugins →
  unchanged output.

**Done (D5, 2026-08-13):** implemented as designed. `AddHookSource` (name validated:
bare `<file>.go`, not the reserved `hooks.go`); the loader writes hook sources into
`internal/hooks/`, tracks every package-level function name via `hookFuncNames`, and
`collectFnHooks` skips stubs for those names. `attachHook` merges fn hooks when the fn
name is backed by a source (fatal otherwise). `generateHooks` now also emits `hooks.go`
(Scope only, no stubs/imports) when plugin hook sources exist even with zero YAML hook
blocks, so plugin files compile. The audit example gained a `LogCustomerCreated` fn hook
+ `audit_hooks.go` source; e2e verified the hook fires on customer create (audit_log row)
and delete (SQL hook) on sqlite, and the generated app builds. Regression: existing
no-plugin hook tests byte-identical (all green). Also fixed a latent flag bug: `cmdGenerate`
and `cmdValidate` mis-indexed `parseGlobalFlags`'s tuple so `--force`/`--verbose`/`--skip-plugins`
were swapped (`--verbose` silently disabled the plugin loader).

---

### D6 — SQLite stored procedures (SQL-batch-in-table, YAML-seeded)

**Status: implemented (2026-08-13).** Config block `procedures:` (top-level,
sqlite-only semantics) with `name`/`description`/`sql`. `internal/generator/procs.go`
emits `sql/migrations/procedures.sql` (`CREATE TABLE IF NOT EXISTS sql_procedures(name PK,
body, description, updated_at)` + one `INSERT OR IGNORE` seed per procedure, `''`-escaped
bodies) and `internal/panel/procs/procs.go` — `Exec(db, name, id) error` looks the body up
at call time, splits it with a tokenizer (`'…'` strings incl. `''` escapes, `"…"`/`[…]`
identifiers, `--`/`/* */` comments) and runs each statement inside one transaction,
draining result rows and rolling back on error; the id is bound only when the statement
contains a `$N` placeholder (mattn errors when args exceed placeholders). Driver-aware
flips: `hookBlockEmits` is true for a declared proc on sqlite, `hookCallsStr` emits
`procs.Exec(db, "<name>", scope.ID)`, `actionExecSQL`/bulk emit `procs.Exec(db, "<name>",
id)`, and create gains `RETURNING <id>` capture for proc-only after-hooks. `procs` import
is added per-handler only when that handler actually emits a `procs.Exec` call. Undeclared
sqlite proc refs are skipped (feature-off output byte-identical — `TestGenerateProcSQLiteIgnored`
guards this). Validator (`validateProcedures`) requires unique non-empty names and — when
the driver is sqlite — every `proc:` reference on an action/hook to match a declared body
(fatal config error). Postgres/mssql ignore the block. e2e verified on sqlite: a proc
after-hook on create and a bulk proc action both ran their multi-statement batches
atomically with `$1` bound (customer_log rows + audit intact).

SQLite has no stored procedures; a "procedure stored in a table and run by the sqlite
engine" is a **named SQL-batch executor** — the body is read from a table at call time,
split into statements, and executed inside one transaction. This gives `proc:` real
semantics on sqlite (today it is a silent no-op: `procSQL` returns `""`, proc hooks/actions
emit nothing). mattn/go-sqlite3 v1.14.24 facts that shape the design: `Exec`/`Query` only
run the **first** statement of a multi-statement string (no tail loop) → must split;
`$1` binds positionally → the existing `$1` convention keeps working.

**Config** (top-level, sqlite-only semantics):
```yaml
procedures:                       # top-level
  - name: archive_old_orders
    description: "Archive orders older than 1 year"
    sql: |
      UPDATE orders SET status='archived' WHERE created_at < datetime('now','-1 year');
      INSERT INTO audit_events (msg) VALUES ('bulk archive ran');
```
Existing `proc:` fields (actions + hooks) reference these by name — same config on all
three drivers, three execution strategies (`CALL` / `EXEC` / helper).

**Generator changes** (new `internal/generator/procs.go`):
- Emits `sql_procedures(name PK, body, description, updated_at)` DDL into
  `sql/migrations/` with `INSERT OR IGNORE` seeds.
- Emits the shared `internal/panel/procs/procs.go` package:
  `Exec(db, name, id) error` — look up body → **tokenizer-based statement split**
  (handles `'…'`, `"…"`/`[…]`, `--`/`/* */` comments) → one transaction,
  `ExecContext(stmt, id)` per statement, drain stray result rows, rollback on error.
- **Driver-aware `proc` emission flips for sqlite:** `hookBlockEmits` returns true for proc
  on sqlite; `actionExecSQL`/`hookCallsStr`/bulk emit `procs.Exec(db, "<name>", <id>)`
  instead of an empty block. Create gains `RETURNING <id>` capture for proc-only
  after-hooks.
- ⚠️ **Inverts a documented AGENTS.md invariant** ("proc-only hooks on sqlite emit
  nothing") — rewrite that gotcha; regression guard asserts feature-off output stays
  byte-identical.
- **Validator:** when the driver is sqlite, every `proc:` reference must match a
  `procedures:` body — fatal config error at generation time (no runtime editor exists),
  mirroring plugin-load-failure semantics.
- Postgres/mssql: the `procedures:` block is ignored (real procs come from user DDL);
  emitting `CREATE PROCEDURE` from the YAML batch is a documented future extension.

**Tests / exit criteria:** unit — statement splitter cases (quoted `;`, comments, trailing
`;`, empty body), sqlite proc emission in actions/hooks/create, validator error on a
missing body, idempotent seeds; e2e (sqlite) — YAML-declared proc on an action + an
after-hook runs the multi-statement batch atomically; one failing statement rolls back all;
missing proc → clean `httperr` page.

---

### D7 — AI-assisted config editing (`yaga edit` via OpenRouter / LM Studio)

**Status: done (2026-08-10).** Non-interactive and opt-in: AI flags live on `edit`
only; without `--prompt` the current TUI runs unchanged. Provider is OpenRouter by
default (`openRouterBaseURL`), with an opt-in local LM Studio provider selected by
the `--model "lmstudio"` sentinel (`lmStudioBaseURL` = `http://127.0.0.1:1234/v1`,
no API key; the loaded model id is discovered via GET `/models`). Decisions taken
(2026-08-10):
one-shot write + `--dry-run` preview; `--model` flag defaulting to `openrouter/auto`;
API key via `--apikey` with `OPENROUTER_API_KEY` env fallback; after a successful run
the effective key/model are persisted to `.ENV` in the current folder so later runs can
omit the flags (`--apikey` > `OPENROUTER_API_KEY` env > `.ENV`; `--model` > `.ENV` > default);
terminal output prints only the changed keys and their new values as `path -> 'value'` lines
(`changedPaths`), never the whole file.

**Command shape:**
```
yaga edit --apikey KEY [--model MODEL] --prompt "Change dashboard title to: Order management"
yaga edit [--apikey KEY] --prompt "…" --dry-run      # preview changed sections, no write
yaga edit --prompt "…"                               # uses key + model persisted in .ENV
```

**Design:**
- `cmdEdit` branches to an AI path when `--prompt` is set; a second flag pass
  (`parseEditFlags` in a new `cmd/yaga/ai.go`) picks out `--apikey/--prompt/--model/
  --dry-run`, leaving `parseGlobalFlags`'s tuple untouched (only `edit` understands them).
- Load via `parser.ParseFile(configPath)`, marshal current YAML. Build messages: a system
  role with the output contract ("return ONLY the changed sections of the config as a YAML
  fragment in a ```yaml fence; keyed-item lists by `name` / navigation groups by `group` /
  items by `resource`/`page`/`url`; keep `version`; don't invent keys") + a user message of
  the embedded compact schema cheat-sheet (`//go:embed ai_spec.md`, ~7 KB — the 33 KB
  `SPEC.md` stays out of the prompt to keep tokens low) + the current YAML + the user's
  instruction.
- POST `{provider}/chat/completions` (stdlib `net/http`, `Authorization: Bearer`
  only when a key is set — the local LM Studio provider sends none; the key is
  never logged/echoed), `temperature: 0`, 300 s HTTP client timeout; a `spinner` on
  stderr gives live progress while waiting. On merge/validate failure, retry **once**
  feeding the validator error back; on failure exit 1 with the original file untouched.
- `extractYAMLBlock` (```yaml``` fence with fallback heuristics) → `mergeYAML` (yaml.v3
  Node merge: mappings recurse, sequences merge item-by-item by identity key, keyless lists
  replace wholesale, null fragment values leave targets untouched, no deletion support) →
  `yaml.Unmarshal` into `types.Config` → `parser.Validate`. After the run `persistEnv`
  writes the effective `OPENROUTER_API_KEY`/`MODEL` into `.ENV` (0600, unrelated lines
  preserved); both write and `--dry-run` then print the changed keys as `path -> 'value'`
  lines (`changedPaths` walks both docs and emits one line per differing leaf, keyed-list
  identity values inline, strings single-quoted) and exit 0 without echoing the whole file.
  Fragment-only output keeps responses small, so slow free-tier models finish instead of
  timing out.
- Config-only scope: SQL/`sql/queries` files are not edited by the AI path. Full
  `yaga.yaml` is transmitted to OpenRouter (documented in usage text — consent is the
  user supplying the key + prompt).

**Files:** `cmd/yaga/ai.go` (`parseEditFlags`+`.ENV` fallback, `chatCompletions`,
`lmStudioModelID`, `buildEditPrompt`, `extractYAMLBlock`, `mergeYAML` + identity-key merge
helpers, `proposeEdit` with single retry, `spinner`, `changedPaths` leaf diff, `readEnvFile`/
`writeEnvFile`/`persistEnv`, `envPathFunc`) + `ai_spec.md` (embedded schema reference incl. § AI edit
output); `edit.go` branch; `main.go` usage text.

**Tests / exit criteria:** httptest provider stub (serves GET `/models` so the same stub covers
OpenRouter and LM Studio) — happy path writes the file + prints path/value diff lines and preserves
unrelated sections; retry-on-invalid yields a valid second attempt; `--dry-run` never writes (but
still persists `.ENV`); fence extraction; a `mergeYAML` unit suite (mapping deep-merge, keyed-item
resource/fields/navigation merge, item append, wholesale widgets replace, null leaves untouched,
unknown-key/malformed/empty/non-mapping fragment errors); a `changedPaths` suite (scalar/resource/
column/navigation/index paths, added-resource leaves, value quoting, no-changes); LM Studio happy
path (discovered model id sent, no auth header, stale key ignored) and no-model-loaded error;
flag/env/`.ENV` key resolution (missing key → clear error, `.ENV` persisted + reused, precedence
flag > env > `.ENV`, model flag > `.ENV` > default, unrelated `.ENV` lines preserved). Docs:
AGENTS.md CLI section + `SPEC.md` usage lines (config is sent to the provider; `lmstudio` model).

---

### D9 — Editor validation with jump-to-fix (`yaga edit` → Validate)

**Status: done (2026-08-11).** Adds a "Validate" entry to the editor's left
nav that runs a full health check (structural + field-name + missing table/query)
and lists every problem; pressing Enter on a finding jumps to the exact editor
page and highlights the offending column/field row. Decisions taken (2026-08-11):
results-list screen (not jump-to-first), full health-check scope, missing columns
are warnings (computed-column tolerance) while structural / missing-table /
missing-query findings are errors.

**Design:**
1. **`internal/parser/validator.go`** — split `Validate` into
   `ValidateAll(cfg) []error` (collects every structural problem instead of
   early-returning) plus a thin `Validate` wrapper returning the first error, so
   `parser.Parse` and the editor's save path keep their current behaviour.
2. **`internal/schema/references.go`** — location-aware column refs: new
   `ColumnRef{Column, Section string, Index int}` +
   `References.ColumnRefs map[string][]ColumnRef` (kept beside the existing
   `Columns` map). `CollectReferences` records section+index for `list.columns`,
   `card.fields`, `detail.fields`, `form.create/update/delete.fields`, plus
   `list/card.default_sort`, `card.kanban_field`, `card.searchable` (leading `-`
   stripped via a `sortColumn` helper).
3. **`cmd/yaga/editor/sync.go`** — `syncReport.missingCols` becomes
   `[]colMissing{resource string; ref schema.ColumnRef}`; the Sync screen renders
   `resource.section.column` (more precise than today's `resource.column`).
   *(Moot since 2026-08-16: the Sync screen was removed with the D11 Phase 2
   query-file purge; the schema-column pass now lives in `validate.go`.)*
4. **`cmd/yaga/editor/validate.go` (new)** — `finding{kind, label, detail;
   goTo}` + `runValidation()`:
   - structural: validate a YAML copy via `parser.ValidateAll` (same copy
     technique as `validateCopy`); a `goTo` is attached when the message parses
     `resources[i]`/`pages[i]` (→ resource/page page) or mentions `panel.name`/
     `panel.path` (→ panel page).
   - schema: reuse `analyze()` — missing tables → resource page; missing columns
     → `sectionJump`; missing queries → the resource's SQL-queries page (query
     row focused when it appears there); missing FK `List{}` queries →
     informational warning linking to the Sync screen.
   - `sectionJump(idx, section, focusIdx)` maps sections to the existing builders
     (`columnsPage`, `cardFieldsPage`, `detailFieldsPage`, `formFieldsPage(idx,
     which)`, `listPage`, `cardPage`), `showPage`s the result, then
     `SetCurrentItem(focusIdx)` to highlight the bad row.
   - `validatePage()`: tview.List of findings (red errors / yellow warnings),
     "No problems found" empty state, Refresh (Ctrl+R) + Back (Ctrl+B) buttons —
     mirrors the Sync screen layout.
5. **`cmd/yaga/editor/editor.go`** — `buildNav` gains
   `e.navItem("Validate (Ctrl+V)", "validate", e.validatePage)` between "Sync SQL
   & YAML" and "Preview", plus a global `tcell.KeyCtrlV` case in `capture`.

**Tests / exit criteria (met):** parser — `ValidateAll` returns multiple errors while
`Validate` returns the first (`TestValidateAllReportsEveryProblem`); schema — `ColumnRefs` sections/indexes asserted in
`TestCollectReferences`; editor — `validatePage` builds (added to
`TestPageBuilders`), `runValidation` flags a bad column in list/card/form sections
with a working `goTo` (`TestRunValidationFindsBadColumns`), `sectionJump` focuses
the offending row (`TestSectionJumpFocusesOffendingRow`), sim-screen smoke
navigates to Validate (`TestValidateGlobalShortcut`). Also added a save-then-quit
regression test for the reported "Ctrl+S then Ctrl+Q asks to save" bug
(`TestSaveThenQuitSkipsConfirm`, `TestQuitConfirmClearsModified`). Docs: AGENTS.md
editor section.

---

### D8 — Drop Node.js/npm from the generated dashboard build

**Status: implemented (2026-08-11).** Goal: `make` (and `yaga generate`) must not require
node/npm; generated-app output stays byte-identical and runtime stays offline. The only
npm consumers are Tailwind CSS compilation (`npx tailwindcss`) and Chart.js vendoring
(`cp node_modules/...`). Decisions taken (2026-08-10): Tailwind via the **standalone
binary** (PATH + optional `make get-tailwind` download, sqlc-style toolchain model);
Chart.js **embedded** into yaga via `//go:embed`.

**Design:**
- **Chart.js**: commit `chart.umd.min.js` @ **4.4.1** (MIT, license banner intact) at
  `internal/generator/assets/chart.umd.min.js`; `//go:embed` it and copy to
  `OutDir/static/js/chart.js` during `generateAssets` (mkdir `static/js`). Charts then work
  after `yaga generate` alone — a bare `go build` in `admin/` serves them, zero network.
  yaga binary grows ~180 KB. `/static/js/chart.js` reference in `templ.go` unchanged.
- **Tailwind**: `RunTailwind` in `tailwind.go` swaps `npx tailwindcss` for the
  `tailwindcss` binary (PATH, honoring a `TAILWIND` env override; still non-fatal from
  `cmdGenerate`). `generateStaticAssets` **stops emitting `package.json`** (keeps
  `tailwind.config.js` + `styles.css`). Rewritten generated `Makefile`
  (`makefile.go`): remove `deps`/`npm install`; `build: css sqlc templ`; `css:`
  runs `$(TAILWIND) -i ./internal/assets/css/styles.css -o ./static/css/styles.css
  --minify` with `TAILWIND ?= $(if $(wildcard .tools/tailwindcss),.tools/tailwindcss,tailwindcss)`; new optional `get-tailwind` target maps
  `uname -s/-m` (linux/macos × x64/arm64; Windows excluded as today) and curls the pinned
  **v3.4.x** standalone binary to `.tools/tailwindcss`, which the `css` target then uses
  automatically (falling back to a `tailwindcss` on PATH).
- **Docs + hints**: `cmdGenerate` warning/Next-steps drop npm (`make css` /
  install the standalone binary); update `AGENTS.md`, `README.md` (prereq table),
  `SPEC.md`, `AGENTS_for_generated_dashboard.md`.

**Tests / exit criteria:** generator unit — `generateAssets` emits `static/js/chart.js`
equal to the embedded bytes and **no** `package.json`; the generated `Makefile` contains no
`npm` and its `css` target invokes `$(TAILWIND)`. E2E — `yaga init --demo` + `make css`
with the standalone binary produces `static/css/styles.css` and `static/js/chart.js`; a bare
`go build` serves chart.js.

---

### D10 — Rename project to YAGA

**Status: done (2026-08-13).** Renamed the project/brand from "go-fila" to
**YAGA** (**YA**ml-based **G**enerator of **A**pplications) across code, generated output,
binary, module path, GitHub repository and documentation. Decisions taken (2026-08-11): new
module path `github.com/MichalHerstus/yaga` (matches the real remote owner — the current
`github.com/go-fila/go-fila` never matched the remote `github.com/MichalHerstus/go-fila`);
default config file `go-fila.yaml` → `yaga.yaml`; version bumped 0.14.0 → 1.0.0; generated-app
runtime identifiers renamed (`go-fila-session` cookie → `yaga-session`, `gf-theme` storage key
→ `yaga-theme` — sessions invalidate + theme resets on redeploy, acceptable at 1.0.0); GitHub
repo renamed to `MichalHerstus/yaga`; `session-ses_*.md` transcripts left untouched (historical).

**Design:**
1. **Repo restructure** — `git mv cmd/go-fila cmd/yaga` (embedded `ai_spec.md` +
   `AGENTS_for_generated_dashboard.md` moved with it); repo `Makefile` `BINARY := yaga` and
   `build` target `go build -o $(BINARY) ./cmd/yaga`; `.gitignore` `/go-fila` → `/yaga`.
2. **Module path** — `go.mod` → `module github.com/MichalHerstus/yaga`; replaced
   `github.com/go-fila/go-fila` → `github.com/MichalHerstus/yaga` across the imports,
   `internal/generator/plugin.go` `gofilaModule` const (→ `yagaModule`, plus
   `gofilaCheckout`/`findGoFilaCheckout` → `yaga*`), and `examples/plugins/audit/go.mod`
   (require + `replace ... => ../../..`).
3. **CLI** — binary/command name `go-fila` → `yaga` in usage text (`main.go`), version
   output (`yaga version 1.0.0`, `main.go:46`), next-step prints (`main.go:413`,
   `demo.go:80`, `introspect.go:1456`), and default config path `go-fila.yaml` →
   `yaga.yaml` (`main.go:118`). TUI nav + title bar (`editor.go`) → "YAGA".
4. **Generated output** (`internal/generator/`) — session cookie `go-fila-session` →
   `yaga-session` (`auth.go`, 5 sites), theme key `gf-theme` → `yaga-theme` (`templ.go`,
   `auth.go`), Makefile comment "generated by go-fila" → "generated by YAGA"
   (`makefile.go:28`), plugin shim tmpdir `go-fila-plugin-shim` → `yaga-plugin-shim`
   (`plugin.go:65`).
5. **Docs & embedded content** — `README.md` (incl. `go install
   github.com/MichalHerstus/yaga/cmd/yaga@latest`), `AGENTS.md`, `SPEC.md`,
   `SPECv05plus.md`, `SPEC_yaml_editor.md`, `cmd/yaga/ai_spec.md`,
   `cmd/yaga/AGENTS_for_generated_dashboard.md` (lands inside generated apps:
   `./yaga generate --config yaga.yaml ...`). Prose brand = "YAGA", CLI word = `yaga`.
6. **GitHub** — `gh repo rename yaga --repo MichalHerstus/go-fila`,
   `git remote set-url origin https://github.com/MichalHerstus/yaga.git`, push.

**Tests / exit criteria:** new generator unit test asserting generated output contains no
`go-fila`; `go build ./...` / `go vet ./...` / `go test ./...` / `gofmt -l .` clean;
`grep -r go-fila` matches only `session-ses_*.md`. E2E — fresh `./yaga init --demo` →
`yaga generate` → `make` in the generated dir → login smoke verifying `yaga.yaml`,
`yaga-session` cookie, `yaga-theme` key, no npm.

---

### D11 — Drop sqlc & make the DB the sole schema source (2.0, core-first)

**Status: planned, not started.** A v2-level model inversion: the live database becomes the
**only** source of schema truth (tables, views, columns, types, primary keys, foreign
keys) and the **sqlc** external toolchain is removed entirely. `yaga init --db DSN` becomes
the **only** entry point (the `--db` flag is mandatory); `yaga generate` and `make build`
run **offline**. Decisions taken: **captured `schema:` block** — `init --db` introspects the
live DB, converts it to a `schema:` block inside `yaga.yaml`, and generate/build read that
block (no DB connection at build time); **inline `options_sql`** — non-derivable custom
option lists are supplied as inline SQL in YAML, while FK relations auto-generate their
option SQL from the schema block; **core-first** — editor/Sync/sqlc-adjacent UI cleanup is a
follow-up phase; **stored procedures are out of scope** for this plan; the `init` schema
scaffold and `init --demo` are removed (demo comes from the service's prepared SQLite
file). Custom SQL stays in YAML (action `query:`, page-widget `query:`, hook `sql:`,
`options_sql:`).

**Phase 0 — Git & version bookkeeping.** Tag current `main` as `v1.0.0` (project's first
tag; the clean-tree tip is the live 1.0.0). Work on `feature/db-schema-source`; `main`
stays green and identical to 1.0.0 until the cutover. Merge to `main` (bump to **2.0.0**)
only when stable. No long-lived `release/1.0.x` maintenance line unless 1.0 patching is
wanted after 2.0 (default: the tag is enough).

**YAML schema additions** (`internal/types/config.go`, `internal/types/resource.go`):

```yaml
schema:                      # captured by init --db; the sole schema source of truth
  tables:
    - name: orders
      pk: id
      view: false
      columns:
        - { name: id, type: integer, primary_key: true }
        - { name: customer_name, type: string }
      foreign_keys:
        - { column: customer_id, foreign_table: customers, foreign_column: id, label: name }
# and on a relation/dropdown form field instead of options_query:
  - name: role_id
    type: select
    options_sql: "SELECT id, name FROM roles WHERE active = 1"
```

**Phase 1 — Core: DB as source of truth, no sqlc (offline generate):**
1. `internal/types/` — `Schema`/`SchemaTable`/`SchemaColumn`/`SchemaFK` (name, view, pk,
   columns[type,pk], foreign_keys incl. `label`) + `Config.Schema *Schema yaml:"schema"`;
   `Field.OptionsSQL string yaml:"options_sql"`. Keep `SQLCConfig` **parse-only + inert**
   this phase so editor/parser/validate still compile (removed in Phase 2).
2. `cmd/yaga/introspect.go` — convert `[]TableInfo → *types.Schema`; `generateYAML` appends
   a `schema:` block (views stay read-only via existing logic). **Stop writing**
   `sql/migrations/schema.sql` and `sql/queries/*.sql`; fold/drop `generateSchemaSQL`,
   `mssqlTypeToPostgres`, `generateQueries`-as-files, `pkGoType`/`findPKColumn`
   reverse-engineering (Go types are derived in the generator from stored column types; FK
   targets carry their label column for option SQL).
3. `internal/generator/data.go` (new) — from `cfg.Schema`, emit `internal/data` with
   `Get{Resource}(db, ctx, key) (map[string]interface{}, error)` per resource (raw
   `SELECT cols FROM {table} WHERE {pk}=?`, scan into an `interface{}` map). Emit FK option
   SQL: for a relation field without `options_sql`, derive
   `SELECT {options_value},{options_label} FROM {table}` from schema FKs/labels;
   `options_sql` wins when present. `detail.go`/`update.go` swap sqlc struct fields for the
   map result (`itemMap := item`) — removes the one real runtime dependency on sqlc output;
   the `data.New(db).GetX(ctx, idType(id))` call shape and `id_type`/`id_column` overrides
   stay.
4. `internal/generator/generator.go` / `sqlc.go` / `makefile.go` — remove the
   `generateSQLCConfig` call + `SQLC.Config` gate; remove `RunSQLC`, `copySQLFiles`
   (query files retired). `build: css templ` (drop the `sqlc` target and the dependency on
   an external sqlc run).
5. `cmd/yaga/main.go` / `demo.go` — `init` requires `--db`; remove the `init`
   schema-scaffold branch and `--demo`; delete `cmdInitDemo`, `demo.go`, `demo_test.go`;
   update usage text; remove the `RunSQLC()` call in `cmdGenerate`.
6. `internal/generator/handler.go` (`buildOptionsLoader`) — source option SQL from
   `options_sql` or the generated FK SQL (drop `findSQLCQuery` file reads).

**Phase 2 — Editor / Sync cleanup (implemented 2026-08-16):** removed the TUI editor's
`SQLC` query editor (`sqledit.go`/`sqlview.go`), the SQL-file `Sync` screen (`sync.go`)
and their staged `pendingSQL` machinery; removed wedit's `queries` tab, `/api/analyze`
and `/api/queries/{name}` + `PUT /api/queries` endpoints; removed the MCP `analyze` tool;
deleted `internal/schema/queries.go` (`ParseQueries`/`ParseQueriesForFile`/
`RewriteQueryBody`/`SelectColumns`/`Query.RawBody`) and `GenerateQueries` from
`generate.go`. The captured `schema:` block is the sole schema source; Validate is the
single health check. **The query-name YAML fields are kept** — `list.query`,
`count_query`, `detail.query`, `form.*.query`, `populate_query`, `options_query` remain
plain config fields (options_sql/schema-FK resolution is untouched); only the SQLC
query-file body viewers/editors were removed.

**Tests / exit criteria (Phase 1):** `types`/`introspect_test.go` — `[]TableInfo → Schema`
conversion, `schema:` embedded, no `schema.sql`/`queries/*.sql` emitted by
`cmdInitFromDB`; `generator_test.go` — `internal/data` `Get` from a `schema:` block,
detail/update use the map result, no `sqlc.yaml`, no `make sqlc`, no query files in output;
`main.go` — `init` errors without `--db`. Gates: `go build ./...`, `go vet ./...`,
`gofmt -l .`. Risks: map-based detail/update is low-risk (renderers already index maps +
route through `viewmodels.Stringify`); keeping `SQLCConfig`/`internal/schema` inert in
Phase 1 lets legacy `sqlc:` YAML still parse (ignored), removed cleanly in Phase 2.

---

### D12 — Embed pre-built CSS into the yaga binary (drop the Tailwind build step)

**Status: implemented (2026-08-14).** Follows D11 (v2.0.0, no sqlc). Same pattern
as D8's vendored Chart.js: a single pre-built, CSS-variable-based Tailwind stylesheet is
committed into `internal/generator/assets/styles.css`, embedded via `//go:embed`, and
written to the generated project's `static/css/styles.css` at generation time — so the
dashboard builds with no Tailwind binary (and, post-D11, no sqlc): `make build` = `templ`
+ `go build`, fully offline. Decisions taken (2026-08-14): **embed, not hybrid** — no
optional `css`/`get-tailwind` escape hatch in the generated Makefile; **CSS-variable
theming** — the prebuilt CSS is generated with `colors.brand.primary`
→ `rgb(var(--brand-primary-rgb) / <alpha-value>)` (and the same for secondary), where the
`--brand-primary-rgb` channel triplet is emitted alongside the existing `--brand-primary`
hex in `:root` by both `Base` and `LoginPage` (`viewmodels.BrandChannels` at runtime for
Base; the generator-side `hexChannels` for the baked login page). The channel-triplet form
(not a bare `var(--brand-primary)`) is what lets alpha modifiers like
`bg-brand-primary/10` and `hover:text-brand-primary/80` resolve at runtime — a bare
`var()` color silently drops every `/alpha` utility from the compiled CSS; **fonts stay
inline** — `Base`/`LoginPage` already apply `body { font-family }` / `code, pre { font-family }` via
inline `<style>` (not Tailwind utilities), so custom fonts keep working untouched and the
prebuilt config adds no `fontFamily` extend (`font-mono` comes from Tailwind defaults);
**bounded grid/max-w knobs** — the three config-driven class names (`lg:grid-cols-N` card
view, `lg:grid-cols-N` stats_grid, `max-w-{V}` from `max_content_width`) are covered by a
safelist superset and validated with **silent clamp + warning** (columns → [1,12]; unknown
`max_content_width` → `max-w-none` fallback + warn); **guarded regen** — a repo `make
styles` target regenerates the asset and a coverage test fails loudly if generator templates
ever emit a class missing from the embedded CSS.

**Embedded asset** (`internal/generator/tailwind.go`): commit minified
`internal/generator/assets/styles.css` + `//go:embed assets/styles.css` →
`embeddedStylesCSS`; `generateAssets()` → `writeChartJS()` + `writeStylesCSS()` (writes the
embedded bytes to `static/css/styles.css`); `ensureDirs` adds `static/css` and drops
`internal/assets/css`; delete `generateTailwindCSS`, `generateStaticAssets`, `fontStack`,
`RunTailwind` + their now-unused imports.

**Config-driven classes** (`internal/parser/validator.go`): clamp `card.columns` and
stats_grid widget `columns` to [1,12] (card currently only clamps `<1→4`; widgets have no
bounds today); validate `max_content_width` against the allowlist, unknown values fall back
to `max-w-none` with a warning. Behavior change: values >12 previously emitted arbitrary
Tailwind classes.

**Generated Makefile** (`internal/generator/makefile.go`, post-D11): remove the
`css`/`get-tailwind` targets, `TAILWIND`/`TAILWIND_VERSION` vars and the
`tailwindStandaloneVersion` const; `build: css templ` → `build: templ`.

**CLI** (`cmd/yaga/main.go`): `cmdGenerate` drops the `RunTailwind()` call + warning (D11
already removed `RunSQLC()`); Next-steps becomes `make` / `make run`.

**Rebuild tooling** (yaga repo, dev workflow only): root `Makefile` gains
`make styles` → `scripts/build-styles.sh`; `scripts/styles.tailwind.config.js` holds the
var-based colors + safelist `{ pattern: /grid-cols-(1|…|12)/, variants: ['sm','md','lg'] }`
and the explicit `max-w-*` allowlist; the script generates a project from the kitchen-sink
fixture offline, drops in the config + `@tailwind` input, runs the pinned tailwind v3.4.19
standalone (same OS/arch mapping as today's `get-tailwind`), and copies the minified output
to `internal/generator/assets/styles.css`. No sqlc, no sql fixtures.

**Kitchen-sink fixture** `testdata/kitchen.yaml`: hand-authored post-D11 YAML with a
`schema:` block and `options_sql:` (no `sql/queries` files), covering list/card (grid +
kanban)/detail/create/update/actions/bulk/policies/hooks, all widget types, pickers,
`file`/`image`/`json`/`gps` fields, `card.columns: 12`, stats_grid `columns: 12`,
`max_content_width: 7xl` — every class the generator can emit appears literally in the
generated `.templ`.

**Coverage guard test** (`internal/generator/styles_test.go`): generate from the fixture,
extract every `class="…"` token from the emitted `.templ` files, and assert each token + the
full safelist + `var(--brand-primary)`/`var(--brand-secondary)` presence exist in
`embeddedStylesCSS`.

**Touch points:**
1. `internal/generator/tailwind.go` — embed + write the asset; delete the tailwind emitters / `RunTailwind`.
2. `internal/generator/makefile.go` — `build: templ`, drop css/get-tailwind/TAILWIND.
3. `internal/parser/validator.go` — column/max-w clamps + warnings.
4. `cmd/yaga/main.go` — remove `RunTailwind()` + next-steps text.
5. `internal/generator/generator.go` — `ensureDirs` (add `static/css`, drop `internal/assets/css`).
6. `internal/generator/styles_test.go` + `testdata/kitchen.yaml` — guard test + fixture.
7. `scripts/styles.tailwind.config.js` + `scripts/build-styles.sh` + root `Makefile` — regen tooling.
8. Docs — `SPEC.md`, `README.md`, `TESTs.md`, `AGENTS.md` (drop `make css`/`get-tailwind`).

**Tests / exit criteria:** guard test green (every generated class token present in the
embedded CSS); `generator_test.go` updated (`TestGenerateMakefile` asserts `build: templ`,
no TAILWIND/get-tailwind lines; the tailwind.config.js assertion becomes
`static/css/styles.css` == embedded bytes); `make styles` regenerates the asset; demo/kitchen
project `make build` succeeds **with no tailwind binary on PATH** and serves
`/static/css/styles.css` honoring a custom brand color (via CSS vars). Gates:
`go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .`. Regression guard: feature-off
output stays byte-identical except the CSS asset + Makefile.

---

### D13 — List/Card filter section

**Status: implemented (2026-08-14).** A YAML-defined filter on list and card
views: a collapsible filter section above the table/cards that builds an arbitrary
AND/OR filtering combination over the resource's columns, with runtime-valued
`$N` parameters. Decisions taken (2026-08-11): **one filter per view** (`list.filter`,
`card.filter` — the "multiple filters per view" idea was stepped down); expression is a
**mini-DSL** compiled at generation time to dialect-correct SQL (no raw SQL in YAML);
`$N` values are collected via **inline labeled inputs** in the filter form and travel in
URL query params (`filter=1`, `fp_<name>=<value>`); an empty param on Apply **skips the
filter** (like an empty search box).

**YAML schema** (`internal/types/resource.go`: `Filter *FilterConfig` on `ListConfig`
and `CardConfig`):

```yaml
list:
  filter:
    label: "Advanced filter"               # shown in the collapsible header
    where: "(price > 1000 and prod_name contains 'abc') or prod_code = $1"
    params:                                # optional; defaults to p<N> / "Value N"
      - name: code
        label: "Product code"
```

**DSL** (new package `internal/filterexpr/`, shared by generator + schema refs; standard
SQL precedence — AND binds tighter than OR — plus parentheses):

```
expr      := or
or        := and ( "and" and )*
and       := primary ( "or" primary )*
primary   := "(" expr ")" | condition
condition := column OP [value]
column    := [A-Za-z_][A-Za-z0-9_]*          # emitted with the `t.` colPrefix when FK joins exist
OP        := =  !=  <>  <  <=  >  >=  | contains | not_contains | is_null | is_not_null
value     := number | 'quoted string' ('' escapes) | $N
```

Driver mapping: `contains` → `ILIKE` (pg) / `LIKE` (sqlite, mssql), `not_contains` →
`NOT ILIKE`/`NOT LIKE`; literal values baked into the emitted SQL string; `$N` becomes a
runtime placeholder token (`__GFP__` in the emitted source, replaced at request time with
`?` / `$<argIdx>` per occurrence in SQL-text order, so `$2` before `$1` binds correctly);
`contains` binds are runtime-wrapped `"%" + v + "%"`. Deliberately excluded from the DSL:
`in`, `between`, `not`, param `type:` (values pass as strings; DB coerces — same class as
the existing search args, documented caveat).

**Runtime behavior:** Apply sets `filter=1` + `fp_*` params; the handler builds filter
WHERE fragments *before* the search block (sqlite binds positionally — placement matches
WHERE text order) reusing the existing `argIdx` numbering on pg/mssql; final WHERE =
`(<search ORed>) AND (<filters ANDed>)` via a `parts` join that degrades to today's exact
behavior when no filter exists. Missing/empty param → that filter block is skipped.
Pagination echoes `&filter=1&fp_x=...` so filters survive page changes (extend the shared
`pagination(...)` templ with a `filterQS` string arg). CSV export deliberately untouched
(no filter support in v1, mirrors its existing lack of search). Security posture intact:
only columns/literals from the trusted YAML and bound param values reach the SQL.

**Touch points:**
1. `internal/types/resource.go` — `FilterConfig`/`FilterParam`; `Filter` on list/card.
2. `internal/filterexpr/` (new) — parser + compile: `SQL(driver, colPrefix)` →
   (frag, placeholders in text order, param usage), `Columns()` for validation.
3. `internal/generator/handler.go` — filter build block per driver in
   `generateListHandler`/`generateCardHandler` (token replace, contains-wrapped args,
   whole-filter-skip on empty param), `net/url` import (only when a filter exists),
   `filterClauses` + `parts` WHERE join, `FilterData` population.
4. `internal/generator/viewmodels.go` — `FilterData`/`FilterParamData` (`Key`,
   `Label`, `Value`) + `Applied` flag on `ListData`/`CardData`.
5. `internal/generator/templ.go` — collapsible filter section in
   `generateListTempl`/`generateCardTempl` (toggle + GET form echoing
   `search/sort/order`, prefilled param inputs, Apply/Clear); `pagination(...)`
   gains `filterQS`. No-filter resources emit nothing new.
6. `internal/parser/validator.go` — `where` required; params count ≥ max `$N` when
   `params` present; param names non-empty + unique.
7. `internal/schema/references.go` — record columns referenced by
   `list.filter`/`card.filter` (Section `"list.filter"`, `"card.filter"`) so the editor
   Validate screen flags missing columns; `cmd/yaga/editor/validate.go` goTo mapping.
8. `cmd/yaga/editor/` — list/card sub-editor gains a "Filter" page (label/where
   text inputs + name/label params list, reusing `listSpec`).
9. `cmd/yaga/ai_spec.md` (cheat-sheet example), `cmd/yaga/demo.go` (one demo
   resource, e.g. orders `status = $1`, to exercise the feature end-to-end).
10. Docs — `SPEC.md` (schema + DSL), `README.md`, `TESTs.md`, `AGENTS.md`.

**Tests / exit criteria:** `internal/filterexpr` unit tests (grammar, precedence,
`$2`-before-`$1` ordering, pg/sqlite/mssql SQL output, contains arg wrapping, column
extraction, qualification); generator tests via `assertGeneratedGoParses` (emitted
fragments per driver, token-replacement code, missing-param skip, literal-only filter,
pagination arg; existing no-filter tests stay green); parser validation tests;
editor goTo test. Gates: `go build ./...`, `go vet ./...`, `go test ./...`,
`gofmt -l .`; E2E — `init --demo` → demo YAML filter → `generate` → `make` → login,
exercise filter/collapse/pagination. Regression guard: filter-off output stays
byte-identical.

---

### D14 — Master-detail (header + child lines) navigation

**Status: implemented (2026-08-15).** Opt-in master-detail support for
document-oriented resources (Order/Invoice etc.) built from two related tables with a
1 → many relation: one header table (e.g. `orders`) plus many child-line records that
carry an FK to the header (e.g. `order_lines.order_id → orders.id`). The header's
**detail** and **edit** views embed a read-only table of the header's lines; each line
links to the child's own pre-bound detail/edit; "Add line" opens a pre-bound child
create form. Decisions taken (2026-08-14): **nested navigation** (not an inline
editable grid in the same form — each POST stays a single record, keeping the form
engine and per-record hooks/audit/bulk untouched); relationships are declared by an
**explicit `children:` block** on the header resource **and auto-emitted by
`init --db`** (`--demo` no longer exists — it was removed in D11, `init --db` is the
only init path); the child resource **stays fully independent** — it keeps its own
list/detail/form, appears in navigation, and its FK is only locked when opened from a
parent context.

**YAML schema** (`internal/types/resource.go`: `Children []ChildResource` on
`Resource`):

```yaml
- name: Order
  label: Orders
  table: orders
  children:
    - name: Lines                # optional section heading (default: child Label)
      resource: OrderLine        # required: child resource name
      column: order_id           # optional; auto-derived from schema reverse-FK
      columns:                   # optional; default = child's list.columns
        - { name: product_id_label, label: "Product" }
        - { name: qty, label: "Qty", type: integer }
        - { name: total, label: "Total", type: float }
```

The parent→child relationship is **derived** at generation time from the existing
`schema:` block — there is no new schema metadata. `SchemaTable.ForeignKeys`
(`types/schema.go`) already records the child→parent FK (`order_lines.order_id →
orders.id`), so the generator scans every `schema.tables[].foreign_keys` for an FK
whose `ForeignTable`/`ForeignColumn` match the parent's table/primary key.

**Behaviour**
- **Detail view (a):** header fields, then a `<section>` titled "Lines" listing the
  child rows (`SELECT <cols>, <childID> FROM <childTable> WHERE <fk> = <headerId>`,
  driver-aware placeholder). Each row links to the child's detail ("View").
- **Edit form (b):** header fields, then the "Lines" table with per-line **Edit**
  (→ child edit, FK locked) and **Delete** (→ child delete, postback returns to this
  header edit), plus an **"Add Line"** button (→ child create with
  `<column>=<headerId>`). When adding a new line the FK is auto-sourced from the header
  id (hidden value); when editing an existing line the FK is preserved (rendered
  read-only, hidden value still submitted, Browse suppressed).
- **Create form:** lines section is unavailable (no header id yet) — emits only an
  informational note "Save the header, then add lines.".
- **Child independence:** opened from its own list, the child FK remains an editable
  picker; opened from a parent context it is seeded + locked.
- **Return navigation:** child create/update/delete POST handlers accept an optional
  `?return=<panel>/<child>/<id>/edit` (or raw URL) used as the redirect target;
  default behaviour (redirect to the child list) is unchanged when absent.

**Touch points:**
1. `internal/types/resource.go` — `ChildResource{Name, Resource, Column, Columns
   []Column}`; `Children []ChildResource` on `Resource`.
2. `internal/generator/handler.go` — reverse-FK helper `childRels(parent)` (scan
   `schema.tables[].foreign_keys` for target = parent table/pk → child resource, FK
   column, child table); `generateDetailHandler` + `generateUpdateHandler` GET load
   child lines into the viewmodel (raw `SELECT … WHERE fk = ?`, `scanFields`-style, no
   windowed `_total`); `generateCreateHandler`/`generateUpdateHandler`/`generateDeleteHandler`
   seed + lock the FK field when `?<column>=<parentId>` is present and honor `?return=`.
3. `internal/generator/viewmodels.go` — `ChildLinesData{Heading, Resource,
   ResourceLower, IDColumn, FKColumn, ParentID, PanelPath, CSRFToken, Fields
   []ColumnDef, Rows []map[string]interface{}, Count int}`; `Lines []ChildLinesData`
   on both `DetailData` and `FormData`; `Locked bool` on `ColumnDef`.
4. `internal/generator/templ.go` — child-lines `<section>` in `generateDetailTempl`
   and `generateFormTempl` (edit only; informational note on create);
   `pickerMarkup` skips the Browse button + script when the field def is `Locked`
   (still emits the hidden input + read-only display). Reuse existing Tailwind classes
   only so `TestGenerateStylesEmbedded` stays green (no `make styles`).
5. `internal/parser/validator.go` — `children.resource` must exist; `column` (or the
   derived FK) must be an FK on the child's schema table pointing at the parent's
   table/pk; `columns` must reference child schema columns.
6. `cmd/yaga/introspect.go` — in `writeResourceYAML` (after the update block, ~line
   1077): for each non-view table with an FK targeting this table emit a `children:`
   entry (`resource: toSingularPascal(child.name)`, `column: <local FK column>`);
   default column list omitted so the generator derives it from the child resource.
7. `cmd/yaga/editor/` — per-resource **Lines / Children** screen (keyed `tview.List`
   by `name`; column sub-editor reuses the column-page patterns; `mergeYAML` already
   keys item lists by `name`); canonical path `Resources/<res>/Children` + resolver in
   `nav.go`.
8. Docs — `SPEC.md` (schema), `README.md`, `AGENTS.md`, `cmd/yaga/ai_spec.md`; add an
   `OrderLine` child to `testdata/kitchen.yaml`.

**Tests / exit criteria:** generator tests — children-free output stays byte-identical
(regression guard) + a child-lines config exercising the derived `SELECT…WHERE fk`,
FK seed/lock emission and `?return=` redirect; parser validation tests (valid + invalid
`children:` blocks); editor nav test for `Resources/<res>/Children`; an
`introspect_test` asserting `init --db` emits a `children:` block for a table with an FK
targeting it. Gates: `go build ./...`, `go vet ./...`, `go test ./...`,
`gofmt -l .`; E2E — `init --db` on a header/line schema → `generate` → `make` →
login, exercise header detail with lines, edit-lines pre-bound FK, add-line, delete-line
returning to the header edit (sqlite; driver-aware placeholders for postgres/mssql).

Out of scope: inline multi-row grid editing, delete-cascade of lines on header delete
(DB-enforced FK), and CSV export of a header together with its lines.

---

**Phase E — Mobile & scripting roadmap**

Status: planned (2026-08-14). Mobile device support (**E1**) and Lua scripting
(**E2**) were moved out of Phase D into their own phase (implementation order
E1 → E2; each is independent — no sqlc, DB or layout coupling). **E1's approach
was rewritten (2026-08-16)**: the original server-side UA detection + separate
mobile templ views plan is superseded by an always-on REST/JSON CRUD API on the
generated dashboard plus a generated React Native/Expo app driven by a
spec-derived manifest (`visible_on_mobile` is retained but consumed by the app,
not a UA-sniffing middleware). Editor-side tooling (**E6**, spec 2026-08-16)
was added: a Lua syntax checker surfaced through validation plus a wedit-only
dry-run debugger — Lua `script:` bodies and SQL hook/action/procedure bodies —
against a throwaway in-memory sqlite stub seeded (on explicit refresh) with up
to 100 rows per live table.

| Item | Status |
|---|---|
| Mobile device support (always-on REST/JSON CRUD API at `{panel}/api` with Bearer-token auth + generated React Native/Expo app, manifest-driven screens, `visible_on_mobile` nav filter) | Planned (E1), spec rewritten 2026-08-16 |
| Lua scripting for actions & hooks (gopher-lua, `script:` body, `ctx` scope, `db.*`/`abort`/`log` host API) | Planned (E2), implemented 2026-08-14 — see `internal/generator/luascript.go` and `docs/Lua-for-Yaga-guide.md` |
| Editor-side check + debug dry-run (Lua syntax check in `yaga validate`/Validate/MCP validate; wedit `/api/lua-run` + `/api/sql-run` against an in-memory sqlite stub seeded on explicit refresh) | Planned (E6), spec 2026-08-16 |
| Virtual computed fields (per-list/detail/card SQL expressions, per-driver `helpers.*` functions, CTE-wrapper filter support) | Planned (E7), spec 2026-08-28 |

Phase E was extended (2026-08-28) with **E7 — virtual computed fields**: read-only,
expression-derived columns on list/detail/card views computed at generation time from a
small set of **per-driver SQL helper functions** (`helpers.date_diff`, `helpers.year_diff`,
`helpers.coalesce`, …). They are **not sortable** (kept out of `validSorts`), are
**filterable** in list/card `filter.where` via a CTE wrapper, and never touch the DB
schema — no stored columns, no migrations, no `data.go`/`Querier` changes (compute happens
in the handler). Zero computed fields keeps the generated output byte-identical, guarded
by a regression test.

---

### E1 — Mobile device support

**Status: planned (2026-08-16), not started.** The original approach — server-side UA
detection + separate mobile templ views, `visible_on_mobile` nav filter, mobile
list/card/detail/form templs, load-more fragments, kanban column pages (spec 2026-08-14) —
is **superseded (2026-08-16)** by a two-part design: the generated dashboard ships an
**always-on REST/JSON API** for CRUD on resources, and a **React Native/Expo app** generated
from the spec YAML dashboard definition consumes it. The app replaces the "mobile browser"
experience; there is **no `MobileMiddleware`, no `mobile bool` in `layoutviews.Base(...)`**.
Decisions taken (2026-08-16): the API is **always-on** (no config toggle — `api_tokens` DDL
and routes are unconditional); the RN app is **manifest-driven** (one generic set of screens
driven by a generated `manifest.json`, not per-resource codegen); `visible_on_mobile` lives on
`NavigationItem` and is consumed by the app's generated navigation (default **true / opt-out** —
existing configs show everything in the app, deviating from the literal "only True shown"
reading, ⚠️ open to veto during review).

**YAML schema** (`internal/types/config.go`: `VisibleOnMobile *bool` on `NavigationItem`,
`yaml:"visible_on_mobile"`):

```yaml
navigation:
  - group: "Sales"
    items:
      - resource: Order          # visible on mobile (default)
      - resource: OrderLine
        visible_on_mobile: false # hidden on mobile
```

Generator semantics: `nil`/absent → visible; `false` → hidden. Parser accepts `true`/`false`
only. The generated mobile manifest omits flagged items and drops groups whose items are all
hidden. Desktop nav and all other behavior unchanged.

#### Part 1 — REST API (always-on)

**Auth (token-based):** a driver-aware `api_tokens` table (DDL emitted into
`sql/migrations/api_tokens.sql`, mirroring the audit DDL): `token_hash` PK, `user_id`,
`created_at`, `expires_at`, `last_used_at`. `POST {panel}/api/login` (`{email, password}`)
verifies via bcrypt (reusing the login query), issues a random 32-byte token (SHA-256 hash
stored) and returns `{token, role, name}`. `POST {panel}/api/logout` revokes. The generated
`TokenAuthMiddleware` reads `Authorization: Bearer <token>`, resolves the user and populates
the **same context keys as `AuthMiddleware`** (`UserKey`/`UserRoleKey`/`UserNameKey`),
returning a JSON **401** (not the login 302 redirect), so the existing `RBACMiddleware` and
`audit.UserID`/`UserName` work unchanged. API routes are CSRF-exempt (Bearer tokens are
CSRF-safe).

**Routes** (mounted at `{panel}/api`, registered inside the existing single `r.Route` panel
block **before** `r.Use(auth.CSRFMiddleware)` — chi applies middleware only to routes
registered after `Use`, so the API subtree skips CSRF while the desktop HTML handlers stay
byte-identical; no second `r.Route` — panics on duplicate path):

| Method | Path | Notes |
|---|---|---|
| POST | `{panel}/api/login` | public |
| POST | `{panel}/api/logout` | revoke |
| GET | `{panel}/api/{res}` | list — reuses the list SQL core (FK label joins, search, D13 filter, windowed `COUNT(*) OVER() AS _total`); params `page/search/sort/order/filter&fp_*`; returns `{data, total, page, per_page, total_pages}` |
| GET | `{panel}/api/{res}/{id}` | detail (via the generated `data.Get*`) |
| POST | `{panel}/api/{res}` | create — reuses `buildCreateParams` (bcrypt, bool coercion) |
| PUT | `{panel}/api/{res}/{id}` | update |
| DELETE | `{panel}/api/{res}/{id}` | delete |
| POST | `{panel}/api/{res}/{id}/action/{action}` | custom actions (raw-SQL / stored-proc dispatch) |
| POST | `{panel}/api/{res}/bulk/{action}` | transactional bulk |
| GET | `{panel}/api/{res}/options/{field}` | runtime option / relation-picker lists (reuses the option-SQL loader) |

Every resource route is wrapped with `auth.RBACMiddleware(<res>, <action>)` when the resource
has policies (JSON 403 via the existing middleware). JSON serialization goes through a new
`viewmodels.JSONValue(v)` emitted into the generated app: `time.Time`/`sql.NullTime` →
RFC3339, other `sql.Null*` → value or `null`, `[]byte` → base64, scalars passthrough.
`httperr` gains JSON variants so API errors return JSON without leaking internals.

#### Part 2 — Generated Expo app (`admin/mobile/`)

The generated project gains a `mobile/` subdirectory (Expo managed workflow) — the repo's
first node dependency, opt-in (`cd admin/mobile && npm install && npx expo start`); the
dashboard `make` workflow stays node-free. Emitted files: `package.json` (expo, react-native,
react-navigation, expo-secure-store, expo-document/image-picker), `app.json` (Expo config
from the panel name), `App.js` (login stack + authenticated navigator), `src/api.js` (Bearer
client, base URL via `EXPO_PUBLIC_API_URL`), `src/theme.js` (brand colors + RGB channels,
dark mode, fonts), one **generic manifest-driven** set of screens
(`src/screens/{Login,ResourceList,ResourceDetail,ResourceForm,Cards}Screen.js`), and
**`src/manifest.json`** — the only spec-derived artifact: panel name + API path, theme,
navigation filtered by `visible_on_mobile`, and per-resource screen config (list columns +
searchable/sortable, detail fields, form fields with types + inline options, cards/kanban,
actions, children, D13 filters). Relation/select pickers fetch their options from the options
endpoint at form-open time; list screens use `page`-based load-more pagination against the
list endpoint.

#### Deferred within E1
CSV import/export, bulk-action multi-select, file/image upload (expo-document/image-picker),
token refresh / expiry UX (secure storage).

**Editor / AI / validation:** navigation-item editor (`cmd/yaga/editor/menu.go` `navItemPage`)
gains a "Visible on mobile" yes/no field; `cmd/yaga/ai_spec.md` cheat-sheet gains the flag;
parser/validator test for parse + default.

**Touch points:**
1. `internal/types/config.go` — `VisibleOnMobile *bool` on `NavigationItem`.
2. `internal/parser/validator.go` — flag validation (`true`/`false` only).
3. New `internal/generator/api.go` — `internal/panel/api` package (login/logout,
   `TokenAuthMiddleware`, JSON helpers, `api_tokens` DDL) + per-resource API handlers
   reusing the SQL cores.
4. `internal/generator/viewmodels.go` — emit `JSONValue`; JSON `httperr` variants.
5. `internal/generator/router.go` — register the `/api` subtree before
   `r.Use(auth.CSRFMiddleware)`.
6. New `internal/generator/mobile.go` — `mobile/` scaffold + `manifest.json`.
7. `internal/generator/generator.go` — wire api + mobile generation into the pipeline.
8. `cmd/yaga/editor/menu.go` — "Visible on mobile" field; `cmd/yaga/ai_spec.md` line.
9. Docs — `SPEC.md` (schema), `README.md`, `TESTs.md`, `AGENTS.md`.

**Tests / exit criteria:** parser (flag parse + default-true); `JSONValue` unit suite;
`assertGeneratedGoParses` on the API package + desktop output byte-identical (existing
snippet tests stay green); snippet tests — API routes precede the CSRF line, RBAC wrap,
`api_tokens` DDL emitted; runtime e2e on the generated sqlite project (login → Bearer
list/detail/create/update/delete/action/options round-trip, role-denied → 403); manifest
JSON validity + `node --check` on emitted JS when node is present. Gates: `go build ./...`,
`go vet ./...`, `go test ./...`, `gofmt -l .`.

---

### E2 — Lua scripting for actions & hooks (gopher-lua)

**Status: implemented (2026-08-16).** Actions and hooks gain a YAML-embedded
scripting language so admin logic (conditional DB ops, default values, validation
guards) lives in `yaga.yaml` instead of Go stubs, raw `sql:` strings, or DB-dialect
stored procedures. Decisions taken (2026-08-14): **runtime = gopher-lua v1.1.1** —
pure Go, no CGO (matches the modernc/sqlite philosophy), tiny, mature, and
`L.SetContext` gives per-request timeouts with no goja-style interrupt/memory
sandboxing; **executes in the generated dashboard at request time** (like `sql:`/
`proc:` today), with scripts embedded as `%q` string literals in the generated Go
source — the yaga binary itself gains NO dependency; **bare script body** — the script
is the body of a single `run(ctx)` function, wrapped by the runtime, matching "single
function per action/hook"; **runtime-only syntax check** — scripts compile lazily when
the action/hook runs, so typos surface as request-time errors (no generate-time check
in v1; flagged as an easy add-on). `script:` is mutually exclusive with the existing
hook/action fields (parser enforces it), keeping the feature-off output byte-identical.
**Editor parity** — script bodies are editable in the TUI editor, the wedit SPA, and via
agent/MCP. Highlighting is wedit-only: a small embedded JS Lua tokenizer
(keywords/strings/comments/numbers, no new npm/runtime deps); the TUI edits script bodies
in a plain `TextArea`, matching how procedures `sql:` bodies are edited today.

**YAML schema** (`internal/types/hook.go`: `Hook.Script string yaml:"script"`;
`internal/types/resource.go`: `Action.Script string yaml:"script"`):

```yaml
hooks:
  before:
    - name: set_default_status
      script: |
        if ctx.values["status"] == nil then
          ctx.values["status"] = "draft"
        end
actions:
  - name: archive
    script: |
      if ctx.values["status"] == "archived" then
        abort("Already archived")
      end
      db.exec("UPDATE customers SET status = 'archived' WHERE id = ?", ctx.id)
```

**Script model:** each script is the body of one `run(ctx)` function (the generated
runtime wraps it: `function run(ctx) ... end`). `ctx` table: `id` (number), `values`
(map; **in/out** — a before-create/update script can set defaults and the handler
writes them back into the INSERT/UPDATE `vals` slice by column index), `table`,
`action` (`create|update|delete|<actionName>`), `user`, `role`.

**Host API** (registered as Lua globals in the runtime, the only way to touch the DB):
`db.exec(sql, ...)` → affected-row count (errors propagate); `db.query(sql, ...)` →
array of row tables; `db.query_one(sql, ...)` → row table or `nil`; `abort(msg)` →
raises a distinguishable error; `log(msg)` → server-side `log.Printf`. Positional `?`
placeholders are renumbered to `$N` on postgres/mssql and kept as `?` on sqlite
(driver-aware, mirroring the list handlers; `*sql.DB` and `*sql.Tx` both satisfy the
runtime's `Execer` interface). No `os`/`io`/`require`/network access is exposed — the
Lua state only contains the host globals + standard Lua builtins.

**Generated runtime** `internal/panel/luascript/luascript.go` (new file, emitted only
when ≥1 `script:` exists — mirror of `internal/panel/procs`): `Run(ctx, db, script,
scope *Scope, timeout)` with a fixed 5 s `context.WithTimeout` per run
(`L.SetContext`); lua↔go value converters; `IsAbort(err)` distinguishes `abort()` from
real failures. **Audited script actions run against the audit `tx`** (the `Execer`
interface accepts `*sql.Tx`), so the op + audit insert stay inside the single
transaction like raw-SQL actions today; script hooks run against `db`.

**Emission sites:**
1. `internal/generator/hooks.go` — `hookCallsStr` gains the `script:` case
   (`luascript.Run(...)` before/after the op, `IsAbort` → `httperr.BadRequest` with the
   message, real errors → `httperr.Internal`); `hookBlockEmits`/`hasAnyHooks` treat
   scripts like sql hooks; create/update write `ctx.values` back into `vals` by column
   index after a before-script.
2. `internal/generator/handler.go` — `generateActionHandler` emits a script branch in
   the action `case` body (mutually exclusive with `query`/`proc`); action `abort()` →
   redirect to the list with `?flash=<msg>`; audit flag includes script actions
   (`exec != ""` semantics). `generateBulkHandler` loops `luascript.Run` per selected id
   with **no outer tx** (mirrors proc bulk actions).
3. `internal/generator/mod.go` — add `github.com/yuin/gopher-lua v1.1.1` to the generated
   `go.mod` **only when a script exists** (conditional, like `driverDep`), so
   feature-off output stays byte-identical and `go mod tidy` never strips it.
4. `internal/parser/validator.go` — "exactly one of fn, sql, proc, script" for hooks;
   `validateAction` becomes query/proc/script mutual exclusion.
5. `internal/panel/httperr` (`httperr.go`) — add `BadRequest(w, msg string)` (safe:
   only trusted config-author text reaches it).
6. Editors — script-body editing everywhere the config is edited: `cmd/yaga/editor/` —
   plain `TextArea` on action pages (`actionPage` in `actions.go`) + hook items
   (`hooksPage`), via the existing `long` helper (in-memory, in the action/hook page
   form); `internal/serve/static/app.js` — `script` added to `ACTION_SCHEMA` + hook-script
   editing with a Lua-highlighted textarea (small embedded JS tokenizer, no npm deps);
   `cmd/yaga/ai_spec.md` cheat-sheet line documenting `script:` and the switch-to-script
   workflow (null the old `fn`/`sql`/`proc` field first — `merge_yaml_fragment` null
   leaves targets untouched); `testdata/kitchen.yaml` gains one scripted action to
   exercise the feature.
7. Docs — `SPEC.md` (schema + host API), `README.md`, `TESTs.md`, `AGENTS.md`.

**Tests / exit criteria:** parser mut-ex (`fn/sql/proc/script`, query/proc/script);
generator snippets — conditional go.mod dep, `luascript.go` emitted only when a script
exists, hook/action/bulk/audit emission, `ctx.values` write-back, abort paths, all via
`assertGeneratedGoParses`; byte-identical feature-off regression (existing hook/action
tests stay green); MCP — `get_value`/`set_value`/`merge_yaml_fragment` on
`resources/<res>/actions/<name>/script` and a hook `script` path resolve and round-trip,
a both-present mut-ex edit stays `isError` (commitOrError rejects), then nulling the old
field and setting the script succeeds; wedit SPA smoke — the script textarea round-trips
through `PUT /api/config`.
Gates: `go build ./...`, `go vet ./...`, `go test ./...`,
`gofmt -l .`; E2E — generated project: before-hook sets a default (visible in the
created row), `abort()` flashes on the list (action) / 400s with the message (hook), a
scripted action using `db.query_one` + `db.exec` works, and an audited script action
writes one `audit_log` row in the same tx.

---

### Order, dependencies & cross-cutting

**D2 → D3 → D5 → D6.** Rationale: D2's audit INSERT weaving and transactional single-row
ops establish the op-wrapping pattern that D3 (transactional import) and D6 (transactional
proc batches) mirror; D3's `buildCreateParams` refactor is a hard dependency for CSV import
reuse; D5 and D6 are independent and smallest — last. **D7 is independent of D2–D6**
(CLI-only, no generator changes; smallest surface) and can slot in alongside any milestone.
**D8 (no-npm build) is independent of D2–D7** — it touches only the build/asset tooling, not
generated-app handlers or the editor.
**D10 (rename to YAGA) is independent of all other phases** and lands last: it renames the
project, module path, CLI, repo and the very docs/`cmd` paths this roadmap references.
**D11 (drop sqlc, DB as sole schema source) is the biggest and most invasive change** — a
v2 model inversion that reworks init/generate, the Makefile, introspection, the `internal/data`
replacement, and (in a follow-up phase) the editor/Sync sqlc surfaces. It lands **after**
D10 and takes priority over subsequent milestones: its removal of the sqlc toolchain touches
many of the same code paths that D13+ build on.
**D12 (pre-built CSS, drop the Tailwind build step) is the direct successor to D11**: it
removes the last external toolchain step from the generated Makefile (`build: css templ` →
`build: templ`) and touches the same asset/build/`makefile.go` surfaces; it also fits the D8
asset-tooling precedent. **D13 (list/card filters) is independent of D2–D12; it should land
after D10 since its docs/code references and example YAML (demo/ai_spec) touch the renamed
paths.** **E1 (mobile device support) is independent of D2–D13 and builds on D11**: the
always-on REST API reuses the schema-derived SQL cores (list SELECT/FK joins, `data.Get*`,
`buildCreateParams`, option loaders) that D11 introduced, and the generated `mobile/` Expo
project is a node/JS sibling of the Go module with no sqlc, DB or layout coupling; the
`{panel}/api` subtree is added inside the single `r.Route` block before the CSRF middleware,
so the desktop HTML output stays byte-identical.
**E2 (Lua scripting) is independent of D2–D13 and E1**: it adds a generated runtime package
(`internal/panel/luascript`), a conditional go.mod dependency and new `script:` fields on
hooks/actions — no sqlc, DB, or layout coupling — so it can slot in alongside any other
milestone; it mirrors the D5 (plugin hook sources) and D6 (procedures) precedent of
embedding logic in `yaga.yaml` and emitting runtime support into the generated app.

Cross-cutting for every milestone: version bump `0.8.0 → 0.9.0`; docs
(`SPEC.md`, `README.md`, `AGENTS.md`) updated per milestone; every milestone ends with
`go build ./...`, `go vet ./...`, `gofmt -l .`, and a templ + tailwind compile of a
generated project. Respect the documented generator gotchas: Sprintf spec counting
(biggest risk — most milestones add emitters), `templ.SafeURL` on every new URL-bearing
attr, no `style={}`/conditional `class={}`, IIFE-wrapped inline scripts, driver-aware
placeholders + sqlite arg order, `idColumn(r)` everywhere, no comments in generated code.
Each milestone carries a **regression guard**: feature-off output stays byte-identical
(snippet-asserted in `generator_test.go`).

---

### E3 — TUI editor polish (color palette, SQL highlighting, input enhancements)

**Status: planned (2026-08-14), not started.** Three targeted improvements to the
terminal-based editor (`cmd/yaga/editor/`) that require no architectural changes: a
modern dark-theme color palette (Catppuccin Mocha), syntax-highlighted SQL in the
query viewer, and enhanced form input widgets (Tab-completion on string fields.
Each is independent; order: style → sqlview
→ widgets. (2026-08-16: **E3b is moot** — the SQL query viewer `sqlview.go` was
deleted with the D11 Phase 2 query-file purge; the remaining items are E3a + E3c.)

**E3a — Color palette (`style.go`)**

Replace the current zinc/warm-gray palette with Catppuccin Mocha colors:

| Variable | Current | New |
|----------|---------|-----|
| `colAccent` | `#6366f1` (indigo) | `#89b4fa` (blue) |
| `colAccentHi` | `#8b5cf6` (violet) | `#b4befe` (lavender) |
| `colText` | `#d4d4d8` (zinc-300) | `#cdd6f4` (text) |
| `colMuted` | `#71717a` (zinc-500) | `#6c7086` (overlay0) |
| `colBorder` | `#3f3f46` (zinc-700) | `#45475a` (surface1) |
| `colBg` | `#1c1917` (warm gray-900) | `#1e1e2e` (base) |
| `colOk` | `#22c55e` (green) | `#a6e3a1` (green) |
| `colWarn` | `#eab308` (yellow) | `#f9e2af` (yellow) |
| `colErr` | `#ef4444` (red) | `#f38ba8` (red) |

Add `colFormBg = #313244` (surface0) and replace the 3 hardcoded `0x27272a`
`SetFieldBackgroundColor` calls in `lists.go`/`menu.go` → `colFormBg`.

Mechanical: 43 color references across 8 files — no logic change, just hex values.

**E3b — SQL syntax highlighting (`sqlview.go`)**

Add `sqlHighlight(raw string) string` that injects tview `[color]...[-:-:-]` tags via
simple regex-based tokenization:
1. Strings: `'...'` (incl. `''` escapes) → `[green]...[-:-:-]`
2. `--`/`/* */` comments → `[#585b70]...[-:-:-]`
3. SQL keywords (SELECT, FROM, WHERE, JOIN, AND, OR, INSERT, UPDATE, DELETE, etc.) →
   `[teal]KEYWORD[-:-:-]`
4. Placeholders (`$N`, `?`) → `[yellow]...[-:-:-]`

Used in `sqlManifest` instead of `tview.Escape` — the existing `SetDynamicColors(true)`
on the `TextView` renders the tags. `sqlRowHeight` uses `tview.TaggedStringWidth` which
strips color tags, so the height estimate stays correct.

**E3c — Input enhancements (`widgets.go`)**

- **`strWithCompletion(form, label, value, set, completions []string)`** — same as `str`
  but also captures Tab key to auto-complete from the provided list (first prefix match).
  Used for: driver field (`"postgres"`, `"sqlite"`, `"mssql"`), icon field (from
  `iconOptions`), table name fields.

**Touch points:** `cmd/yaga/editor/style.go`, `sqlview.go`, `widgets.go`, plus
`SetFieldBackgroundColor` replacements in `lists.go` and `menu.go`. No test changes
needed for colors (no test asserts hex values); add `TestSqlHighlight` for tokenizer;
widget tests are covered by existing sim-screen tests.

**Tests / exit criteria:** `TestSqlHighlight` — keyword coloring, string literal
coloring, comment skipping, placeholder coloring; `TestNumCtrlIncrement` — Ctrl+↑
increments, Ctrl+↓ decrements, stays ≥ 0; go test pass, no regressions in existing
editor tests.

---

 ### E4 — `yaga wedit`: web-based config editor

 **Status: implemented (2026-08-15).** The command is `yaga wedit` (not the
 drafted `serve`), so it is clearly the web version of the YAML editor rather
 than a running generated dashboard; the internal Go package is still
 `internal/serve`. A new `yaga wedit` subcommand that
 starts a local HTTP server with a REST API and an embedded single-page-application
 frontend (vanilla HTML/CSS/JS, no bundler or npm) for editing `yaga.yaml` in a
 browser. The server reuses all existing Go logic (`parser.ValidateAll`,
 `schema.CollectReferences`, etc.)
 — the same functions the TUI editor calls — wrapped in JSON endpoints.
 (2026-08-16 cleanup: the draft's `GET /api/analyze` + `GET/PUT /api/queries`
 SQL-query endpoints and the SPA `queries` tab were removed with the D11
 Phase 2 query-file purge; the SPA's health check is the Validate tab.)

 **Command shape:**
 ```
 yaga wedit              # default :9090, yaga.yaml
 yaga wedit --port 9091  --config path/to/yaga.yaml
 yaga wedit --open       # open browser automatically
 ```

 **Architecture:**
 ```
 yaga wedit
   └─ internal/serve/Server
        ├─ GET  /api/config            → full config JSON
        ├─ PUT  /api/config            → JSON body, validate, return errors
        ├─ GET  /api/validate          → ValidateAll + analyze → findings JSON
        ├─ POST /api/save              → yaml.Marshal + WriteFile to disk
        ├─ GET  /api/analyze           → sync analysis (tables, queries, refs)
        ├─ POST /api/generate-queries  → generate missing SQL query files
        ├─ GET  /api/queries/{name}    → staged-over-disk query body
        ├─ PUT  /api/queries           → stage a query body rewrite (pendingSQL)
         ├─ GET  /api/raw               → raw YAML text
         ├─ PUT  /api/raw               → replace config from raw YAML (validate)
         ├─ GET  /api/rev               → {"rev": N} revision counter
         ├─ GET  /api/events            → SSE stream: event: rev / data: <n>
         └─ GET  /static/*              → embedded SPA (index.html, app.js, style.css)
 ```

 **Go server (`internal/serve/serve.go` + `handlers.go`):**

 - `Server` struct holds in-memory `*types.Config`, `configPath`, `sync.RWMutex`, and
   `pendingSQL map[string]string` (staged SQL edits, same pattern as the TUI's
   `pendingSQL`).
 - Routes use Go 1.22+ `http.ServeMux` method patterns (`"GET /api/config"`,
   `"PUT /api/config"`) — no chi dependency needed.
 - Handlers call the same functions as the TUI editor: `parser.ValidateAll`,
   `schema.ParseQueries`, `schema.CollectReferences`, `schema.GenerateQueries`. The
   config↔JSON bridge is `yaml.Marshal` → generic YAML tree → JSON (and back), so
   the field names the SPA submits match the YAML names exactly; numbers, nulls and
   `-created_at`-style strings round-trip.
 - Save writes `yaml.Marshal(cfg)` to `configPath` and flushes staged SQL files.
 - Static files served via `//go:embed internal/serve/static/*` → `embed.FS`.
 - sqlBase/queriesDir/schemaDir reuse the TUI editor's SQL-tree resolution.

 **Frontend (`internal/serve/static/`, embedded SPA):**

 Three files, no framework, no bundler:

 - **`index.html`** — shell with `<header>` (title, save indicator),
   `<nav id="sidebar">` (tabs matching the TUI nav items), `<main id="content">`,
   `<footer>` (shortcuts hint).
 - **`app.js`** (~700 lines) — vanilla JS: `fetch()` calls to the REST API,
   page-rendering functions (one per section: panel, connections, sqlc, auth,
   navigation, resources, pages, queries, validate, sync, raw), DOM manipulation
   via `innerHTML`. Tab pages: `panel`, `connections`, `sqlc`, `auth`,
   `navigation`, `resources`, `pages`, `queries`, `validate`, `sync`, `raw`.
   Field rendering helpers: `textField()`, `numField()`, `boolField()`,
   `selectField()`, `stringListField()`. Collection editors (resources, columns,
   fields, actions, children, widgets, navigation items) use a shared
   `collectionEditor()` with row add/edit/delete. Validate screen: `GET
   /api/validate` → red/yellow finding rows. SQL queries: `GET /api/analyze` →
   click query name → edit `<textarea>` → staged via `PUT /api/queries` → flushed
   by the global save.
 - **`style.css`** (~250 lines) — Catppuccin Mocha dark theme (same palette as E3a),
   flex layout (sidebar 220px + content), form field styling, status colors.

 **What v1 does NOT do** (deferred to follow-up):
 - No file watcher — click "Reload" to re-fetch config after external edits.
 - No cd-navigation dialog — sidebar tabs are the only navigation.
 - No ASCII preview — the TUI's `preview.go` is terminal-specific; web preview is
   a separate topic.
 - No Monaco editor — raw YAML tab uses a plain `<textarea>`; vendoring Monaco's UMD
   bundle (like Chart.js) is a future add-on.

 **Touch points:**
 1. `cmd/yaga/main.go` — `case "wedit":` + usage line.
 2. `cmd/yaga/wedit.go` (~50 lines) — entry point, `--port`/`--open` flags
    (`--config` comes from the shared global flags; `-p` is not used because
    `parseGlobalFlags` maps it to `--admin-password`), `parser.ParseFile`,
    `serve.New(cfg, configPath, serve.Options{...}).Start()`.
 3. `internal/serve/serve.go` — `Server` struct, route registration,
    `Start()` (graceful shutdown via `signal.NotifyContext`), static file
    serving via `embed.FS`.
 4. `internal/serve/handlers.go` — REST handlers + validation helpers.
 5. `internal/serve/static/index.html` — SPA shell.
 6. `internal/serve/static/app.js` — vanilla JS frontend.
 7. `internal/serve/static/style.css` — Catppuccin Mocha theme.
 8. `internal/serve/serve_test.go` — endpoint tests (config GET/PUT round-trip,
    422 on invalid, save writes YAML, raw YAML round-trip, validate/analyze
    findings over a temp sql tree, generate-queries writes without overwriting,
    query stage+flush preserving other query blocks, static serving, sqlBase
    resolution).

 **Tests / exit criteria:** `go vet ./...` / `go build ./...` / `go test ./...` —
 compilation and embed integrity, plus the serve endpoint test suite. No e2e in v1
 (manual smoke: `yaga wedit --config testdata/kitchen.yaml --open` → browser loads,
 panel/auth/resources pages render, edit a field → save → YAML file updated).

 ---

### E5 — MCP over wedit: AI agent config editing endpoint

**Status: implemented (2026-08-16); supersedes the earlier stdio `yaga mcp` command
and is planned, not started.** An MCP (Model Context Protocol) server that
exposes the yaga config and editing operations as structured tools, resources,
and prompts over JSON-RPC 2.0. Instead of a separate `yaga mcp` subprocess, the
MCP protocol rides the **existing `yaga wedit` HTTP server as an endpoint**
(`POST /mcp`, Streamable HTTP transport) — wedit already runs a server and owns
the in-memory config, so MCP becomes "just another endpoint" sharing that state.
AI agents (e.g. Opencode) connect with a remote-URL MCP config — no new CLI
command, no full-config serialization to an LLM. (2026-08-16 cleanup: the draft
`analyze` tool was dropped with the D11 Phase 2 query-file purge; the toolset is
`validate`/`save`/`open`/`get_config`/`get_value`/`list_resources`/
`list_navigation`/`set_value`/`merge_yaml_fragment`/`add_resource`/
`remove_resource`/`add_column`/`add_field`/`add_nav_item`/`remove_nav_item`.)

**Command shape (unchanged — wedit simply gains a route):**
```
yaga wedit                       # also serves MCP at POST /mcp
```
Client-side registration in `opencode.json`:
```json
{ "mcp": { "yaga": { "type": "remote", "url": "http://localhost:9090/mcp" } } }
```

**Transport:** MCP **Streamable HTTP** — JSON-RPC 2.0 over `POST /mcp`
(`Content-Type: application/json`, `Accept: application/json, text/event-stream`).
All tools are one-shot and synchronous, so the server answers plain
`application/json` (no SSE streaming, no session bookkeeping emitted). `GET /mcp`
answers 405 or an SSE 200 for spec compliance. `initialize` /
`notifications/initialized`, `ping`, `tools/list`, `tools/call`,
`resources/list`/`read`, `prompts/list`/`get` are implemented. Zero new Go
dependencies (`encoding/json` is stdlib). **State sharing:** MCP writes go to
the same in-memory `*types.Config` the SPA edits — every mutating path (MCP
`Commit`, `PUT /api/config`, `PUT /api/raw`, `POST /api/fix`) funnels through
`Server.replaceConfig`, which bumps a `rev` counter. The SPA holds one
`EventSource("/api/events")` and reloads on `event: rev`, unless it has unsaved
edits (one-shot toast via `state.warnedRev`); Save / Raw-Apply first compare
`GET /api/rev` to `state.rev` and open a stale-write Overwrite/Reload modal on a
mismatch, so a stale agent/browser can't silently clobber newer changes.

**Layering:**

| File | Purpose |
|------|---------|
| `internal/mcp/mcp.go` | `Server` — JSON-RPC 2.0 dispatch, MCP method surface, capabilities (transport-agnostic) |
| `internal/mcp/tools.go` | all tool handler implementations |
| `internal/mcp/path.go` | case-insensitive `nav.go`-style path resolution on the yaml.Node tree + get/set/add/remove helpers |
| `internal/mcp/mcp_test.go` | request→response shape tests for every tool |
| `internal/serve/serve.go` | +`POST /mcp` +`GET /mcp` +`GET /api/rev` +`GET /api/events` routes; `replaceConfig`/rev/SSE |
| `internal/serve/mcp.go` | wires `internal/mcp` to the existing `Server` (`s.cfg`, `s.pendingSQL`, `configFromYAML`, `analyze`, shared `save()`) |
| `internal/serve/mcp_test.go` | httptest flow: `initialize` → `tools/list` → `set_value` → `validate` → `save` |

`internal/mcp` depends only on a narrow `State` interface (`GetConfig`,
`SetConfig`, `Save`, `Validate`, `Analyze`); the serve package implements it.
Node-path helpers for get/set/add/remove reuse the surgical `yaml.v3` round-trip
pattern from `internal/fixer` and the AI path — edits edit the node tree, then
`configFromYAML` → `parser.ValidateAll` → `SetConfig` (defaults never injected;
a mutating tool that would leave the config invalid fails with the validator
errors and applies nothing, mirroring `PUT /api/config` 422).

**Tool categories (full set for v1):**

| Category | Tools | Purpose |
|----------|-------|---------|
| Lifecycle | `validate`, `save`, `open {path}`, `analyze` | pre-save check, persist `yaga.yaml` (+ `.bak`) and flush `pendingSQL`, load/switch the in-memory config, schema/query sync report |
| Read | `get_config`, `get_value {path}`, `list_resources`, `list_navigation` | targeted queries |
| Edit (scalar) | `set_value {path, value}` | single field change |
| Edit (structural) | `add_resource`, `remove_resource`, `add_column`, `add_field`, `add_nav_item`, `remove_nav_item` | list mutations on keyed sequences (`name`/`group`/identity keys) |
| Edit (bulk) | `merge_yaml_fragment {yaml}` | AI-generated multi-key edit (reuses the AI path's merge semantics) |

Structural tool list for v1: `add_resource`, `remove_resource`, `add_column`,
`add_field`, `add_nav_item`, `remove_nav_item`. `save` writes disk only after
the config passes validation and backs up the pre-save bytes to the
`<config>.bak` (like `validate --fix`); mutations are in-memory until then.

**Path resolution** on the yaml.Node tree uses the same case-insensitive matching
as the TUI editor — `resources/Customer` → the `Customer` resource,
`navigation/0/items/1` → the second item of the first group, `#<idx>` for
unnamed/duplicate segments; identity keys for keyed sequences (`name` for
resources/pages/columns/fields, `group` then `resource`/`page`/`url` for
navigation items).

**Example agent workflow:**
```
Agent → get_value {path: "panel/name"}                        → "My Admin"
     → set_value {path: "panel/brand/logo", value: "newlogo.jpg"}
     → add_column {resource: "User", column: {name: "created_at", type: "datetime"}}
     → validate                                               → "OK"
     → save                                                   → "Written (backup: yaga.yaml.bak)"
```

**Dependencies:** zero new (stdlib `encoding/json`, `net/http`). **Security:**
wedit already binds `:9090` on all interfaces and `POST /api/save` already writes
files, so `/mcp` adds no new exposure class; optionally default to `127.0.0.1` in
a follow-up.

**Tests / exit criteria:** `internal/mcp/mcp_test.go` — JSON-RPC shape tests for
`initialize`/`tools/list`/`tools/call` and every tool (get/set path round-trip,
structural add/remove, invalid edit returns `isError` + leaves config untouched,
`merge_yaml_fragment`); `internal/serve/mcp_test.go` — httptest of the full
opencode flow against a temp `yaga.yaml` (initialize → tools/list → set_value →
validate → save → assert disk + `.bak`). `go vet ./...` / `go build ./...` /
`go test ./...` all clean; existing wedit/`--fix` suites stay green.

---

### E6 — Check + debug inside edit/wedit (Lua & SQL)

**Status: planned (2026-08-16), not started.** Add the ability to **check** Lua
`script:` bodies (actions + create/update/delete before/after hooks) for syntax
errors and to **debug** them — and the SQL bodies: hook `sql:`, action `query:`,
and sqlite `procedures:` batches — with a dry-run against a throwaway,
**user-seeded** in-memory sqlite DB, instead of only discovering mistakes in the
generated app at runtime. Scope decision (2026-08-16): the debug dry-run uses
**in-memory sqlite** and is **wedit-only** (the TUI gets the Lua syntax *check*
too, but not a Run panel; SQL gets no validation tier at all). SQL decision
(2026-08-16, §D): SQL debugging is **wedit-only** and covers **hook `sql:` +
action `query:` + sqlite `procedures:` batches**; the stub is **empty by
default** and seeded **only on explicit user action** ("Refresh sample data")
with the **first 100 rows (max) of each `schema:` table**.

Two tiers + a data plane:

- **Check (Tier 1, Lua only)** — catch Lua syntax errors in five places: `yaga
  validate`, the TUI Validate screen, the wedit Validate tab, MCP `validate`, and
  a per-field **"Check"** button next to each Script editor. There is **no SQL
  tier 1**: a sqlite-based syntax/schema check against pg/mssql-only SQL
  (`ILIKE`, `[x]`, `CALL`/`EXEC`, `::casts`, `OUTPUT INSERTED`) would
  false-negative on perfectly valid configs, so SQL debugging stays a wedit
  dry-run.
- **Debug (Tier 2, wedit only)** — dry-run a Lua **script** or a **SQL body**
  against the shared seeded in-memory sqlite stub, capturing the SQL it would
  issue, result rows, `rows_affected`/`last_insert_id`, Lua `log` output, mutated
  `ctx.values`, and abort/error.

**0. Data plane — seeded in-memory sqlite stub (shared by Lua + SQL).** Fresh
in-memory sqlite via `modernc.org/sqlite` (already a yaga dependency);
`CREATE TABLE` from `cfg.Schema.Tables` with quoted identifiers, **no FK
enforcement** (`PRAGMA foreign_keys` stays off, so row-copy order is irrelevant).
The stub is **empty until asked**. `POST /api/sample-refresh` opens the first
`connections[].dsn` (driver from `connections[].driver`; pgx / mattn /
go-mssqldb are all already yaga deps via `init --db`) and for each `schema:`
table SELECTs **at most 100 rows** — postgres/sqlite `SELECT "c",… FROM "t" LIMIT
100`, mssql `SELECT TOP 100 "c",… FROM "t"` — with the column list from the
schema block; tables missing in the live DB are skipped (not fatal). Values are
coerced to sqlite-friendly storage: `time.Time` → RFC3339 text, `[]byte` → BLOB,
`bool` → 0/1, numerics stay native (sqlite dynamic typing absorbs the rest). The
seeded stub is cached in `Server`; **refresh is explicit only** — the run
endpoints never touch the live DB. No `connections:`/`schema:` or an unreachable
DB → the stub stays empty. **Privacy:** the sample copies real row bytes (incl.
password-hash columns) only into an in-memory DB — never persisted, never
transmitted; the UI displays a note. **Spike item:** verify `modernc.org/sqlite`
binds `$N` positionally like mattn (the generated app's driver); if not, map
numbered `$N` tokens to `?` in statement order via a token-aware pass (the
inverse of `luascript.renumber`).

**A. Shared runtime as a single importable package (foundation).** Move the Lua
runtime out of the emitted `const luaPackageSrc` (`internal/generator/luascript.go`)
into one real package **`internal/generator/luasrc/luascript.go`** (`package
luascript`) containing `Scope`, `Execer`, `Run`, `SyntaxCheck`,
`NewCtx`, `renumber`, `luaQueryArgs`, `goToLua`/`luaToGo`, `abortPrefix`.
`keepQuestion` becomes an exported `SetKeepQuestion(bool)` var (default on). The
generated app gets one `luascript.SetKeepQuestion(...)` line appended in generated
`main.go` (gated); the yaga binary sets it from the config driver. The generator
`//go:embed`s that file and writes it verbatim into the generated
`internal/panel/luascript/luascript.go` (package name already `luascript`); emitted
call sites keep calling `luascript.Run(...)` unchanged. `SyntaxCheck`:
`LoadString("function run(ctx) " + code + "\nend")` → parse each `<line>: <msg>`.
Driving both sides from one file avoids drift, guarded by the existing
`TestGenerateScriptFeatureOff` byte-identical test. **Dependency:** add direct
`github.com/yuin/gopher-lua v1.1.1` to the yaga `go.mod` (currently only the
generated module).

**B. Tier 1 — syntax check surfacing.**

- **Parser:** new `validateScripts(cfg, add)` in `internal/parser/validator.go`
  (wired into `ValidateAll`), walking every `Script != ""` body and emitting a
  non-blocking `parser.Warning` `"<resource>/<hook-or-action> script: <line>: <msg>"`
  per `SyntaxCheck` error. Flows automatically into `yaga validate`, the TUI
  Validate screen, wedit Validate, and MCP `validate`.
- **TUI** (`cmd/yaga/editor/hooks.go`, `actions.go`): a "Check" button under the
  Script `long` field (existing `addButton`/shortcut infra) running
  `luascript.SyntaxCheck` on the current value, showing line-numbered errors in a
  modal; errors also appear on the Validate screen via the parser warning.
- **wedit:** `POST /api/lua-check` `{script}` → `{errors:[{line,message}]}`
  (`internal/serve/handlers.go`, route in `serve.go`); a "Check" button + error
  list under the `luaTextArea` in `static/app.js`.

**C. Tier 2 — Lua debug dry-run (wedit only).** Runs against the shared seeded
stub (§0) through the recording `Execer`, `SetKeepQuestion(true)` so `?` binding
matches sqlite. Documented caveat: mssql/postgres-only SQL inside the script's
`db.*` calls is best-effort — a debug aid, not a correctness guarantee.
`POST /api/lua-run` `{script, id, table, action, values}` →
`{ok, captured:[{sql,args}], log:[], values:{}, error:{line,msg}?}` running
`luascript.Run(ctx, recordingExecer, Scope{...}, script)`. UI: a per-script
debug panel (`id`/`table`/`action` inputs, values JSON) showing captured SQL,
log lines, mutated `ctx.values`, and abort/error.

**D. Tier 2 — SQL debug dry-run (wedit only).** `POST /api/sql-run`
`{kind: "hook"|"action"|"procedure", sql, id, table, action}` — hook/action
carry the SQL text; `procedure` resolves its body from `cfg.Procedures` by name
(only meaningful when the driver is sqlite). Runs against the shared seeded stub
through the recording `Execer`.

- **hook / action:** `ExecContext(sql, {id})` **verbatim** (faithful to the
  generator — hook `sql:` and action `query:` execute as written, `$1` = scope
  ID / row id), then a QueryContext attempt so SELECT-style statements render
  result rows (decision: rows are surfaced). Response
  `{ok, steps:[{sql, args, rows, rows_affected, last_insert_id, error}], driver}`
  — hook/action = a single step.
- **procedure (sqlite):** split the body with the `splitStatements` tokenizer
  copy (§E), run each statement with `$1` = id only when `containsPlaceholder`,
  all statements inside one sqlite transaction, rollback on error — mirrors
  `procs.Exec` in the generated app; one response step per statement, in order;
  a failing step carries `error`, later steps carry `skipped: true`.
- **UI:** the action `query` field becomes a `type:"sql"` field in
  `ACTION_SCHEMA` (textarea + Run button); the action `script` field already
  renders via `luaTextArea` (gains Check + Run under B/C). **Hooks get a minimal
  row editor** (wedit today edits hooks only through the JSON modal, so no Run
  surface exists): name/kind (`fn`/`sql`/`proc`/`script`) + textarea + a **Run**
  button for `sql`/`script` bodies. A shared debug modal for C + D: `id`/`table`/
  `action` prefilled from context, `values` JSON (Lua only), the results table
  (rows for SELECT-style, else `rows_affected`/`last_insert_id`, or `error`), and
  the "sample data: N rows · Refresh" bar (§0) plus the driver-caveat note.

**E. Stub / seed / recording wiring (shared).** `internal/serve/sqlrun.go` (new)
holds `BuildStubDB(cfg)`, `SeedFromDB(dsn, driver, schema, stub)`, the recording
`Execer` wrapper, `RunSQL(stub, sql, id)` + the procedure batch runner, and
**copies** of `splitStatements`/`containsPlaceholder` (duplicated from
`internal/generator/procs.go` by the same convention as its existing generator
copy — parity is unit-tested so the emitted runner and the editor runner stay in
sync). Both `POST /api/lua-run` and `POST /api/sql-run` go through it.

**Decisions taken (2026-08-16):** debug is wedit-only (TUI Run panel is heavier
to render and out of scope for v1); in-memory sqlite for the stub; the stub is
seeded from the **live DB — first 100 rows max per `schema:` table** — and
**only on explicit Refresh** (`POST /api/sample-refresh`), defaulting to empty.
SQL debug covers **hook `sql:` + action `query:` + sqlite `procedures:` batches**;
SELECT-style SQL renders **result rows** alongside `rows_affected`/
`last_insert_id`; there is **no Tier-1 SQL check**. **Out of scope (unless
requested):** MCP `lua_check`/`lua_run`/`sql_run` tools (MCP `validate` already
gains the Lua warnings for free); TUI Run/Refresh panels; seeded-row CRUD or
editing beyond the first-100 window; any real-driver execution at edit time.

**Files:**

| Path | Purpose |
|------|---------|
| `internal/generator/luasrc/luascript.go` | canonical runtime: `Run`, `Scope`, `Execer`, `SyntaxCheck`, `renumber`, conversions (shared by yaga + generated app) |
| `internal/generator/luascript.go` | slimmed to embed + emit `luascript.go` verbatim; `main.go` gains gated `SetKeepQuestion` |
| `internal/generator/procs.go` | unchanged (its splitter copy stays; the new parity test guards ours) |
| `internal/parser/validator.go` | `validateScripts` → `Warning` per Lua syntax error, wired into `ValidateAll` |
| `internal/serve/sqlrun.go` + `_test.go` | stub builder, `SeedFromDB`, recording `Execer`, single/batch SQL runner, splitter/placeholder copies |
| `internal/serve/handlers.go` | `POST /api/lua-check`, `POST /api/lua-run`, `POST /api/sql-run`, `POST /api/sample-refresh` |
| `internal/serve/serve.go` | register those four routes |
| `internal/serve/static/app.js` | hook row editor, `type:"sql"` action field, Check/Run buttons, shared debug modal, sample-data bar |
| `cmd/yaga/editor/hooks.go`, `actions.go` | "Check" button + line-numbered error modal on the Script field |
| `go.mod` | `+github.com/yuin/gopher-lua v1.1.1` |

**Tests / exit criteria:** `internal/generator/luasrc/*_test.go` — `SyntaxCheck`
line numbers, `Run` happy/abort/error, `TestGenerateScriptFeatureOff` stays
byte-identical; `internal/parser` — a config with a malformed script yields a
`Warning` with the path; `internal/serve/serve_test.go` — httptest of
`/api/lua-check` and `/api/lua-run` (captured SQL, log, `ctx.values` mutation,
abort, error) and `/api/sql-run` (hook/action single step, procedure batch +
rollback on a failing statement, SELECT-style rows vs `rows_affected`, `$N`
binding) and `/api/sample-refresh` (at-most-100 per table, missing tables
skipped, unreachable DB → empty-stub fallback, seed cached until the next
Refresh). `go vet ./...` / `go build ./...` / `go test ./...` all clean;
existing wedit/`--fix`/editor suites stay green.

---

### E7 — Virtual computed fields (per-view SQL expressions with per-driver helpers)

**Status: planned (2026-08-28), not started.** Add **read-only, expression-derived
columns** to the list/detail/card views of a resource — e.g. "Warranty days left"
(`helpers.date_diff(warranty_expiry, CURRENT_DATE)`), "Full name", "Order total".
Computations happen **at generation time** via a small set of built-in **per-driver SQL
helper functions**; nothing is stored in the DB, no migrations, no schema-block changes,
and the existing `data.go`/`Querier` surface is untouched (compute runs in the handler).

**Design decisions (confirmed 2026-08-28):**
1. **Helpers** — a curated set of built-in per-driver SQL functions expanded at
   generation time from `helpers.<name>(…)` tokens (regex `helpers\.(\w+)\(([^)]+)\)`,
   args split on commas respecting nested parentheses, nested helpers supported via an
   expand-to-fixpoint loop). The docs call out that **driver differences exist**
   (`date_diff` is `julianday`-based on sqlite, `DATEDIFF` on mssql, `EXTRACT` on
   postgres). Custom expressions use rest-of-SQL verbatim (any dialect feature), so no
   per-driver expression language is needed.
2. **Not sortable** — computed fields never appear in `validSorts` (the runtime `sort`
   column whitelist); their headers render without the sort link. Keeps the ORDER BY
   column list static and the existing whitelist complete.
3. **Filterable** — `list.filter`/`card.filter` `where` expressions **may reference
   computed names**. Because the compiled filter WHERE fragment is emitted as part of the
   data query, references to computed columns (which exist only as SELECT aliases) are
   made visible by wrapping the data query in a **CTE**: `WITH _base AS (SELECT …, <expr>
   AS warranty_days_left …) SELECT …, COUNT(*) OVER() AS _total FROM _base WHERE
   <filter> AND <search>`. The filter's `$N` params and the windowed count work unchanged
   (the `_base` CTE has no ORDER BY/LIMIT, so sqlite/mssql restrictions don't apply).

**YAML shape** — one new optional `computed:` list per `list:`/`detail:`/`card:` block:
```yaml
list:
  columns:
    - name: name
      label: Name
  computed:
    - name: warranty_days_left
      label: Warranty Days
      type: integer
      expression: "helpers.date_diff(warranty_expiry, CURRENT_DATE)"
    - name: full_name
      label: Full Name
      type: string
      expression: "helpers.coalesce(first_name, '') || ' ' || helpers.coalesce(last_name, '')"
```
The `expression` may reference: real table columns (FK label columns via the
pre-existing `{fk}_label` join aliases), other computed fields defined earlier in the same
list (SQL alias scoping — compute in declaration order, no forward refs), and the helper
functions. Field `type` comes from the existing `FieldType` set (`string`, `integer`,
`float`, `boolean`, `datetime`, `date`, `badge`, `email`) so list/card cells reuse the
existing `renderCell` renderers unchanged.

**Helper table (generation-time expansion, per driver from `connections[*].driver`):**

| Helper | Postgres | SQLite | MSSQL |
|---|---|---|---|
| `date_diff(a, b)` | `EXTRACT(DAY FROM ({a})::timestamp - ({b})::timestamp)` | `julianday({a}) - julianday({b})` | `DATEDIFF(DAY, {b}, {a})` |
| `year_diff(a, b)` | `EXTRACT(YEAR FROM age(({a})::timestamp, ({b})::timestamp))` | `CAST((julianday({a}) - julianday({b})) / 365.25 AS INTEGER)` | `DATEDIFF(YEAR, {b}, {a})` |
| `month_diff(a, b)` | `(EXTRACT(YEAR FROM age(({a})::timestamp, ({b})::timestamp)) * 12 + EXTRACT(MONTH FROM age(({a})::timestamp, ({b})::timestamp)))` | `CAST((julianday({a}) - julianday({b})) / 30.44 AS INTEGER)` | `DATEDIFF(MONTH, {b}, {a})` |
| `coalesce(a, b)` | `COALESCE({a}, {b})` | `COALESCE({a}, {b})` | `COALESCE({a}, {b})` |
| `round(a, n)` | `ROUND({a}::numeric, {n})` | `ROUND({a}, {n})` | `ROUND({a}, {n})` |
| `now()` | `NOW()` | `datetime('now')` | `GETDATE()` |
| `ifnull(a, b)` | `COALESCE({a}, {b})` | `IFNULL({a}, {b})` | `ISNULL({a}, {b})` |

**List/card generation (`handler.go`):** when a resource has `computed:` on `list`/`card`,
1. `listSelectFrom()` additionally emits `, <expanded expr> AS <quoted name>` per computed
   field (in declaration order, after the real columns).
2. The computed names are appended to the scan/`colNames` slice **after** the real
   columns, so `scanFields(colNames, true)` picks them up into the row map unchanged.
3. When a computed field exists AND a filter is present, the data query is wrapped in the
   `WITH _base AS (…) SELECT col1, …, COUNT(*) OVER() AS _total FROM _base` CTE (the
   filter/search/order fragments move to the outer query; `_base` stays bare `SELECT`,
   no ORDER BY/LIMIT). When no filter references a computed name, the plain query shape is
   kept (only the `, <expr> AS col` addition) — the CTE is strictly a visibility device for
   the filter, never a semantic requirement.
4. Computed names are **never** added to `validSorts`; the sort header is plain text.

**Detail generation (`handler.go`, `detail.go`):** after `data.Get<Resource>(db, id)`,
when `detail.computed:` has entries the handler calls a generated
`compute<Resource>Row(db, item map[string]interface{})` helper — one `db.QueryRowContext`
`SELECT <expr>, <expr> …` whose `Scan` target names are the computed fields — then loops
`Scan(&item["<name>"], …)` into the same `item` map used by the view (falls back to the
computed field's zero value via `Stringify`-safe `sql.Null*`/nil scan buffers on error).
`data.go` and the `Querier` interface are unchanged.

**Templ generation (`templ.go`):** list headers/cells, card fields and detail rows render
from the same `data.Fields`/`data.Columns` `[]viewmodels.ColumnDef` slices as today — the
handler appends a `ColumnDef` per computed field (`{Name, Label, Type, Computed: true}`).
`renderCell` already handles every allowed computed `type`, so only the **header** changes:
computed columns render a non-sortable `<th>` (no `<a href="?sort=…">`). Detail view appends
computed fields to `DetailData.Fields` in the same way.

**Validation (`internal/parser/validator.go`)** — `validateComputed(cfg, add)`: for each
`computed:` block — empty `name`, duplicate name, unknown `type` (not in `FieldTypes`),
empty `expression` are errors; a `list`/`card` computed name referenced by a
`filter.where` via adapters/`filterColumns` that doesn't exist is a `parser.Warning`
(compile-time WHERE failures are the job of the DB). Computed expressions are not parsed
further (no SQL grammar in the validator) — a fresh-token check that every `<ident>` in
the expression is either a real column (or `{fk}_label`/computed-alias in scope) is a
`Warning`, not an error (drivers differ and custom SQL is free-form).

**Editor integration:** TUI — `computed:` rows editable under
`Resources/<res>/List/Computed`, `…/Detail/Computed`, `…/Card/Computed` (reuses the
`stringMapPage`/column-list patterns; canonical paths registered in `nav.go`); wedit —
`CONFIG_SCHEMA` list/`detail`/`card` objects gain a `computed` array
(`collectionEditor` rows: name/label/type/expression). The generated admin gets no editor UI
change (computed columns are read-only by design).

**Docs:** `SPEC.md` (schema), `docs/USER_GUIDE.md` (a "Computed fields" section with the
helper table + per-driver caveats), `AGENTS.md` (touch-points list).

**Files:**

| Path | Purpose |
|------|---------|
| `internal/types/resource.go` | `ComputedField{Name, Label, Type, Expression}` + `Computed []ComputedField` on `ListConfig`/`DetailConfig`/`CardConfig` |
| `internal/types/field.go` | `FieldTypes` unchanged (computed types reuse it) |
| `internal/generator/helpers.go` (new) | per-driver helper map + `expandHelpers(driver, expr)` (fixpoint regex expansion, comma-split with paren depth, `embedSQL`-safe) |
| `internal/generator/helpers_test.go` (new) | per-driver expansions, nested args, unknown-helper passthrough, paren-comma split |
| `internal/generator/handler.go` | list/card: computed `SELECT` items + scan names + CTE wrapper when filter references computed; detail: `compute<Resource>Row` emission + call |
| `internal/generator/templ.go` | non-sortable computed `<th>` on list/card; detail rows from appended `ColumnDef`s |
| `internal/parser/validator.go` | `validateComputed` (name/type/expression/dupes) + filter-reference warning |
| `cmd/yaga/editor/nav.go` | `Computed` canonical paths for list/detail/card |
| `internal/serve/static/app.js` | `computed` arrays in the wedit tab schema |
| `docs/USER_GUIDE.md`, `AGENTS.md`, `SPEC.md` | computed-fields docs + helper table |

**Feature-off / regression guards:** a config with **no `computed:`** produces
byte-identical generated output (existing `TestGenerateFilter`, `TestGenerateOptionsLoaderDedupe`,
etc. stay green — the new code paths are all gated on `len(computed) > 0`).

**Tests / exit criteria:** `TestExpandHelpers` (all helpers, three drivers, nested
`helpers.` inside args, paren-split with commas inside `coalesce(a, now())`); generator
tests — `TestGenerateListWithComputed` (SELECT items + scan names + non-sortable header),
`TestGenerateDetailWithComputed` (compute helper emitted + called, item map augmented),
`TestGenerateCardWithComputed` (kanban + grid), `TestGenerateFilterWithComputed` (CTE
wrapper with `$N` params + windowed count on the outer query, postgres/sqlite/mssql
variants), `TestGenerateComputedFeatureOff` (byte-identical); parser tests —
`TestValidateComputed` (empty/dup name, unknown type, empty expression, filter reference
to a real column OK + to an unknown computed Warning); `TestComputedFieldRoundTrip`
(types parse/marshal). `go vet ./...` / `go build ./...` / `go test ./...` clean;
E2E — generate a kitchen-style config with a `computed` date_diff/coalesce column, `make`,
assert the column renders (sqlite).
