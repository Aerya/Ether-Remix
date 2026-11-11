package releasedb

import (
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
	"testing"
	"time"
)

func TestSFRCandidatesEpisodeAndPacks(t *testing.T) {
	rows := []sfrRow{
		{Hash: strings.Repeat("a", 40), Name: "Show.S02E03.FRENCH.1080p", Seasons: "[2]", Episodes: "[3]"},
		{Hash: strings.Repeat("b", 40), Name: "Show.S02.FRENCH.1080p", Seasons: "[2]", Episodes: "[]"},
		{Hash: strings.Repeat("c", 40), Name: "Show.S01E03.FRENCH.1080p", Seasons: "[1]", Episodes: "[3]"},
		{Hash: strings.Repeat("d", 40), Name: "Show.S02E04.FRENCH.1080p", Seasons: "[2]", Episodes: "[4]"},
		{Hash: "bad", Name: "Show.S02E03.FRENCH.1080p"},
	}
	rows = append(rows, rows[0])
	got := sfrCandidates(rows, core.SearchRequest{Type: core.TypeEpisode, Season: 2, Episode: 3})
	if len(got) != 2 {
		t.Fatalf("episode/pack candidates: %+v", got)
	}
	for _, r := range got {
		if r.InfoHash == "" || r.MagnetURL == "" || len(r.DebridCached) > 0 {
			t.Fatal("must be revalidated by provider", r)
		}
	}
}
func TestSFRCacheProofExpires(t *testing.T) {
	e := newFreshEnv(t)
	key := core.MovieKey(42)
	sfrCatalog(t, e, "movie", key)
	old := OverridePlayable(NewPlayable(1, "lang=fr;cached=1", false))
	defer old()
	row := sfrRow{ID: "a", Name: "Film.2020.FRENCH.1080p", Hash: strings.Repeat("a", 40), Media: "movie", TMDB: 42, Cached: []string{"realdebrid"}, CacheUntil: e.now.Add(time.Minute).Unix()}
	if _, err := e.s.sfrSave(e.ctx, []sfrRow{row}); err != nil {
		t.Fatal(err)
	}
	rs, err := e.s.st.Releases(e.ctx, key, Filter{CachedOnly: true})
	if err != nil || len(rs) != 1 || rs[0].DebridCached[0] != "realdebrid" {
		t.Fatal(rs, err)
	}
	e.now = e.now.Add(2 * time.Minute)
	rs, err = e.s.st.Releases(e.ctx, key, Filter{CachedOnly: true})
	if err != nil || len(rs) != 0 {
		t.Fatal("expired proof", rs, err)
	}
	if err = e.s.expireSFRCache(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.playable(t, key); n != 0 {
		t.Fatal("materialized availability survives expiry", n)
	}
	row.Cached = nil
	row.CacheUntil = 0
	if _, err = e.s.sfrSave(e.ctx, []sfrRow{row}); err != nil {
		t.Fatal(err)
	}
	if n := e.playable(t, key); n != 0 {
		t.Fatal("unknown cache was certified", n)
	}
}

func TestSFRExpiredProofSchedulesBoundedRefresh(t *testing.T) {
	e := newFreshEnv(t)
	key := core.MovieKey(42)
	sfrCatalog(t, e, "movie", key)
	row := sfrRow{ID: "a", Name: "Film.FRENCH.1080p", Hash: strings.Repeat("a", 40), Media: "movie", TMDB: 42, Cached: []string{"realdebrid"}, CacheUntil: e.now.Add(-time.Minute).Unix()}
	if _, err := e.s.sfrSave(e.ctx, []sfrRow{row}); err != nil {
		t.Fatal(err)
	}
	rows, err := e.s.st.sfrUnchecked(e.ctx, e.now.Add(-sfrTitlesRecheck), 500)
	if err != nil || len(rows) != 1 {
		t.Fatal("expired proof not scheduled", rows, err)
	}
	if err = e.s.st.sfrChecked(e.ctx, rows, e.now); err != nil {
		t.Fatal(err)
	}
	rows, err = e.s.st.sfrUnchecked(e.ctx, e.now.Add(-sfrTitlesRecheck), 500)
	if err != nil || len(rows) != 0 {
		t.Fatal("proof refresh busy-loop", rows, err)
	}
	e.now = e.now.Add(sfrDefaultStep + time.Second)
	rows, err = e.s.st.sfrUnchecked(e.ctx, e.now.Add(-sfrTitlesRecheck), 500)
	if err != nil || len(rows) != 1 {
		t.Fatal("proof refresh not retried", rows, err)
	}
}
