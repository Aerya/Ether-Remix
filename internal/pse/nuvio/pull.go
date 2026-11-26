package nuvio

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// watchedEvery : les éléments vus de Nuvio n'ont pas de curseur ; après
// l'import complet, ils sont relus une fois par heure.
const watchedEvery = time.Hour

func (s *Service) cycleLock(user int64) *sync.Mutex {
	m, _ := s.cycles.LoadOrStore(user, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Sync lance maintenant un cycle de lecture Nuvio → Ether pour le spectateur.
func (s *Service) Sync(ctx context.Context, user int64) (Summary, error) {
	m := s.cycleLock(user)
	m.Lock()
	defer m.Unlock()
	l, ok, err := s.Store.Get(ctx, user)
	if err != nil {
		return Summary{}, err
	}
	if !ok || !l.Chosen {
		return Summary{}, userError{"Choisissez d'abord un profil Nuvio"}
	}
	if !l.Pull {
		return Summary{}, nil
	}
	return s.pull(ctx, l), nil
}

func (s *Service) pull(ctx context.Context, l Link) Summary {
	var sum Summary
	cursor, watchedAt := l.cursor, l.watchedAt
	var errText string
	fail := func(err error) {
		if errors.Is(err, ErrUnauthorized) {
			errText = "Reconnexion à Nuvio nécessaire"
		} else {
			errText = "Nuvio injoignable ou en erreur"
		}
		sum.Error = errText
		s.log().Warn("nuvio: cycle interrompu", "user", l.UserID, "err", err)
	}
	ctx = frontend.WithWatchOrigin(ctx, frontend.WatchOrigin{Surface: OriginSurface, ClientID: "ether-remix-pull"})
	now := s.now()

	var prog []ProgressEntry
	err := s.call(ctx, l.UserID, func(tok string) (e error) {
		prog, e = s.Client.PullProgress(ctx, tok, l.ProfileIndex, l.cursor)
		return
	})
	if err != nil {
		fail(err)
		s.Store.report(ctx, l.UserID, sum, cursor, watchedAt, errText)
		return sum
	}
	for _, e := range prog {
		if e.LastWatched > cursor {
			cursor = e.LastWatched
		}
		t, ok := targetOf(e.ContentID, e.ContentType, e.VideoID, e.Season, e.Episode)
		if !ok {
			sum.Skipped++
			continue
		}
		if e.LastWatched <= 0 || e.Position < 0 {
			sum.Skipped++
			continue
		}
		key, ok := s.Catalog.KeyFor(ctx, t)
		if !ok {
			sum.Unknown++
			continue
		}
		res, err := s.Applier.ApplyOfflineProgress(ctx, l.UserID, key, frontend.OfflineProgress{
			Position:   time.Duration(e.Position) * time.Millisecond,
			Duration:   time.Duration(e.Duration) * time.Millisecond,
			ObservedAt: time.UnixMilli(e.LastWatched),
			ReceivedAt: now,
		})
		if err != nil {
			sum.Skipped++
			continue
		}
		if res.Changed {
			sum.Imported++
		}
	}

	if watchedAt == 0 || now.Sub(time.Unix(watchedAt, 0)) >= watchedEvery {
		var watched []WatchedEntry
		err := s.call(ctx, l.UserID, func(tok string) (e error) {
			watched, e = s.Client.PullWatched(ctx, tok, l.ProfileIndex)
			return
		})
		if err != nil {
			fail(err)
		} else {
			for _, e := range watched {
				t, ok := targetOf(e.ContentID, e.ContentType, "", e.Season, e.Episode)
				if !ok || e.WatchedAt <= 0 || (e.ContentType == "series" && !t.Series) {
					sum.Skipped++
					continue
				}
				key, ok := s.Catalog.KeyFor(ctx, t)
				if !ok {
					sum.Unknown++
					continue
				}
				res, err := s.Applier.ApplyOfflineProgress(ctx, l.UserID, key, frontend.OfflineProgress{
					ObservedAt: time.UnixMilli(e.WatchedAt), ReceivedAt: now, Played: true,
				})
				if err != nil {
					sum.Skipped++
					continue
				}
				if res.Changed {
					sum.Imported++
				}
			}
			watchedAt = now.Unix()
		}
	}
	s.Store.report(ctx, l.UserID, sum, cursor, watchedAt, errText)
	return sum
}

// Run lit périodiquement les liaisons actives jusqu'à l'annulation de ctx.
func (s *Service) Run(ctx context.Context) {
	go s.runPush(ctx)
	for {
		wait := LoadSettings().Interval
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		links, err := s.Store.All(ctx)
		if err != nil {
			s.log().Warn("nuvio: liaisons illisibles", "err", err)
			continue
		}
		for _, l := range links {
			if ctx.Err() != nil {
				return
			}
			if l.Chosen && l.Pull {
				_, _ = s.Sync(ctx, l.UserID)
			}
		}
	}
}
