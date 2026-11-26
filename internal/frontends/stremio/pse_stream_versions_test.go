package stremio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

func TestPSEParseLinkOptions(t *testing.T) {
	for in, want := range map[string]pseLinkOptions{
		"direct-1":    {pseFormatDirect, 1},
		"compact-3":   {pseFormatCompact, 3},
		"detaille-10": {pseFormatDetailed, 10},
	} {
		if got, ok := pseParseLinkOptions(in); !ok || got != want {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "compact", "compact-", "compact-0", "compact-11", "compact-x", "compact--2", "fancy-2", "-3", "compact-+3", "compact-003"} {
		if _, ok := pseParseLinkOptions(in); ok {
			t.Errorf("%q must be rejected", in)
		}
	}
}

type versionLazy struct{ lazyFixture }

func (v *versionLazy) Issue(_ context.Context, credential string, req frontend.PlanRequest) (string, error) {
	if credential != "grant" {
		return "", fmt.Errorf("wrong scope")
	}
	if req.Version == "" {
		return "/sessions/lazy/auto/file", nil
	}
	if !req.Intent.ExactVersion || !req.Intent.Estimate {
		return "", fmt.Errorf("version must be exact")
	}
	if strings.HasSuffix(req.Version, "bad") {
		return "", fmt.Errorf("boom")
	}
	return "/sessions/lazy/" + req.Version + "/file", nil
}

type fakeHandles struct{ frontend.VersionHandles }

func (fakeHandles) Issue(_ context.Context, _ frontend.VersionScope, _ frontend.RatingKey, id string) (string, error) {
	return "h-" + id, nil
}

func versionList(t *testing.T, path string, versions []frontend.Version) *httptest.ResponseRecorder {
	t.Helper()
	exp := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Versions: versions}, nil
	}}
	host := frontend.Host{
		Identity:   frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 7}, nil }},
		Experience: exp, Sessions: frontendtest.Sessions{}, LazyStreams: &versionLazy{}, VersionHandles: fakeHandles{},
		Capabilities: frontendtest.Capabilities{}, ClientIP: func(*http.Request) (netip.Addr, bool) { return netip.MustParseAddr("192.168.1.7"), true },
		SecurePortalURL: func(context.Context, *http.Request) (string, error) { return "https://ether.example/ether/me", nil },
	}
	inst, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func testVersions() []frontend.Version {
	hdr := true
	return []frontend.Version{
		{ID: "ether:auto", Name: "Auto · 2160p WEB-DL · DD+ 5.1 · FLUX", Media: frontend.MediaInfo{Size: 8 << 30, Streams: []frontend.StreamInfo{{Type: "video", Codec: "hevc", Width: 3840, Height: 1600, ColorTransfer: "smpte2084", HDR10Plus: &hdr}, {Type: "audio", Codec: "eac3", Channels: 6, Language: "fre"}}}, FileName: "Film.2160p.WEB-DL.DDP5.1-FLUX.mkv"},
		{ID: "r1", Name: "1080p Blu-ray · AMIABLE", Edition: "Director's Cut", Media: frontend.MediaInfo{Size: 3 << 30}},
		{ID: "ph", Name: "Same-quality version 1"},
		{ID: "bad", Name: "720p WEB-DL"},
		{ID: "r3", Name: "720p HDTV"},
	}
}

func streamsOf(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var body struct{ Streams []map[string]any }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return body.Streams
}

func TestVersionListDirectKeepsSingleStream(t *testing.T) {
	for _, path := range []string{"/stremio/grant/stream/movie/tmdb:42.json", "/stremio/grant/o/direct-5/stream/movie/tmdb:42.json"} {
		s := streamsOf(t, versionList(t, path, testVersions()))
		if len(s) != 1 || s[0]["name"] != "Ether" || s[0]["description"] != "Auto · 2160p WEB-DL · DD+ 5.1 · FLUX" {
			t.Fatalf("%s: %v", path, s)
		}
	}
}

func TestVersionListCompactSkipsPlaceholdersAndFailures(t *testing.T) {
	w := versionList(t, "/stremio/grant/o/compact-5/stream/movie/tmdb:42.json", testVersions())
	s := streamsOf(t, w)
	if len(s) != 3 {
		t.Fatalf("want auto + 2 (placeholder and failed handle dropped): %s", w.Body.String())
	}
	if s[0]["name"] != "Ether 2160p" || !strings.Contains(s[0]["url"].(string), "/sessions/lazy/auto/file") {
		t.Fatalf("auto: %v", s[0])
	}
	if s[1]["description"] != "1080p Blu-ray · AMIABLE · 3,0 Go · Director's Cut" || !strings.Contains(s[1]["url"].(string), "/sessions/lazy/h-r1/file") {
		t.Fatalf("second: %v", s[1])
	}
	if s[1]["behaviorHints"].(map[string]any)["bingeGroup"] != "ether-1080p" {
		t.Fatalf("hints: %v", s[1])
	}
	for _, x := range s {
		if strings.Contains(x["description"].(string), "Same-quality") || strings.Contains(x["description"].(string), "\n") {
			t.Fatalf("compact line: %v", x)
		}
	}
}

func TestVersionListCountBoundsStreams(t *testing.T) {
	if s := streamsOf(t, versionList(t, "/stremio/grant/o/compact-2/stream/movie/tmdb:42.json", testVersions())); len(s) != 2 {
		t.Fatalf("got %d", len(s))
	}
	if s := streamsOf(t, versionList(t, "/stremio/grant/fichiers/o/compact-1/stream/movie/tmdb:42.json", testVersions())); len(s) != 1 {
		t.Fatalf("got %d", len(s))
	}
}

func TestVersionListDetailed(t *testing.T) {
	s := streamsOf(t, versionList(t, "/stremio/grant/o/detaille-2/stream/movie/tmdb:42.json", testVersions()))
	if s[0]["name"] != "Ether ⚡ 2160p" {
		t.Fatalf("name: %v", s[0]["name"])
	}
	desc := s[0]["description"].(string)
	for _, want := range []string{"2160p • WEB-DL • HDR10+", "🌐 FR • 💾 8,0 Go", "🎞 HEVC • 🔊 DD+ 5.1 • 👥 FLUX", "📁 Film.2160p.WEB-DL.DDP5.1-FLUX.mkv"} {
		if !strings.Contains(desc, want) {
			t.Errorf("missing %q in %q", want, desc)
		}
	}
	hints := s[0]["behaviorHints"].(map[string]any)
	if hints["bingeGroup"] != "ether-2160p" || hints["videoSize"].(float64) != float64(8<<30) {
		t.Fatalf("hints: %v", hints)
	}
}

func TestInvalidLinkOptionsAre404(t *testing.T) {
	for _, path := range []string{"/stremio/grant/o/compact-99/stream/movie/tmdb:42.json", "/stremio/grant/o/zzz/manifest.json", "/stremio/grant/fichiers/o/compact/manifest.json"} {
		if w := versionList(t, path, testVersions()); w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if w := versionList(t, "/stremio/grant/o/detaille-3/manifest.json", nil); w.Code != 200 {
		t.Errorf("manifest: %d", w.Code)
	}
}
