# YAGA — Uživatelská příručka

**YAGA** (YAML Advanced Generator for Admin panels) je generátor administračních panelů
pro Go, řízený YAML souborem. Nasměrujete ho na existující databázi, popíšete požadovaný
dashboard v `yaga.yaml` a nástroj vygeneruje kompletní, samostatný administrátorský panel:
CRUD zdroje, zobrazení karet/kanban, vlastní stránky s widgety, vyhledávání/řazení/filtrování,
import/export CSV, autentizaci, RBAC, auditní protokolování, vlastní akce, háčky before/after
(Go, SQL nebo Lua) a další.

Důležitý mentální model: **databáze je základ**! YAGA introspektuje vaši databázi,
zachytí její schéma a přidává chování **navrch** — nenahrazuje dobrý návrh databáze.

---

## 1. Instalace

### 1.1 Předpoklady — funkční Go toolchain

| Nástroj |Potřebný pro ||
|---|---|---|
| [Go](https://go.dev/dl/) 1.26+ | pro komplilace vygenerovaného dashboardu a instalaci YAGA ze zdrojových kódů |

Tailwind stylesheet a Chart.js jsou uloženy ve vygenerovaném projektu.

### 1.2 Instalace ze zdrojových kódů

Nejjednodušší instalace:

```sh
go install github.com/MichalHerstus/yaga/cmd/yaga@latest
```

Binární soubor se umístí do `$(go env GOPATH)/bin/yaga` (obvykle `~/go/bin/yaga`); ujistěte se,
že je tento adresář na vašem `PATH` (nebo nastavte `GOBIN` před instalací). Pro sestavení
z místní kopie:

```sh
git clone https://github.com/MichalHerstus/yaga.git
cd yaga
go build -o yaga ./cmd/yaga
```

Ověření instalace:

```sh
yaga version          # např. yaga version 2.1.5
# vypíše text s použitím
```

### 1.3 Předem sestavené binární soubory (GitHub Releases)

Hotové binární soubory pro běžné kombinace OS/arch jsou publikovány na stránce
[GitHub Releases](https://github.com/MichalHerstus/yaga/releases) projektu. Stáhněte
build odpovídající vaší platformě (např. `yaga-MacOS`).


> **Funkční instalace Go je stále povinná — i když používáte předem sestavený binární
> soubor yaga.** yaga pouze *generuje* admin panel; neobsahuje překladač. Sestavení
> vygenerovaného dashboardu vždy spouští nástroje Go proti vygenerovanému projektu:
> `go mod tidy`, `go tool templ generate` a `go build ./...`. Pokud nemůžete nainstalovat Go
> na cílový stroj, použijte `yaga generate` na stroji s Go a přeneste **binární soubor**
> (ne zdrojové kódy) na cíl — `make package` sestaví přesně takový
> instalační archiv.

### 1.4 Konfigurace DSN (jak dashboard najde svou databázi)

Vygenerovaný dashboard řeší DSN své databáze při spuštění v tomto pořadí:

1. **Proměnná prostředí `DATABASE_URL`** — má přednost před vším. Ideální pro CI/CD a
   přepisy na úrovni shellu.
2. **Soubor `.ENV`** vedle binárního souboru dashboardu — generovaný do složky projektu
   příkazem `yaga generate` (mód 0600, čitelný pouze vlastníkem), obsahující
   `DATABASE_URL=<dsn>`. Upravte ho pro přesměrování na jinou databázi (např. přepnutí
   z testovací na produkční databázi) bez překladu.
3. **Nedůvěryhodný localhost fallback** — použije se pouze tehdy, když konfigurace
   *nemá* žádné připojení (konfigurace s připojením odmítne spuštění, pokud DSN není nalezena).

```ini
# vygenerovaný admin/.ENV  (0600)
# Proměnná prostředí DATABASE_URL přepíše tuto hodnotu za běhu.
DATABASE_URL=postgres://user:pass@localhost:5432/mydb?sslmode=disable
```

Příklady pro ostatní ovladače:

```sh
DATABASE_URL="file:./data/admin.db" ./admin                    # SQLite (relativní cesta!)
DATABASE_URL="sqlserver://user:pass@localhost:1433?database=mydb" ./admin   # MSSQL
```

Poznámky:

- DSN je **nikdy** nekompilována do binárního souboru dashboardu — tajemství zůstávají mimo
  artefakt. Zdrojový `yaga.yaml` stále obsahuje plaintextový `dsn:` v `connections:`
  (řídí generování), proto považujte `yaga.yaml` za citlivý soubor.
- **`yaga generate` přepisuje `.ENV`** z konfigurace při každém spuštění. Pro prostředí
  nasazení upravte `.ENV` (nebo nastavte `DATABASE_URL`) *po* generování / balení —
  `make package` zahrnuje `.ENV` do release archivu.
- Vygenerovaný server spouští sanity dotaz na DB **před navázáním portu**, takže
  chybějící/inicializovaná databázie je fatální chyba při spuštění místo obsazeného portu.

---

## 2. Přehled příkazů a přepínačů

```
yaga init --db DSN  Introspektuje existující databázi a vygeneruje yaga.yaml
yaga edit           Interaktivní editor YAML konfigurace (TUI)
yaga wedit          Webový editor YAML konfigurace (prohlížeč, lokální HTTP server)
yaga generate       Vygeneruje Go aplikaci admin panelu (offline, bez sqlc)
yaga validate       Ověří YAML konfiguraci
yaga version        Vypíše informace o verzi
```

### Globální přepínače (použitelné s většinou příkazů)

| Přepínač | Zkratka | Výchozí | Význam |
|---|---|---|---|
| `--config` | `-c` | `yaga.yaml` | Cesta k YAML konfiguračnímu souboru |
| `--out` | `-o` | `./admin` | Výstupní adresář pro generovaný kód |
| `--db` | `-d` | — | Připojovací řetězec DB pro `init` (`postgres://…`, `sqlserver://…`, `mssql://…` nebo cesta k sqlite souboru) |
| `--admin-password` | `-p` | náhodné | Počáteční heslo administrátora pro `init --db` scaffolding |
| `--force` | `-f` | false | Přepsat existující soubory |
| `--verbose` | `-v` | false | Podrobný logování |
| `--skip-plugins` | `-s` | false | Přeskočit načítání deklarovaných pluginů (pro `generate`) |
| `--update` | — | false | Sloučit nové tabulky do existující konfigurace místo přepisu (`init`) |

### `yaga init`

```sh
yaga init --db "postgres://user:pass@localhost:5432/mydb" [--config yaga.yaml] [--force] [--admin-password HESLO]
yaga init --db "postgres://user:pass@localhost:5432/mydb" --update               # Sloučit nové tabulky do existující konfigurace
```

**Jediný** způsob, jak vytvořit kostru projektu. Připojí se k databázi, introspektuje schéma,
vytvoří auth tabulky `users`/`roles` s výchozími rolemi **a** uživatele admina, pokud
chybí, a zapíše `yaga.yaml` obsahující jeden zdroj na tabulku plus zachycený blok `schema:`.

**Režim aktualizace** (`--update`): Sloučí nově objevené tabulky do existujícího `yaga.yaml`
místo přepisu. Všechna uživatelská přizpůsobení (vlastní popisky sloupců, akce,
vypočítaná pole, navigace, stránky atd.) jsou zachována. Blok `schema:` je plně
nahrazen (zůstává jediným zdrojem pravdy). Zdroje, jejichž tabulky již v databázi
neexistují, jsou označeny komentářem `# ORPHANED` ale ponechány pro ruční kontrolu.
Navigace a stránky nejsou nikdy automaticky upravovány.

### `yaga edit` / `yaga wedit`

| Přepínač | Význam |
|---|---|
| `--prompt TEXT` | Upravit konfiguraci přes AI místo TUI (`file://PATH` přečte prompt ze souboru, `~` se rozbalí) |
| `--apikey KEY` | OpenRouter API klíč (fallback na `OPENROUTER_API_KEY` env, pak `.ENV`) |
| `--model MODEL` | ID modelu (fallback na `.ENV`, jinak `openrouter/auto`); `"lmstudio"` použije lokální LM Studio server bez klíče |
| `--dry-run` | (s `--prompt`) vypíše navržený YAML + diff bez zápisu |

`wedit` navíc přijímá:

| Přepínač | Význam |
|---|---|
| `--port N` | Port webového editoru (výchozí `9090`) |
| `--open` | Otevře editor ve výchozím prohlížeči po navázání |

### `yaga validate`

| Přepínač | Význam |
|---|---|
| `--fix` | Automaticky opraví známé opravitelné problémy (např. inertní blok list/card filter) a přepíše konfiguraci (záloha v `<config>.bak`) |
| `--dry-run` | Zobrazí, co by `--fix` aplikoval, bez zápisu čehokoli |

---

## 3. Pracovní postup

```
[1. návrh databáze] → [2. init --db] → [3. úprava yaga.yaml] → [4. generate]
        → [5. build] → [6. spuštění a testování] → [opakování od 3. k opravě/vylepšení]
```

**Vývoj schématu**: Po přidání tabulek do databáze spusťte `yaga init --db DSN --update`
pro sloučení nových tabulek do existujícího `yaga.yaml` bez ztráty přizpůsobení.
Poté pokračujte cyklem od kroku 3.

### 3.1 Návrh databáze — základ

yaga je **řízený schématem**: databáze je základ a yaga vrství administrační chování
navrch. Dobře navržená databáze produkuje dobře fungující admin panel téměř zdarma.

Věci, které při návrhu záleží:

- **Cizí klíče jsou zapojení.** yaga introspektuje každý FK a používá ho pro:
  - vykreslení polí `relation` / modálních **record pickerů** (volby odvozené z
    label sloupce FK),
  - zobrazení labelu souvisejícího záznamu místo surového id v seznamech a detailech
    (přes `LEFT JOIN`),
  - automatické generování **master–detail children** (`children:` bloky).
  - Deklarujte `options_value`/`options_label` a picker funguje bez vlastního SQL.
- **Databázové pohledy** lze procházet jako tabulky. Introspektace je označí
  `view: true` v zachyceném bloku `schema:` a vygenerované zdroje pro pohledy jsou
  pouze pro čtení (bez create/update/delete).
- **Uložené procedury** lze volat z dashboardu. `action` (nebo hook) může
  zavolat proceduru pomocí `proc: <name>` — `CALL` na Postgres, `EXEC` na MSSQL a pro
  SQLite (který nemá skutečné procedury) blok `procedures:` v konfiguraci poskytuje
  pojmenované SQL dávky spouštěné uvnitř jedné transakce.
- Mějte **primární klíč** na každé tabulce (jednosloupcový je nejjednodušší), zvolte stabilní
  přirozený sloupec `label` pro "název řádku" (yaga upřednostňuje `name`, pak `title`,
  pak `label`, pak první textový sloupec mimo PK) a upřednostňujte typy `varchar`/`text`,
  které se čistě mapují (viz mapování typů v sekci Schéma).

### 3.2 `init --db` — kostra z databáze

```sh
yaga init --db "postgres://user:pass@localhost:5432/mydb?sslmode=disable"
yaga init --db "./mydata.db"                                            # SQLite
yaga init --db "sqlserver://user:pass@localhost:1433?database=mydb"     # MSSQL
```

Co se stane:

1. Připojí se k databázi a introspektuje tabulky, sloupce, primární a cizí klíče.
2. Vytvoří auth tabulky `users`/`roles` s výchozími rolemi **a** uživatelem admina, pokud
   chybí — přihlašovací jméno `admin@admin.test` / vygenerované heslo vypsané do
   konzole (nebo `--admin-password`).
3. Zapíše `yaga.yaml`: jeden zdroj na tabulku (oddíly list/detail/form, pole FK s
   pickery), blok `auth:`, jedno připojení a — co je nejdůležitější — zachycený
   **blok `schema:`**, **jediný zdroj schématu** pro generování.

Dokumenty se zapíší na disk; poté je přizpůsobíte před generováním.

### 3.3 Úprava YAML specifikace

Vyberte jeden z editorů (podrobně popsán v sekci 5):

```bash
yaga edit                 # terminálové UI
yaga wedit                # webový editor + živý náhled + MCP
yaga edit --prompt "…"    # AI podporovaná úprava (experimentální)
```

Typické věci, které upravíte po `init --db`:

- `panel` branding/jazyk, sidebar, téma;
- které sloupce jsou `sortable` / `searchable`, popisky sloupců, výchozí řazení;
- která pole se zobrazí na formulářích create/update, viditelnost polí, povinné náznaky;
- přidání pohledů, filtrů, vlastních akcí, hooků, children, politik, auditu.

### 3.4 Generování

```bash
yaga generate                     # zapíše ./admin (plně offline — bez DB, bez sqlc)
```

Generátor odvozuje každý dotaz z zachyceného bloku `schema:`, vypíše zdrojový kód
dashboardu a přidá předem sestavený stylesheet + Chart.js. `--force` obnoví existující
výstup. Můžete to spouštět znovu a znovu — vše se generuje od začátku.

> AI cesta i tok `--prompt` také prochází přes `yaga generate` po
> úpravě.

### 3.5 Sestavení

```bash
cd admin
make build          # go mod tidy → go tool templ generate → go build
```

nebo ručně:

```bash
go mod tidy
go tool templ generate
go build -o admin .
```

Na **Windows** (bez `make`, bez gcc, bez CGO — sqlite ovladač je čistý Go) místo toho
použijte generovaný `build.ps1`, který zrcadlí stejné cíle:

```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1 build   # build | templ | tidy | run | package | clean
```

Cross-kompilace dashboardu pro Linux/Windows z jakéhokoli stroje:
`CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o admin.exe .`.

### 3.6 Spuštění a testování

```bash
make run                                # sestaví + spustí, výchozí port 8080
./admin --port 8080 --log full          # krátké formy: -p 8080 -l err
./admin -h                              # vypíše všechny přepínače běhu
```

Poté otevřete `http://localhost:8080`, přihlaste se jako administrátor a vyzkoušejte seznam/vyhledávání/
řazení/filtrování, vytváření/upravování/mazání, akce, stránky a zobrazení karet.

### 3.7 Opakování z YAML

Změna konfigurace → `yaga generate` → `make build` → test. Smyčka mezi kroky 3–6 je
místo, kde se tvaruje produkt: popisky, které sloupce se zobrazí, náznaky validace, akce,
vzhled, hooky, audit — vše řízené z YAML, žádný ručně psaný UI kód, žádný únik do běhu.

---

## 4. YAML bloky — co znamená každá sekce

Plné schéma je dokumentováno v `README.md`, autoritativním `SPEC.md`. Zde je
mapa bloků nejvyšší úrovně.

### Klíče nejvyšší úrovně

| Klíč | Povinný | Význam |
|---|---|---|
| `version` | ano | Řetězec verze schématu, např. `"1"`. Jakákoli neprázdná hodnota je přijata. |
| `panel` | ano | Identita panelu, branding, layout a téma. |
| `connections` | — | Připojení k DB (driver + dsn). **První** záznam používá vygenerovaná aplikace. |
| `schema` | — | Zachycené schéma databáze — **jediný zdroj schématu** pro generování (zapsáno pomocí `init --db`, pak ručně upravitelné). |
| `auth` | — | Přihlašovací tabulka, pole identity/hesla, přesměrování po přihlášení, volitelný limit pokusů. |
| `navigation` | — | Skupiny a položky bočního panelu. |
| `resources` | — | CRUD entity. Alespoň jeden resource nebo stránka je povinná. |
| `pages` | — | Vlastní stránky dashboardu s widgety. Alespoň jeden resource nebo stránka je povinná. |
| `audit` | — | Auditní protokol každého create/update/delete/action (přidá resource `AuditLog`). |
| `procedures` | — | SQLite SQL-dávkové "uložené procedury" (ignorováno na postgres/mssql). |
| `plugins` | — | Pluginy při generování, které přispívají zdroji/stránkami/hooky. |

### `panel` — identita a vzhled

```yaml
panel:
  id: admin            # malá písmena; součást generovaných názvů handlerů (AdminDashboard)
  path: /admin         # URL prefix, MUSÍ začínat "/" (základ všech rout)
  name: "My Admin"     # zobrazeno v bočním panelu + přihlašovací stránce
  brand:
    logo: /assets/logo.svg
    colors: { primary: "#6366f1", secondary: "#64748b" }
  layout:
    sidebar: { collapsible: true, width: 280 }
    topbar:  { sticky: true }
    max_content_width: 7xl          # validuje proti seznamu povolených
  theme:
    dark_mode: true
    font: { family: "Inter, sans-serif", mono: "JetBrains Mono, monospace" }
```

### `connections` — databáze dashboardu

```yaml
connections:
  default:
    driver: postgres          # postgres (výchozí) | sqlite | sqlite3 | mssql | sqlserver
    dsn: "postgres://user:pass@localhost:5432/db?sslmode=disable"
    pool: { max_open: 25, max_idle: 10, lifetime: 5m }
```

Driver určuje `sql.Open`, operátor LIKE (`ILIKE` vs `LIKE`), zástupné znaky
(`$N` vs pozicionální `?`), quoting identifikátorů (`"name"` vs `[name]`) a Go typy id
(`int32` postgres/mssql, `int64` sqlite). Hodnota `dsn` se zapíše do `.ENV` projektu
(viz 1.4).

### `schema` — zachycená databáze

Zapsáno pomocí `init --db`; generátor mu plně důvěřuje (offline). Můžete ho upravovat
ručně (přidávat sloupce, upravovat typy) — `validate` a editory varují/chybují, když
resource odkazuje na tabulku/sloupec, který zde chybí.

### `auth` — přihlášení

```yaml
auth:
  table: users                     # tabulka pro vyhledávání při přihlášení
  login:
    fields: [email, password]      # identity + heslo (bcrypt v DB)
    redirect: /custom/dashboard    # kam po přihlášení (zaregistrovaná ruta)
    rate_limit: { max_attempts: 5, window_seconds: 300 }
```

### `navigation` — boční panel

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

Položky odkazují na seznam `resource`, rutu `page` nebo externí `link`.

### `resources` — CRUD entity

```yaml
resources:
  - name: User            # POVINNÉ PascalCase (zmenšené → Go pkg/dir/URL: "user")
    label: Users          # UI label; výchozí = name
    table: users          # volitelný přepis DB tabulky (generován introspektací)
    id_column: id         # volitelný přepis row-key (např. "ID" na mssql)
    id_type: int32        # volitelný přepis typu id (např. int64 pro bigint pk)
    import_csv: true      # přidá tlačítko "Import CSV" + POST /import/csv
```

Každý resource má až tři pohledy + extras:

#### `list` — tabulkový pohled

```yaml
    list:
      per_page: 20
      columns:
        - { name: id,         type: integer, sortable: true }
        - { name: name,       type: string,  searchable: true }
        - { name: email,      type: email,   sortable: true }
        - { name: status,     type: badge,   options: { active: success, inactive: warning } }
        - { name: role_label, label: Role,   type: text }   # FK label sloupec (introspektovaný)
      default_sort: -created_at      # prefix "-" = sestupně
      export: [id, name, email]     # volitelný podmnožina sloupců CSV
      filter:                       # sbalitelná sekce filtru
        label: "Status"
        where: "status = $1"
        params: [ { name: status, label: Status } ]
```

Vyhledávání, řazení, filtrování a stránkování se generují. `sortable`/`searchable` určují,
které sloupce reagují na vyhledávací pole / záhlaví řazení.

#### `card` — mřížkový / kanban pohled (volitelný)

```yaml
    card:
      fields:   [ { name: title }, { name: status, type: select, options: {todo: "To Do", doing: "In Progress"} } ]
      columns: 3              # karty na řádek (1..12)
      rows: 4                 # řádků na stránku
      kanban_field: status    # volitelné → kanban board seskupený podle hodnoty volby
      default_sort: -created_at
```

Pouze pro čtení, dostupný na `/cards`, dosažitelný přes tlačítko "Cards" v seznamu.

#### `detail` — pohled na záznam (volitelný)

```yaml
    detail:
      fields:
        - { name: id,   type: integer }
        - { name: name, type: string }
        - { name: email, type: email }
```

Vykreslí se jako stránka záznamu pouze pro čtení; klíčována podle row key resource.

#### `computed:` — virtuální sloupce (list / card / detail)

Kterýkoli ze tří pohledů může přidat sloupce pouze pro čtení, odvozené SQL
výrazem **v době dotazu**, místo výběru existujících sloupců tabulky:

```yaml
    list:
      computed:
        - { name: total_gross, label: "Total gross", type: float,    expression: "helpers.round(total * 1.21, 2)" }
        - { name: age_days,    label: "Age (days)",  type: integer,  expression: "helpers.date_diff(helpers.now(), created_at)" }
      filter:
        where: "total_gross > $1"        # vypočítané sloupce fungují v filter.where
```

- `name` je klíč sloupce (jedinečný v rámci bloku, nesmí kolidovat s reálným
  sloupcem), `type` jeden ze sdílených typů polí, `expression` SQL fragment.
- Výraz může odkazovat na **skutečné sloupce tabulky** (včetně aliasů joinů
  `{fk}_label`) a **dřívější vypočítané názvy ve stejném bloku**. Předává se
  doslovně nakonfigurovanému ovladači — použijte SQL syntaxi tohoto ovladače, ne yagy.
- Tokeny `helpers.*` se expandují při generování do driver-korektního SQL:
  `helpers.date_diff(a,b)` / `helpers.year_diff` / `helpers.month_diff`,
  `helpers.coalesce`, `helpers.ifnull` (IFNULL/ISNULL/COALESCE podle driveru),
  `helpers.round(x,n)` (numeric cast na postgres), `helpers.now()`. Volání mohou
  být vnořená (`helpers.date_diff(helpers.now(), created_at)`); neznámé helpers nebo
  špatné arity se vypíšou doslovně.
- Vypočítané sloupce se vykreslují a skenují jako sloupce pohledů, ale nikdy nejsou řaditelné
  ani vyhledávatelné a nikdy se neobjeví ve formulářích. Filtr odkazující na vypočítaný název
  je podporován (dotaz se generuje z wrapperu derived table).
- Vypočítaná pole jsou **výstupy pouze pro čtení** — nedochází k žádnému uložení, žádné
  zápisové cestě a neovlivňují introspekci `init`.

#### `form` — create / update / delete

```yaml
    form:
      create:
        fields:
          - { name: name,      type: text,     required: true }
          - { name: email,     type: email,    required: true }
          - { name: password,  type: password }            # bcrypt hash před insertem
          - { name: role_id,   type: relation, options_value: id, options_label: name }
          - { name: status,    type: select,   options: { active: Active, inactive: Inactive } }
        hooks: { before: [ { name: validate_domain, fn: ValidateUserDomain } ] }
      update:
        fields: [ { name: name }, { name: email }, { name: status } ]
      delete: {}                 # přítomnost aktivuje delete rutu
    children:                   # volitelné master-detail sekce
      - name: Lines
        resource: OrderLine
        column: order_id
        columns: [ { name: qty, label: "Qty", type: integer } ]
```

- Pole `select`/`relation` s řešitelnými volbami se vykreslí jako **modální record picker**;
  `copies:` automaticky vyplní sousední pole formuláře z vybraného řádku.
- Sdílená šablona formuláře vykreslí **sjednocení** polí create + update
  (`visible: [create]`/`[update]` jemně doladí podle kontextu).
- `delete: {}` aktivuje tlačítko smazání + POST rutu.

#### `policies` — RBAC (volitelné)

```yaml
    policies:
      view_any: "admin|manager"
      view:      "admin|manager"
      create:    "admin"
      update:    "admin|manager"
      delete:    "admin"
```

Vygenerovaná aplikace kontroluje roli přihlášeného uživatele proti seznamu oddělenému
pipingem pro každou rutu.

#### `audit`

```yaml
audit:
  enabled: true
  table: audit_log
  include_values: true      # uložit změněné hodnoty jako JSON
  policy: "admin"           # kdo může zobrazit vygenerovaný seznam AuditLog
  exclude_resources: [Users]
```

Přidá resource `AuditLog` pouze pro čtení + navigační skupinu "Audit Log" a obalí každou
mutaci + audit insert do jedné transakce.

### `pages` — vlastní dashboards

```yaml
pages:
  - name: Dashboard
    default: true                 # úvodní stránka po přihlášení (namontována na / a /dashboard)
    widgets:
      - { type: stat,       label: "Total Users",   query: "SELECT COUNT(*) FROM users", icon: users }
      - { type: chart,      label: "Revenue",       query: "SELECT month, total FROM revenue ORDER BY month",
          chart: { type: line } }
      - { type: table,      label: "Recent Orders", query: "SELECT id, customer_id, total FROM orders ORDER BY created_at DESC LIMIT 5",
          data_columns: [id, customer_id, total] }
      - { type: list,       label: "Top Products",  query: "SELECT name, price FROM products ORDER BY price DESC LIMIT 5" }
      - { type: html,       label: "Note",          query: "SELECT note FROM notes LIMIT 1" }   # pouze důvěryhodný vstup
```

Widgety: `stat`, `stats_grid`, `chart` (line/bar/pie/area), `table`, `list`, `html`.
`query` je surový SQL spouštěný v době požadavku; chyby widgetů se logují a nikdy
nevyprázdní stránku.

### Typy polí

`type` je **nápověda pro UI rendering** — skutečné typy sloupců DB pocházejí z
bloku `schema:`. Vztahuje se na `columns` v list, `fields` v detail, `fields` v card a
`fields` v form: `string`, `text`, `integer`, `float`, `email`, `password`, `boolean`, `select`,
`datetime`, `date`, `badge`, `image`, `file`, `relation`, `json`, `gps`.

---

## 5. Editory

`yaga.yaml` je prostý YAML soubor; upravte ho libovolným textovým editorem — ale yaga
dodává čtyři integrované způsoby:

### 5.1 TUI editor — `yaga edit`

Klávesnicí řízené terminálové UI (3 panely: seznam navigace | obsah | stavový řádek)
pokrývající každou sekci konfigurace s živou validací.

- `Ctrl+S` uložit (nejprve validuje), `Ctrl+V` validovat, `Ctrl+Q`/`F10` ukončit, `Esc` zpět.
- `Ctrl+P` otevře **cd-style navigátor cest** (např. `/Resources/User/List/Columns`,
  `../Columns`), `Tab` doplní.
- `Ctrl+O` domů. Každé tlačítko má také zkratku `Ctrl+<písmeno>` zobrazenou v
  jeho popisku.
- V editorech seznamů `a`/`d` přidává/maže řádky, `Enter` upravuje; `stringMapPage` upravuje
  mapy (`options:`, parametry dotazů, `copies:`).

Nesouvisející soubory nechává nedotčené; upravuje pouze konfiguraci, kterou editujete.

### 5.2 Webový editor — `yaga wedit`

```sh
yaga wedit                       # http://localhost:9090
yaga wedit --port 9091 --open    # vlastní port / otevřít prohlížeč
```

Lokální HTTP server s vestavěnou single-page aplikací:

- Panelové editory pro panel, connections, auth, navigaci, resources, stránky;
- obrazovka **Validate** spouštějící plný validátor (+ auto-fix) živě;
- záložka **Preview** renderující mock dashboard a pohledy seznamů per-resource (mocky
  stránek/zdrojů, světlý/tmavý režim);
- záložka surového YAML;
- úpravy se drží **v paměti** — explicitní **Save** zapíše na disk (nástroj MCP `save`
  nejprve vytvoří zálohu `<config>.bak`);
- více záložek prohlížeče se **synchronizují živě** (SSE + čítač revizí); zastaralá záložka
  je varována, než může potichu přepsat novější změny.

### 5.3 AI podporovaná úprava — `yaga edit --prompt "…"`

```sh
yaga edit --prompt "Change the dashboard title to: Order management"
yaga edit --prompt file://./instructions.txt
```

Pošle celou konfiguraci modelu a zpět sloučí **pouze změněné sekce** (validované;
neplatné sloučení se znovu zkusí jednou, pak soubor zůstane nedotčený). Užitečné pro rychlé
jednoúčelové úpravy. Pro vážnější AI řízenou práci upřednostňujte cestu **MCP** (viz níže),
která vidí stejnou konfiguraci v paměti jako webový editor.

### 5.4 MCP (AI agenti přes `wedit`)

`yaga wedit` poskytuje endpoint **Model Context Protocol (Streamable HTTP)** na
`POST /mcp` (také `GET /mcp`), takže AI agenti mohou číst a upravovat živou konfiguraci
přes strukturované nástroje: `get_config`, `get_value`, `set_value`, `merge_yaml_fragment`,
`add_resource`, `remove_resource`, `add_column`, `add_field`, `add_nav_item`,
`remove_nav_item`, `validate`, `save`, … Úpravy se validují (neplatná úprava je zamítnuta)
a automaticky se šíří do každé připojené záložky prohlížeče.

Pro použití z opencode (nebo jiného MCP klienta):

```json
{ "mcp": { "yaga": { "type": "remote", "url": "http://localhost:9090/mcp" } } }
```
Plný příklad konfigurace Opencode MCP:
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

## 6. Akce a háčky (Actions & Hooks)

Dva mechanismy pro spuštění vlastní logiky z dashboardu v době požadavku.

### 6.1 Akce — tlačítka, která něco dělají

**Akce** je vlastní tlačítko na resource (pro každý záznam nebo hromadně), které po kliknutí
spustí kus logiky. Použijte je pro operace, které CRUD builder nedokáže vyjádřit: "Označit jako
odeslané", "Archivovat", "Přepočítat", "Zavolat uloženou proceduru".

- `query:` inline surový SQL spouštěný s id záznamu navázaným jako `$1`;
- `proc:` název uložené procedury (Postgres `CALL`, MSSQL `EXEC`, SQLite
  `procedures:` dávka);
- `script:` vestavěné Lua tělo (spouštění v době požadavku s 5s timeoutem);
- každá akce dostane POST rutu `/<panel>/<resource>/{id}/action/<name>` (neznámé názvy
  → 404);
- `bulk: true` vykreslí zaškrtávací pole řádků + panel nástrojů; hromadný cyklus běží uvnitř
  **jedné transakce**.

**Příklad — SQL akce:**

```yaml
    actions:
      - name: mark_done
        label: "Mark done"
        icon: check
        color: success
        requires_confirmation: true
        query: "UPDATE orders SET status = 'done' WHERE id = $1"
```

**Příklad — akce procedury:**

```yaml
      - name: archive
        label: "Archive"
        proc: sp_archive_customer      # CALL sp_archive_customer($1) na postgres,
                                       # EXEC sp_archive_customer $1 na mssql
```

Na SQLite (bez skutečných uložených procedur), stejný `proc:` odkazuje na pojmenovanou SQL dávku
deklarovanou v bloku nejvyšší úrovně `procedures:`, spouštěnou uvnitř jedné transakce:

```yaml
procedures:
  - name: sp_archive_customer
    description: "Archive a customer and record the event"
    sql: |
      UPDATE customers SET status = 'inactive' WHERE id = $1;
      INSERT INTO customer_log (customer_id, msg) VALUES ($1, 'archived');
```

**Příklad — Lua akce:**

```yaml
      - name: flag_audit
        label: "Flag"
        script: |
          local row = db.query_one("SELECT * FROM orders WHERE id = ?", ctx.id)
          if row ~= nil then
            db.exec("UPDATE orders SET status = 'flagged' WHERE id = ?", ctx.id)
          end
```

### 6.2 Háčky — spuštění before / after create, update, delete nebo akce

**Hook** je kus kódu připojený k životnímu cyklu mutační operace:

- `form.create` → `before` (s `scope.id` = 0) a `after` (s novým id řádku);
- `form.update` / `form.delete` → `before` / `after`;
- `action` → `before` / `after`;

Každý hook je jednoho ze čtyř typů:

1. **`fn: <Name>`** — generátor vypíše kompilace-schopný stub `func <Name>(s *hooks.Scope)`
   do `internal/hooks/hooks.go`; vy doplníte Go tělo. Plná moc.
2. **`sql: "…"`** — inline SQL příkaz spouštěný přes `db.ExecContext(…, scope.ID)`.
3. **`proc: <name>`** — zavolá uloženou proceduru s id záznamu.
4. **`script: |`** — vestavěné Lua tělo (ve stejném kontextu jako script akce).

**Příklad — SQL hook (after create):**

```yaml
    form:
      create:
        hooks:
          after:
            - name: notify
              sql: "INSERT INTO notifications (target, msg) VALUES ($1, 'user created')"
```

**Příklad — Lua hook (nastavení výchozí hodnoty before create):**

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

`ctx` poskytuje `id`, `table`, `action`, `user`, `role` a `values` (pro before-
create/update, změny se zapisují zpět do řádku). Host helpers: `db.exec(sql,
vars...)`, `db.query(sql, vars...)`, `db.query_one(sql, vars...)` (pozicionální `?`
navázaný na sqlite, automaticky přečíslovaný na `$N` na postgres/mssql), `abort(msg)` (zastaví
s viditelným flashem / 400) a `log(msg)`. Při create se insert přepne na driver-aware
`RETURNING` / `OUTPUT INSERTED.<id>` takže after-create hooky obdrží skutečné id řádku. Chyba
hooku přeruší požadavek s HTTP 500.

---

## 7. Důležité technické poznámky

- **Sestavení vygenerované aplikace vyžaduje Go toolchain.** Žádný npm, žádný sqlc, žádný
  binární soubor Tailwind — ale `go` musí být na stroji, který spouští `make build`. Pro
  stroje bez Go nasadte binární soubor (`make package` zabalí binární soubor + static + `.ENV` + migrace).
- **DSN je záležitost běhu.** Žije v `.ENV` (0600) vedle binárního souboru, s
  přepisem `DATABASE_URL` (env) a nikdy není kompilována dovnitř. `yaga.yaml` stále obsahuje
  plaintextovou DSN.
- **`.ENV` je přegenerováno příkazem `yaga generate`**; pro databáze per-nasazení nastavte
  `DATABASE_URL` (env) nebo upravte `.ENV` po generování.
- **`yaga generate` je plně offline** — nikdy se nepřipojuje k databázi a nikdy nespouští sqlc
  ani binární soubor Tailwind. Schéma pochází z zachyceného bloku `schema:`.
- **`init --db` je jediná kostra.** Bez `--db` init chybuje; neexistuje prázdná
  šablona ani `--demo`.
- **Výchozí přihlášení administrátora** — `admin@admin.test` / jednorázové heslo vypsané
  příkazem `init --db` (nebo `--admin-password`); tabulka rolí má výchozí hodnoty `admin`/`manager`/`user`.
- **Server předem kontroluje DB** před navázáním portu (sanity `SELECT 1` proti
  auth tabulce), takže rozbitá/chybějící DB je fatální chyba při spuštění, ne běh
  za otevřeným portem.
- **Tajemství session** — nastavte `SESSION_SECRET` (≥ 32 znaků) pro perzistenci; s
  `APP_ENV=production` chybějící tajemství je fatální. Jinak se session resetují při restartu.
- **`query:`/`count_query:`/`populate_query:`/`params:` (a starší blok `sqlc:`)**
  jsou přijaty ale ignorovány v D11 — handlery používají surový SQL + blok `schema:` místo nich.
- **Pole `type:` je nápověda**; typy DB jsou autoritativní. Odpovídající sloupce db/schema
  udržují editory a Validate spokojené.
- **Generovaný kód neobsahuje komentáře** a vygenerovaná aplikace má **žádnou běhovou
  závislost** na modulu yaga — nasadte binární soubor a je hotovo.
- **Výchozí bezpečnostní nastavení** jsou připravena: CSRF, rotace session, validace uploadu
  (HTML/SVG zamítnuto), bezpečné odpovědi `500/404`, CSV formula-injection shell, whitelist
  řazení/objednávky, volitelný limit pokusů o přihlášení (podrobnosti v `README.md` → Security).
- `yaga generate` (a každý další příkaz) také zapíše průvodce agenta `AGENTS.md` do
  aktuálního adresáře, pokud chybí — říká AI agentům, jak pracovat s vygenerovaným
  projektem.
- Plné reference: `README.md` (rychlý start + refernce konfigurace), `SPEC.md`
  (autoritativní schéma), `AGENTS.md` (detaily agenta + správce), `SPEC_summary.md`
  (matrice funkcí).
