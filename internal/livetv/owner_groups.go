package livetv

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/go-chi/chi/v5"
)

const (
	maxOwnerGroupRules        = 50
	maxOwnerGroupRuleValueLen = 4096
	ownerGroupPreviewLimit    = 50
)

func init() {
	store.Register("livetv", 20, `CREATE TABLE livetv_owner_guides(id INTEGER PRIMARY KEY, data TEXT NOT NULL);`)

	sqliteV24 := `ALTER TABLE livetv_owner_guides RENAME TO livetv_owner_groups;
ALTER TABLE livetv_guide_visibility RENAME TO livetv_group_visibility;
CREATE TRIGGER livetv_owner_groups_epoch_insert AFTER INSERT ON livetv_owner_groups BEGIN UPDATE livetv_channel_epoch SET generation=generation+1 WHERE id=1; END;
CREATE TRIGGER livetv_owner_groups_epoch_update AFTER UPDATE ON livetv_owner_groups BEGIN UPDATE livetv_channel_epoch SET generation=generation+1 WHERE id=1; END;
CREATE TRIGGER livetv_owner_groups_epoch_delete AFTER DELETE ON livetv_owner_groups BEGIN UPDATE livetv_channel_epoch SET generation=generation+1 WHERE id=1; END;`
	postgresV24 := `ALTER TABLE livetv_owner_guides RENAME TO livetv_owner_groups;
ALTER TABLE livetv_guide_visibility RENAME TO livetv_group_visibility;
CREATE TRIGGER livetv_owner_groups_epoch AFTER INSERT OR UPDATE OR DELETE ON livetv_owner_groups FOR EACH STATEMENT EXECUTE FUNCTION livetv_channel_epoch_bump();`

	store.Register("livetv", 24, sqliteV24)
	store.RegisterDialect("livetv", 24, store.Postgres, postgresV24)

	store.Register("livetv", 25, "SELECT 1;")
	store.RegisterApply("livetv", 25, migrateOwnerGroupsV25)
}

func migrateOwnerGroupsV25(ctx context.Context, tx *sql.Tx) error {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM livetv_owner_groups WHERE id=1`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var gs []OwnerGroup
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &gs); err != nil {
			return fmt.Errorf("live TV owner groups: %w", err)
		}
	}
	if err := saveGroupVisibility(ctx, tx, gs); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM livetv_sources WHERE data LIKE '%"hubGroups"%'`)
	if err != nil {
		return err
	}
	type sourceUpdate struct {
		id   string
		data []byte
	}
	var updates []sourceUpdate
	for rows.Next() {
		var id string
		var srcRaw []byte
		if err := rows.Scan(&id, &srcRaw); err != nil {
			rows.Close()
			return err
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(srcRaw, &data); err != nil {
			rows.Close()
			return fmt.Errorf("live TV source %s: %w", id, err)
		}
		delete(data, "hubGroups")
		b, err := json.Marshal(data)
		if err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, sourceUpdate{id: id, data: b})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE livetv_sources SET data=? WHERE id=?`, string(u.data), u.id); err != nil {
			return err
		}
	}
	return nil
}

// OwnerGroupSource identifies a provider group without relying on its display name alone.
type OwnerGroupSource struct {
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
}

func (g *OwnerGroupSource) UnmarshalJSON(data []byte) error {
	var wire struct {
		SourceID   string `json:"sourceId"`
		Name       string `json:"name"`
		SourceName string `json:"sourceName,omitempty"`
		Count      int    `json:"count,omitempty"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return err
	}
	g.SourceID = wire.SourceID
	g.Name = wire.Name
	return nil
}

// OwnerGroupRule matches or excludes channels by one channel field.
//
// Field is one of "name", "group", "guideId", "number", or "source".
// Op is one of "contains", "is", "startsWith", "matches", "notContains", or "notMatches".
// Exclude operators ("notContains", "notMatches") always subtract from group membership.
type OwnerGroupRule struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// OwnerGroup is one of the owner's named channel groups ("Your groups").
//
// Membership is (provider groups ∪ explicit channels ∪ rule matches) − rule excludes
// (or (entire lineup) − rule excludes when All is true). Because provider-group and
// rule memberships are evaluated dynamically per lineup generation, channels a
// provider adds on later refreshes join the group and inherit its UnratedRating and
// Apps publication rules automatically.
//
// Apps maps app surface names (see Apps in publication.go) to whether members of
// this group are published there (true = shown, false = hidden; omitted = no rule).
// Explicit per-channel overrides (Channel.HiddenApps and Channel.ShownApps) take
// precedence over group rules; when multiple visible groups match a channel, the
// first group in owner order that sets Apps[app] wins.
type OwnerGroup struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Groups        []OwnerGroupSource `json:"groups"`
	Channels      []string           `json:"channels,omitempty"`
	Rules         []OwnerGroupRule   `json:"rules,omitempty"`
	Match         string             `json:"match,omitempty"`
	All           bool               `json:"all,omitempty"`
	Hidden        bool               `json:"hidden,omitempty"`
	UnratedRating string             `json:"unratedRating,omitempty"`
	Apps          map[string]bool    `json:"apps,omitempty"`
	HiddenApps    []string           `json:"hiddenApps,omitempty"`
	// ChannelCount is computed on owner API responses and ignored when saving.
	ChannelCount int `json:"channelCount,omitempty"`
}

// OwnerGroupChoice describes one imported provider group available for membership.
type OwnerGroupChoice struct {
	OwnerGroupSource
	SourceName string `json:"sourceName,omitempty"`
	Count      int    `json:"count"`
}

// OwnerGroupPreview is returned by POST /livetv/groups/preview for live editing.
type OwnerGroupPreview struct {
	Count    int              `json:"count"`
	Channels []ChannelSummary `json:"channels"`
}

type ownerFieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ownerValidationError struct {
	message string
	fields  []ownerFieldError
}

func (e *ownerValidationError) Error() string { return e.message }

func isOwnerGroupRuleField(field string) bool {
	switch field {
	case "name", "group", "guideId", "number", "source":
		return true
	default:
		return false
	}
}

func isOwnerGroupExcludeOp(op string) bool {
	return op == "notContains" || op == "notMatches"
}

func isOwnerGroupRuleOp(op string) bool {
	switch op {
	case "contains", "is", "startsWith", "matches", "notContains", "notMatches":
		return true
	default:
		return false
	}
}

// patternSetsFlags reports whether pattern already contains an unescaped RE2 flag
// directive such as (?i), (?-i), or (?im:...) outside character classes.
func patternSetsFlags(pattern string) bool {
	inClass := false
	classStart := 0
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '\\' {
			i++
			continue
		}
		if inClass {
			if ch == '[' && i+1 < len(pattern) && pattern[i+1] == ':' {
				if end := strings.Index(pattern[i+2:], ":]"); end >= 0 {
					i += end + 3
					continue
				}
			}
			if ch == ']' && i > classStart {
				inClass = false
			}
			continue
		}
		if ch == '[' {
			inClass = true
			classStart = i + 1
			if classStart < len(pattern) && pattern[classStart] == '^' {
				classStart++
			}
			continue
		}
		if ch == '(' && i+1 < len(pattern) && pattern[i+1] == '?' {
			j := i + 2
			hasFlag := false
			for j < len(pattern) {
				f := pattern[j]
				if f == 'i' || f == 'm' || f == 's' || f == 'U' {
					hasFlag = true
					j++
					continue
				}
				if f == '-' {
					j++
					continue
				}
				break
			}
			if hasFlag && j < len(pattern) && (pattern[j] == ')' || pattern[j] == ':') {
				return true
			}
		}
	}
	return false
}

func compileOwnerGroupRegexp(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > maxOwnerGroupRuleValueLen {
		return nil, fmt.Errorf("pattern exceeds %d bytes", maxOwnerGroupRuleValueLen)
	}
	expr := pattern
	if !patternSetsFlags(pattern) {
		expr = "(?i)" + pattern
	}
	return regexp.Compile(expr)
}

func validateOwnerGroupRules(rules []OwnerGroupRule, match string, allowEmptyValue bool) ([]OwnerGroupRule, string, error) {
	if len(rules) > maxOwnerGroupRules {
		return nil, "", fmt.Errorf("too many rules (at most %d)", maxOwnerGroupRules)
	}
	match = strings.TrimSpace(match)
	switch match {
	case "", "any", "all":
	default:
		return nil, "", fmt.Errorf("invalid rule match %q", match)
	}
	var out []OwnerGroupRule
	for ri, r := range rules {
		field := strings.TrimSpace(r.Field)
		op := strings.TrimSpace(r.Op)
		if !isOwnerGroupRuleField(field) {
			msg := fmt.Sprintf("invalid rule field %q", r.Field)
			return nil, "", &ownerValidationError{
				message: msg,
				fields:  []ownerFieldError{{Field: fmt.Sprintf("rules.%d.field", ri), Message: msg}},
			}
		}
		if !isOwnerGroupRuleOp(op) {
			msg := fmt.Sprintf("invalid rule operator %q", r.Op)
			return nil, "", &ownerValidationError{
				message: msg,
				fields:  []ownerFieldError{{Field: fmt.Sprintf("rules.%d.op", ri), Message: msg}},
			}
		}
		if len(r.Value) > maxOwnerGroupRuleValueLen {
			msg := fmt.Sprintf("rule pattern exceeds %d bytes", maxOwnerGroupRuleValueLen)
			return nil, "", &ownerValidationError{
				message: msg,
				fields:  []ownerFieldError{{Field: fmt.Sprintf("rules.%d.value", ri), Message: msg}},
			}
		}
		if r.Value == "" {
			if allowEmptyValue {
				continue
			}
			msg := "rule value is required"
			return nil, "", &ownerValidationError{
				message: msg,
				fields:  []ownerFieldError{{Field: fmt.Sprintf("rules.%d.value", ri), Message: msg}},
			}
		}
		if op == "matches" || op == "notMatches" {
			if _, err := compileOwnerGroupRegexp(r.Value); err != nil {
				return nil, "", &ownerValidationError{
					message: fmt.Sprintf("invalid pattern %q: %v", r.Value, err),
					fields:  []ownerFieldError{{Field: fmt.Sprintf("rules.%d.value", ri), Message: err.Error()}},
				}
			}
		}
		out = append(out, OwnerGroupRule{Field: field, Op: op, Value: r.Value})
	}
	normMatch := ""
	if match == "all" {
		normMatch = "all"
	} else if len(out) > 0 {
		normMatch = "any"
	}
	if len(out) == 0 {
		out = nil
	}
	return out, normMatch, nil
}

func anyGroupHasRules(gs []OwnerGroup) bool {
	for i := range gs {
		if len(gs[i].Rules) > 0 {
			return true
		}
	}
	return false
}

func groupUsesSourceField(g OwnerGroup) bool {
	for _, r := range g.Rules {
		if r.Field == "source" {
			return true
		}
	}
	return false
}

func anyRuleUsesSourceField(gs []OwnerGroup) bool {
	for i := range gs {
		if groupUsesSourceField(gs[i]) {
			return true
		}
	}
	return false
}

type channelRuleTarget struct {
	ID       string
	Name     string
	Group    string
	GuideID  string
	Number   string
	SourceID string
}

func targetFromChannel(c Channel) channelRuleTarget {
	return channelRuleTarget{
		ID:       c.ID,
		Name:     c.Name,
		Group:    c.Group,
		GuideID:  c.GuideID,
		Number:   c.Number,
		SourceID: c.SourceID,
	}
}

func targetFromSummary(c ChannelSummary) channelRuleTarget {
	return channelRuleTarget{
		ID:       c.ID,
		Name:     c.Name,
		Group:    c.Group,
		GuideID:  c.GuideID,
		Number:   c.Number,
		SourceID: c.SourceID,
	}
}

func targetsFromChannels(cs []Channel) []channelRuleTarget {
	out := make([]channelRuleTarget, 0, len(cs))
	for i := range cs {
		if cs[i].MergedInto != "" {
			continue
		}
		out = append(out, targetFromChannel(cs[i]))
	}
	return out
}

func targetsFromSummaries(cs []ChannelSummary) []channelRuleTarget {
	out := make([]channelRuleTarget, 0, len(cs))
	for i := range cs {
		if cs[i].MergedInto != "" {
			continue
		}
		out = append(out, targetFromSummary(cs[i]))
	}
	return out
}

type compiledOwnerGroupRule struct {
	field      string
	op         string
	valueLower string
	re         *regexp.Regexp
}

func (r *compiledOwnerGroupRule) matchesValue(val string) bool {
	switch r.op {
	case "contains", "notContains":
		return strings.Contains(strings.ToLower(val), r.valueLower)
	case "is":
		return strings.ToLower(val) == r.valueLower
	case "startsWith":
		return strings.HasPrefix(strings.ToLower(val), r.valueLower)
	case "matches", "notMatches":
		return r.re != nil && r.re.MatchString(val)
	default:
		return false
	}
}

func (r *compiledOwnerGroupRule) matchesTarget(t channelRuleTarget, sourceNames map[string]string) bool {
	switch r.field {
	case "name":
		return r.matchesValue(t.Name)
	case "group":
		return r.matchesValue(t.Group)
	case "guideId":
		return r.matchesValue(t.GuideID)
	case "number":
		return r.matchesValue(t.Number)
	case "source":
		name := sourceNames[t.SourceID]
		if name != "" && r.matchesValue(name) {
			return true
		}
		if name == "" || t.SourceID != name {
			return r.matchesValue(t.SourceID)
		}
		return false
	default:
		return false
	}
}

type compiledOwnerGroup struct {
	all         bool
	sources     map[OwnerGroupSource]struct{}
	channels    map[string]struct{}
	include     []compiledOwnerGroupRule
	exclude     []compiledOwnerGroupRule
	matchAll    bool
	sourceNames map[string]string
}

func compileSingleOwnerGroup(g OwnerGroup, sourceNames map[string]string) (*compiledOwnerGroup, error) {
	cg := &compiledOwnerGroup{
		all:         g.All,
		sources:     make(map[OwnerGroupSource]struct{}, len(g.Groups)),
		channels:    make(map[string]struct{}, len(g.Channels)),
		matchAll:    g.Match == "all",
		sourceNames: sourceNames,
	}
	for _, sg := range g.Groups {
		cg.sources[sg] = struct{}{}
	}
	for _, cid := range g.Channels {
		cg.channels[cid] = struct{}{}
	}
	for _, r := range g.Rules {
		if r.Value == "" {
			continue
		}
		cr := compiledOwnerGroupRule{
			field: strings.TrimSpace(r.Field),
			op:    strings.TrimSpace(r.Op),
		}
		if cr.op == "matches" || cr.op == "notMatches" {
			re, err := compileOwnerGroupRegexp(r.Value)
			if err != nil {
				return nil, err
			}
			cr.re = re
		} else {
			cr.valueLower = strings.ToLower(r.Value)
		}
		if isOwnerGroupExcludeOp(cr.op) {
			cg.exclude = append(cg.exclude, cr)
		} else {
			cg.include = append(cg.include, cr)
		}
	}
	return cg, nil
}

func compileSingleOwnerGroupBestEffort(g OwnerGroup, sourceNames map[string]string) *compiledOwnerGroup {
	cg, err := compileSingleOwnerGroup(g, sourceNames)
	if err == nil {
		return cg
	}
	fallback := g
	fallback.Rules = nil
	for _, r := range g.Rules {
		if r.Value == "" {
			continue
		}
		if r.Op == "matches" || r.Op == "notMatches" {
			if _, rerr := compileOwnerGroupRegexp(r.Value); rerr != nil {
				continue
			}
		}
		fallback.Rules = append(fallback.Rules, r)
	}
	cg, _ = compileSingleOwnerGroup(fallback, sourceNames)
	return cg
}

func (cg *compiledOwnerGroup) matchesTarget(t channelRuleTarget) bool {
	if cg == nil {
		return false
	}
	included := cg.all
	if !included && len(cg.sources) > 0 {
		_, included = cg.sources[OwnerGroupSource{SourceID: t.SourceID, Name: t.Group}]
	}
	if !included && len(cg.channels) > 0 {
		_, included = cg.channels[t.ID]
	}
	if !included && len(cg.include) > 0 {
		if cg.matchAll {
			included = true
			for i := range cg.include {
				if !cg.include[i].matchesTarget(t, cg.sourceNames) {
					included = false
					break
				}
			}
		} else {
			for i := range cg.include {
				if cg.include[i].matchesTarget(t, cg.sourceNames) {
					included = true
					break
				}
			}
		}
	}
	if !included {
		return false
	}
	for i := range cg.exclude {
		if cg.exclude[i].matchesTarget(t, cg.sourceNames) {
			return false
		}
	}
	return true
}

func (cg *compiledOwnerGroup) matchesChannel(c Channel) bool {
	return cg.matchesTarget(targetFromChannel(c))
}

func (cg *compiledOwnerGroup) matchesSummary(c ChannelSummary) bool {
	return cg.matchesTarget(targetFromSummary(c))
}

type ownerGroupAppRule struct {
	order int
	show  bool
	set   bool
}

type ownerGroupEntry struct {
	group        OwnerGroup
	sources      map[OwnerGroupSource]struct{}
	channels     map[string]struct{}
	compiled     *compiledOwnerGroup
	hasRules     bool
	precomputed  bool
	matchedChans map[string]struct{}
}

// ownerGroupIndex compiles saved OwnerGroup rules once per livetv_channel_epoch
// generation so channel publication, ratings, and group memberships run in O(1)
// without per-request channel scans or extra SQL reads.
type ownerGroupIndex struct {
	groups        []OwnerGroup
	hasRules      bool
	byID          map[string]ownerGroupEntry
	ratings       *groupRatingIndex
	appAll        map[string]ownerGroupAppRule
	appGroups     map[string]map[OwnerGroupSource]ownerGroupAppRule
	appChans      map[string]map[string]ownerGroupAppRule
	appHasHide    map[string]bool
	visibleAll    []frontend.LiveGroup
	visibleGroups map[OwnerGroupSource][]frontend.LiveGroup
	visibleChans  map[string][]frontend.LiveGroup
	visibleOrder  map[string]int
	hiddenAll     bool
	hiddenGroups  map[OwnerGroupSource]struct{}
	hiddenChans   map[string]struct{}
}

func compileOwnerGroupIndex(gs []OwnerGroup) *ownerGroupIndex {
	return compileOwnerGroupIndexWithTargets(gs, nil, nil)
}

func compileOwnerGroupIndexWithTargets(gs []OwnerGroup, targets []channelRuleTarget, sourceNames map[string]string) *ownerGroupIndex {
	if gs == nil {
		gs = []OwnerGroup{}
	}
	hasRules := anyGroupHasRules(gs)
	x := &ownerGroupIndex{
		groups:        gs,
		hasRules:      hasRules,
		byID:          make(map[string]ownerGroupEntry, len(gs)),
		ratings:       newGroupRatingIndex(),
		appAll:        make(map[string]ownerGroupAppRule, len(Apps)),
		appGroups:     make(map[string]map[OwnerGroupSource]ownerGroupAppRule, len(Apps)),
		appChans:      make(map[string]map[string]ownerGroupAppRule, len(Apps)),
		appHasHide:    make(map[string]bool, len(Apps)),
		visibleGroups: map[OwnerGroupSource][]frontend.LiveGroup{},
		visibleChans:  map[string][]frontend.LiveGroup{},
		visibleOrder:  make(map[string]int, len(gs)),
		hiddenGroups:  map[OwnerGroupSource]struct{}{},
		hiddenChans:   map[string]struct{}{},
	}
	for _, app := range Apps {
		x.appGroups[app] = map[OwnerGroupSource]ownerGroupAppRule{}
		x.appChans[app] = map[string]ownerGroupAppRule{}
	}
	precompute := targets != nil
	for i, g := range gs {
		x.visibleOrder[g.ID] = i
		compiled := compileSingleOwnerGroupBestEffort(g, sourceNames)
		entry := ownerGroupEntry{
			group:    g,
			sources:  compiled.sources,
			channels: compiled.channels,
			compiled: compiled,
			hasRules: len(g.Rules) > 0,
		}
		if entry.hasRules && precompute {
			entry.precomputed = true
			entry.matchedChans = map[string]struct{}{}
			for ti := range targets {
				if compiled.matchesTarget(targets[ti]) {
					entry.matchedChans[targets[ti].ID] = struct{}{}
				}
			}
			x.byID[g.ID] = entry
			if g.Hidden {
				for cid := range entry.matchedChans {
					x.hiddenChans[cid] = struct{}{}
				}
				continue
			}
			lg := frontend.LiveGroup{ID: g.ID, Name: g.Name}
			for cid := range entry.matchedChans {
				x.visibleChans[cid] = append(x.visibleChans[cid], lg)
			}
			for app, show := range g.Apps {
				if !slices.Contains(Apps, app) {
					continue
				}
				if !show {
					x.appHasHide[app] = true
				}
				rule := ownerGroupAppRule{order: i, show: show, set: true}
				cm := x.appChans[app]
				for cid := range entry.matchedChans {
					if cand, exists := cm[cid]; !exists || rule.order < cand.order {
						cm[cid] = rule
					}
				}
			}
			if g.UnratedRating != "" {
				rr := groupRatingRule{rating: g.UnratedRating, order: i}
				for cid := range entry.matchedChans {
					if cand, exists := x.ratings.channels[cid]; !exists || rr.order < cand.order {
						x.ratings.channels[cid] = rr
					}
				}
			}
			continue
		}

		x.byID[g.ID] = entry
		if g.Hidden {
			if !entry.hasRules {
				if g.All {
					x.hiddenAll = true
				}
				for _, sg := range g.Groups {
					x.hiddenGroups[sg] = struct{}{}
				}
				for _, cid := range g.Channels {
					x.hiddenChans[cid] = struct{}{}
				}
			}
			continue
		}
		if entry.hasRules {
			// Standalone (un-precomputed) mode: record app hides and dynamic rating fallback.
			for app, show := range g.Apps {
				if slices.Contains(Apps, app) && !show {
					x.appHasHide[app] = true
				}
			}
			if g.UnratedRating != "" {
				x.ratings.dynamic = append(x.ratings.dynamic, dynamicGroupRatingRule{
					compiled: compiled,
					rule:     groupRatingRule{rating: g.UnratedRating, order: i},
				})
			}
			continue
		}
		lg := frontend.LiveGroup{ID: g.ID, Name: g.Name}
		if g.All {
			x.visibleAll = append(x.visibleAll, lg)
		}
		for _, sg := range g.Groups {
			x.visibleGroups[sg] = append(x.visibleGroups[sg], lg)
		}
		for _, cid := range g.Channels {
			x.visibleChans[cid] = append(x.visibleChans[cid], lg)
		}
		for app, show := range g.Apps {
			if !slices.Contains(Apps, app) {
				continue
			}
			if !show {
				x.appHasHide[app] = true
			}
			rule := ownerGroupAppRule{order: i, show: show, set: true}
			if g.All && !x.appAll[app].set {
				x.appAll[app] = rule
			}
			gm := x.appGroups[app]
			for _, sg := range g.Groups {
				if _, exists := gm[sg]; !exists {
					gm[sg] = rule
				}
			}
			cm := x.appChans[app]
			for _, cid := range g.Channels {
				if cand, exists := cm[cid]; !exists || rule.order < cand.order {
					cm[cid] = rule
				}
			}
		}
		if g.UnratedRating != "" {
			rr := groupRatingRule{rating: g.UnratedRating, order: i}
			if g.All && x.ratings.all.rating == "" {
				x.ratings.all = rr
			}
			for _, cid := range g.Channels {
				if cand, exists := x.ratings.channels[cid]; !exists || rr.order < cand.order {
					x.ratings.channels[cid] = rr
				}
			}
			for _, sg := range g.Groups {
				if _, exists := x.ratings.groups[sg]; !exists {
					x.ratings.groups[sg] = rr
				}
			}
		}
	}
	return x
}

func decodeOwnerGroupsRaw(raw string) ([]OwnerGroup, error) {
	var gs []OwnerGroup
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &gs); err != nil {
			return nil, err
		}
	}
	return gs, nil
}

func decodeOwnerGroupIndex(raw string) (*ownerGroupIndex, error) {
	gs, err := decodeOwnerGroupsRaw(raw)
	if err != nil {
		return nil, err
	}
	return compileOwnerGroupIndex(gs), nil
}

func (s *Service) sourceNamesMap(ctx context.Context) (map[string]string, error) {
	rows, err := s.o.DB.QueryContext(ctx, `SELECT id, data FROM livetv_sources`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]string{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var doc struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &doc); err == nil && doc.Name != "" {
			names[id] = doc.Name
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func (s *Service) ruleTargetsForGeneration(ctx context.Context, rev int64) ([]channelRuleTarget, error) {
	s.lineupMu.Lock()
	if s.lineupIndex != nil && s.lineupRev == rev {
		summaries := s.lineupIndex
		s.lineupMu.Unlock()
		return targetsFromSummaries(summaries), nil
	}
	s.lineupMu.Unlock()

	s.chanSnap.mu.Lock()
	if s.chanSnap.list != nil && s.chanSnap.generation == rev {
		list := s.chanSnap.list
		s.chanSnap.mu.Unlock()
		return targetsFromChannels(list), nil
	}
	s.chanSnap.mu.Unlock()

	rows, err := s.o.DB.QueryContext(ctx, `SELECT data FROM livetv_channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]channelRuleTarget, 0, 256)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var doc struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Group      string `json:"group"`
			GuideID    string `json:"guideId"`
			Number     string `json:"number"`
			SourceID   string `json:"sourceId"`
			MergedInto string `json:"mergedInto"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		if doc.MergedInto != "" {
			continue
		}
		targets = append(targets, channelRuleTarget{
			ID:       doc.ID,
			Name:     doc.Name,
			Group:    doc.Group,
			GuideID:  doc.GuideID,
			Number:   doc.Number,
			SourceID: doc.SourceID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *Service) loadOwnerGroupIndexWithTargets(ctx context.Context, rev int64, raw string, targets []channelRuleTarget) (*ownerGroupIndex, error) {
	gs, err := decodeOwnerGroupsRaw(raw)
	if err != nil {
		return nil, err
	}
	if !anyGroupHasRules(gs) {
		return compileOwnerGroupIndex(gs), nil
	}
	if targets == nil {
		targets, err = s.ruleTargetsForGeneration(ctx, rev)
		if err != nil {
			return nil, err
		}
	}
	var sourceNames map[string]string
	if anyRuleUsesSourceField(gs) {
		sourceNames, err = s.sourceNamesMap(ctx)
		if err != nil {
			return nil, err
		}
	}
	return compileOwnerGroupIndexWithTargets(gs, targets, sourceNames), nil
}

func (s *Service) loadOwnerGroupIndex(ctx context.Context, rev int64, raw string) (*ownerGroupIndex, error) {
	return s.loadOwnerGroupIndexWithTargets(ctx, rev, raw, nil)
}

func (x *ownerGroupIndex) groupAppRule(sourceID, group, channelID, app string) (bool, bool) {
	if x == nil {
		return true, false
	}
	best := x.appAll[app]
	if gm := x.appGroups[app]; gm != nil {
		if cand := gm[OwnerGroupSource{SourceID: sourceID, Name: group}]; cand.set && (!best.set || cand.order < best.order) {
			best = cand
		}
	}
	if cm := x.appChans[app]; cm != nil {
		if cand := cm[channelID]; cand.set && (!best.set || cand.order < best.order) {
			best = cand
		}
	}
	if !best.set {
		return true, false
	}
	return best.show, true
}

// groupAppRuleForChannel evaluates group app rules directly against c's fields so
// unsaved or freshly-edited channels reconcile accurately before database write.
func (x *ownerGroupIndex) groupAppRuleForChannel(c Channel, app string) (bool, bool) {
	if x == nil {
		return true, false
	}
	if !x.hasRules {
		return x.groupAppRule(c.SourceID, c.Group, c.ID, app)
	}
	target := targetFromChannel(c)
	for _, g := range x.groups {
		if g.Hidden {
			continue
		}
		show, exists := g.Apps[app]
		if !exists {
			continue
		}
		entry := x.byID[g.ID]
		if entry.compiled != nil && entry.compiled.matchesTarget(target) {
			return show, true
		}
	}
	return true, false
}

func (x *ownerGroupIndex) publishedToFor(app, sourceID, group, channelID string, hiddenApps, shownApps []string) bool {
	if app == "" {
		return true
	}
	if slices.Contains(hiddenApps, app) {
		return false
	}
	if slices.Contains(shownApps, app) {
		return true
	}
	if show, set := x.groupAppRule(sourceID, group, channelID, app); set {
		return show
	}
	return true
}

func (x *ownerGroupIndex) publishedTo(app string, c Channel) bool {
	return x.publishedToFor(app, c.SourceID, c.Group, c.ID, c.HiddenApps, c.ShownApps)
}

func (x *ownerGroupIndex) effectiveHiddenAppsFor(sourceID, group, channelID string, hiddenApps, shownApps []string) []string {
	var out []string
	for _, app := range Apps {
		if !x.publishedToFor(app, sourceID, group, channelID, hiddenApps, shownApps) {
			out = append(out, app)
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}

func (x *ownerGroupIndex) effectiveHiddenApps(c Channel) []string {
	return x.effectiveHiddenAppsFor(c.SourceID, c.Group, c.ID, c.HiddenApps, c.ShownApps)
}

func (x *ownerGroupIndex) hasAppHides(app string) bool {
	return x != nil && x.appHasHide[app]
}

func (x *ownerGroupIndex) inHiddenGroup(sourceID, group, channelID string) bool {
	if x == nil {
		return false
	}
	if x.hiddenAll {
		return true
	}
	if _, ok := x.hiddenGroups[OwnerGroupSource{SourceID: sourceID, Name: group}]; ok {
		return true
	}
	_, ok := x.hiddenChans[channelID]
	return ok
}

func (x *ownerGroupIndex) memberships(sourceID, group, channelID string) []frontend.LiveGroup {
	if x == nil || len(x.groups) == 0 {
		return []frontend.LiveGroup{}
	}
	byGroup := x.visibleGroups[OwnerGroupSource{SourceID: sourceID, Name: group}]
	byChan := x.visibleChans[channelID]
	if len(x.visibleAll) == 0 && len(byGroup) == 0 && len(byChan) == 0 {
		return []frontend.LiveGroup{}
	}
	out := make([]frontend.LiveGroup, 0, len(x.visibleAll)+len(byGroup)+len(byChan))
	add := func(lg frontend.LiveGroup) {
		if !slices.ContainsFunc(out, func(v frontend.LiveGroup) bool { return v.ID == lg.ID }) {
			out = append(out, lg)
		}
	}
	for _, lg := range x.visibleAll {
		add(lg)
	}
	for _, lg := range byGroup {
		add(lg)
	}
	for _, lg := range byChan {
		add(lg)
	}
	if len(out) > 1 {
		sort.SliceStable(out, func(i, j int) bool {
			return x.visibleOrder[out[i].ID] < x.visibleOrder[out[j].ID]
		})
	}
	return out
}

func (x *ownerGroupIndex) inGroup(groupID, sourceID, group, channelID string) bool {
	if x == nil {
		return false
	}
	if groupID == "none" || groupID == "__ungrouped__" {
		return len(x.memberships(sourceID, group, channelID)) == 0
	}
	entry, ok := x.byID[groupID]
	if !ok {
		return false
	}
	if entry.precomputed {
		_, ok := entry.matchedChans[channelID]
		return ok
	}
	if entry.group.All {
		return true
	}
	if _, ok := entry.sources[OwnerGroupSource{SourceID: sourceID, Name: group}]; ok {
		return true
	}
	_, ok = entry.channels[channelID]
	return ok
}

type ownerGroupIndexContextKey struct{}

func contextWithOwnerGroupIndex(ctx context.Context, idx *ownerGroupIndex) context.Context {
	if idx == nil {
		return ctx
	}
	return context.WithValue(ctx, ownerGroupIndexContextKey{}, idx)
}

func (s *Service) withOwnerGroupIndex(ctx context.Context) context.Context {
	if idx, ok := ctx.Value(ownerGroupIndexContextKey{}).(*ownerGroupIndex); ok && idx != nil {
		return ctx
	}
	idx, err := s.cachedOwnerGroupIndex(ctx)
	if err != nil || idx == nil {
		return ctx
	}
	return contextWithOwnerGroupIndex(ctx, idx)
}

func (s *Service) getOwnerGroupCacheState() (int64, *ownerGroupIndex) {
	s.ownerGroupsMu.Lock()
	defer s.ownerGroupsMu.Unlock()
	if s.ownerGroupsIndex == nil {
		return -1, nil
	}
	return s.ownerGroupsIndexRev, s.ownerGroupsIndex
}

func (s *Service) setOwnerGroupCacheState(rev int64, idx *ownerGroupIndex) {
	s.ownerGroupsMu.Lock()
	// Never go backwards: a slower reader that compiled an older generation
	// must not replace a newer index, or a channel that just joined a group
	// would briefly escape that group's app hides and rating.
	if s.ownerGroupsIndex != nil && rev < s.ownerGroupsIndexRev {
		s.ownerGroupsMu.Unlock()
		return
	}
	s.ownerGroupsIndexRev = rev
	s.ownerGroupsIndex = idx
	s.ownerGroupsMu.Unlock()
}

func (s *Service) cachedOwnerGroupIndex(ctx context.Context) (*ownerGroupIndex, error) {
	if idx, ok := ctx.Value(ownerGroupIndexContextKey{}).(*ownerGroupIndex); ok && idx != nil {
		return idx, nil
	}
	prevRev, prevIdx := s.getOwnerGroupCacheState()
	var rev int64
	var raw string
	err := s.o.DB.QueryRowContext(ctx,
		`SELECT generation, CASE WHEN generation=? THEN '' ELSE COALESCE((SELECT data FROM livetv_owner_groups WHERE id=1),'[]') END FROM livetv_channel_epoch WHERE id=1`,
		prevRev,
	).Scan(&rev, &raw)
	if err != nil {
		return nil, err
	}
	if prevIdx != nil && rev == prevRev {
		return prevIdx, nil
	}
	idx, err := s.loadOwnerGroupIndex(ctx, rev, raw)
	if err != nil {
		return nil, err
	}
	s.setOwnerGroupCacheState(rev, idx)
	return idx, nil
}

// WithOwnerGroupIndex attaches the compiled Your-groups rule index to ctx so
// repeated ChannelGroups and publishedTo lookups on a request run in memory.
func (s *Service) WithOwnerGroupIndex(ctx context.Context) context.Context {
	return s.withOwnerGroupIndex(ctx)
}

// ChannelGroups returns the visible Your-groups memberships for one channel in owner order.
func (s *Service) ChannelGroups(ctx context.Context, sourceID, group, channelID string) ([]frontend.LiveGroup, error) {
	idx, err := s.cachedOwnerGroupIndex(ctx)
	if err != nil {
		return nil, err
	}
	return idx.memberships(sourceID, group, channelID), nil
}

func (s *Service) providerGroupChoices(ctx context.Context) ([]OwnerGroupChoice, error) {
	var rev int64
	if err := s.o.DB.QueryRowContext(ctx, `SELECT generation FROM livetv_channel_epoch WHERE id=1`).Scan(&rev); err != nil {
		return nil, err
	}
	s.ownerGroupsMu.Lock()
	if s.ownerGroupChoices != nil && s.ownerGroupChoicesRev == rev {
		cached := s.ownerGroupChoices
		s.ownerGroupsMu.Unlock()
		return cached, nil
	}
	s.ownerGroupsMu.Unlock()

	cs, err := s.ChannelSummaries(ctx)
	if err != nil {
		return nil, err
	}
	out := []OwnerGroupChoice{}
	at := map[OwnerGroupSource]int{}
	for _, c := range cs {
		key := OwnerGroupSource{SourceID: c.SourceID, Name: c.Group}
		if i, ok := at[key]; ok {
			out[i].Count++
		} else {
			at[key] = len(out)
			out = append(out, OwnerGroupChoice{OwnerGroupSource: key, Count: 1})
		}
	}
	sources, err := s.Sources(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, src := range sources {
		names[src.ID] = src.Name
	}
	for i := range out {
		out[i].SourceName = names[out[i].SourceID]
	}
	s.ownerGroupsMu.Lock()
	s.ownerGroupChoices = out
	s.ownerGroupChoicesRev = rev
	s.ownerGroupsMu.Unlock()
	return out, nil
}

// OwnerGroups returns the owner's saved groups in owner order. When no groups
// have been saved yet, it returns an empty slice ([]OwnerGroup{}, nil).
func (s *Service) OwnerGroups(ctx context.Context) ([]OwnerGroup, error) {
	var raw []byte
	err := s.o.DB.QueryRowContext(ctx, `SELECT data FROM livetv_owner_groups WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []OwnerGroup{}, nil
	}
	if err != nil {
		return nil, err
	}
	gs := []OwnerGroup{}
	if err := json.Unmarshal(raw, &gs); err != nil {
		return nil, err
	}
	if gs == nil {
		gs = []OwnerGroup{}
	}
	return gs, nil
}

func validateAndNormalizeOwnerGroups(gs []OwnerGroup) ([]OwnerGroup, error) {
	if len(gs) > 200 {
		return nil, fmt.Errorf("too many groups")
	}
	if gs == nil {
		gs = []OwnerGroup{}
	}
	out := slices.Clone(gs)
	seen := map[string]bool{}
	names := map[string]bool{}
	for i := range out {
		g := &out[i]
		g.ChannelCount = 0
		g.Name = strings.TrimSpace(g.Name)
		if g.ID == "" {
			g.ID = randomID()
		}
		if g.Groups == nil {
			g.Groups = []OwnerGroupSource{}
		}
		if len(g.ID) > 100 || strings.ContainsAny(g.ID, "/\\?# \t\r\n") || seen[g.ID] || names[strings.ToLower(g.Name)] || g.Name == "" || len(g.Name) > 150 || len(g.Groups) > 2000 || len(g.Channels) > 20000 {
			return nil, fmt.Errorf("invalid group")
		}
		rules, match, err := validateOwnerGroupRules(g.Rules, g.Match, false)
		if err != nil {
			return nil, err
		}
		g.Rules = rules
		g.Match = match
		legacyHidden, err := normalizeHiddenApps(g.HiddenApps)
		if err != nil {
			return nil, err
		}
		apps, err := normalizeGroupApps(g.Apps)
		if err != nil {
			return nil, err
		}
		if len(legacyHidden) > 0 {
			if apps == nil {
				apps = make(map[string]bool, len(legacyHidden))
			}
			for _, app := range legacyHidden {
				if _, exists := apps[app]; !exists {
					apps[app] = false
				}
			}
		}
		g.Apps = apps
		g.HiddenApps = nil
		g.Channels = uniqueIDs(g.Channels)
		seen[g.ID] = true
		names[strings.ToLower(g.Name)] = true
		slices.SortFunc(g.Groups, func(a, b OwnerGroupSource) int {
			if n := strings.Compare(a.SourceID, b.SourceID); n != 0 {
				return n
			}
			return strings.Compare(a.Name, b.Name)
		})
		g.Groups = slices.Compact(g.Groups)
	}
	return out, nil
}

func (s *Service) SaveOwnerGroups(ctx context.Context, gs []OwnerGroup) error {
	normalized, err := validateAndNormalizeOwnerGroups(gs)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO livetv_owner_groups(id,data) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, raw); err != nil {
		return err
	}
	if err = saveGroupVisibility(ctx, tx, normalized); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		s.bumpLineup()
	}
	return err
}

func populateOwnerGroupCounts(gs []OwnerGroup, cs []ChannelSummary) []OwnerGroup {
	return populateOwnerGroupCountsWithSources(gs, cs, nil)
}

func populateOwnerGroupCountsWithSources(gs []OwnerGroup, cs []ChannelSummary, sourceNames map[string]string) []OwnerGroup {
	out := make([]OwnerGroup, len(gs))
	for i, g := range gs {
		copyGroup := g
		if copyGroup.Groups == nil {
			copyGroup.Groups = []OwnerGroupSource{}
		}
		compiled := compileSingleOwnerGroupBestEffort(g, sourceNames)
		count := 0
		if g.All && len(compiled.exclude) == 0 {
			count = len(cs)
		} else {
			for ci := range cs {
				if compiled.matchesSummary(cs[ci]) {
					count++
				}
			}
		}
		copyGroup.ChannelCount = count
		out[i] = copyGroup
	}
	return out
}

func (s *Service) ownerGroupsWithCounts(ctx context.Context) ([]OwnerGroup, []ChannelSummary, error) {
	gs, err := s.OwnerGroups(ctx)
	if err != nil {
		return nil, nil, err
	}
	cs, err := s.ChannelSummaries(ctx)
	if err != nil {
		return nil, nil, err
	}
	var sourceNames map[string]string
	if anyRuleUsesSourceField(gs) {
		sourceNames, err = s.sourceNamesMap(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	return populateOwnerGroupCountsWithSources(gs, cs, sourceNames), cs, nil
}

// PreviewOwnerGroup evaluates a draft OwnerGroup against the current lineup
// without saving, returning the total matching count and first ~50 channels.
func (s *Service) PreviewOwnerGroup(ctx context.Context, draft OwnerGroup) (OwnerGroupPreview, error) {
	if len(draft.Groups) > 2000 || len(draft.Channels) > 20000 {
		return OwnerGroupPreview{}, fmt.Errorf("invalid group")
	}
	rules, match, err := validateOwnerGroupRules(draft.Rules, draft.Match, true)
	if err != nil {
		return OwnerGroupPreview{}, err
	}
	previewGroup := OwnerGroup{
		ID:       "preview",
		Groups:   draft.Groups,
		Channels: uniqueIDs(draft.Channels),
		Rules:    rules,
		Match:    match,
		All:      draft.All,
	}
	cs, err := s.ChannelSummaries(ctx)
	if err != nil {
		return OwnerGroupPreview{}, err
	}
	var sourceNames map[string]string
	if groupUsesSourceField(previewGroup) {
		sourceNames, err = s.sourceNamesMap(ctx)
		if err != nil {
			return OwnerGroupPreview{}, err
		}
	}
	compiled, err := compileSingleOwnerGroup(previewGroup, sourceNames)
	if err != nil {
		return OwnerGroupPreview{}, err
	}
	matched := make([]ChannelSummary, 0, min(len(cs), ownerGroupPreviewLimit))
	count := 0
	for i := range cs {
		if compiled.matchesSummary(cs[i]) {
			count++
			if len(matched) < ownerGroupPreviewLimit {
				matched = append(matched, cs[i])
			}
		}
	}
	return OwnerGroupPreview{Count: count, Channels: matched}, nil
}

func (s *Service) ownerGroupPreviewRoute(w http.ResponseWriter, r *http.Request) {
	var draft OwnerGroup
	if !ownerBody(w, r, &draft) {
		return
	}
	res, err := s.PreviewOwnerGroup(r.Context(), draft)
	ownerResult(w, res, err)
}

type ownerGroupPatch struct {
	Name          *string             `json:"name,omitempty"`
	Groups        *[]OwnerGroupSource `json:"groups,omitempty"`
	Channels      *[]string           `json:"channels,omitempty"`
	Rules         *[]OwnerGroupRule   `json:"rules,omitempty"`
	Match         *string             `json:"match,omitempty"`
	All           *bool               `json:"all,omitempty"`
	Hidden        *bool               `json:"hidden,omitempty"`
	UnratedRating *string             `json:"unratedRating,omitempty"`
	Apps          *map[string]bool    `json:"apps,omitempty"`
	HiddenApps    *[]string           `json:"hiddenApps,omitempty"`
	Position      *int                `json:"position,omitempty"`
	ChannelCount  *int                `json:"channelCount,omitempty"`
}

func (s *Service) ownerGroupsRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodGet:
		gs, _, err := s.ownerGroupsWithCounts(ctx)
		if err != nil {
			ownerResult(w, nil, err)
			return
		}
		if r.URL.Query().Get("include") == "sources" {
			choices, err := s.providerGroupChoices(ctx)
			if err != nil {
				ownerResult(w, nil, err)
				return
			}
			ownerResult(w, map[string]any{
				"groups":  gs,
				"sources": choices,
				"apps":    Apps,
			}, nil)
			return
		}
		ownerResult(w, gs, nil)

	case http.MethodPut:
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
			return
		}
		trimmed := bytes.TrimSpace(raw)
		var gs []OwnerGroup
		if len(trimmed) > 0 && trimmed[0] == '{' {
			var wrapped struct {
				Groups []OwnerGroup `json:"groups"`
			}
			dec := json.NewDecoder(bytes.NewReader(trimmed))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&wrapped); err != nil {
				ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
				return
			}
			gs = wrapped.Groups
		} else {
			dec := json.NewDecoder(bytes.NewReader(trimmed))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&gs); err != nil {
				ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
				return
			}
		}
		if err := s.SaveOwnerGroups(ctx, gs); err != nil {
			ownerResult(w, nil, err)
			return
		}
		saved, _, err := s.ownerGroupsWithCounts(ctx)
		ownerResult(w, saved, err)

	case http.MethodPost:
		var g OwnerGroup
		if !ownerBody(w, r, &g) {
			return
		}
		if g.ID == "" {
			g.ID = randomID()
		}
		gs, err := s.OwnerGroups(ctx)
		if err != nil {
			ownerResult(w, nil, err)
			return
		}
		gs = append(gs, g)
		if err := s.SaveOwnerGroups(ctx, gs); err != nil {
			ownerResult(w, nil, err)
			return
		}
		saved, _, err := s.ownerGroupsWithCounts(ctx)
		if err != nil {
			ownerResult(w, nil, err)
			return
		}
		var created OwnerGroup
		for _, item := range saved {
			if item.ID == g.ID {
				created = item
				break
			}
		}
		ownerResult(w, map[string]any{"group": created, "groups": saved}, nil)
	}
}

func (s *Service) ownerGroupsOrderRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
		return
	}
	trimmed := bytes.TrimSpace(raw)
	var ids []string
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &ids); err != nil {
			ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
			return
		}
	} else {
		var wrapped struct {
			IDs []string `json:"ids"`
		}
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&wrapped); err != nil {
			ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
			return
		}
		ids = wrapped.IDs
	}
	existing, err := s.OwnerGroups(ctx)
	if err != nil {
		ownerResult(w, nil, err)
		return
	}
	if len(ids) != len(existing) {
		ownerResult(w, nil, fmt.Errorf("group order must name all %d groups", len(existing)))
		return
	}
	byID := make(map[string]OwnerGroup, len(existing))
	for _, g := range existing {
		byID[g.ID] = g
	}
	reordered := make([]OwnerGroup, 0, len(existing))
	seen := make(map[string]bool, len(existing))
	for _, id := range ids {
		g, ok := byID[id]
		if !ok || seen[id] {
			ownerResult(w, nil, fmt.Errorf("invalid group order id %q", id))
			return
		}
		seen[id] = true
		reordered = append(reordered, g)
	}
	if err := s.SaveOwnerGroups(ctx, reordered); err != nil {
		ownerResult(w, nil, err)
		return
	}
	saved, _, err := s.ownerGroupsWithCounts(ctx)
	ownerResult(w, map[string]any{"groups": saved}, err)
}

func (s *Service) ownerGroupItemRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	gs, err := s.OwnerGroups(ctx)
	if err != nil {
		ownerResult(w, nil, err)
		return
	}
	idx := slices.IndexFunc(gs, func(g OwnerGroup) bool { return g.ID == id })
	if idx < 0 {
		ownerResult(w, nil, core.ErrNotFound)
		return
	}
	switch r.Method {
	case http.MethodDelete:
		gs = slices.Delete(gs, idx, idx+1)
		if err := s.SaveOwnerGroups(ctx, gs); err != nil {
			ownerResult(w, nil, err)
			return
		}
		saved, _, err := s.ownerGroupsWithCounts(ctx)
		ownerResult(w, map[string]any{"ok": true, "groups": saved}, err)

	case http.MethodPut:
		var g OwnerGroup
		if !ownerBody(w, r, &g) {
			return
		}
		g.ID = id
		gs[idx] = g
		if err := s.SaveOwnerGroups(ctx, gs); err != nil {
			ownerResult(w, nil, err)
			return
		}
		saved, _, err := s.ownerGroupsWithCounts(ctx)
		if err != nil {
			ownerResult(w, nil, err)
			return
		}
		ownerResult(w, map[string]any{"group": saved[idx], "groups": saved}, nil)

	case http.MethodPatch:
		var patch ownerGroupPatch
		if !ownerBody(w, r, &patch) {
			return
		}
		g := gs[idx]
		if patch.Name != nil {
			g.Name = *patch.Name
		}
		if patch.Groups != nil {
			g.Groups = *patch.Groups
		}
		if patch.Channels != nil {
			g.Channels = *patch.Channels
		}
		if patch.Rules != nil {
			g.Rules = *patch.Rules
		}
		if patch.Match != nil {
			g.Match = strings.TrimSpace(*patch.Match)
		}
		if patch.All != nil {
			g.All = *patch.All
		}
		if patch.Hidden != nil {
			g.Hidden = *patch.Hidden
		}
		if patch.UnratedRating != nil {
			g.UnratedRating = strings.TrimSpace(*patch.UnratedRating)
		}
		if patch.Apps != nil {
			g.Apps = *patch.Apps
		}
		if patch.HiddenApps != nil {
			g.HiddenApps = *patch.HiddenApps
		}
		gs[idx] = g
		targetIdx := idx
		if patch.Position != nil {
			pos := max(0, min(*patch.Position, len(gs)-1))
			if pos != idx {
				gs = slices.Delete(gs, idx, idx+1)
				gs = slices.Insert(gs, pos, g)
				targetIdx = pos
			}
		}
		if err := s.SaveOwnerGroups(ctx, gs); err != nil {
			ownerResult(w, nil, err)
			return
		}
		saved, _, err := s.ownerGroupsWithCounts(ctx)
		if err != nil {
			ownerResult(w, nil, err)
			return
		}
		ownerResult(w, map[string]any{"group": saved[targetIdx], "groups": saved}, nil)
	}
}

func (s *Service) ownerGroupAppsRoute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	gs, err := s.OwnerGroups(ctx)
	if err != nil {
		ownerResult(w, nil, err)
		return
	}
	idx := slices.IndexFunc(gs, func(g OwnerGroup) bool { return g.ID == id })
	if idx < 0 {
		ownerResult(w, nil, core.ErrNotFound)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
		return
	}
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
		return
	}
	var appEntries map[string]*bool
	clearOverrides := false
	if _, wrapped := rawMap["apps"]; wrapped {
		var body struct {
			Apps                  map[string]*bool `json:"apps"`
			ClearChannelOverrides bool             `json:"clearChannelOverrides,omitempty"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
			return
		}
		appEntries = body.Apps
		clearOverrides = body.ClearChannelOverrides
	} else {
		if err := json.Unmarshal(raw, &appEntries); err != nil {
			ownerResult(w, nil, fmt.Errorf("invalid request body: %w", err))
			return
		}
	}

	var next map[string]bool
	if r.Method == http.MethodPut {
		next = make(map[string]bool, len(appEntries))
	} else {
		next = make(map[string]bool, len(gs[idx].Apps)+len(appEntries))
		for k, v := range gs[idx].Apps {
			next[k] = v
		}
	}
	var touchedApps []string
	for app, val := range appEntries {
		if !slices.Contains(Apps, app) {
			ownerResult(w, nil, fmt.Errorf("unknown Live TV app %q", app))
			return
		}
		touchedApps = append(touchedApps, app)
		if val == nil {
			delete(next, app)
		} else {
			next[app] = *val
		}
	}
	normalized, err := normalizeGroupApps(next)
	if err != nil {
		ownerResult(w, nil, err)
		return
	}
	gs[idx].Apps = normalized
	if err := s.SaveOwnerGroups(ctx, gs); err != nil {
		ownerResult(w, nil, err)
		return
	}
	if clearOverrides && len(touchedApps) > 0 {
		if err := s.clearGroupChannelAppOverrides(ctx, gs[idx], touchedApps); err != nil {
			ownerResult(w, nil, err)
			return
		}
	}
	saved, _, err := s.ownerGroupsWithCounts(ctx)
	if err != nil {
		ownerResult(w, nil, err)
		return
	}
	ownerResult(w, map[string]any{"group": saved[idx], "groups": saved}, nil)
}

func (s *Service) clearGroupChannelAppOverrides(ctx context.Context, g OwnerGroup, apps []string) error {
	idx, err := s.cachedOwnerGroupIndex(ctx)
	if err != nil {
		return err
	}
	all, err := s.allChannels(ctx)
	if err != nil {
		return err
	}
	var ids []string
	for _, c := range all {
		if !idx.inGroup(g.ID, c.SourceID, c.Group, c.ID) {
			continue
		}
		hasOverride := false
		for _, app := range apps {
			if slices.Contains(c.HiddenApps, app) || slices.Contains(c.ShownApps, app) {
				hasOverride = true
				break
			}
		}
		if hasOverride {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, err = s.UpdateChannels(ctx, ChannelBatch{IDs: ids, ClearApps: apps})
	return err
}

func (h liveTVHost) Groups(ctx context.Context, u *core.User) ([]frontend.LiveGroup, error) {
	cs, err := h.Channels(ctx, u)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, c := range cs {
		for _, g := range c.Groups {
			present[g.ID] = true
		}
	}
	gs, err := h.s.OwnerGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := []frontend.LiveGroup{}
	for _, g := range gs {
		if !g.Hidden && present[g.ID] {
			out = append(out, frontend.LiveGroup{ID: g.ID, Name: g.Name})
		}
	}
	return out, nil
}

func (h liveTVHost) GroupChannels(ctx context.Context, u *core.User, id string) ([]frontend.LiveTVChannel, error) {
	idx, err := h.s.cachedOwnerGroupIndex(ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := idx.byID[id]
	if !ok || entry.group.Hidden {
		return nil, core.ErrNotFound
	}
	cs, err := h.s.AllowedChannels(ctx, u)
	if err != nil {
		return nil, err
	}
	out := []frontend.LiveTVChannel{}
	for _, c := range cs {
		if idx.inGroup(id, c.SourceID, c.Group, c.ID) {
			out = append(out, ToFrontendChannel(c))
		}
	}
	return out, nil
}
