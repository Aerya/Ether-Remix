package playback

import (
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

const maxUnknownAudioProbes = releasepolicy.MaxUnknownAudioProbes

func normalizedProfileLanguage(profile core.QualityProfile) string {
	if profile.EffectiveAudioLanguageMode() != core.AudioLanguageModeRequired {
		return ""
	}
	raw := strings.ToLower(strings.TrimSpace(profile.Language))
	if raw == "" || raw == core.ProfileLanguageOriginal || raw == core.ProfileLanguageAny {
		return ""
	}
	if releaseparse.KnownLanguage(profile.Language) {
		return releaseparse.LanguageName(profile.Language)
	}
	return ""
}

func playbackDeclaresConcreteLanguage(info core.ReleaseInfo, want string, title *core.Title) bool {
	if len(info.Languages) == 0 {
		if title == nil {
			return false
		}
		orig := strings.TrimSpace(title.Language)
		return orig != "" && releaseparse.LanguageMatch(orig, want) != releaseparse.LanguageNoMatch
	}
	for _, l := range info.Languages {
		if l == releaseparse.LanguageMulti || l == releaseparse.LanguageDual {
			continue
		}
		if releaseparse.LanguageMatch(l, want) != releaseparse.LanguageNoMatch {
			return true
		}
	}
	return false
}

// candidateNeedsAudioVerification reports whether rel requires a media probe to
// confirm that its audio tracks satisfy profile's strict language rules.
func candidateNeedsAudioVerification(profile core.QualityProfile, title *core.Title, rel core.Release) (required []string, unverified bool) {
	if rel.Info.Title == "" && len(rel.Info.Languages) == 0 && rel.Name != "" {
		rel.Info = releaseparse.Parse(rel.Name)
	}
	orig := ""
	if title != nil {
		orig = strings.TrimSpace(title.Language)
	}
	if len(profile.ReleaseRules) > 0 {
		if pol, err := releasepolicy.Compile(profile.ReleaseRules); err == nil {
			ctx := releasepolicy.Context{OriginalLanguage: orig}
			if pol.Evaluate(rel, ctx).Rejected {
				if langs, ok := pol.UnverifiedAudioLanguages(rel, ctx); ok {
					required = append(required, langs...)
				}
			}
		}
	}
	if specific := normalizedProfileLanguage(profile); specific != "" {
		if !playbackDeclaresConcreteLanguage(rel.Info, specific, title) {
			found := false
			for _, existing := range required {
				if existing == specific {
					found = true
					break
				}
			}
			if !found {
				required = append(required, specific)
			}
		}
	}
	return required, len(required) > 0
}

// probedRequiredAudioRejection verifies actual probed audio tracks against the
// profile's strict audio-language requirements (profile.Language and
// profile.ReleaseRules "require" audio rules). It returns "" when satisfied or
// "audio is not in the profile's language (<lang>)" when no probed track
// matches the required language.
func probedRequiredAudioRejection(profile core.QualityProfile, title *core.Title, rel core.Release, info core.MediaInfo, fromResume bool) string {
	if rel.Info.Title == "" && len(rel.Info.Languages) == 0 && rel.Name != "" {
		rel.Info = releaseparse.Parse(rel.Name)
	}
	audioCount := 0
	for _, st := range info.Streams {
		if st.Type == "audio" {
			audioCount++
		}
	}
	unverifiedLangs, needsVerify := candidateNeedsAudioVerification(profile, title, rel)
	// Only a release admitted because its name left the required language
	// unknown is checked here. A release whose name already declares the
	// language keeps the existing open path: checking it too failed many
	// correctly named releases whose tracks are tagged "und", costing an
	// extra open, indexer grab and start delay each.
	if !needsVerify {
		return ""
	}
	// Synthetic video-only test stubs or already-confirmed release names whose
	// container probe yielded no audio stream metadata are not rejected unless
	// the release itself required audio verification on a fresh open.
	if fromResume && audioCount == 0 && !info.Estimated {
		return ""
	}
	if !needsVerify && (info.Estimated || audioCount == 0) {
		return ""
	}
	if needsVerify && (info.Estimated || audioCount == 0) {
		return fmt.Sprintf("audio is not in the profile's language (%s)", unverifiedLangs[0])
	}

	if specific := normalizedProfileLanguage(profile); specific != "" {
		matched := false
		for _, tag := range releasepolicy.ProbedAudioLanguages(info.Streams) {
			if releaseparse.LanguageMatch(tag, specific) != releaseparse.LanguageNoMatch {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Sprintf("audio is not in the profile's language (%s)", specific)
		}
	}

	if len(profile.ReleaseRules) > 0 {
		if pol, err := releasepolicy.Compile(profile.ReleaseRules); err == nil {
			orig := ""
			if title != nil {
				orig = strings.TrimSpace(title.Language)
			}
			if missing, ok := pol.RequiredAudioMissing(rel, releasepolicy.Context{OriginalLanguage: orig}, info.Streams, info.Estimated); !ok {
				return fmt.Sprintf("audio is not in the profile's language (%s)", missing)
			}
		}
	}
	return ""
}
