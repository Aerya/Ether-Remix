package resolver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestRulesEngineEvaluationOrderAndLimitAccumulation(t *testing.T) {
	q := config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "4K", MaxHeight: 2160, AllowHDR: true, AllowDolbyVision: true},
			{Name: "1080p", MaxHeight: 1080},
			{Name: "720p", MaxHeight: 720},
		},
		Default:     "1080p",
		DefaultAway: "720p",
		Rules: []config.QualityRule{
			// Limit-only rule at top: caps away streams to 8 Mbps, does not choose profile.
			{ID: "r_away_cap", Where: config.WhereAway, MaxBitrateKbps: 8000},
			// First profile rule for Apple TV: picks 4K, also sets 1080p cap.
			{ID: "r_atv_4k", Devices: []config.DeviceMatch{{Type: "Apple TV"}}, Profile: "4K", MaxHeight: 1080},
			// Lower profile rule for viewer 7: profile ignored (already picked),
			// but its stricter bitrate (4500) and stereo audio STILL accumulate.
			{ID: "r_v7_limits", Viewers: []int64{7}, Profile: "720p", MaxBitrateKbps: 4500, StereoAudio: true},
			// Lower rule with looser bitrate (12000) and stricter height (720):
			// height drops to 720, bitrate stays 4500.
			{ID: "r_away_720", Where: config.WhereAway, MaxHeight: 720, MaxBitrateKbps: 12000},
		},
	}

	caps := appleTV(true)
	caps.AudioCodecs = []string{"aac", "ac3", "eac3", "truehd"}
	d := decideProfile(q, caps, &core.User{ID: 7}, playTarget{media: core.MediaMovie, titled: true})
	if d.profile.Name != "4K" {
		t.Fatalf("profile = %q, want 4K (first profile rule wins)", d.profile.Name)
	}
	if d.profile.MaxHeight != 720 {
		t.Fatalf("maxHeight = %d, want 720 (strictest across matching rules)", d.profile.MaxHeight)
	}
	if d.caps.MaxBitrateKbps != 4500 {
		t.Fatalf("maxBitrateKbps = %d, want 4500 (strictest across matching rules)", d.caps.MaxBitrateKbps)
	}
	if !d.effect.limits.stereo || d.caps.SupportsAudio("eac3") || !d.caps.SupportsAudio("aac") {
		t.Fatalf("stereo limit not applied: limits=%+v audio=%v", d.effect.limits, d.caps.AudioCodecs)
	}
	wantLimitRules := []int{0, 1, 2, 3}
	if !slices.Equal(d.effect.limitRules, wantLimitRules) {
		t.Fatalf("limitRules = %v, want %v", d.effect.limitRules, wantLimitRules)
	}
}

func TestRulesEngineNegationAndDefaults(t *testing.T) {
	q := config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "4K", MaxHeight: 2160, AllowHDR: true},
			{Name: "1080p", MaxHeight: 1080},
			{Name: "720p", MaxHeight: 720},
		},
		Default:     "1080p",
		DefaultAway: "720p",
		Rules: []config.QualityRule{
			// Everyone EXCEPT viewer 1 gets 720p when watching movies NOT in library 5.
			{
				ID:              "r_except",
				Viewers:         []int64{1},
				ExceptViewers:   true,
				Libraries:       []int64{5},
				ExceptLibraries: true,
				Kind:            config.KindMovie,
				Profile:         "720p",
			},
			// Non-mobile devices at home get 4K for episodes.
			{
				ID:            "r_non_mobile_tv",
				Devices:       []config.DeviceMatch{{Group: "mobile"}},
				ExceptDevices: true,
				Where:         config.WhereHome,
				Kind:          config.KindEpisode,
				Profile:       "4K",
			},
		},
	}

	inLib5 := func(id core.LibraryID) bool { return id == 5 }
	notInLib5 := func(core.LibraryID) bool { return false }

	// Viewer 2, movie not in lib 5 -> matches r_except -> 720p.
	d := decideProfile(q, appleTV(false), &core.User{ID: 2}, playTarget{media: core.MediaMovie, titled: true, inLib: notInLib5})
	if d.profile.Name != "720p" {
		t.Fatalf("viewer 2 outside lib 5: got %q, want 720p", d.profile.Name)
	}
	// Viewer 2, movie IN lib 5 -> r_except excluded -> Default 1080p.
	d = decideProfile(q, appleTV(false), &core.User{ID: 2}, playTarget{media: core.MediaMovie, titled: true, inLib: inLib5})
	if d.profile.Name != "1080p" {
		t.Fatalf("viewer 2 inside lib 5: got %q, want 1080p", d.profile.Name)
	}
	// Viewer 1 (excluded from r_except), away -> DefaultAway 720p.
	d = decideProfile(q, appleTV(true), &core.User{ID: 1}, playTarget{media: core.MediaMovie, titled: true, inLib: notInLib5})
	if d.profile.Name != "720p" || d.effect.choice.source != ProfileFromDefaultAway {
		t.Fatalf("viewer 1 away: got %q (%v), want 720p (DefaultAway)", d.profile.Name, d.effect.choice.source)
	}
	// Episode at home on TV -> matches r_non_mobile_tv -> 4K.
	d = decideProfile(q, appleTV(false), &core.User{ID: 1}, playTarget{media: core.MediaTV, titled: true})
	if d.profile.Name != "4K" {
		t.Fatalf("episode on Apple TV at home: got %q, want 4K", d.profile.Name)
	}
	// Episode at home on Android phone (mobile) -> excluded -> Default 1080p.
	d = decideProfile(q, androidPhone(false), &core.User{ID: 1}, playTarget{media: core.MediaTV, titled: true})
	if d.profile.Name != "1080p" {
		t.Fatalf("episode on mobile at home: got %q, want 1080p", d.profile.Name)
	}
}

func TestAndroidLiftRequiresDeviceCondition(t *testing.T) {
	base := config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "4K", MaxHeight: 2160, AllowHDR: true},
			{Name: "1080p", MaxHeight: 1080},
		},
		Default: "1080p",
	}

	// Viewer-only rule picking 4K must NOT lift Android's 1080p ceiling.
	qViewer := base
	qViewer.Rules = []config.QualityRule{{ID: "r_v7", Viewers: []int64{7}, Profile: "4K"}}
	dViewer := decideProfile(qViewer, androidPhone(false), &core.User{ID: 7}, playTarget{media: core.MediaMovie, titled: true})
	if dViewer.profile.Name != "4K" || dViewer.profile.MaxHeight != 1080 || dViewer.effect.lifted != 0 {
		t.Fatalf("viewer rule should not lift Android ceiling: profile=%+v lifted=%d", dViewer.profile, dViewer.effect.lifted)
	}

	// Device-conditioned rule picking 4K lifts Android's 1080p ceiling to 2160p + HDR.
	qDevice := base
	qDevice.Rules = []config.QualityRule{{ID: "r_and", Devices: []config.DeviceMatch{{Group: "mobile"}}, Profile: "4K"}}
	dDevice := decideProfile(qDevice, androidPhone(false), &core.User{ID: 7}, playTarget{media: core.MediaMovie, titled: true})
	if dDevice.profile.Name != "4K" || dDevice.profile.MaxHeight != 2160 || !dDevice.profile.AllowHDR || dDevice.effect.lifted != 2160 {
		t.Fatalf("device rule should lift Android ceiling: profile=%+v lifted=%d", dDevice.profile, dDevice.effect.lifted)
	}
}

func TestApplyClientRulesForMediaPreTitleLimits(t *testing.T) {
	q := config.Quality{
		Profiles: []core.QualityProfile{{Name: "4K", MaxHeight: 2160}},
		Default:  "4K",
		Rules: []config.QualityRule{
			// Library-scoped rule should NOT apply when no title/library is known.
			{ID: "r_lib", Libraries: []int64{2}, MaxBitrateKbps: 1500},
			// Away bitrate cap applies to Live TV and plugin plays.
			{ID: "r_away", Where: config.WhereAway, MaxBitrateKbps: 6000, MaxHeight: 1080},
			// Viewer stereo limit applies regardless of title.
			{ID: "r_v9", Viewers: []int64{9}, StereoAudio: true},
		},
	}
	caps := appleTV(true)
	caps.AudioCodecs = []string{"aac", "eac3", "truehd"}

	got := ApplyClientRulesForMedia(q, caps, &core.User{ID: 9}, core.MediaAny)
	if got.MaxBitrateKbps != 6000 {
		t.Fatalf("MaxBitrateKbps = %d, want 6000 (skipping library rule)", got.MaxBitrateKbps)
	}
	if got.MaxHeight != 1080 {
		t.Fatalf("MaxHeight = %d, want 1080", got.MaxHeight)
	}
	if got.SupportsAudio("eac3") || !got.SupportsAudio("aac") {
		t.Fatalf("stereo limit not applied: %v", got.AudioCodecs)
	}
}

func TestSummarizeQualityUnfilteredFilteredMixedAndPlugin(t *testing.T) {
	q := config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "4K", MaxHeight: 2160},
			{Name: "Anime", MaxHeight: 1080},
			{Name: "TV", MaxHeight: 1080},
			{Name: "Away", MaxHeight: 720},
		},
		Default:     "4K",
		DefaultAway: "Away",
		Rules: []config.QualityRule{
			// Unfiltered exception for viewer 3 on Movies.
			{ID: "r_v3", Viewers: []int64{3}, Libraries: []int64{1}, Profile: "Away", MaxBitrateKbps: 4000},
			// Library rule for Anime (filter library, mixed movies+episodes).
			{ID: "r_anime", Libraries: []int64{3}, Profile: "Anime"},
			// Episode-only rule causes mixed library to split by kind.
			{ID: "r_tv", Kind: config.KindEpisode, Profile: "TV"},
			// Away bitrate cap applies everywhere away, including plugin libraries.
			{ID: "r_away_cap", Where: config.WhereAway, MaxBitrateKbps: 8000},
		},
	}
	libs := []SummaryLibrary{
		{ID: 1, Name: "Movies", MediaType: core.TypeMovie},
		{ID: 2, Name: "TV Shows", MediaType: core.TypeShow},
		{ID: 3, Name: "Anime", MediaType: ""}, // mixed filter library
		{ID: 4, Name: "YouTube", Plugin: "youtube", ChosenByPlugin: true},
	}

	// Unfiltered summary: viewer-scoped rule shows up as exception on Movies.
	unfiltered := SummarizeQuality(q, libs, 0, SummaryDevice{})
	if len(unfiltered.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(unfiltered.Rows))
	}
	movies := unfiltered.Rows[0]
	if len(movies.Cells) != 1 || movies.Cells[0].Home.Profile != "4K" || movies.Cells[0].Away.Profile != "Away" {
		t.Fatalf("movies cells = %+v", movies.Cells)
	}
	if movies.Cells[0].Away.MaxBitrateKbps != 8000 {
		t.Fatalf("movies away bitrate = %d, want 8000", movies.Cells[0].Away.MaxBitrateKbps)
	}
	if len(movies.Exceptions) != 1 || movies.Exceptions[0].RuleID != "r_v3" {
		t.Fatalf("movies exceptions = %+v, want [r_v3]", movies.Exceptions)
	}

	tv := unfiltered.Rows[1]
	if len(tv.Cells) != 1 || tv.Cells[0].Home.Profile != "TV" || tv.Cells[0].Home.ProfileRuleID != "r_tv" {
		t.Fatalf("tv cells = %+v", tv.Cells)
	}

	// Plugin library: no profile, but away bitrate limit still applies.
	plugin := unfiltered.Rows[3]
	if !plugin.ChosenByPlugin || len(plugin.Cells) != 1 || plugin.Cells[0].Home.Profile != "" || plugin.Cells[0].Away.MaxBitrateKbps != 8000 {
		t.Fatalf("plugin row = %+v", plugin)
	}

	// Filtered by viewer 3: exception is now evaluated directly in Movies cells.
	filtered := SummarizeQuality(q, libs, 3, SummaryDevice{})
	moviesV3 := filtered.Rows[0]
	if len(moviesV3.Exceptions) != 0 {
		t.Fatalf("filtered movies should have no exceptions: %+v", moviesV3.Exceptions)
	}
	if moviesV3.Cells[0].Home.Profile != "Away" || moviesV3.Cells[0].Home.MaxBitrateKbps != 4000 {
		t.Fatalf("filtered movies home = %+v, want Away @ 4000", moviesV3.Cells[0].Home)
	}

	// Mixed library without library-level profile splits when an episode rule applies.
	qSplit := q
	qSplit.Rules = []config.QualityRule{{ID: "r_tv", Kind: config.KindEpisode, Profile: "TV"}}
	splitSum := SummarizeQuality(qSplit, []SummaryLibrary{{ID: 3, Name: "Mixed"}}, 0, SummaryDevice{})
	if len(splitSum.Rows[0].Cells) != 2 || splitSum.Rows[0].Cells[0].Kind != config.KindMovie || splitSum.Rows[0].Cells[1].Kind != config.KindEpisode {
		t.Fatalf("mixed library split = %+v, want movie + episode cells", splitSum.Rows[0].Cells)
	}
	if splitSum.Rows[0].Cells[0].Home.Profile != "4K" || splitSum.Rows[0].Cells[1].Home.Profile != "TV" {
		t.Fatalf("mixed library profiles = %+v", splitSum.Rows[0].Cells)
	}

	// Partial rule: library rule targeting Anime doesn't know if every movie is in Anime -> partialRuleIds on Movies.
	qPartial := config.Quality{
		Profiles: []core.QualityProfile{{Name: "4K", MaxHeight: 2160}, {Name: "Anime", MaxHeight: 1080}},
		Default:  "4K",
		Rules:    []config.QualityRule{{ID: "r_anime", Libraries: []int64{3}, Profile: "Anime"}},
	}
	partialSum := SummarizeQuality(qPartial, libs[:3], 0, SummaryDevice{})
	if !slices.Contains(partialSum.Rows[0].Cells[0].Home.PartialRuleIDs, "r_anime") {
		t.Fatalf("expected r_anime in Movies partialRuleIds: %+v", partialSum.Rows[0].Cells[0].Home)
	}
	if len(partialSum.Rows[2].Cells[0].Home.PartialRuleIDs) != 0 || partialSum.Rows[2].Cells[0].Home.Profile != "Anime" {
		t.Fatalf("expected Anime row to apply r_anime directly: %+v", partialSum.Rows[2].Cells[0].Home)
	}
}

func TestConsolePresetsDatabaseCheckAndNoSync(t *testing.T) {
	f := newImportFixture(t, true)

	// GET /presets returns Ether's shipped profiles without import metadata.
	var presets struct {
		Items []core.QualityProfile `json:"items"`
	}
	if code := f.do(t, "GET", "/presets", "", &presets); code != 200 || len(presets.Items) == 0 {
		t.Fatalf("GET /presets = %d %+v", code, presets)
	}
	for _, p := range presets.Items {
		if p.Import != nil {
			t.Fatalf("preset %q must not carry import metadata: %+v", p.Name, p.Import)
		}
	}

	// Unknown ?database= returns 404.
	var e apiError
	if code := f.do(t, "GET", "/dictionarry/radarr/profiles?database=missing", "", &e); code != 404 {
		t.Fatalf("unknown database = %d %+v, want 404", code, e)
	}

	// POST /quality-databases/check rejects invalid repo strings with ok:false.
	var badCheck struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if code := f.do(t, "POST", "/quality-databases/check", `{"repo":"not a repo"}`, &badCheck); code != 200 || badCheck.OK || badCheck.Error == "" {
		t.Fatalf("bad repo check = %d %+v", code, badCheck)
	}

	// Wire a custom HTTP transport rewriting github archive URLs to the local test tar.gz server.
	archiveBytes := buildDictionarryTarGz(t, dictionarryTestFiles())
	archiveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(archiveBytes)
	}))
	t.Cleanup(archiveSrv.Close)

	rewriteClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			cloned := req.Clone(context.Background())
			cloned.URL.Scheme = "http"
			cloned.URL.Host = archiveSrv.Listener.Addr().String()
			return http.DefaultTransport.RoundTrip(cloned)
		}),
	}
	norm, err := config.NormalizeProfileDatabase("https://github.com/owner/custom-db", "main")
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.Quality.Databases = []config.ProfileDatabase{{ID: norm.ID, Name: norm.Name, Repo: norm.Repo, Branch: norm.Branch}}
	f.svc.UpdateConfig(f.cfg)

	customDeps := ConsoleDeps{
		Dictionarry: NewDictionarry(DictionarryOptions{
			ArchiveURL: archiveSrv.URL + "/database-v2.tar.gz",
			Client:     rewriteClient,
			UserAgent:  "Ether/test",
		}),
		SaveConfig: func(mutate func(*config.Config) error) error {
			f.cfgMu.Lock()
			defer f.cfgMu.Unlock()
			next := f.cfg
			next.Quality.Profiles = append([]core.QualityProfile(nil), f.cfg.Quality.Profiles...)
			next.Resolver.CustomFormats = append([]core.CustomFormat(nil), f.cfg.Resolver.CustomFormats...)
			if err := mutate(&next); err != nil {
				return err
			}
			f.cfg = next
			f.saves++
			f.svc.UpdateConfig(next)
			return nil
		},
	}
	customSrv := httptest.NewServer(f.svc.ConsoleHandler(customDeps))
	t.Cleanup(customSrv.Close)
	f.srv = customSrv

	// POST /quality-databases/check succeeds against the rewritten archive URL.
	var okCheck struct {
		OK       bool   `json:"ok"`
		ID       string `json:"id"`
		Repo     string `json:"repo"`
		Branch   string `json:"branch"`
		Profiles int    `json:"profiles"`
	}
	if code := f.do(t, "POST", "/quality-databases/check", `{"repo":"https://github.com/owner/custom-db","branch":"main"}`, &okCheck); code != 200 || !okCheck.OK || okCheck.ID != norm.ID || okCheck.Profiles <= 0 {
		t.Fatalf("ok check = %d %+v", code, okCheck)
	}

	// GET /dictionarry/radarr/profiles?database=<id> serves profiles from the configured database.
	var dbProfiles struct {
		Items []TrashProfile `json:"items"`
	}
	if code := f.do(t, "GET", "/dictionarry/radarr/profiles?database="+norm.ID, "", &dbProfiles); code != 200 || len(dbProfiles.Items) == 0 {
		t.Fatalf("custom database profiles = %d %+v", code, dbProfiles)
	}

	// POST /quality-import with sync disabled preserves the import source.
	var applied struct {
		Name string `json:"name"`
	}
	if code := f.do(t, "POST", "/quality-import", body(importRequest{
		App:      AppRadarr,
		Source:   ImportSourceDictionarry,
		Database: norm.ID,
		Profile:  "2160p-remux",
		Name:     "Custom Copy",
		Sync:     boolp(false),
	}), &applied); code != 200 || applied.Name != "Custom Copy" {
		t.Fatalf("noSync import = %d %+v", code, applied)
	}
	last := f.cfg.Quality.Profiles[len(f.cfg.Quality.Profiles)-1]
	if last.Name != "Custom Copy" || last.Import == nil || !last.Import.NoSync || last.Import.Database != norm.ID {
		t.Fatalf("expected NoSync profile to record Import.NoSync=true & Database=%q: %+v", norm.ID, last.Import)
	}
}

func TestQualityFallbackLimitsMatchPlaybackSummaryAndExplain(t *testing.T) {
	q := config.Quality{
		Profiles:          []core.QualityProfile{{Name: "Home", MaxHeight: 2160}, {Name: "Away", MaxHeight: 2160}},
		Default:           "Home",
		DefaultLimits:     config.QualityLimits{MaxHeight: 720, MaxBitrateKbps: 8000, StereoAudio: true},
		DefaultAwayLimits: config.QualityLimits{MaxHeight: 480, MaxBitrateKbps: 4000},
		Rules: []config.QualityRule{
			{ID: "r_limit", MaxHeight: 1080, MaxBitrateKbps: 6000},
			{ID: "r_profile", Libraries: []int64{2}, Profile: "Home"},
		},
	}
	for _, tt := range []struct {
		name, awayProfile, profile, fallbackID string
		remote                                 bool
		lib                                    int64
		height, bitrate                        int
		stereo                                 bool
	}{
		{"home", "", "Home", "default", false, 1, 720, 6000, true},
		{"away inherits home", "", "Home", "default", true, 1, 720, 6000, true},
		{"away uses its own limits", "Away", "Away", "defaultAway", true, 1, 480, 4000, false},
		{"library rule bypasses fallback limits", "Away", "Home", "", true, 2, 1080, 6000, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := q
			q.DefaultAway = tt.awayProfile
			caps := core.ClientCaps{Remote: tt.remote, MaxHeight: 2160, MaxBitrateKbps: 10000}
			lib := SummaryLibrary{ID: tt.lib}
			inLib := func(id core.LibraryID) bool { return int64(id) == lib.ID }
			where := config.WhereHome
			if tt.remote {
				where = config.WhereAway
			}
			wantIDs := []string{"r_limit"}
			if tt.fallbackID != "" {
				wantIDs = append(wantIDs, tt.fallbackID)
			}
			preTitle := ApplyClientRulesForMedia(q, caps, nil, core.MediaMovie)
			if preTitle.MaxHeight != 1080 || preTitle.MaxBitrateKbps != 6000 || preTitle.ConvertSurroundAudio {
				t.Fatalf("unexpected pre-title limits: %+v", preTitle)
			}
			title := &core.Title{Key: core.MovieKey(51)}
			got := ApplyClientRulesForTitle(q, preTitle, nil, title, inLib)
			if got.MaxHeight != tt.height || got.MaxBitrateKbps != tt.bitrate || got.ConvertSurroundAudio != tt.stereo {
				t.Fatalf("unexpected playback limits: %+v", got)
			}
			cell := evalSummaryCell(q, lib, map[int64]SummaryLibrary{tt.lib: lib}, where, config.KindMovie, false, 0, summaryDeviceMatcher{})
			if cell.Profile != tt.profile || cell.MaxHeight != tt.height || cell.MaxBitrateKbps != tt.bitrate ||
				cell.Stereo != tt.stereo || !slices.Equal(cell.LimitRuleIDs, wantIDs) {
				t.Fatalf("unexpected summary: %+v", cell)
			}
			answer := ExplainQuality(QualityQuestion{Quality: q, Caps: caps, Title: title, InLibrary: inLib})
			if answer.Profile != tt.profile || answer.MaxHeight != tt.height || answer.BitrateKbps != tt.bitrate ||
				answer.Stereo != tt.stereo || !slices.Equal(answer.LimitRuleIDs, wantIDs) {
				t.Fatalf("unexpected explanation: %+v", answer)
			}
			if tt.fallbackID != "" && !slices.ContainsFunc(answer.Layers, func(l QualityLayer) bool {
				return l.Kind == LayerRuleLimit && l.RuleID == tt.fallbackID && l.Decisive
			}) {
				t.Fatalf("missing decisive fallback layer: %+v", answer.Layers)
			}
		})
	}
}

func TestQualityFallbackBitrateSurvivesImplicitRemoteCapRemoval(t *testing.T) {
	q := config.Quality{
		Profiles:      []core.QualityProfile{{Name: "4K", MaxHeight: 2160}},
		Default:       "4K",
		DefaultLimits: config.QualityLimits{MaxBitrateKbps: 12000},
	}
	caps := core.ClientCaps{Remote: true, MaxHeight: 2160, MaxBitrateKbps: 8000, DefaultRemoteBitrate: true}
	answer := ExplainQuality(QualityQuestion{Quality: q, Caps: caps})
	if answer.BitrateKbps != 12000 || !slices.Equal(answer.LimitRuleIDs, []string{"default"}) {
		t.Fatalf("unexpected fallback bitrate: %+v", answer)
	}
	caps.DefaultRemoteBitrate = false
	answer = ExplainQuality(QualityQuestion{Quality: q, Caps: caps})
	if answer.BitrateKbps != 8000 {
		t.Fatalf("fallback lifted an explicit client cap: %+v", answer)
	}
}
