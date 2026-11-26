package app

import (
	"context"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Bounds for app-requested title refreshes ("Refresh metadata" in Plex,
// Jellyfin and Emby apps, "Update Metadata" in Infuse). Each request
// refreshes one title from TMDB. An app that asks again for the same title
// within the cooldown, or while refreshes are already running at the cap,
// is answered without new work.
const (
	titleRefreshTimeout     = 2 * time.Minute
	titleRefreshCooldown    = 10 * time.Minute
	titleRefreshConcurrency = 2
)

type titleRefresher struct {
	refresh func(context.Context, core.RatingKey) error
	now     func() time.Time

	mu      sync.Mutex
	running int
	recent  map[core.RatingKey]time.Time
}

func newTitleRefresher(refresh func(context.Context, core.RatingKey) error) *titleRefresher {
	return &titleRefresher{refresh: refresh, now: time.Now, recent: map[core.RatingKey]time.Time{}}
}

// refreshTarget is the catalogue title an app's refresh names: movies and
// shows themselves, a season or episode its show. Other kinds have no
// catalogue details to refresh.
func refreshTarget(key core.RatingKey) (core.RatingKey, bool) {
	if key.IsPluginKey() {
		return 0, false
	}
	switch key.Kind() {
	case core.KindMovie, core.KindShow:
		return key, true
	case core.KindSeason, core.KindEpisode:
		return core.ShowKey(key.TMDB()), true
	}
	return 0, false
}

func (t *titleRefresher) RefreshTitleMetadata(_ context.Context, viewer *core.Viewer, key core.RatingKey) error {
	if viewer == nil || !viewer.IsOwner {
		return core.ErrNotAvailable
	}
	target, ok := refreshTarget(key)
	if !ok {
		return core.ErrNotFound
	}
	if !t.reserve(target, true) {
		return nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), titleRefreshTimeout)
		defer cancel()
		t.finish(target, t.refresh(ctx, target))
	}()
	return nil
}

func (t *titleRefresher) reserve(key core.RatingKey, cooldown bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for k, at := range t.recent {
		if !at.IsZero() && now.Sub(at) >= titleRefreshCooldown {
			delete(t.recent, k)
		}
	}
	if at, seen := t.recent[key]; (seen && (at.IsZero() || cooldown)) || t.running >= titleRefreshConcurrency {
		return false
	}
	t.recent[key] = time.Time{}
	t.running++
	return true
}

func (t *titleRefresher) finish(key core.RatingKey, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.running--
	if err != nil {
		delete(t.recent, key)
	} else {
		t.recent[key] = t.now()
	}
}

func (t *titleRefresher) run(ctx context.Context, key core.RatingKey) error {
	if !t.reserve(key, false) {
		return core.ErrNotAvailable
	}
	ctx, cancel := context.WithTimeout(ctx, titleRefreshTimeout)
	defer cancel()
	err := t.refresh(ctx, key)
	t.finish(key, err)
	return err
}

var _ frontend.TitleRefresher = (*titleRefresher)(nil)

// appTitleRefresher refreshes and persists one title's catalogue details and
// drops its cached row so the next read serves them.
func (a *App) appTitleRefresher() *titleRefresher {
	a.titleRefreshOnce.Do(func() {
		a.titleRefresh = newTitleRefresher(func(ctx context.Context, key core.RatingKey) error {
			if a.catalog == nil {
				return core.ErrNotAvailable
			}
			paths, err := a.catalog.TitleTreeArtwork(ctx, key)
			if err != nil {
				return err
			}
			if _, err := a.catalog.RefreshTitleTree(ctx, key); err != nil {
				return err
			}
			current, err := a.catalog.TitleTreeArtwork(ctx, key)
			if a.images != nil {
				a.images.InvalidateTMDB(append(paths, current...))
			}
			return err
		})
	})
	return a.titleRefresh
}
