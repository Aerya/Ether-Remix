// Package screendoc compiles the documents a plugin ships for its screens and
// surfaces (docs/plugin-screens.md). A document is data: Vela's node language
// (owned by Vela, see its packages/protocol/screen.ts) plus two Ether-owned
// blocks, params and sources. Compile validates a document once, at install,
// and derives everything Ether needs to serve it and resolve its data; Serve
// produces the immutable, localised document a client downloads.
package screendoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits shared with Vela (SCREEN_LIMITS in Vela's screen.ts).
const (
	MaxDocumentBytes  = 256 << 10
	MaxNodes          = 4000
	MaxDepth          = 32
	MaxStringLength   = 4096
	MaxArrayItems     = 500
	MaxPaletteColours = 8
	MaxParams         = 8
	MaxSources        = 16
	MaxRequires       = 32
	MinRenderer       = 19
	MinRefreshSeconds = 60
)

// Presentations a document can be served as.
const (
	PresentSection       = "section"
	PresentPage          = "page"
	PresentPlayerOverlay = "playerOverlay"
	PresentToast         = "toast"
	PresentHomeRow       = "homeRow"
	PresentTitlePanel    = "titlePanel"
)

// Context names a surface hands Ether, by presentation. Each travels on data
// reads as p.<name> and is a hole {context.<name>} in source arguments.
var surfaceContext = map[string][]string{
	PresentPlayerOverlay: {"event", "channel", "title", "kind"},
	PresentToast:         {"alert.ref", "alert.id"},
	PresentHomeRow:       nil,
	PresentTitlePanel:    {"title"},
}

// ContextParam describes one context value a surface accepts.
type ContextParam struct {
	Name string
	// Ref is the record prefix the value must carry ("event", "channel",
	// "item"), or "" for a plain value.
	Ref string
}

// SurfaceContext lists the context a presentation hands Ether.
func SurfaceContext(presentation string) []ContextParam {
	out := []ContextParam{}
	for _, name := range surfaceContext[presentation] {
		p := ContextParam{Name: name}
		switch name {
		case "event", "alert.ref":
			p.Ref = "event"
		case "channel":
			p.Ref = "channel"
		case "title":
			p.Ref = "item"
		}
		out = append(out, p)
	}
	return out
}

var (
	slugRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	paramNameRe  = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,31}$`)
	sourceIDRe   = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,31}$`)
	stringKeyRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
	requireRe    = regexp.MustCompile(`^[a-zA-Z][A-Za-z0-9.-]{0,63}$`)
	colourRe     = regexp.MustCompile(`^(#[0-9a-fA-F]{3}|#[0-9a-fA-F]{6}|#[0-9a-fA-F]{8}|rgba?\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*(,\s*(0|1|0?\.\d+)\s*)?\))$`)
	holeRe       = regexp.MustCompile(`\{([a-z]+)\.([A-Za-z0-9_.]+)\}`)
	urlRe        = regexp.MustCompile(`(?i)^\s*(https?:)?//`)
	stringHoleRe = regexp.MustCompile(`\{s:([A-Za-z][A-Za-z0-9_.-]{0,63})\}`)
)

// topLevel lists the document keys an author may write. Keys Ether serves
// (screen, page, version, presentation, actions, deps) are accepted and
// replaced, so a served document can be copied back into a package.
var topLevel = map[string]bool{
	"schema": true, "kind": true, "renderer": true, "requires": true, "title": true,
	"params": true, "sources": true, "strings": true, "tokens": true, "palette": true,
	"templates": true, "motion": true, "keyframes": true, "tree": true, "loading": true,
	"failure": true, "refresh": true, "focus": true,
	"screen": true, "page": true, "version": true, "presentation": true, "actions": true,
}

var etherServed = map[string]bool{"screen": true, "page": true, "version": true, "presentation": true, "actions": true}

// FieldError is one problem, at a JSON pointer inside the document.
type FieldError struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

// Error collects every problem found in one document.
type Error struct {
	Path   string
	Fields []FieldError
}

func (e *Error) Error() string {
	if e == nil || len(e.Fields) == 0 {
		return "screendoc: invalid document"
	}
	return fmt.Sprintf("%s%s: %s", e.Path, e.Fields[0].Pointer, e.Fields[0].Message)
}

// Param is a declared page parameter.
type Param struct {
	Type    string   `json:"type"` // string|enum|date|ref|int|bool
	Default string   `json:"default,omitempty"`
	Max     int      `json:"max,omitempty"`
	Enum    []string `json:"enum,omitempty"`
}

// Source is a validated source declaration.
type Source struct {
	ID   string
	From string
	// Args are the source's arguments as written, templates intact.
	Args map[string]any
	Lazy bool
	// Deps are the page params and context names the arguments read.
	Deps []string
}

// Options supply what a document may refer to.
type Options struct {
	// Presentation the document is compiled for.
	Presentation string
	// Feeds are the plugin's own live feed ids; Kinds its own media kinds.
	Feeds []string
	Kinds []string
	// Asset reports whether the package holds path, for "asset:" values.
	Asset func(path string) bool
	// Text looks up the plugin's own English text, used when a document
	// names a key it does not define itself.
	Text func(key string) (string, bool)
}

// Compiled is a validated document, ready to serve.
type Compiled struct {
	Renderer int
	Requires []string
	Params   map[string]Param
	Sources  map[string]Source
	// Strings are the document's own default (English) texts by key.
	Strings map[string]string
	// Assets are the package paths named by "asset:" values.
	Assets []string
	// Hash identifies the compiled content (not the locale).
	Hash string

	// body is the validated document without the Ether-owned blocks, kept
	// as JSON so every Serve works on its own copy.
	body []byte
}

// SourceIDs returns the declared source ids in sorted order.
func (c *Compiled) SourceIDs() []string {
	ids := make([]string, 0, len(c.Sources))
	for id := range c.Sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

type compiler struct {
	opts       Options
	errs       []FieldError
	nodes      int
	assets     map[string]bool
	paramNames map[string]bool
	usedKeys   map[string]string // {s:key} -> first pointer using it
}

func (c *compiler) fail(pointer, format string, args ...any) {
	if len(c.errs) < 64 {
		c.errs = append(c.errs, FieldError{Pointer: pointer, Message: fmt.Sprintf(format, args...)})
	}
}

// Compile validates raw and returns the compiled document, or an *Error
// listing every problem. path prefixes error pointers ("screens/sports.json").
func Compile(path string, raw []byte, opts Options) (*Compiled, error) {
	c := &compiler{opts: opts, assets: map[string]bool{}, usedKeys: map[string]string{}}
	out, err := c.compile(raw)
	if err != nil {
		return nil, err
	}
	if len(c.errs) > 0 {
		return nil, &Error{Path: path, Fields: c.errs}
	}
	return out, nil
}

func (c *compiler) compile(raw []byte) (*Compiled, error) {
	if len(raw) > MaxDocumentBytes {
		c.fail("", "the document is %d KB; the limit is %d KB", len(raw)>>10, MaxDocumentBytes>>10)
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		c.fail("", "not valid JSON: %v", err)
		return nil, nil
	}
	if dec.More() {
		c.fail("", "more than one JSON value")
		return nil, nil
	}
	doc, ok := root.(map[string]any)
	if !ok {
		c.fail("", "must be a JSON object")
		return nil, nil
	}
	for k := range doc {
		if !topLevel[k] {
			c.fail("/"+k, "unknown field %q", k)
		}
	}
	if n, ok := intOf(doc["schema"]); !ok || n != 1 {
		c.fail("/schema", "must be 1")
	}
	if doc["kind"] != "screen" {
		c.fail("/kind", `must be "screen"`)
	}
	renderer, ok := intOf(doc["renderer"])
	if !ok || renderer < MinRenderer {
		c.fail("/renderer", "must be a Vela renderer version, %d or later", MinRenderer)
	}
	out := &Compiled{Renderer: renderer, Params: map[string]Param{}, Sources: map[string]Source{}, Strings: map[string]string{}}
	if v, present := doc["requires"]; present {
		list, ok := v.([]any)
		if !ok || len(list) > MaxRequires {
			c.fail("/requires", "must be a list of at most %d block names", MaxRequires)
		}
		seen := map[string]bool{}
		for i, item := range list {
			name, _ := item.(string)
			if !requireRe.MatchString(name) || seen[name] {
				c.fail(fmt.Sprintf("/requires/%d", i), "must be a unique block or kit name")
				continue
			}
			seen[name] = true
			out.Requires = append(out.Requires, name)
		}
	}
	if _, ok := doc["tree"].(map[string]any); !ok {
		c.fail("/tree", "is required: the document's root node")
	}
	if v, present := doc["refresh"]; present {
		if n, ok := intOf(v); !ok || n < MinRefreshSeconds || n > 86400 {
			c.fail("/refresh", "must be whole seconds between %d and 86400", MinRefreshSeconds)
		}
	}
	if v, present := doc["palette"]; present {
		pal, ok := v.(map[string]any)
		if !ok || len(pal) > MaxPaletteColours {
			c.fail("/palette", "must be an object of at most %d colours", MaxPaletteColours)
		}
		for k, col := range pal {
			if s, _ := col.(string); !colourRe.MatchString(s) {
				c.fail("/palette/"+k, "must be a colour: #rgb, #rrggbb, #rrggbbaa, rgb() or rgba()")
			}
		}
	}
	c.params(doc, out)
	c.paramNames = map[string]bool{}
	for name := range out.Params {
		c.paramNames[name] = true
	}
	c.strings(doc, out)
	c.sources(doc, out)
	// Every value: sizes, node count and depth, no URLs, assets resolved.
	body := map[string]any{}
	for k, v := range doc {
		if etherServed[k] || k == "params" || k == "sources" || k == "strings" {
			continue
		}
		body[k] = c.walk(v, "/"+k, 0)
	}
	for key, ptr := range c.usedKeys {
		if _, ok := out.Strings[key]; ok {
			continue
		}
		if c.opts.Text != nil {
			if t, ok := c.opts.Text(key); ok {
				out.Strings[key] = t
				continue
			}
		}
		c.fail(ptr, "{s:%s} has no text: add it to the document's strings or the plugin's English strings", key)
	}
	if c.nodes > MaxNodes {
		c.fail("/tree", "has %d nodes; the limit is %d", c.nodes, MaxNodes)
	}
	for a := range c.assets {
		out.Assets = append(out.Assets, a)
	}
	sort.Strings(out.Assets)
	out.body, _ = json.Marshal(body)
	canon, _ := json.Marshal(struct {
		Body    map[string]any    `json:"body"`
		Params  map[string]Param  `json:"params"`
		Sources map[string]any    `json:"sources"`
		Strings map[string]string `json:"strings"`
	}{body, out.Params, sourcesWire(out), out.Strings})
	sum := sha256.Sum256(canon)
	out.Hash = hex.EncodeToString(sum[:12])
	if len(c.errs) == 0 {
		served, err := out.Serve(ServeMeta{Screen: "screen", Page: "index", Version: "0.0.0", Title: "Title", Presentation: c.presentation()})
		if err != nil {
			c.fail("", "%v", err)
		} else {
			c.checkSchema(served)
		}
	}
	return out, nil
}

func (c *compiler) presentation() string {
	if c.opts.Presentation == "" {
		return PresentSection
	}
	return c.opts.Presentation
}

func (c *compiler) walk(v any, ptr string, depth int) any {
	switch n := v.(type) {
	case map[string]any:
		if t, ok := n["type"].(string); ok && t != "" {
			c.nodes++
			depth++
			if depth > MaxDepth {
				c.fail(ptr, "nodes are nested deeper than %d", MaxDepth)
				return n
			}
		}
		for k, child := range n {
			n[k] = c.walk(child, ptr+"/"+escapePointer(k), depth)
		}
		return n
	case []any:
		if len(n) > MaxArrayItems {
			c.fail(ptr, "has %d items; the limit is %d", len(n), MaxArrayItems)
		}
		for i, child := range n {
			n[i] = c.walk(child, ptr+"/"+strconv.Itoa(i), depth)
		}
		return n
	case string:
		return c.text(n, ptr)
	default:
		return v
	}
}

// text checks one string value: bounded, never a URL, and "asset:" paths
// resolved to the package.
func (c *compiler) text(s, ptr string) string {
	if utf8.RuneCountInString(s) > MaxStringLength {
		c.fail(ptr, "is longer than %d characters", MaxStringLength)
		return s
	}
	if urlRe.MatchString(s) || (strings.Contains(s, "url(") && strings.Contains(s, "//")) {
		c.fail(ptr, "documents never contain URLs; ship the file in the package and write asset:<path>")
		return s
	}
	for _, m := range stringHoleRe.FindAllStringSubmatch(s, -1) {
		if _, seen := c.usedKeys[m[1]]; !seen {
			c.usedKeys[m[1]] = ptr
		}
	}
	if rest, ok := strings.CutPrefix(s, "asset:"); ok {
		p := strings.TrimPrefix(rest, "/")
		if p == "" || strings.Contains(p, "..") || c.opts.Asset == nil || !c.opts.Asset(p) {
			c.fail(ptr, "the package has no file %q", rest)
			return s
		}
		c.assets[p] = true
		return AssetPrefix + p
	}
	return s
}

// AssetPrefix marks an asset path in a compiled document; Serve rewrites it to
// the served asset URL.
const AssetPrefix = "asset:"

func (c *compiler) params(doc map[string]any, out *Compiled) {
	v, present := doc["params"]
	if !present {
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		c.fail("/params", "must be an object of parameters")
		return
	}
	if len(m) > MaxParams {
		c.fail("/params", "declares %d parameters; the limit is %d", len(m), MaxParams)
	}
	for name, raw := range m {
		ptr := "/params/" + escapePointer(name)
		if !paramNameRe.MatchString(name) {
			c.fail(ptr, "parameter names are camelCase letters and digits, starting with a lower-case letter")
			continue
		}
		spec, ok := raw.(map[string]any)
		if !ok {
			c.fail(ptr, "must be an object")
			continue
		}
		p := Param{}
		for k := range spec {
			switch k {
			case "type", "default", "max", "enum":
			default:
				c.fail(ptr+"/"+k, "unknown field %q", k)
			}
		}
		p.Type, _ = spec["type"].(string)
		switch p.Type {
		case "string", "enum", "date", "ref", "int", "bool":
		default:
			c.fail(ptr+"/type", "must be string, enum, date, ref, int or bool")
		}
		if mx, present := spec["max"]; present {
			n, ok := intOf(mx)
			if !ok || n < 1 || n > 512 {
				c.fail(ptr+"/max", "must be between 1 and 512")
			}
			p.Max = n
		}
		if list, present := spec["enum"]; present {
			items, ok := list.([]any)
			if !ok || len(items) == 0 || len(items) > 50 {
				c.fail(ptr+"/enum", "must list 1 to 50 values")
			}
			for i, it := range items {
				s, _ := it.(string)
				if s == "" || len(s) > 64 {
					c.fail(fmt.Sprintf("%s/enum/%d", ptr, i), "must be a value of 1 to 64 characters")
					continue
				}
				p.Enum = append(p.Enum, s)
			}
		}
		if p.Type == "enum" && len(p.Enum) == 0 {
			c.fail(ptr+"/enum", "an enum parameter lists its values")
		}
		if d, present := spec["default"]; present {
			s, ok := d.(string)
			if !ok {
				c.fail(ptr+"/default", "must be a string")
			} else if s != "" {
				if _, err := CheckParam(p, s); err != nil {
					c.fail(ptr+"/default", "%v", err)
				}
			}
			p.Default = s
		}
		out.Params[name] = p
	}
}

func (c *compiler) strings(doc map[string]any, out *Compiled) {
	v, present := doc["strings"]
	if !present {
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		c.fail("/strings", "must be an object of text by key")
		return
	}
	for k, raw := range m {
		ptr := "/strings/" + escapePointer(k)
		s, ok := raw.(string)
		if !stringKeyRe.MatchString(k) || !ok {
			c.fail(ptr, "must be text under a key of letters, digits, _ . -")
			continue
		}
		out.Strings[k] = c.text(s, ptr)
	}
}

// CheckParam validates one parameter value against its declaration and
// returns it canonicalised.
func CheckParam(p Param, v string) (string, error) {
	if utf8.RuneCountInString(v) > 512 {
		return "", fmt.Errorf("is longer than 512 characters")
	}
	switch p.Type {
	case "string":
		if len(p.Enum) > 0 && v != "" {
			for _, e := range p.Enum {
				if e == v {
					return v, nil
				}
			}
			return "", fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
		}
		max := p.Max
		if max == 0 {
			max = 128
		}
		if utf8.RuneCountInString(v) > max {
			return "", fmt.Errorf("is longer than %d characters", max)
		}
	case "enum":
		for _, e := range p.Enum {
			if e == v {
				return v, nil
			}
		}
		return "", fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
	case "date":
		if !dateRe.MatchString(v) {
			return "", fmt.Errorf("must be a date, YYYY-MM-DD")
		}
	case "ref":
		if !refRe.MatchString(v) {
			return "", fmt.Errorf("must be a record key such as event:<id>")
		}
	case "int":
		n, err := strconv.Atoi(v)
		if err != nil || (p.Max > 0 && (n < -p.Max || n > p.Max)) {
			return "", fmt.Errorf("must be a whole number")
		}
		return strconv.Itoa(n), nil
	case "bool":
		switch v {
		case "true", "1":
			return "true", nil
		case "false", "0":
			return "false", nil
		}
		return "", fmt.Errorf("must be true or false")
	}
	return v, nil
}

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	refRe  = regexp.MustCompile(`^(event|entity|item|channel|p):[^\s]{1,300}$`)
)

func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := strconv.Atoi(n.String())
		return i, err == nil
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	case int:
		return n, true
	}
	return 0, false
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
