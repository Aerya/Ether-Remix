package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/state"
)

func TestPluginWatchStateHostAndEvents(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	viewer, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const writer = "test.watch.writer"
	const observer = "test.watch.observer"
	mgr := a.pluginsRT.mgr
	for _, id := range []string{writer, observer} {
		manifest, err := json.Marshal(map[string]any{
			"id": id, "name": "Watch sync", "version": "1.0.0", "description": "Watch sync.",
			"ether": map[string]any{
				"protocol": 1, "kind": "extension", "summary": "Watch sync",
				"runtime":      map[string]any{"kind": "hosted", "entry": "plugin.js"},
				"capabilities": []string{"events"},
				"permissions":  map[string]any{"viewers": map[string]string{"identity": "granted_viewers", "write_watch_state": "granted_viewers"}},
				"contributes":  map[string]any{"events": map[string]any{"subscribe": []string{events.EventViewerWatchScrobbled, plugins.EventPlaybackCompleted}}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		preview, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "studio", Manifest: manifest, Files: map[string][]byte{
			"plugin.js": []byte(`function handle(req) {
				if (req.path === "/set") ether.viewers.setWatchState(req.body);
				return {status: 200, body: {status: "ok", ack: []}};
			}`),
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.Install(ctx, preview, plugins.InstallOptions{Grants: plugins.Grants{Viewers: []int64{viewer.ID}}}); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := a.libraries.UpsertPluginLibrary(ctx, writer, "tracks", libraries.PluginLibraryDecl{Name: "Tracks"})
	if err != nil {
		t.Fatal(err)
	}
	items := a.pluginsRT.pluginitems
	if _, err := items.UpsertKind(ctx, writer, lib.ID, plugins.MediaKindMeta{
		Key: "tracks", Media: string(core.MediaClassMusic),
		Levels: []plugins.KindLevel{{Key: "track", Role: string(core.MediaRoleTrack), Playable: true}},
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := items.UpsertItems(ctx, writer, "tracks", []core.KindItem{{ID: "track-1", Level: "track", Title: "Fixture", Playable: true}}); err != nil {
		t.Fatal(err)
	}
	ref := core.NewItemRef(writer, "tracks", "track-1")
	item, _, err := items.GetByRef(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.HostFor(observer).SetWatchState(ctx, viewer.ID, string(ref), true); !errors.Is(err, plugins.ErrNotFound) {
		t.Fatalf("foreign item write: %v", err)
	}

	revision := func() experience.WatchRevision {
		t.Helper()
		rev, err := experience.ReadWatchRevision(ctx, a.db, viewer.ID, a.state.PendingWatchRevision(viewer.ID))
		if err != nil {
			t.Fatal(err)
		}
		return rev
	}
	before := revision()
	rec := events.NewRecorder(a.EventBus(), events.EventViewerWatchScrobbled)
	defer rec.Close()
	body, err := json.Marshal(map[string]any{"viewerId": viewer.ID, "titleId": ref, "watched": true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := mgr.Call(ctx, writer, plugins.Request{Method: "POST", Path: "/set", Body: body})
	if err != nil || resp.Status != 200 {
		t.Fatalf("setWatchState: status %d, err %v", resp.Status, err)
	}
	ev := rec.WaitFor(t, events.EventViewerWatchScrobbled, 3*time.Second)
	payload := decodePayload[events.ViewerWatchScrobbledPayload](t, ev)
	if ev.Source != "plugin:"+writer || payload.Source != "plugin" || payload.State != "watched" ||
		!payload.Watched || payload.ViewerID != viewer.ID || payload.TitleKey != item.Key.String() || payload.ItemRef != string(ref) {
		t.Fatalf("plugin watch event: %+v", payload)
	}
	if status, err := a.state.Get(ctx, viewer.ID, item.Key); err != nil || !state.IsWatched(status) || revision() == before {
		t.Fatalf("watch state or revision did not change: %+v, %v", status, err)
	}

	// Run the same bridge handler synchronously to verify recipient projection
	// and suppression without relying on the background outbox's tick.
	mgr.HandleBusEvent(ctx, ev)
	readDelivery := func(plugin, eventID string) events.ViewerWatchScrobbledPayload {
		t.Helper()
		var raw []byte
		if err := a.db.QueryRowContext(ctx, "SELECT payload FROM plugins_outbox WHERE plugin=? AND event_id=?", plugin, eventID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var delivery plugins.Event
		if err := json.Unmarshal(raw, &delivery); err != nil {
			t.Fatal(err)
		}
		return decodePayload[events.ViewerWatchScrobbledPayload](t, events.Envelope{Payload: delivery.Payload})
	}
	if got := readDelivery(observer, ev.ID); got.ItemRef != "" || got.ViewerID != viewer.ID || got.State != "watched" {
		t.Fatalf("observer delivery: %+v", got)
	}
	var echoes int
	if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugins_outbox WHERE plugin=?", writer).Scan(&echoes); err != nil || echoes != 0 {
		t.Fatalf("echoes = %d, err %v", echoes, err)
	}

	if err := a.state.Unscrobble(state.WithOrigin(ctx, state.Origin{Surface: "vela"}), viewer.ID, item.Key); err != nil {
		t.Fatal(err)
	}
	manual := rec.WaitForFunc(t, "manual unscrobble", func(ev events.Envelope) bool {
		return ev.Source == "state"
	}, 3*time.Second)
	mgr.HandleBusEvent(ctx, manual)
	if got := readDelivery(writer, manual.ID); got.ItemRef != string(ref) || got.State != "unwatched" || got.Source != "manual" {
		t.Fatalf("owner delivery: %+v", got)
	}

	// A catalogue episode can be written by rating key, and the event
	// carries the series IDs plus the coordinates of its special.
	show := core.ShowKey(124)
	episode := core.EpisodeKey(124, 0, 2)
	ids := core.IDs{TMDB: 124, TVDB: 246, IMDB: "tt1234567"}
	aired := time.Now().AddDate(-1, 0, 0)
	for _, title := range []core.Title{
		{Key: show, Type: core.TypeShow, Title: "Fixture show", IDs: ids, Released: aired},
		{Key: episode, Type: core.TypeEpisode, Title: "Fixture special", ShowKey: show, ShowIDs: ids, Episode: 2, Released: aired},
	} {
		if err := a.catalog.UpsertDetailedTitle(ctx, title); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.HostFor(writer).SetWatchState(ctx, viewer.ID, episode.String(), true); err != nil {
		t.Fatal(err)
	}
	catalogue := rec.WaitForFunc(t, "catalogue scrobble", func(ev events.Envelope) bool {
		return ev.Correlation.TitleID == episode.String()
	}, 3*time.Second)
	got := decodePayload[events.ViewerWatchScrobbledPayload](t, catalogue)
	if got.TMDBID != ids.TMDB || got.TVDBID != ids.TVDB || got.IMDBID != ids.IMDB ||
		got.Season == nil || *got.Season != 0 || got.Episode == nil || *got.Episode != 2 || got.ItemRef != "" {
		t.Fatalf("catalogue delivery: %+v", got)
	}
}
