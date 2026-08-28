package editor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// cd-style navigation for the editor. Every screen has a canonical, unique,
// human-friendly path (also the key under which the page lives in e.pages and
// e.history). The cd dialog (Ctrl+P / Ctrl+>) resolves absolute ("~/Panel",
// "/Resources") and relative ("../Columns") paths against the current screen,
// with Tab autocompletion and a two-stage Esc (clear path, then close).

// navTarget is a screen resolved from a path: its canonical page name and a
// builder for the page primitive.
type navTarget struct {
	name  string
	build func() tview.Primitive
}

// navErr is returned when a path does not resolve to a screen.
func navErr(seg string) error {
	return fmt.Errorf("no such screen: %q", seg)
}

// segName returns the canonical path segment for the idx-th item of a
// collection: its own label when non-empty, else "#<idx>".
func segName(label string, idx int) string {
	if strings.TrimSpace(label) != "" {
		return label
	}
	return fmt.Sprintf("#%d", idx)
}

// foldSeg normalizes a segment for comparison (lowercase, no spaces).
func foldSeg(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "")
}

// matchesSeg compares two segments ignoring case and spaces.
func matchesSeg(a, b string) bool {
	return foldSeg(a) == foldSeg(b)
}

// capSeg capitalizes the first rune ("create" -> "Create").
func capSeg(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// commonPrefix returns the longest common prefix of a and b.
func commonPrefix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return a[:n]
}

// findSeg matches a segment against a list of labels: "Name" matches a label
// case-insensitively, "#i" matches by index. Returns -1 when nothing matches.
func findSeg(labels []string, seg string) int {
	if strings.HasPrefix(seg, "#") {
		if i, err := strconv.Atoi(seg[1:]); err == nil && i >= 0 && i < len(labels) {
			return i
		}
		return -1
	}
	for i, l := range labels {
		if matchesSeg(l, seg) {
			return i
		}
	}
	return -1
}

// currentPath returns the canonical path of the currently displayed screen
// (the top of the history stack, or the root "home").
func (e *Editor) currentPath() string {
	if len(e.history) == 0 {
		return "home"
	}
	return e.history[len(e.history)-1]
}

// splitPath breaks a canonical path into segments (home -> nil).
func splitPath(p string) []string {
	if p == "" || p == "home" {
		return nil
	}
	return strings.Split(p, "/")
}

// normalizePath turns a cd-style input into an absolute segment list relative
// to the current screen. Leading "~"/"/" start at the root; ".." pops a level;
// "." and empty segments are ignored.
func (e *Editor) normalizePath(input string) []string {
	input = strings.TrimSpace(input)
	cur := splitPath(e.currentPath())
	if input == "" {
		return cur
	}
	if strings.HasPrefix(input, "~") {
		cur = nil
		input = strings.TrimPrefix(input, "~")
		input = strings.TrimPrefix(input, "/")
	} else if strings.HasPrefix(input, "/") {
		cur = nil
		input = strings.TrimPrefix(input, "/")
	}
	for _, seg := range strings.Split(input, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(cur) > 0 {
				cur = cur[:len(cur)-1]
			}
		default:
			cur = append(cur, seg)
		}
	}
	return cur
}

// resolvePath resolves a cd-style path to a screen.
func (e *Editor) resolvePath(input string) (navTarget, error) {
	return e.resolveSegs(e.normalizePath(input))
}

// resolveSegs walks an absolute segment list to a screen target.
func (e *Editor) resolveSegs(segs []string) (navTarget, error) {
	if len(segs) == 0 {
		return navTarget{"home", e.homePage}, nil
	}
	head, rest := segs[0], segs[1:]
	switch foldSeg(head) {
	case "home":
		if len(rest) == 0 {
			return navTarget{"home", e.homePage}, nil
		}
		return e.resolveSegs(rest)
	case "panel":
		return e.resolvePanel(rest)
	case "connections":
		return e.resolveConnections(rest)
	case "auth":
		return e.resolveAuth(rest)
	case "audit":
		if len(rest) == 0 {
			return navTarget{"Audit", e.auditPage}, nil
		}
		if len(rest) == 1 && matchesSeg(rest[0], "Excluded Resources") {
			path := "Audit/Excluded Resources"
			return navTarget{path, func() tview.Primitive {
				return e.stringListPage(path, "Audit excluded resources",
					func() []string { return e.auditCfg().ExcludeResources },
					func(v []string) { e.auditCfg().ExcludeResources = v })
			}}, nil
		}
	case "procedures":
		return e.resolveProcedures(rest)
	case "plugins":
		return e.resolvePlugins(rest)
	case "navigation":
		return e.resolveNavigation(rest)
	case "resources":
		return e.resolveResources(rest)
	case "pages":
		return e.resolvePages(rest)
	case "validate":
		if len(rest) == 0 {
			return navTarget{"Validate", e.validatePage}, nil
		}
	case "preview":
		return e.resolvePreview(rest)
	}
	return navTarget{}, navErr(head)
}

func (e *Editor) resolvePanel(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Panel", e.panelPage}, nil
	}
	if len(rest) == 1 {
		switch foldSeg(rest[0]) {
		case "brand":
			return navTarget{"Panel/Brand", e.brandPage}, nil
		case "layout":
			return navTarget{"Panel/Layout", e.layoutPage}, nil
		case "theme":
			return navTarget{"Panel/Theme", e.themePage}, nil
		}
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

func (e *Editor) resolveConnections(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Connections", e.connectionsPage}, nil
	}
	name := ""
	for cn := range e.cfg.Connections {
		if matchesSeg(cn, rest[0]) {
			name = cn
			break
		}
	}
	if name == "" || len(rest) > 1 {
		return navTarget{}, navErr(strings.Join(rest, "/"))
	}
	return navTarget{"Connections/" + name, func() tview.Primitive { return e.connectionPage(name) }}, nil
}

func (e *Editor) resolveAuth(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Auth", e.authPage}, nil
	}
	if len(rest) == 1 && matchesSeg(rest[0], "Login Fields") {
		a := &e.cfg.Auth
		return navTarget{"Auth/Login Fields", func() tview.Primitive {
			return e.tagsPage("Auth/Login Fields", "Auth / Login fields", loginFieldOptions,
				func() []string { return a.Login.Fields },
				func(v []string) { a.Login.Fields = v })
		}}, nil
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

// resolveProcedures resolves "Procedures[/<name>]".
func (e *Editor) resolveProcedures(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Procedures", e.proceduresPage}, nil
	}
	pdx := e.procedureIdxBySeg(rest[0])
	if pdx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	if len(rest) > 1 {
		return navTarget{}, navErr(strings.Join(rest[1:], "/"))
	}
	return navTarget{e.procedurePath(pdx), func() tview.Primitive { return e.procedurePage(pdx) }}, nil
}

// resolvePlugins resolves "Plugins[/<name>]".
func (e *Editor) resolvePlugins(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Plugins", e.pluginsPage}, nil
	}
	plx := e.pluginIdxBySeg(rest[0])
	if plx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	if len(rest) > 1 {
		return navTarget{}, navErr(strings.Join(rest[1:], "/"))
	}
	return navTarget{e.pluginPath(plx), func() tview.Primitive { return e.pluginPage(plx) }}, nil
}

// navigation

func (e *Editor) navGroupPath(i int) string {
	return "Navigation/" + segName(e.cfg.Navigation[i].Group, i)
}

func (e *Editor) navGroupItemsPath(i int) string {
	return e.navGroupPath(i) + "/Items"
}

func (e *Editor) navItemSeg(gidx, iidx int) string {
	it := e.cfg.Navigation[gidx].Items[iidx]
	label := it.Label
	if label == "" {
		label = it.Resource + it.Page + it.URL
	}
	return segName(label, iidx)
}

func (e *Editor) navItemPath(gidx, iidx int) string {
	return e.navGroupItemsPath(gidx) + "/" + e.navItemSeg(gidx, iidx)
}

func (e *Editor) navGroupIdxBySeg(seg string) int {
	labels := make([]string, len(e.cfg.Navigation))
	for i, g := range e.cfg.Navigation {
		labels[i] = segName(g.Group, i)
	}
	return findSeg(labels, seg)
}

func (e *Editor) navItemIdxBySeg(gidx int, seg string) int {
	var labels []string
	for i := range e.cfg.Navigation[gidx].Items {
		labels = append(labels, e.navItemSeg(gidx, i))
	}
	return findSeg(labels, seg)
}

func (e *Editor) resolveNavigation(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Navigation", e.navGroupsPage}, nil
	}
	gidx := e.navGroupIdxBySeg(rest[0])
	if gidx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	base := e.navGroupPath(gidx)
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{base, func() tview.Primitive { return e.navGroupPage(gidx) }}, nil
	}
	if matchesSeg(rest[0], "Items") {
		rest = rest[1:]
		if len(rest) == 0 {
			return navTarget{base + "/Items", func() tview.Primitive { return e.navItemsPage(gidx) }}, nil
		}
		iidx := e.navItemIdxBySeg(gidx, rest[0])
		if iidx < 0 {
			return navTarget{}, navErr(rest[0])
		}
		if len(rest) > 1 {
			return navTarget{}, navErr(strings.Join(rest[1:], "/"))
		}
		return navTarget{base + "/Items/" + e.navItemSeg(gidx, iidx),
			func() tview.Primitive { return e.navItemPage(gidx, iidx) }}, nil
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

// resources

func (e *Editor) resPath(i int) string {
	return "Resources/" + segName(e.cfg.Resources[i].Name, i)
}

func (e *Editor) resListPath(i int) string         { return e.resPath(i) + "/List" }
func (e *Editor) resListFilterPath(i int) string   { return e.resListPath(i) + "/Filter" }
func (e *Editor) resColumnsPath(i int) string      { return e.resListPath(i) + "/Columns" }
func (e *Editor) resListComputedPath(i int) string { return e.resListPath(i) + "/Computed" }
func (e *Editor) resCardPath(i int) string         { return e.resPath(i) + "/Card" }
func (e *Editor) resCardFilterPath(i int) string   { return e.resCardPath(i) + "/Filter" }
func (e *Editor) resCardFieldsPath(i int) string   { return e.resCardPath(i) + "/Fields" }
func (e *Editor) resCardComputedPath(i int) string { return e.resCardPath(i) + "/Computed" }
func (e *Editor) resDetailPath(i int) string       { return e.resPath(i) + "/Detail" }
func (e *Editor) resDetailFieldsPath(i int) string { return e.resDetailPath(i) + "/Fields" }
func (e *Editor) resDetailComputedPath(i int) string {
	return e.resDetailPath(i) + "/Computed"
}
func (e *Editor) resFormPath(i int) string         { return e.resPath(i) + "/Form" }
func (e *Editor) resActionsPath(i int) string      { return e.resPath(i) + "/Actions" }
func (e *Editor) resPoliciesPath(i int) string     { return e.resPath(i) + "/Policies" }
func (e *Editor) resChildrenPath(i int) string     { return e.resPath(i) + "/Children" }

func (e *Editor) resColumnPath(i, ci int) string {
	return e.resColumnsPath(i) + "/" + segName(e.cfg.Resources[i].List.Columns[ci].Name, ci)
}

func (e *Editor) resFormWhichPath(i int, which string) string {
	return e.resFormPath(i) + "/" + capSeg(which)
}

func (e *Editor) resActionPath(i, ai int) string {
	return e.resActionsPath(i) + "/" + segName(e.cfg.Resources[i].Actions[ai].Name, ai)
}

func (e *Editor) resourceIdxBySeg(seg string) int {
	labels := make([]string, len(e.cfg.Resources))
	for i, r := range e.cfg.Resources {
		labels[i] = segName(r.Name, i)
	}
	return findSeg(labels, seg)
}

func (e *Editor) columnIdxBySeg(ridx int, seg string) int {
	if e.cfg.Resources[ridx].List == nil {
		return -1
	}
	var labels []string
	for i, c := range e.cfg.Resources[ridx].List.Columns {
		labels = append(labels, segName(c.Name, i))
	}
	return findSeg(labels, seg)
}

func (e *Editor) actionIdxBySeg(ridx int, seg string) int {
	var labels []string
	for i, a := range e.cfg.Resources[ridx].Actions {
		labels = append(labels, segName(a.Name, i))
	}
	return findSeg(labels, seg)
}

func (e *Editor) resolveResources(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Resources", e.resourcesPage}, nil
	}
	ridx := e.resourceIdxBySeg(rest[0])
	if ridx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	base := e.resPath(ridx)
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{base, func() tview.Primitive { return e.resourcePage(ridx) }}, nil
	}
	switch foldSeg(rest[0]) {
	case "list":
		return e.resolveResList(ridx, base, rest[1:])
	case "card":
		return e.resolveResCard(ridx, base, rest[1:])
	case "detail":
		return e.resolveResDetail(ridx, base, rest[1:])
	case "form":
		return e.resolveResForm(ridx, base, rest[1:])
	case "actions":
		return e.resolveResActions(ridx, base, rest[1:])
	case "policies":
		if len(rest) == 1 {
			return navTarget{base + "/Policies", func() tview.Primitive { return e.policiesPage(ridx) }}, nil
		}
	case "children":
		return e.resolveResChildren(ridx, base, rest[1:])
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

// resolveResChildren resolves .../Children[/<child>[/Columns]].
func (e *Editor) resolveResChildren(ridx int, base string, rest []string) (navTarget, error) {
	childrenPath := base + "/Children"
	if len(rest) == 0 {
		return navTarget{childrenPath, func() tview.Primitive { return e.childrenPage(ridx) }}, nil
	}
	r := &e.cfg.Resources[ridx]
	ci := -1
	for i := range r.Children {
		label := r.Children[i].Name
		if label == "" {
			label = r.Children[i].Resource
		}
		if matchesSeg(label, rest[0]) {
			ci = i
			break
		}
	}
	if ci < 0 {
		labels := make([]string, len(r.Children))
		for i := range r.Children {
			label := r.Children[i].Name
			if label == "" {
				label = r.Children[i].Resource
			}
			labels[i] = segName(label, i)
		}
		ci = findSeg(labels, rest[0])
	}
	if ci < 0 {
		return navTarget{}, navErr(rest[0])
	}
	chPath := childrenPath + "/" + segName(r.Children[ci].Name, ci)
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{chPath, func() tview.Primitive { return e.childResourcePage(ridx, ci) }}, nil
	}
	if matchesSeg(rest[0], "Columns") && len(rest) == 1 {
		return navTarget{chPath + "/Columns", func() tview.Primitive {
			return e.childColumnsPage(&e.cfg.Resources[ridx].Children[ci])
		}}, nil
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

func (e *Editor) resolveResList(ridx int, base string, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{base + "/List", func() tview.Primitive { return e.listPage(ridx) }}, nil
	}
	if matchesSeg(rest[0], "Columns") {
		colsPath := base + "/List/Columns"
		rest = rest[1:]
		if len(rest) == 0 {
			return navTarget{colsPath, func() tview.Primitive { return e.columnsPage(ridx) }}, nil
		}
		ci := e.columnIdxBySeg(ridx, rest[0])
		if ci < 0 {
			return navTarget{}, navErr(rest[0])
		}
		colPath := e.resColumnPath(ridx, ci)
		rest = rest[1:]
		if len(rest) == 0 {
			return navTarget{colPath, func() tview.Primitive { return e.columnPage(ridx, ci) }}, nil
		}
		if len(rest) == 1 && matchesSeg(rest[0], "Options") {
			c := &e.cfg.Resources[ridx].List.Columns[ci]
			return navTarget{colPath + "/Options", func() tview.Primitive {
				return e.stringMapPage(colPath+"/Options", "Column options",
					func() map[string]string { return c.Options },
					func(v map[string]string) { c.Options = v })
			}}, nil
		}
	}
	if matchesSeg(rest[0], "Export") && len(rest) == 1 {
		l := e.cfg.Resources[ridx].List
		path := base + "/List/Export"
		return navTarget{path, func() tview.Primitive {
			return e.stringListPage(path, "Export columns",
				func() []string { return l.Export },
				func(v []string) { l.Export = v })
		}}, nil
	}
	if matchesSeg(rest[0], "Computed") {
		l := ensureList(&e.cfg.Resources[ridx])
		return e.resolveResComputed(base+"/List/Computed", "List computed columns", func() *[]types.ComputedField {
			return &l.Computed
		}, rest[1:])
	}
	if matchesSeg(rest[0], "Filter") {
		path := base + "/List/Filter"
		if len(rest) == 1 {
			return navTarget{path, func() tview.Primitive { return e.filterPage(ridx, "list") }}, nil
		}
		if matchesSeg(rest[1], "Params") && len(rest) == 2 {
			f := e.cfg.Resources[ridx].List.Filter
			return navTarget{path + "/Params", func() tview.Primitive { return e.filterParamsPage(f) }}, nil
		}
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

func (e *Editor) resolveResCard(ridx int, base string, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{base + "/Card", func() tview.Primitive { return e.cardPage(ridx) }}, nil
	}
	if matchesSeg(rest[0], "Fields") {
		return e.resolveResFields(ridx, base+"/Card/Fields", "Card fields", func() *[]types.Field {
			r := e.cfg.Resources[ridx]
			if r.Card == nil {
				r.Card = &types.CardConfig{Columns: 4, Rows: 4}
			}
			return &r.Card.Fields
		}, rest[1:])
	}
	if matchesSeg(rest[0], "Searchable") && len(rest) == 1 {
		path := base + "/Card/Searchable"
		return navTarget{path, func() tview.Primitive {
			r := e.cfg.Resources[ridx]
			if r.Card == nil {
				r.Card = &types.CardConfig{Columns: 4, Rows: 4}
			}
			return e.stringListPage(path, "Card searchable",
				func() []string { return r.Card.Searchable },
				func(v []string) { r.Card.Searchable = v })
		}}, nil
	}
	if matchesSeg(rest[0], "Computed") {
		r := &e.cfg.Resources[ridx]
		if r.Card == nil {
			r.Card = &types.CardConfig{Columns: 4, Rows: 4}
		}
		return e.resolveResComputed(base+"/Card/Computed", "Card computed fields", func() *[]types.ComputedField {
			return &r.Card.Computed
		}, rest[1:])
	}
	if matchesSeg(rest[0], "Filter") {
		path := base + "/Card/Filter"
		if len(rest) == 1 {
			return navTarget{path, func() tview.Primitive { return e.filterPage(ridx, "card") }}, nil
		}
		if matchesSeg(rest[1], "Params") && len(rest) == 2 {
			r := e.cfg.Resources[ridx]
			if r.Card == nil {
				r.Card = &types.CardConfig{Columns: 4, Rows: 4}
			}
			f := r.Card.Filter
			return navTarget{path + "/Params", func() tview.Primitive { return e.filterParamsPage(f) }}, nil
		}
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

func (e *Editor) resolveResDetail(ridx int, base string, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{base + "/Detail", func() tview.Primitive { return e.detailPage(ridx) }}, nil
	}
	if matchesSeg(rest[0], "Params") && len(rest) == 1 {
		path := base + "/Detail/Params"
		return navTarget{path, func() tview.Primitive {
			r := e.cfg.Resources[ridx]
			if r.Detail == nil {
				r.Detail = &types.DetailConfig{}
			}
			return e.stringMapPage(path, "Detail params",
				func() map[string]string { return r.Detail.Params },
				func(v map[string]string) { r.Detail.Params = v })
		}}, nil
	}
	if matchesSeg(rest[0], "Fields") {
		return e.resolveResFields(ridx, base+"/Detail/Fields", "Detail fields", func() *[]types.Field {
			r := e.cfg.Resources[ridx]
			if r.Detail == nil {
				r.Detail = &types.DetailConfig{}
			}
			return &r.Detail.Fields
		}, rest[1:])
	}
	if matchesSeg(rest[0], "Computed") {
		r := &e.cfg.Resources[ridx]
		if r.Detail == nil {
			r.Detail = &types.DetailConfig{}
		}
		return e.resolveResComputed(base+"/Detail/Computed", "Detail computed fields", func() *[]types.ComputedField {
			return &r.Detail.Computed
		}, rest[1:])
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

var formWhichSegs = []string{"Create", "Update", "Delete"}

// formAction returns the (lazily allocated) FormAction for a form step.
func (e *Editor) formAction(ridx int, which string) *types.FormAction {
	r := &e.cfg.Resources[ridx]
	e.ensureFormAction(r, which)
	switch which {
	case "create":
		return r.Form.Create
	case "update":
		return r.Form.Update
	}
	return r.Form.Delete
}

func (e *Editor) resolveResForm(ridx int, base string, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{base + "/Form", func() tview.Primitive { return e.formPage(ridx) }}, nil
	}
	which := ""
	for _, w := range formWhichSegs {
		if matchesSeg(w, rest[0]) {
			which = strings.ToLower(w)
			break
		}
	}
	if which == "" {
		return navTarget{}, navErr(rest[0])
	}
	whichPath := base + "/Form/" + capSeg(which)
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{whichPath, func() tview.Primitive { return e.formActionPage(ridx, which) }}, nil
	}
	fa := e.formAction(ridx, which)
	switch {
	case matchesSeg(rest[0], "Params") && len(rest) == 1:
		path := whichPath + "/Params"
		return navTarget{path, func() tview.Primitive {
			return e.stringMapPage(path, capSeg(which)+" populate params",
				func() map[string]string { return fa.PopulateParams },
				func(v map[string]string) { fa.PopulateParams = v })
		}}, nil
	case matchesSeg(rest[0], "Fields"):
		return e.resolveResFields(ridx, whichPath+"/Fields", capSeg(which)+" fields",
			func() *[]types.Field { return &fa.Fields }, rest[1:])
	case matchesSeg(rest[0], "Hooks"):
		return e.resolveResHooks(whichPath+"/Hooks", &fa.Hooks, rest[1:])
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

func (e *Editor) resolveResActions(ridx int, base string, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{base + "/Actions", func() tview.Primitive { return e.actionsPage(ridx) }}, nil
	}
	aidx := e.actionIdxBySeg(ridx, rest[0])
	if aidx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	actionPath := e.resActionPath(ridx, aidx)
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{actionPath, func() tview.Primitive { return e.actionPage(ridx, aidx) }}, nil
	}
	if matchesSeg(rest[0], "Hooks") {
		a := &e.cfg.Resources[ridx].Actions[aidx]
		if a.Hooks == nil {
			a.Hooks = &types.Hooks{}
		}
		return e.resolveResHooks(actionPath+"/Hooks", &a.Hooks, rest[1:])
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

func (e *Editor) resolveResFields(ridx int, fp, title string, get func() *[]types.Field, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{fp, func() tview.Primitive { return e.fieldsListPage(fp, title, get) }}, nil
	}
	fs := *get()
	fidx := -1
	for i := range fs {
		if matchesSeg(fs[i].Name, rest[0]) {
			fidx = i
			break
		}
	}
	if fidx < 0 {
		labels := make([]string, len(fs))
		for i := range fs {
			labels[i] = segName(fs[i].Name, i)
		}
		fidx = findSeg(labels, rest[0])
	}
	if fidx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	fieldPath := fp + "/" + segName(fs[fidx].Name, fidx)
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{fieldPath, func() tview.Primitive { return e.fieldPage(fp, get, fidx) }}, nil
	}
	if len(rest) == 1 {
		fld := &(*get())[fidx]
		switch {
		case matchesSeg(rest[0], "Validation"):
			v := fld.Validation
			if v == nil {
				v = &types.Validation{}
			}
			return navTarget{fieldPath + "/Validation", func() tview.Primitive { return e.validationPage(fieldPath+"/Validation", v) }}, nil
		case matchesSeg(rest[0], "Options"):
			return navTarget{fieldPath + "/Options", func() tview.Primitive {
				return e.stringMapPage(fieldPath+"/Options", "Field options",
					func() map[string]string { return fld.Options },
					func(v map[string]string) { fld.Options = v })
			}}, nil
		case matchesSeg(rest[0], "Visible"):
			return navTarget{fieldPath + "/Visible", func() tview.Primitive {
				return e.tagsPage(fieldPath+"/Visible", "Field visible in", visibleOptions,
					func() []string { return fld.Visible },
					func(v []string) { fld.Visible = v })
			}}, nil
		case matchesSeg(rest[0], "Copies"):
			return navTarget{fieldPath + "/Copies", func() tview.Primitive {
				return e.stringMapPage(fieldPath+"/Copies", "Copy into fields (field: related column)",
					func() map[string]string { return fld.Copies },
					func(v map[string]string) { fld.Copies = v })
			}}, nil
		}
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

// resolveResComputed resolves .../Computed[/<computed>] — the E7 computed
// fields of a list/card/detail block.
func (e *Editor) resolveResComputed(base, title string, get func() *[]types.ComputedField, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{base, func() tview.Primitive { return e.computedListPage(base, title, get) }}, nil
	}
	cs := *get()
	ci := -1
	for i := range cs {
		if matchesSeg(cs[i].Name, rest[0]) {
			ci = i
			break
		}
	}
	if ci < 0 {
		labels := make([]string, len(cs))
		for i := range cs {
			labels[i] = segName(cs[i].Name, i)
		}
		ci = findSeg(labels, rest[0])
	}
	if ci < 0 {
		return navTarget{}, navErr(rest[0])
	}
	cPath := base + "/" + segName(cs[ci].Name, ci)
	if len(rest) > 1 {
		return navTarget{}, navErr(strings.Join(rest[1:], "/"))
	}
	return navTarget{cPath, func() tview.Primitive { return e.computedFieldPage(base, get, ci) }}, nil
}

// resolveResHooks resolves .../Hooks[/Before|After[/<hook>]].
func (e *Editor) resolveResHooks(base string, hooks **types.Hooks, rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{base, func() tview.Primitive { return e.hooksPage(base, hooks, "Hooks") }}, nil
	}
	before := matchesSeg(rest[0], "Before")
	after := matchesSeg(rest[0], "After")
	if !before && !after {
		return navTarget{}, navErr(rest[0])
	}
	listPath := base + "/" + capSeg(rest[0])
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{listPath, func() tview.Primitive { return e.hookListPage(base, hooks, before) }}, nil
	}
	get := hookListGet(hooks, before)
	hs := *get()
	hidx := -1
	for i := range hs {
		if matchesSeg(hs[i].Name, rest[0]) {
			hidx = i
			break
		}
	}
	if hidx < 0 {
		labels := make([]string, len(hs))
		for i := range hs {
			labels[i] = segName(hs[i].Name, i)
		}
		hidx = findSeg(labels, rest[0])
	}
	if hidx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	if len(rest) > 1 {
		return navTarget{}, navErr(strings.Join(rest[1:], "/"))
	}
	hookPath := listPath + "/" + segName(hs[hidx].Name, hidx)
	return navTarget{hookPath, func() tview.Primitive { return e.hookPage(get, hidx) }}, nil
}

// pages

func (e *Editor) pagePath(i int) string {
	return "Pages/" + segName(e.cfg.Pages[i].Name, i)
}

func (e *Editor) pageWidgetsPath(i int) string { return e.pagePath(i) + "/Widgets" }

func (e *Editor) widgetSeg(pi, wi int) string {
	return segName(e.cfg.Pages[pi].Widgets[wi].Label, wi)
}

func (e *Editor) pageWidgetPath(pi, wi int) string {
	return e.pageWidgetsPath(pi) + "/" + e.widgetSeg(pi, wi)
}

func (e *Editor) pageSubWidgetsPath(pi, wi int) string {
	return e.pageWidgetPath(pi, wi) + "/Sub-widgets"
}

func (e *Editor) pageSubWidgetPath(pi, wi, si int) string {
	return e.pageSubWidgetsPath(pi, wi) + "/" + segName(e.cfg.Pages[pi].Widgets[wi].Widgets[si].Label, si)
}

func (e *Editor) pageIdxBySeg(seg string) int {
	labels := make([]string, len(e.cfg.Pages))
	for i, p := range e.cfg.Pages {
		labels[i] = segName(p.Name, i)
	}
	return findSeg(labels, seg)
}

func (e *Editor) widgetIdxBySeg(pi int, seg string) int {
	var labels []string
	for i, w := range e.cfg.Pages[pi].Widgets {
		labels = append(labels, segName(w.Label, i))
	}
	return findSeg(labels, seg)
}

func (e *Editor) subWidgetIdxBySeg(pi, wi int, seg string) int {
	var labels []string
	for i, w := range e.cfg.Pages[pi].Widgets[wi].Widgets {
		labels = append(labels, segName(w.Label, i))
	}
	return findSeg(labels, seg)
}

func (e *Editor) resolvePages(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Pages", e.pagesPage}, nil
	}
	pidx := e.pageIdxBySeg(rest[0])
	if pidx < 0 {
		return navTarget{}, navErr(rest[0])
	}
	base := e.pagePath(pidx)
	rest = rest[1:]
	if len(rest) == 0 {
		return navTarget{base, func() tview.Primitive { return e.pagePage(pidx) }}, nil
	}
	if matchesSeg(rest[0], "Widgets") {
		widgetsPath := base + "/Widgets"
		rest = rest[1:]
		if len(rest) == 0 {
			return navTarget{widgetsPath, func() tview.Primitive { return e.widgetsPage(pidx) }}, nil
		}
		widx := e.widgetIdxBySeg(pidx, rest[0])
		if widx < 0 {
			return navTarget{}, navErr(rest[0])
		}
		widgetPath := e.pageWidgetPath(pidx, widx)
		rest = rest[1:]
		if len(rest) == 0 {
			return navTarget{widgetPath, func() tview.Primitive { return e.widgetPage(pidx, widx) }}, nil
		}
		switch {
		case matchesSeg(rest[0], "Sub-widgets"):
			subsPath := widgetPath + "/Sub-widgets"
			rest = rest[1:]
			if len(rest) == 0 {
				return navTarget{subsPath, func() tview.Primitive { return e.subWidgetsPage(pidx, widx) }}, nil
			}
			si := e.subWidgetIdxBySeg(pidx, widx, rest[0])
			if si < 0 {
				return navTarget{}, navErr(rest[0])
			}
			if len(rest) > 1 {
				return navTarget{}, navErr(strings.Join(rest[1:], "/"))
			}
			return navTarget{subsPath + "/" + segName(e.cfg.Pages[pidx].Widgets[widx].Widgets[si].Label, si),
				func() tview.Primitive { return e.subWidgetPage(pidx, widx, si) }}, nil
		case matchesSeg(rest[0], "Data Columns") && len(rest) == 1:
			path := widgetPath + "/Data Columns"
			return navTarget{path, func() tview.Primitive {
				return e.stringListPage(path, "Data columns",
					func() []string { return e.cfg.Pages[pidx].Widgets[widx].DataColumns },
					func(v []string) { e.cfg.Pages[pidx].Widgets[widx].DataColumns = v })
			}}, nil
		}
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

func (e *Editor) resolvePreview(rest []string) (navTarget, error) {
	if len(rest) == 0 {
		return navTarget{"Preview", e.previewPage}, nil
	}
	if len(rest) == 2 {
		if matchesSeg(rest[0], "Page") {
			for _, p := range e.cfg.Pages {
				if matchesSeg(p.Name, rest[1]) {
					return navTarget{"Preview/Page/" + p.Name, func() tview.Primitive { return e.pagePreview(p.Name) }}, nil
				}
			}
		} else if matchesSeg(rest[0], "Resource") {
			for _, r := range e.cfg.Resources {
				if matchesSeg(r.Name, rest[1]) {
					return navTarget{"Preview/Resource/" + r.Name, func() tview.Primitive { return e.resourcePreview(r.Name) }}, nil
				}
			}
		}
	}
	return navTarget{}, navErr(strings.Join(rest, "/"))
}

// childrenOf returns the child segment names of a directory path, and whether
// the path is a known directory. Used for Tab completion.
func (e *Editor) childrenOf(segs []string) ([]string, bool) {
	if len(segs) == 0 {
		return []string{"Panel", "Connections", "Auth", "Audit", "Procedures", "Plugins", "Navigation", "Resources", "Pages", "Validate", "Preview"}, true
	}
	rest := segs[1:]
	switch foldSeg(segs[0]) {
	case "panel":
		if len(rest) == 0 {
			return []string{"Brand", "Layout", "Theme"}, true
		}
	case "connections":
		if len(rest) == 0 {
			var out []string
			for n := range e.cfg.Connections {
				out = append(out, n)
			}
			sort.Strings(out)
			return out, true
		}
	case "auth":
		if len(rest) == 0 {
			return []string{"Login Fields"}, true
		}
	case "audit":
		if len(rest) == 0 {
			return []string{"Excluded Resources"}, true
		}
	case "procedures":
		if len(rest) == 0 {
			var out []string
			for i, p := range e.cfg.Procedures {
				out = append(out, segName(p.Name, i))
			}
			return out, true
		}
	case "plugins":
		if len(rest) == 0 {
			var out []string
			for i, pl := range e.cfg.Plugins {
				out = append(out, segName(pl.Name, i))
			}
			return out, true
		}
	case "navigation":
		if len(rest) == 0 {
			var out []string
			for i, g := range e.cfg.Navigation {
				out = append(out, segName(g.Group, i))
			}
			return out, true
		}
		gidx := e.navGroupIdxBySeg(rest[0])
		if gidx < 0 {
			return nil, false
		}
		rest = rest[1:]
		if len(rest) == 0 {
			return []string{"Items"}, true
		}
		if matchesSeg(rest[0], "Items") {
			if len(rest) == 1 {
				var out []string
				for i := range e.cfg.Navigation[gidx].Items {
					out = append(out, e.navItemSeg(gidx, i))
				}
				return out, true
			}
		}
	case "resources":
		if len(rest) == 0 {
			var out []string
			for i, r := range e.cfg.Resources {
				out = append(out, segName(r.Name, i))
			}
			return out, true
		}
		ridx := e.resourceIdxBySeg(rest[0])
		if ridx < 0 {
			return nil, false
		}
		r := &e.cfg.Resources[ridx]
		rest = rest[1:]
		if len(rest) == 0 {
			return []string{"List", "Card", "Detail", "Form", "Actions", "Policies", "Children"}, true
		}
		switch foldSeg(rest[0]) {
		case "list":
			if len(rest) == 1 {
				return []string{"Columns", "Computed", "Export"}, true
			}
			if matchesSeg(rest[1], "Columns") {
				if len(rest) == 2 {
					var out []string
					if r.List != nil {
						for i, c := range r.List.Columns {
							out = append(out, segName(c.Name, i))
						}
					}
					return out, true
				}
				if len(rest) == 3 {
					return []string{"Options"}, true
				}
			}
			if matchesSeg(rest[1], "Computed") && len(rest) == 2 {
				var out []string
				if r.List != nil {
					for i, c := range r.List.Computed {
						out = append(out, segName(c.Name, i))
					}
				}
				return out, true
			}
		case "card":
			if len(rest) == 1 {
				return []string{"Fields", "Computed", "Searchable"}, true
			}
			if matchesSeg(rest[1], "Fields") {
				if len(rest) == 2 {
					return fieldSegs(&r.Card.Fields), true
				}
				if len(rest) == 3 {
					return []string{"Validation", "Options", "Visible"}, true
				}
			}
			if matchesSeg(rest[1], "Computed") && len(rest) == 2 {
				return computedSegs(&r.Card.Computed), true
			}
		case "detail":
			if len(rest) == 1 {
				return []string{"Params", "Fields", "Computed"}, true
			}
			if matchesSeg(rest[1], "Fields") {
				if len(rest) == 2 {
					return fieldSegs(&r.Detail.Fields), true
				}
				if len(rest) == 3 {
					return []string{"Validation", "Options", "Visible"}, true
				}
			}
			if matchesSeg(rest[1], "Computed") && len(rest) == 2 {
				return computedSegs(&r.Detail.Computed), true
			}
		case "form":
			if len(rest) == 1 {
				return formWhichSegs, true
			}
			which := ""
			for _, w := range formWhichSegs {
				if matchesSeg(w, rest[1]) {
					which = strings.ToLower(w)
					break
				}
			}
			if which == "" {
				return nil, false
			}
			if len(rest) == 2 {
				return []string{"Params", "Fields", "Hooks"}, true
			}
			fa := e.formAction(ridx, which)
			if matchesSeg(rest[2], "Fields") {
				if len(rest) == 3 {
					return fieldSegs(&fa.Fields), true
				}
				if len(rest) == 4 {
					return []string{"Validation", "Options", "Visible"}, true
				}
			}
			if matchesSeg(rest[2], "Hooks") {
				if len(rest) == 3 {
					return []string{"Before", "After"}, true
				}
				if len(rest) == 4 {
					before := matchesSeg(rest[3], "Before")
					if before || matchesSeg(rest[3], "After") {
						return hookSegs(&fa.Hooks, before), true
					}
				}
			}
		case "actions":
			if len(rest) == 1 {
				var out []string
				for i, a := range r.Actions {
					out = append(out, segName(a.Name, i))
				}
				return out, true
			}
			aidx := e.actionIdxBySeg(ridx, rest[1])
			if aidx < 0 {
				return nil, false
			}
			if len(rest) == 2 {
				return []string{"Hooks"}, true
			}
			if matchesSeg(rest[2], "Hooks") {
				if len(rest) == 3 {
					return []string{"Before", "After"}, true
				}
				if len(rest) == 4 {
					a := &r.Actions[aidx]
					if a.Hooks == nil {
						a.Hooks = &types.Hooks{}
					}
					before := matchesSeg(rest[3], "Before")
					if before || matchesSeg(rest[3], "After") {
						return hookSegs(&a.Hooks, before), true
					}
				}
			}
		case "policies":
			return nil, true
		}
	case "pages":
		if len(rest) == 0 {
			var out []string
			for i, p := range e.cfg.Pages {
				out = append(out, segName(p.Name, i))
			}
			return out, true
		}
		pidx := e.pageIdxBySeg(rest[0])
		if pidx < 0 {
			return nil, false
		}
		rest = rest[1:]
		if len(rest) == 0 {
			return []string{"Widgets"}, true
		}
		if matchesSeg(rest[0], "Widgets") {
			if len(rest) == 1 {
				var out []string
				for i, w := range e.cfg.Pages[pidx].Widgets {
					out = append(out, segName(w.Label, i))
				}
				return out, true
			}
			widx := e.widgetIdxBySeg(pidx, rest[1])
			if widx < 0 {
				return nil, false
			}
			if len(rest) == 2 {
				return []string{"Sub-widgets", "Data Columns"}, true
			}
			if matchesSeg(rest[2], "Sub-widgets") {
				if len(rest) == 3 {
					var out []string
					for i, w := range e.cfg.Pages[pidx].Widgets[widx].Widgets {
						out = append(out, segName(w.Label, i))
					}
					return out, true
				}
			}
		}
	case "validate":
		return nil, true
	case "preview":
		if len(rest) == 0 {
			return []string{"Page", "Resource"}, true
		}
		if matchesSeg(rest[0], "Page") {
			if len(rest) == 1 {
				var out []string
				for _, p := range e.cfg.Pages {
					out = append(out, p.Name)
				}
				return out, true
			}
		} else if matchesSeg(rest[0], "Resource") {
			if len(rest) == 1 {
				var out []string
				for _, r := range e.cfg.Resources {
					out = append(out, r.Name)
				}
				return out, true
			}
		}
	}
	return nil, false
}

func fieldSegs(fs *[]types.Field) []string {
	var out []string
	for i, f := range *fs {
		out = append(out, segName(f.Name, i))
	}
	return out
}

func computedSegs(cs *[]types.ComputedField) []string {
	var out []string
	for i, c := range *cs {
		out = append(out, segName(c.Name, i))
	}
	return out
}

func hookSegs(hooks **types.Hooks, before bool) []string {
	get := hookListGet(hooks, before)
	var out []string
	for i, h := range *get() {
		out = append(out, segName(h.Name, i))
	}
	return out
}

func hookListGet(hooks **types.Hooks, before bool) func() *[]types.Hook {
	return func() *[]types.Hook {
		if *hooks == nil {
			*hooks = &types.Hooks{}
		}
		if before {
			return &(*hooks).Before
		}
		return &(*hooks).After
	}
}

// completePath expands the trailing segment of a cd input to the longest common
// prefix of the matching child screens, returning the new input and the list of
// matches for the dialog's hint line.
func (e *Editor) completePath(input string) (string, []string) {
	segs := e.normalizePath(input)
	if len(segs) == 0 {
		children, ok := e.childrenOf(nil)
		if !ok {
			return input, nil
		}
		return e.finishComplete(input, nil, "", children)
	}
	prefix := segs[len(segs)-1]
	parents := segs[:len(segs)-1]
	children, ok := e.childrenOf(parents)
	if !ok {
		return input, nil
	}
	return e.finishComplete(input, parents, prefix, children)
}

func (e *Editor) finishComplete(input string, parents []string, prefix string, children []string) (string, []string) {
	var matches []string
	for _, c := range children {
		if strings.HasPrefix(foldSeg(c), foldSeg(prefix)) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 || prefix == "" {
		return input, matches
	}
	lcp := matches[0]
	for _, m := range matches[1:] {
		lcp = commonPrefix(lcp, m)
	}
	abs := strings.HasPrefix(strings.TrimSpace(input), "~") || strings.HasPrefix(strings.TrimSpace(input), "/")
	segs := append([]string{}, parents...)
	segs = append(segs, lcp)
	out := strings.Join(segs, "/")
	if abs {
		out = "~/" + out
	}
	return out, matches
}

// cd dialog

// openNav shows the cd-navigation dialog. It is ignored while another modal
// is open.
func (e *Editor) openNav() {
	if e.modalOpen {
		return
	}
	input := tview.NewInputField().SetLabel("Path")
	input.SetFieldBackgroundColor(tcell.NewHexColor(0x27272a))
	input.SetLabelColor(colText)
	hint := tview.NewTextView().SetDynamicColors(true)
	hint.SetLabel("")

	form := tview.NewForm()
	form.SetBorder(true).SetBorderColor(colBorder).SetTitle("Go to")
	form.SetLabelColor(colText)
	form.SetFieldBackgroundColor(tcell.NewHexColor(0x27272a))
	form.AddFormItem(input)
	form.AddFormItem(hint)

	e.navInput = input
	e.navHint = hint
	input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			e.navGo()
			return nil
		case tcell.KeyTab:
			e.navTab()
			return nil
		}
		return event
	})

	e.navOpen = true
	e.showModal(form)
	e.navShowHint()
	e.app.SetFocus(input)
}

// navShowHint renders the dialog's cwd line (used on open and on Esc-clear).
func (e *Editor) navShowHint() {
	if e.navHint == nil {
		return
	}
	cwd := e.currentPath()
	show := "~"
	if cwd != "home" {
		show = "~/" + cwd
	}
	e.navHint.SetText(fmt.Sprintf("[::d]cwd: %s    Tab: complete   Enter: go   Esc: clear / close[-:-:-]", show))
}

// navGo resolves the dialog input and navigates when the path is valid.
// Unknown paths keep the dialog open (with an error hint); the current screen
// is untouched.
func (e *Editor) navGo() {
	if e.navInput == nil {
		return
	}
	input := e.navInput.GetText()
	t, err := e.resolvePath(input)
	if err != nil {
		if e.navHint != nil {
			e.navHint.SetText("[red]no such path: " + tview.Escape(strings.TrimSpace(input)) + "[-:-:-]")
		}
		return
	}
	e.navClose()
	if e.currentPath() == t.name {
		e.toast(t.name)
		return
	}
	e.showPage(t.name, t.build())
}

// navTab completes the dialog input to the longest common prefix of matching
// child screens and lists the matches in the hint line.
func (e *Editor) navTab() {
	if e.navInput == nil {
		return
	}
	out, matches := e.completePath(e.navInput.GetText())
	e.navInput.SetText(out)
	if e.navHint == nil {
		return
	}
	if len(matches) == 0 {
		e.navHint.SetText("[yellow]no matches[-:-:-]")
		return
	}
	list := strings.Join(matches, "  ")
	if len(list) > 60 {
		list = list[:57] + "..."
	}
	e.navHint.SetText("[::d]matches: " + tview.Escape(list) + "[-:-:-]")
}

// navClose closes the cd dialog.
func (e *Editor) navClose() {
	if !e.navOpen {
		return
	}
	e.navOpen = false
	e.navInput = nil
	e.navHint = nil
	e.closeModal()
}

// goHome jumps to the root screen, closing the cd dialog if it is open. It is
// a no-op when already on home.
func (e *Editor) goHome() {
	if e.navOpen {
		e.navClose()
	}
	if e.currentPath() == "home" {
		return
	}
	e.home()
}
