package quality

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"testing"
)

func TestDetailedFormatFacts(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"Film.1080p.BluRay.REMUX", "BluRay REMUX"}, {"Film.DVD.REMUX", "DVD REMUX"},
		{"Film.HC.HD-Rip", "HC HD-Rip"}, {"Film.HDRip", "HDRip"}, {"Film.DVDRip", "DVDRip"},
		{"Film.1080p.BluRay", "BluRay"}, {"Film.WEB-DL", "WEB-DL"}, {"Film.WEBRip", "WEBRip"},
		{"Film.HDCAM", "CAM"}, {"Film.HDTS", "TS"}, {"Film.Telecine", "TC"}, {"Film.DVDSCR", "SCR"},
	} {
		r := core.Release{Name: tc.name, Info: releaseparse.Parse(tc.name)}
		if got := formatFact(r); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
func TestImportMixedKnownAndUnknownResolution(t *testing.T) {
	out, e := ImportAIO(json.RawMessage(`{"requiredResolutions":["1080p","Unknown"]}`))
	if e != nil {
		t.Fatal(e)
	}
	p, e := compilePolicy(out.Rules)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Rules) != 1 {
		t.Fatal("missing rule")
	}
	for _, tc := range []struct {
		height int
		reject bool
	}{{0, false}, {1080, false}, {720, true}, {2160, true}} {
		r := core.Release{Info: core.ReleaseInfo{Height: tc.height}}
		if got := evaluate(p, r).Rejected; got != tc.reject {
			t.Errorf("height %d: rejected=%v", tc.height, got)
		}
	}
}
func TestVisualAndCacheFacts(t *testing.T) {
	for _, tc := range []struct {
		field, value, name string
		cached, uncached   []string
		want               bool
	}{
		{"remix_visual", "DV", "Film.Dovi", nil, nil, true},
		{"remix_visual", "H-SBS", "Film.H-SBS", nil, nil, true},
		{"remix_visual", "3D", "Film.H-OU", nil, nil, true},
		{"remix_visual", "SDR", "Film.1080p", nil, nil, false},
		{"remix_visual", "Unknown", "Film.1080p", nil, nil, true},
		{"remix_cache", "uncached", "Film", nil, []string{"realdebrid"}, true},
		{"remix_cache", "uncached", "Film", nil, nil, false},
		{"remix_cache", "cached", "Film", []string{"direct"}, nil, false},
	} {
		r := core.Release{Name: tc.name, Info: releaseparse.Parse(tc.name), DebridCached: tc.cached, DebridUncached: tc.uncached}
		got, known := matchFact(r, core.ReleaseCondition{Field: tc.field, Value: tc.value}, "")
		if !known || got != tc.want {
			t.Errorf("%s/%s: %v known=%v", tc.field, tc.value, got, known)
		}
	}
}
func TestImportDetailedFilterGroups(t *testing.T) {
	raw := json.RawMessage(`{"requiredResolutions":["2160p","1080p","Unknown"],"requiredQualities":["BluRay","WEB-DL","WEBRip","HDRip","HC HD-Rip","DVD REMUX","DVDRip"],"excludedQualities":["CAM","SCR","TS","TC","BluRay REMUX"],"excludedVisualTags":["3D","SDR","H-OU","H-SBS","DV"],"requiredLanguages":["French","Multi","English","Original"],"excludeUncachedFromStreamTypes":["debrid"],"excludedRegexPatterns":["/trailer/i"]}`)
	out, e := ImportAIO(raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Rules) != 7 {
		t.Fatalf("wanted 7 complete filter groups, got %d: %v", len(out.Rules), out.Warnings)
	}
	p, e := compilePolicy(out.Rules)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		reject bool
	}{
		{"Film.FRENCH.1080p.WEB-DL.x265", false},
		{"Film.FRENCH.WEB-DL.x265", false},
		{"Film.FRENCH.720p.WEB-DL.x265", true},
		{"Film.FRENCH.1080p.HC.HD-Rip", false},
		{"Film.FRENCH.1080p.BluRay.REMUX", true},
		{"Film.FRENCH.1080p.WEB-DL.DV", true},
		{"Film.FRENCH.1080p.WEB-DL.trailer", true},
	} {
		r := core.Release{Name: tc.name, Info: releaseparse.Parse(tc.name)}
		if got := evaluate(p, r, "fr").Rejected; got != tc.reject {
			t.Errorf("%s rejected=%v", tc.name, got)
		}
	}
	r := core.Release{Name: "Film.FRENCH.1080p.WEB-DL", Info: releaseparse.Parse("Film.FRENCH.1080p.WEB-DL"), DebridUncached: []string{"realdebrid"}}
	if !evaluate(p, r).Rejected {
		t.Fatal("explicit uncached debrid accepted")
	}
}
