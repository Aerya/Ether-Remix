package flows

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxTemplateDepth = 8

// HasTemplateSyntax reports whether s contains a "{{ ... }}" expression or block marker.
func HasTemplateSyntax(s string) bool {
	return strings.Contains(s, "{{") || strings.Contains(s, "}}")
}

// ValidateTemplate checks that all "{{ ... }}" expressions and conditional
// blocks ({{#if ...}}...{{#else}}...{{/if}}, {{#unless ...}}...{{/unless}})
// in tmpl are balanced, within nesting limits, and use only recognized filters
// and operators.
func ValidateTemplate(tmpl string) error {
	if !HasTemplateSyntax(tmpl) {
		return nil
	}
	_, err := parseTemplate(tmpl)
	return err
}

// RenderTemplate evaluates all "{{ expr }}" placeholders and inline
// "{{#if ...}}" / "{{#unless ...}}" blocks inside tmpl against ctx.
func RenderTemplate(tmpl string, ctx EvalContext) (string, error) {
	if !HasTemplateSyntax(tmpl) {
		return tmpl, nil
	}
	nodes, err := parseTemplate(tmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := renderTemplateNodes(&b, nodes, ctx); err != nil {
		return "", err
	}
	return b.String(), nil
}

// RenderParams recursively renders template strings inside an action params map.
// If a string value is strictly a single "{{ path }}" with no surrounding text,
// no block directive, and no filters, the resolved value preserves its native
// Go type (e.g. int/bool/slice).
func RenderParams(params map[string]any, ctx EvalContext) (map[string]any, error) {
	if params == nil {
		return map[string]any{}, nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		rv, err := renderAny(v, ctx)
		if err != nil {
			return nil, fmt.Errorf("param %q: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

func renderAny(v any, ctx EvalContext) (any, error) {
	switch x := v.(type) {
	case string:
		trimmed := strings.TrimSpace(x)
		if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") &&
			strings.Count(trimmed, "{{") == 1 && strings.Count(trimmed, "}}") == 1 &&
			trimmed == x {
			expr := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
			if !strings.HasPrefix(expr, "#") && !strings.HasPrefix(expr, "/") {
				path, filters, err := parseTemplateExpr(expr)
				if err != nil {
					return nil, err
				}
				if len(filters) == 0 {
					val, ok := ResolvePath(path, ctx)
					if !ok {
						return "", nil
					}
					return val, nil
				}
			}
		}
		return RenderTemplate(x, ctx)
	case map[string]any:
		return RenderParams(x, ctx)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			rv, err := renderAny(item, ctx)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case []string:
		out := make([]string, len(x))
		for i, item := range x {
			rv, err := RenderTemplate(item, ctx)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

// --- Template AST & Parser ---

type tmplNodeKind uint8

const (
	tmplText tmplNodeKind = iota
	tmplExpr
	tmplCond
)

type tmplNode struct {
	kind     tmplNodeKind
	text     string
	path     string
	filters  []templateFilter
	cond     tmplCondExpr
	unless   bool
	thenBody []tmplNode
	elseBody []tmplNode
}

type tmplCondExpr struct {
	path   string
	op     string // "" (truthy), "==", "!=", ">", ">=", "<", "<=", "contains"
	hasVal bool
	val    any
}

type tmplTokenKind uint8

const (
	tokText tmplTokenKind = iota
	tokExpr
	tokIf
	tokUnless
	tokElse
	tokEndIf
	tokEndUnless
)

type tmplToken struct {
	kind tmplTokenKind
	raw  string
}

func tokenizeTemplate(tmpl string) ([]tmplToken, error) {
	var tokens []tmplToken
	rest := tmpl
	for {
		openIdx := strings.Index(rest, "{{")
		closeIdx := strings.Index(rest, "}}")
		if closeIdx >= 0 && (openIdx < 0 || closeIdx < openIdx) {
			return nil, fmt.Errorf("flows: template has unmatched closing }}")
		}
		if openIdx < 0 {
			if rest != "" {
				tokens = append(tokens, tmplToken{kind: tokText, raw: rest})
			}
			return tokens, nil
		}
		if openIdx > 0 {
			tokens = append(tokens, tmplToken{kind: tokText, raw: rest[:openIdx]})
		}
		after := rest[openIdx+2:]
		end := strings.Index(after, "}}")
		if end < 0 {
			return nil, fmt.Errorf("flows: template has unclosed {{")
		}
		tag := strings.TrimSpace(after[:end])
		if tag == "" {
			return nil, fmt.Errorf("flows: empty template expression")
		}
		switch {
		case tag == "#else":
			tokens = append(tokens, tmplToken{kind: tokElse})
		case tag == "/if":
			tokens = append(tokens, tmplToken{kind: tokEndIf})
		case tag == "/unless":
			tokens = append(tokens, tmplToken{kind: tokEndUnless})
		case tag == "#if" || strings.HasPrefix(tag, "#if ") || strings.HasPrefix(tag, "#if\t"):
			tokens = append(tokens, tmplToken{kind: tokIf, raw: strings.TrimSpace(tag[3:])})
		case tag == "#unless" || strings.HasPrefix(tag, "#unless ") || strings.HasPrefix(tag, "#unless\t"):
			tokens = append(tokens, tmplToken{kind: tokUnless, raw: strings.TrimSpace(tag[7:])})
		case strings.HasPrefix(tag, "#") || strings.HasPrefix(tag, "/"):
			return nil, fmt.Errorf("flows: unknown template directive %q", tag)
		default:
			tokens = append(tokens, tmplToken{kind: tokExpr, raw: tag})
		}
		rest = after[end+2:]
	}
}

func parseTemplate(tmpl string) ([]tmplNode, error) {
	tokens, err := tokenizeTemplate(tmpl)
	if err != nil {
		return nil, err
	}
	pos := 0
	nodes, stop, err := parseTemplateBlock(tokens, &pos, 0, false)
	if err != nil {
		return nil, err
	}
	if stop != tokText {
		return nil, fmt.Errorf("flows: unexpected template block terminator")
	}
	return nodes, nil
}

func parseTemplateBlock(tokens []tmplToken, pos *int, depth int, inBlock bool) ([]tmplNode, tmplTokenKind, error) {
	if depth > maxTemplateDepth {
		return nil, tokText, fmt.Errorf("flows: template conditional nesting exceeds %d", maxTemplateDepth)
	}
	var out []tmplNode
	for *pos < len(tokens) {
		tok := tokens[*pos]
		*pos++
		switch tok.kind {
		case tokText:
			out = append(out, tmplNode{kind: tmplText, text: tok.raw})
		case tokExpr:
			path, filters, err := parseTemplateExpr(tok.raw)
			if err != nil {
				return nil, tokText, err
			}
			out = append(out, tmplNode{kind: tmplExpr, path: path, filters: filters})
		case tokIf, tokUnless:
			cond, err := parseCondExpr(tok.raw)
			if err != nil {
				return nil, tokText, err
			}
			thenBody, stop, err := parseTemplateBlock(tokens, pos, depth+1, true)
			if err != nil {
				return nil, tokText, err
			}
			var elseBody []tmplNode
			if stop == tokElse {
				elseBody, stop, err = parseTemplateBlock(tokens, pos, depth+1, true)
				if err != nil {
					return nil, tokText, err
				}
				if stop == tokElse {
					return nil, tokText, fmt.Errorf("flows: duplicate {{#else}} in template block")
				}
			}
			wantEnd := tokEndIf
			wantName := "{{/if}}"
			if tok.kind == tokUnless {
				wantEnd = tokEndUnless
				wantName = "{{/unless}}"
			}
			if stop != wantEnd {
				return nil, tokText, fmt.Errorf("flows: unclosed template block, expected %s", wantName)
			}
			out = append(out, tmplNode{
				kind:     tmplCond,
				cond:     cond,
				unless:   tok.kind == tokUnless,
				thenBody: thenBody,
				elseBody: elseBody,
			})
		case tokElse, tokEndIf, tokEndUnless:
			if !inBlock {
				return nil, tokText, fmt.Errorf("flows: unexpected template block tag outside conditional")
			}
			return out, tok.kind, nil
		}
	}
	return out, tokText, nil
}

func parseCondExpr(raw string) (tmplCondExpr, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return tmplCondExpr{}, fmt.Errorf("flows: empty condition in template block")
	}
	parts, err := splitWordsOutsideQuotes(raw)
	if err != nil {
		return tmplCondExpr{}, err
	}
	if len(parts) == 1 {
		return tmplCondExpr{path: parts[0]}, nil
	}
	if len(parts) != 3 {
		return tmplCondExpr{}, fmt.Errorf("flows: invalid template condition %q (expected '<path>' or '<path> <op> <value>')", raw)
	}
	op := parts[1]
	switch op {
	case "==", "!=", ">", ">=", "<", "<=", "contains":
	default:
		return tmplCondExpr{}, fmt.Errorf("flows: unsupported template condition operator %q", op)
	}
	val, err := parseCondLiteral(parts[2])
	if err != nil {
		return tmplCondExpr{}, err
	}
	return tmplCondExpr{path: parts[0], op: op, hasVal: true, val: val}, nil
}

func parseCondLiteral(raw string) (any, error) {
	if len(raw) >= 2 && ((raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
		return raw[1 : len(raw)-1], nil
	}
	if strings.EqualFold(raw, "true") {
		return true, nil
	}
	if strings.EqualFold(raw, "false") {
		return false, nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n, nil
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return f, nil
	}
	return nil, fmt.Errorf("flows: invalid template condition literal %q (quote strings with \"...\")", raw)
}

func renderTemplateNodes(b *strings.Builder, nodes []tmplNode, ctx EvalContext) error {
	for _, n := range nodes {
		switch n.kind {
		case tmplText:
			b.WriteString(n.text)
		case tmplExpr:
			val, found := ResolvePath(n.path, ctx)
			if !found {
				val = nil
			}
			var err error
			for _, f := range n.filters {
				val, err = applyTemplateFilter(val, f)
				if err != nil {
					return err
				}
			}
			b.WriteString(formatTemplateValue(val))
		case tmplCond:
			matched := evalCondExpr(n.cond, ctx)
			if n.unless {
				matched = !matched
			}
			body := n.elseBody
			if matched {
				body = n.thenBody
			}
			if err := renderTemplateNodes(b, body, ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func evalCondExpr(c tmplCondExpr, ctx EvalContext) bool {
	val, found := ResolvePath(c.path, ctx)
	if !c.hasVal {
		return found && isTruthy(val)
	}
	switch c.op {
	case "==":
		return found && valuesEqual(val, c.val, false)
	case "!=":
		return !found || !valuesEqual(val, c.val, false)
	case ">", ">=", "<", "<=":
		if !found {
			return false
		}
		cmp, ok := compareOrdered(val, c.val)
		if !ok {
			return false
		}
		switch c.op {
		case ">":
			return cmp > 0
		case ">=":
			return cmp >= 0
		case "<":
			return cmp < 0
		case "<=":
			return cmp <= 0
		}
	case "contains":
		return found && evalContains(val, c.val, false)
	}
	return false
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i != 0
		}
		if f, err := x.Float64(); err == nil {
			return f != 0 && !math.IsNaN(f)
		}
		return false
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0 && !math.IsNaN(x)
	case []string:
		return len(x) > 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		if items, ok := asSlice(v); ok {
			return len(items) > 0
		}
		if f, ok := asFloat(v); ok {
			return f != 0 && !math.IsNaN(f)
		}
		return formatTemplateValue(v) != ""
	}
}

// --- Filters ---

type templateFilter struct {
	name string
	args []string
}

func parseTemplateExpr(expr string) (path string, filters []templateFilter, err error) {
	if expr == "" {
		return "", nil, fmt.Errorf("flows: empty template expression")
	}
	parts := splitPipeOutsideQuotes(expr)
	path = strings.TrimSpace(parts[0])
	if path == "" {
		return "", nil, fmt.Errorf("flows: empty path in template expression")
	}
	for _, raw := range parts[1:] {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", nil, fmt.Errorf("flows: empty filter in template expression")
		}
		words, splitErr := splitWordsOutsideQuotes(raw)
		if splitErr != nil {
			return "", nil, splitErr
		}
		name := words[0]
		args := make([]string, 0, len(words)-1)
		for _, w := range words[1:] {
			args = append(args, unquoteTemplateArg(w))
		}
		switch name {
		case "upper", "lower", "title", "trim", "bytes", "duration", "json", "first", "last", "count":
			if len(args) > 0 {
				return "", nil, fmt.Errorf("flows: filter %q takes no arguments", name)
			}
		case "default":
			if len(args) != 1 {
				return "", nil, fmt.Errorf("flows: default filter requires 1 argument")
			}
		case "join":
			if len(args) > 1 {
				return "", nil, fmt.Errorf("flows: join filter takes at most 1 separator argument")
			}
		case "truncate":
			if len(args) != 1 {
				return "", nil, fmt.Errorf("flows: truncate filter requires a positive integer length")
			}
			if n, convErr := strconv.Atoi(args[0]); convErr != nil || n <= 0 {
				return "", nil, fmt.Errorf("flows: truncate filter requires a positive integer length")
			}
		case "slice":
			if len(args) < 1 || len(args) > 2 {
				return "", nil, fmt.Errorf("flows: slice filter requires 1 or 2 integer arguments (start [end])")
			}
			start, err1 := strconv.Atoi(args[0])
			if err1 != nil || start < 0 {
				return "", nil, fmt.Errorf("flows: slice start must be a non-negative integer")
			}
			if len(args) == 2 {
				end, err2 := strconv.Atoi(args[1])
				if err2 != nil || end < start {
					return "", nil, fmt.Errorf("flows: slice end must be an integer >= start")
				}
			}
		case "percent":
			if len(args) > 1 {
				return "", nil, fmt.Errorf("flows: percent filter takes at most 1 precision argument")
			}
			if len(args) == 1 {
				if p, convErr := strconv.Atoi(args[0]); convErr != nil || p < 0 || p > 4 {
					return "", nil, fmt.Errorf("flows: percent precision must be an integer between 0 and 4")
				}
			}
		default:
			return "", nil, fmt.Errorf("flows: unknown template filter %q", name)
		}
		filters = append(filters, templateFilter{name: name, args: args})
	}
	return path, filters, nil
}

func applyTemplateFilter(val any, f templateFilter) (any, error) {
	switch f.name {
	case "default":
		if val == nil || formatTemplateValue(val) == "" {
			return f.args[0], nil
		}
		return val, nil
	case "upper":
		return strings.ToUpper(formatTemplateValue(val)), nil
	case "lower":
		return strings.ToLower(formatTemplateValue(val)), nil
	case "title":
		return titleCase(formatTemplateValue(val)), nil
	case "trim":
		return strings.TrimSpace(formatTemplateValue(val)), nil
	case "truncate":
		n, _ := strconv.Atoi(f.args[0])
		s := formatTemplateValue(val)
		if utf8.RuneCountInString(s) <= n {
			return s, nil
		}
		r := []rune(s)
		if n <= 1 {
			return string(r[:n]), nil
		}
		return string(r[:n-1]) + "…", nil
	case "slice":
		start, _ := strconv.Atoi(f.args[0])
		hasEnd := len(f.args) == 2
		end := 0
		if hasEnd {
			end, _ = strconv.Atoi(f.args[1])
		}
		if items, ok := asSlice(val); ok {
			if start >= len(items) {
				return []any{}, nil
			}
			hi := len(items)
			if hasEnd && end < hi {
				hi = end
			}
			out := make([]any, hi-start)
			copy(out, items[start:hi])
			return out, nil
		}
		r := []rune(formatTemplateValue(val))
		if start >= len(r) {
			return "", nil
		}
		hi := len(r)
		if hasEnd && end < hi {
			hi = end
		}
		return string(r[start:hi]), nil
	case "join":
		sep := ", "
		if len(f.args) == 1 {
			sep = f.args[0]
		}
		if items, ok := asSlice(val); ok {
			parts := make([]string, len(items))
			for i, item := range items {
				parts[i] = formatTemplateValue(item)
			}
			return strings.Join(parts, sep), nil
		}
		return formatTemplateValue(val), nil
	case "first":
		if items, ok := asSlice(val); ok {
			if len(items) == 0 {
				return "", nil
			}
			return items[0], nil
		}
		return val, nil
	case "last":
		if items, ok := asSlice(val); ok {
			if len(items) == 0 {
				return "", nil
			}
			return items[len(items)-1], nil
		}
		return val, nil
	case "count":
		if val == nil {
			return 0, nil
		}
		if items, ok := asSlice(val); ok {
			return len(items), nil
		}
		if m, ok := val.(map[string]any); ok {
			return len(m), nil
		}
		return utf8.RuneCountInString(formatTemplateValue(val)), nil
	case "bytes":
		n, ok := asFloat(val)
		if !ok {
			return formatTemplateValue(val), nil
		}
		return formatBytes(n), nil
	case "duration":
		ms, ok := asFloat(val)
		if !ok {
			return formatTemplateValue(val), nil
		}
		return (time.Duration(ms) * time.Millisecond).Round(time.Millisecond).String(), nil
	case "percent":
		n, ok := asFloat(val)
		if !ok {
			return formatTemplateValue(val), nil
		}
		prec := 0
		if len(f.args) == 1 {
			prec, _ = strconv.Atoi(f.args[0])
		}
		return strconv.FormatFloat(n*100, 'f', prec, 64) + "%", nil
	case "json":
		b, err := json.Marshal(val)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return nil, fmt.Errorf("flows: unknown template filter %q", f.name)
	}
}

func titleCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	newWord := true
	for _, r := range s {
		if unicode.IsSpace(r) || r == '-' || r == '_' || r == '/' {
			newWord = true
			b.WriteRune(r)
			continue
		}
		if newWord {
			b.WriteRune(unicode.ToUpper(r))
			newWord = false
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func formatTemplateValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case []string:
		return strings.Join(x, ", ")
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = formatTemplateValue(item)
		}
		return strings.Join(parts, ", ")
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

func formatBytes(n float64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
		tib = 1024 * gib
	)
	switch {
	case n >= tib:
		return fmt.Sprintf("%.2f TiB", n/tib)
	case n >= gib:
		return fmt.Sprintf("%.2f GiB", n/gib)
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", n/mib)
	case n >= kib:
		return fmt.Sprintf("%.0f KiB", n/kib)
	default:
		return fmt.Sprintf("%.0f B", n)
	}
}

func splitPipeOutsideQuotes(s string) []string {
	var parts []string
	var cur strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			cur.WriteRune(r)
		case r == '|':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	parts = append(parts, cur.String())
	return parts
}

func splitWordsOutsideQuotes(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
			cur.WriteRune(r)
		case unicode.IsSpace(r):
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("flows: unterminated quote in template expression")
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words, nil
}

func unquoteTemplateArg(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}
