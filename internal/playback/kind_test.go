package playback

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// kindSource serves a fakeRelease's bytes as a core.MediaSource, standing in
// for a kindstream ByteSource a plugin resolver opened.
type kindSource struct {
	rel    *fakeRelease
	closed bool
}

func (k *kindSource) ReadAt(p []byte, off int64) (int, error) {
	if k.closed {
		return 0, fmt.Errorf("read on closed kind source")
	}
	if off < 0 || off >= k.rel.Size {
		return 0, io.EOF
	}
	n := int64(len(p))
	if off+n > k.rel.Size {
		n = k.rel.Size - off
	}
	for i := int64(0); i < n; i++ {
		p[i] = contentAt(k.rel.ID, off+i)
	}
	if n < int64(len(p)) {
		return int(n), io.EOF
	}
	return int(n), nil
}
func (k *kindSource) Size() int64       { return k.rel.Size }
func (k *kindSource) Name() string      { return k.rel.Name }
func (k *kindSource) ReleaseID() string { return k.rel.ID }
func (k *kindSource) Close() error      { k.closed = true; return nil }

var pluginKindKey = core.PluginKey(1)

// kindInput builds a KindInput whose projected title is a plugin leaf and
// whose bytes come from a fakeRelease registered with the harness engine so
// the fake prober can identify it by its header.
func kindInput(rel *fakeRelease) core.KindInput {
	title := &core.Title{Key: pluginKindKey, Type: core.TypeMovie, Title: "Plugin Leaf", Year: 2025, Runtime: rel.Duration}
	return core.KindInput{
		Title:    title,
		SourceID: rel.ID,
		Open: func(context.Context) (core.MediaSource, error) {
			return &kindSource{rel: rel}, nil
		},
	}
}

// A plugin video leaf is opened through the shared session pipeline: it is
// probed, a session is registered, and its bytes are served with HTTP range
// support exactly like a catalogue title (spec §15.1).
func TestPrepareKindProbesAndServes(t *testing.T) {
	rel := mkv("K1", 200000, 100*time.Second)
	h := newHarness(t, []*fakeRelease{rel}, nil)

	ps, err := h.m.PrepareKind(context.Background(), owner, kindInput(rel), defaultCaps("kind-1"))
	if err != nil {
		t.Fatalf("PrepareKind: %v", err)
	}
	if ps.Key != pluginKindKey {
		t.Fatalf("session key = %s, want %s", ps.Key, pluginKindKey)
	}
	if ps.Resolution == nil || ps.Resolution.Release.ID != "kind:K1" {
		t.Fatalf("session resolution = %+v, want synthetic kind release", ps.Resolution)
	}
	if ps.Info.Size != rel.Size {
		t.Fatalf("probed size = %d, want %d", ps.Info.Size, rel.Size)
	}
	if ps.Info.Container != "mkv" {
		t.Fatalf("probed container = %q, want mkv", ps.Info.Container)
	}
	if v := ps.Info.Video(); v == nil || v.Codec != "h264" {
		t.Fatalf("probed video = %+v, want h264", v)
	}

	// A live session exists for the viewer/client/item.
	if got, ok := h.m.SessionForKind(owner.ID, "kind-1", pluginKindKey); !ok || got.ID != ps.ID {
		t.Fatalf("SessionForKind = %v, %v; want the prepared session", got, ok)
	}

	// Direct-play range reads flow through the loopback server.
	srv := directPlayServer(t, h.m, ps.ID)
	resp, body, err := get(t, srv.URL, map[string]string{"Range": "bytes=100-199"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 206 {
		t.Fatalf("range status = %d, want 206", resp.StatusCode)
	}
	want := expected("K1", 100, 100)
	if string(body) != string(want) {
		t.Fatalf("range bytes mismatch")
	}
}

// Seeking across a Sequence leaf boundary works through the same session: the
// kind source is a concatenated multi-leaf stream, and a read spanning the
// boundary returns the joined bytes (spec §5.2, §15.1).
func TestPrepareKindSequenceSeekAcrossLeaf(t *testing.T) {
	leafA := mkv("KA", 4096, 10*time.Second)
	leafB := mkv("KB", 4096, 10*time.Second)
	h := newHarness(t, nil, nil)

	// A sequence MediaSource over two leaves, joined at offset 4096.
	seq := &sequenceSource{leaves: []*fakeRelease{leafA, leafB}, size: leafA.Size + leafB.Size}
	in := core.KindInput{
		Title:    &core.Title{Key: pluginKindKey, Type: core.TypeMovie, Title: "Plugin Seq"},
		SourceID: "SEQ",
		Open:     func(context.Context) (core.MediaSource, error) { return seq, nil },
	}
	ps, err := h.m.PrepareKind(context.Background(), owner, in, defaultCaps("kind-seq"))
	if err != nil {
		t.Fatalf("PrepareKind: %v", err)
	}
	srv := directPlayServer(t, h.m, ps.ID)
	// Read 200 bytes straddling the 4096 boundary.
	resp, body, err := get(t, srv.URL, map[string]string{"Range": "bytes=3996-4195"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 206 {
		t.Fatalf("range status = %d, want 206", resp.StatusCode)
	}
	want := append(expected("KA", 3996, 100), expected("KB", 0, 100)...)
	if string(body) != string(want) {
		t.Fatalf("boundary-spanning read mismatch")
	}
}

// sequenceSource is a minimal concatenated MediaSource over two fake leaves.
type sequenceSource struct {
	leaves []*fakeRelease
	size   int64
}

func (s *sequenceSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= s.size {
		return 0, io.EOF
	}
	read := 0
	for read < len(p) {
		cur := off + int64(read)
		var rel *fakeRelease
		var base int64
		switch {
		case cur < s.leaves[0].Size:
			rel, base = s.leaves[0], 0
		default:
			rel, base = s.leaves[1], s.leaves[0].Size
		}
		relOff := cur - base
		avail := rel.Size - relOff
		n := int64(len(p) - read)
		if n > avail {
			n = avail
		}
		for i := int64(0); i < n; i++ {
			p[read+int(i)] = contentAt(rel.ID, relOff+i)
		}
		read += int(n)
		if int64(read)+off >= s.size {
			break
		}
	}
	if read < len(p) {
		return read, io.EOF
	}
	return read, nil
}
func (s *sequenceSource) Size() int64       { return s.size }
func (s *sequenceSource) Name() string      { return "sequence.mkv" }
func (s *sequenceSource) ReleaseID() string { return "SEQ" }
func (s *sequenceSource) Close() error      { return nil }

// Stream limits apply to kind sessions exactly as to catalogue sessions.
func TestPrepareKindRespectsStreamLimit(t *testing.T) {
	relA := mkv("K1", 200000, 100*time.Second)
	relB := mkv("K2", 200000, 100*time.Second)
	h := newHarness(t, []*fakeRelease{relA, relB}, nil, func(h *harness, _ *Options) {
		h.cfg.Server.DefaultStreamLimit = 1
	})

	if _, err := h.m.PrepareKind(context.Background(), owner, kindInput(relA), defaultCaps("c1")); err != nil {
		t.Fatalf("first PrepareKind: %v", err)
	}
	// A second item for the same user on a different client exceeds the limit.
	key2 := core.PluginKey(2)
	in2 := kindInput(relB)
	in2.Title.Key = key2
	if _, err := h.m.PrepareKind(context.Background(), owner, in2, defaultCaps("c2")); err == nil {
		t.Fatalf("second PrepareKind succeeded despite the stream limit")
	}
}

// PrepareKind scopes deferred sessions by (PreferSourceID, Exact) so version
// switches never reuse a mismatched live session, and crossing the real-play
// threshold on an explicit plugin version records a viewer choice.
func TestPrepareKindVersionSelectionAndRememberedChoice(t *testing.T) {
	rel4K := mkv("K4K", 200000, 100*time.Second)
	rel1080 := mkv("K1080", 150000, 100*time.Second)
	h := newHarness(t, []*fakeRelease{rel4K, rel1080}, nil)
	rec := &recordingChoiceResolver{Resolver: h.resolver}
	h.m.deps.Resolver = rec
	title := &core.Title{Key: pluginKindKey, Type: core.TypeMovie, Title: "Plugin Multi", Runtime: 100 * time.Second}
	mkInput := func(prefer string, exact, deferSup bool) core.KindInput {
		in := core.KindInput{
			Title:             title,
			SourceID:          "multi",
			PreferSourceID:    prefer,
			Exact:             exact,
			DeferSupersession: deferSup,
		}
		in.OpenCandidate = func(_ context.Context, _ []string) (core.MediaSource, core.Release, error) {
			if in.PreferSourceID == "kind:K1080" {
				return &kindSource{rel: rel1080}, core.Release{ID: "kind:K1080", Name: "Movie 1080p WEB-DL", Protocol: core.ProtocolHTTP}, nil
			}
			return &kindSource{rel: rel4K}, core.Release{ID: "kind:K4K", Name: "Movie 2160p Remux", Protocol: core.ProtocolHTTP}, nil
		}
		return in
	}

	caps := defaultCaps("vela-1")
	caps.Protocol = "vela"
	psAuto, err := h.m.PrepareKind(context.Background(), owner, mkInput("", false, true), caps)
	if err != nil {
		t.Fatalf("PrepareKind(Auto): %v", err)
	}
	if psAuto.Resolution == nil || psAuto.Resolution.Release.ID != "kind:K4K" {
		t.Fatalf("Auto resolution = %+v, want kind:K4K", psAuto.Resolution)
	}
	if !h.m.MarkSurfaceOpened(psAuto.ID) {
		t.Fatal("MarkSurfaceOpened(Auto) failed")
	}

	// Preparing an explicit alternative version with DeferSupersession must NOT
	// reuse the live Auto session, and must leave Auto open until admitted.
	ps1080, err := h.m.PrepareKind(context.Background(), owner, mkInput("kind:K1080", true, true), caps)
	if err != nil {
		t.Fatalf("PrepareKind(1080p): %v", err)
	}
	if ps1080.ID == psAuto.ID || ps1080.Resolution == nil || ps1080.Resolution.Release.ID != "kind:K1080" {
		t.Fatalf("picked 1080p reused Auto or opened wrong release: id=%s rel=%+v", ps1080.ID, ps1080.Resolution)
	}
	if _, ok := h.m.Session(psAuto.ID); !ok {
		t.Fatal("deferred version prepare prematurely closed Auto session")
	}
	if !h.m.MarkSurfaceOpened(ps1080.ID) {
		t.Fatal("MarkSurfaceOpened(1080p) failed")
	}

	// Advancing playback past 5% on the explicitly chosen plugin version records
	// the viewer's choice in the resolver.
	h.m.NoteStartPosition(ps1080.ID, 0)
	s := h.m.lookup(ps1080.ID)
	h.m.observeViewerVersionChoice(s, 10*time.Second, "playing", "playing")
	rec.mu.Lock()
	got := append([]recordedChoice(nil), rec.records...)
	rec.mu.Unlock()
	if len(got) != 1 || got[0].release.ID != "kind:K1080" || got[0].key != pluginKindKey {
		t.Fatalf("recorded viewer choices = %+v, want kind:K1080 for %s", got, pluginKindKey)
	}
}
