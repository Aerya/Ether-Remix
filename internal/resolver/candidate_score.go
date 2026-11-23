package resolver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

// ResolveSourceProfile looks up the quality profile for a plugin source query.
// An empty name selects the owner's default profile (media-neutral Default,
// then DefaultMovie, then DefaultTV, then the first configured profile).
func ResolveSourceProfile(q config.Quality, name string) (core.QualityProfile, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		if p, ok := core.FindProfile(q.Profiles, q.Default, core.MediaAny); ok {
			return p, nil
		}
		if p, ok := core.FindProfile(q.Profiles, q.DefaultMovie, core.MediaMovie); ok {
			return p, nil
		}
		if p, ok := core.FindProfile(q.Profiles, q.DefaultTV, core.MediaTV); ok {
			return p, nil
		}
		if len(q.Profiles) > 0 {
			return q.Profiles[0], nil
		}
		return defaultProfile(), nil
	}
	if p, ok := core.FindProfile(q.Profiles, trimmed, core.MediaAny); ok {
		return p, nil
	}
	for _, p := range q.Profiles {
		if strings.EqualFold(p.Name, trimmed) {
			return p, nil
		}
	}
	switch strings.ToLower(trimmed) {
	case core.MediaMovie, core.MediaTV:
		return chooseProfileForMedia(q, core.ClientCaps{}, nil, strings.ToLower(trimmed), "").profile, nil
	}
	return core.QualityProfile{}, fmt.Errorf("%w: %q", ErrUnknownProfile, trimmed)
}

// ScoreSourceCandidates converts indexer search results into plugin
// SourceCandidates, populating Score, Resolution, Source and Codec on video
// release names against the requested (or owner default) quality profile and
// leaving those fields empty on non-video candidates. When s is nil, built-in
// default quality settings are used.
func ScoreSourceCandidates(ctx context.Context, s *Service, profileName string, rels []core.Release) ([]core.SourceCandidate, error) {
	var (
		q      config.Quality
		rcfg   config.Resolver
		scorer *FormatScorer
		now    time.Time
	)
	if s != nil {
		s.cfgMu.RLock()
		q = s.quality
		rcfg = s.rcfg
		scorer = s.scorer
		scorerErr := s.scorerErr
		s.cfgMu.RUnlock()
		if scorerErr != nil {
			return nil, scorerErr
		}
		now = s.now()
	} else {
		def := config.Defaults("")
		q = def.Quality
		rcfg = def.Resolver
		var err error
		scorer, err = NewFormatScorer(rcfg.CustomFormats)
		if err != nil {
			return nil, err
		}
		now = time.Now()
	}

	profile, err := ResolveSourceProfile(q, profileName)
	if err != nil {
		return nil, err
	}
	if len(rels) == 0 {
		return []core.SourceCandidate{}, nil
	}

	var policy *releasepolicy.Policy
	if s != nil {
		policy, err = s.compileRules(profile.ReleaseRules)
	} else {
		policy, err = releasepolicy.Compile(profile.ReleaseRules)
	}
	if err != nil {
		return nil, err
	}

	var hot RankingSession
	if s != nil && usesPluginRanking(profile) {
		hot = s.rankingSession(ctx, core.ClientCaps{})
		if hot != nil {
			defer hot.Close()
		}
	}

	preferRevision := !scorer.scoresRevision(profile.CustomFormatScores)
	usenetBackoff := s != nil && s.usenetBackingOff()
	out := make([]core.SourceCandidate, len(rels))

	type videoSlot struct {
		idx int
		rel core.Release
		cf  int
		rej bool
		fb  bool
		pol releasepolicy.Result
	}
	var videos []videoSlot

	for i, rel := range rels {
		infoHash := strings.ToLower(strings.TrimSpace(rel.InfoHash))
		magnet := strings.TrimSpace(rel.MagnetURL)
		dlURL := strings.TrimSpace(rel.NZBURL)
		isTorrent := rel.Protocol == core.ProtocolTorrent ||
			(rel.Protocol == "" && (infoHash != "" || magnet != ""))
		cand := core.SourceCandidate{
			InfoHash:  infoHash,
			Magnet:    magnet,
			Title:     rel.Name,
			Size:      rel.Size,
			Seeders:   rel.Seeders,
			Cached:    len(rel.DebridCached) > 0,
			Indexer:   rel.Indexer,
			IndexerID: rel.IndexerID,
			Category:  rel.Category,
		}
		if isTorrent {
			if strings.HasPrefix(strings.ToLower(dlURL), "magnet:") {
				if cand.Magnet == "" {
					cand.Magnet = dlURL
				}
				if cand.InfoHash == "" {
					cand.InfoHash = newznab.InfoHashFromMagnet(dlURL)
				}
			} else if dlURL != "" {
				cand.TorrentURL = dlURL
			}
			if cand.InfoHash == "" && cand.Magnet != "" {
				cand.InfoHash = newznab.InfoHashFromMagnet(cand.Magnet)
			}
		} else if rel.Protocol == core.ProtocolUsenet || rel.Protocol == "" {
			cand.NZBURL = dlURL
		}
		if !releaseparse.IsVideoRelease(rel.Name) {
			out[i] = cand
			continue
		}

		info := releaseparse.Parse(rel.Name)
		rel.Info = info
		cand.Resolution = info.Height
		cand.Source = string(info.Source)
		cand.Codec = info.VideoCodec

		var policyCtx releasepolicy.Context
		if hot != nil {
			verified := false
			if s != nil {
				verified = s.recentlyVerifiedKeys(ctx, s.allVerificationKeys(rel))
			}
			policyCtx.ExternalMatch = func(r core.Release, c core.ReleaseCondition) (bool, bool) {
				return hot.Match(r, c, verified)
			}
		}
		policyResult := policy.Evaluate(rel, policyCtx)
		hardSubbed := releaseparse.HardSubbed(rel.Name)
		if rcfg.HardSubtitles != "allow" && hardSubbed {
			policyResult.Fallback += 100
		}

		rejected := isRejectedSourceRelease(s, rel, profile, rcfg, policyResult, hardSubbed)
		pastRet := s != nil && s.pastRetention(rel, now)
		sc, serr := scoreSourceRelease(s, scorer, rel, profile, policyResult, formatFacts{pastRetention: pastRet})
		if serr != nil {
			if errors.Is(serr, errFormatTimeout) {
				rejected = true
			} else {
				return nil, serr
			}
		}
		for _, match := range sc.matches {
			if core.CountsTowardMinimum(match.ID) && match.Score <= -10000 {
				rejected = true
				break
			}
		}

		belowScore := profile.MinCustomFormatScore != nil && sc.counted < *profile.MinCustomFormatScore
		belowQuality := belowPreferredQuality(info, profile)
		out[i] = cand
		videos = append(videos, videoSlot{
			idx: i,
			rel: rel,
			cf:  sc.total,
			rej: rejected,
			fb:  belowScore || belowQuality || policyResult.Fallback > 0,
			pol: policyResult,
		})
	}

	if hot != nil && len(videos) > 0 {
		videoRels := make([]core.Release, len(videos))
		verified := make([]bool, len(videos))
		for i, v := range videos {
			videoRels[i] = v.rel
		}
		if s != nil {
			vmap := s.verifiedReleases(ctx, videoRels)
			for i, r := range videoRels {
				verified[i] = vmap[r.ID]
			}
		}
		steps := make([]string, 0, len(profile.PluginScores))
		for step := range profile.PluginScores {
			if core.IsPluginRankingStep(step) {
				steps = append(steps, step)
			}
		}
		slices.Sort(steps)
		for _, step := range steps {
			keys := hot.Rank(step, profile.PluginScores[step], videoRels, verified)
			if len(keys) != len(videos) {
				continue
			}
			for i, key := range keys {
				points := int(max(min(key, maxPluginPoints), -maxPluginPoints))
				videos[i].cf += points
			}
		}
	}

	for _, v := range videos {
		backingOff := usenetBackoff && isUsenetSourceRelease(v.rel)
		score := combineSourceCandidateScore(v.rel.Info, profile, preferRevision, v.cf, v.rej, v.fb, v.pol.Fallback, backingOff)
		out[v.idx].Score = &score
	}
	return out, nil
}

func isUsenetSourceRelease(rel core.Release) bool {
	if rel.Protocol == core.ProtocolUsenet {
		return true
	}
	return rel.Protocol == "" && strings.TrimSpace(rel.NZBURL) != "" && strings.TrimSpace(rel.InfoHash) == "" && strings.TrimSpace(rel.MagnetURL) == ""
}

func scoreSourceRelease(s *Service, scorer *FormatScorer, rel core.Release, profile core.QualityProfile, policy releasepolicy.Result, facts formatFacts) (releaseScore, error) {
	if s != nil {
		return s.scoreRelease(rel, profile, core.ClientCaps{}, nil, policy, facts)
	}
	formatRel := sizedForEpisode(rel)
	counted, matches, err := scorer.ScoreForProfile(formatRel, profile.CustomFormatScores, nil)
	if err != nil {
		return releaseScore{}, err
	}
	total := counted
	revisionScored := false
	for _, m := range matches {
		revisionScored = revisionScored || (m.revision && m.Score != 0)
	}
	for _, id := range builtinFormatMatches(formatRel, profile, core.ClientCaps{}, facts) {
		if id == core.FormatRepackProper && revisionScored {
			continue
		}
		score, scored := profile.CustomFormatScores[id]
		if id == core.FormatViewerLanguage && facts.viewerLanguage.ProfilePreferred != "" && (!scored || score == 0) {
			score, scored = defaultPreferredLanguageScore, true
		}
		if !scored || score == 0 {
			continue
		}
		name := BuiltinFormatName(id)
		if id == core.FormatViewerLanguage {
			score = viewerLanguageScore(score, facts.viewerLanguage.Rank)
			name = viewerLanguageFormatName(facts.viewerLanguage)
		}
		total += score
		matches = append(matches, FormatMatch{ID: id, Name: name, Score: score})
	}
	for _, r := range profile.ReleaseRules {
		if r.Action != "prefer" || r.Disabled || policy.PreferRule(r.ID) <= 0 {
			continue
		}
		id := core.RuleFormatPrefix + r.ID
		score := profile.CustomFormatScores[id]
		total += score
		matches = append(matches, FormatMatch{ID: id, Name: r.Name, Score: score})
	}
	return releaseScore{total: total, counted: counted, matches: matches}, nil
}

func isRejectedSourceRelease(s *Service, rel core.Release, profile core.QualityProfile, rcfg config.Resolver, policyResult releasepolicy.Result, hardSubbed bool) bool {
	if rcfg.HardSubtitles == "exclude" && hardSubbed {
		return true
	}
	if rel.Password && rel.Protocol == core.ProtocolTorrent {
		return true
	}
	if s != nil && (s.isBadRelease(rel) || s.recentlyFailedRelease(rel)) {
		return true
	}
	if policyResult.Rejected {
		return true
	}
	for _, term := range rcfg.BlockedTerms {
		if releaseparse.KeywordMatch(rel.Name, term) {
			return true
		}
	}
	for _, g := range rcfg.BlockedGroups {
		if g != "" && strings.EqualFold(rel.Info.Group, g) {
			return true
		}
	}
	if sourceSuitability(rel, nil, profile, false) != "" {
		return true
	}
	lowerName := strings.ToLower(rel.Name)
	for _, tok := range strings.FieldsFunc(lowerName, func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == ' '
	}) {
		if tok == "sample" {
			return true
		}
	}
	return rejectPolicy(rel, &core.Title{}, profile, core.ClientCaps{}, false, false) != ""
}

func combineSourceCandidateScore(info core.ReleaseInfo, profile core.QualityProfile, preferRevision bool, cf int, rejected, fallback bool, policyFallback int, providerBackoff bool) int {
	if rejected {
		return -1_000_000 + clampCandidatePoints(cf, -40_000, 40_000)
	}
	pos := qualityPosition(profile.QualityOrder, info, info.Height)
	rev := 0
	if preferRevision {
		rev = revisionRank(info)
	}
	if providerBackoff {
		order := pos*100 + rev*50 + clampCandidatePoints(cf/100, -100, 100)
		if fallback {
			order -= 5_000
		}
		return -930_000 + clampCandidatePoints(order, -15_000, 15_000)
	}
	if fallback {
		tier := min(max(policyFallback, 0), 8)
		return -500_000 - tier*50_000 + fallbackHeight(info)*10 + clampCandidatePoints(cf, -10_000, 10_000)
	}
	return pos*100_000 + rev*50_000 + clampCandidatePoints(cf, -20_000, 20_000)
}

func clampCandidatePoints(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
