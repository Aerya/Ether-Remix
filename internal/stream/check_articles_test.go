package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
)

type priorityRecordingFetcher struct {
	*fakeFetcher
	mu         sync.Mutex
	statBG     []bool
	bodyBG     []bool
	bodyPrios  []core.Priority
	bodyErr    map[string]error
	statErr    error
	totalCalls int
}

func newPriorityRecordingFetcher() *priorityRecordingFetcher {
	return &priorityRecordingFetcher{
		fakeFetcher: newFakeFetcher(),
		bodyErr:     map[string]error{},
	}
}

func (f *priorityRecordingFetcher) Stat(ctx context.Context, id string) (bool, error) {
	f.mu.Lock()
	f.statBG = append(f.statBG, core.IsBackgroundCheck(ctx))
	f.totalCalls++
	err := f.statErr
	f.mu.Unlock()
	if err != nil {
		return false, err
	}
	return f.fakeFetcher.Stat(ctx, id)
}

func (f *priorityRecordingFetcher) CheckBodyPart(ctx context.Context, id string, _ []string, prio core.Priority, _ core.ArticlePart) error {
	f.mu.Lock()
	f.bodyBG = append(f.bodyBG, core.IsBackgroundCheck(ctx))
	f.bodyPrios = append(f.bodyPrios, prio)
	f.totalCalls++
	forced, hasForced := f.bodyErr[id]
	f.mu.Unlock()
	if hasForced {
		return forced
	}
	_, _, err := f.fakeFetcher.BodyPart(ctx, id, nil, prio)
	return err
}

func (f *priorityRecordingFetcher) BodyPart(ctx context.Context, id string, groups []string, prio core.Priority) ([]byte, core.ArticlePart, error) {
	f.mu.Lock()
	f.bodyBG = append(f.bodyBG, core.IsBackgroundCheck(ctx))
	f.bodyPrios = append(f.bodyPrios, prio)
	f.totalCalls++
	forced, hasForced := f.bodyErr[id]
	f.mu.Unlock()
	if hasForced {
		return nil, core.ArticlePart{}, forced
	}
	return f.fakeFetcher.BodyPart(ctx, id, groups, prio)
}

func (f *priorityRecordingFetcher) Body(ctx context.Context, id string, groups []string, prio core.Priority) ([]byte, error) {
	b, _, err := f.BodyPart(ctx, id, groups, prio)
	return b, err
}

func buildMultiFileNZB(f *fakeFetcher, fileCount, segsPerFile int, ext string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
	segPayload := make([]byte, 1024)
	for fi := 0; fi < fileCount; fi++ {
		name := fmt.Sprintf("Track_%03d.%s", fi+1, ext)
		fmt.Fprintf(&b, `<file poster="p" date="1700000000" subject="%s (1/%d)"><groups><group>alt.binaries.test</group></groups><segments>`, name, segsPerFile)
		for si := 0; si < segsPerFile; si++ {
			id := fmt.Sprintf("f%d-s%d@test", fi, si)
			f.articles[id] = fakeArticle{
				data: segPayload,
				part: core.ArticlePart{
					Part:     si + 1,
					Total:    segsPerFile,
					FileSize: int64(segsPerFile * len(segPayload)),
					Begin:    int64(si*len(segPayload) + 1),
					End:      int64((si + 1) * len(segPayload)),
				},
			}
			fmt.Fprintf(&b, `<segment bytes="%d" number="%d">%s</segment>`, len(segPayload), si+1, id)
		}
		b.WriteString(`</segments></file>`)
	}
	b.WriteString(`</nzb>`)
	return []byte(b.String())
}

func TestCheckArticlesBoundsAndBackgroundPriority(t *testing.T) {
	tests := []struct {
		name          string
		fileCount     int
		segsPerFile   int
		ext           string
		healthSamples int
	}{
		{
			name:          "large single video file caps checked articles",
			fileCount:     1,
			segsPerFile:   500,
			ext:           "mkv",
			healthSamples: 64,
		},
		{
			name:          "many non-video album tracks cap checked articles",
			fileCount:     100,
			segsPerFile:   20,
			ext:           "flac",
			healthSamples: 12,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := newPriorityRecordingFetcher()
			doc := buildMultiFileNZB(ff.fakeFetcher, tc.fileCount, tc.segsPerFile, tc.ext)
			e := New(ff, nil, Options{HealthSamples: tc.healthSamples}, nil)
			defer e.Close()

			// Pass a plain foreground context; CheckArticles must still run
			// every NNTP probe at background/prefetch priority.
			res, err := e.CheckArticles(context.Background(), "rel-bound", doc)
			if err != nil {
				t.Fatalf("CheckArticles: %v", err)
			}
			if !res.Available {
				t.Fatalf("Available = false (reason=%q), want true", res.Reason)
			}
			if res.Missing != 0 {
				t.Fatalf("Missing = %d, want 0", res.Missing)
			}
			if res.Checked == 0 || res.Checked > MaxArticleCheckSamples {
				t.Fatalf("Checked = %d, want in [1, %d]", res.Checked, MaxArticleCheckSamples)
			}

			ff.mu.Lock()
			defer ff.mu.Unlock()
			if ff.totalCalls == 0 || ff.totalCalls > MaxArticleCheckSamples {
				t.Fatalf("total NNTP calls = %d, want in [1, %d]", ff.totalCalls, MaxArticleCheckSamples)
			}
			for i, bg := range ff.statBG {
				if !bg {
					t.Fatalf("STAT call %d ran without background check context", i)
				}
			}
			if len(ff.bodyPrios) == 0 {
				t.Fatalf("expected at least one BODY probe")
			}
			for i, prio := range ff.bodyPrios {
				if prio != core.PriorityPrefetch {
					t.Fatalf("BODY probe %d priority = %v, want PriorityPrefetch", i, prio)
				}
				if !ff.bodyBG[i] {
					t.Fatalf("BODY probe %d ran without background check context", i)
				}
			}
		})
	}
}

func TestCheckArticlesMissingAndErrors(t *testing.T) {
	t.Run("missing article on STAT", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		doc := buildMultiFileNZB(ff.fakeFetcher, 1, 30, "mkv")
		ff.missing["f0-s0@test"] = true

		e := New(ff, nil, Options{HealthSamples: 12}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-miss-stat", doc)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if res.Available {
			t.Fatalf("Available = true, want false")
		}
		if res.Missing < 1 || res.Checked < res.Missing || res.Checked > MaxArticleCheckSamples {
			t.Fatalf("unexpected counts: %+v", res)
		}
		if res.Reason != "articles_missing" {
			t.Fatalf("Reason = %q, want articles_missing", res.Reason)
		}
	})

	t.Run("mid-file DMCA takedown caught by BODY probe", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		doc := buildMultiFileNZB(ff.fakeFetcher, 1, 30, "mkv")
		// STAT reports all present, but mid-file BODY probe returns missing.
		for si := 0; si < 30; si++ {
			ff.bodyErr[fmt.Sprintf("f0-s%d@test", si)] = fmt.Errorf("451 dmca: %w", core.ErrArticleMissing)
		}

		e := New(ff, nil, Options{HealthSamples: 12}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-miss-body", doc)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if res.Available || res.Missing < 1 || res.Checked < res.Missing {
			t.Fatalf("unexpected verdict: %+v", res)
		}
		if res.Reason != "articles_missing" {
			t.Fatalf("Reason = %q, want articles_missing (no raw provider/message-id leak)", res.Reason)
		}
	})

	t.Run("NZB XML gap fails without NNTP calls", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		gapNZB := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="p" date="1700000000" subject="Album.flac (1/3)">
    <groups><group>alt.binaries.test</group></groups>
    <segments>
      <segment bytes="1024" number="1">g1@test</segment>
      <segment bytes="1024" number="3">g3@test</segment>
    </segments>
  </file>
</nzb>`)
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-xml-gap", gapNZB)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if res.Available || res.Missing != 1 || res.Checked != 1 || res.Reason != "segment_absent_from_nzb" {
			t.Fatalf("unexpected XML gap verdict: %+v", res)
		}
		ff.mu.Lock()
		calls := ff.totalCalls
		ff.mu.Unlock()
		if calls != 0 {
			t.Fatalf("NNTP calls = %d, want 0 for XML segment gap", calls)
		}
	})

	t.Run("transient provider error is returned, not cached as missing", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		doc := buildMultiFileNZB(ff.fakeFetcher, 1, 6, "flac")
		providerErr := errors.New("nntp connection reset")
		ff.statErr = providerErr
		for si := 0; si < 6; si++ {
			ff.bodyErr[fmt.Sprintf("f0-s%d@test", si)] = providerErr
		}

		e := New(ff, nil, Options{HealthSamples: 6}, nil)
		defer e.Close()

		if _, err := e.CheckArticles(context.Background(), "rel-err", doc); err == nil {
			t.Fatal("expected transient provider error, got nil")
		}
	})
}

func buildNamedFilesNZB(f *fakeFetcher, names []string, segsPerFile int, segSize int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
	segPayload := make([]byte, segSize)
	for fi, name := range names {
		fmt.Fprintf(&b, `<file poster="p" date="1700000000" subject="&quot;%s&quot; yEnc (1/%d)"><groups><group>alt.binaries.test</group></groups><segments>`, name, segsPerFile)
		for si := 0; si < segsPerFile; si++ {
			id := fmt.Sprintf("named-%d-s%d@test", fi, si)
			if f != nil {
				f.articles[id] = fakeArticle{
					data: segPayload,
					part: core.ArticlePart{
						Part:     si + 1,
						Total:    segsPerFile,
						FileSize: int64(segsPerFile * segSize),
						Begin:    int64(si*segSize + 1),
						End:      int64((si + 1) * segSize),
					},
				}
			}
			fmt.Fprintf(&b, `<segment bytes="%d" number="%d">%s</segment>`, segSize, si+1, id)
		}
		b.WriteString(`</segments></file>`)
	}
	b.WriteString(`</nzb>`)
	return []byte(b.String())
}

func TestCheckArticlesMissingArchiveVolumes(t *testing.T) {
	tests := []struct {
		name        string
		files       []string
		wantMissing int
	}{
		{
			name:        "new-style RAR skipping part14 and part15",
			files:       []string{"Release.part01.rar", "Release.part02.rar", "Release.part03.rar", "Release.part04.rar", "Release.part05.rar", "Release.part06.rar", "Release.part07.rar", "Release.part08.rar", "Release.part09.rar", "Release.part10.rar", "Release.part11.rar", "Release.part12.rar", "Release.part13.rar", "Release.part16.rar", "Release.part17.rar"},
			wantMissing: 2,
		},
		{
			name:        "new-style RAR missing part01",
			files:       []string{"Release.part02.rar", "Release.part03.rar"},
			wantMissing: 1,
		},
		{
			name:        "old-style RAR missing r01",
			files:       []string{"Release.rar", "Release.r00", "Release.r02"},
			wantMissing: 1,
		},
		{
			name:        "7z volume gap",
			files:       []string{"Release.7z.001", "Release.7z.003"},
			wantMissing: 1,
		},
		{
			name:        "zip split missing closing zip",
			files:       []string{"Release.z01", "Release.z02"},
			wantMissing: 1,
		},
		{
			name:        "plain split volume gap",
			files:       []string{"Release.mkv.001", "Release.mkv.003"},
			wantMissing: 1,
		},
		{
			name:        "multi-set pack with one incomplete set",
			files:       []string{"Ep01.part01.rar", "Ep01.part02.rar", "Ep02.part01.rar", "Ep02.part03.rar", "Ep03.part01.rar", "Ep03.part02.rar"},
			wantMissing: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := newPriorityRecordingFetcher()
			doc := buildNamedFilesNZB(ff.fakeFetcher, tc.files, 2, 1024)
			e := New(ff, nil, Options{}, nil)
			defer e.Close()

			res, err := e.CheckArticles(context.Background(), "rel-missing-vol", doc)
			if err != nil {
				t.Fatalf("CheckArticles: %v", err)
			}
			if res.Available {
				t.Fatalf("Available = true, want false (%+v)", res)
			}
			if res.Reason != "volumes_missing" {
				t.Fatalf("Reason = %q, want volumes_missing", res.Reason)
			}
			if res.Missing != tc.wantMissing || res.Checked != tc.wantMissing {
				t.Fatalf("counts = (checked=%d missing=%d), want (%d, %d)", res.Checked, res.Missing, tc.wantMissing, tc.wantMissing)
			}
			ff.mu.Lock()
			calls := ff.totalCalls
			ff.mu.Unlock()
			if calls != 0 {
				t.Fatalf("NNTP calls = %d, want 0 when archive volumes are missing", calls)
			}
		})
	}

	t.Run("healthy dominant video ignores tiny incomplete subtitle archive", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
		bigSeg := make([]byte, 4096)
		ff.articles["vid-0@test"] = fakeArticle{data: bigSeg, part: core.ArticlePart{Part: 1, Total: 1, FileSize: 4096, Begin: 1, End: 4096}}
		b.WriteString(`<file poster="p" date="1700000000" subject="&quot;Movie.2024.mkv&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="4096" number="1">vid-0@test</segment></segments></file>`)
		smallSeg := make([]byte, 128)
		ff.articles["sub-0@test"] = fakeArticle{data: smallSeg, part: core.ArticlePart{Part: 1, Total: 1, FileSize: 128, Begin: 1, End: 128}}
		b.WriteString(`<file poster="p" date="1700000000" subject="&quot;Subs.part02.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="128" number="1">sub-0@test</segment></segments></file>`)
		b.WriteString(`</nzb>`)

		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-dom-video", []byte(b.String()))
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if !res.Available {
			t.Fatalf("Available = false (reason=%q), want true for dominant complete video", res.Reason)
		}
	})
}

func postByteSplitNZB(t testing.TB, f *fakeFetcher, partSize int, raw []byte, chunks []struct {
	name       string
	start, end int
}) []byte {
	t.Helper()
	// Compute total yEnc part count across chunks (matching a single concatenated post).
	totalParts := 0
	for _, ch := range chunks {
		n := (ch.end - ch.start + partSize - 1) / partSize
		totalParts += n
	}
	// Map each chunk's starting 1-based global part number by byte offset.
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
	for ci, ch := range chunks {
		segCount := (ch.end - ch.start + partSize - 1) / partSize
		firstGlobalPart := ch.start/partSize + 1
		fmt.Fprintf(&b, `<file poster="p" date="1700000000" subject="&quot;%s&quot; yEnc (1/%d)"><groups><group>alt.binaries.test</group></groups><segments>`, ch.name, segCount)
		for si := 0; si < segCount; si++ {
			off := ch.start + si*partSize
			end := min(off+partSize, ch.end)
			id := fmt.Sprintf("split-%d-s%d@test", ci, si)
			f.articles[id] = fakeArticle{
				data: append([]byte(nil), raw[off:end]...),
				part: core.ArticlePart{
					Part:     firstGlobalPart + si,
					Total:    totalParts,
					FileSize: int64(len(raw)),
					Begin:    int64(off + 1),
					End:      int64(end),
				},
			}
			fmt.Fprintf(&b, `<segment bytes="%d" number="%d">%s</segment>`, end-off, si+1, id)
		}
		b.WriteString(`</segments></file>`)
	}
	b.WriteString(`</nzb>`)
	return []byte(b.String())
}

func TestCheckArticlesRARHeaders(t *testing.T) {
	assertBackgroundBounded := func(t *testing.T, ff *priorityRecordingFetcher, maxCalls int) {
		t.Helper()
		ff.mu.Lock()
		defer ff.mu.Unlock()
		if ff.totalCalls == 0 || ff.totalCalls > maxCalls {
			t.Fatalf("total NNTP calls = %d, want in [1, %d]", ff.totalCalls, maxCalls)
		}
		for i, bg := range ff.statBG {
			if !bg {
				t.Fatalf("STAT call %d ran without background check context", i)
			}
		}
		for i, prio := range ff.bodyPrios {
			if prio != core.PriorityPrefetch {
				t.Fatalf("BODY call %d priority = %v, want PriorityPrefetch", i, prio)
			}
			if !ff.bodyBG[i] {
				t.Fatalf("BODY call %d ran without background check context", i)
			}
		}
	}

	t.Run("healthy store-mode RAR5 passes within budget at background priority", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		video := mkvBytes(71, 600_000)
		vols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video}}, archivetest.RAROptions{Version: 5, VolumeSize: 150_000})
		doc := postVolumes(t, ff.fakeFetcher, 30_000, vols, newStyle("Movie.2026"))
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-rar-ok", doc)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if !res.Available || res.Missing != 0 {
			t.Fatalf("got %+v, want Available=true Missing=0", res)
		}
		assertBackgroundBounded(t, ff, MaxArticleCheckSamples)
	})

	for _, tc := range []struct {
		name    string
		version int
		method  int
		solid   bool
	}{
		{"compressed RAR5 (m3)", 5, 3, false},
		{"compressed RAR4 (m3)", 4, 3, false},
		{"compressed solid RAR5", 5, 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ff := newPriorityRecordingFetcher()
			video := mkvBytes(72, 400_000)
			vols := buildRAR(t, []archivetest.RARFile{
				{Name: "Movie.2026.mp4", Data: video, Method: tc.method, Solid: tc.solid},
			}, archivetest.RAROptions{Version: tc.version, VolumeSize: 120_000, NewNumbering: true})
			doc := postVolumes(t, ff.fakeFetcher, 30_000, vols, newStyle("Movie.2026"))
			e := New(ff, nil, Options{}, nil)
			defer e.Close()

			res, err := e.CheckArticles(context.Background(), "rel-rar-comp", doc)
			if err != nil {
				t.Fatalf("CheckArticles: %v", err)
			}
			if res.Available || res.Reason != "archive_compressed" {
				t.Fatalf("got %+v, want Available=false Reason=archive_compressed", res)
			}
			// Fast-fail in 1-2 header articles without scanning the rest of the set.
			assertBackgroundBounded(t, ff, 2)
		})
	}

	t.Run("first volume missing (part00 omitted so part01 is RAR vol 2)", func(t *testing.T) {
		for _, ver := range []int{5, 4} {
			t.Run(fmt.Sprintf("rar%d", ver), func(t *testing.T) {
				ff := newPriorityRecordingFetcher()
				video := mkvBytes(73, 450_000)
				vols := buildRAR(t, []archivetest.RARFile{
					{Name: "Movie.2026.mp4", Data: video},
				}, archivetest.RAROptions{Version: ver, VolumeSize: 120_000, NewNumbering: true})
				if len(vols) < 3 {
					t.Fatalf("only %d volumes", len(vols))
				}
				// Drop volume 0 and name remaining volumes part01..partN (as when 0-based part00.rar is missing).
				doc := postVolumes(t, ff.fakeFetcher, 30_000, vols[1:], newStyle("Movie.2026"))
				e := New(ff, nil, Options{}, nil)
				defer e.Close()

				res, err := e.CheckArticles(context.Background(), "rel-rar-missing-first", doc)
				if err != nil {
					t.Fatalf("CheckArticles: %v", err)
				}
				if res.Available || res.Reason != "archive_first_volume_missing" {
					t.Fatalf("got %+v, want Available=false Reason=archive_first_volume_missing", res)
				}
				assertBackgroundBounded(t, ff, MaxArticleCheckSamples)
			})
		}
	})

	t.Run("large 40-volume RAR5 missing first volume stays within MaxArticleCheckSamples", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		video := mkvBytes(74, 41*2000)
		vols := buildRAR(t, []archivetest.RARFile{{Name: "Big.mp4", Data: video}}, archivetest.RAROptions{Version: 5, VolumeSize: 2000})
		if len(vols) < 40 {
			t.Fatalf("only %d volumes", len(vols))
		}
		doc := postVolumes(t, ff.fakeFetcher, 1000, vols[1:], newStyle("Big"))
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-rar-large-missing-first", doc)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if res.Available || res.Reason != "archive_first_volume_missing" {
			t.Fatalf("got %+v, want Available=false Reason=archive_first_volume_missing", res)
		}
		assertBackgroundBounded(t, ff, MaxArticleCheckSamples)
	})

	t.Run("part01.rar starting 66 MB in with no offset-0 volume reports archive_parts_out_of_order", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		segPayload := make([]byte, 258048)
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`)
		for i := 0; i < 2; i++ {
			id := fmt.Sprintf("shifted-%d@test", i)
			begin := int64(66142681 + i*len(segPayload))
			ff.articles[id] = fakeArticle{
				data: segPayload,
				part: core.ArticlePart{
					Part:     257 + i,
					Total:    400,
					FileSize: 100_000_000,
					Begin:    begin,
					End:      begin + int64(len(segPayload)) - 1,
				},
			}
			fmt.Fprintf(&b, `<file poster="p" date="1700000000" subject="&quot;Movie.part%02d.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file>`, i+1, len(segPayload), id)
		}
		b.WriteString(`</nzb>`)
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-rar-66mb", []byte(b.String()))
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if res.Available || res.Reason != "archive_parts_out_of_order" {
			t.Fatalf("got %+v, want Available=false Reason=archive_parts_out_of_order", res)
		}
		assertBackgroundBounded(t, ff, MaxArticleCheckSamples)
	})

	t.Run("out-of-order RAR5 volumes are reordered before accepting or rejecting", func(t *testing.T) {
		video := mkvBytes(75, 450_000)
		// Store-mode out-of-order [vol2, vol0, vol1] passes after reordering.
		ff := newPriorityRecordingFetcher()
		vols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video}}, archivetest.RAROptions{Version: 5, VolumeSize: 160_000})
		if len(vols) != 3 {
			t.Fatalf("got %d volumes, want 3", len(vols))
		}
		scrambled := [][]byte{vols[2], vols[0], vols[1]}
		doc := postVolumes(t, ff.fakeFetcher, 40_000, scrambled, newStyle("Movie.2026"))
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-rar-reorder-ok", doc)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if !res.Available || res.Missing != 0 {
			t.Fatalf("got %+v, want Available=true after reordering", res)
		}
		assertBackgroundBounded(t, ff, MaxArticleCheckSamples)

		// Compressed out-of-order [vol1, vol0] still detects archive_compressed on real vol 0.
		ff2 := newPriorityRecordingFetcher()
		compVols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video, Method: 3}}, archivetest.RAROptions{Version: 5, VolumeSize: 250_000})
		doc2 := postVolumes(t, ff2.fakeFetcher, 40_000, [][]byte{compVols[1], compVols[0]}, newStyle("Movie.2026"))
		e2 := New(ff2, nil, Options{}, nil)
		defer e2.Close()

		res2, err := e2.CheckArticles(context.Background(), "rel-rar-reorder-comp", doc2)
		if err != nil {
			t.Fatalf("CheckArticles compressed: %v", err)
		}
		if res2.Available || res2.Reason != "archive_compressed" {
			t.Fatalf("got %+v, want Available=false Reason=archive_compressed", res2)
		}
	})

	t.Run("out-of-order yEnc byte-split RAR reorders when contiguous and rejects on gap", func(t *testing.T) {
		video := mkvBytes(76, 180_000)
		single := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video}}, archivetest.RAROptions{Version: 5})
		raw := single[0]
		mid := (len(raw) / 2 / 30_000) * 30_000

		// Contiguous chunks posted out of order: part01 is [mid, end), part02 is [0, mid).
		ff := newPriorityRecordingFetcher()
		doc := postByteSplitNZB(t, ff.fakeFetcher, 30_000, raw, []struct {
			name       string
			start, end int
		}{
			{"Movie.2026.part01.rar", mid, len(raw)},
			{"Movie.2026.part02.rar", 0, mid},
		})
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-yenc-reorder-ok", doc)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if !res.Available || res.Missing != 0 {
			t.Fatalf("got %+v, want Available=true for contiguous reordered yEnc split", res)
		}
		assertBackgroundBounded(t, ff, MaxArticleCheckSamples)

		// Gap between chunks ([0, mid-30000) and [mid, len(raw))) must reject.
		ffGap := newPriorityRecordingFetcher()
		docGap := postByteSplitNZB(t, ffGap.fakeFetcher, 30_000, raw, []struct {
			name       string
			start, end int
		}{
			{"Movie.2026.part01.rar", mid, len(raw)},
			{"Movie.2026.part02.rar", 0, mid - 30_000},
		})
		eGap := New(ffGap, nil, Options{}, nil)
		defer eGap.Close()

		resGap, err := eGap.CheckArticles(context.Background(), "rel-yenc-gap", docGap)
		if err != nil {
			t.Fatalf("CheckArticles gap: %v", err)
		}
		if resGap.Available || resGap.Reason != "archive_parts_out_of_order" {
			t.Fatalf("got %+v, want Available=false Reason=archive_parts_out_of_order", resGap)
		}
	})

	t.Run("compressed non-video RAR (FLAC album) is not falsely rejected", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		track := randBytes(77, 120_000)
		vols := buildRAR(t, []archivetest.RARFile{
			{Name: "01 - Track.flac", Data: track, Method: 3},
		}, archivetest.RAROptions{Version: 5})
		doc := postVolumes(t, ff.fakeFetcher, 30_000, vols, newStyle("Album"))
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		res, err := e.CheckArticles(context.Background(), "rel-flac-comp", doc)
		if err != nil {
			t.Fatalf("CheckArticles: %v", err)
		}
		if !res.Available {
			t.Fatalf("non-video compressed RAR rejected: %+v", res)
		}
	})

	t.Run("transient provider error during RAR header read is returned", func(t *testing.T) {
		ff := newPriorityRecordingFetcher()
		video := mkvBytes(78, 120_000)
		vols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.mkv", Data: video}}, archivetest.RAROptions{Version: 5})
		doc := postVolumes(t, ff.fakeFetcher, 40_000, vols, newStyle("Movie"))
		ff.failAll = true
		e := New(ff, nil, Options{}, nil)
		defer e.Close()

		if _, err := e.CheckArticles(context.Background(), "rel-rar-transient", doc); err == nil {
			t.Fatal("expected transient error, got nil")
		}
	})
}
