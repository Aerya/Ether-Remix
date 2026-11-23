package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func TestManifestLiveFeedsValidation(t *testing.T) {
	base := func(caps []Capability, feeds []LiveFeedMeta) Manifest {
		return Manifest{
			ID:      "community.acme.live",
			Version: "1.0.0",
			Name:    "Live feed plugin",
			Ether: &EtherBlock{
				Protocol:     Protocol,
				Kind:         KindExtension,
				Capabilities: caps,
				Summary:      "Provides live event updates",
				Runtime:      RuntimeDecl{Kind: "hosted", Entry: "index.js"},
				Contributes:  Contributes{LiveFeeds: feeds},
			},
		}
	}

	valid := base([]Capability{CapLiveEvents}, []LiveFeedMeta{
		{ID: "main", Title: "Live events", MinIntervalSec: 15, EntityKinds: []string{"team", "driver"}},
	})
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid manifest, got %v", err)
	}
	if lf, ok := valid.LiveFeedByID("main"); !ok || lf.MinIntervalSec != 15 {
		t.Fatalf("LiveFeedByID failed: ok=%v lf=%+v", ok, lf)
	}

	// Missing liveFeeds when capability is set.
	if err := base([]Capability{CapLiveEvents}, nil).Validate(); err == nil {
		t.Fatal("expected error when live_events has no liveFeeds")
	}
	// liveFeeds declared without capability.
	if err := base(nil, []LiveFeedMeta{{ID: "main", Title: "Live events", MinIntervalSec: 15}}).Validate(); err == nil {
		t.Fatal("expected error when liveFeeds lacks live_events capability")
	}
	// minIntervalSec < 10.
	err := base([]Capability{CapLiveEvents}, []LiveFeedMeta{{ID: "main", Title: "Live events", MinIntervalSec: 5}}).Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) || !slices.ContainsFunc(ve.Fields, func(f FieldError) bool { return f.Field == "ether.contributes.liveFeeds[0].minIntervalSec" }) {
		t.Fatalf("expected minIntervalSec error, got %v", err)
	}
	// Duplicate feed id and invalid entityKind.
	err = base([]Capability{CapLiveEvents}, []LiveFeedMeta{
		{ID: "main", Title: "Live events", MinIntervalSec: 10, EntityKinds: []string{"Bad:Kind"}},
		{ID: "main", Title: "Second feed", MinIntervalSec: 10},
	}).Validate()
	if !errors.As(err, &ve) || len(ve.Fields) < 2 {
		t.Fatalf("expected duplicate id and entityKinds errors, got %v", err)
	}

	// RolesOff hides liveFeeds via EffectiveEther.
	off := valid
	off.rolesOff = []Capability{CapLiveEvents}
	if off.HasCapability(CapLiveEvents) {
		t.Fatal("expected role-off to disable live_events")
	}
	if _, ok := off.LiveFeedByID("main"); ok {
		t.Fatal("expected role-off to hide liveFeeds")
	}

	// PermissionSheet & PermissionDiff mention live event feeds.
	lines, _, _ := PermissionSheet(valid)
	if !slices.Contains(lines, "Provides live event feeds") {
		t.Fatalf("PermissionSheet missing live events line: %v", lines)
	}
	without := base(nil, nil)
	diff := PermissionDiff(without, valid)
	if !slices.Contains(diff, "Provides live event feeds") {
		t.Fatalf("PermissionDiff missing live events line: %v", diff)
	}
}

func TestManagerActiveLiveFeedsLifecycle(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	raw, err := json.Marshal(map[string]any{
		"id":      "community.acme.live",
		"version": "1.0.0",
		"name":    "Acme Live",
		"ether": map[string]any{
			"protocol":     Protocol,
			"kind":         KindExtension,
			"summary":      "Provides live event feeds",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{string(CapLiveEvents)},
			"contributes": map[string]any{
				"liveFeeds": []map[string]any{
					{"id": "scores", "title": "Live scores", "minIntervalSec": 15, "entityKinds": []string{"team"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"dist/plugin.js": []byte("globalThis.handle = () => ({ status: 200, body: '{}' });"),
	}

	prev, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true}); err != nil {
		t.Fatal(err)
	}
	feeds := env.m.ActiveLiveFeeds(ctx)
	if len(feeds) != 1 || feeds[0].PluginID != "community.acme.live" || feeds[0].FeedID != "scores" || feeds[0].MinIntervalSec != 15 {
		t.Fatalf("ActiveLiveFeeds after install = %+v", feeds)
	}

	// Revoking live_events grant hides the feed.
	if err := env.m.SetGrants(ctx, "community.acme.live", Grants{Revoked: []string{string(CapLiveEvents)}}); err != nil {
		t.Fatal(err)
	}
	if got := env.m.ActiveLiveFeeds(ctx); len(got) != 0 {
		t.Fatalf("revoked plugin still in ActiveLiveFeeds: %+v", got)
	}
	if err := env.m.SetGrants(ctx, "community.acme.live", Grants{}); err != nil {
		t.Fatal(err)
	}
	if got := env.m.ActiveLiveFeeds(ctx); len(got) != 1 {
		t.Fatalf("restored grant missing from ActiveLiveFeeds: %+v", got)
	}

	// Disabling hides the feed; re-enabling restores it.
	if err := env.m.SetEnabled(ctx, "community.acme.live", false); err != nil {
		t.Fatal(err)
	}
	if got := env.m.ActiveLiveFeeds(ctx); len(got) != 0 {
		t.Fatalf("disabled plugin still in ActiveLiveFeeds: %+v", got)
	}
	if err := env.m.SetEnabled(ctx, "community.acme.live", true); err != nil {
		t.Fatal(err)
	}
	if got := env.m.ActiveLiveFeeds(ctx); len(got) != 1 {
		t.Fatalf("re-enabled plugin missing from ActiveLiveFeeds: %+v", got)
	}

	// Shadowing hides the feed.
	if err := env.m.SetShadow(ctx, "community.acme.live", true); err != nil {
		t.Fatal(err)
	}
	if got := env.m.ActiveLiveFeeds(ctx); len(got) != 0 {
		t.Fatalf("shadowed plugin still in ActiveLiveFeeds: %+v", got)
	}
	if err := env.m.SetShadow(ctx, "community.acme.live", false); err != nil {
		t.Fatal(err)
	}

	// Removing deletes the feed.
	if err := env.m.Remove(ctx, "community.acme.live", false); err != nil {
		t.Fatal(err)
	}
	if got := env.m.ActiveLiveFeeds(ctx); len(got) != 0 {
		t.Fatalf("removed plugin still in ActiveLiveFeeds: %+v", got)
	}
}
