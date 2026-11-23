package overlays

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

// PluginBadgeSet describes one enabled plugin poster badge set (#594).
type PluginBadgeSet struct {
	PluginID    string `json:"pluginId"`
	PluginName  string `json:"pluginName"`
	Key         string `json:"key"`
	LayerKey    string `json:"layerKey"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// IsBadgeSetDisabled reports whether the owner switched off a plugin badge set.
func (v Settings) IsBadgeSetDisabled(pluginID, setKey string) bool {
	if len(v.DisabledBadgeSets) == 0 {
		return false
	}
	layer := "plugin:" + pluginID + ":" + setKey
	slashLayer := "plugin:" + pluginID + "/" + setKey
	colonPair := pluginID + ":" + setKey
	slashPair := pluginID + "/" + setKey
	for _, d := range v.DisabledBadgeSets {
		if d == layer || d == slashLayer || d == colonPair || d == slashPair {
			return true
		}
	}
	return false
}

// PluginBadgeSets returns the active plugin-contributed poster badge sets.
func (s *Service) PluginBadgeSets(ctx context.Context) []PluginBadgeSet {
	if s == nil || s.o.PluginBadges == nil {
		return []PluginBadgeSet{}
	}
	sets := s.o.PluginBadges(ctx)
	if sets == nil {
		return []PluginBadgeSet{}
	}
	out := make([]PluginBadgeSet, len(sets))
	copy(out, sets)
	for i := range out {
		if out[i].LayerKey == "" {
			out[i].LayerKey = "plugin:" + out[i].PluginID + ":" + out[i].Key
		}
	}
	return out
}

type pluginBadgesPageKey struct{ svc *Service }

type pluginBadgesPage struct {
	checked map[string]bool
	byLayer map[string]map[string][]string
}

func candidateBadgeKeysForTitle(t *core.Title) []string {
	if t == nil || (t.Type != core.TypeMovie && t.Type != core.TypeShow) {
		return nil
	}
	var out []string
	add := func(k string) {
		if k == "" {
			return
		}
		for _, existing := range out {
			if existing == k {
				return
			}
		}
		out = append(out, k)
	}
	if t.Key != 0 {
		add(t.Key.String())
	}
	tmdb := t.IDs.TMDB
	if tmdb == 0 && t.Key != 0 {
		tmdb = t.Key.TMDB()
	}
	if tmdb > 0 {
		add(fmt.Sprintf("tmdb:%s:%d", t.Type, tmdb))
	}
	if imdb := strings.ToLower(strings.TrimSpace(t.IDs.IMDB)); imdb != "" {
		add(fmt.Sprintf("imdb:%s:%s", t.Type, imdb))
	}
	return out
}

func candidateBadgeKeysForRatingKey(k core.RatingKey) []string {
	if k == 0 {
		return nil
	}
	switch {
	case k.Kind() == core.KindMovie:
		out := []string{k.String()}
		if tmdb := k.TMDB(); tmdb > 0 {
			out = append(out, "tmdb:movie:"+strconv.FormatInt(tmdb, 10))
		}
		return out
	case k.Kind() == core.KindShow:
		out := []string{k.String()}
		if tmdb := k.TMDB(); tmdb > 0 {
			out = append(out, "tmdb:show:"+strconv.FormatInt(tmdb, 10))
		}
		return out
	case k.IsPluginKey():
		return []string{k.String()}
	default:
		return nil
	}
}

func (s *Service) enabledPluginLayerMap(cfg Settings, sets []PluginBadgeSet) map[string]bool {
	if !cfg.Enabled || len(sets) == 0 {
		return nil
	}
	var out map[string]bool
	for _, ps := range sets {
		if cfg.IsBadgeSetDisabled(ps.PluginID, ps.Key) {
			continue
		}
		if out == nil {
			out = make(map[string]bool, len(sets))
		}
		layerKey := ps.LayerKey
		if layerKey == "" {
			layerKey = "plugin:" + ps.PluginID + ":" + ps.Key
		}
		out[layerKey] = true
	}
	return out
}

// queryPluginBadges loads pushed badge labels for titleKeys in a single indexed
// query on plugin_poster_badges_title_key, filtering active sets in memory so
// the planner always probes title_key rather than scanning a plugin's full set.
func (s *Service) queryPluginBadges(ctx context.Context, enabledLayers map[string]bool, titleKeys []string) (map[string]map[string][]string, error) {
	if s == nil || s.db == nil || len(enabledLayers) == 0 || len(titleKeys) == 0 {
		return nil, nil
	}
	const chunkSize = 900
	var out map[string]map[string][]string
	for start := 0; start < len(titleKeys); start += chunkSize {
		end := min(start+chunkSize, len(titleKeys))
		slice := titleKeys[start:end]
		args := make([]any, len(slice))
		ph := make([]string, len(slice))
		for i, k := range slice {
			args[i] = k
			ph[i] = "?"
		}
		rows, err := s.db.QueryContext(ctx, `SELECT plugin_id, set_key, title_key, labels_json FROM plugin_poster_badges WHERE title_key IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var pluginID, setKey, titleKey, raw string
			if err := rows.Scan(&pluginID, &setKey, &titleKey, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			layerKey := "plugin:" + pluginID + ":" + setKey
			if !enabledLayers[layerKey] {
				continue
			}
			var labels []string
			if json.Unmarshal([]byte(raw), &labels) != nil || len(labels) == 0 {
				continue
			}
			if out == nil {
				out = make(map[string]map[string][]string, len(enabledLayers))
			}
			if out[layerKey] == nil {
				out[layerKey] = make(map[string][]string)
			}
			out[layerKey][titleKey] = labels
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func (s *Service) prefetchPluginBadges(ctx context.Context, cfg Settings, candKeys []string) context.Context {
	if s == nil || s.db == nil || !cfg.Enabled || len(candKeys) == 0 {
		return ctx
	}
	sets := s.PluginBadgeSets(ctx)
	enabledLayers := s.enabledPluginLayerMap(cfg, sets)
	if len(enabledLayers) == 0 {
		return ctx
	}

	prev, hasPrev := ctx.Value(pluginBadgesPageKey{s}).(pluginBadgesPage)
	var missing []string
	seen := make(map[string]bool, len(candKeys))
	for _, k := range candKeys {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		if hasPrev && prev.checked[k] {
			continue
		}
		missing = append(missing, k)
	}
	if len(missing) == 0 {
		return ctx
	}

	fetched, err := s.queryPluginBadges(ctx, enabledLayers, missing)
	if err != nil {
		return ctx
	}

	checked := make(map[string]bool, len(prev.checked)+len(missing))
	for k := range prev.checked {
		checked[k] = true
	}
	for _, k := range missing {
		checked[k] = true
	}
	byLayer := make(map[string]map[string][]string, len(prev.byLayer)+len(fetched))
	for layer, m := range prev.byLayer {
		cp := make(map[string][]string, len(m))
		for tk, lbls := range m {
			cp[tk] = lbls
		}
		byLayer[layer] = cp
	}
	for layer, m := range fetched {
		if byLayer[layer] == nil {
			byLayer[layer] = make(map[string][]string, len(m))
		}
		for tk, lbls := range m {
			byLayer[layer][tk] = lbls
		}
	}
	return context.WithValue(ctx, pluginBadgesPageKey{s}, pluginBadgesPage{
		checked: checked,
		byLayer: byLayer,
	})
}

func (s *Service) withPluginBadgesForKeys(ctx context.Context, keys []core.RatingKey) context.Context {
	if s == nil || len(keys) == 0 {
		return ctx
	}
	cfg, err := s.Settings(ctx)
	if err != nil || !cfg.Enabled {
		return ctx
	}
	var candKeys []string
	for _, k := range keys {
		candKeys = append(candKeys, candidateBadgeKeysForRatingKey(k)...)
	}
	return s.prefetchPluginBadges(ctx, cfg, candKeys)
}

func (s *Service) withPluginBadgesForTitles(ctx context.Context, cfg Settings, titles []core.Title) context.Context {
	if s == nil || len(titles) == 0 || !cfg.Enabled {
		return ctx
	}
	candKeys := make([]string, 0, len(titles)*2)
	for i := range titles {
		candKeys = append(candKeys, candidateBadgeKeysForTitle(&titles[i])...)
	}
	return s.prefetchPluginBadges(ctx, cfg, candKeys)
}

func (s *Service) pluginBadgeLabelsForTitle(ctx context.Context, t *core.Title, cfg Settings, sets []PluginBadgeSet) map[string][]string {
	enabledLayers := s.enabledPluginLayerMap(cfg, sets)
	if len(enabledLayers) == 0 {
		return nil
	}
	candKeys := candidateBadgeKeysForTitle(t)
	if len(candKeys) == 0 {
		return nil
	}

	var byLayer map[string]map[string][]string
	if page, ok := ctx.Value(pluginBadgesPageKey{s}).(pluginBadgesPage); ok {
		allChecked := true
		for _, ck := range candKeys {
			if !page.checked[ck] {
				allChecked = false
				break
			}
		}
		if allChecked {
			byLayer = page.byLayer
		} else {
			var missing []string
			for _, ck := range candKeys {
				if !page.checked[ck] {
					missing = append(missing, ck)
				}
			}
			extra, _ := s.queryPluginBadges(ctx, enabledLayers, missing)
			byLayer = page.byLayer
			if len(extra) > 0 {
				merged := make(map[string]map[string][]string, len(byLayer)+len(extra))
				for l, m := range byLayer {
					merged[l] = m
				}
				for l, m := range extra {
					cp := make(map[string][]string, len(merged[l])+len(m))
					for k, v := range merged[l] {
						cp[k] = v
					}
					for k, v := range m {
						cp[k] = v
					}
					merged[l] = cp
				}
				byLayer = merged
			}
		}
	} else {
		memoKey := "overlays:plugin-badges:" + strings.Join(candKeys, "|")
		fetched, err := reqmemo.Do(ctx, memoKey, func() (map[string]map[string][]string, error) {
			return s.queryPluginBadges(ctx, enabledLayers, candKeys)
		})
		if err == nil {
			byLayer = fetched
		}
	}

	if len(byLayer) == 0 {
		return nil
	}
	out := make(map[string][]string, len(enabledLayers))
	for _, ps := range sets {
		layerKey := ps.LayerKey
		if layerKey == "" {
			layerKey = "plugin:" + ps.PluginID + ":" + ps.Key
		}
		if !enabledLayers[layerKey] {
			continue
		}
		layerMap := byLayer[layerKey]
		for _, ck := range candKeys {
			if lbls := layerMap[ck]; len(lbls) > 0 {
				out[layerKey] = lbls
				break
			}
		}
	}
	return out
}
