package experience

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/continuewatch"
)

// Ajout Remix — voir docs/pse/FORK.fr.md. Réglages de « Continuer à regarder » :
// plafond de séries examinées, période du dernier visionnage, fenêtre des
// nouveaux épisodes (classés à leur date de sortie) et quantités. Sans
// Continue.PSE, rien ne change : tous les points d'accroche reçoivent nil et
// retombent sur le comportement d'Ether.

// pseDeckScanLimit borne les séries examinées quand ShowOnDeck est fourni
// (Plex) : Prepare charge d'un coup tous les épisodes et états de ces séries.
const pseDeckScanLimit = 100

type pseContinueKey struct{}

// pseContinue est l'état d'une construction de rangée. Les méthodes acceptent
// un récepteur nil (réglages absents).
type pseContinue struct {
	s     continuewatch.Settings
	now   time.Time
	rank  map[core.RatingKey]time.Time
	taken map[core.MediaType]int
}

// pseContinueBegin lit les réglages et les attache au contexte, que appendNext relit.
func (c Continue) pseContinueBegin(ctx context.Context) (context.Context, *pseContinue) {
	if c.PSE == nil {
		// Une rangée imbriquée sans réglages ne doit pas hériter de ceux d'une autre.
		return context.WithValue(ctx, pseContinueKey{}, (*pseContinue)(nil)), nil
	}
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	r := &pseContinue{s: c.PSE(), now: now, rank: map[core.RatingKey]time.Time{}, taken: map[core.MediaType]int{}}
	return context.WithValue(ctx, pseContinueKey{}, r), r
}

func pseContinueFrom(ctx context.Context) *pseContinue {
	r, _ := ctx.Value(pseContinueKey{}).(*pseContinue)
	return r
}

// pseLimit ramène la limite au plafond des quantités réglées : sans cela,
// la boucle d'élargissement d'InProgress, qui attend len(out) >= limit,
// relancerait appendNext jusqu'à six fois.
func (c Continue) pseLimit(t core.MediaType, limit int) int {
	if c.PSE == nil || limit <= 0 {
		return limit
	}
	s := c.PSE()
	var capacity int
	switch t {
	case core.TypeMovie:
		capacity = s.MaxMovies
	case core.TypeShow, core.TypeEpisode:
		capacity = s.MaxShows
	default:
		if s.MaxMovies <= 0 || s.MaxShows <= 0 {
			return limit // un type sans plafond peut remplir toute la rangée
		}
		capacity = s.MaxMovies + s.MaxShows
	}
	if capacity <= 0 {
		return limit
	}
	return min(limit, capacity)
}

// historyLimit : séries lues dans l'historique (200 chez Ether).
func (r *pseContinue) historyLimit(deck bool) int {
	if r == nil {
		return 200
	}
	return r.scanLimit(deck)
}

// recentCap : séries examinées (min(limite, 50) chez Ether).
func (r *pseContinue) recentCap(limit int, deck bool) int {
	if r == nil {
		return min(max(limit, 1), 50)
	}
	return r.scanLimit(deck)
}

func (r *pseContinue) scanLimit(deck bool) int {
	if deck {
		return min(r.s.ScanLimit, pseDeckScanLimit)
	}
	return r.s.ScanLimit
}

func (r *pseContinue) days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// inWatchWindow : le dernier visionnage est dans la période réglée (0 = illimitée).
func (r *pseContinue) inWatchWindow(viewed time.Time) bool {
	if r.s.WatchDays <= 0 || viewed.IsZero() {
		return true
	}
	return !viewed.Before(r.now.Add(-r.days(r.s.WatchDays)))
}

// filterInProgress écarte les titres en cours regardés hors de la période.
func (r *pseContinue) filterInProgress(titles []core.Title, state map[core.RatingKey]core.WatchStatus) []core.Title {
	if r == nil || r.s.WatchDays <= 0 {
		return titles
	}
	out := titles[:0:0]
	for _, t := range titles {
		if r.inWatchWindow(state[t.Key].LastViewedAt) {
			out = append(out, t)
		}
	}
	return out
}

// admit décide si l'épisode suivant d'une série de l'historique entre dans la
// rangée et enregistre sa date de classement. Un épisode non vu, sorti dans la
// fenêtre des nouveaux épisodes (et pas dans le futur), est classé à
// max(dernier visionnage, sortie) et reste candidat hors de la période de
// visionnage. Un épisode en cours de lecture suit la seule période.
func (r *pseContinue) admit(viewed time.Time, ep *core.Title, st core.WatchStatus) bool {
	if r == nil {
		return true
	}
	if st.ViewOffset <= 0 && r.s.NewDays > 0 {
		rel := ep.Released
		if !rel.IsZero() && !rel.After(r.now) && !rel.Before(r.now.Add(-r.days(r.s.NewDays))) {
			rank := viewed
			if rel.After(rank) {
				rank = rel
			}
			r.rank[ep.Key] = rank
			return true
		}
	}
	return r.inWatchWindow(viewed)
}

// rankOf est la date de classement : celle d'un nouvel épisode, sinon le dernier visionnage.
func (r *pseContinue) rankOf(key core.RatingKey, state map[core.RatingKey]core.WatchStatus) time.Time {
	if r != nil {
		if t, ok := r.rank[key]; ok {
			return t
		}
	}
	return state[key].LastViewedAt
}

// take applique les quantités films et séries, après le tri.
func (r *pseContinue) take(t core.MediaType) bool {
	if r == nil {
		return true
	}
	limit := 0
	switch t {
	case core.TypeMovie:
		limit = r.s.MaxMovies
	case core.TypeEpisode:
		limit = r.s.MaxShows
	}
	if limit > 0 && r.taken[t] >= limit {
		return false
	}
	r.taken[t]++
	return true
}
