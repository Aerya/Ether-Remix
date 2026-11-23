package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/vault"
)

type fakePrewarmVault struct {
	vault.Store
	records map[vault.Key]*vault.Record
}

func (v *fakePrewarmVault) Get(_ context.Context, k vault.Key) (*vault.Record, error) {
	if r, ok := v.records[k]; ok {
		return r, nil
	}
	return nil, core.ErrNotFound
}

func TestPrewarmFailsOverWhenProbedReleaseLacksViewerAudio(t *testing.T) {
	for _, mode := range []string{"ProbeMetadata", "Prewarm"} {
		t.Run(mode, func(t *testing.T) {
			rels := []*fakeRelease{
				mkv("rel-1", 20<<20, time.Minute),
				mkv("rel-2", 20<<20, time.Minute),
				mkv("rel-3", 20<<20, time.Minute),
			}
			rels[0].AudioLanguages = []string{"kor", "spa"}
			rels[1].AudioLanguages = []string{"kor", "por"}
			rels[2].AudioLanguages = []string{"kor", "eng"}

			h := newHarness(t, rels, []string{"rel-1", "rel-2", "rel-3"}, withNativeProbe(func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
				video := core.StreamInfo{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}
				info := &core.MediaInfo{Container: "mkv", Duration: time.Minute, Size: src.Size()}
				switch src.ReleaseID() {
				case "rel-1":
					info.Streams = []core.StreamInfo{
						video,
						{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "kor"},
						{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "spa"},
					}
				case "rel-2":
					info.Streams = []core.StreamInfo{
						video,
						{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "kor"},
						{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "por"},
					}
				case "rel-3":
					info.Streams = []core.StreamInfo{
						video,
						{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "kor", Default: true},
						{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
						{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng"},
						{Index: 4, Type: "subtitle", Codec: "subrip", Language: "kor"},
					}
				}
				return info, nil
			}, nil))
			h.catalog.titles[epKey].Language = "ko"
			h.m.deps.Vault = &fakePrewarmVault{records: map[vault.Key]*vault.Record{
				"nzb:rel-2": {Key: "nzb:rel-2", XML: []byte("nzb-rel-2")},
				"nzb:rel-3": {Key: "nzb:rel-3", XML: []byte("nzb-rel-3")},
			}}
			for i := range h.resolver.candidates {
				h.resolver.candidates[i].ContentID = "nzb:" + h.resolver.candidates[i].ID
			}
			user := &core.User{ID: 1, Username: "owner", IsOwner: true, AudioLanguages: []string{"en"}}
			caps := defaultCaps("shield")

			if mode == "ProbeMetadata" {
				if err := h.m.ProbeMetadata(context.Background(), user, epKey, caps); err != nil {
					t.Fatalf("ProbeMetadata: %v", err)
				}
			} else {
				if !h.m.Prewarm(context.Background(), user, epKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail, PlayIntent: true}) {
					t.Fatal("Prewarm skipped")
				}
				waitWarm(t, h, user.ID, caps.ClientID, epKey)
			}

			info, ok := h.m.WarmProbeInfo(user, caps, epKey)
			if !ok || info.Estimated || len(info.Streams) != 5 {
				t.Fatalf("WarmProbeInfo = (%+v, %v), want rel-3's 5 streams", info, ok)
			}
			hasEngAudio, subCount := false, 0
			for _, st := range info.Streams {
				if st.Type == "audio" && st.Language == "eng" {
					hasEngAudio = true
				}
				if st.Type == "subtitle" {
					subCount++
				}
			}
			if !hasEngAudio || subCount != 2 {
				t.Fatalf("published streams = %+v, want eng audio and 2 subtitles from rel-3", info.Streams)
			}
			if h.resolver.isBad("rel-1") || h.resolver.isBad("rel-2") {
				t.Fatalf("audio-mismatched candidates marked bad: %+v", h.resolver.bad)
			}

			opensBeforePrepare := h.engine.openCount()
			ps, err := h.m.Prepare(context.Background(), user, epKey, caps)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if ps.Resolution == nil || ps.Resolution.Release.ID != "rel-3" {
				t.Fatalf("Prepare selected %+v, want rel-3", ps.Resolution)
			}
			// Prepare either reuses the warm source or opens rel-3 once; it
			// never fails over again.
			if got := h.engine.openCount(); got > opensBeforePrepare+1 {
				t.Fatalf("Prepare reopened/failed over unexpectedly: opens %d -> %d", opensBeforePrepare, got)
			}
		})
	}
}

func TestPrewarmFallsBackWhenNoCandidateHasViewerAudio(t *testing.T) {
	rels := []*fakeRelease{
		mkv("rel-1", 20<<20, time.Minute),
		mkv("rel-2", 20<<20, time.Minute),
	}
	rels[0].AudioLanguages = []string{"kor", "spa"}
	rels[1].AudioLanguages = []string{"kor", "por"}

	h := newHarness(t, rels, []string{"rel-1", "rel-2"}, withNativeProbe(func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
		video := core.StreamInfo{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}
		info := &core.MediaInfo{Container: "mkv", Duration: time.Minute, Size: src.Size()}
		if src.ReleaseID() == "rel-1" {
			info.Streams = []core.StreamInfo{
				video,
				{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "kor"},
				{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "spa"},
				{Index: 3, Type: "subtitle", Codec: "subrip", Language: "spa"},
			}
		} else {
			info.Streams = []core.StreamInfo{
				video,
				{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "kor"},
				{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "por"},
			}
		}
		return info, nil
	}, nil))
	h.catalog.titles[epKey].Language = "ko"
	user := &core.User{ID: 1, Username: "owner", IsOwner: true, AudioLanguages: []string{"en"}}
	caps := defaultCaps("shield")

	if err := h.m.ProbeMetadata(context.Background(), user, epKey, caps); err != nil {
		t.Fatalf("ProbeMetadata: %v", err)
	}
	info, ok := h.m.WarmProbeInfo(user, caps, epKey)
	if !ok || info.Estimated || len(info.Streams) != 4 {
		t.Fatalf("WarmProbeInfo = (%+v, %v), want fallback rel-1's 4 streams", info, ok)
	}
	ps, err := h.m.Prepare(context.Background(), user, epKey, caps)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if ps.Resolution == nil || ps.Resolution.Release.ID != "rel-1" {
		t.Fatalf("Prepare selected %+v, want fallback rel-1", ps.Resolution)
	}
}

func TestPrewarmFailsOverOnStrictProfileRequiredAudio(t *testing.T) {
	t.Run("accepts-later-matching-candidate", func(t *testing.T) {
		jpnOnly := mkv("jpn-untagged", 10_000, time.Minute)
		jpnOnly.AudioLanguages = []string{"jpn"}

		dualEng := mkv("dual-eng", 10_000, time.Minute)
		dualEng.AudioLanguages = []string{"jpn", "eng"}

		h := newHarness(t, []*fakeRelease{jpnOnly, dualEng}, []string{"jpn-untagged", "dual-eng"})
		h.catalog.titles[movieKey].Language = "ja"
		profile := englishRequiredProfile()
		h.m.deps.SelectProfile = func(core.ClientCaps, *core.User, string) core.QualityProfile { return profile }
		h.resolver.candidates = []core.Release{
			{ID: "jpn-untagged", ContentID: "nzb:jpn-untagged", Name: "[SubsPlease] One Piece - 001 (1080p).mkv", Info: releaseparse.Parse("[SubsPlease] One Piece - 001 (1080p).mkv")},
			{ID: "dual-eng", ContentID: "nzb:dual-eng", Name: "[Group] One Piece - 001 [Dual Audio].mkv", Info: releaseparse.Parse("[Group] One Piece - 001 [Dual Audio].mkv")},
		}
		h.m.deps.Vault = &fakePrewarmVault{records: map[vault.Key]*vault.Record{
			"nzb:dual-eng": {Key: "nzb:dual-eng", XML: []byte("nzb-dual-eng")},
		}}

		caps := defaultCaps("shield")
		if err := h.m.ProbeMetadata(context.Background(), owner, movieKey, caps); err != nil {
			t.Fatalf("ProbeMetadata: %v", err)
		}
		info, ok := h.m.WarmProbeInfo(owner, caps, movieKey)
		if !ok || info.Estimated {
			t.Fatalf("WarmProbeInfo = (%+v, %v), want dual-eng", info, ok)
		}
		hasEng := false
		for _, st := range info.Streams {
			if st.Type == "audio" && st.Language == "eng" {
				hasEng = true
			}
		}
		if !hasEng {
			t.Fatalf("WarmProbeInfo missed eng audio: %+v", info.Streams)
		}
		ps, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		if ps.Resolution == nil || ps.Resolution.Release.ID != "dual-eng" {
			t.Fatalf("Prepare selected %+v, want dual-eng", ps.Resolution)
		}
	})

	t.Run("rejects-all-and-bounds-unknown-probes", func(t *testing.T) {
		var fakes []*fakeRelease
		var ids []string
		var cands []core.Release
		for i := 1; i <= 6; i++ {
			id := fmt.Sprintf("u%d", i)
			fr := mkv(id, 10_000, time.Minute)
			if i <= 4 {
				fr.AudioLanguages = []string{"jpn"}
			} else {
				fr.AudioLanguages = []string{"jpn", "eng"}
			}
			fakes = append(fakes, fr)
			ids = append(ids, id)
			name := fmt.Sprintf("[Group%d] One Piece - 001 [Dual Audio].mkv", i)
			cands = append(cands, core.Release{ID: id, Name: name, Info: releaseparse.Parse(name)})
		}

		h := newHarness(t, fakes, ids)
		h.m.opts.MaxAttempts = 8
		h.catalog.titles[movieKey].Language = "ja"
		profile := englishRequiredProfile()
		h.m.deps.SelectProfile = func(core.ClientCaps, *core.User, string) core.QualityProfile { return profile }
		h.resolver.candidates = cands
		recs := map[vault.Key]*vault.Record{}
		for i := range h.resolver.candidates {
			cid := "nzb:" + h.resolver.candidates[i].ID
			h.resolver.candidates[i].ContentID = cid
			recs[vault.Key(cid)] = &vault.Record{Key: vault.Key(cid), XML: []byte("nzb-" + h.resolver.candidates[i].ID)}
		}
		h.m.deps.Vault = &fakePrewarmVault{records: recs}

		caps := defaultCaps("shield")
		err := h.m.ProbeMetadata(context.Background(), owner, movieKey, caps)
		if !errors.Is(err, core.ErrPrepareSlow) {
			t.Fatalf("ProbeMetadata error = %v, want ErrPrepareSlow", err)
		}
		if _, ok := h.m.WarmProbeInfo(owner, caps, movieKey); ok {
			t.Fatal("WarmProbeInfo published a rejected release")
		}
		if got := h.engine.openCount(); got != maxUnknownAudioProbes {
			t.Fatalf("opened %d unknown-language sources, want at most %d", got, maxUnknownAudioProbes)
		}
	})
}

func TestPrewarmFailsOverWhenFirstCandidateFailsOpen(t *testing.T) {
	bad := mkv("rel-open-fail", 20<<20, time.Minute)
	bad.OpenErr = errors.New("temporary open failure")
	good := mkv("rel-good", 20<<20, time.Minute)
	good.AudioLanguages = []string{"eng"}

	h := newHarness(t, []*fakeRelease{bad, good}, []string{"rel-open-fail", "rel-good"}, withNativeProbe(func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
		return &core.MediaInfo{
			Container: "mkv",
			Duration:  time.Minute,
			Size:      src.Size(),
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
				{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
				{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng"},
			},
		}, nil
	}, nil))
	h.resolver.candidates[1].ContentID = "nzb:rel-good"
	h.m.deps.Vault = &fakePrewarmVault{records: map[vault.Key]*vault.Record{
		"nzb:rel-good": {Key: "nzb:rel-good", XML: []byte("nzb-rel-good")},
	}}
	caps := defaultCaps("shield")
	if err := h.m.ProbeMetadata(context.Background(), owner, movieKey, caps); err != nil {
		t.Fatalf("ProbeMetadata: %v", err)
	}
	info, ok := h.m.WarmProbeInfo(owner, caps, movieKey)
	if !ok || info.Estimated || len(info.Streams) != 3 {
		t.Fatalf("WarmProbeInfo = (%+v, %v), want rel-good's 3 streams", info, ok)
	}
	ps, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if ps.Resolution == nil || ps.Resolution.Release.ID != "rel-good" {
		t.Fatalf("Prepare selected %+v, want rel-good", ps.Resolution)
	}
}

func TestPrewarmKeepsResumedSourceWithoutExtraResolve(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dir := t.TempDir()

	makeRels := func() []*fakeRelease {
		r1 := mkv("rel-1", 20<<20, time.Minute)
		r1.AudioLanguages = []string{"kor", "spa"}
		r2 := mkv("rel-2", 20<<20, time.Minute)
		r2.AudioLanguages = []string{"kor", "eng"}
		return []*fakeRelease{r1, r2}
	}
	probeFn := withNativeProbe(func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
		video := core.StreamInfo{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080}
		info := &core.MediaInfo{Container: "mkv", Duration: time.Minute, Size: src.Size()}
		if src.ReleaseID() == "rel-1" {
			info.Streams = []core.StreamInfo{
				video,
				{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "kor"},
				{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "spa"},
				{Index: 3, Type: "subtitle", Codec: "subrip", Language: "spa"},
			}
		} else {
			info.Streams = []core.StreamInfo{
				video,
				{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "kor"},
				{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
			}
		}
		return info, nil
	}, nil)
	shared := func(h *harness, o *Options) {
		h.db = db
		h.dataDir = dir
		o.DataDir = dir
	}

	user := &core.User{ID: 1, Username: "owner", IsOwner: true, AudioLanguages: []string{"en"}}
	caps := defaultCaps("shield")

	h1 := newHarness(t, makeRels(), []string{"rel-1"}, shared, probeFn)
	h1.catalog.titles[epKey].Language = "ko"
	ps, err := h1.m.Prepare(context.Background(), user, epKey, caps)
	if err != nil {
		t.Fatalf("seed Prepare: %v", err)
	}
	h1.m.waitResumeSaves()
	h1.m.Stop(ps.ID)
	if err := h1.m.Close(); err != nil {
		t.Fatal(err)
	}

	// Drop the persisted probe so ProbeMetadata takes prewarm's fromResume path.
	if _, err := db.Exec(`DELETE FROM playback_probe`); err != nil {
		t.Fatal(err)
	}

	h2 := newHarness(t, makeRels(), []string{"rel-2", "rel-1"}, shared, probeFn)
	h2.catalog.titles[epKey].Language = "ko"
	resolvesBefore := h2.resolver.callCount()

	if err := h2.m.ProbeMetadata(context.Background(), user, epKey, caps); err != nil {
		t.Fatalf("ProbeMetadata: %v", err)
	}
	if got := h2.resolver.callCount(); got != resolvesBefore {
		t.Fatalf("resumed source triggered %d resolver calls, want 0", got-resolvesBefore)
	}
	info, ok := h2.m.WarmProbeInfo(user, caps, epKey)
	if !ok || info.Estimated || len(info.Streams) != 4 {
		t.Fatalf("WarmProbeInfo = (%+v, %v), want rel-1's 4 streams", info, ok)
	}
	hasSpaSub := false
	for _, st := range info.Streams {
		if st.Type == "subtitle" && st.Language == "spa" {
			hasSpaSub = true
		}
	}
	if !hasSpaSub {
		t.Fatalf("WarmProbeInfo = %+v, want spa subtitle from resumed rel-1", info.Streams)
	}
}
