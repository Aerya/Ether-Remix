package plexguid

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestTitleCreditsMatchPriorityAndGuard(t *testing.T) {
	item := providerItem{
		GUID:             "plex://movie/5d7768374de0ee001fccc04a",
		Slug:             "the-burbs",
		ContentRatingAge: 0,
		CommonSenseMedia: []struct {
			AgeRating []struct {
				Age  int    `json:"age"`
				Type string `json:"type"`
			} `json:"AgeRating"`
		}{{AgeRating: []struct {
			Age  int    `json:"age"`
			Type string `json:"type"`
		}{{Age: 13, Type: "official"}}}},
		Director: []providerPerson{
			{ID: "5d7768253c3c2a001fbcace5", Tag: "Joe Dante", Thumb: "https://metadata-static.plex.tv/f/people/dante.jpg"},
		},
		Writer: []providerPerson{
			{ID: "5d776836880197001ec938c9", Tag: "Dana Olsen", Thumb: "https://evil.example/nope.jpg"},
		},
		Producer: []providerPerson{
			{ID: "5d7768284de0ee001fcc8dc8", Tag: "Larry Brezner", Thumb: "https://metadata-static.plex.tv/9/people/brezner.jpg"},
			{ID: "tmdb-person-999", Tag: "Fabricated Person"},
		},
		Role: []providerPerson{
			{ID: "5d77682654f42c001f8c2650", Tag: "Tom Hanks", Role: "Ray Peterson", Thumb: "https://metadata-static.plex.tv/d/people/hanks.jpg"},
			{ID: "5d7768260000000000000001", Tag: "John Smith", Role: "Cop"},
			{ID: "5d7768260000000000000002", Tag: "John Smith", Role: "Driver"},
			{ID: "5d7768260000000000000003", Tag: "Stellan Skarsgard", Role: "Neighbor"},
			{ID: "5d7768260000000000000004", Tag: "C.C.H. Pounder", Role: "Detective", IDs: []struct {
				ID string `json:"id"`
			}{{ID: "tmdb://30485"}}},
		},
	}
	tc := creditsFromProviderItem(item)
	if tc.Slug != "the-burbs" || tc.ContentRatingAge != 13 {
		t.Fatalf("slug=%q age=%d, want the-burbs/13", tc.Slug, tc.ContentRatingAge)
	}
	// Fabricated tagKey is dropped on ingest; non-Plex thumb host is stripped.
	if len(tc.Producer) != 1 || tc.Producer[0].TagKey != "5d7768284de0ee001fcc8dc8" {
		t.Fatalf("producers = %+v", tc.Producer)
	}
	if len(tc.Writer) != 1 || tc.Writer[0].Thumb != "" {
		t.Fatalf("untrusted thumb survived: %+v", tc.Writer)
	}

	// Name + character disambiguates two actors sharing the exact same name.
	c1, ok := tc.Match("actor", core.Person{Name: "John Smith", Role: "Cop (uncredited)"})
	if !ok || c1.TagKey != "5d7768260000000000000001" {
		t.Fatalf("John Smith Cop = (%+v, %v)", c1, ok)
	}
	c2, ok := tc.Match("actor", core.Person{Name: "John Smith", Role: "Driver / Guard"})
	if !ok || c2.TagKey != "5d7768260000000000000002" {
		t.Fatalf("John Smith Driver = (%+v, %v)", c2, ok)
	}
	// Ambiguous namesake without character or TMDB id must never guess.
	if c3, ok := tc.Match("actor", core.Person{Name: "John Smith"}); ok {
		t.Fatalf("ambiguous namesake guessed: %+v", c3)
	}

	// Provider TMDB id matches even when name punctuation differs.
	ctmdb, ok := tc.Match("actor", core.Person{TMDB: 30485, Name: "CCH Pounder"})
	if !ok || ctmdb.TagKey != "5d7768260000000000000004" {
		t.Fatalf("TMDB id match = (%+v, %v)", ctmdb, ok)
	}

	// Diacritic-insensitive unique name match.
	caccent, ok := tc.Match("actor", core.Person{Name: "Stellan Skarsgård", Role: "Other"})
	if !ok || caccent.TagKey != "5d7768260000000000000003" {
		t.Fatalf("diacritic match = (%+v, %v)", caccent, ok)
	}

	// Cross-kind unique match (e.g., Dana Olsen also acting in a cameo).
	ccross, ok := tc.Match("actor", core.Person{Name: "Dana Olsen"})
	if !ok || ccross.TagKey != "5d776836880197001ec938c9" {
		t.Fatalf("cross-kind match = (%+v, %v)", ccross, ok)
	}

	if IsPersonTagKey("tmdb-person-31") || IsPersonTagKey("5d77682654f42c001f8c265G") || !IsPersonTagKey("5d77682654f42c001f8c2650") {
		t.Fatal("IsPersonTagKey guard broken")
	}
}

func TestTitleCreditsCacheInvalidatesOnCatalogRefreshAndGUIDChange(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	key := core.MovieKey(11974)
	const (
		hexA  = "5d7768374de0ee001fccc04a"
		guidA = "plex://movie/" + hexA
		guidB = "plex://movie/5d7768374de0ee001fccc04b"
	)
	if _, err := db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id,title,year,detail,detail_at,fetched_at) VALUES(?,'movie',11974,'The Burbs',1989,1,1000,1000)`, int64(key)); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+guidA+`","type":"movie","slug":"the-burbs","Role":[{"id":"5d77682654f42c001f8c2650","tag":"Tom Hanks","role":"Ray Peterson","thumb":"https://metadata-static.plex.tv/d/people/hanks.jpg"}]}]}}`)
	}))
	defer srv.Close()

	s1 := New(db, Options{BaseURL: srv.URL, Token: func() string { return "tok" }})
	defer s1.Close()

	tc, ok := s1.ResolveCreditsBounded(ctx, key, guidA, 2*time.Second)
	if !ok || len(tc.Role) != 1 || tc.Role[0].TagKey != "5d77682654f42c001f8c2650" || calls.Load() != 1 {
		t.Fatalf("first resolve: ok=%v calls=%d tc=%+v", ok, calls.Load(), tc)
	}

	// Simulate server restart: fresh Service hits durable DB cache without network.
	s2 := New(db, Options{BaseURL: srv.URL, Token: func() string { return "tok" }})
	defer s2.Close()
	if tc2, ok := s2.ResolveCreditsBounded(ctx, key, guidA, 2*time.Second); !ok || tc2.Slug != "the-burbs" || calls.Load() != 1 {
		t.Fatalf("durable cache miss after restart: ok=%v calls=%d tc=%+v", ok, calls.Load(), tc2)
	}

	// Catalogue refresh advances detail_at -> cache is invalidated and re-fetched once.
	if _, err := db.Exec(`UPDATE catalog_titles SET detail_at=2000 WHERE key=?`, int64(key)); err != nil {
		t.Fatal(err)
	}
	if _, hit := s2.CachedCredits(ctx, key, guidA); hit {
		t.Fatal("cached credits not invalidated by catalogue refresh")
	}
	if _, ok := s2.ResolveCreditsBounded(ctx, key, guidA, 2*time.Second); !ok || calls.Load() != 2 {
		t.Fatalf("post-refresh resolve: ok=%v calls=%d", ok, calls.Load())
	}

	// Different verified GUID misses cached credits for old GUID.
	if _, hit := s2.CachedCredits(ctx, key, guidB); hit {
		t.Fatal("cached credits served for mismatched GUID")
	}
}

func TestTitleCreditsBoundedSingleFlight(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	key := core.MovieKey(11974)
	const guid = "plex://movie/5d7768374de0ee001fccc04a"
	seedCatalogTitle(t, db, key, "movie", "The 'Burbs", 1989)

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+guid+`","type":"movie","Role":[{"id":"5d77682654f42c001f8c2650","tag":"Tom Hanks","role":"Ray Peterson"}]}]}}`)
	}))
	defer srv.Close()

	s := New(db, Options{BaseURL: srv.URL, Token: func() string { return "tok" }})
	defer s.Close()

	// Concurrent callers share one single-flight fetch; bounded wait returns
	// promptly when upstream is slow while background completes.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := s.ResolveCreditsBounded(ctx, key, guid, 25*time.Millisecond); ok {
				t.Error("expected slow upstream to exceed short wait")
			}
		}()
	}
	<-started
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("single-flight calls = %d, want 1", calls.Load())
	}

	// Releasing upstream lets the background goroutine fill the durable cache.
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if tc, hit := s.CachedCredits(ctx, key, guid); hit && len(tc.Role) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background credit fetch never populated cache")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("unexpected second call: %d", calls.Load())
	}
}

func TestPersonMapRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	s := New(db, Options{})
	defer s.Close()

	const hanksKey = "5d77682654f42c001f8c2650"
	s.RecordPersonMappings(ctx, []PersonMapping{
		{TagKey: hanksKey, PersonTMDB: 31, Name: "Tom Hanks", Thumb: "https://metadata-static.plex.tv/d/people/hanks.jpg"},
		{TagKey: "tmdb-person-99", PersonTMDB: 99, Name: "Fabricated"},
	})

	got, ok := s.PersonByTagKey(ctx, hanksKey)
	if !ok || got.PersonTMDB != 31 || got.Thumb != "https://metadata-static.plex.tv/d/people/hanks.jpg" {
		t.Fatalf("PersonByTagKey = (%+v, %v)", got, ok)
	}
	if _, ok := s.PersonByTagKey(ctx, "tmdb-person-99"); ok {
		t.Fatal("fabricated tagKey must never be stored or returned")
	}
	byTMDB, ok := s.PersonByTMDB(ctx, 31)
	if !ok || byTMDB.TagKey != hanksKey {
		t.Fatalf("PersonByTMDB = (%+v, %v)", byTMDB, ok)
	}
	batch := s.PeopleByTMDB(ctx, []int64{31, 99})
	if len(batch) != 1 || batch[31].TagKey != hanksKey {
		t.Fatalf("PeopleByTMDB = %+v", batch)
	}
}

func TestVerifyTitleDoesNotPersistCredits(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	key := core.MovieKey(11974)
	const (
		hex  = "5d7768374de0ee001fccc04a"
		guid = "plex://movie/" + hex
	)
	seedCatalogTitle(t, db, key, "movie", "The 'Burbs", 1989)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+guid+`","type":"movie","title":"The 'Burbs","year":1989,"slug":"the-burbs","Guid":[{"id":"tmdb://11974"}],"Role":[{"id":"5d77682654f42c001f8c2650","tag":"Tom Hanks","role":"Ray Peterson"}]}]}}`)
	}))
	defer srv.Close()

	s := New(db, Options{BaseURL: srv.URL, Token: func() string { return "tok" }})
	defer s.Close()

	id, ok, err := s.verifyTitle(ctx, key, "movie", guid, localIdentity{title: "The 'Burbs", year: 1989})
	if err != nil || !ok || id.GUID != guid {
		t.Fatalf("verifyTitle = (%+v, %v, %v)", id, ok, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plexguid_title_credits`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("verifyTitle persisted %d plexguid_title_credits rows as a side effect, want 0", count)
	}
}
