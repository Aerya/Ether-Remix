package vault

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/store"
)

const fixture = `<nzb><file subject="Movie.2020.mkv" poster="A"><groups><group>test</group></groups><segments><segment bytes="512" number="1">aa@a</segment><segment bytes="510" number="2">bb@b</segment><segment bytes="510" number="2">fill@c</segment></segments></file></nzb>`

func TestPackRecovery(t *testing.T) {
	ctx := context.Background()
	o := Options{Dir: t.TempDir(), DB: store.OpenTest(t)}
	s, e := Open(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	p := PutRequest{NZB: []byte(fixture), Release: core.Release{ID: "first", IndexerID: "a"}}
	k, e := s.Put(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.Stats(ctx)
	if e != nil {
		t.Fatal(e)
	}
	p.Release.IndexerID = "b"
	again, e := s.Put(ctx, p)
	if e != nil || again != k {
		t.Fatal("dedupe", e)
	}
	after, e := s.Stats(ctx)
	if e != nil || before.Bytes != after.Bytes {
		t.Fatal("duplicate payload", e)
	}
	l, e := s.(*library).loc(ctx, o.DB, k)
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(filepath.Join(o.Dir, "packs", l.pack), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Write([]byte("EV01\x03"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	alias, ok, e := s.ByAlias(ctx, "b", "first")
	if !ok || alias != k || e != nil {
		t.Fatal("alias", e)
	}
	r, e := s.Get(ctx, k)
	if e != nil {
		t.Fatal(e)
	}
	want, _ := nzb.Parse(p.NZB)
	if !reflect.DeepEqual(want, r.NZB) {
		t.Fatalf("round trip changed document: %#v vs %#v", want, r.NZB)
	}
	if _, e = nzb.Parse(r.XML); e != nil {
		t.Fatal(e)
	}
}

func TestDecodeRefreshesStaleSubjectDerivedName(t *testing.T) {
	raw := []byte(`<nzb><file subject="Some Show Episode Title 4k.mp4 (1/2)" poster="A"><groups><group>test</group></groups><segments><segment bytes="512" number="1">aa@a</segment></segments></file></nzb>`)
	doc, err := nzb.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy vault record written before NameFromSubject preserved
	// spaces in unquoted filenames (no SubjectName persisted, stale Name).
	doc.Files[0].Name = "4k.mp4"
	doc.Files[0].SubjectName = ""
	encoded, err := Encode(&Record{Key: "k", Kind: KindUsenet, NZB: doc})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.NZB.Files[0]
	if got.Name != "Some Show Episode Title 4k.mp4" || got.SubjectName != "Some Show Episode Title 4k.mp4" {
		t.Fatalf("decoded file = name=%q subjectName=%q", got.Name, got.SubjectName)
	}
	xmlOut, ok := decoded.NZB.Subset(func(*nzb.File) bool { return true })
	if !ok {
		t.Fatal("Subset failed")
	}
	reparsed, err := nzb.Parse(xmlOut)
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Files[0].Name != "Some Show Episode Title 4k.mp4" {
		t.Fatalf("re-encoded XML name = %q, want %q", reparsed.Files[0].Name, "Some Show Episode Title 4k.mp4")
	}
}
func FuzzDecode(f *testing.F) {
	d, _ := nzb.Parse([]byte(fixture))
	b, e := Encode(&Record{Kind: KindUsenet, NZB: d})
	if e != nil {
		f.Fatal(e)
	}
	f.Add(b)
	dec, e := decoder()
	if e != nil {
		f.Fatal(e)
	}
	raw, e := dec.DecodeAll(b, nil)
	if e != nil {
		f.Fatal(e)
	}
	f.Add(raw)
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = Decode(b); _, _ = decodeRaw(b) })
}
