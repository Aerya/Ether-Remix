package plugins

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestMediaKindMediaClassRoleAndLevelValidation(t *testing.T) {
	base := func(mutate func(*MediaKindMeta)) Manifest {
		k := MediaKindMeta{
			Key:   "music",
			Label: "Music",
			Media: string(core.MediaClassMusic),
			Library: KindLibraryDecl{
				Name: "Music",
				Card: "square",
				Tabs: []KindLibraryTab{
					{Key: "recommended", Label: "Recommended", Kind: "recommended"},
					{Key: "artists", Label: "Artists", Kind: "library", Level: "artist"},
					{Key: "albums", Label: "Albums", Kind: "library", Level: "album"},
					{Key: "tracks", Label: "Tracks", Kind: "view", Level: "track", Sort: "lastViewed", Direction: "desc"},
					{Key: "genres", Label: "Genres", Kind: "collections", Level: "album", Facet: "genre"},
					{Key: "composers", Label: "Composers", Kind: "people", Level: "track", Facet: "composer"},
				},
				Shelves: []KindLibraryShelf{
					{Key: "recent-albums", Label: "Jump back in", Level: "album", Sort: "lastViewed", Direction: "desc"},
				},
			},
			Levels: []KindLevel{
				{Key: "artist", Label: "Artist", Role: string(core.MediaRoleArtist), Container: true},
				{Key: "album", Label: "Album", Role: string(core.MediaRoleAlbum), Container: true},
				{Key: "track", Label: "Track", Role: string(core.MediaRoleTrack), Playable: true},
			},
			Fields: []KindField{
				{Key: "genre", Type: "strings", Label: "Genre", Indexed: true},
				{Key: "composer", Type: "people", Label: "Composer", Indexed: true},
			},
			Present: core.Presentations{
				Vela: &core.VelaPresentation{Type: "shows"},
			},
			Browse: KindBrowseDecl{
				Sorts:  []string{"title", "released", "added", "lastViewed"},
				Facets: []string{"genre", "composer"},
			},
			Sync: KindSyncDecl{Mode: KindSyncPush},
		}
		if mutate != nil {
			mutate(&k)
		}
		m := contentManifest()
		m.ID = "community.example.music"
		m.Name = "Music"
		m.Ether.Capabilities = []Capability{CapCatalogProvider, CapKindResolver}
		m.Ether.Contributes.MediaKinds = []MediaKindMeta{k}
		return m
	}

	if err := base(nil).Validate(); err != nil {
		t.Fatalf("valid multi-level music manifest failed validation: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*MediaKindMeta)
		want   string
	}{
		{
			name:   "invalid media class",
			mutate: func(k *MediaKindMeta) { k.Media = "vr" },
			want:   ".media",
		},
		{
			name:   "invalid level role",
			mutate: func(k *MediaKindMeta) { k.Levels[0].Role = "bad\nrole" },
			want:   ".levels[0].role",
		},
		{
			name: "tab level undeclared",
			mutate: func(k *MediaKindMeta) {
				k.Library.Tabs[1].Level = "disc"
			},
			want: ".library.tabs[1].level",
		},
		{
			name: "tab level on recommended tab",
			mutate: func(k *MediaKindMeta) {
				k.Library.Tabs[0].Level = "album"
			},
			want: ".library.tabs[0].level",
		},
		{
			name: "tab level on live tab",
			mutate: func(k *MediaKindMeta) {
				k.Library.Tabs = append(k.Library.Tabs, KindLibraryTab{Key: "radio", Label: "Radio", Kind: "live", Level: "track"})
			},
			want: ".level",
		},
		{
			name: "shelf level undeclared",
			mutate: func(k *MediaKindMeta) {
				k.Library.Shelves[0].Level = "disc"
			},
			want: ".library.shelves[0].level",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := base(tc.mutate).Validate()
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestKindLibraryShelfFiltersAllJSONShapes(t *testing.T) {
	makeManifestJSON := func(filtersJSON string) []byte {
		raw := `{
			"id": "community.example.music",
			"version": "1.0.0",
			"name": "Music",
			"ether": {
				"protocol": 1,
				"kind": "content",
				"summary": "Adds a music library",
				"runtime": { "kind": "remote" },
				"capabilities": ["catalog_provider", "kind_resolver"],
				"contributes": {
					"mediaKinds": [{
						"key": "music",
						"label": "Music",
						"media": "music",
						"library": {
							"name": "Music",
							"shelves": [{
								"key": "shelf-one",
								"label": "Jump back in",
								"level": "album",
								"sort": "lastViewed",
								"direction": "desc",
								"filters": ` + filtersJSON + `
							}]
						},
						"levels": [
							{ "key": "artist", "label": "Artist", "role": "artist" },
							{ "key": "album", "label": "Album", "role": "album" },
							{ "key": "track", "label": "Track", "role": "track", "playable": true }
						],
						"fields": [
							{ "key": "genre", "type": "strings", "label": "Genre", "indexed": true }
						],
						"present": { "vela": { "type": "movies" } },
						"browse": { "sorts": ["title", "released", "lastViewed"], "facets": ["genre"] },
						"sync": { "mode": "push" }
					}]
				}
			}
		}`
		return []byte(raw)
	}

	validShapes := []struct {
		name      string
		json      string
		wantTab   bool
		wantFacet bool
	}{
		{
			name:      "legacy facet map",
			json:      `{"genre": ["Jazz", "Soul"]}`,
			wantFacet: true,
		},
		{
			name:    "TabFilter object with explicit op",
			json:    `{"match": "all", "rules": [{"field": "viewer", "op": "is", "value": {"history": true}}]}`,
			wantTab: true,
		},
		{
			name:    "single viewer rule shorthand without op",
			json:    `{"field": "viewer", "value": {"history": true}}`,
			wantTab: true,
		},
		{
			name:    "rule array shorthand without op",
			json:    `[{"field": "viewer", "value": {"history": true}}]`,
			wantTab: true,
		},
	}

	for _, tc := range validShapes {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest(makeManifestJSON(tc.json))
			if err != nil {
				t.Fatalf("ParseManifest: %v", err)
			}
			if err := m.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			sh := m.Ether.Contributes.MediaKinds[0].Library.Shelves[0]
			if tc.wantTab && sh.EffectiveTabFilter() == nil {
				t.Fatalf("expected EffectiveTabFilter() != nil, got nil")
			}
			if tc.wantFacet && len(sh.EffectiveFilters()) == 0 {
				t.Fatalf("expected EffectiveFilters() non-empty, got %v", sh.EffectiveFilters())
			}
			// Round-trip through JSON must remain valid.
			b, err := json.Marshal(sh)
			if err != nil {
				t.Fatalf("Marshal shelf: %v", err)
			}
			var rt KindLibraryShelf
			if err := json.Unmarshal(b, &rt); err != nil {
				t.Fatalf("Unmarshal round-trip shelf: %v", err)
			}
			if tc.wantTab && rt.EffectiveTabFilter() == nil {
				t.Fatalf("round-trip lost EffectiveTabFilter")
			}
		})
	}

	// Invalid rule inside rule-based shelf filters must fail validation.
	badViewer, err := ParseManifest(makeManifestJSON(`[{"field": "viewer", "value": {}}]`))
	if err != nil {
		t.Fatalf("ParseManifest bad viewer: %v", err)
	}
	if err := badViewer.Validate(); err == nil || !strings.Contains(err.Error(), ".library.shelves[0].filters") {
		t.Fatalf("empty viewer rule should fail validation, got %v", err)
	}

	// Undeclared field in rule-based shelf filters must fail validation.
	badField, err := ParseManifest(makeManifestJSON(`[{"field": "browse:unknown", "op": "in", "value": ["x"]}]`))
	if err != nil {
		t.Fatalf("ParseManifest bad field: %v", err)
	}
	if err := badField.Validate(); err == nil || !strings.Contains(err.Error(), ".library.shelves[0].filters") {
		t.Fatalf("undeclared field rule should fail validation, got %v", err)
	}
}
