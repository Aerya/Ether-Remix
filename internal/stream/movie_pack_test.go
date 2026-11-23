package stream

import (
	"bytes"
	"context"
	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/nzb"
	"io"
	"testing"
)

func TestMoviePackMembers(t *testing.T) {
	doc, err := nzb.Parse([]byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="ether-movie">{"Titles":["The Mummy"],"Year":1999}</meta></head><file subject="&quot;The Mummy.rar&quot; yEnc" date="1" poster="test"><groups><group>test</group></groups><segments><segment bytes="100" number="1">test</segment></segments></file></nzb>`))
	if err != nil {
		t.Fatal(err)
	}
	b := &builder{rel: &release{doc: doc}}
	members := []archive.Member{{Name: "The Mummy Returns.mkv", Size: 9000}, {Name: "The Mummy (1959).mkv", Size: 8000}, {Name: "The Mummy (1999).mkv", Size: 5000}}
	if got := b.chooseMember(members); got == nil || got.Name != members[2].Name {
		t.Fatalf("selected %+v", got)
	}
	if got := b.chooseMember(members[:2]); got != nil {
		t.Fatalf("wrong collection member %+v", got)
	}
	for _, name := range []string{"movie.mkv", "odd.scene.name.mkv", "The Mummy Returns.mkv"} {
		files := []archive.Member{{Name: name, Size: 5000}, {Name: "sample.mkv", Size: 100}, {Name: "extras/Deleted.mkv", Size: 200}}
		if got := b.chooseMember(files); got == nil || got.Name != name {
			t.Fatalf("single feature %s: %+v", name, got)
		}
	}
}

func TestMovieSingleNZBFeature(t *testing.T) {
	for _, name := range []string{"movie.mkv", "odd.scene.name.mkv"} {
		t.Run(name, func(t *testing.T) {
			f := newFakeFetcher()
			data := mkvBytes(71, 1200000)
			raw := post(t, f, 100000, postFile{name: name, data: data}, postFile{name: "sample.mkv", data: mkvBytes(72, 100000)})
			doc, err := debrid.EpisodeNZB(raw, &core.ReleaseTarget{MovieTitles: []string{"The Mummy"}, MovieYear: 1999})
			if err != nil {
				t.Fatal(err)
			}
			e := newTestEngine(t, f, nil, Options{})
			src, err := e.Open(context.Background(), "single", doc)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("single movie %s: %v bytes=%d", name, err, len(got))
			}
		})
	}
}

func TestMoviePackLanguagePreferenceArchiveAndNZB(t *testing.T) {
	// 1. Archive members: prefer smaller matching-language member over larger sibling.
	doc, err := nzb.Parse([]byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="ether-movie">{"Titles":["The Mummy"],"Year":1999,"WantedLanguages":["french","english"],"OriginalLanguage":"english"}</meta></head><file subject="&quot;The.Mummy.1999.DUAL.rar&quot; yEnc" date="1" poster="test"><groups><group>test</group></groups><segments><segment bytes="100" number="1">test</segment></segments></file></nzb>`))
	if err != nil {
		t.Fatal(err)
	}
	b := &builder{rel: &release{doc: doc}}
	members := []archive.Member{
		{Name: "The.Mummy.1999.ENGLISH.1080p.mkv", Size: 6_000_000_000},
		{Name: "The.Mummy.1999.FRENCH.1080p.mkv", Size: 5_000_000_000},
	}
	if got := b.chooseMember(members); got == nil || got.Name != "The.Mummy.1999.FRENCH.1080p.mkv" {
		t.Fatalf("archive French preference: got %+v", got)
	}

	// 2. Multi-file Usenet NZB: prefer French file over larger English file, and
	// fall back to largest file when no candidate matches WantedLanguages.
	f := newFakeFetcher()
	enData := mkvBytes(81, 1_400_000)
	frData := mkvBytes(82, 1_000_000)
	raw := post(t, f, 100000,
		postFile{name: "The.Mummy.1999.ENGLISH.1080p.mkv", data: enData},
		postFile{name: "The.Mummy.1999.FRENCH.1080p.mkv", data: frData},
	)

	frNZB, err := debrid.EpisodeNZB(raw, &core.ReleaseTarget{
		MovieTitles:      []string{"The Mummy"},
		MovieYear:        1999,
		WantedLanguages:  []string{"french", "english"},
		OriginalLanguage: "english",
	})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(t, f, nil, Options{})
	src, err := e.Open(context.Background(), "dual-fr", frNZB)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
	if err != nil || !bytes.Equal(got, frData) {
		t.Fatalf("NZB French preference: name=%s err=%v bytes=%d", src.Name(), err, len(got))
	}

	jaNZB, err := debrid.EpisodeNZB(raw, &core.ReleaseTarget{
		MovieTitles:     []string{"The Mummy"},
		MovieYear:       1999,
		WantedLanguages: []string{"japanese"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srcFallback, err := e.Open(context.Background(), "dual-fallback", jaNZB)
	if err != nil {
		t.Fatal(err)
	}
	defer srcFallback.Close()
	if srcFallback.Name() != "The.Mummy.1999.ENGLISH.1080p.mkv" {
		t.Fatalf("NZB fallback to largest: got %s", srcFallback.Name())
	}
}
