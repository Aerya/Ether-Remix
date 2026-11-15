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
