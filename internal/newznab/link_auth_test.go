package newznab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/safefetch"
)

func TestStripAndAuthenticateLinkCredentials(t *testing.T) {
	var wantKey atomic.Value
	wantKey.Store("initial-secret-key")
	var lastAuthParam atomic.Value
	var lastQuery atomic.Value

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		lastQuery.Store(r.URL.RawQuery)
		key := wantKey.Load().(string)
		switch r.URL.Path {
		case "/api":
			if q.Get("apikey") != key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			lastAuthParam.Store("apikey")
			fmt.Fprint(w, fakeNZB)
		case "/getnzb":
			if q.Get("r") != key || q.Get("apikey") != "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			lastAuthParam.Store("r")
			fmt.Fprint(w, fakeNZB)
		case "/dl/file.torrent":
			if q.Get("apikey") != key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			doc, _ := sampleTorrent()
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = w.Write(doc)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := config.Indexer{
		ID:         "idx-1",
		Name:       "Geek",
		URL:        srv.URL,
		APIKey:     "initial-secret-key",
		Enabled:    true,
		DailyGrabs: 20,
	}
	m := NewManager(nil, []config.Indexer{cfg}, Options{})
	ctx := context.Background()

	// 1. Standard ?apikey= link strips cleanly with zero placeholder and fetches
	// with the configured API key on same-origin.
	rawStandard := srv.URL + "/api?t=get&id=book1&apikey=initial-secret-key"
	stripped := m.StripLinkAuth(core.Release{
		IndexerID: "idx-1",
		NZBURL:    rawStandard,
		Protocol:  core.ProtocolUsenet,
	})
	if strings.Contains(stripped.NZBURL, "initial-secret-key") || strings.Contains(stripped.NZBURL, "apikey") || strings.Contains(stripped.NZBURL, linkSecretPrefix) {
		t.Fatalf("standard stripped URL leaked key or placeholder: %q", stripped.NZBURL)
	}
	if _, err := m.FetchNZB(ctx, stripped); err != nil {
		t.Fatalf("FetchNZB(key-less standard link): %v", err)
	}
	if got := lastAuthParam.Load(); got != "apikey" {
		t.Fatalf("auth param = %v, want apikey", got)
	}

	// 2. Non-standard ?r=<apikey> link replaces only the secret with a URL-safe
	// token and hydrates it back without appending a duplicate apikey param.
	rawR := srv.URL + "/getnzb?id=abc&r=initial-secret-key"
	strippedR := m.StripLinkAuth(core.Release{
		IndexerID: "idx-1",
		NZBURL:    rawR,
		Protocol:  core.ProtocolUsenet,
	})
	if strings.Contains(strippedR.NZBURL, "initial-secret-key") {
		t.Fatalf("non-standard stripped URL leaked key: %q", strippedR.NZBURL)
	}
	if _, err := m.FetchNZB(ctx, strippedR); err != nil {
		t.Fatalf("FetchNZB(key-less r= link): %v", err)
	}
	if got := lastAuthParam.Load(); got != "r" {
		t.Fatalf("auth param = %v, want r (query=%v)", got, lastQuery.Load())
	}

	// 3. Rotating the indexer's API key keeps previously cached key-less links
	// working (both standard and non-standard with URL-special characters).
	rotatedKey := "rotated+key/2026"
	wantKey.Store(rotatedKey)
	cfg.APIKey = rotatedKey
	m.Reload([]config.Indexer{cfg})

	if _, err := m.FetchNZB(ctx, stripped); err != nil {
		t.Fatalf("FetchNZB(standard link after key rotation): %v", err)
	}
	if _, err := m.FetchNZB(ctx, strippedR); err != nil {
		t.Fatalf("FetchNZB(r= link after key rotation): %v", err)
	}

	// 4. Short key must not corrupt unrelated query params or path segments.
	shortClient := New(config.Indexer{ID: "short", Name: "Short", URL: srv.URL, APIKey: "k", Enabled: true}, nil, Options{})
	if got := shortClient.StripLinkAuth(srv.URL + "/api?id=book&t=get&apikey=k"); got != srv.URL+"/api?id=book&t=get" {
		t.Fatalf("short key corrupted link: %q", got)
	}

	// 5. Torznab .torrent links strip and re-authenticate on same-origin.
	tzCfg := config.Indexer{
		ID:         "tz-1",
		Name:       "Prowlarr",
		URL:        srv.URL,
		APIKey:     rotatedKey,
		Protocol:   "torrent",
		Enabled:    true,
		DailyGrabs: 20,
	}
	tzMgr := NewManager(nil, []config.Indexer{tzCfg}, Options{})
	strippedTor := tzMgr.StripLinkAuth(core.Release{
		IndexerID: "tz-1",
		NZBURL:    srv.URL + "/dl/file.torrent?apikey=" + rotatedKey,
		Protocol:  core.ProtocolTorrent,
	})
	if strings.Contains(strippedTor.NZBURL, rotatedKey) || strings.Contains(strippedTor.NZBURL, "apikey") {
		t.Fatalf("stripped torrent URL leaked key: %q", strippedTor.NZBURL)
	}
	resolved, err := tzMgr.ResolveTorrent(ctx, strippedTor)
	if err != nil || resolved.InfoHash == "" {
		t.Fatalf("ResolveTorrent(key-less torrent link): rel=%+v err=%v", resolved, err)
	}
}

func TestAuthenticateLinkNeverLeaksOffOriginOrOnDowngrade(t *testing.T) {
	var evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer evil.Close()

	cfg := config.Indexer{
		ID:         "idx-sec",
		Name:       "Secure",
		URL:        "https://indexer.example.test:8443",
		APIKey:     "super-secret-api-key",
		Enabled:    true,
		DailyGrabs: 10,
	}
	c := New(cfg, nil, Options{})

	// Off-origin link smuggling a credential placeholder fails closed with ErrBlocked.
	if _, err := c.authenticateLink(evil.URL + "/steal?k=~ether.apikey~"); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("off-origin token err = %v, want ErrBlocked", err)
	}
	if _, err := c.FetchNZB(context.Background(), core.Release{
		ID:        "steal",
		IndexerID: "idx-sec",
		NZBURL:    evil.URL + "/steal?k=~ether.apikey~",
		Protocol:  core.ProtocolUsenet,
	}); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("FetchNZB off-origin token err = %v, want ErrBlocked", err)
	}
	if evilHits.Load() != 0 {
		t.Fatalf("evil host hit %d times, want 0", evilHits.Load())
	}

	// Off-origin link without placeholder must never receive apikey.
	gotOff, err := c.authenticateLink("https://other.example.test/api?t=get&id=1")
	if err != nil {
		t.Fatalf("off-origin plain link err = %v", err)
	}
	if strings.Contains(gotOff, "super-secret-api-key") || strings.Contains(gotOff, "apikey") {
		t.Fatalf("off-origin link received credentials: %q", gotOff)
	}

	// HTTPS -> HTTP downgrade on same host:port must not receive credentials.
	gotDown, err := c.authenticateLink("http://indexer.example.test:8443/api?t=get&id=1")
	if err != nil {
		t.Fatalf("downgraded plain link err = %v", err)
	}
	if strings.Contains(gotDown, "super-secret-api-key") || strings.Contains(gotDown, "apikey") {
		t.Fatalf("scheme-downgraded link received credentials: %q", gotDown)
	}
	if _, err := c.authenticateLink("http://indexer.example.test:8443/getnzb?r=~ether.apikey~"); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("scheme-downgraded token err = %v, want ErrBlocked", err)
	}

	// Unknown credential placeholder on same origin fails closed.
	if _, err := c.authenticateLink("https://indexer.example.test:8443/getnzb?r=~ether.unknown~"); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("unknown placeholder err = %v, want ErrReleaseRejected", err)
	}
}
