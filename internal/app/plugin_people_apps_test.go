package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/plugins"
)

type staticUntrustedImages struct {
	byURL map[string]struct {
		data []byte
		ct   string
	}
}

func (f staticUntrustedImages) GetUntrustedURL(_ context.Context, rawURL string) ([]byte, string, error) {
	entry, ok := f.byURL[rawURL]
	if !ok {
		return nil, "", core.ErrNotFound
	}
	return append([]byte(nil), entry.data...), entry.ct, nil
}

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 255) / max(1, w)),
				G: uint8((y * 255) / max(1, h)),
				B: 160,
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeImageSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode image (%d bytes): %v", len(data), err)
	}
	return cfg.Width, cfg.Height
}

func TestPluginPeopleSearchAndThumbsAcrossApps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	const (
		portraitURL = "https://portraits.example.test/cast/elena.png"
		posterURL   = "https://portraits.example.test/posters/item.png"
	)
	remoteImages := staticUntrustedImages{
		byURL: map[string]struct {
			data []byte
			ct   string
		}{
			portraitURL: {data: makeTestPNG(t, 240, 240), ct: "image/png"},
			posterURL:   {data: makeTestPNG(t, 300, 450), ct: "image/png"},
		},
	}

	media := &operabilityMedia{tmdb: 999, relID: "unused", body: []byte("media")}
	trans := &operabilityTranscoder{max: 2}
	if err = cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "test-owner"
		c.Transcode.FFmpegPath = "/not-installed/ffmpeg"
		c.Frontends = map[string]config.Frontend{
			"plex":     {Enabled: true},
			"jellyfin": {Enabled: true, Settings: map[string]any{"port": float64(0), "discovery": false}},
			"emby":     {Enabled: true, Settings: map[string]any{"port": float64(0), "discovery": false}},
			"silo":     {Enabled: true, Settings: map[string]any{"port": float64(0), "discovery": false}},
			"vela":     {Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	a, err := New(ctx, Options{
		DataDir:           dir,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		FixtureMedia:      media,
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

	// Install a generic content-kind plugin declaring a library with people fields.
	manifest := []byte(`{
		"id": "community.example.archive",
		"version": "1.0.0",
		"name": "Studio Archive",
		"description": "Generic content plugin for people search and portraits",
		"ether": {
			"protocol": 1,
			"kind": "content",
			"summary": "Studio archive items",
			"runtime": {"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": ["catalog_provider", "kind_resolver"],
			"contributes": {
				"mediaKinds": [{
					"key": "feature",
					"label": "Features",
					"library": {"name": "Studio Archive"},
					"levels": [{"key": "feature", "label": "Feature", "playable": true}],
					"fields": [
						{"key": "cast", "type": "people", "label": "Cast"},
						{"key": "directors", "type": "people", "label": "Directors"}
					],
					"present": {
						"plex": {"base": "movie"},
						"mediaBrowser": {"collectionType": "movies"},
						"silo": {"type": "Movie"}
					},
					"browse": {"search": true},
					"sync": {"mode": "push"}
				}]
			}
		}
	}`)
	script := []byte(`function handle(req) { return {status: 200, body: {status: "ok"}}; }`)
	preview, err := a.pluginsRT.mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: manifest,
		Files:    map[string][]byte{"dist/plugin.js": script},
	})
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	if _, err := a.pluginsRT.mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	marshalField := func(v any) json.RawMessage {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}

	// Seed 3 plugin items:
	// - "Beacon": direct title match for query "Beacon"
	// - "Northern Drift": features Elena Vance (with thumb)
	// - "Southern Tide": features Elena Vance (with thumb) AND Beacon Mercer (people match for "Beacon")
	if _, err := a.pluginsRT.pluginitems.UpsertItems(ctx, "community.example.archive", "feature", []core.KindItem{
		{
			ID:       "feat-beacon",
			Level:    "feature",
			Title:    "Beacon",
			Year:     2024,
			Released: 1704067200,
			Art:      []core.KindArtRef{{Role: "poster", URL: posterURL}},
			Fields: map[string]json.RawMessage{
				"cast": marshalField([]map[string]any{{"name": "Marcus Reed"}}),
			},
		},
		{
			ID:       "feat-north",
			Level:    "feature",
			Title:    "Northern Drift",
			Year:     2024,
			Released: 1706745600,
			Art:      []core.KindArtRef{{Role: "poster", URL: posterURL}},
			Fields: map[string]json.RawMessage{
				"cast": marshalField([]map[string]any{
					{"name": "Elena Vance", "role": "Lead", "thumb": portraitURL},
				}),
			},
		},
		{
			ID:       "feat-south",
			Level:    "feature",
			Title:    "Southern Tide",
			Year:     2023,
			Released: 1698796800,
			Art:      []core.KindArtRef{{Role: "poster", URL: posterURL}},
			Fields: map[string]json.RawMessage{
				"cast": marshalField([]map[string]any{
					{"name": "Elena Vance", "role": "Lead", "thumb": portraitURL},
					{"name": "Beacon Mercer", "role": "Guest"},
				}),
			},
		},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	northStored, libID, err := a.pluginsRT.pluginitems.GetByExternal(ctx, "community.example.archive", "feature", "feat-north")
	if err != nil {
		t.Fatal(err)
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
	for _, surface := range []string{"vela", "jellyfin", "emby", "silo"} {
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

	client := server.Client()
	doReq := func(method, path, body string, headers map[string]string) (int, []byte) {
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
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, data
	}

	t.Run("Vela", func(t *testing.T) {
		hdr := map[string]string{
			"Authorization":    "Bearer " + tokens["vela"],
			"X-Vela-Device-Id": "dev-vela",
		}
		// 1. Searching cast member name brings up all of Elena Vance's plugin titles.
		code, data := doReq(http.MethodPost, "/vela/v0/search", `{"idempotencyKey":"idem-vela-1","query":"Elena Vance","mode":"fast"}`, hdr)
		if code != 200 {
			t.Fatalf("Vela search Elena Vance: %d %s", code, data)
		}
		var searchRes struct {
			Items []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &searchRes); err != nil {
			t.Fatal(err)
		}
		if len(searchRes.Items) != 2 {
			t.Fatalf("Vela expected 2 titles for Elena Vance, got %+v", searchRes.Items)
		}

		// 2. Title-first ranking: "Beacon" ranks ahead of cast member match "Southern Tide".
		code, data = doReq(http.MethodPost, "/vela/v0/search", `{"idempotencyKey":"idem-vela-2","query":"Beacon","mode":"fast"}`, hdr)
		if code != 200 {
			t.Fatalf("Vela search Beacon: %d %s", code, data)
		}
		if err := json.Unmarshal(data, &searchRes); err != nil {
			t.Fatal(err)
		}
		if len(searchRes.Items) != 2 || searchRes.Items[0].Name != "Beacon" || searchRes.Items[1].Name != "Southern Tide" {
			t.Fatalf("Vela Beacon ranking = %+v", searchRes.Items)
		}

		// 3. Person detail + filmography + portrait load & resize.
		personGUID := core.FacetID("person", strconv.FormatInt(core.PluginPersonID("Elena Vance"), 10))
		code, data = doReq(http.MethodGet, "/vela/v0/people/"+personGUID, "", hdr)
		if code != 200 {
			t.Fatalf("Vela person detail: %d %s", code, data)
		}
		var personRes struct {
			Name  string `json:"name"`
			Photo struct {
				Sizes []struct {
					URL string `json:"url"`
				} `json:"sizes"`
			} `json:"photo"`
		}
		if err := json.Unmarshal(data, &personRes); err != nil {
			t.Fatal(err)
		}
		if personRes.Name != "Elena Vance" || len(personRes.Photo.Sizes) == 0 || personRes.Photo.Sizes[0].URL == "" {
			t.Fatalf("Vela person missing photo: %s", data)
		}
		photoPath := personRes.Photo.Sizes[0].URL
		code, imgBytes := doReq(http.MethodGet, photoPath, "", nil)
		if code != 200 {
			t.Fatalf("Vela full portrait: %d", code)
		}
		if w, h := decodeImageSize(t, imgBytes); w != 240 || h != 240 {
			t.Fatalf("Vela full portrait size = %dx%d, want 240x240", w, h)
		}
		parsedPhoto, err := url.Parse(photoPath)
		if err != nil {
			t.Fatal(err)
		}
		photoQuery := parsedPhoto.Query()
		photoQuery.Set("w", "60")
		parsedPhoto.RawQuery = photoQuery.Encode()
		code, smallBytes := doReq(http.MethodGet, parsedPhoto.String(), "", nil)
		if code != 200 {
			t.Fatalf("Vela resized portrait: %d %s", code, smallBytes)
		}
		if w, h := decodeImageSize(t, smallBytes); w != 60 || h != 60 {
			t.Fatalf("Vela resized portrait size = %dx%d, want 60x60", w, h)
		}

		code, data = doReq(http.MethodGet, "/vela/v0/people/"+personGUID+"/titles", "", hdr)
		if code != 200 {
			t.Fatalf("Vela person titles: %d %s", code, data)
		}
		var titlesRes struct {
			Items []struct {
				Role  string `json:"role"`
				Title struct {
					Name string `json:"name"`
				} `json:"title"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &titlesRes); err != nil {
			t.Fatal(err)
		}
		if len(titlesRes.Items) != 2 || titlesRes.Items[0].Title.Name == "" {
			t.Fatalf("Vela person filmography expected 2 titles, got %+v", titlesRes.Items)
		}
	})

	t.Run("JellyfinAndEmby", func(t *testing.T) {
		jfHdr := map[string]string{
			"Authorization": `MediaBrowser Client="Web", Device="Browser", DeviceId="dev-jellyfin", Version="10.9.0", Token="` + tokens["jellyfin"] + `"`,
		}
		emHdr := map[string]string{
			"X-Emby-Token":     tokens["emby"],
			"X-Emby-Device-Id": "dev-emby",
		}

		// 1. /Persons?searchTerm=Elena+Vance returns person with PrimaryImageTag and no fake TMDB ID.
		code, data := doReq(http.MethodGet, "/jellyfin/Persons?searchTerm=Elena+Vance", "", jfHdr)
		if code != 200 {
			t.Fatalf("Jellyfin /Persons: %d %s", code, data)
		}
		var personsRes struct {
			Items []struct {
				ID        string            `json:"Id"`
				Name      string            `json:"Name"`
				Type      string            `json:"Type"`
				ImageTags map[string]string `json:"ImageTags"`
			} `json:"Items"`
		}
		if err := json.Unmarshal(data, &personsRes); err != nil {
			t.Fatal(err)
		}
		if len(personsRes.Items) != 1 || personsRes.Items[0].Name != "Elena Vance" || personsRes.Items[0].ImageTags["Primary"] == "" {
			t.Fatalf("Jellyfin /Persons = %+v", personsRes.Items)
		}
		personID := personsRes.Items[0].ID
		tag := personsRes.Items[0].ImageTags["Primary"]

		// 2. /Items?Recursive=true&SearchTerm=Elena+Vance returns all of Elena Vance's titles.
		code, data = doReq(http.MethodGet, "/jellyfin/Items?Recursive=true&SearchTerm=Elena+Vance", "", jfHdr)
		if code != 200 {
			t.Fatalf("Jellyfin /Items search cast member: %d %s", code, data)
		}
		var itemsRes struct {
			Items []struct {
				Name string `json:"Name"`
			} `json:"Items"`
		}
		if err := json.Unmarshal(data, &itemsRes); err != nil {
			t.Fatal(err)
		}
		if len(itemsRes.Items) != 2 {
			t.Fatalf("Jellyfin /Items expected 2 titles for Elena Vance, got %+v", itemsRes.Items)
		}

		// 3. Emby /Search/Hints?searchTerm=Elena+Vance&IncludePeople=true returns Person hint AND both titles.
		code, data = doReq(http.MethodGet, "/emby/Search/Hints?searchTerm=Elena+Vance&IncludePeople=true", "", emHdr)
		if code != 200 {
			t.Fatalf("Emby /Search/Hints: %d %s", code, data)
		}
		var hintsRes struct {
			SearchHints []struct {
				Name string `json:"Name"`
				Type string `json:"Type"`
			} `json:"SearchHints"`
		}
		if err := json.Unmarshal(data, &hintsRes); err != nil {
			t.Fatal(err)
		}
		if len(hintsRes.SearchHints) != 3 || hintsRes.SearchHints[0].Type != "Person" {
			t.Fatalf("Emby /Search/Hints expected Person + 2 titles, got %+v", hintsRes.SearchHints)
		}

		// 4. Title-first ranking on /Items?Recursive=true&SearchTerm=Beacon.
		code, data = doReq(http.MethodGet, "/jellyfin/Items?Recursive=true&SearchTerm=Beacon", "", jfHdr)
		if code != 200 {
			t.Fatalf("Jellyfin /Items Beacon: %d %s", code, data)
		}
		if err := json.Unmarshal(data, &itemsRes); err != nil {
			t.Fatal(err)
		}
		if len(itemsRes.Items) != 2 || itemsRes.Items[0].Name != "Beacon" || itemsRes.Items[1].Name != "Southern Tide" {
			t.Fatalf("Jellyfin Beacon ranking = %+v", itemsRes.Items)
		}

		// 5. Person detail (/Items/{personId}), PersonIds filter, and portrait load + resize (?maxWidth=60).
		code, data = doReq(http.MethodGet, "/jellyfin/Items/"+personID, "", jfHdr)
		if code != 200 {
			t.Fatalf("Jellyfin person item detail: %d %s", code, data)
		}
		var personDTO struct {
			Type        string            `json:"Type"`
			ProviderIDs map[string]string `json:"ProviderIds"`
		}
		if err := json.Unmarshal(data, &personDTO); err != nil {
			t.Fatal(err)
		}
		if personDTO.Type != "Person" || personDTO.ProviderIDs["Tmdb"] != "" {
			t.Fatalf("unexpected Jellyfin person detail: %s", data)
		}

		code, data = doReq(http.MethodGet, "/jellyfin/Items?Recursive=true&PersonIds="+url.QueryEscape(personID), "", jfHdr)
		if code != 200 || json.Unmarshal(data, &itemsRes) != nil || len(itemsRes.Items) != 2 {
			t.Fatalf("Jellyfin /Items?PersonIds=%s expected 2 titles: %d %s", personID, code, data)
		}

		code, imgBytes := doReq(http.MethodGet, fmt.Sprintf("/jellyfin/Items/%s/Images/Primary?tag=%s", personID, url.QueryEscape(tag)), "", jfHdr)
		if code != 200 {
			t.Fatalf("Jellyfin full portrait: %d", code)
		}
		if w, h := decodeImageSize(t, imgBytes); w != 240 || h != 240 {
			t.Fatalf("Jellyfin full portrait = %dx%d, want 240x240", w, h)
		}
		code, smallBytes := doReq(http.MethodGet, fmt.Sprintf("/jellyfin/Items/%s/Images/Primary?tag=%s&maxWidth=60", personID, url.QueryEscape(tag)), "", jfHdr)
		if code != 200 {
			t.Fatalf("Jellyfin resized portrait: %d", code)
		}
		if w, h := decodeImageSize(t, smallBytes); w != 60 || h != 60 {
			t.Fatalf("Jellyfin resized portrait = %dx%d, want 60x60", w, h)
		}
	})

	t.Run("Silo", func(t *testing.T) {
		siloHdr := map[string]string{
			"Authorization":    "Bearer " + tokens["silo"],
			"X-Silo-Device-Id": "dev-silo",
			"X-Silo-Client":    "tv",
			"X-Profile-Id":     strconv.FormatInt(owner.ID, 10),
		}

		// 1. /silo/api/v2/catalog/people?q=Elena+Vance returns Elena Vance with photo_url and no synthetic tmdb_id.
		code, data := doReq(http.MethodGet, "/silo/api/v2/catalog/people?q=Elena+Vance", "", siloHdr)
		if code != 200 {
			t.Fatalf("Silo people search: %d %s", code, data)
		}
		var peopleRes struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(data, &peopleRes); err != nil {
			t.Fatal(err)
		}
		if len(peopleRes.Items) != 1 || peopleRes.Items[0]["name"] != "Elena Vance" {
			t.Fatalf("Silo people search = %+v", peopleRes.Items)
		}
		if _, hasTMDB := peopleRes.Items[0]["tmdb_id"]; hasTMDB {
			t.Fatalf("Silo leaked synthetic tmdb_id: %+v", peopleRes.Items[0])
		}
		personID, _ := peopleRes.Items[0]["id"].(string)
		photoURL, _ := peopleRes.Items[0]["photo_url"].(string)
		if personID == "" || photoURL == "" {
			t.Fatalf("Silo missing person id or photo_url: %+v", peopleRes.Items[0])
		}

		// 2. /silo/api/v2/catalog?q=Elena+Vance returns all of Elena Vance's plugin titles.
		code, data = doReq(http.MethodGet, "/silo/api/v2/catalog?q=Elena+Vance", "", siloHdr)
		if code != 200 {
			t.Fatalf("Silo catalog search cast member: %d %s", code, data)
		}
		var catRes struct {
			Items []struct {
				Title string `json:"title"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &catRes); err != nil {
			t.Fatal(err)
		}
		if len(catRes.Items) != 2 {
			t.Fatalf("Silo catalog search expected 2 titles for Elena Vance, got %+v", catRes.Items)
		}

		// 3. /silo/api/v2/catalog?person_id={personID} returns all of Elena Vance's titles.
		code, data = doReq(http.MethodGet, "/silo/api/v2/catalog?person_id="+url.QueryEscape(personID), "", siloHdr)
		if code != 200 {
			t.Fatalf("Silo catalog by person_id: %d %s", code, data)
		}
		if err := json.Unmarshal(data, &catRes); err != nil {
			t.Fatal(err)
		}
		if len(catRes.Items) != 2 {
			t.Fatalf("Silo catalog person_id expected 2 titles, got %+v", catRes.Items)
		}

		// 4. Title-first ranking on /silo/api/v2/catalog?q=Beacon.
		code, data = doReq(http.MethodGet, "/silo/api/v2/catalog?q=Beacon", "", siloHdr)
		if code != 200 {
			t.Fatalf("Silo catalog Beacon: %d %s", code, data)
		}
		if err := json.Unmarshal(data, &catRes); err != nil {
			t.Fatal(err)
		}
		if len(catRes.Items) != 2 || catRes.Items[0].Title != "Beacon" || catRes.Items[1].Title != "Southern Tide" {
			t.Fatalf("Silo Beacon ranking = %+v", catRes.Items)
		}

		// 5. Cast member photo loads full (240x240) and resizes with ?photo_width=60 (60x60).
		code, imgBytes := doReq(http.MethodGet, photoURL, "", nil)
		if code != 200 {
			t.Fatalf("Silo full portrait: %d", code)
		}
		if w, h := decodeImageSize(t, imgBytes); w != 240 || h != 240 {
			t.Fatalf("Silo full portrait = %dx%d, want 240x240", w, h)
		}
		code, data = doReq(http.MethodGet, "/silo/api/v2/catalog/people?q=Elena+Vance&photo_width=60", "", siloHdr)
		if code != 200 || json.Unmarshal(data, &peopleRes) != nil {
			t.Fatalf("Silo people photo_width=60: %d %s", code, data)
		}
		resizedURL, _ := peopleRes.Items[0]["photo_url"].(string)
		code, smallBytes := doReq(http.MethodGet, resizedURL, "", nil)
		if code != 200 {
			t.Fatalf("Silo resized portrait: %d", code)
		}
		if w, h := decodeImageSize(t, smallBytes); w != 60 || h != 60 {
			t.Fatalf("Silo resized portrait = %dx%d, want 60x60", w, h)
		}
	})

	t.Run("Plex", func(t *testing.T) {
		plexHdr := map[string]string{
			"X-Plex-Token": "test-owner",
			"Accept":       "application/json",
		}

		// 1. /hubs/search?query=Elena+Vance returns Actor hub entry (with thumb) AND Movies hub with both titles.
		code, data := doReq(http.MethodGet, "/hubs/search?query=Elena+Vance", "", plexHdr)
		if code != 200 {
			t.Fatalf("Plex /hubs/search: %d %s", code, data)
		}
		var hubsBody struct {
			MediaContainer struct {
				Hub []struct {
					HubIdentifier string `json:"hubIdentifier"`
					Directory     []struct {
						Tag   string `json:"tag"`
						Thumb string `json:"thumb"`
						Key   string `json:"key"`
					} `json:"Directory"`
					Metadata []struct {
						Title string `json:"title"`
					} `json:"Metadata"`
				} `json:"Hub"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(data, &hubsBody); err != nil {
			t.Fatal(err)
		}
		var actorThumb, actorKey string
		var movieTitles []string
		for _, h := range hubsBody.MediaContainer.Hub {
			if h.HubIdentifier == "actor" && len(h.Directory) > 0 {
				actorThumb = h.Directory[0].Thumb
				actorKey = h.Directory[0].Key
			}
			if h.HubIdentifier == "movie" {
				for _, m := range h.Metadata {
					movieTitles = append(movieTitles, m.Title)
				}
			}
		}
		if actorThumb == "" || actorKey == "" {
			t.Fatalf("Plex /hubs/search missing actor directory or thumb: %s", data)
		}
		if len(movieTitles) != 2 {
			t.Fatalf("Plex /hubs/search Movies hub expected 2 titles, got %v", movieTitles)
		}

		// 2. Following the actor section key AND /library/people/tmdb-person-{id}/media returns both titles.
		code, data = doReq(http.MethodGet, actorKey, "", plexHdr)
		if code != 200 {
			t.Fatalf("Plex section actor filter (%s): %d %s", actorKey, code, data)
		}
		var secMedia struct {
			MediaContainer struct {
				Metadata []struct {
					Title string `json:"title"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(data, &secMedia); err != nil {
			t.Fatal(err)
		}
		if len(secMedia.MediaContainer.Metadata) != 2 {
			t.Fatalf("Plex section actor filter expected 2 titles, got %+v", secMedia.MediaContainer.Metadata)
		}

		personNumericID := core.PluginPersonID("Elena Vance")
		code, data = doReq(http.MethodGet, fmt.Sprintf("/library/people/tmdb-person-%d/media", personNumericID), "", plexHdr)
		if code != 200 || json.Unmarshal(data, &secMedia) != nil || len(secMedia.MediaContainer.Metadata) != 2 {
			t.Fatalf("Plex /library/people media: %d %s", code, data)
		}

		// 3. /library/search?query=Elena+Vance returns both Directory (person) and Metadata (both titles).
		code, data = doReq(http.MethodGet, "/library/search?query=Elena+Vance", "", plexHdr)
		if code != 200 {
			t.Fatalf("Plex /library/search: %d %s", code, data)
		}
		var libSearch struct {
			MediaContainer struct {
				SearchResult []struct {
					Directory *struct {
						Tag string `json:"tag"`
					} `json:"Directory"`
					Metadata *struct {
						Title string `json:"title"`
					} `json:"Metadata"`
				} `json:"SearchResult"`
			} `json:"MediaContainer"`
		}
		if err := json.Unmarshal(data, &libSearch); err != nil {
			t.Fatal(err)
		}
		if len(libSearch.MediaContainer.SearchResult) != 3 {
			t.Fatalf("Plex /library/search expected 1 person + 2 titles, got %d: %s", len(libSearch.MediaContainer.SearchResult), data)
		}

		// 4. Title-first ranking on /search?query=Beacon and /library/sections/{libID}/search?query=Beacon.
		code, data = doReq(http.MethodGet, "/search?query=Beacon", "", plexHdr)
		if code != 200 || json.Unmarshal(data, &secMedia) != nil {
			t.Fatalf("Plex /search Beacon: %d %s", code, data)
		}
		if len(secMedia.MediaContainer.Metadata) != 2 || secMedia.MediaContainer.Metadata[0].Title != "Beacon" || secMedia.MediaContainer.Metadata[1].Title != "Southern Tide" {
			t.Fatalf("Plex /search Beacon ranking = %+v", secMedia.MediaContainer.Metadata)
		}
		code, data = doReq(http.MethodGet, fmt.Sprintf("/library/sections/%d/search?query=Beacon", libID), "", plexHdr)
		if code != 200 || json.Unmarshal(data, &secMedia) != nil {
			t.Fatalf("Plex section search Beacon: %d %s", code, data)
		}
		if len(secMedia.MediaContainer.Metadata) != 2 || secMedia.MediaContainer.Metadata[0].Title != "Beacon" || secMedia.MediaContainer.Metadata[1].Title != "Southern Tide" {
			t.Fatalf("Plex section search Beacon ranking = %+v", secMedia.MediaContainer.Metadata)
		}
		_ = northStored

		// 5. Cast member thumb loads full (240x240) and resizes via /photo/:/transcode?width=60&height=60 (60x60).
		code, imgBytes := doReq(http.MethodGet, actorThumb, "", plexHdr)
		if code != 200 {
			t.Fatalf("Plex full actor thumb (%s): %d", actorThumb, code)
		}
		if w, h := decodeImageSize(t, imgBytes); w != 240 || h != 240 {
			t.Fatalf("Plex full actor thumb = %dx%d, want 240x240", w, h)
		}
		transcodePath := "/photo/:/transcode?width=60&height=60&url=" + url.QueryEscape(actorThumb)
		code, smallBytes := doReq(http.MethodGet, transcodePath, "", plexHdr)
		if code != 200 {
			t.Fatalf("Plex transcoded actor thumb: %d %s", code, smallBytes)
		}
		if w, h := decodeImageSize(t, smallBytes); w != 60 || h != 60 {
			t.Fatalf("Plex transcoded actor thumb = %dx%d, want 60x60", w, h)
		}
	})
}
