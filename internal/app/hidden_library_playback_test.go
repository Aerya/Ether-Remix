package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/compactids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestHiddenAppLibraryUnreachableAcrossPlaybackEndpoints(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	const posterURL = "https://art.example.test/posters/track.png"
	remoteImages := staticUntrustedImages{
		byURL: map[string]struct {
			data []byte
			ct   string
		}{
			posterURL: {data: makeTestPNG(t, 200, 300), ct: "image/png"},
		},
	}

	trans := &operabilityTranscoder{max: 2}
	if err = cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "test-owner"
		c.Server.DefaultStreamLimit = 8
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Frontends = map[string]config.Frontend{
			"plex":     {Enabled: true},
			"jellyfin": {Enabled: true, Settings: map[string]any{"port": float64(0), "discovery": false}},
			"emby":     {Enabled: true, Settings: map[string]any{"port": float64(0), "discovery": false}},
			"silo":     {Enabled: true, Settings: map[string]any{"port": float64(0), "discovery": false}},
			"vela":     {Enabled: true},
			"stremio":  {Enabled: true},
			"xtream":   {Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	a, err := New(ctx, Options{
		DataDir:           dir,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		FixtureTranscoder: trans,
		plextvHTTP:        &http.Client{Transport: fakePlexTVTransport{}},
		urlImages:         remoteImages,
		diskUsage:         func() (int64, int64, error) { return 1 << 40, 1 << 39, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)

	if err = a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("0123456789abcdef"), 64)
	copy(payload[:12], []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})

	const (
		pluginID = "community.example.tracks"
		kindKey  = "track"
		itemExt  = "trk-1"
	)
	manifest := []byte(`{
		"id": "community.example.tracks",
		"version": "1.0.0",
		"name": "Track Archive",
		"description": "Content plugin for hidden library playback visibility tests",
		"ether": {
			"protocol": 1,
			"kind": "content",
			"summary": "Playable plugin library",
			"runtime": {"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": ["catalog_provider", "kind_resolver"],
			"contributes": {
				"mediaKinds": [{
					"key": "track",
					"label": "Tracks",
					"library": {"name": "Track Archive"},
					"levels": [{"key": "track", "label": "Track", "playable": true}],
					"present": {
						"vela": {"type": "movies"},
						"plex": {"base": "movie"},
						"mediaBrowser": {"collectionType": "movies"},
						"stremio": {"type": "movie"},
						"xtream": {"category": "movie"},
						"silo": {"type": "movies"}
					},
					"sync": {"mode": "push"}
				}]
			}
		}
	}`)
	// First install with a placeholder script; we update the blob ref right
	// after the library key is allocated and the blob is cached.
	placeholderScript := []byte(`function handle(req) { return {status: 200, body: {status: "ok"}}; }`)
	preview, err := a.pluginsRT.mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: manifest,
		Files:    map[string][]byte{"dist/plugin.js": placeholderScript},
	})
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	if _, err := a.pluginsRT.mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, err := a.pluginsRT.pluginitems.UpsertItems(ctx, pluginID, kindKey, []core.KindItem{
		{
			ID:       itemExt,
			Level:    kindKey,
			Title:    "Hidden Track",
			Year:     2025,
			Released: 1704067200,
			Playable: true,
			Art:      []core.KindArtRef{{Role: "poster", URL: posterURL}},
		},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	stored, libID, err := a.pluginsRT.pluginitems.GetByExternal(ctx, pluginID, kindKey, itemExt)
	if err != nil {
		t.Fatalf("GetByExternal: %v", err)
	}
	lib, err := a.libraries.Get(ctx, libID)
	if err != nil {
		t.Fatalf("libraries.Get: %v", err)
	}
	a.pluginsRT.blobcache.BindPluginLibrary(pluginID, lib.Key)
	blobRef, err := a.pluginsRT.blobcache.Namespace(lib.Key, blobcache.Quota{MaxBytes: 1 << 20}).Put(
		ctx, "track-blob", bytes.NewReader(payload), int64(len(payload)),
	)
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}

	resolveScript := fmt.Sprintf(`function handle(req) {
		if (req.path && req.path.indexOf("/resolve") !== -1) {
			return {status: 200, body: {sources: [{
				id: "src-1",
				pattern: "range",
				mime: "video/mp4",
				size: %d,
				blobRef: %q,
				label: "Hidden Track.mp4"
			}]}};
		}
		return {status: 200, body: {status: "ok"}};
	}`, len(payload), string(blobRef))
	manifestV2 := bytes.Replace(manifest, []byte(`"version": "1.0.0"`), []byte(`"version": "1.0.1"`), 1)
	updatePreview, err := a.pluginsRT.mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: manifestV2,
		Files:    map[string][]byte{"dist/plugin.js": []byte(resolveScript)},
	})
	if err != nil {
		t.Fatalf("PreviewInstall(update): %v", err)
	}
	if _, err := a.pluginsRT.mgr.Install(ctx, updatePreview, plugins.InstallOptions{AcceptAddedPermissions: true}); err != nil {
		t.Fatalf("Install(update): %v", err)
	}
	// Re-ensure blob is present after plugin update.
	if _, err := a.pluginsRT.blobcache.Namespace(lib.Key, blobcache.Quota{MaxBytes: 1 << 20}).Put(
		ctx, "track-blob", bytes.NewReader(payload), int64(len(payload)),
	); err != nil {
		t.Fatalf("blob Put(post-update): %v", err)
	}

	// Ensure the library starts explicitly published to all six apps.
	fullPresent := core.Presentations{
		Vela:         &core.VelaPresentation{Type: "movies"},
		Plex:         &core.PlexPresentation{Base: "movie"},
		MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "movies"},
		Stremio:      &core.StremioPresentation{Type: "movie"},
		Xtream:       &core.XtreamPresentation{Category: "movie"},
		Silo:         &core.SiloPresentation{Type: "movies"},
	}
	if err := a.libraries.SetPresentAndOverrides(ctx, libID, fullPresent, core.PresentationOverrides{}); err != nil {
		t.Fatalf("SetPresentAndOverrides(all): %v", err)
	}

	server := httptest.NewServer(a.Handler())
	t.Cleanup(server.Close)
	a.frontends.start(ctx, server.Listener.Addr())
	t.Cleanup(a.frontends.stop)

	grants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, surface := range []string{"vela", "jellyfin", "emby", "silo", "stremio", "xtream"} {
		dev := core.Device{ID: "dev-" + surface}
		tokens[surface], err = grants.Issue(ctx, owner.ID, nil, surface, dev)
		if err != nil {
			t.Fatal(err)
		}
		if surface == "vela" {
			if _, err = grants.IssueRefresh(ctx, "vela", tokens[surface], dev); err != nil {
				t.Fatal(err)
			}
		}
	}

	allocator, err := compactids.New(a.db)
	if err != nil {
		t.Fatal(err)
	}
	xtreamID, err := allocator.Compact(ctx, "xtream", "item", strconv.FormatInt(int64(stored.Key), 10))
	if err != nil {
		t.Fatalf("compact xtream id: %v", err)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	call := func(method, path, body string, headers map[string]string) (int, []byte) {
		t.Helper()
		fullURL := path
		if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
			fullURL = server.URL + path
		}
		req, e := http.NewRequest(method, fullURL, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if body != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, e := client.Do(req)
		if e != nil {
			t.Fatalf("%s %s: %v", method, path, e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, data
	}

	guid := ids.RatingKeyGUID(stored.Key)
	pkRef := string(stored.Ref)
	jfHeaders := map[string]string{
		"Authorization": `MediaBrowser Client="Web", Device="Browser", DeviceId="dev-jellyfin", Version="1.0", Token="` + tokens["jellyfin"] + `"`,
	}
	embyHeaders := map[string]string{
		"X-Emby-Token":     tokens["emby"],
		"X-Emby-Device-Id": "dev-emby",
	}
	plexHeaders := map[string]string{
		"X-Plex-Token":             "test-owner",
		"X-Plex-Client-Identifier": "dev-plex",
		"Accept":                   "application/json",
	}
	velaHeaders := map[string]string{
		"Authorization":    "Bearer " + tokens["vela"],
		"X-Vela-Device-Id": "dev-vela",
	}
	velaPlayBody := func(idem string) string {
		return fmt.Sprintf(`{"idempotencyKey":%q,"titleId":%q,"client":{"device":{"id":"dev-vela"},"maxWidth":1920,"maxHeight":1080,"hls":true}}`, idem, guid)
	}
	siloHeaders := map[string]string{
		"Authorization":    "Bearer " + tokens["silo"],
		"X-Silo-Device-Id": "dev-silo",
		"X-Silo-Client":    "tv",
	}

	// --- Control phase: while published to all apps, playback works ---
	code, body := call(http.MethodPost, "/jellyfin/Items/"+guid+"/PlaybackInfo", `{"IsPlayback":true}`, jfHeaders)
	if code != http.StatusOK {
		t.Fatalf("control jellyfin PlaybackInfo = %d %s", code, body)
	}
	var pbResp struct {
		PlaySessionId string
		MediaSources  []struct {
			DirectStreamUrl string
		}
	}
	if err := json.Unmarshal(body, &pbResp); err != nil || pbResp.PlaySessionId == "" || len(pbResp.MediaSources) == 0 {
		t.Fatalf("control jellyfin PlaybackInfo missing session/source: %s", body)
	}
	jfSessionID := pbResp.PlaySessionId
	jfDirectURL := pbResp.MediaSources[0].DirectStreamUrl

	code, body = call(http.MethodGet, "/jellyfin/Audio/"+guid+"/stream?Static=true", "", jfHeaders)
	if code != http.StatusOK && code != http.StatusPartialContent {
		t.Fatalf("control jellyfin Audio/stream = %d %s", code, body)
	}
	code, body = call(http.MethodGet, "/jellyfin/Audio/"+guid+"/universal", "", jfHeaders)
	if code != http.StatusOK && code != http.StatusPartialContent {
		t.Fatalf("control jellyfin Audio/universal = %d %s", code, body)
	}
	code, _ = call(http.MethodGet, "/jellyfin/Items/"+guid+"/Images/Primary", "", jfHeaders)
	if code != http.StatusOK {
		t.Fatalf("control jellyfin primary image = %d", code)
	}
	// Capture a tagged Jellyfin image URL from item detail before hiding.
	code, body = call(http.MethodGet, "/jellyfin/Items/"+guid, "", jfHeaders)
	if code != http.StatusOK {
		t.Fatalf("control jellyfin item detail = %d %s", code, body)
	}
	var jfDetail struct {
		ImageTags map[string]string
	}
	_ = json.Unmarshal(body, &jfDetail)
	jfImageTag := jfDetail.ImageTags["Primary"]

	// Capture a Stremio lazy stream URL before hiding.
	code, body = call(http.MethodGet, "/stremio/"+tokens["stremio"]+"/stream/movie/"+url.PathEscape(pkRef)+".json", "", nil)
	if code != http.StatusOK {
		t.Fatalf("control stremio stream = %d %s", code, body)
	}
	var stremioResp struct {
		Streams []struct {
			URL string `json:"url"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(body, &stremioResp); err != nil || len(stremioResp.Streams) == 0 {
		t.Fatalf("control stremio missing stream URL: %s", body)
	}
	stremioLazyURL := stremioResp.Streams[0].URL

	// Capture a Silo file_id + installation_id before hiding.
	code, body = call(http.MethodGet, "/silo/api/v2/playback/capabilities", "", siloHeaders)
	if code != http.StatusOK {
		t.Fatalf("control silo playback/capabilities = %d %s", code, body)
	}
	var siloInfo struct {
		InstallationID string `json:"installation_id"`
	}
	if err := json.Unmarshal(body, &siloInfo); err != nil || siloInfo.InstallationID == "" {
		t.Fatalf("control silo playback/capabilities missing installation_id: %s", body)
	}
	code, body = call(http.MethodGet, "/silo/api/v2/watch/"+guid, "", siloHeaders)
	if code != http.StatusOK {
		t.Fatalf("control silo watch = %d %s", code, body)
	}
	var siloWatch struct {
		Versions []struct {
			FileID string `json:"file_id"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(body, &siloWatch); err != nil || len(siloWatch.Versions) == 0 {
		t.Fatalf("control silo watch missing file_id: %s", body)
	}
	siloFileID := siloWatch.Versions[0].FileID
	siloStartControl := fmt.Sprintf(`{"protocol_version":3,"file_id":%q,"installation_id":%q,"playback_attempt_id":"att-control","client_capabilities":{"codecs_video":["h264"]}}`,
		siloFileID, siloInfo.InstallationID)
	code, body = call(http.MethodPost, "/silo/api/v2/playback/start", siloStartControl, siloHeaders)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("control silo playback start = %d %s", code, body)
	}

	// Verify Plex, Vela, and Xtream work in the control phase too.
	code, body = call(http.MethodGet, fmt.Sprintf("/video/:/transcode/universal/decision?path=/library/metadata/%d&directPlay=1", stored.Key), "", plexHeaders)
	if code != http.StatusOK {
		t.Fatalf("control plex decision = %d %s", code, body)
	}
	code, body = call(http.MethodPost, "/vela/v0/playback", velaPlayBody("idem-control"), velaHeaders)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("control vela playback = %d %s", code, body)
	}
	xtreamPath := fmt.Sprintf("/xtream/movie/%s/%s/%d.mp4", frontend.XtreamUsername(tokens["xtream"]), tokens["xtream"], xtreamID)
	code, body = call(http.MethodGet, xtreamPath, "", nil)
	if code != http.StatusOK && code != http.StatusPartialContent {
		t.Fatalf("control xtream stream = %d %s", code, body)
	}

	// --- Hide phase 1: hide from mediaBrowser, plex, stremio, xtream, silo (keep Vela visible) ---
	off := false
	if err := a.libraries.SetPresentAndOverrides(ctx, libID,
		core.Presentations{Vela: &core.VelaPresentation{Type: "movies"}},
		core.PresentationOverrides{
			Plex:         &off,
			MediaBrowser: &off,
			Stremio:      &off,
			Xtream:       &off,
			Silo:         &off,
		},
	); err != nil {
		t.Fatalf("SetPresentAndOverrides(hide): %v", err)
	}

	assert404 := func(label, method, path, reqBody string, hdr map[string]string) {
		t.Helper()
		c, b := call(method, path, reqBody, hdr)
		if c != http.StatusNotFound {
			t.Errorf("%s (%s %s): got %d, want 404 (body=%q)", label, method, path, c, string(b))
		}
	}

	// Jellyfin & Emby: PlaybackInfo, Audio/Videos stream, universal, HLS master,
	// active pre-hide session, subtitles, images, download, played mutation.
	for _, mb := range []struct {
		name    string
		prefix  string
		headers map[string]string
	}{
		{"jellyfin", "/jellyfin", jfHeaders},
		{"emby", "/emby", embyHeaders},
	} {
		assert404(mb.name+" PlaybackInfo POST play", http.MethodPost, mb.prefix+"/Items/"+guid+"/PlaybackInfo", `{"IsPlayback":true}`, mb.headers)
		assert404(mb.name+" PlaybackInfo POST estimate", http.MethodPost, mb.prefix+"/Items/"+guid+"/PlaybackInfo", `{"IsPlayback":false}`, mb.headers)
		assert404(mb.name+" PlaybackInfo GET", http.MethodGet, mb.prefix+"/Items/"+guid+"/PlaybackInfo", "", mb.headers)
		assert404(mb.name+" Audio stream static", http.MethodGet, mb.prefix+"/Audio/"+guid+"/stream?Static=true", "", mb.headers)
		assert404(mb.name+" Audio universal", http.MethodGet, mb.prefix+"/Audio/"+guid+"/universal", "", mb.headers)
		assert404(mb.name+" Videos stream static", http.MethodGet, mb.prefix+"/Videos/"+guid+"/stream?Static=true", "", mb.headers)
		assert404(mb.name+" Videos master.m3u8", http.MethodGet, mb.prefix+"/Videos/"+guid+"/master.m3u8?PlaySessionId="+jfSessionID, "", mb.headers)
		assert404(mb.name+" Subtitles", http.MethodGet, mb.prefix+"/Videos/"+guid+"/"+guid+"/Subtitles/0/stream.vtt?PlaySessionId="+jfSessionID, "", mb.headers)
		assert404(mb.name+" Image tagless", http.MethodGet, mb.prefix+"/Items/"+guid+"/Images/Primary", "", mb.headers)
		assert404(mb.name+" Download", http.MethodGet, mb.prefix+"/Items/"+guid+"/Download", "", mb.headers)
		assert404(mb.name+" File", http.MethodGet, mb.prefix+"/Items/"+guid+"/File", "", mb.headers)
		assert404(mb.name+" SetPlayed", http.MethodPost, mb.prefix+"/UserPlayedItems/"+guid, "", mb.headers)
	}
	assert404("jellyfin pre-hide session stream", http.MethodGet, "/jellyfin/Videos/"+guid+"/stream?PlaySessionId="+jfSessionID, "", jfHeaders)
	if jfDirectURL != "" && strings.HasPrefix(jfDirectURL, "/sessions/media/") {
		assert404("jellyfin pre-hide direct media URL", http.MethodGet, "/jellyfin"+jfDirectURL, "", jfHeaders)
	}
	if jfImageTag != "" {
		assert404("jellyfin pre-hide tagged image", http.MethodGet, "/jellyfin/Items/"+guid+"/Images/Primary?tag="+url.QueryEscape(jfImageTag), "", jfHeaders)
	}

	// Plex: metadata, transcode decision/start (video & music), thumb.
	assert404("plex metadata", http.MethodGet, fmt.Sprintf("/library/metadata/%d", stored.Key), "", plexHeaders)
	assert404("plex video decision", http.MethodGet, fmt.Sprintf("/video/:/transcode/universal/decision?path=/library/metadata/%d", stored.Key), "", plexHeaders)
	assert404("plex video start.m3u8", http.MethodGet, fmt.Sprintf("/video/:/transcode/universal/start.m3u8?path=/library/metadata/%d", stored.Key), "", plexHeaders)
	assert404("plex music decision", http.MethodGet, fmt.Sprintf("/music/:/transcode/universal/decision?path=/library/metadata/%d", stored.Key), "", plexHeaders)
	assert404("plex thumb", http.MethodGet, fmt.Sprintf("/library/metadata/%d/thumb", stored.Key), "", plexHeaders)

	// Stremio: stream endpoint and previously issued lazy stream URL.
	assert404("stremio stream", http.MethodGet, "/stremio/"+tokens["stremio"]+"/stream/movie/"+url.PathEscape(pkRef)+".json", "", nil)
	assert404("stremio pre-hide lazy stream", http.MethodGet, stremioLazyURL, "", nil)

	// Xtream: VOD playback stream.
	assert404("xtream stream", http.MethodGet, xtreamPath, "", nil)

	// Silo: playback start with previously issued file_id.
	siloStart := fmt.Sprintf(`{"protocol_version":3,"file_id":%q,"installation_id":%q,"playback_attempt_id":"att-1","client_capabilities":{"codecs_video":["h264"]}}`,
		siloFileID, siloInfo.InstallationID)
	assert404("silo playback start", http.MethodPost, "/silo/api/v2/playback/start", siloStart, siloHeaders)

	// Positive control: Vela still sees and plays the library while published there.
	code, body = call(http.MethodPost, "/vela/v0/playback", velaPlayBody("idem-visible"), velaHeaders)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("positive control vela playback while still published = %d %s", code, body)
	}

	// --- Hide phase 2: hide from Vela too ---
	if err := a.libraries.SetPresentAndOverrides(ctx, libID,
		core.Presentations{},
		core.PresentationOverrides{
			Vela:         &off,
			Plex:         &off,
			MediaBrowser: &off,
			Stremio:      &off,
			Xtream:       &off,
			Silo:         &off,
		},
	); err != nil {
		t.Fatalf("SetPresentAndOverrides(hide vela): %v", err)
	}
	assert404("vela playback hidden", http.MethodPost, "/vela/v0/playback", velaPlayBody("idem-hidden"), velaHeaders)
}
