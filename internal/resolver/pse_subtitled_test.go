package resolver

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Prepared subtitles break a tie, never a quality, language or format gap.
func TestSubtitledRanksFirstOnlyAmongEquals(t *testing.T) {
	profile := core.QualityProfile{QualityOrder: []core.QualityItem{{Qualities: []string{"webdl-2160"}}, {Qualities: []string{"webdl-1080"}}}}
	torrent := func(name string, cf, seeders int, size int64, subtitled bool) candidate {
		c := arrCand(profile, name, cf, func(r *core.Release) { r.Protocol, r.Seeders, r.Size = core.ProtocolTorrent, seeders, size<<30 })
		c.subtitled = subtitled
		return c
	}
	ready := torrent("Film.2026.1080p.WEB-DL.x264-GRP", 10, 10, 2, true)
	if bigger := torrent("Film.2026.1080p.WEB-DL.x264-OTHER", 10, 1000, 8, false); !compareCandidates(ready, bigger) || compareCandidates(bigger, ready) {
		t.Error("a prepared release beats more seeders and a larger file")
	}
	if better := torrent("Film.2026.2160p.WEB-DL.x265-GRP", 10, 10, 2, false); !compareCandidates(better, ready) {
		t.Error("a better quality still ranks first")
	}
	if scored := torrent("Film.2026.1080p.WEB-DL.x264-OTHER", 20, 10, 2, false); !compareCandidates(scored, ready) {
		t.Error("a higher custom format score still ranks first")
	}
	foreign := ready
	foreign.langOK = false
	if plain := torrent("Film.2026.1080p.WEB-DL.x264-OTHER", 10, 10, 2, false); !compareCandidates(plain, foreign) {
		t.Error("an accepted audio language still ranks first")
	}
}

func TestMarkSubtitledAsksOncePerPass(t *testing.T) {
	s := &Service{}
	calls := 0
	s.SetSubtitled(func(_ context.Context, _ *core.Title, rels []core.Release) []bool {
		calls++
		out := make([]bool, len(rels))
		for i, r := range rels {
			out[i] = r.Name == "ready"
		}
		return out
	})
	cands := []candidate{{rel: core.Release{Name: "plain"}, trace: 0}, {rel: core.Release{Name: "ready"}, trace: 1}}
	trace := &Trace{Candidates: make([]CandidateTrace, 2)}
	s.markSubtitled(context.Background(), &core.Title{}, cands, trace)
	if calls != 1 || cands[0].subtitled || !cands[1].subtitled || trace.Candidates[1].CompatibilityReason == "" {
		t.Fatalf("calls=%d cands=%+v", calls, cands)
	}
	s.SetSubtitled(nil)
	cands[1].subtitled = false
	s.markSubtitled(context.Background(), &core.Title{}, cands, nil)
	if cands[1].subtitled {
		t.Fatal("no hook marks nothing")
	}
}
