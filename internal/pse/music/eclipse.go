package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Eclipse résout les flux via un ou plusieurs addons Eclipse, comme Remux :
// GET <base>/search?q=artiste titre, puis GET <base>/stream/<id> qui renvoie
// {"url": …, "quality": …}. Les addons sont essayés dans l'ordre.
type Eclipse struct {
	bases []string
	http  *http.Client
	now   func() time.Time

	mu    sync.Mutex
	cache map[int64]cachedStream
}

type cachedStream struct {
	s     Stream
	until time.Time
}

// streamTTL borne la réutilisation d'une URL de flux : les lecteurs
// relancent une requête à chaque déplacement dans le morceau.
const streamTTL = 10 * time.Minute

// ParseEclipseURLs découpe une liste de liens manifest (séparés par des
// espaces, virgules ou retours à la ligne) et ne garde que les http(s).
func ParseEclipseURLs(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) }) {
		u, err := url.Parse(f)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// NewEclipse prépare un résolveur pour les liens manifest fournis.
func NewEclipse(manifests []string, hc *http.Client) *Eclipse {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	e := &Eclipse{http: hc, now: time.Now, cache: map[int64]cachedStream{}}
	for _, m := range manifests {
		if b, err := eclipseBase(m); err == nil {
			e.bases = append(e.bases, b)
		}
	}
	return e
}

// eclipseBase retire /manifest.json du chemin et garde la requête (un jeton
// d'addon peut s'y trouver).
func eclipseBase(manifest string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(manifest))
	if err != nil || u.Host == "" {
		return "", errors.New("lien manifest invalide")
	}
	p := strings.TrimRight(u.Path, "/")
	if path.Base(p) == "manifest.json" {
		p = path.Dir(p)
	}
	u.Path = strings.TrimRight(p, "/")
	u.RawPath = ""
	return u.String(), nil
}

func endpoint(base string, segments []string, query url.Values) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	// Chaque segment est échappé en entier : un identifiant d'addon peut
	// contenir « / ».
	raw := strings.TrimRight(u.EscapedPath(), "/")
	for _, s := range segments {
		raw += "/" + url.PathEscape(s)
	}
	keep := u.RawQuery
	if u, err = u.Parse(raw); err != nil {
		return "", err
	}
	u.RawQuery = keep
	if len(query) > 0 {
		q := u.Query()
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (e *Eclipse) getJSON(ctx context.Context, u string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Ether-Remix/1.0 (+https://github.com/Aerya/Ether-Remix)")
	res, err := e.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("music: addon Eclipse injoignable: %w", ue.Err)
		}
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("music: addon Eclipse a répondu %d", res.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 4<<20))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("music: réponse Eclipse illisible: %w", err)
	}
	return v, nil
}

// Check vérifie que chaque addon publie un manifeste avec search et stream.
func (e *Eclipse) Check(ctx context.Context) error {
	if len(e.bases) == 0 {
		return errors.New("aucun lien manifest valide")
	}
	for i, b := range e.bases {
		u, _ := endpoint(b, []string{"manifest.json"}, nil)
		v, err := e.getJSON(ctx, u)
		if err != nil {
			return fmt.Errorf("addon %d : %v", i+1, err)
		}
		m, _ := v.(map[string]any)
		res := map[string]bool{}
		if arr, ok := m["resources"].([]any); ok {
			for _, r := range arr {
				switch t := r.(type) {
				case string:
					res[t] = true
				case map[string]any:
					res[str(t["name"])] = true
				}
			}
		}
		if !res["search"] || !res["stream"] {
			return fmt.Errorf("addon %d : le manifeste ne propose pas search et stream", i+1)
		}
	}
	return nil
}

// Count renvoie le nombre d'addons utilisables.
func (e *Eclipse) Count() int { return len(e.bases) }

type eclipseHit struct {
	id, title, artist, isrc string
	duration                int64
}

// Resolve cherche le titre sur chaque addon et renvoie le premier flux.
func (e *Eclipse) Resolve(ctx context.Context, t Track) (Stream, error) {
	if len(e.bases) == 0 {
		return Stream{}, errors.New("music: aucun addon Eclipse configuré")
	}
	e.mu.Lock()
	if c, ok := e.cache[t.ID]; ok && e.now().Before(c.until) && !skipped(ctx, c.s.URL) {
		e.mu.Unlock()
		return c.s, nil
	}
	e.mu.Unlock()

	query := strings.TrimSpace(t.Artist.Name + " " + t.Title)
	var last error = ErrNotFound
	for _, b := range e.bases {
		s, err := e.resolveOn(ctx, b, query, t)
		if err != nil {
			if ctx.Err() != nil {
				return Stream{}, ctx.Err()
			}
			last = err
			continue
		}
		e.mu.Lock()
		if len(e.cache) > 2048 {
			e.cache = map[int64]cachedStream{}
		}
		e.cache[t.ID] = cachedStream{s: s, until: streamExpiry(s, e.now())}
		e.mu.Unlock()
		return s, nil
	}
	return Stream{}, last
}

func (e *Eclipse) resolveOn(ctx context.Context, base, query string, t Track) (Stream, error) {
	u, err := endpoint(base, []string{"search"}, url.Values{"q": {query}})
	if err != nil {
		return Stream{}, err
	}
	v, err := e.getJSON(ctx, u)
	if err != nil {
		return Stream{}, err
	}
	m, _ := v.(map[string]any)
	var hits []eclipseHit
	for _, o := range objects(m["tracks"]) {
		h := eclipseHit{id: str(o["id"]), title: str(o["title"]), isrc: str(o["isrc"]), duration: num(o["duration"])}
		switch a := o["artist"].(type) {
		case string:
			h.artist = a
		case map[string]any:
			h.artist = str(a["name"])
		}
		if h.id != "" {
			hits = append(hits, h)
		}
	}
	best, ok := bestHit(hits, t)
	if !ok {
		return Stream{}, ErrNotFound
	}
	su, err := endpoint(base, []string{"stream", best.id}, nil)
	if err != nil {
		return Stream{}, err
	}
	sv, err := e.getJSON(ctx, su)
	if err != nil {
		return Stream{}, err
	}
	link := findString(sv, "url", 0)
	pu, perr := url.Parse(link)
	if link == "" || perr != nil || pu.Hostname() == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
		return Stream{}, errors.New("music: l'addon Eclipse n'a pas fourni de flux")
	}
	if skipped(ctx, link) {
		return Stream{}, ErrNotFound
	}
	sm, _ := sv.(map[string]any)
	if expiry := num(sm["expiresAt"]); expiry > 0 && expiry <= e.now().Unix() {
		return Stream{}, ErrNotFound
	}
	quality := str(sm["quality"])
	format := str(sm["format"])
	return Stream{URL: link, Quality: quality, Mime: guessMime(format, quality, pu.Path), Provider: addonName(base), ExpiresAt: num(sm["expiresAt"])}, nil
}

// fold normalise un titre pour la comparaison : minuscules, sans accents ni
// ponctuation.
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bestHit choisit le résultat le plus proche (ISRC, version, titre,
// artiste, durée) ; aucun résultat n'est retenu sans correspondance sûre.
func bestHit(hits []eclipseHit, t Track) (eclipseHit, bool) {
	bestScore, best, found := -1, eclipseHit{}, false
	for _, h := range hits {
		score, ok := matchScore(candidate{title: h.title, artist: h.artist, isrc: h.isrc, duration: h.duration}, t)
		if ok && score > bestScore {
			bestScore, best, found = score, h, true
		}
	}
	return best, found
}

func guessMime(format, quality, p string) string {
	for _, s := range []string{format, path.Ext(p)} {
		s = strings.ToLower(s)
		switch {
		case s == "":
		case strings.Contains(s, "flac"):
			return "audio/flac"
		case strings.Contains(s, "mp3"):
			return "audio/mpeg"
		case strings.Contains(s, "aac"), strings.Contains(s, "m4a"), strings.Contains(s, "mp4"):
			return "audio/mp4"
		case strings.Contains(s, "opus"), strings.Contains(s, "ogg"):
			return "audio/ogg"
		}
	}
	return ""
}

// Respect an addon's advertised expiration instead of replaying a stale URL.
func streamExpiry(s Stream, now time.Time) time.Time {
	until := now.Add(streamTTL)
	if s.ExpiresAt > 0 {
		expiry := time.Unix(s.ExpiresAt, 0).Add(-5 * time.Second)
		if expiry.Before(until) {
			until = expiry
		}
	}
	return until
}
