package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/clientreg"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
)

const qualityRulesUnavailable = "Quality rules are not available on this server."

// QualityRulesSnapshot is the current quality mapping configuration plus the
// referenced profiles, libraries and viewers needed to understand and edit rules.
type QualityRulesSnapshot struct {
	Default           string
	DefaultAway       string
	DefaultLimits     config.QualityLimits
	DefaultAwayLimits config.QualityLimits
	Profiles          []string
	Rules             []config.QualityRule
	Libraries         []QualityLibraryRef
	Viewers           []QualityViewerRef
	Devices           *QualityDevicesRef
}

// QualityLibraryRef is one library that a quality rule or explain call can target.
type QualityLibraryRef struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	MediaType      string `json:"mediaType,omitempty"`
	ChosenByPlugin bool   `json:"chosenByPlugin,omitempty"`
}

// QualityViewerRef is one viewer that a quality rule or explain call can target.
type QualityViewerRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// QualityDeviceAppRef is one registry app id accepted by quality rules and explain.
type QualityDeviceAppRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

// QualityDeviceClientRef is one seen device id accepted by quality rules and explain.
type QualityDeviceClientRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group,omitempty"`
	Type     string `json:"type,omitempty"`
	App      string `json:"app,omitempty"`
	Viewer   string `json:"viewer,omitempty"`
	LastSeen string `json:"lastSeen,omitempty"`
}

// QualityDevicesRef holds the opaque device ids returned by GET /api/quality/devices.
type QualityDevicesRef struct {
	Groups  []clientreg.ClassInfo      `json:"groups"`
	Types   []clientreg.DeviceTypeInfo `json:"types"`
	Apps    []QualityDeviceAppRef      `json:"apps"`
	Clients []QualityDeviceClientRef   `json:"clients"`
}

// QualityClientIdentity is a custom client identity for quality_explain.
type QualityClientIdentity struct {
	Product  string
	Platform string
	Device   string
	Protocol core.Protocol
}

// QualityExplainRequest is the canonical input to explain which profile and
// limits apply to a play.
type QualityExplainRequest struct {
	ViewerID  int64
	ClientID  string
	Client    *QualityClientIdentity
	Remote    bool
	TitleKey  core.RatingKey
	LibraryID int64
	Kind      string
}

// QualityRulesService is the narrow rules management surface exposed to
// built-in MCP and Ask Ether tools.
type QualityRulesService interface {
	QualityRules(ctx context.Context, includeDevices bool) (QualityRulesSnapshot, error)
	ExplainQuality(ctx context.Context, req QualityExplainRequest) (resolver.QualityAnswer, error)
	// ValidateQuality checks a candidate mutation through the canonical config
	// validator (admin.ValidateConfig + app-level library/viewer checks) without
	// saving anything.
	ValidateQuality(ctx context.Context, mutate func(*config.Quality) error) (config.Quality, error)
	// UpdateQuality applies mutate and persists through the canonical validated
	// config save path.
	UpdateQuality(ctx context.Context, mutate func(*config.Quality) error) (config.Quality, error)
}

func qualityRulesTools(d Deps) []tools.Tool {
	return []tools.Tool{
		qualityRulesListTool(d),
		qualityExplainTool(d),
		qualityRuleAddTool(d),
		qualityRuleEditTool(d),
		qualityRuleMoveTool(d),
		qualityDefaultsSetTool(d),
	}
}

func qualityRulesListTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_rules_list", Title: "Quality rules", ReadOnly: true,
		Description: "List the ordered quality rules (with stable rule ids and 1-based positions), the At home and Away default profiles, available profile names, libraries and viewers. Rules are checked from top to bottom: the first matching rule with a profile picks it, and every matching rule's limits accumulate (strictest wins). Set includeDevices=true to also list valid device groups, types, apps and seen client ids for rule conditions or quality_explain.",
		InputSchema: schema(map[string]any{
			"includeDevices": pBool("Include valid device groups, types, apps and seen client ids"),
		}),
		Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
			if d.QualityRules == nil {
				return tools.Result{}, tools.Userf(qualityRulesUnavailable)
			}
			includeDevices, err := boolArg(in, "includeDevices", false)
			if err != nil {
				return tools.Result{}, err
			}
			snap, err := d.QualityRules.QualityRules(ctx, includeDevices)
			if err != nil {
				return tools.Result{}, err
			}
			rules := make([]map[string]any, 0, len(snap.Rules))
			for i, r := range snap.Rules {
				rules = append(rules, qualityRuleMap(r, i+1))
			}
			profiles := snap.Profiles
			if profiles == nil {
				profiles = []string{}
			}
			libraries := snap.Libraries
			if libraries == nil {
				libraries = []QualityLibraryRef{}
			}
			viewers := snap.Viewers
			if viewers == nil {
				viewers = []QualityViewerRef{}
			}
			data := map[string]any{
				"rules":             rules,
				"default":           snap.Default,
				"defaultAway":       snap.DefaultAway,
				"defaultLimits":     snap.DefaultLimits,
				"defaultAwayLimits": snap.DefaultAwayLimits,
				"profiles":          profiles,
				"libraries":         libraries,
				"viewers":           viewers,
			}
			if includeDevices && snap.Devices != nil {
				data["devices"] = snap.Devices
			}
			return tools.Result{
				Data:  data,
				Links: []tools.Link{{Label: "Open quality rules", Route: "/releases/rules"}},
			}, nil
		},
	})
}

func qualityExplainTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_explain", Title: "Explain quality", ReadOnly: true,
		Description: "Explain which quality profile, resolution ceiling, HDR and Dolby Vision support, bitrate cap, stereo audio and deciding rules apply to a play. Uses the same engine as playback and Check quality. Pass viewerId, clientId or client, remote, and optionally titleKey, libraryId or kind. Call quality_rules_list (with includeDevices=true for device ids) to see valid ids.",
		InputSchema: schema(map[string]any{
			"viewerId": pInt("Viewer id from quality_rules_list; omit for any viewer", 1, 1<<31-1),
			"clientId": pString("Device or app id: \"any\", a seen client id, a built-in client id (for example plex-apple-tv), or \"<field>:<id>\" (for example type:Apple TV, app:plex:Plex for iOS)", 160),
			"client": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"description":          "Custom client identity when not using clientId",
				"properties": map[string]any{
					"product":  pString("Client product name, for example Plex for iOS", 128),
					"platform": pString("Client platform, for example iOS, tvOS or Android", 128),
					"device":   pString("Optional device model, for example Apple TV or Pixel 8", 128),
					"protocol": pString("Optional protocol, for example plex, jellyfin or emby", 32),
				},
			},
			"remote":    pBool("True when streaming away from home (default false)"),
			"titleKey":  pString("Optional movie or episode title key from catalog_search or playback_history", 32),
			"libraryId": pInt("Optional library id from quality_rules_list (when titleKey is not given)", 1, 1<<31-1),
			"kind":      pEnum("Title kind when titleKey is not given and the library does not imply one; defaults to movie (the answer reports kindAssumed)", config.KindMovie, config.KindEpisode),
		}),
		Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
			if d.QualityRules == nil {
				return tools.Result{}, tools.Userf(qualityRulesUnavailable)
			}
			viewerID, err := intArg(in, "viewerId", 0, 0, 1<<31-1)
			if err != nil {
				return tools.Result{}, err
			}
			if _, hasViewer := in["viewerId"]; hasViewer && in["viewerId"] != nil && viewerID <= 0 {
				return tools.Result{}, tools.Userf("viewerId must be a positive viewer id")
			}
			clientID, err := optStr(in, "clientId", 160)
			if err != nil {
				return tools.Result{}, err
			}
			clientIdent, err := clientIdentityArg(in)
			if err != nil {
				return tools.Result{}, err
			}
			if clientID != "" && clientIdent != nil {
				return tools.Result{}, tools.Userf("give either clientId or client, not both")
			}
			remote, err := boolArg(in, "remote", false)
			if err != nil {
				return tools.Result{}, err
			}
			var titleKey core.RatingKey
			if rawKey, ok := in["titleKey"]; ok && rawKey != nil {
				if s, isStr := rawKey.(string); !isStr || strings.TrimSpace(s) != "" {
					titleKey, err = keyArg(in, "titleKey")
					if err != nil {
						return tools.Result{}, err
					}
				}
			}
			libraryID, err := intArg(in, "libraryId", 0, 0, 1<<31-1)
			if err != nil {
				return tools.Result{}, err
			}
			if _, hasLib := in["libraryId"]; hasLib && in["libraryId"] != nil && libraryID <= 0 {
				return tools.Result{}, tools.Userf("libraryId must be a positive library id")
			}
			if titleKey != 0 && libraryID > 0 {
				return tools.Result{}, tools.Userf("give either titleKey or libraryId, not both")
			}
			kind, err := optStr(in, "kind", 16)
			if err != nil {
				return tools.Result{}, err
			}
			if kind != "" && kind != config.KindMovie && kind != config.KindEpisode {
				return tools.Result{}, tools.Userf("kind must be movie or episode")
			}
			ans, err := d.QualityRules.ExplainQuality(ctx, QualityExplainRequest{
				ViewerID:  int64(viewerID),
				ClientID:  clientID,
				Client:    clientIdent,
				Remote:    remote,
				TitleKey:  titleKey,
				LibraryID: int64(libraryID),
				Kind:      kind,
			})
			if err != nil {
				return tools.Result{}, err
			}
			raw, err := json.Marshal(ans)
			if err != nil {
				return tools.Result{}, err
			}
			var data map[string]any
			if err := json.Unmarshal(raw, &data); err != nil {
				return tools.Result{}, err
			}
			links := []tools.Link{{Label: "Open Check quality", Route: "/releases/check"}}
			if ans.ProfileRuleID != "" {
				links = append(links, tools.Link{
					Label: "Open deciding rule",
					Route: "/releases/rules?rule=" + url.QueryEscape(ans.ProfileRuleID),
				})
			}
			return tools.Result{Data: data, Links: links}, nil
		},
	})
}

func qualityRuleAddTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_rule_add", Title: "Add a quality rule",
		Description: "Propose adding a new ordered quality rule. Every condition is optional; leaving one out means any. Each rule must pick a profile, set at least one limit (maxHeight, maxBitrateKbps or stereoAudio), or both. Rules are checked from top to bottom: the first matching rule with a profile picks it, while every matching rule's limits accumulate (strictest wins). The server assigns a stable rule id on approval. Call quality_rules_list first to see current rules, profile names, libraries, viewers and devices. Changes require owner approval.",
		InputSchema: schema(map[string]any{
			"name":            pString("Optional short name for the rule", 80),
			"position":        pInt("Optional 1-based position in the rule list (1 = highest priority; omit to append at the end)", 1, 200),
			"libraries":       pIDList("Library ids from quality_rules_list", 100),
			"exceptLibraries": pBool("Invert libraries (all libraries except these); requires libraries"),
			"viewers":         pIDList("Viewer ids from quality_rules_list", 100),
			"exceptViewers":   pBool("Invert viewers (everyone except these); requires viewers"),
			"devices":         pDeviceMatchList("Device conditions; any match fits"),
			"exceptDevices":   pBool("Invert devices (any device except these); requires devices"),
			"where":           pEnum("Where the play happens; omit for anywhere", config.WhereHome, config.WhereAway),
			"kind":            pEnum("Title kind; omit for both movies and episodes", config.KindMovie, config.KindEpisode),
			"profile":         pString("Profile name from quality_rules_list; omit for a limits-only rule", 80),
			"maxHeight":       pInt("Resolution ceiling: 480, 720, 1080 or 2160 (0 = no limit; only lowers)", 0, 2160),
			"maxBitrateKbps":  pInt("Bitrate cap in kbps (0 = no cap; for example 8000 for 8 Mbps; only lowers)", 0, 1000000),
			"stereoAudio":     pBool("Force stereo audio (downmix surround)"),
		}),
		Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
			if d.QualityRules == nil {
				return tools.Result{}, tools.Userf(qualityRulesUnavailable)
			}
			var hasPos bool
			var position int
			if rawPos, ok := in["position"]; ok && rawPos != nil {
				pos, err := intArg(in, "position", 0, 1, 200)
				if err != nil {
					return tools.Result{}, err
				}
				hasPos, position = true, pos
			}
			rule, err := parseNewQualityRule(in)
			if err != nil {
				return tools.Result{}, err
			}
			snap, err := d.QualityRules.QualityRules(ctx, false)
			if err != nil {
				return tools.Result{}, err
			}
			if rule.Profile != "" {
				rule.Profile = canonicalProfileName(snap.Profiles, rule.Profile)
			}
			targetPos := len(snap.Rules) + 1
			if hasPos {
				if position > len(snap.Rules)+1 {
					return tools.Result{}, tools.Userf("position must be between 1 and %d", len(snap.Rules)+1)
				}
				targetPos = position
			}
			insertRule := func(q *config.Quality) (config.QualityRule, int, error) {
				idx := len(q.Rules)
				if hasPos {
					if position > len(q.Rules)+1 {
						return config.QualityRule{}, 0, tools.Userf("position must be between 1 and %d", len(q.Rules)+1)
					}
					idx = position - 1
				}
				r := cloneQualityRule(rule)
				r.ID = ""
				if r.Profile != "" {
					r.Profile = canonicalProfileNameFromProfiles(q.Profiles, r.Profile)
				}
				next := make([]config.QualityRule, 0, len(q.Rules)+1)
				next = append(next, q.Rules[:idx]...)
				next = append(next, r)
				next = append(next, q.Rules[idx:]...)
				q.Rules = next
				config.AssignRuleIDs(q)
				return q.Rules[idx], idx + 1, nil
			}
			if _, err := d.QualityRules.ValidateQuality(ctx, func(q *config.Quality) error {
				_, _, err := insertRule(q)
				return err
			}); err != nil {
				return tools.Result{}, err
			}

			storedInput := buildAddRuleInput(rule, hasPos, position)
			summary := describeQualityRule(rule, snap)
			details := []tools.ProposalDetail{
				{Label: "Position", Value: fmt.Sprintf("%d of %d", targetPos, len(snap.Rules)+1)},
			}
			if rule.Name != "" {
				details = append(details, tools.ProposalDetail{Label: "Name", Value: rule.Name})
			}
			details = append(details,
				tools.ProposalDetail{Label: "When", Value: describeRuleConditions(rule, snap)},
				tools.ProposalDetail{Label: "Then", Value: describeRuleEffect(rule)},
			)
			return propose(
				"Add a quality rule",
				fmt.Sprintf("Add quality rule at position %d: %s.", targetPos, summary),
				details,
				storedInput,
				func(applyCtx context.Context) (tools.Result, error) {
					var created config.QualityRule
					var finalPos int
					if _, err := d.QualityRules.UpdateQuality(applyCtx, func(q *config.Quality) error {
						var err error
						created, finalPos, err = insertRule(q)
						return err
					}); err != nil {
						return tools.Result{}, err
					}
					return tools.Result{
						Data: map[string]any{
							"added":    true,
							"position": finalPos,
							"rule":     qualityRuleMap(created, finalPos),
						},
						Links: []tools.Link{{
							Label: "Open quality rule",
							Route: "/releases/rules?rule=" + url.QueryEscape(created.ID),
						}},
					}, nil
				},
			), nil
		},
	})
}

func qualityRuleEditTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_rule_edit", Title: "Edit a quality rule",
		Description: "Propose editing an existing quality rule by its stable ruleId from quality_rules_list. Only fields included in the call are changed; omitted fields keep their current values. Pass \"\" for profile, where or kind to clear them, 0 for maxHeight or maxBitrateKbps to clear them, false for stereoAudio, or [] to clear libraries, viewers or devices. The updated rule is validated before proposing. Changes require owner approval.",
		InputSchema: schema(map[string]any{
			"ruleId":          pString("Stable rule id from quality_rules_list (for example r_...)", 64),
			"name":            pString("Rule name; pass \"\" to clear", 80),
			"libraries":       pIDList("Replace library ids; pass [] to match any library", 100),
			"exceptLibraries": pBool("Invert libraries (all libraries except these)"),
			"viewers":         pIDList("Replace viewer ids; pass [] to match anyone", 100),
			"exceptViewers":   pBool("Invert viewers (everyone except these)"),
			"devices":         pDeviceMatchList("Replace device conditions; pass [] to match any device"),
			"exceptDevices":   pBool("Invert devices (any device except these)"),
			"where":           pEnum("Where the play happens; pass \"\" for anywhere", "", config.WhereHome, config.WhereAway),
			"kind":            pEnum("Title kind; pass \"\" for both movies and episodes", "", config.KindMovie, config.KindEpisode),
			"profile":         pString("Profile name; pass \"\" for limits-only (no profile change)", 80),
			"maxHeight":       pInt("Resolution ceiling: 0 (no limit), 480, 720, 1080 or 2160", 0, 2160),
			"maxBitrateKbps":  pInt("Bitrate cap in kbps (0 = no cap)", 0, 1000000),
			"stereoAudio":     pBool("Force stereo audio"),
		}, "ruleId"),
		Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
			if d.QualityRules == nil {
				return tools.Result{}, tools.Userf(qualityRulesUnavailable)
			}
			ruleID, err := reqStr(in, "ruleId", 64)
			if err != nil {
				return tools.Result{}, err
			}
			patch, storedInput, err := parseQualityRulePatch(in, ruleID)
			if err != nil {
				return tools.Result{}, err
			}
			snap, err := d.QualityRules.QualityRules(ctx, false)
			if err != nil {
				return tools.Result{}, err
			}
			idx := findQualityRuleIndex(snap.Rules, ruleID)
			if idx < 0 {
				return tools.Result{}, tools.Userf("no quality rule with id %q; call quality_rules_list to see rule ids", ruleID)
			}
			existing := snap.Rules[idx]
			if patch.profile != nil && *patch.profile != "" {
				canon := canonicalProfileName(snap.Profiles, *patch.profile)
				patch.profile = &canon
				storedInput["profile"] = canon
			}
			preview := applyQualityRulePatch(existing, patch)
			applyPatch := func(q *config.Quality) (config.QualityRule, int, error) {
				i := findQualityRuleIndex(q.Rules, ruleID)
				if i < 0 {
					return config.QualityRule{}, 0, tools.Userf("no quality rule with id %q; call quality_rules_list to see rule ids", ruleID)
				}
				p := patch
				if p.profile != nil && *p.profile != "" {
					canon := canonicalProfileNameFromProfiles(q.Profiles, *p.profile)
					p.profile = &canon
				}
				updated := applyQualityRulePatch(q.Rules[i], p)
				q.Rules[i] = updated
				return updated, i + 1, nil
			}
			if _, err := d.QualityRules.ValidateQuality(ctx, func(q *config.Quality) error {
				_, _, err := applyPatch(q)
				return err
			}); err != nil {
				return tools.Result{}, err
			}
			if reflect.DeepEqual(existing, preview) {
				return tools.Result{
					Data: map[string]any{
						"changed":  false,
						"position": idx + 1,
						"rule":     qualityRuleMap(existing, idx+1),
					},
					Links: []tools.Link{{
						Label: "Open quality rule",
						Route: "/releases/rules?rule=" + url.QueryEscape(existing.ID),
					}},
				}, nil
			}
			return propose(
				"Edit a quality rule",
				fmt.Sprintf("Edit quality rule %s.", ruleDisplayLabel(existing, idx+1)),
				[]tools.ProposalDetail{
					{Label: "Rule ID", Value: existing.ID},
					{Label: "Before", Value: describeQualityRule(existing, snap)},
					{Label: "After", Value: describeQualityRule(preview, snap)},
				},
				storedInput,
				func(applyCtx context.Context) (tools.Result, error) {
					var updated config.QualityRule
					var pos int
					if _, err := d.QualityRules.UpdateQuality(applyCtx, func(q *config.Quality) error {
						var err error
						updated, pos, err = applyPatch(q)
						return err
					}); err != nil {
						return tools.Result{}, err
					}
					return tools.Result{
						Data: map[string]any{
							"updated":  true,
							"position": pos,
							"rule":     qualityRuleMap(updated, pos),
						},
						Links: []tools.Link{{
							Label: "Open quality rule",
							Route: "/releases/rules?rule=" + url.QueryEscape(updated.ID),
						}},
					}, nil
				},
			), nil
		},
	})
}

func qualityRuleMoveTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_rule_move", Title: "Move a quality rule",
		Description: "Propose moving an existing quality rule (by its stable ruleId from quality_rules_list) to a new 1-based position in the ordered rule list. Position 1 is checked first (highest priority). Changes require owner approval.",
		InputSchema: schema(map[string]any{
			"ruleId":   pString("Stable rule id from quality_rules_list (for example r_...)", 64),
			"position": pInt("Target 1-based position in the rule list (1 = highest priority)", 1, 200),
		}, "ruleId", "position"),
		Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
			if d.QualityRules == nil {
				return tools.Result{}, tools.Userf(qualityRulesUnavailable)
			}
			ruleID, err := reqStr(in, "ruleId", 64)
			if err != nil {
				return tools.Result{}, err
			}
			if _, ok := in["position"]; !ok || in["position"] == nil {
				return tools.Result{}, tools.Userf("position is required")
			}
			position, err := intArg(in, "position", 0, 1, 200)
			if err != nil {
				return tools.Result{}, err
			}
			snap, err := d.QualityRules.QualityRules(ctx, false)
			if err != nil {
				return tools.Result{}, err
			}
			fromIdx := findQualityRuleIndex(snap.Rules, ruleID)
			if fromIdx < 0 {
				return tools.Result{}, tools.Userf("no quality rule with id %q; call quality_rules_list to see rule ids", ruleID)
			}
			if position > len(snap.Rules) {
				return tools.Result{}, tools.Userf("position must be between 1 and %d", len(snap.Rules))
			}
			existing := snap.Rules[fromIdx]
			if fromIdx+1 == position {
				return tools.Result{
					Data: map[string]any{
						"changed":  false,
						"ruleId":   existing.ID,
						"position": position,
						"rule":     qualityRuleMap(existing, position),
					},
					Links: []tools.Link{{
						Label: "Open quality rule",
						Route: "/releases/rules?rule=" + url.QueryEscape(existing.ID),
					}},
				}, nil
			}
			moveRule := func(q *config.Quality) (config.QualityRule, int, int, error) {
				cur := findQualityRuleIndex(q.Rules, ruleID)
				if cur < 0 {
					return config.QualityRule{}, 0, 0, tools.Userf("no quality rule with id %q; call quality_rules_list to see rule ids", ruleID)
				}
				if position > len(q.Rules) {
					return config.QualityRule{}, 0, 0, tools.Userf("position must be between 1 and %d", len(q.Rules))
				}
				target := position - 1
				moved := q.Rules[cur]
				rest := make([]config.QualityRule, 0, len(q.Rules)-1)
				rest = append(rest, q.Rules[:cur]...)
				rest = append(rest, q.Rules[cur+1:]...)
				next := make([]config.QualityRule, 0, len(q.Rules))
				next = append(next, rest[:target]...)
				next = append(next, moved)
				next = append(next, rest[target:]...)
				q.Rules = next
				return moved, cur + 1, position, nil
			}
			if _, err := d.QualityRules.ValidateQuality(ctx, func(q *config.Quality) error {
				_, _, _, err := moveRule(q)
				return err
			}); err != nil {
				return tools.Result{}, err
			}
			return propose(
				"Move a quality rule",
				fmt.Sprintf("Move quality rule %s from position %d to position %d.", ruleDisplayLabel(existing, fromIdx+1), fromIdx+1, position),
				[]tools.ProposalDetail{
					{Label: "Rule", Value: describeQualityRule(existing, snap)},
					{Label: "Rule ID", Value: existing.ID},
					{Label: "Before", Value: fmt.Sprintf("Position %d of %d", fromIdx+1, len(snap.Rules))},
					{Label: "After", Value: fmt.Sprintf("Position %d of %d", position, len(snap.Rules))},
				},
				map[string]any{"ruleId": ruleID, "position": position},
				func(applyCtx context.Context) (tools.Result, error) {
					var moved config.QualityRule
					var fromPos, toPos int
					if _, err := d.QualityRules.UpdateQuality(applyCtx, func(q *config.Quality) error {
						var err error
						moved, fromPos, toPos, err = moveRule(q)
						return err
					}); err != nil {
						return tools.Result{}, err
					}
					return tools.Result{
						Data: map[string]any{
							"moved":        true,
							"ruleId":       moved.ID,
							"fromPosition": fromPos,
							"position":     toPos,
							"rule":         qualityRuleMap(moved, toPos),
						},
						Links: []tools.Link{{
							Label: "Open quality rule",
							Route: "/releases/rules?rule=" + url.QueryEscape(moved.ID),
						}},
					}, nil
				},
			), nil
		},
	})
}

func qualityDefaultsSetTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_defaults_set", Title: "Change default quality profiles and limits",
		Description: "Propose changing the fallback profiles and limits used when no rule picks a profile. Pass an empty defaultAway to inherit both the home profile and home limits. Read quality_rules_list first. Changes require owner approval.",
		InputSchema: schema(map[string]any{
			"default":           pString("Home fallback profile name", 80),
			"defaultAway":       pString("Away fallback profile name, or empty to inherit the home row", 80),
			"defaultLimits":     qualityLimitsSchema("Home fallback limits"),
			"defaultAwayLimits": qualityLimitsSchema("Away fallback limits"),
		}),
		Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
			if d.QualityRules == nil {
				return tools.Result{}, tools.Userf(qualityRulesUnavailable)
			}
			snap, err := d.QualityRules.QualityRules(ctx, false)
			if err != nil {
				return tools.Result{}, err
			}
			type fallbackEdit struct {
				profile *string
				limits  *config.QualityLimits
			}
			stored := map[string]any{}
			var details []tools.ProposalDetail
			route := "/releases/rules?default=home"
			parse := func(key, label, inherit, current string, currentLimits config.QualityLimits) (fallbackEdit, error) {
				var edit fallbackEdit
				profile, provided, err := str(in, key)
				if err != nil {
					return edit, err
				}
				if provided {
					if len(profile) > 80 {
						return edit, tools.Userf("%s is too long (at most 80 characters)", key)
					}
					profile = canonicalProfileName(snap.Profiles, profile)
					edit.profile = &profile
					stored[key] = profile
					if profile != current {
						details = append(details, tools.ProposalDetail{
							Label: label,
							Value: fmt.Sprintf("%s → %s", displayDefault(current, inherit), displayDefault(profile, inherit)),
						})
					}
				}
				limitKey := key + "Limits"
				if raw, provided := in[limitKey]; provided {
					limits, err := qualityLimitsArg(limitKey, raw)
					if err != nil {
						return edit, err
					}
					edit.limits = &limits
					stored[limitKey] = limits
					if limits != currentLimits {
						details = append(details, tools.ProposalDetail{
							Label: label + " limits",
							Value: fmt.Sprintf("%s → %s", describeQualityLimits(currentLimits), describeQualityLimits(limits)),
						})
					}
				}
				return edit, nil
			}
			home, err := parse("default", "Default (at home)", "None", snap.Default, snap.DefaultLimits)
			if err != nil {
				return tools.Result{}, err
			}
			homeChanged := len(details) > 0
			away, err := parse("defaultAway", "Default away", "Same as at home", snap.DefaultAway, snap.DefaultAwayLimits)
			if err != nil {
				return tools.Result{}, err
			}
			if len(stored) == 0 {
				return tools.Result{}, tools.Userf("provide default, defaultAway, defaultLimits or defaultAwayLimits")
			}
			setDefaults := func(q *config.Quality) error {
				if home.profile != nil {
					q.Default = canonicalProfileNameFromProfiles(q.Profiles, *home.profile)
				}
				if away.profile != nil {
					q.DefaultAway = canonicalProfileNameFromProfiles(q.Profiles, *away.profile)
				}
				if home.limits != nil {
					q.DefaultLimits = *home.limits
				}
				if away.limits != nil {
					q.DefaultAwayLimits = *away.limits
				}
				return nil
			}
			if _, err := d.QualityRules.ValidateQuality(ctx, setDefaults); err != nil {
				return tools.Result{}, err
			}
			result := func(q config.Quality, changed bool) tools.Result {
				data := map[string]any{
					"default":           q.Default,
					"defaultAway":       q.DefaultAway,
					"defaultLimits":     q.DefaultLimits,
					"defaultAwayLimits": q.DefaultAwayLimits,
				}
				if changed {
					data["updated"] = true
				} else {
					data["changed"] = false
				}
				return tools.Result{
					Data:  data,
					Links: []tools.Link{{Label: "Open quality rules", Route: route}},
				}
			}
			if len(details) == 0 {
				return result(config.Quality{
					Default: snap.Default, DefaultAway: snap.DefaultAway,
					DefaultLimits: snap.DefaultLimits, DefaultAwayLimits: snap.DefaultAwayLimits,
				}, false), nil
			}
			if !homeChanged {
				route = "/releases/rules?default=away"
			}
			return propose(
				"Change default quality profiles and limits",
				"Change fallback playback settings used when no rule picks a profile.",
				details,
				stored,
				func(applyCtx context.Context) (tools.Result, error) {
					saved, err := d.QualityRules.UpdateQuality(applyCtx, setDefaults)
					if err != nil {
						return tools.Result{}, err
					}
					return result(saved, true), nil
				},
			), nil
		},
	})
}

func qualityLimitsSchema(label string) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"description": label + ". Replaces the row's limits. Pass {} to clear them.",
		"properties": map[string]any{
			"maxHeight":      map[string]any{"type": "integer", "enum": []int{0, 480, 720, 1080, 2160}},
			"maxBitrateKbps": pInt("Maximum bitrate in kbps (0 leaves bitrate unchanged)", 0, 1000000),
			"stereoAudio":    pBool("Convert surround audio to stereo"),
		},
	}
}

func qualityLimitsArg(key string, raw any) (config.QualityLimits, error) {
	var limits config.QualityLimits
	fields, ok := raw.(map[string]any)
	if !ok {
		return limits, tools.Userf("%s must be an object", key)
	}
	for field := range fields {
		switch field {
		case "maxHeight", "maxBitrateKbps", "stereoAudio":
		default:
			return limits, tools.Userf("%s.%s is not an allowed field", key, field)
		}
	}
	height, err := intArg(fields, "maxHeight", 0, 0, 2160)
	if err != nil {
		return limits, tools.Userf("%s: %v", key, err)
	}
	bitrate, err := intArg(fields, "maxBitrateKbps", 0, 0, 1000000)
	if err != nil {
		return limits, tools.Userf("%s: %v", key, err)
	}
	stereo, err := boolArg(fields, "stereoAudio", false)
	if err != nil {
		return limits, tools.Userf("%s: %v", key, err)
	}
	return config.QualityLimits{MaxHeight: height, MaxBitrateKbps: bitrate, StereoAudio: stereo}, nil
}

func describeQualityLimits(limits config.QualityLimits) string {
	var parts []string
	if limits.MaxHeight > 0 {
		parts = append(parts, fmt.Sprintf("up to %dp", limits.MaxHeight))
	}
	if limits.MaxBitrateKbps > 0 {
		parts = append(parts, fmt.Sprintf("up to %d kbps", limits.MaxBitrateKbps))
	}
	if limits.StereoAudio {
		parts = append(parts, "stereo audio")
	}
	if len(parts) == 0 {
		return "No limits"
	}
	return strings.Join(parts, ", ")
}

// --- schema and parsing helpers for quality rules ---

func pIDList(desc string, maxItems int) map[string]any {
	return map[string]any{
		"type":        "array",
		"maxItems":    maxItems,
		"description": desc,
		"items": map[string]any{
			"type":    "integer",
			"minimum": 1,
			"maximum": 1<<31 - 1,
		},
	}
}

func pDeviceMatchList(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"maxItems":    50,
		"description": desc,
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"description":          "Set exactly one of group, type, app, client or contains",
			"properties": map[string]any{
				"group":    pEnum("Device class: tv, mobile or computer", clientreg.ClassTV, clientreg.ClassMobile, clientreg.ClassComputer),
				"type":     pString("Registry device type id from quality_rules_list(includeDevices=true), for example Apple TV", 120),
				"app":      pString("Registry app id from quality_rules_list(includeDevices=true), for example plex:Plex for iOS", 160),
				"client":   pString("Exact seen client id from quality_rules_list(includeDevices=true)", 160),
				"contains": pString("Product or platform substring match", 120),
				"label":    pString("Optional display label for the device match", 120),
			},
		},
	}
}

func clientIdentityArg(in map[string]any) (*QualityClientIdentity, error) {
	raw, ok := in["client"]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, tools.Userf("client must be an object")
	}
	for k := range m {
		switch k {
		case "product", "platform", "device", "protocol":
		default:
			return nil, tools.Userf("client.%s is not an allowed field", k)
		}
	}
	product, err := optStr(m, "product", 128)
	if err != nil {
		return nil, err
	}
	if product == "" {
		return nil, tools.Userf("client.product is required when client is provided")
	}
	platform, err := optStr(m, "platform", 128)
	if err != nil {
		return nil, err
	}
	device, err := optStr(m, "device", 128)
	if err != nil {
		return nil, err
	}
	protocol, err := optStr(m, "protocol", 32)
	if err != nil {
		return nil, err
	}
	return &QualityClientIdentity{
		Product:  product,
		Platform: platform,
		Device:   device,
		Protocol: core.Protocol(strings.ToLower(protocol)),
	}, nil
}

func idListArg(in map[string]any, key string, maxItems int) ([]int64, bool, error) {
	raw, ok := in[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case []int64:
		items = make([]any, len(v))
		for i, n := range v {
			items[i] = float64(n)
		}
	case []int:
		items = make([]any, len(v))
		for i, n := range v {
			items[i] = float64(n)
		}
	default:
		return nil, false, tools.Userf("%s must be a list of positive ids", key)
	}
	if len(items) > maxItems {
		return nil, false, tools.Userf("%s has too many entries (at most %d)", key, maxItems)
	}
	out := make([]int64, 0, len(items))
	seen := make(map[int64]bool, len(items))
	for _, item := range items {
		f, ok, err := num(map[string]any{key: item}, key)
		if err != nil || !ok {
			return nil, false, tools.Userf("%s entries must be positive whole numbers", key)
		}
		n := int64(f)
		if float64(n) != f || n <= 0 || n > 1<<31-1 {
			return nil, false, tools.Userf("%s entries must be positive whole numbers", key)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, true, nil
}

func deviceMatchesArg(in map[string]any) ([]config.DeviceMatch, bool, error) {
	raw, ok := in["devices"]
	if !ok || raw == nil {
		return nil, false, nil
	}
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case []config.DeviceMatch:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, false, tools.Userf("devices must be a list of device match objects")
		}
		if err := json.Unmarshal(b, &items); err != nil {
			return nil, false, tools.Userf("devices must be a list of device match objects")
		}
	default:
		return nil, false, tools.Userf("devices must be a list of device match objects")
	}
	if len(items) > 50 {
		return nil, false, tools.Userf("devices has too many entries (at most 50)")
	}
	out := make([]config.DeviceMatch, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false, tools.Userf("devices[%d] must be an object", i)
		}
		for k := range m {
			switch k {
			case "group", "type", "app", "client", "contains", "label":
			default:
				return nil, false, tools.Userf("devices[%d].%s is not an allowed field", i, k)
			}
		}
		dm := config.DeviceMatch{}
		var err error
		if dm.Group, err = optStr(m, "group", 16); err != nil {
			return nil, false, err
		}
		if dm.Type, err = optStr(m, "type", 120); err != nil {
			return nil, false, err
		}
		if dm.App, err = optStr(m, "app", 160); err != nil {
			return nil, false, err
		}
		if dm.Client, err = optStr(m, "client", 160); err != nil {
			return nil, false, err
		}
		if dm.Contains, err = optStr(m, "contains", 120); err != nil {
			return nil, false, err
		}
		if dm.Label, err = optStr(m, "label", 120); err != nil {
			return nil, false, err
		}
		n := 0
		for _, s := range []string{dm.Group, dm.Type, dm.App, dm.Client, dm.Contains} {
			if s != "" {
				n++
			}
		}
		if n != 1 {
			return nil, false, tools.Userf("devices[%d] must set exactly one of group, type, app, client or contains", i)
		}
		if dm.Group != "" && dm.Group != clientreg.ClassTV && dm.Group != clientreg.ClassMobile && dm.Group != clientreg.ClassComputer {
			return nil, false, tools.Userf("devices[%d].group must be tv, mobile or computer", i)
		}
		out = append(out, dm)
	}
	return out, true, nil
}

func parseNewQualityRule(in map[string]any) (config.QualityRule, error) {
	var r config.QualityRule
	var err error
	if r.Name, err = optStr(in, "name", 80); err != nil {
		return r, err
	}
	if libs, ok, err := idListArg(in, "libraries", 100); err != nil {
		return r, err
	} else if ok && len(libs) > 0 {
		r.Libraries = libs
	}
	if r.ExceptLibraries, err = boolArg(in, "exceptLibraries", false); err != nil {
		return r, err
	}
	if viewers, ok, err := idListArg(in, "viewers", 100); err != nil {
		return r, err
	} else if ok && len(viewers) > 0 {
		r.Viewers = viewers
	}
	if r.ExceptViewers, err = boolArg(in, "exceptViewers", false); err != nil {
		return r, err
	}
	if devs, ok, err := deviceMatchesArg(in); err != nil {
		return r, err
	} else if ok && len(devs) > 0 {
		r.Devices = devs
	}
	if r.ExceptDevices, err = boolArg(in, "exceptDevices", false); err != nil {
		return r, err
	}
	if r.Where, err = optStr(in, "where", 16); err != nil {
		return r, err
	}
	if r.Where != "" && r.Where != config.WhereHome && r.Where != config.WhereAway {
		return r, tools.Userf("where must be home or away")
	}
	if r.Kind, err = optStr(in, "kind", 16); err != nil {
		return r, err
	}
	if r.Kind != "" && r.Kind != config.KindMovie && r.Kind != config.KindEpisode {
		return r, tools.Userf("kind must be movie or episode")
	}
	if r.Profile, err = optStr(in, "profile", 80); err != nil {
		return r, err
	}
	if r.MaxHeight, err = intArg(in, "maxHeight", 0, 0, 2160); err != nil {
		return r, err
	}
	switch r.MaxHeight {
	case 0, 480, 720, 1080, 2160:
	default:
		return r, tools.Userf("maxHeight must be 0, 480, 720, 1080 or 2160")
	}
	if r.MaxBitrateKbps, err = intArg(in, "maxBitrateKbps", 0, 0, 1000000); err != nil {
		return r, err
	}
	if r.StereoAudio, err = boolArg(in, "stereoAudio", false); err != nil {
		return r, err
	}
	return r, nil
}

type qualityRulePatch struct {
	name            *string
	libraries       *[]int64
	exceptLibraries *bool
	viewers         *[]int64
	exceptViewers   *bool
	devices         *[]config.DeviceMatch
	exceptDevices   *bool
	where           *string
	kind            *string
	profile         *string
	maxHeight       *int
	maxBitrateKbps  *int
	stereoAudio     *bool
}

func parseQualityRulePatch(in map[string]any, ruleID string) (qualityRulePatch, map[string]any, error) {
	var p qualityRulePatch
	stored := map[string]any{"ruleId": ruleID}
	changed := 0

	if v, ok, err := str(in, "name"); err != nil {
		return p, nil, err
	} else if ok {
		if len(v) > 80 {
			return p, nil, tools.Userf("name is too long (at most 80 characters)")
		}
		p.name = &v
		stored["name"] = v
		changed++
	}
	if libs, ok, err := idListArg(in, "libraries", 100); err != nil {
		return p, nil, err
	} else if ok {
		p.libraries = &libs
		stored["libraries"] = int64SliceToAny(libs)
		changed++
	}
	if _, ok := in["exceptLibraries"]; ok && in["exceptLibraries"] != nil {
		b, err := boolArg(in, "exceptLibraries", false)
		if err != nil {
			return p, nil, err
		}
		p.exceptLibraries = &b
		stored["exceptLibraries"] = b
		changed++
	}
	if viewers, ok, err := idListArg(in, "viewers", 100); err != nil {
		return p, nil, err
	} else if ok {
		p.viewers = &viewers
		stored["viewers"] = int64SliceToAny(viewers)
		changed++
	}
	if _, ok := in["exceptViewers"]; ok && in["exceptViewers"] != nil {
		b, err := boolArg(in, "exceptViewers", false)
		if err != nil {
			return p, nil, err
		}
		p.exceptViewers = &b
		stored["exceptViewers"] = b
		changed++
	}
	if devs, ok, err := deviceMatchesArg(in); err != nil {
		return p, nil, err
	} else if ok {
		p.devices = &devs
		stored["devices"] = deviceMatchesToInput(devs)
		changed++
	}
	if _, ok := in["exceptDevices"]; ok && in["exceptDevices"] != nil {
		b, err := boolArg(in, "exceptDevices", false)
		if err != nil {
			return p, nil, err
		}
		p.exceptDevices = &b
		stored["exceptDevices"] = b
		changed++
	}
	if v, ok, err := str(in, "where"); err != nil {
		return p, nil, err
	} else if ok {
		if v != "" && v != config.WhereHome && v != config.WhereAway {
			return p, nil, tools.Userf("where must be \"\", home or away")
		}
		p.where = &v
		stored["where"] = v
		changed++
	}
	if v, ok, err := str(in, "kind"); err != nil {
		return p, nil, err
	} else if ok {
		if v != "" && v != config.KindMovie && v != config.KindEpisode {
			return p, nil, tools.Userf("kind must be \"\", movie or episode")
		}
		p.kind = &v
		stored["kind"] = v
		changed++
	}
	if v, ok, err := str(in, "profile"); err != nil {
		return p, nil, err
	} else if ok {
		if len(v) > 80 {
			return p, nil, tools.Userf("profile is too long (at most 80 characters)")
		}
		p.profile = &v
		stored["profile"] = v
		changed++
	}
	if _, ok := in["maxHeight"]; ok && in["maxHeight"] != nil {
		n, err := intArg(in, "maxHeight", 0, 0, 2160)
		if err != nil {
			return p, nil, err
		}
		switch n {
		case 0, 480, 720, 1080, 2160:
		default:
			return p, nil, tools.Userf("maxHeight must be 0, 480, 720, 1080 or 2160")
		}
		p.maxHeight = &n
		stored["maxHeight"] = n
		changed++
	}
	if _, ok := in["maxBitrateKbps"]; ok && in["maxBitrateKbps"] != nil {
		n, err := intArg(in, "maxBitrateKbps", 0, 0, 1000000)
		if err != nil {
			return p, nil, err
		}
		p.maxBitrateKbps = &n
		stored["maxBitrateKbps"] = n
		changed++
	}
	if _, ok := in["stereoAudio"]; ok && in["stereoAudio"] != nil {
		b, err := boolArg(in, "stereoAudio", false)
		if err != nil {
			return p, nil, err
		}
		p.stereoAudio = &b
		stored["stereoAudio"] = b
		changed++
	}
	if changed == 0 {
		return p, nil, tools.Userf("provide at least one field to update")
	}
	return p, stored, nil
}

func applyQualityRulePatch(r config.QualityRule, p qualityRulePatch) config.QualityRule {
	out := cloneQualityRule(r)
	if p.name != nil {
		out.Name = *p.name
	}
	if p.libraries != nil {
		if len(*p.libraries) == 0 {
			out.Libraries = nil
			if p.exceptLibraries == nil {
				out.ExceptLibraries = false
			}
		} else {
			out.Libraries = append([]int64(nil), (*p.libraries)...)
		}
	}
	if p.exceptLibraries != nil {
		out.ExceptLibraries = *p.exceptLibraries
	}
	if p.viewers != nil {
		if len(*p.viewers) == 0 {
			out.Viewers = nil
			if p.exceptViewers == nil {
				out.ExceptViewers = false
			}
		} else {
			out.Viewers = append([]int64(nil), (*p.viewers)...)
		}
	}
	if p.exceptViewers != nil {
		out.ExceptViewers = *p.exceptViewers
	}
	if p.devices != nil {
		if len(*p.devices) == 0 {
			out.Devices = nil
			if p.exceptDevices == nil {
				out.ExceptDevices = false
			}
		} else {
			out.Devices = append([]config.DeviceMatch(nil), (*p.devices)...)
		}
	}
	if p.exceptDevices != nil {
		out.ExceptDevices = *p.exceptDevices
	}
	if p.where != nil {
		out.Where = *p.where
	}
	if p.kind != nil {
		out.Kind = *p.kind
	}
	if p.profile != nil {
		out.Profile = *p.profile
	}
	if p.maxHeight != nil {
		out.MaxHeight = *p.maxHeight
	}
	if p.maxBitrateKbps != nil {
		out.MaxBitrateKbps = *p.maxBitrateKbps
	}
	if p.stereoAudio != nil {
		out.StereoAudio = *p.stereoAudio
	}
	return out
}

func buildAddRuleInput(r config.QualityRule, hasPos bool, position int) map[string]any {
	out := map[string]any{}
	if r.Name != "" {
		out["name"] = r.Name
	}
	if hasPos {
		out["position"] = position
	}
	if len(r.Libraries) > 0 {
		out["libraries"] = int64SliceToAny(r.Libraries)
	}
	if r.ExceptLibraries {
		out["exceptLibraries"] = true
	}
	if len(r.Viewers) > 0 {
		out["viewers"] = int64SliceToAny(r.Viewers)
	}
	if r.ExceptViewers {
		out["exceptViewers"] = true
	}
	if len(r.Devices) > 0 {
		out["devices"] = deviceMatchesToInput(r.Devices)
	}
	if r.ExceptDevices {
		out["exceptDevices"] = true
	}
	if r.Where != "" {
		out["where"] = r.Where
	}
	if r.Kind != "" {
		out["kind"] = r.Kind
	}
	if r.Profile != "" {
		out["profile"] = r.Profile
	}
	if r.MaxHeight > 0 {
		out["maxHeight"] = r.MaxHeight
	}
	if r.MaxBitrateKbps > 0 {
		out["maxBitrateKbps"] = r.MaxBitrateKbps
	}
	if r.StereoAudio {
		out["stereoAudio"] = true
	}
	return out
}

func int64SliceToAny(in []int64) []any {
	out := make([]any, len(in))
	for i, n := range in {
		out[i] = n
	}
	return out
}

func deviceMatchesToInput(in []config.DeviceMatch) []any {
	out := make([]any, 0, len(in))
	for _, d := range in {
		m := map[string]any{}
		if d.Group != "" {
			m["group"] = d.Group
		}
		if d.Type != "" {
			m["type"] = d.Type
		}
		if d.App != "" {
			m["app"] = d.App
		}
		if d.Client != "" {
			m["client"] = d.Client
		}
		if d.Contains != "" {
			m["contains"] = d.Contains
		}
		if d.Label != "" {
			m["label"] = d.Label
		}
		out = append(out, m)
	}
	return out
}

func qualityRuleMap(r config.QualityRule, position int) map[string]any {
	m := map[string]any{
		"position": position,
		"id":       r.ID,
	}
	if r.Name != "" {
		m["name"] = r.Name
	}
	if len(r.Libraries) > 0 {
		m["libraries"] = append([]int64(nil), r.Libraries...)
	}
	if r.ExceptLibraries {
		m["exceptLibraries"] = true
	}
	if len(r.Viewers) > 0 {
		m["viewers"] = append([]int64(nil), r.Viewers...)
	}
	if r.ExceptViewers {
		m["exceptViewers"] = true
	}
	if len(r.Devices) > 0 {
		m["devices"] = deviceMatchesToInput(r.Devices)
	}
	if r.ExceptDevices {
		m["exceptDevices"] = true
	}
	if r.Where != "" {
		m["where"] = r.Where
	}
	if r.Kind != "" {
		m["kind"] = r.Kind
	}
	if r.Profile != "" {
		m["profile"] = r.Profile
	}
	if r.MaxHeight > 0 {
		m["maxHeight"] = r.MaxHeight
	}
	if r.MaxBitrateKbps > 0 {
		m["maxBitrateKbps"] = r.MaxBitrateKbps
	}
	if r.StereoAudio {
		m["stereoAudio"] = true
	}
	return m
}

func cloneQualityRule(r config.QualityRule) config.QualityRule {
	out := r
	if r.Libraries != nil {
		out.Libraries = append([]int64(nil), r.Libraries...)
	}
	if r.Viewers != nil {
		out.Viewers = append([]int64(nil), r.Viewers...)
	}
	if r.Devices != nil {
		out.Devices = append([]config.DeviceMatch(nil), r.Devices...)
	}
	return out
}

func findQualityRuleIndex(rules []config.QualityRule, id string) int {
	for i, r := range rules {
		if r.ID == id {
			return i
		}
	}
	return -1
}

func canonicalProfileName(names []string, want string) string {
	for _, n := range names {
		if strings.EqualFold(n, want) {
			return n
		}
	}
	return want
}

func canonicalProfileNameFromProfiles(profiles []core.QualityProfile, want string) string {
	for _, p := range profiles {
		if strings.EqualFold(p.Name, want) {
			return p.Name
		}
	}
	return want
}

func ruleDisplayLabel(r config.QualityRule, pos int) string {
	if r.Name != "" {
		return fmt.Sprintf("%q (%s)", r.Name, r.ID)
	}
	return fmt.Sprintf("#%d (%s)", pos, r.ID)
}

func displayDefault(v, emptyLabel string) string {
	if v == "" {
		return emptyLabel
	}
	return v
}

func describeQualityRule(r config.QualityRule, snap QualityRulesSnapshot) string {
	cond := describeRuleConditions(r, snap)
	eff := describeRuleEffect(r)
	if r.Name != "" {
		return fmt.Sprintf("%s (%s → %s)", r.Name, cond, eff)
	}
	return fmt.Sprintf("%s → %s", cond, eff)
}

func describeRuleConditions(r config.QualityRule, snap QualityRulesSnapshot) string {
	libName := map[int64]string{}
	for _, l := range snap.Libraries {
		libName[l.ID] = l.Name
	}
	viewerName := map[int64]string{}
	for _, v := range snap.Viewers {
		viewerName[v.ID] = v.Name
	}
	var parts []string
	if len(r.Libraries) > 0 {
		names := make([]string, 0, len(r.Libraries))
		for _, id := range r.Libraries {
			if n := libName[id]; n != "" {
				names = append(names, n)
			} else {
				names = append(names, fmt.Sprintf("library %d", id))
			}
		}
		prefix := "Libraries: "
		if r.ExceptLibraries {
			prefix = "Libraries except: "
		}
		parts = append(parts, prefix+strings.Join(names, ", "))
	}
	if len(r.Viewers) > 0 {
		names := make([]string, 0, len(r.Viewers))
		for _, id := range r.Viewers {
			if n := viewerName[id]; n != "" {
				names = append(names, n)
			} else {
				names = append(names, fmt.Sprintf("viewer %d", id))
			}
		}
		prefix := "Viewers: "
		if r.ExceptViewers {
			prefix = "Viewers except: "
		}
		parts = append(parts, prefix+strings.Join(names, ", "))
	}
	if len(r.Devices) > 0 {
		names := make([]string, 0, len(r.Devices))
		for _, dm := range r.Devices {
			if dm.Label != "" {
				names = append(names, dm.Label)
				continue
			}
			field, val := dm.Field()
			switch field {
			case "group":
				switch val {
				case clientreg.ClassTV:
					names = append(names, "TVs & streaming boxes")
				case clientreg.ClassMobile:
					names = append(names, "Phones & tablets")
				case clientreg.ClassComputer:
					names = append(names, "Computers & browsers")
				default:
					names = append(names, val)
				}
			default:
				names = append(names, val)
			}
		}
		prefix := "Devices: "
		if r.ExceptDevices {
			prefix = "Devices except: "
		}
		parts = append(parts, prefix+strings.Join(names, ", "))
	}
	switch r.Where {
	case config.WhereHome:
		parts = append(parts, "At home")
	case config.WhereAway:
		parts = append(parts, "Away")
	}
	switch r.Kind {
	case config.KindMovie:
		parts = append(parts, "Movies")
	case config.KindEpisode:
		parts = append(parts, "Episodes")
	}
	if len(parts) == 0 {
		return "Everything"
	}
	return strings.Join(parts, " · ")
}

func describeRuleEffect(r config.QualityRule) string {
	var parts []string
	if r.Profile != "" {
		parts = append(parts, r.Profile)
	} else {
		parts = append(parts, "No profile change")
	}
	if r.MaxHeight > 0 {
		if r.MaxHeight == 2160 {
			parts = append(parts, "≤4K")
		} else {
			parts = append(parts, fmt.Sprintf("≤%dp", r.MaxHeight))
		}
	}
	if r.MaxBitrateKbps > 0 {
		if r.MaxBitrateKbps%1000 == 0 {
			parts = append(parts, fmt.Sprintf("%d Mbps", r.MaxBitrateKbps/1000))
		} else {
			parts = append(parts, fmt.Sprintf("%.1f Mbps", float64(r.MaxBitrateKbps)/1000))
		}
	}
	if r.StereoAudio {
		parts = append(parts, "Stereo")
	}
	return strings.Join(parts, ", ")
}
