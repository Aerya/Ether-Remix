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

// SyncInBackground vérifie la liaison puis lance Sync sans attendre ; un
// cycle déjà en cours pour ce spectateur n'est pas doublé.
func (s *Service) SyncInBackground(ctx context.Context, user int64) error {
	l, ok, err := s.Store.Get(ctx, user)
	if err != nil {
		return err
	}
	if !ok || !l.Chosen {
		return userError{"Choisissez d'abord un profil Nuvio"}
	}
	if !l.Pull {
		return userError{"La récupération Nuvio → Ether est désactivée"}
	}
	m := s.cycleLock(user)
	if !m.TryLock() {
		return nil
	}
	m.Unlock()
	go func() {
		if _, err := s.Sync(ctx, user); err != nil {
			s.log().Warn("nuvio: synchronisation manuelle en échec", "user", user, "err", err)
		}
	}()
	return nil
}

const (
	// firstImportTimeout borne le premier import complet d'un profil.
	firstImportTimeout = 15 * time.Minute
	cycleTimeout       = 2 * time.Minute
	watchedFull  = 24 * time.Hour
)

func (s *Service) pull(ctx context.Context, l Link) Summary {
	timeout := cycleTimeout
	if l.cursor == 0 {
		timeout = firstImportTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var sum Summary
	cur := cursors{progress: l.cursor, watchedAt: l.watchedAt, watchedCur: l.watchedCur, watchedFull: l.watchedFull}
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
	// apply passe par le chemin des lecteurs (droits, contrôle parental) : un
	// titre interdit ou inconnu compte comme « non reconnu ».
	apply := func(t Target, p frontend.OfflineProgress) {
		key, ok := s.Catalog.KeyFor(ctx, t)
		if !ok {
			sum.Unknown++
			return
		}
		if err := s.Applier.Apply(ctx, l.UserID, key, p); err != nil {
			sum.Unknown++
			return
		}
		sum.Imported++
	}

	// Curseur moins 1 ms : rien n'est perdu à la frontière, l'idempotence
	// de l'état absorbe le doublon.
	since := l.cursor
	if since > 0 {
		since--
	}
	var prog []ProgressEntry
	err := s.call(ctx, l.UserID, func(tok string) (e error) {
		prog, e = s.Client.PullProgress(ctx, tok, l.ProfileIndex, since)
		return
	})
	if err != nil {
		fail(err)
		s.Store.report(ctx, l.UserID, sum, cur, errText)
		return sum
	}
	for _, e := range prog {
		if e.LastWatched > cur.progress {
			cur.progress = e.LastWatched
		}
		t, ok := targetOf(e.ContentID, e.ContentType, e.VideoID, e.Season, e.Episode)
		if !ok || e.LastWatched <= 0 || e.Position < 0 {
			sum.Skipped++
			continue
		}
		apply(t, frontend.OfflineProgress{
			Position:   time.Duration(e.Position) * time.Millisecond,
			Duration:   time.Duration(e.Duration) * time.Millisecond,
			ObservedAt: time.UnixMilli(e.LastWatched),
			ReceivedAt: now,
		})
	}

	if cur.watchedAt == 0 || now.Sub(time.Unix(cur.watchedAt, 0)) >= watchedEvery {
		var watched []WatchedEntry
		err := s.call(ctx, l.UserID, func(tok string) (e error) {
			watched, e = s.Client.PullWatched(ctx, tok, l.ProfileIndex)
			return
		})
		if err != nil {
			fail(err)
		} else {
			// Relecture complète au premier cycle puis une fois par jour ;
			// sinon seuls les vus plus récents que le dernier appliqué.
			full := cur.watchedFull == 0 || now.Sub(time.Unix(cur.watchedFull, 0)) >= watchedFull
			floor := cur.watchedCur
			if full {
				floor = 0
			}
			for _, e := range watched {
				if e.WatchedAt > cur.watchedCur {
					cur.watchedCur = e.WatchedAt
				}
				t, ok := targetOf(e.ContentID, e.ContentType, "", e.Season, e.Episode)
				if !ok || e.WatchedAt <= 0 || (e.ContentType == "series" && !t.Series) {
					sum.Skipped++
					continue
				}
				if e.WatchedAt <= floor {
					continue
				}
				apply(t, frontend.OfflineProgress{ObservedAt: time.UnixMilli(e.WatchedAt), ReceivedAt: now, Played: true})
			}
			cur.watchedAt = now.Unix()
			if full {
				cur.watchedFull = now.Unix()
			}
		}
	}
	s.Store.report(ctx, l.UserID, sum, cur, errText)
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
