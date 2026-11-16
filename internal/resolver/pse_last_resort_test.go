package resolver

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

func TestLastResortOnlyOpensAfterPrimaryFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "primary works", true: "primary fails"}[fail], func(t *testing.T) {
			a := newFakeAddon(t)
			a.fail["primary"] = fail
			primary, backup := a.release("primary", "AAA"), a.release("backup", "BBB")
			primary.IndexerID = "primary"
			backup.IndexerID = "backup"
			cfg := testCfg()
			cfg.Resolver.HealthRace = 3
			cfg.Indexers = []config.Indexer{{ID: "primary", Kind: config.IndexerKindStremio, Enabled: true, Priority: 20}, {ID: "backup", Kind: config.IndexerKindStremio, Enabled: true, Priority: 0, PlaybackLastResort: true}}
			inner := &streamingFake{fakeIndexer{releases: []core.Release{backup, primary}}}
			s := New(debrid.NewIndexer(inner, debrid.NewService(config.Debrid{}, debrid.Options{})), fakeHealth{}, nil, cfg, Options{})
			defer s.Close()
			ctx := core.WithIndexerPurpose(context.Background(), core.IndexerPurposePlayback)
			res, err := s.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
			if err != nil {
				t.Fatal(err)
			}
			want := "primary"
			if fail {
				want = "backup"
			}
			if res.Release.ID != want {
				t.Fatalf("winner %s want %s", res.Release.ID, want)
			}
			links := a.links()
			if !fail && slices.Contains(links, "backup") {
				t.Fatalf("backup opened: %v", links)
			}
			if fail && (len(links) != 2 || links[0] != "primary" || links[1] != "backup") {
				t.Fatalf("wrong order: %v", links)
			}
			// Discovery/indexation retains the backup irrespective of playback phase.
			if !s.sourceInPlaybackPhase(core.WithIndexerPurpose(context.Background(), core.IndexerPurposeAvailability), core.ResolveRequest{}, backup, "") {
				t.Fatal("backup excluded from indexing")
			}
		})
	}
}
