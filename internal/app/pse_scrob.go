package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/scrobview"
	"github.com/braedonsaunders/ether/internal/pse/scrobpush"
)

// buildPSEScrob branche l'historique des spectateurs (lectures terminées
// et notes) sur les bibliothèques « Vus et notés » de PSE
// (PSE_SCROB_VIEWS=on).
func (a *App) buildPSEScrob() {
	if a.db == nil || a.catalog == nil {
		return
	}
	scrobview.SetProvider(scrobview.Provider{
		Watched: func(ctx context.Context, viewerID int64) ([]core.RatingKey, error) {
			if a.state == nil {
				return nil, nil
			}
			seen := map[core.RatingKey]bool{}
			var out []core.RatingKey
			for _, ratings := range []bool{false, true} {
				entries, err := a.state.ExportEntries(ctx, viewerID, ratings)
				if err != nil {
					return nil, err
				}
				for _, e := range entries {
					if !seen[e.Key] {
						seen[e.Key] = true
						out = append(out, e.Key)
					}
				}
			}
			return out, nil
		},
		Titles: a.catalog.CachedTitles,
		Resolve: func(ctx context.Context, key core.RatingKey) (*core.Title, error) {
			return a.catalog.Get(ctx, key)
		},
	})
}

// startPSEScrobPush relaie les lectures vers les webhooks Scrob des
// spectateurs (PSE_SCROB_PUSH=on). Voir docs/pse/FORK.fr.md.
func (a *App) startPSEScrobPush() {
	if a.db == nil || a.state == nil {
		return
	}
	rt := pseRuntimeOf(a)
	p := &scrobpush.Pusher{
		Links:   scrobpush.Links{DB: a.db},
		Enabled: scrobpush.Enabled,
		Log:     a.log.With("component", "pse-scrob"),
		Lookup: func(ctx context.Context, key core.RatingKey) (scrobpush.Item, bool) {
			if a.catalog == nil {
				return scrobpush.Item{}, false
			}
			t, err := a.catalog.Get(ctx, key)
			if err != nil || t == nil {
				return scrobpush.Item{}, false
			}
			it := scrobpush.Item{Name: t.Title, Year: t.Year, RuntimeSecs: int64(t.Runtime.Seconds())}
			if key.Kind() == core.KindEpisode && t.ShowTitle != "" {
				it.Name, it.Year = t.ShowTitle, t.ShowYear
			}
			return it, true
		},
		Users: func(ctx context.Context, id int64) string {
			if a.users == nil {
				return ""
			}
			users, err := a.users.Users(ctx)
			if err != nil {
				return ""
			}
			for _, u := range users {
				if u.ID == id {
					if u.Title != "" {
						return u.Title
					}
					return u.Username
				}
			}
			return ""
		},
	}
	rt.scrob.Store(p)
	ctx, cancel := context.WithCancel(context.Background())
	go p.Run(ctx)
	unsub := a.state.Subscribe(p.Handle)
	a.closers = append(a.closers, func() { unsub(); cancel() })
}
