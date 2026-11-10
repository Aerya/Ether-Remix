package quality

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
)

type ImportResult struct {
	Rules    []core.ReleaseRule `json:"rules"`
	Warnings []string           `json:"warnings"`
}

// ImportAIO accepts only an exported JSON object. Credentials, URLs and addon
// settings are never copied, fetched or persisted with a quality profile.
func ImportAIO(raw json.RawMessage) (ImportResult, error) {
	out := ImportResult{Rules: []core.ReleaseRule{}, Warnings: []string{"Import partiel : les parseurs diffèrent. Vérifiez les règles avant enregistrement ; tri, limites de résultats, variantes et expressions conditionnelles ne sont pas reproduits."}}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return out, fmt.Errorf("export JSON AIOStreams attendu")
	}
	for _, k := range []string{"userData", "config"} {
		if v, ok := data[k]; ok {
			var inner map[string]json.RawMessage
			if json.Unmarshal(v, &inner) == nil && inner != nil {
				data = inner
				break
			}
		}
	}
	keys := []string{}
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !strings.HasPrefix(k, "required") && !strings.HasPrefix(k, "excluded") && !strings.HasPrefix(k, "included") && !strings.HasPrefix(k, "preferred") && !strings.Contains(strings.ToLower(k), "range") && !strings.HasPrefix(k, "synced") && !strings.HasPrefix(k, "exclude") {
			continue
		}
		if string(data[k]) == "null" || string(data[k]) == "[]" || string(data[k]) == "false" {
			continue
		}
		action := "require"
		suffix := strings.TrimPrefix(k, "required")
		if strings.HasPrefix(k, "excluded") {
			action = "avoid"
			suffix = strings.TrimPrefix(k, "excluded")
		}
		if strings.HasPrefix(k, "included") || strings.HasPrefix(k, "preferred") || strings.HasPrefix(k, "synced") {
			out.Warnings = append(out.Warnings, k+" : non importé (priorité, contournement ou règle distante)")
			continue
		}
		var values []string
		if err := json.Unmarshal(data[k], &values); err != nil {
			out.Warnings = append(out.Warnings, k+" : format non pris en charge")
			continue
		}
		r := core.ReleaseRule{ID: fmt.Sprintf("aio-%d", len(out.Rules)+1), Name: k, Action: action, Match: "any"}
		unsupported := false
		for _, v := range values {
			c := core.ReleaseCondition{Operator: "equals", Value: strings.ToLower(v)}
			switch suffix {
			case "Resolutions":
				n, e := strconv.Atoi(strings.TrimSuffix(v, "p"))
				if e != nil || n < 1 {
					unsupported = true
					break
				}
				c = core.ReleaseCondition{Field: "height", Operator: "range", Min: float64(n), Max: float64(n)}
			case "Encodes":
				c.Field = "video"
				switch strings.ToLower(v) {
				case "h264", "h.264", "x264", "avc":
					c.Value = "h264"
				case "h265", "h.265", "x265", "hevc":
					c.Value = "hevc"
				case "av1", "vp9":
				default:
					unsupported = true
				}
			case "Qualities":
				c.Field = "source"
				m := map[string]string{"web-dl": "webdl", "webrip": "webrip", "bluray": "bluray", "remux": "remux", "bluray remux": "remux", "hdtv": "hdtv", "dvdrip": "dvd", "dvd": "dvd"}
				var ok bool
				c.Value, ok = m[strings.ToLower(v)]
				if !ok {
					unsupported = true
				}
			case "Languages":
				c.Field = "language"
				if strings.EqualFold(v, "unknown") || strings.EqualFold(v, "original") {
					unsupported = true
				}
			case "Subtitles":
				c.Field = "subtitles"
				if strings.EqualFold(v, "unknown") || strings.EqualFold(v, "original") {
					unsupported = true
				}
			case "VisualTags":
				c.Field = "name"
				c.Operator = "regex"
				m := map[string]string{"dv": `(?i)\b(dv|dovi|dolby[ ._-]?vision)\b`, "hdr": `(?i)\bhdr(?:10(?:\+|plus)?)?\b`, "hdr10": `(?i)\bhdr10\b`, "hdr10+": `(?i)\bhdr10(?:\+|plus)`, "hlg": `(?i)\bhlg\b`, "sdr": `(?i)\bsdr\b`}
				var ok bool
				c.Value, ok = m[strings.ToLower(v)]
				if !ok {
					unsupported = true
				}
			case "AudioChannels":
				if v == "Unknown" {
					unsupported = true
					break
				}
				c.Field = "name"
				c.Operator = "regex"
				c.Value = `(?i)\b` + strings.ReplaceAll(regexp.QuoteMeta(v), `\.`, `[ .]`) + `\b`
			case "AudioTags":
				if strings.EqualFold(v, "Atmos") {
					c.Field = "name"
					c.Operator = "regex"
					c.Value = `(?i)\batmos\b`
					break
				}
				c.Field = "audio"
				m := map[string]string{"aac": "aac", "ac3": "ac3", "dd": "ac3", "eac3": "eac3", "dd+": "eac3", "truehd": "truehd", "dts": "dts", "flac": "flac", "opus": "opus", "mp3": "mp3"}
				var ok bool
				c.Value, ok = m[strings.ToLower(v)]
				if !ok {
					unsupported = true
				}
			case "ReleaseGroups":
				c.Field = "group"
			case "Keywords":
				c.Field = "name"
				c.Operator = "regex"
				c.Value = `(?i)(?:^|[ \[(_\-.])` + regexp.QuoteMeta(v) + `(?:[ \[(\)\]_.-]|$)`
				out.Warnings = append(out.Warnings, k+" : recherche limitée au nom de release ; vérifier les séparateurs des mots")
			case "RegexPatterns":
				c.Field = "name"
				c.Operator = "regex"
				c.Value = v
				if strings.HasPrefix(v, "/") {
					last := strings.LastIndex(v, "/")
					if last <= 0 {
						unsupported = true
						break
					}
					flags := v[last+1:]
					if strings.Trim(flags, "im") != "" {
						unsupported = true
						break
					}
					c.Value = v[1:last]
					if flags != "" {
						c.Value = "(?" + flags + ")" + c.Value
					}
				}
				out.Warnings = append(out.Warnings, k+" : regex appliquées au nom uniquement, sans nom du dossier ni indexeur")
				if _, e := regexp.Compile(c.Value); e != nil {
					unsupported = true
				}
			default:
				unsupported = true
			}
			if unsupported {
				break
			}
			r.Conditions = append(r.Conditions, c)
		}
		// Never silently import only part of an OR whitelist (or blacklist).
		if unsupported {
			out.Warnings = append(out.Warnings, k+" : règle entière non importée, valeur ou regex incompatible")
			continue
		}
		if len(r.Conditions) > 0 {
			if _, e := releasepolicy.Compile([]core.ReleaseRule{r}); e != nil {
				out.Warnings = append(out.Warnings, k+" : règle incompatible")
			} else {
				out.Rules = append(out.Rules, r)
			}
		}
	}
	if len(out.Rules) == 0 {
		out.Warnings = append(out.Warnings, "Aucun filtre compatible détecté. Utilisez un export JSON déchiffré, pas un manifest.")
	}
	return out, nil
}
