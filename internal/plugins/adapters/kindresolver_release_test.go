package adapters

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type captureDebridBackend struct {
	lastRelease core.Release
	files       []core.ReleaseFile
}

func (c *captureDebridBackend) CheckCached(_ context.Context, hashes []string) (map[string]bool, bool) {
	out := make(map[string]bool, len(hashes))
	return out, true
}

func (c *captureDebridBackend) Acquire(_ context.Context, rel core.Release) ([]byte, error) {
	c.lastRelease = rel
	return []byte("payload"), nil
}

func (c *captureDebridBackend) ListReleaseFiles(_ context.Context, rel core.Release) ([]core.ReleaseFile, error) {
	c.lastRelease = rel
	if c.files != nil {
		return c.files, nil
	}
	return nil, core.ErrNotCached
}

type stubEngine struct {
	core.Engine
}

func (stubEngine) Open(_ context.Context, id string, data []byte) (core.MediaSource, error) {
	return stubMediaSource{id: id, data: data}, nil
}

type stubMediaSource struct {
	id   string
	data []byte
}

func (s stubMediaSource) ReleaseID() string { return s.id }
func (s stubMediaSource) Name() string      { return "file.epub" }
func (s stubMediaSource) Size() int64       { return int64(len(s.data)) }
func (s stubMediaSource) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s stubMediaSource) Close() error { return nil }

func TestKindResolverReleaseExplicitSelectorsEnableAllowNonVideo(t *testing.T) {
	ctx := context.Background()
	deb := &captureDebridBackend{}
	kr := NewKindResolver(KindResolverDeps{
		Debrid: deb,
		Engine: stubEngine{},
	})

	// Default HTTP client must honour request context (Timeout == 0) with 30s response-header timeout.
	if kr.http.Timeout != 0 {
		t.Fatalf("NewKindResolver default HTTP Timeout = %v, want 0", kr.http.Timeout)
	}
	if tr, ok := kr.http.Transport.(*http.Transport); !ok || tr.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("NewKindResolver default Transport ResponseHeaderTimeout = %v, want 30s", kr.http.Transport)
	}

	idx0 := 0
	idx2 := 2
	for _, tc := range []struct {
		name      string
		hint      core.ReleaseHint
		wantAllow bool
		wantIdx   *int
		wantExact string
	}{
		{
			name:      "explicit release.file",
			hint:      core.ReleaseHint{InfoHash: "0123456789abcdef0123456789abcdef01234567", File: "Books/Title.epub"},
			wantAllow: true,
			wantExact: "Books/Title.epub",
		},
		{
			name:      "explicit release.fileIndex",
			hint:      core.ReleaseHint{InfoHash: "0123456789abcdef0123456789abcdef01234567", FileIndex: &idx0},
			wantAllow: true,
			wantIdx:   &idx0,
		},
		{
			name: "explicit release.candidate.fileIndex",
			hint: core.ReleaseHint{
				Candidate: &core.SourceCandidate{
					InfoHash:  "0123456789abcdef0123456789abcdef01234567",
					FileIndex: &idx2,
				},
			},
			wantAllow: true,
			wantIdx:   &idx2,
		},
		{
			name:      "automatic selection without explicit selector",
			hint:      core.ReleaseHint{InfoHash: "0123456789abcdef0123456789abcdef01234567"},
			wantAllow: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deb.lastRelease = core.Release{}
			ms, err := kr.openRelease(ctx, tc.hint)
			if err != nil {
				t.Fatalf("openRelease: %v", err)
			}
			_ = ms.Close()

			if !tc.wantAllow {
				if deb.lastRelease.Target != nil {
					t.Fatalf("expected nil Target for automatic selection, got %+v", deb.lastRelease.Target)
				}
				return
			}
			if deb.lastRelease.Target == nil || !deb.lastRelease.Target.AllowNonVideo {
				t.Fatalf("expected Target.AllowNonVideo=true, got %+v", deb.lastRelease.Target)
			}
			if deb.lastRelease.Target.ExactFile != tc.wantExact {
				t.Fatalf("ExactFile = %q, want %q", deb.lastRelease.Target.ExactFile, tc.wantExact)
			}
			if (tc.wantIdx == nil) != (deb.lastRelease.Target.FileIndex == nil) ||
				(tc.wantIdx != nil && *deb.lastRelease.Target.FileIndex != *tc.wantIdx) {
				t.Fatalf("FileIndex = %v, want %v", deb.lastRelease.Target.FileIndex, tc.wantIdx)
			}
		})
	}

	// Empty MIME on kindLocalFile defaults to application/octet-stream.
	lf := &kindLocalFile{mime: ""}
	if got := lf.MIME(); got != "application/octet-stream" {
		t.Fatalf("kindLocalFile.MIME() = %q, want application/octet-stream", got)
	}
}

type releaseFilesIndexer struct {
	nzbs          map[string][]byte
	torrents      map[string]core.Release
	fetchNZBCalls int
	resolveCalls  int
}

func (i *releaseFilesIndexer) Name() string { return "release-files-idx" }
func (i *releaseFilesIndexer) Search(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	return nil, nil
}
func (i *releaseFilesIndexer) FetchNZB(_ context.Context, rel core.Release) ([]byte, error) {
	i.fetchNZBCalls++
	if b, ok := i.nzbs[rel.NZBURL]; ok {
		return b, nil
	}
	return nil, core.ErrNotAvailable
}
func (i *releaseFilesIndexer) ResolveTorrent(_ context.Context, rel core.Release) (core.Release, error) {
	i.resolveCalls++
	if resolved, ok := i.torrents[rel.NZBURL]; ok {
		return resolved, nil
	}
	return rel, core.ErrReleaseRejected
}

type stubNZBListEngine struct {
	stubEngine
	files []core.ReleaseFile
}

func (s stubNZBListEngine) ListFiles(_ context.Context, _ string, _ []byte) ([]core.ReleaseFile, error) {
	return s.files, nil
}

// release_files lists a cached torrent's files through debrid and an
// NZB's files through its indexer; an uncached torrent without .torrent
// metainfo answers empty.
func TestResolveSourcesReleaseFiles(t *testing.T) {
	ctx := context.Background()
	deb := &captureDebridBackend{files: []core.ReleaseFile{
		{Index: 0, Path: "Collection/First.2020.mp4", Size: 111},
		{Index: 1, Path: "Collection/Second.2021.mp4", Size: 222},
	}}
	nzbDoc := []byte(`<nzb>` +
		`<file poster="p" date="1" subject="&quot;First.2020.mp4&quot; yEnc (1/1)"><groups><group>a.b</group></groups><segments><segment bytes="1031" number="1">a@x</segment></segments></file>` +
		`<file poster="p" date="1" subject="&quot;Second.2021.mp4&quot; yEnc (1/1)"><groups><group>a.b</group></groups><segments><segment bytes="2062" number="1">b@x</segment></segments></file>` +
		`</nzb>`)
	idx := &releaseFilesIndexer{nzbs: map[string][]byte{"https://idx.test/nzb/1": nzbDoc}}
	kr := NewKindResolver(KindResolverDeps{
		Debrid:  deb,
		Indexer: idx,
	})

	out, err := kr.ResolveSources(ctx, "plug", core.SourceQuery{Kind: "release_files", InfoHash: "ABCDEF"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !out[0].Cached || len(out[0].Files) != 2 || out[0].Files[1].Path != "Collection/Second.2021.mp4" || out[0].Files[1].Index != 1 {
		t.Fatalf("torrent release_files = %+v", out)
	}
	if deb.lastRelease.InfoHash != "abcdef" {
		t.Fatalf("debrid saw hash %q", deb.lastRelease.InfoHash)
	}

	deb.files = nil // uncached now
	out, err = kr.ResolveSources(ctx, "plug", core.SourceQuery{Kind: "release_files", InfoHash: "abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Cached || len(out[0].Files) != 0 {
		t.Fatalf("uncached release_files = %+v, want cached=false no files", out)
	}

	// Without an engine, NZB release_files falls back to segment-derived
	// decoded estimates marked SizeApproximate=true (never the raw encoded sum).
	out, err = kr.ResolveSources(ctx, "plug", core.SourceQuery{
		Kind:      "release_files",
		Candidate: &core.SourceCandidate{NZBURL: "https://idx.test/nzb/1", Indexer: "idx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].Files) != 2 || out[0].Files[0].Path != "First.2020.mp4" ||
		out[0].Files[0].Size != 1000 || !out[0].Files[0].SizeApproximate {
		t.Fatalf("nzb fallback release_files = %+v, want decoded estimate 1000 with SizeApproximate=true", out)
	}

	// With a streaming engine that implements ListFiles, exact decoded sizes
	// are returned with SizeApproximate=false.
	krExact := NewKindResolver(KindResolverDeps{
		Indexer: idx,
		Engine: stubNZBListEngine{files: []core.ReleaseFile{
			{Index: 0, Path: "First.2020.mp4", Size: 987, SizeApproximate: false},
			{Index: 1, Path: "Second.2021.mp4", Size: 1974, SizeApproximate: false},
		}},
	})
	out, err = krExact.ResolveSources(ctx, "plug", core.SourceQuery{
		Kind:      "release_files",
		Candidate: &core.SourceCandidate{NZBURL: "https://idx.test/nzb/1", Indexer: "idx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(out[0].Files) != 2 || out[0].Files[0].Size != 987 || out[0].Files[0].SizeApproximate {
		t.Fatalf("nzb exact release_files = %+v, want exact 987 with SizeApproximate=false", out)
	}
}

// Cardigann torrent candidates carry their HTML release page or .torrent link
// in TorrentURL (never NZBURL). release_files and openRelease must use the
// torrent resolution path (ResolveTorrent -> debrid or .torrent metainfo) and
// never call FetchNZB.
func TestCardigannTorrentCandidateUsesTorrentPathNeverFetchNZB(t *testing.T) {
	ctx := context.Background()
	const resolvedHash = "0123456789abcdef0123456789abcdef01234567"
	const pageURL = "https://limetorrents.example/Artist-Album-FLAC-torrent-12345.html"
	metaFiles := []core.ReleaseFile{
		{Index: 0, Path: "Album/01 - Opening.flac", Size: 25_000_000},
		{Index: 1, Path: "Album/02 - Finale.flac", Size: 30_000_000},
	}
	idx := &releaseFilesIndexer{
		torrents: map[string]core.Release{
			pageURL: {
				Name:         "Artist - Album [FLAC]",
				Protocol:     core.ProtocolTorrent,
				InfoHash:     resolvedHash,
				MagnetURL:    "magnet:?xt=urn:btih:" + resolvedHash,
				TorrentFiles: metaFiles,
			},
		},
	}

	// 1. Cached on debrid: ResolveTorrent resolves the Cardigann link and
	//    debrid supplies the provider file list; FetchNZB is never called.
	debridFiles := []core.ReleaseFile{
		{Index: 0, Path: "Album/01 - Opening.flac", Size: 25_000_000},
		{Index: 1, Path: "Album/02 - Finale.flac", Size: 30_000_000},
	}
	deb := &captureDebridBackend{files: debridFiles}
	kr := NewKindResolver(KindResolverDeps{
		Debrid:  deb,
		Indexer: idx,
		Engine:  stubEngine{},
	})
	cand := &core.SourceCandidate{
		TorrentURL: pageURL,
		Title:      "Artist - Album [FLAC]",
		Indexer:    "LimeTorrents",
		IndexerID:  "cardigann-lime",
	}
	out, err := kr.ResolveSources(ctx, "music", core.SourceQuery{
		Kind:      "release_files",
		Candidate: cand,
	})
	if err != nil {
		t.Fatalf("release_files cached Cardigann torrent: %v", err)
	}
	if idx.fetchNZBCalls != 0 {
		t.Fatalf("FetchNZB called %d times for Cardigann torrent candidate, want 0", idx.fetchNZBCalls)
	}
	if idx.resolveCalls != 1 {
		t.Fatalf("ResolveTorrent called %d times, want 1", idx.resolveCalls)
	}
	if len(out) != 1 || !out[0].Cached || out[0].InfoHash != resolvedHash || out[0].NZBURL != "" || len(out[0].Files) != 2 {
		t.Fatalf("cached Cardigann release_files = %+v", out)
	}

	// 2. Uncached on debrid: falls back to .torrent metainfo files with Cached=false.
	deb.files = nil
	out, err = kr.ResolveSources(ctx, "music", core.SourceQuery{
		Kind:      "release_files",
		Candidate: cand,
	})
	if err != nil {
		t.Fatalf("release_files uncached Cardigann torrent: %v", err)
	}
	if idx.fetchNZBCalls != 0 {
		t.Fatalf("FetchNZB called %d times, want 0", idx.fetchNZBCalls)
	}
	if len(out) != 1 || out[0].Cached || out[0].InfoHash != resolvedHash || len(out[0].Files) != 2 || out[0].Files[0].Path != "Album/01 - Opening.flac" {
		t.Fatalf("uncached metainfo fallback = %+v", out)
	}

	// 3. openRelease with TorrentURL directly resolves via ResolveTorrent -> debrid.Acquire.
	deb.lastRelease = core.Release{}
	ms, err := kr.openRelease(ctx, core.ReleaseHint{Candidate: cand})
	if err != nil {
		t.Fatalf("openRelease with TorrentURL: %v", err)
	}
	_ = ms.Close()
	if idx.fetchNZBCalls != 0 {
		t.Fatalf("openRelease called FetchNZB %d times, want 0", idx.fetchNZBCalls)
	}
	if deb.lastRelease.InfoHash != resolvedHash || deb.lastRelease.Protocol != core.ProtocolTorrent {
		t.Fatalf("debrid.Acquire saw %+v, want torrent %s", deb.lastRelease, resolvedHash)
	}
}

// When a source sets both release.fileHint and container (member-inside-a-member,
// e.g. a named ROM .zip inside a pack torrent whose inner .nes is selected by
// container.member), releaseFromSource must keep fileHint on Release.Target.Member
// and propagate FileCount (#709, #667).
func TestKindResolverReleaseFileHintAndFileCountWithContainer(t *testing.T) {
	src := core.PlayableSource{
		ID:      "rom-nested",
		Pattern: "release",
		MIME:    "application/octet-stream",
		Release: &core.ReleaseHint{
			InfoHash:  "0123456789abcdef0123456789abcdef01234567",
			FileHint:  "Super Mario Bros. (World).zip",
			FileCount: 250,
		},
		Container: &core.ContainerRef{
			Member: "Super Mario Bros. (World).nes",
		},
	}
	rel, err := releaseFromSource(src, "No-Intro NES", "")
	if err != nil {
		t.Fatalf("releaseFromSource: %v", err)
	}
	if rel.FileCount != 250 {
		t.Fatalf("FileCount = %d, want 250", rel.FileCount)
	}
	if rel.Target == nil || rel.Target.Member != "Super Mario Bros. (World).zip" || !rel.Target.AllowNonVideo {
		t.Fatalf("Target = %+v, want Member=Super Mario Bros. (World).zip AllowNonVideo=true", rel.Target)
	}
}
