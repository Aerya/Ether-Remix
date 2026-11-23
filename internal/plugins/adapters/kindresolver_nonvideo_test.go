package adapters

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/stream"
)

type countingNZBIndexer struct {
	nzbs  map[string][]byte
	grabs atomic.Int32
}

func (i *countingNZBIndexer) Name() string { return "counting-idx" }
func (i *countingNZBIndexer) Search(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	return nil, nil
}
func (i *countingNZBIndexer) FetchNZB(_ context.Context, rel core.Release) ([]byte, error) {
	i.grabs.Add(1)
	if b, ok := i.nzbs[rel.NZBURL]; ok {
		return b, nil
	}
	return nil, core.ErrNotAvailable
}

type simpleMemFetcher struct {
	mu       sync.Mutex
	articles map[string]struct {
		data []byte
		part core.ArticlePart
	}
}

func (m *simpleMemFetcher) Body(ctx context.Context, id string, groups []string, prio core.Priority) ([]byte, error) {
	b, _, err := m.BodyPart(ctx, id, groups, prio)
	return b, err
}

func (m *simpleMemFetcher) BodyPart(_ context.Context, id string, _ []string, _ core.Priority) ([]byte, core.ArticlePart, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.articles[id]
	if !ok {
		return nil, core.ArticlePart{}, core.ErrArticleMissing
	}
	return bytes.Clone(a.data), a.part, nil
}

func (m *simpleMemFetcher) Stat(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.articles[id]
	return ok, nil
}

func buildSingleFileNZB(f *simpleMemFetcher, filename string, data []byte) []byte {
	id := strings.ReplaceAll(filename, " ", "_") + ".1@test"
	f.mu.Lock()
	f.articles[id] = struct {
		data []byte
		part core.ArticlePart
	}{
		data: data,
		part: core.ArticlePart{Name: filename, FileSize: int64(len(data)), Part: 1, Total: 1, Begin: 1, End: int64(len(data))},
	}
	f.mu.Unlock()
	subj := fmt.Sprintf(`[1/1] - "%s" yEnc (1/1)`, filename)
	return []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="p@test" date="1" subject="%s"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file></nzb>`,
		html.EscapeString(subj), len(data)+200, html.EscapeString(id),
	))
}

func TestKindResolverNonVideoReleaseTargetAndVideoInvariance(t *testing.T) {
	// Music/book release sources without explicit file selectors enable AllowNonVideo with MIME.
	musicRel, err := releaseFromSource(core.PlayableSource{
		Pattern: kindstream.PatternRelease,
		MIME:    "audio/flac",
		Release: &core.ReleaseHint{NZBURL: "https://idx.test/music.nzb"},
	}, "Album", "")
	if err != nil {
		t.Fatalf("releaseFromSource(music): %v", err)
	}
	if musicRel.Target == nil || !musicRel.Target.AllowNonVideo || musicRel.Target.MIME != "audio/flac" {
		t.Fatalf("music Target = %+v, want AllowNonVideo=true MIME=audio/flac", musicRel.Target)
	}

	// Book release source with fileHint populates Member + MIME.
	bookRel, err := releaseFromSource(core.PlayableSource{
		Pattern: kindstream.PatternRelease,
		MIME:    "application/epub+zip",
		Release: &core.ReleaseHint{NZBURL: "https://idx.test/book.nzb", FileHint: "Dune.epub"},
	}, "Dune", "")
	if err != nil {
		t.Fatalf("releaseFromSource(book): %v", err)
	}
	if bookRel.Target == nil || !bookRel.Target.AllowNonVideo || bookRel.Target.Member != "Dune.epub" || bookRel.Target.MIME != "application/epub+zip" {
		t.Fatalf("book Target = %+v", bookRel.Target)
	}

	// Video release sources (with or without FileHint) keep Target == nil and stable ID.
	vidRel, err := releaseFromSource(core.PlayableSource{
		Pattern: kindstream.PatternRelease,
		MIME:    "video/x-matroska",
		Release: &core.ReleaseHint{NZBURL: "https://idx.test/movie.nzb", FileHint: "Movie.2024.mkv"},
	}, "Movie", "")
	if err != nil {
		t.Fatalf("releaseFromSource(video): %v", err)
	}
	if vidRel.Target != nil || vidRel.ID != "kind:https://idx.test/movie.nzb" {
		t.Fatalf("video release changed: id=%q target=%+v", vidRel.ID, vidRel.Target)
	}

	// kindDefaultMIME derives fallback MIMEs from MediaBrowser / Plex presentations only for non-video kinds.
	var musicDecl plugins.MediaKindMeta
	musicDecl.Present.MediaBrowser = &core.MediaBrowserPresentation{CollectionType: "music"}
	if got := kindDefaultMIME(musicDecl); got != "audio/*" {
		t.Fatalf("kindDefaultMIME(music) = %q, want audio/*", got)
	}

	var movieDecl plugins.MediaKindMeta
	movieDecl.Present.MediaBrowser = &core.MediaBrowserPresentation{CollectionType: "movies"}
	if got := kindDefaultMIME(movieDecl); got != "" {
		t.Fatalf("kindDefaultMIME(movies) = %q, want empty", got)
	}
}

func TestKindResolverUsenetLooseAudioAndFailFastGrabs(t *testing.T) {
	ctx := context.Background()
	fetcher := &simpleMemFetcher{articles: map[string]struct {
		data []byte
		part core.ArticlePart
	}{}}
	eng := stream.New(fetcher, nil, stream.Options{}, nil)
	t.Cleanup(func() { _ = eng.Close() })

	flacData := append([]byte("fLaC"), bytes.Repeat([]byte("track-audio-data"), 2000)...)
	goodNZB := buildSingleFileNZB(fetcher, "01 - Track.flac", flacData)
	badNZB := buildSingleFileNZB(fetcher, "readme.nfo", []byte("no audio here"))

	idx := &countingNZBIndexer{nzbs: map[string][]byte{
		"https://idx.test/good.nzb": goodNZB,
	}}
	for i := 1; i <= 10; i++ {
		idx.nzbs[fmt.Sprintf("https://idx.test/bad-%d.nzb", i)] = badNZB
	}

	kr := NewKindResolver(KindResolverDeps{
		Indexer: idx,
		Engine:  eng,
	})

	deps := kindstream.Deps{
		OpenReleaseSource: func(ctx context.Context, src core.PlayableSource) (core.MediaSource, error) {
			return kr.openReleaseForSource(ctx, src, "audio/*", nil)
		},
		DefaultReleaseMIME: "audio/*",
	}

	// 1. Loose audio file plays end-to-end through KindResolver + stream.Engine with Range support.
	bs, _, err := kindstream.OpenSources(ctx, []core.PlayableSource{{
		ID:      "good",
		Pattern: kindstream.PatternRelease,
		MIME:    "audio/flac",
		Release: &core.ReleaseHint{NZBURL: "https://idx.test/good.nzb"},
	}}, deps)
	if err != nil {
		t.Fatalf("OpenSources(loose flac): %v", err)
	}
	if bs.MIME() != "audio/flac" || bs.Name() != "01 - Track.flac" {
		_ = bs.Close()
		t.Fatalf("loose flac = (name=%q, mime=%q)", bs.Name(), bs.MIME())
	}
	got := make([]byte, bs.Size())
	if n, err := bs.ReadAt(got, 0); (err != nil && err != io.EOF) || int64(n) != bs.Size() || !bytes.Equal(got, flacData) {
		_ = bs.Close()
		t.Fatalf("loose flac read mismatch: n=%d err=%v", n, err)
	}
	rangeSlice := make([]byte, 64)
	if n, err := bs.ReadAt(rangeSlice, 1024); err != nil || n != 64 || !bytes.Equal(rangeSlice, flacData[1024:1088]) {
		_ = bs.Close()
		t.Fatalf("loose flac Range ReadAt(1024): n=%d err=%v", n, err)
	}
	_ = bs.Close()

	// 2. 10 unusable non-video Usenet sources obey DefaultMaxNonVideoReleaseGrabs (not 10).
	idx.grabs.Store(0)
	var badSources []core.PlayableSource
	for i := 1; i <= 10; i++ {
		badSources = append(badSources, core.PlayableSource{
			ID:      fmt.Sprintf("bad-%d", i),
			Pattern: kindstream.PatternRelease,
			MIME:    "audio/flac",
			Release: &core.ReleaseHint{NZBURL: fmt.Sprintf("https://idx.test/bad-%d.nzb", i)},
		})
	}
	if _, _, err := kindstream.OpenSources(ctx, badSources, deps); err == nil {
		t.Fatal("expected error for 10 unusable non-video NZBs")
	}
	if grabs := idx.grabs.Load(); int(grabs) != kindstream.DefaultMaxNonVideoReleaseGrabs {
		t.Fatalf("10 unusable non-video Usenet sources burned %d grabs, want %d", grabs, kindstream.DefaultMaxNonVideoReleaseGrabs)
	}

	// 3. One release's archive missing a requested track moves on to the next
	// release instead of skipping it, and reports a media-neutral error (#753).
	track10Data := append([]byte("fLaC"), bytes.Repeat([]byte("track10-payload"), 2000)...)
	rarMiss, err := archivetest.BuildRAR([]archivetest.RARFile{
		{Name: "01 - Opening.flac", Data: flacData},
	}, archivetest.RAROptions{Version: 5})
	if err != nil {
		t.Fatalf("BuildRAR(miss): %v", err)
	}
	rarHit, err := archivetest.BuildRAR([]archivetest.RARFile{
		{Name: "01 - Opening.flac", Data: flacData},
		{Name: "10 - 7 rings.flac", Data: track10Data},
	}, archivetest.RAROptions{Version: 5})
	if err != nil {
		t.Fatalf("BuildRAR(hit): %v", err)
	}
	idx.nzbs["https://idx1.test/rel-a.nzb"] = buildSingleFileNZB(fetcher, "release-a.rar", rarMiss[0])
	idx.nzbs["https://idx2.test/rel-b.nzb"] = buildSingleFileNZB(fetcher, "release-b.rar", rarHit[0])
	idx.grabs.Store(0)

	var failedErrs []error
	trackDeps := deps
	trackDeps.OnSourceSkipped = func(failures []kindstream.SourceFailure, _ core.PlayableSource) {
		for _, f := range failures {
			failedErrs = append(failedErrs, f.Err)
		}
	}
	wantTrack := &core.TrackHint{Disc: 1, Number: 10, Title: "7 rings"}
	bs2, win2, err := kindstream.OpenSources(ctx, []core.PlayableSource{
		{
			ID:      "rel-a",
			Pattern: kindstream.PatternRelease,
			MIME:    "audio/flac",
			Release: &core.ReleaseHint{NZBURL: "https://idx1.test/rel-a.nzb", Track: wantTrack},
		},
		{
			ID:      "rel-b",
			Pattern: kindstream.PatternRelease,
			MIME:    "audio/flac",
			Release: &core.ReleaseHint{NZBURL: "https://idx2.test/rel-b.nzb", Track: wantTrack},
		},
	}, trackDeps)
	if err != nil {
		t.Fatalf("OpenSources(track miss -> working release): %v", err)
	}
	gotTrack10 := make([]byte, bs2.Size())
	if n, rerr := bs2.ReadAt(gotTrack10, 0); (rerr != nil && rerr != io.EOF) || int64(n) != bs2.Size() || !bytes.Equal(gotTrack10, track10Data) {
		_ = bs2.Close()
		t.Fatalf("track 10 read mismatch: n=%d err=%v", n, rerr)
	}
	_ = bs2.Close()
	if win2.ID != "rel-b" || idx.grabs.Load() != 2 {
		t.Fatalf("winner=%q grabs=%d, want (rel-b, 2)", win2.ID, idx.grabs.Load())
	}
	if len(failedErrs) != 1 || strings.Contains(failedErrs[0].Error(), "video") || !strings.Contains(failedErrs[0].Error(), "no playable file in release") {
		t.Fatalf("candidate 1 failure = %v, want single media-neutral no playable file error", failedErrs)
	}
}
