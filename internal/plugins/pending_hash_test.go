package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// The two protocols must compose: viewer retries remain bounded while the
// host's single remote read outlives any individual guest invocation.
func TestPendingHashContracts(t *testing.T) {
	t.Run("pending retry and job wake", func(t *testing.T) {
		env := newTestEnv(t)
		rec := env.installHello(t, "1.0.0")
		var calls atomic.Int32
		path := "/ether/v1/kinds/recording/resolve"
		env.fake.routes[path] = func(req Request) (Response, error) {
			if calls.Add(1) == 1 {
				return Response{Status: 200, Body: json.RawMessage(`{"pending":true,"retryInMs":0}`)}, nil
			}
			return Response{Status: 200, Body: json.RawMessage(`{"sources":[{"pattern":"http"}]}`)}, nil
		}
		resp, err := env.m.Call(context.Background(), rec.ID, Request{Path: path, Method: "POST", Body: json.RawMessage(`{"item":{"id":"one"}}`)})
		if err != nil || calls.Load() != 2 || resp.Status != 200 {
			t.Fatalf("retry: %s %v calls=%d", resp.Body, err, calls.Load())
		}
		var complete atomic.Bool
		env.fake.routes[path] = func(req Request) (Response, error) {
			if !complete.Load() {
				return Response{Status: 200, Body: json.RawMessage(`{"pending":true,"retryInMs":5000,"job":"/ether/job/resolve"}`)}, nil
			}
			return Response{Status: 200, Body: json.RawMessage(`{"sources":[{}]}`)}, nil
		}
		env.fake.routes["/ether/job/resolve"] = func(req Request) (Response, error) {
			var body struct {
				Item struct {
					ExternalID string `json:"id"`
				} `json:"item"`
			}
			if json.Unmarshal(req.Body, &body) != nil || body.Item.ExternalID != "one" {
				return Response{}, fmt.Errorf("lost item request: %s", req.Body)
			}
			complete.Store(true)
			return Response{Status: 200, Body: json.RawMessage(`{"done":true}`)}, nil
		}
		defer env.m.cancelActionJobs(rec.ID)
		resp, err = env.m.Call(context.Background(), rec.ID, Request{Path: path, Method: "POST", Body: json.RawMessage(`{"item":{"id":"one"}}`)})
		if err != nil || resp.Status != 200 {
			t.Fatalf("job did not wake resolver: %s %v", resp.Body, err)
		}
	})
	t.Run("total viewer budget", func(t *testing.T) {
		env := newTestEnv(t)
		rec := env.installHello(t, "1.0.0")
		path := "/ether/v1/kinds/recording/match"
		var calls atomic.Int32
		env.fake.routes[path] = func(req Request) (Response, error) {
			calls.Add(1)
			d, err := time.Parse(time.RFC3339, req.Headers["X-Ether-Deadline"])
			if err != nil || time.Until(d) > 5*time.Second {
				return Response{}, fmt.Errorf("individual call exceeds five seconds")
			}
			return Response{Status: 200, Body: json.RawMessage(`{"pending":true,"retryInMs":999999}`)}, nil
		}
		resp, err := env.m.Call(context.Background(), rec.ID, Request{Path: path, Method: "POST", Body: json.RawMessage(`{"item":{"id":"one"}}`)})
		if err != nil || resp.Status != 202 || calls.Load() > 6 {
			t.Fatalf("budget: status=%d err=%v calls=%d", resp.Status, err, calls.Load())
		}
		health, err := env.m.store.health(context.Background(), rec.ID)
		if err != nil || health.Status == "error" {
			t.Fatalf("pending health: %+v %v", health, err)
		}
	})
	t.Run("hash dedup cache and validators", func(t *testing.T) {
		env := newTestEnv(t)
		rec := env.installHello(t, "1.0.0")
		var reads atomic.Int32
		release := make(chan struct{})
		started := make(chan struct{}, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reads.Add(1)
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(304)
				return
			}
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte("remote bytes"))
		}))
		defer srv.Close()
		u, _ := url.Parse(srv.URL)
		rec.Manifest.Ether.Permissions.Network = []string{u.Hostname()}
		rec.ManifestRaw, _ = json.Marshal(rec.Manifest)
		if err := env.m.store.upsert(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
		env.m.client = srv.Client()
		h := env.m.HostFor(rec.ID).(*host)
		defer func() {
			env.m.cancelRemoteHashes(rec.ID)
			env.m.remoteHashMu.Lock()
			w := env.m.remoteHashes[rec.ID]
			env.m.remoteHashMu.Unlock()
			if w != nil {
				<-w.done
			}
		}()
		req := HashRequest{URL: srv.URL, Algo: "sha256"}
		out, err := h.HTTPHash(context.Background(), req)
		if err != nil || !out.Pending || out.RetryInMS < 250 {
			t.Fatalf("first answer: %+v %v", out, err)
		}
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("hash did not start")
		}
		for range 10 {
			out, err = h.HTTPHash(context.Background(), req)
			if err != nil || !out.Pending {
				t.Fatalf("duplicate: %+v %v", out, err)
			}
		}
		close(release)
		wait := func() {
			env.m.remoteHashMu.Lock()
			w := env.m.remoteHashes[rec.ID]
			env.m.remoteHashMu.Unlock()
			select {
			case <-w.done:
				if w.err != nil {
					t.Fatal(w.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("hash did not finish")
			}
		}
		wait()
		req.Algo = "md5"
		out, err = h.HTTPHash(context.Background(), req)
		if err != nil || out.Pending || out.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("remote bytes"))) || out.MD5 == "" || reads.Load() != 1 {
			t.Fatalf("cache: %+v %v reads=%d", out, err, reads.Load())
		}
		if _, err = env.m.store.db.Exec(`UPDATE plugins_remote_hashes SET checked=0`); err != nil {
			t.Fatal(err)
		}
		out, err = h.HTTPHash(context.Background(), req)
		if err != nil || !out.Pending {
			t.Fatalf("revalidation: %+v %v", out, err)
		}
		wait()
		out, err = h.HTTPHash(context.Background(), req)
		if err != nil || out.Pending || reads.Load() != 2 {
			t.Fatalf("validator cache: %+v %v reads=%d", out, err, reads.Load())
		}
	})
}
