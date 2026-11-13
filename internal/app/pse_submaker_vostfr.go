package app

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/submaker"
)

// pseSubmakerVostfr links SubMaker to the titles of VOSTFR on demand.
func (a *App) pseSubmakerVostfr(rt *pseRuntime) submaker.VostfrHooks {
	return submaker.VostfrHooks{
		Keys: func(ctx context.Context) []core.RatingKey {
			th := a.pseThemes()
			if th == nil {
				return nil
			}
			raw, err := th.Vostfr(ctx)
			if err != nil {
				return nil
			}
			keys := make([]core.RatingKey, 0, len(raw))
			for _, k := range raw {
				if key, err := core.ParseRatingKey(strings.TrimSpace(k)); err == nil {
					keys = append(keys, key)
				}
			}
			return keys
		},
		Languages: func(ctx context.Context, key core.RatingKey) (int, []string, []string) {
			rows, err := a.db.QueryContext(ctx, `SELECT languages, subtitle_languages FROM pse_releases WHERE title_key=?`, int64(key))
			if err != nil {
				return 0, nil, nil
			}
			defer rows.Close()
			n := 0
			var audio, subs []string
			for rows.Next() {
				var langs, subtitles string
				if rows.Scan(&langs, &subtitles) != nil {
					continue
				}
				n++
				audio = append(audio, splitLangs(langs)...)
				subs = append(subs, splitLangs(subtitles)...)
			}
			return n, audio, subs
		},
		Search: a.psePriorityHooks(rt).Search,
	}
}

func splitLangs(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
