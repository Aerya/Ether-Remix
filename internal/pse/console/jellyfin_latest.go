package console

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/braedonsaunders/ether/internal/pse/latestfilter"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// API de l'éditeur de règles des derniers ajouts Jellyfin
// (/ether/api/pse/jellyfin-latest) : le corps est {"movie": TabFilter,
// "show": TabFilter}, seul « exclude » de chaque filtre est utilisé ; la
// réponse ajoute « defaults », le modèle proposé par la console.

type latestView struct {
	latestfilter.Settings
	Defaults latestfilter.Settings `json:"defaults"`
}

func (h *handler) latestReply(w http.ResponseWriter, code int) {
	s, _ := latestSettings(pseenv.Get(latestfilter.Key))
	writeJSON(w, code, latestView{Settings: s, Defaults: latestfilter.Defaults()})
}

func latestSettings(raw string) (latestfilter.Settings, error) {
	var s latestfilter.Settings
	if raw == "" {
		return s, nil
	}
	return s, json.Unmarshal([]byte(raw), &s)
}

func (h *handler) getLatest(w http.ResponseWriter, r *http.Request) {
	h.latestReply(w, http.StatusOK)
}

func (h *handler) putLatest(w http.ResponseWriter, r *http.Request) {
	body, err := readAllLimited(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Requête invalide"})
		return
	}
	var in latestfilter.Settings
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Requête invalide"})
		return
	}
	raw, _ := json.Marshal(in)
	norm, err := latestfilter.Normalize(string(raw))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	values := pseenv.Snapshot()
	values[latestfilter.Key] = norm
	if err := writeAll(r.Context(), h.d.DB, values); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Enregistrement impossible"})
		return
	}
	pseenv.Replace(values)
	if h.d.Apply != nil {
		h.d.Apply(r.Context())
	}
	h.latestReply(w, http.StatusOK)
}

func readAllLimited(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(http.MaxBytesReader(w, r.Body, latestfilter.MaxSize))
	return buf.Bytes(), err
}
