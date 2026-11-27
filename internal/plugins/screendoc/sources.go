package screendoc

import (
	"fmt"
	"sort"
	"strings"
)

// Source types (docs/plugin-screens.md, Sources). Every type is generic: the
// live* types read the live-events hub for one of the plugin's own feeds.
const (
	FromLiveEvents   = "liveEvents"
	FromLiveSchedule = "liveSchedule"
	FromLiveEvent    = "liveEvent"
	FromLiveTables   = "liveTables"
	FromLiveEntities = "liveEntities"
	FromLiveLeagues  = "liveLeagues"
	FromWatch        = "watch"
	FromViewer       = "viewer"
	FromItems        = "items"
	FromLiveItems    = "liveItems"
	FromChannels     = "channels"
	FromPlugin       = "plugin"
)

// argKind says how an argument is checked.
type argKind int

const (
	argText   argKind = iota // a string, holes allowed
	argFeed                  // one of the plugin's feeds, no holes
	argKind_                 // one of the plugin's media kinds, no holes
	argRef                   // a record key, normally a hole
	argEnum                  // one of a fixed set, no holes
	argInt                   // a whole number in range
	argBool                  // true/false
	argObject                // nested, checked by its own table
	argList                  // a list of text (holes allowed per item)
	argArgs                  // plugin args: an object of text
)

type argSpec struct {
	kind     argKind
	required bool
	values   []string
	min, max int
	fields   map[string]argSpec
}

var eventQuery = map[string]argSpec{
	"state":     {kind: argList, values: []string{"pre", "in", "post", "delayed", "postponed", "canceled"}, max: 6},
	"league":    {kind: argText},
	"favorites": {kind: argBool},
	"window":    {kind: argEnum, values: []string{"today", "week", "recent", "ticker"}},
	"category":  {kind: argText},
	"date":      {kind: argText},
	"ids":       {kind: argList, max: 50},
}

var sourceTypes = map[string]map[string]argSpec{
	FromLiveEvents: {
		"feed":    {kind: argFeed, required: true},
		"query":   {kind: argObject, fields: eventQuery},
		"sort":    {kind: argEnum, values: []string{"start", "featured"}},
		"limit":   {kind: argInt, min: 1, max: 100},
		"exclude": {kind: argRef},
	},
	FromLiveSchedule: {
		"feed":   {kind: argFeed, required: true},
		"league": {kind: argText},
		"date":   {kind: argText},
	},
	FromLiveEvent: {
		"feed": {kind: argFeed, required: true},
		"id":   {kind: argRef, required: true},
	},
	FromLiveTables: {
		"feed":   {kind: argFeed, required: true},
		"league": {kind: argText},
	},
	FromLiveEntities: {
		"feed":   {kind: argFeed, required: true},
		"kind":   {kind: argText, required: true},
		"league": {kind: argText},
		"prefer": {kind: argText},
		"q":      {kind: argText},
	},
	FromLiveLeagues: {
		"feed": {kind: argFeed, required: true},
	},
	FromWatch: {
		"event": {kind: argRef, required: true},
	},
	FromViewer: {},
	FromItems: {
		"kind":      {kind: argKind_, required: true},
		"level":     {kind: argText},
		"sort":      {kind: argEnum, values: []string{"title", "added", "released", "year", "rating", "index", "lastViewed"}},
		"direction": {kind: argEnum, values: []string{"asc", "desc"}},
		"facet":     {kind: argText},
		"values":    {kind: argList, max: 20},
		"limit":     {kind: argInt, min: 1, max: 50},
	},
	FromLiveItems: {
		"kind":   {kind: argKind_, required: true},
		"tab":    {kind: argText},
		"q":      {kind: argText},
		"filter": {kind: argText},
	},
	FromChannels: {
		"ids":   {kind: argList, max: 50},
		"group": {kind: argText},
	},
	FromPlugin: {
		"route":      {kind: argEnum, required: true},
		"args":       {kind: argArgs},
		"scope":      {kind: argEnum, values: []string{"shared", "viewer"}},
		"maxAgeSec":  {kind: argInt, min: 5, max: 3600},
		"refreshSec": {kind: argInt, min: 10, max: 3600},
	},
}

var routeRe = slugRe

func (c *compiler) sources(doc map[string]any, out *Compiled) {
	v, present := doc["sources"]
	if !present {
		return
	}
	m, ok := v.(map[string]any)
	if !ok {
		c.fail("/sources", "must be an object of sources by id")
		return
	}
	if len(m) > MaxSources {
		c.fail("/sources", "declares %d sources; the limit is %d", len(m), MaxSources)
	}
	for id, raw := range m {
		ptr := "/sources/" + escapePointer(id)
		if !sourceIDRe.MatchString(id) {
			c.fail(ptr, "source ids are camelCase letters and digits, starting with a lower-case letter")
			continue
		}
		spec, ok := raw.(map[string]any)
		if !ok {
			c.fail(ptr, "must be an object")
			continue
		}
		from, _ := spec["from"].(string)
		table, known := sourceTypes[from]
		if !known {
			c.fail(ptr+"/from", "unknown source type %q", from)
			continue
		}
		src := Source{ID: id, From: from, Args: map[string]any{}}
		deps := map[string]bool{}
		for k, val := range spec {
			switch k {
			case "from":
				continue
			case "lazy":
				b, ok := val.(bool)
				if !ok {
					c.fail(ptr+"/lazy", "must be true or false")
				}
				src.Lazy = b
				continue
			case "deps":
				// Served by Ether; ignored when copied back in.
				continue
			}
			as, ok := table[k]
			if !ok {
				c.fail(ptr+"/"+escapePointer(k), "%s sources take no %q argument", from, k)
				continue
			}
			src.Args[k] = c.arg(from, as, val, ptr+"/"+escapePointer(k), deps, out)
		}
		for k, as := range table {
			if as.required {
				if _, ok := spec[k]; !ok {
					c.fail(ptr+"/"+k, "is required for %s sources", from)
				}
			}
		}
		for d := range deps {
			src.Deps = append(src.Deps, d)
		}
		sort.Strings(src.Deps)
		out.Sources[id] = src
	}
}

func (c *compiler) arg(from string, as argSpec, val any, ptr string, deps map[string]bool, out *Compiled) any {
	switch as.kind {
	case argFeed:
		s, _ := val.(string)
		if !contains(c.opts.Feeds, s) {
			c.fail(ptr, "must be one of this plugin's live feeds (%s)", strings.Join(c.opts.Feeds, ", "))
		}
		return s
	case argKind_:
		s, _ := val.(string)
		if !contains(c.opts.Kinds, s) {
			c.fail(ptr, "must be one of this plugin's media kinds (%s)", strings.Join(c.opts.Kinds, ", "))
		}
		return s
	case argEnum:
		s, ok := val.(string)
		if from == FromPlugin && strings.HasSuffix(ptr, "/route") {
			if !ok || !routeRe.MatchString(s) {
				c.fail(ptr, "must be a route name matching ^[a-z][a-z0-9-]{0,31}$")
			}
			return s
		}
		if !ok || !contains(as.values, s) {
			c.fail(ptr, "must be one of %s", strings.Join(as.values, ", "))
		}
		return s
	case argInt:
		n, ok := intOf(val)
		if !ok || n < as.min || n > as.max {
			c.fail(ptr, "must be a whole number from %d to %d", as.min, as.max)
		}
		return n
	case argBool:
		if s, ok := val.(string); ok && isHole(s) {
			c.holes(s, ptr, deps, out)
			return s
		}
		b, ok := val.(bool)
		if !ok {
			c.fail(ptr, "must be true or false")
		}
		return b
	case argText, argRef:
		s, ok := val.(string)
		if !ok {
			c.fail(ptr, "must be text")
			return val
		}
		if len(s) > 256 {
			c.fail(ptr, "is longer than 256 characters")
		}
		if urlRe.MatchString(s) {
			c.fail(ptr, "source arguments never contain URLs")
		}
		c.holes(s, ptr, deps, out)
		if as.kind == argRef && !isHole(s) && !refRe.MatchString(s) {
			c.fail(ptr, "must be a record key such as event:<id>, or a hole such as {param.event}")
		}
		return s
	case argList:
		items, ok := val.([]any)
		if !ok || (as.max > 0 && len(items) > as.max) {
			c.fail(ptr, "must be a list of at most %d values", as.max)
			return val
		}
		list := make([]any, 0, len(items))
		for i, it := range items {
			s, ok := it.(string)
			ip := fmt.Sprintf("%s/%d", ptr, i)
			if !ok {
				c.fail(ip, "must be text")
				continue
			}
			if len(as.values) > 0 && !contains(as.values, s) {
				c.fail(ip, "must be one of %s", strings.Join(as.values, ", "))
			}
			c.holes(s, ip, deps, out)
			list = append(list, s)
		}
		return list
	case argObject:
		obj, ok := val.(map[string]any)
		if !ok {
			c.fail(ptr, "must be an object")
			return val
		}
		res := map[string]any{}
		for k, v := range obj {
			sub, ok := as.fields[k]
			if !ok {
				c.fail(ptr+"/"+escapePointer(k), "unknown field %q", k)
				continue
			}
			res[k] = c.arg(from, sub, v, ptr+"/"+escapePointer(k), deps, out)
		}
		return res
	case argArgs:
		obj, ok := val.(map[string]any)
		if !ok || len(obj) > 16 {
			c.fail(ptr, "must be an object of at most 16 text values")
			return val
		}
		res := map[string]any{}
		for k, v := range obj {
			if !paramNameRe.MatchString(k) {
				c.fail(ptr+"/"+escapePointer(k), "argument names are camelCase letters and digits")
				continue
			}
			res[k] = c.arg(from, argSpec{kind: argText}, v, ptr+"/"+escapePointer(k), deps, out)
		}
		return res
	}
	return val
}

func isHole(s string) bool {
	return holeRe.MatchString(s)
}

// holes checks every {scope.name} in s: params must be declared, context
// names must be ones the presentation hands over, viewer.tz is the only
// viewer value. Each found param or context name becomes a dependency.
func (c *compiler) holes(s, ptr string, deps map[string]bool, out *Compiled) {
	for _, m := range holeRe.FindAllStringSubmatch(s, -1) {
		scope, name := m[1], m[2]
		switch scope {
		case "param":
			if !c.declaredParam(name) {
				c.fail(ptr, "{param.%s} is not a declared parameter", name)
				continue
			}
			deps["param."+name] = true
		case "context":
			if !contains(surfaceContext[c.opts.Presentation], name) {
				allowed := surfaceContext[c.opts.Presentation]
				if len(allowed) == 0 {
					c.fail(ptr, "{context.%s}: a %s has no context", name, presentationName(c.opts.Presentation))
				} else {
					c.fail(ptr, "{context.%s} is not given to a %s (it has %s)", name, presentationName(c.opts.Presentation), strings.Join(allowed, ", "))
				}
				continue
			}
			deps["context."+name] = true
		case "viewer":
			if name != "tz" {
				c.fail(ptr, "{viewer.%s}: only {viewer.tz} is available to sources", name)
			}
		default:
			c.fail(ptr, "{%s.%s}: source arguments read only {param.*}, {context.*} and {viewer.tz}", scope, name)
		}
	}
}

// declaredParam is filled before sources are checked: params are parsed
// first, so the compiler's params map is final here.
func (c *compiler) declaredParam(name string) bool {
	return c.paramNames[name]
}

func presentationName(p string) string {
	if p == "" {
		return "screen page"
	}
	return p
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sourcesWire is the served form of the sources block: as written, plus the
// derived deps (param and context names without their scope).
func sourcesWire(c *Compiled) map[string]any {
	out := make(map[string]any, len(c.Sources))
	for id, s := range c.Sources {
		m := map[string]any{"from": s.From}
		for k, v := range s.Args {
			m[k] = v
		}
		if s.Lazy {
			m["lazy"] = true
		}
		deps := make([]string, 0, len(s.Deps))
		for _, d := range s.Deps {
			deps = append(deps, d[strings.Index(d, ".")+1:])
		}
		m["deps"] = deps
		out[id] = m
	}
	return out
}
