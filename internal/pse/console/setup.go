package console

// The guided setup and future clients share this bounded planning API. It
// changes existing settings, never maintains a second effective configuration.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/quality"
)

// Deliberately no credentials, manifests, network configuration, provider
// activation or download permissions. Those use their existing editors.
var setupKeys = []string{
	"PSE_FRENCH", "PSE_FRENCH_RELEASES", "PSE_PLAYABLE", "PSE_PLAYABLE_MIN",
	"PSE_PLAYABLE_SINGLE", "PSE_PLAYABLE_PENDING", "PSE_INDEXER", "PSE_PREDB",
	"PSE_LIBRARY_SORT", "PSE_POSTER_REFRESH_HOURS",
}

type setupRequest struct {
	Revision string             `json:"revision"`
	Values   map[string]*string `json:"values"`
	Plan     string             `json:"plan,omitempty"`
}
type setupChange struct {
	Key    string `json:"key"`
	Label  Text   `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
	Reset  bool   `json:"reset"`
}
type setupWarning struct {
	ID      string `json:"id"`
	Message Text   `json:"message"`
	Href    string `json:"href"`
}
type setupPlan struct {
	Revision string             `json:"revision"`
	Plan     string             `json:"plan"`
	Values   map[string]*string `json:"values"`
	Changes  []setupChange      `json:"changes"`
	Warnings []setupWarning     `json:"warnings"`
}

func setupLabel(key string) Text {
	for _, feature := range Features {
		for _, field := range feature.Fields {
			if field.Key == key {
				return tx(feature.Title.FR+" — "+field.Label.FR, feature.Title.EN+" — "+field.Label.EN)
			}
		}
	}
	return fieldByKey[key].Label
}

func setupEffective(key string, overrides map[string]string) string {
	fd := fieldByKey[key]
	v, ok := overrides[key]
	if !ok {
		v, ok = pseenv.Env(key)
	}
	if !ok {
		v = fd.Default
	}
	// Environment aliases accepted by the runtime must look enabled in the
	// wizard too, without manufacturing an override for an unchanged value.
	if fd.Kind == KindToggle {
		if normalized, err := validate(fd, strings.TrimSpace(v), nil); err == nil {
			return normalized
		}
	}
	return v
}

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (h *handler) setupRevision(values map[string]string) string {
	// Include all effective settings and profile rules so changes in another
	// editor invalidate a preview. Only the digest leaves the server.
	effective := map[string]string{}
	for k := range fieldByKey {
		effective[k] = setupEffective(k, values)
	}
	return digest([]any{values, effective, quality.Signature(h.d.DB)})
}

func (h *handler) setupWarnings(values map[string]string) []setupWarning {
	on := func(k string) bool { return setupEffective(k, values) == "on" }
	out := []setupWarning{}
	add := func(id, fr, en, href string) { out = append(out, setupWarning{id, tx(fr, en), href}) }
	if on("PSE_FRENCH") && on("PSE_PLAYABLE") {
		add("catalogue-intersection", "Le catalogue français et la disponibilité se cumulent. Une sortie en France ne prouve pas qu’une source possède une VF.", "French catalogue and availability filters both apply. A release in France does not prove that a source has French audio.", "/remix/lecture?view=qualite")
	}
	if quality.Active(h.d.DB) {
		add("profiles", "Les profils remplacent les critères simples, mais le minimum de releases reste actif. Les règles audio du lecteur s’ajoutent encore.", "Profiles replace simple criteria, but the minimum release count remains active. Player audio rules still apply.", "/remix/lecture?view=qualite")
	}
	if on("PSE_PREDB") {
		add("announcements", "Les pre comptent actuellement dans les releases indexées sans fournir de lien. « Lisible uniquement » ne garantit donc pas la lecture d’une annonce.", "Pre announcements currently count as indexed releases without providing a link. Playable-only does not guarantee an announcement can play.", "/remix/indexation?view=pre")
	}
	if on("PSE_PLAYABLE") && !on("PSE_INDEXER") {
		add("indexing", "La recherche de fond est désactivée : les imports et recherches à la lecture peuvent remplir l’index, mais le catalogue peut rester réduit.", "Background search is disabled: imports and playback searches can populate the index, but the catalogue may stay small.", "/remix/indexation")
	}
	if on("PSE_SUBMAKER") {
		add("subtitles", "SubMaker ne contourne pas un profil exigeant l’audio français. Vérifiez aussi le périmètre des collections et bibliothèques ; une traduction en attente n’est pas disponible.", "SubMaker does not bypass a profile requiring French audio. Check collection and library scopes too; a pending translation is not available.", "/remix/lecture?view=soustitres")
	}
	if on("PSE_SEERRCATALOG") {
		add("seerr", "Les disponibilités SeerrCatalog suivent ses critères ; les règles de lecture Remix restent actives. Le partage et la recherche utilisent deux connexions distinctes.", "SeerrCatalog availability follows its criteria; Remix playback rules still apply. Sharing and search use separate connections.", "/remix/sources?source=stremio")
	}
	return out
}

func (h *handler) setupState(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	values := pseenv.Snapshot()
	fields := []fieldView{}
	for _, k := range setupKeys {
		fd := fieldByKey[k]
		fd.Label = setupLabel(k)
		fields = append(fields, fieldView{Field: fd, Value: setupEffective(k, values), Source: pseenv.Source(k), Options: fd.Options})
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": h.setupRevision(values), "fields": fields, "warnings": h.setupWarnings(values)})
}

func decodeSetup(w http.ResponseWriter, r *http.Request) (setupRequest, bool) {
	var body setupRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&body); err != nil || d.Decode(new(any)) != io.EOF || len(body.Values) > len(setupKeys) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Requête de configuration invalide"})
		return body, false
	}
	return body, true
}

// planSetup is pure with respect to persisted settings and never probes sources.
func (h *handler) planSetup(body setupRequest, current map[string]string) (setupPlan, map[string]string, error) {
	p := setupPlan{Revision: h.setupRevision(current), Values: map[string]*string{}, Changes: []setupChange{}}
	next := map[string]string{}
	for k, v := range current {
		next[k] = v
	}
	allowed := map[string]bool{}
	for _, k := range setupKeys {
		allowed[k] = true
	}
	keys := []string{}
	for k := range body.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowed[k] {
			return p, nil, fmt.Errorf("réglage non autorisé dans l’assistant")
		}
		value := body.Values[k]
		if value == nil {
			delete(next, k)
			p.Values[k] = nil
		} else {
			v, err := validate(fieldByKey[k], strings.TrimSpace(*value), nil)
			if err != nil {
				return p, nil, fmt.Errorf("valeur de réglage invalide")
			}
			next[k] = v
			p.Values[k] = &v
		}
		before, after := setupEffective(k, current), setupEffective(k, next)
		_, hadOverride := current[k]
		// Keeping an effective value must not silently pin an environment value.
		if value != nil && before == after {
			if hadOverride {
				next[k] = current[k]
			} else {
				delete(next, k)
			}
			delete(p.Values, k)
			continue
		}
		if before != after || (value == nil && hadOverride) {
			p.Changes = append(p.Changes, setupChange{k, setupLabel(k), before, after, value == nil})
		}
	}
	p.Warnings = h.setupWarnings(next)
	p.Plan = digest([]any{p.Revision, p.Values, p.Warnings})
	return p, next, nil
}

func (h *handler) setupPreview(w http.ResponseWriter, r *http.Request) { h.setupExecute(w, r, false) }
func (h *handler) setupApply(w http.ResponseWriter, r *http.Request)   { h.setupExecute(w, r, true) }
func (h *handler) setupExecute(w http.ResponseWriter, r *http.Request, apply bool) {
	body, ok := decodeSetup(w, r)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	current := pseenv.Snapshot()
	p, next, err := h.planSetup(body, current)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if body.Revision != p.Revision {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "La configuration a changé. Actualisez l’état et examinez un nouvel aperçu."})
		return
	}
	if !apply {
		writeJSON(w, http.StatusOK, p)
		return
	}
	if body.Plan == "" || body.Plan != p.Plan {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Examinez un nouvel aperçu avant d’appliquer."})
		return
	}
	if len(p.Changes) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"revision": p.Revision, "undo": map[string]*string{}, "changes": p.Changes})
		return
	}
	undo := map[string]*string{}
	for k := range p.Values {
		if v, exists := current[k]; exists {
			undo[k] = &v
		} else {
			undo[k] = nil
		}
	}
	if err := writeAll(r.Context(), h.d.DB, next); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Enregistrement impossible"})
		return
	}
	pseenv.Replace(next)
	if h.d.Apply != nil {
		h.d.Apply(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": h.setupRevision(next), "undo": undo, "changes": p.Changes})
}
