// Package xdcc ajoute à Ether-Remix une source XDCC adossée à XDCC Grabscher
// (fork de CaptainEpix, branche mono-2026), en option et désactivée par
// défaut : le téléchargement écrit des médias sur le disque.
//
// Grabscher reste un service externe. Remix l'interroge par son API REST
// (/api/1.0, clé dans l'en-tête Authorization) pour la recherche et l'état
// des bots, et par son API de compatibilité SABnzbd (clé dans le corps POST)
// pour les tâches de téléchargement. Les NZB de Grabscher ne sont qu'une
// enveloppe : Remix ne les passe jamais à son chemin Usenet.
package xdcc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Modes de lecture (PSE_XDCC_MODE).
const (
	// ModeDownload : lecture une fois le fichier complet, fichier gardé.
	ModeDownload = "download"
	// ModeStream : lecture progressive pendant le transfert, fichier gardé
	// en cache temporaire ; repli sur le fichier complet.
	ModeStream = "stream"
	// ModeBoth : lecture progressive et fichier gardé dans la bibliothèque.
	ModeBoth = "both"
)

// Catégories SABnzbd utilisées chez Grabscher. Avec
// XG_CATEGORY_FOLDERS=remix,remix-cache, chacune a son dossier.
const (
	CategoryKeep  = "remix"
	CategoryCache = "remix-cache"
)

// Settings regroupe les réglages PSE_XDCC_*.
type Settings struct {
	Enabled bool   // PSE_XDCC : désactivé par défaut
	URL     string // PSE_XDCC_URL : adresse de Grabscher, ex. http://xdcc-grabscher:5556
	Key     string // PSE_XDCC_KEY : clé d'API de Grabscher (jamais affichée)
	Mode    string // PSE_XDCC_MODE

	// PathMap : PSE_XDCC_PATH_MAP, « dossier Grabscher=dossier Remix »
	// séparés par « ; » (ex. /config/.config/XG=/xdcc).
	PathMap []Mapping
	// TempDir : PSE_XDCC_TEMP_DIR, dossier temporaire de Grabscher tel que
	// Remix le voit. Vide : déduit du dossier de téléchargement de Grabscher.
	TempDir string

	MaxActive  int           // PSE_XDCC_MAX_ACTIVE : téléchargements simultanés
	MaxPerDay  int           // PSE_XDCC_MAX_DAY : lancements par jour
	CacheBytes int64         // PSE_XDCC_CACHE_GB : taille maximale du cache
	MinFree    int64         // PSE_XDCC_MIN_FREE : place libre minimale (Go)
	Retention  time.Duration // PSE_XDCC_CACHE_HOURS : conservation du cache
	Buffer     int64         // PSE_XDCC_BUFFER_MB : tampon avant lecture
	Wait       time.Duration // PSE_XDCC_WAIT : délai maximal face à un bot lent
	MaxResults int           // PSE_XDCC_MAX_RESULTS : résultats par recherche
	Daily      int           // PSE_XDCC_SEARCH_DAILY : recherches par jour
	// ExcludeBots : PSE_XDCC_BOTS_EXCLUDE, bots ignorés (noms séparés par
	// des virgules, sans tenir compte de la casse).
	ExcludeBots []string
}

// Mapping relie un dossier vu par Grabscher à celui vu par Remix.
type Mapping struct{ Remote, Local string }

const (
	defaultMaxActive = 2
	defaultMaxPerDay = 20
	defaultCacheGB   = 50
	defaultMinFreeGB = 20
	defaultRetention = 48 * time.Hour
	defaultBufferMB  = 8
	defaultWait      = 10 * time.Second
	defaultResults   = 50
	defaultDaily     = 3000
)

// LoadSettings lit les réglages effectifs (console, sinon environnement).
func LoadSettings() Settings {
	s := Settings{
		Enabled:    on(pseenv.Get("PSE_XDCC")),
		URL:        strings.TrimRight(strings.TrimSpace(pseenv.Get("PSE_XDCC_URL")), "/"),
		Key:        strings.TrimSpace(pseenv.Get("PSE_XDCC_KEY")),
		Mode:       NormalizeMode(pseenv.Get("PSE_XDCC_MODE")),
		TempDir:    cleanAbs(pseenv.Get("PSE_XDCC_TEMP_DIR")),
		MaxActive:  intIn(pseenv.Get("PSE_XDCC_MAX_ACTIVE"), defaultMaxActive, 1, 20),
		MaxPerDay:  intIn(pseenv.Get("PSE_XDCC_MAX_DAY"), defaultMaxPerDay, 1, 500),
		CacheBytes: int64(intIn(pseenv.Get("PSE_XDCC_CACHE_GB"), defaultCacheGB, 0, 100000)) << 30,
		MinFree:    int64(intIn(pseenv.Get("PSE_XDCC_MIN_FREE"), defaultMinFreeGB, 0, 100000)) << 30,
		Retention:  time.Duration(intIn(pseenv.Get("PSE_XDCC_CACHE_HOURS"), int(defaultRetention/time.Hour), 1, 24*365)) * time.Hour,
		Buffer:     int64(intIn(pseenv.Get("PSE_XDCC_BUFFER_MB"), defaultBufferMB, 1, 512)) << 20,
		Wait:       durIn(pseenv.Get("PSE_XDCC_WAIT"), defaultWait, 2*time.Second, 12*time.Second),
		MaxResults: intIn(pseenv.Get("PSE_XDCC_MAX_RESULTS"), defaultResults, 5, 200),
		Daily:      intIn(pseenv.Get("PSE_XDCC_SEARCH_DAILY"), defaultDaily, 50, 100000),
	}
	s.PathMap, _ = ParsePathMap(pseenv.Get("PSE_XDCC_PATH_MAP"))
	for _, b := range strings.Split(pseenv.Get("PSE_XDCC_BOTS_EXCLUDE"), ",") {
		if b = strings.ToLower(strings.TrimSpace(b)); b != "" {
			s.ExcludeBots = append(s.ExcludeBots, b)
		}
	}
	if s.URL == "" || s.Key == "" || ValidateURL(s.URL) != nil {
		s.Enabled = false
	}
	return s
}

// NormalizeMode renvoie un mode connu (stream par défaut).
func NormalizeMode(v string) string {
	switch m := strings.ToLower(strings.TrimSpace(v)); m {
	case ModeDownload, ModeBoth, ModeStream:
		return m
	}
	return ModeStream
}

// Progressive indique si la lecture commence avant la fin du transfert.
func (s Settings) Progressive() bool { return s.Mode != ModeDownload }

// KeepOnPlay indique si un fichier lancé par une lecture est gardé.
func (s Settings) KeepOnPlay() bool { return s.Mode != ModeStream }

// Identity résume ce qui change les résultats de recherche ; la clé n'y
// entre que hachée (empreinte tronquée), jamais en clair :
// elle lie le cache des recherches d'Ether à la configuration courante.
func (s Settings) Identity() string {
	h := sha256.Sum256([]byte(s.URL + "\x00" + s.Key + "\x00" + strings.Join(s.ExcludeBots, ",") + "\x00" + strconv.Itoa(s.MaxResults)))
	return hex.EncodeToString(h[:8])
}

// Excluded indique si un bot est ignoré.
func (s Settings) Excluded(bot string) bool {
	b := strings.ToLower(strings.TrimSpace(bot))
	for _, x := range s.ExcludeBots {
		if x == b {
			return true
		}
	}
	return false
}

// Local traduit un chemin vu par Grabscher en chemin vu par Remix. Sans
// correspondance, le chemin est identique (même montage dans les deux
// conteneurs) ; avec des correspondances, un chemin hors de toutes est
// refusé.
func (s Settings) Local(remote string) (string, bool) {
	p := path.Clean(remote)
	if !path.IsAbs(p) {
		return "", false
	}
	if len(s.PathMap) == 0 {
		return p, true
	}
	best := -1
	for i, m := range s.PathMap {
		if (p == m.Remote || strings.HasPrefix(p, strings.TrimRight(m.Remote, "/")+"/")) && (best < 0 || len(m.Remote) > len(s.PathMap[best].Remote)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	m := s.PathMap[best]
	return path.Join(m.Local, strings.TrimPrefix(p, m.Remote)), true
}

// ParsePathMap lit « /dossier/grabscher=/dossier/remix ; … ».
func ParsePathMap(v string) ([]Mapping, error) {
	var out []Mapping
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		r, l, ok := strings.Cut(part, "=")
		r, l = strings.TrimSpace(r), strings.TrimSpace(l)
		if !ok || !strings.HasPrefix(r, "/") || !strings.HasPrefix(l, "/") {
			return nil, errors.New("format attendu : /dossier/grabscher=/dossier/remix ; …")
		}
		out = append(out, Mapping{Remote: path.Clean(r), Local: path.Clean(l)})
	}
	return out, nil
}

// ValidateURL vérifie l'adresse de Grabscher : http(s), sans identifiants,
// sans requête. La clé passe dans l'en-tête ou le corps, jamais dans l'URL.
func ValidateURL(v string) error {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("adresse http(s) attendue, ex. http://xdcc-grabscher:5556")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("adresse sans identifiants ni paramètres : la clé se règle à part")
	}
	return nil
}

func on(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "1", "true", "yes", "oui":
		return true
	}
	return false
}

func intIn(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}

func durIn(v string, def, lo, hi time.Duration) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		if n, err2 := strconv.Atoi(v); err2 == nil {
			d = time.Duration(n) * time.Second
		} else {
			return def
		}
	}
	return min(max(d, lo), hi)
}

func cleanAbs(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || !strings.HasPrefix(v, "/") {
		return ""
	}
	return path.Clean(v)
}
