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
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

func TestPSEParseLinkOptions(t *testing.T) {
	for in, want := range map[string]pseLinkOptions{
		"direct-1":                     {Format: pseFormatDirect, Legacy: 1},
		"compact-3":                    {Format: pseFormatCompact, Legacy: 3},
		"detaille-10":                  {Format: pseFormatDetailed, Legacy: 10},
		"detaille-2160.2-1080.3-720.1": {Format: pseFormatDetailed, Quotas: []pseQuota{{2160, 2}, {1080, 3}, {720, 1}}},
		"compact-1080.0":               {Format: pseFormatCompact, Quotas: []pseQuota{{1080, 0}}},
	} {
		got, ok := pseParseLinkOptions(in)
		if !ok || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "compact", "compact-", "compact-0", "compact-11", "compact-x", "compact--2", "fancy-2", "-3", "compact-003", "compact-2160.9-1080.9", "compact-2160.1-2160.1", "compact-480.1", "compact-1080", "compact-1080.", "compact-.2"} {
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

type fakeEditions struct {
	cold   bool
	ranked []frontend.ReleaseVariant
}

func (*fakeEditions) Editions(context.Context, *frontend.Viewer, frontend.Device, frontend.RatingKey) ([]frontend.Version, error) {
	return nil, nil
}
func (*fakeEditions) Variants(context.Context, frontend.ResolveRequest, int) ([]frontend.ReleaseVariant, error) {
	return nil, nil
}
func (*fakeEditions) Available(context.Context, frontend.RatingKey) (bool, bool) { return false, false }

func (f *fakeEditions) RankedReleases(context.Context, frontend.ResolveRequest) ([]frontend.ReleaseVariant, error) {
	if f.cold {
		return nil, nil
	}
	return f.ranked, nil
}

func (f *fakeEditions) RefillSearchCache(*frontend.Title) <-chan struct{} {
	if !f.cold {
		return nil
	}
	f.cold = false
	done := make(chan struct{})
	close(done)
	return done
}

func rel(id string, height int, src frontend.ReleaseVariant) frontend.ReleaseVariant {
	src.ID = id
	src.Release.ID = id
	src.Release.Info.Height = height
	return src
}

func testRanked() []frontend.ReleaseVariant {
	r1 := rel("a", 2160, frontend.ReleaseVariant{})
	r1.Release.Name = "Film.2160p.REMUX.DV.MULTI-FLUX"
	r1.Release.Indexer, r1.Release.Size = "Alkolites", 8<<30
	r1.Release.Info.Source, r1.Release.Info.DolbyVision = "remux", true
	r1.Release.Info.VideoCodec, r1.Release.Info.AudioCodec, r1.Release.Info.Channels, r1.Release.Info.Group = "hevc", "eac3", "5.1", "FLUX"
	r1.Release.Info.Languages = []string{"french", "english"}
	r1.Release.DebridCached = []string{"alldebrid"}
	return []frontend.ReleaseVariant{
		r1,
		rel("b", 2160, frontend.ReleaseVariant{}),
		rel("c", 2160, frontend.ReleaseVariant{}),
		rel("x1080", 1080, frontend.ReleaseVariant{}),
		rel("d", 1080, frontend.ReleaseVariant{}),
		rel("e", 720, frontend.ReleaseVariant{}),
		rel("f", 480, frontend.ReleaseVariant{}),
	}
}

func versionList(t *testing.T, path string, ed *fakeEditions) *httptest.ResponseRecorder {
	t.Helper()
	exp := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		d := frontend.ItemDetail{Versions: []frontend.Version{{ID: "ether:auto", Name: "Auto · 1080p"}}}
		d.Title.Type, d.Title.Runtime = "movie", 2*time.Hour
		return d, nil
	}}
	host := frontend.Host{
		Identity:   frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 7}, nil }},
		Experience: exp, Sessions: frontendtest.Sessions{}, LazyStreams: &versionLazy{}, VersionHandles: fakeHandles{}, Editions: ed,
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

func streamsOf(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var body struct{ Streams []map[string]any }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return body.Streams
}

func urls(s []map[string]any) string {
	var out []string
	for _, x := range s {
		u := x["url"].(string)
		out = append(out, u[strings.LastIndex(u[:strings.LastIndex(u, "/")], "/")+1:strings.LastIndex(u, "/")])
	}
	return strings.Join(out, ",")
}

const listPath = "/stremio/grant/o/%s/stream/movie/tmdb:42.json"

func TestVersionListDirectKeepsSingleStream(t *testing.T) {
	for _, path := range []string{"/stremio/grant/stream/movie/tmdb:42.json", fmt.Sprintf(listPath, "direct-1")} {
		s := streamsOf(t, versionList(t, path, &fakeEditions{ranked: testRanked()}))
		if len(s) != 1 || s[0]["name"] != "Ether" || s[0]["description"] != "Auto · 1080p" {
			t.Fatalf("%s: %v", path, s)
		}
	}
}

func TestVersionListQuotasPerQuality(t *testing.T) {
	s := streamsOf(t, versionList(t, fmt.Sprintf(listPath, "compact-2160.2-1080.1-720.0"), &fakeEditions{ranked: testRanked()}))
	if got := urls(s); got != "auto,h-a,h-b,h-x1080" {
		t.Fatalf("got %s", got)
	}
}

func TestVersionListLegacyTotal(t *testing.T) {
	s := streamsOf(t, versionList(t, fmt.Sprintf(listPath, "compact-3"), &fakeEditions{ranked: testRanked()}))
	if got := urls(s); got != "auto,h-a,h-b" {
		t.Fatalf("got %s", got)
	}
}

func TestVersionListColdSearchIsAwaited(t *testing.T) {
	ed := &fakeEditions{cold: true, ranked: testRanked()}
	s := streamsOf(t, versionList(t, fmt.Sprintf(listPath, "compact-2160.1"), ed))
	if len(s) != 2 {
		t.Fatalf("got %v", s)
	}
}

func TestVersionListWithoutRankedKeepsAuto(t *testing.T) {
	s := streamsOf(t, versionList(t, fmt.Sprintf(listPath, "detaille-2160.2"), &fakeEditions{}))
	if len(s) != 1 || s[0]["description"] != "Auto · 1080p" {
		t.Fatalf("got %v", s)
	}
}

func TestVersionListDetailedFormat(t *testing.T) {
	s := streamsOf(t, versionList(t, fmt.Sprintf(listPath, "detaille-2160.2"), &fakeEditions{ranked: testRanked()}))
	if len(s) != 3 || s[0]["name"] != "[AD⚡] Ether" {
		t.Fatalf("got %v", s)
	}
	want := "Auto\n2160p • REMUX • DV • 📶 9,5 Mb/s\n🇫🇷🇬🇧 • 💾 8,0 Go • 🔎 Alkolites\n🎧 HEVC • EAC3 5.1 • FLUX\n📁 Film.2160p.REMUX.DV.MULTI-FLUX"
	if s[0]["description"] != want {
		t.Fatalf("description:\n%v\nwant:\n%s", s[0]["description"], want)
	}
	if s[2]["name"] != "Ether" || strings.HasPrefix(s[2]["description"].(string), "Auto") {
		t.Fatalf("third: %v", s[2])
	}
	if h := s[0]["behaviorHints"].(map[string]any); h["bingeGroup"] != "ether-2160p" || h["videoSize"].(float64) != float64(8<<30) {
		t.Fatalf("hints: %v", h)
	}
}

func TestVersionListCompactIsOneLine(t *testing.T) {
	s := streamsOf(t, versionList(t, fmt.Sprintf(listPath, "compact-2160.1"), &fakeEditions{ranked: testRanked()}))
	d := s[0]["description"].(string)
	if strings.Contains(d, "\n") || d != "Auto · 2160p · REMUX · DV · EAC3 5.1 · 8,0 Go · FLUX" {
		t.Fatalf("compact: %q", d)
	}
}

func TestInvalidLinkOptionsAre404(t *testing.T) {
	for _, path := range []string{fmt.Sprintf(listPath, "compact-99"), "/stremio/grant/o/zzz/manifest.json", "/stremio/grant/fichiers/o/compact/manifest.json"} {
		if w := versionList(t, path, &fakeEditions{}); w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if w := versionList(t, "/stremio/grant/o/detaille-2160.2-1080.3-720.1/manifest.json", &fakeEditions{}); w.Code != 200 {
		t.Errorf("manifest: %d", w.Code)
	}
}
