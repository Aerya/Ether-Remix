// Package seerrcatalog reuses explicit, opt-in SeerrCatalog observations.
// Stream credentials live only in memory, never in catalogue tables or responses.
package seerrcatalog

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stremio"
)

const Schema = "seerrcatalog.availability.v1"
const EventsPath = "/api/integrations/seerrcatalog/v1/events"

func init() {
	store.Register("pse_seerrcatalog", 1, `CREATE TABLE pse_seerrcatalog (
 media_id INTEGER PRIMARY KEY, title_key INTEGER NOT NULL, available INTEGER NOT NULL,
 expires INTEGER NOT NULL, signature TEXT NOT NULL);
 CREATE INDEX pse_seerrcatalog_title ON pse_seerrcatalog(title_key,expires);`)
	store.Register("pse_seerrcatalog", 2, `ALTER TABLE pse_seerrcatalog ADD COLUMN confirmed INTEGER NOT NULL DEFAULT 0;`)
}

type Settings struct {
	Enabled    bool
	URL, Token string
}

func Load() Settings {
	v := strings.ToLower(pseenv.Get("PSE_SEERRCATALOG"))
	return Settings{Enabled: v == "on" || v == "true" || v == "1", URL: strings.TrimRight(pseenv.Get("PSE_SEERRCATALOG_URL"), "/"), Token: pseenv.Get("PSE_SEERRCATALOG_TOKEN")}
}
func (c Settings) signature() string {
	h := sha256.Sum256([]byte(c.URL + "\x00" + c.Token))
	return hex.EncodeToString(h[:])
}
func (c Settings) valid() bool {
	u, e := url.Parse(c.URL)
	return c.Enabled && e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && len(c.Token) >= 32
}

// TrustedSQL is independent of stream lifetimes and uses the source's own
// availability rules. The settings fingerprint immediately revokes old data.
func TrustedSQL(key string) (string, []any) {
	c := Load()
	if !c.valid() {
		return "0", nil
	}
	return key + " IN (SELECT title_key FROM pse_seerrcatalog WHERE available=1 AND (confirmed=1 OR expires>unixepoch()) AND signature=?)", []any{c.signature()}
}
func Trusted(ctx context.Context, db *sql.DB, key core.RatingKey) bool {
	q, args := TrustedSQL("?")
	args = append([]any{int64(key)}, args...)
	if q == "0" {
		return false
	}
	var yes bool
	return db.QueryRowContext(ctx, "SELECT "+q, args...).Scan(&yes) == nil && yes
}

type Media struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	IMDB  string `json:"imdbId"`
	TMDB  int64  `json:"tmdbId"`
	Title string `json:"title"`
	Year  int    `json:"year"`
}

func (m Media) key() core.RatingKey {
	if m.Type == "movie" {
		return core.MovieKey(m.TMDB)
	}
	return core.ShowKey(m.TMDB)
}

type Release struct {
	AddonID     string         `json:"addonId"`
	AddonName   string         `json:"addonName"`
	AddonOrigin string         `json:"addonOrigin"`
	StreamID    string         `json:"streamId"`
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Stream      stremio.Stream `json:"stream"`
}
type Snapshot struct {
	Confirmed      bool      `json:"availabilityConfirmed"`
	Schema         string    `json:"schema"`
	Media          Media     `json:"media"`
	Available      bool      `json:"available"`
	Status         string    `json:"checkStatus"`
	Checked        time.Time `json:"checkedAt"`
	Expires        time.Time `json:"expiresAt"`
	ReleasesExpire time.Time `json:"releasesExpireAt"`
	Releases       []Release `json:"releases"`
}
type page struct {
	Schema   string     `json:"schema"`
	Revision *int64     `json:"revision"`
	Items    []Snapshot `json:"items"`
	Next     *int64     `json:"nextCursor"`
}
type Status struct {
	LastSync *time.Time `json:"lastSync,omitempty"`
	Items    int        `json:"items"`
	Error    string     `json:"error,omitempty"`
}
type Service struct {
	DB   *sql.DB
	HTTP *http.Client
	// Ensure imports metadata, never performs a stream search.
	Ensure    func(context.Context, core.RatingKey) error
	Changed   func()
	mu        sync.RWMutex
	snapshots []Snapshot
	signature string
	status    Status
	wake      chan struct{}
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func New(db *sql.DB, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	cp := *client
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
	return &Service{DB: db, HTTP: &cp, wake: make(chan struct{}, 1)}
}
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			cctx, ccancel := context.WithTimeout(ctx, 50*time.Second)
			err := s.Sync(cctx)
			ccancel()
			if err != nil {
				s.mu.Lock()
				s.status.Error = "Synchronisation impossible ; nouvel essai dans une minute"
				s.mu.Unlock()
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-s.wake:
			}
		}
	}()
}
func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Status() Status { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }
func (s *Service) clear(ctx context.Context) error {
	result, e := s.DB.ExecContext(ctx, "DELETE FROM pse_seerrcatalog")
	if e != nil {
		return e
	}
	removed, _ := result.RowsAffected()
	s.mu.Lock()
	s.snapshots = nil
	s.signature = ""
	s.status = Status{}
	s.mu.Unlock()
	if removed > 0 && s.Changed != nil {
		s.Changed()
	}
	return nil
}

func (s *Service) Sync(ctx context.Context) error {
	c := Load()
	if !c.valid() {
		return s.clear(ctx)
	}
	after := int64(0)
	var revision *int64
	var all []Snapshot
	for {
		u := c.URL + "/api/integrations/remix/v1/availability?limit=100&after=" + strconv.FormatInt(after, 10)
		if revision != nil {
			u += "&revision=" + strconv.FormatInt(*revision, 10)
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if e != nil {
			return errors.New("invalid endpoint")
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, e := s.HTTP.Do(req)
		if e != nil {
			return errors.New("feed unavailable")
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				_ = s.clear(ctx)
			}
			return fmt.Errorf("feed status %d", resp.StatusCode)
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		resp.Body.Close()
		if e != nil || len(raw) > 4<<20 {
			return errors.New("feed too large")
		}
		var p page
		if json.Unmarshal(raw, &p) != nil || p.Schema != Schema || p.Revision == nil {
			return errors.New("invalid feed")
		}
		if revision != nil && *p.Revision != *revision {
			return errors.New("snapshot changed")
		}
		revision = p.Revision
		for _, v := range p.Items {
			if v.Schema != Schema || v.Media.ID <= 0 || v.Media.TMDB <= 0 || v.Media.TMDB >= 1e9 || (v.Media.Type != "movie" && v.Media.Type != "series") || v.Checked.IsZero() || v.Expires.After(v.Checked.Add(26*time.Hour)) || v.Checked.After(time.Now().Add(5*time.Minute)) || !v.Expires.After(v.Checked) || (v.Status != "available" && v.Status != "unavailable") || v.Available != (v.Status == "available") || len(v.Releases) > 50 || (v.Confirmed && !v.Available) {
				return errors.New("invalid observation")
			}
			if !v.Confirmed && !v.Expires.After(time.Now()) {
				continue
			}
			all = append(all, v)
		}
		if len(all) > 100000 {
			return errors.New("too many observations")
		}
		if p.Next == nil {
			break
		}
		if *p.Next <= after {
			return errors.New("invalid cursor")
		}
		after = *p.Next
	}
	// Failure on any page leaves the previous complete snapshot intact.
	if Load() != c {
		return errors.New("settings changed")
	}
	if s.Ensure != nil {
		seen := map[core.RatingKey]bool{}
		for _, v := range all {
			k := v.Media.key()
			if !seen[k] {
				if err := s.Ensure(ctx, k); err != nil {
					return errors.New("metadata unavailable")
				}
				seen[k] = true
			}
		}
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "DELETE FROM pse_seerrcatalog"); e != nil {
		return e
	}
	for _, v := range all {
		if _, e = tx.ExecContext(ctx, "INSERT INTO pse_seerrcatalog(media_id,title_key,available,expires,signature,confirmed) VALUES(?,?,?,?,?,?)", v.Media.ID, int64(v.Media.key()), v.Available, v.Expires.Unix(), c.signature(), v.Confirmed); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.mu.Lock()
	s.snapshots = all
	s.signature = c.signature()
	synced := time.Now()
	s.status = Status{LastSync: &synced, Items: len(all)}
	s.mu.Unlock()
	if s.Changed != nil {
		s.Changed()
	}
	return nil
}

// Lookup returns reusable candidates only for the exact checked movie/episode.
// A trusted availability without a reusable stream suppresses background checks,
// while playback falls back to the ordinary search. Rescue always bypasses it.
func (s *Service) Lookup(ctx context.Context, req core.SearchRequest) ([]core.Release, bool) {
	c := Load()
	if !c.valid() || core.IndexerPurpose(ctx) == core.IndexerPurposeRescue {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c.signature() != s.signature {
		return nil, false
	}
	now := time.Now()
	var out []core.Release
	trusted := false
	for _, v := range s.snapshots {
		if !v.Available || (!v.Confirmed && !v.Expires.After(now)) || (v.Media.Type == "movie") != (req.Type == core.TypeMovie) {
			continue
		}
		if req.IDs.TMDB != 0 {
			if req.IDs.TMDB != v.Media.TMDB {
				continue
			}
		} else if req.IDs.IMDB == "" || req.IDs.IMDB != v.Media.IMDB {
			continue
		}
		if req.Type == core.TypeMovie {
			trusted = true
		}
		for _, r := range v.Releases {
			want := v.Media.IMDB
			if req.Type != core.TypeMovie {
				if req.Season < 0 || req.Episode <= 0 {
					continue
				}
				want += fmt.Sprintf(":%d:%d", req.Season, req.Episode)
			}
			if want == "" || r.StreamID != want {
				continue
			}
			trusted = true
			st := r.Stream
			// Clamp lifetime even if a sender declares an overly generous deadline.
			if !v.ReleasesExpire.After(now) || !v.Checked.Add(30*time.Minute).After(now) {
				st.URL = ""
				st.NZBURL = ""
				st.Sources = nil
				st.BehaviorHints = stremio.StreamHints{}
				if st.InfoHash == "" {
					continue
				}
			}
			st.Name = r.Name
			st.Title = r.Title
			if st.BehaviorHints.Filename == "" {
				st.BehaviorHints.Filename = r.Name
			}
			rs := stremio.Convert([]stremio.Stream{st}, req, stremio.Lookup{}, stremio.ConvertOptions{Origin: r.AddonOrigin, Instance: "seerrcatalog:" + r.AddonID}).Releases
			for i := range rs {
				rs[i].Indexer = "SeerrCatalog · " + r.AddonName
				rs[i].IndexerID = "seerrcatalog"
				rs[i].TitleIDs = core.IDs{TMDB: v.Media.TMDB, IMDB: v.Media.IMDB}
			}
			out = append(out, rs...)
		}
	}
	purpose := core.IndexerPurpose(ctx)
	return out, len(out) > 0 || (trusted && (purpose == core.IndexerPurposeAvailability || purpose == core.IndexerPurposeWarmup || purpose == core.IndexerPurposeDetail))
}

// Events are wake-up hints only. The authoritative full feed handles ordering,
// duplicates, missed deletions and updates; the supplied payload is never trusted.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := Load()
	a := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	b := sha256.Sum256([]byte("Bearer " + c.Token))
	if !c.valid() || subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var event struct {
		Schema string `json:"schema"`
		Event  string `json:"event"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&event) != nil || event.Schema != Schema || (event.Event != "availability.updated" && event.Event != "media.removed") {
		http.Error(w, "Invalid event", 400)
		return
	}
	s.Wake()
	w.WriteHeader(http.StatusAccepted)
}
