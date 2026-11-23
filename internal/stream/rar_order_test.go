package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestOpenReordersRARByVolumeNumber(t *testing.T) {
	video := mkvBytes(81, 450_000)

	cases := []struct {
		name    string
		version int
		volSize int64
		perm    func(n int) []int
	}{
		{
			name:    "rar5 first volume swapped to middle",
			version: 5,
			volSize: 160_000,
			perm:    func(n int) []int { return []int{2, 0, 1} },
		},
		{
			name:    "rar5 middle and last swapped while first is ok",
			version: 5,
			volSize: 160_000,
			perm:    func(n int) []int { return []int{0, 2, 1} },
		},
		{
			name:    "rar4 two volumes reversed",
			version: 4,
			volSize: 250_000,
			perm:    func(n int) []int { return []int{1, 0} },
		},
		{
			name:    "rar4 three volumes scrambled using tail/role ordering",
			version: 4,
			volSize: 160_000,
			perm:    func(n int) []int { return []int{2, 0, 1} },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher()
			vols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video}}, archivetest.RAROptions{
				Version:      tc.version,
				VolumeSize:   tc.volSize,
				NewNumbering: true,
			})
			order := tc.perm(len(vols))
			if len(order) != len(vols) {
				t.Fatalf("built %d volumes, test permutation has %d", len(vols), len(order))
			}
			scrambled := make([][]byte, len(vols))
			for i, idx := range order {
				scrambled[i] = vols[idx]
			}
			doc := postVolumes(t, f, 40_000, scrambled, newStyle("Movie.2026"))
			e := newTestEngine(t, f, nil, Options{})
			src := openSource(t, e, "reorder-vol", doc)
			if src.Name() != "Movie.2026.mkv" || src.Size() != int64(len(video)) {
				t.Fatalf("got name=%q size=%d, want Movie.2026.mkv %d", src.Name(), src.Size(), len(video))
			}
			if got := readAll(t, src, 32<<10); !bytes.Equal(got, video) {
				t.Fatal("sequential read mismatch after volume reorder")
			}
			checkRandomReads(t, src, video, 82, 64)
		})
	}

	t.Run("missing first volume rejects with ErrMissingVolume", func(t *testing.T) {
		f := newFakeFetcher()
		vols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video}}, archivetest.RAROptions{
			Version:      5,
			VolumeSize:   160_000,
			NewNumbering: true,
		})
		doc := postVolumes(t, f, 40_000, vols[1:], newStyle("Movie.2026"))
		e := newTestEngine(t, f, nil, Options{})
		_, err := e.Open(context.Background(), "missing-first", doc)
		if !errors.Is(err, archive.ErrMissingVolume) || !IsRejection(err) {
			t.Fatalf("Open err = %v, want ErrMissingVolume rejection", err)
		}
	})
}

func TestOpenReordersRARByYEncBegin(t *testing.T) {
	video := mkvBytes(83, 270_000)
	single := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video}}, archivetest.RAROptions{Version: 5})
	raw := single[0]
	const partSize = 30_000
	cut1 := 3 * partSize
	cut2 := 6 * partSize
	if cut2 >= len(raw) {
		t.Fatalf("archive too small (%d) for 3 chunks", len(raw))
	}

	t.Run("three contiguous chunks posted out of order stream cleanly", func(t *testing.T) {
		f := newFakeFetcher()
		// Post chunks in order [mid, tail, head] as part01..part03.rar.
		doc := postByteSplitNZB(t, f, partSize, raw, []struct {
			name       string
			start, end int
		}{
			{"Movie.2026.part01.rar", cut1, cut2},
			{"Movie.2026.part02.rar", cut2, len(raw)},
			{"Movie.2026.part03.rar", 0, cut1},
		})
		e := newTestEngine(t, f, nil, Options{})
		src := openSource(t, e, "reorder-yenc", doc)
		if src.Name() != "Movie.2026.mkv" || src.Size() != int64(len(video)) {
			t.Fatalf("got name=%q size=%d, want Movie.2026.mkv %d", src.Name(), src.Size(), len(video))
		}
		if got := readAll(t, src, 24<<10); !bytes.Equal(got, video) {
			t.Fatal("sequential read mismatch after yEnc reorder")
		}
		checkRandomReads(t, src, video, 84, 80)
	})

	t.Run("unorderable mid-file yEnc begin rejects with ErrCorrupt", func(t *testing.T) {
		f := newFakeFetcher()
		payload := make([]byte, 30_000)
		f.articles["s0@test"] = fakeArticle{
			data: payload,
			part: core.ArticlePart{Part: 200, Total: 400, FileSize: 12_000_000, Begin: 66_142_681, End: 66_172_680},
		}
		f.articles["s1@test"] = fakeArticle{
			data: payload,
			part: core.ArticlePart{Part: 201, Total: 400, FileSize: 12_000_000, Begin: 66_172_681, End: 66_202_680},
		}
		doc := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` +
			`<file poster="p" date="1" subject="&quot;Movie.part01.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="30000" number="1">s0@test</segment></segments></file>` +
			`<file poster="p" date="1" subject="&quot;Movie.part02.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="30000" number="1">s1@test</segment></segments></file>` +
			`</nzb>`))
		e := newTestEngine(t, f, nil, Options{})
		_, err := e.Open(context.Background(), "bad-yenc", doc)
		if !errors.Is(err, ErrCorrupt) || !IsRejection(err) {
			t.Fatalf("Open err = %v, want ErrCorrupt rejection", err)
		}
	})
}
