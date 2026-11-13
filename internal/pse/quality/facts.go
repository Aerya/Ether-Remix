package quality

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

// Remix fields use releasepolicy's extension hook, without changing Ether's
// evaluator or registering global plugin fields.
var factValues = map[string][]string{
	"remix_resolution": {"2160p", "1440p", "1080p", "720p", "576p", "480p", "360p", "240p", "144p", "Unknown"},
	"remix_format":     {"BluRay REMUX", "BluRay", "WEB-DL", "WEBRip", "HDRip", "HC HD-Rip", "DVD REMUX", "DVDRip", "HDTV", "CAM", "TS", "TC", "SCR", "Unknown"},
	"remix_visual":     {"HDR+DV", "DV Only", "HDR Only", "HDR10+", "HDR10", "DV", "HDR", "HLG", "10bit", "3D", "IMAX", "AI", "Upscaled", "SDR", "H-OU", "H-SBS", "Unknown"},
	"remix_cache":      {"cached", "uncached", "unknown"},
}

func lookupField(id string) (releasepolicy.FieldSpec, bool) {
	if _, ok := factValues[id]; ok || id == "remix_language" {
		return releasepolicy.FieldSpec{ID: id, ValueClass: "choice", Operators: []string{"equals"}, DefaultOperator: "equals", External: true}, true
	}
	return releasepolicy.LookupBuiltinField(id)
}
func compilePolicy(rules []core.ReleaseRule) (*releasepolicy.Policy, error) {
	for _, r := range rules {
		for _, cs := range [][]core.ReleaseCondition{r.Conditions, r.Unless} {
			for _, c := range cs {
				if values, ok := factValues[c.Field]; ok {
					found := false
					for _, v := range values {
						if strings.EqualFold(c.Value, v) {
							found = true
						}
					}
					if !found {
						return nil, fmt.Errorf("valeur non prise en charge pour %s", c.Field)
					}
				}
			}
		}
	}
	return releasepolicy.CompileWith(rules, lookupField)
}

var tags = map[string]*regexp.Regexp{}

func init() {
	for k, v := range map[string]string{
		"remux": `\b(?:bd|dvd)?[ ._-]?remux\b`, "dvd": `\bdvd(?:[ ._-]?(?:rip|remux))?\b`, "bluray": `\b(blu[ ._-]?ray|bd[ ._-]?rip|brrip)\b`,
		"webdl": `\bweb[ ._-]?dl\b`, "webrip": `\bweb[ ._-]?rip\b`, "hdrip": `\b(?:hd[ ._-]?rip|web[ ._-]?dl[ ._-]?rip)\b`, "hc": `\bhc\b`, "hdtv": `\b(?:hdtv|pdtv|tv[ ._-]?rip|dsr(?:ip)?|sat[ ._-]?rip)\b`,
		"cam": `\b(hd[ ._-]?)?cam(?:rip)?\b`, "ts": `\b(hd[ ._-]?)?ts\b|\btelesync\b`, "tc": `\b(hd[ ._-]?)?tc\b|\btelecine\b`, "scr": `\b(dvd[ ._-]?)?scr\b|\bscreener\b`,
		"DV": `\b(dv|dovi|dolby[ ._-]?vision)\b`, "HDR10+": `\bhdr10(?:\+|plus)`, "HDR10": `\bhdr10\b`, "HDR": `\bhdr(?:10(?:\+|plus)?)?\b`, "HLG": `\bhlg\b`,
		"SDR": `\bsdr\b`, "3D": `\b3d\b`, "H-OU": `\b(?:h[ ._-]?)?(?:ou|over[ ._-]?under)\b`, "H-SBS": `\b(?:h[ ._-]?)?sbs\b`, "10bit": `\b10[ ._-]?bit\b`, "IMAX": `\bimax\b`, "AI": `\bai\b`, "Upscaled": `\bupscal(?:e|ed)\b`,
	} {
		tags[k] = regexp.MustCompile("(?i)" + v)
	}
}
func tag(name, k string) bool { return tags[k] != nil && tags[k].MatchString(name) }
func formatFact(r core.Release) string {
	n := r.Name
	if tag(n, "remux") {
		if tag(n, "dvd") {
			return "DVD REMUX"
		}
		return "BluRay REMUX"
	}
	for _, p := range [][2]string{{"bluray", "BluRay"}, {"webdl", "WEB-DL"}, {"webrip", "WEBRip"}, {"hdrip", "HDRip"}, {"hc", "HC HD-Rip"}, {"dvd", "DVDRip"}, {"hdtv", "HDTV"}, {"cam", "CAM"}, {"ts", "TS"}, {"tc", "TC"}, {"scr", "SCR"}} {
		if tag(n, p[0]) {

			return p[1]
		}
	}
	switch r.Info.Source {
	case core.SourceRemux:
		return "BluRay REMUX"
	case core.SourceBluRay:
		return "BluRay"
	case core.SourceWEBDL:
		return "WEB-DL"
	}
	// Source enum values are stable strings; keep remaining fallbacks explicit.
	return map[string]string{"webdl": "WEB-DL", "webrip": "WEBRip", "hdtv": "HDTV", "dvd": "DVDRip", "cam": "CAM", "screener": "SCR"}[string(r.Info.Source)]
}
func matchFact(r core.Release, c core.ReleaseCondition, original string) (bool, bool) {
	eq := func(v string) bool { return strings.EqualFold(v, c.Value) }
	switch c.Field {
	case "remix_resolution":
		if r.Info.Height == 0 {
			return eq("Unknown"), true
		}
		return eq(fmt.Sprintf("%dp", r.Info.Height)), true
	case "remix_format":
		v := formatFact(r)
		if v == "" {
			v = "Unknown"
		}
		return eq(v), true
	case "remix_language":
		if eq("Unknown") {
			return len(r.Info.Languages) == 0, true
		}
		want := c.Value
		if eq("Original") {
			want = original
			if want == "" {
				return false, true
			}
		}
		want = releaseparse.LanguageName(want)
		for _, v := range r.Info.Languages {
			if strings.EqualFold(releaseparse.LanguageName(v), want) {
				return true, true
			}
		}
		return false, true
	case "remix_cache":
		cached := false
		for _, v := range r.DebridCached {
			if v != "direct" {
				cached = true
			}
		}
		if cached {
			return eq("cached"), true
		}
		if len(r.DebridUncached) > 0 {
			return eq("uncached"), true
		}
		return eq("unknown"), true
	case "remix_visual":
		dv := r.Info.DolbyVision || tag(r.Name, "DV")
		hdr := r.Info.HDR10 || tag(r.Name, "HDR")

		if eq("DV") {
			return dv, true
		}
		if eq("HDR+DV") {
			return hdr && dv, true
		}
		if eq("DV Only") {
			return dv && !hdr, true
		}
		if eq("HDR Only") {
			return hdr && !dv, true
		}
		if eq("HDR") {
			return hdr, true
		}
		if eq("HDR10") {
			return r.Info.HDR10 || tag(r.Name, "HDR10"), true
		}
		for _, v := range factValues[c.Field] {
			if eq(v) && v != "Unknown" {
				return tag(r.Name, v), true
			}
		}
		if eq("Unknown") {
			if dv || hdr {
				return false, true
			}
			for _, v := range factValues[c.Field] {
				if tag(r.Name, v) {
					return false, true
				}
			}
			return true, true
		}
	}
	return false, false
}
