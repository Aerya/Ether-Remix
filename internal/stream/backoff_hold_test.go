package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/usenet"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

func TestLiveByteRangeHoldsAcrossProviderBackoffAndKeepsHTTPAlive(t *testing.T) {
	const part = 64 << 10
	want := mkvBytes(41, 8*part)
	srv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})
	postRes, err := srv.Post(map[string][]byte{"video.mkv": want}, part, nntptest.PostOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var ipLocked atomic.Bool
	refused := make(chan struct{})
	var refusedOnce sync.Once
	srv.SetFault(func(cmd, _ string) nntptest.Fault {
		if cmd == "AUTHINFO" && ipLocked.Load() {
			refusedOnce.Do(func() { close(refused) })
			return nntptest.Fault{Code: 481, Text: "max number of simultaneous IP addresses reached"}
		}
		return nntptest.Fault{}
	})

	fetcher := usenet.New([]config.UsenetProvider{srv.Provider("primary", 4)}, usenet.Options{
		MinIdle:    -1,
		KeepAlive:  -1,
		BackoffMin: 25 * time.Millisecond,
		BackoffMax: 80 * time.Millisecond,
		Retries:    0,
	})
	t.Cleanup(func() { _ = fetcher.Close() })

	e := newTestEngine(t, fetcher, nil, Options{
		ReadAheadMin:      2 * part,
		ReadAheadMax:      4 * part,
		ReadAheadParallel: 2,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	media, err := e.Open(ctx, "backoff-http", postRes.NZB)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()

	// Drop the warm socket opened during Open and lock the provider with NNTP 481
	// so both the active HTTP byte-range read and read-ahead hit provider backoff.
	ipLocked.Store(true)
	fetcher.Redial()

	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		buf := make([]byte, end-start+1)
		rc := media.(core.ContextReaderAt)
		n, rerr := rc.ReadAtContext(r.Context(), buf, start)
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			http.Error(w, rerr.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+int64(n)-1, media.Size()))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(buf[:n])
	}))
	defer httpSrv.Close()

	rangeStart := int64(2 * part)
	rangeEnd := int64(4*part - 1)
	type httpResult struct {
		status int
		body   []byte
		err    error
	}
	resCh := make(chan httpResult, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpSrv.URL+"/stream", nil)
		if err != nil {
			resCh <- httpResult{err: err}
			return
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd))
		resp, err := httpSrv.Client().Do(req)
		if err != nil {
			resCh <- httpResult{err: err}
			return
		}
		defer resp.Body.Close()
		body, rerr := io.ReadAll(resp.Body)
		resCh <- httpResult{status: resp.StatusCode, body: body, err: rerr}
	}()

	select {
	case <-refused:
	case <-ctx.Done():
		t.Fatal("timed out waiting for NNTP 481 refusal")
	}

	// The HTTP range request must still be held open while the provider is in backoff.
	select {
	case r := <-resCh:
		t.Fatalf("HTTP range returned early during backoff: status=%d err=%v", r.status, r.err)
	default:
	}

	ipLocked.Store(false)

	res := <-resCh
	if res.err != nil {
		t.Fatalf("HTTP range request failed: %v", res.err)
	}
	if res.status != http.StatusPartialContent {
		t.Fatalf("HTTP status = %d, want 206 (%s)", res.status, string(res.body))
	}
	if !bytes.Equal(res.body, want[rangeStart:rangeEnd+1]) {
		t.Fatalf("HTTP range payload mismatch: got %d bytes, want %d", len(res.body), rangeEnd-rangeStart+1)
	}

	// Read-ahead failures during backoff must not poison subsequent sequential reads.
	nextBuf := make([]byte, 2*part)
	if n, err := media.ReadAt(nextBuf, rangeEnd+1); err != nil || n != len(nextBuf) || !bytes.Equal(nextBuf, want[rangeEnd+1:rangeEnd+1+int64(len(nextBuf))]) {
		t.Fatalf("follow-up sequential read after backoff recovery: n=%d err=%v", n, err)
	}
}

func TestLiveByteRangeFailsOnlyAfterBackoffHoldBoundExpires(t *testing.T) {
	const part = 64 << 10
	want := mkvBytes(42, 4*part)
	srv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})
	postRes, err := srv.Post(map[string][]byte{"video.mkv": want}, part, nntptest.PostOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var ipLocked atomic.Bool
	srv.SetFault(func(cmd, _ string) nntptest.Fault {
		if cmd == "AUTHINFO" && ipLocked.Load() {
			return nntptest.Fault{Code: 481, Text: "max number of simultaneous IP addresses reached"}
		}
		return nntptest.Fault{}
	})

	fetcher := usenet.New([]config.UsenetProvider{srv.Provider("primary", 4)}, usenet.Options{
		MinIdle:    -1,
		KeepAlive:  -1,
		BackoffMin: 10 * time.Second,
		BackoffMax: 30 * time.Second,
		Retries:    0,
	})
	t.Cleanup(func() { _ = fetcher.Close() })

	e := newTestEngine(t, fetcher, nil, Options{
		ReadAheadMin: 1,
		ReadAheadMax: 1,
		BackoffHold:  60 * time.Millisecond,
	})
	media, err := e.Open(context.Background(), "backoff-expire", postRes.NZB)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()

	ipLocked.Store(true)
	fetcher.Redial()

	_, err = media.(core.ContextReaderAt).ReadAtContext(context.Background(), make([]byte, part), part)
	if !errors.Is(err, usenet.ErrProviderBackoff) || !errors.Is(err, core.ErrSourceUnavailable) {
		t.Fatalf("ReadAtContext after BackoffHold expiry = %v, want ErrProviderBackoff / ErrSourceUnavailable", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadAtContext leaked DeadlineExceeded instead of provider backoff: %v", err)
	}
}

func TestReadAheadBackoffDoesNotPoisonArchiveVerificationOrActiveRead(t *testing.T) {
	const part = 64 << 10
	payload := mkvBytes(43, 2<<20)
	vols := buildRAR(t, []archivetest.RARFile{{Name: "archive.mkv", Data: payload}}, archivetest.RAROptions{
		Version:      5,
		VolumeSize:   256 << 10,
		NewNumbering: true,
	})

	srv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})
	files := make(map[string][]byte, len(vols))
	for i, v := range vols {
		files[fmt.Sprintf("archive.part%02d.rar", i+1)] = v
	}
	postRes, err := srv.Post(files, part, nntptest.PostOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nzbBytes, err := nzb.Parse(postRes.NZB)
	if err != nil {
		t.Fatal(err)
	}
	_ = nzbBytes

	var ipLocked atomic.Bool
	srv.SetFault(func(cmd, _ string) nntptest.Fault {
		if cmd == "AUTHINFO" && ipLocked.Load() {
			return nntptest.Fault{Code: 481, Text: "max number of simultaneous IP addresses reached"}
		}
		return nntptest.Fault{}
	})

	fetcher := usenet.New([]config.UsenetProvider{srv.Provider("primary", 4)}, usenet.Options{
		MinIdle:    -1,
		KeepAlive:  -1,
		BackoffMin: 25 * time.Millisecond,
		BackoffMax: 80 * time.Millisecond,
		Retries:    0,
	})
	t.Cleanup(func() { _ = fetcher.Close() })

	e := newTestEngine(t, fetcher, nil, Options{
		ReadAheadMin:      128 << 10,
		ReadAheadMax:      256 << 10,
		ReadAheadParallel: 2,
	})
	src := openMedia(t, e, "rar-backoff", postRes.NZB).(*source)
	if src.plan.isVerified(2) {
		t.Fatal("third RAR volume unexpectedly pre-verified")
	}

	// Force provider into NNTP 481 simultaneous-IP backoff and let read-ahead
	// hit unverified volume 2 while backing off.
	ipLocked.Store(true)
	fetcher.Redial()

	start := src.plan.layout().ext[2].inner
	src.readAhead(start-1, 1)
	src.raWG.Wait()

	// Now start an active client read across volume 2 while still in backoff,
	// then clear the IP lock so the provider recovers.
	got := make([]byte, 128<<10)
	readErr := make(chan error, 1)
	go func() {
		_, err := src.ReadAtContext(context.Background(), got, start)
		readErr <- err
	}()

	ipLocked.Store(false)
	if err := <-readErr; err != nil {
		t.Fatalf("active ReadAtContext into volume 2 after read-ahead backoff failed: %v", err)
	}
	if !bytes.Equal(got, payload[start:start+int64(len(got))]) {
		t.Fatal("RAR payload mismatch after recovering from backoff")
	}
}

func openMedia(t *testing.T, e *Engine, id string, nzbData []byte) core.MediaSource {
	t.Helper()
	m, err := e.Open(context.Background(), id, nzbData)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}
