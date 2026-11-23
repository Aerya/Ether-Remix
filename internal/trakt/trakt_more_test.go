package trakt

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func connect(t *testing.T, c *Client, userID int64) {
	t.Helper()
	if err := c.saveTokens(context.Background(), Tokens{UserID: userID, AccessToken: "acc1", RefreshToken: "ref1", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitDevice(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		seq  []int
		want PollStatus
	}{
		{"authorized after pending+slowdown", []int{400, 429, 200}, PollAuthorized},
		{"denied", []int{400, 418}, PollDenied},
		{"expired", []int{410}, PollExpired},
		{"already used", []int{409}, PollUsed},
		{"invalid", []int{404}, PollInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTrakt{deviceSeq: tc.seq}
			c := newTestClient(t, f)
			c.pollUnit = time.Millisecond
			dc, err := c.StartDevice(ctx)
			if err != nil {
				t.Fatal(err)
			}
			st, err := c.WaitDevice(ctx, 5, dc)
			if err != nil || st != tc.want {
				t.Fatalf("got %v %v, want %v", st, err, tc.want)
			}
			if c.Connected(ctx, 5) != (tc.want == PollAuthorized) {
				t.Fatalf("connected mismatch for %v", st)
			}
		})
	}

	// Never-authorised code times out as expired; ctx cancel is an error.
	f := &fakeTrakt{deviceSeq: []int{400}}
	c := newTestClient(t, f)
	c.pollUnit = time.Millisecond
	st, err := c.WaitDevice(ctx, 1, &DeviceCode{DeviceCode: "d", Interval: 1, ExpiresIn: 5})
	if err != nil || st != PollExpired {
		t.Fatalf("deadline: %v %v", st, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.WaitDevice(cctx, 1, &DeviceCode{DeviceCode: "d", Interval: 1, ExpiresIn: 100}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestScrobbleDedupeAndNoDoubleHistory(t *testing.T) {
	ctx := context.Background()
	f := &fakeTrakt{}
	c := newTestClient(t, f)
	connect(t, c, 1)
	key := core.MovieKey(949)
	ev := func(kind state.EventKind, st string, frac float64, completed bool) {
		c.HandleEvent(ctx, state.Event{Kind: kind, UserID: 1, Key: key, State: st, Fraction: frac, Completed: completed, At: time.Now()})
	}
	ev(state.EventProgress, "playing", 0.10, false)
	ev(state.EventProgress, "playing", 0.11, false) // timeline tick: dropped
	ev(state.EventProgress, "playing", 0.12, false) // dropped
	ev(state.EventProgress, "paused", 0.12, false)
	ev(state.EventProgress, "paused", 0.12, false) // dropped
	ev(state.EventProgress, "playing", 0.13, false)
	ev(state.EventProgress, "stopped", 0.95, true) // completion -> stop 100
	ev(state.EventCompleted, "stopped", 1, true)   // must not push history again
	ev(state.EventProgress, "stopped", 0.96, false)

	f.mu.Lock()
	defer f.mu.Unlock()
	var actions []string
	for _, s := range f.scrobbles {
		actions = append(actions, s["_action"].(string))
	}
	want := []string{"start", "pause", "start", "stop"}
	if len(actions) != len(want) {
		t.Fatalf("actions %v want %v", actions, want)
	}
	for i := range want {
		if actions[i] != want[i] {
			t.Fatalf("actions %v want %v", actions, want)
		}
	}
	last := f.scrobbles[len(f.scrobbles)-1]
	if last["progress"] != 100.0 {
		t.Fatalf("completion stop progress: %v", last["progress"])
	}
	if m, ok := last["movie"].(map[string]any); !ok || m["ids"].(map[string]any)["tmdb"] != 949.0 {
		t.Fatalf("movie ids: %+v", last)
	}
	if len(f.history) != 0 {
		t.Fatalf("completion double-pushed history: %+v", f.history)
	}
}

func TestSyncPushes(t *testing.T) {
	ctx := context.Background()
	f := &fakeTrakt{}
	c := newTestClient(t, f)
	connect(t, c, 1)
	ep := core.EpisodeKey(95396, 2, 4)
	c.HandleEvent(ctx, state.Event{Kind: state.EventScrobble, UserID: 1, Key: ep, At: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)})
	c.HandleEvent(ctx, state.Event{Kind: state.EventUnscrobble, UserID: 1, Key: ep})
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: core.MovieKey(1), Rating: 7.6})
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: core.MovieKey(1), Rating: 0})
	// Shows/seasons don't scrobble and must not error out.
	c.HandleEvent(ctx, state.Event{Kind: state.EventProgress, UserID: 1, Key: core.SeasonKey(1, 1), State: "playing", Fraction: 0.5})

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.history) != 1 {
		t.Fatalf("history: %+v", f.history)
	}
	shows := f.history[0]["shows"].([]any)
	show := shows[0].(map[string]any)
	season := show["seasons"].([]any)[0].(map[string]any)
	epi := season["episodes"].([]any)[0].(map[string]any)
	if show["ids"].(map[string]any)["tmdb"] != 95396.0 || season["number"] != 2.0 || epi["number"] != 4.0 || epi["watched_at"] != "2026-03-01T00:00:00Z" {
		t.Fatalf("nested history body: %+v", f.history[0])
	}
	if len(f.ratings) != 1 {
		t.Fatalf("ratings: %+v", f.ratings)
	}
	mv := f.ratings[0]["movies"].([]any)[0].(map[string]any)
	if mv["rating"] != 8.0 {
		t.Fatalf("rating rounding: %+v", mv)
	}
	if len(f.removed) != 2 { // history remove + rating remove
		t.Fatalf("removes: %+v", f.removed)
	}
}

func TestRefreshOn401AndRevocation(t *testing.T) {
	ctx := context.Background()
	f := &fakeTrakt{rejectToken: "acc1"}
	c := newTestClient(t, f)
	connect(t, c, 1)
	// acc1 is rejected -> refresh -> acc2 -> retry succeeds.
	if _, err := c.Watchlist(ctx, 1, 5); err != nil {
		t.Fatal(err)
	}
	toks, _, _ := c.Tokens(ctx, 1)
	if toks.AccessToken != "acc2" || toks.RefreshToken != "ref2" {
		t.Fatalf("tokens after 401 refresh: %+v", toks)
	}

	// A rejected refresh token disconnects the user.
	f.mu.Lock()
	f.refreshStatus = 401
	f.mu.Unlock()
	_ = c.saveTokens(ctx, Tokens{UserID: 1, AccessToken: "old", RefreshToken: "dead", ExpiresAt: time.Now().Add(-time.Minute)})
	if err := c.Refresh(ctx, 1); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked refresh: %v", err)
	}
	if c.Connected(ctx, 1) {
		t.Fatal("revoked user should be disconnected")
	}
	if err := c.Refresh(ctx, 1); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("unconnected refresh: %v", err)
	}
	// Unconnected users get empty sources, not errors.
	if recs, err := c.Recommendations(ctx, 1, 5); err != nil || recs != nil {
		t.Fatalf("unconnected recs: %v %v", recs, err)
	}
}

func TestPullCacheStaleWhileRevalidate(t *testing.T) {
	ctx := context.Background()
	f := &fakeTrakt{}
	c := newTestClient(t, f)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	c.now = func() time.Time { return time.Unix(0, now.Load()) }
	c.ttl = time.Minute
	connect(t, c, 1)

	if _, err := c.Recommendations(ctx, 1, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Recommendations(ctx, 1, 5); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/recommendations/movies"); n != 1 {
		t.Fatalf("fresh cache should serve: %d calls", n)
	}
	// Stale: served immediately, refreshed in the background.
	now.Add(int64(2 * time.Minute))
	recs, err := c.Recommendations(ctx, 1, 5)
	if err != nil || len(recs) != 2 {
		t.Fatalf("stale serve: %v %v", recs, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.count("/recommendations/movies") < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.count("/recommendations/movies") != 2 {
		t.Fatal("background refresh did not run")
	}
	// Interleaved movie/show order.
	if recs[0].Type != core.TypeMovie || recs[1].Type != core.TypeShow {
		t.Fatalf("interleave: %+v", recs)
	}
	// A watched event invalidates the user's cache.
	for c.cmuRefreshing() {
		time.Sleep(time.Millisecond)
	}
	c.HandleEvent(ctx, state.Event{Kind: state.EventScrobble, UserID: 1, Key: core.MovieKey(949), At: time.Now()})
	if _, err := c.Recommendations(ctx, 1, 5); err != nil {
		t.Fatal(err)
	}
	if n := f.count("/recommendations/movies"); n != 3 {
		t.Fatalf("invalidation: %d calls", n)
	}
}

// cmuRefreshing reports whether any cache entry has a refresh in flight.
func (c *Client) cmuRefreshing() bool {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	for _, e := range c.cache {
		if e.refreshing {
			return true
		}
	}
	return false
}

func TestEventWorkerWithState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeTrakt{}
	c := newTestClient(t, f)
	connect(t, c, 1)
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := state.New(db, state.Options{})
	unsub := c.SubscribeTo(st)
	defer unsub()
	done := make(chan struct{})
	go func() { c.Start(ctx); close(done) }()

	if err := st.Scrobble(ctx, 1, core.MovieKey(949)); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress(ctx, 1, core.MovieKey(10), 30*time.Minute, time.Hour, "playing"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		ok := len(f.history) == 1 && len(f.scrobbles) == 1
		f.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.mu.Lock()
	h, s := len(f.history), len(f.scrobbles)
	f.mu.Unlock()
	if h != 1 || s != 1 {
		t.Fatalf("worker pushes: history=%d scrobbles=%d", h, s)
	}
	c.Close()
	<-done
}

func TestQueueBoundAndPacing(t *testing.T) {
	f := &fakeTrakt{}
	c := newTestClient(t, f)
	c.qsize = 3
	db, _ := store.OpenMemory(context.Background())
	defer db.Close()
	st := state.New(db, state.Options{})
	c.SubscribeTo(st)
	for i := 0; i < 10; i++ {
		_ = st.Rate(context.Background(), 1, core.MovieKey(int64(i+1)), 5)
	}
	c.evMu.Lock()
	n, first := len(c.queue), c.queue[0].Key
	c.evMu.Unlock()
	if n != 3 || first != core.MovieKey(8) {
		t.Fatalf("queue bound: n=%d first=%v", n, first)
	}
}

type stubCatalog struct {
	core.Catalog
	titles map[core.RatingKey]*core.Title
}

func (s *stubCatalog) Get(_ context.Context, key core.RatingKey) (*core.Title, error) {
	if t, ok := s.titles[key]; ok {
		return t, nil
	}
	return nil, core.ErrNotFound
}

func TestShowAndSeasonKeysNeverSentAsMovies(t *testing.T) {
	ctx := context.Background()
	f := &fakeTrakt{}
	c := newTestClient(t, f)
	connect(t, c, 1)

	showKey := core.ShowKey(1396)
	seasonKey := core.SeasonKey(1396, 2)
	epKey := core.EpisodeKey(1396, 2, 5)
	collKey := core.CollectionKey(10)
	localKey := core.LocalKey(42)

	c.catalog = &stubCatalog{titles: map[core.RatingKey]*core.Title{
		showKey: {
			Key: showKey, Type: core.TypeShow, Title: "Breaking Bad", Year: 2008,
			IDs: core.IDs{TMDB: 1396, IMDB: "tt0903747", TVDB: 81189},
		},
		seasonKey: {
			// Real catalogue season rows hold the TMDB season id in IDs and the
			// show's ids in ShowIDs; Trakt must use ShowIDs, never IDs.
			Key: seasonKey, Type: core.TypeSeason, Title: "Season 2", Season: 2,
			IDs: core.IDs{TMDB: 3573}, ShowKey: showKey,
			ShowIDs:   core.IDs{TMDB: 1396, IMDB: "tt0903747", TVDB: 81189},
			ShowTitle: "Breaking Bad", ShowYear: 2008,
		},
		epKey: {
			Key: epKey, Type: core.TypeEpisode, Title: "Breakage", Season: 2, Episode: 5,
			IDs: core.IDs{TMDB: 62090}, ShowKey: showKey,
			ShowIDs:   core.IDs{TMDB: 1396, IMDB: "tt0903747", TVDB: 81189},
			ShowTitle: "Breaking Bad",
		},
		collKey:  {Key: collKey, Type: core.TypeMovie, Title: "Collection", IDs: core.IDs{TMDB: 10}},
		localKey: {Key: localKey, Type: core.TypeMovie, Title: "Local Film", IDs: core.IDs{TMDB: 999}},
	}}

	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	// Parent-touch scrobble/unscrobble/progress events for shows, seasons,
	// collections, and local keys must never hit Trakt (and must never be
	// wrapped as movies).
	for _, k := range []core.RatingKey{showKey, seasonKey, collKey, localKey} {
		c.HandleEvent(ctx, state.Event{Kind: state.EventScrobble, UserID: 1, Key: k, At: at})
		c.HandleEvent(ctx, state.Event{Kind: state.EventUnscrobble, UserID: 1, Key: k, At: at})
		c.HandleEvent(ctx, state.Event{Kind: state.EventProgress, UserID: 1, Key: k, State: "playing", Fraction: 0.5, At: at})
	}
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: collKey, Rating: 8})
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: localKey, Rating: 8})

	f.mu.Lock()
	if len(f.history) != 0 || len(f.scrobbles) != 0 || len(f.removed) != 0 || len(f.ratings) != 0 {
		t.Fatalf("unexpected Trakt calls for non-playable/non-ratable keys: history=%+v scrobbles=%+v removed=%+v ratings=%+v",
			f.history, f.scrobbles, f.removed, f.ratings)
	}
	f.mu.Unlock()

	// Episode scrobble pushes nested show/season/episode.
	c.HandleEvent(ctx, state.Event{Kind: state.EventScrobble, UserID: 1, Key: epKey, At: at})
	// Show and season ratings push under "shows", never "movies".
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: showKey, Rating: 9})
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: seasonKey, Rating: 8})
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: showKey, Rating: 0})
	c.HandleEvent(ctx, state.Event{Kind: state.EventRated, UserID: 1, Key: seasonKey, Rating: 0})

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.history) != 1 {
		t.Fatalf("expected 1 episode history push, got %+v", f.history)
	}
	if _, hasMovies := f.history[0]["movies"]; hasMovies {
		t.Fatalf("episode history pushed as movies: %+v", f.history[0])
	}
	if len(f.ratings) != 2 {
		t.Fatalf("expected 2 ratings (show + season), got %+v", f.ratings)
	}
	showRating := f.ratings[0]
	if _, hasMovies := showRating["movies"]; hasMovies {
		t.Fatalf("show rating pushed as movies: %+v", showRating)
	}
	sr := showRating["shows"].([]any)[0].(map[string]any)
	if sr["ids"].(map[string]any)["tmdb"] != 1396.0 || sr["rating"] != 9.0 {
		t.Fatalf("unexpected show rating payload: %+v", showRating)
	}
	seasonRating := f.ratings[1]
	if _, hasMovies := seasonRating["movies"]; hasMovies {
		t.Fatalf("season rating pushed as movies: %+v", seasonRating)
	}
	snr := seasonRating["shows"].([]any)[0].(map[string]any)
	if snr["ids"].(map[string]any)["tmdb"] != 1396.0 {
		t.Fatalf("season rating must use show TMDB id, not season TMDB id: %+v", seasonRating)
	}
	snEntry := snr["seasons"].([]any)[0].(map[string]any)
	if snEntry["number"] != 2.0 || snEntry["rating"] != 8.0 {
		t.Fatalf("unexpected season rating entry: %+v", seasonRating)
	}
	if len(f.removed) != 2 {
		t.Fatalf("expected 2 rating removals, got %+v", f.removed)
	}
	for i, rem := range f.removed {
		if _, hasMovies := rem["movies"]; hasMovies {
			t.Fatalf("removal %d pushed as movies: %+v", i, rem)
		}
		if _, hasShows := rem["shows"]; !hasShows {
			t.Fatalf("removal %d missing shows key: %+v", i, rem)
		}
	}

	// Import queue must ignore show/season scrobble parent-touches.
	c.enqueueImport(state.Event{Kind: state.EventScrobble, UserID: 1, Key: showKey, At: at, Imported: true})
	c.enqueueImport(state.Event{Kind: state.EventScrobble, UserID: 1, Key: seasonKey, At: at, Imported: true})
	var queued int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trakt_import_queue`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 0 {
		t.Fatalf("import queue accepted %d show/season scrobble events, want 0", queued)
	}
}
