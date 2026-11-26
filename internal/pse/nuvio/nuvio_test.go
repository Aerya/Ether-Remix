package nuvio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeNuvio struct {
	mu        sync.Mutex
	refreshes atomic.Int32
	validRef  string
	calls     map[string][]map[string]any
	progress  []map[string]any
	watched   []map[string]any
	addons    []map[string]any
	status    string
	srv       *httptest.Server
}

func newFake(t *testing.T) *fakeNuvio {
	f := &fakeNuvio{validRef: "r0", calls: map[string][]map[string]any{}, status: "pending"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNuvio) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.URL.Path] = append(f.calls[r.URL.Path], body)
	out := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case strings.HasSuffix(r.URL.Path, "start_tv_login_session"):
		out([]map[string]any{{"code": "ABC123", "web_url": "https://nuvio.tv/tv-login?code=ABC123", "expires_at": time.Now().Add(10 * time.Minute).Format(time.RFC3339), "poll_interval_seconds": 2}})
	case strings.HasSuffix(r.URL.Path, "poll_tv_login_session"):
		out(map[string]any{"status": f.status})
	case strings.HasSuffix(r.URL.Path, "tv-logins-exchange"):
		out(map[string]any{"access_token": "a0", "refresh_token": "r0", "expires_in": 604800, "token_type": "bearer"})
	case strings.HasSuffix(r.URL.Path, "/auth/v1/token"):
		f.refreshes.Add(1)
		time.Sleep(20 * time.Millisecond)
		if body["refresh_token"] != f.validRef { // usage unique
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n := f.refreshes.Load()
		f.validRef = "r" + string(rune('0'+n))
		out(map[string]any{"access_token": "a" + string(rune('0'+n)), "refresh_token": f.validRef, "expires_in": 604800})
	case strings.HasSuffix(r.URL.Path, "sync_pull_profiles"):
		out([]map[string]any{{"id": "u1", "profile_index": 1, "name": "Parents"}, {"id": "u2", "profile_index": 4, "name": "Enfants"}})
	case strings.HasSuffix(r.URL.Path, "sync_pull_watch_progress"):
		out(f.progress)
	case strings.HasSuffix(r.URL.Path, "sync_pull_watched_items"):
		if p, _ := body["p_page"].(float64); p == 1 {
			out(f.watched)
		} else {
			out([]any{})
		}
	case strings.HasSuffix(r.URL.Path, "sync_push_addons"):
		f.addons = nil
		for _, a := range body["p_addons"].([]any) {
			f.addons = append(f.addons, a.(map[string]any))
		}
		out(nil)
	case strings.HasSuffix(r.URL.Path, "/rest/v1/addons"):
		out(f.addons)
	default:
		out(nil)
	}
}

func (f *fakeNuvio) count(suffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for p, c := range f.calls {
		if strings.HasSuffix(p, suffix) {
			n += len(c)
		}
	}
	return n
}

type fakeCatalog struct{}

func (fakeCatalog) KeyFor(_ context.Context, t Target) (core.RatingKey, bool) {
	switch {
	case t.IMDB == "tt0000001" && !t.Series:
		return core.MovieKey(1), true
	case t.IMDB == "tt0000002" && t.Series:
		return core.EpisodeKey(2, t.Season, t.Episode), true
	}
	return 0, false
}

func (fakeCatalog) TargetOf(_ context.Context, k core.RatingKey) (Target, string, bool) {
	switch k.Kind() {
	case core.KindMovie:
		return Target{IMDB: "tt0000001"}, "Film", true
	case core.KindEpisode:
		return Target{IMDB: "tt0000002", Series: true, Season: k.Season(), Episode: k.Episode()}, "Ep", true
	}
	return Target{}, "", false
}

type applied struct {
	key core.RatingKey
	p   frontend.OfflineProgress
	org string
}

// fakeApplier ignore une observation qui n'est pas plus récente que la précédente.
type fakeApplier struct {
	mu   sync.Mutex
	last map[core.RatingKey]time.Time
	got  []applied
}

func (a *fakeApplier) ApplyOfflineProgress(ctx context.Context, _ int64, k core.RatingKey, p frontend.OfflineProgress) (frontend.TimelinePersistenceResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		a.last = map[core.RatingKey]time.Time{}
	}
	if !p.ObservedAt.After(a.last[k]) {
		return frontend.TimelinePersistenceResult{}, nil
	}
	a.last[k] = p.ObservedAt
	a.got = append(a.got, applied{k, p, string(frontend.WatchOriginFrom(ctx).Surface)})
	return frontend.TimelinePersistenceResult{Changed: true}, nil
}

func newService(t *testing.T, f *fakeNuvio) (*Service, *fakeApplier) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ap := &fakeApplier{}
	c := NewClient(f.srv.Client())
	c.Bases = []string{f.srv.URL}
	c.Settings = func() Settings { return Settings{AnonKey: "anon"} }
	return &Service{Store: &Store{DB: db}, Client: c, Catalog: fakeCatalog{}, Applier: ap}, ap
}

func link(t *testing.T, s *Service, f *fakeNuvio) {
	t.Helper()
	ctx := context.Background()
	if err := s.Store.Create(ctx, 7, Tokens{Access: "a0", Refresh: "r0", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Choose(ctx, 7, 4, true, true); err != nil {
		t.Fatal(err)
	}
}

func TestTVLoginFlow(t *testing.T) {
	f := newFake(t)
	s, _ := newService(t, f)
	ctx := context.Background()
	st, err := s.Start(ctx, 7)
	if err != nil || st.Login == nil || st.Login.Code != "ABC123" {
		t.Fatalf("start: %+v %v", st, err)
	}
	now := time.Now()
	s.Now = func() time.Time { return now.Add(3 * time.Second) }
	if st, _ = s.Poll(ctx, 7); st.Status != "pending" || st.Linked {
		t.Fatalf("poll pending: %+v", st)
	}
	f.status = "approved"
	s.Now = func() time.Time { return now.Add(6 * time.Second) }
	st, err = s.Poll(ctx, 7)
	if err != nil || !st.Linked || st.Status != "approved" || len(st.Profiles) != 2 {
		t.Fatalf("poll approved: %+v %v", st, err)
	}
	// l'échange envoie le nonce de la session
	ex := f.calls["/functions/v1/tv-logins-exchange"][0]
	st0 := f.calls["/rest/v1/rpc/start_tv_login_session"][0]
	if ex["device_nonce"] != st0["p_device_nonce"] || len(ex["device_nonce"].(string)) != 64 || st0["p_redirect_base_url"] != "https://nuvio.tv/tv-login" {
		t.Fatalf("nonce: %v %v", ex, st0)
	}
	if err := s.Unlink(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if st, _ = s.State(ctx, 7); st.Linked {
		t.Fatal("encore relié")
	}
}

func TestRefreshOnceUnderConcurrency(t *testing.T) {
	f := newFake(t)
	s, _ := newService(t, f)
	ctx := context.Background()
	if err := s.Store.Create(ctx, 7, Tokens{Access: "old", Refresh: "r0", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := s.Store.accessToken(ctx, s.Client, 7, ""); err != nil || tok != "a1" {
				t.Errorf("token %q %v", tok, err)
			}
		}()
	}
	wg.Wait()
	if n := f.refreshes.Load(); n != 1 {
		t.Fatalf("%d rafraîchissements", n)
	}
	l, _, _ := s.Store.Get(ctx, 7)
	if l.tokens.Refresh != "r1" {
		t.Fatalf("nouveau couple non enregistré : %+v", l.tokens)
	}
	// 401 : un jeton déjà renouvelé par un autre appel n'est pas rafraîchi de nouveau
	if tok, err := s.Store.accessToken(ctx, s.Client, 7, "a0"); err != nil || tok != "a1" || f.refreshes.Load() != 1 {
		t.Fatalf("401 tardif : %q %v", tok, err)
	}
}

func TestHostFallback(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer bad.Close()
	f := newFake(t)
	c := NewClient(nil)
	c.Bases = []string{bad.URL, f.srv.URL}
	c.Settings = func() Settings { return Settings{AnonKey: "anon"} }
	ps, err := c.Profiles(context.Background(), "tok")
	if err != nil || len(ps) != 2 || ps[1].Index != 4 {
		t.Fatalf("%v %v", ps, err)
	}
	c.Bases = []string{"http://127.0.0.1:1", f.srv.URL} // erreur réseau
	if _, err := c.Profiles(context.Background(), "tok"); err != nil {
		t.Fatal(err)
	}
}

func TestIDs(t *testing.T) {
	two, five := 2, 5
	cases := []struct {
		id, typ, vid string
		s, e         *int
		want         Target
		ok           bool
	}{
		{"tt1", "movie", "tt1", nil, nil, Target{IMDB: "tt1"}, true},
		{"tt2", "series", "tt2:2:5", &two, &five, Target{IMDB: "tt2", Series: true, Season: 2, Episode: 5}, true},
		{"tt2", "series", "tt2:3:4", nil, nil, Target{IMDB: "tt2", Series: true, Season: 3, Episode: 4}, true},
		{"tt2", "series", "tt2", nil, nil, Target{}, false},
		{"channel:abc", "tv", "channel:abc", nil, nil, Target{}, false},
		{"kitsu:12", "series", "", &two, &five, Target{}, false},
	}
	for _, c := range cases {
		got, ok := targetOf(c.id, c.typ, c.vid, c.s, c.e)
		if ok != c.ok || got != c.want {
			t.Errorf("%v: %+v %v", c, got, ok)
		}
	}
	if (Target{IMDB: "tt2", Series: true, Season: 1, Episode: 3}).videoID() != "tt2:1:3" {
		t.Fatal("video id")
	}
}

func TestPull(t *testing.T) {
	f := newFake(t)
	s, ap := newService(t, f)
	link(t, s, f)
	base := time.Now().Add(-48 * time.Hour).UnixMilli()
	two, three := 2, 3
	f.progress = []map[string]any{
		{"content_id": "tt0000001", "content_type": "movie", "video_id": "tt0000001", "position": 60000, "duration": 6000000, "last_watched": base},                                             // commencé
		{"content_id": "tt0000002", "content_type": "series", "video_id": "tt0000002:2:3", "season": two, "episode": three, "position": 1800000, "duration": 2700000, "last_watched": base + 1}, // en cours
		{"content_id": "tt0000001", "content_type": "movie", "video_id": "tt0000001", "position": 5900000, "duration": 6000000, "last_watched": base + 5000},                                    // terminé, plus récent
		{"content_id": "chan:1", "content_type": "tv", "video_id": "chan:1", "position": 1, "duration": 2, "last_watched": base},                                                                // chaîne
		{"content_id": "tt9999999", "content_type": "movie", "video_id": "tt9999999", "position": 1, "duration": 2, "last_watched": base},                                                       // inconnu
	}
	f.watched = []map[string]any{{"content_id": "tt0000002", "content_type": "series", "season": 1, "episode": 1, "watched_at": base + 10}}
	sum, err := s.Sync(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Imported != 4 || sum.Unknown != 1 || sum.Skipped != 1 {
		t.Fatalf("résumé : %+v", sum)
	}
	if ap.got[0].org != OriginSurface {
		t.Fatalf("origine : %q", ap.got[0].org)
	}
	// le vu daté est appliqué comme joué
	last := ap.got[len(ap.got)-1]
	if !last.p.Played || last.key != core.EpisodeKey(2, 1, 1) || last.p.ObservedAt.UnixMilli() != base+10 {
		t.Fatalf("vu : %+v", last)
	}
	// deuxième cycle : rien de neuf (élément ancien ignoré, curseur transmis)
	n := len(ap.got)
	sum, _ = s.Sync(context.Background(), 7)
	if sum.Imported != 0 || len(ap.got) != n {
		t.Fatalf("pas idempotent : %+v", sum)
	}
	calls := f.calls["/rest/v1/rpc/sync_pull_watch_progress"]
	if _, ok := calls[0]["p_since_last_watched"]; ok {
		t.Fatal("le premier cycle doit tout importer")
	}
	if calls[1]["p_since_last_watched"].(float64) != float64(base+5000) || calls[1]["p_profile_id"].(float64) != 4 {
		t.Fatalf("curseur : %v", calls[1])
	}
	if f.count("sync_pull_watched_items") != 2 { // une page vide en plus seulement au premier cycle ; pas relu avant une heure
		t.Logf("pages vus : %d", f.count("sync_pull_watched_items"))
	}
	l, _, _ := s.Store.Get(context.Background(), 7)
	if l.Imported != 4 || l.Unknown != 2 || l.LastCycle == 0 {
		t.Fatalf("compteurs : %+v", l)
	}
}

func TestPushNoEcho(t *testing.T) {
	f := newFake(t)
	s, _ := newService(t, f)
	link(t, s, f)
	// événement venu de Nuvio : jamais renvoyé
	s.Handle(state.Event{Kind: state.EventCompleted, UserID: 7, Key: core.MovieKey(1), At: time.Now(), Origin: frontend.WatchOrigin{Surface: OriginSurface}})
	if len(s.pushQueue()) != 0 {
		t.Fatal("écho vers Nuvio")
	}
	// arrêt d'une lecture dans un autre lecteur : progression
	at := time.Now()
	s.Handle(state.Event{Kind: state.EventProgress, UserID: 7, Key: core.EpisodeKey(2, 1, 3), State: "stopped", Duration: 45 * time.Minute, Resume: 10 * time.Minute, ResumeSet: true, At: at, Origin: frontend.WatchOrigin{Surface: "plex"}})
	s.Handle(state.Event{Kind: state.EventCompleted, UserID: 7, Key: core.MovieKey(1), At: at, Origin: frontend.WatchOrigin{Surface: "plex"}})
	s.Handle(state.Event{Kind: state.EventProgress, UserID: 7, Key: core.MovieKey(1), State: "playing", Duration: time.Hour, Resume: time.Minute, ResumeSet: true, At: at}) // en cours de lecture : ignoré
	if len(s.pushQueue()) != 2 {
		t.Fatalf("file : %d", len(s.pushQueue()))
	}
	s.push(context.Background(), <-s.pushQueue())
	s.push(context.Background(), <-s.pushQueue())
	p := f.calls["/rest/v1/rpc/sync_push_watch_progress"][0]
	e := p["p_entries"].([]any)[0].(map[string]any)
	if p["p_profile_id"].(float64) != 4 || e["video_id"] != "tt0000002:1:3" || e["position"].(float64) != 600000 || e["duration"].(float64) != 2700000 || e["season"].(float64) != 1 {
		t.Fatalf("progression : %v", p)
	}
	w := f.calls["/rest/v1/rpc/sync_push_watched_items"][0]["p_items"].([]any)[0].(map[string]any)
	if w["content_id"] != "tt0000001" || w["content_type"] != "movie" || w["watched_at"].(float64) != float64(at.UnixMilli()) {
		t.Fatalf("vu : %v", w)
	}
	if l, _, _ := s.Store.Get(context.Background(), 7); l.Sent != 2 {
		t.Fatalf("envoyés : %d", l.Sent)
	}
	// sens désactivé : rien ne part
	_ = s.Store.SetDirections(context.Background(), 7, true, false)
	s.push(context.Background(), pushItem{user: 7, key: core.MovieKey(1), watched: true, at: at})
	if f.count("sync_push_watched_items") != 1 {
		t.Fatal("envoi malgré push désactivé")
	}
}

func TestInstallAddonKeepsOthers(t *testing.T) {
	f := newFake(t)
	s, _ := newService(t, f)
	link(t, s, f)
	f.addons = []map[string]any{
		{"url": "https://cinemeta.example/manifest.json", "name": "Cinemeta", "enabled": true, "sort_order": 0.0, "profile_id": 4.0},
		{"url": "https://ether.example/old/manifest.json", "name": "Vieux", "enabled": false, "sort_order": 1.0},
		{"url": "https://other.example/x/manifest.json", "name": "Autre", "enabled": true, "sort_order": 2.0},
	}
	ctx := context.Background()
	if err := s.InstallAddon(ctx, 7, "http://ether.example/s/manifest.json"); err == nil {
		t.Fatal("http accepté")
	}
	if err := s.InstallAddon(ctx, 7, "https://ether.example/new/manifest.json"); err != nil {
		t.Fatal(err)
	}
	if len(f.addons) != 3 || f.addons[0]["name"] != "Cinemeta" || f.addons[2]["name"] != "Autre" ||
		f.addons[1]["url"] != "https://ether.example/new/manifest.json" || f.addons[1]["enabled"] != true {
		t.Fatalf("liste : %v", f.addons)
	}
	// nouvel hôte : ajouté en dernier
	if err := s.InstallAddon(ctx, 7, "https://autre-hote.example/m/manifest.json"); err != nil {
		t.Fatal(err)
	}
	if len(f.addons) != 4 || f.addons[3]["sort_order"].(float64) != 3 {
		t.Fatalf("ajout : %v", f.addons)
	}
}

func TestHandlerAPI(t *testing.T) {
	f := newFake(t)
	s, _ := newService(t, f)
	h := s.Handler(func(_ context.Context, id int64) bool { return id == 7 })
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	if w := do("GET", "/9/nuvio", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := do("POST", "/7/nuvio/start", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "ABC123") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := do("POST", "/7/nuvio/sync", ""); w.Code != 422 {
		t.Fatal("sync sans liaison", w.Code)
	}
}

func TestIncomingWriteFromPlayerIsNotEchoed(t *testing.T) {
	f := newFake(t)
	s, _ := newService(t, f)
	now := time.Now()
	s.Now = func() time.Time { return now }
	s.MarkIncoming(7, core.MovieKey(1))
	ev := state.Event{Kind: state.EventCompleted, UserID: 7, Key: core.MovieKey(1), At: now, Origin: frontend.WatchOrigin{Surface: "stremio"}}
	s.Handle(ev)
	if len(s.pushQueue()) != 0 {
		t.Fatal("écho d'une écriture venue de Nuvio")
	}
	s.Now = func() time.Time { return now.Add(time.Minute) } // le marqueur a expiré
	s.Handle(ev)
	if len(s.pushQueue()) != 1 {
		t.Fatal("un vrai événement doit partir")
	}
}
