package debrid

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestQuickNativeNeverAcquiresOnDebrid(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	remote := h.provs["torbox-0"]
	remote.cached[testHash] = true
	rel := torrentRelease("quick-native")
	// Even a previously stored, playable cloud payload must not bypass source order.
	if _, err := h.svc.Acquire(t.Context(), rel); err != nil {
		t.Fatal(err)
	}
	before := remote.adds
	ctx := core.WithQuickSource(t.Context(), "torrent", nil)
	if _, err := h.svc.Acquire(ctx, rel); err == nil {
		t.Fatal("used remote debrid without a native client")
	}
	local := &localFake{fakeProvider: newFakeProvider("remix-torrent", h.media, movieFiles(1<<20)), downloads: true}
	local.cached[testHash] = true
	h.svc.RegisterPluginProvider("remix-torrent", "Native", local)
	data, err := h.svc.Acquire(ctx, rel)
	if err != nil {
		t.Fatal(err)
	}
	p, err := DecodePayload(data)
	if err != nil || p.Kind != "plugin:remix-torrent" || !p.NativeTorrentOnly {
		t.Fatalf("payload=%+v err=%v", p, err)
	}
	if _, err := h.svc.alternateProvider(t.Context(), p); err == nil {
		t.Fatal("changed provider after play")
	}
	if remote.adds != before {
		t.Fatal("native stage acquired on remote debrid")
	}
}

func TestQuickNativeQueueNeverUsesCloud(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox")
	cfg := h.svc.Config()
	cfg.CachedOnly = false
	h.svc.UpdateConfig(cfg)
	ctx := core.WithQuickSource(t.Context(), "torrent", nil)
	_, _ = h.svc.QueueDownload(ctx, torrentRelease("native-missing"))
	if h.provs["torbox-0"].adds != 0 {
		t.Fatal("native queue used cloud account")
	}
}
