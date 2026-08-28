// helpers.go
//
// Phase E7: virtual computed fields. Config expressions may reference built-in
// helpers.* SQL functions (helpers.date_diff(a, b), helpers.coalesce(a, b),
// helpers.now(), ...). The expression text lives in yaga.yaml and is emitted
// verbatim into generated SQL for the configured driver, so each helpers.*
// token is expanded HERE at generation time into driver-correct SQL. Nested
// helper calls are supported via an expand-to-fixpoint loop that always
// replaces the innermost balanced call first.
package generator

import (
	"fmt"
	"sort"
	"strings"
)

// helperCallSpan is one "helpers.<fn>(args)" match inside an expression.
type helperCallSpan struct {
	start int // byte offset of "helpers"
	end   int // byte offset of the closing ')'
	fn    string
	args  string // raw text between the parens (may contain nested calls)
}

// findHelperCalls scans expr for all balanced "helpers.<ident>(...)" calls.
// Nested calls are reported as their own spans, so the caller can expand the
// innermost one first.
func findHelperCalls(expr string) []helperCallSpan {
	const prefix = "helpers."
	var spans []helperCallSpan
	i := 0
	for i < len(expr) {
		idx := strings.Index(expr[i:], prefix)
		if idx < 0 {
			break
		}
		pos := i + idx
		j := pos + len(prefix)
		k := j
		for k < len(expr) && isHelperIdentPart(expr[k]) {
			k++
		}
		fn := expr[j:k]
		if fn != "" && k < len(expr) && expr[k] == '(' {
			depth := 0
			end := -1
			for m := k; m < len(expr); m++ {
				switch expr[m] {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth == 0 {
					end = m
					break
				}
			}
			if end > 0 {
				spans = append(spans, helperCallSpan{start: pos, end: end, fn: fn, args: expr[k+1 : end]})
				// Advance just past the opening paren so nested calls inside
				// this balanced call are also registered as spans.
				i = k + 1
				continue
			}
		}
		i = pos + len(prefix)
	}
	return spans
}

func isHelperIdentPart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// splitHelperArgs splits a helper's argument list on top-level commas, keeping
// nested parentheses (already-expanded inner expressions may contain them) and
// quoted text intact.
func splitHelperArgs(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	depth := 0
	start := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				if i+1 < len(s) && s[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				part := strings.TrimSpace(s[start:i])
				if part != "" {
					out = append(out, part)
				}
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

// innermostFirst orders helper-call spans with the innermost first: an inner
// call always closes before its enclosing call, so sorting by end offset
// ascending puts nested calls ahead of their parents. Expansion replaces one
// span at a time and re-scans, so this ordering guarantees nested calls are
// replaced inside-out.
func innermostFirst(spans []helperCallSpan) []helperCallSpan {
	out := make([]helperCallSpan, len(spans))
	copy(out, spans)
	sort.Slice(out, func(i, j int) bool { return out[i].end < out[j].end })
	return out
}

// expandHelpers replaces every helpers.<name>(...) token in expr with the
// driver-correct SQL expansion. Nested calls are expanded inner-out via a
// fixpoint loop; unknown helpers or wrong arities are left untouched (a broken
// call must not block the expansion of valid neighbours).
// Returns: the expression with all recognized helper calls expanded.
func (g *Generator) expandHelpers(expr string) string {
	driver := g.driver()
	for pass := 0; pass < 32; pass++ {
		spans := innermostFirst(findHelperCalls(expr))
		if len(spans) == 0 {
			return expr
		}
		changed := false
		for _, s := range spans {
			expanded, ok := g.sqlHelper(driver, s.fn, splitHelperArgs(s.args))
			if !ok {
				continue
			}
			expr = expr[:s.start] + expanded + expr[s.end+1:]
			changed = true
			break
		}
		if !changed {
			return expr
		}
	}
	return expr
}

// sqlHelper returns the driver-correct SQL for one built-in helpers.* function,
// or ok=false when the name is unknown or the arity does not match. Args are
// expression fragments (already helper-expanded) spliced verbatim.
func (g *Generator) sqlHelper(driver, name string, args []string) (string, bool) {
	switch name {
	case "date_diff", "year_diff", "month_diff":
		if len(args) != 2 {
			return "", false
		}
		a, b := args[0], args[1]
		switch {
		case g.isSQLite():
			switch name {
			case "date_diff":
				return fmt.Sprintf("(julianday(%s) - julianday(%s))", a, b), true
			case "year_diff":
				return fmt.Sprintf("CAST((julianday(%s) - julianday(%s)) / 365.25 AS INTEGER)", a, b), true
			default:
				return fmt.Sprintf("CAST((julianday(%s) - julianday(%s)) / 30.44 AS INTEGER)", a, b), true
			}
		case g.isMSSQL():
			switch name {
			case "date_diff":
				return fmt.Sprintf("DATEDIFF(DAY, %s, %s)", b, a), true
			case "year_diff":
				return fmt.Sprintf("DATEDIFF(YEAR, %s, %s)", b, a), true
			default:
				return fmt.Sprintf("DATEDIFF(MONTH, %s, %s)", b, a), true
			}
		default: // postgres
			switch name {
			case "date_diff":
				return fmt.Sprintf("EXTRACT(DAY FROM (%s)::timestamp - (%s)::timestamp)", a, b), true
			case "year_diff":
				return fmt.Sprintf("EXTRACT(YEAR FROM age((%s)::timestamp, (%s)::timestamp))", a, b), true
			default:
				return fmt.Sprintf("(EXTRACT(YEAR FROM age((%s)::timestamp, (%s)::timestamp)) * 12 + EXTRACT(MONTH FROM age((%s)::timestamp, (%s)::timestamp)))", a, b, a, b), true
			}
		}
	case "coalesce":
		if len(args) < 2 {
			return "", false
		}
		return "COALESCE(" + strings.Join(args, ", ") + ")", true
	case "ifnull":
		if len(args) != 2 {
			return "", false
		}
		switch {
		case g.isSQLite():
			return fmt.Sprintf("IFNULL(%s, %s)", args[0], args[1]), true
		case g.isMSSQL():
			return fmt.Sprintf("ISNULL(%s, %s)", args[0], args[1]), true
		default:
			return fmt.Sprintf("COALESCE(%s, %s)", args[0], args[1]), true
		}
	case "round":
		if len(args) != 2 {
			return "", false
		}
		if g.isSQLite() || g.isMSSQL() {
			return fmt.Sprintf("ROUND(%s, %s)", args[0], args[1]), true
		}
		return fmt.Sprintf("ROUND(%s::numeric, %s)", args[0], args[1]), true
	case "now":
		if len(args) != 0 {
			return "", false
		}
		switch {
		case g.isSQLite():
			return "datetime('now')", true
		case g.isMSSQL():
			return "GETDATE()", true
		default:
			return "NOW()", true
		}
	}
	return "", false
}