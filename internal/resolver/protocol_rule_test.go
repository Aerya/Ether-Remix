package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestDeviceRuleMatching(t *testing.T) {
	tests := []struct {
		rule config.QualityRule
		caps core.ClientCaps
		want bool
	}{
		{config.QualityRule{Devices: []config.DeviceMatch{{App: "jellyfin:Infuse"}}}, core.ClientCaps{Protocol: "jellyfin", Product: "Infuse-Direct", Platform: "tvOS"}, true},
		{config.QualityRule{Devices: []config.DeviceMatch{{App: "jellyfin:Infuse"}}}, core.ClientCaps{Protocol: "plex", Product: "Plex for Apple TV", Platform: "tvOS"}, false},
		{config.QualityRule{Devices: []config.DeviceMatch{{Contains: "tv"}}}, core.ClientCaps{Protocol: "plex", Product: "Plex TV"}, true},
		{config.QualityRule{Devices: []config.DeviceMatch{{Contains: "tv"}}}, core.ClientCaps{Protocol: "silo", Product: "Silo TV"}, true},
		// Empty conditions match any play (catch-all rule).
		{config.QualityRule{}, core.ClientCaps{Protocol: "plex"}, true},
		// "Fire TV" contains matches the real app, which calls itself
		// "Plex for Android (TV)" and sends the AFT model number.
		{config.QualityRule{Devices: []config.DeviceMatch{{Contains: "Fire TV"}}}, core.ClientCaps{Product: "Plex for Android (TV)", Platform: "Android", Device: "AFTKRT"}, true},
		{config.QualityRule{Devices: []config.DeviceMatch{{Contains: "Fire TV"}}}, core.ClientCaps{Product: "Plex for Android (TV)", Platform: "Android", Device: "SHIELD Android TV"}, false},
		{config.QualityRule{Devices: []config.DeviceMatch{{Contains: "Fire TV"}}}, core.ClientCaps{Product: "Plex for Android", Platform: "Android", Device: "Pixel 9"}, false},
		// Device type matching ("Fire TV").
		{config.QualityRule{Devices: []config.DeviceMatch{{Type: "Fire TV"}}}, core.ClientCaps{Product: "Plex for Android (TV)", Platform: "Android", Device: "AFTKRT"}, true},
		{config.QualityRule{Devices: []config.DeviceMatch{{Type: "Fire TV"}}}, core.ClientCaps{Product: "Plex for Android (TV)", Platform: "Android", Device: "SHIELD Android TV"}, false},
	}
	for _, v := range tests {
		facts := &clientFacts{caps: v.caps}
		if got := ruleMatches(v.rule, facts, nil, playTarget{}); got != v.want {
			t.Fatalf("%+v %+v: got %v want %v", v.rule, v.caps, got, v.want)
		}
	}
}
