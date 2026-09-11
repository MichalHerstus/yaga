// router.go
//
// Generates the router for the admin panel application (internal/panel/router.go)
// and the HTTP handler for each custom page (internal/panel/pages).
// The router wires chi middleware, static file serving, login/logout routes,
// per-resource CRUD routes (with optional RBAC wrapping) and page routes under
// the configured panel path.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
)

// generateRouter writes internal/panel/router.go. It builds the import list
// (database/sql, chi, auth, pages and one resource package per resource), then
// emits the NewRouter function with middleware, static file handling, the
// login/logout endpoints, CRUD routes per resource and page routes. Resources
// with policies get their routes wrapped in auth.RBACMiddleware; action
// routes are always plain POST. Returns an error on write failure.
func (g *Generator) generateRouter() error {
	var importPaths []string
	importPaths = append(importPaths, `"database/sql"`, `"log"`, `"net/http"`)
	importPaths = append(importPaths, `"github.com/go-chi/chi/v5"`, `"github.com/go-chi/chi/v5/middleware"`)
	importPaths = append(importPaths, fmt.Sprintf("%q", g.moduleImport("internal/panel/auth")))
	importPaths = append(importPaths, fmt.Sprintf("%q", g.moduleImport("internal/panel/pages")))
	importPaths = append(importPaths, fmt.Sprintf("%q", g.moduleImport("internal/viewmodels")))

	for _, r := range g.Config.Resources {
		name := resourcePkgName(r.Name)
		importPaths = append(importPaths, fmt.Sprintf("%q", g.moduleImport("internal/panel/resources/"+name)))
	}

	code := "package panel\n\nimport (\n"
	for _, ip := range importPaths {
		code += "\t" + ip + "\n"
	}
	code += ")\n\n"

	code += `func NewRouter(db *sql.DB, logLevel string) http.Handler {
	r := chi.NewRouter()

	if logLevel == "err" {
		r.Use(errorOnlyLogger)
	} else {
		r.Use(middleware.Logger)
	}
	r.Use(middleware.Recoverer)
	r.Use(auth.SessionMiddleware)
	r.Use(securityHeaders)
	r.Use(flashHandler)

	fileServer := http.FileServer(http.Dir("static"))
	r.Handle("/static/*", http.StripPrefix("/static/", fileServer))
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", "attachment")
		http.FileServer(http.Dir("static/uploads")).ServeHTTP(w, r)
	})))

	panelPath := "` + g.Config.Panel.Path + `"

	r.Route(panelPath, func(r chi.Router) {
		r.Use(auth.CSRFMiddleware)
		r.Get("/login", auth.LoginHandler(db))
		r.Post("/login", auth.LoginHandler(db))

		r.Group(func(r chi.Router) {
			r.Use(auth.AuthMiddleware)
`

	for _, res := range g.Config.Resources {
		name := resourcePkgName(res.Name)
		rbacPrefix := func(action string) string {
			if res.Policies != nil {
				return fmt.Sprintf("r.With(auth.RBACMiddleware(%q, %q)).", name, action)
			}
			return "r."
		}
		if res.List != nil {
			code += fmt.Sprintf("\t\t%sGet(\"/%s\", %s.List(db))\n", rbacPrefix("view_any"), name, name)
		}
		if res.Card != nil {
			code += fmt.Sprintf("\t\t%sGet(\"/%s/cards\", %s.Cards(db))\n", rbacPrefix("view_any"), name, name)
		}
		if res.Form != nil && res.Form.Create != nil {
			code += fmt.Sprintf("\t\t%sGet(\"/%s/new\", %s.Create(db))\n", rbacPrefix("create"), name, name)
			code += fmt.Sprintf("\t\t%sPost(\"/%s/new\", %s.Create(db))\n", rbacPrefix("create"), name, name)
		}
		if res.Detail != nil {
			code += fmt.Sprintf("\t\t%sGet(\"/%s/{id}\", %s.Detail(db))\n", rbacPrefix("view"), name, name)
		}
		if res.Form != nil && res.Form.Update != nil {
			code += fmt.Sprintf("\t\t%sGet(\"/%s/{id}/edit\", %s.Update(db))\n", rbacPrefix("update"), name, name)
			code += fmt.Sprintf("\t\t%sPost(\"/%s/{id}\", %s.Update(db))\n", rbacPrefix("update"), name, name)
		}
		if res.Form != nil && res.Form.Delete != nil {
			code += fmt.Sprintf("\t\t%sPost(\"/%s/{id}/delete\", %s.Delete(db))\n", rbacPrefix("delete"), name, name)
		}
		if len(res.Actions) > 0 {
			actionPrefix := "r."
			if hasActionPolicies(res) {
				actionPrefix = fmt.Sprintf("r.With(auth.ActionRBACMiddleware(%q)).", name)
			}
			code += fmt.Sprintf("\t\t%sPost(\"/%s/{id}/action/{action}\", %s.Action(db))\n", actionPrefix, name, name)
			if hasBulkActions(res) {
				code += fmt.Sprintf("\t\t%sPost(\"/%s/bulk/{action}\", %s.Bulk(db))\n", actionPrefix, name, name)
			}
		}
		if res.List != nil {
			code += fmt.Sprintf("\t\t%sGet(\"/%s/export/csv\", %s.ExportCSV(db))\n", rbacPrefix("view_any"), name, name)
		}
		if res.ImportCSV {
			code += fmt.Sprintf("\t\t%sPost(\"/%s/import/csv\", %s.ImportCSV(db))\n", rbacPrefix("create"), name, name)
		}
	}

	for _, p := range g.Config.Pages {
		capID := capitalize(goIdent(g.Config.Panel.ID))
		handlerName := fmt.Sprintf("pages.%s%s(db)", capID, pageIdent(p.Name))
		if p.Default {
			code += fmt.Sprintf("\t\tr.Get(\"/\", %s)\n", handlerName)
		}
		path := p.Path
		if path == "" {
			path = "/" + pageIdent(p.Name)
		}
		if path != "/" {
			code += fmt.Sprintf("\t\tr.Get(\"%s\", %s)\n", path, handlerName)
		}
	}

	code += fmt.Sprintf("\t\tr.Post(\"/logout\", auth.LogoutHandler(db))\n")

	code += `		})
	})

	return r
}

// errorOnlyLogger logs only requests that produced an error response (>= 400).
func errorOnlyLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if ww.Status() >= http.StatusBadRequest {
			log.Printf("%s %s -> %d", r.Method, r.RequestURI, ww.Status())
		}
	})
}

// securityHeaders sets a baseline set of HTTP security headers on every
// response: frame protection, MIME sniffing protection, referrer policy,
// permissions policy and a restrictive Content-Security-Policy. Inline scripts
// and styles are allowed (the generated views use them for theme toggling and
// the record picker); all other assets must come from the app origin.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; font-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// flashHandler surfaces a one-shot ?flash= query message (set by redirects
// such as the CSV import result) to every rendered layout via the request
// context; Base renders it in the topbar area.
func flashHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f := r.URL.Query().Get("flash"); f != "" {
			r = r.WithContext(viewmodels.SetFlash(r.Context(), f))
		}
		next.ServeHTTP(w, r)
	})
}
`

	dir := filepath.Join(g.OutDir, "internal/panel")
	return os.WriteFile(filepath.Join(dir, "router.go"), []byte(code), 0644)
}

// pageIdent converts a page name into a safe identifier used for the generated
// Go/templ function names and file names. The raw name is preserved for display
// (page heading, nav label); the identifier replaces each run of
// whitespace/punctuation with a single underscore (e.g. "Order Management"
// -> "Order_Management"). Names containing spaces or leading digits are never
// emitted verbatim — see goIdent for the exact collapse rules.
func pageIdent(name string) string {
	return goIdent(name)
}

// generatePage writes one page handler per configured Page. The handler
// initializes an empty widget list, runs the DB queries declared by each
// widget (stat, chart, table, stats_grid), packs them into a viewmodels.PageData
// and renders the matching templ page view.
// Params: p (the Page definition to generate a handler for).
// Returns: an error on write failure.
func (g *Generator) generatePage(p types.Page) error {
	dir := filepath.Join(g.OutDir, "internal/panel/pages")
	name := pageIdent(p.Name)
	panelID := g.Config.Panel.ID
	panelPath := g.Config.Panel.Path
	capitalID := capitalize(goIdent(panelID))
	handlerName := capitalID + name
	viewName := capitalID + name

	var widgetInit []string
	for i, w := range p.Widgets {
		switch w.Type {
		case "stat":
			var valExpr string
			if w.Query != "" {
				valExpr = fmt.Sprintf(`template.HTML(fmt.Sprintf("%%s%%d%%s", %q, count%d, %q))`, w.Prefix, i, "")
			} else {
				valExpr = `"0"`
			}
			widgetInit = append(widgetInit, fmt.Sprintf(`
        var count%d int64
        if q := %q; q != "" {
            if err := db.QueryRowContext(r.Context(), q).Scan(&count%d); err != nil {
                log.Printf("page %%s widget %%d (%%s) stat: %%v", %q, %d, %q, err)
            }
        }
        widgets = append(widgets, viewmodels.WidgetData{
            Type: "stat",
            Label: %q,
            Color: %q,
            Icon: %q,
            Value: %s,
        })`, i, w.Query, i, p.Name, i, w.Label, w.Label, w.Color, w.Icon, valExpr))
		case "chart":
			widgetInit = append(widgetInit, fmt.Sprintf(`
        {
        var chartLabels []string
        var chartValues []float64
        if q := %q; q != "" {
            rows, err := db.QueryContext(r.Context(), q)
            if err != nil {
                log.Printf("page %%s widget %%d (%%s) chart: %%v", %q, %d, %q, err)
            } else {
                defer rows.Close()
                for rows.Next() {
                    var label string
                    var val float64
                    if err := rows.Scan(&label, &val); err != nil {
                        log.Printf("page %%s widget %%d (%%s) chart scan: %%v", %q, %d, %q, err)
                        continue
                    }
                    chartLabels = append(chartLabels, label)
                    chartValues = append(chartValues, val)
                }
            }
        }
        chartLabelsJSON, _ := json.Marshal(chartLabels)
        chartValuesJSON, _ := json.Marshal(chartValues)
        widgets = append(widgets, viewmodels.WidgetData{
            Type: "chart",
            Label: %q,
            ChartType: %q,
            ChartLabelsJSON: string(chartLabelsJSON),
            ChartValuesJSON: string(chartValuesJSON),
        })
        }`, w.Query, p.Name, i, w.Label, p.Name, i, w.Label, w.Label, w.Chart.Type))
		case "table":
			colList := "[]string{"
			for _, col := range w.DataColumns {
				colList += fmt.Sprintf("%q, ", col)
			}
			colList += "}"
			widgetInit = append(widgetInit, fmt.Sprintf(`
        {
        var tableRows []map[string]interface{}
        if q := %q; q != "" {
            dataRows, err := db.QueryContext(r.Context(), q)
            if err != nil {
                log.Printf("page %%s widget %%d (%%s) table: %%v", %q, %d, %q, err)
            } else {
                defer dataRows.Close()
                dCols, _ := dataRows.Columns()
                for dataRows.Next() {
                    vals := make([]interface{}, len(dCols))
                    valPtrs := make([]interface{}, len(dCols))
                    for i := range vals {
                        valPtrs[i] = &vals[i]
                    }
                    if err := dataRows.Scan(valPtrs...); err != nil {
                        log.Printf("page %%s widget %%d (%%s) table scan: %%v", %q, %d, %q, err)
                        continue
                    }
                    row := make(map[string]interface{})
                    for i, col := range dCols {
                        row[col] = vals[i]
                    }
                    tableRows = append(tableRows, row)
                }
            }
        }
        widgets = append(widgets, viewmodels.WidgetData{
            Type: "table",
            Label: %q,
            TableColumns: %s,
            TableRows: tableRows,
        })
        }`, w.Query, p.Name, i, w.Label, p.Name, i, w.Label, w.Label, colList))
		case "stats_grid":
			widgetInit = append(widgetInit, fmt.Sprintf(`
        var subWidgets%d []viewmodels.WidgetData`, i))
			for _, sw := range w.Widgets {
				widgetInit = append(widgetInit, fmt.Sprintf(`
        {
            var subCount int64
            if q := %q; q != "" {
                if err := db.QueryRowContext(r.Context(), q).Scan(&subCount); err != nil {
                    log.Printf("page %%s widget %%d (%%s) stats_grid: %%v", %q, %d, %q, err)
                }
            }
            subWidgets%d = append(subWidgets%d, viewmodels.WidgetData{
                Type: "stat",
                Label: %q,
                Color: %q,
                Icon: %q,
                Value: template.HTML(fmt.Sprintf("%%d", subCount)),
            })
        }`, sw.Query, p.Name, i, sw.Label, i, i, sw.Label, sw.Color, sw.Icon))
			}
			widgetInit = append(widgetInit, fmt.Sprintf(`
        widgets = append(widgets, viewmodels.WidgetData{
            Type: "stats_grid",
            SubWidgets: subWidgets%d,
        })`, i))
		case "list":
			widgetInit = append(widgetInit, fmt.Sprintf(`
        {
        var listRows []map[string]interface{}
        if q := %q; q != "" {
            listQueryRows, err := db.QueryContext(r.Context(), q)
            if err != nil {
                log.Printf("page %%s widget %%d (%%s) list: %%v", %q, %d, %q, err)
            } else {
                defer listQueryRows.Close()
                listCols, _ := listQueryRows.Columns()
                for listQueryRows.Next() {
                    listVals := make([]interface{}, len(listCols))
                    listPtrs := make([]interface{}, len(listCols))
                    for i := range listVals {
                        listPtrs[i] = &listVals[i]
                    }
                    if err := listQueryRows.Scan(listPtrs...); err != nil {
                        log.Printf("page %%s widget %%d (%%s) list scan: %%v", %q, %d, %q, err)
                        continue
                    }
                    listRow := make(map[string]interface{})
                    for i, col := range listCols {
                        listRow[col] = listVals[i]
                    }
                    listRows = append(listRows, listRow)
                }
            }
        }
        widgets = append(widgets, viewmodels.WidgetData{
            Type: "list",
            Label: %q,
            TableRows: listRows,
        })
        }`, w.Query, p.Name, i, w.Label, p.Name, i, w.Label, w.Label))
		case "html":
			widgetInit = append(widgetInit, fmt.Sprintf(`
        {
        var htmlVal string
        if q := %q; q != "" {
            if err := db.QueryRowContext(r.Context(), q).Scan(&htmlVal); err != nil {
                log.Printf("page %%s widget %%d (%%s) html: %%v", %q, %d, %q, err)
            }
        }
        widgets = append(widgets, viewmodels.WidgetData{
            Type: "html",
            Label: %q,
            Value: template.HTML(htmlVal),
        })
        }`, w.Query, p.Name, i, w.Label, w.Label))
		}
	}

	logImport := ""
	if len(p.Widgets) > 0 {
		logImport = `    "log"
`
	}

	jsonImport := ""
	for _, w := range p.Widgets {
		if w.Type == "chart" {
			jsonImport = `    "encoding/json"
`
			break
		}
	}

	code := fmt.Sprintf(`package pages

import (
    "database/sql"
%s    "fmt"
    "html/template"
%s    "net/http"

    %q
    auth %q
    httperr %q
    layoutviews %q
    pageviews %q
)

func %s(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var widgets []viewmodels.WidgetData

        %s

        pd := &viewmodels.PageData{
            Name:      %q,
            PanelID:   %q,
            PanelPath: %q,
            Widgets:   widgets,
        }

        err := layoutviews.Base(pd.Name, pd.PanelPath, viewmodels.DefaultTheme(), auth.UserName(r), auth.CSRFToken(r, w), pageviews.%s(pd)).Render(r.Context(), w)
        if err != nil {
            httperr.Internal(w, err)
        }
    }
}
`, jsonImport, logImport, g.moduleImport("internal/viewmodels"), g.moduleImport("internal/panel/auth"), g.moduleImport("internal/panel/httperr"), g.moduleImport("internal/views/layout"), g.moduleImport("internal/views/pages"),
		handlerName, strings.Join(widgetInit, "\n"), p.Name, panelID, panelPath, viewName)

	return os.WriteFile(filepath.Join(dir, name+".go"), []byte(code), 0644)
}
