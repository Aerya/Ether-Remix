package contentrating

import "testing"

func TestEnforceCertificationsForeignToUSScale(t *testing.T) {
	cases := []struct {
		country     string
		label       string
		wantDisplay string
		wantEnforce string
	}{
		{"PH", "R-18", "ph/R-18", "NC-17"},
		{"PH", "X", "", "NC-17"},
		{"PH", "R-16", "ph/R-16", "R"},
		{"PH", "R-13", "ph/R-13", "PG-13"},
		{"DE", "18", "de/18", "NC-17"},
		{"DE", "16", "de/16", "R"},
		{"DE", "12", "de/12", "PG-13"},
		{"DE", "6", "de/6", "PG"},
		{"DE", "0", "de/0", "G"},
		{"GB", "18", "gb/18", "NC-17"},
		{"GB", "R18", "gb/R18", "NC-17"},
		{"GB", "15", "gb/15", "R"},
		{"GB", "12A", "gb/12A", "PG-13"},
		{"FR", "18", "fr/18", "NC-17"},
		{"FR", "16", "fr/16", "R"},
		{"FR", "12", "fr/12", "PG-13"},
		{"JP", "R18+", "jp/R18+", "NC-17"},
		{"JP", "R15+", "jp/R15+", "R"},
		{"JP", "PG12", "jp/PG12", "PG"},
		{"KR", "청소년관람불가", "kr/청소년관람불가", "NC-17"},
		{"KR", "Restricted Screening", "", "NC-17"},
		{"AU", "R18+", "au/R18+", "NC-17"},
		{"AU", "X18+", "au/X18+", "NC-17"},
		{"AU", "RC", "", "NC-17"},
		{"AU", "MA15+", "au/MA15+", "R"},
		{"AU", "M", "au/M", "PG-13"},
		{"BR", "18", "br/18", "NC-17"},
		{"ES", "18", "es/18", "NC-17"},
		{"ES", "X", "es/X", "NC-17"},
		{"IN", "A", "in/A", "NC-17"},
		{"IN", "U/A 16+", "in/U/A 16+", "R"},
		{"IN", "U/A 13+", "in/U/A 13+", "PG-13"},
	}

	for _, tc := range cases {
		certs := []Certification{{Country: tc.country, Label: tc.label, ReleaseType: 3}}
		sel := Select(certs, "US")
		if sel.Value != tc.wantDisplay {
			t.Errorf("%s %q display Select = %q, want %q", tc.country, tc.label, sel.Value, tc.wantDisplay)
		}
		got := EnforceCertifications(certs, "US")
		if got != tc.wantEnforce {
			t.Errorf("%s %q EnforceCertifications = %q, want %q", tc.country, tc.label, got, tc.wantEnforce)
		}
		if tc.wantEnforce == "NC-17" || tc.wantEnforce == "R" {
			if !EnforceExceeds(got, "PG-13") {
				t.Errorf("%s %q (%s) must exceed PG-13", tc.country, tc.label, got)
			}
		}
	}
}

func TestEffectiveEnforcementUncertifiedAdultAndKeywords(t *testing.T) {
	if got := EffectiveEnforcement("", "", true, nil); got != UncertifiedAdult {
		t.Fatalf("adult=true uncertified = %q, want %q", got, UncertifiedAdult)
	}
	if got := EffectiveEnforcement("", "NR", false, []string{"romance", "softcore"}); got != UncertifiedAdult {
		t.Fatalf("NR + softcore = %q, want %q", got, UncertifiedAdult)
	}
	if got := EffectiveEnforcement("", "", false, []string{"female nudity", "sexual obsession"}); got != UncertifiedAdult {
		t.Fatalf("two leaning adult keywords = %q, want %q", got, UncertifiedAdult)
	}
	// A single mild drama keyword or an ordinary unrated title remains "" so
	// owner unrated settings continue to apply.
	if got := EffectiveEnforcement("", "", false, []string{"nudity", "coming of age"}); got != "" {
		t.Fatalf("single leaning keyword = %q, want empty", got)
	}
	if got := EffectiveEnforcement("", "NR", false, []string{"documentary"}); got != "" {
		t.Fatalf("ordinary NR = %q, want empty", got)
	}
	if !EnforceExceeds(UncertifiedAdult, "NC-17") || !EnforceExceeds(UncertifiedAdult, "PG-13") {
		t.Fatal("UncertifiedAdult must exceed every non-empty rating limit")
	}
	if EnforceExceeds(UncertifiedAdult, "") {
		t.Fatal("UncertifiedAdult must not exceed an empty (unrestricted) limit")
	}
}
