package contentrating

import "testing"

func TestAlpha2AndByCountry(t *testing.T) {
	if got := Alpha2("us"); got != "US" {
		t.Fatalf("Alpha2(us) = %q, want US", got)
	}
	if got := Alpha2("ca-qc"); got != "CA" {
		t.Fatalf("Alpha2(ca-qc) = %q, want CA", got)
	}
	if got := Alpha2("419"); got != "" {
		t.Fatalf("Alpha2(419) = %q, want empty", got)
	}

	got := ByCountry([]Certification{
		{Country: "GB", Label: "12", ReleaseType: 5},  // physical
		{Country: "GB", Label: "12A", ReleaseType: 3}, // theatrical wins over physical
		{Country: "US", Label: "PG-13", ReleaseType: 3},
		{Country: "DE", Label: "12", ReleaseType: 3},
		{Country: "PH", Label: "X", ReleaseType: 3}, // unknown non-NR ignored
		{Country: "FR", Label: "NR", ReleaseType: 3},
	})
	if got["GB"] != "12A" || got["US"] != "PG-13" || got["DE"] != "12" || got["FR"] != "NR" {
		t.Fatalf("ByCountry = %#v", got)
	}
	if _, ok := got["PH"]; ok {
		t.Fatalf("expected unrated non-NR PH/X omitted, got %#v", got)
	}
}

func TestDisplayPreferenceOrder(t *testing.T) {
	certs := map[string]string{"US": "PG-13", "GB": "12A", "DE": "12", "CA-QC": "13+"}

	cases := []struct {
		name        string
		stored      string
		certs       map[string]string
		prefs       []string
		wantLabel   string
		wantCountry string
	}{
		{
			name:        "prefers viewer Accept-Language region over server and US",
			stored:      "PG-13",
			certs:       certs,
			prefs:       []string{"GB", "DE", "US"},
			wantLabel:   "12A",
			wantCountry: "GB",
		},
		{
			name:        "falls back to server region when viewer region absent",
			stored:      "PG-13",
			certs:       certs,
			prefs:       []string{"JP", "DE", "US"},
			wantLabel:   "12",
			wantCountry: "DE",
		},
		{
			name:        "falls back to US when neither viewer nor server region present",
			stored:      "PG-13",
			certs:       certs,
			prefs:       []string{"JP", "FR", "US"},
			wantLabel:   "PG-13",
			wantCountry: "US",
		},
		{
			name:        "sub-region certificate collapses ratingCountry to ISO alpha-2",
			stored:      "ca-qc/13+",
			certs:       certs,
			prefs:       []string{"CA", "US"},
			wantLabel:   "13+",
			wantCountry: "CA",
		},
		{
			name:        "stored country-prefixed foreign rating returns bare label and alpha-2",
			stored:      "de/6",
			certs:       nil,
			prefs:       []string{"GB", "US"},
			wantLabel:   "6",
			wantCountry: "DE",
		},
		{
			name:        "stored hyphenated foreign rating returns bare label and alpha-2",
			stored:      "DE-6",
			certs:       nil,
			prefs:       []string{"US"},
			wantLabel:   "6",
			wantCountry: "DE",
		},
		{
			name:        "bare US rating without multi-country map returns US",
			stored:      "TV-MA",
			certs:       nil,
			prefs:       []string{"GB", "US"},
			wantLabel:   "TV-MA",
			wantCountry: "US",
		},
		{
			name:        "unknown bare rating keeps bare label and omits country",
			stored:      "CustomBoard-9",
			certs:       nil,
			prefs:       []string{"US"},
			wantLabel:   "CustomBoard-9",
			wantCountry: "",
		},
		{
			name:        "provider US rating in stored augments foreign TMDB certs",
			stored:      "PG-13",
			certs:       map[string]string{"DE": "12"},
			prefs:       []string{"US"},
			wantLabel:   "PG-13",
			wantCountry: "US",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			label, country := Display(tc.stored, tc.certs, tc.prefs...)
			if label != tc.wantLabel || country != tc.wantCountry {
				t.Fatalf("Display(%q, %v, %v) = (%q, %q), want (%q, %q)",
					tc.stored, tc.certs, tc.prefs, label, country, tc.wantLabel, tc.wantCountry)
			}
		})
	}
}
