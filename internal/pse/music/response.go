package music

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
)

// APIVersion est la version de l'API Subsonic annoncée aux clients.
const APIVersion = "1.16.1"

// el est un élément de réponse Subsonic, sérialisable en XML ou en JSON.
// En JSON, les attributs deviennent des champs et les enfants de même nom
// sont regroupés ; list force un tableau même pour un seul enfant.
type el struct {
	name  string
	attrs []kv
	kids  []*el
	list  bool
	text  string // contenu texte (« value » en JSON)
}

type kv struct {
	k string
	v any
}

func node(name string, attrs ...any) *el {
	e := &el{name: name}
	for i := 0; i+1 < len(attrs); i += 2 {
		e.set(attrs[i].(string), attrs[i+1])
	}
	return e
}

// set ajoute un attribut, en ignorant nil et les chaînes vides.
func (e *el) set(k string, v any) *el {
	switch t := v.(type) {
	case nil:
		return e
	case string:
		if t == "" {
			return e
		}
	}
	e.attrs = append(e.attrs, kv{k, v})
	return e
}

// opt ajoute un attribut numérique seulement s'il est non nul.
func (e *el) opt(k string, v int64) *el {
	if v != 0 {
		e.attrs = append(e.attrs, kv{k, v})
	}
	return e
}

// value fixe le contenu texte de l'élément (genres).
func (e *el) value(v string) *el {
	e.text = v
	return e
}

func (e *el) add(kids ...*el) *el {
	e.kids = append(e.kids, kids...)
	return e
}

// item ajoute un enfant toujours sérialisé en tableau JSON.
func (e *el) item(kid *el) *el {
	kid.list = true
	e.kids = append(e.kids, kid)
	return e
}

func (e *el) jsonValue() map[string]any {
	m := make(map[string]any, len(e.attrs)+len(e.kids))
	for _, a := range e.attrs {
		m[a.k] = a.v
	}
	if e.text != "" {
		m["value"] = e.text
	}
	order := []string{}
	groups := map[string][]*el{}
	for _, k := range e.kids {
		if _, ok := groups[k.name]; !ok {
			order = append(order, k.name)
		}
		groups[k.name] = append(groups[k.name], k)
	}
	for _, name := range order {
		g := groups[name]
		if len(g) == 1 && !g[0].list {
			m[name] = g[0].jsonValue()
			continue
		}
		arr := make([]any, len(g))
		for i, k := range g {
			arr[i] = k.jsonValue()
		}
		m[name] = arr
	}
	return m
}

func (e *el) writeXML(enc *xml.Encoder) error {
	start := xml.StartElement{Name: xml.Name{Local: e.name}}
	for _, a := range e.attrs {
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: a.k}, Value: fmt.Sprint(a.v)})
	}
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	if e.text != "" {
		if err := enc.EncodeToken(xml.CharData(e.text)); err != nil {
			return err
		}
	}
	for _, k := range e.kids {
		if err := k.writeXML(enc); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

// Codes d'erreur Subsonic.
const (
	errGeneric       = 0
	errMissingParam  = 10
	errClientTooOld  = 20
	errWrongAuth     = 40
	errTokenNoSupp   = 41
	errNotAuthorized = 50
	errNotFound      = 70
)

var callbackRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$.]{0,63}$`)

func root(status string) *el {
	return node("subsonic-response", "status", status, "version", APIVersion,
		"type", "pse", "serverVersion", "pse", "openSubsonic", true)
}

// write envoie payload (peut être nil) au format demandé par le client.
func write(w http.ResponseWriter, r *http.Request, payload *el) {
	resp := root("ok")
	if payload != nil {
		resp.add(payload)
	}
	send(w, r, resp)
}

func fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	resp := root("failed").add(node("error", "code", code, "message", msg))
	send(w, r, resp)
}

func send(w http.ResponseWriter, r *http.Request, resp *el) {
	w.Header().Set("Cache-Control", "no-store")
	switch f := r.Form.Get("f"); f {
	case "json", "jsonp":
		body, err := json.Marshal(map[string]any{"subsonic-response": resp.jsonValue()})
		if err != nil {
			http.Error(w, "Internal Server Error", 500)
			return
		}
		if cb := r.Form.Get("callback"); f == "jsonp" && callbackRe.MatchString(cb) {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			_, _ = fmt.Fprintf(w, "%s(%s);", cb, body)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(body)
	default:
		var buf bytes.Buffer
		buf.WriteString(xml.Header)
		resp.attrs = append([]kv{{"xmlns", "http://subsonic.org/restapi"}}, resp.attrs...)
		enc := xml.NewEncoder(&buf)
		if err := resp.writeXML(enc); err != nil || enc.Flush() != nil {
			http.Error(w, "Internal Server Error", 500)
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	}
}
