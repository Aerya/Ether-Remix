package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// TestBranchingLevelsVideoChildrenTabsAndPlayback exercises the W289 generic
// branching-level contract end-to-end with an artist -> album -> track plus
// artist -> concert (video) fixture kind:
//   - artist ChildCounts grouped by child role/level
//   - video child projected as movie/video title with artist parent and resume enabled
//   - SQL-pushed ChildrenFiltered (?role= / ?level=)
//   - KindLeaves on artist stays audio-only (excludes video concerts)
//   - video child playback resolves as video (not audio MIME/category) even inside a music library
//   - per-tab recommended rows with scoped Continue Watching (concerts tab vs music recommended tab)
func TestBranchingLevelsVideoChildrenTabsAndPlayback(t *testing.T) {
	concertBytes := []byte("0123456789fake-mp4-video-concert-payload")
	trackBytes := []byte("0123456789fake-flac-audio-track-payload")
	mediaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/concert.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			http.ServeContent(w, r, "concert.mp4", time.Unix(1700000000, 0), bytes.NewReader(concertBytes))
		case "/track.flac":
			w.Header().Set("Content-Type", "audio/flac")
			http.ServeContent(w, r, "track.flac", time.Unix(1700000000, 0), bytes.NewReader(trackBytes))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mediaSrv.Close()

	client := &http.Client{Transport: hostRewriteTransport{target: mediaSrv.URL}}
	h := newKindTestHarness(t, 250*time.Millisecond, client)
	ctx := context.Background()

	const pluginID = "community.example.music-concerts"
	h.rt.routes["/ether/v1/kinds/music/resolve"] = func(req plugins.Request) (plugins.Response, error) {
		var rr core.KindResolveRequest
		_ = json.Unmarshal(req.Body, &rr)
		var out core.KindResolveResponse
		if strings.HasPrefix(rr.Item.ID, "con-") {
			out = core.KindResolveResponse{
				Sources: []core.PlayableSource{{
					ID:      "src-con",
					Pattern: "range",
					URL:     "https://concert.example.test/concert.mp4",
					MIME:    "video/mp4",
					Size:    int64(len(concertBytes)),
				}},
			}
		} else {
			out = core.KindResolveResponse{
				Sources: []core.PlayableSource{{
					ID:      "src-trk",
					Pattern: "range",
					URL:     "https://concert.example.test/track.flac",
					MIME:    "audio/flac",
					Size:    int64(len(trackBytes)),
				}},
			}
		}
		b, _ := json.Marshal(out)
		return plugins.Response{Status: http.StatusOK, Body: b}, nil
	}

	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Music & Concerts",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Music library with video concerts",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"permissions":  map[string]any{"network": []string{"concert.example.test"}},
			"contributes": map[string]any{
				"mediaKinds": []any{map[string]any{
					"key":   "music",
					"label": "Music",
					"media": "music",
					"library": map[string]any{
						"name": "Music",
						"card": "square",
						"tabs": []any{
							map[string]any{"key": "recommended", "label": "Recommended", "kind": "recommended"},
							map[string]any{
								"key":   "concerts",
								"label": "Concerts",
								"kind":  "recommended",
								"card":  "landscape",
								"shelves": []any{
									map[string]any{"key": "recent-concerts", "label": "Recent concerts", "level": "concert", "sort": "added", "direction": "desc"},
								},
							},
							map[string]any{"key": "artists", "label": "Artists", "kind": "library", "level": "artist"},
							map[string]any{"key": "albums", "label": "Albums", "kind": "library", "level": "album"},
						},
						"shelves": []any{
							map[string]any{"key": "recent-albums", "label": "Recent albums", "level": "album", "sort": "added", "direction": "desc"},
						},
					},
					"levels": []any{
						map[string]any{"key": "artist", "label": "Artist", "container": true},
						map[string]any{"key": "album", "label": "Album", "parent": "artist", "container": true},
						map[string]any{"key": "track", "label": "Track", "parent": "album", "playable": true},
						map[string]any{"key": "concert", "label": "Concert", "parent": "artist", "media": "video", "role": "concert", "playable": true},
					},
					"present": map[string]any{
						"vela":         map[string]any{"type": "shows"},
						"plex":         map[string]any{"base": "artist"},
						"mediaBrowser": map[string]any{"collectionType": "music"},
					},
					"browse": map[string]any{"sorts": []string{"title", "added", "released"}},
					"sync":   map[string]any{"mode": "push"},
				}},
			},
		},
	}, nil)

	lib := pluginLibraryOf(t, h, pluginID)
	viewID := strconv.Itoa(int(lib.ID))

	if _, err := h.items.KindUpsert(ctx, pluginID, "music", []core.KindItem{
		{ID: "art-1", Level: "artist", Title: "Miles Davis"},
		{ID: "alb-1", Level: "album", ParentID: "art-1", Index: 1, Title: "Kind of Blue", Year: 1959},
		{ID: "alb-2", Level: "album", ParentID: "art-1", Index: 2, Title: "Bitches Brew", Year: 1970},
		{ID: "trk-1", Level: "track", ParentID: "alb-1", Index: 1, Title: "So What", Playable: true, Fields: map[string]json.RawMessage{"duration": json.RawMessage(`562`)}},
		{ID: "trk-2", Level: "track", ParentID: "alb-2", Index: 1, Title: "Pharaoh's Dance", Playable: true, Fields: map[string]json.RawMessage{"duration": json.RawMessage(`1200`)}},
		{ID: "con-1", Level: "concert", ParentID: "art-1", Index: 1, Title: "Live at Newport 1958", Playable: true, Year: 1958, Fields: map[string]json.RawMessage{"duration": json.RawMessage(`3600`)}},
		{ID: "con-2", Level: "concert", ParentID: "art-1", Index: 2, Title: "Live in Paris 1960", Playable: true, Year: 1960, Fields: map[string]json.RawMessage{"duration": json.RawMessage(`4200`)}},
	}); err != nil {
		t.Fatal(err)
	}

	exp := h.experienceFor(t, "vela")

	// 1. Artist carries per-role ChildCounts; concert is projected as movie/video with artist parent and resume enabled.
	artistsPage, err := exp.LibraryItems(ctx, h.users[1], core.Device{}, viewID, frontend.ItemQuery{Limit: 10})
	if err != nil || len(artistsPage.Items) != 1 {
		t.Fatalf("artists browse = %+v, err=%v", artistsPage, err)
	}
	artist := artistsPage.Items[0]
	wantCounts := map[string]int{"album": 2, "concert": 2}
	if artist.ChildCount != 4 || !maps.Equal(artist.ChildCounts, wantCounts) {
		t.Fatalf("artist ChildCount=%d ChildCounts=%v, want 4 / %v", artist.ChildCount, artist.ChildCounts, wantCounts)
	}

	// 2. Filtered children (?role= accepts every media.childCounts key: declared role first, then role-less level key; ?level= matches level key).
	concertsPage, err := exp.Children(ctx, h.users[1], core.Device{}, artist.Key, frontend.ItemQuery{Role: "concert", Limit: 1})
	if err != nil || concertsPage.Total != 2 || len(concertsPage.Items) != 1 || concertsPage.Items[0].Title != "Live at Newport 1958" {
		t.Fatalf("children?role=concert p1 = %+v, err=%v", concertsPage, err)
	}
	concertsPage2, err := exp.Children(ctx, h.users[1], core.Device{}, artist.Key, frontend.ItemQuery{Role: "concert", Offset: 1, Limit: 1})
	if err != nil || concertsPage2.Total != 2 || len(concertsPage2.Items) != 1 || concertsPage2.Items[0].Title != "Live in Paris 1960" {
		t.Fatalf("children?role=concert p2 = %+v, err=%v", concertsPage2, err)
	}
	// Role-less child level ("album") is listed under childCounts["album"] and must match ?role=album.
	albumsByRole, err := exp.Children(ctx, h.users[1], core.Device{}, artist.Key, frontend.ItemQuery{Role: "album", Limit: 10})
	if err != nil || albumsByRole.Total != 2 || len(albumsByRole.Items) != 2 {
		t.Fatalf("children?role=album (role-less key fallback) = %+v, err=%v", albumsByRole, err)
	}
	albumsPage, err := exp.Children(ctx, h.users[1], core.Device{}, artist.Key, frontend.ItemQuery{Level: "album", Limit: 10})
	if err != nil || albumsPage.Total != 2 || len(albumsPage.Items) != 2 {
		t.Fatalf("children?level=album = %+v, err=%v", albumsPage, err)
	}
	// Every key in artist.ChildCounts must return its exact count via ?role=<key>.
	for roleKey, wantN := range artist.ChildCounts {
		p, err := exp.Children(ctx, h.users[1], core.Device{}, artist.Key, frontend.ItemQuery{Role: roleKey, Limit: 10})
		if err != nil || p.Total != wantN || len(p.Items) != wantN {
			t.Fatalf("children?role=%s = total:%d len:%d err:%v, want %d", roleKey, p.Total, len(p.Items), err, wantN)
		}
	}

	con1 := concertsPage.Items[0]
	if con1.Type != core.TypeMovie || con1.MediaClass != core.MediaClassVideo || con1.MediaRole != "concert" || con1.MBItemType != "Video" {
		t.Fatalf("concert title = %+v, want movie/video/concert/Video", con1)
	}
	if len(con1.MediaParents) != 1 || con1.MediaParents[0].Key != artist.Key || con1.MediaParents[0].Role != core.MediaRoleArtist {
		t.Fatalf("concert MediaParents = %+v, want artist %v", con1.MediaParents, artist.Key)
	}

	conView, err := h.catalog.Item(ctx, con1.Key)
	if err != nil {
		t.Fatalf("catalog.Item(concert): %v", err)
	}
	if conView.SkipContinue {
		t.Fatalf("video concert must allow Continue Watching / Resume, got SkipContinue=%v", conView.SkipContinue)
	}

	// 3. KindLeaves on artist returns audio tracks ONLY (never video concerts).
	leaves, err := h.resolver.KindLeaves(ctx, h.users[1], artist.Key)
	if err != nil {
		t.Fatal(err)
	}
	var leafTitles []string
	for _, lv := range leaves {
		leafTitles = append(leafTitles, lv.Title)
	}
	if !slices.Equal(leafTitles, []string{"So What", "Pharaoh's Dance"}) {
		t.Fatalf("KindLeaves(artist) = %v, want audio tracks only", leafTitles)
	}

	// Track still skips continue/resume inside music library.
	trkView, err := h.catalog.Item(ctx, leaves[0].Key)
	if err != nil || !trkView.SkipContinue {
		t.Fatalf("audio track should skip continue/resume, got SkipContinue=%v err=%v", trkView.SkipContinue, err)
	}

	// 4. Playback resolution for video concert inside a music library resolves as video (NOT audio)
	//    and EstimateInfo produces a video plan (with video stream) for concert vs audio-only for track.
	kmCon, err := h.resolver.OpenKind(ctx, h.users[1], con1.Key, nil)
	if err != nil {
		t.Fatalf("OpenKind(concert): %v", err)
	}
	if kmCon.MIME != "video/mp4" {
		_ = kmCon.Close()
		t.Fatalf("concert MIME = %q, want video/mp4", kmCon.MIME)
	}
	gotConBytes, err := io.ReadAll(io.NewSectionReader(kmCon, 0, kmCon.Size))
	_ = kmCon.Close()
	if err != nil || !bytes.Equal(gotConBytes, concertBytes) {
		t.Fatalf("concert read = (%q, %v), want %q", gotConBytes, err, concertBytes)
	}
	kmTrk, err := h.resolver.OpenKind(ctx, h.users[1], leaves[0].Key, nil)
	if err != nil {
		t.Fatalf("OpenKind(track): %v", err)
	}
	_ = kmTrk.Close()
	if kmTrk.MIME != "audio/flac" {
		t.Fatalf("track MIME = %q, want audio/flac", kmTrk.MIME)
	}
	conEst := playback.EstimateInfo(&con1, core.ClientCaps{}, core.QualityProfile{})
	if conEst.Video() == nil {
		t.Fatalf("concert EstimateInfo must include a video stream, got %+v", conEst)
	}
	trkEst := playback.EstimateInfo(&trkView.Title, core.ClientCaps{}, core.QualityProfile{})
	if trkEst.Video() != nil {
		t.Fatalf("track EstimateInfo must be audio-only, got %+v", trkEst)
	}

	// 5. Plex ?type=10 (track) browse inside branching music library must NOT return video concerts.
	tracksOnly, err := h.catalog.Browse(ctx, lib, frontend.ItemQuery{Types: []core.MediaType{"track"}, Recursive: true}, "", 20)
	if err != nil || len(tracksOnly.Items) != 2 {
		t.Fatalf("Browse(Types=[track]) = %+v, err=%v", tracksOnly.Items, err)
	}
	for _, it := range tracksOnly.Items {
		if it.KindLevel != "track" {
			t.Fatalf("Browse(Types=[track]) leaked level %q (%q)", it.KindLevel, it.Title)
		}
	}

	// 5b. Unfiltered recursive Children(artist, recursive=true) stays in the
	//     artist's audio media class (excludes video concerts) for playlists,
	//     scrobble, and Jellyfin/Vela play queues, while filtered Role=concert
	//     still returns concerts.
	recAudio, err := h.catalog.Children(ctx, artist.Key, true, "", 50)
	if err != nil || len(recAudio.Items) != 2 {
		t.Fatalf("unfiltered recursive Children(artist) = %+v, err=%v, want 2 tracks only", recAudio.Items, err)
	}
	for _, it := range recAudio.Items {
		if it.KindLevel != "track" || it.MediaClass == core.MediaClassVideo {
			t.Fatalf("unfiltered recursive Children(artist) leaked %q (%s/%s)", it.Title, it.KindLevel, it.MediaClass)
		}
	}
	recConcerts, err := h.catalog.ChildrenQuery(ctx, artist.Key, frontend.ItemQuery{Recursive: true, Role: "concert"}, "", 50)
	if err != nil || len(recConcerts.Items) != 2 {
		t.Fatalf("filtered recursive Children(artist, Role=concert) = %+v, err=%v, want 2 concerts", recConcerts.Items, err)
	}

	// 6. Per-tab recommended rows & Continue Watching scoping:
	//    Record mid-playback progress on concert-1; Concerts tab gets Continue Watching with con-1,
	//    while default Music Recommended tab stays audio-only (no concert in Continue Watching)
	//    and preserves legacy row IDs.
	if err := h.state.Progress(ctx, 1, con1.Key, 20*time.Minute, 60*time.Minute, "paused"); err != nil {
		t.Fatal(err)
	}

	defaultRows, err := exp.PluginLibraryRows(ctx, h.users[1], core.Device{}, viewID, "recommended")
	if err != nil {
		t.Fatalf("PluginLibraryTabRows(recommended): %v", err)
	}
	for _, row := range defaultRows {
		if strings.HasSuffix(row.ID, ":continue") {
			t.Fatalf("Music recommended tab must not show video concert Continue Watching row: %+v", row)
		}
	}
	// Without the Recommendations engine every candidate comes back: the
	// declared shelf first, then the library's own rows the page may add.
	wantDefaultShelfID := "plugin:" + viewID + ":shelf:recent-albums"
	if len(defaultRows) == 0 || defaultRows[0].ID != wantDefaultShelfID || len(defaultRows[0].Items) != 2 {
		t.Fatalf("recommended rows = %+v, want single legacy shelf %q with 2 albums", defaultRows, wantDefaultShelfID)
	}

	concertRows, err := exp.PluginLibraryRows(ctx, h.users[1], core.Device{}, viewID, "concerts")
	if err != nil {
		t.Fatalf("PluginLibraryTabRows(concerts): %v", err)
	}
	wantConcertContinueID := "plugin:" + viewID + ":tab:concerts:continue"
	wantConcertShelfID := "plugin:" + viewID + ":tab:concerts:shelf:recent-concerts"
	// Continue and the declared shelf lead; the library's own rows follow.
	if len(concertRows) < 2 {
		t.Fatalf("concerts tab rows = %+v, want [continue, recent-concerts, …]", concertRows)
	}
	if concertRows[0].ID != wantConcertContinueID || concertRows[0].Title != "Continue Watching" || len(concertRows[0].Items) != 1 || concertRows[0].Items[0].Key != con1.Key {
		t.Fatalf("concerts continue row = %+v, want %q with con-1", concertRows[0], wantConcertContinueID)
	}
	if concertRows[1].ID != wantConcertShelfID || len(concertRows[1].Items) != 2 {
		t.Fatalf("concerts shelf row = %+v, want %q with 2 concerts", concertRows[1], wantConcertShelfID)
	}
}

// TestChildrenRolePrecedenceMatchesDeclaredRoleThenRolelessKey proves the W289
// Vela wire rule: ?role=<key> matches child levels by declared role first, then
// matches a role-less child level by its level key, and never confuses a level's
// key with a role when that level declares a different explicit role.
func TestChildrenRolePrecedenceMatchesDeclaredRoleThenRolelessKey(t *testing.T) {
	h := newKindTestHarness(t, 250*time.Millisecond, nil)
	ctx := context.Background()

	const pluginID = "community.example.role-precedence"
	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Role Precedence",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Role precedence fixture",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []any{map[string]any{
					"key":     "catalog",
					"label":   "Catalog",
					"media":   "music",
					"library": map[string]any{"name": "Catalog"},
					"levels": []any{
						map[string]any{"key": "artist", "label": "Artist", "container": true},
						map[string]any{"key": "album", "label": "Album", "parent": "artist", "container": true},
						map[string]any{"key": "live-film", "label": "Live Film", "parent": "artist", "media": "video", "role": "concert", "playable": true},
						map[string]any{"key": "concert", "label": "Audio Concert", "parent": "artist", "playable": true},
					},
					"present": map[string]any{"vela": map[string]any{"type": "shows"}},
					"browse":  map[string]any{"sorts": []string{"title"}},
					"sync":    map[string]any{"mode": "push"},
				}},
			},
		},
	}, nil)

	if _, err := h.items.KindUpsert(ctx, pluginID, "catalog", []core.KindItem{
		{ID: "a1", Level: "artist", Title: "Artist"},
		{ID: "alb1", Level: "album", ParentID: "a1", Index: 1, Title: "Studio Album"},
		{ID: "film1", Level: "live-film", ParentID: "a1", Index: 1, Title: "Concert Film", Playable: true},
		{ID: "aud1", Level: "concert", ParentID: "a1", Index: 1, Title: "Audio Bootleg", Playable: true},
	}); err != nil {
		t.Fatal(err)
	}

	artItem, _, err := h.items.GetByRef(ctx, core.NewItemRef(pluginID, "catalog", "a1"))
	if err != nil {
		t.Fatal(err)
	}

	// 1. Role-less child level ("album") matches ?role=album via level key fallback.
	alb, err := h.catalog.ChildrenFiltered(ctx, artItem.Key, false, "", "album", "", 10)
	if err != nil || len(alb.Items) != 1 || alb.Items[0].Title != "Studio Album" {
		t.Fatalf("?role=album = %+v, err=%v", alb.Items, err)
	}

	// 2. Declared role ("concert" on live-film) wins ahead of role-less level key ("concert").
	byRole, err := h.catalog.ChildrenFiltered(ctx, artItem.Key, false, "", "concert", "", 10)
	if err != nil || len(byRole.Items) != 1 || byRole.Items[0].Title != "Concert Film" {
		t.Fatalf("?role=concert = %+v, err=%v (declared role must match first)", byRole.Items, err)
	}

	// 3. Level with explicit role ("live-film" has role="concert") does NOT match ?role=live-film,
	//    but DOES match ?level=live-film.
	noKeyRole, err := h.catalog.ChildrenFiltered(ctx, artItem.Key, false, "", "live-film", "", 10)
	if err != nil || len(noKeyRole.Items) != 0 {
		t.Fatalf("?role=live-film = %+v, err=%v (want empty; live-film has explicit role)", noKeyRole.Items, err)
	}
	byLevel, err := h.catalog.ChildrenFiltered(ctx, artItem.Key, false, "live-film", "", "", 10)
	if err != nil || len(byLevel.Items) != 1 || byLevel.Items[0].Title != "Concert Film" {
		t.Fatalf("?level=live-film = %+v, err=%v", byLevel.Items, err)
	}
}

// TestBranchingContainerDirectChildCountHydrationAndTypedChildren proves:
//   - container ChildCount reflects direct children (sum of ChildCounts) even
//     when a plugin stores a larger leaf/track total in item.ChildCount, while
//     LeafCount preserves the recursive leaf count;
//   - list surfaces carry ChildCounts from the indexed per-level tally;
//   - unhydrated branching containers commit the first page of every child
//     branch before returning so childCounts is complete on first open;
//   - typed recursive child queries (MusicAlbum / Video / Audio) return only
//     the matching branch without mixing levels or dropping intermediate folders.
func TestBranchingContainerDirectChildCountHydrationAndTypedChildren(t *testing.T) {
	h := newKindTestHarness(t, 250*time.Millisecond, nil)
	ctx := context.Background()

	const pluginID = "community.example.music-w302"
	h.rt.routes["/ether/v1/kinds/music/browse"] = func(req plugins.Request) (plugins.Response, error) {
		var br core.KindBrowseRequest
		_ = json.Unmarshal(req.Body, &br)
		var items []core.KindItem
		if br.ParentID == "art-stub" {
			switch br.Level {
			case "album":
				items = []core.KindItem{{ID: "stub-alb-1", Level: "album", ParentID: "art-stub", Index: 1, Title: "Stub Album"}}
			case "concert":
				items = []core.KindItem{{ID: "stub-con-1", Level: "concert", ParentID: "art-stub", Index: 1, Title: "Stub Concert", Playable: true}}
			}
		}
		b, _ := json.Marshal(core.KindPage{Items: items})
		return plugins.Response{Status: http.StatusOK, Body: b}, nil
	}

	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Music W302",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Two-branch music fixture",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"contributes": map[string]any{
				"mediaKinds": []any{map[string]any{
					"key":   "music",
					"label": "Music",
					"media": "music",
					"library": map[string]any{
						"name": "Music",
						"card": "square",
						"tabs": []any{
							map[string]any{"key": "recommended", "label": "Recommended", "kind": "recommended"},
							map[string]any{"key": "library", "label": "Library", "kind": "library"},
							map[string]any{"key": "artists", "label": "Artists", "kind": "library", "level": "artist"},
						},
					},
					"levels": []any{
						map[string]any{"key": "artist", "label": "Artist", "role": "artist", "container": true},
						map[string]any{"key": "album", "label": "Album", "parent": "artist", "role": "album", "container": true},
						map[string]any{"key": "track", "label": "Track", "parent": "album", "role": "track", "playable": true},
						map[string]any{"key": "concert", "label": "Concert", "parent": "artist", "media": "video", "role": "concert", "playable": true},
					},
					"present": map[string]any{
						"vela":         map[string]any{"type": "shows"},
						"plex":         map[string]any{"base": "artist"},
						"mediaBrowser": map[string]any{"collectionType": "music"},
					},
					"browse": map[string]any{"sorts": []string{"title"}},
					"sync":   map[string]any{"mode": "push"},
				}},
			},
		},
	}, nil)

	lib := pluginLibraryOf(t, h, pluginID)
	viewID := strconv.Itoa(int(lib.ID))

	// Seed AC/DC with plugin-reported ChildCount=27 (leaf/track total), 5 albums x 5 tracks + 2 concerts (7 direct children, 27 leaves),
	// The Who with plugin-reported ChildCount=71 and 3 albums, and an unhydrated stub artist.
	items := []core.KindItem{
		{ID: "art-acdc", Level: "artist", Title: "AC/DC", ChildCount: 27},
		{ID: "art-who", Level: "artist", Title: "The Who", ChildCount: 71, LeafCount: 71},
		{ID: "art-stub", Level: "artist", Title: "Stub Artist", ChildCount: 10},
	}
	for a := 1; a <= 5; a++ {
		albID := "acdc-alb-" + strconv.Itoa(a)
		items = append(items, core.KindItem{ID: albID, Level: "album", ParentID: "art-acdc", Index: a, Title: "AC/DC Album " + strconv.Itoa(a)})
		for tr := 1; tr <= 5; tr++ {
			items = append(items, core.KindItem{
				ID:       albID + "-trk-" + strconv.Itoa(tr),
				Level:    "track",
				ParentID: albID,
				Index:    tr,
				Title:    "Track " + strconv.Itoa(tr),
				Playable: true,
			})
		}
	}
	items = append(items,
		core.KindItem{ID: "acdc-con-1", Level: "concert", ParentID: "art-acdc", Index: 1, Title: "Live at River Plate", Playable: true},
		core.KindItem{ID: "acdc-con-2", Level: "concert", ParentID: "art-acdc", Index: 2, Title: "Live at Donington", Playable: true},
	)
	for a := 1; a <= 3; a++ {
		items = append(items, core.KindItem{ID: "who-alb-" + strconv.Itoa(a), Level: "album", ParentID: "art-who", Index: a, Title: "Who Album " + strconv.Itoa(a)})
	}
	if _, err := h.items.KindUpsert(ctx, pluginID, "music", items); err != nil {
		t.Fatal(err)
	}

	exp := h.experienceFor(t, "vela")

	// 1. List surface projects direct ChildCount (7 / 3), preserves LeafCount (27 / 71),
	//    sets PrimaryChildLevel="album", and populates ChildCounts from the indexed tally.
	page, err := exp.LibraryItems(ctx, h.users[1], core.Device{}, viewID, frontend.ItemQuery{Sort: core.SortTitle, Limit: 20})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("LibraryItems = %+v, err=%v", page.Items, err)
	}
	byTitle := map[string]core.Title{}
	for _, it := range page.Items {
		byTitle[it.Title] = it
	}
	acdc := byTitle["AC/DC"]
	wantACDC := map[string]int{"album": 5, "concert": 2}
	if acdc.ChildCount != 7 || acdc.LeafCount != 27 || !maps.Equal(acdc.ChildCounts, wantACDC) || acdc.PrimaryChildLevel != "album" {
		t.Fatalf("AC/DC list projection = ChildCount:%d LeafCount:%d ChildCounts:%v PrimaryChildLevel:%q, want 7 / 27 / %v / album",
			acdc.ChildCount, acdc.LeafCount, acdc.ChildCounts, acdc.PrimaryChildLevel, wantACDC)
	}
	who := byTitle["The Who"]
	wantWho := map[string]int{"album": 3}
	if who.ChildCount != 3 || who.LeafCount != 71 || !maps.Equal(who.ChildCounts, wantWho) || who.PrimaryChildLevel != "album" {
		t.Fatalf("The Who list projection = ChildCount:%d LeafCount:%d ChildCounts:%v PrimaryChildLevel:%q, want 3 / 71 / %v / album",
			who.ChildCount, who.LeafCount, who.ChildCounts, who.PrimaryChildLevel, wantWho)
	}

	// 2. Detail projection matches list projection.
	acdcDetail, err := exp.Item(ctx, frontend.ItemRequest{Viewer: h.users[1], Key: acdc.Key})
	if err != nil {
		t.Fatal(err)
	}
	if acdcDetail.Title.ChildCount != 7 || acdcDetail.Title.LeafCount != 27 || !maps.Equal(acdcDetail.Title.ChildCounts, wantACDC) {
		t.Fatalf("AC/DC detail = ChildCount:%d LeafCount:%d ChildCounts:%v", acdcDetail.Title.ChildCount, acdcDetail.Title.LeafCount, acdcDetail.Title.ChildCounts)
	}

	// 3. Unhydrated stub artist hydrates BOTH child branches (album + concert) before
	//    Children returns so both branches immediately appear in ChildCounts on list and detail.
	stub := byTitle["Stub Artist"]
	if len(stub.ChildCounts) != 0 {
		t.Fatalf("expected unhydrated stub to start with no ChildCounts, got %v", stub.ChildCounts)
	}
	stubAlbums, err := exp.Children(ctx, h.users[1], core.Device{}, stub.Key, frontend.ItemQuery{Level: "album", Limit: 10})
	if err != nil || len(stubAlbums.Items) != 1 || stubAlbums.Items[0].Title != "Stub Album" {
		t.Fatalf("stub Children(level=album) = %+v, err=%v", stubAlbums.Items, err)
	}
	stubRefreshed, err := exp.Item(ctx, frontend.ItemRequest{Viewer: h.users[1], Key: stub.Key})
	if err != nil {
		t.Fatal(err)
	}
	wantStubCounts := map[string]int{"album": 1, "concert": 1}
	if stubRefreshed.Title.ChildCount != 2 || !maps.Equal(stubRefreshed.Title.ChildCounts, wantStubCounts) {
		t.Fatalf("hydrated stub = ChildCount:%d ChildCounts:%v, want 2 / %v", stubRefreshed.Title.ChildCount, stubRefreshed.Title.ChildCounts, wantStubCounts)
	}

	// 4. Jellyfin-style typed recursive children queries do not mix branches or drop MusicAlbum folders.
	mbExp := h.experienceFor(t, "jellyfin")
	albumsRec, err := mbExp.Children(ctx, h.users[1], core.Device{}, acdc.Key, frontend.ItemQuery{
		Recursive:     true,
		UnservedTypes: []string{"musicalbum"},
		Limit:         20,
	})
	if err != nil || len(albumsRec.Items) != 5 {
		t.Fatalf("Children(MusicAlbum, Recursive=true) len=%d err=%v, want 5 albums", len(albumsRec.Items), err)
	}
	for _, it := range albumsRec.Items {
		if it.MBItemType != "MusicAlbum" {
			t.Fatalf("Children(MusicAlbum, Recursive=true) leaked %q (%q)", it.MBItemType, it.Title)
		}
	}
	videosRec, err := mbExp.Children(ctx, h.users[1], core.Device{}, acdc.Key, frontend.ItemQuery{
		Recursive:     true,
		UnservedTypes: []string{"video"},
		Limit:         20,
	})
	if err != nil || len(videosRec.Items) != 2 {
		t.Fatalf("Children(Video, Recursive=true) len=%d err=%v, want 2 concerts", len(videosRec.Items), err)
	}
	for _, it := range videosRec.Items {
		if it.MBItemType != "Video" {
			t.Fatalf("Children(Video, Recursive=true) leaked %q (%q)", it.MBItemType, it.Title)
		}
	}
	tracksRec, err := mbExp.Children(ctx, h.users[1], core.Device{}, acdc.Key, frontend.ItemQuery{
		Recursive:     true,
		UnservedTypes: []string{"audio"},
		Limit:         50,
	})
	if err != nil || len(tracksRec.Items) != 25 {
		t.Fatalf("Children(Audio, Recursive=true) len=%d err=%v, want 25 tracks", len(tracksRec.Items), err)
	}
	for _, it := range tracksRec.Items {
		if it.MBItemType != "Audio" {
			t.Fatalf("Children(Audio, Recursive=true) leaked %q (%q)", it.MBItemType, it.Title)
		}
	}
}
