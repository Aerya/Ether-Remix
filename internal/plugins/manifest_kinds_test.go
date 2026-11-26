package plugins

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func contentManifest() Manifest {
	m := baseManifest()
	m.ID = "community.example.library"
	m.Name = "Library"
	m.Ether.Kind = KindContent
	m.Ether.Summary = "Adds a field recordings library"
	m.Ether.Capabilities = []Capability{CapCatalogProvider, CapKindResolver, CapMatcher}
	m.Ether.Permissions.Sources = "search"
	m.Ether.Settings = []byte(`{"fields":[{"key":"api_key","kind":"secret","label":"Access key"},{"key":"endpoint","kind":"url","label":"Endpoint"}]}`)
	m.Ether.Contributes.MediaKinds = []MediaKindMeta{validMediaKind()}
	m.Ether.Contributes.Setup = &SetupMeta{Steps: []SetupStep{
		{Key: "connect", Label: "Connect the account", Fields: []string{"api_key", "endpoint"}, Test: "/ether/v1/setup/connect/test", Required: true},
	}}
	return m
}

func validMediaKind() MediaKindMeta {
	return MediaKindMeta{
		Key:     "recording",
		Label:   "Field recordings",
		Library: KindLibraryDecl{Name: "Field recordings"},
		Levels: []KindLevel{
			{Key: "collection", Label: "Collection"},
			{Key: "album", Label: "Album", Container: true},
			{Key: "track", Label: "Track", Playable: true},
		},
		Fields: []KindField{
			{Key: "genre", Type: "enum", Label: "Genre", Indexed: true, Enum: []string{"nature", "city"}},
			{Key: "recordist", Type: "people", Label: "Recordist"},
			{Key: "duration", Type: "duration", Label: "Duration"},
		},
		Art: []KindArt{{Role: "waveform", Label: "Waveform"}},
		Present: core.Presentations{
			Plex: &core.PlexPresentation{Base: "show"},
			MediaBrowser: &core.MediaBrowserPresentation{
				CollectionType: "music",
				ItemTypes:      map[string]string{"album": "MusicAlbum", "track": "Audio"},
			},
		},
		Browse: KindBrowseDecl{Sorts: []string{"title", "added"}, Facets: []string{"genre"}, Search: true, SearchFields: []string{"recordist"}},
		Sync:   KindSyncDecl{Mode: KindSyncPull, Refresh: "1h"},
		Match:  KindMatchDecl{ThresholdDefault: 0.8},
	}
}

func TestContentManifestValidates(t *testing.T) {
	if err := contentManifest().Validate(); err != nil {
		t.Fatalf("valid content manifest: %v", err)
	}
	// Landscape card + facet collections tab (with order) + people tab validate cleanly.
	m := contentManifest()
	m.Ether.Contributes.MediaKinds[0].Fields[1].Indexed = true
	m.Ether.Contributes.MediaKinds[0].Browse.Facets = []string{"genre", "recordist"}
	m.Ether.Contributes.MediaKinds[0].Browse.Sorts = []string{"released", "title", "added"}
	m.Ether.Contributes.MediaKinds[0].Library.Card = "landscape"
	searchOff := false
	searchOn := true
	m.Ether.Strings = map[string]json.RawMessage{
		"en": json.RawMessage(`{"search_recordists":"Search recordists"}`),
	}
	m.Ether.Contributes.MediaKinds[0].Library.Tabs = []KindLibraryTab{
		{Key: "recommended", Label: "Recommended", Kind: "recommended"},
		{Key: "library", Label: "Library", Kind: "library"},
		{Key: "genres", Label: "Genres", Kind: "collections", Facet: "genre", Order: "name", SearchPlaceholder: "Search genres", Sort: "released", Direction: "desc"},
		{Key: "recordists", Label: "Recordists", Kind: "people", Facet: "recordist", Order: "name", Search: &searchOn, SearchPlaceholder: "$t:search_recordists"},
		{Key: "top-recordists", Label: "Top recordists", Kind: "people", Facet: "recordist", Order: "count", Search: &searchOff},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("landscape + facet collections + people tabs: %v", err)
	}
	tabs := m.Ether.Contributes.MediaKinds[0].Library.Tabs
	if tabs[0].EffectiveSearch() || tabs[1].EffectiveSearch() {
		t.Fatalf("recommended and library tabs must have EffectiveSearch() == false")
	}
	if !tabs[2].EffectiveSearch() || !tabs[3].EffectiveSearch() || tabs[4].EffectiveSearch() {
		t.Fatalf("unexpected EffectiveSearch() defaults/overrides: [%v, %v, %v]", tabs[2].EffectiveSearch(), tabs[3].EffectiveSearch(), tabs[4].EffectiveSearch())
	}
	// Vela-only presentation (movies on 1 level, shows on >=2 levels) validates.
	m = contentManifest()
	m.Ether.Contributes.MediaKinds[0].Present = core.Presentations{
		Vela: &core.VelaPresentation{Type: "shows"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("vela shows kind: %v", err)
	}
	m.Ether.Contributes.MediaKinds[0].Levels = []KindLevel{{Key: "film", Label: "Film", Playable: true}}
	m.Ether.Contributes.MediaKinds[0].Present = core.Presentations{
		Vela: &core.VelaPresentation{Type: "movies"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("vela movies flat kind: %v", err)
	}
	// Empty present validates: Ether infers per-app presentations from kind media/roles/shape.
	m = contentManifest()
	m.Ether.Contributes.MediaKinds[0].Media = "music"
	m.Ether.Contributes.MediaKinds[0].Levels[0].Role = "album"
	m.Ether.Contributes.MediaKinds[0].Levels[1].Role = "track"
	m.Ether.Contributes.MediaKinds[0].Present = core.Presentations{}
	if err := m.Validate(); err != nil {
		t.Fatalf("inferred presentation with media+roles: %v", err)
	}
	// Manifest default-off hint without repeating inferred type validates.
	off := false
	m = contentManifest()
	m.Ether.Contributes.MediaKinds[0].Present = core.Presentations{
		Stremio: &core.StremioPresentation{Default: &off},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("default-off hint: %v", err)
	}
	// Adapter-only presentation with the adapter declared.
	m = contentManifest()
	m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
	m.Ether.Contributes.FrontendAdapters = []FrontendAdapterMeta{
		{Key: "feed", Label: "Recordings feed", ProtocolHint: "custom", PathPrefix: "/recordings"},
	}
	m.Ether.Contributes.MediaKinds[0].Present = core.Presentations{AdapterOnly: true}
	if err := m.Validate(); err != nil {
		t.Fatalf("adapter only kind: %v", err)
	}
	// A declared library kind key on the adapter is valid.
	m = contentManifest()
	m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
	m.Ether.Contributes.FrontendAdapters = []FrontendAdapterMeta{
		{Key: "feed", Label: "Recordings feed", ProtocolHint: "custom", PathPrefix: "/recordings", Libraries: []string{"recording"}},
	}
	m.Ether.Contributes.MediaKinds[0].Present = core.Presentations{AdapterOnly: true}
	if err := m.Validate(); err != nil {
		t.Fatalf("adapter with declared library kind: %v", err)
	}
	// Resolver-only content with no sync is valid but owns no sync.
	m = contentManifest()
	m.Ether.Capabilities = []Capability{CapKindResolver}
	m.Ether.Contributes.MediaKinds[0].Sync = KindSyncDecl{}
	if err := m.Validate(); err != nil {
		t.Fatalf("resolver only kind: %v", err)
	}
	// A mediaKind without a library block creates no library and needs no
	// presentation or sync mode.
	m = contentManifest()
	m.Ether.Contributes.MediaKinds[0].Library = KindLibraryDecl{}
	m.Ether.Contributes.MediaKinds[0].Present = core.Presentations{}
	m.Ether.Contributes.MediaKinds[0].Sync = KindSyncDecl{}
	if m.Ether.Contributes.MediaKinds[0].HasLibrary() {
		t.Fatal("empty Library block must report HasLibrary() == false")
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("kind without library block: %v", err)
	}
}

// TestContentManifestMusicAndAudiobookItemTypes proves the full declared
// MediaBrowser type set a music/audiobook plugin needs validates, including
// MusicArtist and AudioBook.
func TestContentManifestMusicAndAudiobookItemTypes(t *testing.T) {
	m := contentManifest()
	m.Ether.Contributes.MediaKinds[0].Present.Plex.Base = "artist"
	m.Ether.Contributes.MediaKinds[0].Present.MediaBrowser.ItemTypes = map[string]string{
		"collection": "MusicArtist", "album": "MusicAlbum", "track": "Audio",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("music item types: %v", err)
	}
	m = contentManifest()
	m.Ether.Contributes.MediaKinds[0].Present.MediaBrowser.CollectionType = "audiobooks"
	m.Ether.Contributes.MediaKinds[0].Present.MediaBrowser.ItemTypes = map[string]string{
		"collection": "Book", "album": "AudioBook", "track": "Audio",
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("audiobook item types: %v", err)
	}
	// A single-level flat album kind may declare the Plex artist base: it
	// publishes as a flat album list in a music section.
	m = contentManifest()
	m.Ether.Contributes.MediaKinds[0].Levels = []KindLevel{{Key: "album", Label: "Album", Playable: true}}
	m.Ether.Contributes.MediaKinds[0].Present.Plex.Base = "artist"
	m.Ether.Contributes.MediaKinds[0].Present.MediaBrowser.ItemTypes = map[string]string{"album": "MusicAlbum"}
	if err := m.Validate(); err != nil {
		t.Fatalf("single-level plex artist kind: %v", err)
	}
}

func TestContentManifestRules(t *testing.T) {
	kind := func(m *Manifest) *MediaKindMeta { return &m.Ether.Contributes.MediaKinds[0] }
	cases := []struct {
		name  string
		mut   func(*Manifest)
		field string
	}{
		{"content without provider or resolver", func(m *Manifest) {
			m.Ether.Capabilities = []Capability{CapMatcher}
		}, "ether.capabilities"},
		{"provider without kinds", func(m *Manifest) {
			m.Ether.Contributes.MediaKinds = nil
		}, "ether.contributes.mediaKinds"},
		{"provider on extension kind", func(m *Manifest) {
			m.Ether.Kind = KindExtension
		}, "ether.capabilities[0]"},
		{"frontend adapter on source kind", func(m *Manifest) {
			m.Ether.Kind = KindSource
			m.Ether.Capabilities = []Capability{CapFrontendAdapter}
			m.Ether.Contributes.MediaKinds = nil
		}, "ether.capabilities[0]"},
		{"bad kind key", func(m *Manifest) { kind(m).Key = "Recordings" }, ".key"},
		{"duplicate kind key", func(m *Manifest) {
			m.Ether.Contributes.MediaKinds = append(m.Ether.Contributes.MediaKinds, validMediaKind())
		}, "mediaKinds[1].key"},
		{"bad library card", func(m *Manifest) { kind(m).Library.Card = "banner" }, "library.card"},
		{"bad library experience", func(m *Manifest) { kind(m).Library.Experience = "Big Screen" }, "library.experience"},
		{"too many library tabs", func(m *Manifest) {
			for i := 0; i < 7; i++ {
				kind(m).Library.Tabs = append(kind(m).Library.Tabs, KindLibraryTab{Key: "tab", Label: "Tab", Kind: "library"})
			}
		}, "library.tabs"},
		{"duplicate tab key", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{
				{Key: "library", Label: "Library", Kind: "library"},
				{Key: "library", Label: "All", Kind: "library"},
			}
		}, "library.tabs[1].key"},
		{"bad tab kind", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "grid", Label: "Grid", Kind: "grid"}}
		}, "library.tabs[0].kind"},
		{"facet on non-collections tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "library", Label: "Library", Kind: "library", Facet: "genre"}}
		}, "library.tabs[0].facet"},
		{"undeclared facet on collections tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "moods", Label: "Moods", Kind: "collections", Facet: "mood"}}
		}, "library.tabs[0].facet"},
		{"undeclared sort on collections tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "genres", Label: "Genres", Kind: "collections", Facet: "genre", Sort: "rating"}}
		}, "library.tabs[0].sort"},
		{"sort without facet on collections tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cols", Label: "Collections", Kind: "collections", Sort: "title"}}
		}, "library.tabs[0].sort"},
		{"bad direction on collections tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "genres", Label: "Genres", Kind: "collections", Facet: "genre", Direction: "newest"}}
		}, "library.tabs[0].direction"},
		{"bad order on collections tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "genres", Label: "Genres", Kind: "collections", Facet: "genre", Order: "popular"}}
		}, "library.tabs[0].order"},
		{"order without facet on collections tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cols", Label: "Collections", Kind: "collections", Order: "name"}}
		}, "library.tabs[0].order"},
		{"order on library tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "library", Label: "Library", Kind: "library", Order: "name"}}
		}, "library.tabs[0].order"},
		{"people tab without facet", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cast", Label: "Cast", Kind: "people"}}
		}, "library.tabs[0].facet"},
		{"people tab undeclared browse facet", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cast", Label: "Cast", Kind: "people", Facet: "recordist"}}
		}, "library.tabs[0].facet"},
		{"people tab non-people facet", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cast", Label: "Cast", Kind: "people", Facet: "genre"}}
		}, "library.tabs[0].facet"},
		{"people tab with sort", func(m *Manifest) {
			kind(m).Fields[1].Indexed = true
			kind(m).Browse.Facets = []string{"genre", "recordist"}
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cast", Label: "Cast", Kind: "people", Facet: "recordist", Sort: "title"}}
		}, "library.tabs[0].sort"},
		{"people tab with direction", func(m *Manifest) {
			kind(m).Fields[1].Indexed = true
			kind(m).Browse.Facets = []string{"genre", "recordist"}
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cast", Label: "Cast", Kind: "people", Facet: "recordist", Direction: "asc"}}
		}, "library.tabs[0].direction"},
		{"bad order on people tab", func(m *Manifest) {
			kind(m).Fields[1].Indexed = true
			kind(m).Browse.Facets = []string{"genre", "recordist"}
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cast", Label: "Cast", Kind: "people", Facet: "recordist", Order: "asc"}}
		}, "library.tabs[0].order"},
		{"search on library tab", func(m *Manifest) {
			on := true
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "library", Label: "Library", Kind: "library", Search: &on}}
		}, "library.tabs[0].search"},
		{"search on recommended tab", func(m *Manifest) {
			off := false
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "recommended", Label: "Recommended", Kind: "recommended", Search: &off}}
		}, "library.tabs[0].search"},
		{"searchPlaceholder on library tab", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "library", Label: "Library", Kind: "library", SearchPlaceholder: "Search"}}
		}, "library.tabs[0].searchPlaceholder"},
		{"searchPlaceholder with search false on collections tab", func(m *Manifest) {
			off := false
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "genres", Label: "Genres", Kind: "collections", Facet: "genre", Search: &off, SearchPlaceholder: "Search genres"}}
		}, "library.tabs[0].searchPlaceholder"},
		{"searchPlaceholder with search false on people tab", func(m *Manifest) {
			off := false
			kind(m).Fields[1].Indexed = true
			kind(m).Browse.Facets = []string{"genre", "recordist"}
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "cast", Label: "Cast", Kind: "people", Facet: "recordist", Search: &off, SearchPlaceholder: "Search cast"}}
		}, "library.tabs[0].searchPlaceholder"},
		{"searchPlaceholder over 60 chars", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "genres", Label: "Genres", Kind: "collections", Facet: "genre", SearchPlaceholder: "Search across every developer studio and publisher in this library now"}}
		}, "library.tabs[0].searchPlaceholder"},
		{"searchPlaceholder bad $t key", func(m *Manifest) {
			kind(m).Library.Tabs = []KindLibraryTab{{Key: "genres", Label: "Genres", Kind: "collections", Facet: "genre", SearchPlaceholder: "$t:"}}
		}, "library.tabs[0].searchPlaceholder"},
		{"no levels", func(m *Manifest) { kind(m).Levels = nil }, ".levels"},
		{"six levels", func(m *Manifest) {
			for i := 0; i < 4; i++ {
				kind(m).Levels = append(kind(m).Levels, KindLevel{Key: "extra", Label: "Extra"})
			}
		}, ".levels"},
		{"duplicate level", func(m *Manifest) { kind(m).Levels[1].Key = "collection" }, "levels[1].key"},
		{"no playable level", func(m *Manifest) { kind(m).Levels[2].Playable = false }, ".levels"},
		{"field envelope collision", func(m *Manifest) { kind(m).Fields[0].Key = "title" }, "fields[0].key"},
		{"field bad type", func(m *Manifest) { kind(m).Fields[0].Type = "blob" }, "fields[0].type"},
		{"enum without values", func(m *Manifest) { kind(m).Fields[0].Enum = nil }, "fields[0].enum"},
		{"enum values on text field", func(m *Manifest) {
			kind(m).Fields[1].Enum = []string{"x"}
		}, "fields[1].enum"},
		{"values on non-strings field", func(m *Manifest) {
			kind(m).Fields[1].Values = []string{"x"}
		}, "fields[1].values"},
		{"values on enum field", func(m *Manifest) {
			kind(m).Fields[0].Values = []string{"x"}
		}, "fields[0].values"},
		{"blank value in strings values", func(m *Manifest) {
			kind(m).Fields = append(kind(m).Fields, KindField{Key: "tags", Type: "strings", Label: "Tags", Indexed: true, Values: []string{"ambient", "  "}})
		}, "fields[3].values"},
		{"duplicate value in strings values", func(m *Manifest) {
			kind(m).Fields = append(kind(m).Fields, KindField{Key: "tags", Type: "strings", Label: "Tags", Indexed: true, Values: []string{"ambient", "ambient"}})
		}, "fields[3].values"},
		{"facet not indexed", func(m *Manifest) { kind(m).Fields[0].Indexed = false }, "browse.facets[0]"},
		{"facet unknown field", func(m *Manifest) { kind(m).Browse.Facets = []string{"mood"} }, "browse.facets[0]"},
		{"bad sort", func(m *Manifest) { kind(m).Browse.Sorts = []string{"vibes"} }, "browse.sorts[0]"},
		{"search field not text", func(m *Manifest) {
			kind(m).Browse.SearchFields = []string{"duration"}
		}, "browse.searchFields[0]"},
		{"search fields without search", func(m *Manifest) { kind(m).Browse.Search = false }, "browse.search"},
		{"builtin art role", func(m *Manifest) { kind(m).Art[0].Role = "poster" }, "art[0].role"},
		{"invalid media class", func(m *Manifest) { kind(m).Media = "games" }, ".media"},
		{"invalid level role", func(m *Manifest) { kind(m).Levels[0].Role = " leading" }, ".levels[0].role"},
		{"adapter only without adapter", func(m *Manifest) {
			kind(m).Present = core.Presentations{AdapterOnly: true}
		}, ".present.adapterOnly"},
		{"vela shows one level", func(m *Manifest) {
			kind(m).Levels = []KindLevel{{Key: "track", Label: "Track", Playable: true}}
			kind(m).Present = core.Presentations{
				Vela: &core.VelaPresentation{Type: "shows"},
			}
		}, "present.vela.type"},
		{"vela bad type", func(m *Manifest) {
			kind(m).Present.Vela = &core.VelaPresentation{Type: "music"}
		}, "present.vela.type"},
		{"plex show one level", func(m *Manifest) {
			kind(m).Levels = []KindLevel{{Key: "track", Label: "Track", Playable: true}}
			kind(m).Present.MediaBrowser = nil
		}, "present.plex.base"},
		{"plex bad base", func(m *Manifest) { kind(m).Present.Plex.Base = "book" }, "present.plex.base"},
		{"mediabrowser bad collection", func(m *Manifest) {
			kind(m).Present.MediaBrowser.CollectionType = "podcasts"
		}, "mediaBrowser.collectionType"},
		{"mediabrowser unknown level", func(m *Manifest) {
			kind(m).Present.MediaBrowser.ItemTypes = map[string]string{"disc": "Audio"}
		}, "mediaBrowser.itemTypes"},
		{"mediabrowser unknown item type", func(m *Manifest) {
			kind(m).Present.MediaBrowser.ItemTypes = map[string]string{"album": "Podcast"}
		}, "mediaBrowser.itemTypes"},
		{"ondemand sync", func(m *Manifest) {
			kind(m).Sync = KindSyncDecl{Mode: "ondemand"}
		}, "sync.mode"},
		{"pull refresh too fast", func(m *Manifest) { kind(m).Sync.Refresh = "5m" }, "sync.refresh"},
		{"push refresh", func(m *Manifest) {
			kind(m).Sync = KindSyncDecl{Mode: KindSyncPush, Refresh: "1h"}
		}, "sync.refresh"},
		{"pull sync undo", func(m *Manifest) {
			kind(m).Sync = KindSyncDecl{Mode: KindSyncPull, Undo: KindSyncUndoIDs}
		}, "sync.undo"},
		{"bad sync undo", func(m *Manifest) {
			kind(m).Sync = KindSyncDecl{Mode: KindSyncPush, Undo: "none"}
		}, "sync.undo"},
		{"sync without provider", func(m *Manifest) {
			m.Ether.Capabilities = []Capability{CapKindResolver}
		}, "sync.mode"},
		{"negative cache", func(m *Manifest) { kind(m).Cache.MaxBytes = -1 }, "cache.maxBytes"},
		{"bad cache ttl", func(m *Manifest) { kind(m).Cache.TTL = "soon" }, "cache.ttl"},
		{"threshold over one", func(m *Manifest) { kind(m).Match.ThresholdDefault = 1.5 }, "match.thresholdDefault"},
		{"sources without resolver", func(m *Manifest) {
			m.Ether.Capabilities = []Capability{CapCatalogProvider}
		}, "ether.permissions.sources"},
		{"bad sources level", func(m *Manifest) { m.Ether.Permissions.Sources = "all" }, "ether.permissions.sources"},
		{"adapter without declaration", func(m *Manifest) {
			m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
		}, "ether.contributes.frontendAdapters"},
		{"adapter bad prefix", func(m *Manifest) {
			m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
			m.Ether.Contributes.FrontendAdapters = []FrontendAdapterMeta{{Key: "feed", Label: "Recordings feed", ProtocolHint: "custom", PathPrefix: "recordings"}}
		}, "frontendAdapters[0].pathPrefix"},
		{"adapter unknown library kind", func(m *Manifest) {
			m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
			m.Ether.Contributes.FrontendAdapters = []FrontendAdapterMeta{{Key: "feed", Label: "Recordings feed", ProtocolHint: "custom", PathPrefix: "/recordings", Libraries: []string{"missing"}}}
		}, "frontendAdapters[0].libraries[0]"},
		{"adapter duplicate library kind", func(m *Manifest) {
			m.Ether.Capabilities = append(m.Ether.Capabilities, CapFrontendAdapter)
			m.Ether.Contributes.FrontendAdapters = []FrontendAdapterMeta{{Key: "feed", Label: "Recordings feed", ProtocolHint: "custom", PathPrefix: "/recordings", Libraries: []string{"recording", "recording"}}}
		}, "frontendAdapters[0].libraries[1]"},
		{"setup field not in schema", func(m *Manifest) {
			m.Ether.Contributes.Setup.Steps[0].Fields = []string{"password"}
		}, "setup.steps[0].fields[0]"},
		{"setup bad test route", func(m *Manifest) {
			m.Ether.Contributes.Setup.Steps[0].Test = "ether/v1/setup/connect/test"
		}, "setup.steps[0].test"},
		{"setup duplicate step", func(m *Manifest) {
			m.Ether.Contributes.Setup.Steps = append(m.Ether.Contributes.Setup.Steps, m.Ether.Contributes.Setup.Steps[0])
		}, "setup.steps[1].key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := contentManifest()
			tc.mut(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("want error on %s, got none", tc.field)
			}
			found := false
			for _, f := range err.(*ValidationError).Fields {
				if strings.Contains(f.Field, tc.field) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want field containing %s in %v", tc.field, err.(*ValidationError).Fields)
			}
		})
	}
}

func TestMediaKindDeclHash(t *testing.T) {
	a, b := validMediaKind(), validMediaKind()
	if a.DeclHash() != b.DeclHash() || a.DeclHash() == "" {
		t.Fatal("equal declarations must hash equal")
	}
	b.Label = "Other recordings"
	if a.DeclHash() == b.DeclHash() {
		t.Fatal("changed declaration must hash differently")
	}
}

func TestValidateMediaKindsUpgrade(t *testing.T) {
	base := []MediaKindMeta{validMediaKind()}
	clone := func() []MediaKindMeta {
		k := validMediaKind()
		return []MediaKindMeta{k}
	}
	// Additive: new field, trailing level, enum value, strings values, label change.
	next := clone()
	next[0].Label = "Field recordings and more"
	next[0].Fields = append(next[0].Fields,
		KindField{Key: "mood", Type: "string", Label: "Mood"},
		KindField{Key: "tags", Type: "strings", Label: "Tags", Indexed: true, Values: []string{"field", "night"}},
	)
	next[0].Fields[0].Enum = append(next[0].Fields[0].Enum, "night")
	next[0].Levels = append(next[0].Levels, KindLevel{Key: "take", Label: "Take", Playable: true})
	if err := ValidateMediaKindsUpgrade(base, next); err != nil {
		t.Fatalf("additive upgrade: %v", err)
	}
	// Updating strings.values across upgrades is allowed (declared vocabulary, not a closed enum).
	next2 := clone()
	next2[0].Fields = append(next2[0].Fields,
		KindField{Key: "mood", Type: "string", Label: "Mood"},
		KindField{Key: "tags", Type: "strings", Label: "Tags", Indexed: true, Values: []string{"night", "rain"}},
	)
	next2[0].Fields[0].Enum = append(next2[0].Fields[0].Enum, "night")
	next2[0].Levels = append(next2[0].Levels, KindLevel{Key: "take", Label: "Take", Playable: true})
	if err := ValidateMediaKindsUpgrade(next, next2); err != nil {
		t.Fatalf("strings.values update upgrade: %v", err)
	}
	bad := []struct {
		name string
		mut  func(*MediaKindMeta)
	}{
		{"field removed", func(k *MediaKindMeta) { k.Fields = k.Fields[1:] }},
		{"field retyped", func(k *MediaKindMeta) { k.Fields[0].Type = "string" }},
		{"enum shrunk", func(k *MediaKindMeta) { k.Fields[0].Enum = []string{"nature"} }},
		{"index dropped", func(k *MediaKindMeta) { k.Fields[0].Indexed = false }},
		{"level removed", func(k *MediaKindMeta) { k.Levels = k.Levels[:2] }},
		{"level reordered", func(k *MediaKindMeta) { k.Levels[0], k.Levels[1] = k.Levels[1], k.Levels[0] }},
		{"level reflagged", func(k *MediaKindMeta) { k.Levels[2].Playable = false }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			n := clone()
			tc.mut(&n[0])
			if err := ValidateMediaKindsUpgrade(base, n); err == nil {
				t.Fatal("want a non-additive upgrade error")
			}
		})
	}
	if err := ValidateMediaKindsUpgrade(base, nil); err == nil {
		t.Fatal("removing every kind must be refused")
	}
}

func TestContentPermissionSheet(t *testing.T) {
	m := contentManifest()
	lines, _, _ := PermissionSheet(m)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Searches your indexers") || !strings.Contains(joined, "Adds a library: Field recordings") {
		t.Fatalf("sheet lines: %v", lines)
	}
	cur := baseManifest()
	if diff := PermissionDiff(cur, m); len(diff) == 0 {
		t.Fatal("content upgrade must ask again for new permissions")
	}

	// A kind without a library block must not claim it adds a library, and
	// adding a library block on upgrade must surface in PermissionDiff.
	noLib := contentManifest()
	noLib.Ether.Contributes.MediaKinds[0].Library = KindLibraryDecl{}
	noLib.Ether.Contributes.MediaKinds[0].Present = core.Presentations{}
	noLibLines, _, _ := PermissionSheet(noLib)
	if strings.Contains(strings.Join(noLibLines, "\n"), "Adds a library:") {
		t.Fatalf("kind without library must not emit Adds a library: %v", noLibLines)
	}
	diff := PermissionDiff(noLib, m)
	if !strings.Contains(strings.Join(diff, "\n"), "Adds a library: Field recordings") {
		t.Fatalf("adding library declaration must appear in PermissionDiff, got %v", diff)
	}
}

func TestManifestLiveTabAndDateDisplay(t *testing.T) {
	searchOff := false
	searchOn := true

	ok := contentManifest()
	ok.Ether.Contributes.MediaKinds[0].Library.DateDisplay = "date"
	ok.Ether.Contributes.MediaKinds[0].Library.Tabs = []KindLibraryTab{
		{Key: "recommended", Label: "Recommended", Kind: "recommended"},
		{Key: "library", Label: "Library", Kind: "library"},
		{Key: "live", Label: "Live", Kind: "live", Card: "landscape", Search: &searchOn, SearchPlaceholder: "Search streams"},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid live tab + dateDisplay manifest rejected: %v", err)
	}
	if got := ok.Ether.Contributes.MediaKinds[0].Library.EffectiveDateDisplay(); got != "date" {
		t.Fatalf("EffectiveDateDisplay = %q, want date", got)
	}
	if got := ok.Ether.Contributes.MediaKinds[0].Library.Tabs[2].EffectiveCard(); got != "landscape" {
		t.Fatalf("EffectiveCard = %q, want landscape", got)
	}
	if !ok.Ether.Contributes.MediaKinds[0].Library.Tabs[2].EffectiveSearch() {
		t.Fatal("EffectiveSearch on live tab should be true")
	}

	// Default card & search on live tab when omitted.
	defLive := KindLibraryTab{Key: "live", Label: "Live", Kind: "live"}
	if defLive.EffectiveCard() != "landscape" || !defLive.EffectiveSearch() {
		t.Fatalf("live tab defaults: card=%q search=%v", defLive.EffectiveCard(), defLive.EffectiveSearch())
	}

	// Additive upgrade adding dateDisplay and a live tab must succeed.
	prev := []MediaKindMeta{validMediaKind()}
	next := []MediaKindMeta{ok.Ether.Contributes.MediaKinds[0]}
	if err := ValidateMediaKindsUpgrade(prev, next); err != nil {
		t.Fatalf("upgrade adding live tab and dateDisplay failed: %v", err)
	}

	cases := []struct {
		name  string
		field string
		mut   func(*MediaKindMeta)
	}{
		{
			name:  "invalid dateDisplay",
			field: ".library.dateDisplay",
			mut:   func(k *MediaKindMeta) { k.Library.DateDisplay = "month" },
		},
		{
			name:  "invalid live card",
			field: ".library.tabs[0].card",
			mut: func(k *MediaKindMeta) {
				k.Library.Tabs = []KindLibraryTab{{Key: "live", Label: "Live", Kind: "live", Card: "wide"}}
			},
		},
		{
			name:  "live tab with facet",
			field: ".library.tabs[0].facet",
			mut: func(k *MediaKindMeta) {
				k.Library.Tabs = []KindLibraryTab{{Key: "live", Label: "Live", Kind: "live", Facet: "habitat"}}
			},
		},
		{
			name:  "live tab with sort",
			field: ".library.tabs[0].sort",
			mut: func(k *MediaKindMeta) {
				k.Library.Tabs = []KindLibraryTab{{Key: "live", Label: "Live", Kind: "live", Sort: "title"}}
			},
		},
		{
			name:  "live tab searchPlaceholder when search disabled",
			field: ".library.tabs[0].searchPlaceholder",
			mut: func(k *MediaKindMeta) {
				k.Library.Tabs = []KindLibraryTab{{Key: "live", Label: "Live", Kind: "live", Search: &searchOff, SearchPlaceholder: "Find"}}
			},
		},
		{
			name:  "non-live tab with card",
			field: ".library.tabs[0].card",
			mut: func(k *MediaKindMeta) {
				k.Library.Tabs = []KindLibraryTab{{Key: "lib", Label: "Library", Kind: "library", Card: "landscape"}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := contentManifest()
			tc.mut(&m.Ether.Contributes.MediaKinds[0])
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
			verr := err.(*ValidationError)
			found := false
			for _, f := range verr.Fields {
				if strings.Contains(f.Field, tc.field) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want field containing %s in %v", tc.field, verr.Fields)
			}
		})
	}
}

func TestValidateKindLibraryShelves(t *testing.T) {
	docManifest := func(shelves []KindLibraryShelf) Manifest {
		m := contentManifest()
		m.Ether.Strings = map[string]json.RawMessage{"en": json.RawMessage(`{"shelf_science":"Science documentaries"}`)}
		k := &m.Ether.Contributes.MediaKinds[0]
		k.Key = "documentary"
		k.Label = "Documentaries"
		k.Library = KindLibraryDecl{
			Name:    "Documentaries",
			Shelves: shelves,
		}
		k.Fields = []KindField{
			{Key: "topic", Type: "strings", Label: "Topic", Indexed: true},
			{Key: "format", Type: "enum", Label: "Format", Indexed: true, Enum: []string{"feature", "miniseries", "short"}},
			{Key: "narrator", Type: "people", Label: "Narrator"},
			{Key: "notes", Type: "string", Label: "Editorial notes", Indexed: false},
		}
		k.Browse = KindBrowseDecl{
			Sorts:  []string{"title", "released"},
			Facets: []string{"topic"},
		}
		return m
	}

	// Valid shelves covering defaults, literal + $t: labels, single value, OR values,
	// multi-facet AND filters, people field, and envelope facets.
	validShelves := []KindLibraryShelf{
		{Key: "latest", Label: "Latest releases"},
		{Key: "science", Label: "$t:shelf_science", Sort: "rating", Direction: "desc", Facet: "topic", Value: "science"},
		{Key: "earth-space", Label: "Earth and space", Sort: "title", Direction: "asc", Facet: "topic", Values: []string{"earth", "space"}},
		{Key: "feature-history", Label: "Feature history", Filters: &KindLibraryShelfFilters{Facets: map[string][]string{"topic": {"history"}, "format": {"feature", "miniseries"}}}},
		{Key: "narrated", Label: "Narrated picks", Facet: "narrator", Value: "Elena Vance"},
		{Key: "recent-years", Label: "Recent years", Facet: "year", Values: []string{"2024", "2025"}},
	}
	if err := docManifest(validShelves).Validate(); err != nil {
		t.Fatalf("valid shelves failed validation: %v", err)
	}
	if validShelves[0].EffectiveSort() != "released" || validShelves[0].EffectiveDirection() != "desc" || validShelves[0].EffectiveFilters() != nil {
		t.Fatalf("shelf defaults = (%q, %q, %v)", validShelves[0].EffectiveSort(), validShelves[0].EffectiveDirection(), validShelves[0].EffectiveFilters())
	}
	if got := validShelves[1].EffectiveFilters(); len(got["topic"]) != 1 || got["topic"][0] != "science" {
		t.Fatalf("single-value filter = %v", got)
	}
	if got := validShelves[2].EffectiveFilters(); len(got["topic"]) != 2 || validShelves[2].EffectiveDirection() != "asc" {
		t.Fatalf("multi-value filter = %v, dir=%q", got, validShelves[2].EffectiveDirection())
	}

	// Up to MaxKindLibraryShelves (40) is valid.
	forty := make([]KindLibraryShelf, MaxKindLibraryShelves)
	for i := range forty {
		forty[i] = KindLibraryShelf{Key: fmt.Sprintf("shelf-%d", i+1), Label: fmt.Sprintf("Shelf %d", i+1)}
	}
	if err := docManifest(forty).Validate(); err != nil {
		t.Fatalf("40 shelves must be valid: %v", err)
	}

	cases := []struct {
		name    string
		shelves []KindLibraryShelf
		want    string
	}{
		{
			name:    "more than 40 shelves",
			shelves: append(append([]KindLibraryShelf(nil), forty...), KindLibraryShelf{Key: "shelf-41", Label: "Shelf 41"}),
			want:    ".library.shelves",
		},
		{
			name:    "invalid key uppercase",
			shelves: []KindLibraryShelf{{Key: "BadKey", Label: "Latest"}},
			want:    ".library.shelves[0].key",
		},
		{
			name:    "key too long",
			shelves: []KindLibraryShelf{{Key: "a" + strings.Repeat("b", 40), Label: "Latest"}},
			want:    ".library.shelves[0].key",
		},
		{
			name:    "duplicate key",
			shelves: []KindLibraryShelf{{Key: "latest", Label: "Latest"}, {Key: "latest", Label: "Also latest"}},
			want:    ".library.shelves[1].key",
		},
		{
			name:    "label over 60 chars",
			shelves: []KindLibraryShelf{{Key: "long", Label: strings.Repeat("a", 61)}},
			want:    ".library.shelves[0].label",
		},
		{
			name:    "empty label",
			shelves: []KindLibraryShelf{{Key: "empty", Label: ""}},
			want:    ".library.shelves[0].label",
		},
		{
			name:    "bad $t label key",
			shelves: []KindLibraryShelf{{Key: "badt", Label: "$t:Bad Key"}},
			want:    ".library.shelves[0].label",
		},
		{
			name:    "unknown sort",
			shelves: []KindLibraryShelf{{Key: "bad-sort", Label: "Bad sort", Sort: "views"}},
			want:    ".library.shelves[0].sort",
		},
		{
			name:    "unknown direction",
			shelves: []KindLibraryShelf{{Key: "bad-dir", Label: "Bad direction", Direction: "up"}},
			want:    ".library.shelves[0].direction",
		},
		{
			name:    "facet without value or values",
			shelves: []KindLibraryShelf{{Key: "no-val", Label: "Science", Facet: "topic"}},
			want:    ".library.shelves[0].value",
		},
		{
			name:    "facet with both value and values",
			shelves: []KindLibraryShelf{{Key: "both", Label: "Science", Facet: "topic", Value: "science", Values: []string{"nature"}}},
			want:    ".library.shelves[0].values",
		},
		{
			name:    "value without facet",
			shelves: []KindLibraryShelf{{Key: "orphan-val", Label: "Science", Value: "science"}},
			want:    ".library.shelves[0].value",
		},
		{
			name:    "values without facet",
			shelves: []KindLibraryShelf{{Key: "orphan-vals", Label: "Science", Values: []string{"science"}}},
			want:    ".library.shelves[0].values",
		},
		{
			name:    "facet combined with filters",
			shelves: []KindLibraryShelf{{Key: "mixed", Label: "Mixed", Facet: "topic", Value: "science", Filters: &KindLibraryShelfFilters{Facets: map[string][]string{"format": {"feature"}}}}},
			want:    ".library.shelves[0].filters",
		},
		{
			name:    "undeclared facet",
			shelves: []KindLibraryShelf{{Key: "unknown-facet", Label: "Unknown", Facet: "missing", Value: "x"}},
			want:    ".library.shelves[0].facet",
		},
		{
			name:    "unindexed facet",
			shelves: []KindLibraryShelf{{Key: "unindexed", Label: "Notes", Facet: "notes", Value: "x"}},
			want:    ".library.shelves[0].facet",
		},
		{
			name:    "unindexed filters facet",
			shelves: []KindLibraryShelf{{Key: "unindexed-filter", Label: "Notes", Filters: &KindLibraryShelfFilters{Facets: map[string][]string{"notes": {"x"}}}}},
			want:    ".library.shelves[0].filters.notes",
		},
		{
			name:    "empty filters slice",
			shelves: []KindLibraryShelf{{Key: "empty-slice", Label: "Topic", Filters: &KindLibraryShelfFilters{Facets: map[string][]string{"topic": {}}}}},
			want:    ".library.shelves[0].filters.topic",
		},
		{
			name:    "blank value in filters",
			shelves: []KindLibraryShelf{{Key: "blank-val", Label: "Topic", Filters: &KindLibraryShelfFilters{Facets: map[string][]string{"topic": {"  "}}}}},
			want:    ".library.shelves[0].filters.topic[0]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := docManifest(tc.shelves).Validate()
			if err == nil {
				t.Fatalf("expected validation error containing %s, got nil", tc.want)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			found := false
			for _, f := range ve.Fields {
				if strings.Contains(f.Field, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected field containing %s in %+v", tc.want, ve.Fields)
			}
		})
	}

	// Using $t: in shelf label requires strings.en.
	noStrings := docManifest([]KindLibraryShelf{{Key: "science", Label: "$t:shelf_science"}})
	noStrings.Ether.Strings = nil
	if err := noStrings.Validate(); err == nil {
		t.Fatal("expected missing strings.en error when shelf label uses $t:")
	}
}

func TestManifestKindLibraryViewTabsAnd50TabCap(t *testing.T) {
	makeDocManifest := func(tabs []KindLibraryTab) Manifest {
		m := contentManifest()
		k := m.Ether.Contributes.MediaKinds[0]
		k.Fields = []KindField{
			{Key: "topic", Type: "enum", Label: "Topic", Indexed: true, Enum: []string{"science", "nature", "history"}},
			{Key: "notes", Type: "string", Label: "Notes", Indexed: false},
		}
		k.Browse.Sorts = []string{"released", "title", "added"}
		k.Browse.Facets = []string{"topic"}
		k.Browse.SearchFields = nil
		k.Library.Tabs = tabs
		m.Ether.Contributes.MediaKinds[0] = k
		return m
	}

	// Valid Smart View tabs: History (timeline + lastViewed + viewer history), Favorites (list), and indexed-field filter (grid).
	searchOff := false
	validTabs := []KindLibraryTab{
		{Key: "recommended", Label: "Recommended", Kind: "recommended"},
		{Key: "library", Label: "Library", Kind: "library"},
		{
			Key: "history", Label: "History", Kind: "view", Layout: "timeline",
			Sort: "lastViewed", Direction: "desc", SearchPlaceholder: "Search watch history",
			Filters: &core.TabFilter{
				Match: "all",
				Rules: []core.CollectionRule{{Field: "viewer", Op: "is", Value: json.RawMessage(`{"history":true}`)}},
			},
		},
		{
			Key: "favorites", Label: "Favorites", Kind: "view", Layout: "list",
			Sort: "title", Direction: "asc", Search: &searchOff,
			Filters: &core.TabFilter{
				Match: "all",
				Rules: []core.CollectionRule{{Field: "viewer", Op: "is", Value: json.RawMessage(`{"favorite":true}`)}},
			},
		},
		{
			Key: "science", Label: "Science", Kind: "view", Layout: "grid",
			Sort: "released", Direction: "desc",
			Filters: &core.TabFilter{
				Match: "all",
				Rules: []core.CollectionRule{
					{Field: "browse:topic", Op: "is", Value: json.RawMessage(`"science"`)},
					{Field: "viewer", Op: "is", Value: json.RawMessage(`{"unwatched":true}`)},
				},
			},
		},
	}
	if err := makeDocManifest(validTabs).Validate(); err != nil {
		t.Fatalf("valid view tabs rejected: %v", err)
	}
	if !validTabs[2].EffectiveSearch() || validTabs[3].EffectiveSearch() {
		t.Fatalf("unexpected view tab EffectiveSearch(): history=%v favorites=%v", validTabs[2].EffectiveSearch(), validTabs[3].EffectiveSearch())
	}
	if validTabs[2].EffectiveLayout() != "timeline" || validTabs[3].EffectiveLayout() != "list" || validTabs[4].EffectiveLayout() != "grid" {
		t.Fatalf("unexpected view tab EffectiveLayout()")
	}

	// 50 tabs accepted, 51 rejected.
	fifty := make([]KindLibraryTab, MaxKindLibraryTabs)
	for i := range fifty {
		fifty[i] = KindLibraryTab{Key: fmt.Sprintf("tab-%d", i+1), Label: fmt.Sprintf("Tab %d", i+1), Kind: "view"}
	}
	if err := makeDocManifest(fifty).Validate(); err != nil {
		t.Fatalf("50 tabs should validate, got: %v", err)
	}
	fiftyOne := append(append([]KindLibraryTab(nil), fifty...), KindLibraryTab{Key: "tab-51", Label: "Tab 51", Kind: "view"})
	if err := makeDocManifest(fiftyOne).Validate(); err == nil {
		t.Fatal("51 tabs should be rejected")
	}

	// Invalid view tab configurations must be rejected.
	badCases := []struct {
		name string
		tab  KindLibraryTab
		want string
	}{
		{
			name: "invalid layout",
			tab:  KindLibraryTab{Key: "v", Label: "View", Kind: "view", Layout: "masonry"},
			want: ".layout",
		},
		{
			name: "layout on library tab",
			tab:  KindLibraryTab{Key: "lib", Label: "Library", Kind: "library", Layout: "list"},
			want: ".layout",
		},
		{
			name: "filters on library tab",
			tab: KindLibraryTab{
				Key: "lib", Label: "Library", Kind: "library",
				Filters: &core.TabFilter{Rules: []core.CollectionRule{{Field: "viewer", Op: "is", Value: json.RawMessage(`{"history":true}`)}}},
			},
			want: ".filters",
		},
		{
			name: "unindexed field in view filters",
			tab: KindLibraryTab{
				Key: "v", Label: "View", Kind: "view",
				Filters: &core.TabFilter{Rules: []core.CollectionRule{{Field: "browse:notes", Op: "is", Value: json.RawMessage(`"abc"`)}}},
			},
			want: ".filters",
		},
		{
			name: "undeclared built-in genre in view filters",
			tab: KindLibraryTab{
				Key: "v", Label: "View", Kind: "view",
				Filters: &core.TabFilter{Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["Drama"]`)}}},
			},
			want: ".filters",
		},
		{
			name: "unknown viewer field",
			tab: KindLibraryTab{
				Key: "v", Label: "View", Kind: "view",
				Filters: &core.TabFilter{Rules: []core.CollectionRule{{Field: "viewer", Op: "is", Value: json.RawMessage(`{"bogusFlag":true}`)}}},
			},
			want: ".filters",
		},
		{
			name: "empty viewer condition",
			tab: KindLibraryTab{
				Key: "v", Label: "View", Kind: "view",
				Filters: &core.TabFilter{Rules: []core.CollectionRule{{Field: "viewer", Op: "is", Value: json.RawMessage(`{}`)}}},
			},
			want: ".filters",
		},
		{
			name: "facet on view tab",
			tab:  KindLibraryTab{Key: "v", Label: "View", Kind: "view", Facet: "topic"},
			want: ".facet",
		},
		{
			name: "undeclared sort on view tab",
			tab:  KindLibraryTab{Key: "v", Label: "View", Kind: "view", Sort: "rating"},
			want: ".sort",
		},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			err := makeDocManifest([]KindLibraryTab{tc.tab}).Validate()
			if err == nil {
				t.Fatalf("expected error containing %s, got nil", tc.want)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			found := false
			for _, f := range ve.Fields {
				if strings.Contains(f.Field, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected field containing %s in %+v", tc.want, ve.Fields)
			}
		})
	}
}

func TestSourcesBackgroundCheckPermissionAndDiff(t *testing.T) {
	for _, level := range []string{"search+check", "acquire+check"} {
		m := contentManifest()
		m.Ether.Permissions.Sources = level
		if err := m.Validate(); err != nil {
			t.Fatalf("permissions.sources=%q unexpectedly invalid: %v", level, err)
		}
		lines, _, _ := PermissionSheet(m)
		wantCheck := "Checks Usenet releases in the background within a daily allowance"
		found := false
		for _, line := range lines {
			if line == wantCheck {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("PermissionSheet(%q) missing %q in %v", level, wantCheck, lines)
		}
	}

	// Upgrading search -> search+check reports only the background check addition.
	mSearch := contentManifest()
	mSearch.Ether.Permissions.Sources = "search"
	mSearchCheck := contentManifest()
	mSearchCheck.Ether.Permissions.Sources = "search+check"
	diff := PermissionDiff(mSearch, mSearchCheck)
	if len(diff) != 1 || diff[0] != "Checks Usenet releases in the background within a daily allowance" {
		t.Fatalf("diff(search->search+check) = %v", diff)
	}

	// Upgrading search+check -> acquire reports only the acquire upgrade.
	mAcquire := contentManifest()
	mAcquire.Ether.Permissions.Sources = "acquire"
	diff = PermissionDiff(mSearchCheck, mAcquire)
	if len(diff) != 1 || diff[0] != "Searches your indexers and opens sources through your debrid accounts" {
		t.Fatalf("diff(search+check->acquire) = %v", diff)
	}

	// Upgrading search -> acquire+check reports both acquire and background check.
	mAcquireCheck := contentManifest()
	mAcquireCheck.Ether.Permissions.Sources = "acquire+check"
	diff = PermissionDiff(mSearch, mAcquireCheck)
	if len(diff) != 2 {
		t.Fatalf("diff(search->acquire+check) = %v, want 2 lines", diff)
	}
}
