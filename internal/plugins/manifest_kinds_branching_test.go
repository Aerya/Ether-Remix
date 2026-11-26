package plugins

import (
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func branchingMusicKindManifest(mutate func(*MediaKindMeta)) Manifest {
	k := MediaKindMeta{
		Key:   "music",
		Label: "Music",
		Media: string(core.MediaClassMusic),
		Library: KindLibraryDecl{
			Name: "Music",
			Card: "square",
			Tabs: []KindLibraryTab{
				{Key: "recommended", Label: "Recommended", Kind: "recommended"},
				{
					Key:   "concerts",
					Label: "Concerts",
					Kind:  "recommended",
					Card:  "landscape",
					Shelves: []KindLibraryShelf{
						{Key: "recent-concerts", Label: "Recent concerts", Level: "concert", Sort: "added", Direction: "desc"},
						{Key: "top-concerts", Label: "Top concerts", Level: "concert", Sort: "rating", Direction: "desc"},
					},
				},
				{Key: "artists", Label: "Artists", Kind: "library", Level: "artist"},
				{Key: "albums", Label: "Albums", Kind: "library", Level: "album"},
				{Key: "concert-list", Label: "All concerts", Kind: "library", Level: "concert"},
			},
			Shelves: []KindLibraryShelf{
				{Key: "recent-albums", Label: "Jump back in", Level: "album", Sort: "lastViewed", Direction: "desc"},
			},
		},
		Levels: []KindLevel{
			{Key: "artist", Label: "Artist", Role: "artist", Container: true},
			{Key: "album", Label: "Album", Parent: "artist", Role: "album", Container: true},
			{Key: "track", Label: "Track", Parent: "album", Role: "track", Playable: true},
			{Key: "concert", Label: "Concert", Parent: "artist", Media: "video", Role: "concert", Playable: true},
		},
		Fields: []KindField{
			{Key: "genre", Type: "strings", Label: "Genre", Indexed: true},
		},
		Present: core.Presentations{
			Vela: &core.VelaPresentation{Type: "shows"},
			Plex: &core.PlexPresentation{Base: "artist"},
			MediaBrowser: &core.MediaBrowserPresentation{
				CollectionType: "music",
				ItemTypes: map[string]string{
					"artist":  "MusicArtist",
					"album":   "MusicAlbum",
					"track":   "Audio",
					"concert": "Video",
				},
			},
		},
		Browse: KindBrowseDecl{
			Sorts:  []string{"title", "released", "added", "lastViewed", "rating"},
			Facets: []string{"genre"},
		},
		Sync: KindSyncDecl{Mode: KindSyncPush},
	}
	if mutate != nil {
		mutate(&k)
	}
	m := contentManifest()
	m.ID = "community.example.music-branching"
	m.Name = "Music"
	m.Ether.Capabilities = []Capability{CapCatalogProvider, CapKindResolver}
	m.Ether.Contributes.MediaKinds = []MediaKindMeta{k}
	return m
}

func TestBranchingLevelsAndTabShelvesValidation(t *testing.T) {
	valid := branchingMusicKindManifest(nil)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid branching manifest rejected: %v", err)
	}

	kind := valid.Ether.Contributes.MediaKinds[0]
	if got := kind.LevelParent("artist"); got != "" {
		t.Fatalf("artist parent = %q, want empty", got)
	}
	if got := kind.LevelParent("album"); got != "artist" {
		t.Fatalf("album parent = %q, want artist", got)
	}
	if got := kind.LevelParent("track"); got != "album" {
		t.Fatalf("track parent = %q, want album", got)
	}
	if got := kind.LevelParent("concert"); got != "artist" {
		t.Fatalf("concert parent = %q, want artist", got)
	}
	childKeys := func(parent string) []string {
		var out []string
		for _, lv := range kind.ChildLevels(parent) {
			out = append(out, lv.Key)
		}
		return out
	}
	if got := childKeys("artist"); !slices.Equal(got, []string{"album", "concert"}) {
		t.Fatalf("artist children = %v, want [album concert]", got)
	}
	if got := childKeys("album"); !slices.Equal(got, []string{"track"}) {
		t.Fatalf("album children = %v, want [track]", got)
	}
	if !kind.IsLeafLevel("track") || !kind.IsLeafLevel("concert") || kind.IsLeafLevel("album") || kind.IsLeafLevel("artist") {
		t.Fatalf("leaf detection wrong: track=%v concert=%v album=%v artist=%v",
			kind.IsLeafLevel("track"), kind.IsLeafLevel("concert"), kind.IsLeafLevel("album"), kind.IsLeafLevel("artist"))
	}
	if kind.LevelDepth("artist") != 0 || kind.LevelDepth("album") != 1 || kind.LevelDepth("track") != 2 || kind.LevelDepth("concert") != 1 {
		t.Fatalf("depths = artist:%d album:%d track:%d concert:%d",
			kind.LevelDepth("artist"), kind.LevelDepth("album"), kind.LevelDepth("track"), kind.LevelDepth("concert"))
	}
	if !kind.IsVideoTitleLevel("concert") || kind.IsVideoTitleLevel("track") || kind.IsVideoTitleLevel("album") {
		t.Fatalf("IsVideoTitleLevel wrong: concert=%v track=%v album=%v",
			kind.IsVideoTitleLevel("concert"), kind.IsVideoTitleLevel("track"), kind.IsVideoTitleLevel("album"))
	}
	if got := kind.Library.Tabs[1].EffectiveCard(); got != "landscape" {
		t.Fatalf("recommended tab EffectiveCard = %q, want landscape", got)
	}

	// Default parent behaviour (linear manifest with empty Parent) must remain unchanged.
	linear := branchingMusicKindManifest(func(k *MediaKindMeta) {
		k.Levels = []KindLevel{
			{Key: "artist", Label: "Artist", Role: "artist", Container: true},
			{Key: "album", Label: "Album", Role: "album", Container: true},
			{Key: "track", Label: "Track", Role: "track", Playable: true},
		}
		delete(k.Present.MediaBrowser.ItemTypes, "concert")
		k.Library.Tabs = k.Library.Tabs[:1]
	})
	if err := linear.Validate(); err != nil {
		t.Fatalf("linear manifest rejected: %v", err)
	}
	lk := linear.Ether.Contributes.MediaKinds[0]
	if lk.LevelParent("album") != "artist" || lk.LevelParent("track") != "album" {
		t.Fatalf("linear default parents broken: album->%q track->%q", lk.LevelParent("album"), lk.LevelParent("track"))
	}

	cases := []struct {
		name   string
		mutate func(*MediaKindMeta)
		want   string
	}{
		{
			name:   "root sets parent",
			mutate: func(k *MediaKindMeta) { k.Levels[0].Parent = "album" },
			want:   ".levels[0].parent",
		},
		{
			name:   "self parent cycle",
			mutate: func(k *MediaKindMeta) { k.Levels[1].Parent = "album" },
			want:   ".levels[1].parent",
		},
		{
			name:   "forward reference parent",
			mutate: func(k *MediaKindMeta) { k.Levels[1].Parent = "concert" },
			want:   ".levels[1].parent",
		},
		{
			name:   "unknown parent",
			mutate: func(k *MediaKindMeta) { k.Levels[3].Parent = "venue" },
			want:   ".levels[3].parent",
		},
		{
			name:   "invalid level media",
			mutate: func(k *MediaKindMeta) { k.Levels[3].Media = "hologram" },
			want:   ".levels[3].media",
		},
		{
			name:   "level role with control char",
			mutate: func(k *MediaKindMeta) { k.Levels[3].Role = "concert\tfilm" },
			want:   ".levels[3].role",
		},
		{
			name:   "level role too long",
			mutate: func(k *MediaKindMeta) { k.Levels[3].Role = strings.Repeat("a", 41) },
			want:   ".levels[3].role",
		},
		{
			name: "shelves on non-recommended tab",
			mutate: func(k *MediaKindMeta) {
				k.Library.Tabs[2].Shelves = []KindLibraryShelf{{Key: "bad", Label: "Bad", Level: "artist"}}
			},
			want: ".library.tabs[2].shelves",
		},
		{
			name: "invalid card on recommended tab",
			mutate: func(k *MediaKindMeta) {
				k.Library.Tabs[1].Card = "widescreen"
			},
			want: ".library.tabs[1].card",
		},
		{
			name: "tab shelf references unknown level",
			mutate: func(k *MediaKindMeta) {
				k.Library.Tabs[1].Shelves[0].Level = "setlist"
			},
			want: ".library.tabs[1].shelves[0].level",
		},
		{
			name: "duplicate shelf key within tab",
			mutate: func(k *MediaKindMeta) {
				k.Library.Tabs[1].Shelves[1].Key = k.Library.Tabs[1].Shelves[0].Key
			},
			want: ".library.tabs[1].shelves[1].key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := branchingMusicKindManifest(tc.mutate).Validate()
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateMediaKindsUpgradeBranchingLevels(t *testing.T) {
	prev := []MediaKindMeta{{
		Key:   "music",
		Label: "Music",
		Media: "music",
		Levels: []KindLevel{
			{Key: "artist", Label: "Artist", Role: "artist", Container: true},
			{Key: "album", Label: "Album", Role: "album", Container: true},
			{Key: "track", Label: "Track", Role: "track", Playable: true},
		},
	}}

	// Adding a trailing branching level and making implicit parents explicit is valid.
	addBranch := []MediaKindMeta{{
		Key:   "music",
		Label: "Music",
		Media: "music",
		Levels: []KindLevel{
			{Key: "artist", Label: "Artist", Role: "artist", Container: true},
			{Key: "album", Label: "Album", Parent: "artist", Role: "album", Container: true},
			{Key: "track", Label: "Track", Parent: "album", Role: "track", Playable: true},
			{Key: "concert", Label: "Concert", Parent: "artist", Media: "video", Role: "concert", Playable: true},
		},
	}}
	if err := ValidateMediaKindsUpgrade(prev, addBranch); err != nil {
		t.Fatalf("adding trailing branching level should be compatible: %v", err)
	}

	// Reparenting an existing level must be rejected.
	reparent := []MediaKindMeta{{
		Key:   "music",
		Label: "Music",
		Media: "music",
		Levels: []KindLevel{
			{Key: "artist", Label: "Artist", Role: "artist", Container: true},
			{Key: "album", Label: "Album", Parent: "artist", Role: "album", Container: true},
			{Key: "track", Label: "Track", Parent: "artist", Role: "track", Playable: true},
		},
	}}
	if err := ValidateMediaKindsUpgrade(prev, reparent); err == nil || !strings.Contains(err.Error(), "parent") {
		t.Fatalf("reparenting existing level error = %v, want parent mismatch error", err)
	}
}

func TestListLevelValidation(t *testing.T) {
	withList := func(extra func(*MediaKindMeta)) error {
		m := branchingMusicKindManifest(func(k *MediaKindMeta) {
			k.Levels = append(k.Levels, KindLevel{Key: "playlist", Label: "Playlist", Role: "playlist", Container: true, Members: true})
			if extra != nil {
				extra(k)
			}
		})
		return m.Validate()
	}
	if err := withList(nil); err != nil {
		t.Fatalf("list level rejected: %v", err)
	}
	m := branchingMusicKindManifest(func(k *MediaKindMeta) {
		k.Levels = append(k.Levels, KindLevel{Key: "playlist", Label: "Playlist", Container: true, Members: true})
	})
	if got := m.Ether.Contributes.MediaKinds[0].LevelParent("playlist"); got != "" {
		t.Fatalf("a list level is a root, parent = %q", got)
	}
	for name, bad := range map[string]func(*MediaKindMeta){
		"playable": func(k *MediaKindMeta) { k.Levels[len(k.Levels)-1].Playable = true },
		"parent":   func(k *MediaKindMeta) { k.Levels[len(k.Levels)-1].Parent = "artist" },
		"parent of another level": func(k *MediaKindMeta) {
			k.Levels[3] = KindLevel{Key: "entry", Label: "Entry", Parent: "album", Playable: true}
			k.Levels[3], k.Levels[4] = k.Levels[4], k.Levels[3]
			k.Levels[4].Parent = "playlist"
			k.Library.Tabs, k.Library.Shelves = nil, nil
		},
	} {
		if err := withList(bad); err == nil || !strings.Contains(err.Error(), "list level") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
