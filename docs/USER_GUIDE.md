# YAGA — User Guide

**YAGA** (YAML Advanced Generator for Admin panels) is a YAML-driven admin dashboard
generator for Go. You point it at an existing database, describe the dashboard you want
in a `yaga.yaml`, and it generates a complete, self-contained Go admin panel: CRUD
resources, card/kanban views, custom pages with widgets, search/sort/filter, CSV
import/export, authentication, RBAC, audit logging, custom actions, before/after hooks
(Go, SQL or Lua) and more.

The important mental model: **the database is the base**! YAGA introspects your database,
captures its schema, and adds behaviour **on top** of it — it does not replace a good
database design.

---

## 1. Installation

### 1.1 Prerequisites — a working Go toolchain

| Tool | Required for | Notes |
|---|---|---|
| [Go](https://go.dev/dl/) 1.26+ | Running yaga **and** building the generated dashboard | Non-negotiable; see 1.4 |
| [Templ](https://templ.dev/) | Compiling `.templ` views in the generated app | Optional to install manually — the generated `go.mod` declares `tool github.com/a-h/templ/cmd/templ`, so `go tool templ generate` works through the Go toolchain |

No Node.js/npm, no sqlc, and no Tailwind binary are needed. The Tailwind stylesheet is
pre-built and vendored into the generated project, and Chart.js is embedded into the yaga
binary — the running dashboard needs **no internet at runtime**.

### 1.2 Install from source

The simplest installation:

```sh
go install github.com/MichalHerstus/yaga/cmd/yaga@latest
```

The binary lands in `$(go env GOPATH)/bin/yaga` (commonly `~/go/bin/yaga`); make sure
that directory is on your `PATH` (or set `GOBIN` before installing). To build from a
local checkout instead:

```sh
git clone https://github.com/MichalHerstus/yaga.git
cd yaga
go build -o yaga ./cmd/yaga
```

Verify the installation:

```sh
yaga version          # e.g. yaga version 2.1.0
yaga                  # prints the usage text
```

### 1.3 Pre-built binaries (GitHub Releases)

Ready-to-run binaries for the common OS/arch combinations are published on the project's
[GitHub Releases](https://github.com/MichalHerstus/yaga/releases) page. Download the
archive matching your platform (e.g. `yaga_2.1.0_darwin_arm64.tar.gz`), extract it and
place the `yaga` binary somewhere on your `PATH`:

```sh
tar xzf yaga_2.1.0_darwin_arm64.tar.gz
sudo mv yaga /usr/local/bin/
yaga version
```

> **A working Go installation is still required — even when you use a pre-built yaga
> binary.** yaga only *generates* the admin panel; it does not ship a compiler. Building
> the generated dashboard always runs Go tools against the generated project:
> `go mod tidy`, `go tool templ generate` and `go build ./...`. If you cannot install Go
> on the machine, use `yaga generate` on a machine that has Go and transfer the built
> **binary** (not the source) to the target — `make package` builds exactly that
> deployment archive for you.

### 1.4 DSN configuration (how the dashboard finds its database)

The generated dashboard resolves its database DSN at startup, in this order:

1. **`DATABASE_URL` environment variable** — wins over everything. Ideal for CI/CD and
   shell-level overrides.
2. **`.ENV` file** next to the dashboard binary — generated into the project folder by
   `yaga generate` (mode 0600, owner-readable only), containing `DATABASE_URL=<dsn>`.
   Edit it to point at a different database (e.g. switching from a test to a production
   database) without rebuilding.
3. **Non-secret localhost fallback** — only used when the config declared *no* connection
   at all (a config that does declare one refuses to start when no DSN is found).

```ini
# generated admin/.ENV  (0600)
# The DATABASE_URL environment variable overrides this value at runtime.
DATABASE_URL=postgres://user:pass@localhost:5432/mydb?sslmode=disable
```

Examples for the other drivers:

```sh
DATABASE_URL="file:./data/admin.db" ./admin                    # SQLite (relative path!)
DATABASE_URL="sqlserver://user:pass@localhost:1433?database=mydb" ./admin   # MSSQL
```

Notes:

- The DSN is **never** compiled into the dashboard binary — secrets stay out of the
  artifact. The source `yaga.yaml` still holds the plaintext `dsn:` under `connections:`
  (it drives generation), so treat `yaga.yaml` as sensitive.
- **`yaga generate` rewrites `.ENV`** from the config on every run. For deployment
  environments, edit `.ENV` (or set `DATABASE_URL`) *after* generating / packaging —
  `make package` includes `.ENV` in the release archive.
- The generated server runs a DB sanity query **before binding the port**, so a
  missing/uninitialised database is a fatal startup error instead of an occupied port.

---

## 2. Commands and flags summary

```
yaga init --db DSN  Introspect an existing database and generate yaga.yaml
yaga edit           Interactive YAML config editor (TUI)
yaga wedit          Web-based YAML config editor (browser, local HTTP server)
yaga generate       Generate the admin panel Go application (offline, no sqlc)
yaga validate       Validate the YAML configuration
yaga version        Print version information
```

### Global flags (usable with most commands)

| Flag | Short | Default | Meaning |
|---|---|---|---|
| `--config` | `-c` | `yaga.yaml` | Path to the YAML config file |
| `--out` | `-o` | `./admin` | Output directory for generated code |
| `--db` | `-d` | — | DB connection string for `init` (`postgres://…`, `sqlserver://…`, `mssql://…`, or a sqlite file path) |
| `--admin-password` | `-p` | random | Initial admin password for `init --db` scaffolding |
| `--force` | `-f` | false | Overwrite existing files |
| `--verbose` | `-v` | false | Verbose logging |
| `--skip-plugins` | `-s` | false | Skip loading declared plugins (for `generate`) |
| `--update` | — | false | Merge new tables into existing config instead of overwriting (`init` only) |

### `yaga init`

```sh
yaga init --db "postgres://user:pass@localhost:5432/mydb" [--config yaga.yaml] [--force] [--admin-password PASSWORD]
yaga init --db "postgres://user:pass@localhost:5432/mydb" --update               # Merge new tables into existing config
```

The **only** way to scaffold a project. Connects to the database, introspects its schema,
creates the `users`/`roles` auth tables and an `admin@…` user when they are missing, and
writes `yaga.yaml` containing one resource per table plus the captured `schema:` block.

**Update mode** (`--update`): Merges newly discovered tables into an existing `yaga.yaml`
instead of overwriting it. All user customisations (custom column labels, actions,
computed fields, navigation, pages, etc.) are preserved. The `schema:` block is fully
replaced (it remains the sole source of truth). Resources whose tables no longer exist
in the database are marked with an `# ORPHANED` comment but kept for manual review.
Navigation and pages are never auto-modified.

### `yaga edit` / `yaga wedit`

| Flag | Meaning |
|---|---|
| `--prompt TEXT` | Edit the config via AI instead of the TUI (`file://PATH` reads the prompt from a file, `~` expands) |
| `--apikey KEY` | OpenRouter API key (falls back to `OPENROUTER_API_KEY` env, then `.ENV`) |
| `--model MODEL` | Model id (falls back to `.ENV`, else `openrouter/auto`); `"lmstudio"` uses a local LM Studio server without a key |
| `--dry-run` | (with `--prompt`) print the proposed YAML + diff without writing |

`wedit` additionally accepts:

| Flag | Meaning |
|---|---|
| `--port N` | Web editor listen port (default `9090`) |
| `--open` | Open the editor in the default browser after binding |

### `yaga validate`

| Flag | Meaning |
|---|---|
| `--fix` | Auto-repair known-fixable problems (e.g. an inert list/card filter block) and rewrite the config (backup at `<config>.bak`) |
| `--dry-run` | Show what `--fix` would apply without writing anything |

---

## 3. Usage workflow

```
[1. database design] → [2. init --db] → [3. edit yaga.yaml] → [4. generate]
        → [5. build] → [6. run and test] → [repeat from 3. to fix/enhance]
```

**Schema evolution**: After adding tables to your database, run `yaga init --db DSN --update`
to merge new tables into your existing `yaga.yaml` without losing customisations.
Then continue the cycle from step 3.

### 3.1 Database design — the foundation

yaga is **schema-driven**: the database is the base, and yaga layers the admin behaviour
on top of it. A well-designed database produces a well-behaved admin panel almost for free.

Things that matter for your design:

- **Foreign keys are the wiring.** yaga introspects every FK and uses it to:
  - render `relation` fields / modal **record pickers** (options derived from the FK's
    label column),
  - show the related record's label instead of a raw id in lists and details (via
    `LEFT JOIN`),
  - generate **master–detail children** (`children:` blocks) automatically.
  - Declare `options_value`/`options_label` and the picker works without any custom SQL.
- **Database views** can be browsed like tables. The introspection marks them
  `view: true` in the captured `schema:` block, and generated resources for views are
  read-only (no create/update/delete).
- **Stored procedures** can be called from the dashboard. An `action` (or a hook) can
  invoke a procedure with `proc: <name>` — `CALL` on Postgres, `EXEC` on MSSQL, and for
  SQLite (which has no real procedures) the config's `procedures:` block provides named
  SQL batches executed inside one transaction.
- Keep a **primary key** on every table (single-column is easiest), choose a stable
  natural `label` column for the "name of a row" (yaga prefers `name`, then `title`,
  then `label`, then the first non-PK text column), and prefer `varchar`/`text` types that
  map cleanly (see the type mapping in the Schema section).

### 3.2 `init --db` — scaffold from the database

```sh
yaga init --db "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
yaga init --db "./mydata.db"                                            # SQLite
yaga init --db "sqlserver://user:pass@localhost:1433?database=mydb"     # MSSQL
```

What happens:

1. Connects to the database and introspects tables, columns, primary keys and foreign keys.
2. Creates the `users`/`roles` auth tables with default roles **and** an admin user when
   they are missing — login `admin@admin.test` / the generated password printed to the
   console (or `--admin-password`).
3. Writes `yaga.yaml`: a resource per table (list/detail/form sections, FK fields with
   pickers), the `auth:` block, one connection, and — critically — the captured
   **`schema:` block**, the **sole schema source** for generation.

The docs are written to disk; you then customise them before generating.

### 3.3 Edit the YAML spec

Pick one of the editors (detailed in Section 5):

```bash
yaga edit                 # terminal UI
yaga wedit                # browser editor + live preview + MCP
yaga edit --prompt "…"    # AI-assisted edit (experimental)
```

Typical things you tune after `init --db`:

- `panel` branding/language, sidebar, theme;
- which columns are `sortable` / `searchable`, column labels, default sort;
- which fields appear on create/update forms, field visibility, required hints;
- add views, filters, custom actions, hooks, children, policies, audit.

### 3.4 Generate

```bash
yaga generate                     # writes ./admin (fully offline — no DB, no sqlc)
```

The generator derives every query from the captured `schema:` block, emits the dashboard
source, and vendors the pre-built stylesheet + Chart.js. `--force` refreshes an existing
output. You can re-run this as often as you like — everything is regenerated from scratch.

> The AI path and the `--prompt` flow also round-trip through `yaga generate` after
> editing.

### 3.5 Build

```bash
cd admin
make build          # go mod tidy → go tool templ generate → go build
```

or manually:

```bash
go mod tidy
go tool templ generate
go build -o admin .
```

### 3.6 Run and test

```bash
make run                                # builds + runs, default port 8080
./admin --port 8080 --log full          # short forms: -p 8080 -l err
./admin -h                              # print all runtime flags
```

Then open `http://localhost:8080`, log in as the admin user, and exercise list/search/
sort/filter, create/edit/delete, actions, pages and the cards view.

### 3.7 Repeat from the YAML

Config change → `yaga generate` → `make build` → test. The loop between steps 3–6 is
where the product is shaped: labels, which columns appear, validation hints, actions,
looks, hooks, audit — all driven from YAML, no hand-written UI code, no run-time bleeding.

---

## 4. YAML blocks — what everything means

The full schema is documented in `README.md`, the authoritative `SPEC.md`. Here is the
map of the top-level blocks.

### Top-level keys

| Key | Required | Meaning |
|---|---|---|
| `version` | yes | Schema version string, e.g. `"1"`. Any non-empty value is accepted. |
| `panel` | yes | Panel identity, branding, layout and theme. |
| `connections` | — | DB connections (driver + dsn). The **first** entry is used by the generated app. |
| `schema` | — | The captured database schema — **the sole schema** source for generation (written by `init --db`, then hand-editable). |
| `auth` | — | Login table, identity/password fields, redirect after login, optional rate limit. |
| `navigation` | — | Sidebar groups + items. |
| `resources` | — | CRUD entities. At least one resource or page is needed. |
| `pages` | — | Custom dashboard pages with widgets. At least one resource or page is needed. |
| `audit` | — | Audit log of every create/update/delete/action (adds an `AuditLog` resource). |
| `procedures` | — | SQLite SQL-batch “stored procedures” (ignored on postgres/mssql). |
| `plugins` | — | Generation-time plugins that contribute resources/pages/hooks. |

### `panel` — identity and look

```yaml
panel:
  id: admin            # lowercase; part of generated handler names (AdminDashboard)
  path: /admin         # URL prefix, MUST start with "/" (base of all routes)
  name: "My Admin"     # shown in the sidebar + login page
  brand:
    logo: /assets/logo.svg
    colors: { primary: "#6366f1", secondary: "#64748b" }
  layout:
    sidebar: { collapsible: true, width: 280 }
    topbar:  { sticky: true }
    max_content_width: 7xl          # validates against an allowlist
  theme:
    dark_mode: true
    font: { family: "Inter, sans-serif", mono: "JetBrains Mono, monospace" }
```

### `connections` — the dashboard’s database

```yaml
connections:
  default:
    driver: postgres          # postgres (default) | sqlite | sqlite3 | mssql | sqlserver
    dsn: "postgres://user:pass@localhost:5432/db?sslmode=disable"
    pool: { max_open: 25, max_idle: 10, lifetime: 5m }
```

The driver determines `sql.Open`, the LIKE operator (`ILIKE` vs `LIKE`), placeholders
(`$N` vs positional `?`), identifier quoting (`"name"` vs `[name]`) and id Go types
(`int32` postgres/mssql, `int64` sqlite). The `dsn` is written to the project’s `.ENV`
(see 1.4).

### `schema` — the captured database

Written by `init --db`; the generator trusts it entirely (offline). You can hand-edit it
(add columns, adjust types) — `validate` and the editors warn/error when a resource
references a table/column that is missing here.

### `auth` — login

```yaml
auth:
  table: users                     # login lookup table
  login:
    fields: [email, password]      # identity + password (bcrypt in DB)
    redirect: /custom/dashboard    # where to go after login (a registered route)
    rate_limit: { max_attempts: 5, window_seconds: 300 }
```

### `navigation` — sidebar

```yaml
navigation:
  - group: "User Management"
    icon: users
    items:
      - { resource: User }
      - { resource: Role }
  - group: "Analytics"
    items:
      - { page: Dashboard }
      - { type: link, label: "Google Analytics", url: https://analytics.google.com, opens_in_new_tab: true }
```

Items link to a `resource` list, a `page` route, or an external `link`.

### `resources` — CRUD entities

```yaml
resources:
  - name: User            # REQUIRED PascalCase (lowercased → Go pkg/dir/URL: "user")
    label: Users          # UI label; default = name
    table: users          # optional DB table override (emitted by introspection)
    id_column: id         # optional row-key override (e.g. "ID" on mssql)
    id_type: int32        # optional id type override (e.g. int64 for bigint pks)
    import_csv: true      # adds an "Import CSV" button + POST /import/csv
```

Each resource has up to three views + extras:

#### `list` — table view

```yaml
    list:
      per_page: 20
      columns:
        - { name: id,         type: integer, sortable: true }
        - { name: name,       type: string,  searchable: true }
        - { name: email,      type: email,   sortable: true }
        - { name: status,     type: badge,   options: { active: success, inactive: warning } }
        - { name: role_label, label: Role,   type: text }   # FK label column (introspected)
      default_sort: -created_at      # "-" prefix = descending
      export: [id, name, email]     # optional CSV column subset
      filter:                       # collapsible filter section
        label: "Status"
        where: "status = $1"
        params: [ { name: status, label: Status } ]
```

Search, sort, filter and pagination are generated. `sortable`/`searchable` decide which
columns react to the search box / sort header.

#### `card` — grid / kanban view (optional)

```yaml
    card:
      fields:   [ { name: title }, { name: status, type: select, options: {todo: "To Do", doing: "In Progress"} } ]
      columns: 3              # cards per row (1..12)
      rows: 4                 # rows per page
      kanban_field: status    # optional → kanban board grouped by option value
      default_sort: -created_at
```

View-only, served at `/cards`, reachable via a “Cards” button on the list.

#### `detail` — record view (optional)

```yaml
    detail:
      fields:
        - { name: id,   type: integer }
        - { name: name, type: string }
        - { name: email, type: email }
```

Rendered as a read-only record page; keyed by the resource’s row key.

#### `computed:` — virtual columns (list / card / detail)

Any of the three views may add readonly columns that are derived by an SQL
expression **at query time**, instead of selecting existing table columns:

```yaml
    list:
      computed:
        - { name: total_gross, label: "Total gross", type: float,    expression: "helpers.round(total * 1.21, 2)" }
        - { name: age_days,    label: "Age (days)",  type: integer,  expression: "helpers.date_diff(helpers.now(), created_at)" }
      filter:
        where: "total_gross > $1"        # computed columns work in filter.where
```

- `name` is the column key (unique within its block, must not collide with a real
  column), `type` one of the shared field types, `expression` the SQL fragment.
- The expression may reference **real table columns** (including `{fk}_label`
  join aliases) and **earlier computed names in the same block**. It is passed
  verbatim to the configured driver — use that driver's SQL syntax, not yaga's.
- `helpers.*` tokens are expanded at generation time into driver-correct SQL:
  `helpers.date_diff(a,b)` / `helpers.year_diff` / `helpers.month_diff`,
  `helpers.coalesce`, `helpers.ifnull` (IFNULL/ISNULL/COALESCE per driver),
  `helpers.round(x,n)` (numeric cast on postgres), `helpers.now()`. Calls may
  nest (`helpers.date_diff(helpers.now(), created_at)`); unknown helpers or
  wrong arities are emitted verbatim.
- Computed columns render and scan like view columns, but are never sortable or
  searchable and never appear on forms. A filter that references a computed name
  is supported (the query is generated from a derived-table wrapper).
- Computed fields are **read-only outputs** — there is no persistence, no write
  path, and they do not affect `init` introspection.

#### `form` — create / update / delete

```yaml
    form:
      create:
        fields:
          - { name: name,      type: text,     required: true }
          - { name: email,     type: email,    required: true }
          - { name: password,  type: password }            # bcrypt-hashed before insert
          - { name: role_id,   type: relation, options_value: id, options_label: name }
          - { name: status,    type: select,   options: { active: Active, inactive: Inactive } }
        hooks: { before: [ { name: validate_domain, fn: ValidateUserDomain } ] }
      update:
        fields: [ { name: name }, { name: email }, { name: status } ]
      delete: {}                 # presence enables the delete route
    children:                   # optional master-detail sections
      - name: Lines
        resource: OrderLine
        column: order_id
        columns: [ { name: qty, label: "Qty", type: integer } ]
```

- `select`/`relation` fields with resolvable options render as a **modal record picker**;
  `copies:` auto-fills sibling form fields from the picked row.
- The shared form template renders the **union** of create + update fields
  (`visible: [create]`/`[update]` fine-tunes per context).
- `delete: {}` enables the delete button + POST route.

#### `policies` — RBAC (optional)

```yaml
    policies:
      view_any: "admin|manager"
      view:      "admin|manager"
      create:    "admin"
      update:    "admin|manager"
      delete:    "admin"
```

The generated app checks the logged-in user’s role against the pipe-separated list per
route.

#### `audit`

```yaml
audit:
  enabled: true
  table: audit_log
  include_values: true      # store changed values as JSON
  policy: "admin"           # who can view the generated AuditLog list
  exclude_resources: [Users]
```

Adds a list-only `AuditLog` resource + “Audit Log” navigation group, and wraps every
mutating op + audit insert in one transaction.

### `pages` — custom dashboards

```yaml
pages:
  - name: Dashboard
    default: true                 # landing page after login (mounted at / and /dashboard)
    widgets:
      - { type: stat,       label: "Total Users",   query: "SELECT COUNT(*) FROM users", icon: users }
      - { type: chart,      label: "Revenue",       query: "SELECT month, total FROM revenue ORDER BY month",
          chart: { type: line } }
      - { type: table,      label: "Recent Orders", query: "SELECT id, customer_id, total FROM orders ORDER BY created_at DESC LIMIT 5",
          data_columns: [id, customer_id, total] }
      - { type: list,       label: "Top Products",  query: "SELECT name, price FROM products ORDER BY price DESC LIMIT 5" }
      - { type: html,       label: "Note",          query: "SELECT note FROM notes LIMIT 1" }   # trusted input only
```

Widgets: `stat`, `stats_grid`, `chart` (line/bar/pie/area), `table`, `list`, `html`.
`query` is raw SQL executed at request time; widget errors are logged and never blank the
page.

### Field types

`type` is a **UI rendering hint** — the actual DB column types come from the
`schema:` block. Applies to list `columns`, detail `fields`, card `fields` and form
`fields`: `string`, `text`, `integer`, `float`, `email`, `password`, `boolean`, `select`,
`datetime`, `date`, `badge`, `image`, `file`, `relation`, `json`, `gps`.

---

## 5. Editors

`yaga.yaml` is a plain YAML file; edit it with any text editor — but yaga ships four
integrated ways:

### 5.1 TUI editor — `yaga edit`

Keyboard-driven terminal UI (3 panes: navigation list | content | status bar) covering
every config section with live validation.

- `Ctrl+S` save (validates first), `Ctrl+V` validate, `Ctrl+Q`/`F10` quit, `Esc` back.
- `Ctrl+P` opens a **cd-style path navigator** (e.g. `/Resources/User/List/Columns`,
  `../Columns`), `Tab` autocompletes.
- `Ctrl+O` goes home. Every button also gets a `Ctrl+<letter>` shortcut shown in its
  label.
- In list editors `a`/`d` add/delete rows, `Enter` edits; `stringMapPage` edits
  maps (`options:`, query params, `copies:`).

Unrelated files are left alone; only the config you edit is touched.

### 5.2 Web editor — `yaga wedit`

```sh
yaga wedit                       # http://localhost:9090
yaga wedit --port 9091 --open    # custom port / open the browser
```

A local HTTP server with an embedded single-page app:

- Tab editors for panel, connections, auth, navigation, resources, pages;
- a **Validate** screen running the full validator (+ auto-fix) live;
- a **Preview** tab rendering a mock dashboard and per-resource list views (page/resource
  mocks, light/dark theme);
- a raw-YAML tab;
- edits are held **in memory** — explicit **Save** writes to disk (the MCP `save` tool
  backs up `<config>.bak` first);
- multiple browser tabs **live-sync** (SSE + revision counter); a stale tab is warned before
  it can silently overwrite newer changes.

### 5.3 AI-assisted edit — `yaga edit --prompt "…"`

```sh
yaga edit --prompt "Change the dashboard title to: Order management"
yaga edit --prompt file://./instructions.txt
```

Sends the full config to a model and merges back **only the changed sections** (validated;
an invalid merge is retried once, then the file is left untouched). Useful for quick
single-purpose edits. For serious AI-driven work, prefer the **MCP** route (see below),
which sees the same in-memory config as the web editor.

### 5.4 MCP (AI agents over `wedit`)

`yaga wedit` serves a **Model Context Protocol (Streamable HTTP)** endpoint at
`POST /mcp` (also `GET /mcp`), so AI agents can read and edit the live config through
structured tools: `get_config`, `get_value`, `set_value`, `merge_yaml_fragment`,
`add_resource`, `remove_resource`, `add_column`, `add_field`, `add_nav_item`,
`remove_nav_item`, `validate`, `save`, … Edits are validated (an invalid edit is rejected)
and propagate to every connected browser tab automatically.

To use from opencode (or another MCP client):

```json
{ "mcp": { "yaga": { "type": "remote", "url": "http://localhost:9090/mcp" } } }
```
Full example Opencode MCP configuration:
```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "yaga": {
      "type": "remote",
      "url": "http://localhost:9090/mcp",
      "enabled": true
    }
  }
}
```
---

## 6. Actions & Hooks

Two mechanisms to run your own logic from the dashboard at request time.

### 6.1 Actions — buttons that do things

An **action** is a custom button on a resource (per-record or bulk) that runs a piece of
logic when clicked. Use them for operations the CRUD builder can’t express: “Mark as
shipped”, “Archive”, “Recalculate”, “Call a stored procedure”.

- `query:` inline raw SQL executed with the record id bound as `$1`;
- `proc:` the name of a stored procedure (Postgres `CALL`, MSSQL `EXEC`, SQLite
  `procedures:` batch);
- `script:` an embedded Lua body (request-time execution under a 5 s timeout);
- each action gets a POST route `/<panel>/<resource>/{id}/action/<name>` (unknown names
  → 404);
- `bulk: true` renders row checkboxes + a toolbar; the bulk loop runs inside **one
  transaction**.

**Example — SQL action:**

```yaml
    actions:
      - name: mark_done
        label: "Mark done"
        icon: check
        color: success
        requires_confirmation: true
        query: "UPDATE orders SET status = 'done' WHERE id = $1"
```

**Example — procedure action:**

```yaml
      - name: archive
        label: "Archive"
        proc: sp_archive_customer      # CALL sp_archive_customer($1) on postgres,
                                       # EXEC sp_archive_customer $1 on mssql
```

On SQLite (no real stored procedures), the same `proc:` refers to a named SQL batch
declared under the top-level `procedures:` block, executed inside one transaction:

```yaml
procedures:
  - name: sp_archive_customer
    description: "Archive a customer and record the event"
    sql: |
      UPDATE customers SET status = 'inactive' WHERE id = $1;
      INSERT INTO customer_log (customer_id, msg) VALUES ($1, 'archived');
```

**Example — Lua action:**

```yaml
      - name: flag_audit
        label: "Flag"
        script: |
          local row = db.query_one("SELECT * FROM orders WHERE id = ?", ctx.id)
          if row ~= nil then
            db.exec("UPDATE orders SET status = 'flagged' WHERE id = ?", ctx.id)
          end
```

### 6.2 Hooks — run before / after a create, an update, a delete or an action

A **hook** is a piece of code attached to the lifecycle of a mutating operation:

- `form.create` → `before` (with `scope.id` = 0) and `after` (with the new row id);
- `form.update` / `form.delete` → `before` / `after`;
- `action` → `before` / `after`;

Each hook in one of four kinds:

1. **`fn: <Name>`** — the generator emits a compile-ready `func <Name>(s *hooks.Scope)`
   stub into `internal/hooks/hooks.go`; you fill in the Go body. Full power.
2. **`sql: "…"`** — an inline SQL statement executed with `db.ExecContext(…, scope.ID)`.
3. **`proc: <name>`** — call a stored procedure with the record id.
4. **`script: |`** — an embedded Lua body (in the same context as script actions).

**Example — SQL hook (after create):**

```yaml
    form:
      create:
        hooks:
          after:
            - name: notify
              sql: "INSERT INTO notifications (target, msg) VALUES ($1, 'user created')"
```

**Example — Lua hook (set a default before create):**

```yaml
    form:
      create:
        hooks:
          before:
            - name: default_status
              script: |
                if ctx.values["status"] == nil then
                  ctx.values["status"] = "draft"
                end
```

`ctx` exposes `id`, `table`, `action`, `user`, `role`, and `values` (for before-
create/update, changes are written back to the row). Host helpers: `db.exec(sql,
vars...)`, `db.query(sql, vars...)`, `db.query_one(sql, vars...)` (positional `?`
bound on sqlite, auto-renumbered to `$N` on postgres/mssql), `abort(msg)` (stops with a
visible flash / 400) and `log(msg)`. On create, the insert switches to a driver-aware
`RETURNING` / `OUTPUT INSERTED.<id>` so after-create hooks receive the real row id. A
hook error aborts the request with HTTP 500.

---

## 7. Important technical notes

- **Building generated app requires a Go toolchain.** No npm, no sqlc, no Tailwind
  binary — but `go` must be on the machine that runs `make build`. For machines without
  Go, deploy the binary (`make package` bundles binary + static + `.ENV` + migrations).
- **The DSN is a runtime concern.** It lives in `.ENV` (0600) next to the binary, with
  `DATABASE_URL` env override, and is never compiled in. `yaga.yaml` still contains the
  plaintext DSN.
- **`.ENV` is regenerated by `yaga generate`**; for per-deployment databases set
  `DATABASE_URL` (env) or edit `.ENV` after generation.
- **`yaga generate` is fully offline** — it never hits the database and never runs sqlc
  or a Tailwind binary. Schema comes from the captured `schema:` block.
- **`init --db` is the only scaffold.** Without `--db`, `init` errors; there is no empty
  template or `--demo`.
- **Default admin login** — `admin@admin.test` / the one-time password printed by
  `init --db` (or `--admin-password`); roles table defaults to `admin`/`manager`/`user`.
- **The server pre-checks the DB** before binding the port (sanity `SELECT 1` against the
  auth table), so a broken/missing DB is a fatal startup error, not a runtime waiting
  behind an open port.
- **Session secret** — set `SESSION_SECRET` (≥ 32 chars) for persistence; with
  `APP_ENV=production` a missing secret is fatal. Otherwise sessions reset on restart.
- **`query:`/`count_query:`/`populate_query:`/`params:` (and the legacy `sqlc:` block)**
  are accepted but ignored in D11 — handlers use raw SQL + the `schema:` block instead.
- **Field `type:` is a hint**; the DB types are authoritative. Matching db/schema columns
  keep the editors and Validate happy.
- **Generated code contains no comments** and the generated app has **no runtime
  dependency** on the yaga module — deploy the binary and you’re done.
- **Security defaults** ship out of the box: CSRF, session rotation, upload validation
  (HTML/SVG rejected), safe `500/404` responses, CSV formula-injection shell, sort/order
  whitelist, optional login rate limiting (details in `README.md` → Security).
- `yaga generate` (and every other command) also writes an `AGENTS.md` agent guide into
  the current directory when absent — it tells AI agents how to work with the generated
  project.
- Full references: `README.md` (quick start + config reference), `SPEC.md`
  (authoritative schema), `AGENTS.md` (agent + maintainer details), `SPEC_summary.md`
  (feature matrix).
