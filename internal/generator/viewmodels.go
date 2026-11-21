// viewmodels.go
//
// Generates internal/viewmodels/models.go for the admin panel application:
// plain Go structs that carry data from the handlers into the templ views
// (list, detail, form, page and widget data).
package generator

import (
	"fmt"
	"os"
	"path/filepath"
)

// generateViewModels writes internal/viewmodels/models.go containing the view
// data structs (ColumnDef, ListData, DetailData, FormData, PageData,
// WidgetData, AuthData, NavGroupData, NavItemData, ThemeConfig) plus the
// DefaultTheme constructor that packs the panel's theming/layout config for the
// templ Base layout. Returns an error on write failure.
func (g *Generator) generateViewModels() error {
	panel := g.Config.Panel
	primary := panel.Brand.Colors.Primary
	if primary == "" {
		primary = "#6366f1"
	}
	secondary := panel.Brand.Colors.Secondary
	if secondary == "" {
		secondary = "#8b5cf6"
	}
	width := panel.Layout.Sidebar.Width
	if width == 0 {
		width = 256
	}
	collapsed := panel.Layout.Sidebar.CollapsedWidth
	if collapsed == 0 {
		collapsed = 64
	}
	maxWidth := panel.Layout.MaxContentWidth
	if maxWidth == "" {
		maxWidth = "none"
	}

	code := fmt.Sprintf(`package viewmodels

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "html/template"
    "time"
)

type flashKey struct{}

// SetFlash stashes a one-shot message (e.g. a CSV import result) in the request
// context so the Base layout can render it in the topbar area.
func SetFlash(ctx context.Context, msg string) context.Context {
    if msg == "" {
        return ctx
    }
    return context.WithValue(ctx, flashKey{}, msg)
}

// FlashMessage returns the flash message stashed in the context, if any.
func FlashMessage(ctx context.Context) string {
    if s, ok := ctx.Value(flashKey{}).(string); ok {
        return s
    }
    return ""
}

type ThemeConfig struct {
	DarkMode            bool
	BrandPrimary        string
	BrandSecondary      string
	FontFamily          string
	FontMono            string
	SidebarWidth        int
	SidebarCollapsedWidth int
	SidebarCollapsible  bool
	TopbarSticky        bool
	MaxContentWidth     string
}

func DefaultTheme() ThemeConfig {
    return ThemeConfig{
        DarkMode: %t,
        BrandPrimary: %q,
        BrandSecondary: %q,
        FontFamily: %q,
        FontMono: %q,
        SidebarWidth: %d,
        SidebarCollapsedWidth: %d,
        SidebarCollapsible: %t,
        TopbarSticky: %t,
        MaxContentWidth: %q,
    }
}

// BrandChannels converts a "#rrggbb" hex color into the "r g b" channel triplet
// the pre-built stylesheet expects. Brand colors are emitted as CSS variables of
// the form rgb(var(--brand-primary-rgb) / <alpha-value>) so opacity modifiers
// like bg-brand-primary/10 resolve at runtime; this helper feeds the -rgb
// variables from the same hex the --brand-* variables carry.
func BrandChannels(hex string) string {
    if len(hex) == 7 && hex[0] == '#' {
        var r, g, b int
        if _, err := fmt.Sscanf(hex[1:], "%%02x%%02x%%02x", &r, &g, &b); err == nil {
            return fmt.Sprintf("%%d %%d %%d", r, g, b)
        }
    }
    return "99 102 241"
}

type ColumnDef struct {
	Name       string
	Label      string
	FieldType  string
	Sortable   bool
	Searchable bool
	Picker     bool
	Locked     bool
	Options    map[string]string
	CopyData   map[string]map[string]string
}
`,
		panel.Theme.DarkMode, primary, secondary, panel.Theme.Font.Family, panel.Theme.Font.Mono,
		width, collapsed, panel.Layout.Sidebar.Collapsible, panel.Layout.Topbar.Sticky, maxWidth)

	code += `

type ListData struct {
	Items      []map[string]interface{}
	Page       int
	PerPage    int
	Total      int
	TotalPages int
	Search     string
	Sort       string
	Order      string
	Columns    []ColumnDef
	Resource   string
	PanelPath  string
	CSRFToken  string
	FilterQS   string
	Filter     *FilterData
	Applied    bool
}

type DetailData struct {
	Item      map[string]interface{}
	Fields    []ColumnDef
	Resource  string
	PanelPath string
	CSRFToken string
	Lines     []ChildLinesData
}

// ChildLinesData describes one master-detail section on a header resource
// (D14): the heading, the child resource it links to, the FK column used to
// scope the lines, the columns and rows of the embedded child table, and the
// navigation info needed to build pre-bound child links. Fields is the child
// column definitions (Label for the header) and Rows holds the selected child
// rows keyed by raw column name.
type ChildLinesData struct {
	Heading       string
	Resource      string
	ResourceLower string
	IDColumn      string
	FKColumn      string
	ParentID      string
	PanelPath     string
	CSRFToken     string
	ReturnURL     string
	Fields        []ColumnDef
	Rows          []map[string]interface{}
	Count         int
}

type CardColumnData struct {
	Key      string
	Label    string
	Items    []map[string]interface{}
}

type CardData struct {
	Items      []map[string]interface{}
	Page       int
	PerPage    int
	Total      int
	TotalPages int
	Search     string
	Sort       string
	Order      string
	Fields     []ColumnDef
	Columns    int
	Rows       int
	Kanban     bool
	KanbanField string
	KanbanColumns []CardColumnData
	Resource   string
	PanelPath  string
	FilterQS   string
	Filter     *FilterData
	Applied    bool
}

// FilterData describes the collapsible filter section on a list/card view:
// its header label, whether it is currently applied, and the labeled param
// inputs with their current (echoed) values.
type FilterData struct {
	Label   string
	Applied bool
	Params  []FilterParamData
}

// FilterParamData is a single labeled input on a filter form: the URL query
// key (fp_<Key>), its label and the current value to prefill.
type FilterParamData struct {
	Key   string
	Label string
	Value string
}

type FormData struct {
	Item      map[string]interface{}
	Fields    []ColumnDef
	Action    string
	Method    string
	Resource  string
	PanelPath string
	IsCreate  bool
	CSRFToken string
	Lines     []ChildLinesData
	// Return, when non-empty, is the same-site path the form POST should
	// redirect to on success (master-detail "back to header" navigation, D14).
	// It is submitted as a hidden _return field so the POST handler honors it
	// without needing the query string.
	Return string
	// ViewMode, when true, renders the form read-only (disabled inputs, no
	// submit button) for viewing a child record in a modal instead of editing it.
	ViewMode bool
}

type PageData struct {
	Name     string
	PanelID  string
	PanelPath string
	Widgets  []WidgetData
}

type WidgetData struct {
	Type        string
	Label       string
	Value       template.HTML
	Color       string
	Icon        string
	Prefix      string
	Suffix      string
	ChartType   string
	ChartLabels []string
	ChartValues []float64
	ChartLabelsJSON string
	ChartValuesJSON string
	TableColumns    []string
	TableRows       []map[string]interface{}
	SubWidgets      []WidgetData
}

type AuthData struct {
	Email     string
	Error     string
	PanelPath string
	PanelName string
}

type NavGroupData struct {
	Group string
	Icon  string
	Items []NavItemData
}

type NavItemData struct {
	Label       string
	URL         string
	OpensInNewTab bool
}

// Stringify renders any value (including sql.Null* wrappers, time.Time and
// nil) into a plain display string, returning "" for nil and for invalid NULL
// wrappers. This is the single source of truth for turning a row value into
// text; every input value, list/detail cell and select comparison routes
// through it so nullable columns never leak Go struct syntax like "{1 true}".
func Stringify(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int64:
		return fmt.Sprintf("%d", t)
	case int32:
		return fmt.Sprintf("%d", t)
	case int:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%v", t)
	case time.Time:
		return t.String()
	case sql.NullString:
		if t.Valid {
			return t.String
		}
		return ""
	case sql.NullInt32:
		if t.Valid {
			return fmt.Sprintf("%d", t.Int32)
		}
		return ""
	case sql.NullInt64:
		if t.Valid {
			return fmt.Sprintf("%d", t.Int64)
		}
		return ""
	case sql.NullFloat64:
		if t.Valid {
			return fmt.Sprintf("%v", t.Float64)
		}
		return ""
	case sql.NullBool:
		if t.Valid && t.Bool {
			return "true"
		}
		if t.Valid {
			return "false"
		}
		return ""
	case sql.NullTime:
		if t.Valid {
			return t.Time.String()
		}
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// BoolValue reports the effective boolean of a value: true only for the true
// state, false for everything else (nil, invalid NULL, empty, false). Used by
// the boolean cell renderer and checkbox checked-state detection.
func BoolValue(v interface{}) bool {
	return Stringify(v) == "true"
}

// TimeValue unwraps a time.Time or sql.NullTime value, returning the time and
// true when present, or the zero time and false otherwise. Used by the
// datetime/date renderers to format the instant without leaking the struct.
func TimeValue(v interface{}) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case sql.NullTime:
		if t.Valid {
			return t.Time, true
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

func OptionValue(v interface{}) string {
	return Stringify(v)
}

// OptionLabel returns the label for a field option key, falling back to the
// raw key when the field has no matching option (or the field is unknown).
func OptionLabel(fields []ColumnDef, name string, raw string) string {
	for _, fd := range fields {
		if fd.Name == name {
			if l, ok := fd.Options[raw]; ok {
				return l
			}
			return raw
		}
	}
	return raw
}

// ItemValue formats a form item value for display/input, returning "" for
// missing (nil) values and unwrapping sql.Null* wrappers instead of leaking
// Go struct syntax (BUG-1/BUG-2).
func ItemValue(item map[string]interface{}, name string) string {
	if v, ok := item[name]; ok && v != nil {
		return Stringify(v)
	}
	return ""
}

// FieldLocked reports whether a form field's record picker is locked (D14): a
// field seeded from a parent context hides its Browse button and modal script
// while still showing the read-only display, submitting the hidden value.
func FieldLocked(fields []ColumnDef, name string) bool {
	for _, fd := range fields {
		if fd.Name == name {
			return fd.Locked
		}
	}
	return false
}

// FormTimeValue renders a form item's datetime/date value in the layout a
// browser expects (datetime-local / date inputs), unwrapping sql.NullTime and
// converting to local time so the displayed wall time matches the user's tz.
// Returns "" when the value is absent or a NULL wrapper.
func FormTimeValue(item map[string]interface{}, name, layout string) string {
	if v, ok := item[name]; ok && v != nil {
		if t, ok := TimeValue(v); ok {
			return t.Local().Format(layout)
		}
		return Stringify(v)
	}
	return ""
}

// TimeInputValue renders a datetime-local input value for a form item.
func TimeInputValue(item map[string]interface{}, name string) string {
	return FormTimeValue(item, name, "2006-01-02T15:04")
}

// DateInputValue renders a date input value for a form item.
func DateInputValue(item map[string]interface{}, name string) string {
	return FormTimeValue(item, name, "2006-01-02")
}

// PickCopyValue formats a raw value loaded from a picker's related row for
// auto-filling a form field of the given type (the 'copies:' config). nil and
// NULL scan as "". datetime/date targets are formatted into the layout the
// browser inputs expect; everything else falls back to Stringify.
func PickCopyValue(v interface{}, fieldType string) string {
	if v == nil {
		return ""
	}
	switch fieldType {
	case "date":
		if t, ok := TimeValue(v); ok {
			return t.Format("2006-01-02")
		}
	case "datetime", "time":
		if t, ok := TimeValue(v); ok {
			return t.Format("2006-01-02T15:04")
		}
	}
	return Stringify(v)
}

// OptionsJS renders a field's option map as a JSON object literal (keys
// sorted, proper escaping) for use by the modal picker. Returns "{}" when the
// field is unknown or has no options.
func OptionsJS(fields []ColumnDef, name string) string {
	for _, fd := range fields {
		if fd.Name == name {
			b, err := json.Marshal(fd.Options)
			if err != nil {
				return "{}"
			}
			return string(b)
		}
	}
	return "{}"
}

// CopyDataJSON renders a picker field's per-row copy data as a JSON object
// literal (row key → {target form field: string value}) so selecting a row can
// auto-fill other form fields (the 'copies:' config). Returns "{}" when the
// field is unknown or carries no copy data.
func CopyDataJSON(fields []ColumnDef, name string) string {
	for _, fd := range fields {
		if fd.Name == name {
			b, err := json.Marshal(fd.CopyData)
			if err != nil {
				return "{}"
			}
			return string(b)
		}
	}
	return "{}"
}
`

	return os.WriteFile(filepath.Join(g.OutDir, "internal/viewmodels/models.go"), []byte(code), 0644)
}
