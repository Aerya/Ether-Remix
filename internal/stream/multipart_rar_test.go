package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/nzb"
)

func TestSplitNamedRARVolumes(t *testing.T) {
	t.Run("multivolume rar named .rar.001", func(t *testing.T) {
		f := newFakeFetcher()
		video := mkvBytes(201, 140_000)
		vols := buildRAR(t, []archivetest.RARFile{{Name: "The.Runner.2026.mkv", Data: video}},
			archivetest.RAROptions{Version: 5, VolumeSize: 40_000})
		if len(vols) < 3 {
			t.Fatalf("expected >=3 volumes, got %d", len(vols))
		}
		var files []postFile
		for i, v := range vols {
			files = append(files, postFile{name: fmt.Sprintf("The.Runner.2026.rar.%03d", i+1), data: v})
		}
		doc := post(t, f, 20_000, files...)
		e := newTestEngine(t, f, nil, Options{})
		src := openSource(t, e, "split-rar-mv", doc)
		got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
		if err != nil || !bytes.Equal(got, video) {
			t.Fatalf("read = err=%v len=%d want=%d", err, len(got), len(video))
		}
	})

	t.Run("byte-split single rar named .rar.001", func(t *testing.T) {
		f := newFakeFetcher()
		video := mkvBytes(202, 130_000)
		single := buildRAR(t, []archivetest.RARFile{{Name: "The.Runner.2026.mkv", Data: video}},
			archivetest.RAROptions{Version: 5})
		if len(single) != 1 {
			t.Fatalf("expected 1 rar, got %d", len(single))
		}
		raw := single[0]
		const chunk = 45_000
		var files []postFile
		for off, i := 0, 1; off < len(raw); off, i = off+chunk, i+1 {
			end := min(off+chunk, len(raw))
			files = append(files, postFile{name: fmt.Sprintf("The.Runner.2026.rar.%03d", i), data: raw[off:end]})
		}
		doc := post(t, f, 20_000, files...)
		e := newTestEngine(t, f, nil, Options{})
		src := openSource(t, e, "split-rar-concat", doc)
		got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
		if err != nil || !bytes.Equal(got, video) {
			t.Fatalf("read = err=%v len=%d want=%d", err, len(got), len(video))
		}
	})
}

func TestDistinctNamedMultiVolumeRAR(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names func(n int) []string
	}{
		{
			name: "numeric prefix before .rar",
			names: func(n int) []string {
				out := make([]string, n)
				for i := range out {
					out[i] = fmt.Sprintf("The.Runner.2026.%03d.rar", i+1)
				}
				return out
			},
		},
		{
			name: "obfuscated distinct .rar names",
			names: func(n int) []string {
				out := make([]string, n)
				for i := range out {
					out[i] = fmt.Sprintf("%s.rar", obfName(40+i))
				}
				return out
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher()
			video := mkvBytes(203, 140_000)
			vols := buildRAR(t, []archivetest.RARFile{{Name: "The.Runner.2026.mkv", Data: video}},
				archivetest.RAROptions{Version: 5, VolumeSize: 40_000})
			names := tc.names(len(vols))
			// Post out of order to verify header-based regrouping.
			perm := rand.New(rand.NewPCG(7, 9)).Perm(len(vols))
			var files []postFile
			for _, idx := range perm {
				files = append(files, postFile{name: names[idx], data: vols[idx]})
			}
			doc := post(t, f, 20_000, files...)
			e := newTestEngine(t, f, nil, Options{})
			src := openSource(t, e, "distinct-rar-"+tc.name, doc)
			got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
			if err != nil || !bytes.Equal(got, video) {
				t.Fatalf("read = err=%v len=%d want=%d", err, len(got), len(video))
			}
		})
	}
}

func TestMovieHintSingleFeatureWithShortClipAndDiscStreams(t *testing.T) {
	doc, err := nzb.Parse([]byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="ether-movie">{"Titles":["The Runner"],"Year":2026}</meta></head><file subject="&quot;rnr.rar&quot; yEnc" date="1" poster="test"><groups><group>test</group></groups><segments><segment bytes="100" number="1">test</segment></segments></file></nzb>`))
	if err != nil {
		t.Fatal(err)
	}
	b := &builder{rel: &release{doc: doc}}

	// Scene-abbreviated feature plus a short non-sample clip must pick the feature.
	shortClip := []archive.Member{
		{Name: "rnr-2026-1080p.mkv", Size: 4_000_000_000},
		{Name: "promo-clip.mkv", Size: 120_000_000},
	}
	if got := b.chooseMember(shortClip); got == nil || got.Name != "rnr-2026-1080p.mkv" {
		t.Fatalf("feature + short clip selected %+v", got)
	}

	// Disc stream members inside a RAR must pick the largest stream rather than rejecting all.
	discStreams := []archive.Member{
		{Name: "BDMV/STREAM/00001.m2ts", Size: 20_000_000_000},
		{Name: "BDMV/STREAM/00002.m2ts", Size: 10_000_000_000},
	}
	if got := b.chooseMember(discStreams); got == nil || got.Name != "BDMV/STREAM/00001.m2ts" {
		t.Fatalf("disc streams selected %+v", got)
	}

	// End-to-end multipart RAR with MovieHint and a short unlabelled clip.
	f := newFakeFetcher()
	video := mkvBytes(204, 150_000)
	clip := mkvBytes(205, 10_000)
	vols := buildRAR(t, []archivetest.RARFile{
		{Name: "rnr-2026-1080p.mkv", Data: video},
		{Name: "intro.mkv", Data: clip},
	}, archivetest.RAROptions{Version: 5, VolumeSize: 45_000})
	var files []postFile
	for i, v := range vols {
		files = append(files, postFile{name: fmt.Sprintf("rnr.part%02d.rar", i+1), data: v})
	}
	raw := post(t, f, 20_000, files...)
	hinted, err := debrid.EpisodeNZB(raw, &core.ReleaseTarget{
		MovieTitles: []string{"The Runner"},
		MovieYear:   2026,
	})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(t, f, nil, Options{})
	src, err := e.Open(context.Background(), "movie-hint-clip", hinted)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if src.Name() != "rnr-2026-1080p.mkv" || src.Size() != int64(len(video)) {
		t.Fatalf("opened %q (%d bytes), want rnr-2026-1080p.mkv (%d)", src.Name(), src.Size(), len(video))
	}
}

func TestObfuscatedLargeRARWithoutNamedPar2(t *testing.T) {
	for _, withObfPar2 := range []bool{true, false} {
		name := "no-par2"
		if withObfPar2 {
			name = "obfuscated-par2"
		}
		t.Run(name, func(t *testing.T) {
			f := newFakeFetcher()
			const volSize = 40_000
			const n = 70
			video := mkvBytes(206, n*volSize-volSize/2)
			vols := buildRAR(t, []archivetest.RARFile{{Name: "The.Runner.2026.mkv", Data: video}},
				archivetest.RAROptions{Version: 5, VolumeSize: volSize})
			if len(vols) != n {
				t.Fatalf("built %d volumes, want %d", len(vols), n)
			}
			order := rand.New(rand.NewPCG(11, 13)).Perm(len(vols))
			var extras []postFile
			if withObfPar2 {
				idx := par2Index(65_536, rarPar2Files(vols)...)
				// Obfuscated PAR2 index without ".par2" extension, smaller than every volume.
				extras = append(extras, postFile{name: obfName(999), data: idx})
			}
			doc := postShuffled(t, f, vols, order, extras...)
			e := newTestEngine(t, f, nil, Options{})
			src := openSource(t, e, "obf-large-"+name, doc)
			if src.Name() != "The.Runner.2026.mkv" || src.Size() != int64(len(video)) {
				t.Fatalf("name=%q size=%d want=%d", src.Name(), src.Size(), len(video))
			}
			checkRandomReads(t, src, video, 6, 40)
		})
	}
}
