package resolver

import (
	"cmp"
	"fmt"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// explainCeiling says why a resolution's winner is below 4K, or "" when it
// is 4K or nothing won. In order: a profile that stops below 4K (and why
// that profile applied), a client or rule ceiling below 4K (naming the exact
// declaration or source), else the fate of each 4K release (rejected, ranked
// lower, failed its check).
func (s *Service) explainCeiling(tr *Trace, profile core.QualityProfile, req core.ResolveRequest) string {
	if tr.Winner == "" {
		return ""
	}
	winner := 0
	for _, c := range tr.Candidates {
		if c.ReleaseID == tr.Winner {
			winner = c.Height
		}
	}
	if winner >= 2160 {
		return ""
	}
	configured := configuredMaxHeight(profile)
	if configured > 0 && configured < 2160 {
		return fmt.Sprintf("The %s profile stops at %dp: %s.", profile.Name, configured, profileReason(s.qualityCfg(), req.Client, req.User, profile.Name))
	}
	if profile.MaxHeight > 0 && profile.MaxHeight < 2160 {
		if src := cmp.Or(strings.TrimSpace(profile.CeilingSource), strings.TrimSpace(req.Client.MaxHeightSource)); src != "" {
			return fmt.Sprintf("Resolution capped at %dp by %s, so 4K releases were not considered.", profile.MaxHeight, src)
		}
		return fmt.Sprintf("Client capability maxHeight=%dp (unspecified source), so 4K releases were not considered.", profile.MaxHeight)
	}
	reasons := map[string]int{}
	found := 0
	for _, c := range tr.Candidates {
		if c.Height < 2160 {
			continue
		}
		found++
		switch {
		case c.Rejected && c.Attempted:
			reasons["failed its check"]++
		case c.Rejected:
			reason := strings.TrimPrefix(c.Reason, "audio language absent: ")
			reason = strings.TrimPrefix(reason, "release preference: ")
			if idx := strings.Index(reason, "; needs "); idx > 0 {
				reason = reason[:idx]
			}
			if strings.HasPrefix(reason, "title mismatch") {
				reason = "title mismatch"
			}
			reasons[reason]++
		case strings.Contains(c.CompatibilityReason, "presumed upscale"):
			reasons["presumed upscale"]++
		case strings.Contains(c.CompatibilityReason, "too little bitrate"):
			reasons["too little bitrate for 4K"]++
		case strings.Contains(c.CompatibilityReason, "accepted audio language") || strings.Contains(c.CompatibilityReason, "other language (fallback)") || strings.Contains(c.CompatibilityReason, "audio language unverified"):
			reasons["audio in another or unverified language"]++
		case strings.Contains(c.CompatibilityReason, "bare DUAL names no dub language"):
			reasons["bare DUAL without named dub language"]++
		case strings.Contains(c.CompatibilityReason, "connection carries"):
			reasons["too large for the client's connection"]++
		case strings.Contains(c.CompatibilityReason, "Usenet provider"):
			reasons["faster than recent Usenet provider reads"]++
		case strings.Contains(c.CompatibilityReason, "debrid account recently delivered"), strings.Contains(c.CompatibilityReason, "recent reads from this source"):
			reasons["faster than recent source reads"]++
		case strings.Contains(c.Route, "add-on order"):
			reasons["ranked lower in the add-on's order"]++
		default:
			reasons["ranked lower by the owner's release preferences"]++
		}
	}
	if found == 0 {
		return "No 4K release was found for this title."
	}
	parts := make([]string, 0, len(reasons))
	for r, n := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", n, r))
	}
	sort.Strings(parts)
	return fmt.Sprintf("%d 4K release(s) lost: %s.", found, strings.Join(parts, "; "))
}

// profileReason says why SelectProfile chose the named profile.
func profileReason(q config.Quality, caps core.ClientCaps, user *core.User, name string) string {
	choice := chooseProfile(q, caps, user)
	if choice.profile.Name != name {
		return "it is the first profile"
	}
	switch choice.source {
	case ProfileFromRule:
		return "a client rule chose it for this " + clientLabel(caps)
	case ProfileFromRemote:
		return "it is the profile for remote viewers"
	case ProfileFromDefault:
		return "it is the default, and no client rule matched this " + clientLabel(caps)
	}
	return "it is the first profile"
}

func clientLabel(caps core.ClientCaps) string {
	if p := strings.TrimSpace(caps.Product); p != "" {
		return "client (" + p + ")"
	}
	return "client"
}
