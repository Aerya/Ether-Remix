package stream

import (
	"context"
	"fmt"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/par2/par2test"
)

func TestNameFilesWritesPar2NamesOfObfuscatedPack(t *testing.T) {
	f := newFakeFetcher()
	ep1 := mkvBytes(51, 400_000)
	ep2 := mkvBytes(52, 450_000)
	idx := par2Index(16_384,
		par2test.File{Name: "Show.S01E01.1080p.mkv", Data: ep1},
		par2test.File{Name: "Show.S01E02.1080p.mkv", Data: ep2},
	)
	doc := post(t, f, 100_000,
		postFile{name: obfName(1), data: ep1},
		postFile{name: obfName(2), data: ep2},
		postFile{name: "d41d8cd98f00b204e9800998ecf8427e.par2", data: idx},
	)
	e := newTestEngine(t, f, nil, Options{})
	out, err := e.NameFiles(context.Background(), "pack", doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := nzb.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, file := range parsed.Files {
		got[file.Name] = true
	}
	if !got["Show.S01E01.1080p.mkv"] || !got["Show.S01E02.1080p.mkv"] {
		t.Fatalf("names = %v", got)
	}
}

func TestNameFilesIsMemoized(t *testing.T) {
	f := newFakeFetcher()
	ep := mkvBytes(53, 400_000)
	idx := par2Index(16_384, par2test.File{Name: "Show.S01E01.1080p.mkv", Data: ep})
	doc := post(t, f, 100_000,
		postFile{name: obfName(1), data: ep},
		postFile{name: "d41d8cd98f00b204e9800998ecf8427e.par2", data: idx},
	)
	e := newTestEngine(t, f, nil, Options{})
	first, err := e.NameFiles(context.Background(), "pack", doc)
	if err != nil {
		t.Fatal(err)
	}
	calls := f.totalCalls()
	second, err := e.NameFiles(context.Background(), "pack", doc)
	if err != nil || string(first) != string(second) || f.totalCalls() != calls {
		t.Fatalf("second call refetched (%d -> %d calls) or differed: %v", calls, f.totalCalls(), err)
	}
}

func TestNameFilesNeverReordersArchiveVolumes(t *testing.T) {
	video := mkvBytes(91, 320_000)
	vols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2026.mkv", Data: video}}, archivetest.RAROptions{
		Version:      5,
		VolumeSize:   120_000,
		NewNumbering: true,
	})
	if len(vols) != 3 {
		t.Fatalf("got %d volumes, want 3", len(vols))
	}
	obfBase := obfName(9)

	t.Run("swapped yEnc names on obfuscated RAR set are reverted", func(t *testing.T) {
		f := newFakeFetcher()
		doc := postVolumes(t, f, 40_000, vols, newStyle(obfBase))
		// Corrupt the yEnc Name headers on part01/part02 to swap their volume numbers.
		for i, wantName := range []string{"Movie.2026.part02.rar", "Movie.2026.part01.rar", "Movie.2026.part03.rar"} {
			id := postID(fmt.Sprintf("%s.part%02d.rar", obfBase, i+1), 1)
			a := f.articles[id]
			a.part.Name = wantName
			f.articles[id] = a
		}
		e := newTestEngine(t, f, nil, Options{})
		out, err := e.NameFiles(context.Background(), "swap-yenc", doc)
		if err != nil {
			t.Fatalf("NameFiles: %v", err)
		}
		parsed, err := nzb.Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		sets := parsed.ArchiveSets()
		if len(sets) != 1 || len(sets[0].Volumes) != 3 {
			t.Fatalf("ArchiveSets = %d (vols=%d), want 1 set of 3", len(sets), len(sets[0].Volumes))
		}
		for i, vf := range sets[0].Volumes {
			want := fmt.Sprintf("%s.part%02d.rar", obfBase, i+1)
			if vf.Name != want {
				t.Fatalf("volume %d = %q, want original %q", i, vf.Name, want)
			}
		}
	})

	t.Run("colliding yEnc names and partial renames on obfuscated RAR set are reverted", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			yNames []string
		}{
			{"colliding plain rar", []string{"Movie.2026.rar", "Movie.2026.rar", "Movie.2026.rar"}},
			{"partial rename splits set", []string{"Movie.2026.part01.rar", "", ""}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newFakeFetcher()
				doc := postVolumes(t, f, 40_000, vols, newStyle(obfBase))
				for i, yn := range tc.yNames {
					id := postID(fmt.Sprintf("%s.part%02d.rar", obfBase, i+1), 1)
					a := f.articles[id]
					a.part.Name = yn
					f.articles[id] = a
				}
				e := newTestEngine(t, f, nil, Options{})
				out, err := e.NameFiles(context.Background(), tc.name, doc)
				if err != nil {
					t.Fatalf("NameFiles: %v", err)
				}
				parsed, err := nzb.Parse(out)
				if err != nil {
					t.Fatal(err)
				}
				sets := parsed.ArchiveSets()
				if len(sets) != 1 || len(sets[0].Volumes) != 3 || len(sets[0].Missing) != 0 {
					t.Fatalf("sets corrupted: count=%d vols=%d missing=%v", len(sets), len(sets[0].Volumes), sets[0].Missing)
				}
				for i, vf := range sets[0].Volumes {
					want := fmt.Sprintf("%s.part%02d.rar", obfBase, i+1)
					if vf.Name != want {
						t.Fatalf("volume %d = %q, want original %q", i, vf.Name, want)
					}
				}
			})
		}
	})

	t.Run("unknown RAR files whose yEnc names contradict RAR volume numbers are reverted", func(t *testing.T) {
		f := newFakeFetcher()
		doc := post(t, f, 40_000,
			postFile{name: obfName(11), data: vols[0]},
			postFile{name: obfName(12), data: vols[1]},
			postFile{name: obfName(13), data: vols[2]},
		)
		// yEnc headers invert vol 0 and vol 1.
		for i, yn := range []string{"Movie.2026.part02.rar", "Movie.2026.part01.rar", "Movie.2026.part03.rar"} {
			id := postID(obfName(11+i), 1)
			a := f.articles[id]
			a.part.Name = yn
			f.articles[id] = a
		}
		e := newTestEngine(t, f, nil, Options{})
		out, err := e.NameFiles(context.Background(), "contradict-rar", doc)
		if err != nil {
			t.Fatalf("NameFiles: %v", err)
		}
		parsed, err := nzb.Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		for i, pf := range parsed.Files {
			if pf.Name != obfName(11+i) {
				t.Fatalf("file %d renamed to %q despite contradicting RAR headers, want %q", i, pf.Name, obfName(11+i))
			}
		}
	})

	t.Run("non-zero yEnc begin segment never renames file", func(t *testing.T) {
		f := newFakeFetcher()
		payload := make([]byte, 30_000)
		f.articles["seg0@test"] = fakeArticle{
			data: payload,
			part: core.ArticlePart{Part: 1, Total: 1, FileSize: 0, Begin: 66_142_681, End: 66_172_680, Name: "Movie.2026.part02.rar"},
		}
		doc := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">`+
			`<file poster="p" date="1" subject="&quot;%s.part01.rar&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="30000" number="1">seg0@test</segment></segments></file></nzb>`, obfBase))
		e := newTestEngine(t, f, nil, Options{})
		out, err := e.NameFiles(context.Background(), "nonzero-begin", doc)
		if err != nil {
			t.Fatalf("NameFiles: %v", err)
		}
		parsed, err := nzb.Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		want := obfBase + ".part01.rar"
		if parsed.Files[0].Name != want {
			t.Fatalf("got %q, want unchanged %q", parsed.Files[0].Name, want)
		}
	})

	t.Run("valid order-preserving PAR2 rename of obfuscated RAR set succeeds", func(t *testing.T) {
		f := newFakeFetcher()
		idx := par2Index(16_384,
			par2test.File{Name: "Movie.2026.part01.rar", Data: vols[0]},
			par2test.File{Name: "Movie.2026.part02.rar", Data: vols[1]},
			par2test.File{Name: "Movie.2026.part03.rar", Data: vols[2]},
		)
		doc := postVolumes(t, f, 40_000, vols, newStyle(obfBase), postFile{name: "movie.par2", data: idx})
		e := newTestEngine(t, f, nil, Options{})
		out, err := e.NameFiles(context.Background(), "valid-rar-rename", doc)
		if err != nil {
			t.Fatalf("NameFiles: %v", err)
		}
		parsed, err := nzb.Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		sets := parsed.ArchiveSets()
		if len(sets) != 1 || len(sets[0].Volumes) != 3 {
			t.Fatalf("ArchiveSets = %+v", sets)
		}
		for i, vf := range sets[0].Volumes {
			want := fmt.Sprintf("Movie.2026.part%02d.rar", i+1)
			if vf.Name != want {
				t.Fatalf("volume %d = %q, want %q", i, vf.Name, want)
			}
		}
	})

	t.Run("ListFiles then NameFiles never reorders volumes on swapped yEnc names", func(t *testing.T) {
		f := newFakeFetcher()
		doc := postVolumes(t, f, 40_000, vols, newStyle(obfBase))
		for i, yn := range []string{"Movie.2026.part02.rar", "Movie.2026.part01.rar", "Movie.2026.part03.rar"} {
			id := postID(fmt.Sprintf("%s.part%02d.rar", obfBase, i+1), 1)
			a := f.articles[id]
			a.part.Name = yn
			f.articles[id] = a
		}
		e := newTestEngine(t, f, nil, Options{})
		listed, err := e.ListFiles(context.Background(), "list-then-name", doc)
		if err != nil {
			t.Fatalf("ListFiles: %v", err)
		}
		for i, lf := range listed {
			want := fmt.Sprintf("%s.part%02d.rar", obfBase, i+1)
			if lf.Path != want {
				t.Fatalf("ListFiles[%d].Path = %q, want %q", i, lf.Path, want)
			}
		}
		out, err := e.NameFiles(context.Background(), "list-then-name", doc)
		if err != nil {
			t.Fatalf("NameFiles: %v", err)
		}
		parsed, err := nzb.Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		sets := parsed.ArchiveSets()
		if len(sets) != 1 || len(sets[0].Volumes) != 3 {
			t.Fatalf("ArchiveSets = %+v", sets)
		}
		for i, vf := range sets[0].Volumes {
			want := fmt.Sprintf("%s.part%02d.rar", obfBase, i+1)
			if vf.Name != want {
				t.Fatalf("volume %d = %q, want %q", i, vf.Name, want)
			}
		}
	})
}
