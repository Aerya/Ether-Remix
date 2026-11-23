package plex

import "testing"

// Plex for Roku before 6.4.2 needs libraries without Pivot and id; every
// later app needs them, whatever build suffix its version carries (#571).
func TestLegacyRokuVersions(t *testing.T) {
	cases := []struct {
		platform, version string
		want              bool
	}{
		{"Roku", "9.21.12.12191-2cb8e41a8-Plex", false},
		{"roku", "9.21.12.12191-2cb8e41a8-Plex", false},
		{"Roku", "9.22.1-beta", false},
		{"Roku", "6.4", false},
		{"Roku", "6.4.2", false},
		{"Roku", "6.4.1", true},
		{"Roku", "5.1.0.1234", true},
		{"Roku", "", true},
		{"Roku", "x.1", true},
		{"iOS", "1.0", false},
	}
	for _, c := range cases {
		if got := legacyRoku(&Request{Platform: c.platform, Version: c.version}); got != c.want {
			t.Errorf("legacyRoku(%q, %q) = %v, want %v", c.platform, c.version, got, c.want)
		}
	}
}
