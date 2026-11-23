package vela

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	_ "modernc.org/sqlite"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type identityFake struct{ frontend.SurfaceIdentity }

func (identityFake) ResolveDevice(context.Context, string, frontend.Device) (*frontend.Viewer, time.Time, error) {
	return &frontend.Viewer{ID: 1, Username: "viewer"}, time.Now().Add(time.Hour), nil
}
func (identityFake) GrantScope(context.Context, string, frontend.Device) (string, error) {
	return "fake-family", nil
}
func (identityFake) GrantScopeCurrent(context.Context, string, frontend.Device) (*frontend.Viewer, error) {
	return &frontend.Viewer{ID: 1, Username: "viewer"}, nil
}

type descriptiveFake struct {
	frontendtest.Experience
	available bool
	calls     int
	state     frontend.DescriptiveSearchState
}

func (f *descriptiveFake) AISearchVisible(context.Context, *frontend.Viewer, []frontend.RatingKey) ([]frontend.Title, error) {
	return nil, nil
}

func (f *descriptiveFake) AISearchAvailable() bool { return f.available }
func (f *descriptiveFake) AISearch(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.SearchPage, error) {
	return frontend.SearchPage{}, nil
}
func (f *descriptiveFake) DescriptiveSearchAvailable() bool { return f.available }
func (f *descriptiveFake) DescriptiveSearch(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.SearchPage, error) {
	f.calls++
	return frontend.SearchPage{Continuation: frontend.ContinuationUnknown, Descriptive: &frontend.DescriptiveSearchStatus{State: f.state}}, nil
}

func TestVelaProtocolInvariants(t *testing.T) {
	t.Run("AI search jobs", checkAISearch)
	t.Run("pairing lineage", checkPairingLineage)
	t.Run("live pages", checkLivePages)
	t.Run("fresh home visit", checkFreshHomeVisit)
	t.Run("identity retry budget", func(t *testing.T) {
		for _, tc := range []struct {
			err                error
			status             int
			delay, class, code string
			attempts           int
		}{
			{&frontend.IdentityRateLimitError{RetryAfter: 125*time.Second + time.Millisecond}, 503, "126", "capacity", "rate_limited", 2},
			{&frontend.ReceiptCapacityError{RetryAfter: time.Minute}, 429, "60", "capacity", "receipt_capacity", 3},
			{frontend.ErrTrackSourceUnavailable, 502, "2", "source", "track_source_unavailable", 3},
			{&bootstrapChangedError{}, 409, "1", "conflict", "bootstrap_changed", 3},
		} {
			w := httptest.NewRecorder()
			fail(w, tc.err)
			var response struct {
				Error struct {
					Class, Code string
					Retry       struct {
						Allowed                   bool
						AfterSeconds, MaxAttempts int
					}
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			delay, _ := strconv.Atoi(tc.delay)
			if w.Code != tc.status || w.Header().Get("Retry-After") != tc.delay || response.Error.Class != tc.class || response.Error.Code != tc.code || !response.Error.Retry.Allowed || response.Error.Retry.AfterSeconds != delay || response.Error.Retry.MaxAttempts != tc.attempts {
				t.Fatal("identity retry policy lost", w.Code, response)
			}
		}
	})
	t.Run("typed playback failures", func(t *testing.T) {
		for _, tc := range []struct {
			err         error
			status      int
			class, code string
		}{
			{&frontend.NoCompatiblePathError{RejectedPaths: []frontend.RejectedPlaybackPath{{Path: "hls", Reasons: []string{"transcode not allowed"}}}}, 422, "playback", "no_compatible_path"},
			{frontend.ErrArchiveUnstreamable, 422, "source", "archive_unstreamable"},
			{frontend.ErrNotAvailable, 409, "source", "not_available"},
			{frontend.ErrNotFound, 404, "not-found", "not_found"},
			{frontend.ErrPolicyUnconfirmed, 403, "policy", "policy_unconfirmed"},
			{frontend.ErrSourceChanged, 409, "conflict", "source_changed"},
			{frontend.ErrSourceUnavailable, 502, "source", "source_unavailable"},
		} {
			w := httptest.NewRecorder()
			fail(w, tc.err)
			var wire struct {
				Error struct {
					Class, Code, Message string
					RejectedPaths        []frontend.RejectedPlaybackPath
					Retry                struct{ Allowed bool }
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || wire.Error.Class != tc.class || wire.Error.Code != tc.code || wire.Error.Retry.Allowed {
				t.Fatalf("typed failure: %d %s", w.Code, w.Body)
			}
			if tc.code == "no_compatible_path" && (len(wire.Error.RejectedPaths) != 1 || wire.Error.RejectedPaths[0].Reasons[0] != "transcode not allowed") {
				t.Fatal("rejected paths lost", w.Body)
			}
			if tc.code == "archive_unstreamable" && wire.Error.Message != "this archive format or compression method cannot be streamed" {
				t.Fatalf("archive_unstreamable message = %q", wire.Error.Message)
			}
		}
	})
	t.Run("search capability and mode", func(t *testing.T) {
		source := &descriptiveFake{available: true, state: frontend.DescriptiveReady}
		fastCalls := 0
		source.SearchFunc = func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.SearchPage, error) {
			fastCalls++
			return frontend.SearchPage{Continuation: frontend.ContinuationUnknown}, nil
		}
		i := &instance{host: frontend.Host{Identity: identityFake{}, Experience: source}}
		for _, mode := range []string{"descriptive", "fast"} {
			raw := json.RawMessage(`{"query":"80s sci-fi","mode":"` + mode + `"}`)
			r := httptest.NewRequest(http.MethodPost, "/vela/v0/search", nil)
			r = r.WithContext(context.WithValue(r.Context(), bodyKey{}, raw))
			r.Header.Set("Authorization", "Bearer token")
			r.Header.Set("X-Vela-Device-Id", "device")
			w := httptest.NewRecorder()
			i.search(w, r)
			if w.Code != 200 {
				t.Fatalf("%s search: %d", mode, w.Code)
			}
		}
		if source.calls != 1 || fastCalls != 1 {
			t.Fatal("search modes shared the wrong source")
		}
		for _, state := range []frontend.DescriptiveSearchState{frontend.DescriptiveReady, frontend.DescriptivePending, frontend.DescriptiveBusy, frontend.DescriptiveFailed} {
			source.state = state
			r := httptest.NewRequest(http.MethodPost, "/vela/v0/search", nil)
			r = r.WithContext(context.WithValue(r.Context(), bodyKey{}, json.RawMessage(`{"query":"cozy cute girls night in","mode":"descriptive"}`)))
			r.Header.Set("Authorization", "Bearer token")
			r.Header.Set("X-Vela-Device-Id", "device")
			w := httptest.NewRecorder()
			i.search(w, r)
			var result struct {
				Items       []any
				Descriptive struct {
					State string
					Retry struct {
						Allowed      bool
						AfterSeconds int
					}
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			retry := state != frontend.DescriptiveReady
			if w.Code != 200 || result.Descriptive.State != string(state) || result.Descriptive.Retry.Allowed != retry || (retry && result.Descriptive.Retry.AfterSeconds < 1) || len(result.Items) != 0 {
				t.Fatalf("job state %s: %d %s", state, w.Code, w.Body.String())
			}
		}

		for _, available := range []bool{true, false} {
			source.available = available
			if got := i.capabilities()["features"].(object)["descriptiveSearch"].(object)["available"]; got != available {
				t.Fatal("descriptive capability does not match source availability")
			}
		}
	})
	t.Run("broker media mount", func(t *testing.T) {
		mediaPath := "/sessions/media/session/grant/master.m3u8"
		mounted := false
		for _, listener := range (&instance{}).Listeners() {
			if listener.Shared && strings.HasPrefix(mediaPath, listener.PathPrefix+"/") {
				mounted = true
			}
		}
		if !mounted {
			t.Fatal("broker URL falls through to another frontend")
		}
		calls := 0
		i := &instance{sessions: map[string]*playSession{"session": {scope: "fake-family", device: "device"}}, host: frontend.Host{Identity: identityFake{}, Sessions: frontendtest.Sessions{ServeMediaFunc: func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.URL.Path != mediaPath {
				t.Fatal("media path changed before grant authentication")
			}
			// The broker, rather than API bearer authentication, owns denial.
			w.WriteHeader(http.StatusNotFound)
		}}}}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			i.serve(w, httptest.NewRequest(method, mediaPath, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("broker denial replaced: %d", w.Code)
			}
		}
		if calls != 2 {
			t.Fatal("media reads bypassed the scoped broker")
		}
	})
	for version, want := range map[string]bool{"0.1.0": true, "0.1.9": true, "0.1.0+build.7": true, "0.1.1-beta.2": true, "0.2.0-rc.1": true, "0.2.0": true, "0.2.7": true, "0.3.0-rc.1": true, "0.0.9": false, "0.1.0-alpha": false, "0.3.0": false, "1.0.0": false, "0.1": false, "0.01.0": false, "0.1.1-01": false, "": false} {
		if got := compatibleBuild(version); got != want {
			t.Fatalf("build %q: got %v, want %v", version, got, want)
		}
	}
	advertised := (&instance{}).capabilities()
	if advertised["compatibleClientBuilds"].(object)["minInclusive"] != "0.1.0" {
		t.Fatal("missing compatibility interval")
	}

	db, e := sql.Open("sqlite", ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`)
	if e != nil {
		t.Fatal(e)
	}
	seal, e := encryption(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	i := &instance{host: frontend.Host{Identity: identityFake{}}, ctx: ctx, cancel: cancel, mux: http.NewServeMux(), db: db, seal: seal, replays: map[string]*replay{}}
	calls := 0
	i.route("POST", "/test", func(w http.ResponseWriter, r *http.Request) {
		calls++
		send(w, 200, object{"secret": "access-token", "calls": calls})
	})
	credential := "test"
	request := func(b string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/vela/v0/test", strings.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Vela-Device-Id", "device")
		w := httptest.NewRecorder()
		i.serve(w, r)
		return w
	}
	first := request(`{"idempotencyKey":"one"}`)
	credential = "rotated-access"
	second := request(`{"idempotencyKey":"one"}`)
	if first.Code != 200 || second.Body.String() != first.Body.String() || calls != 1 {
		t.Fatalf("replay: %d %d calls %d", first.Code, second.Code, calls)
	}
	conflict := request(`{"idempotencyKey":"one","changed":true}`)
	if conflict.Code != 409 {
		t.Fatalf("conflict: %d", conflict.Code)
	}
	var saved []byte
	if e = db.QueryRow(`SELECT response FROM vela_replays`).Scan(&saved); e != nil || strings.Contains(string(saved), "access-token") {
		t.Fatal("response must be encrypted", e)
	}
	s := &playSession{source: 1, seek: 2, revision: 1, cursor: 1, signal: make(chan struct{}), plan: object{"sessionId": "session", "state": "ready"}}
	staleReply := httptest.NewRecorder()
	if !stale(staleReply, s, 1, 1) || staleReply.Code != 409 {
		t.Fatal("stale seek generation accepted")
	}
	s.publish("plan")
	s.plan["state"] = "paused"
	b, _ := json.Marshal(s.events[0])
	if strings.Contains(string(b), "paused") {
		t.Fatal("retained events alias mutable plan")
	}
	for range 100 {
		s.publish("plan")
	}
	if len(s.events) != 64 {
		t.Fatal("unbounded replay retention")
	}
	r := httptest.NewRequest("GET", "/vela/v0/capabilities", nil)
	w := httptest.NewRecorder()
	cache(w, r, object{"protocolVersion": 0})
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	again := httptest.NewRecorder()
	cache(again, r, object{"protocolVersion": 0})
	if again.Code != 304 {
		t.Fatal("etag not honored")
	}
}
