package discovery

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/braedonsaunders/ether/internal/safefetch"
)

// Imported manifests are untrusted: no cookies, credentials or LAN exception,
// bounded bodies, and DNS/redirect validation through the shared guard.
var packClient = (&safefetch.Guard{}).Client(&http.Client{Timeout: 25 * time.Second})

func (s *Service) importPack(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 10<<10)).Decode(&input) != nil {
		http.Error(w, "URL de pack invalide", 400)
		return
	}
	u, err := url.Parse(input.URL)
	if err != nil || len(input.URL) > 8192 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		http.Error(w, "Utilisez une URL HTTPS directe vers un fichier JSON public", 400)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		http.Error(w, "URL de pack invalide", 400)
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Ether-Remix artwork packs")
	res, err := packClient.Do(req)
	if err != nil {
		http.Error(w, "Pack inaccessible : utilisez un export JSON local", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		http.Error(w, "Pack inaccessible : utilisez un export JSON local", 502)
		return
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 || !json.Valid(data) {
		http.Error(w, "Pack JSON invalide ou supérieur à 4 Mo", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
