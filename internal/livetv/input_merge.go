package livetv

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// ChannelInputMerge configures regex/pattern-based stream merging on a target
// IPTV channel. Matching imported channels attach their streams to the target
// channel's StreamSources (up to MaxChannelInputs) and are optionally hidden as
// standalone lineup entries without deleting their stored channel rows.
type ChannelInputMerge struct {
	Rules          []OwnerGroupRule `json:"rules,omitempty"`
	Match          string           `json:"match,omitempty"`
	HideDuplicates *bool            `json:"hideDuplicates,omitempty"`
}

func (m *ChannelInputMerge) hideDuplicatesEnabled() bool {
	return m != nil && (m.HideDuplicates == nil || *m.HideDuplicates)
}

func hasInputMergeRules(c Channel) bool {
	return c.Kind == "iptv" && c.InputMerge != nil && len(c.InputMerge.Rules) > 0
}

func hasMergedStreamSources(c Channel) bool {
	for i := range c.StreamSources {
		if c.StreamSources[i].MergedFrom != "" {
			return true
		}
	}
	return false
}

func normalizeChannelInputMerge(m *ChannelInputMerge) (*ChannelInputMerge, error) {
	if m == nil || len(m.Rules) == 0 {
		return nil, nil
	}
	rules, match, err := validateOwnerGroupRules(m.Rules, m.Match, false)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, nil
	}
	var hide *bool
	if m.HideDuplicates != nil && !*m.HideDuplicates {
		v := false
		hide = &v
	}
	return &ChannelInputMerge{
		Rules:          rules,
		Match:          match,
		HideDuplicates: hide,
	}, nil
}

// Quality tiers ordered best-first: 4K/UHD > FHD/1080 > HD/720 > untagged > SD.
const (
	qualityTier4K      = 0
	qualityTierFHD     = 1
	qualityTierHD      = 2
	qualityTierUnknown = 3
	qualityTierSD      = 4
)

func streamQualityTier(names ...string) (int, string) {
	best := qualityTierUnknown
	sawSD := false
	for _, raw := range names {
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)
		start := -1
		for i := 0; i <= len(lower); i++ {
			var ch byte
			if i < len(lower) {
				ch = lower[i]
			}
			isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
			if isAlnum {
				if start < 0 {
					start = i
				}
				continue
			}
			if start < 0 {
				continue
			}
			tok := lower[start:i]
			start = -1
			tier, isMarker, _ := classifyQualityToken(tok)
			if !isMarker {
				continue
			}
			switch tier {
			case qualityTier4K:
				return qualityTier4K, "4K"
			case qualityTierFHD:
				if qualityTierFHD < best {
					best = qualityTierFHD
				}
			case qualityTierHD:
				if qualityTierHD < best {
					best = qualityTierHD
				}
			case qualityTierSD:
				sawSD = true
			}
		}
	}
	switch best {
	case qualityTierFHD:
		return qualityTierFHD, "FHD"
	case qualityTierHD:
		return qualityTierHD, "HD"
	default:
		if sawSD {
			return qualityTierSD, "SD"
		}
		return qualityTierUnknown, ""
	}
}

func sourcePriorityKey(s Source) int {
	if s.Priority > 0 {
		return s.Priority
	}
	return 1 << 30
}

func sortSourcesByPriority(srcs []Source) {
	sort.SliceStable(srcs, func(i, j int) bool {
		pi, pj := sourcePriorityKey(srcs[i]), sourcePriorityKey(srcs[j])
		if pi != pj {
			return pi < pj
		}
		return srcs[i].ID < srcs[j].ID
	})
}

const unrankedSourcePriority = 1 << 29

func buildSourcePriorityMaps(srcs []Source) (names map[string]string, ranks map[string]int) {
	ordered := slices.Clone(srcs)
	sortSourcesByPriority(ordered)
	names = make(map[string]string, len(ordered))
	ranks = make(map[string]int, len(ordered))
	for i, s := range ordered {
		if s.Name != "" {
			names[s.ID] = s.Name
		}
		ranks[s.ID] = i + 1
	}
	return names, ranks
}

type compiledChannelMerge struct {
	channelID      string
	hideDuplicates bool
	compiled       *compiledOwnerGroup
}

// inputMergeIndex caches compiled channel input merge rules per
// livetv_channel_epoch generation so preview and reconciliation reuse compiled
// regexes across calls within the same lineup epoch.
type inputMergeIndex struct {
	rev         int64
	targets     map[string]compiledChannelMerge
	sourceNames map[string]string
	sourceRanks map[string]int
}

func compileInputMergeIndex(rev int64, cs []Channel, srcs []Source) *inputMergeIndex {
	names, ranks := buildSourcePriorityMaps(srcs)
	targets := map[string]compiledChannelMerge{}
	for i := range cs {
		c := cs[i]
		if !hasInputMergeRules(c) {
			continue
		}
		cg := compileSingleOwnerGroupBestEffort(OwnerGroup{
			Rules: c.InputMerge.Rules,
			Match: c.InputMerge.Match,
		}, names)
		targets[c.ID] = compiledChannelMerge{
			channelID:      c.ID,
			hideDuplicates: c.InputMerge.hideDuplicatesEnabled(),
			compiled:       cg,
		}
	}
	return &inputMergeIndex{
		rev:         rev,
		targets:     targets,
		sourceNames: names,
		sourceRanks: ranks,
	}
}

func (s *Service) getCachedInputMergeIndex(rev int64) *inputMergeIndex {
	s.inputMergeMu.Lock()
	defer s.inputMergeMu.Unlock()
	if s.inputMergeIdx != nil && s.inputMergeRev == rev {
		return s.inputMergeIdx
	}
	return nil
}

func (s *Service) setCachedInputMergeIndex(idx *inputMergeIndex) {
	if idx == nil {
		return
	}
	s.inputMergeMu.Lock()
	s.inputMergeIdx = idx
	s.inputMergeRev = idx.rev
	s.inputMergeMu.Unlock()
}

func (s *Service) invalidateInputMergeIndex() {
	s.inputMergeMu.Lock()
	s.inputMergeIdx = nil
	s.inputMergeRev = 0
	s.inputMergeMu.Unlock()
}

func (s *Service) sourcesTx(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]Source, error) {
	rows, err := q.QueryContext(ctx, `SELECT data FROM livetv_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		src, _, err := s.decodeSource(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortSourcesByPriority(out)
	return out, nil
}

func donorCandidateStreams(donor Channel) []StreamSource {
	if len(donor.StreamSources) > 0 {
		out := make([]StreamSource, 0, len(donor.StreamSources))
		for _, src := range donor.StreamSources {
			if src.MergedFrom != "" || src.AutoFrom != "" || !validStreamURL(strings.TrimSpace(src.URL)) {
				continue
			}
			out = append(out, src)
		}
		if len(out) > 0 {
			return out
		}
	}
	rawURL := strings.TrimSpace(donor.StreamURL)
	if !validStreamURL(rawURL) {
		return nil
	}
	return []StreamSource{{
		ID:      stableID(donor.ID, "playlist"),
		Name:    "Imported stream",
		URL:     rawURL,
		Enabled: true,
	}}
}

func channelOwnBaseStreams(target Channel) []StreamSource {
	var out []StreamSource
	for _, src := range target.StreamSources {
		if src.MergedFrom != "" {
			continue
		}
		out = append(out, src)
	}
	if len(out) > 0 {
		return out
	}
	rawURL := strings.TrimSpace(target.StreamURL)
	if !validStreamURL(rawURL) {
		return nil
	}
	id := stableID(target.ID, "primary")
	name := "Primary stream"
	if target.SourceID != "" {
		id = stableID(target.ID, "playlist")
		name = "Imported stream"
	}
	return []StreamSource{{
		ID:      id,
		Name:    name,
		URL:     rawURL,
		Enabled: true,
	}}
}

func effectiveInputSourceID(target Channel, src StreamSource) string {
	if src.AuthSourceID != "" {
		return src.AuthSourceID
	}
	if target.SourceID != "" && (src.ID == stableID(target.ID, "playlist") || src.ID == stableID(target.ID, "primary")) {
		return target.SourceID
	}
	return ""
}

func inputQualityNames(target Channel, src StreamSource, donorByID map[string]Channel) []string {
	if src.MergedFrom != "" {
		if donor, ok := donorByID[src.MergedFrom]; ok {
			return []string{src.Name, donor.Name, donor.TvgName}
		}
		return []string{src.Name}
	}
	if src.ID == stableID(target.ID, "playlist") || src.ID == stableID(target.ID, "primary") {
		if src.Name != "" && src.Name != "Imported stream" && src.Name != "Primary stream" {
			return []string{src.Name, target.TvgName, target.Name}
		}
		return []string{target.TvgName, target.Name}
	}
	return []string{src.Name}
}

type rankedChannelInput struct {
	src        StreamSource
	sourceRank int
	quality    int
	ownPrimary bool
	order      int
}

func orderAndBoundTargetInputs(target Channel, candidates []StreamSource, sourceRanks map[string]int, donorByID map[string]Channel) []StreamSource {
	playlistID := stableID(target.ID, "playlist")
	primaryID := stableID(target.ID, "primary")

	ranked := make([]rankedChannelInput, 0, len(candidates))
	for i, src := range candidates {
		src.URL = strings.TrimSpace(src.URL)
		if !validStreamURL(src.URL) {
			continue
		}
		effSource := effectiveInputSourceID(target, src)
		sRank := unrankedSourcePriority
		if effSource != "" {
			if r, ok := sourceRanks[effSource]; ok {
				sRank = r
			} else {
				sRank = unrankedSourcePriority - 1
			}
		}
		qRank, _ := streamQualityTier(inputQualityNames(target, src, donorByID)...)
		own := src.MergedFrom == "" && (src.ID == playlistID || src.ID == primaryID)
		ranked = append(ranked, rankedChannelInput{
			src:        src,
			sourceRank: sRank,
			quality:    qRank,
			ownPrimary: own,
			order:      i,
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.sourceRank != b.sourceRank {
			return a.sourceRank < b.sourceRank
		}
		if a.quality != b.quality {
			return a.quality < b.quality
		}
		if a.ownPrimary != b.ownPrimary {
			return a.ownPrimary
		}
		return a.order < b.order
	})

	deduped := make([]StreamSource, 0, min(len(ranked), MaxChannelInputs))
	seenIDs := make(map[string]bool, len(ranked))
	seenURLs := make(map[string]bool, len(ranked))
	var displacedOwn *StreamSource

	for _, item := range ranked {
		src := item.src
		if src.ID == "" {
			src.ID = stableID(target.ID, src.URL)
		}
		if seenIDs[src.ID] || seenURLs[src.URL] {
			continue
		}
		seenIDs[src.ID] = true
		seenURLs[src.URL] = true
		if len(deduped) < MaxChannelInputs {
			deduped = append(deduped, src)
		} else if item.ownPrimary && displacedOwn == nil {
			cp := src
			displacedOwn = &cp
		}
	}

	// Keep the target channel's own playlist input in the final slot if 16
	// higher-priority feeds displaced it, so removing merge rules later never
	// loses the target channel's native stream.
	if displacedOwn != nil && len(deduped) == MaxChannelInputs {
		deduped[MaxChannelInputs-1] = *displacedOwn
	}

	for i := range deduped {
		deduped[i].Priority = i
	}
	return deduped
}

func syncMergedDonorInput(donor Channel, donorSrc StreamSource, prev map[string]StreamSource) StreamSource {
	playlistID := stableID(donor.ID, "playlist")
	name := strings.TrimSpace(donor.Name)
	if donorSrc.Name != "" && donorSrc.Name != "Imported stream" && donorSrc.Name != "Primary stream" {
		name = strings.TrimSpace(donorSrc.Name)
	}
	if name == "" {
		name = "Imported stream"
	}

	if existing, ok := prev[donorSrc.ID]; ok {
		if donorSrc.ID == playlistID {
			// Reuse mergeImportedStreams so refreshed playlist URLs and headers
			// stay in sync while preserving owner enablement on the input.
			donorCopy := donor
			donorCopy.StreamURL = donorSrc.URL
			donorCopy.StreamSources = []StreamSource{donorSrc}
			synced := mergeImportedStreams(donorCopy, Channel{StreamSources: []StreamSource{existing}})
			existing = synced[0]
		} else {
			existing.URL = donorSrc.URL
			existing.Headers = CleanInputHeaders(donorSrc.Headers)
		}
		existing.Name = name
		existing.AuthSourceID = donor.SourceID
		existing.MergedFrom = donor.ID
		return existing
	}

	return StreamSource{
		ID:           donorSrc.ID,
		Name:         name,
		URL:          donorSrc.URL,
		Enabled:      true,
		AuthSourceID: donor.SourceID,
		MergedFrom:   donor.ID,
		Headers:      CleanInputHeaders(donorSrc.Headers),
	}
}

func sameChannelJSON(a, b Channel) bool {
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(ja, jb)
}

func needsMergeReconcile(cs []Channel) bool {
	for i := range cs {
		if hasInputMergeRules(cs[i]) || cs[i].MergedInto != "" || hasMergedStreamSources(cs[i]) {
			return true
		}
	}
	return false
}

// reconcileChannelInputMerges evaluates all target channels' InputMerge rules
// against the IPTV channel universe, mutates cs in place, and returns the
// indices of channels whose persisted JSON changed.
func reconcileChannelInputMerges(cs []Channel, srcs []Source, cached *inputMergeIndex) []int {
	if !needsMergeReconcile(cs) {
		return nil
	}
	before := make([][]byte, len(cs))
	for i := range cs {
		before[i], _ = json.Marshal(cs[i])
	}

	var names map[string]string
	var ranks map[string]int
	var compiledTargets map[string]compiledChannelMerge
	if cached != nil {
		names = cached.sourceNames
		ranks = cached.sourceRanks
		compiledTargets = cached.targets
	} else {
		idx := compileInputMergeIndex(0, cs, srcs)
		names = idx.sourceNames
		ranks = idx.sourceRanks
		compiledTargets = idx.targets
	}
	_ = names

	// Sort candidate evaluation deterministically by channel number then ID.
	donorOrder := make([]int, 0, len(cs))
	donorByID := make(map[string]Channel, len(cs))
	for i := range cs {
		if cs[i].Kind == "iptv" {
			donorOrder = append(donorOrder, i)
			donorByID[cs[i].ID] = cs[i]
		}
	}
	sort.SliceStable(donorOrder, func(i, j int) bool {
		a, b := cs[donorOrder[i]], cs[donorOrder[j]]
		if cmp := parseNumberKey(a.Number).compare(parseNumberKey(b.Number)); cmp != 0 {
			return cmp < 0
		}
		return a.ID < b.ID
	})

	// Clear stale MergedInto first; active target rules re-apply below.
	for i := range cs {
		cs[i].MergedInto = ""
	}

	// Process target channels in deterministic number/ID order.
	for _, ti := range donorOrder {
		target := &cs[ti]
		base := channelOwnBaseStreams(*target)

		entry, active := compiledTargets[target.ID]
		if !active || entry.compiled == nil {
			if hasMergedStreamSources(*target) {
				target.StreamSources = base
				hasEnabled := false
				for _, s := range target.StreamSources {
					if s.Enabled {
						hasEnabled = true
						break
					}
				}
				if target.Enabled && !hasEnabled && len(target.StreamSources) > 0 {
					target.StreamSources[0].Enabled = true
				}
				_ = normalizeStreamSources(target)
			}
			continue
		}

		prevByID := make(map[string]StreamSource, len(target.StreamSources))
		for _, src := range target.StreamSources {
			prevByID[src.ID] = src
		}

		candidates := make([]StreamSource, 0, len(base))
		for _, src := range base {
			if src.AutoFrom == "" {
				candidates = append(candidates, src)
			}
		}
		if len(candidates) == 0 && validStreamURL(strings.TrimSpace(target.StreamURL)) {
			id := stableID(target.ID, "primary")
			name := "Primary stream"
			if target.SourceID != "" {
				id = stableID(target.ID, "playlist")
				name = "Imported stream"
			}
			candidates = append(candidates, StreamSource{
				ID:      id,
				Name:    name,
				URL:     strings.TrimSpace(target.StreamURL),
				Enabled: true,
			})
		}
		for _, di := range donorOrder {
			if di == ti {
				continue
			}
			donor := &cs[di]
			// A channel that itself defines merge rules is a target channel, not
			// a duplicate feed to be hidden under another target.
			if hasInputMergeRules(*donor) {
				continue
			}
			if !entry.compiled.matchesChannel(*donor) {
				continue
			}
			dStreams := donorCandidateStreams(*donor)
			if len(dStreams) == 0 {
				continue
			}
			for _, ds := range dStreams {
				candidates = append(candidates, syncMergedDonorInput(*donor, ds, prevByID))
			}
			if entry.hideDuplicates && donor.MergedInto == "" {
				donor.MergedInto = target.ID
			}
		}

		ordered := orderAndBoundTargetInputs(*target, candidates, ranks, donorByID)
		if len(ordered) > 0 {
			hasEnabled := false
			for _, s := range ordered {
				if s.Enabled {
					hasEnabled = true
					break
				}
			}
			if target.Enabled && !hasEnabled {
				ordered[0].Enabled = true
			}
			target.StreamSources = ordered
			_ = normalizeStreamSources(target)
		}
	}

	var changed []int
	for i := range cs {
		after, err := json.Marshal(cs[i])
		if err != nil || !bytes.Equal(before[i], after) {
			changed = append(changed, i)
		}
	}
	return changed
}

// reconcileImportedMerges runs inside importSource's transaction before
// writeSourceChannels marshals stored. It updates stored in-place for src.ID
// channels and writes any affected channels on other sources inside tx.
func (s *Service) reconcileImportedMerges(ctx context.Context, tx *sql.Tx, src Source, stored []Channel, old []Channel) (bool, []string, error) {
	if !needsMergeReconcile(stored) && !needsMergeReconcile(old) {
		return false, nil, nil
	}
	srcs, err := s.sourcesTx(ctx, tx)
	if err != nil {
		return false, nil, err
	}
	foundSrc := false
	for i := range srcs {
		if srcs[i].ID == src.ID {
			srcs[i] = src
			foundSrc = true
			break
		}
	}
	if !foundSrc {
		srcs = append(srcs, src)
		sortSourcesByPriority(srcs)
	}

	universe := make([]Channel, 0, len(stored)+len(old))
	universe = append(universe, stored...)
	otherBase := len(stored)
	otherOld := make(map[string]Channel)
	for _, c := range old {
		if c.SourceID == src.ID {
			continue
		}
		otherOld[c.ID] = c
		universe = append(universe, c)
	}

	changedIdx := reconcileChannelInputMerges(universe, srcs, nil)
	copy(stored, universe[:otherBase])

	crossChanged := false
	var invalidated []string
	for _, idx := range changedIdx {
		if idx < otherBase {
			continue
		}
		crossChanged = true
		updated := universe[idx]
		b, err := json.Marshal(updated)
		if err != nil {
			return false, nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, updated.ID, b); err != nil {
			return false, nil, err
		}
		if prev, ok := otherOld[updated.ID]; ok && prev.StreamURL != updated.StreamURL {
			invalidated = append(invalidated, updated.ID)
		}
	}
	s.invalidateInputMergeIndex()
	return crossChanged, invalidated, nil
}

// reconcileInputMergesTx reconciles all stored channels' merge rules inside tx,
// returning the IDs of channels whose active StreamURL changed.
func (s *Service) reconcileInputMergesTx(ctx context.Context, tx *sql.Tx) (changed bool, invalidated []string, err error) {
	all, err := channels(ctx, tx)
	if err != nil {
		return false, nil, err
	}
	if !needsMergeReconcile(all) {
		return false, nil, nil
	}
	srcs, err := s.sourcesTx(ctx, tx)
	if err != nil {
		return false, nil, err
	}
	prevStreamURL := make(map[string]string, len(all))
	for _, c := range all {
		prevStreamURL[c.ID] = c.StreamURL
	}
	changedIdx := reconcileChannelInputMerges(all, srcs, nil)
	for _, idx := range changedIdx {
		updated := all[idx]
		b, err := json.Marshal(updated)
		if err != nil {
			return false, nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, updated.ID, b); err != nil {
			return false, nil, err
		}
		if prevStreamURL[updated.ID] != updated.StreamURL {
			invalidated = append(invalidated, updated.ID)
		}
	}
	s.invalidateInputMergeIndex()
	return len(changedIdx) > 0, invalidated, nil
}

// saveChannelWithMerges persists c and reconciles input merges across stored
// channels atomically when merge rules or merged state are involved.
func (s *Service) saveChannelWithMerges(ctx context.Context, c *Channel) ([]string, error) {
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	all, err := channels(ctx, tx)
	if err != nil {
		return nil, err
	}
	replaced := false
	needReconcile := hasInputMergeRules(*c) || c.MergedInto != "" || hasMergedStreamSources(*c)
	prevStreamURL := make(map[string]string, len(all)+1)
	for i := range all {
		prevStreamURL[all[i].ID] = all[i].StreamURL
		if all[i].ID == c.ID {
			if hasInputMergeRules(all[i]) || all[i].MergedInto != "" || hasMergedStreamSources(all[i]) {
				needReconcile = true
			}
			all[i] = *c
			replaced = true
		} else if hasInputMergeRules(all[i]) || all[i].MergedInto == c.ID {
			needReconcile = true
		}
	}
	if !replaced {
		all = append(all, *c)
	}

	if !needReconcile {
		b, err := json.Marshal(*c)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, c.ID, b); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return []string{c.ID}, nil
	}

	srcs, err := s.sourcesTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	changedIdx := reconcileChannelInputMerges(all, srcs, nil)
	writtenC := false
	invalidated := []string{c.ID}
	for _, idx := range changedIdx {
		updated := all[idx]
		if updated.ID == c.ID {
			*c = updated
			writtenC = true
		}
		b, err := json.Marshal(updated)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, updated.ID, b); err != nil {
			return nil, err
		}
		if updated.ID != c.ID && prevStreamURL[updated.ID] != updated.StreamURL {
			invalidated = append(invalidated, updated.ID)
		}
	}
	if !writtenC {
		for i := range all {
			if all[i].ID == c.ID {
				*c = all[i]
				break
			}
		}
		b, err := json.Marshal(*c)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, c.ID, b); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE livetv_channel_epoch SET generation=generation+1 WHERE id=1`); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.invalidateInputMergeIndex()
	return invalidated, nil
}

type SourceOrderRequest struct {
	IDs []string `json:"ids"`
}

// SetSourceOrder persists the owner's provider failover priority order and
// re-orders merged StreamSources on affected channels.
func (s *Service) SetSourceOrder(ctx context.Context, ids []string) ([]Source, error) {
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	srcs, err := s.sourcesTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Source, len(srcs))
	for _, src := range srcs {
		byID[src.ID] = src
	}

	ordered := make([]Source, 0, len(srcs))
	seen := make(map[string]bool, len(srcs))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		if src, ok := byID[id]; ok {
			seen[id] = true
			ordered = append(ordered, src)
		}
	}
	for _, src := range srcs {
		if !seen[src.ID] {
			ordered = append(ordered, src)
		}
	}

	anyChanged := false
	for i := range ordered {
		want := i + 1
		if ordered[i].Priority == want {
			continue
		}
		ordered[i].Priority = want
		b, err := s.encodeSource(ordered[i])
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE livetv_sources SET data=? WHERE id=?`, b, ordered[i].ID); err != nil {
			return nil, err
		}
		anyChanged = true
	}

	channelsChanged, invalidated, err := s.reconcileInputMergesTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	autoChanged, autoInv, err := s.reconcileAutoBackupsTx(ctx, tx, 0, nil)
	if err != nil {
		return nil, err
	}
	channelsChanged = channelsChanged || autoChanged
	invalidated = append(invalidated, autoInv...)
	if anyChanged || channelsChanged {
		if _, err := tx.ExecContext(ctx, `UPDATE livetv_channel_epoch SET generation=generation+1 WHERE id=1`); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if anyChanged || channelsChanged {
		s.invalidateInputMergeIndex()
		for _, cid := range invalidated {
			s.invalidate(cid, "")
		}
		s.bumpLineup()
	}
	return s.Sources(ctx)
}

type ChannelMergePreviewRequest struct {
	ChannelID string           `json:"channelId,omitempty"`
	Name      string           `json:"name,omitempty"`
	SourceID  string           `json:"sourceId,omitempty"`
	StreamURL string           `json:"streamUrl,omitempty"`
	Rules     []OwnerGroupRule `json:"rules"`
	Match     string           `json:"match,omitempty"`
}

type ChannelMergePreviewItem struct {
	ID             string    `json:"id"`
	ChannelID      string    `json:"channelId"`
	Name           string    `json:"name"`
	Group          string    `json:"group,omitempty"`
	GuideID        string    `json:"guideId,omitempty"`
	SourceID       string    `json:"sourceId,omitempty"`
	SourceName     string    `json:"sourceName,omitempty"`
	SourcePriority int       `json:"sourcePriority,omitempty"`
	Quality        string    `json:"quality,omitempty"`
	Primary        bool      `json:"primary,omitempty"`
	Included       bool      `json:"included"`
	State          string    `json:"state"`
	Failures       int       `json:"failures,omitempty"`
	LastPlayed     time.Time `json:"lastPlayed,omitempty"`
	RetryAfter     time.Time `json:"retryAfter,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
}

type ChannelMergePreviewResult struct {
	MatchCount int                       `json:"matchCount"`
	TotalFeeds int                       `json:"totalFeeds"`
	MaxInputs  int                       `json:"maxInputs"`
	Inputs     []ChannelMergePreviewItem `json:"inputs"`
}

// PreviewChannelMerge evaluates candidate merge rules against stored IPTV
// channels and returns matched feeds in resolved provider + quality failover
// order with their current health state.
func (s *Service) PreviewChannelMerge(ctx context.Context, req ChannelMergePreviewRequest) (ChannelMergePreviewResult, error) {
	rules, match, err := validateOwnerGroupRules(req.Rules, req.Match, true)
	if err != nil {
		return ChannelMergePreviewResult{}, err
	}
	res := ChannelMergePreviewResult{
		MaxInputs: MaxChannelInputs,
		Inputs:    []ChannelMergePreviewItem{},
	}
	if len(rules) == 0 {
		return res, nil
	}

	var rev int64
	_ = s.o.DB.QueryRowContext(ctx, `SELECT generation FROM livetv_channel_epoch WHERE id=1`).Scan(&rev)

	all, err := s.Channels(ctx)
	if err != nil {
		return ChannelMergePreviewResult{}, err
	}
	var names map[string]string
	var ranks map[string]int
	if cached := s.getCachedInputMergeIndex(rev); cached != nil && rev > 0 {
		names = cached.sourceNames
		ranks = cached.sourceRanks
	} else {
		srcs, err := s.sourcesTx(ctx, s.o.DB)
		if err != nil {
			return ChannelMergePreviewResult{}, err
		}
		idx := compileInputMergeIndex(rev, all, srcs)
		if rev > 0 {
			s.setCachedInputMergeIndex(idx)
		}
		names = idx.sourceNames
		ranks = idx.sourceRanks
	}

	cg, err := compileSingleOwnerGroup(OwnerGroup{Rules: rules, Match: match}, names)
	if err != nil {
		return ChannelMergePreviewResult{}, fmt.Errorf("invalid merge rules: %w", err)
	}

	var target Channel
	hasTarget := false
	donorByID := make(map[string]Channel, len(all))
	donors := make([]Channel, 0, len(all))
	for _, c := range all {
		if c.Kind != "iptv" {
			continue
		}
		donorByID[c.ID] = c
		if req.ChannelID != "" && c.ID == req.ChannelID {
			target = c
			hasTarget = true
			continue
		}
		donors = append(donors, c)
	}
	if !hasTarget {
		target = Channel{
			ID:        req.ChannelID,
			Name:      strings.TrimSpace(req.Name),
			Kind:      "iptv",
			SourceID:  strings.TrimSpace(req.SourceID),
			StreamURL: strings.TrimSpace(req.StreamURL),
			Enabled:   true,
		}
		if target.ID == "" {
			target.ID = "preview"
		}
	} else {
		if strings.TrimSpace(req.Name) != "" {
			target.Name = strings.TrimSpace(req.Name)
		}
		if strings.TrimSpace(req.SourceID) != "" {
			target.SourceID = strings.TrimSpace(req.SourceID)
		}
		if strings.TrimSpace(req.StreamURL) != "" && req.StreamURL != redactedSecret {
			target.StreamURL = strings.TrimSpace(req.StreamURL)
		}
	}

	sort.SliceStable(donors, func(i, j int) bool {
		if cmp := parseNumberKey(donors[i].Number).compare(parseNumberKey(donors[j].Number)); cmp != 0 {
			return cmp < 0
		}
		return donors[i].ID < donors[j].ID
	})

	prevByID := make(map[string]StreamSource, len(target.StreamSources))
	for _, src := range target.StreamSources {
		prevByID[src.ID] = src
	}

	type rawCandidate struct {
		src      StreamSource
		donor    Channel
		primary  bool
		qualName []string
	}
	var raw []rawCandidate
	for _, base := range channelOwnBaseStreams(target) {
		if base.AutoFrom != "" || !validStreamURL(strings.TrimSpace(base.URL)) {
			continue
		}
		raw = append(raw, rawCandidate{
			src:      base,
			donor:    target,
			primary:  true,
			qualName: inputQualityNames(target, base, donorByID),
		})
	}
	if len(raw) == 0 && validStreamURL(strings.TrimSpace(target.StreamURL)) {
		id := stableID(target.ID, "primary")
		name := "Primary stream"
		if target.SourceID != "" {
			id = stableID(target.ID, "playlist")
			name = "Imported stream"
		}
		base := StreamSource{
			ID:      id,
			Name:    name,
			URL:     strings.TrimSpace(target.StreamURL),
			Enabled: true,
		}
		raw = append(raw, rawCandidate{
			src:      base,
			donor:    target,
			primary:  true,
			qualName: inputQualityNames(target, base, donorByID),
		})
	}

	matchedChannels := 0
	for _, donor := range donors {
		if hasInputMergeRules(donor) || !cg.matchesChannel(donor) {
			continue
		}
		dStreams := donorCandidateStreams(donor)
		if len(dStreams) == 0 {
			continue
		}
		matchedChannels++
		for _, ds := range dStreams {
			synced := syncMergedDonorInput(donor, ds, prevByID)
			raw = append(raw, rawCandidate{
				src:      synced,
				donor:    donor,
				primary:  false,
				qualName: []string{synced.Name, donor.Name, donor.TvgName},
			})
		}
	}

	type sortablePreview struct {
		cand       rawCandidate
		sourceRank int
		quality    int
		qualLabel  string
		order      int
	}
	sorted := make([]sortablePreview, 0, len(raw))
	for i, c := range raw {
		effSrc := effectiveInputSourceID(target, c.src)
		sRank := unrankedSourcePriority
		if effSrc != "" {
			if r, ok := ranks[effSrc]; ok {
				sRank = r
			} else {
				sRank = unrankedSourcePriority - 1
			}
		}
		qRank, qLabel := streamQualityTier(c.qualName...)
		sorted = append(sorted, sortablePreview{
			cand:       c,
			sourceRank: sRank,
			quality:    qRank,
			qualLabel:  qLabel,
			order:      i,
		})
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.sourceRank != b.sourceRank {
			return a.sourceRank < b.sourceRank
		}
		if a.quality != b.quality {
			return a.quality < b.quality
		}
		if a.cand.primary != b.cand.primary {
			return a.cand.primary
		}
		return a.order < b.order
	})

	seenIDs := map[string]bool{}
	seenURLs := map[string]bool{}
	for _, sp := range sorted {
		src := sp.cand.src
		src.URL = strings.TrimSpace(src.URL)
		if src.ID == "" {
			src.ID = stableID(target.ID, src.URL)
		}
		if seenIDs[src.ID] || seenURLs[src.URL] {
			continue
		}
		seenIDs[src.ID] = true
		seenURLs[src.URL] = true

		effSrc := effectiveInputSourceID(target, src)
		healthChannelID := target.ID
		if !hasTarget && !sp.cand.primary {
			healthChannelID = sp.cand.donor.ID
		} else if hasTarget {
			if _, existed := prevByID[src.ID]; !existed && !sp.cand.primary {
				healthChannelID = sp.cand.donor.ID
			}
		}
		st := s.sourceStatus(healthChannelID, src)
		displayName := src.Name
		if sp.cand.primary && (displayName == "" || displayName == "Imported stream" || displayName == "Primary stream") && target.Name != "" {
			displayName = target.Name
		}
		prio := 0
		if effSrc != "" {
			prio = ranks[effSrc]
		}
		res.Inputs = append(res.Inputs, ChannelMergePreviewItem{
			ID:             src.ID,
			ChannelID:      sp.cand.donor.ID,
			Name:           displayName,
			Group:          sp.cand.donor.Group,
			GuideID:        sp.cand.donor.GuideID,
			SourceID:       effSrc,
			SourceName:     names[effSrc],
			SourcePriority: prio,
			Quality:        sp.qualLabel,
			Primary:        sp.cand.primary,
			Included:       len(res.Inputs) < MaxChannelInputs,
			State:          st.State,
			Failures:       st.Failures,
			LastPlayed:     st.LastPlayed,
			RetryAfter:     st.RetryAfter,
			LastError:      st.LastError,
		})
	}
	res.MatchCount = matchedChannels
	res.TotalFeeds = len(res.Inputs)
	return res, nil
}
