package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
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
		c.PSE = func() continuewatch.Settings { return s }
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
		return Continue{State: st, Now: func() time.Time { return pseNow }, PSE: func() continuewatch.Settings { return s },
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
			PSE:            func() continuewatch.Settings { return s },
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

func TestPSEContinueNilSettingsResetsInheritedContext(t *testing.T) {
	with := Continue{PSE: func() continuewatch.Settings { return continuewatch.Defaults() }}
	ctx, run := with.pseContinueBegin(context.Background())
	if run == nil || pseContinueFrom(ctx) != run {
		t.Fatal("réglages non attachés au contexte")
	}
	ctx, run = Continue{}.pseContinueBegin(ctx)
	if run != nil || pseContinueFrom(ctx) != nil {
		t.Fatal("une rangée sans réglages hérite de ceux du contexte")
	}
}

func TestPSEContinueQuantitiesBoundTheWideningLoop(t *testing.T) {
	// 25 séries en cours : sans plafond effectif, la boucle d'élargissement
	// d'InProgress (len(out) < limit) relance appendNext (une lecture d'historique).
	var statuses []core.WatchStatus
	var titles []core.Title
	for i := int64(1); i <= 25; i++ {
		e := core.EpisodeKey(i, 1, 1)
		statuses = append(statuses, core.WatchStatus{Key: e, LastViewedAt: daysAgo(int(i))})
		titles = append(titles, core.Title{Key: e, Type: core.TypeEpisode})
	}
	run := func(maxShows int) int {
		s := continuewatch.Defaults()
		s.MaxShows = maxShows
		st := &pseHistoryState{continueState: continueState{statuses: statuses, values: map[core.RatingKey]core.WatchStatus{}}}
		c := Continue{State: st, Now: func() time.Time { return pseNow }, PSE: func() continuewatch.Settings { return s },
			GetTitles:   func(context.Context, []core.RatingKey) []core.Title { return titles },
			NextEpisode: func(context.Context, core.RatingKey) (*core.Title, error) { return nil, nil },
			Visible: func(_ context.Context, _ *core.User, ti *core.Title) bool {
				return maxShows > 0 || ti.Key.TMDB() <= 2 // base : seuls 2 titres visibles, la boucle d'Ether s'élargit
			},
			SameDayUnavailable: func(context.Context, *core.Title) bool { return false },
			SectionAllowed:     func(*core.User, core.MediaType) bool { return true }}
		got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5)
		if maxShows > 0 && len(got) != maxShows {
			t.Fatalf("quantité non respectée : %d", len(got))
		}
		return len(st.limits)
	}
	if n := run(2); n != 1 {
		t.Fatalf("appendNext relancé %d fois malgré le plafond atteint", n)
	}
	if n := run(0); n < 2 {
		t.Fatalf("sans quantité, la boucle d'élargissement d'Ether doit rester inchangée (%d)", n)
	}
	var c Continue
	if c.pseLimit(core.TypeShow, 7) != 7 {
		t.Fatal("PSE nil : limite modifiée")
	}
	c.PSE = func() continuewatch.Settings { return continuewatch.Settings{MaxMovies: 3, MaxShows: 4} }
	for typ, want := range map[core.MediaType]int{"": 7, core.TypeMovie: 3, core.TypeShow: 4, core.TypeEpisode: 4} {
		if got := c.pseLimit(typ, 10); got != want {
			t.Errorf("pseLimit(%q) = %d, attendu %d", typ, got, want)
		}
	}
	c.PSE = func() continuewatch.Settings { return continuewatch.Settings{MaxMovies: 3} }
	if c.pseLimit("", 10) != 10 || c.pseLimit(core.TypeShow, 10) != 10 {
		t.Fatal("un type sans plafond ne doit pas être borné")
	}
}

func TestPSEContinueFavoritesStillFirst(t *testing.T) {
	fav := pseShow{tmdb: 1, viewed: daysAgo(400), released: daysAgo(500)} // pas de nouvel épisode
	fresh := pseShow{tmdb: 2, viewed: daysAgo(300), released: daysAgo(5)} // nouvel épisode
	c, _ := pseContinueFor([]pseShow{fresh, fav}, pseDefaults())
	c.FavoriteShows = func(context.Context, *core.User) (map[core.RatingKey]bool, error) {
		return map[core.RatingKey]bool{core.ShowKey(fav.tmdb): true}, nil
	}
	got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5)
	if len(got) != 2 || got[0].Key != fav.next() || got[1].Key != fresh.next() {
		t.Fatalf("les favoris passent après le tri, avant le nouvel épisode : %v", pseKeys(got))
	}
}

func TestPSEContinueIgnoresSeasonZero(t *testing.T) {
	a := pseShow{tmdb: 1, viewed: daysAgo(400)}
	b := pseShow{tmdb: 2, viewed: daysAgo(400)}
	c, _ := pseContinueFor([]pseShow{a, b}, pseDefaults())
	special := core.EpisodeKey(a.tmdb, 0, 1)
	real := core.EpisodeKey(a.tmdb, 2, 1)
	onlySpecial := core.EpisodeKey(b.tmdb, 0, 1)
	c.NextEpisode = func(_ context.Context, k core.RatingKey) (*core.Title, error) {
		switch k {
		case a.watched():
			return &core.Title{Key: special, Type: core.TypeEpisode, Season: 0, Episode: 1, Released: daysAgo(3)}, nil
		case special:
			return &core.Title{Key: real, Type: core.TypeEpisode, Season: 2, Episode: 1, Released: daysAgo(4)}, nil
		case b.watched():
			return &core.Title{Key: onlySpecial, Type: core.TypeEpisode, Season: 0, Episode: 1, Released: daysAgo(3)}, nil
		}
		return nil, nil
	}
	got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5)
	if len(got) != 1 || got[0].Key != real {
		t.Fatalf("la saison 0 doit être ignorée : %v", pseKeys(got))
	}
}

func TestPSEContinueBatchedNextEpisodes(t *testing.T) {
	old := pseShow{tmdb: 1, viewed: daysAgo(400), released: daysAgo(10)}
	mid := pseShow{tmdb: 2, viewed: daysAgo(40), released: daysAgo(900)}
	done := pseShow{tmdb: 3, viewed: daysAgo(20)}
	c, _ := pseContinueFor([]pseShow{done, mid, old}, pseDefaults())
	single := c.NextEpisode
	c.NextEpisode = func(context.Context, core.RatingKey) (*core.Title, error) {
		t.Fatal("la lecture par lots doit suffire pour le premier épisode suivant")
		return nil, nil
	}
	calls, asked := 0, 0
	c.NextEpisodes = func(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]*core.Title, error) {
		calls++
		asked += len(keys)
		out := map[core.RatingKey]*core.Title{}
		for _, k := range keys {
			if n, _ := single(ctx, k); n != nil {
				out[k] = n
			}
		}
		return out, nil
	}
	got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5)
	if calls != 1 || asked != 3 || len(got) != 2 || got[0].Key != old.next() || got[1].Key != mid.next() {
		t.Fatalf("lecture par lots : %d appel(s), %d clés, %v", calls, asked, pseKeys(got))
	}
}

func TestPSEContinueShowOnDeckWithRealDeck(t *testing.T) {
	shows := []pseShow{
		{tmdb: 1, viewed: daysAgo(30), released: daysAgo(900)},
		{tmdb: 2, viewed: daysAgo(400), released: daysAgo(10)},
		{tmdb: 3, viewed: daysAgo(20)}, // série terminée : rien à reprendre
	}
	episodes := map[core.RatingKey][]core.Title{}
	states := map[core.RatingKey]core.WatchStatus{}
	byKey := map[core.RatingKey]core.Title{}
	for _, s := range shows {
		eps := []core.Title{{Key: s.watched(), Type: core.TypeEpisode, Season: 1, Episode: 1}}
		states[s.watched()] = core.WatchStatus{Key: s.watched(), ViewCount: 1, LastViewedAt: s.viewed}
		if !s.released.IsZero() {
			eps = append(eps, core.Title{Key: s.next(), Type: core.TypeEpisode, Season: 2, Episode: 1, Released: s.released})
		}
		episodes[core.ShowKey(s.tmdb)] = eps
		for _, e := range eps {
			byKey[e.Key] = e
		}
	}
	deck := &ContinueDeck{
		Load: func(context.Context, []core.RatingKey) (map[core.RatingKey][]core.Title, map[core.RatingKey]core.WatchStatus) {
			return episodes, states
		},
		Hydrate: func(_ context.Context, keys []core.RatingKey) []core.Title {
			var out []core.Title
			for _, k := range keys {
				out = append(out, byKey[k])
			}
			return out
		},
		Unavailable: func(context.Context, *core.Title) bool { return false },
	}
	var prepared int
	c, _ := pseContinueFor(shows, pseDefaults())
	c.ShowOnDeck = deck.Select
	c.PrepareShowOnDeck = func(ctx context.Context, keys []core.RatingKey) { prepared = len(keys); deck.Prepare(ctx, keys) }
	got, _ := c.InProgress(context.Background(), &core.User{ID: 1}, core.TypeShow, 5)
	if len(got) != 2 || got[0].Key != shows[1].next() || got[1].Key != shows[0].next() || prepared != 3 {
		t.Fatalf("ContinueDeck : %v, %d séries préparées", pseKeys(got), prepared)
	}
}
