package editor

import (
	"fmt"
	"sort"

	"github.com/MichalHerstus/yaga/internal/types"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// formShell builds a bordered form and adds a Back button.
func (e *Editor) formShell(title string, fill func(*tview.Form)) tview.Primitive {
	f := tview.NewForm()
	f.SetBorder(true).SetBorderColor(colBorder).SetTitle(title)
	f.SetLabelColor(colText)
	f.SetFieldBackgroundColor(tcell.NewHexColor(0x27272a))
	f.SetButtonBackgroundColor(colAccent)
	f.SetButtonTextColor(tcell.ColorWhite)
	fill(f)
	e.backButton(f)
	return f
}

// panelPage edits the panel identity/brand/layout/theme sections.
func (e *Editor) panelPage() tview.Primitive {
	p := &e.cfg.Panel
	return e.formShell("Panel", func(f *tview.Form) {
		e.str(f, "ID", p.ID, func(v string) { p.ID = v })
		e.str(f, "Name", p.Name, func(v string) { p.Name = v })
		e.str(f, "Path", p.Path, func(v string) { p.Path = v })
		e.addButton(f, "Brand", func() { e.showPage("Panel/Brand", e.brandPage()) })
		e.addButton(f, "Layout", func() { e.showPage("Panel/Layout", e.layoutPage()) })
		e.addButton(f, "Theme", func() { e.showPage("Panel/Theme", e.themePage()) })
	})
}

// brandPage edits the logo/favicon/brand colors.
func (e *Editor) brandPage() tview.Primitive {
	b := &e.cfg.Panel.Brand
	return e.formShell("Panel / Brand", func(f *tview.Form) {
		e.str(f, "Logo", b.Logo, func(v string) { b.Logo = v })
		e.str(f, "Favicon", b.Favicon, func(v string) { b.Favicon = v })
		e.str(f, "Color Primary", b.Colors.Primary, func(v string) { b.Colors.Primary = v })
		e.str(f, "Color Secondary", b.Colors.Secondary, func(v string) { b.Colors.Secondary = v })
	})
}

// layoutPage edits the sidebar/topbar/content layout.
func (e *Editor) layoutPage() tview.Primitive {
	l := &e.cfg.Panel.Layout
	return e.formShell("Panel / Layout", func(f *tview.Form) {
		e.yesno(f, "Sidebar collapsible", l.Sidebar.Collapsible, func(v bool) { l.Sidebar.Collapsible = v })
		e.num(f, "Sidebar width", l.Sidebar.Width, func(v int) { l.Sidebar.Width = v })
		e.num(f, "Sidebar collapsed width", l.Sidebar.CollapsedWidth, func(v int) { l.Sidebar.CollapsedWidth = v })
		e.yesno(f, "Topbar sticky", l.Topbar.Sticky, func(v bool) { l.Topbar.Sticky = v })
		e.str(f, "Max content width", l.MaxContentWidth, func(v string) { l.MaxContentWidth = v })
	})
}

// themePage edits dark mode and fonts.
func (e *Editor) themePage() tview.Primitive {
	t := &e.cfg.Panel.Theme
	return e.formShell("Panel / Theme", func(f *tview.Form) {
		e.yesno(f, "Dark mode", t.DarkMode, func(v bool) { t.DarkMode = v })
		e.str(f, "Font family", t.Font.Family, func(v string) { t.Font.Family = v })
		e.str(f, "Mono font", t.Font.Mono, func(v string) { t.Font.Mono = v })
	})
}

// connectionsPage manages the named database connections.
func (e *Editor) connectionsPage() tview.Primitive {
	names := func() []string {
		out := make([]string, 0, len(e.cfg.Connections))
		for n := range e.cfg.Connections {
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}
	spec := listSpec{
		title:  "Connections",
		labels: names,
		sub: func(i int) string {
			n := names()[i]
			c := e.cfg.Connections[n]
			return fmt.Sprintf("%s  %s", c.Driver, c.DSN)
		},
		add: nil,
		edit: func(i int) {
			n := names()[i]
			e.showPage("Connections/"+n, e.connectionPage(n))
		},
		remove: nil,
	}
	return e.recordList("Connections", spec)
}

// connectionPage edits a single named connection.
func (e *Editor) connectionPage(name string) tview.Primitive {
	c := e.cfg.Connections[name]
	set := func(mutate func(*types.Connection)) {
		mutate(&c)
		e.cfg.Connections[name] = c
		e.markModified()
	}
	return e.formShell("Connection: "+name, func(f *tview.Form) {
		e.pick(f, "Driver", driverOptions, c.Driver, func(v string) { set(func(x *types.Connection) { x.Driver = v }) })
		e.str(f, "DSN", c.DSN, func(v string) { set(func(x *types.Connection) { x.DSN = v }) })
		e.head(f, "Pool")
		e.num(f, "Max open", c.Pool.MaxOpen, func(v int) { set(func(x *types.Connection) { x.Pool.MaxOpen = v }) })
		e.num(f, "Max idle", c.Pool.MaxIdle, func(v int) { set(func(x *types.Connection) { x.Pool.MaxIdle = v }) })
		e.str(f, "Lifetime", c.Pool.Lifetime, func(v string) { set(func(x *types.Connection) { x.Pool.Lifetime = v }) })
	})
}

// authPage edits the authentication configuration.
func (e *Editor) authPage() tview.Primitive {
	a := &e.cfg.Auth
	return e.formShell("Auth", func(f *tview.Form) {
		e.pick(f, "Guard", authGuardOptions, a.Guard, func(v string) { a.Guard = v })
		e.pick(f, "Provider", authProviderOptions, a.Provider, func(v string) { a.Provider = v })
		e.str(f, "Table", a.Table, func(v string) { a.Table = v })
		e.str(f, "Login redirect", a.Login.Redirect, func(v string) { a.Login.Redirect = v })
		e.addButton(f, "Login fields", func() {
			e.showPage("Auth/Login Fields", e.tagsPage("Auth/Login Fields", "Auth / Login fields", loginFieldOptions, func() []string {
				return a.Login.Fields
			}, func(v []string) { a.Login.Fields = v }))
		})
		e.head(f, "Login rate limit")
		rl := a.Login.RateLimit
		e.yesno(f, "Enabled", rl != nil, func(v bool) {
			if v {
				if a.Login.RateLimit == nil {
					a.Login.RateLimit = &types.LoginRateLimit{MaxAttempts: 5, WindowSeconds: 300}
				}
			} else {
				a.Login.RateLimit = nil
			}
		})
		max, win := 0, 0
		if rl != nil {
			max, win = rl.MaxAttempts, rl.WindowSeconds
		}
		e.num(f, "Max attempts", max, func(v int) {
			if a.Login.RateLimit == nil {
				a.Login.RateLimit = &types.LoginRateLimit{}
			}
			a.Login.RateLimit.MaxAttempts = v
		})
		e.num(f, "Window seconds", win, func(v int) {
			if a.Login.RateLimit == nil {
				a.Login.RateLimit = &types.LoginRateLimit{}
			}
			a.Login.RateLimit.WindowSeconds = v
		})
	})
}

// navGroupsPage manages the sidebar navigation groups.
func (e *Editor) navGroupsPage() tview.Primitive {
	spec := listSpec{
		title: "Navigation groups",
		labels: func() []string {
			out := make([]string, len(e.cfg.Navigation))
			for i, g := range e.cfg.Navigation {
				label := g.Group
				if label == "" {
					label = "(untitled)"
				}
				out[i] = label
			}
			return out
		},
		sub: func(i int) string {
			g := e.cfg.Navigation[i]
			return fmt.Sprintf("%d items  sort=%d", len(g.Items), g.Sort)
		},
		add: func() {
			e.cfg.Navigation = append(e.cfg.Navigation, types.NavigationGroup{Group: "New group"})
			e.markModified()
		},
		edit: func(i int) {
			e.showPage(e.navGroupPath(i), e.navGroupPage(i))
		},
		remove: func(i int) {
			e.cfg.Navigation = append(e.cfg.Navigation[:i], e.cfg.Navigation[i+1:]...)
			e.markModified()
		},
	}
	return e.recordList("Navigation", spec)
}

// navGroupPage edits a group's meta and item list.
func (e *Editor) navGroupPage(idx int) tview.Primitive {
	g := &e.cfg.Navigation[idx]
	return e.formShell("Navigation group", func(f *tview.Form) {
		e.str(f, "Group", g.Group, func(v string) { g.Group = v })
		e.pick(f, "Icon", iconOptions, g.Icon, func(v string) { g.Icon = v })
		e.num(f, "Sort", g.Sort, func(v int) { g.Sort = v })
		e.addButton(f, "Items", func() {
			e.showPage(e.navGroupItemsPath(idx), e.navItemsPage(idx))
		})
	})
}

// navItemsPage manages a group's items.
func (e *Editor) navItemsPage(gidx int) tview.Primitive {
	g := &e.cfg.Navigation[gidx]
	spec := listSpec{
		title: "Navigation items",
		labels: func() []string {
			out := make([]string, len(g.Items))
			for i, it := range g.Items {
				label := it.Label
				if label == "" {
					label = it.Resource + it.Page + it.URL
				}
				out[i] = label
			}
			return out
		},
		sub: func(i int) string {
			it := g.Items[i]
			return it.Type + "  " + it.Resource + it.Page + it.URL
		},
		add: func() {
			g.Items = append(g.Items, types.NavigationItem{Type: "resource"})
			e.markModified()
		},
		edit: func(i int) {
			e.showPage(e.navItemPath(gidx, i), e.navItemPage(gidx, i))
		},
		remove: func(i int) {
			g.Items = append(g.Items[:i], g.Items[i+1:]...)
			e.markModified()
		},
	}
	return e.recordList(e.navGroupItemsPath(gidx), spec)
}

// navItemPage edits a single navigation item.
func (e *Editor) navItemPage(gidx, idx int) tview.Primitive {
	it := &e.cfg.Navigation[gidx].Items[idx]
	resourceNames := func() []string {
		out := make([]string, 0, len(e.cfg.Resources))
		for _, r := range e.cfg.Resources {
			out = append(out, r.Name)
		}
		return out
	}
	return e.formShell("Navigation item", func(f *tview.Form) {
		e.pick(f, "Type", navItemTypeOptions, it.Type, func(v string) { it.Type = v })
		e.str(f, "Label", it.Label, func(v string) { it.Label = v })
		if it.Type == "resource" {
			e.pick(f, "Resource", freeOptions(resourceNames()), it.Resource, func(v string) { it.Resource = v })
		} else if it.Type == "page" {
			pageNames := make([]Option, 0, len(e.cfg.Pages))
			for _, p := range e.cfg.Pages {
				pageNames = append(pageNames, Option{Label: p.Name, Value: p.Name})
			}
			e.pick(f, "Page", pageNames, it.Page, func(v string) { it.Page = v })
		} else {
			e.str(f, "URL", it.URL, func(v string) { it.URL = v })
			e.yesno(f, "Open in new tab", it.OpensInNewTab, func(v bool) { it.OpensInNewTab = v })
		}
	})
}

// pagesPage manages the dashboard pages.
func (e *Editor) pagesPage() tview.Primitive {
	spec := listSpec{
		title: "Pages",
		labels: func() []string {
			out := make([]string, len(e.cfg.Pages))
			for i, p := range e.cfg.Pages {
				out[i] = p.Name
			}
			return out
		},
		sub: func(i int) string {
			p := e.cfg.Pages[i]
			def := ""
			if p.Default {
				def = "  (default)"
			}
			return fmt.Sprintf("%s  %d widgets%s", p.Path, len(p.Widgets), def)
		},
		add: func() {
			e.cfg.Pages = append(e.cfg.Pages, types.Page{Name: "New page", Path: "/new-page"})
			e.markModified()
		},
		edit: func(i int) {
			e.showPage(e.pagePath(i), e.pagePage(i))
		},
		remove: func(i int) {
			e.cfg.Pages = append(e.cfg.Pages[:i], e.cfg.Pages[i+1:]...)
			e.markModified()
		},
	}
	return e.recordList("Pages", spec)
}

// pagePage edits a single page and its widgets.
func (e *Editor) pagePage(idx int) tview.Primitive {
	p := &e.cfg.Pages[idx]
	return e.formShell("Page: "+p.Name, func(f *tview.Form) {
		e.str(f, "Name", p.Name, func(v string) { p.Name = v })
		e.str(f, "Path", p.Path, func(v string) { p.Path = v })
		e.yesno(f, "Default", p.Default, func(v bool) { p.Default = v })
		e.addButton(f, "Widgets", func() {
			e.showPage(e.pageWidgetsPath(idx), e.widgetsPage(idx))
		})
	})
}

// widgetsPage manages a page's widgets.
func (e *Editor) widgetsPage(pidx int) tview.Primitive {
	p := &e.cfg.Pages[pidx]
	spec := listSpec{
		title: "Widgets",
		labels: func() []string {
			out := make([]string, len(p.Widgets))
			for i, w := range p.Widgets {
				out[i] = w.Label
			}
			return out
		},
		sub: func(i int) string {
			return p.Widgets[i].Type
		},
		add: func() {
			p.Widgets = append(p.Widgets, types.Widget{Type: "stat", Label: "New widget"})
			e.markModified()
		},
		edit: func(i int) {
			e.showPage(e.pageWidgetPath(pidx, i), e.widgetPage(pidx, i))
		},
		remove: func(i int) {
			p.Widgets = append(p.Widgets[:i], p.Widgets[i+1:]...)
			e.markModified()
		},
	}
	return e.recordList(e.pageWidgetsPath(pidx), spec)
}

// widgetPage edits a single widget by type.
func (e *Editor) widgetPage(pidx, idx int) tview.Primitive {
	w := &e.cfg.Pages[pidx].Widgets[idx]
	return e.formShell("Widget", func(f *tview.Form) {
		e.pick(f, "Type", widgetTypeOptions, w.Type, func(v string) {
			w.Type = v
			if v == "chart" && w.Chart == nil {
				w.Chart = &types.ChartConfig{Type: "line"}
			}
			if v != "stats_grid" && v != "chart" {
				w.Chart = nil
			}
		})
		e.str(f, "Label", w.Label, func(v string) { w.Label = v })
		switch w.Type {
		case "stat":
			e.str(f, "Query", w.Query, func(v string) { w.Query = v })
			e.pick(f, "Icon", iconOptions, w.Icon, func(v string) { w.Icon = v })
			e.pick(f, "Color", actionColorOptions, w.Color, func(v string) { w.Color = v })
			e.str(f, "Prefix", w.Prefix, func(v string) { w.Prefix = v })
		case "stats_grid":
			e.num(f, "Columns", w.Columns, func(v int) { w.Columns = v })
			e.addButton(f, "Sub-widgets", func() {
				e.showPage(e.pageSubWidgetsPath(pidx, idx), e.subWidgetsPage(pidx, idx))
			})
		case "chart":
			if w.Chart == nil {
				w.Chart = &types.ChartConfig{Type: "line"}
			}
			e.pick(f, "Chart type", chartTypeOptions, w.Chart.Type, func(v string) { w.Chart.Type = v })
			e.str(f, "Query", w.Chart.Query, func(v string) { w.Chart.Query = v })
			e.str(f, "X axis", w.Chart.X, func(v string) { w.Chart.X = v })
			e.str(f, "Y axis", w.Chart.Y, func(v string) { w.Chart.Y = v })
		case "table", "list":
			e.str(f, "Query", w.Query, func(v string) { w.Query = v })
			e.num(f, "Limit", w.Limit, func(v int) { w.Limit = v })
			e.addButton(f, "Data columns", func() {
				path := e.pageWidgetPath(pidx, idx) + "/Data Columns"
				e.showPage(path, e.stringListPage(path, "Data columns", func() []string {
					return w.DataColumns
				}, func(v []string) { w.DataColumns = v }))
			})
		case "html":
			e.str(f, "Query", w.Query, func(v string) { w.Query = v })
		}
	})
}

// subWidgetsPage manages the nested stat widgets of a stats_grid widget.
func (e *Editor) subWidgetsPage(pidx, idx int) tview.Primitive {
	parent := &e.cfg.Pages[pidx].Widgets[idx]
	spec := listSpec{
		title: "Sub-widgets",
		labels: func() []string {
			out := make([]string, len(parent.Widgets))
			for i, w := range parent.Widgets {
				out[i] = w.Label
			}
			return out
		},
		sub: func(i int) string {
			return parent.Widgets[i].Type
		},
		add: func() {
			parent.Widgets = append(parent.Widgets, types.Widget{Type: "stat", Label: "New widget"})
			e.markModified()
		},
		edit: func(i int) {
			e.showPage(e.pageSubWidgetPath(pidx, idx, i), e.subWidgetPage(pidx, idx, i))
		},
		remove: func(i int) {
			parent.Widgets = append(parent.Widgets[:i], parent.Widgets[i+1:]...)
			e.markModified()
		},
	}
	return e.recordList(e.pageSubWidgetsPath(pidx, idx), spec)
}

// subWidgetPage edits one nested stat widget.
func (e *Editor) subWidgetPage(pidx, widx, idx int) tview.Primitive {
	w := &e.cfg.Pages[pidx].Widgets[widx].Widgets[idx]
	return e.formShell("Sub-widget", func(f *tview.Form) {
		e.str(f, "Label", w.Label, func(v string) { w.Label = v })
		e.str(f, "Query", w.Query, func(v string) { w.Query = v })
		e.pick(f, "Icon", iconOptions, w.Icon, func(v string) { w.Icon = v })
		e.pick(f, "Color", actionColorOptions, w.Color, func(v string) { w.Color = v })
		e.str(f, "Prefix", w.Prefix, func(v string) { w.Prefix = v })
	})
}

// freeOptions turns arbitrary strings into self-labelled options.
func freeOptions(values []string) []Option {
	out := make([]Option, 0, len(values))
	for _, v := range values {
		out = append(out, Option{Label: v, Value: v})
	}
	return out
}

// namePrompt asks for a new collection member name and runs onDone.
func (e *Editor) namePrompt(title, label string, existing func() []string, onDone func(string)) {
	input := tview.NewInputField().SetLabel(label)
	form := e.promptForm(title, input, func(name string) {
		for _, ex := range existing() {
			if ex == name {
				e.toast("Name already exists")
				return
			}
		}
		if name == "" {
			e.toast("Name is required")
			return
		}
		onDone(name)
	})
	e.showModal(form)
}
