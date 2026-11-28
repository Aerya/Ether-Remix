package alko

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/store"
)

var testKey = strings.Repeat("k", 32) // factice, jamais une vraie clé

// fakeSFR imite l'API de lecture d'SFR (contrat : docs/guides/alko-lecture-tiers.md).
type fakeSFR struct {
	mu      sync.Mutex
	srv     *httptest.Server
	files   map[string]Element
	log     []Modification
	epoch   int64
	purge   int64
	journal bool
	seq     int64 // last_seq
	jseq    int64 // journal_seq
	pageMax int
	status  map[string]int // chemin -> code forcé
	calls   map[string]int
	asked   [][]string // relectures par identifiant
	// afterCatalogue s'exécute après chaque page du catalogue (verrou tenu).
	afterCatalogue func()
	// failCatalogueFrom : les pages du catalogue à partir de cet appel répondent 500.
	failCatalogueFrom int
	// modsJournalInvalid : le cliché des modifications annonce un journal en retard.
	modsJournalInvalid bool
}

func newFakeSFR(t *testing.T) *fakeSFR {
	f := &fakeSFR{files: map[string]Element{}, epoch: 1, journal: true, pageMax: 2, status: map[string]int{}, calls: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSFR) state() State {
	return State{Epoch: f.epoch, PurgeSeq: f.purge, JournalSeq: f.jseq, LastSeq: f.seq, Journal: f.journal}
}

func (f *fakeSFR) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.URL.Path]++
	if r.Header.Get("alko-key") != testKey {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	if code := f.status[r.URL.Path]; code != 0 {
		http.Error(w, "forced", code)
		return
	}
	out := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	limit := func() int {
		n, _ := strconv.Atoi(r.URL.Query().Get("limite"))
		if n <= 0 || n > f.pageMax {
			n = f.pageMax
		}
		return n
	}
	switch r.URL.Path {
	case "/api/alko/v1/etat":
		out(f.state())
	case "/api/alko/v1/catalogue":
		if f.failCatalogueFrom > 0 && f.calls[r.URL.Path] >= f.failCatalogueFrom {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if f.afterCatalogue != nil {
			defer f.afterCatalogue()
		}
		after, n := r.URL.Query().Get("apres"), limit()
		ids := f.sortedIDs()
		var page []Element
		for _, id := range ids {
			if id > after && len(page) < n {
				page = append(page, f.files[id])
			}
		}
		var next *string
		if len(page) == n {
			last := page[len(page)-1].PlaybackID
			next = &last
		}
		out(CataloguePage{State: f.state(), Elements: nonNil(page), Next: next})
	case "/api/alko/v1/modifications":
		since, _ := strconv.ParseInt(r.URL.Query().Get("depuis"), 10, 64)
		if since < f.purge {
			http.Error(w, "gone", http.StatusGone)
			return
		}
		var page []Modification
		for _, m := range f.log {
			if m.Seq > since {
				page = append(page, m)
			}
		}
		var next *int64
		if n := limit(); len(page) >= n {
			page = page[:n]
			last := page[len(page)-1].Seq
			next = &last
		}
		if page == nil {
			page = []Modification{}
		}
		st := f.state()
		if f.modsJournalInvalid {
			st.JournalSeq = st.LastSeq - 1
		}
		out(ModificationsResult{State: st, Modifications: page, Next: next})
	case "/api/alko/v1/fichiers":
		var body struct {
			IDs []string `json:"playback_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.asked = append(f.asked, body.IDs)
		var got []Element
		for _, id := range body.IDs {
			if e, ok := f.files[id]; ok {
				got = append(got, e)
			}
		}
		out(filesResult{State: f.state(), Elements: nonNil(got)})
	default:
		http.NotFound(w, r)
	}
}

func nonNil(e []Element) []Element {
	if e == nil {
		return []Element{}
	}
	return e
}

func (f *fakeSFR) sortedIDs() []string {
	ids := make([]string, 0, len(f.files))
	for id := range f.files {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func testID(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

func movie(n int, tmdb int64, name string) Element {
	return Element{PlaybackID: testID(n), Kind: "movie", TMDB: tmdb, Filename: name, Filesize: 1 << 30, Fingerprint: "fp" + strconv.Itoa(n),
		SyncedAt: "2026-11-27T00:00:00Z", SourceURL: "https://alldebrid.com/f/token" + strconv.Itoa(n)}
}

func episode(n int, tmdb int64, season, ep int, name string) Element {
	e := movie(n, tmdb, name)
	e.Kind, e.Season, e.Episode = "episode", &season, &ep
	return e
}

// put ajoute ou remplace un fichier et l'inscrit au journal.
func (f *fakeSFR) put(e Element, op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[e.PlaybackID] = e
	f.record(e.PlaybackID, op, e.Kind, e.TMDB)
}

func (f *fakeSFR) del(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.files[id]
	delete(f.files, id)
	f.record(id, "D", e.Kind, e.TMDB)
}

func (f *fakeSFR) record(id, op, kind string, tmdb int64) {
	f.seq++
	if f.journal {
		f.jseq = f.seq
	}
	f.log = append(f.log, Modification{Seq: f.seq, PlaybackID: id, Operation: op, Kind: kind, TMDB: tmdb})
}

// seal : chiffrement de test, réversible mais qui ne laisse rien lisible.
func testSeal(s string) (string, error) {
	return "sealed:" + base64.StdEncoding.EncodeToString([]byte(s)), nil
}
func testOpen(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "sealed:"))
	return string(b), err
}

type env struct {
	t    *testing.T
	f    *fakeSFR
	db   *sql.DB
	svc  *Service
	logs *bytes.Buffer
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := store.OpenTest(t)
	if _, err := db.Exec(`CREATE TABLE catalog_titles(key INTEGER PRIMARY KEY,type TEXT,tmdb_id INTEGER,popularity REAL NOT NULL DEFAULT 0, imdb TEXT NOT NULL DEFAULT '',tvdb INTEGER NOT NULL DEFAULT 0,title TEXT NOT NULL DEFAULT 'Film',original_title TEXT NOT NULL DEFAULT '',year INTEGER NOT NULL DEFAULT 2020,language TEXT NOT NULL DEFAULT ''); CREATE TABLE catalog_alt_titles(key INTEGER,title TEXT)`); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, f: newFakeSFR(t), db: db, logs: &bytes.Buffer{}, now: time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)}
	zero := time.Duration(0)
	e.svc = &Service{
		Settings: Settings{Enabled: true, URL: e.f.srv.URL, Key: testKey, LangFR: true, Every: 10 * time.Minute},
		DB:       db, Log: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:  func() time.Time { return e.now },
		Seal: testSeal, Open: testOpen, Pause: &zero,
		Publisher: &releasedb.AlkoPublisher{DB: db, Now: func() time.Time { return e.now }},
	}
	return e
}

func (e *env) catalog(typ string, tmdb int64, title string) core.RatingKey {
	e.t.Helper()
	var key core.RatingKey
	if typ == "movie" {
		key = core.MovieKey(tmdb)
	} else {
		key = core.ShowKey(tmdb)
	}
	if _, err := e.db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id,title) VALUES(?,?,?,?)`, int64(key), typ, tmdb, title); err != nil {
		e.t.Fatal(err)
	}
	return key
}

func (e *env) sync() error { return e.svc.Sync(context.Background()) }

func (e *env) mustSync() {
	e.t.Helper()
	if err := e.sync(); err != nil {
		e.t.Fatalf("synchronisation : %v", err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) releases(key core.RatingKey) int {
	return e.count(`SELECT COUNT(*) FROM pse_releases WHERE source_id=? AND title_key=?`, releasedb.AlkoSourceID, int64(key))
}
