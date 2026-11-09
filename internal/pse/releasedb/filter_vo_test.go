package releasedb

import "testing"

func TestFilterVOSTFRAndVO(t *testing.T) {
	vostfr := Release{Name: "Film.2024.VOSTFR.1080p", SubtitleLanguages: []string{"french"}}
	vo := Release{Name: "Film.2024.1080p.WEB"}
	english := Release{Name: "Film.2024.ENG.1080p", Languages: []string{"english"}}
	german := Release{Name: "Film.2024.German.DL.1080p", Languages: []string{"german"}}
	vf := Release{Name: "Film.2024.TRUEFRENCH.1080p", Languages: []string{"french", "vff"}}
	cases := []struct {
		filter string
		r      Release
		want   bool
	}{
		{"lang=fr", vostfr, false},
		{"lang=fr,vostfr", vostfr, true},
		{"lang=stfr", vostfr, true},
		{"lang=fr,vostfr", vf, true},
		{"lang=vostfr", vf, false},
		{"lang=vo", vo, true},
		{"lang=vo", english, true},
		{"lang=vo", german, false},
		{"lang=vo", vf, false},
		{"lang=fr,vo", german, false},
	}
	for _, c := range cases {
		if got := ParseFilter(c.filter).Match(c.r); got != c.want {
			t.Errorf("%s / %s = %v", c.filter, c.r.Name, got)
		}
	}
}
