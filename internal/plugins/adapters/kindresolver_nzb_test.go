package adapters

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/vault"
)

func TestPluginNZBConformance(t *testing.T) {
	ctx := t.Context()
	var grabs atomic.Int32
	const testAPIKey = "example-key"
	const descriptor = `<nzb><file subject="Track01.flac"><groups><group>test</group></groups><segments><segment bytes="100" number="1">m1@test</segment></segments></file><file subject="Track02.flac"><groups><group>test</group></groups><segments><segment bytes="100" number="1">m2@test</segment></segments></file></nzb>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/getnzb/") {
			switch {
			case r.URL.Path == "/getnzb/album.nzb&i=1&r="+testAPIKey:
				fmt.Fprint(w, descriptor)
			case r.URL.Path == "/getnzb/book.nzb" && r.URL.Query().Get("r") == testAPIKey:
				fmt.Fprint(w, strings.NewReplacer("Track01.flac", "Book.epub", "Track02.flac", "Bonus.epub", "@test", "@book.test").Replace(descriptor))
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
			return
		}
		switch r.URL.Query().Get("t") {
		case "caps":
			fmt.Fprint(w, `<caps><searching><search available="yes" supportedParams="q"/></searching></caps>`)
		case "search":
			fmt.Fprint(w, `<rss><channel><item><title>Artist - Album</title><guid>release-guid</guid><link>/api?t=get&amp;id=release-guid</link><enclosure url="/api?t=get&amp;id=release-guid" length="123456" type="application/x-nzb"/></item></channel></rss>`)
		case "get":
			grabs.Add(1)
			if r.URL.Query().Get("id") == "missing" {
				fmt.Fprint(w, `<error code="409" description="Already grabbed this NZB"/>`)
				return
			}
			fmt.Fprint(w, descriptor)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	sealer, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	library, err := vault.Open(ctx, vault.Options{DB: db, Dir: t.TempDir(), CapBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	mgr := newznab.NewManager(db, []config.Indexer{{ID: "test-indexer", Name: "Test", URL: server.URL, Enabled: true}},
		newznab.Options{Vault: library, PluginReferences: sealer})
	t.Cleanup(mgr.Close)
	r := NewKindResolver(KindResolverDeps{Indexer: mgr, PluginNZBs: mgr, Engine: &keylessCheckEngine{}})
	t.Cleanup(r.Close)
	search, err := r.ResolveSources(ctx, "plugin.one", core.SourceQuery{Kind: "indexer_search", Query: "Artist Album", Category: "music"})
	if err != nil || len(search) != 1 || search[0].NZBRef == "" || search[0].NZBURL != core.PluginNZBRefPrefix+search[0].NZBRef {
		t.Fatalf("search must return one opaque reference: count=%d err=%v", len(search), err)
	}
	candidate := search[0]
	t.Run("album listing and targeted playback", func(t *testing.T) {
		listed, err := r.ResolveSources(ctx, "plugin.one", core.SourceQuery{Kind: "release_files", Candidate: &candidate})
		if err != nil || len(listed) != 1 || len(listed[0].Files) != 2 || grabs.Load() != 1 {
			t.Fatalf("listing album: candidates=%d grabs=%d err=%v", len(listed), grabs.Load(), err)
		}
		var previousID string
		for _, file := range listed[0].Files {
			index := file.Index
			media, err := r.openReleaseForSource(ctx, core.PlayableSource{
				Pattern: kindstream.PatternRelease,
				MIME:    "audio/flac",
				Release: &core.ReleaseHint{Candidate: &candidate, FileIndex: &index},
			}, "audio/*", nil)
			if err != nil {
				t.Fatalf("open album track %d: %v", index, err)
			}
			id := media.ReleaseID()
			_ = media.Close()
			if id == "" || id == previousID {
				t.Fatal("each track must retain its own playback ID")
			}
			previousID = id
		}
		if n := grabs.Load(); n != 1 {
			t.Fatalf("listing and playing two album tracks used %d grabs, want 1", n)
		}
	})
	t.Run("deprecated nzbUrl only", func(t *testing.T) {
		link := candidate.NZBURL
		if !strings.HasPrefix(link, core.PluginNZBRefPrefix) || strings.Contains(link, server.URL) ||
			strings.Contains(link, "release-guid") || strings.Contains(link, "/api") {
			t.Fatal("deprecated link field must contain only an opaque Ether reference")
		}
		restarted := newznab.NewManager(db, []config.Indexer{{ID: "test-indexer", Name: "Test", URL: server.URL, Enabled: true}},
			newznab.Options{Vault: library, PluginReferences: sealer})
		defer restarted.Close()
		other := NewKindResolver(KindResolverDeps{Indexer: restarted, PluginNZBs: restarted, Engine: &keylessCheckEngine{}})
		defer other.Close()
		again, err := other.ResolveSources(ctx, "plugin.one", core.SourceQuery{Kind: "indexer_search", Query: "Artist", Category: "music"})
		if err != nil || len(again) != 1 || again[0].NZBURL != link || grabs.Load() != 1 {
			t.Fatalf("fresh search after restart must preserve the reference: candidates=%d grabs=%d err=%v", len(again), grabs.Load(), err)
		}

		checked, err := r.ResolveSources(ctx, "plugin.one", core.SourceQuery{Kind: "usenet_check", NZBURL: link})
		if err != nil || len(checked) != 1 || checked[0].NZBURL != link ||
			checked[0].UsenetCheck == nil || !checked[0].UsenetCheck.Available || grabs.Load() != 1 {
			t.Fatalf("check through deprecated field: candidates=%d grabs=%d err=%v", len(checked), grabs.Load(), err)
		}
		media, err := r.openRelease(ctx, core.ReleaseHint{NZBURL: link})
		if err != nil {
			t.Fatal(err)
		}
		_ = media.Close()
		opened, err := kindstream.Open(ctx, core.PlayableSource{URL: "nzb:" + link}, kindstream.Deps{OpenRelease: r.openRelease})
		if err != nil {
			t.Fatal(err)
		}
		_ = opened.Close()
		if grabs.Load() != 1 {
			t.Fatalf("deprecated search, check and playback must share one grab, got %d", grabs.Load())
		}
	})
	request := core.SourceQuery{Kind: "nzb_request", NZBRef: candidate.NZBRef}
	got, err := r.ResolveSources(ctx, "plugin.one", request)
	if err != nil || len(got) != 1 || got[0].NZBRef == "" || len(got[0].Files) != 2 || grabs.Load() != 1 {
		t.Fatalf("first request: response count=%d grabs=%d err=%v", len(got), grabs.Load(), err)
	}
	storeRef := got[0].NZBRef
	hint := core.ReleaseHint{NZBRef: storeRef}
	play := func(viewer int64) {
		t.Helper()
		media, err := r.openRelease(plugins.WithViewer(ctx, viewer), hint)
		if err != nil {
			t.Fatal(err)
		}
		_ = media.Close()
	}
	for _, viewer := range []int64{1, 1, 1, 2} { // first play, second play, retry, and another viewer
		play(viewer)
	}
	_, err = r.ResolveSources(ctx, "plugin.two", request)
	if err != nil {
		t.Fatal(err)
	}
	identity := &core.NZBIdentity{IndexerID: "test-indexer", GUID: "release-guid"}
	if _, err := r.ResolveSources(ctx, "plugin.two", core.SourceQuery{Kind: "nzb_request", NZBIdentity: identity}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"release_files", "usenet_check"} {
		if _, err := r.ResolveSources(ctx, "plugin.two", core.SourceQuery{Kind: kind, Candidate: &candidate}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	legacy := core.PlayableSource{URL: "nzb:" + server.URL + "/api?t=get&id=release-guid"}
	opened, err := kindstream.Open(ctx, legacy, kindstream.Deps{OpenRelease: r.openRelease})
	if err != nil {
		t.Fatal(err)
	}
	_ = opened.Close()
	if n := grabs.Load(); n != 1 {
		t.Fatalf("stored NZB caused %d additional indexer grabs", n-1)
	}
	missing := core.SourceQuery{Kind: "nzb_request", NZBIdentity: &core.NZBIdentity{IndexerID: "test-indexer", GUID: "missing"}}
	for range 2 {
		_, err = r.ResolveSources(ctx, "plugin.two", missing)
		var refusal *newznab.GrabRefusal
		if !errors.As(err, &refusal) || !refusal.RetryAt.After(time.Now()) {
			t.Fatalf("expected temporary grab refusal, got %v", err)
		}
	}
	if n := grabs.Load(); n != 2 {
		t.Fatalf("refused NZB was retried within its window: grabs=%d", n)
	}
	// An import or peer can supply the release during its hold window.
	missingID := fmt.Sprintf("%x", sha1.Sum([]byte("test-indexer|missing")))
	if _, err := library.Put(ctx, vault.PutRequest{
		Release: core.Release{ID: missingID, GUID: "missing", IndexerID: "test-indexer"},
		NZB:     []byte(descriptor),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveSources(ctx, "plugin.one", missing); err != nil {
		t.Fatalf("vault hit during a refusal window: %v", err)
	}
	if grabs.Load() != 2 {
		t.Fatal("vault hit during refusal contacted the indexer")
	}

	mgr.Reload([]config.Indexer{{ID: "test-indexer", Name: "Test", URL: server.URL}})
	if _, err := r.ResolveSources(ctx, "plugin.two", core.SourceQuery{Kind: "nzb_request", NZBIdentity: identity}); err != nil {
		t.Fatalf("vault hit after disabling indexer: %v", err)
	}
	opened, err = kindstream.Open(ctx, legacy, kindstream.Deps{OpenRelease: r.openRelease})
	if err != nil {
		t.Fatalf("legacy vault hit after disabling indexer: %v", err)
	}
	_ = opened.Close()
	if grabs.Load() != 2 {
		t.Fatal("stored descriptor contacted a disabled indexer")
	}

	t.Run("stored links stay private through targeted playback", func(t *testing.T) {
		cfg := config.Indexer{ID: "test-indexer", Name: "Test", URL: server.URL, APIKey: testAPIKey, Enabled: true}
		mgr.Reload([]config.Indexer{cfg})
		albumLink := server.URL + "/getnzb/album.nzb&i=1&r=~ether.apikey~"
		bookLink := server.URL + "/getnzb/book.nzb?r=~ether.apikey~"
		raw := fmt.Appendf(nil, `[{"id":%q,"mime":"audio/flac","release":{"candidate":{"nzbUrl":%q},"fileIndex":1}},{"mime":"application/epub+zip","release":{"nzbUrl":%q,"fileIndex":0}}]`,
			"nzb:"+albumLink, albumLink, bookLink)
		normalized, err := r.NormalizeStoredReleaseLinks(ctx, raw)
		if err != nil || strings.Contains(string(normalized), server.URL) || strings.Contains(string(normalized), "~ether.") {
			t.Fatalf("stored release links must remain private: %v", err)
		}
		var sources []core.PlayableSource
		if err := json.Unmarshal(normalized, &sources); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sources[0].ID, "nzb:"+core.PluginNZBRefPrefix) ||
			sources[0].Release.FileIndex == nil || *sources[0].Release.FileIndex != 1 {
			t.Fatal("stored source must retain its file selection and use an opaque source ID")
		}
		for _, src := range sources {
			media, err := r.openReleaseForSource(ctx, src, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = media.Close()
		}
		cfg.Enabled = false
		mgr.Reload([]config.Indexer{cfg})
		for _, link := range []string{albumLink, bookLink} {
			media, err := r.openRelease(ctx, core.ReleaseHint{NZBURL: link})
			if err != nil {
				t.Fatalf("the saved link must open from the vault after disabling its indexer: %v", err)
			}
			_ = media.Close()
		}

		// Simulate a previously played track whose plugin and host caches
		// expired, with an NZB stored before download aliases were recorded.
		legacy := core.SourceCandidate{
			NZBURL: server.URL + "/getnzb/older.nzb&i=1&r=~ether.apikey~",
			Title:  "Earlier Album",
			Size:   123456,
		}
		data := strings.ReplaceAll(descriptor, "@test", "@older.test")
		if _, err := library.Put(ctx, vault.PutRequest{
			Release: core.Release{Name: legacy.Title, Size: legacy.Size},
			NZB:     []byte(data),
		}); err != nil {
			t.Fatal(err)
		}
		items, err := pluginitems.New(ctx, db, pluginitems.Options{})
		if err != nil {
			t.Fatal(err)
		}
		opener := NewKindResolver(KindResolverDeps{Items: items, Indexer: mgr, PluginNZBs: mgr, Engine: &keylessCheckEngine{}})
		t.Cleanup(opener.Close)
		item := core.KindItem{Ref: core.NewItemRef("plugin.one", "music", "track1")}
		index := 1
		src := core.PlayableSource{
			Pattern: kindstream.PatternRelease, MIME: "audio/flac", ExpiresAt: 1,
			Release: &core.ReleaseHint{Candidate: &legacy, FileIndex: &index},
		}
		if err := items.SavePartResolution(ctx, item.Ref, nil, src); err != nil {
			t.Fatal(err)
		}
		stream, saved, err := opener.openItemByteSourceExcluding(ctx, pluginitems.KindRecord{}, core.Library{}, item, nil, []string{"release"}, nil)
		if err != nil {
			t.Fatalf("expired, previously opened source should open from the vault without a plugin or indexer: %v", err)
		}
		_ = stream.Close()
		if saved.Release.FileIndex == nil || *saved.Release.FileIndex != index {
			t.Fatal("vault reuse must preserve the original file selection")
		}

	})

}
