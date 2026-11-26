package resolver

import (
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// QualityQuestion is one viewer on one device, at home or away, optionally
// scoped to a title, library or kind.
type QualityQuestion struct {
	Quality config.Quality
	// Caps is the client as playback declares it: the capability registry,
	// the owner's App compatibility edits and the request headers.
	Caps core.ClientCaps
	// Builtin is the same client without the owner's App compatibility
	// edits, so the answer can say when an edit decided. Optional.
	Builtin *core.ClientCaps
	User    *core.User
	// Title is the title the viewer is asking about. Optional.
	Title *core.Title
	// Kind is the scope when no title is supplied: "movie" or "episode".
	// A real play is always one of the two, so when neither the title, the
	// library nor Kind says which, the answer is for a movie and says so
	// (QualityAnswer.KindAssumed).
	Kind string
	// InLibrary reports whether the question's title or library is in a
	// library a rule names. Optional.
	InLibrary Membership
	// ChosenByPlugin names the plugin when the target library chooses its
	// own releases; only rule limits apply.
	ChosenByPlugin string
	// RemoteDisabled is set when the viewer may not stream away from home.
	RemoteDisabled bool
}

// Quality layer kinds, in the order playback applies them.
const (
	LayerProfile       = "profile"        // the quality profile picked for the play
	LayerRuleLimit     = "rule-limit"     // a matching rule's resolution, bitrate or stereo limit
	LayerDevice        = "device"         // what the app and device can play
	LayerAndroidLift   = "android-lift"   // a device rule lifts Android's usual 1080p ceiling
	LayerRemoteBlocked = "remote-blocked" // the viewer may not stream away from home
)

// QualityLayer is one layer that took part in the answer.
type QualityLayer struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	// Profile layer.
	Profile  string        `json:"profile,omitempty"`
	Source   ProfileSource `json:"source,omitempty"`
	Rule     int           `json:"rule,omitempty"` // 1-based rule number
	RuleID   string        `json:"ruleId,omitempty"`
	RuleName string        `json:"ruleName,omitempty"`
	// MaxHeight is the ceiling this layer sets; 0 = none.
	MaxHeight   int   `json:"maxHeight,omitempty"`
	HDR         *bool `json:"hdr,omitempty"`
	DolbyVision *bool `json:"dolbyVision,omitempty"`
	BitrateKbps int   `json:"bitrateKbps,omitempty"`
	Stereo      bool  `json:"stereo,omitempty"`
	// Edited is set when an App compatibility edit changed this device.
	Edited bool `json:"edited,omitempty"`
	// Decisive marks the layer that set the final resolution ceiling, the
	// final bitrate cap, stereo audio, or blocked playback.
	Decisive bool `json:"decisive"`
}

// QualityAnswer is the profile, limits and ceiling a play gets, and why.
type QualityAnswer struct {
	Profile       string        `json:"profile"`
	ProfileSource ProfileSource `json:"profileSource"`
	ProfileRuleID string        `json:"profileRuleId,omitempty"`
	LimitRuleIDs  []string      `json:"limitRuleIds"`
	// Kind is the kind the answer was evaluated for: "movie" or "episode".
	Kind string `json:"kind"`
	// KindAssumed is set when nothing in the question named the kind, so
	// Kind is the default ("movie"); ask again with Kind for the other.
	KindAssumed bool           `json:"kindAssumed,omitempty"`
	MaxHeight   int            `json:"maxHeight"` // 0 = no ceiling
	HDR         bool           `json:"hdr"`
	DolbyVision bool           `json:"dolbyVision"`
	BitrateKbps int            `json:"bitrateKbps"` // 0 = no cap
	Stereo      bool           `json:"stereo,omitempty"`
	Blocked     bool           `json:"blocked"`
	Layers      []QualityLayer `json:"layers"`
}

func mediaToKind(media string) string {
	switch media {
	case core.MediaMovie:
		return config.KindMovie
	case core.MediaTV:
		return config.KindEpisode
	default:
		return ""
	}
}

// ExplainQuality answers QualityQuestion with the code path playback uses:
// evaluate (ordered rules: first profile wins, limits accumulate strictest),
// client clamp, and remote playback access.
func ExplainQuality(in QualityQuestion) QualityAnswer {
	declared := in.Caps
	media := titleMedia(in.Title)
	if media == core.MediaAny {
		media = normalizeMedia(in.Kind)
	}
	// Rules scoped to Movies or Episodes match every real play of that kind,
	// so an answer with no kind would skip them and contradict playback.
	kindAssumed := false
	if media != core.MediaMovie && media != core.MediaTV {
		media, kindAssumed = core.MediaMovie, true
	}
	pluginChosen := strings.TrimSpace(in.ChosenByPlugin) != ""
	target := playTarget{
		media:              media,
		titled:             in.Title != nil || in.InLibrary != nil,
		inLib:              in.InLibrary,
		skipFallbackLimits: pluginChosen,
	}
	d := decideProfile(in.Quality, declared, in.User, target)
	choice, eff := d.effect.choice, d.effect

	_, profileRuleID, limitRuleIDs := d.ruleDecision(in.Quality)
	if limitRuleIDs == nil {
		limitRuleIDs = []string{}
	}

	out := QualityAnswer{
		Profile:       d.profile.Name,
		ProfileSource: choice.source,
		LimitRuleIDs:  limitRuleIDs,
		Kind:          mediaToKind(media),
		KindAssumed:   kindAssumed,
		MaxHeight:     d.profile.MaxHeight,
		HDR:           d.profile.AllowHDR,
		DolbyVision:   d.profile.AllowDolbyVision,
		BitrateKbps:   d.caps.MaxBitrateKbps,
		Stereo:        eff.limits.stereo,
		Layers:        []QualityLayer{},
	}
	if pluginChosen {
		out.Profile = ""
		out.ProfileSource = ProfileFromPlugin
		out.MaxHeight = d.caps.MaxHeight
		out.HDR = d.caps.HDR10
		out.DolbyVision = d.caps.DolbyVision
	} else {
		out.ProfileRuleID = profileRuleID
	}

	configured := configuredMaxHeight(d.profile)
	if configured < 0 {
		configured = 0
	}
	var profileLayer QualityLayer
	if pluginChosen {
		profileLayer = QualityLayer{
			Kind:   LayerProfile,
			Source: ProfileFromPlugin,
			Reason: fmt.Sprintf("Releases are chosen by %s.", strings.TrimSpace(in.ChosenByPlugin)),
		}
	} else {
		profileLayer = QualityLayer{
			Kind:        LayerProfile,
			Profile:     choice.profile.Name,
			Source:      choice.source,
			MaxHeight:   configured,
			HDR:         boolp(choice.profile.AllowHDR),
			DolbyVision: boolp(choice.profile.AllowDolbyVision),
		}
		if choice.source == ProfileFromRule && choice.rule >= 0 && choice.rule < len(in.Quality.Rules) {
			r := in.Quality.Rules[choice.rule]
			profileLayer.Rule = choice.rule + 1
			profileLayer.RuleID = r.ID
			profileLayer.RuleName = r.Name
		}
		profileLayer.Reason = profileLayerReason(profileLayer)
	}
	out.Layers = append(out.Layers, profileLayer)

	device := QualityLayer{
		Kind:        LayerDevice,
		MaxHeight:   declared.MaxHeight,
		HDR:         boolp(declared.HDR10),
		DolbyVision: boolp(declared.DolbyVision),
	}
	if b := in.Builtin; b != nil {
		device.Edited = b.MaxHeight != declared.MaxHeight || b.HDR10 != declared.HDR10 || b.DolbyVision != declared.DolbyVision
	}
	device.Reason = deviceLayerReason(device)
	out.Layers = append(out.Layers, device)

	androidLiftIdx := -1
	if !pluginChosen && eff.lifted > 0 && choice.source == ProfileFromRule && choice.rule >= 0 && choice.rule < len(in.Quality.Rules) {
		r := in.Quality.Rules[choice.rule]
		lift := QualityLayer{
			Kind:      LayerAndroidLift,
			Rule:      choice.rule + 1,
			RuleID:    r.ID,
			RuleName:  r.Name,
			MaxHeight: eff.lifted,
			Reason:    fmt.Sprintf("Rule %d lifts Android's usual 1080p limit to %s.", choice.rule+1, heightName(eff.lifted)),
		}
		if eff.hdrLifted {
			lift.HDR = boolp(true)
		}
		androidLiftIdx = len(out.Layers)
		out.Layers = append(out.Layers, lift)
	}

	for _, idx := range eff.limitRules {
		if idx < 0 || idx >= len(in.Quality.Rules) {
			continue
		}
		r := in.Quality.Rules[idx]
		layer := QualityLayer{
			Kind:        LayerRuleLimit,
			Rule:        idx + 1,
			RuleID:      r.ID,
			RuleName:    r.Name,
			MaxHeight:   r.MaxHeight,
			BitrateKbps: r.MaxBitrateKbps,
			Stereo:      r.StereoAudio,
		}
		layer.Reason = ruleLimitReason(layer)
		out.Layers = append(out.Layers, layer)
	}

	if eff.fallbackLimitSource != "" {
		source, limits := fallbackLimits(in.Quality, choice)
		layer := QualityLayer{
			Kind:        LayerRuleLimit,
			Source:      source,
			RuleID:      string(source),
			MaxHeight:   limits.MaxHeight,
			BitrateKbps: limits.MaxBitrateKbps,
			Stereo:      limits.StereoAudio,
		}
		layer.Reason = ruleLimitReason(layer)
		out.Layers = append(out.Layers, layer)
	}

	// Mark the decisive layer for the resolution ceiling.
	if out.MaxHeight > 0 {
		switch {
		case !pluginChosen && configured > 0 && configured == out.MaxHeight && (d.caps.MaxHeight == 0 || configured <= d.caps.MaxHeight):
			out.Layers[0].Decisive = true
		case eff.ruleHeight > 0 && eff.ruleHeight == out.MaxHeight:
			for i := range out.Layers {
				if out.Layers[i].Kind == LayerRuleLimit && out.Layers[i].MaxHeight == out.MaxHeight {
					out.Layers[i].Decisive = true
					break
				}
			}
		case androidLiftIdx >= 0 && eff.lifted == out.MaxHeight:
			out.Layers[androidLiftIdx].Decisive = true
		default:
			out.Layers[1].Decisive = true
		}
	}

	// Mark the decisive layer for the bitrate cap.
	if out.BitrateKbps > 0 {
		marked := false
		for i := range out.Layers {
			if out.Layers[i].Kind == LayerRuleLimit && out.Layers[i].BitrateKbps == out.BitrateKbps {
				out.Layers[i].Decisive = true
				marked = true
				break
			}
		}
		if !marked && declared.MaxBitrateKbps == out.BitrateKbps {
			out.Layers[1].Decisive = true
		}
	}

	// Mark the decisive layer for stereo audio.
	if out.Stereo {
		for i := range out.Layers {
			if out.Layers[i].Kind == LayerRuleLimit && out.Layers[i].Stereo {
				out.Layers[i].Decisive = true
				break
			}
		}
	}

	if declared.Remote && in.RemoteDisabled {
		out.Blocked = true
		out.Layers = append(out.Layers, QualityLayer{
			Kind:     LayerRemoteBlocked,
			Reason:   "This viewer can't stream away from home.",
			Decisive: true,
		})
	}
	return out
}

func profileLayerReason(l QualityLayer) string {
	switch l.Source {
	case ProfileFromRule:
		return fmt.Sprintf("Rule %d picks the %s profile.", l.Rule, l.Profile)
	case ProfileFromDefaultAway:
		return fmt.Sprintf("Away from home uses the %s default profile.", l.Profile)
	case ProfileFromDefault:
		return fmt.Sprintf("No rule picks a profile, so the %s profile applies.", l.Profile)
	case ProfileFromPlugin:
		return "Releases are chosen by the library's plugin."
	}
	return "No quality profile is set, so the built-in one applies."
}

func ruleLimitReason(l QualityLayer) string {
	var parts []string
	if l.MaxHeight > 0 {
		parts = append(parts, "up to "+heightName(l.MaxHeight))
	}
	if l.BitrateKbps > 0 {
		parts = append(parts, bitrateName(l.BitrateKbps))
	}
	if l.Stereo {
		parts = append(parts, "stereo audio")
	}
	name := fmt.Sprintf("Rule %d", l.Rule)
	switch l.Source {
	case ProfileFromDefault:
		name = "Default"
	case ProfileFromDefaultAway:
		name = "Default away"
	}
	return fmt.Sprintf("%s limits playback to %s.", name, strings.Join(parts, ", "))
}

func deviceLayerReason(l QualityLayer) string {
	what := "This app plays"
	if l.Edited {
		what = "Your App compatibility edit lets this app play"
	}
	height := "any resolution"
	if l.MaxHeight > 0 {
		height = "up to " + heightName(l.MaxHeight)
	}
	switch {
	case *l.HDR && *l.DolbyVision:
		return fmt.Sprintf("%s %s, with HDR and Dolby Vision.", what, height)
	case *l.HDR:
		return fmt.Sprintf("%s %s, with HDR but not Dolby Vision.", what, height)
	}
	return fmt.Sprintf("%s %s, without HDR.", what, height)
}

func heightName(h int) string {
	if h == 2160 {
		return "4K"
	}
	return fmt.Sprintf("%dp", h)
}

func bitrateName(kbps int) string {
	if kbps%1000 == 0 {
		return fmt.Sprintf("%d Mbps", kbps/1000)
	}
	return fmt.Sprintf("%.1f Mbps", float64(kbps)/1000)
}

func boolp(b bool) *bool { return &b }
