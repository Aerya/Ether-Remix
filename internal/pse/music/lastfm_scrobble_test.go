package music

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

type fakeSealer struct{}

func (fakeSealer) SealField(p string, _ ...string) (string, error) { return "sealed:" + p, nil }
func (fakeSealer) OpenField(s string, _ ...string) (string, bool, error) {
	return strings.TrimPrefix(s, "sealed:"), false, nil
}

func validSig(q url.Values, secret string) bool {
	var keys []string
	for k := range q {
		if k != "format" && k != "api_sig" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + q.Get(k))
	}
	sum := md5.Sum([]byte(b.String() + secret))
	return hex.EncodeToString(sum[:]) == q.Get("api_sig")
}

func TestPlaysStatsAndScrobble(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 7, 15, 0, 0, 0, time.Local)
	lib := NewLibrary(db)
	lib.now = func() time.Time { return now }
	daft := Track{ID: 1, Title: "One More Time", Duration: 320, Artist: Artist{ID: 7, Name: "Daft Punk"}, Album: Album{ID: 9, Title: "Discovery"}}
	air := Track{ID: 2, Title: "La femme d'argent", Duration: 430, Artist: Artist{ID: 8, Name: "Air"}}

	// Une écoute avant la liaison Last.fm : comptée, jamais envoyée.
	lib.now = func() time.Time { return now.Add(-time.Hour) }
	if err := lib.Played(ctx, 1, air); err != nil {
		t.Fatal(err)
	}
	lib.now = func() time.Time { return now }

	var scrobbled []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		q := r.PostForm
		if !validSig(q, "s3cret") || q.Get("api_key") != "k3y" {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": 13, "message": "Invalid method signature"})
			return
		}
		switch q.Get("method") {
		case "auth.getToken":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
		case "auth.getSession":
			_ = json.NewEncoder(w).Encode(map[string]any{"session": map[string]string{"name": "aerya", "key": "sk1"}})
		case "track.scrobble":
			if q.Get("sk") != "sk1" {
				_ = json.NewEncoder(w).Encode(map[string]any{"error": 9, "message": "Invalid session key"})
				return
			}
			scrobbled = append(scrobbled, q)
			_ = json.NewEncoder(w).Encode(map[string]any{"scrobbles": map[string]any{"@attr": map[string]int{"accepted": 1}}})
		}
	}))
	defer srv.Close()
	sc := &Scrobbler{DB: db, Library: lib, Sealer: fakeSealer{}, API: srv.URL, Now: func() time.Time { return now.Add(-time.Minute) },
		Key: func() string { return "k3y" }, Secret: func() string { return "s3cret" }}

	_, link, err := sc.AuthURL(ctx)
	if err != nil || !strings.Contains(link, "token=tok") || !strings.Contains(link, "api_key=k3y") {
		t.Fatalf("lien d'autorisation %q, %v", link, err)
	}
	if _, err := sc.Confirm(ctx, 1, "tok"); err != nil {
		t.Fatal(err)
	}
	var stored string
	_ = db.QueryRow(`SELECT session FROM pse_music_scrobble WHERE viewer_id=1`).Scan(&stored)
	if stored != "sealed:sk1" {
		t.Fatalf("session stockée en clair : %q", stored)
	}
	if err := lib.Played(ctx, 1, daft); err != nil {
		t.Fatal(err)
	}
	if err := lib.Played(ctx, 2, daft); err != nil { // spectateur non lié
		t.Fatal(err)
	}
	if err := sc.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(scrobbled) != 1 || scrobbled[0].Get("artist[0]") != "Daft Punk" || scrobbled[0].Get("album[0]") != "Discovery" || scrobbled[0].Has("artist[1]") {
		t.Fatalf("envois = %v", scrobbled)
	}
	if err := sc.Flush(ctx); err != nil || len(scrobbled) != 1 {
		t.Fatalf("écoute renvoyée deux fois : %d, %v", len(scrobbled), err)
	}
	links, _ := sc.Links(ctx)
	if links[1].Username != "aerya" || links[1].Sent != 1 || links[1].Error != "" {
		t.Fatalf("liaison = %+v", links[1])
	}

	st, err := lib.Stats(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if st.Plays != 3 || st.Listeners != 2 || len(st.Days) != 30 || st.Days[29].Plays != 3 {
		t.Fatalf("stats = %+v", st)
	}
	if st.TopTracks[0].Name != "One More Time" || st.TopTracks[0].Plays != 2 || st.TopArtists[0].Name != "Daft Punk" {
		t.Fatalf("classements = %+v %+v", st.TopTracks, st.TopArtists)
	}
	if h := st.Hours; h < 0.29 || h > 0.31 {
		t.Fatalf("heures = %v", h)
	}

	if err := sc.Unlink(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if links, _ := sc.Links(ctx); len(links) != 0 {
		t.Fatalf("liaison restante : %+v", links)
	}
}
