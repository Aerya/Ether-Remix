package plex

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type spyPluginSlots struct {
	mu           sync.Mutex
	byKey        map[core.RatingKey][]core.ReleaseVariant
	labelCalls   int
	noLabelCalls int
}

func (s *spyPluginSlots) Variants(ctx context.Context, req core.ResolveRequest, max int) ([]core.ReleaseVariant, error) {
	return s.VersionSlots(ctx, req, max, true)
}

func (s *spyPluginSlots) VersionSlots(_ context.Context, req core.ResolveRequest, max int, labels bool) ([]core.ReleaseVariant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !labels {
		s.noLabelCalls++
		return nil, nil
	}
	s.labelCalls++
	vs := append([]core.ReleaseVariant(nil), s.byKey[req.Title.Key]...)
	if max > 0 && len(vs) > max {
		vs = vs[:max]
	}
	return vs, nil
}

func TestPlexPluginListNeverResolvesAndDetailPlayVersionHonoursPick(t *testing.T) {
	k1 := core.PluginKey(501)
	k2 := core.PluginKey(502)
	t1 := core.Title{Key: k1, Type: core.TypeMovie, Title: "Plugin One", Year: 2025, Runtime: time.Hour}
	t2 := core.Title{Key: k2, Type: core.TypeMovie, Title: "Plugin Two", Year: 2025, Runtime: time.Hour}

	info4K := core.MediaInfo{Container: "mkv", Duration: time.Hour, Size: 20 << 30, Estimated: true, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160}}}
	info1080 := core.MediaInfo{Container: "mkv", Duration: time.Hour, Size: 6 << 30, Estimated: true, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}

	slots := &spyPluginSlots{
		byKey: map[core.RatingKey][]core.ReleaseVariant{
			k1: {
				{ID: core.KindAutoVersionID, Label: "Auto · 4K", Auto: true, Release: core.Release{ID: "kind:4k", Name: "4K"}, Info: info4K},
				{ID: "kind:1080p", Label: "1080p", Release: core.Release{ID: "kind:1080p", Name: "1080p"}, Info: info1080},
			},
			k2: {
				{ID: core.KindAutoVersionID, Label: "Auto · 4K", Auto: true, Release: core.Release{ID: "kind:4k-2", Name: "4K"}, Info: info4K},
			},
		},
	}

	cat := &fakeCatalog{
		titles:   map[core.RatingKey]core.Title{k1: t1, k2: t2},
		children: map[core.RatingKey][]core.RatingKey{},
		facets:   map[string][]core.FacetValue{},
	}
	pb := &fakeVariantPlayback{fakePlayback: newFakePlayback(info1080)}
	d := testServerDeps{
		Catalog: cat,
		State:   newFakeState(),
		Deps: Deps{
			Playback:          pb,
			Transcoder:        newFakeTranscoder(core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mkv"}),
			DisableEnrichment: true,
		},
	}
	setDepsAuth(&d, &fakeAuth{users: map[string]core.User{ownerToken: ownerUser}})
	setDepsConfig(&d, func() config.Config {
		c := config.Config{}
		c.Server.Port = 32400
		c.Server.FriendlyName = "Ether Test"
		c.Server.MachineIdentifier = "0123456789abcdef0123456789abcdef01234567"
		return c
	})
	setDepsIdentity(&d, Identity{Version: "1.43.4.10903-e5521bd8c", Platform: "Linux"})
	setDepsNow(&d, func() time.Time { return fixedNow })
	srv := newTestServer(d)
	srv.setVariants(slots)
	t.Cleanup(func() { srv.Close() })
	mountTestBroker(srv, func() time.Time { return fixedNow })

	pr := &Request{User: &ownerUser, ClientID: "plex-client", Product: "Plex Web"}
	ctx := context.Background()

	// 1. Multi-item list render (even with detail:true like On Deck / batch lookup)
	// must NEVER call VersionSlots with labels=true for plugin items.
	list := srv.renderTitles(ctx, pr, []core.Title{t1, t2}, renderOpts{detail: true, media: true})
	if len(list) != 2 {
		t.Fatalf("renderTitles list len = %d, want 2", len(list))
	}
	if slots.labelCalls != 0 {
		t.Fatalf("multi-item list render triggered %d resolving VersionSlots(labels=true) calls, want 0", slots.labelCalls)
	}

	// 2. Single-item detail render DOES expose Play Version <Media> rows for the plugin key.
	single := srv.renderTitles(ctx, pr, []core.Title{t1}, renderOpts{detail: true, media: true})
	if len(single) != 1 || len(single[0].Media) != 2 {
		t.Fatalf("detail render Media count = %d, want 2: %+v", len(single[0].Media), single[0].Media)
	}
	if slots.labelCalls != 1 {
		t.Fatalf("detail render labelCalls = %d, want 1", slots.labelCalls)
	}

	// 3. Play Version pick via mediaIndex=1 honours the picked 1080p plugin release.
	req := &playRequest{key: k1, mediaIndex: 1}
	ps, err := srv.preparePlayback(ctx, pr, req)
	if err != nil {
		t.Fatalf("preparePlayback(mediaIndex=1): %v", err)
	}
	if ps == nil {
		t.Fatal("preparePlayback returned nil session")
	}
	if len(pb.variantCalls) == 0 || pb.variantCalls[len(pb.variantCalls)-1] != "kind:1080p" {
		t.Fatalf("PrepareVariant calls = %v, want kind:1080p", pb.variantCalls)
	}
}
