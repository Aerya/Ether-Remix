package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestVerifiedFileOutranksRemembered(t *testing.T) {
	h := newHarness(t, nil, nil)
	vf := core.Release{
		ID: core.VerifiedFileIDPrefix + "x", Size: 1 << 20, Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid",
		StreamURL: "https://alldebrid.com/f/abc",
	}
	if !core.IsVerifiedFile(vf) {
		t.Skip("fixture de fichier vérifié invalide")
	}
	addon := core.Release{ID: "addon", Protocol: core.ProtocolHTTP, StreamURL: "https://x/y"}
	if !h.m.verifiedFileOutranks(vf, addon) {
		t.Fatal("fichier vérifié non prioritaire sur un flux d'addon (défaut first)")
	}
	if h.m.verifiedFileOutranks(addon, vf) || h.m.verifiedFileOutranks(vf, vf) {
		t.Fatal("priorité inversée")
	}
	cfg := h.cfg
	cfg.Resolver.VerifiedFiles = "off"
	h.m.SetConfig(cfg)
	if h.m.verifiedFileOutranks(vf, addon) {
		t.Fatal("verifiedFiles=off ignoré")
	}
}

// A debrid release skipped after an account error does not spend an attempt:
// a Usenet release behind several of them is still reached.

func TestRememberedAddonWithoutPickGivesWayToVerifiedFile(t *testing.T) {
	verified := core.Release{ID: core.VerifiedFileIDPrefix + "y", GUID: core.VerifiedFileIDPrefix + "y", Name: "Alko", Size: 100_000, Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid", StreamURL: "https://alldebrid.com/f/def"}
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute), mkv(verified.ID, 100_000, time.Minute)}, []string{"A"})
	h.resolver.candidates[0].Protocol, h.resolver.candidates[0].StreamURL = core.ProtocolHTTP, "https://addon.example/stream"
	caps := defaultCaps("c1")
	first, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
	if err != nil || first.Resolution.Release.ID != "A" {
		t.Fatalf("first play: %v", err)
	}
	h.m.waitResumeSaves()
	h.m.Stop(first.ID)
	h.resolver.mu.Lock()
	h.resolver.candidates = append([]core.Release{verified}, h.resolver.candidates...)
	h.resolver.mu.Unlock()
	ps, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
	if err != nil || ps.Resolution.Release.ID != verified.ID {
		t.Fatalf("attendu le fichier vérifié: %v %+v", err, ps)
	}
}

func TestVerifiedFileReplacesRememberedAddonChoice(t *testing.T) {
	verified := core.Release{ID: core.VerifiedFileIDPrefix + "x", GUID: core.VerifiedFileIDPrefix + "x", Name: "Alko", Size: 100_000, Protocol: core.ProtocolHTTP, StreamDebrid: "alldebrid", StreamURL: "https://alldebrid.com/f/abc"}
	if !core.IsVerifiedFile(verified) {
		t.Fatal("fixture is not a verified file")
	}
	addon := core.Release{ID: "A", Protocol: core.ProtocolHTTP}
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute), mkv(verified.ID, 100_000, time.Minute)}, []string{"A"})
	h.resolver.candidates[0].Protocol, h.resolver.candidates[0].StreamURL = core.ProtocolHTTP, "https://addon.example/stream"
	if !h.m.verifiedFileOutranks(verified, addon) || h.m.verifiedFileOutranks(addon, verified) || h.m.verifiedFileOutranks(verified, verified) {
		t.Fatal("verifiedFileOutranks")
	}
	for _, mode := range []string{"off", "tie"} {
		cfg := h.cfg
		cfg.Resolver.VerifiedFiles = mode
		h.m.SetConfig(cfg)
		if h.m.verifiedFileOutranks(verified, addon) {
			t.Fatalf("mode %q must keep the remembered choice", mode)
		}
	}
	cfg := h.cfg
	cfg.Resolver.VerifiedFiles = "first"
	h.m.SetConfig(cfg)

	// The addon stream was played and remembered; the verified file appears
	// later. A stale picked version falls back: the file leads.
	caps := defaultCaps("c1")
	first, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
	if err != nil || first.Resolution.Release.ID != "A" {
		t.Fatalf("first play: %v %+v", err, first)
	}
	h.m.waitResumeSaves()
	h.m.Stop(first.ID)
	h.resolver.mu.Lock()
	h.resolver.candidates = append([]core.Release{verified}, h.resolver.candidates...)
	h.resolver.mu.Unlock()
	ps, err := h.m.PrepareVariant(context.Background(), owner, movieKey, caps, "gone")
	if err != nil {
		t.Fatal(err)
	}
	if ps.Resolution.Release.ID != verified.ID {
		t.Fatalf("played %s, want the verified file", ps.Resolution.Release.ID)
	}
}
