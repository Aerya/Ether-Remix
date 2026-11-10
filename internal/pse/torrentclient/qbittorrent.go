package torrentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// QBittorrent parle à l'API WebUI v2 de qBittorrent (4.1 et suivants).
// Les limites de ratio et de durée sont celles de qBittorrent, posées sur
// chaque torrent ajouté ; son action à l'arrivée (pause, arrêt…) reste
// celle réglée dans qBittorrent.
type QBittorrent struct {
	base       *url.URL // sans identifiants
	user, pass string
	hc         *http.Client

	mu     sync.Mutex
	logged bool
}

// NewQBittorrent prépare le client ; les identifiants sont dans l'adresse
// (http://utilisateur:motdepasse@hôte:port), ou absents quand qBittorrent
// dispense le réseau local d'authentification.
func NewQBittorrent(raw string, hc *http.Client) (*QBittorrent, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("torrentclient: adresse qBittorrent http(s)://… attendue")
	}
	q := &QBittorrent{}
	if u.User != nil {
		q.user = u.User.Username()
		q.pass, _ = u.User.Password()
	}
	u.User = nil
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	q.base = u
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	jar, _ := cookiejar.New(nil)
	c := *hc
	c.Jar = jar
	q.hc = &c
	return q, nil
}

func (q *QBittorrent) Kind() string { return "qbittorrent" }

func (q *QBittorrent) endpoint(p string) string {
	u := *q.base
	u.Path = q.base.Path + "/api/v2/" + p
	return u.String()
}

func (q *QBittorrent) login(ctx context.Context) error {
	form := url.Values{"username": {q.user}, "password": {q.pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.endpoint("auth/login"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	q.origin(req)
	resp, err := q.hc.Do(req)
	if err != nil {
		return fmt.Errorf("torrentclient: qBittorrent injoignable")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode == http.StatusForbidden || strings.TrimSpace(string(body)) == "Fails." {
		return ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("torrentclient: connexion qBittorrent refusée (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// origin : qBittorrent refuse les requêtes dont l'origine diffère de son adresse.
func (q *QBittorrent) origin(req *http.Request) {
	o := q.base.Scheme + "://" + q.base.Host
	req.Header.Set("Referer", o)
	req.Header.Set("Origin", o)
}

// call envoie une requête ; une session expirée est rouverte une fois.
func (q *QBittorrent) call(ctx context.Context, method, p string, form url.Values) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		q.mu.Lock()
		if !q.logged && q.user != "" {
			if err := q.login(ctx); err != nil {
				q.mu.Unlock()
				return nil, err
			}
			q.logged = true
		}
		q.mu.Unlock()
		var req *http.Request
		var err error
		if method == http.MethodGet {
			u := q.endpoint(p)
			if len(form) > 0 {
				u += "?" + form.Encode()
			}
			req, err = http.NewRequestWithContext(ctx, method, u, nil)
		} else {
			req, err = http.NewRequestWithContext(ctx, method, q.endpoint(p), strings.NewReader(form.Encode()))
			if req != nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
		}
		if err != nil {
			return nil, err
		}
		q.origin(req)
		resp, err := q.hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("torrentclient: qBittorrent injoignable")
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			if attempt == 0 && q.user != "" {
				q.mu.Lock()
				q.logged = false
				q.mu.Unlock()
				continue
			}
			return nil, ErrAuth
		}
		if resp.StatusCode == http.StatusConflict {
			return body, errConflict
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("torrentclient: qBittorrent %s : HTTP %d", p, resp.StatusCode)
		}
		return body, rerr
	}
}

var errConflict = fmt.Errorf("torrentclient: conflit")

func (q *QBittorrent) Version(ctx context.Context) (string, error) {
	b, err := q.call(ctx, http.MethodGet, "app/version", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (q *QBittorrent) Add(ctx context.Context, o AddOptions) error {
	if o.Category != "" {
		form := url.Values{"category": {o.Category}}
		// Déjà existante : réponse 409, sans conséquence.
		_, _ = q.call(ctx, http.MethodPost, "torrents/createCategory", form)
	}
	form := url.Values{"urls": {o.Magnet}}
	if o.Category != "" {
		form.Set("category", o.Category)
	}
	if o.SavePath != "" {
		form.Set("savepath", o.SavePath)
		form.Set("autoTMM", "false")
	}
	if o.Ratio > 0 {
		form.Set("ratioLimit", strconv.FormatFloat(o.Ratio, 'f', 2, 64))
	}
	if o.SeedTime > 0 {
		form.Set("seedingTimeLimit", strconv.Itoa(int(o.SeedTime/time.Minute)))
	}
	b, err := q.call(ctx, http.MethodPost, "torrents/add", form)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) == "Fails." {
		return fmt.Errorf("torrentclient: qBittorrent a refusé le torrent")
	}
	return nil
}

type qbTorrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Progress    float64 `json:"progress"`
	State       string  `json:"state"`
	Ratio       float64 `json:"ratio"`
	SeedingTime int64   `json:"seeding_time"`
	CompletedOn int64   `json:"completion_on"`
	SavePath    string  `json:"save_path"`
	Tags        string  `json:"tags"`
}

func (t qbTorrent) torrent() Torrent {
	out := Torrent{Hash: normHash(t.Hash), Name: t.Name, Category: t.Category, Ratio: t.Ratio,
		Seeding: time.Duration(t.SeedingTime) * time.Second, Root: t.SavePath}
	out.Done = t.Progress >= 1 || strings.HasSuffix(t.State, "UP")
	switch t.State {
	case "pausedUP", "pausedDL", "stoppedUP", "stoppedDL", "error", "missingFiles", "unknown":
	default:
		out.Active = true
	}
	if t.CompletedOn > 0 {
		out.Finished = time.Unix(t.CompletedOn, 0)
	}
	for _, tag := range strings.Split(t.Tags, ",") {
		if strings.TrimSpace(tag) == NotifiedTag {
			out.Notified = true
		}
	}
	return out
}

func (q *QBittorrent) info(ctx context.Context, form url.Values) ([]Torrent, error) {
	b, err := q.call(ctx, http.MethodGet, "torrents/info", form)
	if err != nil {
		return nil, err
	}
	var raw []qbTorrent
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("torrentclient: réponse qBittorrent illisible")
	}
	out := make([]Torrent, 0, len(raw))
	for _, t := range raw {
		out = append(out, t.torrent())
	}
	return out, nil
}

func (q *QBittorrent) Get(ctx context.Context, hash string) (*Torrent, error) {
	hash = normHash(hash)
	if !validHash(hash) {
		return nil, nil
	}
	ts, err := q.info(ctx, url.Values{"hashes": {hash}})
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.Hash == hash {
			return &t, nil
		}
	}
	return nil, nil
}

func (q *QBittorrent) Lookup(ctx context.Context, hashes []string) (map[string]Torrent, error) {
	var want []string
	for _, h := range hashes {
		if h = normHash(h); validHash(h) {
			want = append(want, h)
		}
	}
	out := map[string]Torrent{}
	for len(want) > 0 {
		n := min(len(want), 100)
		ts, err := q.info(ctx, url.Values{"hashes": {strings.Join(want[:n], "|")}})
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			out[t.Hash] = t
		}
		want = want[n:]
	}
	return out, nil
}

func (q *QBittorrent) List(ctx context.Context, category string) ([]Torrent, error) {
	form := url.Values{}
	if category != "" {
		form.Set("category", category)
	}
	return q.info(ctx, form)
}

func (q *QBittorrent) Files(ctx context.Context, t Torrent) ([]File, error) {
	b, err := q.call(ctx, http.MethodGet, "torrents/files", url.Values{"hash": {t.Hash}})
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("torrentclient: réponse qBittorrent illisible")
	}
	out := make([]File, 0, len(raw))
	for _, f := range raw {
		out = append(out, File{Path: path.Join(t.Root, f.Name), Size: f.Size})
	}
	return out, nil
}

func (q *QBittorrent) MarkNotified(ctx context.Context, hash string) error {
	_, err := q.call(ctx, http.MethodPost, "torrents/addTags", url.Values{"hashes": {normHash(hash)}, "tags": {NotifiedTag}})
	return err
}

// Stop : qBittorrent applique lui-même les limites posées à l'ajout.
func (q *QBittorrent) Stop(context.Context, string) error { return ErrUnsupported }
