package resolver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// resolveQuick keeps discovery, ranking and validation inside each source.
// In particular, neither the feed nor a remembered pin can jump the queue.
func (s *Service) resolveQuick(ctx context.Context, req core.ResolveRequest, profile core.QualityProfile) (*core.Resolution, error) {
	if s.quickSearch == nil {
		return nil, core.ErrNoSources
	}
	cfg := s.resolverCfg().SourceOrder.Quick
	// A manual preference or old pin must not relax mandatory filters or
	// select a source before the earlier enabled ones have been exhausted.
	req.Prefer, req.PreferredOnly = "", false
	search := s.searchRequest(ctx, req.Title)
	trace := Trace{TitleKey: req.Title.Key, TitleName: consoleTitleName(req.Title), Profile: profile.Name, At: s.now(), Failover: len(req.Exclude) > 0}
	started := time.Now()
	defer func() {
		trace.TotalDuration = time.Since(started)
		if !core.IsSpeculative(ctx) && !channelAvailabilityCheck(ctx) {
			s.logPlayDecision(trace)
		}
		s.addTrace(trace)
		s.recordHistory(ctx, trace)
	}()
	seenSources := map[string]bool{}
	seen := map[string]bool{}
	var failures []error
	order := []string{}
	for _, source := range cfg.Order {
		switch source {
		case "alko", "usenet", "torrent", "seedstrem":
			order = append(order, source)
		}
	}
	for _, source := range append(order, "addons", "addons-rescue") {
		if seenSources[source] {
			continue
		}
		switch source {
		case "alko", "usenet", "torrent", "seedstrem", "addons", "addons-rescue":
		default:
			continue
		}
		seenSources[source] = true
		stageSource := source
		if source == "addons-rescue" {
			stageSource = "addons"
		}
		stage := core.WithQuickSource(ctx, stageSource, nil)
		searchStart := time.Now()
		results, err := s.quickSearch(stage, search, source, cfg.SeedstremID)
		trace.SearchDuration += time.Since(searchStart)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			failures = append(failures, err)
		}
		trace.Discovery = append(trace.Discovery, DiscoveryTrace{Path: "quick:" + source, Count: len(results), Rescue: stageSource == "addons"})
		eligible := make([]core.Release, 0, len(results))
		for _, r := range s.normalizeSearchResults(search, results) {
			if seen[r.ID] || !quickReleaseAllowed(stageSource, r) {
				continue
			}
			seen[r.ID] = true
			eligible = append(eligible, r)
		}
		s.rememberReleases(req.Title, eligible)
		candidates, _, err := s.rankCandidates(stage, req, search, profile, eligible, &trace, "")
		if err != nil {
			failures = append(failures, err)
			continue
		}
		strict := candidates[:0]
		for _, c := range candidates {
			if c.fallback || c.belowScore || !c.langOK {
				trace.Candidates[c.trace].Rejected = true
				trace.Candidates[c.trace].Reason = "quick mode: language or quality requirements not met"
				continue
			}
			strict = append(strict, c)
		}
		candidates = strict
		// The ordinary bounded validator retains download guards, health caches,
		// network policy and compatibility checks. No cross-source preparation.
		index, payload, err := s.validateCandidates(stage, candidates, &trace, 1)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if index < 0 && source == "torrent" && core.PlaybackIntent(ctx) {
			if queuedIndex, queuedPayload, queuedErr, handled := s.uncachedOutcome(stage, candidates, eligible, &trace); handled {
				index, payload, err = queuedIndex, queuedPayload, queuedErr
			}
		}
		if index >= 0 && err == nil {
			trace.Err = ""
			trace.Routing = "quick:" + stageSource
			return s.acceptResolution(stage, req.Title.Key, candidates, index, payload, &trace), nil
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	trace.Err = "quick sources exhausted"
	return nil, errors.Join(append(failures, core.ErrNotAvailable)...)
}

func quickReleaseAllowed(source string, r core.Release) bool {
	switch source {
	case "alko":
		return strings.HasPrefix(r.ID, "sfr-alko:") && core.IsVerifiedFile(r) && r.InfoHash == "" && r.MagnetURL == ""
	case "usenet":
		return r.Protocol == core.ProtocolUsenet
	case "torrent":
		return r.Protocol == core.ProtocolTorrent
	case "seedstrem":
		return r.Protocol == core.ProtocolHTTP && r.StreamURL != "" && r.StreamDebrid == ""
	case "addons":
		return true
	}
	return false
}
