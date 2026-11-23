package debrid

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

const passkeyMagnetSuffix = "&dn=Secret.Release&tr=https%3A%2F%2Fprivate.example%2Fannounce%3Fpasskey%3DDO_NOT_LEAK_999"

func privateReq() AcquireRequest {
	req := acquireReq(false) // intentionally CachedOnly=false & RemoveUncached=false to prove private overrides both
	req.RemoveUncached = false
	req.Private = true
	req.Magnet = "magnet:?xt=urn:btih:" + testHash + passkeyMagnetSuffix
	return req
}

// recordingServer wraps h and fails t if any request URL or body carries a
// tracker parameter, display name or passkey.
func recordingServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		mu.Lock()
		raw := r.URL.RequestURI() + "\n" + string(body)
		mu.Unlock()
		for _, forbidden := range []string{"DO_NOT_LEAK_999", "private.example", "tr=", "tr%3D", "dn=", "Secret.Release"} {
			if strings.Contains(raw, forbidden) {
				t.Errorf("request %s %s leaked %q in %q", r.Method, r.URL.Path, forbidden, raw)
			}
		}
		h.ServeHTTP(w, r)
	}))
}

func TestPrivateReleaseNeverAddedWhenUncachedAcrossProviders(t *testing.T) {
	ctx := context.Background()

	// Real-Debrid: uncached -> addMagnet must never be called.
	rdFake := &fakeRD{cached: false}
	rdSrv := recordingServer(t, rdFake.handler(t))
	defer rdSrv.Close()
	rd := newRealDebrid(testKey, fastOpts(rdSrv.URL))
	if _, err := rd.Acquire(ctx, privateReq()); !errors.Is(err, core.ErrPrivateUncached) {
		t.Fatalf("real-debrid uncached private: got %v, want ErrPrivateUncached", err)
	}
	if rdFake.adds != 0 {
		t.Fatalf("real-debrid added uncached private torrent %d times", rdFake.adds)
	}

	// AllDebrid: uncached -> /v4/magnet/upload must never be called.
	adUploads := 0
	adFake := &fakeAD{ready: false}
	adBase := adFake.handler(t)
	adSrv := recordingServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v4/magnet/upload" {
			adUploads++
		}
		adBase.ServeHTTP(w, r)
	}))
	defer adSrv.Close()
	ad := newAllDebrid(testKey, fastOpts(adSrv.URL))
	if _, err := ad.Acquire(ctx, privateReq()); !errors.Is(err, core.ErrPrivateUncached) {
		t.Fatalf("alldebrid uncached private: got %v, want ErrPrivateUncached", err)
	}
	if adUploads != 0 {
		t.Fatalf("alldebrid uploaded uncached private magnet %d times", adUploads)
	}

	// TorBox: uncached -> CheckCached says false; createtorrent must never be called.
	tbFake := &fakeTorBox{cached: false}
	tbSrv := recordingServer(t, tbFake.handler(t))
	defer tbSrv.Close()
	tb := newTorBox(testKey, fastOpts(tbSrv.URL))
	if _, err := tb.Acquire(ctx, privateReq()); !errors.Is(err, core.ErrPrivateUncached) {
		t.Fatalf("torbox uncached private: got %v, want ErrPrivateUncached", err)
	}
	if tbFake.created != 0 {
		t.Fatalf("torbox created uncached private torrent %d times", tbFake.created)
	}

	// Offcloud: uncached -> /cache/info says false; /cloud must never be called.
	ocFake := &fakeOC{cached: false}
	ocSrv := recordingServer(t, ocFake.handler(t))
	defer ocSrv.Close()
	oc := newOffcloud(testKey, fastOpts(ocSrv.URL))
	if _, err := oc.Acquire(ctx, privateReq()); !errors.Is(err, core.ErrPrivateUncached) {
		t.Fatalf("offcloud uncached private: got %v, want ErrPrivateUncached", err)
	}
	if ocFake.created != 0 {
		t.Fatalf("offcloud added uncached private magnet %d times", ocFake.created)
	}

	// Debrid-Link: uncached -> bare hash check refused; nothing added to account.
	dlFake := &fakeDL{cached: false}
	dlSrv := recordingServer(t, dlFake.handler(t))
	defer dlSrv.Close()
	dl := newDebridLink(testKey, fastOpts(dlSrv.URL))
	if _, err := dl.Acquire(ctx, privateReq()); !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("debrid-link uncached private: got %v, want ErrNotCached", err)
	}
	if len(dlFake.torrents) != 0 {
		t.Fatalf("debrid-link added uncached private torrent: %v", dlFake.torrents)
	}

	// Service.Acquire & Service.QueueDownload with CachedOnly=false and RemoveUncached=false.
	h := newHarness(t, 1<<20, "torbox")
	fp := h.provs["torbox-0"]
	fp.bulk = true
	cfg := h.svc.Config()
	cfg.CachedOnly = false
	cfg.RemoveUncached = false
	h.svc.UpdateConfig(cfg)

	rel := torrentRelease("priv-uncached")
	rel.Private = true
	rel.MagnetURL = "magnet:?xt=urn:btih:" + testHash + passkeyMagnetSuffix

	if _, err := h.svc.Acquire(ctx, rel); !errors.Is(err, core.ErrPrivateUncached) || err.Error() != core.PrivateTorrentReason {
		t.Fatalf("Service.Acquire uncached private = %v, want %q", err, core.PrivateTorrentReason)
	}
	if _, err := h.svc.QueueDownload(ctx, rel); !errors.Is(err, core.ErrPrivateUncached) || err.Error() != core.PrivateTorrentReason {
		t.Fatalf("Service.QueueDownload uncached private = %v, want %q", err, core.PrivateTorrentReason)
	}
	if fp.adds != 0 || fp.downloading != 0 || fp.callCount("acquire") != 0 {
		t.Fatalf("uncached private touched provider: adds=%d downloading=%d acquire=%d", fp.adds, fp.downloading, fp.callCount("acquire"))
	}
}

func TestPrivateReleasePlaysWhenCachedByBareHashWithoutTrackers(t *testing.T) {
	ctx := context.Background()

	// TorBox cached: plays by bare info hash with zero trackers sent.
	tbFake := &fakeTorBox{cached: true}
	tbSrv := recordingServer(t, tbFake.handler(t))
	defer tbSrv.Close()
	svc := NewService(config.Debrid{
		CachedOnly:     false,
		RemoveUncached: false,
		Providers:      []config.DebridProvider{{ID: "tb", Name: "TB", Kind: config.DebridTorBox, APIKey: testKey, Enabled: true}},
	}, Options{ProviderOptions: fastOpts(tbSrv.URL)})

	rel := core.Release{
		ID:        "priv-cached",
		Name:      "Movie.2024.1080p",
		Protocol:  core.ProtocolTorrent,
		InfoHash:  strings.ToUpper(testHash),
		MagnetURL: "magnet:?xt=urn:btih:" + testHash + passkeyMagnetSuffix,
		Private:   true,
	}
	raw, err := svc.Acquire(ctx, rel)
	if err != nil {
		t.Fatalf("cached private Acquire failed: %v", err)
	}
	p, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Private || p.Magnet != "magnet:?xt=urn:btih:"+testHash || p.URL == "" {
		t.Fatalf("payload = %+v", p)
	}

	// Real-Debrid already cached on account (Find -> OLD): plays with 0 addMagnet calls and 0 trackers.
	rdFake := &fakeRD{existing: true}
	rdSrv := recordingServer(t, rdFake.handler(t))
	defer rdSrv.Close()
	rd := newRealDebrid(testKey, fastOpts(rdSrv.URL))
	acq, err := rd.Acquire(ctx, privateReq())
	if err != nil || acq.TorrentID != "OLD" || rdFake.adds != 0 {
		t.Fatalf("real-debrid existing private: acq=%+v adds=%d err=%v", acq, rdFake.adds, err)
	}

	// Debrid-Link cached: bare hash sent (not magnet), plays without trackers.
	dlFake := &fakeDL{cached: true}
	dlSrv := recordingServer(t, dlFake.handler(t))
	defer dlSrv.Close()
	dl := newDebridLink(testKey, fastOpts(dlSrv.URL))
	if acq, err := dl.Acquire(ctx, privateReq()); err != nil || acq.TorrentID != "T9" || len(dlFake.adds) != 1 || dlFake.adds[0] != testHash {
		t.Fatalf("debrid-link cached private: acq=%+v adds=%v err=%v", acq, dlFake.adds, err)
	}
}
