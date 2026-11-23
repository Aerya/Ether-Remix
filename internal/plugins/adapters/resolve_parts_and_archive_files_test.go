package adapters

import (
	"context"
	"fmt"
	"testing"

	at "github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/stream"
)

func TestReleaseFromSourceForwardsTrackHintAndReleaseFilesPopulatesArchiveMembers(t *testing.T) {
	// 1. releaseFromSource forwards src.Release.Track into rel.Target.Track.
	rel, err := releaseFromSource(core.PlayableSource{
		Pattern: kindstream.PatternRelease,
		MIME:    "audio/flac",
		Release: &core.ReleaseHint{
			NZBURL: "https://idx.test/album.nzb",
			Track:  &core.TrackHint{Disc: 1, Number: 5, Title: "You Must Burn!"},
		},
	}, "You Must Burn!", "audio/*")
	if err != nil {
		t.Fatalf("releaseFromSource: %v", err)
	}
	if rel.Target == nil || rel.Target.Track == nil || rel.Target.Track.Disc != 1 || rel.Target.Track.Number != 5 || rel.Target.Track.Title != "You Must Burn!" {
		t.Fatalf("rel.Target = %+v", rel.Target)
	}

	// 2. ResolveSources(release_files) populates Files[0].Members for a scene RAR5 NZB!
	fetcher := &simpleMemFetcher{articles: map[string]struct {
		data []byte
		part core.ArticlePart
	}{}}
	vols, err := at.BuildRAR([]at.RARFile{
		{Name: "01-metallica-72_seasons-moonblood.flac", Data: append([]byte("fLaC"), make([]byte, 32*1024)...)},
		{Name: "05-metallica-you_must_burn-moonblood.flac", Data: append([]byte("fLaC"), make([]byte, 64*1024)...)},
	}, at.RAROptions{Version: 5, VolumeSize: 80_000})
	if err != nil {
		t.Fatalf("BuildRAR: %v", err)
	}

	var filesXML string
	for i, v := range vols {
		name := fmt.Sprintf("metallica.part%02d.rar", i+1)
		id := fmt.Sprintf("rarvol%d@test", i+1)
		fetcher.mu.Lock()
		fetcher.articles[id] = struct {
			data []byte
			part core.ArticlePart
		}{
			data: v,
			part: core.ArticlePart{Name: name, FileSize: int64(len(v)), Part: 1, Total: 1, Begin: 1, End: int64(len(v))},
		}
		fetcher.mu.Unlock()
		filesXML += fmt.Sprintf(`<file poster="p@test" date="1" subject="[%d/%d] - &quot;%s&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file>`, i+1, len(vols), name, len(v)+200, id)
	}
	nzbBytes := []byte(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` + filesXML + `</nzb>`)

	eng := stream.New(fetcher, nil, stream.Options{}, nil)
	t.Cleanup(func() { _ = eng.Close() })

	idx := &countingNZBIndexer{nzbs: map[string][]byte{"https://idx.test/album.nzb": nzbBytes}}
	kr := NewKindResolver(KindResolverDeps{
		Engine:  eng,
		Indexer: idx,
	})

	cands, err := kr.ResolveSources(context.Background(), "music.plug", core.SourceQuery{
		Kind: "release_files",
		Candidate: &core.SourceCandidate{
			NZBURL: "https://idx.test/album.nzb",
		},
	})
	if err != nil {
		t.Fatalf("ResolveSources(release_files): %v", err)
	}
	if len(cands) != 1 || len(cands[0].Files) < 1 {
		t.Fatalf("cands = %+v", cands)
	}
	members := cands[0].Files[0].Members
	if len(members) != 2 || members[1].Path != "05-metallica-you_must_burn-moonblood.flac" {
		t.Fatalf("Files[0].Members = %+v, want 2 scene FLAC members", members)
	}
}

func TestWrappedEngineListsArchiveMembersAndPreservesOptionalInterfaces(t *testing.T) {
	ctx := context.Background()
	fetcher := &simpleMemFetcher{articles: map[string]struct {
		data []byte
		part core.ArticlePart
	}{}}
	vols, err := at.BuildRAR([]at.RARFile{
		{Name: "01 - The Beachcomber.flac", Data: append([]byte("fLaC"), make([]byte, 32*1024)...)},
		{Name: "02 - Wait for the Moment.flac", Data: append([]byte("fLaC"), make([]byte, 48*1024)...)},
	}, at.RAROptions{Version: 5, VolumeSize: 60_000})
	if err != nil {
		t.Fatalf("BuildRAR: %v", err)
	}

	var filesXML string
	for i, v := range vols {
		name := fmt.Sprintf("vulfpeck.part%02d.rar", i+1)
		id := fmt.Sprintf("wrapped-rarvol%d@test", i+1)
		fetcher.mu.Lock()
		fetcher.articles[id] = struct {
			data []byte
			part core.ArticlePart
		}{
			data: v,
			part: core.ArticlePart{Name: name, FileSize: int64(len(v)), Part: 1, Total: 1, Begin: 1, End: int64(len(v))},
		}
		fetcher.mu.Unlock()
		filesXML += fmt.Sprintf(`<file poster="p@test" date="1" subject="[%d/%d] - &quot;%s&quot; yEnc (1/1)"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="%d" number="1">%s</segment></segments></file>`, i+1, len(vols), name, len(v)+200, id)
	}
	nzbBytes := []byte(`<?xml version="1.0" encoding="UTF-8"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` + filesXML + `</nzb>`)

	// Mirror internal/app/build_sources.go: *debrid.Engine -> *adapters.Engine -> *stream.Engine.
	base := stream.New(fetcher, nil, stream.Options{}, nil)
	t.Cleanup(func() { _ = base.Close() })
	ss := NewStreamSources(nil)
	wrapped := ss.WrapEngine(base)
	media := debrid.NewEngine(nil, debrid.EngineOptions{Inner: wrapped, Health: wrapped})
	t.Cleanup(func() { media.Close() })

	var eng core.Engine = media

	// Every optional engine interface relied upon by the plugin host and app
	// must survive both wrapping layers.
	if _, ok := eng.(core.HealthChecker); !ok {
		t.Fatal("wrapped engine lost core.HealthChecker")
	}
	if _, ok := eng.(core.ReleaseHealthChecker); !ok {
		t.Fatal("wrapped engine lost core.ReleaseHealthChecker")
	}
	if _, ok := eng.(core.DurablePrefetcher); !ok {
		t.Fatal("wrapped engine lost core.DurablePrefetcher")
	}
	if _, ok := eng.(debrid.PackNamer); !ok {
		t.Fatal("wrapped engine lost debrid.PackNamer")
	}
	if _, ok := eng.(usenetArticleChecker); !ok {
		t.Fatal("wrapped engine lost usenetArticleChecker")
	}
	fl, ok := eng.(interface {
		ListFiles(context.Context, string, []byte) ([]core.ReleaseFile, error)
	})
	if !ok {
		t.Fatal("wrapped engine lost ListFiles")
	}
	files, err := fl.ListFiles(ctx, "rel-1", nzbBytes)
	if err != nil || len(files) != len(vols) {
		t.Fatalf("wrapped ListFiles = (%d files, %v), want %d files", len(files), err, len(vols))
	}
	ml, ok := eng.(interface {
		ListArchiveMembers(context.Context, []byte) (map[int][]core.ReleaseMember, error)
	})
	if !ok {
		t.Fatal("wrapped engine lost ListArchiveMembers")
	}
	directMembers, err := ml.ListArchiveMembers(ctx, nzbBytes)
	if err != nil || len(directMembers[0]) != 2 {
		t.Fatalf("wrapped ListArchiveMembers = (%+v, %v), want 2 members at file 0", directMembers, err)
	}

	// End-to-end through KindResolver.ResolveSources("release_files").
	idx := &countingNZBIndexer{nzbs: map[string][]byte{"https://idx.test/vulfpeck.nzb": nzbBytes}}
	kr := NewKindResolver(KindResolverDeps{
		Engine:  eng,
		Indexer: idx,
	})
	cands, err := kr.ResolveSources(ctx, "music.plug", core.SourceQuery{
		Kind: "release_files",
		Candidate: &core.SourceCandidate{
			NZBURL: "https://idx.test/vulfpeck.nzb",
		},
	})
	if err != nil {
		t.Fatalf("ResolveSources(release_files): %v", err)
	}
	if len(cands) != 1 || len(cands[0].Files) < 1 {
		t.Fatalf("cands = %+v", cands)
	}
	members := cands[0].Files[0].Members
	if len(members) != 2 || members[0].Path != "01 - The Beachcomber.flac" || members[1].Path != "02 - Wait for the Moment.flac" {
		t.Fatalf("wrapped Files[0].Members = %+v, want 2 FLAC members", members)
	}
}

func TestPullKindResolveTimePartsExposeChildrenAndItemProjection(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	items, err := pluginitems.New(ctx, db, pluginitems.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer items.Close()

	libs, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatalf("libraries.New: %v", err)
	}
	lib, err := libs.Create(ctx, "Audiobooks", core.TypeMovie, nil, "show", core.Presentations{})
	if err != nil {
		t.Fatalf("libs.Create: %v", err)
	}
	decl := plugins.MediaKindMeta{
		Key:   "audiobooks",
		Label: "Audiobooks",
		Media: "audiobook",
		Levels: []plugins.KindLevel{
			{Key: "book", Label: "Book", Playable: true},
		},
	}
	if _, err := items.UpsertKind(ctx, "plug.ab", lib.ID, decl, false); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	if _, err := items.KindUpsert(ctx, "plug.ab", "audiobooks", []core.KindItem{{
		ID:       "ab-1",
		Level:    "book",
		Title:    "Project Hail Mary",
		Playable: true,
	}}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	ref := core.NewItemRef("plug.ab", "audiobooks", "ab-1")
	stored, _, err := items.GetByRef(ctx, ref)
	if err != nil {
		t.Fatalf("GetByRef: %v", err)
	}

	// Persist resolve-time parts and verify KindCatalog projects them as role:"part" children.
	fi0, fi1 := 0, 1
	parts, err := items.SaveParts(ctx, ref, []core.KindItemPart{
		{Name: "Disc 1 - Chapter 1", MIME: "audio/mpeg", Size: 4096, DurationSec: 600, FileIndex: &fi0},
		{Name: "Disc 1 - Chapter 2", MIME: "audio/mpeg", Size: 8192, DurationSec: 720, FileIndex: &fi1},
	}, "resolve")
	if err != nil {
		t.Fatalf("SaveParts: %v", err)
	}

	kc := &KindCatalog{items: items, libs: libs}
	children, err := kc.Children(ctx, stored.Key, false, "", 10)
	if err != nil {
		t.Fatalf("KindCatalog.Children: %v", err)
	}
	if len(children.Items) != 2 || children.Items[0].Key != parts[0].Key || children.Items[0].MediaRole != "part" || children.Items[0].ItemIndex != 1 || children.Items[1].ItemIndex != 2 {
		t.Fatalf("Children = %+v", children.Items)
	}
	partItem, err := kc.Item(ctx, parts[1].Key)
	if err != nil || partItem.Title.Title != "Disc 1 - Chapter 2" || partItem.Title.MediaRole != "part" || len(partItem.Title.MediaParents) != 1 || partItem.Title.MediaParents[0].Key != stored.Key {
		t.Fatalf("KindCatalog.Item(part1) = (%+v, %v)", partItem, err)
	}
	parentItem, err := kc.Item(ctx, stored.Key)
	if err != nil || parentItem.Title.ChildCount != 2 {
		t.Fatalf("KindCatalog.Item(parent) ChildCount = %d, err=%v", parentItem.Title.ChildCount, err)
	}
}
