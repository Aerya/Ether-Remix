package plex

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/resolver"
)

// plex.delivery = "redirect": what an authorised direct-play part read is answered
// with, what an unauthorised or mixed-credential one is NOT answered with, and what
// the log is allowed to contain. A URL-authenticated read takes two hops because a
// redirected request names the URL that redirected it (delivery_grant.go); a
// header-authenticated one relays because only the client knows which headers it
// forwards across origins.

const (
	deliveryPartPath  = "/library/parts/1000000000000550/1759276800/file.mkv"
	deliveryEdgeHost  = "nexus-042.invalid"
	deliverySignature = "signature-not-for-logs"
)

// deliveryFixtureLink is the provider URL the fixture's source reports. It is
// release-specific so a test can prove the redirect names the file the session
// actually plays.
func deliveryFixtureLink(releaseID string) string {
	return "https://" + deliveryEdgeHost + "/dld/" + releaseID + "?token=" + deliverySignature
}

type deliverySource struct {
	*bytes.Reader
	id     string
	refuse *atomic.Bool
}

func (s *deliverySource) Size() int64       { return int64(s.Len()) }
func (s *deliverySource) Name() string      { return s.id + ".mkv" }
func (s *deliverySource) ReleaseID() string { return s.id }
func (s *deliverySource) Close() error      { return nil }
func (s *deliverySource) ReadAtContext(_ context.Context, p []byte, off int64) (int, error) {
	return s.Reader.ReadAt(p, off)
}

func (s *deliverySource) DirectURL() (core.DirectSourceLink, bool) {
	if s.refuse.Load() {
		return core.DirectSourceLink{}, false
	}
	return core.DirectSourceLink{URL: deliveryFixtureLink(s.id), Provider: "TorBox", Edge: deliveryEdgeHost}, true
}

type deliveryEngine struct{ refuse atomic.Bool }

func (e *deliveryEngine) Open(_ context.Context, id string, _ []byte) (core.MediaSource, error) {
	return &deliverySource{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), 65536)), id: id, refuse: &e.refuse}, nil
}

func (*deliveryEngine) Prefetch(context.Context, string, []byte, int64, int64) error { return nil }

// deliveryFixture builds the real part handler over a real playback Manager with a
// TorBox-shaped source, and captures the server log so a test can read what the
// signed link did and did not reach.
func deliveryFixture(t *testing.T, delivery string) (*fixture, *deliveryEngine, *bytes.Buffer) {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.Quality.Default = "test"
	cfg.Quality.Profiles = []core.QualityProfile{{Name: "test", MaxHeight: 1080}}
	cfg.Quality.Rules = nil
	cfg.Quality.Remote = ""
	cfg.Plex.Delivery = delivery
	releases := []core.Release{{ID: "hd", GUID: "hd", Indexer: "offline", Name: "Fjord.Harbor.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, PubDate: time.Now().Add(-24 * time.Hour), NZBURL: "offline"}}
	rs := resolver.New(&issue94Indexer{releases}, nil, nil, cfg, resolver.Options{})
	t.Cleanup(func() { rs.Close() })
	var logs bytes.Buffer
	f := newFixture(func(d *testServerDeps) {
		d.DisableEnrichment = true
		d.Host.HostConfig = func() config.Config { return cfg }
	})
	f.srv.log = slog.New(slog.NewTextHandler(&logs, nil))
	tr := &issue94Transcoder{newFakeTranscoder(core.Decision{})}
	engine := &deliveryEngine{}
	mgr, err := playback.New(playback.Deps{Catalog: f.catalog, Resolver: rs, Engine: engine, Transcoder: tr, SelectProfile: func(c core.ClientCaps, u *core.User, _ string) core.QualityProfile {
		return rs.ProfileFor(c, u)
	}, NativeProbe: func(_ context.Context, _ core.MediaSource) (*core.MediaInfo, error) {
		info := sampleInfo
		return &info, nil
	}}, cfg, playback.Options{SessionSurface: func(surface string) string {
		if surface == "plex" {
			return ""
		}
		return surface
	}, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.setPlayback(mgr)
	f.srv.setTranscoder(tr)
	t.Cleanup(func() { f.srv.Close(); mgr.Close() })
	return f, engine, &logs
}

// nativeGet asks for target the way a native loader does: no Plex headers at all, so
// only the URL can carry a credential. Extra headers are sent exactly as given.
func nativeGet(t *testing.T, f *fixture, target string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "192.168.1.50:51000"
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	return rec
}

// firstHop requests the part route as a URL-authenticated player does and returns
// the redirect Location Ether answered with.
func firstHop(t *testing.T, f *fixture, query string) string {
	t.Helper()
	rec := nativeGet(t, f, deliveryPartPath+query)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("first hop status = %d, want 307 (body %q)", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

func TestPartDeliveryRedirectsURLAuthenticatedRead(t *testing.T) {
	f, _, logs := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken)
	if !strings.HasPrefix(location, deliveryPartPath+"?") {
		t.Fatalf("the first hop must stay on the part route, got %q", location)
	}
	if strings.Contains(location, "X-Plex-Token") {
		t.Fatalf("the grant hop must leave the Plex credential behind: %q", location)
	}
	if !strings.Contains(location, deliveryGrantParam+"=") {
		t.Fatalf("the first hop must carry a minted grant: %q", location)
	}
	if strings.Contains(location, deliverySignature) {
		t.Fatalf("the first hop must not name the provider yet: %q", location)
	}

	second := nativeGet(t, f, location, "Range", "bytes=0-9")
	if second.Code != http.StatusTemporaryRedirect {
		t.Fatalf("grant hop status = %d, want 307 (body %q)", second.Code, second.Body.String())
	}
	got := second.Header().Get("Location")
	if want := deliveryFixtureLink("hd"); got != want {
		t.Fatalf("provider redirect = %q, want the session's own file %q", got, want)
	}
	if got := second.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := second.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if logged := logs.String(); strings.Contains(logged, deliverySignature) || strings.Contains(logged, "/dld/") {
		t.Errorf("the signed link reached the log: %q", logged)
	} else if !strings.Contains(logged, deliveryEdgeHost) {
		t.Errorf("the log must name the edge the player was sent to: %q", logged)
	}
}

// A spent grant is not a credential: repeating the hop the player already followed
// is denied outright, with no Location to follow.
func TestPartDeliveryGrantIsSingleUse(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken)
	if rec := nativeGet(t, f, location, "Range", "bytes=0-9"); rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("first use of the grant status = %d, want 307", rec.Code)
	}
	again := nativeGet(t, f, location, "Range", "bytes=0-9")
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("repeated grant status = %d, want 401", again.Code)
	}
	if got := again.Header().Get("Location"); got != "" {
		t.Errorf("a denial must not carry a Location, got %q", got)
	}
}

// A grant minted for one file cannot fetch another.
func TestPartDeliveryGrantIsBoundToItsFile(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken)
	other := strings.Replace(location, deliveryPartPath, "/library/parts/1000000000000551/1759276800/file.mkv", 1)
	if rec := nativeGet(t, f, other, "Range", "bytes=0-9"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a grant asked for another file: status = %d, want 401", rec.Code)
	}
}

// A grant is the second hop's only credential: an account credential pasted
// alongside it is refused rather than quietly preferred.
func TestPartDeliveryRefusesMixedCredentialsOnGrant(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken)
	rec := nativeGet(t, f, location+"&X-Plex-Token=stale", "Range", "bytes=0-9")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a grant presented with another credential: status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("a denial must not carry a Location, got %q", got)
	}
}

// An account credential in a header is not sent to a foreign host, grant parameter
// present or not: the read relays.
func TestPartDeliveryRelaysHeaderCredentialDespiteGrantParam(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	target := deliveryPartPath + "?" + deliveryGrantParam + "=not-minted-here"
	rec := nativeGet(t, f, target, "X-Plex-Token", ownerToken, "Range", "bytes=0-9")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("header credential with a grant parameter: status = %d, want 206 relay", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("a relayed read must not carry a Location, got %q", got)
	}
}

// An account credential on the URL mints its own hop even when a grant parameter is
// already present: the pasted value is neither consumed nor forwarded.
func TestPartDeliveryMintsItsOwnGrantBesideAForeignParam(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken+"&"+deliveryGrantParam+"=not-minted-here")
	query, err := url.ParseQuery(strings.TrimPrefix(location, deliveryPartPath+"?"))
	if err != nil {
		t.Fatalf("unparsable hop location %q: %v", location, err)
	}
	if got := query.Get(deliveryGrantParam); got == "" || got == "not-minted-here" {
		t.Fatalf("the hop must carry a freshly minted grant, got %q", location)
	}
	if len(query) != 1 {
		t.Fatalf("the hop may carry only its grant, got %q", location)
	}
}

// Every spelling of the credential stays behind: a lower-case copy that rode along
// would reach the provider in the referrer.
func TestPartDeliveryLeavesEveryCredentialSpellingBehind(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken+"&x-plex-token="+ownerToken)
	if strings.Contains(strings.ToLower(strings.TrimPrefix(location, deliveryPartPath)), "plex-token") {
		t.Fatalf("a credential spelling survived the first hop: %q", location)
	}
}

// The client identifier a loader carries on its URL must survive the hop, or the
// second read arrives as a different device.
func TestPartDeliveryKeepsTheClientIdentityAcrossTheHop(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken+"&X-Plex-Client-Identifier=tv-1")
	if !strings.Contains(strings.TrimPrefix(location, deliveryPartPath+"?"), "X-Plex-Client-Identifier=tv-1") {
		t.Fatalf("the hop lost the client identity: %q", location)
	}
	if rec := nativeGet(t, f, location, "Range", "bytes=0-9"); rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("the second hop status = %d, want 307", rec.Code)
	}
}

// An unauthorised read never learns the link, which is the whole point of deciding
// delivery inside the authorised part path.
func TestPartDeliveryRefusesUnauthorisedReads(t *testing.T) {
	for name, query := range map[string]string{"no credential": "", "invalid token": "?X-Plex-Token=not-a-plex-token"} {
		f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
		rec := nativeGet(t, f, deliveryPartPath+query, "Range", "bytes=0-9")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "" {
			t.Errorf("%s: a denial must not carry a Location, got %q", name, got)
		}
	}
}

func TestPartDeliveryRelaysWithoutRedirect(t *testing.T) {
	f, _, _ := deliveryFixture(t, "")
	rec := nativeGet(t, f, deliveryPartPath+"?X-Plex-Token="+ownerToken, "Range", "bytes=0-9")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("default delivery status = %d, want 206 relay", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("a relayed read must not carry a Location, got %q", got)
	}
	if rec.Body.Len() != 10 {
		t.Errorf("relayed body = %d bytes, want the requested 10-byte range", rec.Body.Len())
	}
}

// A credential in a header may travel to a foreign host at the client's discretion,
// so those reads keep relaying in this version.
func TestPartDeliveryRelaysHeaderAuthenticatedRead(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	rec := nativeGet(t, f, deliveryPartPath, "X-Plex-Token", ownerToken, "Range", "bytes=0-9")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("header-authenticated delivery status = %d, want 206 relay", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("a header-authenticated read must not be redirected, got %q", got)
	}
}

func TestPartDeliveryRelaysDownloadAndHead(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	rec := nativeGet(t, f, deliveryPartPath+"?X-Plex-Token="+ownerToken+"&download=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200 relay", rec.Code)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "file.mkv") {
		t.Errorf("the download lost its file name: %q", got)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("a download must not be redirected, got %q", got)
	}
	head := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodHead, deliveryPartPath+"?X-Plex-Token="+ownerToken, nil)
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		return rec
	}()
	if head.Code/100 == 3 {
		t.Fatalf("a HEAD probe must be answered by the session, got %d", head.Code)
	}
}

// A source with nothing to hand over relays instead of sending the player on a hop
// for nothing.
func TestPartDeliveryRelaysWithoutProviderLink(t *testing.T) {
	f, engine, _ := deliveryFixture(t, config.DeliveryRedirect)
	engine.refuse.Store(true)
	rec := nativeGet(t, f, deliveryPartPath+"?X-Plex-Token="+ownerToken, "Range", "bytes=0-9")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("delivery without a provider link status = %d, want 206 relay", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("no Location expected, got %q", got)
	}
}

// A link that goes away between the hops must not fail the read: the grant is
// already spent, and the session still has the bytes.
func TestPartDeliveryRelaysWhenTheLinkGoesAway(t *testing.T) {
	f, engine, _ := deliveryFixture(t, config.DeliveryRedirect)
	location := firstHop(t, f, "?X-Plex-Token="+ownerToken)
	engine.refuse.Store(true)
	rec := nativeGet(t, f, location, "Range", "bytes=0-9")
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("a lost link must fall back to the relay, got %d", rec.Code)
	}
}

func TestPartDeliveryRefusesChangedGrantQuery(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	for _, extra := range []string{"Authorization=Bearer+test", "X-Plex-Token=&X-Plex-Token=test", "x=%zz"} {
		target := firstHop(t, f, "?X-Plex-Token="+ownerToken)
		r := nativeGet(t, f, target+"&"+extra)
		if r.Code != http.StatusUnauthorized || r.Header().Get("Location") != "" {
			t.Fatalf("altered grant query: got %d and Location %q", r.Code, r.Header().Get("Location"))
		}
	}
}

func TestPartDeliveryRelaysRepeatedCredentialHeader(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	req := httptest.NewRequest(http.MethodGet, deliveryPartPath+"?X-Plex-Token="+ownerToken, nil)
	req.Header["X-Plex-Token"] = []string{"", ownerToken}
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Location") != "" {
		t.Fatalf("repeated token header: got %d and Location %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestPartDeliveryPreservesPlexRangeSemantics(t *testing.T) {
	f, _, _ := deliveryFixture(t, config.DeliveryRedirect)
	target := deliveryPartPath + "?X-Plex-Token=" + ownerToken
	for _, tc := range []struct {
		name, window, condition string
		wantBytes               int
	}{
		{"conditional seek", "bytes=20-29", "If-Range", 10},
		{"multi-range", "bytes=1-2,4-5", "", 65536},
		{"nonstandard unit", "items=20-29", "", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := []string{"Range", tc.window}
			if tc.condition != "" {
				headers = append(headers, tc.condition, `"stale-origin-validator"`)
			}
			rec := nativeGet(t, f, target, headers...)
			if rec.Code != http.StatusPartialContent || rec.Header().Get("Location") != "" || rec.Body.Len() != tc.wantBytes {
				t.Fatalf("got status %d, %d body bytes, redirected=%v", rec.Code, rec.Body.Len(), rec.Header().Get("Location") != "")
			}
		})
	}
}
