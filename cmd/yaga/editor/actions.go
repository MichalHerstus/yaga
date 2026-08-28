package editor

import (
	"fmt"
	"strings"

	luasrc "github.com/MichalHerstus/yaga/internal/generator/luasrc"
	"github.com/MichalHerstus/yaga/internal/types"
	"github.com/rivo/tview"
)

// actionsPage manages a resource's custom row actions.
func (e *Editor) actionsPage(idx int) tview.Primitive {
	r := &e.cfg.Resources[idx]
	spec := listSpec{
		title: "Actions",
		labels: func() []string {
			out := make([]string, len(r.Actions))
			for i, a := range r.Actions {
				out[i] = a.Name
			}
			return out
		},
		sub: func(i int) string {
			a := r.Actions[i]
			kind := "query"
			if a.Script != "" {
				kind = "script"
			} else if a.Proc != "" {
				kind = "proc"
			}
			return fmt.Sprintf("%s  %s  bulk=%v", a.Label, kind, a.Bulk)
		},
		add: func() {
			r.Actions = append(r.Actions, types.Action{Name: "new_action", Label: "New action"})
			e.markModified()
		},
		edit: func(i int) {
			e.showPage(e.resActionPath(idx, i), e.actionPage(idx, i))
		},
		remove: func(i int) {
			r.Actions = append(r.Actions[:i], r.Actions[i+1:]...)
			e.markModified()
		},
	}
	return e.recordList(e.resActionsPath(idx), spec)
}

// actionPage edits a single custom action.
func (e *Editor) actionPage(idx, aidx int) tview.Primitive {
	a := &e.cfg.Resources[idx].Actions[aidx]
	return e.formShell("Action: "+a.Name, func(f *tview.Form) {
		e.str(f, "Name", a.Name, func(v string) { a.Name = v })
		e.str(f, "Label", a.Label, func(v string) { a.Label = v })
		e.pick(f, "Icon", iconOptions, a.Icon, func(v string) { a.Icon = v })
		e.pick(f, "Color", actionColorOptions, a.Color, func(v string) { a.Color = v })
		e.yesno(f, "Requires confirmation", a.RequiresConfirmation, func(v bool) { a.RequiresConfirmation = v })
		e.yesno(f, "Bulk action", a.Bulk, func(v bool) { a.Bulk = v })
		e.long(f, "Query", a.Query, func(v string) { a.Query = v })
		e.long(f, "Script", a.Script, func(v string) { a.Script = v })
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
		e.str(f, "Proc", a.Proc, func(v string) { a.Proc = v })
		e.addButton(f, "Hooks", func() {
			if a.Hooks == nil {
				a.Hooks = &types.Hooks{}
			}
			base := e.resActionPath(idx, aidx) + "/Hooks"
			e.showPage(base, e.hooksPage(base, &a.Hooks, a.Name))
		})
	})
}
