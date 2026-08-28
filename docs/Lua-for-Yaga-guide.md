# Lua for YAGA — scripting guide

This guide covers the Lua you can write inside **yaga** configs: the `script:`
bodies of actions and lifecycle hooks. It is deliberately scoped — Lua is a big
language, but yaga only runs a small, sandboxed slice of it at request time, so
this document covers exactly the syntax, the `ctx` scope, the host API and the
DB bindings you can rely on. Anything standard Lua offers **outside** this
range (filesystem, `os`, sockets, `require` of files) is intentionally not
available here.

- Who uses it: config authors (you), not end users. Scripts are trusted
  config text, but they are still sandboxed and time-limited.
- Where it runs: inside the **generated dashboard** at request time, once per
  invocation, under a fixed **5 second** timeout (a script that exceeds it is
  aborted and the request fails with an HTTP 500).
- How it runs: yaga wraps your body as one function `run(ctx)`; your body is
  the function's statements. You never write the `function run(ctx) … end`
  wrapper yourself.

```yaml
actions:
  - name: archive
    script: |          # this block is the body of run(ctx)
      if ctx.role ~= "admin" then
        abort("Only admins can archive")
      end
      db.exec("UPDATE orders SET status = 'archived' WHERE id = ?", ctx.id)
```

A `script:` is **mutually exclusive** with the other bodies: a hook uses
exactly one of `fn` / `sql` / `proc` / `script`; an action uses exactly one of
`query` / `proc` / `script`. To switch an existing hook or action to Lua, set
`script:` **and** remove (null) the old body key at the same time.

---

## 1. YAGA script syntax (the slice you get)

### Comments

```lua
-- single line comment
local x = 1  -- trailing comment

--[[ block comment ]]
```

### Values and types

| Lua value | YAGA meaning |
|---|---|
| `nil` | "nothing here". An unset/empty form field reads as `nil` (see `ctx.values`). |
| `true` / `false` | booleans |
| number | one numeric type (double). Whole numbers are passed to the DB as integers, fractional values as floats. |
| string | text; single or double quotes, both fine (`'ok'` == `"ok"`); `[[ long string ]]` for multi-line |
| table | the only "container": arrays *and* maps |

Lua converts a value to its truthiness in `if`/`and`/`or`: only `nil` and
`false` are falsy — `0`, `""`, `0.0` and empty tables are **true**.

```lua
if "" then print("true") end   -- prints "true": an empty string is truthy
if 0 then print("true") end    -- prints "true": zero is truthy
if nil then else print("false") end
```

### Variables

```lua
local name = "Anna"        -- prefer `local` — a global is a table entry too
local qty  = 5
local ok   = qty > 0
name  = "changed"          -- reassign
a, b  = 1, 2               -- multiple assignment

local fmt   = string.format -- hold references
local myfunc = function(k) return k * 2 end
```

### Operators

| Kind | Operators |
|---|---|
| Arithmetic | `+` `-` `*` `/` `%` (modulo) `^` (power) unary `-` |
| Concatenation | `..` (`"v" .. 5` → `"v5"`) |
| Comparison | `==` `~=` (not-equal) `<` `<=` `>` `>=` |
| Logic | `and` `or` `not` (short-circuit, returns one operand, not a boolean) |
| Length | unary `#` (`#t` = table length, `#s` = string length) |

```lua
local label = ctx.table .. "[" .. tostring(ctx.id) .. "]"
-- AND/OR return a value:
local status = ctx.values["status"] or "draft"   -- "draft" when nil
if x > 0 and y > 0 then end
if not ok then abort("not ok") end
```

### Control flow

`if / elseif / else / end`, `while`, and the two `for` loops:

```lua
if ctx.role == "admin" then
  authorized = true
elseif ctx.role == "manager" then
  authorized = true
else
  abort("forbidden")
end

-- while
local i, total, rows = 1, 0, db.query("SELECT amount FROM open_orders")
while i <= #rows do
  total = total + rows[i]["amount"]
  i = i + 1
end

-- numeric for
local acc = 0
for i = 1, 10 do acc = acc + i end
for i = 10, 1, -1 do print(i) end

-- generic for (array part) / pairs (map part)
for idx, row in ipairs(db.query("SELECT name FROM users")) do
  print(idx, row["name"])
end
```

> **Note:** `return` inside the body returns from `run`, which ends the script
> cleanly — but for "stop here with a message" prefer `abort(msg)`.

### Functions

```lua
local function double(n) return n * 2 end

local function choose(sm)
  if sm == nil then return false, "missing" end   -- multiple return values
  return true, sm
end
local ok, msg = choose()
```

A `local function` defined at the top of the body is usable by the rest of the
body. The host functions you call (`db.exec`, `db.query`, `abort`, …) behave
like plain Lua functions — see §3 for their exact signatures.

### Tables — the everything-collection

```lua
local arr = { "a", "b", "c" }            -- array: keys 1..n
local map = { name = "Pipe", qty = 3 }   -- map (record)
local mix = { 1, 2, ["k"] = "v" }

-- access
arr[1]        -- "a" (arrays start at 1)
map.name      -- "Pipe"    (dot is sugar for ["name"])
map["qty"]    -- 3

-- write / remove
map.size = 4             -- add or update
map.name = nil           -- REMOVES the key
table.insert(arr, "d")   -- append
table.remove(arr, 1)     -- remove first (1-based)

#arr        -- length (count of the 1..n prefix)
```

yaga mostly hands you **row tables** (maps of column → value) from `db.query`
/ `db.query_one` and you iterate them with `pairs` / index them by `["col"]`.

### The string & math libraries

The standard `string` and `math` tables are parts of the yaga runtime:

```lua
string.lower  string.upper  string.sub(s, i, j)  string.len(s)
string.format  string.find  string.rep
string.match  string.gsub

math.abs math.ceil math.floor math.fmod math.min math.max
math.modf math.random  math.huge
```

`string.format` uses `%s %d %f %q`:

```lua
local msg = string.format("%s id=%d cost=%.2f", ctx.action, ctx.id, 12.5)
db.exec("INSERT INTO events (msg) VALUES (?)", msg)
```

Caveat: `%` inside a format string is a directive; concatenate with `..`
instead if your text contains literal `%`.

### Table/other helpers you get

`tostring`, `tonumber`, `type`, `pairs` / `ipairs`, `pcall` / `xpcall`,
`next`, `select`, `error`, `assert`, `unpack`, the length operator `#`,
`string`, `math`, `table.insert/remove/concat`. `print(...)` writes to the
server's stdout; for instrumented logging from a hook use `log(msg)` instead
(titled `[lua]` in the server log). In the wedit editor, the action **Run**
button captures `print` output and shows it in the result modal instead of the
terminal (`log` still goes to the wedit server log).

### What is NOT available

- `io`, `os`, `debug`, `coroutine` — no filesystem, no process/env access,
  no reflection hooks.
- `require` only resolves the preloaded standard modules (`string`, `table`,
  `math`, base) — it can never load a file, a C module, or your disk.
- No sockets, no HTTP.
- `io*`/`os*` globals are simply `nil`; using them raises a Lua error and the
  request fails with 500.

---

## 2. The `ctx` scope

Every script receives one argument, `ctx` — a table:

| Field | Type | Content |
|---|---|---|
| `ctx.id` | number | the row id the script runs on (0 during a **before-create** hook) |
| `ctx.table` | string | the resource's table name |
| `ctx.action` | string | `"create"`, `"update"`, `"delete"`, or the action name |
| `ctx.user` | string | the authenticated user (session `name`) |
| `ctx.role` | string | the authenticated user's role |

### `ctx.values` (create/update hooks only, in/out)

For **before-create** and **before-update** hooks, `ctx.values` is a map of the
submitted form fields, keyed by column name. Two rules shape how you use it:

1. **An absent / empty-string field is `nil`.** Unset form inputs are skipped
   when the values table is built, so `ctx.values["status"] == nil` means "the
   user did not send a status". To *intentionally* blank a field, assign
   `""` explicitly.
2. **Whatever you set is written back** into the INSERT / UPDATE after the
   script finishes. This is how a before-hook sets defaults or transforms a
   value that the DB insert actually stores.

```lua
if ctx.values["status"] == nil then
  ctx.values["status"] = "draft"               -- INSERT stores 'draft', not NULL
end
ctx.values["email"] = string.lower(ctx.values["email"] or "")
```

Only keys that exist in the form are ever copied back; adding brand-new keys
has no DB effect. `ctx.values` is **not** populated for `delete` hooks or
custom-action scripts (in those cases read data with `db.query`, not
`ctx.values`).

---

## 3. The host API: `db`, `abort`, `log`

### `db.exec(sql, ...args)` — run a statement, ignore the rows

Returns a table with `rows_affected` and (when the driver reports it)
`last_insert_id`.

```lua
local res = db.exec("UPDATE orders SET status = 'paid' WHERE id = ?", ctx.id)
if res["rows_affected"] == 0 then
  abort("No order found")
end
```

### `db.query(sql, ...args)` → array of row tables

```lua
local rows = db.query("SELECT id, name FROM users WHERE role = ?", "admin")
for i = 1, #rows do
  print(rows[i]["name"])
end
```

### `db.query_one(sql, ...args)` → one row table or `nil`

```lua
local u = db.query_one("SELECT * FROM users WHERE email = ?", ctx.values["email"])
if u == nil then
  abort("Unknown user")
end
local name = u["name"]
```

### Placeholders and parameters

- Arguments are bound **positionally**: `?` (or `$N` — both work everywhere)
  in order. `?` is kept as-is on SQLite; the runtime rewrites `?` to `$N`
  on postgres/mssql, so the **same script works on all drivers**.
- String values must be separate arguments — never inline user text into the
  SQL yourself; the driver escapes/binds it:
  ```lua
  db.exec("UPDATE t SET x = '" .. user_input .. "'") -- WRONG: injection
  db.exec("UPDATE t SET x = ?", user_input)           -- right
  ```
- Numbers that are whole become integers, non-whole become floats; strings,
  booleans, `nil` are all fine. A Lua table is converted to a row-map if you
  ever hand one over.

### SQL errors

`db.*` calls **raise a Lua error** on a failed query — the script stops and
the request will fail with HTTP 500 (a `db` failure is a server error, not a
user story). If you want to fall back instead of failing, wrap it in `pcall`:

```lua
local ok, res = pcall(function() return db.query_one("SELECT …") end)
```

### `abort(msg)`

Stops the script immediately and produces a **user-facing** outcome:
- in a **hook**: the request responds `400` with the message;
- in an **action**: it redirects back to the list with `?flash=<msg>` (the
  message is shown to the operator).

Prefer `abort` for validation failures — it is the only way to give useful
feedback instead of a 500.

### `log(msg)`

Writes a line to the server log (prefixed with `[lua]`). Useful for
troubleshooting. Never use it for user-visible text.

---

## 4. When scripts run (and transactions)

| Where the `script:` sits | Operates on | Notes |
|---|---|---|
| Action `script:` | a `*sql.DB` | runs once per row |
| Action with `bulk: true` | `db` | runs **once per selected id**, no wrapping transaction |
| Hook before/after create/update/delete | `db` | before-create: `ctx.id` is `0`; the id is available in the after hook |
| **Audited** action `script:` | the **audit transaction** | the script's `db.*` calls go through the transaction so the op + the audit row commit together |

The 5-second time limit is per script run; a tight Lua loop (`while true do
end`) is interrupted at the deadline, and the request fails with a 500.

---

## 5. Three worked examples

### Example 1 — before-create hook: defaults + normalization

A form without a `status` field on a drafts-first workflow:

```yaml
form:
  create:
    hooks:
      before:
        - name: apply_defaults
          script: |
            # unset field -> nil -> fill the default
            if ctx.values["status"] == nil then
              ctx.values["status"] = "draft"
            end
            # normalize an optional email
            if ctx.values["email"] ~= nil then
              ctx.values["email"] = string.lower(ctx.values["email"])
            end
            # deny a reserved name early
            if ctx.values["slug"] == "admin" then
              abort("That name is reserved")
            end
```

The `status`/`email` changes are written back into the INSERT by yaga, so the
created row is stored with those values — no separate SQL `UPDATE` needed.

### Example 2 — action: guard + read + update

A writer-facing action that checks role, chains a query and updates, with
messages the operator sees:

```yaml
actions:
  - name: archive
    label: "Archive"
    color: warning
    requires_confirmation: true
    script: |
      if ctx.role ~= "admin" then
        abort("Only admins can archive orders")
      end

      local order = db.query_one("SELECT status, ref FROM orders WHERE id = ?", ctx.id)
      if order == nil then
        abort("Order does not exist")
      end
      if order["status"] == "archived" then
        abort("Order " .. order["ref"] .. " is already archived")
      end

      local res = db.exec(
        "UPDATE orders SET status = 'archived', archived_by = ? WHERE id = ?",
        ctx.user, ctx.id
      )
      log(string.format("archived order %s rows=%d", order["ref"], res["rows_affected"]))
```

### Example 3 — after-delete hook: record an audit-style event

```yaml
form:
  delete:
    hooks:
      after:
        - name: record_deletion
          script: |
            local prev = db.query_one(
              "SELECT name, note FROM orders WHERE id = ?", ctx.id
            )
            local what = prev and prev["name"] or "<unknown>"

            local res = db.exec(
              "INSERT INTO events (table_name, row_id, actor, detail) VALUES (?, ?, ?, ?)",
              ctx.table, ctx.id, ctx.user,
              string.format("%s deleted this %s", what, ctx.table)
            )
            if res["rows_affected"] ~= 1 then
              log("event insert did not apply for " .. ctx.table .. " " .. tostring(ctx.id))
            end
```

Whatever SQL the script runs is what takes effect — the hook itself cannot
"return" a value the handler would use; if it needs data it must query it.

---

## 6. Common mistakes / checklist

- Closing braces: every `if`/`while`/`for`/`function` closes with `end`.
- Array indices start at **1** (`rows[1]`, not `rows[0]`).
- `ctx.values` fields are `nil` **not `""`** when unset — test with
  `== nil`, and assign defaults rather than `""`.
- Don't concatenate values into SQL — bind them as `?`/`$N` args.
- Prefer `abort(msg)` for expected, user-visible stops; an unhandled Lua
  error or `db.*` failure becomes an HTTP 500.
- Scripts run at request time; keep them short and mostly single-statement
  — the 5 s budget kills runaway loops.
- Changes to `ctx.values` only matter in **before-create/update** hooks.
- Test against the actual driver: `?` binds fine everywhere; on postgres /
  mssql the runtime rewrites it to `$N` for you.