package releasedb

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestSFRAlkoCandidatesAreFilesNotMagnets(t *testing.T) {
	rows := []sfrRow{{ID: "test", Name: "Film.2020.FRENCH.1080p.mkv", Size: 1000, TMDB: 42, AlkoURL: "https://alldebrid.com/f/verified"}, {ID: "bad", Size: 1000, AlkoURL: "https://evil.example/file"}}
	got := sfrAlkoCandidates(rows)
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	r := got[0]
	if r.Protocol != core.ProtocolHTTP || r.StreamDebrid != "alldebrid" || r.InfoHash != "" || r.MagnetURL != "" || r.StreamURL != rows[0].AlkoURL {
		t.Fatal("Alko must use the verified file")
	}
}

func TestSFRAlkoImportExpiresAndKeepsTorrentSeparate(t *testing.T) {
	e := newFreshEnv(t)
	key := core.MovieKey(42)
	sfrCatalog(t, e, "movie", key)
	rows := []sfrRow{
		{ID: "file", Name: "Film.2020.FRENCH.1080p.mkv", Size: 1000, TMDB: 42, Media: "movie", AlkoURL: "https://alldebrid.com/f/verified", Cached: []string{"alldebrid"}, CacheUntil: e.now.Add(time.Hour).Unix()},
		{ID: "empty-link", Alko: true, Name: "Film.2020.FRENCH.1080p.mkv", Size: 1000, TMDB: 42, Media: "movie", Cached: []string{"alldebrid"}},
		{ID: "torrent", Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Film.2020.FRENCH.1080p", Size: 1000, TMDB: 42, Media: "movie"},
	}
	if n, err := e.s.sfrSave(e.ctx, rows); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	got, err := e.s.st.Releases(e.ctx, key, Filter{CachedOnly: true})
	if err != nil || len(got) != 1 || got[0].Protocol != core.ProtocolHTTP {
		t.Fatal(got, err)
	}
	e.now = e.now.Add(2 * time.Hour)
	got, err = e.s.st.Releases(e.ctx, key, Filter{CachedOnly: true})
	if err != nil || len(got) != 0 {
		t.Fatal("stale proof", got, err)
	}
}

func TestSFRAlkoFrenchAudioAndSubtitles(t *testing.T) {
	for _, name := range []string{"Film.2020.FRENCH.1080p.mkv", "Film.2020.MULTi.1080p.mkv", "Film.2020.VOSTFR.1080p.mkv", "Film.2020.SUBFRENCH.1080p.mkv"} {
		if !sfrAlkoLanguage(name) {
			t.Fatalf("French release excluded: %s", name)
		}
	}
	if sfrAlkoLanguage("Film.2020.GERMAN.1080p.mkv") {
		t.Fatal("unrelated language included")
	}
}
