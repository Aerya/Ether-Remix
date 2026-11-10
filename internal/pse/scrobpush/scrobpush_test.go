package scrobpush

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestValidateAndMask(t *testing.T) {
	for _, bad := range []string{"", "ftp://x/webhooks/jellyfin?api_key=a", "https://scrob/api/other?api_key=a", "https://scrob/api/proxy/webhooks/jellyfin"} {
		if _, err := ValidateURL(bad); err == nil {
			t.Fatalf("accepté : %q", bad)
		}
	}
	const good = "https://scrob.lan/api/proxy/webhooks/jellyfin/3?api_key=SECRETKEY"
	if _, err := ValidateURL(good); err != nil {
		t.Fatal(err)
	}
	if m := MaskURL(good); strings.Contains(m, "SECRETKEY") || !strings.Contains(m, "/webhooks/jellyfin/3") {
		t.Fatalf("masque : %s", m)
	}
}

func TestPusherSendsJellyfinWebhooks(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var mu sync.Mutex
	var got []map[string]any
	scrob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "k1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	t.Cleanup(scrob.Close)

	links := Links{DB: db}
	if err := links.Set(context.Background(), 7, scrob.URL+"/api/proxy/webhooks/jellyfin/1?api_key=k1"); err != nil {
		t.Fatal(err)
	}
	if err := links.Set(context.Background(), 8, scrob.URL+"/api/proxy/webhooks/jellyfin/1?api_key=wrong"); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	p := &Pusher{Links: links, HTTP: scrob.Client(), Now: func() time.Time { return now },
		Lookup: func(context.Context, core.RatingKey) (Item, bool) {
			return Item{Name: "Dune", Year: 2021, RuntimeSecs: 9300}, true
		},
		Users: func(context.Context, int64) string { return "alice" }}
	if err := p.Test(context.Background(), scrob.URL+"/api/proxy/webhooks/jellyfin/1?api_key=k1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Test(context.Background(), scrob.URL+"/api/proxy/webhooks/jellyfin/1?api_key=bad"); err == nil || !strings.Contains(err.Error(), "clé API") {
		t.Fatalf("clé refusée non signalée : %v", err)
	}
	mu.Lock()
	got = nil
	mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	movie := core.MovieKey(438631)
	ep := core.EpisodeKey(1399, 2, 5)
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 7, Key: movie, State: "playing", Offset: time.Minute, Duration: 155 * time.Minute})
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 7, Key: movie, State: "playing", Offset: 2 * time.Minute}) // trop tôt : ignoré
	now = now.Add(2 * time.Minute)
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 7, Key: movie, State: "paused", Offset: 3 * time.Minute})
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 7, Key: movie, State: "stopped", Offset: 150 * time.Minute, Duration: 155 * time.Minute})
	p.Handle(state.Event{Kind: state.EventScrobble, UserID: 7, Key: ep})
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 7, Key: movie, State: "playing", Imported: true})
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 9, Key: movie, State: "playing"}) // sans lien
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 8, Key: movie, State: "playing"}) // clé refusée

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	var types []string
	for _, m := range got {
		types = append(types, m["NotificationType"].(string))
	}
	if strings.Join(types, ",") != "PlaybackStart,PlaybackProgress,PlaybackStop,MarkPlayed" {
		t.Fatalf("événements : %v", types)
	}
	item := got[0]["Item"].(map[string]any)
	if item["Type"] != "Movie" || item["ProviderIds"].(map[string]any)["Tmdb"] != "438631" || item["Name"] != "Dune" {
		t.Fatalf("film : %v", item)
	}
	stop := got[2]["Session"].(map[string]any)["PlayState"].(map[string]any)
	if stop["PositionTicks"].(float64) != float64((150*time.Minute).Nanoseconds()/100) {
		t.Fatalf("position : %v", stop)
	}
	if got[1]["Session"].(map[string]any)["PlayState"].(map[string]any)["IsPaused"] != true {
		t.Fatal("pause non signalée")
	}
	epItem := got[3]["Item"].(map[string]any)
	if epItem["Type"] != "Episode" || epItem["SeriesProviderIds"].(map[string]any)["Tmdb"] != "1399" || epItem["ParentIndexNumber"].(float64) != 2 || epItem["IndexNumber"].(float64) != 5 {
		t.Fatalf("épisode : %v", epItem)
	}
	all, _ := links.All(context.Background())
	if all[7].LastStatus != "ok" || !strings.Contains(all[8].LastStatus, "clé API") {
		t.Fatalf("statuts : %+v", all)
	}
}

func TestHandlerMasksAndKeepsLinks(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	links := Links{DB: db}
	h := Handler(HandlerDeps{Links: links, Viewers: func(context.Context) ([]Viewer, error) {
		return []Viewer{{ID: 1, Name: "Moi"}, {ID: 2, Name: "Enfant"}}, nil
	}})
	do := func(method, path, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	const link = "https://scrob.lan/api/proxy/webhooks/jellyfin/1?api_key=SECRETKEY"
	code, out := do("PUT", "/", `{"links":{"1":"`+link+`"}}`)
	if code != 200 {
		t.Fatalf("PUT %d %v", code, out)
	}
	v := out["viewers"].([]any)
	shown := v[0].(map[string]any)["url"].(string)
	if len(v) != 2 || strings.Contains(shown, "SECRETKEY") || v[1].(map[string]any)["configured"] != false {
		t.Fatalf("vue : %v", v)
	}
	body, _ := json.Marshal(map[string]any{"links": map[string]any{"1": shown}})
	if code, _ := do("PUT", "/", string(body)); code != 200 {
		t.Fatalf("PUT masqué %d", code)
	}
	if all, _ := links.All(context.Background()); all[1].URL != link {
		t.Fatalf("clé perdue : %+v", all[1])
	}
	if code, _ := do("PUT", "/", `{"links":{"2":"https://autre/x?api_key=••••••"}}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("masque sans lien accepté : %d", code)
	}
	if code, _ := do("PUT", "/", `{"links":{"1":null}}`); code != 200 {
		t.Fatal("retrait refusé")
	}
	if all, _ := links.All(context.Background()); len(all) != 0 {
		t.Fatalf("lien non retiré : %+v", all)
	}
}

// Exercise all timeout layers: console handler, pusher context and HTTP client.
func TestHandlerAcceptsSlowWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(16 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := &Pusher{}
	h := Handler(HandlerDeps{Links: Links{DB: db}, Pusher: func() *Pusher { return p }})
	body, _ := json.Marshal(map[string]any{"viewer": 1, "url": server.URL + "/webhooks/jellyfin/1?api_key=test"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(string(body))))
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.OK {
		t.Fatalf("slow webhook failed: %s", rec.Body.String())
	}
}
