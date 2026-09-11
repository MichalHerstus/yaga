// auth.go
//
// Generates the auth package of the admin panel application
// (internal/panel/auth): the login/logout handlers with bcrypt verification,
// the gorilla/sessions store and session helpers, the session/RBAC middleware,
// and the login templ page. RBAC middleware code is only emitted when at least
// one resource declares policies.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// generateAuth writes the whole internal/panel/auth package:
// handler.go (LoginHandler, LogoutHandler, LoginPageData),
// session.go (session store + GetSession),
// middleware.go (SessionMiddleware, AuthMiddleware, context keys) and
// login.templ (the login form page). It derives the auth table, login field
// names and redirect URL from the config. Returns an error on write failure.
func (g *Generator) generateAuth() error {
	dir := filepath.Join(g.OutDir, "internal/panel/auth")
	panelName := g.Config.Panel.Name
	panelPath := g.Config.Panel.Path
	authTable := g.Config.Auth.Table
	if authTable == "" {
		authTable = "users"
	}
	loginFields := g.Config.Auth.Login.Fields
	emailField := "email"
	passwordField := "password"
	if len(loginFields) >= 2 {
		emailField = loginFields[0]
		passwordField = loginFields[1]
	}
	redirectURL := g.Config.Auth.Login.Redirect
	if redirectURL == "" {
		redirectURL = panelPath + "/dashboard"
	}

	// RBAC middleware generation. The middleware set is emitted when any
	// resource declares policies OR any custom action declares a policy (the
	// action/bulk routes get wrapped in ActionRBACMiddleware).
	var rbacMiddleware string
	var actionRBACMiddleware string
	var hasRBAC bool
	for _, r := range g.Config.Resources {
		if r.Policies != nil && r.Policies.ViewAny != "" {
			hasRBAC = true
		}
		for _, a := range r.Actions {
			if a.Policy != "" {
				hasRBAC = true
			}
		}
		if hasRBAC {
			break
		}
	}
	if hasRBAC {
		rbacMiddleware = `
func checkRole(required string, userRole string) bool {
    if required == "" {
        return true
    }
    roles := strings.Split(required, "|")
    for _, r := range roles {
        if strings.TrimSpace(r) == userRole {
            return true
        }
    }
    return false
}

func RBACMiddleware(resource string, action string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userRole, ok := r.Context().Value(UserRoleKey).(string)
            if !ok {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }
`
		for _, res := range g.Config.Resources {
			if res.Policies == nil {
				continue
			}
resLower := resourcePkgName(res.Name)
			p := res.Policies
			if p.ViewAny != "" {
				rbacMiddleware += fmt.Sprintf(`
            if resource == %q && action == "view_any" && !checkRole(%q, userRole) {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }`, resLower, p.ViewAny)
			}
			if p.View != "" {
				rbacMiddleware += fmt.Sprintf(`
            if resource == %q && action == "view" && !checkRole(%q, userRole) {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }`, resLower, p.View)
			}
			if p.Create != "" {
				rbacMiddleware += fmt.Sprintf(`
            if resource == %q && action == "create" && !checkRole(%q, userRole) {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }`, resLower, p.Create)
			}
			if p.Update != "" {
				rbacMiddleware += fmt.Sprintf(`
            if resource == %q && action == "update" && !checkRole(%q, userRole) {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }`, resLower, p.Update)
			}
			if p.Delete != "" {
				rbacMiddleware += fmt.Sprintf(`
            if resource == %q && action == "delete" && !checkRole(%q, userRole) {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }`, resLower, p.Delete)
			}
		}
		rbacMiddleware += `
            next.ServeHTTP(w, r)
        })
    }
}
`
	}

	// ActionRBACMiddleware: enforced on action/bulk routes when any custom
	// action declares a policy. It mirrors the resource RBAC checks but keys on
	// the :action path value, so per-action roles apply to both the single-row
	// action route and the bulk route.
	var actionChecks []string
	for _, res := range g.Config.Resources {
		resLower := strings.ToLower(res.Name)
		for _, a := range res.Actions {
			if a.Policy == "" {
				continue
			}
			actionChecks = append(actionChecks, fmt.Sprintf(`
            if resource == %q && action == %q && !checkRole(%q, userRole) {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }`, resLower, a.Name, a.Policy))
		}
	}
	if len(actionChecks) > 0 {
		actionRBACMiddleware = fmt.Sprintf(`
func ActionRBACMiddleware(resource string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userRole, ok := r.Context().Value(UserRoleKey).(string)
            if !ok {
                http.Error(w, "Forbidden", http.StatusForbidden)
                return
            }
            action := r.PathValue("action")%s
            next.ServeHTTP(w, r)
        })
    }
}
`, strings.Join(actionChecks, ""))
	}

	rateLimited := g.Config.Auth.Login.RateLimit != nil && g.Config.Auth.Login.RateLimit.MaxAttempts > 0

	loginPrelude := ""
	loginReset := ""
	if rateLimited {
		loginPrelude = fmt.Sprintf(`        if loginLimited(r) {
            vd := LoginPageData{
                Error:     "Too many login attempts. Please try again later.",
                PanelPath: %q,
                PanelName: %q,
                CSRFToken: CSRFToken(r, w),
            }
            LoginPage(vd).Render(r.Context(), w)
            return
        }

`, panelPath, panelName)
		loginReset = `        resetLoginLimit(r)
`
	}

	// Session rotation on successful login: the old session cookie is expired
	// and a brand-new session (fresh ID, empty values) is created, so a stolen
	// pre-login session ID cannot be fixed into an authenticated one.
	successSession := `        if old, err := GetSession(r); err == nil {
            old.Options.MaxAge = -1
            _ = old.Save(r, w)
        }

        // Store.New always returns a usable fresh session; its error only
        // reports that the incoming (possibly stale) cookie failed to decode,
        // which is irrelevant here because the new session is saved anyway.
        session, _ := Store.New(r, "yaga-session")

        session.Values["user_id"] = userID
        session.Values["role"] = userRole
        session.Values["name"] = displayName
        if err := session.Save(r, w); err != nil {
            http.Error(w, "Session save error", http.StatusInternalServerError)
            return
        }
`

	handlerCode := fmt.Sprintf(`package auth

import (
    "database/sql"
    "net/http"
    "golang.org/x/crypto/bcrypt"
)

func LoginHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet {
            vd := LoginPageData{
                PanelPath: %q,
                PanelName: %q,
                CSRFToken: CSRFToken(r, w),
            }
            LoginPage(vd).Render(r.Context(), w)
            return
        }

%s
        if err := r.ParseForm(); err != nil {
            vd := LoginPageData{
                Error:     "Invalid form submission",
                PanelPath: %q,
                PanelName: %q,
                CSRFToken: CSRFToken(r, w),
            }
            LoginPage(vd).Render(r.Context(), w)
            return
        }

        email := r.FormValue(%q)
        password := r.FormValue(%q)

        if email == "" || password == "" {
            vd := LoginPageData{
                Error:     "Email and password are required",
                PanelPath: %q,
                PanelName: %q,
                CSRFToken: CSRFToken(r, w),
            }
            LoginPage(vd).Render(r.Context(), w)
            return
        }

        var userID int64
        var displayName string
        var hashedPassword string
        var userRole string
        err := db.QueryRowContext(r.Context(),
            "SELECT id, COALESCE(name, ''), password, COALESCE(role_name, '') FROM %s WHERE %s = $1",
            email,
        ).Scan(&userID, &displayName, &hashedPassword, &userRole)
        if displayName == "" {
            displayName = email
        }
        if err != nil {
            vd := LoginPageData{
                Email:     email,
                Error:     "Invalid email or password",
                PanelPath: %q,
                PanelName: %q,
                CSRFToken: CSRFToken(r, w),
            }
            LoginPage(vd).Render(r.Context(), w)
            return
        }

        if err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password)); err != nil {
            vd := LoginPageData{
                Email:     email,
                Error:     "Invalid email or password",
                PanelPath: %q,
                PanelName: %q,
                CSRFToken: CSRFToken(r, w),
            }
            LoginPage(vd).Render(r.Context(), w)
            return
        }

%s%s
        http.Redirect(w, r, %q, http.StatusFound)
    }
}

func LogoutHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        session, err := GetSession(r)
        if err == nil {
            session.Values["user_id"] = nil
            session.Values["role"] = nil
            session.Values["name"] = nil
            session.Options.MaxAge = -1
            session.Save(r, w)
        }
        http.Redirect(w, r, %q, http.StatusFound)
    }
}

type LoginPageData struct {
    Email     string
    Error     string
    PanelPath string
    PanelName string
    CSRFToken string
}
`,
		panelPath, panelName,
		loginPrelude,
		panelPath, panelName,
		emailField, passwordField,
		panelPath, panelName,
		embedSQL(g.quoteIdent(authTable)), embedSQL(g.quoteIdent(emailField)),
		panelPath, panelName,
		panelPath, panelName,
		loginReset, successSession,
		redirectURL, panelPath)

	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(handlerCode), 0644); err != nil {
		return err
	}

	sessionCode := `package auth

import (
    "crypto/rand"
    "crypto/subtle"
    "encoding/base64"
    "log"
    "net/http"
    "os"
    "strings"

    "github.com/gorilla/sessions"
)

var Store *sessions.CookieStore

func newStore(secret []byte) *sessions.CookieStore {
    s := sessions.NewCookieStore(secret)
    s.Options = &sessions.Options{
        Path:     "/",
        MaxAge:   0,
        HttpOnly: true,
        Secure:   os.Getenv("APP_ENV") == "production",
        SameSite: http.SameSiteLaxMode,
    }
    return s
}

func Init() {
    if Store != nil {
        return
    }
    if v := os.Getenv("SESSION_SECRET"); v != "" {
        if len(v) < 32 {
            log.Fatal("SESSION_SECRET must be at least 32 characters")
        }
        Store = newStore([]byte(v))
        return
    }
    if os.Getenv("APP_ENV") == "production" {
        log.Fatal("SESSION_SECRET must be set when APP_ENV=production")
    }
    buf := make([]byte, 32)
    if _, err := rand.Read(buf); err != nil {
        log.Fatal(err)
    }
    Store = newStore(buf)
    log.Println("warning: SESSION_SECRET not set; using an ephemeral random secret (sessions invalidated on restart)")
}

func GetSession(r *http.Request) (*sessions.Session, error) {
    if Store == nil {
        Init()
    }
    return Store.Get(r, "yaga-session")
}

// clearSessionCookie deletes the session cookie on the response, used when the
// stored cookie can no longer be decoded.
func clearSessionCookie(w http.ResponseWriter) {
    http.SetCookie(w, &http.Cookie{
        Name:     "yaga-session",
        Value:    "",
        Path:     "/",
        MaxAge:   -1,
        HttpOnly: true,
        Secure:   os.Getenv("APP_ENV") == "production",
        SameSite: http.SameSiteLaxMode,
    })
}

// getOrNewSession returns the request's session. When the stored cookie is
// unreadable (e.g. signed with an earlier ephemeral SESSION_SECRET after a
// restart), it clears the stale cookie and returns a fresh anonymous session
// instead of an error. The boolean reports whether the session was reset.
func getOrNewSession(r *http.Request, w http.ResponseWriter) (*sessions.Session, bool, bool) {
    if Store == nil {
        Init()
    }
    session, err := Store.Get(r, "yaga-session")
    if err == nil {
        return session, false, true
    }
    clearSessionCookie(w)
    fresh := sessions.NewSession(Store, "yaga-session")
    opts := *Store.Options
    fresh.Options = &opts
    return fresh, true, true
}

// CSRFToken returns the session-bound CSRF token, generating and persisting a
// new one on first access. The token must be embedded in every state-changing
// form (hidden _csrf input) or header (X-CSRF-Token) of the rendered page.
func CSRFToken(r *http.Request, w http.ResponseWriter) string {
    session, _, ok := getOrNewSession(r, w)
    if !ok {
        return ""
    }
    if tok, ok := session.Values["csrf_token"].(string); ok && tok != "" {
        return tok
    }
    buf := make([]byte, 32)
    if _, err := rand.Read(buf); err != nil {
        return ""
    }
    tok := base64.RawURLEncoding.EncodeToString(buf)
    session.Values["csrf_token"] = tok
    _ = session.Save(r, w)
    return tok
}

func csrfMatches(expected, actual string) bool {
    if expected == "" || actual == "" {
        return false
    }
    return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func CSRFMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
            next.ServeHTTP(w, r)
            return
        }
        if strings.HasPrefix(r.URL.Path, "/static/") || strings.HasPrefix(r.URL.Path, "/uploads/") {
            next.ServeHTTP(w, r)
            return
        }
        session, reset, ok := getOrNewSession(r, w)
        if !ok {
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
        if reset && r.URL.Path == %s {
            // The session cookie was unreadable (e.g. signed with an earlier
            // ephemeral secret after a restart), so there is no expected token
            // to compare against. Let the login request through so the user can
            // establish a fresh session instead of a hard 403.
            next.ServeHTTP(w, r)
            return
        }
        expected, _ := session.Values["csrf_token"].(string)
        if !csrfMatches(expected, r.FormValue("_csrf")) && !csrfMatches(expected, r.Header.Get("X-CSRF-Token")) {
            http.Error(w, "Forbidden", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}
`
	loginPath := panelPath + "/login"
	sessionCode = fmt.Sprintf(sessionCode, strconv.Quote(loginPath))

	if err := os.WriteFile(filepath.Join(dir, "session.go"), []byte(sessionCode), 0644); err != nil {
		return err
	}

	// Login rate limiting: emitted only when auth.login.rate_limit is set.
	// The limiter is a sliding-window per-IP counter with a global mutex; a
	// successful login resets the caller's bucket via resetLoginLimit.
	if rl := g.Config.Auth.Login.RateLimit; rl != nil && rl.MaxAttempts > 0 {
		rateLimitCode := fmt.Sprintf(`package auth

import (
    "net"
    "net/http"
    "sync"
    "time"
)

type loginAttempt struct {
    count int
    first time.Time
}

var loginAttempts = struct {
    sync.Mutex
    byIP map[string]*loginAttempt
}{byIP: make(map[string]*loginAttempt)}

const loginMaxAttempts = %d
const loginWindow = %d * time.Second

func clientIP(r *http.Request) string {
    host, _, err := net.SplitHostPort(r.RemoteAddr)
    if err != nil {
        return r.RemoteAddr
    }
    return host
}

func loginLimited(r *http.Request) bool {
    ip := clientIP(r)
    loginAttempts.Lock()
    defer loginAttempts.Unlock()
    now := time.Now()
    a := loginAttempts.byIP[ip]
    if a == nil {
        a = &loginAttempt{first: now}
        loginAttempts.byIP[ip] = a
    }
    if now.Sub(a.first) >= loginWindow {
        a.first = now
        a.count = 0
    }
    a.count++
    return a.count > loginMaxAttempts
}

func resetLoginLimit(r *http.Request) {
    ip := clientIP(r)
    loginAttempts.Lock()
    defer loginAttempts.Unlock()
    delete(loginAttempts.byIP, ip)
}
`, rl.MaxAttempts, rl.WindowSeconds)
		if err := os.WriteFile(filepath.Join(dir, "ratelimit.go"), []byte(rateLimitCode), 0644); err != nil {
			return err
		}
	}

	auditImport := ""
	auditHelper := ""
	if g.auditAnyResource() {
		auditImport = "    \"fmt\"\n"
		auditHelper = `
func UserID(r *http.Request) string {
    if id := r.Context().Value(UserKey); id != nil {
        return fmt.Sprintf("%v", id)
    }
    return ""
}
`
	}
	roleHelper := ""
	if g.hasAnyScript() {
		roleHelper = `
func RoleName(r *http.Request) string {
    if role, ok := r.Context().Value(UserRoleKey).(string); ok {
        return role
    }
    return ""
}
`
	}

	middlewareCode := fmt.Sprintf(`package auth

import (
    "context"
    "net/http"
    "strings"
%s)

type contextKey string

const UserKey contextKey = "user"
const UserRoleKey contextKey = "role"
const UserNameKey contextKey = "user_name"

func SessionMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "/static/") {
            next.ServeHTTP(w, r)
            return
        }
        next.ServeHTTP(w, r)
    })
}

func AuthMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "%s/login") {
            next.ServeHTTP(w, r)
            return
        }
        session, err := GetSession(r)
        if err != nil || session.Values["user_id"] == nil {
            http.Redirect(w, r, "%s/login", http.StatusFound)
            return
        }
        ctx := context.WithValue(r.Context(), UserKey, session.Values["user_id"])
        if role, ok := session.Values["role"].(string); ok {
            ctx = context.WithValue(ctx, UserRoleKey, role)
        }
        if name, ok := session.Values["name"].(string); ok {
            ctx = context.WithValue(ctx, UserNameKey, name)
        }
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func UserName(r *http.Request) string {
    if name, ok := r.Context().Value(UserNameKey).(string); ok {
        return name
    }
    return ""
}
%s%s%s`, auditImport, panelPath, panelPath, auditHelper, roleHelper, rbacMiddleware+actionRBACMiddleware)

	if err := os.WriteFile(filepath.Join(dir, "middleware.go"), []byte(middlewareCode), 0644); err != nil {
		return err
	}

	// Generate login Templ component
	return g.generateAuthLoginTempl(dir, panelName, panelPath)
}

// generateAuthLoginTempl writes the login.templ page: a full standalone HTML
// document (Tailwind styled) that posts the email/password form to
// panelPath/login and shows any login error message. The page honors the
// panel's dark-mode default and persists the user's toggle via localStorage
// like the Base layout does.
// Params: dir (the auth package directory), panelName (panel display name),
// panelPath (panel base path used in the form action).
// Returns: an error on write failure.
func (g *Generator) generateAuthLoginTempl(dir string, panelName, panelPath string) error {
	primary := g.Config.Panel.Brand.Colors.Primary
	if primary == "" {
		primary = "#6366f1"
	}
	secondary := g.Config.Panel.Brand.Colors.Secondary
	if secondary == "" {
		secondary = "#8b5cf6"
	}
	styleFonts := ""
	if g.Config.Panel.Theme.Font.Family != "" {
		styleFonts += fmt.Sprintf("\n    body { font-family: %s; }", g.Config.Panel.Theme.Font.Family)
	}
	if g.Config.Panel.Theme.Font.Mono != "" {
		styleFonts += fmt.Sprintf("\n    code, pre { font-family: %s; }", g.Config.Panel.Theme.Font.Mono)
	}
	htmlClass := ""
	if g.Config.Panel.Theme.DarkMode {
		htmlClass = ` class="dark"`
	}

	code := fmt.Sprintf(`package auth

templ LoginPage(data LoginPageData) {
    <!DOCTYPE html>
    <html lang="en"%s>
    <head>
        <meta charset="UTF-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>Login - %s</title>
        <link href="/static/css/styles.css" rel="stylesheet" />
        <style>
            :root {
                --brand-primary: %s;
                --brand-secondary: %s;
                --brand-primary-rgb: %s;
                --brand-secondary-rgb: %s;
            }%s
        </style>
    </head>
    <body class="bg-gray-50 dark:bg-gray-900 min-h-screen flex items-center justify-center">
        <div class="w-full max-w-md">
            <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-8">
                <div class="text-center mb-8">
                    <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">%s</h1>
                    <p class="text-sm text-gray-500 dark:text-gray-400 mt-1">Sign in to your account</p>
                </div>

                if data.Error != "" {
                <div class="bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-700 text-red-700 dark:text-red-300 px-4 py-3 rounded mb-4 text-sm">
                    { data.Error }
                </div>
                }

                <form action="%s/login" method="POST" class="space-y-4">
                    <input type="hidden" name="_csrf" value={ data.CSRFToken } />
                    <div>
                        <label for="email" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Email</label>
                        <input type="email" id="email" name="email" value={ data.Email } required
                            class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
                    </div>
                    <div>
                        <label for="password" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Password</label>
                        <input type="password" id="password" name="password" required
                            class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
                    </div>
                    <div>
                        <button type="submit"
                            class="w-full bg-brand-primary text-white py-2 px-4 rounded-md text-sm font-medium hover:opacity-90 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-brand-primary">
                            Sign in
                        </button>
                    </div>
                </form>
            </div>
        </div>
        <script>
            (function() {
                var html = document.documentElement;
                var saved = localStorage.getItem('yaga-theme');
                if (saved === 'dark') { html.classList.add('dark'); }
                else if (saved === 'light') { html.classList.remove('dark'); }
            })();
        </script>
    </body>
    </html>
}
`, htmlClass, panelName, primary, secondary, hexChannels(primary), hexChannels(secondary), styleFonts, panelName, panelPath)

	return os.WriteFile(filepath.Join(dir, "login.templ"), []byte(code), 0644)
}
