package editor

import (
	"github.com/MichalHerstus/yaga/internal/types"
	"github.com/rivo/tview"
)

// fieldsListPage manages a []types.Field collection (form, card or detail
// fields) with a shared editor. name is the canonical path of the fields list
// screen; field sub-pages extend it.
func (e *Editor) fieldsListPage(name, title string, get func() *[]types.Field) tview.Primitive {
	spec := listSpec{
		title: title,
		labels: func() []string {
			fs := *get()
			out := make([]string, len(fs))
			for i, fld := range fs {
				out[i] = fld.Name
			}
			return out
		},
		sub: func(i int) string {
			fs := *get()
			return fs[i].Type
		},
		add: func() {
			fs := append(*get(), types.Field{Name: "new_field", Label: "New Field", Type: "string"})
			*get() = fs
			e.markModified()
		},
		edit: func(i int) {
			fs := *get()
			e.showPage(name+"/"+segName(fs[i].Name, i), e.fieldPage(name, get, i))
		},
		remove: func(i int) {
			fs := *get()
			fs = append(fs[:i], fs[i+1:]...)
			*get() = fs
			e.markModified()
		},
	}
	return e.recordList(name, spec)
}

// fieldPage edits a single field definition. name is the canonical fields-list
// path; the field's own path (with /Validation, /Options, /Visible children) is
// derived from it.
func (e *Editor) fieldPage(name string, get func() *[]types.Field, idx int) tview.Primitive {
	fs := *get()
	fld := &fs[idx]
	fieldPath := name + "/" + segName(fld.Name, idx)
	return e.formShell("Field: "+fld.Name, func(f *tview.Form) {
		e.str(f, "Name", fld.Name, func(v string) { fld.Name = v })
		e.str(f, "Label", fld.Label, func(v string) { fld.Label = v })
		e.pick(f, "Type", fieldTypeOptions, fld.Type, func(v string) { fld.Type = v })
		e.yesno(f, "Required", fld.Required, func(v bool) { fld.Required = v })
		e.str(f, "Options query", fld.OptionsQuery, func(v string) { fld.OptionsQuery = v })
		e.str(f, "Options value", fld.OptionsValue, func(v string) { fld.OptionsValue = v })
		e.str(f, "Options label", fld.OptionsLabel, func(v string) { fld.OptionsLabel = v })
		e.addButton(f, "Validation", func() {
			if fld.Validation == nil {
				fld.Validation = &types.Validation{}
			}
			e.showPage(fieldPath+"/Validation", e.validationPage(fieldPath, fld.Validation))
		})
		e.addButton(f, "Options", func() {
			optsPath := fieldPath + "/Options"
			e.showPage(optsPath, e.stringMapPage(optsPath, "Field options", func() map[string]string {
				return fld.Options
			}, func(v map[string]string) { fld.Options = v }))
		})
		e.addButton(f, "Visible", func() {
			visPath := fieldPath + "/Visible"
			e.showPage(visPath, e.tagsPage(visPath, "Field visible in", visibleOptions, func() []string {
				return fld.Visible
			}, func(v []string) { fld.Visible = v }))
		})
		e.addButton(f, "Copies", func() {
			copiesPath := fieldPath + "/Copies"
			e.showPage(copiesPath, e.stringMapPage(copiesPath, "Copy into fields (field: related column)", func() map[string]string {
				return fld.Copies
			}, func(v map[string]string) { fld.Copies = v }))
		})
	})
}

// validationPage edits a field's min/max validation.
func (e *Editor) validationPage(fieldPath string, v *types.Validation) tview.Primitive {
	return e.formShell("Validation", func(f *tview.Form) {
		e.num(f, "Min", v.Min, func(x int) { v.Min = x })
		e.num(f, "Max", v.Max, func(x int) { v.Max = x })
	})
}

// cardFieldsPage edits the card view fields of a resource.
func (e *Editor) cardFieldsPage(idx int) tview.Primitive {
	c := e.cfg.Resources[idx].Card
	return e.fieldsListPage(e.resCardFieldsPath(idx), "Card fields", func() *[]types.Field {
		return &c.Fields
	})
}

// detailFieldsPage edits the detail view fields of a resource.
func (e *Editor) detailFieldsPage(idx int) tview.Primitive {
	d := e.cfg.Resources[idx].Detail
	return e.fieldsListPage(e.resDetailFieldsPath(idx), "Detail fields", func() *[]types.Field {
		return &d.Fields
	})
}

// formFieldsPage edits the form fields of one form action.
func (e *Editor) formFieldsPage(idx int, which string) tview.Primitive {
	r := &e.cfg.Resources[idx]
	var fa *types.FormAction
	switch which {
	case "create":
		fa = r.Form.Create
	case "update":
		fa = r.Form.Update
	case "delete":
		fa = r.Form.Delete
	}
	title := "Create fields"
	if which == "update" {
		title = "Update fields"
	} else if which == "delete" {
		title = "Delete fields"
	}
	return e.fieldsListPage(e.resFormWhichPath(idx, which)+"/Fields", title, func() *[]types.Field {
		return &fa.Fields
	})
}

// computedListPage manages a []types.ComputedField collection (list/card/detail
// E7 computed columns) with a shared editor. name is the canonical path of the
// computed list screen; computed sub-pages extend it.
func (e *Editor) computedListPage(name, title string, get func() *[]types.ComputedField) tview.Primitive {
	spec := listSpec{
		title: title,
		labels: func() []string {
			cs := *get()
			out := make([]string, len(cs))
			for i, c := range cs {
				out[i] = c.Name
			}
			return out
		},
		sub: func(i int) string {
			cs := *get()
			return cs[i].Type + "  " + cs[i].Expression
		},
		add: func() {
			cs := append(*get(), types.ComputedField{Name: "new_computed", Type: "string"})
			*get() = cs
			e.markModified()
		},
		edit: func(i int) {
			cs := *get()
			e.showPage(name+"/"+segName(cs[i].Name, i), e.computedFieldPage(name, get, i))
		},
		remove: func(i int) {
			cs := *get()
			cs = append(cs[:i], cs[i+1:]...)
			*get() = cs
			e.markModified()
		},
	}
	return e.recordList(name, spec)
}

// computedFieldPage edits a single computed field definition: name, label,
// type and the raw expression (a helpers.*-aware SQL fragment).
func (e *Editor) computedFieldPage(name string, get func() *[]types.ComputedField, idx int) tview.Primitive {
	cs := *get()
	c := &cs[idx]
	return e.formShell("Computed: "+c.Name, func(f *tview.Form) {
		e.str(f, "Name", c.Name, func(v string) { c.Name = v })
		e.str(f, "Label", c.Label, func(v string) { c.Label = v })
		e.pick(f, "Type", fieldTypeOptions, c.Type, func(v string) { c.Type = v })
		e.long(f, "Expression", c.Expression, func(v string) { c.Expression = v })
	})
}

// listComputedPage edits the list computed columns of a resource.
func (e *Editor) listComputedPage(idx int) tview.Primitive {
	l := ensureList(&e.cfg.Resources[idx])
	return e.computedListPage(e.resListComputedPath(idx), "List computed columns", func() *[]types.ComputedField {
		return &l.Computed
	})
}

// cardComputedPage edits the card computed fields of a resource.
func (e *Editor) cardComputedPage(idx int) tview.Primitive {
	r := &e.cfg.Resources[idx]
	if r.Card == nil {
		r.Card = &types.CardConfig{Columns: 4, Rows: 4}
	}
	return e.computedListPage(e.resCardComputedPath(idx), "Card computed fields", func() *[]types.ComputedField {
		return &r.Card.Computed
	})
}

// detailComputedPage edits the detail computed fields of a resource.
func (e *Editor) detailComputedPage(idx int) tview.Primitive {
	r := &e.cfg.Resources[idx]
	if r.Detail == nil {
		r.Detail = &types.DetailConfig{}
	}
	return e.computedListPage(e.resDetailComputedPath(idx), "Detail computed fields", func() *[]types.ComputedField {
		return &r.Detail.Computed
	})
}
