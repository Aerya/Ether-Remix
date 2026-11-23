package plex

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/livetv"
)

type logoFetcher struct {
	urls        []string
	data        []byte
	contentType string
}

func (f *logoFetcher) GetUntrustedURL(_ context.Context, u string) ([]byte, string, error) {
	f.urls = append(f.urls, u)
	return f.data, f.contentType, nil
}

func TestPlexWebGuideLogoUsesServerOriginAndViewer(t *testing.T) {
	f, service, channel := liveFixture(t)
	response := f.get(t, liveProviderPath+"/lineups/dvr/channels", "Accept", "application/json", "X-Plex-Product", "Plex Web")
	channels := decodeJSON(t, response)["Channel"].([]any)
	logo, err := url.Parse(channels[0].(map[string]any)["thumb"].(string))
	if err != nil || logo.Scheme != "http" || logo.Host != "example.com" || logo.Path != liveLogo(livetv.ToFrontendChannel(channel)) || logo.Query().Get("X-Plex-Token") != ownerToken {
		t.Fatal("Plex Web logo does not point to the authenticated server")
	}
	if rec := f.get(t, logo.RequestURI()); rec.Code != http.StatusOK {
		t.Fatalf("logo status: %d", rec.Code)
	}
	if err := service.SetChannelViewers(context.Background(), channel.ID, livetv.ChannelViewers{Deny: []int64{friendUser.ID}}); err != nil {
		t.Fatal(err)
	}
	logo.RawQuery = url.Values{"X-Plex-Token": {friendToken}}.Encode()
	if rec := f.get(t, logo.RequestURI()); rec.Code != http.StatusNotFound {
		t.Fatalf("restricted logo status: %d", rec.Code)
	}
}

// Plex for Android (Mobile and TV) shares the Bedrock Live TV guide and
// fetches channel logos through Plex's cloud image service. Even when the
// client connects directly over a LAN address at home, both the lineup thumb
// and airing channelThumb must publish a public, token-free logo URL (either
// the channel's own public HTTPS icon or this server's signed /livetv/logos/
// URL on PublicOrigin), while remaining locally servable via /photo/:/transcode.
func TestAndroidGuideLogoIsCloudFetchableOnLAN(t *testing.T) {
	f, service, channel := liveFixture(t)
	const publicBase = "https://159-117-165-230.abc123.plex.direct:32451"
	f.srv.setTestPublicURL(func() string { return publicBase })

	const iptvPublicLogo = "https://cdn.example.org/iptv/sport1.png"
	src := livetv.Source{
		Name:     "Public IPTV",
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"sport1\" tvg-chno=\"8\" tvg-logo=\"" + iptvPublicLogo + "\",Sport 1\nhttps://stream.example.test/live\n",
		Guide:    `<tv><channel id="sport1"><display-name>Sport 1</display-name></channel><programme channel="sport1" start="20261018110000 +0000" stop="20261018130000 +0000"><title>Match Live</title></programme></tv>`,
	}
	if _, err := service.ImportSource(context.Background(), src); err != nil {
		t.Fatal(err)
	}

	liveSearch := service.Frontend().(frontend.LiveTVSearch)
	for deadline := time.Now().Add(2 * time.Second); ; {
		hits, err := liveSearch.SearchLiveTV(context.Background(), &ownerUser, "Owner programme", 10)
		if err != nil {
			t.Fatal(err)
		}
		if !hits.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search index did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	getReady := func(target string, hdr map[string]string) map[string]any {
		t.Helper()
		rec := f.request(t, http.MethodGet, target, hdr)
		for deadline := time.Now().Add(2 * time.Second); rec.Code == http.StatusServiceUnavailable && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
			rec = f.request(t, http.MethodGet, target, hdr)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
		return decodeJSON(t, rec)
	}

	for _, lanOrigin := range []string{
		"https://192-168-2-10.abc123.plex.direct:32450",
		"http://192.168.2.10:32450",
	} {
		for _, product := range []string{"Plex for Android (Mobile)", "Plex for Android (TV)"} {
			hdr := map[string]string{
				"Accept":          "application/json",
				"X-Plex-Product":  product,
				"X-Plex-Platform": "Android",
				"X-Plex-Version":  "2026.23.0",
			}
			lineup := decodeJSON(t, f.request(t, http.MethodGet, lanOrigin+liveProviderPath+"/lineups/dvr/channels", hdr))
			thumbsByID := map[string]string{}
			for _, raw := range lineup["Channel"].([]any) {
				ch := raw.(map[string]any)
				thumb := ch["thumb"].(string)
				if strings.Contains(thumb, "192.168.2.10") || strings.Contains(thumb, "192-168-2-10") || strings.Contains(thumb, "X-Plex-Token") || strings.Contains(thumb, ownerToken) {
					t.Fatalf("%s on %s leaked LAN host or token in lineup thumb: %q", product, lanOrigin, thumb)
				}
				thumbsByID[ch["id"].(string)] = thumb
			}

			signed := thumbsByID[channel.ID]
			logo, err := url.Parse(signed)
			if err != nil || !strings.HasPrefix(signed, publicBase+signedLogoPrefix+channel.ID+"/") {
				t.Fatalf("%s on %s did not publish signed public logo URL: %q", product, lanOrigin, signed)
			}
			for id, thumb := range thumbsByID {
				if id != channel.ID && thumb != iptvPublicLogo {
					t.Fatalf("%s on %s rewrote public IPTV logo to %q, want %q", product, lanOrigin, thumb, iptvPublicLogo)
				}
			}

			for _, guidePath := range []string{"/grid?date=2026-10-18", "/watchnow/all"} {
				guide := getReady(lanOrigin+liveProviderPath+guidePath, hdr)
				for _, raw := range guide["Metadata"].([]any) {
					media := raw.(map[string]any)["Media"].([]any)[0].(map[string]any)
					chID := media["channelIdentifier"].(string)
					if got := media["channelThumb"].(string); got != thumbsByID[chID] {
						t.Fatalf("%s on %s %s channelThumb for %s = %q, want %q", product, lanOrigin, guidePath, chID, got, thumbsByID[chID])
					}
				}
			}

			search := getReady(lanOrigin+liveProviderPath+"/library/search?query="+url.QueryEscape("Owner programme"), hdr)
			results, _ := search["SearchResult"].([]any)
			var checkedSearch bool
			for _, raw := range results {
				md, ok := raw.(map[string]any)["Metadata"].(map[string]any)
				if !ok {
					continue
				}
				checkedSearch = true
				got := md["Media"].([]any)[0].(map[string]any)["channelThumb"].(string)
				if got != signed {
					t.Fatalf("%s on %s search channelThumb = %q, want %q", product, lanOrigin, got, signed)
				}
			}
			if !checkedSearch {
				t.Fatalf("%s on %s returned no programme search results: %#v", product, lanOrigin, search)
			}

			// Plex's cloud fetches the signed URL without credentials.
			cloudRec := f.request(t, http.MethodGet, logo.RequestURI(), map[string]string{"X-Plex-Token": "", "X-Plex-Client-Identifier": ""})
			if _, err := png.Decode(bytes.NewReader(cloudRec.Body.Bytes())); cloudRec.Code != http.StatusOK || err != nil {
				t.Fatalf("cloud fetch %s: %d %v", logo.RequestURI(), cloudRec.Code, err)
			}
			// A client wrapping the signed public URL in /photo/:/transcode on LAN also succeeds locally.
			transcode := f.get(t, "/photo/:/transcode?width=120&height=80&format=jpeg&url="+url.QueryEscape(signed), "X-Plex-Product", product, "X-Plex-Platform", "Android")
			if _, kind, err := image.DecodeConfig(bytes.NewReader(transcode.Body.Bytes())); transcode.Code != http.StatusOK || err != nil || kind != "jpeg" {
				t.Fatalf("LAN /photo/:/transcode on signed logo: %d %s %v", transcode.Code, kind, err)
			}
		}
	}
}

func TestPlexWebGuideLogoEscapesChannelID(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	r := httptest.NewRequest("GET", "http://example.com/livetv/dvr/channels", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, &Request{Token: ownerToken, Product: "Plex Web"}))
	got := f.srv.liveGuideLogo(r, livetv.ToFrontendChannel(livetv.Channel{ID: "a b%c"}))
	if !strings.HasPrefix(got, "http://example.com/livetv/channels/a%20b%25c/logo.png?") {
		t.Fatalf("channel ID is not escaped exactly once: %s", got)
	}
}

func TestLiveChannelLogoPlezyCompatibility(t *testing.T) {
	f, service, generated := liveFixture(t)
	src := livetv.Source{Name: "Logo source", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"external\" tvg-logo=\"https://logos.example.test/logo.svg\",External\nhttps://stream.example.test/live\n"}
	if _, err := service.ImportSource(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	channels, err := service.Channels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var external livetv.Channel
	for _, c := range channels {
		if c.Name == "External" {
			external = c
		}
	}
	if external.ID == "" {
		t.Fatal("external channel missing")
	}
	fetch := &logoFetcher{data: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="red"/></svg>`), contentType: "image/svg+xml"}
	f.srv.channelLogos = fetch
	for _, channel := range []livetv.Channel{generated, external} {
		path := liveLogo(livetv.ToFrontendChannel(channel))
		for _, target := range []string{path, "/photo/:/transcode?width=120&height=80&url=" + url.QueryEscape(path+"?X-Plex-Token=ignored"), "/photo/:/transcode?url=" + url.QueryEscape(strings.Replace(path, "logo.png", "logo.svg", 1))} {
			rec := f.get(t, target, "X-Plex-Token", ownerToken)
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("%s: %d %s", target, rec.Code, rec.Header().Get("Content-Type"))
			}
			if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
				t.Fatalf("%s: invalid PNG: %v", target, err)
			}
		}
	}
	if len(fetch.urls) != 3 {
		t.Fatalf("fetch count: %v", fetch.urls)
	}
	for _, u := range fetch.urls {
		if u != external.Logo {
			t.Fatalf("unexpected fetched URL %q", u)
		}
	}
	// Unsafe SVG cannot trigger a fetch or active content in the client.
	fetch.data = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="http://127.0.0.1/secret"/></svg>`)
	rec := f.get(t, liveLogo(livetv.ToFrontendChannel(external)))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("unsafe SVG fallback: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Fatalf("unsafe SVG fallback is not PNG: %v", err)
	}
	if err := service.SetChannelViewers(context.Background(), external.ID, livetv.ChannelViewers{Deny: []int64{friendUser.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{liveLogo(livetv.ToFrontendChannel(external)), "/photo/:/transcode?url=" + url.QueryEscape(liveLogo(livetv.ToFrontendChannel(external))+"?X-Plex-Token="+ownerToken)} {
		rec := f.get(t, target, "X-Plex-Token", friendToken)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("restricted %s: %d", target, rec.Code)
		}
	}
	if len(fetch.urls) != 4 {
		t.Fatal("unauthorized request fetched artwork")
	}
	for _, target := range []string{"https://evil.example" + liveLogo(livetv.ToFrontendChannel(external)), "http://169.254.169.254/latest/meta-data"} {
		if rec := f.get(t, "/photo/:/transcode?url="+url.QueryEscape(target)); rec.Code != http.StatusForbidden {
			t.Fatalf("remote %s: %d", target, rec.Code)
		}
	}
	f.srv.setTestSubscription(false)
	if rec := f.get(t, "/photo/:/transcode?url="+url.QueryEscape(liveLogo(livetv.ToFrontendChannel(external)))); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled live TV: %d", rec.Code)
	}
}

func TestLiveChannelLogoBlocksPrivateSource(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	f, service, _ := liveFixture(t)
	src := livetv.Source{Name: "Private logo", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"private\" tvg-logo=\"" + upstream.URL + "\",Private logo\nhttps://stream.example.test/live\n"}
	if _, err := service.ImportSource(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	channels, err := service.Channels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range channels {
		if c.Name == "Private logo" {
			rec := f.get(t, "/photo/:/transcode?url="+url.QueryEscape(liveLogo(livetv.ToFrontendChannel(c))))
			if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("fallback: %d", rec.Code)
			}
			if requests.Load() != 0 {
				t.Fatal("private upstream reached")
			}
			return
		}
	}
	t.Fatal("channel missing")
}

func TestLiveChannelLogoFallsBackToGuideLogo(t *testing.T) {
	f, service, _ := liveFixture(t)
	src := livetv.Source{
		Name:     "Guide fallback logo",
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"fallback-guide\" tvg-logo=\"https://logos.example.test/failing.png\",Fallback channel\nhttps://stream.example.test/live\n",
		Guide:    `<tv><channel id="fallback-guide"><display-name>Fallback channel</display-name><icon src="https://logos.example.test/guide.png"/></channel></tv>`,
	}
	if _, err := service.ImportSource(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	channels, err := service.Channels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var target livetv.Channel
	for _, c := range channels {
		if c.Name == "Fallback channel" {
			target = c
		}
	}
	if target.ID == "" {
		t.Fatal("channel missing")
	}

	fetch := &mappedLogoFetcher{
		urls: map[string]mappedLogoResult{
			"https://logos.example.test/failing.png": {err: errors.New("upstream failed")},
			"https://logos.example.test/guide.png":   {data: []byte("guide-png-content"), ct: "image/png"},
		},
	}
	f.srv.channelLogos = fetch

	rec := f.get(t, liveLogo(livetv.ToFrontendChannel(target)), "X-Plex-Token", ownerToken)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("expected 200 image/png, got %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "guide-png-content" {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}

type mappedLogoResult struct {
	data []byte
	ct   string
	err  error
}

type mappedLogoFetcher struct {
	urls map[string]mappedLogoResult
}

func (f *mappedLogoFetcher) GetUntrustedURL(_ context.Context, u string) ([]byte, string, error) {
	if res, ok := f.urls[u]; ok {
		return res.data, res.ct, res.err
	}
	return nil, "", errors.New("unknown url")
}

// Plex for Apple TV draws guide logos only through Plex's cloud (#391): it
// gets the channel's public logo as is, or this server's signed, token-free
// logo URL (on the public address when remote access publishes one), in both
// the lineup and the grid. No viewer token may leave in it.
func TestAppleTVGuideLogoIsCloudFetchable(t *testing.T) {
	f, _, channel := liveFixture(t)
	appleTV := []string{"Accept", "application/json", "X-Plex-Product", "Plex for Apple TV", "X-Plex-Platform", "tvOS"}
	lineupThumb := func() string {
		return decodeJSON(t, f.get(t, liveProviderPath+"/lineups/dvr/channels", appleTV...))["Channel"].([]any)[0].(map[string]any)["thumb"].(string)
	}
	got := lineupThumb()
	logo, err := url.Parse(got)
	if err != nil || logo.Scheme != "http" || logo.Host != "example.com" || !strings.HasPrefix(logo.Path, signedLogoPrefix+channel.ID+"/") || strings.Contains(got, "X-Plex-Token") || strings.Contains(got, ownerToken) {
		t.Fatalf("Apple TV logo is not a signed, token-free URL: %q", got)
	}
	grid := decodeJSON(t, f.get(t, liveProviderPath+"/grid?date=2026-10-18", appleTV...))
	if thumb := grid["Metadata"].([]any)[0].(map[string]any)["Media"].([]any)[0].(map[string]any)["channelThumb"]; thumb != got {
		t.Fatalf("grid channelThumb %v differs from lineup %q", thumb, got)
	}
	// Plex's cloud fetches it with no credentials at all.
	rec := f.request(t, http.MethodGet, logo.RequestURI(), map[string]string{"X-Plex-Token": "", "X-Plex-Client-Identifier": ""})
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("signed logo: %d %v", rec.Code, err)
	}
	tampered := strings.Replace(logo.RequestURI(), "/"+channel.ID+"/", "/"+channel.ID+"/x", 1)
	if rec := f.request(t, http.MethodGet, tampered, map[string]string{"X-Plex-Token": ""}); rec.Code != http.StatusNotFound {
		t.Fatalf("tampered signature: %d", rec.Code)
	}
	f.srv.setTestPublicURL(func() string { return "https://203-0-113-7.abc123.plex.direct:32400" })
	if got := lineupThumb(); !strings.HasPrefix(got, "https://203-0-113-7.abc123.plex.direct:32400"+signedLogoPrefix) {
		t.Fatalf("logo ignores the public address: %q", got)
	}
	// Plezy on Apple and Android platforms loads logos through
	// /photo/:/transcode, not Plex's cloud: it keeps this server's own logo path.
	wantPlezy := liveLogo(livetv.ToFrontendChannel(channel))
	for _, plat := range []string{"tvOS", "iOS", "Android"} {
		plezy := decodeJSON(t, f.get(t, liveProviderPath+"/lineups/dvr/channels", "Accept", "application/json", "X-Plex-Product", "Plezy", "X-Plex-Platform", plat))
		if thumb := plezy["Channel"].([]any)[0].(map[string]any)["thumb"].(string); thumb != wantPlezy {
			t.Fatalf("Plezy/%s got %q, want %q", plat, thumb, wantPlezy)
		}
	}
	// Plex Web keeps the authenticated server URL even on mobile OS platforms.
	for _, plat := range []string{"Chrome", "Android", "iOS"} {
		web := decodeJSON(t, f.get(t, liveProviderPath+"/lineups/dvr/channels", "Accept", "application/json", "X-Plex-Product", "Plex Web", "X-Plex-Platform", plat))
		if thumb := web["Channel"].([]any)[0].(map[string]any)["thumb"].(string); !strings.Contains(thumb, "X-Plex-Token=") {
			t.Fatalf("Plex Web (%s) logo changed: %q", plat, thumb)
		}
	}

	ctx := context.Background()
	for raw, public := range map[string]bool{
		"https://cdn.example.org/logos/hbo.png": true,
		"http://images.example.net/a.jpg":       true,
		"https://cdn.example.org/logos/hbo.svg": false,
		"http://192.168.1.5:8080/logo.png":      false,
		"http://tuliprox:8080/logo.png":         false,
		"https://nas.local/logo.png":            false,
		"data:image/png;base64,AAAA":            false,
	} {
		c := livetv.ToFrontendChannel(livetv.Channel{ID: "c1", Logo: raw})
		if out := f.srv.cloudGuideLogo(ctx, c, "http://example.com"); (out == raw) != public {
			t.Errorf("logo %q: published %q", raw, out)
		}
	}
}

// A shipped decade channel's logo is its own artwork from Ether's public
// repository at this build's source, for Apple TV and every other client.
// An owner's logo or new name wins.
func TestShowcaseChannelLogo(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.setTestRepoFileURL(func(path string) string { return "https://cdn.example/ether@v1/" + path })
	ctx := context.Background()
	rawCh := livetv.Channel{ID: "showcase-chroma-80", Name: "CHROMA 80", Kind: "virtual"}
	c := livetv.ToFrontendChannel(rawCh)
	if got := f.srv.cloudGuideLogo(ctx, c, "http://example.com"); got != "https://cdn.example/ether@v1/internal/livetv/showcaselogos/v2/chroma-80.png" {
		t.Fatalf("shipped logo: %q", got)
	}
	rec := httptest.NewRecorder()
	f.srv.writeChannelLogo(rec, httptest.NewRequest("GET", "/livetv/channels/showcase-chroma-80/logo.png", nil), c, true, "private")
	_, want, _ := livetv.ShowcaseLogo(rawCh)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("served logo: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	owner := c
	owner.Logo = "https://cdn.example.org/mine.png"
	renamed := c
	renamed.Name = "My 80s"
	for _, edited := range []frontend.LiveTVChannel{owner, renamed} {
		if got := f.srv.cloudGuideLogo(ctx, edited, "http://example.com"); strings.Contains(got, "showcaselogos") {
			t.Fatalf("owner edit lost: %q", got)
		}
	}
}
