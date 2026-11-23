package releasedb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

// PreFeedConf est un flux RSS ou une API personnels, avec sa clé facultative.
// La clé n'est envoyée qu'en en-tête X-API-Key, à cette adresse seulement.
type PreFeedConf struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
	Key  string `json:"key,omitempty"`
}

// PreProxy fait passer les appels PRE (flux publics et flux privés) par un proxy ou un
// relais, pour les serveurs que les sites de pre refusent (adresses d'hébergeurs).
type PreProxy struct {
	// Type : « » (aucun), « proxy » (HTTP, HTTPS ou SOCKS5) ou « mediaflow ».
	Type string
	URL  string
	// Auth : « utilisateur:mot de passe » pour un proxy, mot de passe API pour MediaFlow.
	Auth string
}

// PreDBSettings règle l'import des pre (variables PSE_PREDB*).
type PreDBSettings struct {
	Enabled bool
	// Public : flux publics (désactivable pour n'utiliser que les flux personnels).
	Public bool
	// Feeds : flux ou API personnels, propres à l'utilisateur.
	Feeds []PreFeedConf
	Proxy PreProxy
	Langs []string // fr, multi, vostfr
	Delay time.Duration
	Every time.Duration
}

const preDBMaxFeeds = 20

// LoadPreDBSettings lit la configuration effective.
func LoadPreDBSettings() PreDBSettings { return LoadPreDBSettingsWith(pseenv.Get) }

// LoadPreDBSettingsWith lit la configuration avec une autre source de valeurs (test des
// réglages saisis avant enregistrement).
func LoadPreDBSettingsWith(get func(string) string) PreDBSettings {
	s := PreDBSettings{
		Enabled: strings.EqualFold(strings.TrimSpace(get("PSE_PREDB")), "on"),
		Public:  !strings.EqualFold(strings.TrimSpace(get("PSE_PREDB_PUBLIC")), "off"),
		Delay:   preDBDefaultDelay,
		Every:   preDBDefaultEvery,
	}
	s.Feeds = parsePreFeeds(get("PSE_PREDB_FEEDS"))
	if len(s.Feeds) == 0 {
		// Ancien réglage : une liste d'adresses et une clé commune.
		key := strings.TrimSpace(get("PSE_PREDB_PRIVATE_KEY"))
		for _, u := range strings.Fields(get("PSE_PREDB_PRIVATE_URL")) {
			if p, ok := preFeedURL(u); ok && len(s.Feeds) < preDBMaxFeeds {
				s.Feeds = append(s.Feeds, PreFeedConf{URL: p, Key: key})
			}
		}
	}
	switch t := strings.TrimSpace(get("PSE_PREDB_PROXY_TYPE")); t {
	case "proxy", "mediaflow":
		s.Proxy = PreProxy{Type: t, URL: strings.TrimSpace(get("PSE_PREDB_PROXY_URL")), Auth: strings.TrimSpace(get("PSE_PREDB_PROXY_AUTH"))}
	}
	for _, l := range strings.Split(strings.ToLower(get("PSE_PREDB_LANGS")), ",") {
		switch l = strings.TrimSpace(l); l {
		case "fr", "multi", "vostfr":
			s.Langs = append(s.Langs, l)
		}
	}
	if len(s.Langs) == 0 {
		s.Langs = []string{"fr", "multi", "vostfr"}
	}
	if d, err := time.ParseDuration(strings.TrimSpace(get("PSE_PREDB_DELAY"))); err == nil && d >= 0 {
		s.Delay = min(d, 48*time.Hour)
	}
	if d, err := time.ParseDuration(strings.TrimSpace(get("PSE_PREDB_INTERVAL"))); err == nil {
		s.Every = d
	}
	s.Every = max(s.Every, preDBMinEvery)
	return s
}

// preFeedURL valide l'adresse d'un flux personnel : http ou https, sans identifiants.
func preFeedURL(raw string) (string, bool) {
	p, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Hostname() == "" || p.User != nil {
		return "", false
	}
	return p.String(), true
}

func parsePreFeeds(raw string) []PreFeedConf {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var list []PreFeedConf
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil
	}
	out := make([]PreFeedConf, 0, len(list))
	for _, f := range list {
		u, ok := preFeedURL(f.URL)
		if !ok || len(out) >= preDBMaxFeeds {
			continue
		}
		out = append(out, PreFeedConf{Name: strings.TrimSpace(f.Name), URL: u, Key: strings.TrimSpace(f.Key)})
	}
	return out
}

// Problem décrit ce qui empêche d'utiliser le proxy, ou renvoie une chaîne vide.
func (p PreProxy) Problem() string {
	if p.Type == "" {
		return ""
	}
	u, err := url.Parse(p.URL)
	if p.URL == "" || err != nil || u.Hostname() == "" || u.User != nil {
		return "adresse du proxy invalide (sans identifiants dedans)"
	}
	switch p.Type {
	case "proxy":
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return "adresse du proxy : http://, https:// ou socks5:// attendu"
		}
	case "mediaflow":
		if u.Scheme != "http" && u.Scheme != "https" {
			return "adresse du relais : http:// ou https:// attendu"
		}
		if p.Auth == "" {
			return "mot de passe API du relais manquant"
		}
	}
	return ""
}

// preFeed est un flux à lire : public ou personnel.
type preFeed struct {
	id, label, url, key string
	public              bool
}

func (c PreDBSettings) feeds() []preFeed {
	var out []preFeed
	if c.Public {
		for _, u := range preDBPublicFeeds {
			out = append(out, preFeed{id: preFeedID(true, u, 0), url: u, public: true})
		}
	}
	for i, f := range c.Feeds {
		out = append(out, preFeed{id: preFeedID(false, "", i+1), label: f.Name, url: f.URL, key: f.Key})
	}
	return out
}

// preValidateTarget refuse les destinations non publiques d'un flux lu via un proxy
// (le proxy lui-même est un choix explicite de l'utilisateur). Remplaçable en test.
var preValidateTarget = func(ctx context.Context, raw string) error {
	return (&safefetch.Guard{}).ValidatePublicURL(ctx, raw)
}

// preProxyTransport construit le transport d'un proxy HTTP, HTTPS ou SOCKS5.
func preProxyTransport(p PreProxy) (*http.Transport, error) {
	u, err := url.Parse(p.URL)
	if err != nil {
		return nil, err
	}
	if p.Auth != "" {
		if user, pass, ok := strings.Cut(p.Auth, ":"); ok {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(p.Auth)
		}
	}
	return &http.Transport{
		Proxy:                 http.ProxyURL(u),
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 25 * time.Second,
		DisableKeepAlives:     true,
	}, nil
}

// fetchPre lit un flux. Les flux publics comme les flux privés passent par le proxy ou le
// relais choisi, s'il y en a un ; la clé d'un flux privé n'est envoyée qu'en en-tête.
func fetchPre(ctx context.Context, f preFeed, cfg PreDBSettings) ([]PreItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, errors.New("adresse invalide")
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, application/json;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "Ether-Remix")
	if f.key != "" {
		req.Header.Set("X-API-Key", f.key)
	}
	var c http.Client
	relay := false
	switch {
	case cfg.Proxy.Type == "":
		c = *preClient
	case cfg.Proxy.Problem() != "":
		return nil, errors.New("proxy mal configuré")
	default:
		if err := preValidateTarget(ctx, f.url); err != nil {
			return nil, errors.New("adresse non autorisée")
		}
		if cfg.Proxy.Type == "mediaflow" {
			routed, err := egress.MediaFlowRequest(req, cfg.Proxy.URL, cfg.Proxy.Auth)
			if err != nil {
				return nil, errors.New("relais mal configuré")
			}
			req, relay = routed, true
			c = *egress.MediaFlowRelayClient(30 * time.Second)
		} else {
			t, err := preProxyTransport(cfg.Proxy)
			if err != nil {
				return nil, errors.New("proxy mal configuré")
			}
			c = http.Client{Transport: t, Timeout: 30 * time.Second}
		}
	}
	if !relay {
		// La clé personnelle ne suit jamais une redirection vers un autre hôte.
		key := f.key
		c.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 || (key != "" && next.URL.Host != via[0].URL.Host) {
				return http.ErrUseLastResponse
			}
			return nil
		}
	}
	res, err := c.Do(req)
	if err != nil {
		switch {
		case relay:
			return nil, errors.New("relais inaccessible")
		case cfg.Proxy.Type == "proxy":
			return nil, errors.New("proxy inaccessible")
		}
		return nil, errors.New("flux inaccessible")
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusProxyAuthRequired:
		return nil, errors.New("proxy : identifiants refusés (407)")
	case res.StatusCode != http.StatusOK && relay:
		return nil, fmt.Errorf("relais refusé (%d)", res.StatusCode)
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("flux refusé (%d)", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, preDBMaxBody))
	if err != nil {
		return nil, errors.New("flux interrompu")
	}
	return ParsePreFeed(body)
}

// readPreFeed lit un flux et garde les annonces qui correspondent aux langues choisies.
func readPreFeed(ctx context.Context, f preFeed, cfg PreDBSettings) (fetched int, kept []PreItem, err error) {
	items, err := fetchPre(ctx, f, cfg)
	if err != nil {
		return 0, nil, err
	}
	for _, it := range items {
		if _, ok := PreAccept(it.Name, it.Section, cfg.Langs); ok {
			kept = append(kept, it)
		}
	}
	return len(items), kept, nil
}

// PreTestResult est le résultat d'un test des flux : le proxy, puis chaque flux.
type PreTestResult struct {
	Proxy string       `json:"proxy,omitempty"` // problème de configuration du proxy
	Runs  []PreFeedRun `json:"runs"`
}

// TestPreFeeds lit chaque flux maintenant, sans rien enregistrer, et renvoie pour chacun
// le nombre d'annonces reçues et retenues, quelques exemples ou l'erreur rencontrée.
func TestPreFeeds(ctx context.Context, cfg PreDBSettings) PreTestResult {
	out := PreTestResult{Runs: []PreFeedRun{}}
	out.Proxy = cfg.Proxy.Problem()
	feeds := cfg.feeds()
	out.Runs = make([]PreFeedRun, len(feeds))
	var wg sync.WaitGroup
	for i, f := range feeds {
		wg.Add(1)
		go func() { // les flux sont lus en parallèle : un flux lent ne retarde pas les autres
			defer wg.Done()
			run := PreFeedRun{Feed: f.id, Label: f.label, At: time.Now().Unix()}
			fetched, kept, err := readPreFeed(ctx, f, cfg)
			if err != nil {
				run.State, run.Error = "error", err.Error()
			} else {
				run.State, run.Fetched, run.Kept = "ok", fetched, len(kept)
				for _, it := range kept {
					if len(run.Sample) < 3 {
						run.Sample = append(run.Sample, it.Name)
					}
				}
			}
			out.Runs[i] = run
		}()
	}
	wg.Wait()
	return out
}
