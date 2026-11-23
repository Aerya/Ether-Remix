package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/flows/actions"
	"github.com/braedonsaunders/ether/internal/flows/templates"
	"github.com/braedonsaunders/ether/internal/notify"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestAppRegisterBuiltinFlowActionsAndTemplates(t *testing.T) {
	db := store.OpenTest(t)
	a := &App{
		db:   db,
		opts: Options{DataDir: t.TempDir()},
	}

	reg := flows.NewActionRegistry()
	if err := a.registerBuiltinFlowActions(reg); err != nil {
		t.Fatalf("registerBuiltinFlowActions: %v", err)
	}
	want := []string{
		"notify.send",
		"mqtt.publish",
		"playback.stop_session",
		"http.webhook",
		"viewer.restrict",
		"viewer.unrestrict",
		"library.refresh",
		"library.export",
		"digest.generate",
		"source.refresh",
		"release.pin",
		"release.unpin",
		"flow.set_variable",
	}
	for _, kind := range want {
		if _, ok := reg.Get(kind); !ok {
			t.Fatalf("expected built-in action %q registered", kind)
		}
	}

	// Every curated starter template must instantiate and validate cleanly against
	// App's live registered action registry (0 errors).
	valOpts := flows.ValidateOptions{
		Actions:  reg,
		Triggers: flows.NewTriggerRegistry(),
	}
	for _, tpl := range templates.All() {
		f, err := templates.Instantiate(tpl.ID, nil)
		if err != nil {
			t.Fatalf("template %q instantiate: %v", tpl.ID, err)
		}
		res := flows.ValidateFlow(f, valOpts)
		if !res.Valid {
			t.Fatalf("template %q validation errors: %+v", tpl.ID, res.Errors)
		}
	}
}

func TestAppFlowActionsViewerRestrictionPurge(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	a := &App{db: db}

	deps := a.flowActionDeps()
	if deps.Restrictions == nil {
		t.Fatal("expected RestrictionStore wired from App.db")
	}
	rec := actions.ViewerRestrictionRecord{
		ViewerID:  "42",
		Previous:  actions.ViewerPolicySnapshot{ViewerID: "42", StreamLimit: 3, AccountEnabled: true},
		Applied:   actions.ViewerRestrictionPatch{StreamLimit: intPtrForTest(1)},
		Reason:    "flow test",
		AppliedAt: time.Now().UTC(),
	}
	if err := deps.Restrictions.SaveIfAbsentOrUpdate(ctx, rec); err != nil {
		t.Fatalf("Restrictions.SaveIfAbsentOrUpdate: %v", err)
	}
	got, ok, err := deps.Restrictions.Get(ctx, "42")
	if err != nil || !ok || got.Previous.StreamLimit != 3 {
		t.Fatalf("Restrictions.Get = (%+v, %v, %v)", got, ok, err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PurgeViewer(ctx, tx, 42); err != nil {
		_ = tx.Rollback()
		t.Fatalf("store.PurgeViewer: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	_, ok, err = deps.Restrictions.Get(ctx, "42")
	if err != nil {
		t.Fatalf("Restrictions.Get after purge: %v", err)
	}
	if ok {
		t.Fatal("expected viewer restriction row removed by store.PurgeViewer")
	}
}

func TestAppNotifyEventsBridge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db := store.OpenTest(t)
	ns := notify.New(db, notify.Options{})

	emitted := make(chan notify.Event, 4)
	unsub := ns.Subscribe(func(e notify.Event) {
		emitted <- e
	})
	defer unsub()

	bus, _ := events.NewTestBus(t)
	a := &App{notifications: ns}
	bridge, err := a.startNotifyEventsBridge(ctx, bus, notify.BridgeOptions{})
	if err != nil || bridge == nil {
		t.Fatalf("startNotifyEventsBridge = (%v, %v)", bridge, err)
	}
	defer bridge.Close()

	bus.Publish(events.Envelope{
		ID:      "evt-bridge-1",
		Type:    "library.title.available",
		Payload: json.RawMessage(`{"titleName":"Arrival"}`),
	})

	select {
	case ev := <-emitted:
		if ev.Kind != "title.available" || ev.Message != "Arrival" {
			t.Fatalf("unexpected bridged event: %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for bridged notification event")
	}
}

func TestAppFlowExportItemAndOwnerStopHelpers(t *testing.T) {
	title := core.Title{
		Key:           core.MovieKey(550),
		Type:          core.TypeMovie,
		Title:         "Fight Club",
		Year:          1999,
		Summary:       "Insomniac office worker.",
		ContentRating: "R",
		Studios:       []string{"20th Century Fox"},
		Rating:        8.4,
		Runtime:       139 * time.Minute,
		Genres:        []string{"Drama"},
		Directors:     []core.Person{{Name: "David Fincher"}},
		IDs:           core.IDs{TMDB: 550, IMDB: "tt0137523"},
		PosterPath:    "/poster.jpg",
		BackdropPath:  "/backdrop.jpg",
	}
	item := titleToExportItem(title, "1", "Movies")
	if item.Key != core.MovieKey(550).String() {
		t.Fatalf("Key = %q", item.Key)
	}
	if item.Studio != "20th Century Fox" || len(item.Directors) != 1 || item.Directors[0] != "David Fincher" {
		t.Fatalf("unexpected metadata mapping: %+v", item)
	}
	if item.DurationMS != (139*time.Minute).Milliseconds() || len(item.ExternalIDs) != 2 {
		t.Fatalf("unexpected duration/externalIds: %+v", item)
	}

	// stopOwnerSessionWithReason must be safe even on minimal App with nil frontends.
	a := &App{}
	if err := a.stopOwnerSessionWithReason(context.Background(), "sess-missing", "Stopped by flow"); err != nil {
		t.Fatalf("stopOwnerSessionWithReason: %v", err)
	}
}

func intPtrForTest(v int) *int { return &v }
