package editor

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// listSpec parameterizes the generic record-list editor used for collections
// of config items (resources, pages, columns, fields, actions, widgets, ...).
type listSpec struct {
	title  string
	labels func() []string      // one label per item
	sub    func(idx int) string // optional secondary line
	add    func()               // append a new item (marks modified)
	edit   func(idx int)        // push the edit page for item idx
	remove func(idx int)        // delete item idx (marks modified)
	help   string               // extra hint appended under the list
}

// recordList builds a tview.List managing a collection with a:add / d:del /
// Enter:edit. Esc bubbles up to the app-level back handler.
func (e *Editor) recordList(name string, spec listSpec) *tview.List {
	list := tview.NewList().ShowSecondaryText(true)
	list.SetBorder(true).SetBorderColor(colBorder).SetTitle(spec.title)
	list.SetSelectedFocusOnly(true)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'a', 'A':
			if spec.add != nil {
				spec.add()
				e.refreshList(name, spec)
			}
			return nil
		case 'd', 'D':
			e.confirm(fmt.Sprintf("Delete selected %s?", spec.title), "This cannot be undone.", func() {
				if spec.remove != nil {
					spec.remove(list.GetCurrentItem())
					e.refreshList(name, spec)
				}
			})
			return nil
		}
		return event
	})
	list.SetSelectedFunc(func(idx int, _ string, _ string, _ rune) {
		if spec.edit != nil {
			spec.edit(idx)
		}
	})
	e.renderList(spec, list)
	return list
}

// refreshList rebuilds a collection list in place after add/delete.
func (e *Editor) refreshList(name string, spec listSpec) {
	list := e.recordList(name, spec)
	e.refreshPage(name, list)
}

// renderList fills a list from the spec's label function.
func (e *Editor) renderList(spec listSpec, list *tview.List) {
	list.Clear()
	labels := spec.labels()
	for i, label := range labels {
		sub := ""
		if spec.sub != nil {
			sub = spec.sub(i)
		}
		list.AddItem(label, sub, 0, nil)
	}
}

// confirm shows a yes/no modal. onYes runs when the user confirms.
func (e *Editor) confirm(title, msg string, onYes func()) {
	modal := tview.NewModal().SetText(title + "\n\n" + msg)
	labels := e.addModalButtons([]string{"Yes", "No"}, func(index int, _ string) {
		e.closeModal()
		if index == 0 {
			onYes()
		}
	})
	modal.AddButtons(labels).SetDoneFunc(func(index int, _ string) {
		e.closeModal()
		if index == 0 {
			onYes()
		}
	})
	e.showModal(modal)
}

// tagsPage builds a toggle-list page editing a []string from a fixed option
// set (login fields, visible, searchable, ...). Space toggles, Enter returns.
func (e *Editor) tagsPage(name, title string, opts []Option, selected func() []string, set func([]string)) tview.Primitive {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			e.back()
			return nil
		}
		if event.Rune() == ' ' {
			idx := list.GetCurrentItem()
			if idx < 0 || idx >= len(opts) {
				return nil
			}
			val := opts[idx].Value
			cur := selected()
			if containsString(cur, val) {
				var out []string
				for _, v := range cur {
					if v != val {
						out = append(out, v)
					}
				}
				set(out)
			} else {
				set(append(cur, val))
			}
			e.markModified()
			e.renderTags(opts, selected, list)
			if idx < list.GetItemCount() {
				list.SetCurrentItem(idx)
			}
			return nil
		}
		return event
	})
	e.renderTags(opts, selected, list)
	return boxed(list, title+"  (space: toggle, enter: done)")
}

// renderTags fills the tag list with checkmarks.
func (e *Editor) renderTags(opts []Option, selected func() []string, list *tview.List) {
	list.Clear()
	cur := selected()
	for _, o := range opts {
		mark := " "
		if containsString(cur, o.Value) {
			mark = "x"
		}
		list.AddItem(fmt.Sprintf("[%s] %s", mark, o.Label), "", 0, nil)
	}
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// stringListPage manages a free-form []string (data_columns, params keys, ...)
// with add / edit / remove.
func (e *Editor) stringListPage(name, title string, get func() []string, set func([]string)) tview.Primitive {
	spec := listSpec{
		title:  title,
		labels: get,
		add: func() {
			e.promptInput("Add "+title, "value", "", func(v string) {
				set(append(get(), v))
				e.markModified()
			})
		},
		edit: func(i int) {
			e.promptInput("Edit "+title, "value", get()[i], func(v string) {
				cur := get()
				cur[i] = v
				set(cur)
				e.markModified()
			})
		},
		remove: func(i int) {
			cur := get()
			set(append(cur[:i], cur[i+1:]...))
			e.markModified()
		},
	}
	return e.recordList(name, spec)
}

// promptInput asks for a string value (add or edit) and runs onDone.
func (e *Editor) promptInput(title, label, initial string, onDone func(string)) {
	input := tview.NewInputField().SetLabel(label).SetText(initial)
	e.showModal(e.promptForm(title, input, onDone))
}

// promptForm builds the modal form shell shared by namePrompt/promptInput.
func (e *Editor) promptForm(title string, input *tview.InputField, onDone func(string)) *tview.Form {
	input.SetFieldBackgroundColor(tcell.NewHexColor(0x27272a))
	input.SetLabelColor(colText)
	form := tview.NewForm()
	form.SetBorder(true).SetBorderColor(colBorder).SetTitle(title)
	form.SetLabelColor(colText)
	form.SetFieldBackgroundColor(tcell.NewHexColor(0x27272a))
	form.SetButtonBackgroundColor(colAccent)
	form.AddFormItem(input)
	e.addButton(form, "OK", func() {
		val := input.GetText()
		e.closeModal()
		onDone(val)
		e.app.SetFocus(e.pages)
	})
	e.addButton(form, "Cancel", e.closeModal)
	return form
}
