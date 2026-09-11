# yaga

**yaga** is a YAML-driven admin dashboard generator for Go. Point it at an existing database (`yaga init --db DSN`) and it introspects the schema, writes a declarative `yaga.yaml`, and generates a fully functional admin panel with CRUD resources, card/kanban views, custom pages, widgets, authentication, RBAC, audit logging, CSV import/export, filters and hooks — no boilerplate. Generation runs fully offline: the schema comes from a captured `schema:` block in the config, so there is **no sqlc, no Tailwind binary and no DB connection at build time**.

> 📖 **User guide** — for a practical, end-to-end walkthrough (installation, commands,
> workflow, YAML reference, editors, actions/hooks) see [`docs/USER_GUIDE.md`](docs/USER_GUIDE.md).

## Prerequisites

| Tool | Required for | Notes |
|---|---|---|
| [Go](https://go.dev/dl/) 1.26+ | Running yaga + building the generated app | |
| [Templ](https://templ.dev/) | Compiling `.templ` files in the generated app | Optional — the generated `go.mod` declares `tool github.com/a-h/templ/cmd/templ`, so `go tool templ generate` is handled by the Go toolchain |

No Node.js/npm, no sqlc and no Tailwind binary are required. The Tailwind stylesheet is **pre-built and vendored** into `static/css/styles.css` at generation time, and Chart.js 4.4.1 is embedded into the yaga binary and vendored to `static/js/chart.js` — the running dashboard serves charts locally and needs **no internet at runtime**.

## Quick start

```sh
# Install yaga
go install github.com/MichalHerstus/yaga/cmd/yaga@latest

# Introspect an existing database (the ONLY way to scaffold — the schema is
# captured into a `schema:` block inside yaga.yaml, the sole schema source)
yaga init --db "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
yaga init --db "./mydata.db"                                          # SQLite
yaga init --db "sqlserver://user:pass@localhost:1433?database=mydb"   # MSSQL
#   - creates users/roles tables + a seeded admin user when they are missing
#     (login admin@admin.test / <printed password>, or --admin-password)
#   - writes yaga.yaml with a resource per table + the captured schema block

# Edit yaga.yaml — pick one:
yaga edit                       # interactive TUI editor
yaga wedit                      # web-based editor at :9090 (embedded SPA, preview,
                                #   live sync, and an MCP endpoint for AI agents)
yaga edit --prompt "…"          # AI-assisted edit via OpenRouter / LM Studio

# Generate the admin panel (offline — no DB, no sqlc)
yaga generate                   # produces ./admin/

# Build and run the generated app
cd admin
make build                      # go mod tidy -> templ generate -> go build
make run                        # make run PORT=9090 LOG=err for custom port / error-only logs

# Or run the binary directly:
./admin --port 8080             # --log full (default) | err — err logs only error responses

# Individual steps: make templ / tidy / clean
# Deploy: make package (tar.gz of binary + static + sql + data), extract on
# the target machine and run the binary from the extracted dir.
```

The generated `Makefile` runs every step needed to build the dashboard binary (the `--out` basename becomes both the module name and the binary name): `go mod tidy` → `go tool templ generate` → `go build -o <binary> .`. There is no `css` or `sqlc` step — the stylesheet and Chart.js are already in place. Equivalent manual steps: `go mod tidy`, `go tool templ generate`, `go build -o admin .`.

## Editors

`yaga.yaml` is a plain YAML file, but yaga ships three ways to edit it:

- **TUI editor (`yaga edit`)** — a keyboard-driven terminal UI (3-pane: navigation list + content + status bar) covering every config section. Ctrl+S saves, Ctrl+V validates, Ctrl+P opens a cd-style path navigator, Ctrl+O goes home, Ctrl+Q quits.
- **Web editor (`yaga wedit`)** — a local HTTP server (`--port`, default `:9090`, `--open` to launch the browser) with an embedded vanilla-JS single-page app: tabbed editors for panel/connections/auth/navigation/resources/pages, a Validate screen, a live dashboard **Preview** tab (page + resource mocks), and a raw-YAML tab. Changes are edited in memory; Save writes them to disk. Multiple browser tabs live-sync via Server-Sent Events and a revision counter; a stale tab is warned and asked before overwriting newer server/MCP changes. The MCP `save` tool backs up the previous file to `<config>.bak` before writing.
- **AI-assisted edit (`yaga edit --prompt "…"`)** — non-interactive; sends the full config to a model and merges back only the changed sections (validated; invalid merges are retried once, then the file is left untouched). See "CLI" for `--prompt`/`--apikey`/`--model`/`--dry-run`. IT IS ALMOST EXPERIMENTAL FEATURE ONLY! I recommend to use MCP/AI agent way instead.

### MCP (AI agents over `wedit`)

`yaga wedit` also serves a **Model Context Protocol (Streamable HTTP)** endpoint at `POST /mcp` (plus `GET /mcp`), so AI agents can read and edit the in-memory config through structured tools (`get_config`, `set_value`, `merge_yaml_fragment`, `add_resource`, `add_column`, `add_field`, `add_nav_item`, `validate`, `save`, …). Edits go through the same validation gate as the REST API (an invalid edit is rejected and never touches the config) and reach every connected browser tab automatically via live sync. To use it from opencode:

```json
{ "mcp": { "yaga": { "type": "remote", "url": "http://localhost:9090/mcp" } } }
```

## Deployment

`make package` bundles everything the dashboard needs to run into a single `tar.gz` release archive: the binary, the built `static/` assets (CSS, Chart.js, and any uploaded files), plus `sql/` migrations and the sqlite `data/` directory when present. The archive is named `<binary>-<date>.tar.gz` (override with `make package PACKAGE_NAME=my-release`).

```sh
make package
scp admin-20260804.tar.gz user@target:/
# on the target machine
tar xzf admin-20260804.tar.gz && cd admin-20260804
./admin -h                              # print command line syntax + flag meanings and exit
./admin --port 8080 --log full          # --log full (default) | err — err logs only error responses
                                        # short forms: -p 8080 -l err
```

Run the binary from the extracted directory — the sqlite DSN (`file:./data/admin.db`) is relative to the working directory. The dashboard resolves its database at startup as `DATABASE_URL` env var → the `.ENV` file next to the binary (generated from the config's `connections.*.dsn`, readable only by the owner) → for a config with no connection, a non-secret localhost default. Edit `.ENV` to point at another database (test→prod) without rebuilding. The generated server runs a DB sanity query **before** binding the port, so a missing/uninitialized DB is a fatal startup error instead of occupying the port, and it shuts down gracefully on SIGINT/SIGTERM.

The `init` command fails if files already exist unless `--force` is passed.

## Security

The generated dashboard ships with security defaults. Keep them in mind when deploying:

- **Session secret** — set `SESSION_SECRET` (≥ 32 chars). Without it, an ephemeral random secret is used (sessions don't survive restarts, with a warning). When `APP_ENV=production` is set, a missing secret is a fatal startup error.
- **CSRF** — every non-GET request requires a matching token (hidden `_csrf` input or `X-CSRF-Token` header), enforced by a middleware registered first inside the panel router. Logout is POST-only.
- **Login rate limiting** — optional per-IP throttling via `auth.login.rate_limit` (`max_attempts`/`window_seconds`).
- **Session rotation** — a fresh session is minted on successful login.
- **File uploads** — uploaded files are validated by extension and content-type (HTML/SVG rejected) and always served with `Content-Disposition: attachment`.
- **Security headers** — every response carries CSP, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, and `Permissions-Policy`.
- **Error responses** — internal errors log server-side and return generic 500/404 text; SQL/table names never leak to clients.
- **CSV export** — cell values starting with `=`, `+`, `-`, `@`, tab or CR are prefixed with `'` to prevent spreadsheet formula injection.
- **Admin password** — `init --db` accepts `--admin-password`; when omitted a random one-time password is generated and printed.
- **Sort input** — the `order` and `sort` request params are whitelisted against the configured columns.

## CLI

```
yaga init --db DSN  Introspect an existing database and generate yaga.yaml
                    (the captured schema: block is the sole schema source; the
                     only scaffold — requires --db)
yaga edit           Interactive YAML config editor (TUI)
                    with --prompt it edits yaga.yaml via AI instead (see flags)
yaga wedit          Web-based YAML config editor (browser, local HTTP server)
                    --port N (default 9090), --open to launch the browser;
                    serves an MCP endpoint at /mcp for AI agents
yaga generate       Generate the admin panel Go application (offline, no sqlc)
yaga validate       Validate YAML (structure + schema-block references);
                    --fix auto-repairs known-fixable problems, --dry-run previews
yaga version        Print version

Flags:
  --config, -c   Config file path (default: yaga.yaml)
  --out, -o      Output directory (default: ./admin)
  --db, -d DSN   Introspect database (postgres://..., sqlserver://... or sqlite file path)
  --force, -f    Overwrite existing files
  --admin-password, -p PASSWORD
                 Initial admin password for --db scaffolding (random
                 one-time password generated + printed when omitted)
  --verbose, -v  Verbose logging
  --skip-plugins, -s
                 Skip loading declared plugins (generate cannot use them)
  --fix          Auto-repair known-fixable validation problems (e.g. an inert
                 list/card filter block) and rewrite the config
  --dry-run      With validate: show what --fix would apply without writing

AI-assisted edit (edit only):
  --prompt TEXT  Edit yaga.yaml via AI instead of the TUI
                 (the full config is sent to the AI provider)
                 file://PATH reads the prompt from a file (~ expands to home)
  --apikey KEY   OpenRouter API key (fallback: OPENROUTER_API_KEY env, then .ENV)
  --model MODEL  Model id (fallback: .ENV, then openrouter/auto);
                 "lmstudio" uses a local LM Studio server (127.0.0.1:1234, no key)
  --dry-run      Print proposed YAML + diff without writing

WEdit (wedit only):
  --port N       Web editor listen port (default: 9090)
  --open         Open the editor in the default browser after binding
```

`yaga generate` runs fully offline — nothing is executed against the DB and no external tools are invoked, so it cannot "fail non-fatally" on a missing toolchain.

## Project structure

```
yaga/
├── cmd/yaga/                   # CLI entry point + subcommands
│   ├── main.go                 # flag parsing, dispatch, usage
│   ├── introspect.go           # init --db — DB introspection, schema: block capture
│   ├── edit.go                 # edit — TUI + AI-assisted entry points
│   ├── ai.go                   # edit --prompt — provider calls, merge, diff
│   ├── wedit.go                # wedit — web editor server entry point
│   ├── fix.go                  # validate --fix auto-repair plumbing
│   └── editor/                 # tview TUI editor (18 files: nav, lists, fields, …)
├── internal/
│   ├── types/                  # YAML-tagged config structs (config, panel, resource,
│   │                           #   field, hook, procedure, schema)
│   ├── parser/                 # YAML parsing + validation (schema.go, validator.go)
│   ├── fixer/                  # --fix auto-repair engine (shared by CLI/TUI/wedit)
│   ├── filterexpr/             # list/card filter mini-DSL (compiled to SQL)
│   ├── generator/              # Code generation pipeline (handler, templ, auth,
│   │                           #   router, data, makefile, tailwind, procs, audit,
│   │                           #   hooks, import, bulk, …; assets/ = embedded
│   │                           #   Chart.js + pre-built styles.css)
│   ├── serve/                  # wedit HTTP server: REST API, YAML↔JSON bridge,
│   │                           #   SSE live sync, preview, embedded SPA (static/)
│   └── mcp/                    # MCP server (JSON-RPC 2.0) + yaml.Node path helpers
├── examples/plugins/audit/     # complete example plugin (AuditLog resource + hooks)
├── docs/
│   ├── USER_GUIDE.md            # End-to-end user guide (README depth, workflows)
│   └── Lua-for-Yaga-guide.md    # Lua scripting reference (actions/hooks)
├── SPEC.md                     # Authoritative spec
├── testdata/kitchen.yaml       # kitchen-sink fixture for styles + generator tests
└── AGENTS.md                   # Agent instructions
```

## Generated output structure

```
output/
├── main.go                          # DB sanity check, pool settings, chi server, graceful shutdown
├── go.mod / go.sum                  # go.mod declares templ as a Go tool
├── Makefile                         # make build — templ + go build (no css/sqlc steps)
├── static/
│   ├── css/styles.css               # pre-built Tailwind, vendored at generation time
│   └── js/chart.js                  # Chart.js 4.4.1, vendored at generation time
├── sql/migrations/                  # emitted DDL only: audit_log, sql_procedures
├── internal/
│   ├── data/                        # Get queries derived from the schema: block
│   │   └── *.go                     #   (map[string]interface{} rows, not sqlc)
│   ├── panel/
│   │   ├── router.go                # All chi routes (single r.Route + inner group)
│   │   ├── auth/                    # Login handler, session, CSRF, middleware, RBAC,
│   │   │                            #   optional rate limit
│   │   ├── httperr/                 # Safe error helpers (generic 500/404 responses)
│   │   ├── sqlutil/                 # runtime identifier-quoting helper (sort column)
│   │   ├── procs/                   # sqlite SQL-batch procedure runner (when used)
│   │   ├── resources/
│   │   │   └── {resource}/
│   │   │       ├── list.go          # List handler (raw SQL, windowed COUNT(*), filter)
│   │   │       ├── card.go          # Card/kanban handler — if card configured
│   │   │       ├── detail.go        # Detail handler (schema-derived data query)
│   │   │       ├── create.go        # Create handler (raw SQL INSERT, buildCreateParams)
│   │   │       ├── update.go        # Update handler (populate + raw SQL UPDATE)
│   │   │       ├── delete.go        # Delete handler (raw SQL DELETE) — if configured
│   │   │       ├── export.go        # CSV export handler — if list configured
│   │   │       ├── import.go        # CSV import handler — if import_csv set
│   │   │       ├── actions.go       # Custom action handler — if actions defined
│   │   │       ├── bulk.go          # Bulk action handler — if a bulk action exists
│   │   │       └── childlines.go    # Master-detail child-line helper — if children set
│   │   └── pages/
│   │       └── *.go                 # Page handlers (dashboard, reports, etc.)
│   ├── views/
│   │   ├── layout/                  # Base, sidebar, topbar .templ files
│   │   ├── resources/{resource}     # List, detail, form, cards .templ files per resource
│   │   ├── pages/                   # Page .templ files
│   │   ├── widgets/                 # Stat, chart, table, list, html widgets
│   │   └── components/              # Badge, pagination, search, renderers, icons, picker
│   ├── hooks/hooks.go               # Scope struct + per-fn-hook stubs (user-implemented)
│   └── viewmodels/models.go         # View data structs (Stringify, ThemeConfig, …)
```

## YAML config reference

The config file (`yaga.yaml`) is the single source of truth for your admin panel. This reference documents every attribute, its possible values, and its meaning. See `SPEC.md` for the authoritative schema.

### Top-level keys

| Key | Required | Default | Meaning |
|---|---|---|---|
| `version` | yes | — | Schema version string, e.g. `"1.0"`. Any non-empty value is accepted. |
| `panel` | yes | — | Panel identity and branding (see below). |
| `connections` | no | — | Database connections; the **first** entry's `driver` and `dsn` are used by the generated app. |
| `schema` | no | — | The captured database schema (tables, pk, columns, foreign keys) — **the sole schema source** for the generator. Written by `init --db`. |
| `auth` | no | — | Authentication config (see below). |
| `navigation` | no | — | Sidebar navigation groups. |
| `resources` | no | — | CRUD resources. At least one resource **or** page is required. |
| `pages` | no | — | Custom dashboard pages. At least one resource **or** page is required. |
| `audit` | no | — | Audit logging of every create/update/delete/action (see below). |
| `procedures` | no | — | SQLite SQL-batch "stored procedures" (see below). Ignored on postgres/mssql. |
| `plugins` | no | — | Generation-time plugins (see Plugins). |

> A legacy `sqlc:` block is still **parsed but inert** — the generator no longer reads it, emits a `sqlc.yaml`, or runs sqlc (D11). `init --db` does not produce one.

### Panel

```yaml
panel:
  id: admin            # lowercase identifier; becomes part of generated handler/view
                       #   names (e.g. AdminDashboard). Default: "admin".
  path: /admin         # URL prefix for the whole panel, e.g. "/admin". Must start
                       #   with "/". Used as the base for all generated routes.
  name: "My Admin"     # Display name shown in the sidebar and login page.
```

The `brand`, `layout`, and `theme` sub-sections are wired into the generated output. `brand.colors` becomes the Tailwind `brand.primary`/`brand.secondary` palette plus `--brand-primary`/`--brand-secondary` **and** `--brand-primary-rgb`/`--brand-secondary-rgb` CSS variables (the RGB channel form keeps `/alpha` utilities like `bg-brand-primary/10` working); `layout.sidebar` controls the sidebar (a JS toggle shows/hides it entirely via `display`, with `data-width` controlling its width), `layout.topbar.sticky` pins the topbar, `layout.max_content_width` wraps the main content (validated against an allowlist, e.g. `7xl`); `theme.dark_mode` turns dark mode on by default (togglable in the topbar and persisted in `localStorage`), and `theme.font` adds `body`/`code` font families to the layout and login pages:

```yaml
panel:
  brand:
    logo: /assets/logo.svg
    favicon: /assets/favicon.ico
    colors:
      primary: "#6366f1"
      secondary: "#64748b"
  layout:
    sidebar:
      collapsible: true
      width: 280
    topbar:
      sticky: true
    max_content_width: 7xl
  theme:
    dark_mode: true
    font:
      family: "Inter, sans-serif"
      mono: "JetBrains Mono, monospace"
```

### Connections

```yaml
connections:
  default:             # map key — arbitrary name; only the FIRST entry is used
    driver: postgres   # "postgres" (default) | "sqlite" | "sqlite3" | "mssql" | "sqlserver"
    dsn: "postgres://user:pass@localhost:5432/db?sslmode=disable"
    pool:              # emitted as db.SetMaxOpenConns / SetMaxIdleConns /
      max_open: 25     #   SetConnMaxLifetime right after Ping()
      max_idle: 10
      lifetime: 5m
```

- `driver` determines the `sql.Open` driver, the LIKE operator (`ILIKE` on postgres, `LIKE` on sqlite/mssql), bind placeholders (`$N` vs positional `?`), identifier quoting (`"name"` vs `[name]`), and the id type throughout generation (`int32` postgres/mssql, `int64` sqlite unless overridden by `id_type`). If every entry omits `driver`, it defaults to `postgres`.
- `dsn` is written to a `.ENV` file in the generated project (`DATABASE_URL=<dsn>`, mode 0600) — it is **not** compiled into the binary. At runtime the `DATABASE_URL` environment variable wins over the `.ENV` value; if neither is set and a connection was configured, the server refuses to start. A SQLite example: `file:./data/admin.db`.
- SQLite uses `modernc.org/sqlite` (pure Go, no CGO — cross-compiles to Windows/Linux/macOS with `CGO_ENABLED=0`); MSSQL requires `github.com/microsoft/go-mssqldb`. The matching driver import is added to the generated `go.mod` automatically.
- The generated server applies pool settings, then runs a DB sanity query against the auth table **before** binding the port (mssql `SELECT TOP 1 1`, others `SELECT 1 … LIMIT 1`).

### Schema

The `schema:` block is written by `yaga init --db` and captures the database structure. It is the **only** schema source for the generator — `generate` derives every data query, option SQL, FK label join, and master-detail relation from it, fully offline:

```yaml
schema:
  tables:
    - name: customers
      pk: id
      view: false
      columns:
        - { name: id, type: integer, primary_key: true }
        - { name: name, type: string }
        - { name: country_id, type: integer, primary_key: false }
      foreign_keys:
        - { column: country_id, foreign_table: countries, foreign_column: id, label: name }
```

Each table carries its resolved row-key column (`pk`), whether it is a read-only view, its columns (with yaga field types mapped by `init --db`: `int`→`integer`, `varchar`/`text`→`string`, `bool`→`boolean`, `timestamp`/`date`→`datetime`, `real`/`float`/`numeric`→`float`, `json`/`jsonb`→`json`, `bytea`/`blob`→`file`), and its foreign keys (local column → `foreign_table`/`foreign_column` plus the `label` column used for list JOINs and picker option SQL). Views are listed too, marked `view: true`.

You can hand-edit the block (add a column, adjust a type) — the parser validates that every resource table and referenced column exists in it, and the editor's Validate screen warns/errors on mismatches.

### MSSQL support

MSSQL is a first-class database driver for both `init --db` introspection and generated apps.

- **Config & detection**: set `driver: mssql` (or `sqlserver`) in the first `connections` entry, or pass a `sqlserver://` / `mssql://` DSN to `init --db`. DSN prefixes decide the driver: `sqlserver://`/`mssql://` → mssql, `postgres://`/`postgresql://` → postgres, anything else (file path, `:memory:`) → sqlite.
- **Generated app**: opens the DB with `github.com/microsoft/go-mssqldb`.
- **Query dialect**: `LIKE` (case-insensitive default collation) instead of `ILIKE`; `$N` bind placeholders (go-mssqldb loose mode maps them to `@pN`); identifiers quoted with `[name]`; ids default to `int32`, or `int64` when a `bigint` primary key sets `id_type: int64`.
- **Pagination**: list/card queries use `OFFSET $2 ROWS FETCH NEXT $1 ROWS ONLY`, which requires an ORDER BY — when no sort is configured the generator emits `ORDER BY (SELECT NULL)`. Create/update capture the row id via `OUTPUT INSERTED.<id>` (T-SQL has no `RETURNING`).
- **Introspection specifics**: schema discovery uses INFORMATION_SCHEMA + `sys.foreign_keys`; tables without a declared PRIMARY KEY fall back to their identity `ID` column (emitted as `id_column: ID`). PascalCase column names are normalized the same way as elsewhere (`CeleJmeno` → `Celejmeno`), and `table:`/`id_type:`/`id_column:` overrides are emitted whenever the real schema doesn't match the convention.

### Auth

```yaml
auth:
  table: users        # DB table holding auth users (login lookup). Default: "users".
  login:
    fields: [email, password]  # [usernameField, passwordField]; first = identity,
                               #   second = password (bcrypt-hashed in DB).
    redirect: /admin/dashboard  # URL after successful login. Must be a registered
                               #   route (e.g. the default page). Default: "<panel.path>/dashboard".
    rate_limit:               # optional per-IP login throttling
      max_attempts: 5
      window_seconds: 300
```

The login handler reads the identity field and the password field from the POST form, verifies the password against a bcrypt hash in `auth.table`, sets a `gorilla/sessions` cookie, and redirects to `login.redirect`. Unauthenticated users hitting a protected route are redirected to `<panel.path>/login`. `auth.guard`/`auth.provider` are parsed but not yet used; `registration`/`password_reset`/`remember_me` are config-only flags (parsed, not implemented in the generated app).

### Navigation

```yaml
navigation:
  - group: "User Management"  # sidebar section heading
    icon: users               # icon name rendered next to the heading
    sort: 1                   # integer; groups are ordered ascending by this value
    items:
      - resource: User        # link to a resource list -> "<panel.path>/user"
      - resource: Role
  - group: "Analytics"
    icon: chart
    items:
      - page: Dashboard       # link to a page -> "<panel.path>/dashboard"
      - type: link            # external / custom link
        label: "Google Analytics"
        url: https://analytics.google.com
        opens_in_new_tab: true   # adds target="_blank"
```

Each `item` is one of three kinds, selected by which key is present:
- `resource: <Name>` — link to that resource's list view (name is lowercased for the URL).
- `page: <Name>` — link to that page's route (path = `"/" + pageName` unless the page declares a custom `path`).
- `type: link` with `label`, `url`, and optional `opens_in_new_tab`.

### Resources

A resource is a CRUD-managed entity. It has up to three independent views (list, detail, form) plus optional custom actions, RBAC policies, CSV import and master-detail children:

```yaml
resources:
  - name: User            # REQUIRED. PascalCase; lowercased for the Go package,
                          #   output dir and URL segment ("user"). The table name
                          #   is derived as <lowercase> + "s" ("users").
    label: Users          # Human-readable name used in the UI. Default: same as name.
    table: users          # optional DB table override (emitted by introspection when
                          #   the derived name doesn't match the real schema)
    id_column: id         # optional row-key column override (e.g. "ID" on mssql)
    id_type: int32        # optional id Go type override (e.g. "int64" for bigint pks)
    import_csv: true      # optional — adds an "Import CSV" button + POST /import/csv
```

#### list

```yaml
    list:
      query: ListUsers            # ACCEPTED BUT IGNORED — the list handler builds a
                                  #   raw "SELECT <columns> FROM <table>" query instead.
      count_query: CountUsers     # ACCEPTED BUT IGNORED — the total comes from a
                                  #   windowed COUNT(*) OVER() in the data query.
      per_page: 20                # rows per page (default 20)
      columns:
        - name: id                # column name; must match the SQL column used in the
                                  #   raw list query
          type: integer
          sortable: true          # clickable sort header for this column
        - name: name
          type: string
          searchable: true        # included in the global search (OR-ed LIKE/ILIKE)
        - name: email
          type: email
          sortable: true
        - name: role_name
          label: Role             # display label; default: the column name
          type: text
        - name: status
          type: badge
          options:                # map: value -> badge color (success|warning|danger)
            active: success
            inactive: warning
        - name: created_at
          type: datetime
          sortable: true
      default_sort: -created_at   # sort column + direction; leading "-" = descending
      export: [id, name, email]   # optional CSV export column subset (Label headers);
                                  #   empty => all list columns with raw name headers
      filter:                     # optional collapsible filter section (D13)
        label: "Status"
        where: "status = $1 AND created_at >= $2"
        params:
          - { name: status, label: Status }
          - { name: from, label: Created after }
```

The list handler reads `page`, `search`, `sort`, and `order` from the query string, applies the configured `searchable`/`sortable` columns dynamically, and paginates. The `default_sort` prefix `-` is stripped to produce the sort column and `desc` order; a missing prefix means ascending. Sort input is validated against the `sortable` columns (invalid values fall back to the default).

**Filters (D13)** — the `where` expression is a mini-DSL compiled at generation time into dialect-correct SQL: `and`/`or`/parentheses, operators `= != <> < <= > >= contains not_contains is_null is_not_null`, literal values or `$N` params. Each `params` entry renders as a labeled input on the filter form; the value travels as a `fp_<name>` query param and an empty param skips the filter. The filter and the search blocks are AND-ed together (`(<filter>) AND (<search>)`).

#### detail

```yaml
    detail:
      query: GetUser              # ACCEPTED BUT IGNORED — the detail handler uses a
                                  #   data query derived from the schema: block.
      params:                     # ACCEPTED BUT IGNORED — the handler always reads
        id: "{record.id}"         #   the row by the resource's key column.
      fields:
        - name: id
          type: integer
        - name: name
          type: string
        - name: email
          type: email
        - name: role_name
          label: Role
        - name: status
          type: badge
          options: { active: success, inactive: warning }
```

The detail handler parses `:id` from the URL and fetches the row via a generated data query (casting the id to the resource's id type). Omit `detail` entirely to have no detail page.

#### card

```yaml
    card:
      fields:                      # view-only, same field definition as forms
        - name: title
          type: string
        - name: status
          type: select
          options:
            todo: "To Do"
            doing: "In Progress"
            done: "Done"
      columns: 3                   # X: cards per row (clamped to 1..12)
      rows: 4                      # Y: rows per page (a page shows X*Y cards)
      kanban_field: status         # optional select field -> kanban board
      searchable:
        - title
      default_sort: -created_at
      filter:                      # optional, same shape as list.filter
        label: "Status"
        where: "status = $1"
        params: [ { name: status, label: Status } ]
```

The card view is view-only (no CRUD wiring of its own). It is served at `GET /{panel}/{resource}/cards` and reachable via a "Cards" button on the list view. Pagination and search behave exactly like the list view. When `kanban_field` names a `select` field, cards are grouped into columns keyed by that field's option values instead of a grid. Omit `card` entirely to have no card view.

#### form

```yaml
    form:
      create:
        query: CreateUser          # ACCEPTED BUT IGNORED — creation uses a raw
                                   #   INSERT built from the declared fields.
        fields:
          - name: name
            type: text
            required: true         # renders a "Required" hint (no server-side check)
          - name: email
            type: email
            required: true
          - name: password
            type: password         # bcrypt-hashed before insert
            required: true
            visible: [create]      # on create forms, only render if "create" is in
                                   #   this list; other contexts are not special-cased
          - name: role_id
            type: select
            options_sql: "SELECT id, name FROM roles WHERE active = true"  # dynamic
            options_value: id      #   options via raw SQL; options_query: (a SQLC
            options_label: name    #   name) is legacy/parse-only
          - name: customer_id
            type: relation
            options_value: id
            options_label: name
            copies:                # auto-fill siblings when a picker row is selected:
              city: city          #   target field `city` <- related row's `city`
              customer_since: created_at
          - name: status
            type: select
            options: { active: Active, inactive: Inactive }  # static value->label map
          - name: score
            type: integer
            validation: { min: 0, max: 100 }   # renders "Min: 0, Max: 100" hint only
      update:
        populate_query: GetUser     # ACCEPTED BUT IGNORED — the update GET uses the
                                    #   same schema-derived data query as detail.
        populate_params:            # ACCEPTED BUT IGNORED
          id: "{record.id}"
        fields:                     # SET columns on POST (raw UPDATE ... WHERE id = $N).
          - name: name
          - name: email
          - name: role_id
            type: select
            options_sql: "SELECT id, name FROM roles"
      delete: {}                    # presence enables the delete button + route
                                    #   (raw "DELETE FROM <table> WHERE id = $1")
  children:                        # optional master-detail sections (D14)
    - name: Lines                 #   section heading (defaults to child label)
      resource: OrderLine          #   child resource name
      column: order_id             #   child FK column (default: derived from schema)
      columns:                     #   optional; default = child's list columns
        - { name: qty, label: "Qty", type: integer }
```

- **create** renders a GET form and performs a raw `INSERT INTO <table> (<fields>) VALUES ($1...)` on POST, then redirects to the list. `password` fields are bcrypt-hashed first. `file`/`image` fields are saved to `static/uploads/<field>/<timestamp><ext>` and the relative URL is stored.
- **update** renders a GET form pre-filled by the schema-derived data query, and on POST performs a raw `UPDATE <table> SET <col> = $N ... WHERE id = $N`. Password fields are **not** re-hashed. The shared form template renders the **union** of the create + update field sets (update-only fields like `status`/`created_at` still appear on the edit form).
- **delete** is a POST-only route (`/<panel>/<resource>/{id}/delete`) confirmed via a JS `confirm()` dialog.
- **select/relation pickers** — when a `select`/`relation` field resolves option SQL (from `options_sql`, or automatically from the schema block's FK metadata), it renders as a **modal record picker**: a read-only display input + a "Browse…" button opening a searchable list of options. Selecting a row sets the hidden value input. With `copies:`, the selected row's columns are written into the named sibling form fields (`datetime`/`date` targets get the input layout, NULL → empty).
- **children (D14)** — the detail and edit views embed a read-only lines table per child; the edit form offers per-line Edit/Delete and an "Add Line" button that pre-seeds the child's FK (`?<fk>=<parentId>`) and locks it. Child create/update/delete honor a `?return=` redirect back to the parent.

#### actions

```yaml
    actions:
      - name: activate          # REQUIRED action id, used in the POST route + switch case
        label: "Activate"       # button text
        icon: check             # inline SVG icon rendered next to the label
        color: success          # button color: success | danger | warning | (default: gray)
        requires_confirmation: true  # JS confirm() dialog before submitting
        bulk: true              # render as a bulk action (row checkboxes + toolbar)
        query: ActivateUser     # raw SQL executed via db.ExecContext on POST
        # or, instead of query:
        proc: sp_archive        # stored procedure name (CALL/EXEC; on sqlite a
                                #   procedure declared under `procedures:`)
        # or, instead of query/proc:
        script: |               # embedded Lua body (request-time, gopher-lua)
          db.exec("UPDATE users SET status = 'active' WHERE id = ?", ctx.id)
        policy: "admin"         # optional roles allowed to run this action ("|" separates)
        hooks:                  # optional before/after hooks (see Hooks)
          after:
            - name: log
              sql: "INSERT INTO audit_log (msg) VALUES ('activated')"
```

Each action produces a POST route `/<panel>/<resource>/{id}/action/<name>`. On submit the handler `switch`es on the action name and runs its `query` with the record id, then redirects to the list. Unknown action names return 404. A `requires_confirmation` action shows a `confirm()` dialog before POSTing; an `icon` renders a small SVG next to the label. A `bulk: true` action adds row checkboxes + a select-all and a "N Selected" toolbar in the list view that posts to `/<panel>/<resource>/bulk/<name>` (a plain, non-RBAC POST route handled by the generated `bulk.go`, which loops the action's SQL/proc once per selected id **inside one transaction** — a mid-batch failure rolls back the whole operation).

#### hooks

`before`/`after` lifecycle hooks can be attached to any form action (`form.create`, `form.update`, `form.delete`) and to custom `actions`. Each hook is one of a user-implemented Go function, an inline SQL statement, a stored-procedure call, or a Lua script:

```yaml
    form:
      create:
        hooks:
          before:
            - name: validate_domain
              fn: ValidateUserDomain      # stub generated in internal/hooks/hooks.go
            - name: default_status
              script: |                  # Lua: set a default for an unset field
                if ctx.values["status"] == nil then
                  ctx.values["status"] = "draft"
                end
          after:
            - name: notify
              sql: "INSERT INTO notifications (target, msg) VALUES ($1, 'user created')"
```

The generator emits `internal/hooks/hooks.go` with a `Scope` struct (`ID`, `Table`, `Action`, `Values`) and one compile-ready stub per `fn` hook — you fill the bodies in. `sql` hooks run inline via `db.ExecContext(..., <sql>, scope.ID)`, `proc` hooks call a stored procedure (`CALL`/`EXEC`; on sqlite a procedure declared under `procedures:`). On create, the insert switches to a driver-aware `RETURNING <id>` (postgres/sqlite) / `OUTPUT INSERTED.<id>` (mssql) so after-create hooks receive the new row id. Hook failures abort the request with HTTP 500.

**`script:` hooks and script actions** embed Lua (`gopher-lua` v1.1.1) bodies that the generated `internal/panel/luascript` runtime executes at request time under a fixed 5 s timeout (the yaga binary gains no Lua dependency — only the generated dashboard's `go.mod` does, and only when a script exists). The `ctx` table exposes `id`, `table`, `action`, `user`, `role` and `values` (in/out — a before-create/update script's mutations are written back into the row). Host globals: `db.exec(sql, ...)`, `db.query(sql, ...)`, `db.query_one(sql, ...)` (positional `?` bound on sqlite, renumbered to `$N` on postgres/mssql), plus `abort(msg)` and `log(msg)`. An action-script `abort()` redirects to the list with `?flash=<msg>`; a hook-script `abort()` returns a 400. Audited script actions run inside the audit transaction.

#### procedures

SQLite has no stored procedures, so `procedures:` declares a named batch of SQL statements that the generated app stores in a `sql_procedures` table (seeded via `INSERT OR IGNORE`) and executes inside one transaction at call time:

```yaml
procedures:
  - name: sp_archive_customer
    description: "Archive a customer and record the event"
    sql: |
      UPDATE customers SET status = 'inactive' WHERE id = $1;
      INSERT INTO customer_log (customer_id, msg) VALUES ($1, 'archived');
```

The block is **sqlite-only semantics** — on postgres/mssql it is ignored (real procedures come from your DDL). Any `proc:` reference on an action/hook must match a declared procedure when the driver is sqlite (fatal validation error otherwise). The same `proc:` config works across all three drivers with three execution strategies: `CALL <name>($1)` (postgres), `EXEC <name> $1` (mssql), `procs.Exec` (sqlite).

#### policies (RBAC)

```yaml
    policies:
      view_any: "admin|manager"  # pipe-separated roles allowed for the list + CSV export
      view: "admin|manager"      # detail page
      create: "admin"            # create form GET + POST
      update: "admin|manager"    # update form GET + POST
      delete: "admin"            # delete route
```

Policies are optional. When **any** resource declares them, the generator appends an RBAC middleware to the generated app; resource routes are then wrapped with `auth.RBACMiddleware("<resource>", "<action>")` and the authenticated user's role (from the auth session) is checked against the pipe-separated role list. A `|` in a value separates allowed roles. Custom action routes only get RBAC when the action itself sets `policy:` (emitting `ActionRBACMiddleware` for the action + bulk routes).

#### audit

```yaml
audit:
  enabled: true          # weave an audit INSERT into every create/update/delete/action
  table: audit_log       # default: "audit_log"
  include_values: true   # store the changed values as JSON (values_json column)
  policy: "admin"        # roles allowed to view the generated AuditLog list
  exclude_resources: [Users]   # resources that are NOT audited
```

When enabled, the generator adds a list-only `AuditLog` resource (default sort `-created_at`, view policy from `audit.policy`) and an "Audit Log" navigation group, emits the `audit_log` table DDL into `sql/migrations/audit_log.sql`, and wraps each mutating handler's operation + audit insert in **one transaction**. `password` values are stored bcrypt-hashed (never plaintext).

#### Field types

`type` is a **UI rendering hint only** — the DB column types come from the `schema:` block (or your own DDL). The same set applies to list `columns`, detail `fields`, card `fields`, and form `fields`:

| Type | List/detail display | Form input |
|---|---|---|
| `string` | Text | text input |
| `text` | Text | textarea |
| `integer` | Number | number input |
| `float` | Decimal | number input |
| `email` | mailto link | email input |
| `password` | — | password input |
| `boolean` | Check icon | checkbox |
| `select` | — | dropdown (static `options` map or `options_sql`) |
| `datetime` | Formatted date-time | datetime-local input |
| `date` | Date | date input |
| `badge` | Colored badge | text input |
| `image` | Thumbnail | file input (`accept="image/*"`) |
| `file` | Download link | file input |
| `relation` | Link to related record | modal record picker (FK-derived options) |
| `json` | Pretty-printed | textarea (mono font) |
| `gps` | GPS coordinates (maps link) | text input (`"lat, lng"`) |

### Pages & widgets

```yaml
pages:
  - name: Dashboard     # REQUIRED. Becomes the handler name (<PanelID>Dashboard) and,
                        #   unless overridden, the route ("/dashboard").
    path: /dashboard    # custom route path. Default: "/" + name.
    default: true       # mounts the page at BOTH "/" and its path; this is the
                        #   landing page after login.
    widgets:
      - type: stat
        label: "Total Users"
        query: "SELECT COUNT(*) FROM users"   # raw SQL returning ONE scalar
        icon: users
        prefix: "$"        # text prepended to the value
      - type: stats_grid
        columns: 2         # grid columns (detected from the first stats_grid)
        widgets:           # nested stat widgets
          - type: stat
            label: "Active Users"
            query: "SELECT COUNT(*) FROM users WHERE status = 'active'"
            icon: check
      - type: chart
        label: "Monthly Revenue"
        query: "SELECT month, total FROM revenue ORDER BY month"  # raw SQL,
                                          #   must return exactly TWO columns:
                                          #   a label (string) and a value (number)
        chart:
          type: line        # line | bar | pie | area (rendered via Chart.js)
          # note: chart.query / chart.x / chart.y are parsed but NOT used —
          #   the SQL above is the single source of labels + values
      - type: table
        label: "Recent Orders"
        query: "SELECT id, customer_name, total FROM orders ORDER BY created_at DESC LIMIT 5"
        data_columns: [id, customer_name, total]   # column names to display
      - type: list
        label: "Top Products"
        # renders rows as "label : value" pairs from the raw SQL result
      - type: html
        label: "Note"
        # renders the query result as raw HTML (trusted input only)
```

Widget types: `stat`, `stats_grid`, `chart`, `table`, `list`, `html`.

- `stat` runs its `query` and shows the first scalar as a large number; `prefix` (and the templ's `Suffix` field) decorate it.
- `stats_grid` renders nested `stat` widgets in a grid; `columns` sizes the grid (default 4).
- `chart` runs its `query` expecting exactly two result columns (string label, numeric value), serializes them into `data-labels`/`data-values`, and renders a Chart.js canvas of `chart.type`.
- `table` runs its `query` and renders every returned column, filtered to `data_columns` (note: the YAML key is `data_columns`, not `columns`). Rows are formatted via `viewmodels.Stringify` (NULLs render empty, never `{1 true}`).
- `list` renders `label`/`value` columns of the result as a vertical list.
- `html` renders raw HTML in the templ view (document it as requiring trusted input).

**Important:** widget `query` values are **raw SQL** executed at request time. Widget query errors are logged and never blank the page (a broken widget renders whatever rows it got).

## Resource handler SQL strategy

Handlers use a consistent SQL approach:

- **List**: Raw SQL with dynamic WHERE/ORDER BY/LIMIT and a windowed `COUNT(*) OVER() AS _total` — enables search, sort, filters and pagination in one round trip. The dialect is driver-aware: postgres uses `ILIKE` + `LIMIT $1 OFFSET $2`, sqlite binds positionally with `?` + `LIKE`, MSSQL uses `OFFSET … ROWS FETCH NEXT … ROWS ONLY`.
- **Card**: Raw SQL identical to list; `LIMIT = Rows*Columns`, grouped into kanban columns when `kanban_field` is set.
- **Detail**: Schema-derived data query (`data.New(db).GetUser(ctx, id)` → `map[string]interface{}`)
- **Create**: Raw SQL INSERT via `db.ExecContext` (or `buildCreateParams` — shared with CSV import)
- **Update GET**: Schema-derived data query to pre-fill the edit form
- **Update POST**: Raw SQL UPDATE via `db.ExecContext`
- **Delete**: Raw SQL DELETE via `db.ExecContext`
- **Action**: Raw SQL per action name, or stored-proc call when `proc:` is set (switch dispatch)
- **Bulk**: Raw SQL per bulk action name, looped once per selected id, inside one transaction
- **CSV Export**: Raw SQL SELECT + `encoding/csv` (formula-injection safe)
- **CSV Import**: `encoding/csv` → one transaction around every row's INSERT (row errors counted as skipped, not fatal), then a redirect with a `?flash=` summary bar

All table/column identifiers in emitted raw SQL are quoted per-driver (`"name"` / `[name]`) so keyword-named objects work everywhere; user-supplied sort columns are quoted at runtime via the generated `sqlutil` helper. Audited resources wrap the op + audit INSERT in one transaction.

## Plugins

Plugins extend yaga at generation time by contributing resources, pages, navigation, SQL files, and hook attachments. A plugin is a separate Go module that implements the `github.com/MichalHerstus/yaga/pkg/plugin.Plugin` interface. When you declare plugins in `yaga.yaml`, yaga runs each plugin in a throwaway module, collects its manifest, and merges it into the config before generating the admin panel. The generated app has **zero runtime dependency** on yaga or its plugins.

### Using plugins

Add a `plugins` list to your `yaga.yaml`:

```yaml
plugins:
  - name: audit
    source: ./plugins/audit      # local directory (must have go.mod)
    config:
      table: audit_log
      retention_days: 90
  - name: other-plugin
    source: github.com/user/plugin  # module path (fetched from proxy)
    config:
      key: value
```

- `source` can be a local directory (starts with `.`, `/`, or `~`) or a Go module import path.
- Local directories use a `replace` directive in the shim so they compile against the exact local sources.
- The `config` map is passed to the plugin's `Configure` method (optional). yaga injects the database driver under the reserved `"driver"` key (`"postgres"`, `"sqlite"`, or `"mssql"`) so plugins can emit driver-appropriate SQL.

### Plugin authoring API

A plugin module must export a `func New() plugin.Plugin` at its root package (package name must match the last element of the module path). The `Plugin` interface:

```go
type Plugin interface {
    ID() string
    Register(p *Panel) error
    Boot(p *Panel) error
}
```

The `Panel` builder provides methods to contribute to the generated panel:

```go
p.AddResource(Resource) error          // resources
p.AddPage(Page) error                  // pages (path defaults to "/"+Name)
p.AddNavigationGroup(NavigationGroup)  // sidebar groups
p.AddSQLFile(name, content string)     // "queries/..." or "migrations/..."
p.AddHookSource(name, content string)  // a `package hooks` Go source file
p.AddHookToResource(resource, action, when string, Hook) error  // before/after hooks
p.Manifest() Manifest                  // returns the accumulated contributions
```

The `Hook` type has `Name`, `Fn` (for Go function hooks), and `SQL` (for inline SQL hooks). Plugin `Fn` hooks are backed by `AddHookSource` files (which yaga copies into `internal/hooks/`), so a plugin can carry both the hook attachment and its implementation. SQL hooks emit driver-agnostic SQL via `$1`/`Scope.ID`.

### Example: audit plugin

The `examples/plugins/audit/` directory contains a complete plugin that:
- Adds an `AuditLog` resource with list/detail views
- Adds an `AuditOverview` page with stat widgets
- Adds an "Audit" navigation group
- Contributes `migrations/audit_schema.sql` + `queries/audit.sql`
- Attaches an `after-delete` SQL hook to the `Customer` resource

To use it, add to `yaga.yaml`:
```yaml
plugins:
  - name: audit
    source: ./plugins/audit   # or github.com/yaga/plugin-audit if published
    config:
      table: audit_log
      retention_days: 90
```

Run `yaga generate` and the audit contributions will be merged into your panel.

### Escape hatch

Pass `--skip-plugins` to `yaga generate` to skip all plugin loading (useful for CI or when a plugin is temporarily broken).

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go (stdlib `database/sql`) |
| Data layer | Raw SQL handlers + schema-derived data queries (no sqlc) |
| Frontend | Templ (compiled Go templates) |
| CSS | Tailwind CSS (pre-built stylesheet, vendored) |
| Router | chi |
| Auth | gorilla/sessions + bcrypt + CSRF + optional rate limiting |
| Charts | Chart.js 4.4.1 (bundled, offline) |
| Icons | Heroicons (inline SVG) |
| Audit | Generated `audit_log` resource + transactional INSERT weaving |
| Editors | tview TUI, embedded-SPA web editor, AI-assisted edit, MCP |
| Runtime | **Zero runtime dependency on yaga** — pure code-gen |
