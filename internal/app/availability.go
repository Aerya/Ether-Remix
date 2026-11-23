package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/availability"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"time"
)

func (a *App) buildAvailability() error {
	s, err := availability.New(availability.Deps{Clustered: a.clustered(), DB: a.db, Config: a.cfg.Read, ConfigGeneration: a.cfg.Generation, Feeds: a.indexers, Catalog: a.catalog, Validator: a.resolver,
		CacheCheck: a.debrid.CheckCached, PackFiles: a.debrid.CachedPackMatcher, Search: func(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
			if a.indexers == nil {
				return nil, nil
			}
			return a.indexers.Search(ctx, req)
		}, Watchlist: func(ctx context.Context) (map[core.RatingKey]bool, error) {
			out := map[core.RatingKey]bool{}
			follow := func(k core.RatingKey) {
				out[k] = true
				if k.Kind() == core.KindEpisode || k.Kind() == core.KindSeason {
					out[core.ShowKey(k.TMDB())] = true
				}
			}
			rows, err := a.db.QueryContext(ctx, `SELECT DISTINCT item_key FROM watchlist_items`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			for rows.Next() {
				var key int64
				if err := rows.Scan(&key); err != nil {
					return nil, err
				}
				follow(core.RatingKey(key))
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
			if a.state != nil {
				recent, err := a.state.RecentlyViewedKeys(ctx, time.Now().Add(-30*24*time.Hour), 200)
				if err != nil {
					return nil, err
				}
				for _, k := range recent {
					follow(k)
				}
			}
			return out, nil
		}, Active: func() bool { return len(a.playback.Sessions()) > 0 },
		Seal: func(raw string) (string, error) { return a.cfg.Seal("availability-release", []byte(raw)) },
		Open: func(raw string) (string, error) {
			b, e := a.cfg.Unseal("availability-release", raw)
			return string(b), e
		},
		Logger: a.log.With("component", "availability"),
		// Section and home loads read the last evidence answer; judging
		// retained feed releases never runs inside a request (#303).
		Async: true,
		// Background requests to one indexer stay at least this far apart.
		Pace: 3 * time.Second,
	})
	a.availability = s
	a.availabilityLive.Store(s)
	if err == nil {
		a.closers = append(a.closers, s.Close)
	}
	return err
}
func (a *App) discoveredReleases(ctx context.Context, t *core.Title) []core.Release {
	if t == nil || pseRuntimeOf(a).associationPending.Load() {
		return nil
	}
	var out []core.Release
	if releasedb.LibraryEnabled() && t != nil {
		out = a.releaseLibrary().Candidates(ctx, t.Key)
	}
	if a.availability != nil {
		out = append(out, a.availability.Candidates(ctx, t)...)
	}

	requests, err := releasedb.AssociationRequestsFor(ctx, a.db, []core.RatingKey{t.Key, core.ShowKey(t.Key.TMDB())})
	if err != nil {
		return nil
	}
	req, ok := releasedb.AssociationRequest(requests, t.Key, 0, 0)
	if !ok {
		return nil
	}
	verified := make([]core.Release, 0, len(out))
	for _, r := range out {
		if strictReferenceAdmission(req, r) == "" {
			verified = append(verified, r)
		}
	}
	return verified
}
func strictReferenceAdmission(req core.SearchRequest, r core.Release) string {
	if req.Type != core.TypeMovie && req.Type != core.TypeEpisode && req.Type != core.TypeShow {
		return ""
	}
	if reason := releaseparse.AssociationReason(req, r); reason != "" {
		return reason
	}
	return releaseparse.ReferenceReason(r)
}
func (a *App) observeRelease(ctx context.Context, t *core.Title, r core.Release) {
	if a.availability != nil {
		a.availability.Observe(ctx, t, r)
	}
}

func (a *App) invalidateRelease(ctx context.Context, id string) {
	if releasedb.LibraryEnabled() {
		a.releaseLibrary().Invalidate(ctx, id)
	}
	if a.availability != nil {
		a.availability.Invalidate(ctx, id)
	}
}

func (a *App) observeSearch(ctx context.Context, t *core.Title, rs []core.Release) {
	if a.availability != nil {
		a.availability.RecordSearch(ctx, t, rs)
	}
}

func (a *App) revealTitleSeason(key core.RatingKey) {
	if a.availability != nil {
		a.availability.RevealTitleSeason(key)
	}
}
