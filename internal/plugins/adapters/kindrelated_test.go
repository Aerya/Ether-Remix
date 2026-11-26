package adapters_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestKindRelatedStoresItemsAndAsksOnce(t *testing.T) {
	ctx := context.Background()
	h := newKindTestHarness(t, 250*time.Millisecond, nil)
	const pluginID = "community.example.related-music"
	installStudioManifest(t, h.mgr, map[string]any{
		"id": pluginID, "version": "1.0.0", "name": "Related music",
		"ether": map[string]any{
			"protocol": 1, "kind": "content", "summary": "Music with related items",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{"mediaKinds": []any{map[string]any{
				"key": "music", "label": "Music", "media": "music",
				"library": map[string]any{"name": "Music"},
				"levels": []any{
					map[string]any{"key": "artist", "label": "Artist", "role": "artist", "container": true},
					map[string]any{"key": "album", "label": "Album", "role": "album", "container": true},
					map[string]any{"key": "track", "label": "Track", "role": "track", "playable": true},
				},
				"browse":  map[string]any{"sorts": []string{"title"}},
				"sync":    map[string]any{"mode": "push"},
				"related": map[string]any{"similar": true, "continuation": true},
			}}},
		},
	}, nil)
	if _, err := h.items.KindUpsert(ctx, pluginID, "music", []core.KindItem{
		{ID: "ar-1", Level: "artist", Title: "Seed Artist"},
		{ID: "al-1", Level: "album", ParentID: "ar-1", Title: "Seed Album"},
		{ID: "tr-1", Level: "track", ParentID: "al-1", Title: "Seed Track", Playable: true},
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.rt.routes["/ether/v1/kinds/music/related"] = func(req plugins.Request) (plugins.Response, error) {
		calls++
		var in struct{ Purpose string }
		_ = json.Unmarshal(req.Body, &in)
		var body map[string]any
		if in.Purpose == "similar" {
			body = map[string]any{"label": "Fans also like", "items": []any{
				map[string]any{"id": "ar-2", "level": "artist", "title": "Neighbour"},
				map[string]any{"id": "ar-3", "level": "artist", "title": "Cousin"},
			}}
		} else {
			// Tracks arrive with the album and artist they need; order picks what plays.
			body = map[string]any{"items": []any{
				map[string]any{"id": "ar-4", "level": "artist", "title": "Radio Artist"},
				map[string]any{"id": "al-4", "level": "album", "parentId": "ar-4", "title": "Radio Album"},
				map[string]any{"id": "tr-41", "level": "track", "parentId": "al-4", "title": "Second", "playable": true},
				map[string]any{"id": "tr-40", "level": "track", "parentId": "al-4", "title": "First", "playable": true},
			}, "order": []string{"tr-40", "tr-41", "tr-1"}}
		}
		raw, _ := json.Marshal(body)
		return plugins.Response{Status: 200, Body: raw}, nil
	}
	artist, _, _ := h.items.GetByRef(ctx, core.NewItemRef(pluginID, "music", "ar-1"))
	track, _, _ := h.items.GetByRef(ctx, core.NewItemRef(pluginID, "music", "tr-1"))

	label, similar, err := h.catalog.Related(ctx, artist.Key, "similar", 10, 5*time.Second)
	if err != nil || label != "Fans also like" || len(similar) != 2 || similar[0].Title != "Neighbour" {
		t.Fatalf("similar: %q %+v %v", label, similar, err)
	}
	_, next, err := h.catalog.Related(ctx, track.Key, "continuation", 10, 5*time.Second)
	if err != nil || len(next) != 2 || next[0].Title != "First" || next[1].Title != "Second" {
		t.Fatalf("continuation (seed track excluded, order kept): %+v %v", next, err)
	}
	if _, _, err := h.catalog.Related(ctx, artist.Key, "similar", 10, 5*time.Second); err != nil || calls != 2 {
		t.Fatalf("a remembered answer asks the plugin again: calls=%d err=%v", calls, err)
	}
	if _, _, err := h.catalog.Related(ctx, artist.Key, "radio", 10, time.Second); err == nil {
		t.Fatal("undeclared purpose answered")
	}
}
