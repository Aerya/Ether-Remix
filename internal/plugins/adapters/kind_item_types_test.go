package adapters_test

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/libraries"
)

// musicKindManifest builds a two-level album/track content plugin whose
// MediaBrowser presentation is supplied by the caller, so a test can install
// one presentation and update to another.
func musicKindManifest(id, version, collectionType string, itemTypes map[string]any) map[string]any {
	return map[string]any{
		"id":      id,
		"version": version,
		"name":    "Field Recordings",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Field recordings",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []map[string]any{{
					"key":     "recordings",
					"label":   "Field recordings",
					"library": map[string]any{"name": "Field Recordings"},
					"levels": []map[string]any{
						{"key": "album", "label": "Album", "container": true},
						{"key": "track", "label": "Track", "playable": true},
					},
					"fields": []map[string]any{
						{"key": "genre", "type": "string", "label": "Genre", "indexed": true},
					},
					"present": map[string]any{
						"plex": map[string]any{"base": "artist"},
						"mediaBrowser": map[string]any{
							"collectionType": collectionType,
							"itemTypes":      itemTypes,
						},
					},
					"browse": map[string]any{"sorts": []string{"title", "added"}, "facets": []string{"genre"}, "search": true},
					"sync":   map[string]any{"mode": "push"},
				}},
			},
		},
	}
}

func pluginLibraryOf(t *testing.T, h *kindTestHarness, pluginID string) core.Library {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, lib := range mustListLibs(t, h.libs) {
			if lib.Source == core.LibraryPlugin && lib.PluginID == pluginID {
				return lib
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no plugin library for %s", pluginID)
	return core.Library{}
}

// TestKindUpdateReappliesPresentationAndTypesBrowse proves the three update
// behaviours a music plugin needs: a changed declared presentation reaches
// the existing library, IncludeItemTypes-style queries match levels by their
// declared MediaBrowser type, and an unchanged declaration on a later update
// leaves an owner's presentation edit alone.
func TestKindUpdateReappliesPresentationAndTypesBrowse(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()
	const pluginID = "community.example.music"

	installStudioManifest(t, h.mgr, musicKindManifest(pluginID, "1.0.0", "tvshows",
		map[string]any{"album": "Series", "track": "Episode"}), nil)
	lib := pluginLibraryOf(t, h, pluginID)
	if lib.Present.MediaBrowser == nil || lib.Present.MediaBrowser.CollectionType != "tvshows" {
		t.Fatalf("v1 library present = %+v", lib.Present.MediaBrowser)
	}

	if _, err := h.items.KindUpsert(ctx, pluginID, "recordings", []core.KindItem{
		{ID: "alb-1", Level: "album", Title: "Harbour", Year: 2024},
		{ID: "trk-1", Level: "track", ParentID: "alb-1", Index: 3, Title: "Fog Horn", Playable: true},
	}); err != nil {
		t.Fatal(err)
	}

	// Before the update, the declared types are the video ones: a
	// MusicAlbum query matches no level.
	page, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{Recursive: true, UnservedTypes: []string{"musicalbum"}}, "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("v1 musicalbum browse = %+v, %v", page.Items, err)
	}

	// The update changes only the presentation.
	installStudioManifest(t, h.mgr, musicKindManifest(pluginID, "1.1.0", "music",
		map[string]any{"album": "MusicAlbum", "track": "Audio"}), nil)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lib = pluginLibraryOf(t, h, pluginID)
		if lib.Present.MediaBrowser != nil && lib.Present.MediaBrowser.CollectionType == "music" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lib.Present.MediaBrowser == nil || lib.Present.MediaBrowser.CollectionType != "music" ||
		lib.Present.MediaBrowser.ItemTypes["album"] != "MusicAlbum" || lib.Present.MediaBrowser.ItemTypes["track"] != "Audio" {
		t.Fatalf("v2 library present = %+v, want music/MusicAlbum/Audio re-applied", lib.Present.MediaBrowser)
	}

	albums, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{Recursive: true, UnservedTypes: []string{"musicalbum"}}, "", 20)
	if err != nil || len(albums.Items) != 1 || albums.Items[0].MBItemType != "MusicAlbum" || albums.Items[0].Title != "Harbour" {
		t.Fatalf("musicalbum browse = %+v, %v", albums.Items, err)
	}
	tracks, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{Recursive: true, UnservedTypes: []string{"audio"}}, "", 20)
	if err != nil || len(tracks.Items) != 1 {
		t.Fatalf("audio browse = %+v, %v", tracks.Items, err)
	}
	track := tracks.Items[0]
	if track.MBItemType != "Audio" || track.ParentTitle != "Harbour" || track.ParentKey == 0 || track.ParentKey != albums.Items[0].Key {
		t.Fatalf("track = %+v, want Audio under album %v", track, albums.Items[0].Key)
	}

	// Excluding the root's declared type empties the root listing.
	excluded, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{ExcludedTypes: []core.MediaType{"musicalbum"}}, "", 20)
	if err != nil || len(excluded.Items) != 0 {
		t.Fatalf("excluded browse = %+v, %v", excluded.Items, err)
	}

	// An owner's sticky on/off switch survives plugin updates, even when the
	// manifest changes how other apps see the library.
	off := false
	ownerPresent := lib.Present
	ownerPresent.Plex = nil
	if err := h.libs.SetPresentAndOverrides(ctx, lib.ID, ownerPresent, core.PresentationOverrides{Plex: &off}); err != nil {
		t.Fatal(err)
	}
	installStudioManifest(t, h.mgr, musicKindManifest(pluginID, "1.1.1", "music",
		map[string]any{"album": "MusicAlbum", "track": "Audio"}), nil)
	lib = pluginLibraryOf(t, h, pluginID)
	if lib.Present.Plex != nil {
		t.Fatalf("owner switch-off overwritten by update: %+v", lib.Present.Plex)
	}
	if lib.Present.MediaBrowser == nil || lib.Present.MediaBrowser.CollectionType != "music" {
		t.Fatalf("mediabrowser lost on update: %+v", lib.Present.MediaBrowser)
	}
}

// TestFlatPlayableAlbumChildrenAndSongs proves the single-level playable
// MusicAlbum shape (one level, each album one playable item): the album
// lists itself once as an Audio child, and an Audio browse answers with the
// same incarnations, so Jellyfin's album play button and Songs tab work.
func TestFlatPlayableAlbumChildrenAndSongs(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()
	const pluginID = "community.example.flatmusic"

	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Flat Music",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Flat albums",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []map[string]any{{
					"key":     "music",
					"label":   "Music",
					"library": map[string]any{"name": "Music"},
					"levels": []map[string]any{
						{"key": "album", "label": "Album", "playable": true},
					},
					"present": map[string]any{
						"plex": map[string]any{"base": "artist"},
						"mediaBrowser": map[string]any{
							"collectionType": "music",
							"itemTypes":      map[string]any{"album": "MusicAlbum"},
						},
					},
					"browse": map[string]any{"sorts": []string{"title"}, "search": true},
					"sync":   map[string]any{"mode": "push"},
				}},
			},
		},
	}, nil)
	lib := pluginLibraryOf(t, h, pluginID)

	if _, err := h.items.KindUpsert(ctx, pluginID, "music", []core.KindItem{
		{ID: "alb-1", Level: "album", Title: "Discovery", Playable: true},
		{ID: "alb-2", Level: "album", Title: "Unreleased", Playable: false},
	}); err != nil {
		t.Fatal(err)
	}

	albums, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{}, "", 10)
	if err != nil || len(albums.Items) != 2 {
		t.Fatalf("albums = %+v %v", albums.Items, err)
	}
	var album core.Title
	for _, item := range albums.Items {
		if item.Title == "Discovery" {
			album = item
		}
	}
	if album.MBItemType != "MusicAlbum" || album.ChildCount != 1 {
		t.Fatalf("album = %+v, want MusicAlbum with one synthetic child", album)
	}

	kids, err := h.catalog.Children(ctx, album.Key, false, "", 10)
	if err != nil || len(kids.Items) != 1 || !kids.Items[0].SelfChild || kids.Items[0].MBItemType != "Audio" ||
		kids.Items[0].Key != album.Key || kids.Items[0].ParentKey != album.Key || kids.Items[0].ChildCount != 0 {
		t.Fatalf("album children = %+v %v", kids.Items, err)
	}

	songs, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{Recursive: true, UnservedTypes: []string{"audio"}}, "", 10)
	if err != nil || len(songs.Items) != 2 {
		t.Fatalf("songs = %+v %v", songs.Items, err)
	}
	for _, song := range songs.Items {
		if song.Title == "Discovery" && (!song.SelfChild || song.MBItemType != "Audio") {
			t.Fatalf("playable song incarnation = %+v", song)
		}
		if song.Title == "Unreleased" && song.SelfChild {
			t.Fatalf("unplayable album incarnated: %+v", song)
		}
	}

	// Asking for albums and songs together keeps the album typing.
	both, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{Recursive: true, UnservedTypes: []string{"musicalbum", "audio"}}, "", 10)
	if err != nil || len(both.Items) != 2 || both.Items[0].MBItemType != "MusicAlbum" || both.Items[0].SelfChild {
		t.Fatalf("album+audio browse = %+v %v", both.Items, err)
	}
}

// TestKindSyncSelfHealsStaleRowsAndPreservesOwnerSwitches proves that pre-W170
// stale plugin library rows (missing Plex / Vela / MediaBrowser.ItemTypes) are
// healed on sync from kind-level media+levels inference, while owner publish
// overrides (both OFF and explicit ON over manifest default:false) survive
// plugin updates even when the plugin's presentation shape changes.
func TestKindSyncSelfHealsStaleRowsAndPreservesOwnerSwitches(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()
	const pluginID = "community.example.clips"

	// Pre-seed a stale pre-W170 library row missing Plex, Vela, and itemTypes.
	stale, err := h.libs.UpsertPluginLibrary(ctx, pluginID, "clips", libraries.PluginLibraryDecl{
		Name: "Studio Clips",
		Present: core.Presentations{
			MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "movies"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Strip the legacy Vela backfill so the row matches a true pre-W170 DB row
	// (only mediaBrowser.collectionType set, no plex, no vela, no itemTypes,
	// empty present_overrides).
	if err := h.libs.SetPresent(ctx, stale.ID, core.Presentations{
		MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "movies"},
	}); err != nil {
		t.Fatal(err)
	}
	stale, err = h.libs.Get(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Present.Plex != nil || stale.Present.Vela != nil {
		t.Fatalf("expected stale pre-heal row without plex/vela, got %+v", stale.Present)
	}

	makeManifest := func(version string, present map[string]any) map[string]any {
		return map[string]any{
			"id":      pluginID,
			"version": version,
			"name":    "Studio Clips",
			"ether": map[string]any{
				"protocol":     1,
				"kind":         "content",
				"summary":      "Studio video clips",
				"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
				"capabilities": []string{"catalog_provider", "kind_resolver"},
				"contributes": map[string]any{
					"mediaKinds": []map[string]any{{
						"key":     "clips",
						"label":   "Clips",
						"media":   "video",
						"library": map[string]any{"name": "Studio Clips"},
						"levels": []map[string]any{
							{"key": "clip", "label": "Clip", "playable": true},
						},
						"present": present,
						"browse":  map[string]any{"sorts": []string{"title"}, "search": true},
						"sync":    map[string]any{"mode": "push"},
					}},
				},
			},
		}
	}

	// Install v1.0.0: 1-level playable video -> heals stale row via inference,
	// while respecting manifest default:false for Stremio.
	installStudioManifest(t, h.mgr, makeManifest("1.0.0", map[string]any{
		"stremio": map[string]any{"default": false},
	}), nil)

	var lib core.Library
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lib = pluginLibraryOf(t, h, pluginID)
		if lib.Present.Plex != nil && lib.Present.Vela != nil &&
			lib.Present.MediaBrowser != nil && lib.Present.MediaBrowser.ItemTypes["clip"] == "Movie" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lib.Present.Plex == nil || lib.Present.Plex.Base != "movie" {
		t.Fatalf("stale row plex not healed: %+v", lib.Present.Plex)
	}
	if lib.Present.Vela == nil || lib.Present.Vela.Type != "movies" {
		t.Fatalf("stale row vela not healed: %+v", lib.Present.Vela)
	}
	if lib.Present.MediaBrowser == nil || lib.Present.MediaBrowser.ItemTypes["clip"] != "Movie" {
		t.Fatalf("stale row itemTypes not healed: %+v", lib.Present.MediaBrowser)
	}
	if lib.Present.Stremio != nil {
		t.Fatalf("manifest stremio default:false ignored: %+v", lib.Present.Stremio)
	}

	// Owner explicitly turns Plex OFF and Stremio ON (overriding default:false).
	on, off := true, false
	ownerPresent := lib.Present
	ownerPresent.Plex = nil
	ownerPresent.Stremio = &core.StremioPresentation{Type: "movie"}
	if err := h.libs.SetPresentAndOverrides(ctx, lib.ID, ownerPresent, core.PresentationOverrides{
		Plex:    &off,
		Stremio: &on,
	}); err != nil {
		t.Fatal(err)
	}

	// Upgrade to v1.1.0 changing manifest HOW (mediaBrowser -> homevideos,
	// stremio -> channel with default:false): HOW refreshes on the existing row,
	// while sticky owner overrides (Plex OFF, Stremio ON) persist.
	installStudioManifest(t, h.mgr, makeManifest("1.1.0", map[string]any{
		"mediaBrowser": map[string]any{"collectionType": "homevideos"},
		"stremio":      map[string]any{"type": "channel", "default": false},
	}), nil)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lib = pluginLibraryOf(t, h, pluginID)
		if lib.Present.MediaBrowser != nil && lib.Present.MediaBrowser.CollectionType == "homevideos" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lib.Present.MediaBrowser == nil || lib.Present.MediaBrowser.CollectionType != "homevideos" ||
		lib.Present.MediaBrowser.ItemTypes["clip"] != "Movie" {
		t.Fatalf("v1.1.0 HOW not refreshed: %+v", lib.Present.MediaBrowser)
	}
	if lib.Present.Plex != nil {
		t.Fatalf("owner Plex OFF override lost across update: %+v", lib.Present.Plex)
	}
	if lib.Present.Stremio == nil || lib.Present.Stremio.Type != "channel" {
		t.Fatalf("owner Stremio ON override lost or stale HOW across update: %+v", lib.Present.Stremio)
	}
}
