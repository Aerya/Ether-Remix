package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestStepAsideLocalResponseNeverWaitsForCompleteness(t *testing.T) {
	f := newStepAsideFixture()
	s := f.buildStepAside()
	release := make(chan struct{})
	source := s.opts.AiredEpisodes
	s.opts.AiredEpisodes = func(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.RatingKey, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return source(ctx, shows)
	}
	if got := s.compiled(core.WithLocalResponse(context.Background())); got == nil {
		t.Fatal("cold membership snapshot missing")
	} else {
		if _, ok := got.movies[f.heldMovie.Key]; !ok {
			t.Fatal("cold snapshot lost held movie")
		}
		if len(got.fullShows) != 0 {
			t.Fatal("cold snapshot guessed show completeness")
		}
	}
	s.mu.RLock()
	done := s.inflight
	s.mu.RUnlock()
	close(release)
	if done != nil {
		<-done
	}
	if got := s.compiled(core.WithLocalResponse(context.Background())); got == nil {
		t.Fatal("background result not published")
	} else if _, ok := got.fullShows[f.fullShow.Key]; !ok {
		t.Fatal("completed snapshot lost held show")
	}
}
