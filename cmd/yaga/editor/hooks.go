package editor

import (
	"fmt"
	"strings"

	luasrc "github.com/MichalHerstus/yaga/internal/generator/luasrc"
	"github.com/MichalHerstus/yaga/internal/types"
	"github.com/rivo/tview"
)

// hooksPage manages the before/after hook lists of a Hooks block. base is the
// canonical path of the hooks screen (…/Hooks), which the sub-pages extend.
func (e *Editor) hooksPage(base string, hooks **types.Hooks, title string) tview.Primitive {
	return e.formShell(title+" / Hooks", func(f *tview.Form) {
		e.addButton(f, "Before hooks", func() {
			e.showPage(base+"/Before", e.hookListPage(base, hooks, true))
		})
		e.addButton(f, "After hooks", func() {
			e.showPage(base+"/After", e.hookListPage(base, hooks, false))
		})
	})
}

// hookListPage edits one of the before/after hook slices.
func (e *Editor) hookListPage(base string, hooks **types.Hooks, before bool) tview.Primitive {
	get := hookListGet(hooks, before)
	title := "After hooks"
	seg := "After"
	if before {
		title = "Before hooks"
		seg = "Before"
	}
	listPath := base + "/" + seg
	spec := listSpec{
		title: title,
		labels: func() []string {
			hs := *get()
			out := make([]string, len(hs))
			for i, h := range hs {
				out[i] = h.Name
			}
			return out
		},
		sub: func(i int) string {
			hs := *get()
			switch {
			case hs[i].Fn != "":
				return "fn: " + hs[i].Fn
			case hs[i].Proc != "":
				return "proc: " + hs[i].Proc
			case hs[i].Script != "":
				return "script"
			}
			return "sql: " + hs[i].SQL
		},
		add: func() {
			hs := append(*get(), types.Hook{Name: "hook", Fn: ""})
			*get() = hs
			e.markModified()
		},
		edit: func(i int) {
			hs := *get()
			e.showPage(listPath+"/"+segName(hs[i].Name, i), e.hookPage(get, i))
		},
		remove: func(i int) {
			hs := *get()
			hs = append(hs[:i], hs[i+1:]...)
			*get() = hs
			e.markModified()
		},
	}
	return e.recordList(listPath, spec)
}

// hookKindOptions drives the four-way hook type picker (fn/sql/proc/script).
var hookKindOptions = []Option{
	{Label: "Go function", Value: "function"},
	{Label: "SQL", Value: "sql"},
	{Label: "Stored procedure", Value: "proc"},
	{Label: "Script (Lua)", Value: "script"},
}

// hookPage edits a single hook (name + exactly one of fn/sql/proc/script).
func (e *Editor) hookPage(get func() *[]types.Hook, idx int) tview.Primitive {
	hs := *get()
	h := &hs[idx]
	kind := "sql"
	switch {
	case h.Fn != "":
		kind = "function"
	case h.Proc != "":
		kind = "proc"
	case h.Script != "":
		kind = "script"
	}
	return e.formShell("Hook: "+h.Name, func(f *tview.Form) {
		e.str(f, "Name", h.Name, func(v string) { h.Name = v })
		e.pick(f, "Kind", hookKindOptions, kind, func(v string) {
			switch v {
			case "function":
				h.SQL, h.Proc, h.Script = "", "", ""
				if h.Fn == "" {
					h.Fn = "MyHook"
				}
			case "sql":
				h.Fn, h.Proc, h.Script = "", "", ""
			case "proc":
				h.Fn, h.SQL, h.Script = "", "", ""
				if h.Proc == "" {
					h.Proc = "my_proc"
				}
			case "script":
				h.Fn, h.SQL, h.Proc = "", "", ""
			}
			e.markModified()
		})
		switch {
		case h.Fn != "":
			e.str(f, "Function", h.Fn, func(v string) { h.Fn = v })
		case h.Proc != "":
			e.str(f, "Proc", h.Proc, func(v string) { h.Proc = v })
		case h.Script != "":
			e.long(f, "Script", h.Script, func(v string) { h.Script = v })
			e.addButton(f, "Check Script", func() {
				item := f.GetFormItemByLabel("Script")
				if ta, ok := item.(*tview.TextArea); ok {
					script := ta.GetText()
					errs := luasrc.SyntaxCheck(script)
					if len(errs) == 0 {
						e.errorModal("Lua Check", "Syntax OK - no errors found")
					} else {
						var sb strings.Builder
						for _, synErr := range errs {
							fmt.Fprintf(&sb, "Line %d: %s\n", synErr.Line, synErr.Message)
						}
						e.errorModal("Syntax Errors", strings.TrimSpace(sb.String()))
					}
				}
			})
		default:
			e.long(f, "SQL", h.SQL, func(v string) { h.SQL = v })
		}
	})
}
