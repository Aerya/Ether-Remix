package torrentclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RTorrent parle XML-RPC à rTorrent, directement (/RPC2) ou via ruTorrent
// (plugins/rpc/rpc.php). La catégorie est le libellé ruTorrent (d.custom1).
// rTorrent n'a pas de limite de partage par torrent : Remix arrête lui-même
// le partage (sans rien supprimer) quand le ratio ou la durée est atteint.
type RTorrent struct {
	endpoint   string // sans identifiants
	user, pass string
	hc         *http.Client
	now        func() time.Time

	mu      sync.Mutex
	listed  time.Time
	all     []Torrent
	listErr error
}

// rtListTTL : la liste complète sert à toutes les lectures pendant ce délai.
const rtListTTL = 10 * time.Second

// rtNotified est la variable rTorrent qui retient l'envoi à cross-seed.
const rtNotified = "remix_xseed"

// rtLimits retient les limites posées par Remix : « ratio/secondes ».
const rtLimits = "remix_limits"

// NewRTorrent prépare le client (http(s)://utilisateur:motdepasse@hôte/RPC2).
func NewRTorrent(raw string, hc *http.Client) (*RTorrent, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("torrentclient: adresse XML-RPC http(s)://… attendue")
	}
	r := &RTorrent{now: time.Now}
	if u.User != nil {
		r.user = u.User.Username()
		r.pass, _ = u.User.Password()
	}
	u.User = nil
	u.Fragment = ""
	r.endpoint = u.String()
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	r.hc = hc
	return r, nil
}

func (r *RTorrent) Kind() string { return "rtorrent" }

func (r *RTorrent) call(ctx context.Context, method string, params ...any) (any, error) {
	body, err := xmlrpcRequest(method, params...)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")
	if r.user != "" {
		req.SetBasicAuth(r.user, r.pass)
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("torrentclient: rTorrent injoignable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("torrentclient: rTorrent %s : HTTP %d", method, resp.StatusCode)
	}
	return xmlrpcResponse(http.MaxBytesReader(nil, resp.Body, 64<<20))
}

func (r *RTorrent) Version(ctx context.Context) (string, error) {
	v, err := r.call(ctx, "system.client_version")
	if err != nil {
		return "", err
	}
	s, _ := v.(string)
	return s, nil
}

func (r *RTorrent) Add(ctx context.Context, o AddOptions) error {
	params := []any{"", o.Magnet}
	if o.Category != "" {
		params = append(params, "d.custom1.set="+url.PathEscape(o.Category))
	}
	if o.SavePath != "" {
		params = append(params, "d.directory.set="+o.SavePath)
	}
	if o.Ratio > 0 || o.SeedTime > 0 {
		params = append(params, "d.custom.set="+rtLimits+","+formatLimits(o.Ratio, o.SeedTime))
	}
	if _, err := r.call(ctx, "load.start", params...); err != nil {
		return err
	}
	r.mu.Lock()
	r.listed = time.Time{} // la prochaine lecture voit le nouveau torrent
	r.mu.Unlock()
	return nil
}

var rtFields = []any{"", "main", "d.hash=", "d.name=", "d.custom1=", "d.complete=", "d.ratio=",
	"d.timestamp.finished=", "d.directory=", "d.is_active=", "d.custom=" + rtNotified,
	"d.size_bytes=", "d.completed_bytes=", "d.down.rate=", "d.custom=" + rtLimits}

// formatLimits écrit « 1.50/259200 » (ratio, secondes de partage).
func formatLimits(ratio float64, seed time.Duration) string {
	return strconv.FormatFloat(ratio, 'f', 2, 64) + "/" + strconv.FormatInt(int64(seed/time.Second), 10)
}

// parseLimits lit formatLimits ; ok est faux pour une valeur vide ou illisible.
func parseLimits(v string) (float64, time.Duration, bool) {
	a, b, found := strings.Cut(strings.TrimSpace(v), "/")
	if !found {
		return 0, 0, false
	}
	ratio, err1 := strconv.ParseFloat(a, 64)
	secs, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || ratio < 0 || secs < 0 {
		return 0, 0, false
	}
	return ratio, time.Duration(secs) * time.Second, true
}

func (r *RTorrent) listAll(ctx context.Context) ([]Torrent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.listed.IsZero() && r.now().Sub(r.listed) < rtListTTL {
		return r.all, r.listErr
	}
	v, err := r.call(ctx, "d.multicall2", rtFields...)
	r.listed, r.listErr = r.now(), err
	if err != nil {
		r.all = nil
		return nil, err
	}
	rows, _ := v.([]any)
	out := make([]Torrent, 0, len(rows))
	for _, row := range rows {
		f, ok := row.([]any)
		if !ok || len(f) < 9 {
			continue
		}
		str := func(i int) string {
			if i >= len(f) {
				return ""
			}
			s, _ := f[i].(string)
			return s
		}
		num := func(i int) int64 {
			if i >= len(f) {
				return 0
			}
			n, _ := f[i].(int64)
			return n
		}
		label, err := url.PathUnescape(str(2))
		if err != nil {
			label = str(2)
		}
		t := Torrent{Hash: normHash(str(0)), Name: str(1), Category: label, Done: num(3) == 1,
			Ratio: float64(num(4)) / 1000, Root: str(6), Active: num(7) == 1, Notified: str(8) == "1"}
		if fin := num(5); fin > 0 {
			t.Finished = time.Unix(fin, 0)
			t.Seeding = r.now().Sub(t.Finished)
		}
		t.Size = num(9)
		if done := num(10); t.Size > 0 {
			t.Progress = min(float64(done)/float64(t.Size), 1)
			if rate := num(11); rate > 0 && done < t.Size {
				t.ETA = time.Duration((t.Size-done)/rate) * time.Second
			}
		}
		if t.Done {
			t.Progress = 1
		}
		t.RatioLimit, t.SeedLimit, t.HasLimits = parseLimits(str(12))
		out = append(out, t)
	}
	r.all = out
	return out, nil
}

func (r *RTorrent) Get(ctx context.Context, hash string) (*Torrent, error) {
	hash = normHash(hash)
	if !validHash(hash) {
		return nil, nil
	}
	all, err := r.listAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range all {
		if t.Hash == hash {
			return &t, nil
		}
	}
	return nil, nil
}

func (r *RTorrent) Lookup(ctx context.Context, hashes []string) (map[string]Torrent, error) {
	all, err := r.listAll(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, h := range hashes {
		want[normHash(h)] = true
	}
	out := map[string]Torrent{}
	for _, t := range all {
		if want[t.Hash] {
			out[t.Hash] = t
		}
	}
	return out, nil
}

func (r *RTorrent) List(ctx context.Context, category string) ([]Torrent, error) {
	all, err := r.listAll(ctx)
	if err != nil {
		return nil, err
	}
	var out []Torrent
	for _, t := range all {
		if category == "" || t.Category == category {
			out = append(out, t)
		}
	}
	return out, nil
}

func (r *RTorrent) Files(ctx context.Context, t Torrent) ([]File, error) {
	v, err := r.call(ctx, "f.multicall", strings.ToUpper(t.Hash), "", "f.path=", "f.size_bytes=")
	if err != nil {
		var f *errFault
		if errors.As(err, &f) {
			return nil, nil // torrent disparu
		}
		return nil, err
	}
	rows, _ := v.([]any)
	out := make([]File, 0, len(rows))
	for _, row := range rows {
		f, ok := row.([]any)
		if !ok || len(f) < 2 {
			continue
		}
		p, _ := f[0].(string)
		n, _ := f[1].(int64)
		out = append(out, File{Path: path.Join(t.Root, p), Size: n})
	}
	return out, nil
}

func (r *RTorrent) MarkNotified(ctx context.Context, hash string) error {
	_, err := r.call(ctx, "d.custom.set", strings.ToUpper(normHash(hash)), rtNotified, "1")
	if err == nil {
		r.mu.Lock()
		r.listed = time.Time{}
		r.mu.Unlock()
	}
	return err
}

// DefaultDir renvoie directory.default de rTorrent.
func (r *RTorrent) DefaultDir(ctx context.Context) (string, error) {
	v, err := r.call(ctx, "directory.default")
	if err != nil {
		return "", err
	}
	s, _ := v.(string)
	return s, nil
}

func (r *RTorrent) Stop(ctx context.Context, hash string) error {
	_, err := r.call(ctx, "d.stop", strings.ToUpper(normHash(hash)))
	if err == nil {
		r.mu.Lock()
		r.listed = time.Time{}
		r.mu.Unlock()
	}
	return err
}
