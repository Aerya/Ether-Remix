package releaseparse

import "testing"

func TestTitlesMatchKeepsTrailingSDistinct(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Los Simpsons", "Los Simpson", false},
		{"The Travelers", "Traveler", false},
		{"Hidden.Planets", "Hidden Planet", false},
		{"Étoiles lointaines", "Etoile lointaines", false},
		{"Alien", "Aliens", false},
		{"Predator", "Predators", false},
		{"Cars", "Car", false},
		{"Hidden Planets", "Hidden Plant", false},
		{"Hidden Planets", "Hidden Planets Returns", false},
		{"Hidden Planets", "Hiddens Planet", false},
		{"Dragon Ball Z", "Dragon Ball", false},
		{"Alien 3", "Alien 4", false},
		{"Sector 12345s", "Sector 12345", false},
		{"Sector abc12s", "Sector abc12", false},
		{"", "", false},
	} {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			for _, pair := range [][2]string{{tc.a, tc.b}, {tc.b, tc.a}} {
				if got := TitlesMatch(pair[0], pair[1]); got != tc.want {
					t.Errorf("TitlesMatch(%q, %q) = %v, want %v", pair[0], pair[1], got, tc.want)
				}
			}
		})
	}
}
