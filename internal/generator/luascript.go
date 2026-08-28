// luascript.go
//
// Generates the shared internal/panel/luascript package: the request-time Lua
// runtime (gopher-lua) that executes script: hook bodies and script actions.
// The package is emitted only when at least one script: exists anywhere in the
// config (feature-off output stays byte-identical). Every script is wrapped by
// the runtime as the body of a single run(ctx) function and runs with a fixed
// 5 s context.WithTimeout via L.SetContext.
package generator

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MichalHerstus/yaga/internal/types"
)

//go:embed luasrc/luascript.go
var luascriptSrc []byte

// hasAnyScript reports whether any script: body is declared anywhere in the
// config — on a hook (before/after) or on a custom action. Gates the emission
// of the luascript runtime package, the conditional gopher-lua go.mod
// dependency and the auth.RoleName helper in the generated middleware.
// Returns: true when at least one script: exists.
func (g *Generator) hasAnyScript() bool {
	for _, r := range g.Config.Resources {
		if r.Form != nil {
			for _, fa := range []*types.FormAction{r.Form.Create, r.Form.Update, r.Form.Delete} {
				if fa != nil && g.hooksUseScript(fa.Hooks) {
					return true
				}
			}
		}
		for _, a := range r.Actions {
			if a.Script != "" {
				return true
			}
			if g.hooksUseScript(a.Hooks) {
				return true
			}
		}
	}
	return false
}

// hooksUseScript reports whether a Hooks block declares any script: hook in
// its before or after list.
// Params: h (the hooks block; nil is valid).
// Returns: true when at least one hook has a non-empty Script body.
func (g *Generator) hooksUseScript(h *types.Hooks) bool {
	if h == nil {
		return false
	}
	for _, list := range [][]types.Hook{h.Before, h.After} {
		for _, hook := range list {
			if hook.Script != "" {
				return true
			}
		}
	}
	return false
}

// luaImport returns the import line for the luascript package, or "" when no
// script exists anywhere (feature-off output must not reference the package).
// Returns: a single import line (trailing newline) or "".
func (g *Generator) luaImport() string {
	if !g.hasAnyScript() {
		return ""
	}
	return fmt.Sprintf("    luascript %q\n", g.moduleImport("internal/panel/luascript"))
}

// generateLuascript writes internal/panel/luascript/luascript.go when at least
// one script: exists anywhere in the config. The file is the canonical
// internal/generator/luasrc/luascript.go source embedded at build time; the
// generated app calls luascript.SetKeepQuestion() based on the driver.
// Nothing is written when the config declares no scripts.
// Returns: an error on write failure.
func (g *Generator) generateLuascript() error {
	if !g.hasAnyScript() {
		return nil
	}
	dir := filepath.Join(g.OutDir, "internal/panel/luascript")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "luascript.go"), luascriptSrc, 0644)
}


