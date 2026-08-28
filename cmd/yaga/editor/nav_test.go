package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// TestNormalizePath exercises the cd-style path normalizer (relative,
// absolute, "~", "..", "." and empty segments).
func TestNormalizePath(t *testing.T) {
	e := New(testConfig(), "testdata/yaga.yaml")

	atHome := func() {
		e.history = []string{"home"}
	}
	atPanel := func() {
		e.history = []string{"home", "Panel"}
	}

	atHome()
	eq := func(got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	eq(e.normalizePath(""), nil)
	eq(e.normalizePath("~/Panel"), []string{"Panel"})
	eq(e.normalizePath("/Resources"), []string{"Resources"})
	eq(e.normalizePath("../Panel"), []string{"Panel"})
	eq(e.normalizePath("../../Panel"), []string{"Panel"})
	eq(e.normalizePath("a/./b/"), []string{"a", "b"})

	atPanel()
	eq(e.normalizePath(""), []string{"Panel"})
	eq(e.normalizePath("Brand"), []string{"Panel", "Brand"})
	eq(e.normalizePath("../Resources"), []string{"Resources"})
	eq(e.normalizePath("../"), []string{})
}

// TestResolvePath verifies canonical paths resolve to the right screens.
func TestResolvePath(t *testing.T) {
	e := New(testConfig(), "testdata/yaga.yaml")

	check := func(input, want string) {
		t.Helper()
		tgt, err := e.resolvePath(input)
		if err != nil {
			t.Fatalf("resolvePath(%q): %v", input, err)
		}
		if tgt.name != want {
			t.Fatalf("resolvePath(%q) = %q, want %q", input, tgt.name, want)
		}
	}

	check("home", "home")
	check("~/", "home")
	check("", "home")
	check("Panel", "Panel")
	check("panel/brand", "Panel/Brand")
	check("/Panel/Layout", "Panel/Layout")
	check("Panel/Theme", "Panel/Theme")
	check("Connections/default", "Connections/default")
	check("Auth/Login Fields", "Auth/Login Fields")
	check("Navigation", "Navigation")
	check("Navigation/Sales/Items/User", "Navigation/Sales/Items/User")
	check("Resources", "Resources")
	check("Resources/User", "Resources/User")
	check("Resources/User/List", "Resources/User/List")
	check("Resources/User/List/Columns", "Resources/User/List/Columns")
	check("Resources/User/List/Columns/id", "Resources/User/List/Columns/id")
	check("Resources/User/List/Columns/id/Options", "Resources/User/List/Columns/id/Options")
	check("Resources/User/List/Computed", "Resources/User/List/Computed")
	check("Resources/User/List/Computed/total_gross", "Resources/User/List/Computed/total_gross")
	check("Resources/User/Card", "Resources/User/Card")
	check("Resources/User/Card/Computed", "Resources/User/Card/Computed")
	check("Resources/User/Detail", "Resources/User/Detail")
	check("Resources/User/Detail/Computed", "Resources/User/Detail/Computed")
	check("Resources/User/Detail/Fields/email", "Resources/User/Detail/Fields/email")
	check("Resources/User/Form/Create/Fields/email", "Resources/User/Form/Create/Fields/email")
	check("Resources/User/Form/Create/Fields/email/Copies", "Resources/User/Form/Create/Fields/email/Copies")
	check("Resources/User/Children", "Resources/User/Children")
	check("Resources/User/Form/Update", "Resources/User/Form/Update")
	check("Resources/User/Form/Delete/Hooks/After", "Resources/User/Form/Delete/Hooks/After")
	check("Resources/User/Actions/archive", "Resources/User/Actions/archive")
	check("Resources/User/Policies", "Resources/User/Policies")
	check("Pages", "Pages")
	check("Pages/Dashboard", "Pages/Dashboard")
	check("Pages/Dashboard/Widgets", "Pages/Dashboard/Widgets")
	check("Pages/Dashboard/Widgets/Users", "Pages/Dashboard/Widgets/Users")
	check("Pages/Dashboard/Widgets/Grid/Sub-widgets/A", "Pages/Dashboard/Widgets/Grid/Sub-widgets/A")
	check("Validate", "Validate")
	check("Preview", "Preview")
	check("Preview/Page/Dashboard", "Preview/Page/Dashboard")
	check("Preview/Resource/User", "Preview/Resource/User")

	for _, bad := range []string{"Bogus", "Resources/Nope", "Resources/User/Nope", "Panel/Sub", "Pages/Dashboard/Nope", "Preview/Page/Nope"} {
		if _, err := e.resolvePath(bad); err == nil {
			t.Fatalf("resolvePath(%q) should fail", bad)
		}
	}
}

// TestResolveRelativePath verifies relative resolution from a current screen.
func TestResolveRelativePath(t *testing.T) {
	e := New(testConfig(), "testdata/yaga.yaml")
	e.history = []string{"home", "Resources", "Resources/User", "Resources/User/List"}

	tgt, err := e.resolvePath("../Card")
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if tgt.name != "Resources/User/Card" {
		t.Fatalf("resolvePath(../Card) = %q", tgt.name)
	}

	tgt, err = e.resolvePath("../..")
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if tgt.name != "Resources" {
		t.Fatalf("resolvePath(../..) = %q", tgt.name)
	}

	e.history = []string{"home", "Resources", "Resources/User", "Resources/User/List", "Resources/User/List/Columns", "Resources/User/List/Columns/id"}
	tgt, err = e.resolvePath("Options")
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if tgt.name != "Resources/User/List/Columns/id/Options" {
		t.Fatalf("resolvePath(Options) = %q", tgt.name)
	}
}

// TestCompletePath verifies Tab completion finds the longest common prefix of
// the matching child screens.
func TestCompletePath(t *testing.T) {
	e := New(testConfig(), "testdata/yaga.yaml")

	check := func(input, want string, wantMatches []string) {
		t.Helper()
		out, matches := e.completePath(input)
		if out != want {
			t.Fatalf("completePath(%q) out = %q, want %q", input, out, want)
		}
		if len(matches) != len(wantMatches) {
			t.Fatalf("completePath(%q) matches = %v, want %v", input, matches, wantMatches)
		}
		for i := range wantMatches {
			if matches[i] != wantMatches[i] {
				t.Fatalf("completePath(%q) matches = %v, want %v", input, matches, wantMatches)
			}
		}
	}

	check("~/P", "~/P", []string{"Panel", "Procedures", "Plugins", "Pages", "Preview"})
	check("~/Pa", "~/Pa", []string{"Panel", "Pages"})
	check("~/Panel", "~/Panel", []string{"Panel"})
	check("Res", "Resources", []string{"Resources"})
	check("Resources/U", "Resources/User", []string{"User"})
	check("Resources/User/List/Col", "Resources/User/List/Columns", []string{"Columns"})
	check("Resources/User/Card/Fi", "Resources/User/Card/Fields", []string{"Fields"})
	check("", "", []string{"Panel", "Connections", "Auth", "Audit", "Procedures", "Plugins", "Navigation", "Resources", "Pages", "Validate", "Preview"})
}

// newNavEditor returns an editor ready for dialog interactions (pages + app).
func newNavEditor(t *testing.T) *Editor {
	t.Helper()
	e := New(testConfig(), "testdata/yaga.yaml")
	e.pages = tview.NewPages()
	e.app = tview.NewApplication()
	e.buildShell()
	return e
}

// TestNavDialogFlow drives the cd dialog: open, Tab-complete, Enter navigates,
// Enter on an unknown path keeps the dialog open, and the two-stage Esc closes.
func TestNavDialogFlow(t *testing.T) {
	e := newNavEditor(t)
	e.history = []string{"home"}

	e.capture(tcell.NewEventKey(tcell.KeyCtrlP, 'P', tcell.ModCtrl))
	if !e.navOpen || !e.modalOpen {
		t.Fatalf("Ctrl+P should open the dialog, navOpen=%v modalOpen=%v", e.navOpen, e.modalOpen)
	}

	// Tab completes the root children.
	e.navInput.SetText("~/Pa")
	e.navTab()
	if got := e.navInput.GetText(); got != "~/Pa" {
		t.Fatalf("navTab text = %q", got)
	}

	// Enter with a valid absolute path navigates and closes the dialog.
	e.navInput.SetText("~/Panel/Brand")
	e.navGo()
	if e.navOpen || e.modalOpen {
		t.Fatalf("navGo should close the dialog")
	}
	if cur := e.currentPath(); cur != "Panel/Brand" {
		t.Fatalf("current path = %q, want Panel/Brand", cur)
	}

	// Reopen and navigate to an unknown path: dialog stays open, path unchanged.
	e.capture(tcell.NewEventKey(tcell.KeyCtrlP, 'P', tcell.ModCtrl))
	e.navInput.SetText("Bogus/Path")
	e.navGo()
	if !e.navOpen {
		t.Fatal("navGo to unknown path should keep the dialog open")
	}
	if cur := e.currentPath(); cur != "Panel/Brand" {
		t.Fatalf("current path changed to %q", cur)
	}

	// Two-stage Esc: first clears the input, second closes.
	e.navInput.SetText("Resources/User")
	e.capture(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if !e.navOpen {
		t.Fatal("first Esc should keep the dialog open")
	}
	if got := e.navInput.GetText(); got != "" {
		t.Fatalf("first Esc should clear the input, got %q", got)
	}
	e.capture(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if e.navOpen || e.modalOpen {
		t.Fatal("second Esc should close the dialog")
	}
}

// TestNavRelativeNavigation verifies relative paths resolve against the current
// screen through the dialog.
func TestNavRelativeNavigation(t *testing.T) {
	e := newNavEditor(t)
	e.history = []string{"home", "Panel"}

	e.capture(tcell.NewEventKey(tcell.KeyCtrlP, 'P', tcell.ModCtrl))
	e.navInput.SetText("Brand")
	e.navGo()
	if cur := e.currentPath(); cur != "Panel/Brand" {
		t.Fatalf("relative path resolved to %q, want Panel/Brand", cur)
	}
}

// TestNavAliases verifies Ctrl+> and Ctrl+/ behave like Ctrl+P and Ctrl+O.
func TestNavAliases(t *testing.T) {
	e := newNavEditor(t)
	e.history = []string{"home"}

	e.capture(tcell.NewEventKey(0, '>', tcell.ModCtrl))
	if !e.navOpen {
		t.Fatal("Ctrl+> should open the dialog")
	}
	e.navInput.SetText("Validate")
	e.navGo()
	if cur := e.currentPath(); cur != "Validate" {
		t.Fatalf("path = %q, want Validate", cur)
	}

	e.capture(tcell.NewEventKey(tcell.KeyCtrlO, 'O', tcell.ModCtrl))
	if cur := e.currentPath(); cur != "home" {
		t.Fatalf("Ctrl+O should return home, got %q", cur)
	}
	if len(e.history) == 0 || e.history[len(e.history)-1] != "home" {
		t.Fatalf("Ctrl+O should push home, history = %v", e.history)
	}
}

// TestGoHome covers the Ctrl+O home hotkey: no-op at home, jump from a deep
// path, closes an open dialog first, and the Ctrl+/ alias works too.
func TestGoHome(t *testing.T) {
	e := newNavEditor(t)

	e.history = []string{"home"}
	e.goHome()
	if len(e.history) != 1 || e.history[0] != "home" {
		t.Fatalf("goHome at home should be a no-op, history = %v", e.history)
	}

	e.history = []string{"home", "Resources", "Resources/User/List/Columns"}
	e.goHome()
	if cur := e.currentPath(); cur != "home" {
		t.Fatalf("goHome from deep path = %q, want home", cur)
	}
	if f := e.app.GetFocus(); f != e.nav {
		t.Fatalf("goHome should focus the nav menu, got %T", f)
	}

	// Ctrl+O with the dialog open: dialog closes, then home.
	e.history = []string{"home", "Panel", "Panel/Brand"}
	e.capture(tcell.NewEventKey(tcell.KeyCtrlP, 'P', tcell.ModCtrl))
	e.navInput.SetText("~/Panel")
	e.capture(tcell.NewEventKey(0, '>', tcell.ModCtrl))
	if !e.navOpen {
		t.Fatal("expected the dialog to be open")
	}
	e.capture(tcell.NewEventKey(tcell.KeyCtrlO, 'O', tcell.ModCtrl))
	if e.navOpen || e.modalOpen {
		t.Fatal("goHome should close the dialog")
	}
	if cur := e.currentPath(); cur != "home" {
		t.Fatalf("goHome with dialog open = %q, want home", cur)
	}

	// Ctrl+/ alias behaves identically from a deep path.
	e.history = []string{"home", "Validate"}
	e.capture(tcell.NewEventKey(0, '/', tcell.ModCtrl))
	if cur := e.currentPath(); cur != "home" {
		t.Fatalf("Ctrl+/ should return home, got %q", cur)
	}
	if f := e.app.GetFocus(); f != e.nav {
		t.Fatalf("Ctrl+/ should focus the nav menu, got %T", f)
	}
}

// TestNavNoOpWhileModalOpen verifies Ctrl+P is ignored while another modal is up.
func TestNavNoOpWhileModalOpen(t *testing.T) {
	e := newNavEditor(t)
	e.history = []string{"home", "Panel"}
	e.modalOpen = true
	e.capture(tcell.NewEventKey(tcell.KeyCtrlP, 'P', tcell.ModCtrl))
	if e.navOpen {
		t.Fatal("Ctrl+P must be ignored while a modal is open")
	}
}

// TestChildrenOfRoot checks the completion tree at the root.
func TestChildrenOfRoot(t *testing.T) {
	e := New(testConfig(), "testdata/yaga.yaml")
	kids, ok := e.childrenOf(nil)
	if !ok {
		t.Fatal("childrenOf(nil) should resolve")
	}
	got := strings.Join(kids, ",")
	for _, want := range []string{"Panel", "Connections", "Auth", "Audit", "Procedures", "Plugins", "Navigation", "Resources", "Pages", "Validate", "Preview"} {
		if !strings.Contains(got, want) {
			t.Fatalf("childrenOf(nil) missing %s: %v", want, kids)
		}
	}
}
