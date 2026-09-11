// ident.go
//
// Sanitization helpers that turn config-authored strings (resource names, page
// names, panel ids, column-derived names) into valid Go identifiers. Every
// config string is emitted verbatim somewhere in the generated project — as a
// package directory, a package declaration, an import path or a templ/Go
// function name. Names like "Order Management", "size range" or "9panel"
// otherwise produce uncompilable Go on every OS (a leading-digit or
// space-bearing resource name breaks the generated package/import/templ chain
// the same way on Windows, Linux and macOS). The mapping is deterministic so
// the same source string always yields the same identifier and all splice
// sites stay in sync.
package generator

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// goIdent converts an arbitrary string into a valid Go identifier. Runs of
// characters outside [A-Za-z0-9_] are collapsed to a single underscore, a
// leading digit is prefixed with an underscore, and an input that yields
// nothing (empty or all-punctuation) falls back to "_x". The raw input is
// never returned verbatim.
func goIdent(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range s {
		if isIdentRune(r) {
			if underscore && b.Len() > 0 {
				b.WriteByte('_')
			}
			underscore = false
			b.WriteRune(r)
		} else {
			underscore = true
		}
	}
	if b.Len() == 0 {
		return "_x"
	}
	out := b.String()
	if out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	return out
}

// isIdentRune reports whether r may appear inside a Go identifier. The
// character set is kept ASCII so non-ASCII names (diacritics, CJK, ...) map to
// a stable underscore-collapsed form instead of raw UTF-8 bytes.
func isIdentRune(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// capitalize upper-cases the first rune of s ("admin" -> "Admin"). It is safe
// on empty and non-ASCII inputs, unlike the x[:1] byte-slicing it replaces.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// resourcePkgName returns the lower-cased package/directory name used for a
// resource ("User" -> "user", "Order Management" -> "order_management"). All
// sites that derive a package name, directory or import path from a resource
// name must use this helper so they agree.
func resourcePkgName(name string) string {
	return goIdent(strings.ToLower(name))
}