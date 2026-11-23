package resolver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/vault"
)

type replayVault struct {
	vault.Store
	entry      vault.Entry
	entries    []vault.Entry
	verifyErr  error
	chosenKeys []vault.Key
}

func (v *replayVault) allEntries() []vault.Entry {
	if len(v.entries) > 0 {
		return v.entries
	}
	return []vault.Entry{v.entry}
}

func (v *replayVault) Search(context.Context, core.SearchRequest) ([]vault.Entry, error) {
	return append([]vault.Entry(nil), v.allEntries()...), nil
}
func (v *replayVault) Entry(_ context.Context, k vault.Key) (vault.Entry, error) {
	for _, e := range v.allEntries() {
		if e.Key == k {
			return e, nil
		}
	}
	return v.entry, nil
}
func (v *replayVault) Get(context.Context, vault.Key) (*vault.Record, error) {
	return &vault.Record{NZB: &nzb.NZB{Files: []*nzb.File{{Segments: []nzb.Segment{{MessageID: "sample"}}}}}}, nil
}
func (v *replayVault) MarkHealth(_ context.Context, k vault.Key, h vault.Health) error {
	for i := range v.entries {
		if v.entries[i].Key == k {
			v.entries[i].Health = h
		}
	}
	if v.entry.Key == k || len(v.entries) == 0 {
		v.entry.Health = h
	}
	return nil
}
func (v *replayVault) VerifyArticles(context.Context, []string, []string) (bool, error) {
	if v.verifyErr != nil {
		return false, v.verifyErr
	}
	return false, nil
}
func (v *replayVault) MarkChosen(_ context.Context, k vault.Key, _ string) error {
	v.chosenKeys = append(v.chosenKeys, k)
	return nil
}
func (v *replayVault) Touch(context.Context, vault.Key, bool) error { return nil }
func (v *replayVault) ByAlias(context.Context, string, string) (vault.Key, bool, error) {
	return "", false, nil
}

func TestLibraryReplayCutoffFingerprintAndDeadFallback(t *testing.T) {
	for _, scenario := range []string{"replay", "lower quality", "changed profile", "dead"} {
		t.Run(scenario, func(t *testing.T) {
			req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}
			p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
			v := &replayVault{entry: vault.Entry{Key: "sample", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, Health: vault.HealthOK, HealthAt: time.Now()}}
			idx := &fakeIndexer{releases: []core.Release{rel("live", "The.Signal.1999.1080p.BluRay.x264.Proper-GRP", 8100)}}
			s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
			v.entry.ProfileFP = ProfileFingerprint(p, wantedAudio(p, req.User, s.catalogLanguage()))
			switch scenario {
			case "changed profile":
				v.entry.ProfileFP = "older"
			case "lower quality":
				v.entry.Name = "The.Signal.1999.720p.WEB-DL.H264-GRP"
			case "dead":
				v.entry.HealthAt = time.Time{}
			}
			res, err := s.resolve(context.Background(), req, p)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "replay" {
				if res.Release.ID != "vault-sample" || idx.searches.Load() > 0 {
					t.Fatalf("expected library replay, got %s", res.Release.ID)
				}
			} else if res.Release.ID != "live" {
				t.Fatalf("expected live result, got %s", res.Release.ID)
			}
			if scenario == "dead" && v.entry.Health != vault.HealthDead {
				t.Fatal("dead sample was not recorded")
			}
		})
	}
}

func TestVaultPreferReplayAndAlternates(t *testing.T) {
	for _, preferredOnly := range []bool{false, true} {
		name := "prefer"
		if preferredOnly {
			name = "prefer-only"
		}
		t.Run(name, func(t *testing.T) {
			req := core.ResolveRequest{
				Title:         matrixTitle(),
				Client:        webCaps(),
				Prefer:        "vault-lower",
				PreferredOnly: preferredOnly,
			}
			p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
			v := &replayVault{}
			idx := &fakeIndexer{releases: []core.Release{rel("live", "The.Signal.1999.1080p.BluRay.x264.Proper-GRP", 8100)}}
			s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
			defer s.Close()
			fp := ProfileFingerprint(p, wantedAudio(p, req.User, s.catalogLanguage()))
			v.entries = []vault.Entry{
				{Key: "qualifying", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, ProfileFP: fp, Health: vault.HealthOK, HealthAt: time.Now()},
				{Key: "stale", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.BluRay.H264-OLD", Size: 8200 << 20, ProfileFP: "older", Health: vault.HealthOK, HealthAt: time.Now()},
				{Key: "subcutoff", Kind: vault.KindUsenet, Name: "The.Signal.1999.720p.WEB-DL.H264-SUB", Size: 4100 << 20, ProfileFP: fp, Health: vault.HealthOK, HealthAt: time.Now()},
				{Key: "lower", Kind: vault.KindUsenet, Name: "The.Signal.1999.720p.WEB-DL.H264-ALT", Size: 4000 << 20, ProfileFP: "older", Health: vault.HealthOK, HealthAt: time.Now()},
			}

			res, err := s.resolve(context.Background(), req, p)
			if err != nil {
				t.Fatal(err)
			}
			if res.Release.ID != "vault-lower" {
				t.Fatalf("got release %s, want vault-lower", res.Release.ID)
			}
			if idx.searches.Load() != 0 {
				t.Fatalf("expected no indexer search, got %d", idx.searches.Load())
			}
			if preferredOnly {
				if len(res.Alternates) != 0 {
					t.Fatalf("PreferredOnly alternates = %+v, want none", res.Alternates)
				}
			} else {
				if len(res.Alternates) != 1 || res.Alternates[0].ID != "vault-qualifying" {
					t.Fatalf("alternates = %+v, want only [vault-qualifying]", res.Alternates)
				}
			}
			if len(v.chosenKeys) != 0 {
				t.Fatalf("explicit Prefer must not overwrite profile_fp via MarkChosen: %v", v.chosenKeys)
			}
		})
	}
}

func TestVaultPreferredOnlyFailuresSkipLiveSearchAndPreserveTransientError(t *testing.T) {
	t.Run("dead", func(t *testing.T) {
		req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps(), Prefer: "vault-sample", PreferredOnly: true}
		p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
		v := &replayVault{entry: vault.Entry{Key: "sample", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, Health: vault.HealthOK}}
		idx := &fakeIndexer{releases: []core.Release{rel("live", "The.Signal.1999.1080p.BluRay.x264.Proper-GRP", 8100)}}
		s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
		defer s.Close()

		_, err := s.resolve(context.Background(), req, p)
		if !errors.Is(err, core.ErrNotAvailable) {
			t.Fatalf("err = %v, want ErrNotAvailable", err)
		}
		if v.entry.Health != vault.HealthDead {
			t.Fatalf("health = %v, want HealthDead", v.entry.Health)
		}
		if idx.searches.Load() != 0 {
			t.Fatalf("dead PreferredOnly vault hit should not trigger live search, got %d", idx.searches.Load())
		}
	})

	t.Run("transient", func(t *testing.T) {
		nntpErr := errors.New("nntp connection refused")
		req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps(), Prefer: "vault-sample", PreferredOnly: true}
		p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
		v := &replayVault{
			entry:     vault.Entry{Key: "sample", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, Health: vault.HealthOK},
			verifyErr: nntpErr,
		}
		idx := &fakeIndexer{releases: []core.Release{rel("live", "The.Signal.1999.1080p.BluRay.x264.Proper-GRP", 8100)}}
		s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
		defer s.Close()

		_, err := s.resolve(context.Background(), req, p)
		if !errors.Is(err, nntpErr) {
			t.Fatalf("err = %v, want %v", err, nntpErr)
		}
		if errors.Is(err, core.ErrNotAvailable) {
			t.Fatalf("transient verify error must not collapse to ErrNotAvailable: %v", err)
		}
		if idx.searches.Load() != 0 {
			t.Fatalf("transient PreferredOnly vault error should not trigger live search, got %d", idx.searches.Load())
		}
	})
}

func TestVaultPreferDeadSeasonPackRemovedFromFallback(t *testing.T) {
	ep := &core.Title{
		Key: core.EpisodeKey(42, 1, 3), Type: core.TypeEpisode,
		Title: "Episode 3", ShowTitle: "The Signal", Season: 1, Episode: 3, Year: 2020,
		IDs: core.IDs{TMDB: 42},
	}
	packTarget := core.TargetReleaseID("vault-pack", core.ReleaseTarget{Season: 1, Episode: 3})
	req := core.ResolveRequest{Title: ep, Client: webCaps(), Prefer: packTarget}
	p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
	v := &replayVault{entry: vault.Entry{
		Key: "pack", Kind: vault.KindUsenet,
		Name: "The.Signal.S01.1080p.WEB-DL.H264-GRP", Size: 16000 << 20, Health: vault.HealthOK,
	}}
	idx := &fakeIndexer{releases: []core.Release{rel("live-ep", "The.Signal.S01E03.1080p.WEB-DL.H264-GRP", 2000)}}
	s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
	defer s.Close()

	var tr Trace
	sreq := s.searchRequest(context.Background(), ep)
	remaining, hit, retry, err := s.libraryCandidates(context.Background(), req, sreq, p, "fp", &tr, false)
	if err != nil || hit != nil || !retry {
		t.Fatalf("expected dead pack retry: hit=%v retry=%v err=%v", hit, retry, err)
	}
	if len(remaining) != 0 {
		t.Fatalf("dead season pack was not removed from remaining vault releases: %+v", remaining)
	}
}

func TestVaultPreferMissingFallsBackToLiveSearch(t *testing.T) {
	req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps(), Prefer: "missing-id"}
	p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
	v := &replayVault{entry: vault.Entry{Key: "sample", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, Health: vault.HealthOK, HealthAt: time.Now()}}
	idx := &fakeIndexer{releases: []core.Release{rel("live", "The.Signal.1999.1080p.BluRay.x264.Proper-GRP", 8100)}}
	s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
	defer s.Close()
	v.entry.ProfileFP = ProfileFingerprint(p, wantedAudio(p, req.User, s.catalogLanguage()))

	res, err := s.resolve(context.Background(), req, p)
	if err != nil {
		t.Fatal(err)
	}
	if idx.searches.Load() == 0 || res.Release.ID != "live" {
		t.Fatalf("expected live fallback, searches=%d release=%s", idx.searches.Load(), res.Release.ID)
	}
}

func TestVaultPreferDeadMarksDeadAndFallsBackToLiveSearch(t *testing.T) {
	req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps(), Prefer: "vault-sample"}
	p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
	v := &replayVault{entry: vault.Entry{Key: "sample", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, Health: vault.HealthOK}}
	idx := &fakeIndexer{releases: []core.Release{rel("live", "The.Signal.1999.1080p.BluRay.x264.Proper-GRP", 8100)}}
	s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
	defer s.Close()
	v.entry.ProfileFP = ProfileFingerprint(p, wantedAudio(p, req.User, s.catalogLanguage()))

	res, err := s.resolve(context.Background(), req, p)
	if err != nil {
		t.Fatal(err)
	}
	if v.entry.Health != vault.HealthDead {
		t.Fatalf("expected dead vault entry to be marked dead, got %v", v.entry.Health)
	}
	if idx.searches.Load() == 0 || res.Release.ID != "live" {
		t.Fatalf("expected live fallback, searches=%d release=%s", idx.searches.Load(), res.Release.ID)
	}
}

func TestVaultPreferredOnlyEmptyPreferDoesNotReturnArbitraryVaultRelease(t *testing.T) {
	req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps(), PreferredOnly: true}
	p := core.QualityProfile{Name: "Library", MaxHeight: 1080, Language: "any"}
	v := &replayVault{entry: vault.Entry{Key: "sample", Kind: vault.KindUsenet, Name: "The.Signal.1999.1080p.WEB-DL.H264-GRP", Size: 8000 << 20, Health: vault.HealthOK, HealthAt: time.Now()}}
	idx := &fakeIndexer{releases: []core.Release{rel("live", "The.Signal.1999.1080p.BluRay.x264.Proper-GRP", 8100)}}
	s := New(idx, fakeHealth{}, nil, testCfg(), Options{Vault: v, Verifier: v})
	defer s.Close()
	fp := ProfileFingerprint(p, wantedAudio(p, req.User, s.catalogLanguage()))
	v.entry.ProfileFP = fp

	var tr Trace
	sreq := s.searchRequest(context.Background(), req.Title)
	_, hit, retry, err := s.libraryCandidates(context.Background(), req, sreq, p, fp, &tr, false)
	if hit != nil || retry || err != nil {
		t.Fatalf("PreferredOnly with empty Prefer must not return vault hit: hit=%+v retry=%v err=%v", hit, retry, err)
	}
	if _, err := s.resolve(context.Background(), req, p); err != nil {
		t.Fatal(err)
	}
	if idx.searches.Load() == 0 {
		t.Fatal("expected live search when PreferredOnly is set with empty Prefer")
	}
}
