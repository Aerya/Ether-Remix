package resolver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

// MaxUnknownAudioProbes is the maximum number of unknown-audio-language
// candidates admitted for media-probe verification per resolve.
const MaxUnknownAudioProbes = releasepolicy.MaxUnknownAudioProbes

// classifyAudioVerification determines whether a release has unverified
// required audio languages (either from profile.ReleaseRules or from a
// specific profile.Language on a Dual/Multi or unknown-original release).
func classifyAudioVerification(
	policy *releasepolicy.Policy,
	policyCtx releasepolicy.Context,
	policyResult *releasepolicy.Result,
	profile core.QualityProfile,
	title *core.Title,
	rel core.Release,
) (required []string, unverified bool) {
	if policyResult != nil && policyResult.Rejected {
		langs, ok := policy.UnverifiedAudioLanguages(rel, policyCtx)
		if !ok {
			return nil, false
		}
		required = append(required, langs...)
		policyResult.Rejected = false
	}
	if specific := profileRequiredLanguage(profile); specific != "" {
		if !declaresConcreteLanguage(rel.Info, specific, title) {
			if !slices.Contains(required, specific) {
				required = append(required, specific)
			}
		}
	}
	if len(required) == 0 {
		return nil, false
	}
	if policyResult != nil {
		if hasWideAudioTag(rel.Info) {
			policyResult.Fallback += 10
		} else {
			policyResult.Fallback += 11
		}
	}
	return required, true
}

func declaresConcreteLanguage(info core.ReleaseInfo, want string, title *core.Title) bool {
	if len(info.Languages) == 0 {
		orig := originalLanguage(title)
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

func hasWideAudioTag(info core.ReleaseInfo) bool {
	for _, l := range info.Languages {
		if l == releaseparse.LanguageMulti || l == releaseparse.LanguageDual {
			return true
		}
	}
	return false
}

func formatRequiredLanguages(langs []string) string {
	if len(langs) == 0 {
		return "required"
	}
	if len(langs) == 1 {
		return langs[0]
	}
	return strings.Join(langs[:len(langs)-1], "/") + "/" + langs[len(langs)-1]
}

func audioAbsentPolicyReason(policy *releasepolicy.Policy, ctx releasepolicy.Context, rel core.Release, result releasepolicy.Result) string {
	ruleName, outcome, needed, ok := policy.AudioExclusion(rel, ctx)
	if !ok {
		return preferenceReason(result)
	}
	base := fmt.Sprintf("audio language absent: release preference: %s (%s)", ruleName, outcome)
	if len(needed) > 0 {
		return fmt.Sprintf("%s; needs declared %s audio", base, formatRequiredLanguages(needed))
	}
	return base
}

func audioAbsentProfileReason(profileReason string, profile core.QualityProfile) string {
	lang := profileRequiredLanguage(profile)
	if lang == "" {
		return profileReason
	}
	return fmt.Sprintf("audio language absent: %s; needs declared %s or dual/multi audio", profileReason, lang)
}

const profileLanguageAbsentPrefix = "audio language absent: audio is not in the profile's language ("

// traceHasRequiredProfileLanguageRejection reports whether any candidate in tr
// was rejected by the profile's Required audio language gate.
func traceHasRequiredProfileLanguageRejection(tr *Trace) bool {
	if tr == nil {
		return false
	}
	for _, c := range tr.Candidates {
		if c.Rejected && strings.HasPrefix(c.Reason, profileLanguageAbsentPrefix) {
			return true
		}
	}
	return false
}

func audioUnverifiedCompatNote(langs []string) string {
	label := formatRequiredLanguages(langs)
	return fmt.Sprintf("audio language unverified: release name does not declare %s audio; ranked after confirmed releases and verified by media probe before open (needs declared %s audio to rank first)", label, label)
}

func audioUnverifiedCapReason(langs []string) string {
	label := formatRequiredLanguages(langs)
	return fmt.Sprintf("audio language unverified: release name does not declare %s audio (%d-candidate probe limit reached); needs declared %s audio", label, MaxUnknownAudioProbes, label)
}

// capUnverifiedAudioCandidates keeps at most MaxUnknownAudioProbes (minus any
// already-excluded unknown-language candidates in exclude) unverified-audio
// candidates in ranked order, marking excess candidates rejected in trace.
func capUnverifiedAudioCandidates(
	cands []candidate,
	results []core.Release,
	sreq core.SearchRequest,
	exclude map[string]bool,
	policy *releasepolicy.Policy,
	policyCtx releasepolicy.Context,
	profile core.QualityProfile,
	title *core.Title,
	trace *Trace,
) ([]candidate, bool) {
	alreadyProbed := 0
	if len(exclude) > 0 {
		for _, rel := range results {
			rel = withTarget(rel, sreq)
			if !exclude[rel.ID] {
				continue
			}
			pr := policy.Evaluate(rel, policyCtx)
			if _, unverified := classifyAudioVerification(policy, policyCtx, &pr, profile, title, rel); unverified {
				alreadyProbed++
			}
		}
	}
	budget := max(0, MaxUnknownAudioProbes-alreadyProbed)
	seenUnverified := 0
	kept := make([]candidate, 0, len(cands))
	cappedAny := false
	for _, c := range cands {
		if !c.audioUnverified {
			kept = append(kept, c)
			continue
		}
		if seenUnverified < budget {
			seenUnverified++
			kept = append(kept, c)
			continue
		}
		cappedAny = true
		if trace != nil && c.trace >= 0 && c.trace < len(trace.Candidates) {
			ct := &trace.Candidates[c.trace]
			ct.Rejected = true
			ct.Reason = audioUnverifiedCapReason(c.unverifiedLangs)
		}
	}
	return kept, cappedAny
}
