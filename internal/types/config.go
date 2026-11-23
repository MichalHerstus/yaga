// config.go
//
// YAML-tagged structs for the top-level yaga.yaml configuration: the
// version, panel settings (brand/layout/theme), database connections, sqlc
// settings, auth configuration and the sidebar navigation. These types are
// shared by the parser and the generator.
package types

// Config is the root of the yaga.yaml document.
type Config struct {
	Version     string                `yaml:"version"`
	Panel       Panel                 `yaml:"panel"`
	Connections map[string]Connection `yaml:"connections"`
	SQLC        SQLCConfig            `yaml:"sqlc"`
	Schema      *Schema               `yaml:"schema"`
	Auth        AuthConfig            `yaml:"auth"`
	Navigation  []NavigationGroup     `yaml:"navigation"`
	Resources   []Resource            `yaml:"resources"`
	Pages       []Page                `yaml:"pages"`
	Plugins     []PluginConfig        `yaml:"plugins"`
	Audit       *AuditConfig          `yaml:"audit"`
	Procedures  []Procedure           `yaml:"procedures"`
}

// AuditConfig enables a generator-implicit audit log of every mutating
// operation (create/update/delete/action). When enabled, the generator
// augments the config with a list-only AuditLog resource and an "Audit Log"
// navigation group, emits the audit_log table DDL into sql/migrations, and
// weaves an INSERT INTO audit_log into the create/update/delete/action
// handlers (op + audit insert run inside one transaction).
type AuditConfig struct {
	Enabled          bool     `yaml:"enabled"`
	Table            string   `yaml:"table"`
	IncludeValues    bool     `yaml:"include_values"`
	Policy           string   `yaml:"policy"`
	ExcludeResources []string `yaml:"exclude_resources"`
}

// Panel describes the admin panel shell: its id, URL path, display name,
// brand, layout and theme.
type Panel struct {
	ID     string `yaml:"id"`
	Path   string `yaml:"path"`
	Name   string `yaml:"name"`
	Brand  Brand  `yaml:"brand"`
	Layout Layout `yaml:"layout"`
	Theme  Theme  `yaml:"theme"`
}

// Brand holds the panel branding assets and colors.
type Brand struct {
	Logo    string      `yaml:"logo"`
	Favicon string      `yaml:"favicon"`
	Colors  BrandColors `yaml:"colors"`
}

// BrandColors defines the primary/secondary brand colors.
type BrandColors struct {
	Primary   string `yaml:"primary"`
	Secondary string `yaml:"secondary"`
}

// Layout describes the panel layout: sidebar and topbar behavior plus the
// maximum content width.
type Layout struct {
	Sidebar         SidebarLayout `yaml:"sidebar"`
	Topbar          TopbarLayout  `yaml:"topbar"`
	MaxContentWidth string        `yaml:"max_content_width"`
}

// SidebarLayout configures whether the sidebar collapses and its widths.
type SidebarLayout struct {
	Collapsible    bool `yaml:"collapsible"`
	Width          int  `yaml:"width"`
	CollapsedWidth int  `yaml:"collapsed_width"`
}

// TopbarLayout configures whether the topbar sticks to the top on scroll.
type TopbarLayout struct {
	Sticky bool `yaml:"sticky"`
}

// Theme holds panel theming options (dark mode, fonts).
type Theme struct {
	DarkMode bool `yaml:"dark_mode"`
	Font     Font `yaml:"font"`
}

// Font declares the font families used by the panel.
type Font struct {
	Family string `yaml:"family"`
	Mono   string `yaml:"mono"`
}

// Connection describes a database connection: driver, DSN and pool settings.
type Connection struct {
	Driver string     `yaml:"driver"`
	DSN    string     `yaml:"dsn"`
	Pool   PoolConfig `yaml:"pool"`
}

// PoolConfig configures the connection pool limits.
type PoolConfig struct {
	MaxOpen  int    `yaml:"max_open"`
	MaxIdle  int    `yaml:"max_idle"`
	Lifetime string `yaml:"lifetime"`
}

// SQLCConfig points at the sqlc config file and the schema/queries directories,
// plus the Go package the generated data layer is written into. Parse-only and
// inert since D11 (Phase 1): legacy `sqlc:` YAML still parses but the generator
// no longer emits/uses it; removed in Phase 2.
type SQLCConfig struct {
	Config     string `yaml:"config"`
	QueriesDir string `yaml:"queries_dir"`
	SchemaDir  string `yaml:"schema_dir"`
	OutputPkg  string `yaml:"output_pkg"`
}

// AuthConfig configures authentication: the guard/provider, the auth table,
// login behavior and optional registration/password-reset/remember-me flags.
type AuthConfig struct {
	Guard         string       `yaml:"guard"`
	Provider      string       `yaml:"provider"`
	Table         string       `yaml:"table"`
	Login         LoginConfig  `yaml:"login"`
	Roles         []RoleConfig `yaml:"roles"` // known roles; used for RBAC policies and demo-user seeding
	Registration  bool         `yaml:"registration"`
	PasswordReset bool         `yaml:"password_reset"`
	RememberMe    bool         `yaml:"remember_me"`
}

// LoginConfig declares the login field names (e.g. [email, password]), the
// redirect target after a successful login, optional per-role redirect targets
// (role name -> path) that override the fallback, and optional per-IP rate limiting.
type LoginConfig struct {
	Fields    []string          `yaml:"fields"`
	Redirect  string            `yaml:"redirect"`
	Redirects map[string]string `yaml:"redirects"`
	RateLimit *LoginRateLimit   `yaml:"rate_limit"`
}

// RoleConfig declares a role known to the dashboard. Roles are used both for
// RBAC policy checks and for seeding demo users in the SQLite system DB.
type RoleConfig struct {
	Name  string `yaml:"name"`  // machine name, e.g. "admin" (used in redirects, policies, emails)
	Label string `yaml:"label"` // optional human-readable name, e.g. "Administrator"
}

// LoginRateLimit throttles login attempts per client IP. When the block is
// present the generated auth package rejects attempts beyond MaxAttempts in a
// WindowSeconds sliding window with a 429-able login error page; successful
// logins reset the counter. Absent => no throttling (legacy behavior).
type LoginRateLimit struct {
	MaxAttempts   int `yaml:"max_attempts"`
	WindowSeconds int `yaml:"window_seconds"`
}

// NavigationGroup is a labelled group of sidebar links, sorted by its Sort
// value.
type NavigationGroup struct {
	Group string           `yaml:"group"`
	Icon  string           `yaml:"icon"`
	Sort  int              `yaml:"sort"`
	Items []NavigationItem `yaml:"items"`
}

// NavigationItem is a single sidebar link, pointing at a resource, a page, or
// an external URL.
type NavigationItem struct {
	Resource      string `yaml:"resource"`
	Page          string `yaml:"page"`
	Type          string `yaml:"type"`
	Label         string `yaml:"label"`
	URL           string `yaml:"url"`
	OpensInNewTab bool   `yaml:"opens_in_new_tab"`
}
