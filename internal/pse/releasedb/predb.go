package releasedb

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/safefetch"
	"github.com/braedonsaunders/ether/internal/store"
)

// Index des « pre » (annonces de releases de la scène) : option désactivée
// par défaut. Une pre ne contient ni lien ni empreinte ; Remix part du
// principe qu'une release française annoncée est disponible, sous une
// heure, sur les sources habituelles (debrid, BitTorrent, Usenet). Elle est
// donc comptée comme release indexée, et son média est ajouté au catalogue
// sans attendre qu'il y arrive par TMDB. La lecture passe toujours par les
// sources : une pre ne sert jamais de lien.

const (
	// PreDBSourceID identifie les pre dans pse_releases.
	PreDBSourceID = "remix-pre"
	preDBName     = "Pre (annonces de releases)"

	preDBDefaultEvery = 10 * time.Minute
	preDBMinEvery     = 2 * time.Minute
	preDBDefaultDelay = time.Hour
	preDBStartDelay   = 45 * time.Second
	preDBMaxBody      = 8 << 20
	preDBResolveMax   = 40                  // titres résolus au plus par passage
	preDBMaxTries     = 6                   // essais avant d'abandonner une pre sans média
	preDBKeep         = 30 * 24 * time.Hour // mémoire des pre traitées
	preDBMarker       = "direct"            // même repère que les flux directs : « disponible sans attente »
)

// Flux publics lus quand l'option est active.
var preDBPublicFeeds = []string{
	"https://api.predb.net/feed/",
	"https://predb.me/?cats=movies&rss=1",
	"https://predb.me/?cats=tv&rss=1",
	"https://predb.me/?cats=anime&rss=1",
}

func init() {
	store.Register("pse_releases", 12, `
CREATE TABLE pse_pre(
  name TEXT PRIMARY KEY,
  first_seen INTEGER NOT NULL,
  pre_at INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL DEFAULT 'pending',
  tries INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX pse_pre_state ON pse_pre(state, first_seen);
`)
}

// PreDBSettings règle l'import des pre (variables PSE_PREDB*).
type PreDBSettings struct {
	Enabled bool
	// Public : flux publics (désactivable pour n'utiliser que les flux privés).
	Public bool
	// PrivateURLs : flux RSS ou points d'API personnels, propres à l'utilisateur.
	PrivateURLs []string
	// PrivateKey : clé d'API envoyée en en-tête, jamais dans l'adresse.
	PrivateKey string
	Langs      []string // fr, multi, vostfr
	Delay      time.Duration
	Every      time.Duration
}

// LoadPreDBSettings lit la configuration effective.
func LoadPreDBSettings() PreDBSettings {
	s := PreDBSettings{
		Enabled:    isOn("PSE_PREDB"),
		Public:     !strings.EqualFold(strings.TrimSpace(pseenv.Get("PSE_PREDB_PUBLIC")), "off"),
		PrivateKey: strings.TrimSpace(pseenv.Get("PSE_PREDB_PRIVATE_KEY")),
		Delay:      preDBDefaultDelay,
		Every:      preDBDefaultEvery,
	}
	for _, u := range strings.Fields(pseenv.Get("PSE_PREDB_PRIVATE_URL")) {
		if p, err := url.Parse(u); err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Hostname() != "" && p.User == nil {
			s.PrivateURLs = append(s.PrivateURLs, p.String())
		}
	}
	langs := strings.Split(strings.ToLower(pseenv.Get("PSE_PREDB_LANGS")), ",")
	for _, l := range langs {
		switch l = strings.TrimSpace(l); l {
		case "fr", "multi", "vostfr":
			s.Langs = append(s.Langs, l)
		}
	}
	if len(s.Langs) == 0 {
		s.Langs = []string{"fr", "multi", "vostfr"}
	}
	if d, err := time.ParseDuration(strings.TrimSpace(pseenv.Get("PSE_PREDB_DELAY"))); err == nil && d >= 0 {
		s.Delay = min(d, 48*time.Hour)
	}
	if d, err := time.ParseDuration(strings.TrimSpace(pseenv.Get("PSE_PREDB_INTERVAL"))); err == nil {
		s.Every = d
	}
	s.Every = max(s.Every, preDBMinEvery)
	return s
}

// Active indique qu'au moins un flux est lu.
func (s PreDBSettings) Active() bool {
	return s.Enabled && (s.Public || len(s.PrivateURLs) > 0)
}

// ---- filtrage ----

var (
	preFrench = regexp.MustCompile(`(?i)(^|[^a-z0-9])(french|truefrench|vff|vfq|vfi|vf2|vf|vof)([^a-z0-9]|$)`)
	preMulti  = regexp.MustCompile(`(?i)(^|[^a-z0-9])multi[a-z]*([^a-z0-9]|$)`)
	preVOSTFR = regexp.MustCompile(`(?i)(^|[^a-z0-9])(vostfr|subfrench|subfr)([^a-z0-9]|$)`)
	// Une release « autre langue + MULTi » n'est pas retenue.
	preForeignMulti = regexp.MustCompile(`(?i)(^|[^a-z0-9])(spanish|italian|german|dutch|polish|russian|swedish|norwegian|danish|finnish|portuguese|czech|hungarian|turkish|greek|korean|japanese|chinese|hindi)[._ -]+multi`)
	preFix          = regexp.MustCompile(`(?i)(^|[^a-z0-9])(dir|nfo|sample|proof|sync|sub|read ?nfo)?fix([^a-z0-9]|$)`)
	preXXX          = regexp.MustCompile(`(?i)(^|[^a-z0-9])xxx([^a-z0-9]|$)`)
	preBadSection   = regexp.MustCompile(`(?i)(mp3|flac|music|mvid|game|apps?|0day|xxx|ebook|audiobook|dox|iso|ps[0-9p]|nsw|xbox|doc|pda|nds)`)
)

// preLangOK applique le choix de langues et écarte les corrections.
func preLangOK(name string, langs []string) bool {
	if preFix.MatchString(name) || preXXX.MatchString(name) || preForeignMulti.MatchString(name) {
		return false
	}
	for _, l := range langs {
		switch l {
		case "fr":
			if preFrench.MatchString(name) {
				return true
			}
		case "multi":
			if preMulti.MatchString(name) {
				return true
			}
		case "vostfr":
			if preVOSTFR.MatchString(name) {
				return true
			}
		}
	}
	return false
}

// PreAccept dit si une pre est gardée, et renvoie son analyse. Il faut un
// titre et une année (film) ou un épisode (série).
func PreAccept(name, section string, langs []string) (core.ReleaseInfo, bool) {
	if section != "" && preBadSection.MatchString(section) {
		return core.ReleaseInfo{}, false
	}
	if !preLangOK(name, langs) {
		return core.ReleaseInfo{}, false
	}
	info := releaseparse.Parse(name)
	if strings.TrimSpace(info.Title) == "" || (info.Year == 0 && len(info.Seasons) == 0 && len(info.Episodes) == 0) {
		return core.ReleaseInfo{}, false
	}
	return info, true
}

// ---- lecture des flux ----

// PreItem est une annonce de release.
type PreItem struct {
	Name    string
	Section string
	At      time.Time // zéro si le flux n'indique pas d'heure
}

type preRSS struct {
	Items []struct {
		Title   string `xml:"title"`
		Desc    string `xml:"description"`
		PubDate string `xml:"pubDate"`
	} `xml:"channel>item"`
}

var preSection = regexp.MustCompile(`(?i)Section:\s*([^|]+)`)

// ParsePreFeed lit un flux RSS ou une réponse JSON d'API. Les formats JSON
// sont lus de façon tolérante : liste à la racine ou sous results, items,
// data ou releases ; champs name/release/title, section/cat/category,
// date/pretime/pre_at/created_at.
func ParsePreFeed(body []byte) ([]PreItem, error) {
	b := strings.TrimSpace(string(body))
	switch {
	case strings.HasPrefix(b, "<"):
		var f preRSS
		if err := xml.Unmarshal([]byte(b), &f); err != nil {
			return nil, errors.New("flux illisible")
		}
		out := make([]PreItem, 0, len(f.Items))
		for _, it := range f.Items {
			p := PreItem{Name: strings.TrimSpace(it.Title)}
			if m := preSection.FindStringSubmatch(it.Desc); m != nil {
				p.Section = strings.TrimSpace(m[1])
			}
			if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(it.PubDate)); err == nil {
				p.At = t
			} else if t, err := time.Parse(time.RFC1123, strings.TrimSpace(it.PubDate)); err == nil {
				p.At = t
			}
			if p.Name != "" {
				out = append(out, p)
			}
		}
		return out, nil
	case strings.HasPrefix(b, "[") || strings.HasPrefix(b, "{"):
		var raw any
		if err := json.Unmarshal([]byte(b), &raw); err != nil {
			return nil, errors.New("réponse illisible")
		}
		list, _ := raw.([]any)
		if obj, ok := raw.(map[string]any); ok {
			for _, k := range []string{"results", "items", "data", "releases"} {
				if l, ok := obj[k].([]any); ok {
					list = l
					break
				}
			}
		}
		out := make([]PreItem, 0, len(list))
		for _, e := range list {
			m, _ := e.(map[string]any)
			p := PreItem{Name: preStr(m, "name", "release", "title"), Section: preStr(m, "section", "cat", "category")}
			if d := preStr(m, "date", "pretime", "pre_at", "created_at"); d != "" {
				for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
					if t, err := time.Parse(layout, d); err == nil {
						p.At = t.UTC()
						break
					}
				}
			}
			if p.Name != "" {
				out = append(out, p)
			}
		}
		return out, nil
	}
	return nil, errors.New("format de flux inconnu")
}

// preStr lit un champ texte, ou le nom d'un objet (catégorie).
func preStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		case map[string]any:
			if s, ok := v["name"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

var preClient = (&safefetch.Guard{}).Client(&http.Client{Timeout: 30 * time.Second})

// fetchPre lit un flux. La clé personnelle n'est envoyée qu'en en-tête, et
// seulement aux adresses privées.
func fetchPre(ctx context.Context, u, key string) ([]PreItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, application/json;q=0.9, */*;q=0.5")
	req.Header.Set("User-Agent", "Ether-Remix")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	// La clé personnelle ne suit jamais une redirection vers un autre hôte.
	c := *preClient
	c.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || (key != "" && next.URL.Host != via[0].URL.Host) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, errors.New("flux inaccessible")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("flux refusé (%d)", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, preDBMaxBody))
	if err != nil {
		return nil, errors.New("flux interrompu")
	}
	return ParsePreFeed(body)
}

// ---- état ----

// PreDBStatus est affiché dans la console.
type PreDBStatus struct {
	State   string `json:"state"` // running, ok, error
	Last    int64  `json:"last"`
	Added   int    `json:"added"`   // releases enregistrées depuis le début
	Pending int    `json:"pending"` // en attente du délai ou d'un média
	Error   string `json:"error,omitempty"`
}

func (s *Store) preStatus(ctx context.Context) PreDBStatus {
	var st PreDBStatus
	if v, err := s.sfrGet(ctx, "pre_status"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &st)
	}
	return st
}

// LoadPreDBStatus lit le dernier état de l'import des pre.
func (s *Store) LoadPreDBStatus(ctx context.Context) PreDBStatus { return s.preStatus(ctx) }

func (s *Store) preSetStatus(ctx context.Context, st PreDBStatus) {
	b, _ := json.Marshal(st)
	_ = s.sfrSet(ctx, "pre_status", string(b))
}

// preNote enregistre les pre vues ; une pre déjà connue garde son état.
func (s *Store) preNote(ctx context.Context, items []PreItem, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, it := range items {
		var at int64
		if !it.At.IsZero() {
			at = it.At.Unix()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pse_pre(name,first_seen,pre_at) VALUES(?,?,?) ON CONFLICT(name) DO NOTHING`, it.Name, now.Unix(), at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type preRow struct {
	Name  string
	Tries int
}

// preDue liste les pre dont le délai est écoulé. Le délai court depuis
// l'heure indiquée par le flux, sinon depuis la première observation.
func (s *Store) preDue(ctx context.Context, cutoff time.Time, limit int) ([]preRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,tries FROM pse_pre WHERE state='pending'
AND (CASE WHEN pre_at>0 AND pre_at<first_seen THEN pre_at ELSE first_seen END)<=? ORDER BY first_seen LIMIT ?`, cutoff.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []preRow
	for rows.Next() {
		var r preRow
		if err := rows.Scan(&r.Name, &r.Tries); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) preSet(ctx context.Context, name, state string, tries int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pse_pre SET state=?,tries=? WHERE name=?`, state, tries, name)
	return err
}

func (s *Store) prePending(ctx context.Context) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_pre WHERE state='pending'`).Scan(&n)
	return n
}

func (s *Store) preAdded(ctx context.Context) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_releases WHERE source_id=?`, PreDBSourceID).Scan(&n)
	return n
}

// preForget efface les pre et leurs releases (option désactivée ou changée).
func (s *Service) preForget(ctx context.Context) error {
	rows, err := s.st.db.QueryContext(ctx, `SELECT DISTINCT title_key FROM pse_releases WHERE source_id=?`, PreDBSourceID)
	if err != nil {
		return err
	}
	var keys []core.RatingKey
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, core.RatingKey(k))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.st.db.ExecContext(ctx, `DELETE FROM pse_releases WHERE source_id=?`, PreDBSourceID); err != nil {
		return err
	}
	if _, err := s.st.db.ExecContext(ctx, `DELETE FROM pse_pre`); err != nil {
		return err
	}
	if p := Playable(); p.Enabled {
		for _, k := range keys {
			if err := s.st.RefreshPlayable(ctx, k, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- cycle ----

// preSave transforme une pre résolue en release indexée.
func (s *Service) preSave(ctx context.Context, name string, info core.ReleaseInfo, t core.Title, now time.Time) error {
	season, episode := 0, 0
	if t.Type == core.TypeShow {
		if len(info.Seasons) == 1 {
			season = info.Seasons[0]
		}
		if len(info.Episodes) == 1 && season > 0 {
			episode = info.Episodes[0]
		}
	}
	rel := core.Release{ID: "pre:" + strings.ToLower(name), Name: name, Indexer: preDBName, Protocol: core.ProtocolTorrent, Info: info}
	saved := FromCore(t, season, episode, PreDBSourceID, rel, []string{preDBMarker})
	if err := s.st.Save(ctx, []Release{saved}, now); err != nil {
		return err
	}
	if p := Playable(); p.Enabled {
		return s.st.RefreshPlayable(ctx, t.Key, p)
	}
	return nil
}

func preSignature(c PreDBSettings) string {
	return strings.Join(c.Langs, ",") + "|" + fmt.Sprint(c.Public) + "|" + fmt.Sprint(len(c.PrivateURLs))
}

// preCycle lit les flux puis traite les pre dont le délai est écoulé.
func (s *Service) preCycle(ctx context.Context, cfg PreDBSettings) error {
	if sig, _ := s.st.sfrGet(ctx, "pre_sig"); sig != preSignature(cfg) {
		if sig != "" {
			if err := s.preForget(ctx); err != nil {
				return err
			}
		}
		if err := s.st.sfrSet(ctx, "pre_sig", preSignature(cfg)); err != nil {
			return err
		}
	}
	now := s.d.Now()
	s.st.preSetStatus(ctx, PreDBStatus{State: "running", Last: now.Unix(), Added: s.st.preAdded(ctx), Pending: s.st.prePending(ctx)})
	type feed struct {
		url, key string
	}
	var feeds []feed
	if cfg.Public {
		for _, u := range preDBPublicFeeds {
			feeds = append(feeds, feed{u, ""})
		}
	}
	for _, u := range cfg.PrivateURLs {
		feeds = append(feeds, feed{u, cfg.PrivateKey})
	}
	okFeeds, lastErr := 0, error(nil)
	var keep []PreItem
	for _, f := range feeds {
		items, err := fetchPre(ctx, f.url, f.key)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		okFeeds++
		for _, it := range items {
			if _, ok := PreAccept(it.Name, it.Section, cfg.Langs); ok {
				keep = append(keep, it)
			}
		}
	}
	if okFeeds == 0 && lastErr != nil {
		return lastErr
	}
	if err := s.st.preNote(ctx, keep, now); err != nil {
		return err
	}
	due, err := s.st.preDue(ctx, now.Add(-cfg.Delay), preDBResolveMax)
	if err != nil {
		return err
	}
	for _, r := range due {
		if err := s.sfrIdle(ctx); err != nil {
			return err
		}
		info, ok := PreAccept(r.Name, "", cfg.Langs)
		if !ok || s.d.Resolve == nil {
			_ = s.st.preSet(ctx, r.Name, "skipped", r.Tries)
			continue
		}
		t, found := s.d.Resolve(ctx, info)
		if !found {
			if r.Tries+1 >= preDBMaxTries {
				_ = s.st.preSet(ctx, r.Name, "unmatched", r.Tries+1)
			} else {
				_ = s.st.preSet(ctx, r.Name, "pending", r.Tries+1)
			}
			continue
		}
		if err := s.preSave(ctx, r.Name, info, t, now); err != nil {
			return err
		}
		_ = s.st.preSet(ctx, r.Name, "done", r.Tries)
	}
	_, _ = s.st.db.ExecContext(ctx, `DELETE FROM pse_pre WHERE state<>'pending' AND first_seen<?`, now.Add(-preDBKeep).Unix())
	st := PreDBStatus{State: "ok", Last: now.Unix(), Added: s.st.preAdded(ctx), Pending: s.st.prePending(ctx)}
	if okFeeds < len(feeds) {
		st.Error = "un flux est inaccessible"
	}
	s.st.preSetStatus(ctx, st)
	return nil
}

func (s *Service) runPreDB(ctx context.Context, cfg PreDBSettings) {
	for wait := preDBStartDelay; sleepCtx(ctx, wait); wait = cfg.Every {
		if err := s.preCycle(ctx, cfg); err != nil && ctx.Err() == nil {
			s.d.Logger.Warn("pse pre index failed", "err", err)
			s.st.preSetStatus(ctx, PreDBStatus{State: "error", Last: s.d.Now().Unix(), Error: err.Error()})
		}
	}
}
