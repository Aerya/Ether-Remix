package releasedb

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"testing"
)

func TestReferenceBackfillRevisitsOldMetadataAndEpisodes(t *testing.T) {
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	e := newFreshEnv(t)
	movie, show, ep := core.MovieKey(42), core.ShowKey(9), core.EpisodeKey(9, 2, 3)
	sfrCatalog(t, e, "movie", movie)
	sfrCatalog(t, e, "show", show)
	for _, key := range []core.RatingKey{movie, ep} {
		if _, err := e.s.st.db.Exec(`INSERT INTO pse_reference_backfill(title_key) VALUES(?)`, key); err != nil {
			t.Fatal(err)
		}
	}
	q, ok, err := e.s.nextReferenceBackfill(e.ctx, core.TypeShow, e.now)
	if err != nil || !ok || q.Key != ep || q.Type != core.TypeEpisode || q.Season != 2 || q.Episode != 3 {
		t.Fatalf("episode %+v %v %v", q, ok, err)
	}
	var captured []core.Release
	e.s.d.Remember = func(_ context.Context, k core.RatingKey, rs []core.Release) {
		if k != movie {
			t.Fatal(k)
		}
		captured = append(captured, rs...)
	}
	good := frRelease("Film.2020.FRENCH.1080p")
	wrong := frRelease("Superman.2025.1080p")
	missing := good
	missing.ID = "missing"
	missing.StreamURL = ""
	e.stub.rels["a"] = []core.Release{good, wrong, missing}
	q, ok, err = e.s.nextReferenceBackfill(e.ctx, core.TypeMovie, e.now)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err = e.s.IndexTitle(e.ctx, q, e.s.sources()); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0].StreamURL != good.StreamURL || captured[0].IndexerID != "a" {
		t.Fatalf("captured %d", len(captured))
	}
	// A failure to save a source reference keeps the title queued, with backoff.
	if _, ok, err = e.s.nextReferenceBackfill(e.ctx, core.TypeMovie, e.now); err != nil || ok {
		t.Fatal("busy loop", ok, err)
	}
	e.s.d.ReferenceReady = func(context.Context, core.RatingKey) bool { return true }
	if err = e.s.IndexTitle(e.ctx, q, e.s.sources()); err != nil {
		t.Fatal(err)
	}
	var n int
	e.s.st.db.QueryRow(`SELECT COUNT(*) FROM pse_reference_backfill WHERE title_key=?`, movie).Scan(&n)
	if n != 0 {
		t.Fatal("completed title still queued")
	}
}
