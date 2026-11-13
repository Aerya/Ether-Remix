package torrentclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Alert prévient le propriétaire (canaux de notification d'Ether).
// critical : alerte ; sinon retour à la normale.
type Alert func(ctx context.Context, critical bool, title, message string)

// Monitor surveille les torrents de la catégorie Remix sur chaque client :
// il prévient cross-seed une fois chaque torrent complet, arrête le partage
// quand le client n'applique pas lui-même les limites (rTorrent) et alerte
// quand la place libre passe sous le seuil. Il ne supprime rien.
type Monitor struct {
	Provider *Provider
	XSeed    *CrossSeed // nil : pas de cross-seed
	Alert    Alert      // nil : journal seulement
	Log      *slog.Logger
	Every    time.Duration // 5 min par défaut
	// PerRound borne les envois à cross-seed par passage (3 par défaut) :
	// chacun déclenche des recherches sur les trackers.
	PerRound int

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// Start lance la surveillance en arrière-plan ; un premier passage a lieu
// tout de suite pour la place libre.
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
		for first := true; ; first = false {
			if !first {
				select {
				case <-m.stop:
					return
				case <-t.C:
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), every)
			m.Round(ctx)
			cancel()
			select {
			case <-m.stop:
				return
			default:
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

// Round fait un passage sur chaque client.
func (m *Monitor) Round(ctx context.Context) {
	per := m.PerRound
	if per <= 0 {
		per = 3
	}
	sent := 0
	for _, s := range m.Provider.Slots {
		if ctx.Err() != nil {
			return
		}
		m.space(ctx, s)
		sent += m.seeding(ctx, s, per-sent)
	}
}

// seeding arrête les partages terminés et prévient cross-seed ; renvoie le
// nombre d'envois à cross-seed.
func (m *Monitor) seeding(ctx context.Context, s *Slot, budget int) int {
	ts, err := s.Client.List(ctx, m.Provider.Settings.Category)
	if err != nil {
		m.log().Warn("client torrent injoignable", "client", s.Conf.Label(), "err", err)
		return 0
	}
	sent := 0
	for _, t := range ts {
		if ctx.Err() != nil {
			return sent
		}
		if !t.Done {
			continue
		}
		if t.Active && limitReached(s.Conf, t) {
			if err := s.Client.Stop(ctx, t.Hash); err == nil {
				m.log().Info("partage terminé, torrent arrêté (fichiers conservés)", "client", s.Conf.Label(), "hash", t.Hash, "ratio", t.Ratio, "seeding", t.Seeding.Round(time.Minute))
			} else if !errors.Is(err, ErrUnsupported) {
				m.log().Warn("arrêt du partage impossible", "client", s.Conf.Label(), "hash", t.Hash, "err", err)
			}
		}
		if m.XSeed == nil || t.Notified || sent >= budget {
			continue
		}
		if err := m.XSeed.Notify(ctx, t.Hash); err != nil {
			m.log().Warn("cross-seed non prévenu", "hash", t.Hash, "err", err)
			return sent // cross-seed indisponible : nouvel essai au prochain passage
		}
		sent++
		if err := s.Client.MarkNotified(ctx, t.Hash); err != nil {
			m.log().Warn("envoi à cross-seed non retenu par le client", "hash", t.Hash, "err", err)
		}
		m.log().Info("cross-seed prévenu", "hash", t.Hash)
	}
	return sent
}

// limitReached suit la règle de qBittorrent : la première limite atteinte
// (ratio ou durée de partage) termine le partage. Les limites posées sur le
// torrent à l'ajout (client, puis tracker) l'emportent sur celles du client.
func limitReached(c ClientConf, t Torrent) bool {
	ratio, seed := c.Ratio, c.SeedTime
	if t.HasLimits {
		ratio, seed = t.RatioLimit, t.SeedLimit
	}
	if ratio > 0 && t.Ratio >= ratio {
		return true
	}
	return seed > 0 && !t.Finished.IsZero() && t.Seeding >= seed
}

// space alerte une fois quand la place passe sous le seuil, puis une fois
// quand elle revient.
func (m *Monitor) space(ctx context.Context, s *Slot) {
	min := m.Provider.Settings.MinFree
	if min <= 0 {
		return
	}
	free, known := m.Provider.FreeSpace(ctx, s)
	if !known {
		return
	}
	low := free < min
	s.mu.Lock()
	was := s.lowSpace
	s.lowSpace = low
	s.mu.Unlock()
	if low == was {
		return
	}
	if low {
		msg := fmt.Sprintf("Plus que %s libres sur le disque de %s (seuil : %s). Remix ne lui envoie plus de téléchargements ; libérez de la place ou baissez le seuil.", GB(free), s.Conf.Label(), GB(min))
		m.log().Warn("client torrent : place insuffisante", "client", s.Conf.Label(), "free", free, "min", min)
		if m.Alert != nil {
			m.Alert(ctx, true, "Client torrent : place insuffisante", msg)
		}
		return
	}
	msg := fmt.Sprintf("%s libres sur le disque de %s : Remix lui envoie de nouveau les téléchargements.", GB(free), s.Conf.Label())
	m.log().Info("client torrent : place revenue", "client", s.Conf.Label(), "free", free)
	if m.Alert != nil {
		m.Alert(ctx, false, "Client torrent : place revenue", msg)
	}
}

// GB écrit une taille en Go, ex. « 12,4 Go ».
func GB(n int64) string {
	v := float64(n) / (1 << 30)
	s := fmt.Sprintf("%.1f Go", v)
	if v >= 100 {
		s = fmt.Sprintf("%.0f Go", v)
	}
	for i := range s {
		if s[i] == '.' {
			return s[:i] + "," + s[i+1:]
		}
	}
	return s
}
