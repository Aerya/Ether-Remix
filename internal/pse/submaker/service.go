// Package submaker completes explicitly opted-in releases with external subtitles.
// Provider configuration stays in the console secret store; the token is never exported to SeerrCatalog.
package submaker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/subtitles"
)

func init() {
	store.Register("remix_submaker", 1, `CREATE TABLE remix_submaker_jobs(id TEXT PRIMARY KEY, title_key INTEGER NOT NULL, release_name TEXT NOT NULL, state TEXT NOT NULL, language TEXT NOT NULL, translated INTEGER NOT NULL DEFAULT 0, srt TEXT NOT NULL DEFAULT '', updated INTEGER NOT NULL, created INTEGER NOT NULL, error TEXT NOT NULL DEFAULT ''); CREATE TABLE remix_submaker_scopes(id INTEGER PRIMARY KEY, value TEXT NOT NULL); CREATE TABLE remix_submaker_budget(day TEXT PRIMARY KEY, attempts INTEGER NOT NULL);`)
}

type Rule struct {
	Scope   string `json:"scope"`
	Target  string `json:"target"`
	Enabled bool   `json:"enabled"`
}
type Scopes struct {
	Global bool   `json:"global"`
	Rules  []Rule `json:"rules"`
}
type Settings struct {
	Enabled                  bool
	Manifest, Source, Target string
	Daily                    int
}

func Load() Settings {
	n, _ := strconv.Atoi(pseenv.Get("PSE_SUBMAKER_DAILY"))
	if n < 1 || n > 100 {
		n = 10
	}
	source := langtag.Normalize(pseenv.Get("PSE_SUBMAKER_SOURCE"))
	if source == "" {
		source = "en"
	}
	target := langtag.Normalize(pseenv.Get("PSE_SUBMAKER_TARGET"))
	if target == "" {
		target = "fr"
	}
	return Settings{Enabled: pseenv.Get("PSE_SUBMAKER") == "on", Manifest: pseenv.Get("PSE_SUBMAKER_MANIFEST"), Source: source, Target: target, Daily: n}
}

// Endpoint accepts both the public installation and an administrator-chosen private one.
func Endpoint(raw string) (*url.URL, string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, "", errors.New("manifest SubMaker invalide")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "addon" || parts[1] == "" || parts[2] != "manifest.json" {
		return nil, "", errors.New("utilisez le manifeste personnel /addon/…/manifest.json")
	}
	token := parts[1]
	u.Path = ""
	return u, token, nil
}

type Proof struct {
	ID         string `json:"id"`
	Language   string `json:"language"`
	Release    string `json:"release"`
	Translated bool   `json:"translated"`
	SRT        string `json:"srt"`
}
type Job struct {
	ID      string `json:"id"`
	Release string `json:"release"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
	Updated int64  `json:"updated"`
}
type work struct {
	id  string
	req core.SearchRequest
	rel core.Release
	cfg Settings
}
type Service struct {
	DB     *sql.DB
	HTTP   *http.Client
	Match  func(context.Context, Rule, core.RatingKey) bool
	mu     sync.Mutex
	scopes Scopes
	queue  chan work
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(db *sql.DB, client *http.Client) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{DB: db, HTTP: client, queue: make(chan work, 16), ctx: ctx, cancel: cancel}
	var raw string
	if db.QueryRow("SELECT value FROM remix_submaker_scopes WHERE id=1").Scan(&raw) == nil {
		_ = json.Unmarshal([]byte(raw), &s.scopes)
	}
	_, _ = db.Exec("UPDATE remix_submaker_jobs SET state='interrupted',error='Interrupted; retry on next search',updated=0 WHERE state IN ('queued','running')")
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case w := <-s.queue:
				s.run(w)
			}
		}
	}()
	return s
}
func (s *Service) Close() { s.cancel(); s.wg.Wait() }
func Key(req core.SearchRequest) core.RatingKey {
	if req.Type == core.TypeEpisode {
		return core.EpisodeKey(req.IDs.TMDB, req.Season, req.Episode)
	}
	return core.MovieKey(req.IDs.TMDB)
}
func (s *Service) Allowed(ctx context.Context, req core.SearchRequest) bool {
	s.mu.Lock()
	c := s.scopes
	s.mu.Unlock()
	allowed := c.Global
	rank := 0
	key := Key(req)
	if req.Type == core.TypeEpisode {
		key = core.ShowKey(req.IDs.TMDB)
	}
	for _, r := range c.Rules {
		p := 1
		if r.Scope == "collection" {
			p = 2
		}
		if p > rank && s.Match != nil && s.Match(ctx, r, key) {
			allowed = r.Enabled
			rank = p
		}
	}
	return allowed
}
func fingerprint(c Settings, req core.SearchRequest, r core.Release) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%s", c.Manifest, c.Source, c.Target, Key(req), r.InfoHash, Canonical(r.Name))))
	return hex.EncodeToString(h[:])
}

var separators = regexp.MustCompile(`[^\pL\pN]+`)

// SubMaker may list several exact release aliases before its provider label.
func matchesRelease(label, release string) bool {
	for _, alias := range strings.Split(strings.Split(label, " • ")[0], " / ") {
		if Canonical(alias) == Canonical(release) {
			return true
		}
	}
	return false
}

var providerVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.-]+)?$`)

func subtitlePath(p, token string) bool {
	prefix := "/addon/" + token + "/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(p, prefix), "/")
	if len(parts) > 0 && providerVersion.MatchString(parts[0]) {
		parts = parts[1:]
	}
	return len(parts) == 3 && parts[0] == "subtitle" && parts[1] != "" && parts[2] != ""
}

func Canonical(s string) string {
	s = strings.TrimSpace(strings.Split(s, " • ")[0])
	ext := strings.ToLower(path.Ext(s))
	switch ext {
	case ".srt", ".vtt", ".ass", ".ssa", ".mkv", ".mp4", ".avi", ".m4v":
		s = strings.TrimSuffix(s, path.Ext(s))
	}
	return strings.Trim(separators.ReplaceAllString(strings.ToLower(s), " "), " ")
}

// Ensure never blocks an indexer on translation. One worker and a persisted daily
// attempt budget prevent fan-out and duplicate charges across repeated searches.
func (s *Service) Ensure(ctx context.Context, req core.SearchRequest, rel core.Release) (*Proof, bool) {
	c := Load()
	if (req.Type != core.TypeMovie && req.Type != core.TypeEpisode) || (req.Type == core.TypeEpisode && req.Episode < 1) || !c.Enabled || req.IDs.TMDB <= 0 || req.IDs.IMDB == "" || !s.Allowed(ctx, req) {
		return nil, false
	}
	if _, _, e := Endpoint(c.Manifest); e != nil {
		return nil, false
	}
	for _, l := range append(append([]string{}, rel.Info.Languages...), rel.Info.SubtitleLanguages...) {
		if langtag.MatchTier(c.Target, l) > 0 {
			return nil, false
		}
	}
	if len(Canonical(rel.Name)) < 12 {
		return nil, false
	}
	id := fingerprint(c, req, rel)
	s.mu.Lock()
	defer s.mu.Unlock()
	var state, body string
	var updated int64
	var translated bool
	e := s.DB.QueryRowContext(ctx, "SELECT state,srt,updated,translated FROM remix_submaker_jobs WHERE id=?", id).Scan(&state, &body, &updated, &translated)
	if e == nil {
		if state == "ready" {
			return &Proof{ID: id, Language: c.Target, Release: rel.Name, Translated: translated, SRT: body}, false
		}
		if state == "queued" || state == "running" {
			return nil, true
		}
		if time.Now().Unix()-updated < 3600 {
			return nil, false
		}
	}
	if len(s.queue) >= cap(s.queue) {
		return nil, true
	}
	var count int
	day := time.Now().UTC().Format("2006-01-02")
	_ = s.DB.QueryRowContext(ctx, "SELECT attempts FROM remix_submaker_budget WHERE day=?", day).Scan(&count)
	if count >= c.Daily {
		return nil, false
	}
	var bytes int64
	_ = s.DB.QueryRowContext(ctx, "SELECT COALESCE(sum(length(srt)),0) FROM remix_submaker_jobs").Scan(&bytes)
	if bytes >= 512<<20 {
		return nil, false
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO remix_submaker_budget(day,attempts) VALUES(?,1) ON CONFLICT(day) DO UPDATE SET attempts=attempts+1", day)
	if e != nil {
		return nil, false
	}
	now := time.Now().Unix()
	_, e = s.DB.ExecContext(ctx, `INSERT INTO remix_submaker_jobs(id,title_key,release_name,state,language,updated,created) VALUES(?,?,?,'queued',?,?,?) ON CONFLICT(id) DO UPDATE SET state='queued',updated=excluded.updated,created=excluded.created,error=''`, id, Key(req), rel.Name, c.Target, now, now)
	if e != nil {
		return nil, false
	}
	s.queue <- work{id, req, rel, c}
	return nil, true
}
func (s *Service) request(ctx context.Context, method, address string, body []byte) ([]byte, error) {
	r, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		return nil, errors.New("invalid provider request")
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	client := *s.HTTP
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return errors.New("provider redirect rejected")
		}
		return nil
	}
	resp, e := client.Do(r)
	if e != nil {
		return nil, errors.New("SubMaker unavailable or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SubMaker HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if e != nil || len(b) > 2<<20 {
		return nil, errors.New("invalid or oversized subtitle response")
	}
	return b, nil
}
func (s *Service) run(w work) {
	ctx, cancel := context.WithTimeout(s.ctx, 8*time.Minute)
	defer cancel()
	_, _ = s.DB.ExecContext(ctx, "UPDATE remix_submaker_jobs SET state='running',updated=? WHERE id=?", time.Now().Unix(), w.id)
	var p *Proof
	var err error
	if Load() != w.cfg || !s.Allowed(ctx, w.req) {
		err = errors.New("Settings changed")
	} else {
		p, err = s.resolve(ctx, w)
	}
	state, msg := "ready", ""
	if err != nil {
		state = "failed"
		msg = err.Error()
	}
	if Load() != w.cfg || !s.Allowed(ctx, w.req) {
		state = "cancelled"
		msg = "Settings changed"
		p = nil
	}
	body := ""
	translated := false
	if p != nil {
		body = p.SRT
		translated = p.Translated
	}
	_, _ = s.DB.Exec("UPDATE remix_submaker_jobs SET state=?,srt=?,translated=?,error=?,updated=? WHERE id=?", state, body, translated, msg, time.Now().Unix(), w.id)
}
func (s *Service) resolve(ctx context.Context, w work) (*Proof, error) {
	base, token, e := Endpoint(w.cfg.Manifest)
	if e != nil {
		return nil, e
	}
	typ, id := "movie", w.req.IDs.IMDB
	if w.req.Type == core.TypeEpisode {
		typ = "series"
		id = fmt.Sprintf("%s:%d:%d", id, w.req.Season, w.req.Episode)
	}
	extra := url.Values{"filename": {w.rel.Name}}.Encode()
	endpoint := base.String() + "/addon/" + url.PathEscape(token) + "/subtitles/" + typ + "/" + id + "/" + extra + ".json"
	raw, e := s.request(ctx, "GET", endpoint, nil)
	if e != nil {
		return nil, e
	}
	var listing struct {
		Subtitles []struct{ ID, URL, Lang, Label, Filename string }
	}
	if json.Unmarshal(raw, &listing) != nil {
		return nil, errors.New("invalid SubMaker listing")
	}
	for _, wanted := range []string{w.cfg.Target, w.cfg.Source} {
		for _, sub := range listing.Subtitles {
			if langtag.MatchTier(wanted, sub.Lang) == 0 {
				continue
			}
			name := sub.Filename
			if name == "" {
				name = sub.Label
			}
			if !matchesRelease(name, w.rel.Name) {
				continue
			}
			u, e := url.Parse(sub.URL)
			if e != nil || u.Host != base.Host || u.Scheme != base.Scheme || !subtitlePath(u.Path, token) {
				continue
			}
			original, e := s.request(ctx, "GET", u.String(), nil)
			if e != nil {
				return nil, e
			}
			source, e := ValidSRT(original)
			if e != nil {
				return nil, e
			}
			translated := wanted != w.cfg.Target
			out := source
			if translated {
				if Load() != w.cfg || !s.Allowed(ctx, w.req) {
					return nil, errors.New("Settings changed")
				}
				payload, _ := json.Marshal(map[string]any{"content": source, "configStr": token, "sourceLanguage": w.cfg.Source, "targetLanguage": w.cfg.Target, "options": map[string]any{"translationWorkflow": "original"}})
				b, e := s.request(ctx, "POST", base.String()+"/api/translate-file", payload)
				if e != nil {
					return nil, e
				}
				out, e = ValidSRT(b)
				if e != nil {
					return nil, e
				}
				if !SameTiming(source, out) {
					return nil, errors.New("translated subtitle timing or cue count changed")
				}
			}
			return &Proof{ID: w.id, Language: w.cfg.Target, Release: w.rel.Name, Translated: translated, SRT: out}, nil
		}
	}
	return nil, errors.New("No subtitle matched this exact release; manual validation required")
}

var timing = regexp.MustCompile(`(?m)^\d{2}:\d{2}:\d{2}[,.]\d{3} --> \d{2}:\d{2}:\d{2}[,.]\d{3}`)

func ValidSRT(b []byte) (string, error) {
	if len(b) == 0 || len(b) > 512<<10 || bytes.Contains(b, []byte("[TRANSLATION_ERROR]")) {
		return "", errors.New("invalid subtitle or translation failure")
	}
	srt, _, err := subtitles.NormalizeLang(b, "en")
	if err != nil || len(timing.FindAllString(srt, -1)) < 2 {
		return "", errors.New("not a complete subtitle")
	}
	return srt, nil
}
func SameTiming(a, b string) bool {
	x, y := timing.FindAllString(a, -1), timing.FindAllString(b, -1)
	return len(x) > 0 && strings.Join(x, "\n") == strings.Join(y, "\n")
}
func (s *Service) Cached(req core.SearchRequest, rel core.Release) *Proof {
	c := Load()
	if !c.Enabled || !s.Allowed(context.Background(), req) {
		return nil
	}
	p := &Proof{ID: fingerprint(c, req, rel), Language: c.Target, Release: rel.Name}
	if s.DB.QueryRow("SELECT srt,translated FROM remix_submaker_jobs WHERE id=? AND state='ready'", p.ID).Scan(&p.SRT, &p.Translated) != nil {
		return nil
	}
	return p
}
