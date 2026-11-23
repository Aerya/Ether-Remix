package debrid

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type fakeTorBoxQueuedServer struct {
	mu                     sync.Mutex
	activeSlots            int
	activeLimit            int
	createReturnsActiveCnt bool
	createDuplicate        bool
	createDuplicateDetail  string
	createCalls            int
	myListCalls            int
	myListByIDCalls        int
	getQueuedCalls         int
	controlQueuedDeletes   []string

	queued   []tbQueued
	torrents []tbTorrent
}

func (f *fakeTorBoxQueuedServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"success":false,"error":"AUTH_ERROR","detail":"bad token"}`)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/torrents/createtorrent":
			f.createCalls++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if f.createDuplicate {
				detail := f.createDuplicateDetail
				errCode := "DUPLICATE_ITEM"
				if detail != "" {
					errCode = "DIFF_ISSUE"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"error":   errCode,
					"detail":  detail,
					"data":    nil,
				})
				return
			}
			data := map[string]any{
				"torrent_id": 0,
				"queued_id":  456,
				"hash":       testHash,
				"auth_id":    "auth-queued",
			}
			if f.createReturnsActiveCnt {
				data["current_active_downloads"] = f.activeSlots
				data["active_limit"] = f.activeLimit
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"error":   nil,
				"detail":  "Torrent queued successfully.",
				"data":    data,
			})

		case "/torrents/mylist":
			if id := r.URL.Query().Get("id"); id != "" {
				f.myListByIDCalls++
				for _, item := range f.torrents {
					if tbID(item.ID) == id {
						_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": item})
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "ITEM_NOT_FOUND", "detail": "missing"})
				return
			}
			f.myListCalls++
			if r.URL.Query().Get("bypass_cache") != "true" {
				t.Errorf("/torrents/mylist missing bypass_cache=true: %s", r.URL.RawQuery)
			}
			items := make([]tbTorrent, 0, f.activeSlots+len(f.torrents))
			for i := 0; i < f.activeSlots; i++ {
				items = append(items, tbTorrent{
					ID:               json.Number(fmt.Sprintf("%d", 10+i)),
					Hash:             fmt.Sprintf("%040x", 100+i),
					Name:             fmt.Sprintf("Other.Active.%d", i),
					Active:           true,
					DownloadPresent:  false,
					DownloadFinished: false,
					DownloadState:    "downloading",
				})
			}
			items = append(items, f.torrents...)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": items})

		case "/queued/getqueued":
			f.getQueuedCalls++
			if r.URL.Query().Get("type") != "torrent" || r.URL.Query().Get("bypass_cache") != "true" {
				t.Errorf("/queued/getqueued unexpected query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": f.queued})

		case "/queued/controlqueued":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["operation"] == "delete" {
				f.controlQueuedDeletes = append(f.controlQueuedDeletes, fmt.Sprint(body["queued_id"]))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": nil})

		case "/torrents/requestdl":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": "https://cdn.torbox.example/movie.mkv"})

		default:
			t.Errorf("unexpected TorBox request: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newTorBoxQueuedService(t *testing.T, srvURL string, cachedOnly bool) *Service {
	t.Helper()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	opts := ProviderOptions{
		BaseURL:      srvURL,
		PollInterval: time.Nanosecond,
		ReadyPolls:   1,
		Now:          func() time.Time { return now },
	}
	cfg := config.Debrid{
		CachedOnly:     cachedOnly,
		RemoveUncached: true,
		Providers:      []config.DebridProvider{{ID: "tb", Kind: "torbox", APIKey: testKey, Enabled: true}},
	}
	return NewService(cfg, Options{
		Now: func() time.Time { return now },
		NewProvider: func(pc config.DebridProvider, _ ProviderOptions) (Provider, error) {
			return newTorBox(pc.APIKey, opts), nil
		},
	})
}

func TestTorBoxQueuedCreateFollowedUntilReady(t *testing.T) {
	f := &fakeTorBoxQueuedServer{
		activeSlots:            3,
		activeLimit:            3,
		createReturnsActiveCnt: true,
		queued:                 []tbQueued{{ID: "456", Hash: testHash, Name: "Movie.2025", Type: "torrent"}},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	svc := newTorBoxQueuedService(t, srv.URL, false)
	rel := core.Release{
		ID:       "r1",
		Name:     "Movie.2025.2160p",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		TitleIDs: core.IDs{TMDB: 603},
	}

	// 1. TorBox queues createtorrent behind 3 active downloads -> ErrDownloadQueued, never ErrDownloadRefused.
	_, err := svc.QueueDownload(t.Context(), rel)
	if !errors.Is(err, core.ErrDownloadQueued) || errors.Is(err, core.ErrDownloadRefused) {
		t.Fatalf("QueueDownload err = %v, want ErrDownloadQueued without ErrDownloadRefused", err)
	}
	if got := core.DownloadQueuedDetail(err); got != "queued at TorBox behind 3 active downloads" {
		t.Fatalf("DownloadQueuedDetail = %q, want queued behind 3 active downloads", got)
	}
	stats := svc.Stats()
	if len(stats) != 1 || stats[0].Failures != 0 || stats[0].State != "ok" || stats[0].QueuedDownloads != 1 || stats[0].QueuedDetail != "queued at TorBox behind 3 active downloads" {
		t.Fatalf("stats after queue = %+v", stats)
	}

	// 2. Later open while still queued (now 2 active downloads): follows via mylist + getqueued, never re-adds.
	f.mu.Lock()
	f.activeSlots = 2
	f.mu.Unlock()
	_, err = svc.Acquire(t.Context(), rel)
	if !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("Acquire while queued err = %v, want ErrDownloadQueued", err)
	}
	if got := core.DownloadQueuedDetail(err); got != "queued at TorBox behind 2 active downloads" {
		t.Fatalf("updated DownloadQueuedDetail = %q, want 2 active downloads", got)
	}
	if got := svc.Stats()[0].QueuedDetail; got != "queued at TorBox behind 2 active downloads" {
		t.Fatalf("updated stats QueuedDetail = %q", got)
	}
	if f.createCalls != 1 {
		t.Fatalf("createtorrent called %d times while following queued item, want 1", f.createCalls)
	}

	// Another candidate release of the same title does not re-add and keeps the queued-behind reason.
	other := rel
	other.ID = "r2"
	other.Name = "Movie.2025.1080p"
	other.InfoHash = strings.Repeat("cd", 20)
	_, err = svc.QueueDownload(t.Context(), other)
	if !errors.Is(err, core.ErrDownloadQueued) || core.DownloadQueuedDetail(err) != "queued at TorBox behind 2 active downloads" {
		t.Fatalf("second candidate QueueDownload err = %v (detail=%q)", err, core.DownloadQueuedDetail(err))
	}
	if f.createCalls != 1 {
		t.Fatalf("createtorrent called %d times after second candidate, want 1", f.createCalls)
	}

	// 3. TorBox promotes the queued item to an active download (torrent_id=77, not finished yet).
	f.mu.Lock()
	f.queued = nil
	f.torrents = []tbTorrent{{
		ID:               "77",
		Hash:             testHash,
		Name:             "Movie.2025",
		Active:           true,
		DownloadPresent:  false,
		DownloadFinished: false,
		DownloadState:    "downloading",
	}}
	queuedCallsBeforeStart := f.getQueuedCalls
	f.mu.Unlock()

	_, err = svc.Acquire(t.Context(), rel)
	if !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("Acquire after promotion while downloading err = %v, want ErrDownloadQueued", err)
	}
	if got := core.DownloadQueuedDetail(err); got != "" {
		t.Fatalf("detail after TorBox started downloading = %q, want empty", got)
	}
	if f.getQueuedCalls != queuedCallsBeforeStart {
		t.Fatalf("unexpected /queued/getqueued call after item moved to /torrents/mylist")
	}

	// Subsequent open follows by torrent_id directly without re-listing mylist or getqueued.
	myListCallsBeforeDirect := f.myListCalls
	_, err = svc.Acquire(t.Context(), rel)
	if !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("Acquire by promoted torrent_id err = %v, want ErrDownloadQueued", err)
	}
	if f.myListCalls != myListCallsBeforeDirect || f.getQueuedCalls != queuedCallsBeforeStart {
		t.Fatalf("expected direct follow by torrent_id: myListCalls=%d->%d getQueuedCalls=%d->%d",
			myListCallsBeforeDirect, f.myListCalls, queuedCallsBeforeStart, f.getQueuedCalls)
	}

	// 4. Download finishes; plays immediately even after the owner turns CachedOnly back on.
	f.mu.Lock()
	f.torrents[0].Active = false
	f.torrents[0].DownloadPresent = true
	f.torrents[0].DownloadFinished = true
	f.torrents[0].DownloadState = "completed"
	f.torrents[0].Files = []tbFile{{ID: "1", Name: "Movie.2025.mkv", Size: 4 << 30}}
	f.mu.Unlock()

	cfg := svc.Config()
	cfg.CachedOnly = true
	svc.UpdateConfig(cfg)

	raw, err := svc.Acquire(t.Context(), rel)
	if err != nil || !IsPayload(raw) {
		t.Fatalf("Acquire after ready = payload:%v err=%v", IsPayload(raw), err)
	}
	if f.createCalls != 1 {
		t.Fatalf("createtorrent called %d times across full lifecycle, want 1", f.createCalls)
	}
	if st := svc.Stats()[0]; st.QueuedDownloads != 0 || st.QueuedDetail != "" {
		t.Fatalf("queued stats not cleared after ready: %+v", st)
	}
}

func TestTorBoxQueuedItemDisappearedFallsThrough(t *testing.T) {
	f := &fakeTorBoxQueuedServer{
		activeSlots:            0,
		createReturnsActiveCnt: false,
		queued:                 []tbQueued{{ID: "456", Hash: testHash, Name: "Movie.2025", Type: "torrent"}},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	svc := newTorBoxQueuedService(t, srv.URL, false)
	rel := core.Release{
		ID:       "r1",
		Name:     "Movie.2025",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		TitleIDs: core.IDs{TMDB: 604},
	}

	if _, err := svc.QueueDownload(t.Context(), rel); !errors.Is(err, core.ErrDownloadQueued) || core.DownloadQueuedDetail(err) != "" {
		t.Fatalf("QueueDownload err = %v (detail=%q)", err, core.DownloadQueuedDetail(err))
	}

	// Item disappears from both /torrents/mylist and /queued/getqueued.
	f.mu.Lock()
	f.queued = nil
	f.mu.Unlock()

	_, err := svc.Acquire(t.Context(), rel)
	if !errors.Is(err, core.ErrNotCached) || errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("Acquire after queued item vanished = %v, want ErrNotCached (not ErrDownloadQueued)", err)
	}
	if st := svc.Stats()[0]; st.QueuedDownloads != 0 {
		t.Fatalf("vanished queued item remained tracked: %+v", st)
	}
}

func TestTorBoxDuplicateAlreadyQueuedFollowed(t *testing.T) {
	f := &fakeTorBoxQueuedServer{
		activeSlots:           1,
		createDuplicate:       true,
		createDuplicateDetail: "Download already queued.",
		queued:                []tbQueued{{ID: "321", Hash: testHash, Name: "Movie.2025", Type: "torrent"}},
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	svc := newTorBoxQueuedService(t, srv.URL, false)
	rel := core.Release{
		ID:       "r1",
		Name:     "Movie.2025",
		Protocol: core.ProtocolTorrent,
		InfoHash: testHash,
		TitleIDs: core.IDs{TMDB: 605},
	}

	_, err := svc.QueueDownload(t.Context(), rel)
	if !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("QueueDownload duplicate-already-queued err = %v, want ErrDownloadQueued", err)
	}
	if got := core.DownloadQueuedDetail(err); got != "queued at TorBox behind 1 active download" {
		t.Fatalf("detail = %q, want singular 1 active download", got)
	}
}

func TestTorBoxCachedOnlyUnexpectedQueuedIsCleanedUp(t *testing.T) {
	f := &fakeTorBoxQueuedServer{
		activeSlots:            3,
		createReturnsActiveCnt: true,
	}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	p := newTorBox(testKey, fastOpts(srv.URL))
	_, err := p.Acquire(t.Context(), acquireReq(true))
	if !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("cached-only queued reply err = %v, want ErrNotCached", err)
	}
	if _, isQueued := pendingQueue(err); isQueued {
		t.Fatalf("cached-only check must not return queuedPending: %v", err)
	}
	if len(f.controlQueuedDeletes) != 1 || f.controlQueuedDeletes[0] != "456" {
		t.Fatalf("expected queued_id 456 to be deleted on cached-only cleanup, got %v", f.controlQueuedDeletes)
	}
}
