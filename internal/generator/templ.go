// templ.go
//
// Generates all the .templ views of the admin panel application: per-resource
// list/detail/form views, per-page widget views, the shared layout
// (base.templ with sidebar/topbar), and reusable components
// (renderers.templ with field renderers, search bar, sort icon and
// pagination). All views declare package views.
package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichalHerstus/yaga/internal/types"
)

// prefixImports rewrites bare "internal/..." import paths in generated source
// so they are module-qualified, matching the module name written to go.mod.
// Params: code (generated Go or templ source), moduleImport (the module-
// qualified import path to substitute).
// Returns: the source with the bare import rewritten.
func prefixImports(code string, moduleImport string) string {
	var extras []string
	if strings.Contains(code, "fmt.") && !strings.Contains(code, `"fmt"`) {
		extras = append(extras, "fmt")
	}
	if strings.Contains(code, "sort.") && !strings.Contains(code, `"sort"`) {
		extras = append(extras, "sort")
	}
	if len(extras) > 0 {
		block := "import (\n"
		for _, e := range extras {
			block += "    \"" + e + "\"\n"
		}
		block += "    \"internal/viewmodels\"\n)"
		code = strings.Replace(code, `import "internal/viewmodels"`, block, 1)
	}
	return strings.ReplaceAll(code, `"internal/viewmodels"`, fmt.Sprintf("%q", moduleImport))
}

// jsSingleQuote escapes a label for use inside a single-quoted JavaScript
// string that is itself inside a double-quoted HTML attribute (as emitted for
// the confirm() prompt on action forms/buttons). Backslashes are escaped
// first so an existing quote escape is not broken.
// Params: s (the raw label).
// Returns: the label safe to embed in '...' within "...".
func jsSingleQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// actionLabel returns the display label of an action, falling back to the
// action name when no label is configured.
// Params: a (the action definition).
// Returns: the label string.
func actionLabel(a types.Action) string {
	if a.Label == "" {
		return a.Name
	}
	return a.Label
}

// generateViews runs all templ generation steps: one view set per resource,
// one view per page, then the layout and component views.
// Returns: an error if any step fails.
func (g *Generator) generateViews() error {
	for _, r := range g.Config.Resources {
		if err := g.generateResourceViews(r); err != nil {
			return err
		}
	}
	if len(g.Config.Pages) > 0 {
		if err := g.generatePageWidgets(); err != nil {
			return err
		}
	}
	for _, p := range g.Config.Pages {
		if err := g.generatePageViews(p); err != nil {
			return err
		}
	}
	if err := g.generateLayoutViews(); err != nil {
		return err
	}
	if err := g.generateComponentViews(); err != nil {
		return err
	}
	return nil
}

// generateResourceViews writes the list/detail/form templ files for a single
// resource into internal/views/resources/{resource}/, one per declared section.
// The shared renderer components (renderBadge, searchBar, pagination, etc.) are
// emitted into the same directory so every resource view package is
// self-contained.
// Params: r (the resource definition).
// Returns: an error if any templ file fails to write.
func (g *Generator) generateResourceViews(r types.Resource) error {
	viewDir := filepath.Join(g.OutDir, "internal/views/resources", strings.ToLower(r.Name))
	if err := os.WriteFile(filepath.Join(viewDir, "renderers.templ"), []byte(prefixImports(renderersSource(), g.moduleImport("internal/viewmodels"))), 0644); err != nil {
		return err
	}
	if r.List != nil {
		if err := g.generateListTempl(viewDir, r); err != nil {
			return err
		}
	}
	if r.Detail != nil {
		if err := g.generateDetailTempl(viewDir, r); err != nil {
			return err
		}
	}
	if r.Form != nil {
		if err := g.generateFormTempl(viewDir, r); err != nil {
			return err
		}
	}
	if r.Card != nil {
		if err := g.generateCardTempl(viewDir, r); err != nil {
			return err
		}
	}
	return nil
}

// renderCell returns the templ expression used to display a cell value based
// on its field type, delegating to the matching renderer component in
// renderers.templ (badge, boolean, email, image, file, datetime, date,
// select, relation, json, float) or emitting the raw value for plain types.
// Params: fieldType (the column/field type), expr (the templ expression that
// yields the value, e.g. `item["name"]`).
// Returns: the templ expression string for the cell.
func renderCell(fieldType, expr string) string {
	switch fieldType {
	case "badge":
		return fmt.Sprintf(`@renderBadge(%s, "")`, expr)
	case "boolean":
		return fmt.Sprintf(`@renderBoolean(%s)`, expr)
	case "email":
		return fmt.Sprintf(`@renderEmail(%s)`, expr)
	case "image":
		return fmt.Sprintf(`@renderImage(%s)`, expr)
	case "file":
		return fmt.Sprintf(`@renderFile(%s)`, expr)
	case "datetime":
		return fmt.Sprintf(`@renderDateTime(%s)`, expr)
	case "date":
		return fmt.Sprintf(`@renderDate(%s)`, expr)
	case "select":
		return fmt.Sprintf(`@renderSelect(%s)`, expr)
	case "relation":
		return fmt.Sprintf(`@renderRelation(%s)`, expr)
	case "json":
		return fmt.Sprintf(`@renderJSON(%s)`, expr)
	case "float":
		return fmt.Sprintf(`@renderFloat(%s)`, expr)
	case "gps":
		return fmt.Sprintf(`@renderGPS(%s)`, expr)
	case "integer", "string", "text", "password":
		return fmt.Sprintf(`{ viewmodels.Stringify(%s) }`, expr)
	default:
		return fmt.Sprintf(`{ viewmodels.Stringify(%s) }`, expr)
	}
}

// generateListTempl writes list.templ for a resource: a table with sortable
// headers, per-row action forms (view/edit/custom actions/delete), a create
// and CSV export button, the search bar and pagination.
// Params: dir (view directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateListTempl(dir string, r types.Resource) error {
	cols := append([]types.Column{}, r.List.Columns...)
	cols = append(cols, computedColumns(r.List.Computed)...)
	templName := r.Name + "List"
	resLabel := r.Label
	resLower := strings.ToLower(r.Name)
	panelPath := g.Config.Panel.Path
	idCol := idColumn(r)

	hasBulk := false
	for _, a := range r.Actions {
		if a.Bulk {
			hasBulk = true
			break
		}
	}

	var headers strings.Builder
	var cells strings.Builder

	if hasBulk {
		headers.WriteString(`            <th class="w-10 px-6 py-3">
                <input type="checkbox" class="rounded border-gray-300 text-brand-primary focus:ring-brand-primary" onclick="toggleSelectAll(this)" />
            </th>
`)
		cells.WriteString(fmt.Sprintf(`            <td class="px-6 py-4">
                <input type="checkbox" name="ids" value={ fmt.Sprintf("%%v", item[%q]) } class="rounded border-gray-300 text-brand-primary focus:ring-brand-primary" />
            </td>
`, idCol))
	}

	for _, c := range cols {
		label := c.Label
		if label == "" {
			label = c.Name
		}
		if c.Sortable {
			headers.WriteString(fmt.Sprintf(`            <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                <a href={ templ.SafeURL(fmt.Sprintf("?sort=%%s&order=%%s", %q, sortOrder(data.Sort, %q, data.Order))) } class="flex items-center gap-1 hover:text-gray-700 dark:hover:text-gray-200">
                    %s
                    @sortIcon(data.Sort, %q, data.Order)
                </a>
            </th>
`, c.Name, c.Name, label, c.Name))
		} else {
			headers.WriteString(fmt.Sprintf(`            <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">%s</th>
`, label))
		}
		rendered := renderCell(c.Type, fmt.Sprintf(`item[%q]`, c.Name))
		cells.WriteString(fmt.Sprintf(`            <td class="px-6 py-4 whitespace-nowrap text-sm">%s</td>
`, rendered))
	}

	var extraActions string
	for _, a := range r.Actions {
		label := actionLabel(a)
		icon := ""
		if a.Icon != "" {
			icon = fmt.Sprintf(`@actionIcon(%q) `, a.Icon)
		}
		confirm := ""
		if a.RequiresConfirmation {
			if hasBulk {
				confirm = ` onclick="return confirm('` + jsSingleQuote(label) + `?')"`
			} else {
				confirm = ` onsubmit="return confirm('` + jsSingleQuote(label) + `?')"`
			}
		}
		if hasBulk {
			extraActions += fmt.Sprintf(`                <button type="submit" formaction={ fmt.Sprintf("%%s/%%s/%%v/action/%%s", %q, %q, item[%q], %q) } formmethod="POST" class="text-brand-primary hover:text-brand-primary/80 text-sm mr-2 inline-flex items-center"%s>%s%s</button>
`, panelPath, resLower, idCol, a.Name, confirm, icon, label)
		} else {
			extraActions += fmt.Sprintf(`                <form action={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v/action/%%s", %q, %q, item[%q], %q)) } method="POST" class="inline"%s>
                    <input type="hidden" name="_csrf" value={ data.CSRFToken } />
                    <button type="submit" class="text-brand-primary hover:text-brand-primary/80 text-sm mr-2 inline-flex items-center">%s%s</button>
                </form>
`, panelPath, resLower, idCol, a.Name, confirm, icon, label)
		}
	}
	if r.Form != nil && r.Form.Delete != nil {
		if hasBulk {
			extraActions += fmt.Sprintf(`                <button type="submit" formaction={ fmt.Sprintf("%%s/%%s/%%v/delete", %q, %q, item[%q]) } formmethod="POST" class="text-red-600 hover:text-red-900 text-sm" onclick="return confirm('Delete?')">Delete</button>
`, panelPath, resLower, idCol)
		} else {
			extraActions += fmt.Sprintf(`                <form action={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v/delete", %q, %q, item[%q])) } method="POST" class="inline" onsubmit="return confirm('Delete?')">
                    <input type="hidden" name="_csrf" value={ data.CSRFToken } />
                    <button type="submit" class="text-red-600 hover:text-red-900 text-sm">Delete</button>
                </form>
`, panelPath, resLower, idCol)
		}
	}

	viewLink := ""
	if r.Detail != nil {
		viewLink = fmt.Sprintf(`                <a href={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v", %q, %q, item[%q])) } class="text-brand-primary hover:text-brand-primary/80 mr-3">View</a>
`, panelPath, resLower, idCol)
	}
	editLink := ""
	if r.Form != nil && r.Form.Update != nil {
		editLink = fmt.Sprintf(`                <a href={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v/edit", %q, %q, item[%q])) } class="text-brand-primary hover:text-brand-primary/80 mr-3">Edit</a>
`, panelPath, resLower, idCol)
	}
	actionsCol := fmt.Sprintf(`            <td class="px-6 py-4 whitespace-nowrap text-right text-sm font-medium">
%s%s%s            </td>
`, viewLink, editLink, extraActions)

	createBtn := ""
	if r.Form != nil && r.Form.Create != nil {
		createBtn = fmt.Sprintf(`<a href="%s/%s/new" class="bg-brand-primary text-white px-4 py-2 rounded-lg text-sm hover:opacity-90">Create %s</a>`, panelPath, resLower, resLabel)
	}
	exportBtn := fmt.Sprintf(`<a href={ templ.SafeURL(fmt.Sprintf("%%s/%%s/export/csv", %q, %q)) } class="text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100 px-4 py-2 text-sm">Export CSV</a>`, panelPath, resLower)

	importBtn := ""
	importModal := ""
	if r.ImportCSV {
		importBtn = `<button type="button" onclick="document.getElementById('import-modal').classList.remove('hidden')" class="text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100 px-4 py-2 text-sm">Import CSV</button> `
		importModal = fmt.Sprintf(`
        <div id="import-modal" class="hidden fixed inset-0 z-50 flex items-center justify-center bg-black/50">
            <div class="bg-white dark:bg-gray-800 rounded-lg shadow-lg p-6 w-full max-w-md">
                <h2 class="text-lg font-bold mb-4 text-gray-900 dark:text-gray-100">Import %s from CSV</h2>
                <form method="POST" enctype="multipart/form-data" action={ templ.SafeURL(fmt.Sprintf("%%s/%%s/import/csv", %q, %q)) }>
                    <input type="hidden" name="_csrf" value={ data.CSRFToken } />
                    <input type="file" name="file" accept=".csv" required class="block w-full text-sm text-gray-700 dark:text-gray-300 mb-4" />
                    <div class="flex justify-end gap-2">
                        <button type="button" onclick="document.getElementById('import-modal').classList.add('hidden')" class="px-4 py-2 rounded-lg text-sm text-gray-600 hover:text-gray-900 dark:text-gray-400">Cancel</button>
                        <button type="submit" class="bg-brand-primary text-white px-4 py-2 rounded-lg text-sm hover:opacity-90">Import</button>
                    </div>
                </form>
            </div>
        </div>
`, resLabel, panelPath, resLower)
	}

	cardBtn := ""
	if r.Card != nil {
		cardBtn = fmt.Sprintf(`<a href="%s/%s/cards" class="text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100 px-4 py-2 text-sm">Cards</a>`, panelPath, resLower)
	}

	headerBtns := createBtn + " " + exportBtn
	if cardBtn != "" {
		headerBtns = createBtn + " " + cardBtn + " " + exportBtn
	}
	if importBtn != "" {
		headerBtns = importBtn + headerBtns
	}

	bulkBtns := ""
	if hasBulk {
		var sb strings.Builder
		for _, a := range r.Actions {
			if !a.Bulk {
				continue
			}
			sb.WriteString(fmt.Sprintf(`                    <button type="submit" formaction={ fmt.Sprintf("%%s/%%s/bulk/%%s", %q, %q, %q) } formmethod="POST" class="bg-brand-primary text-white px-3 py-1.5 rounded-md text-sm hover:opacity-90">%s Selected</button>
`, panelPath, resLower, a.Name, actionLabel(a)))
		}
		bulkBtns = sb.String()
	}

	code := fmt.Sprintf(`package views

import "internal/viewmodels"

templ %s(data *viewmodels.ListData) {
    <div class="p-6">
        <div class="flex items-center justify-between mb-6">
            <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">%s</h1>
            <div class="flex gap-2 items-center">
                %s
            </div>
        </div>

        @filterBar(data.Filter, data.Search, data.Sort, data.Order)

        @searchBar(data.Search, data.Resource)

        <div class="bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                <thead class="bg-gray-50 dark:bg-gray-900">
                    <tr>
%s                        <th class="px-6 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Actions</th>
                    </tr>
                </thead>
                <tbody class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                    for _, item := range data.Items {
                    <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
%s%s                    </tr>
                    }
                </tbody>
            </table>

            @pagination(data.Page, data.TotalPages, data.Total, data.Search, data.Sort, data.Order, data.FilterQS)
        </div>
%s    </div>
}
`, templName, resLabel, headerBtns, headers.String(), cells.String(), actionsCol, importModal)

	if hasBulk {
		code = fmt.Sprintf(`package views

import "internal/viewmodels"

templ %s(data *viewmodels.ListData) {
    <div class="p-6">
        <div class="flex items-center justify-between mb-6">
            <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">%s</h1>
            <div class="flex gap-2 items-center">
                %s
            </div>
        </div>

        @filterBar(data.Filter, data.Search, data.Sort, data.Order)

        @searchBar(data.Search, data.Resource)

        <div class="bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden">
            <form method="POST">
                <input type="hidden" name="_csrf" value={ data.CSRFToken } />
                <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                    <thead class="bg-gray-50 dark:bg-gray-900">
                        <tr>
%s                        <th class="px-6 py-3 text-right text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Actions</th>
                        </tr>
                    </thead>
                    <tbody class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                        for _, item := range data.Items {
                        <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
%s%s                        </tr>
                        }
                    </tbody>
                </table>
                <div class="bg-gray-50 dark:bg-gray-900 px-4 py-3 border-t border-gray-200 dark:border-gray-700 flex items-center gap-2">
%s                </div>
            </form>

            @pagination(data.Page, data.TotalPages, data.Total, data.Search, data.Sort, data.Order, data.FilterQS)
        </div>
%s    </div>
}
`, templName, resLabel, headerBtns, headers.String(), cells.String(), actionsCol, bulkBtns, importModal)
	}
	code = prefixImports(code, g.moduleImport("internal/viewmodels"))

	return os.WriteFile(filepath.Join(dir, "list.templ"), []byte(code), 0644)
}

// generateDetailTempl writes detail.templ for a resource: a read-only table
// of the detail fields plus action buttons (edit, custom actions, delete).
// Params: dir (view directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateDetailTempl(dir string, r types.Resource) error {
	resName := r.Name
	templName := resName + "Detail"
	resLower := strings.ToLower(resName)
	idCol := idColumn(r)
	panelPath := g.Config.Panel.Path

	var rows strings.Builder
	detailFields := append([]types.Field{}, r.Detail.Fields...)
	detailFields = append(detailFields, computedFields(r.Detail.Computed)...)
	for _, f := range detailFields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		rendered := renderCell(f.Type, fmt.Sprintf(`data.Item[%q]`, f.Name))
		rows.WriteString(fmt.Sprintf(`                <tr>
                    <td class="px-6 py-4 whitespace-nowrap text-sm font-medium text-gray-500 dark:text-gray-400 w-1/4">%s</td>
                    <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-900 dark:text-gray-100">%s</td>
                </tr>
`, label, rendered))
	}

	var actionBtns strings.Builder
	for _, a := range r.Actions {
		label := actionLabel(a)
		icon := ""
		if a.Icon != "" {
			icon = fmt.Sprintf(`@actionIcon(%q) `, a.Icon)
		}
		confirm := ""
		if a.RequiresConfirmation {
			confirm = ` onsubmit="return confirm('` + jsSingleQuote(label) + `?')"`
		}
		actionBtns.WriteString(fmt.Sprintf(`                <form action={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v/action/%%s", %q, %q, data.Item[%q], %q)) } method="POST" class="inline"%s>
                    <input type="hidden" name="_csrf" value={ data.CSRFToken } />
                    <button type="submit" class="%s px-4 py-2 rounded-lg text-sm hover:opacity-90">%s%s</button>
                </form>
`, panelPath, resLower, idCol, a.Name, confirm, actionColor(a.Color), icon, label))
	}
	if r.Form != nil && r.Form.Delete != nil {
		actionBtns.WriteString(fmt.Sprintf(`                <form action={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v/delete", %q, %q, data.Item[%q])) } method="POST" class="inline" onsubmit="return confirm('Delete this %s?')">
                    <input type="hidden" name="_csrf" value={ data.CSRFToken } />
                    <button type="submit" class="bg-red-600 text-white px-4 py-2 rounded-lg text-sm hover:bg-red-700">Delete</button>
                </form>
`, panelPath, resLower, idCol, resName))
	}

	detailEditBtn := ""
	if r.Form != nil && r.Form.Update != nil {
		detailEditBtn = fmt.Sprintf(`                <a href={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v/edit", %q, %q, data.Item[%q])) } class="bg-brand-primary text-white px-4 py-2 rounded-lg text-sm hover:opacity-90">Edit</a>
`, panelPath, resLower, idCol)
	}

	// D14: master-detail lines below the detail table, view-only links.
	childSections := ""
	if len(r.Children) > 0 {
		childSections = `        if len(data.Lines) > 0 {
            for _, sec := range data.Lines {
` + childLinesSection(false) + `            }
        }
`
	}

	code := fmt.Sprintf(`package views

import "internal/viewmodels"

templ %s(data *viewmodels.DetailData) {
    <div class="p-6">
        <div class="flex items-center justify-between mb-6">
            <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">%s Details</h1>
            <div class="flex gap-2">
                <a href="%s/%s" class="text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100 px-4 py-2">Back</a>
%s%s            </div>
        </div>

        <div class="bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                <tbody class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
%s                </tbody>
            </table>
        </div>
%s    </div>
}
`, templName, resName, panelPath, resLower, detailEditBtn, actionBtns.String(), rows.String(), childSections)
	code = prefixImports(code, g.moduleImport("internal/viewmodels"))

	return os.WriteFile(filepath.Join(dir, "detail.templ"), []byte(code), 0644)
}

// actionColor maps a semantic action color (success, danger, warning,
// primary, info or any of their aliases) to the Tailwind button classes used
// on action buttons. Unknown colors fall back to gray.
// Params: c (the configured color name).
// Returns: the Tailwind class string for the button.
func actionColor(c string) string {
	switch c {
	case "success", "green":
		return "bg-green-600 text-white"
	case "danger", "red":
		return "bg-red-600 text-white"
	case "warning", "yellow":
		return "bg-yellow-500 text-white"
	case "primary", "indigo":
		return "bg-brand-primary text-white"
	case "info", "blue":
		return "bg-blue-600 text-white"
	default:
		return "bg-gray-600 text-white"
	}
}

// generateFormTempl writes form.templ for a resource: a shared create/update
// form rendered from the create fields (when present) or update fields. It
// emits the appropriate input widget per field type, adds a multipart enctype
// when any field is a file/image, and shows validation hints for required
// fields.
// Params: dir (view directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateFormTempl(dir string, r types.Resource) error {
	templName := r.Name + "Form"
	resLabel := r.Label
	resLower := strings.ToLower(r.Name)
	panelPath := g.Config.Panel.Path

	both := r.Form.Create != nil && r.Form.Update != nil
	var createFields, updateFields []types.Field
	if r.Form.Create != nil {
		createFields = r.Form.Create.Fields
	}
	if r.Form.Update != nil {
		updateFields = r.Form.Update.Fields
	}

	// merged is the union of create + update fields (deduped by name, create
	// order first then update-only appended). Each entry records which context
	// it belongs to so the shared form can render the right fields per mode.
	var merged []struct {
		f        types.Field
		inCreate bool
		inUpdate bool
	}
	seen := map[string]int{}
	for _, f := range createFields {
		merged = append(merged, struct {
			f        types.Field
			inCreate bool
			inUpdate bool
		}{f: f, inCreate: true, inUpdate: fieldIn(updateFields, f.Name)})
		seen[f.Name] = len(merged) - 1
	}
	for _, f := range updateFields {
		if idx, ok := seen[f.Name]; ok {
			merged[idx].inUpdate = true
		} else {
			merged = append(merged, struct {
				f        types.Field
				inCreate bool
				inUpdate bool
			}{f: f, inCreate: false, inUpdate: true})
		}
	}

	var inputs strings.Builder

	for _, e := range merged {
		f := e.f
		if !e.inCreate && !e.inUpdate {
			continue
		}
		showInCreate := e.inCreate && visibleInContext(f, "create")
		showInUpdate := e.inUpdate && visibleInContext(f, "update")
		if !showInCreate && !showInUpdate {
			continue
		}
		guardOpen := ""
		guardClose := ""
		if both {
			if showInCreate && !showInUpdate {
				guardOpen = "                if data.IsCreate {\n"
				guardClose = "                }\n"
			} else if !showInCreate && showInUpdate {
				guardOpen = "                if !data.IsCreate {\n"
				guardClose = "                }\n"
			}
		}
		inputs.WriteString(guardOpen)

		label := f.Label
		if label == "" {
			label = f.Name
		}

		inputs.WriteString(fmt.Sprintf(`            <div>
                <label for="%s" class="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">%s</label>
`, f.Name, label))

		if g.isPickerField(r, f) {
			inputs.WriteString(pickerMarkup(f, label))
		} else {
			switch f.Type {
			case "text":
				inputs.WriteString(fmt.Sprintf(`                <textarea id="%s" name="%s" rows="3" class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2">{ viewmodels.ItemValue(data.Item, %q) }</textarea>
`, f.Name, f.Name, f.Name))
			case "password":
				inputs.WriteString(fmt.Sprintf(`                <input type="password" id="%s" name="%s" class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name))
			case "email":
				inputs.WriteString(fmt.Sprintf(`                <input type="email" id="%s" name="%s" value={ viewmodels.ItemValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name, f.Name))
			case "select":
				inputs.WriteString(fmt.Sprintf(`                <select id="%s" name="%s" class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2">
                    <option value="">Select...</option>
                    for _, fd := range data.Fields {
                        if fd.Name == %q {
                            for key, label := range fd.Options {
                                <option value={ key } if viewmodels.OptionValue(data.Item[%q]) == key { selected }>{ label }</option>
                            }
                        }
                    }
                </select>
`, f.Name, f.Name, f.Name, f.Name))
			case "boolean":
				inputs.WriteString(fmt.Sprintf(`                <input type="checkbox" id="%s" name="%s" value="true" class="rounded border-gray-300 dark:border-gray-600 text-brand-primary focus:ring-brand-primary"
                    if viewmodels.BoolValue(data.Item[%q]) {
                        checked
                    }
                />
`, f.Name, f.Name, f.Name))
			case "integer", "float":
				inputs.WriteString(fmt.Sprintf(`                <input type="number" id="%s" name="%s" value={ viewmodels.ItemValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name, f.Name))
			case "datetime":
				inputs.WriteString(fmt.Sprintf(`                <input type="datetime-local" id="%s" name="%s" value={ viewmodels.TimeInputValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name, f.Name))
			case "date":
				inputs.WriteString(fmt.Sprintf(`                <input type="date" id="%s" name="%s" value={ viewmodels.DateInputValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name, f.Name))
			case "file":
				inputs.WriteString(fmt.Sprintf(`                <input type="file" id="%s" name="%s" class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name))
			case "image":
				inputs.WriteString(fmt.Sprintf(`                <input type="file" id="%s" name="%s" accept="image/*" class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name))
			case "badge":
				inputs.WriteString(fmt.Sprintf(`                <input type="text" id="%s" name="%s" value={ viewmodels.ItemValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" placeholder="badge value" />
`, f.Name, f.Name, f.Name))
			case "relation":
				inputs.WriteString(fmt.Sprintf(`                <input type="text" id="%s" name="%s" value={ viewmodels.ItemValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" placeholder="related ID" />
`, f.Name, f.Name, f.Name))
			case "json":
				inputs.WriteString(fmt.Sprintf(`                <textarea id="%s" name="%s" rows="5" class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2 font-mono text-xs">{ viewmodels.ItemValue(data.Item, %q) }</textarea>
`, f.Name, f.Name, f.Name))
			case "gps":
				inputs.WriteString(fmt.Sprintf(`                <input type="text" id="%s" name="%s" value={ viewmodels.ItemValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" placeholder="lat, lng" />
`, f.Name, f.Name, f.Name))
			default:
				inputs.WriteString(fmt.Sprintf(`                <input type="text" id="%s" name="%s" value={ viewmodels.ItemValue(data.Item, %q) } class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
`, f.Name, f.Name, f.Name))
			}
		}

		if f.Required {
			inputs.WriteString(`                <p class="text-xs text-red-500 mt-1">Required</p>
`)
		}
		if f.Validation != nil {
			if f.Validation.Min > 0 || f.Validation.Max > 0 {
				inputs.WriteString(fmt.Sprintf(`                <p class="text-xs text-gray-500 dark:text-gray-400 mt-1">Min: %d, Max: %d</p>
`, f.Validation.Min, f.Validation.Max))
			}
		}

		inputs.WriteString("            </div>\n")
		inputs.WriteString(guardClose)
	}

	hasFile := false
	for _, e := range merged {
		if e.f.Type == "file" || e.f.Type == "image" {
			hasFile = true
			break
		}
	}

	hasPicker := false
	for _, e := range merged {
		if g.isPickerField(r, e.f) {
			hasPicker = true
			break
		}
	}

	enctype := ""
	if hasFile {
		enctype = ` enctype="multipart/form-data"`
	}

	listPath := fmt.Sprintf("%s/%s", panelPath, resLower)

	footerStr := ""
	if hasPicker {
		footerStr = pickerFooter()
	}

	// D14 master-detail: the shared form carries a hidden _return field so a
	// child POST redirects back to the header edit; the edit form embeds the
	// child-lines table below the header fields, and create shows a note.
	hasParentCtx := len(r.Children) > 0
	if !hasParentCtx && r.Form != nil {
		for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update} {
			if fa == nil {
				continue
			}
			for _, f := range fa.Fields {
				if g.isPickerField(r, f) {
					hasParentCtx = true
					break
				}
			}
			if hasParentCtx {
				break
			}
		}
	}
	returnInput := ""
	if hasParentCtx {
		returnInput = `                <input type="hidden" name="_return" value={ data.Return } />
`
	}
	childCode := ""
	if len(r.Children) > 0 {
		childCode = `        if data.IsCreate {
            <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6 mt-4">
                <p class="text-sm text-gray-500 dark:text-gray-400">Save the header, then add lines.</p>
            </div>
        } else {
            if len(data.Lines) > 0 {
                for _, sec := range data.Lines {
` + childLinesSection(true) + `                }
            }
        }
`
	}

	code := fmt.Sprintf(`package views

import "internal/viewmodels"

templ %s(data *viewmodels.FormData) {
    <div class="p-6">
        <div class="flex items-center justify-between mb-6">
            <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">
                if data.IsCreate {
                    Create %s
                } else {
                    Edit %s
                }
            </h1>
            <a href="%s" class="text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100 px-4 py-2">Back</a>
        </div>

        <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6">
            <form action={ templ.SafeURL(data.Action) } method="POST"%s class="space-y-6">
                <input type="hidden" name="_csrf" value={ data.CSRFToken } />
%s%s                <div class="flex justify-end pt-4">
                    <button type="submit" class="bg-brand-primary text-white px-6 py-2 rounded-lg text-sm hover:opacity-90">
                        if data.IsCreate {
                            Create
                        } else {
                            Update
                        }
                    </button>
                </div>
            </form>
        </div>
%s    </div>
%s}
`, templName, resLabel, resLabel, listPath, enctype, returnInput, inputs.String(), childCode, footerStr)

	code = prefixImports(code, g.moduleImport("internal/viewmodels"))

	return os.WriteFile(filepath.Join(dir, "form.templ"), []byte(code), 0644)
}

// fieldIn reports whether a field named name appears in the given field list.
func fieldIn(fields []types.Field, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// visibleInContext reports whether a field should render in the given form
// context ("create" or "update"). A field with no visible list renders in both
// contexts; otherwise it renders only in the listed contexts.
func visibleInContext(f types.Field, context string) bool {
	if len(f.Visible) == 0 {
		return true
	}
	for _, v := range f.Visible {
		if v == context {
			return true
		}
	}
	return false
}

// isPickerField reports whether a form field should render as a modal record
// picker: a select/relation field with a runtime option loader. The loader SQL
// resolves via optionSQL (options_sql, then the schema block's FK metadata,
// then legacy options_query), so this must agree with buildOptionsLoader.
func (g *Generator) isPickerField(r types.Resource, f types.Field) bool {
	if f.Type != "select" && f.Type != "relation" {
		return false
	}
	return g.optionSQL(r, f) != ""
}

// pickerMarkup renders the options data (as a data attribute), the hidden
// input + read-only display row + "Browse" button for a picker field, and a
// per-field script that fills the shared modal (emitted once at the end of the
// form) from the field's current option set and wires row-click selection.
// Rows are rendered client-side from the JSON-encoded options map; the search
// box filters rows by label. The options expression must live in a data
// attribute, not inside the <script>, because templ treats script content as
// raw text and never evaluates expressions there.
func pickerMarkup(f types.Field, label string) string {
	display := fmt.Sprintf(`viewmodels.OptionLabel(data.Fields, %q, viewmodels.ItemValue(data.Item, %q))`, f.Name, f.Name)
	copiesAttr := ""
	if len(f.Copies) > 0 {
		copiesAttr = fmt.Sprintf(` data-picker-copies={viewmodels.CopyDataJSON(data.Fields, %q)}`, f.Name)
	}
	return fmt.Sprintf(`                <div hidden data-field="%[1]s" data-picker-options={ viewmodels.OptionsJS(data.Fields, %[1]q) }%[2]s></div>
                <input type="hidden" id="%[1]s" name="%[1]s" value={ viewmodels.ItemValue(data.Item, %[1]q) } />
                <div class="flex items-center gap-2">
                    <input type="text" id="%[1]s-display" value={ %[3]s } readonly class="flex-1 w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2 bg-gray-50 dark:bg-gray-700" />
                    if !viewmodels.FieldLocked(data.Fields, %[1]q) {
                        <button type="button" data-picker-open="%[1]s" class="whitespace-nowrap border border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-300 px-3 py-2 rounded-lg text-sm hover:bg-gray-50 dark:hover:bg-gray-700">Browse…</button>
                    }
                </div>
                if !viewmodels.FieldLocked(data.Fields, %[1]q) {
                    <script data-picker="%[1]s">
                        (function() {
                        const pickerBtn = document.querySelector('[data-picker-open="%[1]s"]');
                        const pickerHidden = document.getElementById('%[1]s');
                        const pickerDisplay = document.getElementById('%[1]s-display');
                        const pickerOptionsEl = document.querySelector('[data-picker-options][data-field="%[1]s"]');
                        const pickerCopiesEl = document.querySelector('[data-picker-copies][data-field="%[1]s"]');
                        if (pickerBtn) {
                            pickerBtn.addEventListener('click', () => {
                                const pickerModal = document.getElementById('record-picker-modal');
                                if (!pickerModal) return;
                                let opts = {};
                                try {
                                    opts = JSON.parse(pickerOptionsEl ? pickerOptionsEl.dataset.pickerOptions : '{}');
                                } catch (e) {
                                    opts = {};
                                }
                                let copyData = {};
                                try {
                                    copyData = JSON.parse(pickerCopiesEl ? pickerCopiesEl.dataset.pickerCopies : '{}');
                                } catch (e) {
                                    copyData = {};
                                }
                                let html = '<tr><td class="px-4 py-2 text-gray-500 dark:text-gray-400 text-sm">No matches</td></tr>';
                                const keys = Object.keys(opts).sort();
                                if (keys.length > 0) {
                                    html = keys.map(k => {
                                        const v = opts[k] == null ? k : String(opts[k]);
                                        return '<tr class="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700" data-value="' + k.replace(/"/g, '&quot;') + '"><td class="px-4 py-2 text-sm text-gray-700 dark:text-gray-300">' + v + '</td></tr>';
                                    }).join('');
                                }
                                pickerModal.querySelector('[data-picker-rows]').innerHTML = html;
                                pickerModal.querySelector('[data-picker-title]').textContent = 'Pick ' + %[4]q;
                                const modalList = pickerModal.querySelector('[data-picker-list]');
                                if (modalList) modalList.value = '';
                                pickerModal.querySelectorAll('tr[data-value]').forEach(row => {
                                    row.style.display = '';
                                    row.addEventListener('click', () => {
                                        pickerHidden.value = row.dataset.value;
                                        pickerDisplay.value = opts[row.dataset.value] == null ? row.dataset.value : String(opts[row.dataset.value]);
                                        const rowCopies = copyData[row.dataset.value];
                                        if (rowCopies) {
                                            Object.keys(rowCopies).forEach(t => {
                                                const tEl = document.querySelector('[name="' + t + '"]');
                                                if (tEl) tEl.value = rowCopies[t] == null ? '' : String(rowCopies[t]);
                                            });
                                        }
                                        pickerModal.classList.add('hidden');
                                    });
                                });
                                pickerModal.classList.remove('hidden');
                            });
                        }
                    })();
                    </script>
                }
`, f.Name, copiesAttr, display, label)
}

// childLinesSection renders one master-detail children section (D14) inside a
// detail or edit template. sec is the template loop variable bound to a
// viewmodels.ChildLinesData. withActions adds per-row Edit + Delete links (and
// an "Add Line" button) for the header's edit form; without actions only a
// "View" link to the child's detail is shown. Reference style only — the
// prefixed class names are all stock Tailwind, so TestGenerateStylesEmbedded
// stays green without a rebuild.
func childLinesSection(withActions bool) string {
	head := ""
	rowActions := `                                    <a href={ templ.SafeURL(fmt.Sprintf("%s/%s/%s", sec.PanelPath, sec.ResourceLower, viewmodels.Stringify(row[sec.IDColumn]))) } class="text-brand-primary hover:opacity-90">View</a>`
	if withActions {
		head = `                <div class="mb-3">
                    <a href={ templ.SafeURL(fmt.Sprintf("%s/%s/new?%s=%s", sec.PanelPath, sec.ResourceLower, sec.FKColumn, sec.ParentID)) } class="bg-brand-primary text-white px-4 py-2 rounded-lg text-sm hover:opacity-90">Add Line</a>
                </div>
`
		rowActions = `                                    <div class="flex items-center gap-2">
                                        <a href={ templ.SafeURL(fmt.Sprintf("%s/%s/%s/edit?%s=%s&return=%s", sec.PanelPath, sec.ResourceLower, viewmodels.Stringify(row[sec.IDColumn]), sec.FKColumn, sec.ParentID, sec.ReturnURL)) } class="text-brand-primary hover:opacity-90">Edit</a>
                                        <form action={ templ.SafeURL(fmt.Sprintf("%s/%s/%s/delete?return=%s", sec.PanelPath, sec.ResourceLower, viewmodels.Stringify(row[sec.IDColumn]), sec.ReturnURL)) } method="POST" class="inline" onsubmit="return confirm('Delete this line?')">
                                            <input type="hidden" name="_csrf" value={ sec.CSRFToken } />
                                            <button type="submit" class="text-red-600 hover:opacity-90">Delete</button>
                                        </form>
                                    </div>`
	}
	return head + `            <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6 mt-4">
                <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100 mb-3">{ sec.Heading }</h2>
                <div class="overflow-x-auto">
                    <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                        <thead class="bg-gray-50 dark:bg-gray-700">
                            <tr>
                                for _, fd := range sec.Fields {
                                    <th class="px-4 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">{ fd.Label }</th>
                                }
                                <th class="px-4 py-3"></th>
                            </tr>
                        </thead>
                        <tbody class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                            for _, row := range sec.Rows {
                                <tr>
                                    for _, fd := range sec.Fields {
                                        <td class="px-4 py-2 text-sm text-gray-700 dark:text-gray-300">{ viewmodels.Stringify(row[fd.Name]) }</td>
                                    }
                                    <td class="px-4 py-2 whitespace-nowrap text-right text-sm">
` + rowActions + `
                                    </td>
                                </tr>
                            }
                        </tbody>
                    </table>
                </div>
            </div>
`
}

// pickerFooter is the shared modal markup + wiring emitted at the end of a
// form that contains at least one picker field. The modal is hidden by
// default; each picker field's script populates its tbody and opens it. The
// wiring handles the modal's own search filtering, close button and
// click-outside dismissal.
func pickerFooter() string {
	return `
    <div id="record-picker-modal" class="hidden fixed inset-0 z-50 flex items-start justify-center bg-black bg-opacity-50 p-6">
        <div class="bg-white dark:bg-gray-800 rounded-lg shadow-xl w-full max-w-lg flex flex-col max-h-[80vh]">
            <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-gray-700">
                <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100" data-picker-title>Pick...</h2>
                <button type="button" data-picker-close class="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 text-2xl leading-none">&times;</button>
            </div>
            <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-700">
                <input type="text" data-picker-list placeholder="Search..." class="w-full rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
            </div>
            <div class="flex-1 overflow-y-auto">
                <table class="w-full">
                    <tbody data-picker-rows></tbody>
                </table>
            </div>
            <div class="px-4 py-3 border-t border-gray-200 dark:border-gray-700 flex justify-end">
                <button type="button" data-picker-close class="border border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-300 px-4 py-2 rounded-lg text-sm hover:bg-gray-50 dark:hover:bg-gray-700">Cancel</button>
            </div>
        </div>
    </div>
    <script>
        const gfPickerModal = document.getElementById('record-picker-modal');
        if (gfPickerModal) {
            const gfPickerList = gfPickerModal.querySelector('[data-picker-list]');
            const gfPickerRows = gfPickerModal.querySelector('[data-picker-rows]');
            const gfPickerClose = gfPickerModal.querySelectorAll('[data-picker-close]');
            if (gfPickerList) {
                gfPickerList.addEventListener('input', () => {
                    const q = gfPickerList.value.trim().toLowerCase();
                    gfPickerRows.querySelectorAll('tr[data-value]').forEach(row => {
                        const txt = (row.textContent || '').toLowerCase();
                        row.style.display = txt.indexOf(q) !== -1 ? '' : 'none';
                    });
                });
            }
            gfPickerClose.forEach(btn => {
                btn.addEventListener('click', () => gfPickerModal.classList.add('hidden'));
            });
            gfPickerModal.addEventListener('click', (e) => {
                if (e.target === gfPickerModal) gfPickerModal.classList.add('hidden');
            });
        }
    </script>
`
}

// generateCardTempl writes cards.templ for a resource: a card grid view (or a
// kanban board when data.Kanban is true). Each card renders the configured
// fields stacked vertically with per-field renderers; the grid uses Tailwind's
// responsive columns so `data.Columns` cards fit per row. Grid mode shows the
// shared search bar and pagination; kanban mode renders columns side by side
// with the search bar only.
// Params: dir (view directory), r (the resource definition).
// Returns: an error on write failure.
func (g *Generator) generateCardTempl(dir string, r types.Resource) error {
	fields := append([]types.Field{}, r.Card.Fields...)
	fields = append(fields, computedFields(r.Card.Computed)...)
	templName := r.Name + "Cards"
	resLabel := r.Label
	resLower := strings.ToLower(r.Name)
	panelPath := g.Config.Panel.Path
	idCol := idColumn(r)

	var cardBody strings.Builder
	for _, f := range fields {
		label := f.Label
		if label == "" {
			label = f.Name
		}
		rendered := renderCell(f.Type, fmt.Sprintf(`item[%q]`, f.Name))
		cardBody.WriteString(fmt.Sprintf(`                    <div class="mb-2">
                        <span class="block text-xs font-medium text-gray-500 dark:text-gray-400">%s</span>
                        %s
                    </div>
`, label, rendered))
	}

	cardView := ""
	if r.Detail != nil {
		cardView = fmt.Sprintf(`                        <a href={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v", %q, %q, item[%q])) } class="text-brand-primary hover:text-brand-primary/80 text-sm">View</a>
`, panelPath, resLower, idCol)
	}
	cardEdit := ""
	if r.Form != nil && r.Form.Update != nil {
		cardEdit = fmt.Sprintf(`                        <a href={ templ.SafeURL(fmt.Sprintf("%%s/%%s/%%v/edit", %q, %q, item[%q])) } class="text-brand-primary hover:text-brand-primary/80 text-sm">Edit</a>
`, panelPath, resLower, idCol)
	}
	actions := fmt.Sprintf(`                    <div class="flex gap-2 border-t pt-3 mt-3">
%s%s                    </div>
`, cardView, cardEdit)

	kanbanField := ""
	if r.Card.KanbanField != "" {
		kanbanField = r.Card.KanbanField
	}

	// Header button: a "Create" link to the create form when configured,
	// otherwise a "View List" link back to the resource list.
	headerBtnURL := fmt.Sprintf("%s/%s", panelPath, resLower)
	headerBtnLabel := "View List"
	if r.Form != nil && r.Form.Create != nil {
		headerBtnURL = fmt.Sprintf("%s/%s/new", panelPath, resLower)
		headerBtnLabel = fmt.Sprintf("Create %s", resLabel)
	}
	headerBtn := fmt.Sprintf(`<a href="%s" class="bg-brand-primary text-white px-4 py-2 rounded-lg text-sm hover:opacity-90">%s</a>`, headerBtnURL, headerBtnLabel)

	gridView := fmt.Sprintf(`                <div class="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-%d">
                    for _, item := range data.Items {
                        <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-4 border border-gray-200 dark:border-gray-700">
%s
%s                    </div>
                    }
                </div>
`, r.Card.Columns, cardBody.String(), actions)

	kanbanView := ""
	if kanbanField != "" {
		kanbanView = fmt.Sprintf(`                <div class="flex gap-4 overflow-x-auto pb-4">
                    for _, col := range data.KanbanColumns {
                        <div class="w-72 flex-shrink-0 bg-gray-50 dark:bg-gray-900 rounded-lg p-3 border border-gray-200 dark:border-gray-700">
                            <div class="flex items-center justify-between mb-3">
                                <span class="text-sm font-medium text-gray-700 dark:text-gray-200">{ col.Label }</span>
                                <span class="text-xs text-gray-500 dark:text-gray-400">{ fmt.Sprintf("%%d", len(col.Items)) }</span>
                            </div>
                            for _, item := range col.Items {
                                <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-4 mb-3 border border-gray-200 dark:border-gray-700">
%s
%s                                </div>
                            }
                        </div>
                    }
                </div>
`, cardBody.String(), actions)
	}

	code := fmt.Sprintf(`package views

import "internal/viewmodels"

templ %s(data *viewmodels.CardData) {
    <div class="p-6">
        <div class="flex items-center justify-between mb-6">
            <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">%s</h1>
            <div class="flex gap-2 items-center">
                <a href="%s/%s" class="text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100 px-4 py-2 text-sm">Back</a>
                %s
            </div>
        </div>

        @filterBar(data.Filter, data.Search, data.Sort, data.Order)

        @searchBar(data.Search, data.Resource)

        %s

        if !data.Kanban {
            @pagination(data.Page, data.TotalPages, data.Total, data.Search, data.Sort, data.Order, data.FilterQS)
        }
    </div>
}
`, templName, resLabel, panelPath, resLower, headerBtn, gridView)
	if kanbanView != "" {
		code = fmt.Sprintf(`package views

import "internal/viewmodels"

templ %s(data *viewmodels.CardData) {
    <div class="p-6">
        <div class="flex items-center justify-between mb-6">
            <h1 class="text-2xl font-bold text-gray-900 dark:text-gray-100">%s</h1>
            <div class="flex gap-2 items-center">
                <a href="%s/%s" class="text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100 px-4 py-2 text-sm">Back</a>
                %s
            </div>
        </div>

        @filterBar(data.Filter, data.Search, data.Sort, data.Order)

        @searchBar(data.Search, data.Resource)

        if data.Kanban {
%s        } else {
%s        }
    </div>
}
`, templName, resLabel, panelPath, resLower, headerBtn, kanbanView, gridView)
	}
	code = prefixImports(code, g.moduleImport("internal/viewmodels"))

	return os.WriteFile(filepath.Join(dir, "cards.templ"), []byte(code), 0644)
}

// generatePageWidgets writes internal/views/pages/widgets.templ containing the
// templ components shared by every page view (widget, statWidget, iconSVG).
// The shared file is written once regardless of how many pages are configured;
// per-page view files only declare their page template and reference these
// components. The stats_grid column count is baked in from the first page that
// declares a stats_grid widget (defaulting to 4).
// Returns: an error on write failure.
func (g *Generator) generatePageWidgets() error {
	viewDir := filepath.Join(g.OutDir, "internal/views/pages")
	gridCols := 4
	for _, p := range g.Config.Pages {
		if c := g.detectGridColumns(p.Widgets); c != 4 {
			gridCols = c
			break
		}
	}
	code := fmt.Sprintf(`package views

import (
    "internal/viewmodels"
    "fmt"
)

templ widget(w viewmodels.WidgetData) {
    switch w.Type {
    case "stats_grid":
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-%d gap-4 mb-6">
            for _, sw := range w.SubWidgets {
                @statWidget(sw)
            }
        </div>
    case "stat":
        @statWidget(w)
    case "chart":
        <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6 mb-6">
            <h3 class="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">{ w.Label }</h3>
            <canvas id={ fmt.Sprintf("chart-%%s", w.Label) } class="w-full h-64"
                data-chart-type={ w.ChartType }
                data-labels={ w.ChartLabelsJSON }
                data-values={ w.ChartValuesJSON }>
            </canvas>
        </div>
    case "table":
        <div class="bg-white dark:bg-gray-800 shadow rounded-lg overflow-hidden mb-6">
            <div class="px-6 py-4 border-b border-gray-200 dark:border-gray-700">
                <h3 class="text-lg font-semibold text-gray-900 dark:text-gray-100">{ w.Label }</h3>
            </div>
            <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                <thead class="bg-gray-50 dark:bg-gray-900">
                    <tr>
                        for _, col := range w.TableColumns {
                        <th class="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">{ col }</th>
                        }
                    </tr>
                </thead>
                <tbody class="bg-white dark:bg-gray-800 divide-y divide-gray-200 dark:divide-gray-700">
                    for _, row := range w.TableRows {
                    <tr class="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                        for _, col := range w.TableColumns {
                        <td class="px-6 py-4 whitespace-nowrap text-sm text-gray-900 dark:text-gray-100">{ fmt.Sprintf("%%v", row[col]) }</td>
                        }
                    </tr>
                    }
                </tbody>
            </table>
        </div>
    case "list":
        <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6 mb-6">
            <h3 class="text-lg font-semibold mb-4 text-gray-900 dark:text-gray-100">{ w.Label }</h3>
            <ul class="divide-y divide-gray-200 dark:divide-gray-700">
                for _, row := range w.TableRows {
                <li class="py-3 flex items-center justify-between">
                    <span class="text-sm text-gray-900 dark:text-gray-100">{ fmt.Sprintf("%%v", row["label"]) }</span>
                    <span class="text-sm text-gray-500 dark:text-gray-400">{ fmt.Sprintf("%%v", row["value"]) }</span>
                </li>
                }
            </ul>
        </div>
    case "html":
        <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6 mb-6 text-gray-900 dark:text-gray-100">
            @templ.Raw(string(w.Value))
        </div>
    }
}

templ statWidget(w viewmodels.WidgetData) {
    <div class="bg-white dark:bg-gray-800 shadow rounded-lg p-6">
        <div class="flex items-center justify-between">
            <div>
                if w.Icon != "" {
                <div class="w-10 h-10 rounded-lg bg-brand-primary/10 flex items-center justify-center mb-3">
                    @iconSVG(w.Icon)
                </div>
                }
                <p class="text-sm text-gray-500 dark:text-gray-400">{ w.Label }</p>
                <p class="text-2xl font-bold text-gray-900 dark:text-gray-100">
                    if w.Prefix != "" {
                        <span class="text-lg">{ w.Prefix }</span>
                    }
                    @templ.Raw(string(w.Value))
                    if w.Suffix != "" {
                        <span class="text-lg">{ w.Suffix }</span>
                    }
                </p>
            </div>
        </div>
    </div>
}

templ iconSVG(name string) {
    switch name {
    case "users":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
        </svg>
    case "chart":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
        </svg>
    case "dollar":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
    case "check":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
        </svg>
    case "clock":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
    case "cog":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
        </svg>
    case "bell":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
        </svg>
    case "home":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
        </svg>
    case "mail":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
        </svg>
    case "lock":
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
        </svg>
    default:
        <svg class="w-6 h-6 text-brand-primary" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
    }
}
`, gridCols)
	code = prefixImports(code, g.moduleImport("internal/viewmodels"))

	return os.WriteFile(filepath.Join(viewDir, "widgets.templ"), []byte(code), 0644)
}

// generatePageViews writes one templ view per page into internal/views/pages.
// Each file only declares its page template; the shared widget/statWidget/
// iconSVG components live in widgets.templ (see generatePageWidgets).
// Params: p (the page definition).
// Returns: an error on write failure.
func (g *Generator) generatePageViews(p types.Page) error {
	viewDir := filepath.Join(g.OutDir, "internal/views/pages")
	panelID := g.Config.Panel.ID

	capitalID := strings.ToUpper(panelID[:1]) + panelID[1:]
	templName := capitalID + pageIdent(p.Name)
	code := fmt.Sprintf(`package views

import (
    "internal/viewmodels"
)

templ %s(data *viewmodels.PageData) {
    <div class="p-6">
        <h1 class="text-2xl font-bold mb-6 text-gray-900 dark:text-gray-100">{ data.Name }</h1>
        for _, w := range data.Widgets {
            @widget(w)
        }
    </div>
}
`, templName)
	code = prefixImports(code, g.moduleImport("internal/viewmodels"))

	return os.WriteFile(filepath.Join(viewDir, pageIdent(p.Name)+".templ"), []byte(code), 0644)
}

// detectGridColumns finds the column count of the first stats_grid widget on a
// page, used to size the generated grid layout. Defaults to 4 when no
// stats_grid widget declares columns.
// Params: widgets (the page's widget list).
// Returns: the number of grid columns to render.
func (g *Generator) detectGridColumns(widgets []types.Widget) int {
	for _, w := range widgets {
		if w.Type == "stats_grid" && w.Columns > 0 {
			return w.Columns
		}
	}
	return 4
}

// generateLayoutViews writes base.templ into internal/views/layout: the Base
// layout document (with the vendored Chart.js script and auto-rendering JS), the
// sidebar with the navigation groups sorted by their sort value, the topbar
// with the logout link, and the iconNav SVG helper.
// Returns: an error on write failure.
func (g *Generator) generateLayoutViews() error {
	dir := filepath.Join(g.OutDir, "internal/views/layout")
	panelPath := g.Config.Panel.Path
	panelName := g.Config.Panel.Name

	sortedNav := make([]types.NavigationGroup, len(g.Config.Navigation))
	copy(sortedNav, g.Config.Navigation)
	sort.Slice(sortedNav, func(i, j int) bool {
		return sortedNav[i].Sort < sortedNav[j].Sort
	})

	var sidebarNav strings.Builder
	for _, ng := range sortedNav {
		sidebarNav.WriteString(fmt.Sprintf(`            <div class="px-4 py-2 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wider mt-4">@iconNav(%q) %s</div>
`, ng.Icon, ng.Group))
		for _, item := range ng.Items {
			if item.Resource != "" {
				label := item.Resource
				sidebarNav.WriteString(fmt.Sprintf(`            <a href="%s/%s" class="block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-brand-primary/10 hover:text-brand-primary mx-2 rounded-md">%s</a>
`, panelPath, strings.ToLower(item.Resource), label))
			}
			if item.Page != "" {
				pagePath := item.Page
				for _, pg := range g.Config.Pages {
					if pg.Name == item.Page {
						pagePath = pg.Path
						if pagePath == "" {
							pagePath = "/" + pageIdent(item.Page)
						}
						break
					}
				}
				sidebarNav.WriteString(fmt.Sprintf(`            <a href="%s%s" class="block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-brand-primary/10 hover:text-brand-primary mx-2 rounded-md">%s</a>
`, panelPath, pagePath, item.Page))
			}
			if item.Type == "link" {
				target := ""
				if item.OpensInNewTab {
					target = ` target="_blank"`
				}
				sidebarNav.WriteString(fmt.Sprintf(`            <a href="%s"%s class="block px-4 py-2 text-sm text-gray-700 dark:text-gray-300 hover:bg-brand-primary/10 hover:text-brand-primary mx-2 rounded-md">%s</a>
`, item.URL, target, item.Label))
			}
		}
	}

	// Theme/layout values interpolated directly into the templ source and the
	// inline scripts (the ThemeConfig struct drives classes; the generator
	// knows the same values for the JS defaults).
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
	darkDefault := "false"
	if g.Config.Panel.Theme.DarkMode {
		darkDefault = "true"
	}
	stickyClass := "relative"
	if g.Config.Panel.Layout.Topbar.Sticky {
		stickyClass = "sticky top-0 z-10"
	}

	code := fmt.Sprintf(`package views

import (
    "fmt"

    "internal/viewmodels"
)

func darkClass(theme viewmodels.ThemeConfig) string {
    if theme.DarkMode {
        return "dark"
    }
    return ""
}

templ Base(title string, panelPath string, theme viewmodels.ThemeConfig, userName string, csrfToken string, children templ.Component) {
    <!DOCTYPE html>
    <html lang="en" class={ darkClass(theme) }>
    <head>
        <meta charset="UTF-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>{ title }</title>
        <link href="/static/css/styles.css" rel="stylesheet" />
        <script src="/static/js/chart.js"></script>
        <style>
            :root {
                --brand-primary: {theme.BrandPrimary};
                --brand-secondary: {theme.BrandSecondary};
                --brand-primary-rgb: { viewmodels.BrandChannels(theme.BrandPrimary) };
                --brand-secondary-rgb: { viewmodels.BrandChannels(theme.BrandSecondary) };
            }%s
        </style>
    </head>
    <body class="bg-gray-50 dark:bg-gray-900">
        <div class="flex h-screen">
            @Sidebar(panelPath, theme)
            <div class="flex-1 flex flex-col min-w-0">
                @Topbar(panelPath, theme, userName, csrfToken)
                if flash := viewmodels.FlashMessage(ctx); flash != "" {
                <div class="bg-green-100 dark:bg-green-900/50 border-b border-green-200 dark:border-green-800 px-6 py-2 text-sm text-green-800 dark:text-green-200">{ flash }</div>
                }
                <main class="flex-1 overflow-y-auto p-6">
                    <div class={ fmt.Sprintf("max-w-%%s mx-auto", theme.MaxContentWidth) }>
                        @children
                    </div>
                </main>
            </div>
        </div>
        <script>
            function toggleSidebar() {
                var aside = document.getElementById('app-sidebar');
                if (!aside) { return; }
                aside.style.display = aside.style.display === 'none' ? '' : 'none';
            }
            function toggleSelectAll(checkbox) {
                var form = checkbox.closest('form');
                if (!form) { return; }
                var checkboxes = form.querySelectorAll('input[name="ids"]');
                for (var i = 0; i < checkboxes.length; i++) {
                    checkboxes[i].checked = checkbox.checked;
                }
            }
            function toggleTheme() {
                var html = document.documentElement;
                html.classList.toggle('dark');
                localStorage.setItem('yaga-theme', html.classList.contains('dark') ? 'dark' : 'light');
            }
            (function() {
                var html = document.documentElement;
                var saved = localStorage.getItem('yaga-theme');
                if (saved === 'dark') { html.classList.add('dark'); }
                else if (saved === 'light') { html.classList.remove('dark'); }
                else if (%s) { html.classList.add('dark'); }
            })();
            (function() {
                var aside = document.getElementById('app-sidebar');
                if (aside) { aside.style.width = aside.getAttribute('data-width') + 'px'; }
            })();
            // Auto-render Chart.js canvases
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
    </html>
}

templ iconNav(name string) {
    switch name {
    case "users":
        <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/></svg>
    case "chart":
        <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/></svg>
    case "cog":
        <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/></svg>
    case "home":
        <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>
    case "clock":
        <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
    case "mail":
        <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/></svg>
    default:
        <svg class="w-4 h-4 inline mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
    }
}

templ Sidebar(panelPath string, theme viewmodels.ThemeConfig) {
    <aside id="app-sidebar" data-width={ fmt.Sprintf("%%d", theme.SidebarWidth) } class="bg-white dark:bg-gray-800 border-r border-gray-200 dark:border-gray-700 shadow-md h-screen overflow-y-auto shrink-0">
        <div class="p-4 border-b border-gray-200 dark:border-gray-700">
            <h1 class="text-xl font-bold text-gray-900 dark:text-gray-100">%s</h1>
        </div>
        <nav class="mt-2">
%s        </nav>
    </aside>
}

templ Topbar(panelPath string, theme viewmodels.ThemeConfig, userName string, csrfToken string) {
    <header class="bg-white dark:bg-gray-800 shadow-sm px-6 py-3 flex items-center justify-between %s">
        <div class="flex items-center gap-4">
            if theme.SidebarCollapsible {
            <button class="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" onclick="toggleSidebar()" title="Toggle navigation">
                <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
                </svg>
            </button>
            }
            <button class="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" onclick="toggleTheme()" title="Toggle dark mode">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
                </svg>
            </button>
        </div>
        <div class="flex items-center gap-4">
            if userName != "" {
            <span class="text-sm text-gray-700 dark:text-gray-300">{ userName }</span>
            }
            <form action={ templ.SafeURL(fmt.Sprintf("%%s/logout", panelPath)) } method="POST" class="inline">
                <input type="hidden" name="_csrf" value={ csrfToken } />
                <button type="submit" class="text-sm text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-100">Logout</button>
            </form>
        </div>
    </header>
}
`, styleFonts, darkDefault, panelName, sidebarNav.String(), stickyClass)

	code = prefixImports(code, g.moduleImport("internal/viewmodels"))

	return os.WriteFile(filepath.Join(dir, "base.templ"), []byte(code), 0644)
}

// generateComponentViews writes renderers.templ into internal/views/components:
// the shared field renderers (badge, boolean, email, image, file, datetime,
// date, select, relation, json, float), the search bar, sort icon, sortOrder
// helper and pagination component.
// Returns: an error on write failure.
func (g *Generator) generateComponentViews() error {
	dir := filepath.Join(g.OutDir, "internal/views/components")
	return os.WriteFile(filepath.Join(dir, "renderers.templ"), []byte(prefixImports(renderersSource(), g.moduleImport("internal/viewmodels"))), 0644)
}

// renderersSource returns the templ source for the shared field renderers and
// utility components (search bar, sort icon, sortOrder helper, pagination).
// The same source is emitted into every resource view directory so each view
// package is self-contained.
// Returns: the templ source as a string.
func renderersSource() string {
	return `package views

import (
    "fmt"
    "internal/viewmodels"
)

// --- Field Renderers ---

templ renderBadge(value interface{}, color string) {
    {{ text := viewmodels.Stringify(value) }}
    if text != "" {
        {{ c := color }}
        if c == "" {
            {{ c = "gray" }}
        }
        <span class={ fmt.Sprintf("inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-%s-100 text-%s-800 dark:bg-%s-900/50 dark:text-%s-300", c, c, c, c) }>{ text }</span>
    }
}

templ renderBoolean(value interface{}) {
    {{ s := viewmodels.Stringify(value) }}
    if s == "true" {
        <span class="text-green-600">
            <svg class="w-5 h-5 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
            </svg>
        </span>
    } else if s == "false" {
        <span class="text-red-600">
            <svg class="w-5 h-5 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
        </span>
    }
}

templ renderEmail(value interface{}) {
    {{ email := viewmodels.Stringify(value) }}
    if email != "" {
        <a href={ templ.SafeURL(fmt.Sprintf("mailto:%s", email)) } class="text-brand-primary hover:text-brand-primary/80 underline">{ email }</a>
    }
}

templ renderImage(value interface{}) {
    {{ src := viewmodels.Stringify(value) }}
    if src != "" {
        <img src={ src } alt="" class="w-10 h-10 rounded-full object-cover" />
    }
}

templ renderFile(value interface{}) {
    {{ name := viewmodels.Stringify(value) }}
    if name != "" {
        <a href={ templ.SafeURL(name) } class="text-brand-primary hover:text-brand-primary/80 underline" download>{ name }</a>
    }
}

templ renderDateTime(value interface{}) {
    {{ t, ok := viewmodels.TimeValue(value) }}
    if ok {
        <span class="text-sm text-gray-600 dark:text-gray-300">{ t.Format("Jan 02, 2006 15:04") }</span>
    } else if s := viewmodels.Stringify(value); s != "" {
        <span class="text-sm text-gray-600 dark:text-gray-300">{ s }</span>
    }
}

templ renderDate(value interface{}) {
    {{ t, ok := viewmodels.TimeValue(value) }}
    if ok {
        <span class="text-sm text-gray-600 dark:text-gray-300">{ t.Format("Jan 02, 2006") }</span>
    } else if s := viewmodels.Stringify(value); s != "" {
        <span class="text-sm text-gray-600 dark:text-gray-300">{ s }</span>
    }
}

templ renderSelect(value interface{}) {
    {{ text := viewmodels.Stringify(value) }}
    if text != "" {
        <span class="text-sm text-gray-900 dark:text-gray-100">{ text }</span>
    }
}

templ renderRelation(value interface{}) {
    {{ text := viewmodels.Stringify(value) }}
    if text != "" {
        <a href="#" class="text-brand-primary hover:text-brand-primary/80 underline">{ text }</a>
    }
}

templ renderJSON(value interface{}) {
    {{ text := viewmodels.Stringify(value) }}
    if text != "" {
        <pre class="text-xs text-gray-600 dark:text-gray-300 bg-gray-50 dark:bg-gray-700 p-2 rounded overflow-x-auto max-w-xs">{ text }</pre>
    }
}

templ renderFloat(value interface{}) {
    {{ s := viewmodels.Stringify(value) }}
    if s != "" {
        if f, ok := value.(float64); ok {
            <span class="text-sm text-gray-900 dark:text-gray-100">{ fmt.Sprintf("%.2f", f) }</span>
        } else {
            <span class="text-sm text-gray-900 dark:text-gray-100">{ s }</span>
        }
    }
}

templ renderGPS(value interface{}) {
    {{ coords := viewmodels.Stringify(value) }}
    if coords != "" {
        <a href={ templ.SafeURL(fmt.Sprintf("https://www.google.com/maps?q=%s", coords)) } target="_blank" rel="noopener noreferrer" class="text-brand-primary hover:text-brand-primary/80 underline">{ coords }</a>
    }
}

templ actionIcon(name string) {
    <svg class="w-4 h-4 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        switch name {
        case "archive":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M7 8v10a2 2 0 002 2h6a2 2 0 002-2V8m-9 4h6" />
        case "play":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z" />
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        case "pause":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 9v6m4-6v6m7-3a9 9 0 11-18 0 9 9 0 0118 0z" />
        case "check":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
        case "trash":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
        case "download":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
        case "mail":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
        case "send":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
        case "star":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.196-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.783-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
        case "refresh":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
        case "x":
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
        default:
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
        }
    </svg>
}

// --- Utility Components ---

templ searchBar(query string, resource string) {
    <div class="mb-4">
        <form method="GET" class="flex gap-2">
            <input type="text" name="search" value={ query } placeholder="Search..." class="w-64 rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
            <button type="submit" class="bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 px-4 py-2 rounded-md text-sm hover:bg-gray-200 dark:hover:bg-gray-600 border dark:border-gray-600">Search</button>
            if query != "" {
                <a href="?" class="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 px-4 py-2 text-sm">Clear</a>
            }
        </form>
    </div>
}

templ sortIcon(sortField string, field string, order string) {
    if sortField == field {
        if order == "desc" {
            <svg class="w-4 h-4 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" />
            </svg>
        } else {
            <svg class="w-4 h-4 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
            </svg>
        }
    }
}

func sortOrder(currentSort string, field string, currentOrder string) string {
    if currentSort == field {
        if currentOrder == "asc" {
            return "desc"
        }
    }
    return "asc"
}

func filterPanelClass(f *viewmodels.FilterData) string {
    if f.Applied {
        return "mt-2 bg-gray-50 dark:bg-gray-900 border dark:border-gray-700 rounded-lg p-4"
    }
    return "mt-2 bg-gray-50 dark:bg-gray-900 border dark:border-gray-700 rounded-lg p-4 hidden"
}

templ filterBar(f *viewmodels.FilterData, search string, sort string, order string) {
    if f != nil {
        <div class="mb-4">
            <button type="button" onclick="document.getElementById('filter-panel').classList.toggle('hidden')" class="flex items-center gap-1 text-sm font-medium text-gray-700 dark:text-gray-200 border dark:border-gray-600 px-3 py-2 rounded-md hover:bg-gray-50 dark:hover:bg-gray-700">
                { f.Label }
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
                </svg>
            </button>
            <div id="filter-panel" class={ filterPanelClass(f) }>
                <form method="GET">
                    <input type="hidden" name="filter" value="1" />
                    <input type="hidden" name="search" value={ search } />
                    <input type="hidden" name="sort" value={ sort } />
                    <input type="hidden" name="order" value={ order } />
                    <div>
                        for _, p := range f.Params {
                            <div class="mb-2">
                                <label class="block text-xs font-medium text-gray-500 dark:text-gray-400">{ p.Label }</label>
                                <input type="text" name={ "fp_" + p.Key } value={ p.Value } placeholder={ p.Label } class="w-64 rounded-md border-gray-300 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 shadow-sm focus:border-brand-primary focus:ring-brand-primary sm:text-sm border px-3 py-2" />
                            </div>
                        }
                    </div>
                    <div class="flex gap-2">
                        <button type="submit" class="bg-brand-primary text-white px-4 py-2 rounded-md text-sm hover:opacity-90">Apply</button>
                        <a href="?" class="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 px-4 py-2 text-sm">Clear</a>
                    </div>
                </form>
            </div>
        </div>
    }
}

templ pagination(page int, totalPages int, total int, search string, sort string, order string, filterQS string) {
    if totalPages > 0 {
        <div class="bg-white dark:bg-gray-800 px-4 py-3 flex items-center justify-between border-t dark:border-gray-700">
            <div class="text-sm text-gray-700 dark:text-gray-300">
                Showing page { fmt.Sprintf("%d", page) } of { fmt.Sprintf("%d", totalPages) } ({ fmt.Sprintf("%d", total) } total)
            </div>
            <div class="flex gap-1">
                if page > 1 {
                    <a href={ templ.SafeURL(fmt.Sprintf("?page=%d&search=%s&sort=%s&order=%s%s", page-1, search, sort, order, filterQS)) } class="px-3 py-1 border rounded text-sm hover:bg-gray-50 dark:hover:bg-gray-700 dark:border-gray-600">Previous</a>
                }
                for i := 1; i <= totalPages; i++ {
                    if i == page {
                        <span class="px-3 py-1 border rounded text-sm bg-brand-primary text-white">{ fmt.Sprintf("%d", i) }</span>
                    } else {
                        <a href={ templ.SafeURL(fmt.Sprintf("?page=%d&search=%s&sort=%s&order=%s%s", i, search, sort, order, filterQS)) } class="px-3 py-1 border rounded text-sm hover:bg-gray-50 dark:hover:bg-gray-700 dark:border-gray-600">{ fmt.Sprintf("%d", i) }</a>
                    }
                }
                if page < totalPages {
                    <a href={ templ.SafeURL(fmt.Sprintf("?page=%d&search=%s&sort=%s&order=%s%s", page+1, search, sort, order, filterQS)) } class="px-3 py-1 border rounded text-sm hover:bg-gray-50 dark:hover:bg-gray-700 dark:border-gray-600">Next</a>
                }
            </div>
        </div>
    }
}
`
}
