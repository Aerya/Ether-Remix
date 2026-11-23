package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type minimumPageKey struct{}

const coldDetailWait = 3 * time.Second

// localDetail reads stored pages immediately. Only the first open may fetch
// the one provider payload needed for that page; the rest belongs to one
// deduplicated show-discovery job. Its season flights are shared with demand.
func (c *Catalog) localDetail(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	lookup := ctx
	if key.Kind() == core.KindShow || key.Kind() == core.KindSeason {
		lookup = context.WithValue(ctx, minimumPageKey{}, true)
	}
	stored, err := c.loadByKey(lookup, key)
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	if c.tmdb() == nil {
		return c.StoredTitle(ctx, key)
	}
	if err == nil && (stored.detail || key.Kind() == core.KindEpisode) {
		c.loadSeasonsLater(ctx, key.TMDB(), nil)
		return &stored.t, nil
	}
	refresh := showRefreshKey(key.TMDB())
	if key.Kind() != core.KindShow {
		refresh = seasonRefreshKey(key.TMDB(), key.Season())
	}
	if !c.refreshMissed(refresh) {
		waitctx, cancel := context.WithTimeout(WithReadWaitBudget(ctx, coldDetailWait), coldDetailWait)
		_, _ = c.hydrateValue(waitctx, refresh, coldDetailWait, func(bg context.Context) (any, error) {
			bg, done := context.WithTimeout(bg, coldDetailWait)
			defer done()
			// A queued viewer may win admission just after another flight
			// committed. Recheck the row itself before spending an API call.
			var detailed int
			e := c.db.QueryRowContext(bg, `SELECT detail FROM catalog_titles WHERE key=?`, int64(key)).Scan(&detailed)
			if e == nil && (detailed != 0 || key.Kind() == core.KindEpisode) {
				return nil, nil
			}
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			var items []upsertItem
			if key.Kind() == core.KindShow {
				d, e := c.tmdb().TVDetails(bg, key.TMDB())
				if e != nil {
					return nil, e
				}
				title := c.tvDetailsToTitle(bg, d)
				items = append(items, upsertItem{t: title, o: upsertOpts{detail: true, browsable: c.meetsThreshold(title)}})
				for _, summary := range d.Seasons {
					if !episodeKeyFits(summary.SeasonNumber, 0) {
						continue
					}
					items = append(items, upsertItem{t: seasonToTitle(title, summary), o: upsertOpts{airDates: true}})
				}
			} else {
				// Navigating a season fetches that season only, even if a show-wide job
				// has not reached it. A direct season URL can fill its parent later.
				show := core.Title{Key: core.ShowKey(key.TMDB()), Type: core.TypeShow, IDs: core.IDs{TMDB: key.TMDB()}}
				if parent, e := c.StoredTitle(bg, show.Key); e == nil {
					show = *parent
				}
				d, e := c.tmdb().SeasonDetails(bg, key.TMDB(), key.Season())
				if e != nil {
					return nil, e
				}
				season := seasonToTitle(show, tmdbSeasonSummary(d))
				var episodes []core.Title
				for _, ep := range d.Episodes {
					if !episodeKeyFits(ep.SeasonNumber, ep.EpisodeNumber) {
						continue
					}
					row := episodeToTitle(show, d.Name, ep)
					episodes = append(episodes, row)
				}
				dateSeasonFromAiredEpisodes(&season, episodes, c.now())
				season.ChildCount = len(episodes)
				items = append(items, upsertItem{t: season, o: upsertOpts{detail: true, airDates: true}})
				for _, row := range episodes {
					items = append(items, upsertItem{t: row, o: upsertOpts{airDates: true}})
				}
			}
			return nil, c.writeLocalPage(bg, items)
		})
		cancel()
	}
	c.loadSeasonsLater(ctx, key.TMDB(), nil)
	title, readErr := c.StoredTitle(ctx, key)
	if readErr == nil {
		return title, nil
	}
	if !errors.Is(readErr, core.ErrNotFound) {
		return nil, readErr
	}
	return nil, errHydrating
}

// The discovery job owns show seasons and episodes. A completed season must
// not inherit pending work belonging to the show's other seasons.
func (c *Catalog) reportDetailPending(ctx context.Context, key core.RatingKey) {
	if key.Kind() != core.KindShow && key.Kind() != core.KindSeason {
		return
	}
	c.hydrateMu.Lock()
	running := c.seasonLoading[key.TMDB()] != nil
	seasonFlight := c.hydrating[seasonRefreshKey(key.TMDB(), key.Season())] != nil
	c.hydrateMu.Unlock()
	if key.Kind() == core.KindShow {
		if running {
			core.ReportDetailPending(ctx, key, "seasons", "episodes")
		}
		return
	}
	c.cacheMu.RLock()
	cached, ok := c.titleCache[key]
	missing := ok && cached.gen == c.writeGen.Load() && !cached.st.detail
	c.cacheMu.RUnlock()
	if seasonFlight || (running && missing) {
		core.ReportDetailPending(ctx, key, "episodes")
	}
}
