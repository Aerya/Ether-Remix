package libraries

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func appByName(t *testing.T, r core.ResolvedPresentations, name string) core.ResolvedAppPresentation {
	t.Helper()
	for _, a := range r.Apps {
		if a.App == name {
			return a
		}
	}
	t.Fatalf("app %q not found in resolved presentations", name)
	return core.ResolvedAppPresentation{}
}

func TestResolveKindPresentations_PureInferenceShapes(t *testing.T) {
	t.Run("1-level playable video", func(t *testing.T) {
		r := ResolveKindPresentations(KindPresentationInput{
			Media: string(core.MediaClassVideo),
			Levels: []KindPresentationLevel{
				{Key: "clip", Playable: true},
			},
		}, core.PresentationOverrides{})

		if r.MediaClass != core.MediaClassVideo {
			t.Fatalf("MediaClass = %q, want video", r.MediaClass)
		}
		if r.Effective.Vela == nil || r.Effective.Vela.Type != "movies" {
			t.Fatalf("vela = %+v, want movies", r.Effective.Vela)
		}
		if r.Effective.Plex == nil || r.Effective.Plex.Base != "movie" {
			t.Fatalf("plex = %+v, want movie", r.Effective.Plex)
		}
		if r.Effective.MediaBrowser == nil || r.Effective.MediaBrowser.CollectionType != "movies" ||
			!reflect.DeepEqual(r.Effective.MediaBrowser.ItemTypes, map[string]string{"clip": "Movie"}) {
			t.Fatalf("mediaBrowser = %+v, want movies/{clip:Movie}", r.Effective.MediaBrowser)
		}
		if r.Effective.Stremio == nil || r.Effective.Stremio.Type != "movie" {
			t.Fatalf("stremio = %+v, want movie", r.Effective.Stremio)
		}
		if r.Effective.Xtream == nil || r.Effective.Xtream.Category != "movie" {
			t.Fatalf("xtream = %+v, want movie", r.Effective.Xtream)
		}
		if r.Effective.Silo == nil || r.Effective.Silo.Type != "movies" {
			t.Fatalf("silo = %+v, want movies", r.Effective.Silo)
		}
		for _, a := range r.Apps {
			if !a.Available || !a.Enabled || a.Source != core.PresentationSourceInferred {
				t.Errorf("app %s = %+v, want available+enabled+inferred", a.App, a)
			}
		}
	})

	t.Run("2-level and 3-level video shows", func(t *testing.T) {
		r2 := ResolveKindPresentations(KindPresentationInput{
			Media: string(core.MediaClassVideo),
			Levels: []KindPresentationLevel{
				{Key: "series", Container: true},
				{Key: "episode", Playable: true},
			},
		}, core.PresentationOverrides{})
		if r2.Effective.Plex == nil || r2.Effective.Plex.Base != "show" {
			t.Fatalf("2-level plex = %+v, want show", r2.Effective.Plex)
		}
		if r2.Effective.MediaBrowser == nil || r2.Effective.MediaBrowser.CollectionType != "tvshows" ||
			!reflect.DeepEqual(r2.Effective.MediaBrowser.ItemTypes, map[string]string{"series": "Series", "episode": "Episode"}) {
			t.Fatalf("2-level mb = %+v", r2.Effective.MediaBrowser)
		}
		if r2.Effective.Vela == nil || r2.Effective.Vela.Type != "shows" ||
			r2.Effective.Stremio == nil || r2.Effective.Stremio.Type != "series" ||
			r2.Effective.Xtream == nil || r2.Effective.Xtream.Category != "series" ||
			r2.Effective.Silo == nil || r2.Effective.Silo.Type != "tvshows" {
			t.Fatalf("2-level effective = %+v", r2.Effective)
		}

		r3 := ResolveKindPresentations(KindPresentationInput{
			Media: string(core.MediaClassVideo),
			Levels: []KindPresentationLevel{
				{Key: "show", Container: true},
				{Key: "season", Container: true},
				{Key: "episode", Playable: true},
			},
		}, core.PresentationOverrides{})
		want3 := map[string]string{"show": "Series", "season": "Season", "episode": "Episode"}
		if !reflect.DeepEqual(r3.Effective.MediaBrowser.ItemTypes, want3) {
			t.Fatalf("3-level itemTypes = %v, want %v", r3.Effective.MediaBrowser.ItemTypes, want3)
		}
	})

	t.Run("1-level, 2-level, and 3-level music", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			levels    []KindPresentationLevel
			wantVela  string
			wantTypes map[string]string
		}{
			{
				name:      "1-level playable album",
				levels:    []KindPresentationLevel{{Key: "album", Playable: true}},
				wantVela:  "movies",
				wantTypes: map[string]string{"album": "MusicAlbum"},
			},
			{
				name: "2-level album/track",
				levels: []KindPresentationLevel{
					{Key: "album", Container: true},
					{Key: "track", Playable: true},
				},
				wantVela:  "shows",
				wantTypes: map[string]string{"album": "MusicAlbum", "track": "Audio"},
			},
			{
				name: "3-level artist/album/track",
				levels: []KindPresentationLevel{
					{Key: "artist", Container: true},
					{Key: "album", Container: true},
					{Key: "track", Playable: true},
				},
				wantVela:  "shows",
				wantTypes: map[string]string{"artist": "MusicArtist", "album": "MusicAlbum", "track": "Audio"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := ResolveKindPresentations(KindPresentationInput{
					Media:  string(core.MediaClassMusic),
					Levels: tc.levels,
				}, core.PresentationOverrides{})
				if r.MediaClass != core.MediaClassMusic {
					t.Fatalf("MediaClass = %q", r.MediaClass)
				}
				if r.Effective.Vela == nil || r.Effective.Vela.Type != tc.wantVela {
					t.Fatalf("vela = %+v, want %q", r.Effective.Vela, tc.wantVela)
				}
				if r.Effective.Plex == nil || r.Effective.Plex.Base != "artist" {
					t.Fatalf("plex = %+v", r.Effective.Plex)
				}
				if r.Effective.MediaBrowser == nil || r.Effective.MediaBrowser.CollectionType != "music" ||
					!reflect.DeepEqual(r.Effective.MediaBrowser.ItemTypes, tc.wantTypes) {
					t.Fatalf("mb = %+v, want %v", r.Effective.MediaBrowser, tc.wantTypes)
				}
				for _, unavail := range []string{"stremio", "xtream", "silo"} {
					a := appByName(t, r, unavail)
					if a.Available || a.Enabled || a.Source != core.PresentationSourceUnavailable || a.Reason == "" {
						t.Errorf("%s = %+v, want unavailable with reason", unavail, a)
					}
				}
				if r.Effective.Stremio != nil || r.Effective.Xtream != nil || r.Effective.Silo != nil {
					t.Fatalf("video-only apps leaked into music effective: %+v", r.Effective)
				}
			})
		}
	})

	t.Run("books, audiobooks, podcasts, and photos", func(t *testing.T) {
		for _, tc := range []struct {
			class       core.MediaClass
			levels      []KindPresentationLevel
			wantVela    string
			wantMBCol   string
			wantMBTypes map[string]string
			wantPlex    string // "" means unavailable
			wantPlexWhy string
		}{
			{
				class:       core.MediaClassBook,
				levels:      []KindPresentationLevel{{Key: "book", Playable: true}},
				wantVela:    "movies",
				wantMBCol:   "books",
				wantMBTypes: map[string]string{"book": "Book"},
				wantPlexWhy: reasonPlexBook,
			},
			{
				class: core.MediaClassAudiobook,
				levels: []KindPresentationLevel{
					{Key: "book", Container: true},
					{Key: "chapter", Playable: true},
				},
				wantVela:    "shows",
				wantMBCol:   "audiobooks",
				wantMBTypes: map[string]string{"book": "MusicAlbum", "chapter": "Audio"},
				wantPlexWhy: reasonPlexAudiobook,
			},
			{
				class: core.MediaClassPodcast,
				levels: []KindPresentationLevel{
					{Key: "podcast", Container: true},
					{Key: "episode", Playable: true},
				},
				wantVela:    "shows",
				wantMBCol:   "music",
				wantMBTypes: map[string]string{"podcast": "MusicAlbum", "episode": "Audio"},
				wantPlexWhy: reasonPlexPodcast,
			},
			{
				class: core.MediaClassPhoto,
				levels: []KindPresentationLevel{
					{Key: "album", Container: true},
					{Key: "photo", Playable: true},
				},
				wantVela:    "shows",
				wantMBCol:   "photos",
				wantMBTypes: map[string]string{"album": "PhotoAlbum", "photo": "Photo"},
				wantPlex:    "photo",
			},
		} {
			t.Run(string(tc.class), func(t *testing.T) {
				r := ResolveKindPresentations(KindPresentationInput{
					Media:  string(tc.class),
					Levels: tc.levels,
				}, core.PresentationOverrides{})
				if r.Effective.Vela == nil || r.Effective.Vela.Type != tc.wantVela {
					t.Fatalf("vela = %+v, want %q", r.Effective.Vela, tc.wantVela)
				}
				if r.Effective.MediaBrowser == nil || r.Effective.MediaBrowser.CollectionType != tc.wantMBCol ||
					!reflect.DeepEqual(r.Effective.MediaBrowser.ItemTypes, tc.wantMBTypes) {
					t.Fatalf("mb = %+v, want %q / %v", r.Effective.MediaBrowser, tc.wantMBCol, tc.wantMBTypes)
				}
				plexApp := appByName(t, r, "plex")
				if tc.wantPlex == "" {
					if plexApp.Available || plexApp.Enabled || plexApp.Reason != tc.wantPlexWhy {
						t.Fatalf("plex = %+v, want unavailable (%q)", plexApp, tc.wantPlexWhy)
					}
					if r.Effective.Plex != nil {
						t.Fatalf("effective.Plex = %+v, want nil", r.Effective.Plex)
					}
				} else {
					if !plexApp.Available || !plexApp.Enabled || r.Effective.Plex == nil || r.Effective.Plex.Base != tc.wantPlex {
						t.Fatalf("plex = %+v / %+v, want %q", plexApp, r.Effective.Plex, tc.wantPlex)
					}
				}
			})
		}
	})
}

func TestResolveKindPresentations_PrecedenceAndPartialItemTypes(t *testing.T) {
	// Explicit kind.media + level.role beats present.mediaBrowser and level key names.
	r := ResolveKindPresentations(KindPresentationInput{
		Media: string(core.MediaClassMusic),
		Levels: []KindPresentationLevel{
			{Key: "release", Role: string(core.MediaRoleAlbum), Container: true},
			{Key: "cut", Role: string(core.MediaRoleTrack), Playable: true},
		},
		Present: core.Presentations{
			// Plugin only supplied collectionType + one level's itemType;
			// Ether must fill the unmapped level rather than leaving it empty.
			MediaBrowser: &core.MediaBrowserPresentation{
				CollectionType: "music",
				ItemTypes:      map[string]string{"release": "MusicAlbum"},
			},
		},
	}, core.PresentationOverrides{})

	if got := r.Effective.MediaBrowser.ItemTypes; !reflect.DeepEqual(got, map[string]string{"release": "MusicAlbum", "cut": "Audio"}) {
		t.Fatalf("partial itemTypes not completed: %v", got)
	}
	if appByName(t, r, "mediaBrowser").Source != core.PresentationSourcePlugin {
		t.Fatalf("mediaBrowser source should be plugin when collectionType declared")
	}
	if appByName(t, r, "plex").Source != core.PresentationSourceInferred || r.Effective.Plex.Base != "artist" {
		t.Fatalf("plex should be inferred artist: %+v", r.Effective.Plex)
	}

	// Fallback priority when kind.media is absent:
	// 1. present.mediaBrowser.collectionType -> music
	rFromMB := ResolveKindPresentations(KindPresentationInput{
		Levels: []KindPresentationLevel{{Key: "unit", Playable: true}},
		Present: core.Presentations{
			MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "music"},
		},
	}, core.PresentationOverrides{})
	if rFromMB.MediaClass != core.MediaClassMusic {
		t.Fatalf("class from mb = %q, want music", rFromMB.MediaClass)
	}

	// 2. present.plex.base -> photo
	rFromPlex := ResolveKindPresentations(KindPresentationInput{
		Levels: []KindPresentationLevel{{Key: "unit", Playable: true}},
		Present: core.Presentations{
			Plex: &core.PlexPresentation{Base: "photo"},
		},
	}, core.PresentationOverrides{})
	if rFromPlex.MediaClass != core.MediaClassPhoto {
		t.Fatalf("class from plex = %q, want photo", rFromPlex.MediaClass)
	}

	// 3. level keys -> book
	rFromKeys := ResolveKindPresentations(KindPresentationInput{
		Levels: []KindPresentationLevel{{Key: "book", Playable: true}},
	}, core.PresentationOverrides{})
	if rFromKeys.MediaClass != core.MediaClassBook {
		t.Fatalf("class from keys = %q, want book", rFromKeys.MediaClass)
	}
}

func TestResolveKindPresentations_DefaultsOverridesAndApplyAppSwitches(t *testing.T) {
	in := KindPresentationInput{
		Media: string(core.MediaClassVideo),
		Levels: []KindPresentationLevel{
			{Key: "clip", Playable: true},
		},
		Present: core.Presentations{
			// Plugin recommends Stremio OFF by default while still letting owners turn it on.
			Stremio: &core.StremioPresentation{Default: boolPtr(false)},
		},
	}

	// Default resolution: Stremio available & inferred, DefaultOn=false, Enabled=false.
	r := ResolveKindPresentations(in, core.PresentationOverrides{})
	stremio := appByName(t, r, "stremio")
	if !stremio.Available || stremio.DefaultOn || stremio.Enabled || stremio.Source != core.PresentationSourceInferred {
		t.Fatalf("stremio default = %+v", stremio)
	}
	if r.Effective.Stremio != nil || r.Available.Stremio == nil || r.Available.Stremio.Type != "movie" {
		t.Fatalf("stremio eff=%+v avail=%+v", r.Effective.Stremio, r.Available.Stremio)
	}

	// Owner explicitly turns Stremio ON and Plex OFF.
	eff, ov, err := ApplyAppSwitches(r, core.PresentationOverrides{}, AppSwitchPatch{
		Stremio: boolPtr(true),
		Plex:    boolPtr(false),
	})
	if err != nil {
		t.Fatalf("ApplyAppSwitches: %v", err)
	}
	if eff.Stremio == nil || eff.Stremio.Type != "movie" {
		t.Fatalf("stremio not enabled after override: %+v", eff.Stremio)
	}
	if eff.Plex != nil {
		t.Fatalf("plex not disabled after override: %+v", eff.Plex)
	}
	if ov.Stremio == nil || !*ov.Stremio || ov.Plex == nil || *ov.Plex {
		t.Fatalf("overrides = %+v", ov)
	}

	// Re-resolving with those sticky overrides preserves owner choices even when
	// the plugin updates its HOW mapping (e.g. switches clip -> show).
	updatedIn := KindPresentationInput{
		Media: string(core.MediaClassVideo),
		Levels: []KindPresentationLevel{
			{Key: "show", Container: true},
			{Key: "episode", Playable: true},
		},
		Present: core.Presentations{
			Stremio: &core.StremioPresentation{Default: boolPtr(false)},
		},
	}
	rUpdated := ResolveKindPresentations(updatedIn, ov)
	if rUpdated.Effective.Stremio == nil || rUpdated.Effective.Stremio.Type != "series" {
		t.Fatalf("updated stremio should stay ON with refreshed HOW (series): %+v", rUpdated.Effective.Stremio)
	}
	if rUpdated.Effective.Plex != nil {
		t.Fatalf("updated plex should stay OFF: %+v", rUpdated.Effective.Plex)
	}

	// AdapterOnly disables all apps while keeping per-app overrides intact.
	effAO, ovAO, err := ApplyAppSwitches(rUpdated, ov, AppSwitchPatch{AdapterOnly: boolPtr(true)})
	if err != nil {
		t.Fatalf("adapterOnly on: %v", err)
	}
	if !effAO.AdapterOnly || effAO.Vela != nil || effAO.Stremio != nil || effAO.MediaBrowser != nil {
		t.Fatalf("adapterOnly effective = %+v", effAO)
	}
	if ovAO.AdapterOnly == nil || !*ovAO.AdapterOnly || ovAO.Plex == nil || *ovAO.Plex {
		t.Fatalf("adapterOnly should preserve existing per-app overrides: %+v", ovAO)
	}

	// Attempting to turn on an unavailable app (Plex on a book library) fails
	// with ErrInvalid and the plain-language explanation.
	booksResolved := ResolveKindPresentations(KindPresentationInput{
		Media:  string(core.MediaClassBook),
		Levels: []KindPresentationLevel{{Key: "book", Playable: true}},
	}, core.PresentationOverrides{})
	_, _, err = ApplyAppSwitches(booksResolved, core.PresentationOverrides{}, AppSwitchPatch{Plex: boolPtr(true)})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), reasonPlexBook) {
		t.Fatalf("enabling Plex on book library = %v, want ErrInvalid with %q", err, reasonPlexBook)
	}
}
