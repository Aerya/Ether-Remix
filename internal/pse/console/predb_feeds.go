package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// feedEntry est un flux personnel saisi dans la console (réglage PSE_PREDB_FEEDS).
type feedEntry struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
	Key  string `json:"key,omitempty"`
}

const maxFeedEntries = 20

func parseFeedEntries(v string) ([]feedEntry, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	var list []feedEntry
	if err := json.Unmarshal([]byte(v), &list); err != nil {
		return nil, errors.New("liste de sites invalide")
	}
	return list, nil
}

func marshalFeedEntries(list []feedEntry) string {
	if len(list) == 0 {
		return ""
	}
	b, _ := json.Marshal(list)
	return string(b)
}

// validateFeeds contrôle la liste : 20 sites au plus, adresses http(s) sans identifiants,
// pas de doublon, clés sur une ligne. Un site est refusé avec un message qui dit lequel.
func validateFeeds(v string) (string, error) {
	list, err := parseFeedEntries(v)
	if err != nil {
		return "", err
	}
	if len(list) > maxFeedEntries {
		return "", fmt.Errorf("%d sites au plus", maxFeedEntries)
	}
	seen := map[string]bool{}
	out := make([]feedEntry, 0, len(list))
	for i, f := range list {
		n := i + 1
		f.Name, f.URL, f.Key = strings.TrimSpace(f.Name), strings.TrimSpace(f.URL), strings.TrimSpace(f.Key)
		if f.URL == "" {
			return "", fmt.Errorf("site %d : adresse manquante", n)
		}
		u, err := url.Parse(f.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return "", fmt.Errorf("site %d : adresse http(s):// sans identifiants attendue", n)
		}
		if seen[u.String()] {
			return "", fmt.Errorf("site %d : adresse déjà ajoutée", n)
		}
		seen[u.String()] = true
		f.URL = u.String()
		if len(f.Name) > 60 || strings.IndexFunc(f.Name, unicode.IsControl) >= 0 {
			return "", fmt.Errorf("site %d : nom de 60 caractères au plus, sur une ligne", n)
		}
		if strings.Contains(f.Key, mask) {
			return "", fmt.Errorf("site %d : ressaisissez la clé complète", n)
		}
		if len(f.Key) > 256 || strings.IndexFunc(f.Key, unicode.IsControl) >= 0 {
			return "", fmt.Errorf("site %d : clé de 256 caractères au plus, sur une ligne", n)
		}
		out = append(out, f)
	}
	return marshalFeedEntries(out), nil
}

// displayFeeds masque les clés avant d'envoyer la liste à la page.
func displayFeeds(v string) string {
	list, err := parseFeedEntries(v)
	if err != nil {
		return ""
	}
	for i := range list {
		if list[i].Key != "" {
			list[i].Key = mask
		}
	}
	return marshalFeedEntries(list)
}

// feedSite renvoie « schéma://hôte:port » d'une adresse, ou une chaîne vide.
func feedSite(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// restoreFeeds remet la clé enregistrée d'un site quand la page renvoie le masque : la clé
// revient seulement pour la même adresse.
func restoreFeeds(v, prev string) string {
	list, err := parseFeedEntries(v)
	if err != nil {
		return v
	}
	old, _ := parseFeedEntries(prev)
	byURL, bySite := map[string]string{}, map[string]string{}
	for _, f := range old {
		byURL[f.URL] = f.Key
		if s := feedSite(f.URL); s != "" && f.Key != "" && bySite[s] == "" {
			bySite[s] = f.Key
		}
	}
	for i := range list {
		if list[i].Key != mask {
			continue
		}
		// Une adresse inconnue garde le masque : la validation demande de ressaisir la clé.
		u := strings.TrimSpace(list[i].URL)
		if k := byURL[u]; k != "" {
			list[i].Key = k
		} else if k := bySite[feedSite(u)]; k != "" {
			// Même site (schéma et hôte), autre chemin : la clé n'est envoyée qu'à ce
			// site, on peut la garder quand on corrige seulement l'adresse.
			list[i].Key = k
		}
	}
	return marshalFeedEntries(list)
}

// Réglages que le test des flux peut lire dans le corps de la requête (valeurs non
// encore enregistrées) ; les autres viennent des réglages enregistrés.
var preTestKeys = []string{"PSE_PREDB_PUBLIC", "PSE_PREDB_LANGS", "PSE_PREDB_FEEDS", "PSE_PREDB_PROXY_TYPE", "PSE_PREDB_PROXY_URL", "PSE_PREDB_PROXY_AUTH"}

func (h *handler) testPre(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Values map[string]string `json:"values"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body)
	values := map[string]string{}
	for _, key := range preTestKeys {
		fd := fieldByKey[key]
		v, given := body.Values[key]
		prev, hadPrev := pseenv.Lookup(key)
		if !given {
			values[key] = prev
			continue
		}
		if fd.Kind != KindSecret {
			v = strings.TrimSpace(v)
		}
		if hadPrev {
			v = restore(fd, v, prev)
		}
		norm, err := validate(fd, v, nil)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Réglage invalide (" + fd.Label.FR + ") : " + err.Error()})
			return
		}
		values[key] = norm
	}
	// Le test lit toujours les flux publics et personnels des réglages en cours, même si la
	// fonction n'est pas encore activée.
	values["PSE_PREDB"] = "on"
	if h.d.TestPre == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Test indisponible"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	result := h.d.TestPre(ctx, func(key string) string { return values[key] })
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}
