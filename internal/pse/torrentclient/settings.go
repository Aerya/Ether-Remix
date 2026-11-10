package torrentclient

import (
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Settings regroupe les réglages PSE_TORRENT_*.
type Settings struct {
	Enabled  bool   // PSE_TORRENT : désactivé par défaut
	Client   string // PSE_TORRENT_CLIENT : qbittorrent ou rtorrent
	URL      string // PSE_TORRENT_URL : adresse du client, identifiants compris
	Download bool   // PSE_TORRENT_DOWNLOAD : envoyer les releases absentes
	Music    bool   // PSE_TORRENT_MUSIC : la musique aussi
	Category string // PSE_TORRENT_CATEGORY : catégorie ou libellé Remix
	SavePath string // PSE_TORRENT_SAVE_PATH : dossier côté client
	// PathMap : PSE_TORRENT_PATH_MAP, « dossier client=dossier Remix »
	// séparés par « ; », quand les deux conteneurs ne montent pas le
	// dossier au même endroit.
	PathMap   []Mapping
	Ratio     float64       // PSE_TORRENT_RATIO : 0 = réglage du client
	SeedTime  time.Duration // PSE_TORRENT_SEED_TIME : 0 = réglage du client
	MaxPerDay int           // PSE_TORRENT_MAX_DAY : ajouts par jour
	XSeedURL  string        // PSE_TORRENT_XSEED_URL : cross-seed, en option
	XSeedKey  string        // PSE_TORRENT_XSEED_KEY
}

// Mapping relie un dossier vu par le client à celui vu par Remix.
type Mapping struct{ Client, Local string }

const (
	defaultCategory  = "remix"
	defaultMaxPerDay = 10
)

// LoadSettings lit les réglages effectifs (console, sinon environnement).
func LoadSettings() Settings {
	s := Settings{
		Enabled:   on(pseenv.Get("PSE_TORRENT")),
		Client:    strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_TORRENT_CLIENT"))),
		URL:       strings.TrimSpace(pseenv.Get("PSE_TORRENT_URL")),
		Download:  onDefault(pseenv.Get("PSE_TORRENT_DOWNLOAD"), true),
		Music:     onDefault(pseenv.Get("PSE_TORRENT_MUSIC"), true),
		Category:  strings.TrimSpace(pseenv.Get("PSE_TORRENT_CATEGORY")),
		SavePath:  strings.TrimSpace(pseenv.Get("PSE_TORRENT_SAVE_PATH")),
		XSeedURL:  strings.TrimSpace(pseenv.Get("PSE_TORRENT_XSEED_URL")),
		XSeedKey:  strings.TrimSpace(pseenv.Get("PSE_TORRENT_XSEED_KEY")),
		MaxPerDay: defaultMaxPerDay,
	}
	if s.Client != "rtorrent" {
		s.Client = "qbittorrent"
	}
	if s.Category == "" {
		s.Category = defaultCategory
	}
	s.PathMap, _ = ParsePathMap(pseenv.Get("PSE_TORRENT_PATH_MAP"))
	if r, err := ParseRatio(pseenv.Get("PSE_TORRENT_RATIO")); err == nil {
		s.Ratio = r
	}
	if d, err := time.ParseDuration(strings.TrimSpace(pseenv.Get("PSE_TORRENT_SEED_TIME"))); err == nil && d > 0 {
		s.SeedTime = d
	}
	if n, err := strconv.Atoi(strings.TrimSpace(pseenv.Get("PSE_TORRENT_MAX_DAY"))); err == nil && n > 0 {
		s.MaxPerDay = min(n, 200)
	}
	if s.URL == "" {
		s.Enabled = false
	}
	return s
}

func on(v string) bool { return onDefault(v, false) }

func onDefault(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "1", "true", "yes", "oui":
		return true
	case "off", "0", "false", "no", "non":
		return false
	}
	return def
}

// ParseRatio lit une limite de ratio (« 1 », « 1.5 », « 1,5 ») ; 0 ou vide : aucune.
func ParseRatio(v string) (float64, error) {
	v = strings.ReplaceAll(strings.TrimSpace(v), ",", ".")
	if v == "" {
		return 0, nil
	}
	r, err := strconv.ParseFloat(v, 64)
	if err != nil || r < 0 || r > 100 {
		return 0, errors.New("ratio entre 0 et 100 attendu, ex. 1 ou 1.5")
	}
	return r, nil
}

// ParsePathMap lit « /downloads=/data/torrents; /films=/media/films ».
func ParsePathMap(v string) ([]Mapping, error) {
	var out []Mapping
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		c, l, ok := strings.Cut(part, "=")
		c, l = strings.TrimSpace(c), strings.TrimSpace(l)
		if !ok || !strings.HasPrefix(c, "/") || !strings.HasPrefix(l, "/") {
			return nil, errors.New("format attendu : /dossier/client=/dossier/remix ; …")
		}
		out = append(out, Mapping{Client: path.Clean(c), Local: path.Clean(l)})
	}
	return out, nil
}

// Local traduit un chemin du client en chemin vu par Remix. ok est faux
// quand des correspondances sont réglées et qu'aucune ne s'applique : Remix
// ne lit alors rien hors des dossiers indiqués.
func (s Settings) Local(clientPath string) (string, bool) {
	p := path.Clean(clientPath)
	if !path.IsAbs(p) {
		return "", false
	}
	if len(s.PathMap) == 0 {
		return p, true
	}
	best := -1
	for i, m := range s.PathMap {
		if (p == m.Client || strings.HasPrefix(p, strings.TrimRight(m.Client, "/")+"/")) && (best < 0 || len(m.Client) > len(s.PathMap[best].Client)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	m := s.PathMap[best]
	return path.Join(m.Local, strings.TrimPrefix(p, m.Client)), true
}

// NewClient construit le client réglé.
func (s Settings) NewClient(hc *http.Client) (Client, error) {
	if s.Client == "rtorrent" {
		return NewRTorrent(s.URL, hc)
	}
	return NewQBittorrent(s.URL, hc)
}

// Label est le nom affiché du client.
func (s Settings) Label() string {
	if s.Client == "rtorrent" {
		return "rTorrent / ruTorrent"
	}
	return "qBittorrent"
}
