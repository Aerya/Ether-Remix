package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/quality"
)

// Owner-only import: never return stored manifests, account data or upstream
// errors containing credentials. Passwords are used for this request only.
func (a *App) pseQualityImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fail := func(code int, msg string) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	if r.Method == http.MethodGet {
		out := []map[string]string{}
		for _, s := range a.cfg.Read().Indexers {
			if s.Kind == config.IndexerKindStremio {
				out = append(out, map[string]string{"id": s.ID, "name": s.Name})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	if r.Method != http.MethodPost {
		fail(405, "Méthode non autorisée")
		return
	}
	var in struct {
		SourceID string `json:"sourceId"`
		Manifest string `json:"manifest"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		fail(400, "Requête invalide")
		return
	}
	if in.SourceID != "" {
		in.Manifest = ""
		for _, s := range a.cfg.Read().Indexers {
			if s.ID == in.SourceID && s.Kind == config.IndexerKindStremio {
				in.Manifest = s.URL
				break
			}
		}
	}
	endpoint, uuid, variant, ok := aioConfigEndpoint(in.Manifest)
	if !ok {
		fail(400, "Manifest AIOStreams HTTPS attendu : /stremio/identifiant/accès/manifest.json")
		return
	}
	if in.Password == "" {
		fail(400, "Le mot de passe de configuration AIOStreams est nécessaire ; le manifest seul ne permet pas de déchiffrer les filtres.")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		fail(400, "URL invalide")
		return
	}
	req.SetBasicAuth(uuid, in.Password)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		fail(502, "AIOStreams injoignable ou délai dépassé")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		fail(502, "AIOStreams refuse l’accès à la configuration : vérifiez le mot de passe et la compatibilité de l’instance")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		fail(502, "Réponse AIOStreams invalide ou trop volumineuse")
		return
	}
	var envelope struct {
		Data struct {
			UserData json.RawMessage `json:"userData"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Data.UserData) == 0 {
		fail(502, "Configuration absente de la réponse AIOStreams")
		return
	}
	result, err := quality.ImportAIO(envelope.Data.UserData)
	if err != nil {
		fail(502, "Configuration AIOStreams non reconnue")
		return
	}
	if variant {
		result.Warnings = append([]string{"ATTENTION : manifest avec variante. Cet aperçu contient les filtres de BASE uniquement ; les instructions de variante ne sont pas exécutées. Utilisez un export de la configuration effective pour reproduire la variante."}, result.Warnings...)
	}
	_ = json.NewEncoder(w).Encode(result)
}
func aioConfigEndpoint(raw string) (endpoint, uuid string, variant, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "stremio" && len(parts) >= i+4 && parts[len(parts)-1] == "manifest.json" {
			uuid = parts[i+1]
			if uuid == "" || parts[i+2] == "" {
				return
			}
			variant = len(parts) > i+4 || u.RawQuery != ""
			u.Path = "/" + strings.Join(append(parts[:i], "api", "v1", "user"), "/")
			u.RawPath = ""
			u.RawQuery = "raw=true"
			return u.String(), uuid, variant, true
		}
	}
	return
}
