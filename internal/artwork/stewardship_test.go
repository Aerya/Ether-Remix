package artwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/safefetch"
)

func testPNG(t *testing.T, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type fakeClock struct {
	mu  sync.Mutex
	cur time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{cur: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *fakeClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = c.cur.Add(d)
	return c.cur
}

// testGuard routes synthetic public hostnames to httptest servers while keeping
// safefetch's real public-IP enforcement active.
func testGuard(hosts map[string]*httptest.Server) *safefetch.Guard {
	addrs := make(map[string]string, len(hosts))
	ips := make(map[string]netip.Addr, len(hosts))
	idx := byte(10)
	for host, srv := range hosts {
		addrs[host] = srv.Listener.Addr().String()
		ip := netip.AddrFrom4([4]byte{93, 184, 216, idx})
		ips[host] = ip
		addrs[ip.String()] = srv.Listener.Addr().String()
		idx++
	}
	return &safefetch.Guard{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			ip, ok := ips[host]
			if !ok {
				return nil, fmt.Errorf("unknown test host %q", host)
			}
			return []netip.Addr{ip}, nil
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			h, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			target, ok := addrs[h]
			if !ok {
				return nil, fmt.Errorf("unmapped dial %q", address)
			}
			var d net.Dialer
			return d.DialContext(ctx, network, target)
		},
	}
}

func TestLogoServed100TimesFetchesOnce(t *testing.T) {
	logo := testPNG(t, color.RGBA{R: 20, G: 120, B: 220, A: 255})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(logo)
	}))
	defer srv.Close()

	clock := newFakeClock()
	dir := t.TempDir()
	guard := testGuard(map[string]*httptest.Server{"logos.example.test": srv})
	opts := ImagesOptions{Dir: dir, Guard: guard, Now: clock.Now}
	im := NewImages(opts)
	defer im.Close()

	ctx := context.Background()
	const logoURL = "http://logos.example.test/channel-1.png"

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, ct, err := im.GetUntrustedURL(ctx, logoURL)
			if err != nil || ct != "image/png" || !bytes.Equal(got, logo) {
				t.Errorf("concurrent GetUntrustedURL: ct=%q err=%v", ct, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	for i := 0; i < 50; i++ {
		got, ct, err := im.GetUntrustedURL(ctx, logoURL)
		if err != nil || ct != "image/png" || !bytes.Equal(got, logo) {
			t.Fatalf("serve %d: ct=%q err=%v", i, ct, err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("100 serves made %d upstream requests, want 1", got)
	}

	// Across a restart (cold memory cache over the same disk directory), still 0 new fetches.
	im2 := NewImages(opts)
	defer im2.Close()
	got, ct, err := im2.GetUntrustedURL(ctx, logoURL)
	if err != nil || ct != "image/png" || !bytes.Equal(got, logo) {
		t.Fatalf("post-restart read: ct=%q err=%v", ct, err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("restart refetched cached logo: %d requests", got)
	}
}

func TestNotFoundLogoServed100TimesFetchesOnce(t *testing.T) {
	var requests404 atomic.Int32
	var requestsHTML atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing.png":
			requests404.Add(1)
			http.NotFound(w, r)
		case "/not-an-image.png":
			requestsHTML.Add(1)
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body>error page</body></html>"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	clock := newFakeClock()
	im := NewImages(ImagesOptions{
		Dir:   t.TempDir(),
		Guard: testGuard(map[string]*httptest.Server{"logos.example.test": srv}),
		Now:   clock.Now,
	})
	defer im.Close()

	ctx := context.Background()
	const missingURL = "http://logos.example.test/missing.png"
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, _, err := im.GetUntrustedURL(ctx, missingURL); !errors.Is(err, ErrNotFound) {
				t.Errorf("concurrent 404 err = %v, want ErrNotFound", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	for i := 0; i < 50; i++ {
		if _, _, err := im.GetUntrustedURL(ctx, missingURL); !errors.Is(err, ErrNotFound) {
			t.Fatalf("serve %d err = %v, want ErrNotFound", i, err)
		}
	}
	if got := requests404.Load(); got != 1 {
		t.Fatalf("404 logo served 100 times made %d upstream requests, want 1", got)
	}

	// Non-image responses are also remembered for 24 h.
	const htmlURL = "http://logos.example.test/not-an-image.png"
	for i := 0; i < 100; i++ {
		if _, _, err := im.GetUntrustedURL(ctx, htmlURL); err == nil {
			t.Fatal("non-image accepted")
		}
	}
	if got := requestsHTML.Load(); got != 1 {
		t.Fatalf("non-image served 100 times made %d upstream requests, want 1", got)
	}

	// Inside 24 h, neither 404 nor non-image contacts the provider.
	clock.Advance(24*time.Hour - time.Second)
	_, _, _ = im.GetUntrustedURL(ctx, missingURL)
	_, _, _ = im.GetUntrustedURL(ctx, htmlURL)
	if requests404.Load() != 1 || requestsHTML.Load() != 1 {
		t.Fatalf("retried before 24 h elapsed: 404=%d html=%d", requests404.Load(), requestsHTML.Load())
	}

	// Past 24 h, a single retry is allowed.
	clock.Advance(2 * time.Second)
	_, _, _ = im.GetUntrustedURL(ctx, missingURL)
	if got := requests404.Load(); got != 2 {
		t.Fatalf("after 24 h expiry got %d requests, want 2", got)
	}
}

func TestTransientFailureNegativeCacheDoublesUpToSixHours(t *testing.T) {
	var requests500 atomic.Int32
	var requestsSlow atomic.Int32
	unblockSlow := make(chan struct{})
	defer close(unblockSlow)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/err500.png":
			requests500.Add(1)
			http.Error(w, "upstream error", http.StatusBadGateway)
		case "/slow.png":
			requestsSlow.Add(1)
			select {
			case <-r.Context().Done():
			case <-unblockSlow:
			}
		}
	}))
	defer srv.Close()

	clock := newFakeClock()
	im := NewImages(ImagesOptions{
		Dir:        t.TempDir(),
		HTTPClient: &http.Client{Timeout: 25 * time.Millisecond},
		Guard:      testGuard(map[string]*httptest.Server{"logos.example.test": srv}),
		Now:        clock.Now,
	})
	defer im.Close()

	ctx := context.Background()
	const errURL = "http://logos.example.test/err500.png"
	// Expected backoff windows: 15m, 30m, 1h, 2h, 4h, 6h, 6h.
	windows := []time.Duration{
		15 * time.Minute,
		30 * time.Minute,
		time.Hour,
		2 * time.Hour,
		4 * time.Hour,
		6 * time.Hour,
		6 * time.Hour,
	}
	for i, w := range windows {
		if _, _, err := im.GetUntrustedURL(ctx, errURL); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", i+1)
		}
		if got := requests500.Load(); got != int32(i+1) {
			t.Fatalf("attempt %d upstream requests = %d, want %d", i+1, got, i+1)
		}
		clock.Advance(w - time.Second)
		if _, _, err := im.GetUntrustedURL(ctx, errURL); err == nil {
			t.Fatalf("inside window %v succeeded", w)
		}
		if got := requests500.Load(); got != int32(i+1) {
			t.Fatalf("retried before %v elapsed: got %d requests", w, got)
		}
		clock.Advance(time.Second)
	}

	// Timeouts follow the same 15 min initial negative cache.
	const slowURL = "http://logos.example.test/slow.png"
	if _, _, err := im.GetUntrustedURL(ctx, slowURL); err == nil {
		t.Fatal("timeout unexpectedly succeeded")
	}
	for i := 0; i < 20; i++ {
		_, _, _ = im.GetUntrustedURL(ctx, slowURL)
	}
	if got := requestsSlow.Load(); got != 1 {
		t.Fatalf("timed-out logo made %d upstream requests inside 15m, want 1", got)
	}
}

func TestExpiredLogoConditionalRevalidation304AndReplacement(t *testing.T) {
	v1 := testPNG(t, color.RGBA{R: 200, G: 40, B: 40, A: 255})
	v2 := testPNG(t, color.RGBA{R: 40, G: 200, B: 80, A: 255})

	var requests atomic.Int32
	var bodyDownloads atomic.Int32
	var mode atomic.Int32 // 1 = v1, 2 = v2, 3 = 500 error
	mode.Store(1)

	const lastMod = "Wed, 18 Nov 2026 10:00:00 GMT"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/long.png":
			w.Header().Set("Cache-Control", "public, max-age=31536000")
			w.Header().Set("ETag", `"long-1"`)
			if r.Header.Get("If-None-Match") == `"long-1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			bodyDownloads.Add(1)
			_, _ = w.Write(v1)
			return
		case "/expires.png":
			w.Header().Set("Date", "Wed, 18 Nov 2026 12:00:00 GMT")
			w.Header().Set("Expires", "Fri, 20 Nov 2026 12:00:00 GMT") // 48 h
			w.Header().Set("ETag", `"exp-1"`)
			if r.Header.Get("If-None-Match") == `"exp-1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write(v1)
			return
		}

		cur := mode.Load()
		if cur == 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		tag := `"v1"`
		payload := v1
		if cur == 2 {
			tag = `"v2"`
			payload = v2
		}
		// Short upstream max-age verifies the 24 h floor.
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("ETag", tag)
		w.Header().Set("Last-Modified", lastMod)
		if r.Header.Get("If-None-Match") == tag && r.Header.Get("If-Modified-Since") == lastMod {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		bodyDownloads.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	clock := newFakeClock()
	im := NewImages(ImagesOptions{
		Dir:   t.TempDir(),
		Guard: testGuard(map[string]*httptest.Server{"logos.example.test": srv}),
		Now:   clock.Now,
	})
	defer im.Close()

	ctx := context.Background()
	const logoURL = "http://logos.example.test/logo.png"

	got, _, err := im.GetUntrustedURL(ctx, logoURL)
	if err != nil || !bytes.Equal(got, v1) {
		t.Fatalf("initial fetch: %v", err)
	}
	if requests.Load() != 1 || bodyDownloads.Load() != 1 {
		t.Fatalf("initial counts: requests=%d downloads=%d", requests.Load(), bodyDownloads.Load())
	}

	// Despite max-age=60, the 24 h floor prevents any upstream contact inside 24 h.
	clock.Advance(12 * time.Hour)
	for i := 0; i < 20; i++ {
		got, _, err = im.GetUntrustedURL(ctx, logoURL)
		if err != nil || !bytes.Equal(got, v1) {
			t.Fatalf("fresh serve: %v", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("contacted provider inside 24 h floor: %d", requests.Load())
	}

	// Past 24 h: callers keep getting the cached copy while one background
	// conditional request returns 304 without re-downloading the body.
	clock.Advance(12*time.Hour + time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, _, err := im.GetUntrustedURL(ctx, logoURL)
			if err != nil || !bytes.Equal(data, v1) {
				t.Errorf("stale-while-revalidate serve: %v", err)
			}
		}()
	}
	wg.Wait()
	im.Wait()

	if requests.Load() != 2 || bodyDownloads.Load() != 1 {
		t.Fatalf("after 304 revalidation: requests=%d (want 2) bodyDownloads=%d (want 1)", requests.Load(), bodyDownloads.Load())
	}

	// Renewed freshness: 50 more serves make 0 upstream requests.
	for i := 0; i < 50; i++ {
		if data, _, err := im.GetUntrustedURL(ctx, logoURL); err != nil || !bytes.Equal(data, v1) {
			t.Fatalf("post-304 serve: %v", err)
		}
	}
	if requests.Load() != 2 || bodyDownloads.Load() != 1 {
		t.Fatalf("304 did not renew freshness: requests=%d downloads=%d", requests.Load(), bodyDownloads.Load())
	}

	// Upstream failure on revalidation keeps serving the cached copy.
	mode.Store(3)
	clock.Advance(24*time.Hour + time.Second)
	if data, _, err := im.GetUntrustedURL(ctx, logoURL); err != nil || !bytes.Equal(data, v1) {
		t.Fatalf("serve during failing revalidation: %v", err)
	}
	im.Wait()
	if data, _, err := im.GetUntrustedURL(ctx, logoURL); err != nil || !bytes.Equal(data, v1) {
		t.Fatalf("cached copy lost after 500 revalidation: %v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("revalidation failure hammered provider: requests=%d", requests.Load())
	}

	// Upstream changes to v2 (200 OK): after backoff + expiry, background
	// revalidation replaces the cached copy with v2.
	mode.Store(2)
	clock.Advance(24 * time.Hour)
	if data, _, err := im.GetUntrustedURL(ctx, logoURL); err != nil || !bytes.Equal(data, v1) {
		t.Fatalf("expected immediate cached v1 before background 200 completes: %v", err)
	}
	im.Wait()
	if data, _, err := im.GetUntrustedURL(ctx, logoURL); err != nil || !bytes.Equal(data, v2) {
		t.Fatalf("200 revalidation did not replace cached bytes: %v", err)
	}

	// Verify 30-day ceiling on max-age=31536000 and Expires header support.
	beforeLong := requests.Load()
	const longURL = "http://logos.example.test/long.png"
	if _, _, err := im.GetUntrustedURL(ctx, longURL); err != nil {
		t.Fatal(err)
	}
	clock.Advance(29 * 24 * time.Hour)
	if _, _, err := im.GetUntrustedURL(ctx, longURL); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != beforeLong+1 {
		t.Fatalf("revalidated before 29 days: %d", requests.Load()-beforeLong)
	}
	clock.Advance(2 * 24 * time.Hour) // 31 days > 30-day ceiling
	if _, _, err := im.GetUntrustedURL(ctx, longURL); err != nil {
		t.Fatal(err)
	}
	im.Wait()
	if requests.Load() != beforeLong+2 {
		t.Fatalf("30-day ceiling did not trigger conditional revalidation: %d", requests.Load()-beforeLong)
	}

	// Reset clock to match the fixed Date/Expires test timestamps.
	expClock := newFakeClock()
	imExp := NewImages(ImagesOptions{
		Dir:   t.TempDir(),
		Guard: testGuard(map[string]*httptest.Server{"logos.example.test": srv}),
		Now:   expClock.Now,
	})
	defer imExp.Close()
	beforeExp := requests.Load()
	const expURL = "http://logos.example.test/expires.png"
	if _, _, err := imExp.GetUntrustedURL(ctx, expURL); err != nil {
		t.Fatal(err)
	}
	expClock.Advance(36 * time.Hour) // < 48 h Expires
	if _, _, err := imExp.GetUntrustedURL(ctx, expURL); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != beforeExp+1 {
		t.Fatalf("Expires header ignored at 36 h")
	}
	expClock.Advance(13 * time.Hour) // 49 h > 48 h Expires
	if _, _, err := imExp.GetUntrustedURL(ctx, expURL); err != nil {
		t.Fatal(err)
	}
	imExp.Wait()
	if requests.Load() != beforeExp+2 {
		t.Fatalf("Expires header did not revalidate after 48 h")
	}
}

func TestRateLimit429And503RetryAfterPausesHost(t *testing.T) {
	logo := testPNG(t, color.RGBA{R: 90, G: 90, B: 230, A: 255})
	var throttledHits atomic.Int32
	var otherHostHits atomic.Int32
	var status atomic.Int32
	status.Store(http.StatusOK)

	clock := newFakeClock()
	var retryHeader atomic.Value
	retryHeader.Store("120")

	throttledSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		throttledHits.Add(1)
		code := int(status.Load())
		if code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", retryHeader.Load().(string))
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(logo)
	}))
	defer throttledSrv.Close()

	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHostHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(logo)
	}))
	defer otherSrv.Close()

	im := NewImages(ImagesOptions{
		Dir: t.TempDir(),
		Guard: testGuard(map[string]*httptest.Server{
			"throttled.example.test": throttledSrv,
			"healthy.example.test":   otherSrv,
		}),
		Now: clock.Now,
	})
	defer im.Close()

	ctx := context.Background()
	// Warm one cached logo on throttled.example.test before the 429 occurs.
	const warmURL = "http://throttled.example.test/warm.png"
	if got, _, err := im.GetUntrustedURL(ctx, warmURL); err != nil || !bytes.Equal(got, logo) {
		t.Fatalf("warm fetch: %v", err)
	}
	if throttledHits.Load() != 1 {
		t.Fatalf("warm hits = %d, want 1", throttledHits.Load())
	}

	// Trigger 429 with Retry-After: 120.
	status.Store(http.StatusTooManyRequests)
	if _, _, err := im.GetUntrustedURL(ctx, "http://throttled.example.test/rate-limited.png"); err == nil {
		t.Fatal("429 unexpectedly succeeded")
	}
	if throttledHits.Load() != 2 {
		t.Fatalf("expected 2 total hits after 429, got %d", throttledHits.Load())
	}

	// Further requests to ANY URL on throttled.example.test must stop until Retry-After.
	for i := 0; i < 100; i++ {
		u := fmt.Sprintf("http://throttled.example.test/other-%d.png", i%20)
		if _, _, err := im.GetUntrustedURL(ctx, u); err == nil {
			t.Fatalf("uncached request %d during host pause succeeded", i)
		}
	}
	// Cached copy on the paused host continues to be served without contacting upstream.
	if got, _, err := im.GetUntrustedURL(ctx, warmURL); err != nil || !bytes.Equal(got, logo) {
		t.Fatalf("cached copy unavailable during host pause: %v", err)
	}
	if throttledHits.Load() != 2 {
		t.Fatalf("429 Retry-After did not stop requests to host: got %d, want 2", throttledHits.Load())
	}

	// Unrelated hosts are not paused.
	if got, _, err := im.GetUntrustedURL(ctx, "http://healthy.example.test/ok.png"); err != nil || !bytes.Equal(got, logo) {
		t.Fatalf("healthy host blocked: %v", err)
	}
	if otherHostHits.Load() != 1 {
		t.Fatalf("healthy host hits = %d, want 1", otherHostHits.Load())
	}

	// Still paused at 119 s; resumes after 120 s.
	clock.Advance(119 * time.Second)
	if _, _, err := im.GetUntrustedURL(ctx, "http://throttled.example.test/other-1.png"); err == nil {
		t.Fatal("host unpaused before Retry-After elapsed")
	}
	if throttledHits.Load() != 2 {
		t.Fatalf("contacted host before Retry-After elapsed: %d", throttledHits.Load())
	}

	status.Store(http.StatusOK)
	clock.Advance(2 * time.Second)
	if got, _, err := im.GetUntrustedURL(ctx, "http://throttled.example.test/other-1.png"); err != nil || !bytes.Equal(got, logo) {
		t.Fatalf("host did not recover after Retry-After: %v", err)
	}
	if throttledHits.Load() != 3 {
		t.Fatalf("post-Retry-After hits = %d, want 3", throttledHits.Load())
	}

	// 503 with an HTTP-date Retry-After also pauses the host until that timestamp.
	untilDate := clock.Now().Add(5 * time.Minute).UTC().Format(http.TimeFormat)
	retryHeader.Store(untilDate)
	status.Store(http.StatusServiceUnavailable)
	if _, _, err := im.GetUntrustedURL(ctx, "http://throttled.example.test/503.png"); err == nil {
		t.Fatal("503 unexpectedly succeeded")
	}
	hitsAfter503 := throttledHits.Load()
	for i := 0; i < 25; i++ {
		_, _, _ = im.GetUntrustedURL(ctx, fmt.Sprintf("http://throttled.example.test/paused-%d.png", i))
	}
	if throttledHits.Load() != hitsAfter503 {
		t.Fatalf("503 Retry-After date did not pause host: got %d, want %d", throttledHits.Load(), hitsAfter503)
	}
}

func TestEvictedDiskImageRefetchedOnce(t *testing.T) {
	imgA := testPNG(t, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	imgB := testPNG(t, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	imgC := testPNG(t, color.RGBA{R: 70, G: 80, B: 90, A: 255})

	var hitsA atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		switch r.URL.Path {
		case "/t/p/w300/a.png":
			hitsA.Add(1)
			_, _ = w.Write(imgA)
		case "/t/p/w300/b.png":
			_, _ = w.Write(imgB)
		case "/t/p/w300/c.png":
			_, _ = w.Write(imgC)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	clock := newFakeClock()
	// MemoryBytes=64 keeps images out of the memory tier (> MemoryBytes/4);
	// DiskBytes fits roughly one image so writing B and C trims A from disk.
	im := NewImages(ImagesOptions{
		Dir:         t.TempDir(),
		MemoryBytes: 64,
		DiskBytes:   int64(len(imgA) + len(imgB)/2),
		HTTPClient:  srv.Client(),
		ImageURL:    func(path, size string) string { return srv.URL + "/t/p/" + size + path },
		Now:         clock.Now,
	})
	defer im.Close()

	ctx := context.Background()
	if _, _, err := im.Get(ctx, "/a.png", 300); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, _, err := im.Get(ctx, "/b.png", 300); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, _, err := im.Get(ctx, "/c.png", 300); err != nil {
		t.Fatal(err)
	}
	if hitsA.Load() != 1 {
		t.Fatalf("initial /a.png hits = %d, want 1", hitsA.Load())
	}

	// /a.png was evicted from disk; 50 concurrent requests must refetch it once.
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, _, err := im.Get(ctx, "/a.png", 300)
			if err != nil || !bytes.Equal(got, imgA) {
				t.Errorf("evicted refetch: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := hitsA.Load(); got != 2 {
		t.Fatalf("evicted image refetched %d times total, want 2 (1 initial + 1 post-eviction)", got)
	}
}
