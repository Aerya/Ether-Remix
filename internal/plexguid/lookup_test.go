package plexguid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestLookupVerifiedIdentity(t *testing.T) {
	const movie = "plex://movie/5d7768265af944001f1f6976"
	const show = "plex://show/5d9c086c46115600200aa2fe"
	for _, tc := range []struct {
		name, guid, body string
		want             core.RatingKey
		wantErr          bool
	}{
		{"movie", movie, `{"guid":"` + movie + `","type":"movie","title":"Fight Club","year":1999,"Guid":[{"id":"tmdb://550"}]}`, core.MovieKey(550), false},
		{"show", show, `{"guid":"` + show + `","type":"show","title":"Boundary Bay","year":2008,"Guid":[{"id":"tmdb://1396"}]}`, core.ShowKey(1396), false},
		{"wrong guid", movie, `{"guid":"plex://movie/5d7768265af944001f1f6977","type":"movie","Guid":[{"id":"tmdb://550"}]}`, 0, true},
		{"wrong type", movie, `{"guid":"` + movie + `","type":"show","Guid":[{"id":"tmdb://550"}]}`, 0, true},
		{"missing external", movie, `{"guid":"` + movie + `","type":"movie"}`, 0, true},
		{"ambiguous", movie, `{"guid":"` + movie + `","type":"movie","Guid":[{"id":"tmdb://550"},{"id":"tmdb://551"}]}`, 0, true},
		{"bad external", movie, `{"guid":"` + movie + `","type":"movie","Guid":[{"id":"tmdb://-1"},{"id":"tmdb://9999999999999999999"},{"id":"tmdb://550/1"}]}`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := store.OpenMemory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			seedCatalogTitle(t, db, core.MovieKey(550), "movie", "Fight Club", 1999)
			seedCatalogTitle(t, db, core.ShowKey(1396), "show", "Boundary Bay", 2008)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/library/metadata/"+tc.guid[len(tc.guid)-24:] || r.URL.Query().Get("includeGuids") != "1" || r.Header.Get("X-Plex-Token") != "fixture" {
					t.Errorf("unexpected request %s", r.URL)
				}
				fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[%s]}}`, tc.body)
			}))
			defer upstream.Close()
			pg := New(db, Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }})
			defer pg.Close()
			k, err := pg.Lookup(ctx, tc.guid)
			if k != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("got %v %v want %v err=%v", k, err, tc.want, tc.wantErr)
			}
			if !tc.wantErr {
				pg.o.Token = nil
				again, e := pg.Lookup(ctx, tc.guid)
				if again != k || e != nil || calls.Load() != 1 || pg.Cached(ctx, k) != tc.guid {
					t.Fatalf("cache=%v err=%v calls=%d", again, e, calls.Load())
				}
			} else if got := pg.Cached(ctx, core.MovieKey(550)); got != "" {
				t.Fatal("unverified identity cached", got)
			}
		})
	}
}

func TestLookupBoundaries(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedCatalogTitle(t, db, core.MovieKey(550), "movie", "Fight Club", 1999)
	seedCatalogTitle(t, db, core.ShowKey(1396), "show", "Boundary Bay", 2008)
	const guid = "plex://movie/5d7768265af944001f1f6976"
	var calls atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "{}") }))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer upstream.Close()
	pg := New(db, Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }})
	defer pg.Close()
	for _, g := range []string{"", "plex://movie/../../secret", "https://localhost/private", "plex://episode/5d7768265af944001f1f6976"} {
		if _, e := pg.Lookup(ctx, g); !errors.Is(e, core.ErrNotFound) {
			t.Fatalf("invalid %q: %v", g, e)
		}
	}
	if _, e := pg.Lookup(ctx, guid); e == nil || calls.Load() != 0 {
		t.Fatalf("redirect followed: %v %d", e, calls.Load())
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := pg.Lookup(canceled, guid); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled lookup: %v", e)
	}
	pg.o.Token = nil
	if _, e := pg.Lookup(ctx, guid); !errors.Is(e, core.ErrNotFound) {
		t.Fatal(e)
	}
	if e := pg.storeMapping(ctx, core.MovieKey(550), identityRecord{GUID: guid, TMDB: 550, Title: "Fight Club", Year: 1999}, time.Now().Add(-2*cacheTTL).Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e := pg.Lookup(ctx, guid); !errors.Is(e, core.ErrNotFound) {
		t.Fatalf("expired cache accepted: %v", e)
	}
	if e := pg.storeMapping(ctx, core.MovieKey(550), identityRecord{GUID: guid, TMDB: 550, Title: "Fight Club", Year: 1999}, time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	if e := pg.storeMapping(ctx, core.MovieKey(551), identityRecord{GUID: guid, TMDB: 551, Title: "Fight Club", Year: 1999}, time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e := pg.Lookup(ctx, guid); e == nil {
		t.Fatal("ambiguous cache accepted")
	}
}

func TestLookupMissingProviderIdentity(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedCatalogTitle(t, db, core.MovieKey(550), "movie", "Fight Club", 1999)
	seedCatalogTitle(t, db, core.ShowKey(1396), "show", "Boundary Bay", 2008)
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	pg := New(db, Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }})
	defer pg.Close()
	if k, err := pg.Lookup(ctx, "plex://movie/5d7768265af944001f1f6976"); k != 0 || !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown identity=%v %v", k, err)
	}
}

func TestExactKeyDoesNotDeriveEpisodeFromShow(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedCatalogTitle(t, db, core.MovieKey(550), "movie", "Fight Club", 1999)
	seedCatalogTitle(t, db, core.ShowKey(1396), "show", "Boundary Bay", 2008)
	pg := New(db, Options{Token: func() string { return "" }})
	defer pg.Close()
	const showGUID = "plex://show/5d9c086c46115600200aa2fe"
	const epGUID = "plex://episode/602e5e2294f08e002d003344"
	show := core.ShowKey(1396)
	ep := core.EpisodeKey(1396, 1, 1)
	if err := pg.storeMapping(ctx, show, identityRecord{GUID: showGUID, TMDB: 1396}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if k, err := pg.ExactKey(ctx, epGUID); !errors.Is(err, core.ErrNotFound) || k != 0 {
		t.Fatalf("episode inferred from show: %v %v", k, err)
	}
	if k, err := pg.ExactKey(ctx, showGUID); err != nil || k != show {
		t.Fatalf("show %v %v", k, err)
	}
	if err := pg.storeMapping(ctx, ep, identityRecord{GUID: epGUID, TMDB: 1396}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if k, err := pg.ExactKey(ctx, epGUID); err != nil || k != ep {
		t.Fatalf("episode %v %v", k, err)
	}
	other := core.EpisodeKey(1396, 1, 2)
	if err := pg.storeMapping(ctx, other, identityRecord{GUID: epGUID, TMDB: 1396}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.ExactKey(ctx, epGUID); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous %v", err)
	}
}

func TestLookupWarmsMissingCatalogTitle(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const guid = "plex://movie/5d7768265af944001f1f6976"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Fight Club","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`, guid)
	}))
	defer upstream.Close()

	pg := New(db, Options{
		BaseURL: upstream.URL,
		Token:   func() string { return "fixture" },
		EnsureTitle: func(ctx context.Context, k core.RatingKey) error {
			if k != core.MovieKey(550) {
				return core.ErrNotFound
			}
			seedCatalogTitle(t, db, k, "movie", "Fight Club", 1999)
			return nil
		},
	})
	defer pg.Close()

	k, err := pg.Lookup(ctx, guid)
	if err != nil || k != core.MovieKey(550) {
		t.Fatalf("Lookup cold title = %v, %v; want %v", k, err, core.MovieKey(550))
	}
}

// Issue #654: upgrading across rule-2 (v0.3.10) leaves pre-existing mappings
// with credits_rule=0 or no plexguid_identity row. Lookup must verify them live
// against the canonical record instead of failing with ErrNotFound, upgrading
// valid rows to credits_rule=2 and dropping wrong legacy namesakes.
func TestLookupUpgradesPreRule2Mappings(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seedCatalogTitle(t, db, core.MovieKey(550), "movie", "Fight Club", 1999)
	seedCatalogTitle(t, db, core.MovieKey(603), "movie", "The Matrix", 1999)
	seedCatalogTitle(t, db, core.MovieKey(770), "movie", "Runner", 2026)

	const (
		staleGUID  = "plex://movie/5d7768265af944001f1f6976"
		legacyGUID = "plex://movie/5d7768265af944001f1f6603"
		wrongGUID  = "plex://movie/5d7768265af944001f1f6962"
	)
	now := time.Now().Unix()
	// Pre-v0.3.10 credits_rule=0 mapping (post-migration-8 state).
	if _, err = db.Exec(`INSERT INTO plexguid_mappings(rating_key,guid,checked_at) VALUES(?,?,?)`, int64(core.MovieKey(550)), staleGUID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO plexguid_verified(rating_key,guid) VALUES(?,?)`, int64(core.MovieKey(550)), staleGUID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO plexguid_identity(rating_key,guid,tmdb,title,year,credits_rule) VALUES(?,?,?,?,?,0)`, int64(core.MovieKey(550)), staleGUID, 550, "Fight Club", 1999); err != nil {
		t.Fatal(err)
	}
	// Legacy mapping without plexguid_identity, plus wrong namesake legacy row.
	if _, err = db.Exec(`INSERT INTO plexguid_mappings(rating_key,guid,checked_at) VALUES(?,?,?),(?,?,?)`,
		int64(core.MovieKey(603)), legacyGUID, now,
		int64(core.MovieKey(770)), wrongGUID, now); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/library/metadata/" + staleGUID[len(staleGUID)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Fight Club","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`, staleGUID)
		case "/library/metadata/" + legacyGUID[len(legacyGUID)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"The Matrix","year":1999,"Guid":[{"id":"tmdb://603"}]}]}}`, legacyGUID)
		case "/library/metadata/" + wrongGUID[len(wrongGUID)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Runner","year":1962,"Guid":[{"id":"tmdb://770"}]}]}}`, wrongGUID)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	// Offline: unproven mappings are never served.
	pg := New(db, Options{BaseURL: upstream.URL})
	defer pg.Close()
	for _, g := range []string{staleGUID, legacyGUID, wrongGUID} {
		if _, err := pg.Lookup(ctx, g); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("offline Lookup(%s) = %v, want ErrNotFound", g, err)
		}
	}

	// Online: valid pre-rule-2 rows verify live and upgrade to credits_rule=2.
	pg.o.Token = func() string { return "fixture" }
	if k, err := pg.Lookup(ctx, staleGUID); err != nil || k != core.MovieKey(550) {
		t.Fatalf("Lookup(staleGUID) = %v, %v; want %v", k, err, core.MovieKey(550))
	}
	if k, err := pg.Lookup(ctx, legacyGUID); err != nil || k != core.MovieKey(603) {
		t.Fatalf("Lookup(legacyGUID) = %v, %v; want %v", k, err, core.MovieKey(603))
	}
	// Wrong namesake legacy row is rejected and removed.
	if _, err := pg.Lookup(ctx, wrongGUID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Lookup(wrongGUID) = %v, want ErrNotFound", err)
	}
	var wrongLeft int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plexguid_mappings WHERE rating_key=?`, int64(core.MovieKey(770))).Scan(&wrongLeft); err != nil || wrongLeft != 0 {
		t.Fatalf("wrong legacy row survived Lookup rejection: count=%d err=%v", wrongLeft, err)
	}
	// Upgraded rows now serve directly from cache (Cached, CachedMany, ExactKey, Lookup) without hitting upstream again.
	before := calls.Load()
	pg.o.Token = nil
	if got := pg.Cached(ctx, core.MovieKey(550)); got != staleGUID {
		t.Fatalf("Cached(550) = %q, want %q", got, staleGUID)
	}
	if got := pg.Cached(ctx, core.MovieKey(603)); got != legacyGUID {
		t.Fatalf("Cached(603) = %q, want %q", got, legacyGUID)
	}
	if k, err := pg.Lookup(ctx, staleGUID); err != nil || k != core.MovieKey(550) || calls.Load() != before {
		t.Fatalf("upgraded Lookup hit network or missed: k=%v err=%v calls=%d/%d", k, err, calls.Load(), before)
	}
}

// Issue #654: background repair automatically upgrades pre-rule-2 mappings
// left behind by upgrades without waiting for every title to be opened first.
func TestBackgroundRepairUpgradesPreRule2Mappings(t *testing.T) {
	oldTick := repairTickInterval
	repairTickInterval = time.Millisecond
	t.Cleanup(func() { repairTickInterval = oldTick })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seedCatalogTitle(t, db, core.MovieKey(550), "movie", "Fight Club", 1999)
	seedCatalogTitle(t, db, core.MovieKey(603), "movie", "The Matrix", 1999)
	seedCatalogTitle(t, db, core.MovieKey(770), "movie", "Runner", 2026)

	const (
		staleGUID  = "plex://movie/5d7768265af944001f1f6976"
		legacyGUID = "plex://movie/5d7768265af944001f1f6603"
		wrongGUID  = "plex://movie/5d7768265af944001f1f6962"
	)
	now := time.Now().Unix()
	if _, err = db.Exec(`INSERT INTO plexguid_mappings(rating_key,guid,checked_at) VALUES(?,?,?),(?,?,?),(?,?,?)`,
		int64(core.MovieKey(550)), staleGUID, now,
		int64(core.MovieKey(603)), legacyGUID, now,
		int64(core.MovieKey(770)), wrongGUID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO plexguid_identity(rating_key,guid,tmdb,title,year,credits_rule) VALUES(?,?,?,?,?,0)`,
		int64(core.MovieKey(550)), staleGUID, 550, "Fight Club", 1999); err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/matches":
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[]}}`)
		case "/library/metadata/" + staleGUID[len(staleGUID)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Fight Club","year":1999,"Guid":[{"id":"tmdb://550"}]}]}}`, staleGUID)
		case "/library/metadata/" + legacyGUID[len(legacyGUID)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"The Matrix","year":1999,"Guid":[{"id":"tmdb://603"}]}]}}`, legacyGUID)
		case "/library/metadata/" + wrongGUID[len(wrongGUID)-24:]:
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Runner","year":1962,"Guid":[{"id":"tmdb://770"}]}]}}`, wrongGUID)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	pg := New(db, Options{BaseURL: upstream.URL, Token: func() string { return "fixture" }, Background: true})
	defer pg.Close()

	for {
		good550 := pg.Cached(ctx, core.MovieKey(550)) == staleGUID
		good603 := pg.Cached(ctx, core.MovieKey(603)) == legacyGUID
		var wrongGuid string
		_ = db.QueryRow(`SELECT guid FROM plexguid_mappings WHERE rating_key=?`, int64(core.MovieKey(770))).Scan(&wrongGuid)
		if good550 && good603 && wrongGuid == "" {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("background repair did not finish: 550=%v 603=%v wrong=%q", good550, good603, wrongGuid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
