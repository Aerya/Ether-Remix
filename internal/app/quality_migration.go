package app

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/clientreg"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/safety"
)

// migrateQualityRules runs the one-time startup migration from the released
// v0.3.20 quality model (per-media defaults, legacy client rules, library
// quality_profile, transcode.remoteMaxBitrateKbps and safety viewer
// maxBitrateKbps) into the unified ordered rule list. Gated on
// quality.migratedRules < 1 in shared settings so cluster peers never repeat
// it. Delete together with config.migrateMediaProfiles in the release after
// next.
func (a *App) migrateQualityRules(ctx context.Context) error {
	if a == nil || a.cfg == nil {
		return nil
	}
	cfg := a.cfg.Get()
	if cfg.Quality.MigratedRules >= 1 {
		return nil
	}

	var libs []core.Library
	if a.libraries != nil {
		var err error
		libs, err = a.libraries.List(ctx)
		if err != nil {
			return fmt.Errorf("quality migration: list libraries: %w", err)
		}
	}
	var profiledLibs []core.Library
	for _, lib := range libs {
		if strings.TrimSpace(lib.QualityProfile) != "" {
			profiledLibs = append(profiledLibs, lib)
		}
	}

	var viewerPolicies map[int64]safety.Policy
	var cappedViewers []int64
	if a.safety != nil {
		viewerPolicies = a.safety.All()
		for uid, pol := range viewerPolicies {
			if pol.MaxBitrateKbps > 0 {
				cappedViewers = append(cappedViewers, uid)
			}
		}
		slices.Sort(cappedViewers)
	}

	mig := cfg.Quality.RulesMigration
	remoteKbps := cfg.Transcode.RemoteMaxBitrateKbps

	// Fresh install or already-clean config: mark migrated without banner.
	if mig == nil && remoteKbps <= 0 && len(profiledLibs) == 0 && len(cappedViewers) == 0 {
		return a.cfg.Update(func(c *config.Config) error {
			if c.Quality.MigratedRules < 1 {
				c.Quality.MigratedRules = 1
			}
			c.Quality.RulesMigration = nil
			config.AssignRuleIDs(&c.Quality)
			return nil
		})
	}

	profiles := cfg.Quality.Profiles
	validProfile := func(name string) bool {
		if name == "" {
			return false
		}
		_, ok := core.FindProfile(profiles, name)
		return ok
	}
	renameFor := func(media, name string) string {
		if mig != nil && mig.Renames != nil {
			if to, ok := mig.Renames[media][name]; ok {
				return to
			}
		}
		return name
	}

	var rules []config.QualityRule
	var addedOverrides []core.ClientCapabilityOverride
	var notes []string

	// Step 6 (Caps): limit-only rules at the top. Because limits accumulate
	// strictest and limit-only rules never consume the profile match, placing
	// them first keeps evaluation exact and makes global caps visible at a
	// glance.
	if remoteKbps > 0 {
		rules = append(rules, config.QualityRule{
			From:           &config.QualityRuleOrigin{Kind: "awayLimit"},
			Where:          config.WhereAway,
			MaxBitrateKbps: remoteKbps,
		})
		notes = append(notes, fmt.Sprintf("migrated remote bitrate cap (%d kbps) to rule %d", remoteKbps, len(rules)))
	}
	for _, uid := range cappedViewers {
		kbps := viewerPolicies[uid].MaxBitrateKbps
		name := fmt.Sprintf("Viewer %d", uid)
		// This startup migration runs before the auth service is built.
		if a.db != nil {
			if viewer, err := auth.StoredIdentity(ctx, a.db, uid); err == nil {
				name = firstNonEmptyStr(viewer.Title, viewer.Username, name)
			}
		}
		rules = append(rules, config.QualityRule{
			From:           &config.QualityRuleOrigin{Kind: "viewerLimit", Name: name},
			Viewers:        []int64{uid},
			MaxBitrateKbps: kbps,
		})
		notes = append(notes, fmt.Sprintf("migrated viewer %d bitrate cap (%d kbps) to rule %d", uid, kbps, len(rules)))
	}

	// Step 2: 0.3.20 client rules, in order.
	if mig == nil && len(cfg.Quality.Rules) > 0 {
		rules = append(rules, cfg.Quality.Rules...)
	}
	if mig != nil {
		var legacy []config.QualityRule
		for _, lr := range mig.Rules {
			if !legacyRuleHasMatcher(lr) {
				notes = append(notes, "dropped legacy quality rule with no matcher")
				continue
			}
			// In 0.3.20 a rule only took effect if its named profile existed
			// (a rule with no profile never matched, so its limits never
			// applied either).
			if !validProfile(lr.Profile) {
				notes = append(notes, fmt.Sprintf("dropped legacy quality rule naming missing profile %q", lr.Profile))
				continue
			}
			var matches []string
			for _, match := range []string{lr.Product, lr.Platform, lr.Protocol} {
				if match = strings.TrimSpace(match); match != "" {
					matches = append(matches, match)
				}
			}
			rule := config.QualityRule{
				From:        &config.QualityRuleOrigin{Kind: "rule", Name: strings.Join(matches, " / ")},
				Profile:     lr.Profile,
				StereoAudio: lr.ConvertSurroundAudio,
			}
			if lr.UserID > 0 {
				rule.Viewers = []int64{lr.UserID}
			}
			switch lr.MediaType {
			case core.MediaMovie:
				rule.Kind = config.KindMovie
			case core.MediaTV:
				rule.Kind = config.KindEpisode
			}
			rule.Devices = migrateLegacyDeviceMatches(lr)

			if lr.MaxHeight > 0 {
				ceiling := legacyDeviceRegistryCeiling(lr)
				if ceiling > 0 && lr.MaxHeight > ceiling && len(rule.Devices) > 0 && legacyAndroidMobileRule(lr) {
					// An Android phone's 1080p ceiling: the engine lifts it to
					// the profile for a device rule (N4), scoped to this
					// rule's viewer and media and with the HDR enable, exactly
					// as 0.3.20 did. A device-wide override would lift every
					// title and lock the registry height. Keep the rule's
					// height only where it is below that lift.
					prof, _ := core.FindProfile(profiles, lr.Profile)
					lift := prof.MaxHeight
					if lift <= 0 {
						lift = 2160
					}
					if lr.MaxHeight < lift {
						rule.MaxHeight = normalizeMigratedHeight(lr.MaxHeight)
					}
					notes = append(notes, fmt.Sprintf("kept legacy maxHeight=%dp Android phone lift on its rule", lr.MaxHeight))
				} else if ceiling > 0 && lr.MaxHeight > ceiling {
					// 0.3.20 maxHeight lifted above the registry ceiling: move
					// the lift to Playback › Devices overrides because rules
					// now only lower.
					ov := legacyHeightOverride(lr)
					addedOverrides = append(addedOverrides, ov)
					notes = append(notes, fmt.Sprintf("moved legacy maxHeight=%dp lift (above registry %dp) to device overrides", lr.MaxHeight, ceiling))
				} else {
					rule.MaxHeight = normalizeMigratedHeight(lr.MaxHeight)
				}
			}

			legacy = append(legacy, rule)
		}
		// 0.3.20 applied only the first matching rule; limits now add up
		// over every matching rule. Keep each later rule's limits off the
		// plays an earlier rule decided.
		for _, rule := range excludeEarlierLegacyMatches(legacy, &notes) {
			rules = append(rules, rule)
			notes = append(notes, fmt.Sprintf("migrated legacy client rule to rule %d", len(rules)))
		}
	}

	// Step 3: Away (media) defaults.
	awayMovie, awayTV := "", ""
	if mig != nil {
		if v := mig.Away[core.MediaMovie]; validProfile(v) {
			awayMovie = v
		}
		if v := mig.Away[core.MediaTV]; validProfile(v) {
			awayTV = v
		}
	}
	// One away profile for both kinds is the away default ("Everyone else,
	// away"). 0.3.20 ranked it above library profiles, so the library rules
	// below say At home instead of an Away rule outranking them: editing the
	// fallback then changes every away play, as it reads.
	defaultAway := ""
	if awayMovie != "" && awayMovie == awayTV {
		defaultAway = awayMovie
		notes = append(notes, fmt.Sprintf("migrated away default to Default away (%q)", defaultAway))
	} else {
		// The kinds went different ways away from home, which the single
		// fallback cannot say: keep Where: Away (+ What) rules here, above
		// the library rules, as 0.3.20 ranked them.
		if awayMovie != "" {
			rules = append(rules, config.QualityRule{
				From:    &config.QualityRuleOrigin{Kind: "awayProfile", Reason: config.OriginAwayByKind},
				Where:   config.WhereAway,
				Kind:    config.KindMovie,
				Profile: awayMovie,
			})
			notes = append(notes, fmt.Sprintf("migrated movie away default (%q) to rule %d", awayMovie, len(rules)))
		}
		if awayTV != "" {
			rules = append(rules, config.QualityRule{
				From:    &config.QualityRuleOrigin{Kind: "awayProfile", Reason: config.OriginAwayByKind},
				Where:   config.WhereAway,
				Kind:    config.KindEpisode,
				Profile: awayTV,
			})
			notes = append(notes, fmt.Sprintf("migrated TV away default (%q) to rule %d", awayTV, len(rules)))
		}
	}
	libraryRule := func(lib core.Library, kind, profile string) config.QualityRule {
		r := config.QualityRule{From: &config.QualityRuleOrigin{Kind: "library", Name: lib.Name}, Libraries: []int64{int64(lib.ID)}, Kind: kind, Profile: profile}
		if defaultAway != "" {
			r.Where = config.WhereHome
			r.From.Reason = config.OriginAwayDefaultWins
		}
		return r
	}

	// Step 5 preview: resolve home defaults now so Step 4's built-in hide-rule
	// edge case knows whether the built-in library profile differs from default.
	homeMovie := cfg.Quality.Default
	homeTV := homeMovie
	if mig != nil {
		if v := mig.Home[core.MediaMovie]; validProfile(v) {
			homeMovie = v
		}
		if v := mig.Home[core.MediaTV]; validProfile(v) {
			homeTV = v
		}
	}
	if !validProfile(homeMovie) && len(profiles) > 0 {
		homeMovie = profiles[0].Name
	}
	if !validProfile(homeTV) {
		homeTV = homeMovie
	}

	// Step 4: Library profiles — filter & collection libraries lowest ID first,
	// then built-in Movies & TV Shows.
	var customLibs, builtinLibs []core.Library
	for _, lib := range profiledLibs {
		if core.IsBuiltinLibrary(lib.ID) {
			builtinLibs = append(builtinLibs, lib)
		} else {
			customLibs = append(customLibs, lib)
		}
	}
	sortByID := func(a, b core.Library) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	}
	slices.SortFunc(customLibs, sortByID)
	slices.SortFunc(builtinLibs, sortByID)

	for _, lib := range customLibs {
		raw := strings.TrimSpace(lib.QualityProfile)
		switch lib.MediaType {
		case core.TypeMovie:
			if p := renameFor(core.MediaMovie, raw); validProfile(p) {
				rules = append(rules, libraryRule(lib, "", p))
				notes = append(notes, fmt.Sprintf("migrated library %s profile (%q) to rule %d", lib.Name, p, len(rules)))
			}
		case core.TypeShow:
			if p := renameFor(core.MediaTV, raw); validProfile(p) {
				rules = append(rules, libraryRule(lib, "", p))
				notes = append(notes, fmt.Sprintf("migrated library %s profile (%q) to rule %d", lib.Name, p, len(rules)))
			}
		default:
			pm := renameFor(core.MediaMovie, raw)
			pt := renameFor(core.MediaTV, raw)
			if pm == pt {
				if validProfile(pm) {
					rules = append(rules, libraryRule(lib, "", pm))
					notes = append(notes, fmt.Sprintf("migrated library %s profile (%q) to rule %d", lib.Name, pm, len(rules)))
				}
			} else {
				if validProfile(pm) {
					rules = append(rules, libraryRule(lib, config.KindMovie, pm))
					notes = append(notes, fmt.Sprintf("migrated library %s movie profile (%q) to rule %d", lib.Name, pm, len(rules)))
				}
				if validProfile(pt) {
					rules = append(rules, libraryRule(lib, config.KindEpisode, pt))
					notes = append(notes, fmt.Sprintf("migrated library %s TV profile (%q) to rule %d", lib.Name, pt, len(rules)))
				}
			}
		}
	}

	var hiddenBuiltinFallbacks []config.QualityRule
	for _, lib := range builtinLibs {
		raw := strings.TrimSpace(lib.QualityProfile)
		media := core.MediaMovie
		kind := config.KindMovie
		homeDef := homeMovie
		if lib.MediaType == core.TypeShow || lib.ID == core.LibraryShows {
			media = core.MediaTV
			kind = config.KindEpisode
			homeDef = homeTV
		}
		p := renameFor(media, raw)
		if !validProfile(p) {
			continue
		}
		rules = append(rules, libraryRule(lib, "", p))
		notes = append(notes, fmt.Sprintf("migrated library %s profile (%q) to rule %d", lib.Name, p, len(rules)))
		// §5 edge case: in 0.3.20 a built-in library's profile also covered
		// titles hidden from that built-in library by an "only this library"
		// rule and in no other profiled library. Preserve the exact result
		// with an extra Kind rule when that can occur.
		if len(core.BuiltinLibraryExtraRules(lib.Rules)) > 0 && p != homeDef {
			fallback := libraryRule(lib, kind, p)
			fallback.Libraries = nil
			hiddenBuiltinFallbacks = append(hiddenBuiltinFallbacks, fallback)
		}
	}
	for _, r := range hiddenBuiltinFallbacks {
		rules = append(rules, r)
		notes = append(notes, fmt.Sprintf("preserved built-in library profile (%q) for hidden %s titles in rule %d", r.Profile, r.Kind, len(rules)))
	}

	// Step 5: Media defaults.
	if homeTV != "" && homeTV != homeMovie && validProfile(homeTV) {
		rule := config.QualityRule{
			From:    &config.QualityRuleOrigin{Kind: "tvDefault"},
			Kind:    config.KindEpisode,
			Profile: homeTV,
		}
		// 0.3.20 put Away above the media defaults. With a shared Away in
		// Default away the TV default must not outrank it away from home.
		if defaultAway != "" {
			rule.Where = config.WhereHome
			rule.From.Reason = config.OriginAwayDefaultWins
		}
		rules = append(rules, rule)
		notes = append(notes, fmt.Sprintf("migrated TV default (%q) to final rule %d", homeTV, len(rules)))
	}

	if err := a.cfg.Update(func(c *config.Config) error {
		if c.Quality.MigratedRules >= 1 {
			return nil
		}
		c.Quality.Default = homeMovie
		c.Quality.DefaultAway = defaultAway
		c.Quality.Rules = rules
		if c.Quality.Rules == nil {
			c.Quality.Rules = []config.QualityRule{}
		}
		config.AssignRuleIDs(&c.Quality)
		c.Quality.MigratedRules = 1
		c.Quality.RulesMigration = nil
		c.Quality.MigrationNote = &config.QualityMigrationNote{At: time.Now().UTC().Truncate(time.Second)}
		c.Quality.RulesMigrationNote = true
		if remoteKbps > 0 {
			c.Transcode.RemoteMaxBitrateKbps = 0
		}
		for _, ov := range addedOverrides {
			if !hasEquivalentOverride(c.Clients.Overrides, ov) {
				c.Clients.Overrides = append(c.Clients.Overrides, ov)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("quality migration: save config: %w", err)
	}

	// Clear retired storage fields after the migrated rules are durably saved.
	if a.libraries != nil {
		for _, lib := range profiledLibs {
			if err := a.libraries.SetQualityProfile(ctx, lib.ID, ""); err != nil && a.log != nil {
				a.log.Warn("quality migration: clear library quality_profile", "libraryId", lib.ID, "err", err)
			}
		}
	}
	if a.safety != nil {
		for _, uid := range cappedViewers {
			pol := a.safety.Get(uid)
			if pol.MaxBitrateKbps > 0 {
				pol.MaxBitrateKbps = 0
				if err := a.safety.Put(ctx, uid, pol); err != nil && a.log != nil {
					a.log.Warn("quality migration: clear viewer maxBitrateKbps", "viewerId", uid, "err", err)
				}
			}
		}
	}

	if a.log != nil {
		for _, note := range notes {
			a.log.Info("quality: " + note)
		}
	}
	return nil
}

func legacyRuleHasMatcher(lr config.LegacyQualityRule) bool {
	return strings.TrimSpace(lr.Protocol) != "" ||
		strings.TrimSpace(lr.Product) != "" ||
		strings.TrimSpace(lr.Platform) != "" ||
		lr.UserID > 0
}

func migrateLegacyDeviceMatches(lr config.LegacyQualityRule) []config.DeviceMatch {
	proto := strings.TrimSpace(lr.Protocol)
	prod := strings.TrimSpace(lr.Product)
	plat := strings.TrimSpace(lr.Platform)
	if proto == "" && prod == "" && plat == "" {
		return nil
	}

	// Protocol-only rule: narrow to that protocol's registry apps, or Contains
	// when the registry has no apps for that protocol.
	if prod == "" && plat == "" {
		var out []config.DeviceMatch
		for _, app := range clientreg.Apps() {
			if strings.EqualFold(app.Protocol, proto) {
				out = append(out, config.DeviceMatch{App: clientreg.AppID(app), Label: app.Name})
			}
		}
		if len(out) > 0 {
			return out
		}
		return []config.DeviceMatch{{Contains: proto, Label: proto}}
	}

	// Product specified (possibly with Protocol and/or Platform).
	if prod != "" {
		// Try exact registry app match when platform does not further narrow it.
		if plat == "" {
			if matched := exactRegistryApps(proto, prod); len(matched) > 0 {
				return matched
			}
			if proto == "" {
				if dt, ok := exactRegistryDeviceType(prod); ok {
					return []config.DeviceMatch{{Type: dt.ID, Label: dt.Name}}
				}
			}
			return []config.DeviceMatch{{Contains: prod, Label: prod}}
		}
		// Both product and platform were set. If they identify a unique
		// platform-specific app (e.g. "Plex for iOS" + "iOS"), use that App.
		// Otherwise preserve the narrower substring (platform if it distinguishes
		// e.g. Plex Web Chrome vs Safari, else product).
		if matched := exactRegistryApps(proto, prod); len(matched) == 1 {
			// Check whether the app's name/product already implies the platform
			// (e.g. "Plex for iOS"/"iOS", "Plex for Roku"/"Roku").
			if strings.Contains(strings.ToLower(prod), strings.ToLower(plat)) {
				return matched
			}
		}
		// Platform narrows further (e.g. Plex Web + Chrome): Contains on platform
		// matches the platform substring as 0.3.20 did.
		return []config.DeviceMatch{{Contains: plat, Label: prod + " · " + plat}}
	}

	// Platform-only rule (prod == "", plat != "").
	if proto == "" {
		if dt, ok := exactRegistryDeviceType(plat); ok && strings.EqualFold(dt.ID, plat) {
			return []config.DeviceMatch{{Type: dt.ID, Label: dt.Name}}
		}
	}
	return []config.DeviceMatch{{Contains: plat, Label: plat}}
}

func exactRegistryApps(proto, prod string) []config.DeviceMatch {
	var out []config.DeviceMatch
	for _, app := range clientreg.Apps() {
		if proto != "" && !strings.EqualFold(app.Protocol, proto) {
			continue
		}
		matched := strings.EqualFold(app.Name, prod)
		if !matched {
			for _, p := range app.AllProducts() {
				if strings.EqualFold(p, prod) {
					matched = true
					break
				}
			}
		}
		if matched {
			out = append(out, config.DeviceMatch{App: clientreg.AppID(app), Label: app.Name})
		}
	}
	return out
}

func exactRegistryDeviceType(name string) (clientreg.DeviceTypeInfo, bool) {
	for _, dt := range clientreg.DeviceTypes() {
		if strings.EqualFold(dt.ID, name) || strings.EqualFold(dt.Name, name) {
			return dt, true
		}
	}
	return clientreg.DeviceTypeInfo{}, false
}

func legacyAndroidMobileRule(lr config.LegacyQualityRule) bool {
	if strings.TrimSpace(lr.Product) == "" && strings.TrimSpace(lr.Platform) == "" {
		return false
	}
	return clientreg.HasQuirk(clientreg.Identity{
		Product:  strings.TrimSpace(lr.Product),
		Platform: strings.TrimSpace(lr.Platform),
	}, "android-mobile-ceiling")
}

// excludeEarlierLegacyMatches narrows any later migrated legacy rule that sets
// limits so it does not also match plays already decided by an earlier legacy
// rule (0.3.20 stopped at the first matching rule; the new rules engine adds up
// limits from every matching rule).
func excludeEarlierLegacyMatches(legacy []config.QualityRule, notes *[]string) []config.QualityRule {
	out := make([]config.QualityRule, 0, len(legacy))
	for j, rule := range legacy {
		if !rule.HasLimits() || j == 0 {
			out = append(out, rule)
			continue
		}
		parts := []config.QualityRule{rule}
		narrowed := false
		for i := 0; i < j; i++ {
			prev := legacy[i]
			var next []config.QualityRule
			for _, part := range parts {
				sub, changed := subtractLegacyRule(part, prev, notes)
				if changed {
					narrowed = true
				}
				next = append(next, sub...)
			}
			parts = next
			if len(parts) == 0 {
				break
			}
		}
		parts = mergeKindHalves(parts)
		if narrowed && notes != nil {
			*notes = append(*notes, fmt.Sprintf("narrowed legacy rule %d to preserve first-match limits", j+1))
		}
		out = append(out, parts...)
	}
	// Two neighbouring rules that differ only in Movies vs Episodes (an
	// owner's pair of 0.3.20 rules whose profiles merged into one) are one
	// rule for both kinds: no play matches both, and nothing sits between.
	for k := 0; k+1 < len(out); k++ {
		if out[k].Kind != "" && out[k+1].Kind == oppositeRuleKind(out[k].Kind) && sameRuleButKind(out[k], out[k+1]) {
			out[k].Kind = ""
			out = slices.Delete(out, k+1, k+2)
		}
	}
	return out
}

// mergeKindHalves joins a Movies part and an Episodes part of one legacy
// rule that are otherwise identical: together they are the rule for both
// kinds. The parts of one rule never overlap, so their order does not
// matter and the join is exact. Without it an owner edits two rules to
// change one (“Plex Web · Movies” and “Plex Web · Episodes”).
func mergeKindHalves(parts []config.QualityRule) []config.QualityRule {
	for i := 0; i < len(parts); i++ {
		if parts[i].Kind == "" {
			continue
		}
		for j := i + 1; j < len(parts); j++ {
			if parts[j].Kind != oppositeRuleKind(parts[i].Kind) || !sameRuleButKind(parts[i], parts[j]) {
				continue
			}
			parts[i].Kind = ""
			parts = slices.Delete(parts, j, j+1)
			break
		}
	}
	return parts
}

func sameRuleButKind(a, b config.QualityRule) bool {
	a, b = cloneQualityRule(a), cloneQualityRule(b)
	a.Kind, b.Kind = "", ""
	return reflect.DeepEqual(a, b)
}

func cloneQualityRule(r config.QualityRule) config.QualityRule {
	cp := r
	cp.Libraries = slices.Clone(r.Libraries)
	cp.Viewers = slices.Clone(r.Viewers)
	cp.Devices = slices.Clone(r.Devices)
	if r.From != nil {
		from := *r.From
		cp.From = &from
	}
	return cp
}

func oppositeRuleKind(kind string) string {
	switch kind {
	case config.KindMovie:
		return config.KindEpisode
	case config.KindEpisode:
		return config.KindMovie
	default:
		return ""
	}
}

// subtractLegacyRule returns a disjoint set of rules covering (r \ prev) where
// prev is an earlier migrated legacy rule (positive Viewers/Devices/Kind only).
func subtractLegacyRule(r, prev config.QualityRule, notes *[]string) ([]config.QualityRule, bool) {
	// Fast-path disjointness checks across all three legacy dimensions.
	if prev.Kind != "" && r.Kind != "" && prev.Kind != r.Kind {
		return []config.QualityRule{r}, false
	}
	if len(prev.Viewers) > 0 && len(r.Viewers) > 0 {
		if !r.ExceptViewers {
			overlap := false
			for _, v := range prev.Viewers {
				if slices.Contains(r.Viewers, v) {
					overlap = true
					break
				}
			}
			if !overlap {
				return []config.QualityRule{r}, false
			}
		} else {
			allExcluded := true
			for _, v := range prev.Viewers {
				if !slices.Contains(r.Viewers, v) {
					allExcluded = false
					break
				}
			}
			if allExcluded {
				return []config.QualityRule{r}, false
			}
		}
	}
	if len(prev.Devices) > 0 && len(r.Devices) > 0 {
		if !r.ExceptDevices {
			if deviceMatchesDisjoint(prev.Devices, r.Devices) {
				return []config.QualityRule{r}, false
			}
		} else if deviceMatchesCover(r.Devices, prev.Devices) {
			return []config.QualityRule{r}, false
		}
	}

	var out []config.QualityRule
	rem := cloneQualityRule(r)

	// Peel off Viewers.
	if len(prev.Viewers) > 0 {
		switch {
		case len(rem.Viewers) == 0:
			outside := cloneQualityRule(rem)
			outside.Viewers = slices.Clone(prev.Viewers)
			outside.ExceptViewers = true
			out = append(out, outside)
			rem.Viewers = slices.Clone(prev.Viewers)
			rem.ExceptViewers = false
		case !rem.ExceptViewers:
			var outsideV, insideV []int64
			for _, v := range rem.Viewers {
				if slices.Contains(prev.Viewers, v) {
					insideV = append(insideV, v)
				} else {
					outsideV = append(outsideV, v)
				}
			}
			if len(outsideV) > 0 {
				outside := cloneQualityRule(rem)
				outside.Viewers = outsideV
				out = append(out, outside)
			}
			rem.Viewers = insideV
		default: // rem.ExceptViewers
			var missing []int64
			for _, v := range prev.Viewers {
				if !slices.Contains(rem.Viewers, v) {
					missing = append(missing, v)
				}
			}
			outside := cloneQualityRule(rem)
			outside.Viewers = append(slices.Clone(rem.Viewers), missing...)
			outside.ExceptViewers = true
			out = append(out, outside)
			rem.Viewers = missing
			rem.ExceptViewers = false
		}
	}

	// Peel off Kind.
	if prev.Kind != "" && rem.Kind == "" {
		outside := cloneQualityRule(rem)
		outside.Kind = oppositeRuleKind(prev.Kind)
		out = append(out, outside)
		rem.Kind = prev.Kind
	}

	// Peel off Devices.
	if len(prev.Devices) > 0 {
		switch {
		case len(rem.Devices) == 0:
			outside := cloneQualityRule(rem)
			outside.Devices = slices.Clone(prev.Devices)
			outside.ExceptDevices = true
			out = append(out, outside)
		case rem.ExceptDevices:
			outside := cloneQualityRule(rem)
			for _, d := range prev.Devices {
				if !deviceMatchesContain(outside.Devices, d) {
					outside.Devices = append(outside.Devices, d)
				}
			}
			out = append(out, outside)
		case deviceMatchesCover(prev.Devices, rem.Devices):
			// rem is fully covered by prev; drop it.
		default:
			// Overlapping positive device matchers cannot be expressed as a
			// single rule with both Devices and ExceptDevices; keep rem so no
			// legitimate device match is dropped.
			out = append(out, rem)
			if notes != nil {
				*notes = append(*notes, "kept overlapping legacy device rule (cannot exclude one positive device matcher from another)")
			}
		}
	}

	return out, true
}

func deviceMatchEqual(a, b config.DeviceMatch) bool {
	fa, va := a.Field()
	fb, vb := b.Field()
	return fa == fb && strings.EqualFold(va, vb)
}

func deviceMatchesContain(list []config.DeviceMatch, want config.DeviceMatch) bool {
	for _, d := range list {
		if deviceMatchEqual(d, want) {
			return true
		}
	}
	return false
}

func deviceMatchesCover(outer, inner []config.DeviceMatch) bool {
	for _, b := range inner {
		if !deviceMatchesContain(outer, b) {
			return false
		}
	}
	return true
}

func deviceMatchesDisjoint(a, b []config.DeviceMatch) bool {
	for _, da := range a {
		for _, db := range b {
			if !deviceMatchPairDisjoint(da, db) {
				return false
			}
		}
	}
	return true
}

func deviceMatchPairDisjoint(a, b config.DeviceMatch) bool {
	fa, va := a.Field()
	fb, vb := b.Field()
	if fa == "" || fb == "" || fa == "contains" || fb == "contains" {
		return false
	}
	if fa == fb {
		return !strings.EqualFold(va, vb)
	}
	return false
}

func legacyDeviceRegistryCeiling(lr config.LegacyQualityRule) int {
	if strings.TrimSpace(lr.Product) == "" && strings.TrimSpace(lr.Platform) == "" {
		return 0
	}
	rec := clientreg.Playback(clientreg.Identity{
		Product:  strings.TrimSpace(lr.Product),
		Platform: strings.TrimSpace(lr.Platform),
	}, nil)
	if rec.ID == "" || rec.ID == "generic" {
		return 0
	}
	return rec.MaxHeight
}

func legacyHeightOverride(lr config.LegacyQualityRule) core.ClientCapabilityOverride {
	prod := strings.TrimSpace(lr.Product)
	plat := strings.TrimSpace(lr.Platform)
	ov := core.ClientCapabilityOverride{
		Product:   prod,
		Platform:  plat,
		MaxHeight: lr.MaxHeight,
		Notes:     "Migrated from quality rule",
	}
	if prod != "" && len(exactRegistryApps(lr.Protocol, prod)) > 0 {
		ov.ProductExact = true
	} else if prod != "" {
		ov.ProductMatch = prod
		ov.Product = ""
	}
	return ov
}

func hasEquivalentOverride(list []core.ClientCapabilityOverride, want core.ClientCapabilityOverride) bool {
	for _, ov := range list {
		if strings.EqualFold(ov.Product, want.Product) &&
			strings.EqualFold(ov.ProductMatch, want.ProductMatch) &&
			strings.EqualFold(ov.Platform, want.Platform) &&
			ov.MaxHeight >= want.MaxHeight {
			return true
		}
	}
	return false
}

func normalizeMigratedHeight(h int) int {
	switch {
	case h <= 0:
		return 0
	case h <= 480:
		return 480
	case h <= 720:
		return 720
	case h <= 1080:
		return 1080
	default:
		return 2160
	}
}

// cleanupLibraryQualityRules drops deleted library id from quality rules,
// removing any rule whose library list becomes empty (§3.4 N16).
func (a *App) cleanupLibraryQualityRules(id core.LibraryID) {
	if a == nil || a.cfg == nil || id <= 0 {
		return
	}
	var removed []string
	err := a.cfg.Update(func(c *config.Config) error {
		ids, changed := config.RemoveLibraryFromRules(&c.Quality, int64(id))
		if !changed {
			return nil
		}
		removed = ids
		return nil
	})
	if err != nil && a.log != nil {
		a.log.Warn("quality: cleanup rules after library delete failed", "libraryId", id, "err", err)
	} else if len(removed) > 0 && a.log != nil {
		a.log.Info("quality: removed rules referencing deleted library", "libraryId", id, "rules", removed)
	}
}
