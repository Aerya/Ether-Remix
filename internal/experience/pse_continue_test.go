package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/pse/continuewatch"
)

// Ajout Remix : tests des réglages de « Continuer à regarder ».

var pseNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func daysAgo(n int) time.Time { return pseNow.Add(-time.Duration(n) * 24 * time.Hour) }

// pseHistoryState honore la limite demandée, comme ContinueSeriesHistory.
type pseHistoryState struct {
	continueState
	limits []int
}

func (s *pseHistoryState) History(_ context.Context, _ int64, _ time.Time, limit int) ([]core.HistoryEntry, error) {
	s.limits = append(s.limits, limit)
	return s.history[:min(limit, len(s.history))], nil
}

// pseShow est une série vue (épisode 1x1 terminé) dont le suivant est 2x1.
type pseShow struct {
	tmdb     int64
	viewed   time.Time
	released time.Time // sortie de 2x1 ; zéro = pas d'épisode suivant
}

func (p pseShow) watched() core.RatingKey { return core.EpisodeKey(p.tmdb, 1, 1) }
func (p pseShow) next() core.RatingKey    { return core.EpisodeKey(p.tmdb, 2, 1) }

func pseContinueFor(shows []pseShow, settings *continuewatch.Settings) (Continue, *pseHistoryState) {
	st := &pseHistoryState{continueState: continueState{values: map[core.RatingKey]core.WatchStatus{}}}
	next := map[core.RatingKey]*core.Title{}
	for _, s := range shows { // l'historique est trié du plus récent au plus ancien
		st.history = append(st.history, core.HistoryEntry{Key: s.watched(), Completed: true, ViewedAt: s.viewed})
		st.values[s.watched()] = core.WatchStatus{ViewCount: 1}
		if !s.released.IsZero() {
			next[s.watched()] = &core.Title{Key: s.next(), Type: core.TypeEpisode, Season: 2, Episode: 1, Released: s.released}
		}
	}
	c := Continue{State: st, Now: func() time.Time { return pseNow },
		GetTitles: func(context.Context, []core.RatingKey) []core.Title { return nil },
		NextEpisode: func(_ context.Context, k core.RatingKey) (*core.Title, error) {
			return next[k], nil
		},
		Visible:            func(context.Context, *core.User, *core.Title) bool { return true },
		SameDayUnavailable: func(context.Context, *core.Title) bool { return false },
		SectionAllowed:     func(*core.User, core.MediaType) bool { return true }}
	if settings != nil {
		s := *settings
		c.PSE = func() frontend.PSEContinueSettings { return s }
	}
	return c, st
}

func pseKeys(titles []core.Title) []core.RatingKey {
	out := make([]core.RatingKey, len(titles))
	for i, t := range titles {
		out[i] = t.Key
	}
	return out
}

func pseDefaults() *continuewatch.Settings { d := continuewatch.Defaults(); return &d }

func TestPSEContinueOldShowWithNewSeasonComesFirst(t *testing.T) {
	old := pseShow{tmdb: 1, viewed: daysAgo(400), released: daysAgo(10)}
	recent := pseShow{tmdb: 2, viewed: daysAgo(30), released: daysAgo(500)}
	c, _ := pseContinueFor([]pseShow{recent, old}, pseDefaults())
	got, state := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5)
	if len(got) != 2 || got[0].Key != old.next() || got[1].Key != recent.next() {
		t.Fatalf("ancienne série attendue en tête avec son 1er épisode non vu : %v", pseKeys(got))
	}
	// La date de classement ne modifie pas l'état renvoyé aux clients.
	if !state[old.next()].LastViewedAt.Equal(old.viewed) {
		t.Fatalf("LastViewedAt modifié : %v", state[old.next()].LastViewedAt)
	}
	first, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 1)
	if len(first) != 1 || first[0].Key != old.next() {
		t.Fatalf("la limite du client coupe avant le tri : %v", pseKeys(first))
	}
}

func TestPSEContinueFinishedShowAndFutureEpisodeAbsent(t *testing.T) {
	finished := pseShow{tmdb: 1, viewed: daysAgo(400)} // aucun épisode suivant
	future := pseShow{tmdb: 2, viewed: daysAgo(400), released: pseNow.Add(48 * time.Hour)}
	c, _ := pseContinueFor([]pseShow{finished, future}, pseDefaults())
	if got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5); len(got) != 0 {
		t.Fatalf("série terminée ou épisode futur affiché : %v", pseKeys(got))
	}
}

func TestPSEContinueRightsAndParentalControlStillApply(t *testing.T) {
	old := pseShow{tmdb: 1, viewed: daysAgo(400), released: daysAgo(10)}
	c, _ := pseContinueFor([]pseShow{old}, pseDefaults())
	c.Visible = func(context.Context, *core.User, *core.Title) bool { return false }
	if got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5); len(got) != 0 {
		t.Fatalf("série hors droits affichée : %v", pseKeys(got))
	}
	c.Visible = func(context.Context, *core.User, *core.Title) bool { return true }
	c.SectionAllowed = func(*core.User, core.MediaType) bool { return false }
	if got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5); len(got) != 0 {
		t.Fatalf("section refusée affichée : %v", pseKeys(got))
	}
}

func TestPSEContinueWatchWindow(t *testing.T) {
	s := continuewatch.Defaults()
	s.WatchDays = 100
	inside := pseShow{tmdb: 1, viewed: daysAgo(50), released: daysAgo(300)}
	outside := pseShow{tmdb: 2, viewed: daysAgo(150), released: daysAgo(200)}
	outsideNew := pseShow{tmdb: 3, viewed: daysAgo(150), released: daysAgo(20)} // exception : nouvel épisode
	c, _ := pseContinueFor([]pseShow{inside, outsideNew, outside}, &s)
	got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 10)
	if len(got) != 2 || got[0].Key != outsideNew.next() || got[1].Key != inside.next() {
		t.Fatalf("fenêtre de visionnage : %v", pseKeys(got))
	}
	s.NewDays = 0 // sans fenêtre de nouveaux épisodes, plus d'exception
	c, _ = pseContinueFor([]pseShow{inside, outsideNew, outside}, &s)
	if got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 10); len(got) != 1 || got[0].Key != inside.next() {
		t.Fatalf("fenêtre sans exception : %v", pseKeys(got))
	}
}

func TestPSEContinueWatchWindowAppliesToInProgress(t *testing.T) {
	recent, old := core.MovieKey(1), core.MovieKey(2)
	st := continueState{statuses: []core.WatchStatus{{Key: recent, LastViewedAt: daysAgo(5)}, {Key: old, LastViewedAt: daysAgo(300)}}, values: map[core.RatingKey]core.WatchStatus{}}
	build := func(days int) Continue {
		s := continuewatch.Defaults()
		s.WatchDays = days
		return Continue{State: st, Now: func() time.Time { return pseNow }, PSE: func() frontend.PSEContinueSettings { return s },
			GetTitles: func(context.Context, []core.RatingKey) []core.Title {
				return []core.Title{{Key: recent, Type: core.TypeMovie}, {Key: old, Type: core.TypeMovie}}
			},
			Visible:        func(context.Context, *core.User, *core.Title) bool { return true },
			SectionAllowed: func(*core.User, core.MediaType) bool { return true }}
	}
	if got, _ := build(0).InProgress(context.Background(), &core.User{ID: 1}, core.TypeMovie, 10); len(got) != 2 {
		t.Fatalf("période illimitée : %v", pseKeys(got))
	}
	if got, _ := build(30).InProgress(context.Background(), &core.User{ID: 1}, core.TypeMovie, 10); len(got) != 1 || got[0].Key != recent {
		t.Fatalf("période de 30 jours : %v", pseKeys(got))
	}
}

func TestPSEContinueQuantities(t *testing.T) {
	var statuses []core.WatchStatus
	var titles []core.Title
	for i := int64(1); i <= 3; i++ {
		m, e := core.MovieKey(i), core.EpisodeKey(100+i, 1, 1)
		statuses = append(statuses, core.WatchStatus{Key: m, LastViewedAt: daysAgo(int(i))}, core.WatchStatus{Key: e, LastViewedAt: daysAgo(int(i) + 10)})
		titles = append(titles, core.Title{Key: m, Type: core.TypeMovie}, core.Title{Key: e, Type: core.TypeEpisode})
	}
	build := func(movies, shows int) Continue {
		s := continuewatch.Defaults()
		s.MaxMovies, s.MaxShows = movies, shows
		return Continue{State: continueState{statuses: statuses, values: map[core.RatingKey]core.WatchStatus{}}, Now: func() time.Time { return pseNow },
			PSE:            func() frontend.PSEContinueSettings { return s },
			GetTitles:      func(context.Context, []core.RatingKey) []core.Title { return titles },
			Visible:        func(context.Context, *core.User, *core.Title) bool { return true },
			SectionAllowed: func(*core.User, core.MediaType) bool { return true }}
	}
	count := func(got []core.Title) (m, e int) {
		for _, t := range got {
			if t.Type == core.TypeMovie {
				m++
			} else {
				e++
			}
		}
		return
	}
	got, _ := build(1, 2).InProgress(context.Background(), &core.User{ID: 1}, "", 10)
	if m, e := count(got); m != 1 || e != 2 {
		t.Fatalf("quantités 1 film / 2 séries : %d films, %d séries", m, e)
	}
	got, _ = build(0, 0).InProgress(context.Background(), &core.User{ID: 1}, "", 10)
	if m, e := count(got); m != 3 || e != 3 {
		t.Fatalf("0 = limite du client : %d films, %d séries", m, e)
	}
	got, _ = build(2, 2).InProgress(context.Background(), &core.User{ID: 1}, "", 3)
	if len(got) != 3 {
		t.Fatalf("la limite du client reste prioritaire : %v", pseKeys(got))
	}
}

func TestPSEContinueScanLimit(t *testing.T) {
	var shows []pseShow
	for i := 1; i <= 120; i++ { // du plus récent au plus ancien
		shows = append(shows, pseShow{tmdb: int64(i), viewed: daysAgo(i), released: daysAgo(900)})
	}
	oldest := pseShow{tmdb: 999, viewed: daysAgo(400), released: daysAgo(10)}
	shows = append(shows, oldest)

	// Sans PSE : Ether n'examine que min(limite, 50) séries et ignore l'ancienne.
	c, st := pseContinueFor(shows, nil)
	got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 200)
	if len(got) != 50 || st.limits[0] != 200 {
		t.Fatalf("comportement d'Ether modifié : %d séries, limites %v", len(got), st.limits)
	}
	// Plafond par défaut (200) : toutes les séries sont examinées, l'ancienne remonte (sortie il y a 10 jours).
	c, st = pseContinueFor(shows, pseDefaults())
	got, _ = c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 500)
	if len(got) != 121 || st.limits[0] != 200 {
		t.Fatalf("plafond par défaut : %d séries, limites %v", len(got), st.limits)
	}
	// Classée au 11 novembre (sortie), elle passe devant toutes les séries vues
	// il y a plus de 10 jours, au lieu de finir dernière (400 jours).
	pos := -1
	for i, ti := range got {
		if ti.Key == oldest.next() {
			pos = i
		}
	}
	if pos < 0 || pos > 10 {
		t.Fatalf("la série ancienne devrait remonter parmi les premières : position %d", pos)
	}
	// Plafond réglé à 20 : l'historique lu et les séries examinées suivent.
	s := continuewatch.Defaults()
	s.ScanLimit = 20
	c, st = pseContinueFor(shows, &s)
	got, _ = c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 500)
	if len(got) != 20 || st.limits[0] != 20 {
		t.Fatalf("plafond 20 : %d séries, limites %v", len(got), st.limits)
	}
}

func TestPSEContinueShowOnDeckPath(t *testing.T) {
	old := pseShow{tmdb: 1, viewed: daysAgo(400), released: daysAgo(10)}
	stale := pseShow{tmdb: 2, viewed: daysAgo(400), released: daysAgo(200)}
	var prepared []core.RatingKey
	s := continuewatch.Defaults()
	s.WatchDays = 90
	c, _ := pseContinueFor([]pseShow{old, stale}, &s)
	c.PrepareShowOnDeck = func(_ context.Context, shows []core.RatingKey) { prepared = shows }
	c.ShowOnDeck = func(_ context.Context, show core.RatingKey) (*core.Title, map[core.RatingKey]core.WatchStatus) {
		p := pseShow{tmdb: show.TMDB()}
		rel := old.released
		if p.tmdb == stale.tmdb {
			rel = stale.released
		}
		return &core.Title{Key: p.next(), Type: core.TypeEpisode, Season: 2, Episode: 1, Released: rel}, map[core.RatingKey]core.WatchStatus{}
	}
	got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5)
	if len(got) != 1 || got[0].Key != old.next() || len(prepared) != 2 {
		t.Fatalf("chemin ShowOnDeck : %v, préparés %v", pseKeys(got), prepared)
	}
	// Prepare charge tous les épisodes des séries examinées : plafond réduit.
	r := &pseContinue{s: continuewatch.Settings{ScanLimit: 1000}}
	if r.historyLimit(true) != pseDeckScanLimit || r.historyLimit(false) != 1000 || r.recentCap(3, true) != pseDeckScanLimit {
		t.Fatal("plafond du chemin ShowOnDeck")
	}
}

func TestPSEContinueNilSettingsKeepsEtherLimits(t *testing.T) {
	var r *pseContinue
	if r.historyLimit(false) != 200 || r.recentCap(12, false) != 12 || r.recentCap(400, false) != 50 || r.recentCap(0, false) != 1 {
		t.Fatal("limites d'Ether modifiées")
	}
	if !r.admit(time.Time{}, &core.Title{}, core.WatchStatus{}) || !r.take(core.TypeMovie) {
		t.Fatal("nil doit tout admettre")
	}
}
