package plex

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/pse/listonly"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func continueRows() []model.Hub {
	return []model.Hub{{HubIdentifier: "home.continue", Context: "hub.home.continue", Metadata: []model.Metadata{{RatingKey: core.MovieKey(550).String(), Type: "movie"}}}}
}

func TestListOnlySkipsBrowseWarmLink(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	wpb := &warmWaiterPlayback{Playback: f.playback}
	f.srv.setPlayback(wpb)
	pr := &Request{User: &core.User{ID: 1}}

	f.srv.warmBrowseLink(listonly.With(context.Background()), pr, continueRows())
	f.srv.warmKey(listonly.With(context.Background()), pr, core.MovieKey(550))
	if len(wpb.prewarms) != 0 {
		t.Fatalf("liste légère : %d préchauffages debrid", len(wpb.prewarms))
	}

	restore := pseenv.Replace(map[string]string{listonly.Key: "off"})
	defer restore()
	f.srv.warmBrowseLink(listonly.With(context.Background()), pr, continueRows())
	if len(wpb.prewarms) != 1 {
		t.Fatalf("réglage off : %d préchauffages, 1 attendu (comportement d'Ether)", len(wpb.prewarms))
	}
}

func TestListOnlyRenderDoesNotWarmTitleDetails(t *testing.T) {
	// Deux résumés de liste sans distribution ni classification.
	a, b := alien, alien
	b.Key, b.IDs = core.MovieKey(349), core.IDs{TMDB: 349}
	titles := []frontend.Title{a, b}
	for _, c := range []struct {
		name    string
		setting string
		want    bool
	}{{"on", "on", false}, {"off", "off", true}} {
		t.Run(c.name, func(t *testing.T) {
			defer pseenv.Replace(map[string]string{listonly.Key: c.setting})()
			f := newFixture()
			defer f.srv.Close()
			var prefetched atomic.Int64
			f.srv.titleDetails().setPrefetch(func(context.Context, []frontend.RatingKey) { prefetched.Add(1) })
			pr := &Request{User: &core.User{ID: 1}}
			ctx := listonly.With(context.Background())
			f.srv.renderTitles(ctx, pr, titles, renderOpts{})
			deadline := time.Now().Add(time.Second)
			for c.want && prefetched.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(50 * time.Millisecond)
			if got := prefetched.Load() > 0; got != c.want {
				t.Fatalf("préchargement des fiches = %v, want %v", got, c.want)
			}
		})
	}
}

// pseLightListsOff rétablit le comportement d'Ether pour un test qui le décrit.
func pseLightListsOff(t *testing.T) {
	t.Helper()
	t.Cleanup(pseenv.Replace(map[string]string{listonly.Key: "off"}))
}
