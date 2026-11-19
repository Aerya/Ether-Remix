package debrid

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestAllDebridFullAccountStillReadsExistingAndVerifiedFiles(t *testing.T) {
	adds := 0
	fake := (&fakeAD{ready: true}).handler(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/magnet/upload" {
			adds++
			fmt.Fprint(w, `{"status":"error","error":{"code":"MAGNET_TOO_MANY","message":"Magnets limit reached"}}`)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	defer srv.Close()
	svc := NewService(config.Debrid{CachedOnly: true, Providers: []config.DebridProvider{{ID: "ad", Kind: "alldebrid", APIKey: testKey, Enabled: true}}}, Options{ProviderOptions: fastOpts(srv.URL)})
	ctx := context.Background()
	for _, ch := range []string{"b", "c"} {
		rel := torrentRelease("missing-" + ch)
		rel.InfoHash = strings.Repeat(ch, 40)
		rel.MagnetURL = "magnet:?xt=urn:btih:" + rel.InfoHash
		if _, err := svc.Acquire(ctx, rel); !errors.Is(err, core.ErrDownloadLimit) {
			t.Fatalf("expected account add limit, got %v", err)
		}
	}
	if adds != 2 {
		t.Fatalf("adds=%d", adds)
	}
	if st := svc.Stats()[0]; st.State == "backoff" || st.State == "auth_failed" {
		t.Fatalf("reads paused: %+v", st)
	}
	if _, err := svc.Acquire(ctx, torrentRelease("existing")); err != nil {
		t.Fatalf("existing torrent blocked: %v", err)
	}
	file := core.Release{ID: "file-after-quota", Name: "Movie.2024.mkv", Size: 3000000000, Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid", StreamURL: "https://alldebrid.com/f/abc"}
	if _, err := svc.acquireFile(ctx, file); err != nil {
		t.Fatalf("verified file blocked: %v", err)
	}
	rel := torrentRelease("third-missing")
	rel.InfoHash = strings.Repeat("d", 40)
	rel.MagnetURL = "magnet:?xt=urn:btih:" + rel.InfoHash
	if _, err := svc.Acquire(ctx, rel); err == nil {
		t.Fatal("missing torrent accepted")
	}
	if adds != 2 {
		t.Fatalf("new adds not paused: %d", adds)
	}
	if err := adError(429, "RATE_LIMITED", "slow down"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("real rate limit lost")
	}
}

// MAGNET_TOO_MANY pauses new additions only: even when its message mentions
// a daily limit it is never a provider quota (HTTP 502 "daily download
// limit"), which would pause every read.
func TestAllDebridMagnetTooManyIsNotAQuota(t *testing.T) {
	for _, msg := range []string{"Magnets limit reached", "Daily magnets limit reached", "You have reached your daily download limit"} {
		err := adError(200, "MAGNET_TOO_MANY", msg)
		if errors.Is(err, ErrQuotaExceeded) || !errors.Is(err, ErrAddRefused) || !errors.Is(err, ErrDownloadLimit) {
			t.Fatalf("%q: wrong class: %v", msg, err)
		}
	}
	if err := adError(502, "", "You have reached your daily download limit"); !errors.Is(err, ErrQuotaExceeded) || errors.Is(err, ErrAddRefused) {
		t.Fatalf("daily limit not a quota: %v", err)
	}

	fake := (&fakeAD{ready: true}).handler(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/magnet/upload" {
			fmt.Fprint(w, `{"status":"error","error":{"code":"MAGNET_TOO_MANY","message":"Daily magnets limit reached"}}`)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	defer srv.Close()
	svc := NewService(config.Debrid{CachedOnly: true, Providers: []config.DebridProvider{{ID: "ad", Kind: "alldebrid", APIKey: testKey, Enabled: true}}}, Options{ProviderOptions: fastOpts(srv.URL)})
	ctx := context.Background()
	rel := torrentRelease("missing-q")
	rel.InfoHash = strings.Repeat("e", 40)
	rel.MagnetURL = "magnet:?xt=urn:btih:" + rel.InfoHash
	if _, err := svc.Acquire(ctx, rel); !errors.Is(err, core.ErrDownloadLimit) || errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("add refusal: %v", err)
	}
	if st := svc.Stats()[0]; st.State == stateQuota || st.State == "backoff" {
		t.Fatalf("provider paused: %+v", st)
	}
	if _, err := svc.Acquire(ctx, torrentRelease("existing")); err != nil {
		t.Fatalf("existing torrent blocked: %v", err)
	}
}
