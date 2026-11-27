package screens

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

type detailHub struct {
	*fakeHub
	spoilerFree bool
}

func (d detailHub) Detail(_ context.Context, _ *core.User, id string) (*core.LiveEventDetail, error) {
	for _, ev := range d.events {
		if ev.ID == id {
			return &core.LiveEventDetail{LiveEvent: ev, Plays: []core.LiveEventPlay{
				{Period: 1, Text: "Goal", At: "2026-11-24T00:10:00Z", Score: map[string]string{"tor": "1", "mtl": "0"}},
				{Period: 2, Text: "Goal", At: "2026-11-24T00:40:00Z", Score: map[string]string{"tor": "2", "mtl": "1"}},
			}}, nil
		}
	}
	return nil, core.ErrNotFound
}

func (d detailHub) SpoilerFree(context.Context, *core.User) (bool, error) { return d.spoilerFree, nil }

func sportsDocsRecord(t *testing.T) (plugins.Record, map[string][]byte) {
	t.Helper()
	files := map[string][]byte{}
	for path, fixture := range map[string]string{"screens/sports.json": "sports-index", "screens/game.json": "sports-game"} {
		raw, err := os.ReadFile("../plugins/screendoc/testdata/" + fixture + ".json")
		if err != nil {
			t.Fatal(err)
		}
		files[path] = raw
	}
	scorebug := `{"schema":1,"kind":"screen","renderer":19,"sources":{"game":{"from":"liveEvent","feed":"sports","id":"{context.event}"}},"tree":{"type":"box"}}`
	files["surfaces/scorebug.json"] = []byte(scorebug)
	man := plugins.Manifest{ID: "org.ether.sports", Version: "2.0.0", Name: "Sports", Ether: &plugins.EtherBlock{
		Capabilities: []plugins.Capability{plugins.CapLiveEvents, plugins.CapScreens},
		Contributes: plugins.Contributes{
			LiveFeeds: []plugins.LiveFeedMeta{{ID: "sports", Title: "Sports", MinIntervalSec: 15, EntityKinds: []string{"team"}}},
			Screens: []plugins.ScreenMeta{{ID: "sports", Title: "Sports", Icon: "trophy",
				Pages: map[string]plugins.DocumentPaths{"index": {"screens/sports.json"}, "game": {"screens/game.json"}}}},
			Surfaces: []plugins.SurfaceMeta{{ID: "scorebug", Presentation: "playerOverlay", Title: "Score", Anchor: "topLeft",
				Setting: &plugins.SurfaceSetting{ID: "scorebug", Label: "Score", Default: true},
				When:    &plugins.SurfaceWhen{Event: &plugins.SurfaceWhenEvent{Feed: "sports"}}, Document: plugins.DocumentPaths{"surfaces/scorebug.json"}}},
		},
	}}
	return plugins.Record{ID: man.ID, Version: man.Version, Enabled: true, Manifest: man}, files
}

func TestDocumentScreensServeAndReadSportsData(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	reg := NewDocRegistry(DocRegistryOptions{})
	rec, files := sportsDocsRecord(t)
	if err := reg.Sync(rec, files, nil); err != nil {
		t.Fatal(err)
	}
	hub := &fakeHub{events: []core.LiveEvent{
		{ID: "org.ether.sports:sports:g1", Feed: "sports", League: core.LiveEventLeague{ID: "nhl", Name: "NHL"}, Start: "2026-11-24T00:00:00Z",
			Status:      core.LiveEventStatus{State: "in", Detail: "2nd · 4:40"},
			Competitors: []core.LiveEventCompetitor{{ID: "mtl", Name: "Montréal Canadiens", Score: "1", EntityID: "mtl"}, {ID: "tor", Name: "Toronto Maple Leafs", Score: "2", EntityID: "tor", Home: true}},
			Probability: &core.LiveEventProbability{Value: 0.82, CompetitorID: "tor"}},
		{ID: "org.ether.sports:sports:g2", Feed: "sports", League: core.LiveEventLeague{ID: "nba", Name: "NBA"}, Start: "2026-11-24T03:00:00Z",
			Status: core.LiveEventStatus{State: "pre"}},
	}}
	svc := NewService(ServiceOptions{Hub: detailHub{fakeHub: hub}, Documents: reg, Now: func() time.Time { return now }})
	viewer := &core.User{ID: 7}
	client := ClientInfo{Frontend: "vela", Renderer: 19, Locale: "en"}

	nav := reg.Navigation(ctx, viewer, client)
	if len(nav) != 1 || nav[0].ID != "sports" || nav[0].Page != "index" || nav[0].Document.Renderer != 19 || !strings.Contains(nav[0].Document.Href, "/vela/v0/screens/sports/pages/index/document?") {
		t.Fatalf("navigation = %+v", nav)
	}
	if got := reg.Navigation(ctx, viewer, ClientInfo{Frontend: "vela", Renderer: 18}); len(got) != 0 {
		t.Fatalf("an older renderer must see no document screen, got %+v", got)
	}
	surfaces := reg.SurfaceEntries(ctx, viewer, client, nil)
	if len(surfaces) != 1 || surfaces[0].ID != "sports.scorebug" || surfaces[0].When.Event.Feed != "org.ether.sports:sports" || !surfaces[0].Setting.Enabled {
		t.Fatalf("surfaces = %+v", surfaces)
	}
	body, tag, err := reg.Document(ctx, viewer, DocumentRequest{Kind: "screen", ID: "sports", Page: "index", ClientInfo: client})
	if err != nil || tag == "" {
		t.Fatalf("document: %v", err)
	}
	var served map[string]any
	if err := json.Unmarshal(body, &served); err != nil || served["screen"] != "sports" || served["presentation"] != "section" {
		t.Fatalf("served document head = %v %v", served["screen"], served["presentation"])
	}

	data, err := svc.Data(ctx, viewer, DataRequest{Kind: "screen", ID: "sports", Page: "index", TZ: "America/Toronto", ClientInfo: client})
	if err != nil {
		t.Fatal(err)
	}
	if data.Live == nil || data.Rev == 0 {
		t.Fatalf("missing live topic or rev: %+v", data)
	}
	if _, lazy := data.Sources["standings"]; lazy {
		t.Fatal("lazy source read on first paint")
	}
	live, _ := json.Marshal(data.Sources["live"])
	if !strings.Contains(string(live), `"$record":"event:org.ether.sports:sports:g1"`) || strings.Contains(string(live), "g2") {
		t.Fatalf("live source = %s", live)
	}
	rec1 := data.Records["event:org.ether.sports:sports:g1"]
	view, ok := rec1.Value.(EventView)
	if !ok || view.Phase != "live" || len(view.Sides) != 2 || view.Probability == nil || view.Probability.Home != 0.82 || view.Probability.SubjectSide != 1 || *view.Sides[1].Share != 82 {
		t.Fatalf("event view = %+v", rec1.Value)
	}

	// The game page reads summary and detail as two records.
	gp, err := svc.Data(ctx, viewer, DataRequest{Kind: "screen", ID: "sports", Page: "game", Params: map[string]string{"event": "event:org.ether.sports:sports:g1"}, ClientInfo: client})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := gp.Records["detail:org.ether.sports:sports:g1"].Value.(DetailView)
	if !ok || len(detail.Plays) != 2 || detail.PlaysNewest[0].Period != 2 {
		t.Fatalf("detail record = %+v", gp.Records["detail:org.ether.sports:sports:g1"])
	}

	// A surface reads its context; a foreign event is not readable.
	if _, err := svc.Data(ctx, viewer, DataRequest{Kind: "surface", ID: "sports.scorebug", Params: map[string]string{"event": "event:org.other:sports:x"}, ClientInfo: client}); err != nil {
		t.Fatal(err)
	} else if res, _ := svc.Data(ctx, viewer, DataRequest{Kind: "surface", ID: "sports.scorebug", Params: map[string]string{"event": "event:org.other:sports:x"}, ClientInfo: client}); res.Errors["game"].Code != "not_found" {
		t.Fatalf("foreign event read = %+v", res)
	}
	if _, err := svc.Data(ctx, viewer, DataRequest{Kind: "screen", ID: "sports", Page: "index", Params: map[string]string{"nope": "1"}, ClientInfo: client}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("unknown parameter = %v", err)
	}

	// The topic reopens exactly this read, for this viewer only.
	tp, err := svc.OpenTopic(viewer, data.Live.Topic)
	if err != nil || tp.Page != "index" || tp.TZ != "America/Toronto" {
		t.Fatalf("topic = %+v %v", tp, err)
	}
	if _, err := svc.OpenTopic(&core.User{ID: 8}, data.Live.Topic); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("another viewer opened the topic: %v", err)
	}

	// Revisions are stable while content is unchanged.
	again, _ := svc.Data(ctx, viewer, DataRequest{Kind: "screen", ID: "sports", Page: "index", TZ: "America/Toronto", ClientInfo: client})
	if again.Records["event:org.ether.sports:sports:g1"].Rev != rec1.Rev {
		t.Fatal("unchanged record changed revision")
	}
}

type fakeCaller struct {
	calls  int
	answer string
}

func (f *fakeCaller) CallScreen(_ context.Context, _ *core.User, _, path string, _ any, _ bool) (int, []byte, error) {
	f.calls++
	return 200, []byte(f.answer), nil
}

type fakeSigner struct{}

func (fakeSigner) Plugin(pluginID, url string) string {
	if strings.HasPrefix(url, "https://cdn.allowed.example/") {
		return "/vela/v0/images/signed"
	}
	return ""
}
func (fakeSigner) Channel(string) string { return "" }

// A plugin's answer reaches the client only through Ether's envelopes: images
// signed on its own hosts, https links, Ether-keyed records; bare URLs and
// unknown envelopes are dropped, and a shared answer is asked for once.
func TestPluginSourceEnvelopes(t *testing.T) {
	ctx := context.Background()
	files := map[string][]byte{"screens/news.json": []byte(`{"schema":1,"kind":"screen","renderer":19,
		"sources":{"front":{"from":"plugin","route":"front","scope":"shared"}},"tree":{"type":"box"}}`)}
	man := plugins.Manifest{ID: "org.example.news", Version: "1.0.0", Name: "News", Ether: &plugins.EtherBlock{
		Capabilities: []plugins.Capability{plugins.CapScreens},
		Contributes:  plugins.Contributes{Screens: []plugins.ScreenMeta{{ID: "news", Title: "News", Pages: map[string]plugins.DocumentPaths{"index": {"screens/news.json"}}}}},
	}}
	reg := NewDocRegistry(DocRegistryOptions{})
	if err := reg.Sync(plugins.Record{ID: man.ID, Version: man.Version, Enabled: true, Manifest: man}, files, nil); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceOptions{Documents: reg, Hub: &fakeHub{}})
	caller := &fakeCaller{answer: `{"value":{"hero":{"$image":"https://cdn.allowed.example/a.jpg"},"evil":{"$image":"https://tracker.example/p.gif"},
		"bare":"https://example.com/x","link":{"$link":"https://example.com/story"},"http":{"$link":"http://example.com"},
		"story":{"$record":{"type":"story","id":"s1"}},"ghost":{"$record":{"type":"story","id":"nope"}},"list":[{"$item":{"kind":"x","id":"1"}},"kept"],
		"$script":"x"},"records":[{"type":"story","id":"s1","value":{"title":"Hi","thumb":{"$image":"https://cdn.allowed.example/t.jpg"}}}],"maxAgeSec":60}`}
	svc.SetPluginCaller(caller)
	client := ClientInfo{Frontend: "vela", Renderer: 19}
	read := func() DataResponse {
		res, err := svc.Data(ctx, &core.User{ID: 3}, DataRequest{Kind: "screen", ID: "news", Page: "index", Images: fakeSigner{}, ClientInfo: client})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := read()
	front, _ := json.Marshal(res.Sources["front"])
	got := map[string]any{}
	_ = json.Unmarshal(front, &got)
	if got["hero"] != "/vela/v0/images/signed" || got["evil"] != nil || got["bare"] != nil || got["http"] != nil || got["ghost"] != nil {
		t.Fatalf("front = %s", front)
	}
	if link, _ := got["link"].(map[string]any); link["href"] != "https://example.com/story" || link["host"] != "example.com" {
		t.Fatalf("link = %v", got["link"])
	}
	if list, _ := got["list"].([]any); len(list) != 1 || list[0] != "kept" {
		t.Fatalf("list = %v", got["list"])
	}
	if _, ok := got["$script"]; ok {
		t.Fatal("unknown $ key survived")
	}
	story, _ := json.Marshal(res.Records["p:story:s1"].Value)
	if !strings.Contains(string(story), `"thumb":"/vela/v0/images/signed"`) {
		t.Fatalf("record = %s", story)
	}
	read()
	if caller.calls != 1 {
		t.Fatalf("shared source asked %d times, want 1", caller.calls)
	}
}

// A Studio preview token swaps in the draft's documents for requests that
// carry it, and only those; drafts are never cached.
func TestStudioPreviewSwapsDraftDocuments(t *testing.T) {
	ctx := context.Background()
	reg := NewDocRegistry(DocRegistryOptions{})
	rec, files := sportsDocsRecord(t)
	if err := reg.Sync(rec, files, nil); err != nil {
		t.Fatal(err)
	}
	draftFiles := map[string][]byte{}
	for k, v := range files {
		draftFiles[k] = v
	}
	draftFiles["screens/sports.json"] = []byte(`{"schema":1,"kind":"screen","renderer":19,"strings":{"x":"Draft"},"tree":{"type":"text","text":"{s:x}"}}`)
	token, _, err := reg.CreatePreview(rec.Manifest, draftFiles, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.User{ID: 7}
	client := ClientInfo{Frontend: "vela", Renderer: 19}
	installed, _, _ := reg.Document(ctx, viewer, DocumentRequest{Kind: "screen", ID: "sports", Page: "index", ClientInfo: client})
	pctx := frontend.WithScreenPreview(ctx, token)
	draft, _, err := reg.Document(pctx, viewer, DocumentRequest{Kind: "screen", ID: "sports", Page: "index", ClientInfo: client})
	if err != nil || !strings.Contains(string(draft), `"Draft"`) || strings.Contains(string(installed), `"Draft"`) {
		t.Fatalf("draft=%v installed has draft=%v err=%v", strings.Contains(string(draft), `"Draft"`), strings.Contains(string(installed), `"Draft"`), err)
	}
	if !reg.IsPreview(pctx) || reg.IsPreview(frontend.WithScreenPreview(ctx, "forged")) {
		t.Fatal("preview token check")
	}
	bad := map[string][]byte{"screens/sports.json": []byte(`{"schema":1,"kind":"screen","renderer":19,"tree":{"type":"image","src":"https://x.example/a.png"}}`), "screens/game.json": files["screens/game.json"], "surfaces/scorebug.json": files["surfaces/scorebug.json"]}
	if _, _, err := reg.CreatePreview(rec.Manifest, bad, nil, time.Now()); err == nil {
		t.Fatal("a draft with a URL compiled")
	}
}

type fakeCards struct{ fakeSigner }

func (fakeCards) TitleCards(_ context.Context, keys []core.RatingKey) map[core.RatingKey]any {
	out := map[core.RatingKey]any{}
	for _, k := range keys {
		if k != 2 { // the viewer may not see title 2
			out[k] = map[string]any{"id": k.String(), "title": "T"}
		}
	}
	return out
}

func (fakeCards) ChannelCards(_ context.Context, ids []string, _ string) ([]string, map[string]any) {
	out := map[string]any{}
	for _, id := range ids {
		out[id] = map[string]any{"id": id}
	}
	return ids, out
}

type fakeFinder struct{}

func (fakeFinder) KindShelf(context.Context, *core.User, string, ItemsQuery) ([]core.RatingKey, error) {
	return []core.RatingKey{1, 2, 3}, nil
}

func (fakeFinder) KindKeys(_ context.Context, _, _ string, ids []string) (map[string]core.RatingKey, error) {
	out := map[string]core.RatingKey{}
	for i, id := range ids {
		out[id] = core.RatingKey(10 + i)
	}
	return out, nil
}

// Library items reach a screen as the frontend's own cards, minus any the
// viewer may not see, through the items source and the $item envelope.
func TestItemsSourceAndItemEnvelope(t *testing.T) {
	ctx := context.Background()
	files := map[string][]byte{"screens/desk.json": []byte(`{"schema":1,"kind":"screen","renderer":19,
		"sources":{"shelf":{"from":"items","kind":"clips","limit":5},"front":{"from":"plugin","route":"front"},"news":{"from":"channels","ids":["ch-1"]}},"tree":{"type":"box"}}`)}
	man := plugins.Manifest{ID: "org.example.desk", Version: "1.0.0", Name: "Desk", Ether: &plugins.EtherBlock{
		Capabilities: []plugins.Capability{plugins.CapScreens},
		Contributes: plugins.Contributes{
			MediaKinds: []plugins.MediaKindMeta{{Key: "clips"}},
			Screens:    []plugins.ScreenMeta{{ID: "desk", Title: "Desk", Pages: map[string]plugins.DocumentPaths{"index": {"screens/desk.json"}}}},
		},
	}}
	reg := NewDocRegistry(DocRegistryOptions{})
	if err := reg.Sync(plugins.Record{ID: man.ID, Version: man.Version, Enabled: true, Manifest: man}, files, nil); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceOptions{Documents: reg, Hub: &fakeHub{}})
	svc.SetItemFinder(fakeFinder{})
	svc.SetPluginCaller(&fakeCaller{answer: `{"value":{"lead":{"$item":{"kind":"clips","id":"c1"}},"foreign":{"$item":{"kind":"other","id":"x"}},"live":{"$channel":"ch-9"}}}`})
	res, err := svc.Data(ctx, &core.User{ID: 3}, DataRequest{Kind: "screen", ID: "desk", Page: "index", Images: fakeCards{}, ClientInfo: ClientInfo{Frontend: "vela", Renderer: 19}})
	if err != nil {
		t.Fatal(err)
	}
	shelf, _ := json.Marshal(res.Sources["shelf"])
	if string(shelf) != `{"items":[{"$record":"item:1"},{"$record":"item:3"}]}` {
		t.Fatalf("shelf = %s", shelf)
	}
	front, _ := json.Marshal(res.Sources["front"])
	if !strings.Contains(string(front), `"lead":{"$record":"item:10"}`) || !strings.Contains(string(front), `"foreign":null`) || !strings.Contains(string(front), `"live":{"$record":"channel:ch-9"}`) {
		t.Fatalf("front = %s", front)
	}
	if _, ok := res.Records["item:2"]; ok {
		t.Fatal("a title the viewer may not see became a record")
	}
	if _, ok := res.Records["channel:ch-1"]; !ok {
		t.Fatalf("channels source records = %v", res.Records)
	}
}
