package releasedb

import "testing"

func TestInternationalLanguageFilters(t *testing.T) {
	for _, tc := range []struct {
		filter      string
		audio, subs []string
		want        bool
	}{
		{"lang=de", []string{"german"}, nil, true},
		{"lang=ar", []string{"arabic"}, nil, true},
		{"lang=pt", []string{"portuguese-brazil"}, nil, true},
		{"lang=pt-pt", []string{"portuguese-brazil"}, nil, false},
		{"lang=es-419", []string{"spanish"}, nil, false},
		{"lang=es-419", []string{"spanish-latino"}, nil, true},
		{"lang=it", []string{"multi"}, nil, false},
		{"lang=sub:de", []string{"german"}, nil, false},
		{"lang=sub:de", []string{"japanese"}, []string{"german"}, true},
		{"lang=de,sub:es", []string{"german"}, nil, true},
		{"lang=de,sub:es", []string{"japanese"}, []string{"spanish-latino"}, true},
		{"lang=sub:ja", []string{"multi"}, nil, false},
		{"lang=fr", []string{"french"}, nil, true},
		{"lang=vostfr", nil, []string{"french"}, true},
	} {
		if got := ParseFilter(tc.filter).Match(Release{Languages: tc.audio, SubtitleLanguages: tc.subs}); got != tc.want {
			t.Errorf("%s audio=%v subs=%v: got %v want %v", tc.filter, tc.audio, tc.subs, got, tc.want)
		}
	}
}
