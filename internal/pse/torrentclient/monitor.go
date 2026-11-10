package torrentclient

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Monitor surveille les torrents de la catégorie Remix : il prévient
// cross-seed une fois chaque torrent complet, et arrête le partage quand le
// client n'applique pas lui-même les limites (rTorrent). Il ne supprime rien.
type Monitor struct {
	Client   Client
	Settings Settings
	XSeed    *CrossSeed // nil : pas de cross-seed
	Log      *slog.Logger
	Every    time.Duration // 5 min par défaut
	// PerRound borne les envois à cross-seed par passage (3 par défaut) :
	// chacun déclenche des recherches sur les trackers.
	PerRound int

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// Start lance la surveillance en arrière-plan.
func (m *Monitor) Start() {
	m.stop, m.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(m.done)
		every := m.Every
		if every <= 0 {
			every = 5 * time.Minute
		}
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), every)
				m.Round(ctx)
				cancel()
			}
		}
	}()
}

// Close arrête la surveillance et attend la fin du passage en cours.
func (m *Monitor) Close() {
	m.once.Do(func() {
		if m.stop != nil {
			close(m.stop)
			<-m.done
		}
	})
}

func (m *Monitor) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Round fait un passage.
func (m *Monitor) Round(ctx context.Context) {
	ts, err := m.Client.List(ctx, m.Settings.Category)
	if err != nil {
		m.log().Warn("client torrent injoignable", "client", m.Client.Kind(), "err", err)
		return
	}
	per := m.PerRound
	if per <= 0 {
		per = 3
	}
	sent := 0
	for _, t := range ts {
		if ctx.Err() != nil {
			return
		}
		if !t.Done {
			continue
		}
		if t.Active && m.limitReached(t) {
			if err := m.Client.Stop(ctx, t.Hash); err == nil {
				m.log().Info("partage terminé, torrent arrêté (fichiers conservés)", "hash", t.Hash, "ratio", t.Ratio, "seeding", t.Seeding.Round(time.Minute))
			} else if !errors.Is(err, ErrUnsupported) {
				m.log().Warn("arrêt du partage impossible", "hash", t.Hash, "err", err)
			}
		}
		if m.XSeed == nil || t.Notified || sent >= per {
			continue
		}
		if err := m.XSeed.Notify(ctx, t.Hash); err != nil {
			m.log().Warn("cross-seed non prévenu", "hash", t.Hash, "err", err)
			return // cross-seed indisponible : nouvel essai au prochain passage
		}
		sent++
		if err := m.Client.MarkNotified(ctx, t.Hash); err != nil {
			m.log().Warn("envoi à cross-seed non retenu par le client", "hash", t.Hash, "err", err)
		}
		m.log().Info("cross-seed prévenu", "hash", t.Hash)
	}
}

// limitReached suit la règle de qBittorrent : la première limite atteinte
// (ratio ou durée de partage) termine le partage.
func (m *Monitor) limitReached(t Torrent) bool {
	if m.Settings.Ratio > 0 && t.Ratio >= m.Settings.Ratio {
		return true
	}
	return m.Settings.SeedTime > 0 && !t.Finished.IsZero() && t.Seeding >= m.Settings.SeedTime
}
