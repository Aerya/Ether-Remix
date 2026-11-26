package clientreg

import "testing"

// Quality rules group devices by kind, so every app must land in the kind
// an owner expects when they pick "Phones & tablets" or "TVs".
func TestClassPlacesEveryKnownApp(t *testing.T) {
	for _, tc := range []struct {
		id         Identity
		class, typ string
	}{
		{Identity{Product: "Findroid", Device: "Pixel 8"}, ClassMobile, ""},
		{Identity{Product: "Streamyfin", Device: "iPhone"}, ClassMobile, ""},
		{Identity{Product: "Swiftfin iOS", Device: "iPad"}, ClassMobile, "iOS"},
		{Identity{Product: "Swiftfin tvOS", Device: "Apple TV"}, ClassTV, "Apple TV"},
		{Identity{Product: "Infuse-Direct", Platform: "tvOS", Device: "Apple TV"}, ClassTV, "Apple TV"},
		{Identity{Product: "Infuse-Direct", Device: "iPhone"}, ClassMobile, ""},
		{Identity{Product: "Jellyfin Android", Device: "SM-G991"}, ClassMobile, "Android"},
		{Identity{Product: "Jellyfin iOS", Device: "iPhone"}, ClassMobile, "iOS"},
		{Identity{Product: "Jellyfin Android TV", Device: "SHIELD Android TV"}, ClassTV, "NVIDIA Shield"},
		{Identity{Product: "Plex for Android (TV)", Platform: "Android", Device: "Google TV"}, ClassTV, "Google TV"},
		{Identity{Product: "Plex for PlayStation", Platform: "PlayStation"}, ClassTV, "PlayStation"},
		{Identity{Product: "Plex for Mac", Platform: "macOS"}, ClassComputer, "Mac"},
		{Identity{Product: "PM4K", Device: "Kodi"}, ClassComputer, ""},
		{Identity{Product: "JellyCon"}, ClassComputer, ""},
	} {
		if got := Class(tc.id); got != tc.class {
			t.Errorf("Class(%+v) = %q, want %q", tc.id, got, tc.class)
		}
		if got := DeviceType(tc.id); got != tc.typ {
			t.Errorf("DeviceType(%+v) = %q, want %q", tc.id, got, tc.typ)
		}
	}
	for _, a := range Apps() {
		if a.Protocol == "dlna" || a.Name == "Emby SDK" || a.Name == "Files and media players" {
			continue // no fixed form factor
		}
		id := a.Identity
		if id.Product == "" {
			id.Product = a.Product
		}
		if id.Product == "" {
			id.Product = a.Name
		}
		if Class(id) == "" {
			t.Errorf("app %s/%s has no device kind", a.Protocol, a.Name)
		}
	}
}
