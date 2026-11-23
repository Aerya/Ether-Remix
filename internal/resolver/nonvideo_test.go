package resolver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

type countingFetchIndexer struct {
	*fakeIndexer
	fetches atomic.Int64
}

func (c *countingFetchIndexer) FetchNZB(ctx context.Context, r core.Release) ([]byte, error) {
	c.fetches.Add(1)
	return c.fakeIndexer.FetchNZB(ctx, r)
}

func TestPreProbeBaitAndNonVideoRejection(t *testing.T) {
	now := time.Now().Add(-24 * time.Hour)
	mk := func(id, name, fileName string, sizeMB int64) core.Release {
		r := core.Release{
			ID:       id,
			GUID:     id,
			Indexer:  "fake",
			Name:     name,
			FileName: fileName,
			NZBURL:   "http://fake/" + id,
			Size:     sizeMB << 20,
			PubDate:  now,
			Grabs:    25,
			Info:     releaseparse.Parse(name),
		}
		return r
	}

	rejected := []core.Release{
		mk("bait-lnk", "The.Signal.1999.2160p.WEB-DL.DDP5.1.H.265-GRP.lnk", "", 8000),
		mk("bait-mkv-lnk", "The.Signal.1999.2160p.BluRay.Remux.HEVC.mkv.lnk", "", 22000),
		mk("bait-mp4-exe", "The.Signal.1999.1080p.WEB-DL.x264.mp4.exe", "", 4500),
		mk("bait-scr", "The.Signal.1999.1080p.BluRay.x264-GRP.scr", "", 4500),
		mk("bait-bat", "The.Signal.1999.1080p.WEB-DL.Play.bat", "", 4200),
		mk("bait-msi", "The.Signal.1999.1080p.CodecSetup.msi", "", 4100),
		mk("bait-filehint", "The.Signal.1999.2160p.WEB-DL.HEVC-GRP", "The.Signal.1999.mkv.exe", 9000),
		mk("ost-flac", "The.Signal.1999.OST.WEB.FLAC-GRP", "", 600),
		mk("ost-mp3", "The Signal (1999) Original Soundtrack [MP3 320kbps]", "", 250),
		mk("ebook-epub", "The.Signal.1999.eBook.EPUB-GRP", "", 50),
		mk("audiobook-m4b", "The Signal (1999) Unabridged Audiobook.m4b", "", 900),
	}
	valid := mk("real-hd", "The.Signal.1999.1080p.BluRay.x264-SPARKS", "", 8500)

	all := append(append([]core.Release(nil), rejected...), valid)
	idx := &countingFetchIndexer{fakeIndexer: &fakeIndexer{releases: all}}
	var healthChecks atomic.Int64
	svc := New(idx, fakeHealth{checks: &healthChecks}, nil, testCfg(), Options{})
	t.Cleanup(func() { _ = svc.Close() })

	res, err := svc.Resolve(context.Background(), core.ResolveRequest{
		Title:  matrixTitle(),
		Client: appleTVCaps(),
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if res.Release.ID != "real-hd" {
		t.Fatalf("winner = %q, want real-hd", res.Release.ID)
	}
	if got := idx.fetches.Load(); got != 1 {
		t.Fatalf("FetchNZB calls = %d, want 1 (bait/non-video must not be fetched or probed)", got)
	}
	if got := healthChecks.Load(); got != 1 {
		t.Fatalf("HealthCheck calls = %d, want 1", got)
	}

	traces := svc.Traces()
	if len(traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(traces))
	}
	byID := map[string]CandidateTrace{}
	for _, c := range traces[0].Candidates {
		byID[c.ReleaseID] = c
	}
	for _, bad := range rejected {
		ct, ok := byID[bad.ID]
		if !ok {
			t.Fatalf("missing trace candidate for %s", bad.ID)
		}
		if !ct.Rejected || ct.Reason != ReasonNotAVideoFile || ct.HealthChecked {
			t.Errorf("%s (%q): Rejected=%v Reason=%q HealthChecked=%v; want Rejected=true Reason=%q HealthChecked=false",
				bad.ID, bad.Name, ct.Rejected, ct.Reason, ct.HealthChecked, ReasonNotAVideoFile)
		}
	}

	// Legitimate releases with screener tags, video+OST bonus tags, MP3 audio
	// codecs, or titles literally named "Soundtrack" must not be flagged.
	legit := []struct {
		name   string
		titles []string
	}{
		{name: "Movie.2024.SCR.x264-GRP", titles: []string{"Movie"}},
		{name: "Movie.2024.1080p.BluRay.x265.Plus.OST-GRP", titles: []string{"Movie"}},
		{name: "Obscure.Film.1974.VHSRip.XviD.MP3-GRP", titles: []string{"Obscure Film"}},
		{name: "Soundtrack.2011.1080p.WEB-DL.x264-GRP", titles: []string{"Soundtrack"}},
		{name: "Soundtrack (2011)", titles: []string{"Soundtrack"}},
	}
	for _, tc := range legit {
		if releaseparse.IsNonVideoRelease(tc.name, "", tc.titles...) {
			t.Errorf("IsNonVideoRelease(%q) = true, want false", tc.name)
		}
	}
}
