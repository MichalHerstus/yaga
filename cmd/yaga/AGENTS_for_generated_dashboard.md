# yaga generated dashboard (Agent Guide)

This project is a **generated** admin dashboard produced by [yaga](https://github.com/MichalHerstus/yaga)
from two sources of truth:

1. `yaga.yaml` (repo root) — the declarative spec: panel, resources, pages, navigation, auth, actions, hooks, policies.
2. The `schema:` block in `yaga.yaml` — the captured database schema (tables, primary keys, foreign keys, column types), written by `yaga init --db DSN` and never re-derived at build time.

Everything under `admin/` is a build artifact. This guide exists so an AI agent
modifies this project correctly: edit the YAML, regenerate, rebuild — and
only hand-write the two Go files that yaga deliberately leaves to the user.

## THE GOLDEN RULE

**Never hand-edit generated Go code.** The generated files are rewritten on every
`yaga generate` run; manual edits are silently lost and cause confusing drift.

There are exactly **two exceptions** — the files yaga emits as *stubs or seams*
for the user to fill in:

- `admin/internal/hooks/hooks.go` — the **fn hook** stubs. yaga writes
  `func <Name>(ctx context.Context, db *sql.DB, s Scope) error { return nil }`
  for every `fn:` hook declared in the YAML. Implementing those bodies is your
  job (see below). Signature: `Scope{ID int64, Table, Action string, Values map[string]interface{}}`.
- `admin/internal/panel/resources/<res>/actions.go` — the **custom action**
  handler switch. Only reach in when an action's inline `query:`/`proc:`/`script:`
  in the YAML cannot express the logic (multi-step, loops, extra args, external
  calls). Prefer the declarative path first (see "Custom actions"). Any
  hand-edit here is **lost on regeneration** — re-apply it after every
  `generate` or document it in this file.

All other generated files are **off-limits**, including:
`admin/main.go`, `admin/internal/panel/**` (handlers/router/auth),
`admin/internal/views/**` (templ), `admin/internal/data/**` (generated data queries),
`admin/internal/viewmodels/**`, `admin/internal/assets/**`,
`admin/tailwind.config.js`, `admin/Makefile`, `admin/static/**`.
(There is no `package.json` and no sqlc — the dashboard builds fully offline.)

## Build & regeneration

```sh
# from the repo root
./yaga generate --config yaga.yaml --out admin --force   # regenerate
cd admin
make                                                           # templ + go build (no tailwind, no npm)
./admin --port 8080                                            # run
```

- `generate` is fully offline: it reads the captured `schema:` block (never the
  live DB, never sqlc). There is **no Tailwind, no npm and no sqlc** — the
  stylesheet and Chart.js are pre-built and vendored by the generator, so a
  bare build has zero network. `make` targets: `build` (default: `go mod tidy`
  → `go tool templ generate` → `go build`), `templ`, `tidy`, `run`,
  `package`, `clean`.
- Sanity-check a config without building: `./yaga validate --verbose`
  (parses YAML + the schema block; does NOT verify SQL against a live DB).

## How the YAML drives the code (the dependency graph)

Read every edit against this chain — a broken link fails the build or renders wrong data:

```
yaga.yaml ──► resource name ──► Go package / URL segment
      │
      ├── list / detail / form.populate queries ──► Get<Resource> methods in admin/internal/data  (SQL derived from the schema block)
      ├── detail.query / form.update.populate_query ──► names that Get<Resource> method
      ├── relation/select fields (options_value + options_label) ──► SELECT value,label FROM <fk table>  (from schema FK metadata)
      │      └── options_sql: overrides the FK-derived option query
      ├── FK *_label list columns ──► LEFT JOIN reconstructed from the schema block's FK metadata
      ├── actions ──► switch in actions.go (inline SQL / proc)
      ├── hooks ──► stubs in internal/hooks/hooks.go + inlined db.ExecContext
      └── pages/widgets ──► raw SQL executed at REQUEST time (tables/columns must exist in the schema block + live DB)
```

### Naming rules (must match exactly)

- Resource `OrderLine` → Go package/dir `orderline`, URL `/admin/orderline`.
  PascalCase in YAML is lowercased verbatim (`User`→`user`, `OrderLine`→`orderline`).
- Field naming (matching `snakeToPascal`): lowercase all, split only on `_`, `id`→`ID`.
  So YAML column `role_id` → Go field `RoleID`, `customer_name` → `CustomerName`.
- `detail.query` / `form.update.populate_query` name a generated `Get<Resource>`
  method in `admin/internal/data` (default `GetByID`); there is no separate SQL
  file tree to cross-reference.
- `table:`/`id_column:`/`id_type:` override conventions only when the DB differs
  (not the case here — sqlite, table name = lowercased resource, PK = `id`, `int64`).

### Drivers: postgres / sqlite (current) / mssql

The driver comes from the first `connections:.*.driver` value in the YAML.
Acceptable values: `postgres` (default when the key is absent), `sqlite`/`sqlite3` (both map to the pure-Go `modernc.org/sqlite` driver — no CGO, cross-compiles to Windows/Linux/macOS),
`mssql`/`sqlserver`. **This project currently uses `sqlite`**, but the YAML
and the generated code must stay driver-correct in case it changes.
When you flip the driver, re-run `generate` (it rewrites `sql.Open`, placeholders,
pagination, sanity check) AND keep every inline `query:`/`sql:`/widget SQL in the
new dialect — the generator does not translate hand-written SQL for you.

| Concern | postgres | sqlite (current) | mssql |
|---|---|---|---|
| YAML `driver:` | `postgres` | `sqlite`/`sqlite3` | `mssql`/`sqlserver` |
| `sql.Open` driver (main.go) | `pgx` | `sqlite` (via blank-imported `modernc.org/sqlite`) | `mssql` |
| bind placeholders | `$1..$N` | `?` | `$1..$N` (loose `$N`→`@pN`) |
| LIKE operator | `ILIKE` | `LIKE` | `LIKE` (case-insensitive collation) |
| pagination | `LIMIT $1 OFFSET $2` | `LIMIT ? OFFSET ?` | `OFFSET $2 ROWS FETCH NEXT $1 ROWS ONLY` (REQUIRES an ORDER BY) |
| data-query id type | `int32` | `int64` | `int32` (bigint PK → `id_type: int64`) |
| create-hook id capture | `RETURNING <id>` | `RETURNING <id>` | `OUTPUT INSERTED.<id>` |
| stored procedures | `CALL name($1)` | not supported | `EXEC name $1` |
| startup sanity check | `SELECT 1 FROM {table} LIMIT 1` | same | `SELECT TOP 1 1 FROM {table}` |
| go.mod extra | `github.com/jackc/pgx/v5` | `github.com/modernc.org/sqlite` | `github.com/microsoft/go-mssqldb` |

#### Postgres rules
- Inline SQL (`query:`/`sql:`/widgets) uses `$N` placeholders, `ILIKE`, `LIMIT/OFFSET`.
- `proc:` hooks/actions → `CALL name($1)`.
- Bind args are ordered with the LIMIT/OFFSET params first, then the search
  clauses numbered `$3..`.

#### SQLite rules (current project)
- Bind placeholders are `?` (positional, SQL-text order — search args before `LIMIT ? OFFSET ?`).
- LIKE operator is `LIKE` (not ILIKE). Pagination `LIMIT ? OFFSET ?`.
- Data-query ids are `int64`.
- No stored procedures: `proc:` hooks/actions are **ignored** on sqlite — use `query:`/`sql:`.

#### MSSQL rules
- **PascalCase columns** (`CeleJmeno`, `ZamestnanecID`). The naming rule
  lowercases + splits only on `_`, so a column like `ZamestnanecID` maps to Go
  field `Zamestnanecid`; `role_id` still maps to `RoleID`. When introspection
  detects a non-`id` key it emits `id_column: ID`, and bigint PKs emit `id_type: int64`.
- `$N` placeholders; go-mssqldb validates arg count against the **highest** `$N`.
- Pagination `OFFSET/FETCH` **requires an ORDER BY**; when no sort is configured
  the generator emits `ORDER BY (SELECT NULL)`. ORDER BY is omitted from
  derived-table `options_sql`/list queries entirely.
- No `RETURNING` — create-hook id capture uses `OUTPUT INSERTED.<id>`.
- `proc:` hooks/actions → `EXEC name $1` (bound `$1` passes positionally to the proc).
- Startup sanity check is `SELECT TOP 1 1 FROM {table}`.

### Custom actions

Declared in YAML, per resource:

```yaml
actions:
  - name: complete            # URL /admin/order/{id}/action/complete
    label: Complete
    icon: check
    color: success
    requires_confirmation: true
    bulk: false
    query: UPDATE orders SET status = 'completed' WHERE id = $1   # $1 = record id (works on sqlite)
    hooks: null
```

- `query:` is inline SQL bound to `$1` = the record id. This is the **preferred** way
  to add an action — no Go code touched. Keep the SQL in the **current driver's dialect**
  (`$N` for postgres/mssql, `?` for sqlite).
- `script:` embeds a Lua body instead of SQL (`query:`/`proc:`/`script:` are mutually
  exclusive). It runs at request time via gopher-lua under a 5 s timeout with a `ctx`
  table (`id`, `table`, `action`, `user`, `role`, `values`) and host globals
  `db.exec/query/query_one` (positional `?` placeholders), `abort(msg)` (redirects to the
  list with `?flash=<msg>`) and `log(msg)`.
- `bulk: true` runs the same SQL once per selected id from `bulk.go` (no hooks run).
- `proc:` is postgres/mssql only — on this sqlite project use `query:`.
  Postgres emits `CALL name($1)`, mssql emits `EXEC name $1`.
- Editing the generated `actions.go` case is the last resort (see Golden Rule).

### Hooks

Attach to `form.create/update/delete.hooks.before/after` or an action's `hooks`.

```yaml
form:
  create:
    hooks:
      before:
        - name: validate_domain
          fn: ValidateUserDomain      # stub → admin/internal/hooks/hooks.go
      after:
        - name: notify
          sql: "INSERT INTO notifications (target, msg) VALUES ($1, 'user created')"
```

- `fn:` → a stub named `func <Fn>(ctx context.Context, db *sql.DB, s Scope) error`
  in `admin/internal/hooks/hooks.go`. **Implement the body there** — it is the
  one generated file you own. `Scope.ID` = 0 before-create, new row id after-create,
  path id otherwise.
- `sql:` → inlined `db.ExecContext(..., "<sql>", scope.ID)` at generate time;
  keep the SQL well-formed for the **current driver's dialect**.
- Returning a non-nil error aborts the request with HTTP 500.
- Adding a hook requires re-running `generate` (stub emission) and `make`.
- On postgres/mssql the create handler switches to a `RETURNING`/`OUTPUT INSERTED`
  id capture so after-create hooks see the new row id (`Scope.ID`).

### Relations / FK labels

- A `select`/`relation` form field with `options_value` + `options_label` renders
  a modal record picker. The option query is derived at generate time from the
  schema block's FK metadata (`SELECT {options_value}, {options_label} FROM {fk table}`),
  or from `options_sql:` when set. The two columns select a key + a label.
- List views show FK targets via `<fk>_label` columns (e.g. `customer_name`).
  The generated handler reconstructs the LEFT JOIN from the schema block's FK
  metadata — if you rename a column or drop the relation field, list queries
  break with `column "<fk>_label" does not exist`.

### Pages & widgets

`pages:` → Dashboard (`/Dashboard`, default) and Reports (`/Reports`).
Widgets (`stat`, `stats_grid`, `chart`, `table`, `list`, `html`) run **raw SQL at
request time** — no generation-time checks. Verify every table/column you
reference exists in the `schema:` block of `yaga.yaml` and in the live DB
(sqlite: `admin/data/admin.db`; postgres/mssql: the configured server). The widget
SQL must also be in the current driver's dialect (e.g. `strftime` is sqlite-only;
postgres uses `to_char`/`date_trunc`). `generate` does not re-seed the DB.

### Schema editing rules

- The database schema lives in the `schema:` block of `yaga.yaml`, captured by
  `yaga init --db DSN`. There is no `admin/sql/` tree in the generated output.
- To change the schema: apply the DDL to the live DB, then re-capture with
  `yaga init --db DSN --force` (rewrites the `schema:` block), or hand-edit the
  block carefully — table name, `pk`, columns, and `foreign_keys` must match the
  live DB or generated queries fail at request time.
- `generate` never touches the live DB and never executes the schema block.

## Workflow checklist (any change)

1. Edit `yaga.yaml` (schema block + resources/pages/navigation).
2. Cross-check every referenced name: `detail.query`/`populate_query` ↔ the
   resource, FK label columns ↔ relation fields + schema FK metadata, page URLs ↔
   navigation, policy roles ↔ `auth` login.
3. `./yaga validate` for YAML sanity.
4. `./yaga generate --config yaga.yaml --out admin --force`.
5. `cd admin && make` (fix templ errors if any).
6. Run and click through the affected screens.

## Do / Don't

- DO edit `yaga.yaml` (including the `schema:` block) freely — it is the source of truth.
- DO implement `fn:` hook bodies in `admin/internal/hooks/hooks.go`.
- DON'T touch any other generated Go/templ/JS/config file.
- DON'T invent table/column names, query names, or URLs that don't exist — verify against the YAML + schema block first.
- DON'T expect `generate` to update the live DB — apply DDL manually, then re-capture `schema:`.
- DON'T mix dialects: keep every inline SQL consistent with the `connections.default.driver`
  in the YAML (placeholders, LIKE/ILIKE, pagination, date functions).
