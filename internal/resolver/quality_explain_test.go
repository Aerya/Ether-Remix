package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func explainQuality() config.Quality {
	return config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "At home", MaxHeight: 2160, AllowHDR: true, AllowDolbyVision: true},
			{Name: "Away", MaxHeight: 1080},
			{Name: "Small", MaxHeight: 720},
		},
		Default:     "At home",
		DefaultAway: "Away",
	}
}

func appleTV(remote bool) core.ClientCaps {
	return core.ClientCaps{Protocol: "plex", PreserveEmptyVideoCodecs: true, Product: "Plex for Apple TV", Platform: "tvOS", MaxHeight: 2160, HDR10: true, DolbyVision: true, Remote: remote}
}

func androidPhone(remote bool) core.ClientCaps {
	return core.ClientCaps{Protocol: "plex", PreserveEmptyVideoCodecs: true, Product: "Plex for Android (Mobile)", Platform: "Android", Device: "Pixel 8", MaxHeight: 1080, Remote: remote}
}

func layer(t *testing.T, a QualityAnswer, kind string) QualityLayer {
	t.Helper()
	for _, l := range a.Layers {
		if l.Kind == kind {
			return l
		}
	}
	t.Fatalf("no %s layer in %+v", kind, a.Layers)
	return QualityLayer{}
}

func layerByRule(t *testing.T, a QualityAnswer, kind, ruleID string) QualityLayer {
	t.Helper()
	for _, l := range a.Layers {
		if l.Kind == kind && l.RuleID == ruleID {
			return l
		}
	}
	t.Fatalf("no %s layer for rule %q in %+v", kind, ruleID, a.Layers)
	return QualityLayer{}
}

func decisive(a QualityAnswer) []string {
	var out []string
	for _, l := range a.Layers {
		if l.Decisive {
			out = append(out, l.Kind)
		}
	}
	return out
}

func sameKinds(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// The checker must agree with decideProfile, the function playback uses.
func assertMatchesPlayback(t *testing.T, in QualityQuestion, a QualityAnswer) {
	t.Helper()
	media := titleMedia(in.Title)
	if media == core.MediaAny {
		media = normalizeMedia(in.Kind)
	}
	target := playTarget{
		media:  media,
		titled: in.Title != nil || in.InLibrary != nil,
		inLib:  in.InLibrary,
	}
	p := decideProfile(in.Quality, in.Caps, in.User, target).profile
	if a.Profile != p.Name || a.MaxHeight != p.MaxHeight || a.HDR != p.AllowHDR || a.DolbyVision != p.AllowDolbyVision {
		t.Fatalf("explain %+v disagrees with decideProfile %s %d hdr=%v dv=%v", a, p.Name, p.MaxHeight, p.AllowHDR, p.AllowDolbyVision)
	}
}

func TestExplainQualityAtHomeUsesDefaultProfile(t *testing.T) {
	in := QualityQuestion{Quality: explainQuality(), Caps: appleTV(false), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.ProfileSource != ProfileFromDefault || a.MaxHeight != 2160 || !a.HDR || !a.DolbyVision || a.BitrateKbps != 0 || a.Blocked {
		t.Fatalf("%+v", a)
	}
	if p := layer(t, a, LayerProfile); p.Source != ProfileFromDefault || p.Reason != "No rule picks a profile, so the At home profile applies." {
		t.Fatalf("%+v", p)
	}
	if !sameKinds(decisive(a), LayerProfile) {
		t.Fatalf("decisive %v", decisive(a))
	}
}

func TestExplainQualityAwayProfileWithoutRuleCap(t *testing.T) {
	in := QualityQuestion{Quality: explainQuality(), Caps: appleTV(true), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "Away" || a.ProfileSource != ProfileFromDefaultAway || a.MaxHeight != 1080 || a.HDR || a.BitrateKbps != 0 {
		t.Fatalf("%+v", a)
	}
	if p := layer(t, a, LayerProfile); p.Source != ProfileFromDefaultAway || p.MaxHeight != 1080 {
		t.Fatalf("%+v", p)
	}
	if !sameKinds(decisive(a), LayerProfile) {
		t.Fatalf("decisive %v", decisive(a))
	}
}

func TestExplainQualityViewerRuleBeatsAwayProfile(t *testing.T) {
	q := explainQuality()
	q.Rules = []config.QualityRule{{ID: "r_v9", Viewers: []int64{9}, Profile: "At home", MaxHeight: 720}}
	in := QualityQuestion{Quality: q, Caps: appleTV(true), User: &core.User{ID: 9}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.ProfileSource != ProfileFromRule || a.ProfileRuleID != "r_v9" || a.MaxHeight != 720 || !a.HDR {
		t.Fatalf("%+v", a)
	}
	p := layer(t, a, LayerProfile)
	if p.Source != ProfileFromRule || p.Rule != 1 || p.RuleID != "r_v9" {
		t.Fatalf("%+v", p)
	}
	if h := layerByRule(t, a, LayerRuleLimit, "r_v9"); h.MaxHeight != 720 || !h.Decisive {
		t.Fatalf("%+v", h)
	}
	if !sameKinds(decisive(a), LayerRuleLimit) {
		t.Fatalf("decisive %v", decisive(a))
	}
	// Another viewer on the same app still gets Default away.
	other := ExplainQuality(QualityQuestion{Quality: q, Caps: appleTV(true), User: &core.User{ID: 10}})
	if other.Profile != "Away" || other.ProfileSource != ProfileFromDefaultAway || other.MaxHeight != 1080 {
		t.Fatalf("%+v", other)
	}
}

func TestExplainQualityDeviceCeilingDecides(t *testing.T) {
	in := QualityQuestion{Quality: explainQuality(), Caps: androidPhone(false), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.MaxHeight != 1080 || a.HDR {
		t.Fatalf("%+v", a)
	}
	d := layer(t, a, LayerDevice)
	if d.MaxHeight != 1080 || *d.HDR || !d.Decisive || d.Reason != "This app plays up to 1080p, without HDR." {
		t.Fatalf("%+v", d)
	}
	if !sameKinds(decisive(a), LayerDevice) {
		t.Fatalf("decisive %v", decisive(a))
	}
}

func TestExplainQualityOwnerEditedDevice(t *testing.T) {
	builtin := appleTV(false)
	edited := appleTV(false)
	edited.MaxHeight, edited.ExplicitMaxHeight = 1080, true
	q := explainQuality()
	q.Rules = []config.QualityRule{{ID: "r_atv", Devices: []config.DeviceMatch{{Contains: "Apple TV"}}, Profile: "At home", MaxHeight: 2160}}
	in := QualityQuestion{Quality: q, Caps: edited, Builtin: &builtin, User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.MaxHeight != 1080 {
		t.Fatalf("%+v", a)
	}
	if d := layer(t, a, LayerDevice); !d.Edited || !d.Decisive {
		t.Fatalf("%+v", d)
	}
	if h := layerByRule(t, a, LayerRuleLimit, "r_atv"); h.Decisive || h.MaxHeight != 2160 {
		t.Fatalf("%+v", h)
	}
}

func TestExplainQualityRuleLiftsAndroidCeiling(t *testing.T) {
	q := explainQuality()
	q.Rules = []config.QualityRule{{ID: "r_and", Devices: []config.DeviceMatch{{Contains: "Plex for Android"}}, Profile: "At home"}}
	in := QualityQuestion{Quality: q, Caps: androidPhone(true), User: &core.User{ID: 7}}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if a.Profile != "At home" || a.MaxHeight != 2160 || !a.HDR {
		t.Fatalf("%+v", a)
	}
	lift := layer(t, a, LayerAndroidLift)
	if lift.MaxHeight != 2160 || lift.HDR == nil || !*lift.HDR || lift.RuleID != "r_and" {
		t.Fatalf("%+v", lift)
	}
	if p := layer(t, a, LayerProfile); !p.Decisive {
		t.Fatalf("%+v", p)
	}
}

func TestExplainQualityBitrateRuleLimitsAccumulate(t *testing.T) {
	q := explainQuality()
	q.Rules = []config.QualityRule{
		{ID: "r_away8", Where: config.WhereAway, MaxBitrateKbps: 8000},
		{ID: "r_v7", Viewers: []int64{7}, MaxBitrateKbps: 4500},
	}
	a := ExplainQuality(QualityQuestion{Quality: q, Caps: appleTV(true), User: &core.User{ID: 7}})
	if a.BitrateKbps != 4500 {
		t.Fatalf("%+v", a)
	}
	if len(a.LimitRuleIDs) != 2 || a.LimitRuleIDs[0] != "r_away8" || a.LimitRuleIDs[1] != "r_v7" {
		t.Fatalf("limitRuleIds = %v", a.LimitRuleIDs)
	}
	if r := layerByRule(t, a, LayerRuleLimit, "r_away8"); r.Decisive {
		t.Fatalf("expected stricter viewer cap to win over away cap: %+v", r)
	}
	if r := layerByRule(t, a, LayerRuleLimit, "r_v7"); !r.Decisive || r.BitrateKbps != 4500 {
		t.Fatalf("%+v", r)
	}
	// At home only the viewer's rule matches.
	aHome := ExplainQuality(QualityQuestion{Quality: q, Caps: appleTV(false), User: &core.User{ID: 7}})
	if aHome.BitrateKbps != 4500 || len(aHome.LimitRuleIDs) != 1 || aHome.LimitRuleIDs[0] != "r_v7" {
		t.Fatalf("%+v", aHome)
	}
}

func TestExplainQualityRemoteBlocked(t *testing.T) {
	a := ExplainQuality(QualityQuestion{Quality: explainQuality(), Caps: appleTV(true), User: &core.User{ID: 7}, RemoteDisabled: true})
	if !a.Blocked || !layer(t, a, LayerRemoteBlocked).Decisive {
		t.Fatalf("%+v", a)
	}
	a = ExplainQuality(QualityQuestion{Quality: explainQuality(), Caps: appleTV(false), User: &core.User{ID: 7}, RemoteDisabled: true})
	if a.Blocked {
		t.Fatalf("blocked at home: %+v", a)
	}
}

func TestExplainQualityNoProfilesUsesBuiltin(t *testing.T) {
	in := QualityQuestion{Caps: appleTV(false)}
	a := ExplainQuality(in)
	assertMatchesPlayback(t, in, a)
	if p := layer(t, a, LayerProfile); p.Source != ProfileBuiltin || a.MaxHeight != 1080 || !p.Decisive {
		t.Fatalf("%+v", a)
	}
}

func TestExplainQualityAnyResolutionHasNoHeightDecider(t *testing.T) {
	q := config.Quality{Profiles: []core.QualityProfile{{Name: "Any"}}, Default: "Any"}
	caps := appleTV(false)
	caps.MaxHeight = 0
	a := ExplainQuality(QualityQuestion{Quality: q, Caps: caps})
	if a.MaxHeight != 0 || len(decisive(a)) != 0 {
		t.Fatalf("%+v", a)
	}
	if d := layer(t, a, LayerDevice); d.Reason != "This app plays any resolution, with HDR and Dolby Vision." {
		t.Fatal(d.Reason)
	}
}
