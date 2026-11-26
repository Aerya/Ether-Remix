package nuvio

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
)

type pushItem struct {
	user    int64
	key     core.RatingKey
	watched bool
	pos     time.Duration
	dur     time.Duration
	at      time.Time
}

func (s *Service) pushQueue() chan pushItem {
	s.qOnce.Do(func() { s.queue = make(chan pushItem, 512) })
	return s.queue
}

// Handle est abonné aux événements d'état : il met en file ce qui doit partir
// vers Nuvio (le réseau est traité plus tard, par Run). Une écriture venue de
// Nuvio n'est jamais renvoyée.
func (s *Service) Handle(ev state.Event) {
	if ev.Origin.Surface == OriginSurface || ev.Account || ev.Imported || s.incoming(ev.UserID, ev.Key) {
		return
	}
	it := pushItem{user: ev.UserID, key: ev.Key, at: ev.At, pos: ev.Resume, dur: ev.Duration}
	switch ev.Kind {
	case state.EventCompleted, state.EventScrobble:
		it.watched = true
	case state.EventProgress:
		if ev.Completed || !ev.ResumeSet || ev.Resume <= 0 || ev.Duration <= 0 {
			return
		}
		if ev.State != "stopped" && ev.State != "paused" && !ev.Offline {
			return
		}
	default:
		return
	}
	if k := ev.Key.Kind(); k != core.KindMovie && k != core.KindEpisode {
		return
	}
	if it.at.IsZero() {
		it.at = s.now()
	}
	select {
	case s.pushQueue() <- it:
	default: // Nuvio lent : on abandonne le plus récent plutôt que de bloquer Ether
	}
}

// runPush vide la file vers Nuvio.
func (s *Service) runPush(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-s.pushQueue():
			s.push(ctx, it)
		}
	}
}

func (s *Service) push(ctx context.Context, it pushItem) {
	l, ok, err := s.Store.Get(ctx, it.user)
	if err != nil || !ok || !l.Chosen || !l.Push {
		return
	}
	t, title, ok := s.Catalog.TargetOf(ctx, it.key)
	if !ok {
		return
	}
	ms := it.at.UnixMilli()
	err = s.call(ctx, it.user, func(tok string) error {
		if it.watched {
			return s.Client.PushWatched(ctx, tok, l.ProfileIndex, []map[string]any{t.watchedItem(title, ms)})
		}
		return s.Client.PushProgress(ctx, tok, l.ProfileIndex, []map[string]any{t.progressEntry(it.pos.Milliseconds(), it.dur.Milliseconds(), ms)})
	})
	if err != nil {
		s.log().Debug("nuvio: envoi impossible", "user", it.user, "err", err)
		s.Store.addSent(ctx, it.user, 0, "Envoi vers Nuvio impossible")
		return
	}
	s.Store.addSent(ctx, it.user, 1, "")
}

// incomingTTL : durée pendant laquelle les événements d'une écriture venue de
// Nuvio (ressources player et library de l'addon) restent ignorés.
const incomingTTL = 15 * time.Second

// MarkIncoming signale qu'une écriture sur key vient de Nuvio : les événements
// qu'elle provoque ne lui sont pas renvoyés.
func (s *Service) MarkIncoming(user int64, key core.RatingKey) {
	s.inMu.Lock()
	defer s.inMu.Unlock()
	now := s.now()
	if s.in == nil || len(s.in) > 1024 {
		s.in = map[incomingKey]time.Time{}
	}
	s.in[incomingKey{user, key}] = now.Add(incomingTTL)
}

type incomingKey struct {
	user int64
	key  core.RatingKey
}

func (s *Service) incoming(user int64, key core.RatingKey) bool {
	s.inMu.Lock()
	defer s.inMu.Unlock()
	until, ok := s.in[incomingKey{user, key}]
	return ok && s.now().Before(until)
}
