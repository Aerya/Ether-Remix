package debrid

import (
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// localFake is the owner's BitTorrent client (Ether-Remix torrent client).
type localFake struct {
	*fakeProvider
	downloads bool
}

func (l *localFake) LocalDownloads() bool { return l.downloads }

// With cached-only on, an uncached release goes to the local BitTorrent
// client only: the debrid account is never asked to download it.
func TestQueueDownloadCachedOnlyUsesLocalClientOnly(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	tb := h.provs["torbox-0"]
	local := &localFake{fakeProvider: newFakeProvider("remix-torrent", h.media, movieFiles(1<<20)), downloads: true}
	h.svc.RegisterPluginProvider("remix-torrent", "Client torrent", local)
	if !h.svc.LocalDownloads() {
		t.Fatal("local downloads not reported")
	}
	rel := torrentRelease("r1")
	rel.TitleIDs = core.IDs{TMDB: 603}
	if _, err := h.svc.QueueDownload(t.Context(), rel); !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("queue: %v, want ErrDownloadQueued", err)
	}
	if tb.adds != 0 || tb.downloading != 0 {
		t.Fatalf("debrid account used: adds=%d downloading=%d", tb.adds, tb.downloading)
	}
	if local.adds != 1 || local.downloading != 1 {
		t.Fatalf("local client: adds=%d downloading=%d, want 1", local.adds, local.downloading)
	}
	// Background checks never add, even to the local client.
	bg := core.WithBackgroundBudget(t.Context(), func(context.Context, string, string, int64) error { return nil })
	other := torrentRelease("r2")
	other.InfoHash = "cd" + testHash[2:]
	if _, err := h.svc.QueueDownload(bg, other); errors.Is(err, core.ErrDownloadQueued) || local.adds != 1 {
		t.Fatalf("background: err=%v adds=%d", err, local.adds)
	}
	// Download finished on the client: the release plays from it.
	local.mu.Lock()
	local.cached[testHash] = true
	local.mu.Unlock()
	if _, err := h.svc.Acquire(t.Context(), rel); err != nil {
		t.Fatalf("after download: %v", err)
	}
}

// A local client with downloads off only serves what it already holds.
func TestQueueDownloadLocalClientDownloadsOff(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	local := &localFake{fakeProvider: newFakeProvider("remix-torrent", h.media, movieFiles(1<<20))}
	h.svc.RegisterPluginProvider("remix-torrent", "Client torrent", local)
	if h.svc.LocalDownloads() {
		t.Fatal("downloads reported while off")
	}
	if _, err := h.svc.QueueDownload(t.Context(), torrentRelease("r1")); errors.Is(err, core.ErrDownloadQueued) || local.adds != 0 {
		t.Fatalf("queue: err=%v adds=%d", err, local.adds)
	}
}

// A torrent remembered as not cached on the local client is asked again:
// a download that finished since must play at once.
func TestAcquireAsksLocalClientDespiteUncachedMemory(t *testing.T) {
	h := newHarness(t, 1<<20)
	local := &localFake{fakeProvider: newFakeProvider("remix-torrent", h.media, movieFiles(1<<20))}
	h.svc.RegisterPluginProvider("remix-torrent", "Client torrent", local)
	rel := torrentRelease("r1")
	if _, err := h.svc.Acquire(t.Context(), rel); !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("first check: %v", err)
	}
	local.mu.Lock()
	local.cached[testHash] = true
	local.mu.Unlock()
	if _, err := h.svc.Acquire(t.Context(), rel); err != nil {
		t.Fatalf("after download: %v", err)
	}
}

// localScripted answers Acquire with a fixed error, like the torrent client
// downloading (LocalDownloadError) or out of space (ErrLocalNoSpace).
type localScripted struct {
	*localFake
	err error
}

func (l *localScripted) Acquire(context.Context, AcquireRequest) (*Acquired, error) {
	return nil, l.err
}

// The torrent client's progress reaches the player, on the first play and
// while the download runs.
func TestLocalDownloadProgressReachesCaller(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	lde := &core.LocalDownloadError{Client: "qBittorrent", Progress: 0.4}
	local := &localScripted{localFake: &localFake{fakeProvider: newFakeProvider("remix-torrent", h.media, movieFiles(1<<20)), downloads: true},
		err: Pending("abc", lde)}
	h.svc.RegisterPluginProvider("remix-torrent", "Client torrent", local)
	rel := torrentRelease("r1")
	rel.TitleIDs = core.IDs{TMDB: 603}
	_, err := h.svc.QueueDownload(t.Context(), rel)
	var got *core.LocalDownloadError
	if !errors.As(err, &got) || got.Percent() != 40 || !errors.Is(err, core.ErrDownloadQueued) {
		t.Fatalf("queue: %v", err)
	}
	// Played again while downloading: still the progress, not a bare "not cached".
	if _, err := h.svc.Acquire(t.Context(), rel); !errors.As(err, &got) {
		t.Fatalf("acquire: %v", err)
	}
}

// A refusal by the torrent client (no free space) is not a queued download.
func TestLocalNoSpaceIsNotQueued(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	local := &localScripted{localFake: &localFake{fakeProvider: newFakeProvider("remix-torrent", h.media, movieFiles(1<<20)), downloads: true},
		err: core.ErrLocalNoSpace}
	h.svc.RegisterPluginProvider("remix-torrent", "Client torrent", local)
	rel := torrentRelease("r1")
	rel.TitleIDs = core.IDs{TMDB: 603}
	_, err := h.svc.QueueDownload(t.Context(), rel)
	if errors.Is(err, core.ErrDownloadQueued) || !errors.Is(err, core.ErrLocalNoSpace) || !errors.Is(err, core.ErrDownloadRefused) {
		t.Fatalf("queue: %v", err)
	}
	if core.LocalDownloadMessage(err, "fr") == "" {
		t.Fatal("no player message")
	}
}
