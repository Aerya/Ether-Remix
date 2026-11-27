package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
)

// normalizeQuality settles q into the one shape this version accepts; see
// normalizeQualityDocument. It works on q's JSON form so the typed and the
// document paths cannot drift apart, and reports what it changed.
func normalizeQuality(q *Quality) []string {
	b, err := json.Marshal(q)
	if err != nil {
		return nil
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&doc) != nil {
		return nil
	}
	notes := normalizeQualityDocument(doc)
	if len(notes) == 0 {
		return nil
	}
	if b, err = json.Marshal(doc); err != nil {
		return nil
	}
	var out Quality
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	*q = out
	return notes
}

// normalizeSharedDocument normalizes the quality section of a settings
// document in place, keeping fields this version does not know.
func normalizeSharedDocument(doc map[string]any) []string {
	quality, ok := doc["quality"].(map[string]any)
	if !ok {
		return nil
	}
	return normalizeQualityDocument(quality)
}

// normalizeQualityDocument settles a quality section: it runs the one-time
// move off per-media profiles (migrateMediaProfiles) and then points the
// defaults and every client rule at a profile that exists. It runs on every
// load (config.json and the cluster's shared settings), so it must be
// idempotent: a settled section comes back unchanged and with no notes.
func normalizeQualityDocument(quality map[string]any) []string {
	notes := migrateMediaProfiles(quality)
	profiles, _ := quality["profiles"].([]any)
	if len(profiles) == 0 {
		return notes
	}
	exists := func(name string) bool {
		for _, item := range profiles {
			if p, ok := item.(map[string]any); ok && docString(p, "name") == name {
				return true
			}
		}
		return false
	}
	first := ""
	if p, ok := profiles[0].(map[string]any); ok {
		first = docString(p, "name")
	}
	// Each repair resolves as the missing name already did at playback: a
	// missing Default profile is the first one, a missing Default away the
	// Default, and a rule naming a removed profile keeps its conditions and
	// limits but no longer picks a profile. Validation refuses a name no
	// profile has, so left alone it would block every quality change.
	if v := docString(quality, "default"); first != "" && (v == "" || !exists(v)) {
		notes = append(notes, fmt.Sprintf("default named a missing profile %q; now %q", v, first))
		quality["default"] = first
	}
	if v := docString(quality, "defaultAway"); v != "" && !exists(v) {
		notes = append(notes, fmt.Sprintf("defaultAway named a missing profile %q; now the Default", v))
		quality["defaultAway"] = ""
	}
	if rules, ok := quality["rules"].([]any); ok {
		changed := false
		for i, item := range rules {
			r, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if prof := docString(r, "profile"); prof != "" && !exists(prof) {
				c := maps.Clone(r)
				delete(c, "profile")
				rules[i] = c
				changed = true
				notes = append(notes, fmt.Sprintf("quality.rules[%d].profile named a missing profile %q; now limits only", i, prof))
			}
		}
		if changed {
			quality["rules"] = rules
		}
	}
	return notes
}

// RulesMigration carries the v0.3.20 mapping (per-media defaults and
// client rules) from config load, which cannot reach the libraries or
// viewers, to the app, which turns it into rules once at startup
// (App.migrateQualityRules) and clears it. Delete both, with
// migrateMediaProfiles, in the release after next.
type RulesMigration struct {
	// Rules are the v0.3.20 client rules in order, their profiles already
	// renamed; a rule for both media whose profile was kept apart is split.
	Rules []LegacyQualityRule `json:"rules,omitempty"`
	// Home and Away are the profiles each media ("movie", "tv") used at
	// home and away, already renamed; "" means none was set.
	Home map[string]string `json:"home,omitempty"`
	Away map[string]string `json:"away,omitempty"`
	// Renames maps a media's old profile name to its new one, for the
	// libraries that had chosen a profile by name.
	Renames map[string]map[string]string `json:"renames,omitempty"`
}

// LegacyQualityRule is a v0.3.20 client rule.
type LegacyQualityRule struct {
	Protocol             string `json:"protocol,omitempty"`
	Product              string `json:"product,omitempty"`
	Platform             string `json:"platform,omitempty"`
	UserID               int64  `json:"userId,omitempty"`
	MediaType            string `json:"mediaType,omitempty"`
	Profile              string `json:"profile"`
	MaxHeight            int    `json:"maxHeight,omitempty"`
	ConvertSurroundAudio bool   `json:"convertSurroundAudio,omitempty"`
}

// legacyMediaDefaults are the per-media At home and Away fields of
// v0.3.20 and earlier.
var legacyMediaDefaults = []struct{ media, home, away string }{
	{core.MediaMovie, "defaultMovie", "remoteMovie"},
	{core.MediaTV, "defaultTv", "remoteTv"},
}

// legacyProfileMedia is the order v0.3.5 to v0.3.20 wrote a name's
// per-media profiles in: the movie one, then the TV one.
var legacyProfileMedia = []string{core.MediaMovie, core.MediaTV}

// legacyQualityDocument reports whether a quality section still has the
// v0.3.20 shape: a remote or per-media default key, a profile with a
// media, or a rule without an id.
func legacyQualityDocument(quality map[string]any) bool {
	if n, ok := quality["migratedRules"]; ok && n != nil && fmt.Sprint(n) != "0" {
		return false
	}
	if _, has := quality["remote"]; has {
		return true
	}
	for _, f := range legacyMediaDefaults {
		for _, k := range []string{f.home, f.away} {
			if _, has := quality[k]; has {
				return true
			}
		}
	}
	profiles, _ := quality["profiles"].([]any)
	for _, item := range profiles {
		if p, ok := item.(map[string]any); ok {
			if _, has := p["mediaType"]; has {
				return true
			}
		}
	}
	rules, _ := quality["rules"].([]any)
	for _, item := range rules {
		if r, ok := item.(map[string]any); ok && docString(r, "id") == "" {
			return true
		}
	}
	return false
}

// migrateMediaProfiles is the config half of the one-time move off the
// v0.3.20 model, where every profile served movies or TV shows (one name
// could be two profiles) and the mapping was spread over per-media
// defaults, client rules and library profiles. A profile now ranks
// releases for whatever rule picks it.
//
// Profiles. The movie and TV profiles of a name become one profile when
// they rank alike. They were copies of each other unless the owner edited
// one: a movie profile drops the scores of the formats only episodes can
// match and has no use for episode size limits, so those are taken from
// the TV profile and the rest must be equal. Movie and TV profiles of a
// name that the owner made rank differently are both kept, the TV one as
// "<name> (TV)", so no customization is lost or blended into the other.
// A profile with no media (saved before v0.3.5) stays as it is.
//
// Mapping. Default becomes the movie At home profile; the rules list is
// emptied and everything the app needs to rebuild it exactly is recorded
// in rulesMigration.
func migrateMediaProfiles(quality map[string]any) []string {
	if !legacyQualityDocument(quality) {
		return nil
	}
	profiles, _ := quality["profiles"].([]any)
	var notes []string

	// Which media each profile served. Copies of a name without a media
	// beyond the first are a per-media list whose media was lost (an
	// older node saved over it): the n-th is the n-th media.
	type variant struct {
		media string // "" serves every media
		p     map[string]any
	}
	byName := map[string][]variant{}
	var order []string
	unmarkedSeen := map[string]int{}
	unmarked := map[string]int{}
	for _, item := range profiles {
		if p, ok := item.(map[string]any); ok && docString(p, "mediaType") == "" {
			unmarked[docString(p, "name")]++
		}
	}
	var others []any
	for _, item := range profiles {
		p, ok := item.(map[string]any)
		if !ok || docString(p, "name") == "" {
			others = append(others, item)
			continue
		}
		name, media := docString(p, "name"), docString(p, "mediaType")
		if media == "" && unmarked[name] > 1 {
			if i := unmarkedSeen[name]; i < len(legacyProfileMedia) {
				media = legacyProfileMedia[i]
			}
			unmarkedSeen[name]++
		}
		c := maps.Clone(p)
		delete(c, "mediaType")
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = append(byName[name], variant{media, c})
	}

	taken := map[string]bool{}
	for _, name := range order {
		taken[name] = true
	}
	renames := map[string]map[string]string{}
	rename := func(media, from, to string) {
		if renames[media] == nil {
			renames[media] = map[string]string{}
		}
		renames[media][from] = to
	}
	out := make([]any, 0, len(profiles))
	for _, name := range order {
		vs := byName[name]
		kept := []map[string]any{}
		for _, v := range vs {
			merged := -1
			for i, k := range kept {
				if m, ok := mergeMediaProfiles(k, v.p, v.media); ok {
					kept[i], merged = m, i
					break
				}
			}
			target := name
			if merged < 0 {
				if len(kept) > 0 {
					suffix := " (TV)"
					if v.media != core.MediaTV {
						suffix = " (2)"
					}
					target = name + suffix
					for n := 2; taken[target]; n++ {
						target = fmt.Sprintf("%s%s %d", name, suffix, n)
					}
					taken[target] = true
					v.p["name"] = target
					notes = append(notes, fmt.Sprintf("the %s profile %q ranks differently from the other %q; kept it as %q", mediaWord(v.media), name, name, target))
				}
				kept = append(kept, v.p)
			} else {
				target = docString(kept[merged], "name")
				notes = append(notes, fmt.Sprintf("merged the %s profile %q into %q", mediaWord(v.media), name, target))
			}
			if v.media == "" {
				for _, m := range legacyProfileMedia {
					if target != name {
						rename(m, name, target)
					}
				}
			} else if target != name {
				rename(v.media, name, target)
			}
		}
		for _, k := range kept {
			out = append(out, k)
		}
	}
	out = append(out, others...)
	quality["profiles"] = out

	renamed := func(media, name string) string {
		if to, ok := renames[media][name]; ok {
			return to
		}
		return name
	}
	mig := RulesMigration{Home: map[string]string{}, Away: map[string]string{}}
	for _, f := range legacyMediaDefaults {
		home := docString(quality, f.home)
		if home == "" {
			home = docString(quality, "default")
		}
		away := docString(quality, f.away)
		if away == "" {
			away = docString(quality, "remote")
		}
		mig.Home[f.media] = renamed(f.media, home)
		mig.Away[f.media] = renamed(f.media, away)
		delete(quality, f.home)
		delete(quality, f.away)
	}
	delete(quality, "remote")
	if len(renames) > 0 {
		mig.Renames = renames
	}
	quality["default"] = mig.Home[core.MediaMovie]
	quality["defaultAway"] = ""

	if rules, ok := quality["rules"].([]any); ok {
		for _, item := range rules {
			r, ok := item.(map[string]any)
			if !ok {
				continue
			}
			var lr LegacyQualityRule
			if b, err := json.Marshal(r); err != nil || json.Unmarshal(b, &lr) != nil {
				continue
			}
			if lr.MediaType != "" || renamed(core.MediaMovie, lr.Profile) == renamed(core.MediaTV, lr.Profile) {
				media := lr.MediaType
				if media == "" {
					media = core.MediaMovie
				}
				lr.Profile = renamed(media, lr.Profile)
				mig.Rules = append(mig.Rules, lr)
				continue
			}
			prof := lr.Profile
			for _, m := range legacyProfileMedia {
				c := lr
				c.MediaType = m
				c.Profile = renamed(m, prof)
				mig.Rules = append(mig.Rules, c)
			}
		}
	}
	quality["rules"] = []any{}
	b, _ := json.Marshal(mig)
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	quality["rulesMigration"] = doc
	notes = append(notes, fmt.Sprintf("profiles no longer have a media; Default is %q; the mapping moves into rules at startup", docString(quality, "default")))
	return notes
}

func mediaWord(media string) string {
	switch media {
	case core.MediaMovie:
		return "movie"
	case core.MediaTV:
		return "TV"
	}
	return "shared"
}

// episodeOnlyProfileKeys are the profile fields only episodes use.
var episodeOnlyProfileKeys = []string{"episodeMinSizeMbPerMin", "episodeMaxSizeMbPerMin"}

// mergeMediaProfiles merges profile b of media into a when the two rank
// every title alike: equal once the formats only episodes match and the
// episode size limits are set aside. The merged profile takes those from
// the one that served episodes, so nothing either one ranked changes.
func mergeMediaProfiles(a, b map[string]any, media string) (map[string]any, bool) {
	strip := func(p map[string]any) map[string]any {
		c := maps.Clone(p)
		delete(c, "name")
		for _, k := range episodeOnlyProfileKeys {
			delete(c, k)
		}
		if scores, ok := c["customFormatScores"].(map[string]any); ok {
			s := maps.Clone(scores)
			for _, id := range core.TVOnlyFormats {
				delete(s, id)
			}
			if len(s) == 0 {
				delete(c, "customFormatScores")
			} else {
				c["customFormatScores"] = s
			}
		}
		return c
	}
	if !reflect.DeepEqual(strip(a), strip(b)) {
		return nil, false
	}
	if media != core.MediaTV {
		return a, true
	}
	out := maps.Clone(a)
	for _, k := range episodeOnlyProfileKeys {
		if v, ok := b[k]; ok {
			out[k] = v
		} else {
			delete(out, k)
		}
	}
	if bs, ok := b["customFormatScores"].(map[string]any); ok {
		scores, _ := out["customFormatScores"].(map[string]any)
		scores = maps.Clone(scores)
		if scores == nil {
			scores = map[string]any{}
		}
		for _, id := range core.TVOnlyFormats {
			if v, ok := bs[id]; ok {
				scores[id] = v
			}
		}
		out["customFormatScores"] = scores
	}
	return out, true
}

func docString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// CloneQualityOrder deep-copies a quality order.
func CloneQualityOrder(order []core.QualityItem) []core.QualityItem {
	if order == nil {
		return nil
	}
	out := make([]core.QualityItem, len(order))
	for i, it := range order {
		out[i] = core.QualityItem{Name: it.Name, Qualities: slices.Clone(it.Qualities)}
	}
	return out
}
