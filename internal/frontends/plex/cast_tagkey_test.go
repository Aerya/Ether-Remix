package plex

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexguid"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestRoleShapeGoldenWithRealPlexTagKeys locks the exact PMS 1.43 Role,
// Director, Writer and Producer XML attribute order and JSON shape when a
// title has verified Plex provider credits, including:
//   - real 24-hex plex://person tagKey on matched people
//   - #653 guard: unmatched people omit tagKey (never fabricated tmdb-person-)
//   - fallback to the provider's metadata-static.plex.tv thumb when Ether has
//     no TMDB portrait, served through Ether's authorized person artwork route
//   - slug, contentRatingAge (from Common Sense Media), and Producer tags
func TestRoleShapeGoldenWithRealPlexTagKeys(t *testing.T) {
	db := store.OpenTest(t)
	const (
		burbsHex   = "5d7768374de0ee001fccc04a"
		burbsGUID  = "plex://movie/" + burbsHex
		hanksKey   = "5d77682654f42c001f8c2650"
		dernKey    = "5d7768263c3c2a001fbcb0b0"
		danteKey   = "5d7768253c3c2a001fbcace5"
		olsenKey   = "5d776836880197001ec938c9"
		breznerKey = "5d7768284de0ee001fcc8dc8"
		dernThumb  = "https://metadata-static.plex.tv/a/people/bruce-dern.jpg"
		brezThumb  = "https://metadata-static.plex.tv/9/people/larry-brezner.jpg"
	)

	movie := burbs
	movie.Cast = []core.Person{
		{TMDB: 31, Name: "Tom Hanks", Role: "Ray Peterson", ThumbPath: "/hanks.jpg"},
		// Bruce Dern has no TMDB portrait: must fall back to provider metadata-static thumb.
		{TMDB: 1150, Name: "Bruce Dern", Role: "Lt. Mark Rumsfield", ThumbPath: ""},
		// Local-only cast member missing from Plex provider: must omit tagKey (#653).
		{TMDB: 99999, Name: "Unmatched Local Actor", Role: "Extra", ThumbPath: ""},
	}
	movie.Directors = []core.Person{{TMDB: 11218, Name: "Joe Dante", ThumbPath: "/dante.jpg"}}
	movie.Writers = []core.Person{{TMDB: 70939, Name: "Dana Olsen"}}
	movie.Crew = []core.Person{{TMDB: 17654, Name: "Larry Brezner", Role: "Producer"}}

	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year,detail,detail_at,fetched_at) VALUES(?,'movie',11974,'The ''Burbs',1989,1,1000,1000)`, int64(movie.Key)); err != nil {
		t.Fatal(err)
	}
	if err := storeVerifiedGUID(db, movie.Key, burbsGUID, 11974, movie.Title, movie.Year); err != nil {
		t.Fatal(err)
	}

	pg := plexguid.New(db, plexguid.Options{})
	defer pg.Close()
	if err := pg.StoreCredits(context.Background(), movie.Key, burbsGUID, plexguid.TitleCredits{
		Slug:             "the-burbs",
		ContentRatingAge: 13,
		Director:         []plexguid.Credit{{TagKey: danteKey, Name: "Joe Dante"}},
		Writer:           []plexguid.Credit{{TagKey: olsenKey, Name: "Dana Olsen"}},
		Producer:         []plexguid.Credit{{TagKey: breznerKey, Name: "Larry Brezner", Thumb: brezThumb}},
		Role: []plexguid.Credit{
			{TagKey: hanksKey, Name: "Tom Hanks", Role: "Ray Peterson", Thumb: "https://metadata-static.plex.tv/d/people/hanks.jpg"},
			{TagKey: dernKey, Name: "Bruce Dern", Role: "Lt. Mark Rumsfield", Thumb: dernThumb},
		},
	}); err != nil {
		t.Fatal(err)
	}

	f := newFixture()
	defer f.srv.Close()
	f.setDB(db)
	f.catalog.titles[movie.Key] = movie
	f.srv.setPlexGUID(pg)

	// XML golden shape check (exact PMS attribute order: id, filter, tag, tagKey, role, thumb).
	recXML := f.get(t, "/library/metadata/"+movie.Key.String()+"?includeExtras=1")
	if recXML.Code != http.StatusOK {
		t.Fatalf("XML status=%d body=%s", recXML.Code, recXML.Body.String())
	}
	xmlBody := recXML.Body.String()
	wantXML := []string{
		`slug="the-burbs"`,
		`contentRating="PG" contentRatingAge="13"`,
		`<Director id="11218" filter="director=11218" tag="Joe Dante" tagKey="` + danteKey + `" thumb="/library/metadata/` + movie.Key.String() + `/person/director/` + base64.RawURLEncoding.EncodeToString([]byte("/dante.jpg")) + `" />`,
		`<Writer id="70939" filter="writer=70939" tag="Dana Olsen" tagKey="` + olsenKey + `" />`,
		`<Role id="31" filter="actor=31" tag="Tom Hanks" tagKey="` + hanksKey + `" role="Ray Peterson" thumb="/library/metadata/` + movie.Key.String() + `/person/actor/` + base64.RawURLEncoding.EncodeToString([]byte("/hanks.jpg")) + `" />`,
		`<Role id="1150" filter="actor=1150" tag="Bruce Dern" tagKey="` + dernKey + `" role="Lt. Mark Rumsfield" thumb="/library/metadata/` + movie.Key.String() + `/person/actor/` + base64.RawURLEncoding.EncodeToString([]byte(dernThumb)) + `" />`,
		`<Role id="99999" filter="actor=99999" tag="Unmatched Local Actor" role="Extra" />`,
		`<Producer id="17654" filter="producer=17654" tag="Larry Brezner" tagKey="` + breznerKey + `" thumb="/library/metadata/` + movie.Key.String() + `/person/producer/` + base64.RawURLEncoding.EncodeToString([]byte(brezThumb)) + `" />`,
	}
	for _, snippet := range wantXML {
		if !strings.Contains(xmlBody, snippet) {
			t.Errorf("XML missing expected PMS tag shape:\nwant: %s\ngot:\n%s", snippet, xmlBody)
		}
	}
	if strings.Contains(xmlBody, `tmdb-person-`) {
		t.Fatalf("fabricated tmdb-person tagKey leaked into XML:\n%s", xmlBody)
	}

	// JSON shape check.
	recJSON := f.get(t, "/library/metadata/"+movie.Key.String()+"?includeExtras=1", jsonAccept...)
	mc := decodeJSON(t, recJSON)
	md := mc["Metadata"].([]any)[0].(map[string]any)
	if md["slug"] != "the-burbs" || int(md["contentRatingAge"].(float64)) != 13 {
		t.Fatalf("JSON slug/contentRatingAge = %v/%v", md["slug"], md["contentRatingAge"])
	}
	roles := md["Role"].([]any)
	if len(roles) != 3 {
		t.Fatalf("Role len = %d", len(roles))
	}
	r0 := roles[0].(map[string]any)
	if r0["tagKey"] != hanksKey || r0["role"] != "Ray Peterson" || r0["filter"] != "actor=31" {
		t.Fatalf("Role[0] = %#v", r0)
	}
	r1 := roles[1].(map[string]any)
	dernThumbURL, _ := r1["thumb"].(string)
	if r1["tagKey"] != dernKey || dernThumbURL == "" {
		t.Fatalf("Role[1] missing real tagKey or metadata-static fallback thumb: %#v", r1)
	}
	r2 := roles[2].(map[string]any)
	if _, hasTagKey := r2["tagKey"]; hasTagKey {
		t.Fatalf("unmatched actor must omit tagKey (#653): %#v", r2)
	}
	prods := md["Producer"].([]any)
	if len(prods) != 1 || prods[0].(map[string]any)["tagKey"] != breznerKey {
		t.Fatalf("Producer = %#v", prods)
	}
	prodThumbURL := prods[0].(map[string]any)["thumb"].(string)

	// Verify the proxied metadata-static.plex.tv fallback portraits actually serve
	// (both directly and through /photo/:/transcode) and respect viewer policy.
	if got := f.get(t, dernThumbURL); got.Code != http.StatusOK {
		t.Fatalf("fallback actor portrait status = %d", got.Code)
	}
	if got := f.get(t, "/photo/:/transcode?width=185&url="+url.QueryEscape(prodThumbURL)); got.Code != http.StatusOK {
		t.Fatalf("fallback producer portrait transcode status = %d", got.Code)
	}
	if f.art.count("/a/people/bruce-dern.jpg") == 0 || f.art.count("/9/people/larry-brezner.jpg") == 0 {
		t.Fatalf("fallback portraits did not fetch from provider URL")
	}
}

func TestDetailFirstOpenResolvesRealTagKeysAndCachesDurably(t *testing.T) {
	const (
		burbsHex  = "5d7768374de0ee001fccc04a"
		burbsGUID = "plex://movie/" + burbsHex
		hanksKey  = "5d77682654f42c001f8c2650"
		brezKey   = "5d7768284de0ee001fcc8dc8"
	)
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year,detail,detail_at,fetched_at) VALUES(?,'movie',11974,'The ''Burbs',1989,1,1000,1000)`, int64(burbs.Key)); err != nil {
		t.Fatal(err)
	}
	if err := storeVerifiedGUID(db, burbs.Key, burbsGUID, 11974, burbs.Title, burbs.Year); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/"+burbsHex {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"guid":"`+burbsGUID+`","type":"movie","slug":"the-burbs","CommonSenseMedia":[{"AgeRating":[{"age":13,"type":"official"}]}],"Director":[{"id":"5d7768253c3c2a001fbcace5","tag":"Joe Dante"}],"Writer":[{"id":"5d776836880197001ec938c9","tag":"Dana Olsen"}],"Role":[{"id":"`+hanksKey+`","tag":"Tom Hanks","role":"Ray Peterson","thumb":"https://metadata-static.plex.tv/d/people/hanks.jpg"}],"Producer":[{"id":"`+brezKey+`","tag":"Larry Brezner"}]}]}}`)
	}))
	defer upstream.Close()

	pg := plexguid.New(db, plexguid.Options{BaseURL: upstream.URL, Token: func() string { return "tok" }})
	defer pg.Close()

	f := newFixture()
	defer f.srv.Close()
	f.setDB(db)
	f.catalog.titles[burbs.Key] = burbs
	f.srv.setPlexGUID(pg)

	// First detail open fetches once within the bounded budget and emits real tagKeys.
	rec1 := f.get(t, "/library/metadata/"+burbs.Key.String()+"?includeExtras=1", jsonAccept...)
	if rec1.Code != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("first open status=%d calls=%d", rec1.Code, calls.Load())
	}
	if !strings.Contains(rec1.Body.String(), `"tagKey":"`+hanksKey+`"`) || !strings.Contains(rec1.Body.String(), `"slug":"the-burbs"`) {
		t.Fatalf("first open missing real tagKey/slug: %s", rec1.Body.String())
	}

	// Second open hits durable DB cache without any upstream request.
	rec2 := f.get(t, "/library/metadata/"+burbs.Key.String()+"?includeExtras=1", jsonAccept...)
	if rec2.Code != http.StatusOK || calls.Load() != 1 || !strings.Contains(rec2.Body.String(), `"tagKey":"`+hanksKey+`"`) {
		t.Fatalf("second open status=%d calls=%d", rec2.Code, calls.Load())
	}

	// Catalogue refresh invalidates the cached credits and triggers one fresh fetch.
	if _, err := db.Exec(`UPDATE catalog_titles SET detail_at=2000 WHERE key=?`, int64(burbs.Key)); err != nil {
		t.Fatal(err)
	}
	rec3 := f.get(t, "/library/metadata/"+burbs.Key.String()+"?includeExtras=1", jsonAccept...)
	if rec3.Code != http.StatusOK || calls.Load() != 2 || !strings.Contains(rec3.Body.String(), `"tagKey":"`+hanksKey+`"`) {
		t.Fatalf("post-refresh open status=%d calls=%d", rec3.Code, calls.Load())
	}
}

func TestLibraryPersonRoutesWithRealPlexTagKey(t *testing.T) {
	const (
		burbsGUID = "plex://movie/5d7768374de0ee001fccc04a"
		hanksKey  = "5d77682654f42c001f8c2650"
	)
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year,detail,detail_at,fetched_at) VALUES(?,'movie',11974,'The ''Burbs',1989,1,1000,1000)`, int64(burbs.Key)); err != nil {
		t.Fatal(err)
	}
	if err := storeVerifiedGUID(db, burbs.Key, burbsGUID, 11974, burbs.Title, burbs.Year); err != nil {
		t.Fatal(err)
	}

	pg := plexguid.New(db, plexguid.Options{})
	defer pg.Close()
	if err := pg.StoreCredits(context.Background(), burbs.Key, burbsGUID, plexguid.TitleCredits{
		Slug: "the-burbs",
		Role: []plexguid.Credit{{TagKey: hanksKey, Name: "Tom Hanks", Role: "Ray Peterson", Thumb: "https://metadata-static.plex.tv/d/people/hanks.jpg"}},
	}); err != nil {
		t.Fatal(err)
	}

	f := newFixture()
	defer f.srv.Close()
	f.setDB(db)
	f.catalog.titles[burbs.Key] = burbs
	// Add a second title Tom Hanks directed to verify union across credit kinds.
	directed := alien
	directed.Key = core.MovieKey(9001)
	directed.Title = "That Thing You Do!"
	directed.Year = 1996
	directed.Released = time.Date(1996, 10, 4, 0, 0, 0, 0, time.UTC)
	directed.Directors = []core.Person{{TMDB: 31, Name: "Tom Hanks", ThumbPath: "/hanks.jpg"}}
	f.catalog.titles[directed.Key] = directed
	f.srv.setPlexGUID(pg)

	// Rendering detail records the 24-hex tagKey -> TMDB mapping durably.
	if rec := f.get(t, "/library/metadata/"+burbs.Key.String()+"?includeExtras=1"); rec.Code != http.StatusOK {
		t.Fatalf("detail status=%d", rec.Code)
	}

	// GET /library/people/{24-hex tagKey} and GET /library/people/{tmdb} both
	// return the real 24-hex tagKey and working portrait thumb.
	for _, id := range []string{hanksKey, "31"} {
		rec := f.get(t, "/library/people/"+id, jsonAccept...)
		if rec.Code != http.StatusOK {
			t.Fatalf("/library/people/%s status=%d body=%s", id, rec.Code, rec.Body.String())
		}
		dir := decodeJSON(t, rec)["Directory"].([]any)[0].(map[string]any)
		if dir["tag"] != "Tom Hanks" || dir["tagKey"] != hanksKey || dir["filter"] != "actor=31" {
			t.Fatalf("/library/people/%s dir=%#v", id, dir)
		}
		if thumb, _ := dir["thumb"].(string); thumb == "" || f.get(t, thumb).Code != http.StatusOK {
			t.Fatalf("/library/people/%s thumb=%q failed", id, thumb)
		}
	}

	// GET /library/people/{24-hex tagKey}/media returns untyped union across kinds.
	recMedia := f.get(t, "/library/people/"+hanksKey+"/media", jsonAccept...)
	if recMedia.Code != http.StatusOK {
		t.Fatalf("/media status=%d body=%s", recMedia.Code, recMedia.Body.String())
	}
	mc := decodeJSON(t, recMedia)
	if _, hasVG := mc["viewGroup"]; hasVG {
		t.Fatalf("tagKey /media must be untyped (no viewGroup): %#v", mc)
	}
	items := mc["Metadata"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected union of acted + directed titles (2), got %d: %#v", len(items), items)
	}

	// Unknown 24-hex Plex person id still 404s.
	if rec := f.get(t, "/library/people/5d776826880197001ec9038e"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown 24-hex person id got %d, want 404", rec.Code)
	}

	// /hubs/search person directory carries real 24-hex tagKey (never tmdb-person-).
	recSearch := f.get(t, "/hubs/search?query=Tom+Hanks", jsonAccept...)
	if recSearch.Code != http.StatusOK {
		t.Fatalf("/hubs/search status=%d", recSearch.Code)
	}
	body := recSearch.Body.String()
	if !strings.Contains(body, `"tagKey":"`+hanksKey+`"`) {
		t.Fatalf("/hubs/search missing real 24-hex tagKey: %s", body)
	}
	if strings.Contains(body, `tmdb-person-`) {
		t.Fatalf("/hubs/search leaked fabricated tagKey: %s", body)
	}
}

// TestSectionListingNeverQueriesOrWritesCredits locks the invariant that
// section listings (/library/sections/{id}/all) remain read-only and batched
// with respect to provider credits and person mappings on both cold (no
// verified GUIDs) and warm (verified GUIDs + cached credits) libraries:
//   - zero per-item plexguid_mappings lookups
//   - zero reads or writes against plexguid_title_credits or plexguid_person_map
//   - zero upstream credit fetches
//   - no Producer tags or tagKey projection on list items
func TestSectionListingNeverQueriesOrWritesCredits(t *testing.T) {
	const (
		burbsHex  = "5d7768374de0ee001fccc04a"
		burbsGUID = "plex://movie/" + burbsHex
		hanksKey  = "5d77682654f42c001f8c2650"
	)
	db := store.OpenTest(t)

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		http.Error(w, "list path must not call metadata provider", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	pg := plexguid.New(db, plexguid.Options{BaseURL: upstream.URL, Token: func() string { return "tok" }})
	defer pg.Close()

	f := newFixture()
	defer f.srv.Close()
	f.setDB(db)
	f.srv.setPlexGUID(pg)

	// Seed 5 movies so any per-item query scales visibly with page size.
	for i := int64(1); i <= 5; i++ {
		m := burbs
		m.Key = core.MovieKey(11974 + i)
		m.IDs = core.IDs{TMDB: 11974 + i}
		m.Title = "List Movie " + string(rune('A'+i-1))
		f.catalog.titles[m.Key] = m
		if _, err := db.Exec(`INSERT OR IGNORE INTO catalog_titles(key,type,tmdb_id,title,year,detail,detail_at,fetched_at) VALUES(?,'movie',?,?,1989,1,1000,1000)`, int64(m.Key), m.IDs.TMDB, m.Title); err != nil {
			t.Fatal(err)
		}
	}

	assertListBudget := func(phase string) {
		t.Helper()
		ctx, stats := store.WithQueryStats(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/library/sections/1/all?X-Plex-Container-Size=50&X-Plex-Container-Start=0", nil).WithContext(ctx)
		req.Header.Set("X-Plex-Token", ownerToken)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", phase, rec.Code, rec.Body.String())
		}
		if stats.Writes() != 0 {
			t.Fatalf("%s: unexpected SQL writes=%d stmts=%+v", phase, stats.Writes(), stats.Statements())
		}
		for _, st := range stats.Statements() {
			q := strings.ToLower(st.SQL)
			if strings.Contains(q, "plexguid_title_credits") || strings.Contains(q, "plexguid_person_map") {
				t.Errorf("%s: list path touched credits/person-map table (%dx): %s", phase, st.Count, st.SQL)
			}
			if strings.Contains(q, "plexguid_mappings") && strings.Contains(q, "rating_key=?") {
				t.Errorf("%s: list path ran per-item plexguid_mappings query (%dx): %s", phase, st.Count, st.SQL)
			}
		}
		if upstreamCalls.Load() != 0 {
			t.Fatalf("%s: list path made %d upstream provider calls", phase, upstreamCalls.Load())
		}
		body := rec.Body.String()
		if strings.Contains(body, `"Producer"`) || strings.Contains(body, `"tagKey"`) {
			t.Fatalf("%s: list response leaked Producer or tagKey: %s", phase, body)
		}
	}

	// 1. Cold library: no verified plex:// GUIDs in plexguid_mappings.
	assertListBudget("cold")

	// 2. Warm library: verified plex:// GUIDs + cached credits in DB.
	firstKey := core.MovieKey(11975)
	if err := storeVerifiedGUID(db, firstKey, burbsGUID, 11975, "List Movie A", 1989); err != nil {
		t.Fatal(err)
	}
	if err := pg.StoreCredits(context.Background(), firstKey, burbsGUID, plexguid.TitleCredits{
		Slug:             "list-movie-a",
		ContentRatingAge: 13,
		Role:             []plexguid.Credit{{TagKey: hanksKey, Name: "Tom Hanks", Role: "Ray Peterson"}},
		Producer:         []plexguid.Credit{{TagKey: "5d7768284de0ee001fcc8dc8", Name: "Larry Brezner"}},
	}); err != nil {
		t.Fatal(err)
	}
	assertListBudget("warm")

	var personRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plexguid_person_map`).Scan(&personRows); err != nil {
		t.Fatal(err)
	}
	if personRows != 0 {
		t.Fatalf("section listing wrote %d plexguid_person_map rows, want 0", personRows)
	}
}
