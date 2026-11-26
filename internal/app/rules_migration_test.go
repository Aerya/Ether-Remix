package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestRulesMigrationKeepsEveryChoice verifies that upgrading a rich v0.3.20
// config + SQLite DB (twin profiles with one differing TV twin, legacy client
// rules including Android height lift and surround audio conversion, Away
// defaults above library profiles, filter/collection/built-in library profiles,
// differing movie/TV defaults, remoteMaxBitrateKbps, and viewer maxBitrateKbps)
// produces ordered rules that preserve every playback decision, clears legacy
// state, and is idempotent on restart.
func TestRulesMigrationKeepsEveryChoice(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Seed SQLite with a viewer safety bitrate cap and profiled libraries
	// BEFORE opening the App so New()'s startup migration converts them.
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := identity.NewLocal(db)
	if err != nil {
		t.Fatal(err)
	}
	cappedUID, err := localID.CreateViewer(ctx, "bob", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE auth_users SET title = ? WHERE id = ?", "Bob's display name", cappedUID); err != nil {
		t.Fatal(err)
	}
	safetySvc, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := safetySvc.Put(ctx, cappedUID, safety.Policy{MaxBitrateKbps: 4000}); err != nil {
		t.Fatal(err)
	}

	libSvc, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Built-in Movies library (ID 1) assigned "4K", plus a filter library
	// ("Anime Shows") with "Anime" and a mixed collection ("Favorites") with "4K".
	if err := libSvc.SetQualityProfile(ctx, core.LibraryMovies, "4K"); err != nil {
		t.Fatal(err)
	}
	if err := libSvc.SetRules(ctx, core.LibraryMovies, []core.LibraryExclusion{{
		Action: "hide", MediaType: "movie", Match: "all", Scope: core.VisibilityLibrary,
		Rules: []core.CollectionRule{{Field: "title", Op: "is", Value: json.RawMessage(`"` + core.MovieKey(701).String() + `"`)}},
	}}, "show"); err != nil {
		t.Fatal(err)
	}
	animeLib, err := libSvc.Create(ctx, "Anime Shows", core.TypeShow, libraryRules(core.Filter{Type: core.TypeShow, Genres: []string{"Animation"}}), "hide", core.Presentations{})
	if err != nil {
		t.Fatal(err)
	}
	if err := libSvc.SetQualityProfile(ctx, animeLib.ID, "Anime"); err != nil {
		t.Fatal(err)
	}
	favColl, err := libSvc.CreateCollection(ctx, "Favorites", 501, core.TypeMovie, core.Presentations{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE libraries SET media_type = '' WHERE id = ?`, favColl.ID); err != nil {
		t.Fatal(err)
	}
	if err := libSvc.SetQualityProfile(ctx, favColl.ID, "4K"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Write a raw v0.3.20 config.json with per-media profiles (mediaType: movie/tv),
	// defaultMovie/defaultTv/remoteMovie/remoteTv, legacy client rules, and
	// transcode.remoteMaxBitrateKbps.
	// - "1080p" is identical across movie & TV -> merged into one "1080p".
	// - "4K" differs between movie (2160p HDR) & TV (2160p SDR) -> TV renamed "4K (TV)".
	// - "Anime" and "Away" are identical twins -> merged cleanly.
	rawCfg := `{
		"setupComplete": true,
		"transcode": {
			"remoteMaxBitrateKbps": 8000
		},
		"quality": {
			"default": "1080p",
			"remote": "Away",
			"defaultMovie": "1080p",
			"remoteMovie": "Away",
			"defaultTv": "4K",
			"remoteTv": "Away",
			"profiles": [
				{"name": "1080p", "mediaType": "movie", "maxHeight": 1080},
				{"name": "1080p", "mediaType": "tv", "maxHeight": 1080},
				{"name": "4K", "mediaType": "movie", "maxHeight": 2160, "allowHDR": true},
				{"name": "4K", "mediaType": "tv", "maxHeight": 2160, "allowHDR": false},
				{"name": "Anime", "mediaType": "tv", "maxHeight": 1080, "minHeight": 720},
				{"name": "Away", "mediaType": "movie", "maxHeight": 720},
				{"name": "Away", "mediaType": "tv", "maxHeight": 720}
			],
			"rules": [
				{"protocol": "jellyfin", "product": "Infuse", "mediaType": "movie", "profile": "4K", "convertSurroundAudio": true},
				{"product": "Plex for Android (Mobile)", "mediaType": "movie", "profile": "4K", "maxHeight": 2160},
				{"product": "Apple TV", "mediaType": "movie", "profile": "4K"},
				{"profile": "4K"},
				{"product": "Ghost", "profile": "DoesNotExist"}
			]
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(rawCfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// Boot App on dir; New() runs config.Open + migrateQualityRules automatically.
	a, err := New(ctx, Options{DataDir: dir, Version: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	// Seed titles matching our libraries for playback verification.
	plainMovie := core.Title{Key: core.MovieKey(701), Type: core.TypeMovie, Title: "Plain Movie", Year: 2022, Released: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)}
	favMovie := core.Title{Key: core.MovieKey(702), Type: core.TypeMovie, Title: "Fav Movie", Year: 2022, Released: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)}
	plainShow := core.Title{Key: core.ShowKey(801), Type: core.TypeShow, Title: "Plain Show", Year: 2022, Released: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)}
	favShow := core.Title{Key: core.ShowKey(802), Type: core.TypeShow, Title: "Fav Show", Year: 2022, Released: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)}
	animeShow := core.Title{Key: core.ShowKey(803), Type: core.TypeShow, Title: "Anime Show", Year: 2022, Released: time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC), Genres: []string{"Animation"}}
	for _, title := range []core.Title{plainMovie, favMovie, plainShow, favShow, animeShow} {
		if err := a.catalog.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.db.ExecContext(ctx, `INSERT INTO collections_members(spec_id,pos,key,shared) VALUES(501,0,?,1),(501,1,?,1)`, favMovie.Key, favShow.Key); err != nil {
		t.Fatal(err)
	}

	gotCfg := a.cfg.Get()
	q := gotCfg.Quality
	if q.MigratedRules != 1 || !q.RulesMigrationNote || q.RulesMigration != nil {
		t.Fatalf("migration markers = migrated:%d note:%v pending:%v", q.MigratedRules, q.RulesMigrationNote, q.RulesMigration)
	}
	if q.MigrationNote == nil || q.MigrationNote.At.IsZero() || q.MigrationNote.Dismissed {
		t.Fatalf("expected an undismissed, timestamped migration note, got %+v", q.MigrationNote)
	}
	if gotCfg.Transcode.RemoteMaxBitrateKbps != 0 {
		t.Fatalf("RemoteMaxBitrateKbps not zeroed: %d", gotCfg.Transcode.RemoteMaxBitrateKbps)
	}
	wantOrigins := []config.QualityRuleOrigin{
		{Kind: "awayLimit"},
		{Kind: "viewerLimit", Name: "Bob's display name"},
		{Kind: "rule", Name: "Infuse / jellyfin"},
		{Kind: "rule", Name: "Plex for Android (Mobile)"},
		{Kind: "rule", Name: "Apple TV"},
		// The shared away profile is Default away; the rules below it apply
		// at home, as 0.3.20 ranked Away above library profiles.
		{Kind: "library", Name: "Anime Shows", Reason: config.OriginAwayDefaultWins},
		{Kind: "library", Name: "Favorites", Reason: config.OriginAwayDefaultWins},
		{Kind: "library", Name: "Favorites", Reason: config.OriginAwayDefaultWins},
		{Kind: "library", Name: "Movies", Reason: config.OriginAwayDefaultWins},
		{Kind: "library", Name: "Movies", Reason: config.OriginAwayDefaultWins}, // hidden built-in fallback
		{Kind: "tvDefault", Reason: config.OriginAwayDefaultWins},
	}
	if q.DefaultAway != "Away" {
		t.Fatalf("DefaultAway = %q, want the shared away profile", q.DefaultAway)
	}
	if len(q.Rules) != len(wantOrigins) {
		t.Fatalf("got %d rules, want %d: %+v", len(q.Rules), len(wantOrigins), q.Rules)
	}
	for i, rule := range q.Rules {
		if rule.From == nil || *rule.From != wantOrigins[i] {
			t.Fatalf("rule %d provenance = %+v, want %+v", i+1, rule.From, wantOrigins[i])
		}
	}
	if r := q.Rules[9]; r.Kind != config.KindMovie || len(r.Libraries) != 0 {
		t.Fatalf("expected hidden built-in fallback, got %+v", r)
	}
	// Every rule must have a unique non-empty ID.
	seenIDs := map[string]bool{}
	for _, r := range q.Rules {
		if r.ID == "" || seenIDs[r.ID] {
			t.Fatalf("invalid/duplicate rule ID %q in %+v", r.ID, q.Rules)
		}
		seenIDs[r.ID] = true
	}
	// Legacy libraries.quality_profile and safety maxBitrateKbps must be cleared.
	postLibs, err := a.libraries.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range postLibs {
		if l.QualityProfile != "" {
			t.Fatalf("library %d (%s) still has quality_profile=%q", l.ID, l.Name, l.QualityProfile)
		}
	}
	if pol := a.safety.ForUser(&core.User{ID: cappedUID}); pol.MaxBitrateKbps != 0 {
		t.Fatalf("viewer %d safety maxBitrateKbps not cleared: %d", cappedUID, pol.MaxBitrateKbps)
	}
	// Differing TV twin "4K" must have been renamed "4K (TV)", while identical "1080p" merged.
	if _, ok := core.FindProfile(q.Profiles, "4K (TV)"); !ok {
		t.Fatalf("expected renamed TV twin profile '4K (TV)' in %+v", q.Profiles)
	}
	if _, ok := core.FindProfile(q.Profiles, "1080p (TV)"); ok {
		t.Fatalf("identical 1080p twin should have merged, not renamed: %+v", q.Profiles)
	}
	// Android mobile maxHeight=2160 lift stays on its rule (handled by N4
	// android-mobile-ceiling lift with HDR) rather than becoming a device-wide
	// Clients.Overrides entry.
	if len(gotCfg.Clients.Overrides) != 0 {
		t.Fatalf("expected Android mobile lift to stay on its rule, not Clients.Overrides: %+v", gotCfg.Clients.Overrides)
	}

	// Verify exact playback choices across representative plays!
	web := core.ClientCaps{Protocol: "plex", Product: "Plex Web", MaxHeight: 2160, HDR10: true, AudioCodecs: []string{"aac", "eac3"}}
	webAway := web
	webAway.Remote = true
	infuse := core.ClientCaps{Protocol: "jellyfin", Product: "Infuse-Direct", Platform: "tvOS", MaxHeight: 2160, HDR10: true, AudioCodecs: []string{"aac", "eac3", "truehd"}}
	appleTV := core.ClientCaps{Protocol: "plex", Product: "Plex for Apple TV", Platform: "tvOS", MaxHeight: 2160, HDR10: true}

	plainEp := core.Title{Key: core.EpisodeKey(801, 1, 1), Type: core.TypeEpisode, ShowKey: plainShow.Key}
	favEp := core.Title{Key: core.EpisodeKey(802, 1, 1), Type: core.TypeEpisode, ShowKey: favShow.Key}
	animeEp := core.Title{Key: core.EpisodeKey(803, 1, 1), Type: core.TypeEpisode, ShowKey: animeShow.Key}

	check := func(name string, caps core.ClientCaps, u *core.User, title *core.Title, wantProfile string, wantBitrate int, wantStereo bool) {
		t.Helper()
		ans := resolver.ExplainQuality(resolver.QualityQuestion{
			Quality:   a.cfg.Get().Quality,
			Caps:      caps,
			User:      u,
			Title:     title,
			InLibrary: a.membershipFor(ctx, title),
		})
		if ans.Profile != wantProfile || ans.BitrateKbps != wantBitrate || ans.Stereo != wantStereo {
			t.Fatalf("%s: got profile=%q bitrate=%d stereo=%v, want profile=%q bitrate=%d stereo=%v (layers=%+v)",
				name, ans.Profile, ans.BitrateKbps, ans.Stereo, wantProfile, wantBitrate, wantStereo, ans.Layers)
		}
	}

	// 1. The library-scoped hide still leaves the plain movie on the built-in Movies profile.
	check("plain movie home web", web, nil, &plainMovie, "4K", 0, false)
	// 2. Plain episode at home on Web -> TV default "4K (TV)".
	check("plain episode home web", web, nil, &plainEp, "4K (TV)", 0, false)
	// 3. Anime episode at home on Web -> filter library profile "Anime".
	check("anime episode home web", web, nil, &animeEp, "Anime", 0, false)
	// 4. Mixed Favorites collection: movie gets "4K", episode gets "4K (TV)".
	check("fav collection movie", web, nil, &favMovie, "4K", 0, false)
	check("fav collection episode", web, nil, &favEp, "4K (TV)", 0, false)
	// 5. Away beats library profile (library rules apply at home; Default away) + 8000 kbps cap.
	check("anime episode away web", webAway, nil, &animeEp, "Away", 8000, false)
	// 6. Capped viewer gets strictest 4000 kbps at home AND away.
	check("capped viewer home", web, &core.User{ID: cappedUID}, &plainMovie, "4K", 4000, false)
	check("capped viewer away", webAway, &core.User{ID: cappedUID}, &plainMovie, "Away", 4000, false)
	// 7. Infuse legacy movie rule -> "4K" + StereoAudio=true on movies.
	check("infuse movie stereo", infuse, nil, &plainMovie, "4K", 0, true)
	// 8. Apple TV away -> legacy Apple TV client rule still beats Away profile ("4K") while keeping 8000 kbps away cap.
	appleTVAway := appleTV
	appleTVAway.Remote = true
	check("apple tv away", appleTVAway, nil, &plainMovie, "4K", 8000, false)

	// Idempotency: re-running migrateQualityRules must be a complete no-op.
	beforeRules := slices.Clone(a.cfg.Get().Quality.Rules)
	if err := a.migrateQualityRules(ctx); err != nil {
		t.Fatalf("second migrateQualityRules: %v", err)
	}
	afterQuality := a.cfg.Get().Quality
	if afterQuality.MigrationNote == nil || *afterQuality.MigrationNote != *q.MigrationNote {
		t.Fatalf("migration note changed on second run: %+v", afterQuality.MigrationNote)
	}
	afterRules := afterQuality.Rules
	if len(afterRules) != len(beforeRules) {
		t.Fatalf("idempotency broken: before=%d after=%d", len(beforeRules), len(afterRules))
	}
	for i := range beforeRules {
		if afterRules[i].ID != beforeRules[i].ID {
			t.Fatalf("rule %d ID changed on second run: %q vs %q", i, beforeRules[i].ID, afterRules[i].ID)
		}
	}
}

func TestRulesMigrationFreshInstallSetsNoBanner(t *testing.T) {
	a := setupTestApp(t, true)
	q := a.cfg.Get().Quality
	if q.MigratedRules != 1 {
		t.Fatalf("fresh install MigratedRules = %d, want 1", q.MigratedRules)
	}
	if q.RulesMigrationNote || q.MigrationNote != nil {
		t.Fatalf("fresh install should not set a migration note")
	}
	if len(q.Rules) != 0 {
		t.Fatalf("fresh install should have 0 rules, got %+v", q.Rules)
	}
}

func TestRulesMigrationUsesDefaultAwayWhenNoProfiledLibraries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rawCfg := `{
		"setupComplete": true,
		"quality": {
			"default": "4K",
			"remote": "1080p",
			"defaultMovie": "4K",
			"remoteMovie": "1080p",
			"defaultTv": "4K",
			"remoteTv": "1080p",
			"profiles": [
				{"name": "4K", "mediaType": "movie", "maxHeight": 2160},
				{"name": "4K", "mediaType": "tv", "maxHeight": 2160},
				{"name": "1080p", "mediaType": "movie", "maxHeight": 1080},
				{"name": "1080p", "mediaType": "tv", "maxHeight": 1080}
			]
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(rawCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, Options{DataDir: dir, Version: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })

	q := a.cfg.Get().Quality
	if q.Default != "4K" || q.DefaultAway != "1080p" || len(q.Rules) != 0 {
		t.Fatalf("expected clean DefaultAway=1080p with 0 rules, got default=%q defaultAway=%q rules=%+v", q.Default, q.DefaultAway, q.Rules)
	}
}
