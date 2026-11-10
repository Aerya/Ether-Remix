package torrentclient

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// MaxClients : le client principal et deux clients supplémentaires.
const MaxClients = 3

// Settings regroupe les réglages PSE_TORRENT_*.
type Settings struct {
	Enabled   bool   // PSE_TORRENT : désactivé par défaut
	Download  bool   // PSE_TORRENT_DOWNLOAD : envoyer les releases absentes
	Music     bool   // PSE_TORRENT_MUSIC : la musique aussi
	Category  string // PSE_TORRENT_CATEGORY : catégorie ou libellé Remix
	MaxPerDay int    // PSE_TORRENT_MAX_DAY : ajouts par jour, tous clients
	// MinFree : PSE_TORRENT_MIN_FREE, en Go ; sous ce seuil, Remix n'envoie
	// plus rien au client et prévient le propriétaire. 0 : aucun contrôle.
	MinFree  int64
	XSeedURL string // PSE_TORRENT_XSEED_URL : cross-seed, en option
	XSeedKey string // PSE_TORRENT_XSEED_KEY
	// Trackers : PSE_TORRENT_TRACKERS, limites propres à certains trackers.
	Trackers []TrackerRule
	// Clients actifs, dans l'ordre de préférence pour les ajouts.
	Clients []ClientConf
}

// ClientConf décrit un client : le principal (PSE_TORRENT_CLIENT…) ou un
// client supplémentaire n (PSE_TORRENT2_…, PSE_TORRENT3_…).
type ClientConf struct {
	N        int    // 1, 2 ou 3
	Kind     string // qbittorrent ou rtorrent
	URL      string // adresse, identifiants compris
	Key      string // clé d'API (qBittorrent 5.2 et suivants)
	SavePath string // dossier côté client
	// PathMap : « dossier client=dossier Remix » séparés par « ; », quand
	// Remix ne voit pas les fichiers au même chemin que le client.
	PathMap  []Mapping
	Ratio    float64       // 0 : réglage du client
	SeedTime time.Duration // 0 : réglage du client
}

// Mapping relie un dossier vu par le client à celui vu par Remix.
type Mapping struct{ Client, Local string }

// TrackerRule fixe les limites des torrents d'un tracker. Match est une
// partie du nom d'hôte de l'annonce (ex. « c411 » ou « tracker.exemple.org »).
type TrackerRule struct {
	Match    string
	Ratio    float64       // 0 : limite du client
	SeedTime time.Duration // 0 : limite du client
}

const (
	defaultCategory  = "remix"
	defaultMaxPerDay = 10
	defaultMinFreeGB = 20
)

// prefix renvoie le préfixe des variables du client n.
func prefix(n int) string {
	if n <= 1 {
		return "PSE_TORRENT_"
	}
	return "PSE_TORRENT" + strconv.Itoa(n) + "_"
}

// LoadSettings lit les réglages effectifs (console, sinon environnement).
func LoadSettings() Settings {
	s := Settings{
		Enabled:   on(pseenv.Get("PSE_TORRENT")),
		Download:  onDefault(pseenv.Get("PSE_TORRENT_DOWNLOAD"), true),
		Music:     onDefault(pseenv.Get("PSE_TORRENT_MUSIC"), true),
		Category:  strings.TrimSpace(pseenv.Get("PSE_TORRENT_CATEGORY")),
		XSeedURL:  strings.TrimSpace(pseenv.Get("PSE_TORRENT_XSEED_URL")),
		XSeedKey:  strings.TrimSpace(pseenv.Get("PSE_TORRENT_XSEED_KEY")),
		MaxPerDay: defaultMaxPerDay,
		MinFree:   defaultMinFreeGB << 30,
	}
	if s.Category == "" {
		s.Category = defaultCategory
	}
	if n, err := strconv.Atoi(strings.TrimSpace(pseenv.Get("PSE_TORRENT_MAX_DAY"))); err == nil && n > 0 {
		s.MaxPerDay = min(n, 200)
	}
	if v := strings.TrimSpace(pseenv.Get("PSE_TORRENT_MIN_FREE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			s.MinFree = int64(min(n, 100000)) << 30
		}
	}
	s.Trackers, _ = ParseTrackerRules(pseenv.Get("PSE_TORRENT_TRACKERS"))
	for n := 1; n <= MaxClients; n++ {
		if n > 1 && !on(pseenv.Get("PSE_TORRENT"+strconv.Itoa(n))) {
			continue
		}
		if c, ok := loadClient(n); ok {
			s.Clients = append(s.Clients, c)
		}
	}
	if len(s.Clients) == 0 {
		s.Enabled = false
	}
	return s
}

func loadClient(n int) (ClientConf, bool) {
	p := prefix(n)
	c := ClientConf{N: n,
		Kind:     strings.ToLower(strings.TrimSpace(pseenv.Get(p + "CLIENT"))),
		URL:      strings.TrimSpace(pseenv.Get(p + "URL")),
		Key:      strings.TrimSpace(pseenv.Get(p + "KEY")),
		SavePath: strings.TrimSpace(pseenv.Get(p + "SAVE_PATH")),
	}
	if c.Kind != "rtorrent" {
		c.Kind = "qbittorrent"
	}
	c.PathMap, _ = ParsePathMap(pseenv.Get(p + "PATH_MAP"))
	if r, err := ParseRatio(pseenv.Get(p + "RATIO")); err == nil {
		c.Ratio = r
	}
	if d, err := ParseSeedTime(pseenv.Get(p + "SEED_TIME")); err == nil {
		c.SeedTime = d
	}
	return c, c.URL != ""
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

// ParseSeedTime lit une durée de partage (« 72h », « 30m ») ; vide : aucune.
func ParseSeedTime(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Minute {
		return 0, errors.New("durée attendue, au moins 1m : ex. 72h ou 168h")
	}
	return d, nil
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

// ParseTrackerRules lit « c411=2/72h; tracker.exemple.org=1; autre=/168h » :
// tracker=ratio/durée, l'une ou l'autre facultative, séparés par « ; ».
func ParseTrackerRules(v string) ([]TrackerRule, error) {
	var out []TrackerRule
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, limits, ok := strings.Cut(part, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if !ok || name == "" || strings.ContainsAny(name, " /:") {
			return nil, errors.New("format attendu : tracker=ratio/durée ; ex. c411=2/72h; exemple.org=1")
		}
		ratioRaw, timeRaw, _ := strings.Cut(strings.TrimSpace(limits), "/")
		r, err := ParseRatio(ratioRaw)
		if err != nil {
			return nil, errors.New(name + " : " + err.Error())
		}
		d, err := ParseSeedTime(timeRaw)
		if err != nil {
			return nil, errors.New(name + " : " + err.Error())
		}
		if r == 0 && d == 0 {
			return nil, errors.New(name + " : ratio ou durée attendu, ex. " + name + "=2/72h")
		}
		out = append(out, TrackerRule{Match: name, Ratio: r, SeedTime: d})
	}
	return out, nil
}

// Limits renvoie le ratio et la durée de partage d'un torrent envoyé à c :
// une règle de tracker l'emporte, champ par champ, sur les limites du client.
func (s Settings) Limits(c ClientConf, magnet string) (float64, time.Duration) {
	ratio, seed := c.Ratio, c.SeedTime
	hosts := trackerHosts(magnet)
	for _, r := range s.Trackers {
		matched := false
		for _, h := range hosts {
			if strings.Contains(h, r.Match) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if r.Ratio > 0 {
			ratio = r.Ratio
		}
		if r.SeedTime > 0 {
			seed = r.SeedTime
		}
		break
	}
	return ratio, seed
}

// trackerHosts renvoie les noms d'hôte des annonces (tr=) d'un magnet.
func trackerHosts(magnet string) []string {
	_, q, ok := strings.Cut(magnet, "?")
	if !ok {
		return nil
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return nil
	}
	var out []string
	for _, tr := range vals["tr"] {
		if u, err := url.Parse(tr); err == nil && u.Hostname() != "" {
			out = append(out, strings.ToLower(u.Hostname()))
		}
	}
	return out
}

// Local traduit un chemin du client en chemin vu par Remix. ok est faux
// quand des correspondances sont réglées et qu'aucune ne s'applique : Remix
// ne lit alors rien hors des dossiers indiqués.
func (c ClientConf) Local(clientPath string) (string, bool) {
	p := path.Clean(clientPath)
	if !path.IsAbs(p) {
		return "", false
	}
	if len(c.PathMap) == 0 {
		return p, true
	}
	best := -1
	for i, m := range c.PathMap {
		if (p == m.Client || strings.HasPrefix(p, strings.TrimRight(m.Client, "/")+"/")) && (best < 0 || len(m.Client) > len(c.PathMap[best].Client)) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	m := c.PathMap[best]
	return path.Join(m.Local, strings.TrimPrefix(p, m.Client)), true
}

// NewClient construit le client.
func (c ClientConf) NewClient(hc *http.Client) (Client, error) {
	if c.Kind == "rtorrent" {
		return NewRTorrent(c.URL, hc)
	}
	return NewQBittorrent(c.URL, c.Key, hc)
}

// Label est le nom affiché du client : « qBittorrent », « Client 2 (rTorrent) ».
func (c ClientConf) Label() string {
	name := "qBittorrent"
	if c.Kind == "rtorrent" {
		name = "rTorrent"
	}
	if c.N > 1 {
		return "Client " + strconv.Itoa(c.N) + " (" + name + ")"
	}
	return name
}

// Label est le nom affiché de l'ensemble des clients.
func (s Settings) Label() string {
	if len(s.Clients) == 1 {
		return s.Clients[0].Label()
	}
	return strconv.Itoa(len(s.Clients)) + " clients"
}
