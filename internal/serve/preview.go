// preview.go
//
// Implements the wedit dashboard preview (GET /preview). It renders a faithful
// HTML mock of a generated admin page (custom pages, resource lists, cards,
// detail views and forms) from the in-memory config: the same Tailwind classes,
// brand colors, fonts, sidebar width, sticky topbar and dark-mode handling the
// generated app uses, plus the vendored Chart.js for chart widgets.
// When `?live=1` is set and a database connection resolves (--db flag or the
// config's first `connections:` entry), widget queries and resource rows are
// fetched from the live DB; otherwise deterministic fake data is used. The mock
// is served inside an iframe by the SPA's Preview tab; the stylesheet and
// Chart.js bundle are served from the generator's embedded assets so the
// preview and the generated dashboard stay byte-identical.
package serve

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/MichalHerstus/yaga/internal/generator"
	"github.com/MichalHerstus/yaga/internal/types"
)

// previewView lists the kinds of preview that can be rendered.
const (
	previewPage     = "page"
	previewResource = "resource"
	previewDetail   = "detail"
	previewForm     = "form"
	previewCard     = "card"
)

// previewMode is the create/update mode for form previews.
const (
	formModeCreate = "create"
	formModeUpdate = "update"
)

// handlePreview renders the dashboard mock for ?view=..&resource=..|page=..|id=.
// Optional ?live=1 fetches widget/resource data from the live database instead
// of fake data. ?theme=auto|light|dark controls the color scheme.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	// Resolve the live connection the same way the schema browser does (--db
	// flag first, then the first configured connection with a relative SQLite
	// DSN anchored at the config's directory) and expose it to the standalone
	// preview helpers through a shallow config copy. Without this the preview
	// ignored --db and mis-resolved relative DSNs, so live data silently fell
	// back to sample rows.
	if dsn, driver := s.liveConnection(); dsn != "" {
		cfgCopy := *cfg
		cfgCopy.Connections = map[string]types.Connection{"live": {Driver: driver, DSN: dsn}}
		cfg = &cfgCopy
	}

	q := r.URL.Query()
	theme := q.Get("theme")
	if theme != "light" && theme != "dark" {
		theme = "auto"
	}
	live := q.Get("live") == "1"
	embed := q.Get("embed") == "1"

	var body string
	switch q.Get("view") {
	case previewResource:
		body = resourcePreviewBody(cfg, q.Get("resource"), live)
	case previewDetail:
		body = detailPreviewBody(cfg, q.Get("resource"), q.Get("id"), live)
	case previewForm:
		body = formPreviewBody(cfg, q.Get("resource"), q.Get("mode"), q.Get("id"), live)
	case previewCard:
		body = cardPreviewBody(cfg, q.Get("resource"), live)
	default:
		body = pagePreviewBody(cfg, q.Get("page"), live)
	}

	doc := previewDoc(cfg, theme, body, live, embed)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Preview is always rendered from live state; never let a browser serve a
	// cached copy (it would show stale data after switching target/live mode).
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	_, _ = w.Write([]byte(doc))
}

// handlePreviewStyles serves the pre-built Tailwind stylesheet the generated
// app ships at static/css/styles.css, so the preview uses the exact compiled
// classes of the real dashboard.
func (s *Server) handlePreviewStyles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(generator.StylesCSS())
}

// handlePreviewChart serves the vendored Chart.js bundle the generated app
// ships at static/js/chart.js.
func (s *Server) handlePreviewChart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(generator.ChartUmdJS())
}

// previewDoc assembles the full HTML document around the mock body: the same
// shell as the generated base.templ (sidebar/topbar/brand vars/fonts/dark
// mode) plus the generated app's theme toggle + Chart.js auto-render scripts.
// When embed is true the preview is shown inside wedit's iframe: sidebar links
// that switch resource/page are disabled — only the wedit toolbar dropdown may
// switch the target.
func previewDoc(cfg *types.Config, theme string, body string, live bool, embed bool) string {
	panel := cfg.Panel
	panelName := panel.Name
	if panelName == "" {
		panelName = "admin"
	}
	primary := panel.Brand.Colors.Primary
	if primary == "" {
		primary = "#6366f1"
	}
	secondary := panel.Brand.Colors.Secondary
	if secondary == "" {
		secondary = "#8b5cf6"
	}

	var fonts strings.Builder
	if panel.Theme.Font.Family != "" {
		fmt.Fprintf(&fonts, "body { font-family: %s; }\n", escCSS(panel.Theme.Font.Family))
	}
	if panel.Theme.Font.Mono != "" {
		fmt.Fprintf(&fonts, "code, pre { font-family: %s; }\n", escCSS(panel.Theme.Font.Mono))
	}

	sticky := "relative"
	if panel.Layout.Topbar.Sticky {
		sticky = "sticky top-0 z-10"
	}
	sidebarWidth := panel.Layout.Sidebar.Width
	if sidebarWidth <= 0 {
		sidebarWidth = 256
	}
	maxW := cfg.Panel.Layout.MaxContentWidth
	if maxW == "" {
		maxW = "none"
	}

	// Resolve live DB connection for the toggle button state.
	_, driver := sLiveConnection(cfg)

	// Live-data toggle button shown in the preview topbar. In embedded mode the
	// wedit toolbar owns the checkbox, so the button is display-only there (two
	// controls for one flag would immediately desync).
	var liveBtnClass, liveBtnText, liveBtnBadge string
	if live {
		if driver != "" {
			liveBtnClass = "btn text-emerald-700 border-emerald-200 hover:bg-emerald-50"
			liveBtnText = "Live: ON"
			liveBtnBadge = `<span class="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>`
		} else {
			liveBtnClass = "btn text-amber-700 border-amber-200 hover:bg-amber-50 opacity-50 cursor-not-allowed"
			liveBtnText = "Live: unavailable"
			liveBtnBadge = `<span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>`
		}
	} else {
		liveBtnClass = "btn text-gray-600 border-gray-200 hover:bg-gray-50"
		liveBtnText = "Live: OFF"
		liveBtnBadge = `<span class="w-1.5 h-1.5 rounded-full bg-gray-400"></span>`
	}
	liveToggleBtn := ""
	if !embed && (driver != "" || !live) {
		liveToggleBtn = `<button onclick="toggleLive()" class="` + liveBtnClass + `" id="liveToggle" title="Toggle live data">
  ` + liveBtnBadge + ` ` + escHTML(liveBtnText) + `
</button>`
	} else {
		liveToggleBtn = `<span class="` + liveBtnClass + `" id="liveToggle" title="Controlled by the preview toolbar">
  ` + liveBtnBadge + ` ` + escHTML(liveBtnText) + `
</span>`
	}

	// Dark-mode init: mirror the generated Base layout. auto follows the
	// config; a forced theme bypasses the saved preference on first load but
	// toggling still writes localStorage (so a later auto view honors it).
	darkInit := "var html = document.documentElement;\n"
	switch theme {
	case "light":
		darkInit += "html.classList.remove('dark');\n"
	case "dark":
		darkInit += "html.classList.add('dark');\n"
	default:
		def := "false"
		if panel.Theme.DarkMode {
			def = "true"
		}
		darkInit += "var saved = localStorage.getItem('yaga-theme');\n" +
			"if (saved === 'dark') { html.classList.add('dark'); }\n" +
			"else if (saved === 'light') { html.classList.remove('dark'); }\n" +
			"else if (" + def + ") { html.classList.add('dark'); }\n"
	}

	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en" class="preview">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Preview</title>
<link href="/preview/styles.css" rel="stylesheet">
<script src="/preview/chart.js"></script>
<style>
:root {
  --brand-primary: ` + primary + `;
  --brand-secondary: ` + secondary + `;
  --brand-primary-rgb: ` + generator.HexChannels(primary) + `;
  --brand-secondary-rgb: ` + generator.HexChannels(secondary) + `;
}
` + fonts.String() + `
</style>
</head>
<body class="bg-gray-50 dark:bg-gray-900">
<div class="flex h-screen">
  <aside id="app-sidebar" data-width="` + fmt.Sprint(sidebarWidth) + `" class="bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 shadow-md h-screen overflow-y-auto shrink-0">
    <div class="p-4 border-b border-gray-200 dark:border-gray-700">
      <h1 class="text-xl font-bold text-gray-900 dark:text-gray-100">` + escHTML(panelName) + `</h1>
    </div>
    <nav class="mt-2">
` + previewSidebar(cfg, embed) + `    </nav>
  </aside>
  <div class="flex-1 flex flex-col min-w-0">
    <header class="bg-white dark:bg-gray-800 shadow-sm px-6 py-3 flex items-center justify-between ` + sticky + `">
      <div class="flex items-center gap-4">
        <button class="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" onclick="toggleTheme()" title="Toggle dark mode">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" /></svg>
        </button>
        <span class="text-sm text-gray-500 dark:text-gray-400">preview</span>
        ` + liveToggleBtn + `
      </div>
      <div class="flex items-center gap-4">
        <span class="text-sm text-gray-700 dark:text-gray-300">admin</span>
        <span class="text-sm text-gray-600 dark:text-gray-400">Logout</span>
      </div>
    </header>
    <main class="flex-1 overflow-y-auto p-6">
      <div class="max-w-` + escAttr(maxW) + ` mx-auto">
` + body + `      </div>
    </main>
  </div>
</div>
<script>
function toggleTheme() {
  var html = document.documentElement;
  html.classList.toggle('dark');
  localStorage.setItem('yaga-theme', html.classList.contains('dark') ? 'dark' : 'light');
}
function toggleLive() {
  var url = new URL(window.location.href);
  var live = url.searchParams.get('live');
  if (live === '1') {
    url.searchParams.delete('live');
  } else {
    url.searchParams.set('live', '1');
  }
  window.location.href = url.toString();
}
(function() { ` + darkInit + ` })();
(function() {
  var aside = document.getElementById('app-sidebar');
  if (aside) { aside.style.width = aside.getAttribute('data-width') + 'px'; }
})();
document.addEventListener('DOMContentLoaded', function() {
  var rootStyle = getComputedStyle(document.documentElement);
  var brand = (rootStyle.getPropertyValue('--brand-primary') || '#6366f1').trim();
  function hexToRgba(hex, alpha) {
    var m = /^#([0-9a-fA-F]{6})$/.exec(hex);
    if (!m) { return 'rgba(99, 102, 241, ' + alpha + ')'; }
    var n = parseInt(m[1], 16);
    return 'rgba(' + ((n >> 16) & 255) + ', ' + ((n >> 8) & 255) + ', ' + (n & 255) + ', ' + alpha + ')';
  }
  document.querySelectorAll('canvas[data-chart-type]').forEach(function(canvas) {
    var ctx = canvas.getContext('2d');
    var type = canvas.dataset.chartType;
    var labels = JSON.parse(canvas.dataset.labels || '[]');
    var values = JSON.parse(canvas.dataset.values || '[]');
    new Chart(ctx, {
      type: type,
      data: {
        labels: labels,
        datasets: [{
          label: canvas.parentElement.querySelector('h3')?.textContent || '',
          data: values,
          borderColor: brand,
          backgroundColor: hexToRgba(brand, 0.2),
        }]
      }
    });
  });
});
</script>
</body>
</html>`)
	return b.String()
}

// sLiveConnection resolves the live DB connection from a config (no lock,
// called from previewDoc which already holds no lock but cfg is a copy).
func sLiveConnection(cfg *types.Config) (dsn, driver string) {
	return configLiveConnection(cfg, "")
}

// previewSidebar renders the sorted navigation groups and their items using
// the exact href/label rules of the generated generateLayoutViews. When embed
// is true (preview inside wedit's iframe) resource/page items are non-clickable
// spans — only the wedit toolbar dropdown may switch the target.
func previewSidebar(cfg *types.Config, embed bool) string {
	sortedNav := make([]types.NavigationGroup, len(cfg.Navigation))
	copy(sortedNav, cfg.Navigation)
	sort.Slice(sortedNav, func(i, j int) bool {
		return sortedNav[i].Sort < sortedNav[j].Sort
	})

	var b strings.Builder
	if len(sortedNav) > 0 {
		for _, ng := range sortedNav {
			icon := navIconSVG(ng.Icon)
			fmt.Fprintf(&b, "      <div class=\"px-4 py-2 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider mt-4\">%s%s</div>\n", icon, escHTML(ng.Group))
			for _, item := range ng.Items {
				if item.Resource != "" {
					label := item.Resource
					if item.Label != "" {
						label = item.Label
					}
					if embed {
						fmt.Fprintf(&b, "      <span class=\"block px-4 py-2 text-sm text-gray-400 dark:text-gray-500 mx-2\">%s</span>\n", escHTML(label))
					} else {
						fmt.Fprintf(&b, "      <a href=\"/preview?view=resource&resource=%s\" class=\"block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-brand-primary/10 hover:text-brand-primary mx-2 rounded-md\">%s</a>\n", escAttr(item.Resource), escHTML(label))
					}
				} else if item.Page != "" {
					label := item.Page
					if item.Label != "" {
						label = item.Label
					}
					if embed {
						fmt.Fprintf(&b, "      <span class=\"block px-4 py-2 text-sm text-gray-400 dark:text-gray-500 mx-2\">%s</span>\n", escHTML(label))
					} else {
						fmt.Fprintf(&b, "      <a href=\"/preview?view=page&page=%s\" class=\"block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-brand-primary/10 hover:text-brand-primary mx-2 rounded-md\">%s</a>\n", escAttr(item.Page), escHTML(label))
					}
				} else if item.Type == "link" {
					target := ""
					if item.OpensInNewTab {
						target = ` target="_blank"`
					}
					label := item.URL
					if item.Label != "" {
						label = item.Label
					}
					fmt.Fprintf(&b, "      <a href=\"%s\"%s class=\"block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-brand-primary/10 hover:text-brand-primary mx-2 rounded-md\">%s</a>\n", escAttr(item.URL), target, escHTML(label))
				}
			}
		}
	} else {
		for _, r := range cfg.Resources {
			if embed {
				fmt.Fprintf(&b, "      <span class=\"block px-4 py-2 text-sm text-gray-400 dark:text-gray-500 mx-2\">%s</span>\n", escHTML(r.Name))
			} else {
				fmt.Fprintf(&b, "      <a href=\"/preview?view=resource&resource=%s\" class=\"block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-brand-primary/10 hover:text-brand-primary mx-2 rounded-md\">%s</a>\n", escAttr(r.Name), escHTML(r.Name))
			}
		}
	}
	return b.String()
}

// pagePreviewBody renders one custom page's widget mock.
func pagePreviewBody(cfg *types.Config, pageName string, live bool) string {
	var page *types.Page
	for i := range cfg.Pages {
		if cfg.Pages[i].Name == pageName {
			page = &cfg.Pages[i]
			break
		}
	}
	var b strings.Builder
	b.WriteString("      <div class=\"p-6\">\n")
	if page == nil {
		b.WriteString("        <p class=\"text-gray-500 dark:text-gray-400\">Select a page from the Preview tab toolbar.</p>\n")
		b.WriteString("      </div>\n")
		return b.String()
	}
	fmt.Fprintf(&b, "        <h1 class=\"text-2xl font-bold mb-6 text-gray-900 dark:text-gray-100\">%s</h1>\n", escHTML(page.Name))
	for _, w := range page.Widgets {
		b.WriteString(previewWidget(w, live, cfg))
	}
	b.WriteString("      </div>\n")
	return b.String()
}

// previewWidget renders one widget card mirroring the generated widget templ,
// with deterministic fake data in place of DB results unless live is true.
func previewWidget(w types.Widget, live bool, cfg *types.Config) string {
	switch w.Type {
	case "stats_grid":
		cols := w.Columns
		if cols <= 0 || cols > 12 {
			cols = 4
		}
		var b strings.Builder
		fmt.Fprintf(&b, "      <div class=\"grid grid-cols-1 md:grid-cols-2 lg:grid-cols-%d gap-4 mb-6\">\n", cols)
		if len(w.Widgets) == 0 {
			b.WriteString("        <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg p-6\"><p class=\"text-sm text-gray-500 dark:text-gray-400\">Empty stats grid</p></div>\n")
		}
		for _, sw := range w.Widgets {
			b.WriteString(previewStat(sw, false, live, cfg))
		}
		b.WriteString("      </div>\n")
		return b.String()
	case "stat":
		return previewStat(w, true, live, cfg)
	case "chart":
		return previewChart(w, live, cfg)
	case "table":
		return previewTable(w, live, cfg)
	case "list":
		return previewList(w, live, cfg)
	default: // html and unknown
		return previewHTML()
	}
}

// previewStat renders a stat card; wrap controls the outer mb-6 so grid items
// (which carry their own gap) do not double the margin.
func previewStat(w types.Widget, wrap bool, live bool, cfg *types.Config) string {
	label := w.Label
	if label == "" {
		label = w.Type
	}
	icon := previewIconSVG(w.Icon)
	mb := ""
	if wrap {
		mb = " mb-6"
	}
	value := fakeNumber(label)
	liveNote := ""
	if live {
		if v, ok := statLiveValue(w, cfg); ok {
			value = fmt.Sprint(v)
			liveNote = ` <span class="text-xs text-gray-400 dark:text-gray-500 ml-2">live</span>`
		} else {
			liveNote = ` <span class="text-xs text-amber-400 dark:text-amber-500 ml-2">(no live data)</span>`
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "      <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg p-6%s\">\n", mb)
	b.WriteString("        <div class=\"flex items-center justify-between\">\n")
	b.WriteString("          <div>\n")
	if w.Icon != "" {
		b.WriteString("            <div class=\"w-10 h-10 rounded-lg bg-brand-primary/10 flex items-center justify-center mb-3\">\n")
		b.WriteString(icon)
		b.WriteString("            </div>\n")
	}
	fmt.Fprintf(&b, "            <p class=\"text-sm text-gray-500 dark:text-gray-400\">%s</p>\n", escHTML(label))
	b.WriteString("            <p class=\"text-2xl font-bold text-gray-900 dark:text-gray-100\">\n")
	if w.Prefix != "" {
		fmt.Fprintf(&b, "              <span class=\"text-lg\">%s</span>\n", escHTML(w.Prefix))
	}
	fmt.Fprintf(&b, "              %s%s\n", escHTML(value), liveNote)
	b.WriteString("            </p>\n")
	b.WriteString("          </div>\n")
	b.WriteString("        </div>\n")
	b.WriteString("      </div>\n")
	return b.String()
}

// statLiveValue runs a stat widget's query against the live DB and returns the
// first column of the first row, if available.
func statLiveValue(w types.Widget, cfg *types.Config) (interface{}, bool) {
	dsn, driver := sLiveConnection(cfg)
	if dsn == "" || w.Query == "" {
		return nil, false
	}
	rows, err := queryLive(driver, dsn, w.Query)
	if err != nil || len(rows) == 0 {
		return nil, false
	}
	for _, v := range rows[0] {
		return v, true
	}
	return nil, false
}

// previewChart renders a chart card with live or fake labels/values consumed by
// the shared Chart.js auto-render script.
func previewChart(w types.Widget, live bool, cfg *types.Config) string {
	label := w.Label
	if label == "" {
		label = "Chart"
	}
	chartType := "line"
	if w.Chart != nil && w.Chart.Type != "" {
		chartType = w.Chart.Type
	}

	var labels []string
	var values []int
	if live && w.Chart != nil && w.Chart.Query != "" {
		dsn, driver := sLiveConnection(cfg)
		if dsn != "" {
			rows, err := queryLive(driver, dsn, w.Chart.Query)
			if err == nil && len(rows) > 0 {
				for _, row := range rows {
					var x string
					var y int
					for col, val := range row {
						if strings.EqualFold(col, w.Chart.X) || (w.Chart.X == "" && len(labels) == 0) {
							x = fmt.Sprint(val)
						}
						if strings.EqualFold(col, w.Chart.Y) || (w.Chart.Y == "" && len(values) == 0) {
							switch v := val.(type) {
							case float64:
								y = int(v)
							case int:
								y = v
							case int64:
								y = int(v)
							case string:
								if n, err := strconv.Atoi(v); err == nil {
									y = n
								}
							}
						}
					}
					if x != "" {
						labels = append(labels, x)
						values = append(values, y)
					}
				}
			}
		}
	}
	if len(labels) == 0 {
		labels = fakeLabels(label)
		values = fakeValues(label)
	}
	labelsJSON, _ := json.Marshal(labels)
	valuesJSON, _ := json.Marshal(values)
	var b strings.Builder
	b.WriteString("      <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg p-6 mb-6\">\n")
	fmt.Fprintf(&b, "        <h3 class=\"text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100\">%s</h3>\n", escHTML(label))
	fmt.Fprintf(&b, "        <canvas id=\"chart-%s\" class=\"w-full h-64\" data-chart-type=\"%s\" data-labels=\"%s\" data-values=\"%s\"></canvas>\n",
		escAttr(label), escAttr(chartType), escAttr(string(labelsJSON)), escAttr(string(valuesJSON)))
	b.WriteString("      </div>\n")
	return b.String()
}

// previewTable renders a table card with header cells from data_columns and
// live or fake rows.
func previewTable(w types.Widget, live bool, cfg *types.Config) string {
	label := w.Label
	if label == "" {
		label = "Table"
	}
	cols := w.DataColumns
	if len(cols) == 0 {
		cols = []string{"value"}
	}

	var rows [][]string
	if live {
		dsn, driver := sLiveConnection(cfg)
		if dsn != "" && w.Query != "" {
			r, err := queryLive(driver, dsn, w.Query)
			if err == nil {
				for _, row := range r {
					var line []string
					for _, c := range cols {
						line = append(line, fmt.Sprint(row[c]))
					}
					rows = append(rows, line)
				}
			}
		}
	}
	if len(rows) == 0 {
		rows = fakeTableRows(cols, 4)
	}

	var b strings.Builder
	b.WriteString("      <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden mb-6\">\n")
	b.WriteString("        <div class=\"px-6 py-4 border-b border-gray-200 dark:border-gray-700\">\n")
	fmt.Fprintf(&b, "          <h3 class=\"text-lg font-semibold text-gray-900 dark:text-gray-100\">%s</h3>\n", escHTML(label))
	b.WriteString("        </div>\n")
	b.WriteString("        <table class=\"min-w-full divide-y divide-gray-200 dark:divide-gray-700\">\n")
	b.WriteString("          <thead class=\"bg-gray-50 dark:bg-gray-900\">\n")
	b.WriteString("            <tr>\n")
	for _, c := range cols {
		fmt.Fprintf(&b, "              <th class=\"px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider\">%s</th>\n", escHTML(c))
	}
	b.WriteString("            </tr>\n")
	b.WriteString("          </thead>\n")
	b.WriteString("          <tbody class=\"bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700\">\n")
	for i, row := range rows {
		b.WriteString("            <tr class=\"hover:bg-gray-50 dark:hover:bg-gray-700/50\">\n")
		for _, cell := range row {
			fmt.Fprintf(&b, "              <td class=\"px-6 py-4 whitespace-nowrap text-sm text-gray-900 dark:text-gray-100\">%s</td>\n", escHTML(cell))
		}
		b.WriteString("            </tr>\n")
		_ = i
	}
	b.WriteString("          </tbody>\n")
	b.WriteString("        </table>\n")
	b.WriteString("      </div>\n")
	return b.String()
}

// fakeTableRows generates a few fake rows for a table widget.
func fakeTableRows(cols []string, n int) [][]string {
	var out [][]string
	for i := 1; i <= n; i++ {
		var row []string
		for ci, c := range cols {
			row = append(row, fakeCell(c, i, ci))
		}
		out = append(out, row)
	}
	return out
}

// previewList renders a list widget card (label/value rows).
func previewList(w types.Widget, live bool, cfg *types.Config) string {
	label := w.Label
	if label == "" {
		label = "List"
	}

	var rows [][]string
	if live {
		dsn, driver := sLiveConnection(cfg)
		if dsn != "" && w.Query != "" {
			r, err := queryLive(driver, dsn, w.Query)
			if err == nil {
				for _, row := range r {
					var line []string
					if len(row) >= 2 {
						// First two columns are label/value.
						keys := make([]string, 0, len(row))
						for k := range row {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						line = append(line, fmt.Sprint(row[keys[0]]), fmt.Sprint(row[keys[1]]))
					} else if len(row) == 1 {
						keys := make([]string, 0, len(row))
						for k := range row {
							keys = append(keys, k)
						}
						line = append(line, fmt.Sprint(row[keys[0]]), "")
					}
					rows = append(rows, line)
				}
			}
		}
	}
	if len(rows) == 0 {
		for i := 1; i <= 5; i++ {
			rows = append(rows, []string{fakeWords(i), fakeNumber(label + "row" + fmt.Sprint(i))})
		}
	}

	var b strings.Builder
	b.WriteString("      <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg p-6 mb-6\">\n")
	fmt.Fprintf(&b, "        <h3 class=\"text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100\">%s</h3>\n", escHTML(label))
	b.WriteString("        <ul class=\"divide-y divide-gray-200 dark:divide-gray-700\">\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "          <li class=\"py-3 flex items-center justify-between\"><span class=\"text-sm text-gray-900 dark:text-gray-100\">%s</span><span class=\"text-sm text-gray-500 dark:text-gray-400\">%s</span></li>\n",
			escHTML(row[0]), escHTML(row[1]))
	}
	b.WriteString("        </ul>\n")
	b.WriteString("      </div>\n")
	return b.String()
}

// previewHTML renders a placeholder note for raw HTML widgets (content comes
// from the database at runtime, so there is nothing real to show in preview).
func previewHTML() string {
	return `      <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6 mb-6 text-gray-900 dark:text-gray-100">
        <p class="text-sm text-gray-500 dark:text-gray-400">Raw HTML widget — content is fetched from the database at runtime and is not shown in preview.</p>
      </div>
`
}

// resourcePreviewBody renders a mock of a resource list view: search bar,
// a table over the list columns with live or fake rows, and action buttons.
func resourcePreviewBody(cfg *types.Config, resName string, live bool) string {
	var res *types.Resource
	for i := range cfg.Resources {
		if cfg.Resources[i].Name == resName {
			res = &cfg.Resources[i]
			break
		}
	}
	var b strings.Builder
	b.WriteString("      <div class=\"p-6\">\n")
	if res == nil {
		b.WriteString("        <p class=\"text-gray-500 dark:text-gray-400\">Select a resource from the Preview tab toolbar.</p>\n")
		b.WriteString("      </div>\n")
		return b.String()
	}
	title := res.Label
	if title == "" {
		title = res.Name
	}
	cols := resourceListColumns(res)
	idCol := idColumn(*res)
	table := tableName(*res)

	var rows []map[string]interface{}
	liveNote := ""
	liveAttempted := false
	if live {
		dsn, driver := sLiveConnection(cfg)
		liveAttempted = dsn != ""
		if dsn == "" {
			liveNote = "No live database configured — showing sample data."
		} else {
			r, err := queryLive(driver, dsn, previewSelect(driver, "*", "FROM "+previewIdent(driver, table), 8))
			if err != nil {
				liveNote = "Live query failed: " + err.Error() + " — showing sample data."
			} else {
				rows = r
			}
		}
	}
	if liveAttempted && len(rows) == 0 && liveNote == "" {
		liveNote = "No live data for this resource!"
	}
	usingLive := len(rows) > 0
	if len(rows) == 0 {
		rows = fakeResourceRows(cols, 4, idCol)
	}

	fmt.Fprintf(&b, "        <div class=\"flex items-center justify-between mb-6\">\n")
	fmt.Fprintf(&b, "          <h1 class=\"text-2xl font-bold text-gray-900 dark:text-gray-100\">%s</h1>\n", escHTML(title))
	b.WriteString("          <a href=\"#\" class=\"bg-brand-primary text-white px-4 py-2 rounded-md text-sm hover:opacity-90\">+ Add</a>\n")
	b.WriteString("        </div>\n")
	b.WriteString("        <div class=\"mb-4\">\n")
	b.WriteString("          <form method=\"GET\" class=\"flex gap-2\">\n")
	b.WriteString("            <input type=\"text\" name=\"search\" placeholder=\"Search...\" class=\"w-64 rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2\" />\n")
	b.WriteString("            <button type=\"submit\" class=\"bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 px-4 py-2 rounded-md text-sm hover:bg-gray-200 dark:hover:bg-gray-600 border dark:border-gray-600\">Search</button>\n")
	b.WriteString("          </form>\n")
	b.WriteString("        </div>\n")
	if liveNote != "" {
		fmt.Fprintf(&b, "        <div class=\"mb-4 rounded-md border border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300\">%s</div>\n", escHTML(liveNote))
	}
	b.WriteString("        <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden mb-6\">\n")
	b.WriteString("          <table class=\"min-w-full divide-y divide-gray-200 dark:divide-gray-700\">\n")
	b.WriteString("            <thead class=\"bg-gray-50 dark:bg-gray-900\">\n")
	b.WriteString("              <tr>\n")
	for _, c := range cols {
		head := c.Label
		if head == "" {
			head = c.Name
		}
		fmt.Fprintf(&b, "                <th class=\"px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider\">%s</th>\n", escHTML(head))
	}
	b.WriteString("                <th class=\"px-6 py-3 text-right\"></th>\n")
	b.WriteString("              </tr>\n")
	b.WriteString("            </thead>\n")
	b.WriteString("            <tbody class=\"bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700\">\n")

	for _, row := range rows {
		idVal := ""
		if v, ok := row[idCol]; ok {
			idVal = fmt.Sprint(v)
		}
		b.WriteString("              <tr class=\"hover:bg-gray-50 dark:hover:bg-gray-700/50\">\n")
		for _, c := range cols {
			val := previewCellText(row[c.Name])
			// Live rows render their real value (empty stays empty); only fake
			// rows get type-based sample content.
			cell := val
			if !usingLive {
				cell = fakeTypedCellForVal(c.Type, val)
			}
			b.WriteString("                <td class=\"px-6 py-4 whitespace-nowrap text-sm text-gray-900 dark:text-gray-100\">" + escHTML(cell) + "</td>\n")
		}
		b.WriteString("                <td class=\"px-6 py-4 whitespace-nowrap text-sm text-right\">\n")
		b.WriteString("                  <a href=\"/preview?view=detail&resource=" + escAttr(res.Name) + "&id=" + escAttr(idVal) + previewLiveQS(live) + "\" class=\"text-brand-primary hover:text-brand-primary/80 underline mr-3\">View</a>\n")
		b.WriteString("                  <a href=\"/preview?view=form&resource=" + escAttr(res.Name) + "&mode=update&id=" + escAttr(idVal) + previewLiveQS(live) + "\" class=\"text-brand-primary hover:text-brand-primary/80 underline mr-3\">Edit</a>\n")
		b.WriteString("                  <a href=\"#\" class=\"text-red-600 hover:text-red-800 underline\">Delete</a>\n")
		// Action test-run buttons.
		for _, a := range res.Actions {
			btnCls := "bg-red-50 hover:bg-red-100 text-red-700 dark:bg-red-900/20 dark:text-red-400 dark:hover:bg-red-800"
			if a.RequiresConfirmation {
				btnCls += " border border-red-200 dark:border-red-800"
			}
			fmt.Fprintf(&b, "                  <button onclick=\"parent.testRunAction('%s','%s',%s)\" class=\"text-xs %s px-2 py-1 rounded border hover:opacity-80\">%s</button>\n",
				escAttr(res.Name), escAttr(a.Name), strconv.FormatInt(0, 10), btnCls, escHTML(a.Label))
		}
		b.WriteString("                </td>\n")
		b.WriteString("              </tr>\n")
	}
	b.WriteString("            </tbody>\n")
	b.WriteString("          </table>\n")
	b.WriteString("          <div class=\"bg-white dark:bg-gray-800 px-4 py-3 flex items-center justify-between border-t dark:border-gray-700\">\n")
	b.WriteString("            <div class=\"text-sm text-gray-700 dark:text-gray-300\">Showing page 1 of 2 (12 total)</div>\n")
	b.WriteString("            <div class=\"flex gap-1\">\n")
	b.WriteString("              <span class=\"px-3 py-1 border rounded text-sm bg-brand-primary text-white\">1</span>\n")
	b.WriteString("              <a href=\"#\" class=\"px-3 py-1 border rounded text-sm hover:bg-gray-50 dark:hover:bg-gray-700 dark:border-gray-600\">2</a>\n")
	b.WriteString("              <a href=\"#\" class=\"px-3 py-1 border rounded text-sm hover:bg-gray-50 dark:hover:bg-gray-700 dark:border-gray-600\">Next</a>\n")
	b.WriteString("            </div>\n")
	b.WriteString("          </div>\n")
	b.WriteString("        </div>\n")
	b.WriteString("      </div>\n")
	return b.String()
}

// isMSSQLDriver reports whether the preview must emit T-SQL syntax.
func isMSSQLDriver(driver string) bool {
	return driver == "mssql" || driver == "sqlserver"
}

// previewIdent quotes an identifier for a preview query: brackets on MSSQL,
// double quotes elsewhere. The preview runs against the real database, so the
// syntax has to match the driver or every live query fails.
func previewIdent(driver, name string) string {
	if isMSSQLDriver(driver) {
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// previewSelect builds a `SELECT <list> <from/where>` query limited to n rows
// using the driver's syntax: `SELECT TOP n` on MSSQL, a trailing `LIMIT n`
// elsewhere. MSSQL has no LIMIT clause, which silently broke live preview data.
func previewSelect(driver, selectList, fromWhere string, n int) string {
	if isMSSQLDriver(driver) {
		return "SELECT TOP " + strconv.Itoa(n) + " " + selectList + " " + fromWhere
	}
	return "SELECT " + selectList + " " + fromWhere + " LIMIT " + strconv.Itoa(n)
}

// previewLiteral renders a value for inline use in a preview query: integers
// pass through, anything else is single-quoted with `”` escaping.
func previewLiteral(v string) string {
	if _, err := strconv.ParseInt(v, 10, 64); err == nil {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// tableName mirrors the generator's tableName(): explicit override or
// lowercase name + "s".
func tableName(r types.Resource) string {
	if r.Table != "" {
		return r.Table
	}
	return strings.ToLower(r.Name) + "s"
}

// idColumn mirrors the generator's idColumn(): explicit override or "id".
func idColumn(r types.Resource) string {
	if r.IDColumn != "" {
		return r.IDColumn
	}
	return "id"
}

// resourceListColumns returns the list columns of a resource, falling back to
// a single id column when none are declared.
func resourceListColumns(res *types.Resource) []types.Column {
	if res != nil && res.List != nil && len(res.List.Columns) > 0 {
		return res.List.Columns
	}
	return []types.Column{{Name: "id", Type: "integer"}}
}

// fakeResourceRows generates fake rows for resource list preview.
func fakeResourceRows(cols []types.Column, n int, idCol string) []map[string]interface{} {
	var out []map[string]interface{}
	for i := 1; i <= n; i++ {
		row := make(map[string]interface{})
		row[idCol] = i
		for _, c := range cols {
			if c.Name == idCol {
				continue
			}
			row[c.Name] = fakeTypedCell(c.Type, i)
		}
		out = append(out, row)
	}
	return out
}

// previewLiveQS keeps live data enabled when navigating within the same
// resource (list -> detail -> edit). Switching resource (sidebar or toolbar)
// deliberately drops it, so live data is always an explicit per-resource choice.
func previewLiveQS(live bool) string {
	if live {
		return "&live=1"
	}
	return ""
}

// previewCellText renders a scanned database value as text: nil (SQL NULL)
// becomes an empty string, []byte is decoded, everything else uses fmt.
func previewCellText(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// fakeTypedCellForVal renders a cell: if val is non-empty use it, otherwise
// fall back to a fake value for the type.
func fakeTypedCellForVal(t string, val string) string {
	if val != "" {
		return val
	}
	return fakeTypedCell(t, 1)
}

// detailPreviewBody renders a resource detail view: the detail fields with
// live data for the given row id (or first row when id is empty).
func detailPreviewBody(cfg *types.Config, resName, id string, live bool) string {
	var res *types.Resource
	for i := range cfg.Resources {
		if cfg.Resources[i].Name == resName {
			res = &cfg.Resources[i]
			break
		}
	}
	var b strings.Builder
	b.WriteString("      <div class=\"p-6\">\n")
	if res == nil {
		b.WriteString("        <p class=\"text-gray-500 dark:text-gray-400\">Select a resource from the Preview tab toolbar.</p>\n")
		b.WriteString("      </div>\n")
		return b.String()
	}
	title := res.Label
	if title == "" {
		title = res.Name
	}

	idCol := idColumn(*res)
	table := tableName(*res)
	rowID := id
	liveNote := ""
	liveAttempted := false
	if live {
		dsn, driver := sLiveConnection(cfg)
		liveAttempted = dsn != ""
		if dsn == "" {
			liveNote = "No live database configured — showing sample data."
		} else {
			if rowID == "" {
				// Fetch the first row's id.
				rows, err := queryLive(driver, dsn, previewSelect(driver, previewIdent(driver, idCol), "FROM "+previewIdent(driver, table), 1))
				if err != nil {
					liveNote = "Live query failed: " + err.Error() + " — showing sample data."
				} else if len(rows) > 0 {
					if v, ok := rows[0][idCol]; ok {
						rowID = fmt.Sprint(v)
					}
				} else {
					liveNote = "No live data for this resource!"
				}
			}
		}
	}

	// The header links are written after rowID resolves so "Edit" targets the
	// row actually being shown (live detail without ?id= uses the first row).
	fmt.Fprintf(&b, "        <div class=\"flex items-center justify-between mb-6\">\n")
	fmt.Fprintf(&b, "          <h1 class=\"text-2xl font-bold text-gray-900 dark:text-gray-100\">%s</h1>\n", escHTML(title))
	fmt.Fprintf(&b, "          <a href=\"/preview?view=form&resource=%s&mode=update&id=%s%s\" class=\"bg-brand-primary text-white px-4 py-2 rounded-md text-sm hover:opacity-90\">Edit</a>\n", escAttr(res.Name), escAttr(rowID), previewLiveQS(live))
	fmt.Fprintf(&b, "          <a href=\"/preview?view=resource&resource=%s%s\" class=\"text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 text-sm\">← Back to list</a>\n", escAttr(res.Name), previewLiveQS(live))
	fmt.Fprintf(&b, "        </div>\n")

	var row map[string]interface{}
	if rowID != "" && live && liveNote == "" {
		dsn, driver := sLiveConnection(cfg)
		if dsn != "" {
			q := previewSelect(driver, "*", "FROM "+previewIdent(driver, table)+" WHERE "+previewIdent(driver, idCol)+" = "+previewLiteral(rowID), 1)
			r, err := queryLive(driver, dsn, q)
			if err != nil {
				liveNote = "Live query failed: " + err.Error() + " — showing sample data."
			} else if len(r) > 0 {
				row = r[0]
			} else if liveAttempted {
				liveNote = "No live data for this resource!"
			}
		}
	}
	if row == nil {
		row = fakeDetailRow(res, idCol)
		if rowID == "" {
			if v, ok := row[idCol]; ok {
				rowID = fmt.Sprint(v)
			}
		}
	}

	if liveNote != "" {
		fmt.Fprintf(&b, "        <div class=\"mb-4 rounded-md border border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300\">%s</div>\n", escHTML(liveNote))
	}
	b.WriteString("        <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden mb-6\">\n")
	b.WriteString("          <table class=\"min-w-full divide-y divide-gray-200 dark:divide-gray-700\">\n")
	b.WriteString("            <tbody class=\"bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700\">\n")
	if res.Detail != nil && len(res.Detail.Fields) > 0 {
		for _, f := range res.Detail.Fields {
			val := ""
			if row != nil {
				if v, ok := row[f.Name]; ok {
					val = fmt.Sprint(v)
				}
			}
			b.WriteString("              <tr>\n")
			fmt.Fprintf(&b, "                <td class=\"px-6 py-4 w-1/3 text-sm font-medium text-gray-500 dark:text-gray-400\">%s</td>\n", escHTML(f.Label))
			fmt.Fprintf(&b, "                <td class=\"px-6 py-4 w-2/3 text-sm text-gray-900 dark:text-gray-100 whitespace-nowrap\">%s</td>\n", escHTML(formatDetailValue(f.Type, val)))
			b.WriteString("              </tr>\n")
		}
	} else {
		// Fallback: show all row columns.
		for k, v := range row {
			b.WriteString("              <tr>\n")
			fmt.Fprintf(&b, "                <td class=\"px-6 py-4 w-1/3 text-sm font-medium text-gray-500 dark:text-gray-400\">%s</td>\n", escHTML(k))
			fmt.Fprintf(&b, "                <td class=\"px-6 py-4 w-2/3 text-sm text-gray-900 dark:text-gray-100 whitespace-nowrap\">%s</td>\n", escHTML(fmt.Sprint(v)))
			b.WriteString("              </tr>\n")
		}
	}
	b.WriteString("            </tbody>\n")
	b.WriteString("          </table>\n")
	b.WriteString("        </div>\n")
	b.WriteString("      </div>\n")
	return b.String()
}

// fakeDetailRow generates a fake row for detail preview.
func fakeDetailRow(res *types.Resource, idCol string) map[string]interface{} {
	row := make(map[string]interface{})
	row[idCol] = 1
	if res.Detail != nil {
		for _, f := range res.Detail.Fields {
			row[f.Name] = fakeTypedCell(f.Type, 1)
		}
	}
	return row
}

// formatDetailValue formats a detail field value for display.
func formatDetailValue(t string, val string) string {
	if val == "" || val == "<nil>" {
		return ""
	}
	switch t {
	case "boolean":
		if val == "true" || val == "1" {
			return `<span class="text-green-600 dark:text-green-400"><svg class="w-5 h-5 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" /></svg></span>`
		}
		return `<span class="text-red-600 dark:text-red-400"><svg class="w-5 h-5 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg></span>`
	case "email":
		return `<a href="mailto:` + escHTML(val) + `" class="text-brand-primary hover:text-brand-primary/80 underline">` + escHTML(val) + `</a>`
	case "image":
		return `<span class="inline-block w-10 h-10 rounded-full bg-gray-200 dark:bg-gray-700"></span>`
	default:
		return escHTML(val)
	}
}

// formPreviewBody renders a create or update form for a resource.
func formPreviewBody(cfg *types.Config, resName, mode, id string, live bool) string {
	var res *types.Resource
	for i := range cfg.Resources {
		if cfg.Resources[i].Name == resName {
			res = &cfg.Resources[i]
			break
		}
	}
	var b strings.Builder
	b.WriteString("      <div class=\"p-6\">\n")
	if res == nil {
		b.WriteString("        <p class=\"text-gray-500 dark:text-gray-400\">Select a resource from the Preview tab toolbar.</p>\n")
		b.WriteString("      </div>\n")
		return b.String()
	}
	title := res.Label
	if title == "" {
		title = res.Name
	}
	pageTitle := "Create " + title
	submitLabel := "Create"
	if mode == formModeUpdate {
		pageTitle = "Edit " + title
		submitLabel = "Update"
	}
	fmt.Fprintf(&b, "        <h1 class=\"text-2xl font-bold mb-6 text-gray-900 dark:text-gray-100\">%s</h1>\n", escHTML(pageTitle))

	// Gather fields: create or update form, or union.
	var fields []types.Field
	if mode == formModeCreate {
		if res.Form != nil && res.Form.Create != nil {
			fields = res.Form.Create.Fields
		}
	} else {
		// Update: union of create + update fields (create first, then update-only).
		seen := map[string]bool{}
		if res.Form != nil && res.Form.Create != nil {
			for _, f := range res.Form.Create.Fields {
				fields = append(fields, f)
				seen[f.Name] = true
			}
		}
		if res.Form != nil && res.Form.Update != nil {
			for _, f := range res.Form.Update.Fields {
				if !seen[f.Name] {
					fields = append(fields, f)
					seen[f.Name] = true
				}
			}
		}
	}

	// Load row data for update mode.
	idCol := idColumn(*res)
	table := tableName(*res)
	var row map[string]interface{}
	if mode == formModeUpdate && id != "" && live {
		dsn, driver := sLiveConnection(cfg)
		if dsn != "" {
			q := previewSelect(driver, "*", "FROM "+previewIdent(driver, table)+" WHERE "+previewIdent(driver, idCol)+" = "+previewLiteral(id), 1)
			r, err := queryLive(driver, dsn, q)
			if err == nil && len(r) > 0 {
				row = r[0]
			}
		}
	}

	hasFile := false
	for _, f := range fields {
		if f.Type == "file" || f.Type == "image" {
			hasFile = true
		}
	}

	enctype := ""
	if hasFile {
		enctype = ` enctype="multipart/form-data"`
	}

	fmt.Fprintf(&b, "        <form method=\"POST\" action=\"#\"%s class=\"space-y-4\">\n", enctype)
	b.WriteString("          <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden\">\n")
	b.WriteString("            <div class=\"px-6 py-4 border-b border-gray-200 dark:border-gray-700\">\n")
	fmt.Fprintf(&b, "              <h3 class=\"text-lg font-semibold text-gray-900 dark:text-gray-100\">%s</h3>\n", escHTML(pageTitle))
	b.WriteString("            </div>\n")
	b.WriteString("            <div class=\"px-6 py-4\">\n")
	b.WriteString("              <div class=\"grid grid-cols-1 md:grid-cols-2 gap-6\">\n")
	for _, f := range fields {
		val := ""
		if row != nil {
			if v, ok := row[f.Name]; ok {
				val = fmt.Sprint(v)
			}
		}
		b.WriteString(renderFormField(f, val, live, cfg, res))
	}
	b.WriteString("              </div>\n")
	b.WriteString("            </div>\n")
	b.WriteString("            <div class=\"px-6 py-4 bg-gray-50 dark:bg-gray-800 border-t border-gray-200 dark:border-gray-700\">\n")
	b.WriteString("              <button type=\"submit\" class=\"bg-brand-primary text-white px-6 py-2 rounded-md text-sm font-medium hover:opacity-90\">" + escHTML(submitLabel) + "</button>\n")
	b.WriteString("              <a href=\"/preview?view=detail&resource=" + escAttr(res.Name) + "&id=" + escAttr(id) + "\" class=\"text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 ml-4 text-sm\">Cancel</a>\n")
	b.WriteString("            </div>\n")
	b.WriteString("          </div>\n")
	b.WriteString("        </form>\n")
	b.WriteString("      </div>\n")
	return b.String()
}

// renderFormField renders one form field as HTML: label + input/select/textarea.
func renderFormField(f types.Field, val string, live bool, cfg *types.Config, res *types.Resource) string {
	label := f.Label
	if label == "" {
		label = f.Name
	}
	required := ""
	if f.Required {
		required = ` <span class="text-red-500">*</span>`
	}
	disabled := ""
	for _, v := range f.Visible {
		if v == "create" || v == "update" {
			// visible in this context
		}
	}

	var input string
	switch f.Type {
	case "password":
		input = `<input type="password" name="` + escAttr(f.Name) + `" class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:ring-brand-primary focus:border-brand-primary" />`
	case "textarea", "text", "string", "email", "integer", "float", "datetime", "date", "json":
		inputType := "text"
		if f.Type == "email" {
			inputType = "email"
		} else if f.Type == "integer" || f.Type == "float" {
			inputType = "number"
		} else if f.Type == "datetime" {
			inputType = "datetime-local"
		} else if f.Type == "date" {
			inputType = "date"
		}
		input = `<input type="` + inputType + `" name="` + escAttr(f.Name) + `" value="` + escAttr(val) + `" class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:ring-brand-primary focus:border-brand-primary" />`
	case "boolean":
		checked := ""
		if val == "true" || val == "1" {
			checked = ` checked`
		}
		input = `<input type="checkbox" name="` + escAttr(f.Name) + `" value="true"` + checked + ` class="w-4 h-4 text-brand-primary focus:ring-brand-primary border-gray-300 dark:border-gray-600 dark:bg-gray-700 rounded" />`
	case "select", "relation":
		input = renderFieldPicker(f, val, live, cfg, res)
	case "file", "image":
		input = `<input type="file" name="` + escAttr(f.Name) + `" class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:ring-brand-primary focus:border-brand-primary" />`
	default:
		input = `<input type="text" name="` + escAttr(f.Name) + `" value="` + escAttr(val) + `" class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:ring-brand-primary focus:border-brand-primary" />`
	}

	_ = required
	_ = disabled

	var b strings.Builder
	fmt.Fprintf(&b, "                <div>\n")
	fmt.Fprintf(&b, "                  <label class=\"block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1\">%s%s</label>\n", escHTML(label), required)
	b.WriteString(input)
	if f.Type == "password" {
		fmt.Fprintf(&b, "                  <p class=\"text-xs text-gray-500 dark:text-gray-400 mt-1\">Leave blank to keep current password.</p>\n")
	}
	fmt.Fprintf(&b, "                </div>\n")
	return b.String()
}

// display input + Browse button (modal record picker), mirroring the generated
// form's picker markup.
func renderFieldPicker(f types.Field, val string, live bool, cfg *types.Config, res *types.Resource) string {
	opts := resolveFieldOptions(f, val, live, cfg, res)
	display := val
	for _, o := range opts {
		if o.Value == val {
			display = o.Label
			break
		}
	}
	if display == "" {
		display = val
	}

	var b strings.Builder
	b.WriteString(`<input type="hidden" name="` + escAttr(f.Name) + `" value="` + escAttr(val) + `">`)
	fmt.Fprintf(&b, `<input type="text" readonly value="%s" class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md focus:ring-brand-primary focus:border-brand-primary cursor-not-allowed opacity-70" />`, escAttr(display))
	fmt.Fprintf(&b, `<button type="button" onclick="openPicker('%s','%s')" class="px-3 py-2 border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md text-sm hover:bg-gray-100 dark:hover:bg-gray-600 ml-2">Browse</button>`, escAttr(f.Name), escAttr(f.Name))
	b.WriteString(`<div id="picker-` + escAttr(f.Name) + `" class="hidden fixed inset-0 z-50 flex items-center justify-center">
  <div class="fixed inset-0 bg-black opacity-50"></div>
  <div class="bg-white dark:bg-gray-800 shadow-xl rounded-lg w-full max-w-2xl p-6 relative">
    <div class="flex justify-between items-center mb-4">
      <h3 class="text-lg font-semibold text-gray-900 dark:text-gray-100">Select ` + escHTML(f.Label) + `</h3>
      <button onclick="closePicker('` + escAttr(f.Name) + `')" class="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200">&times;</button>
    </div>
    <input type="text" placeholder="Search..." id="picker-search-` + escAttr(f.Name) + `" class="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 rounded-md mb-4" />
    <div id="picker-list-` + escAttr(f.Name) + `" class="max-h-60 overflow-y-auto divide-y divide-gray-200 dark:divide-gray-700"></div>
  </div>
</div>`)
	return b.String()
}

// pickerOption is one option for a picker field.
type pickerOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// resolveFieldOptions returns the options for a picker field, from live DB or
// static options.
func resolveFieldOptions(f types.Field, curVal string, live bool, cfg *types.Config, res *types.Resource) []pickerOption {
	var out []pickerOption
	if f.Options != nil {
		for k, v := range f.Options {
			out = append(out, pickerOption{Value: k, Label: v})
		}
		return out
	}
	sql := f.OptionsSQL
	if sql == "" {
		sql = f.OptionsQuery
	}
	if sql != "" && live {
		dsn, driver := sLiveConnection(cfg)
		if dsn != "" {
			rows, err := queryLive(driver, dsn, "SELECT "+sql+" AS _opt")
			// The OptionsSQL is expected to be "value, label FROM table" form.
			// For preview simplicity, run it directly and take first two columns.
			if err == nil {
				for _, row := range rows {
					keys := make([]string, 0, len(row))
					for k := range row {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					if len(keys) >= 2 {
						out = append(out, pickerOption{Value: fmt.Sprint(row[keys[0]]), Label: fmt.Sprint(row[keys[1]])})
					}
				}
				return out
			}
		}
	}
	// Fall back to static options if any.
	if len(out) == 0 && f.Options != nil {
		for k, v := range f.Options {
			out = append(out, pickerOption{Value: k, Label: v})
		}
	}
	return out
}

// cardPreviewBody renders a resource card view: cards in a grid (or kanban
// columns when kanban_field is set).
func cardPreviewBody(cfg *types.Config, resName string, live bool) string {
	var res *types.Resource
	for i := range cfg.Resources {
		if cfg.Resources[i].Name == resName {
			res = &cfg.Resources[i]
			break
		}
	}
	var b strings.Builder
	b.WriteString("      <div class=\"p-6\">\n")
	if res == nil {
		b.WriteString("        <p class=\"text-gray-500 dark:text-gray-400\">Select a resource from the Preview tab toolbar.</p>\n")
		b.WriteString("      </div>\n")
		return b.String()
	}
	title := res.Label
	if title == "" {
		title = res.Name
	}
	fmt.Fprintf(&b, "        <h1 class=\"text-2xl font-bold mb-6 text-gray-900 dark:text-gray-100\">%s</h1>\n", escHTML(title))

	columns := 4
	rows := 2
	if res.Card != nil {
		columns = res.Card.Columns
		if columns <= 0 || columns > 12 {
			columns = 4
		}
		rows = res.Card.Rows
		if rows <= 0 {
			rows = 2
		}
	} else {
		fmt.Fprintf(&b, "        <div class=\"mb-4 rounded-md border border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300\">Card view not configured for this resource.</div>\n")
	}

	idCol := idColumn(*res)
	table := tableName(*res)
	var rowMaps []map[string]interface{}
	liveNote := ""
	liveAttempted := false
	if live {
		dsn, driver := sLiveConnection(cfg)
		liveAttempted = dsn != ""
		if dsn != "" && res.Card != nil {
			limit := columns * rows
			r, err := queryLive(driver, dsn, previewSelect(driver, "*", "FROM "+previewIdent(driver, table), limit))
			if err == nil {
				rowMaps = r
			}
		}
	}
	if liveAttempted && len(rowMaps) == 0 && liveNote == "" && res.Card != nil {
		liveNote = "No live data for this resource!"
	}
	if len(rowMaps) == 0 {
		var cardCols []types.Column
		if res.Card != nil {
			cardCols = cardFieldsAsColumns(res.Card)
		}
		rowMaps = fakeResourceRows(cardCols, columns*rows, idCol)
	}

	if liveNote != "" {
		fmt.Fprintf(&b, "        <div class=\"mb-4 rounded-md border border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-300\">%s</div>\n", escHTML(liveNote))
	}

	kanban := res.Card != nil && res.Card.KanbanField != ""
	if kanban {
		b.WriteString("        <div class=\"flex gap-4 overflow-x-auto pb-4\">\n")
		// Bucket keys from the kanban field values.
		buckets := map[string][]map[string]interface{}{}
		var order []string
		for _, rm := range rowMaps {
			key := ""
			if v, ok := rm[res.Card.KanbanField]; ok {
				key = fmt.Sprint(v)
			}
			if key == "" {
				key = "Uncategorized"
			}
			if _, exists := buckets[key]; !exists {
				order = append(order, key)
			}
			buckets[key] = append(buckets[key], rm)
		}
		for _, key := range order {
			fmt.Fprintf(&b, "          <div class=\"w-64 bg-gray-100 dark:bg-gray-900 rounded-lg p-4 flex flex-col gap-2 shrink-0\">\n")
			fmt.Fprintf(&b, "            <div class=\"text-sm font-semibold text-gray-900 dark:text-gray-100 mb-2 border-b border-gray-300 dark:border-gray-600 pb-1\">%s</div>\n", escHTML(key))
			for _, rm := range buckets[key] {
				b.WriteString(renderCard(res, rm, live, cfg))
			}
			b.WriteString("          </div>\n")
		}
		b.WriteString("        </div>\n")
	} else {
		fmt.Fprintf(&b, "        <div class=\"grid grid-cols-1 md:grid-cols-2 lg:grid-cols-%d gap-4\">\n", columns)
		for _, rm := range rowMaps {
			b.WriteString(renderCard(res, rm, live, cfg))
		}
		b.WriteString("        </div>\n")
	}
	b.WriteString("      </div>\n")
	return b.String()
}

// FieldsAsColumns returns card fields as a Column slice for fake row generation.
func cardFieldsAsColumns(c *types.CardConfig) []types.Column {
	var cols []types.Column
	for _, f := range c.Fields {
		cols = append(cols, types.Column{Name: f.Name, Type: f.Type, Label: f.Label})
	}
	return cols
}

// renderCard renders one card item.
func renderCard(res *types.Resource, row map[string]interface{}, live bool, cfg *types.Config) string {
	var b strings.Builder
	b.WriteString("          <div class=\"bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden\">\n")
	b.WriteString("            <div class=\"px-4 py-3\">\n")
	if res.Card != nil {
		for _, f := range res.Card.Fields {
			val := ""
			if row != nil {
				if v, ok := row[f.Name]; ok {
					val = fmt.Sprint(v)
				}
			}
			if f.Label != "" {
				fmt.Fprintf(&b, "              <div class=\"text-xs font-medium text-gray-500 dark:text-gray-400\">%s</div>\n", escHTML(f.Label))
			}
			fmt.Fprintf(&b, "              <div class=\"text-sm text-gray-900 dark:text-gray-100\">%s</div>\n", escHTML(formatDetailValue(f.Type, val)))
		}
	}
	b.WriteString("            </div>\n")
	b.WriteString("            <div class=\"px-4 py-3 border-t border-gray-200 dark:border-gray-700 flex gap-2\">\n")
	idVal := ""
	if row != nil {
		if v, ok := row[idColumn(*res)]; ok {
			idVal = fmt.Sprint(v)
		}
	}
	fmt.Fprintf(&b, "              <a href=\"/preview?view=detail&resource=%s&id=%s%s\" class=\"text-xs text-brand-primary hover:text-brand-primary/80 underline\">View</a>\n", escAttr(res.Name), escAttr(idVal), previewLiveQS(live))
	fmt.Fprintf(&b, "              <a href=\"/preview?view=form&resource=%s&mode=update&id=%s%s\" class=\"text-xs text-brand-primary hover:text-brand-primary/80 underline\">Edit</a>\n", escAttr(res.Name), escAttr(idVal), previewLiveQS(live))
	for _, a := range res.Actions {
		fmt.Fprintf(&b, "              <button onclick=\"parent.testRunAction('%s','%s',%s)\" class=\"text-xs text-red-600 hover:text-red-800 underline\">%s</button>\n",
			escAttr(res.Name), escAttr(a.Name), strconv.FormatInt(0, 10), escHTML(a.Name))
	}
	b.WriteString("            </div>\n")
	b.WriteString("          </div>\n")
	return b.String()
}

// --- Shared fake data helpers (unchanged) ---

func fakeTypedCell(t string, row int) string {
	switch t {
	case "integer", "float":
		return fakeNumber(t + fmt.Sprint(row))
	case "boolean":
		if row%2 == 0 {
			return `<span class="text-green-600"><svg class="w-5 h-5 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" /></svg></span>`
		}
		return `<span class="text-red-600"><svg class="w-5 h-5 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg></span>`
	case "datetime":
		return fmt.Sprintf("Jan %02d, 2026 14:%02d", row, 5+row)
	case "date":
		return fmt.Sprintf("2026-01-%02d", row)
	case "badge":
		return `<span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-800 dark:bg-gray-900/50 dark:text-gray-300">active</span>`
	case "email":
		return fmt.Sprintf("<a href=\"#\" class=\"text-brand-primary hover:text-brand-primary/80 underline\">user%d@example.com</a>", row)
	case "password":
		return "••••••••"
	case "image":
		return `<span class="inline-block w-10 h-10 rounded-full bg-gray-200 dark:bg-gray-700"></span>`
	case "json":
		return `{ "key": "value" }`
	default:
		return fakeWords(row)
	}
}

func fakeCell(col string, row int, idx int) string {
	switch {
	case strings.Contains(strings.ToLower(col), "id") || strings.Contains(strings.ToLower(col), "count"):
		return fakeNumber(col + fmt.Sprint(row))
	case strings.Contains(strings.ToLower(col), "date") || strings.Contains(strings.ToLower(col), "time"):
		return fmt.Sprintf("2026-0%d-%02d", idx+1, row)
	case strings.Contains(strings.ToLower(col), "total") || strings.Contains(strings.ToLower(col), "price") || strings.Contains(strings.ToLower(col), "amount"):
		return fmt.Sprintf("$%d.%02d", 1000+row*123, row*17)
	default:
		return fakeWords(row + idx)
	}
}

func fakeWords(seed int) string {
	words := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel"}
	extra := []string{" Lorem", " ipsum", " dolor", " sit", " amet", " consectetur"}
	w := words[seed%len(words)]
	if seed >= len(words) {
		w += extra[(seed/len(words))%len(extra)]
	}
	return w
}

func fakeNumber(seed string) string {
	h := 0
	for _, r := range seed {
		h = (h*31 + int(r)) & 0x7fffffff
	}
	v := 1200 + h%888000
	digits := fmt.Sprintf("%d", v)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
}

func fakeLabels(seed string) []string {
	base := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug"}
	n := 6
	if h := hashSeed(seed) % 3; h == 1 {
		n = 7
	} else if h == 2 {
		n = 8
	}
	out := make([]string, n)
	copy(out, base[:n])
	return out
}

func fakeValues(seed string) []int {
	n := len(fakeLabels(seed))
	out := make([]int, n)
	for i := 0; i < n; i++ {
		out[i] = 40 + (hashSeed(seed+fmt.Sprint(i)) % 56)
	}
	return out
}

func hashSeed(s string) int {
	h := 0
	for _, r := range s {
		h = (h*31 + int(r)) & 0x7fffffff
	}
	return h
}

func escHTML(s string) string { return html.EscapeString(s) }
func escAttr(s string) string { return html.EscapeString(s) }
func escCSS(s string) string  { return strings.ReplaceAll(s, "</", "<\\/") }

func navIconSVG(name string) string {
	p := iconPath(name)
	if p == "" {
		return ""
	}
	return `<svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="` + p + `"/></svg>`
}

func previewIconSVG(name string) string {
	p := iconPath(name)
	if p == "" {
		return ""
	}
	return `              <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="` + p + `" /></svg>
`
}

func iconPath(name string) string {
	switch name {
	case "users":
		return "M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"
	case "chart":
		return "M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"
	case "cog":
		return "M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0 3.35a1.724 1.724 0 001.066-2.573c-.94 1.543.826-3.31 2.37 2.37.996.608 2.572-1.065z"
	case "home":
		return "M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"
	case "clock":
		return "M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"
	case "mail":
		return "M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"
	case "dollar":
		return "M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
	case "check":
		return "M5 13l4 4L19 7"
	case "bell":
		return "M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"
	case "lock":
		return "M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z"
	default:
		return ""
	}
}
