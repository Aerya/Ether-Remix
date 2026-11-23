package resolver

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestResolvePopulatesMovieTargetAudioLanguages(t *testing.T) {
	cfg := testCfg()
	for i := range cfg.Quality.Profiles {
		if cfg.Quality.Profiles[i].Name == "4K" {
			cfg.Quality.Profiles[i].Language = core.ProfileLanguageAny
			cfg.Quality.Profiles[i].PreferredLanguages = []string{"fr"}
		}
	}
	r := core.Release{
		ID:      "dual-pack",
		GUID:    "dual-pack",
		Indexer: "fake",
		Name:    "The.Signal.1999.1080p.BluRay.MULTI.x264-GRP",
		NZBURL:  "http://fake/dual",
		Size:    10000 << 20,
		PubDate: time.Now().Add(-24 * time.Hour),
		Grabs:   10,
	}
	r.Info = releaseparse.Parse(r.Name)
	idx := &fakeIndexer{releases: []core.Release{r}}
	svc := New(idx, fakeHealth{}, nil, cfg, Options{})
	t.Cleanup(func() { _ = svc.Close() })

	title := matrixTitle()
	title.Language = "en"
	res, err := svc.Resolve(context.Background(), core.ResolveRequest{
		Title:  title,
		Client: appleTVCaps(),
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if res.Release.Target == nil {
		t.Fatal("expected Release.Target to be populated for movie")
	}
	wantLangs := []string{"french", "english"}
	if !slices.Equal(res.Release.Target.WantedLanguages, wantLangs) {
		t.Fatalf("WantedLanguages = %v, want %v", res.Release.Target.WantedLanguages, wantLangs)
	}
	if res.Release.Target.OriginalLanguage != "english" {
		t.Fatalf("OriginalLanguage = %q, want english", res.Release.Target.OriginalLanguage)
	}
}
