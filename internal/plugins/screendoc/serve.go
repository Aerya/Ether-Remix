package screendoc

import (
	"bytes"
	"encoding/json"
	"strings"
)

// ServeMeta is what Ether adds to a document when it serves it.
type ServeMeta struct {
	Screen       string
	Page         string
	Version      string
	Title        string
	Presentation string
	// Actions are the plugin actions the page may call, with their confirm
	// texts already localised ("" for none).
	Actions map[string]string
	// AssetURL maps a package path to the URL Ether serves it at.
	AssetURL func(path string) string
	// Text localises a string key; ok=false falls back to the document's
	// own default.
	Text func(key string) (string, bool)
}

// Serve returns the client document: the compiled body, the localised
// strings, the sources with their deps, and the Ether-served fields.
func (c *Compiled) Serve(meta ServeMeta) ([]byte, error) {
	doc := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(c.body))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	doc["schema"] = 1
	doc["kind"] = "screen"
	doc["renderer"] = c.Renderer
	doc["screen"] = meta.Screen
	doc["page"] = meta.Page
	doc["version"] = meta.Version
	doc["presentation"] = meta.Presentation
	if _, ok := doc["title"]; !ok || meta.Title != "" {
		doc["title"] = meta.Title
	}
	if len(c.Params) > 0 {
		doc["params"] = c.Params
	}
	doc["sources"] = sourcesWire(c)
	if len(meta.Actions) > 0 {
		acts := make(map[string]any, len(meta.Actions))
		for id, confirm := range meta.Actions {
			if confirm != "" {
				acts[id] = map[string]string{"confirm": confirm}
			} else {
				acts[id] = map[string]string{}
			}
		}
		doc["actions"] = acts
	}
	if len(c.Strings) > 0 {
		strs := make(map[string]string, len(c.Strings))
		for k, v := range c.Strings {
			if meta.Text != nil {
				if t, ok := meta.Text(k); ok {
					v = t
				}
			}
			strs[k] = v
		}
		doc["strings"] = strs
	}
	rewriteAssets(doc, meta.AssetURL)
	return json.Marshal(doc)
}

func rewriteAssets(v any, url func(string) string) any {
	switch n := v.(type) {
	case map[string]any:
		for k, child := range n {
			n[k] = rewriteAssets(child, url)
		}
		return n
	case []any:
		for i, child := range n {
			n[i] = rewriteAssets(child, url)
		}
		return n
	case string:
		if p, ok := strings.CutPrefix(n, AssetPrefix); ok && url != nil {
			return url(p)
		}
		return n
	}
	return v
}
