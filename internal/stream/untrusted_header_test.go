package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/usenet"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
	"github.com/braedonsaunders/ether/internal/yenc"
)

func twoPartFile() *postedFile {
	f := &nzb.File{Name: "x.mkv", Segments: []nzb.Segment{{Number: 1, MessageID: "a"}, {Number: 2, MessageID: "b"}}}
	return newPostedFile(0, f, 0, nil)
}

// A yEnc header is posted by strangers: a short article claiming a huge
// part must not be zero-filled into a process-killing allocation.
func TestNormalizeRejectsImplausiblePartHeader(t *testing.T) {
	e := newTestEngine(t, newFakeFetcher(), nil, Options{})
	pf := twoPartFile()
	data := []byte("short article")
	_, err := e.normalize(pf, 0, data, core.ArticlePart{Name: "x.mkv", FileSize: 1 << 40, Part: 1, Total: 2, Begin: 1, End: 1 << 40})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("implausible header accepted: %v", err)
	}
	// A damaged decoded body is never silently turned into container bytes.
	for _, data := range [][]byte{data, make([]byte, 200)} {
		_, err = e.normalize(pf, 0, data, core.ArticlePart{FileSize: 1000, Begin: 1, End: 100})
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("length mismatch: %v", err)
		}
	}
}

func TestLearnIgnoresAbsurdPartTotal(t *testing.T) {
	pf := twoPartFile()
	pf.learn(0, 0, 100, 150, 1<<30)
	if n := pf.numSlots(); n != 2 {
		t.Fatalf("slots = %d after an absurd total, want 2", n)
	}
	pf.learn(0, 0, 100, 350, 4)
	if n := pf.numSlots(); n != 4 {
		t.Fatalf("slots = %d after a plausible total, want 4", n)
	}
	// Growth is bounded by the NZB listing, so headers cannot compound it.
	pf.learn(1, 100, 200, 350, 2000)
	if n := pf.numSlots(); n != 4 {
		t.Fatalf("slots = %d after a compounding total, want 4", n)
	}
}

func TestNormalizeAndLearnDistrustContradictoryFileMeta(t *testing.T) {
	e := newTestEngine(t, newFakeFetcher(), nil, Options{})

	// Three 100 KB articles whose =ybegin size/part/total/name are randomised
	// while =ypart [Begin, End] carries the true 300 KB layout.
	threeSegs := &nzb.File{
		Name: "obf.part01.rar",
		Segments: []nzb.Segment{
			{Number: 1, MessageID: "p1", Bytes: 103_000},
			{Number: 2, MessageID: "p2", Bytes: 103_000},
			{Number: 3, MessageID: "p3", Bytes: 103_000},
		},
	}
	pf := newPostedFile(0, threeSegs, 0, nil)
	parts := []core.ArticlePart{
		{Name: "rand1.bin", FileSize: 10_888_513, Part: 1, Total: 829, Begin: 1, End: 100_000},
		{Name: "rand2.bin", FileSize: 13_053_958, Part: 9, Total: 1022, Begin: 100_001, End: 200_000},
		{Name: "", FileSize: 0, Part: 3, Total: 0, Begin: 200_001, End: 300_000}, // ParsePart cleared size < end
	}
	for i, ap := range parts {
		seg, err := e.normalize(pf, i, make([]byte, 100_000), ap)
		if err != nil {
			t.Fatalf("slot %d normalize: %v", i, err)
		}
		if seg.fileSize != 0 || seg.total != 0 || seg.name != "" || seg.rangeExact {
			t.Fatalf("slot %d retained untrusted metadata: %+v", i, seg)
		}
		if seg.begin != int64(i*100_000) {
			t.Fatalf("slot %d begin = %d, want %d", i, seg.begin, i*100_000)
		}
		pf.learn(i, seg.begin, seg.end(), seg.fileSize, seg.total)
	}
	if pf.numSlots() != 3 || pf.SizeKnown() || pf.Size() != 300_000 {
		t.Fatalf("pf slots=%d known=%v size=%d, want 3/false/300000", pf.numSlots(), pf.SizeKnown(), pf.Size())
	}

	// Two articles with individually plausible but disagreeing FileSize values
	// invalidate SizeKnown and fall back to the learned =ypart extent.
	disagree := newPostedFile(0, threeSegs, 0, nil)
	disagree.learn(0, 0, 100_000, 280_000, 3)
	if !disagree.SizeKnown() || disagree.Size() != 280_000 {
		t.Fatalf("initial plausible size not learned: known=%v size=%d", disagree.SizeKnown(), disagree.Size())
	}
	disagree.learn(1, 100_000, 200_000, 290_000, 3)
	disagree.learn(2, 200_000, 300_000, 0, 0)
	if disagree.SizeKnown() || disagree.Size() != 300_000 {
		t.Fatalf("conflicting sizes did not fall back to maxEnd: known=%v size=%d", disagree.SizeKnown(), disagree.Size())
	}

	// Honest single-part post and honest multi-part post with a short last article
	// preserve exact FileSize, Total, Name, and rangeExact.
	single := newPostedFile(0, &nzb.File{
		Name:     "single.nfo",
		Segments: []nzb.Segment{{Number: 1, MessageID: "s1", Bytes: 512}},
	}, 0, nil)
	sseg, err := e.normalize(single, 0, make([]byte, 420), core.ArticlePart{
		Name: "single.nfo", FileSize: 420, Part: 1, Total: 1, Begin: 1, End: 420,
	})
	if err != nil {
		t.Fatalf("single normalize: %v", err)
	}
	single.learn(0, sseg.begin, sseg.end(), sseg.fileSize, sseg.total)
	if !sseg.rangeExact || sseg.name != "single.nfo" || !single.SizeKnown() || single.Size() != 420 {
		t.Fatalf("honest single-part broken: seg=%+v known=%v size=%d", sseg, single.SizeKnown(), single.Size())
	}

	honest := newPostedFile(0, threeSegs, 0, nil)
	for i, ap := range []core.ArticlePart{
		{Name: "movie.rar", FileSize: 215_000, Part: 1, Total: 3, Begin: 1, End: 100_000},
		{Name: "movie.rar", FileSize: 215_000, Part: 2, Total: 3, Begin: 100_001, End: 200_000},
		{Name: "movie.rar", FileSize: 215_000, Part: 3, Total: 3, Begin: 200_001, End: 215_000},
	} {
		n := int(ap.End - ap.Begin + 1)
		seg, err := e.normalize(honest, i, make([]byte, n), ap)
		if err != nil {
			t.Fatalf("honest slot %d normalize: %v", i, err)
		}
		if !seg.rangeExact || seg.fileSize != 215_000 || seg.total != 3 || seg.name != "movie.rar" {
			t.Fatalf("honest slot %d metadata = %+v", i, seg)
		}
		honest.learn(i, seg.begin, seg.end(), seg.fileSize, seg.total)
	}
	if !honest.SizeKnown() || honest.Size() != 215_000 {
		t.Fatalf("honest short-tail size: known=%v size=%d", honest.SizeKnown(), honest.Size())
	}
}

// Reproduces #577 end-to-end over real NNTP: an obfuscated multi-volume stored
// RAR5 release whose per-article =ybegin headers carry randomised contradicting
// size/part/total/name values (both size >> volume and end > size) while =ypart
// extents carry the true byte layout.
func TestObfuscatedRAR5ContradictoryYBeginExtentsNNTP(t *testing.T) {
	video := mkvBytes(577, 450_000)
	vols := buildRAR(t, []archivetest.RARFile{
		{Name: "Movie.2026.2160p.mkv", Data: video},
	}, archivetest.RAROptions{Version: 5, VolumeSize: 300_000, NewNumbering: true})
	if len(vols) != 2 || len(vols[0]) != 300_000 {
		t.Fatalf("unexpected RAR5 layout: %d volumes, vol0=%d", len(vols), len(vols[0]))
	}

	srv := nntptest.Start(t, nntptest.Options{})
	type rawSeg struct {
		id     string
		data   []byte
		begin  int64
		ybegin string
	}
	addRawObfuscated := func(segs []rawSeg) []nzb.Segment {
		var out []nzb.Segment
		for idx, s := range segs {
			enc, err := yenc.EncodeBytes(s.data, yenc.EncodeOptions{
				Name: "real.bin", Part: idx + 1, Total: len(segs),
				Begin: s.begin, FileSize: s.begin - 1 + int64(len(s.data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			nl := bytes.Index(enc, []byte("\r\n"))
			rewritten := append([]byte(s.ybegin), enc[nl:]...)
			srv.AddArticle(s.id, rewritten)
			out = append(out, nzb.Segment{Number: idx + 1, Bytes: int64(len(rewritten)), MessageID: s.id})
		}
		return out
	}

	// Volume 1: three 100 KB articles with contradictory =ybegin headers:
	//   - art 1: size=10888513 >> 300000, total=829
	//   - art 2: wrong part=9, total=1022, size=13053958 (also sampled by Check's BODY probe)
	//   - art 3: size=84436 < end=300000 (end > size)
	vol1Segs := addRawObfuscated([]rawSeg{
		{
			id:     "v1.p1@test",
			data:   vols[0][0:100_000],
			begin:  1,
			ybegin: "=ybegin part=1 total=829 line=128 size=10888513 name=9f1a0b.bin",
		},
		{
			id:     "v1.p2@test",
			data:   vols[0][100_000:200_000],
			begin:  100_001,
			ybegin: "=ybegin part=9 total=1022 line=128 size=13053958 name=3c8d2e.bin",
		},
		{
			id:     "v1.p3@test",
			data:   vols[0][200_000:300_000],
			begin:  200_001,
			ybegin: "=ybegin part=3 total=261 line=128 size=84436 name=7b4e1a.bin",
		},
	})
	// Volume 2: 100 KB + short final article, also with contradictory =ybegin sizes.
	vol2Segs := addRawObfuscated([]rawSeg{
		{
			id:     "v2.p1@test",
			data:   vols[1][0:100_000],
			begin:  1,
			ybegin: "=ybegin part=1 total=777 line=128 size=9999999 name=aa11bb.bin",
		},
		{
			id:     "v2.p2@test",
			data:   vols[1][100_000:],
			begin:  100_001,
			ybegin: "=ybegin part=2 total=512 line=128 size=12345 name=cc22dd.bin",
		},
	})

	var xmlBuf strings.Builder
	xmlBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
	for i, segs := range [][]nzb.Segment{vol1Segs, vol2Segs} {
		subj := fmt.Sprintf(`[1/2] - "a1b2c3d4e5f6%02d" yEnc (1/%d)`, i+1, len(segs))
		fmt.Fprintf(&xmlBuf, `<file poster="p" date="1700000000" subject="%s"><groups><group>alt.binaries.test</group></groups><segments>`, html.EscapeString(subj))
		for _, s := range segs {
			fmt.Fprintf(&xmlBuf, `<segment bytes="%d" number="%d">%s</segment>`, s.Bytes, s.Number, html.EscapeString(s.MessageID))
		}
		xmlBuf.WriteString(`</segments></file>`)
	}
	xmlBuf.WriteString(`</nzb>`)
	nzbDoc := []byte(xmlBuf.String())

	fetcher := usenet.New([]config.UsenetProvider{srv.Provider("primary", 4)}, usenet.Options{
		MinIdle: -1, KeepAlive: -1, CommandTimeout: 3 * time.Second,
	})
	t.Cleanup(func() { _ = fetcher.Close() })

	e := newTestEngine(t, fetcher, nil, Options{HealthSamples: 5, ReadAheadMin: 1, ReadAheadMax: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ok, err := e.Check(ctx, nzbDoc); err != nil || !ok {
		t.Fatalf("Check = %v, %v; want true, nil", ok, err)
	}
	src, err := e.Open(ctx, "rel-577", nzbDoc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer src.Close()
	if src.Name() != "Movie.2026.2160p.mkv" || src.Size() != int64(len(video)) {
		t.Fatalf("Name=%q Size=%d, want Movie.2026.2160p.mkv / %d", src.Name(), src.Size(), len(video))
	}
	if got := readAll(t, src, 32<<10); !bytes.Equal(got, video) {
		t.Fatal("sequential read mismatch across obfuscated RAR5 volumes")
	}
	checkRandomReads(t, src, video, 577, 80)

	// Real truncation (missing article) must still surface as ErrArticleMissing.
	srv.SetMissing(true, "v1.p2@test")
	e2 := newTestEngine(t, fetcher, nil, Options{HealthSamples: 5, ReadAheadParallel: 1})
	if ok, err := e2.Check(ctx, nzbDoc); ok || !errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("Check with missing article = %v, %v; want false, ErrArticleMissing", ok, err)
	}
	src2, err := e2.Open(ctx, "rel-577-missing", nzbDoc)
	if err != nil {
		t.Fatalf("Open before hitting missing middle article: %v", err)
	}
	defer src2.Close()
	if _, err := src2.ReadAt(make([]byte, 32<<10), 120_000); !errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("ReadAt across missing article = %v, want ErrArticleMissing", err)
	}
}

type retryOwningFetcher struct{ *fakeFetcher }

func (*retryOwningFetcher) OwnsArticleRetries() {}

func TestFetcherRetryBudgetIsNotMultiplied(t *testing.T) {
	f := newFakeFetcher()
	f.failures["a"] = 100
	e := newTestEngine(t, &retryOwningFetcher{f}, nil, Options{Retries: 2})
	pf := twoPartFile()
	_, _, err := e.load(context.Background(), pf, 0, "a", 0, core.PriorityLive, fetchOpts{})
	if err == nil {
		t.Fatal("expected failure")
	}
	if got := f.bodyCalls["a"]; got != 1 {
		t.Fatalf("fetch calls = %d, want 1", got)
	}
}
