package plex

import "testing"

func TestLocalizedHubTitle(t *testing.T) {
	for _, tc := range []struct{ language, title, want string }{
		{"fr", "Trending Now", "Tendances du moment"},
		{"fr-FR", "Popular", "Populaires"},
		{"fr_CA", "Top Rated", "Les mieux notés"},
		{"fr", "Airing Today", "Diffusés aujourd'hui"},
		{"fr", "Because You Watched Alien", "Parce que vous avez regardé Alien"},
		{"fr", "Top-Rated Comedies", "Les mieux notés : Comédies"},
		{"fr", "Ma collection", "Ma collection"},
		{"en-US", "Trending Now", "Trending Now"},
		{"", "Popular", "Popular"},
	} {
		if got := localizedHubTitle(tc.language, tc.title); got != tc.want {
			t.Errorf("localizedHubTitle(%q, %q) = %q, attendu %q", tc.language, tc.title, got, tc.want)
		}
	}
}
