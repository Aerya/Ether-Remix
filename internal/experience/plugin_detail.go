package experience

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func (s *Service) pluginDetailRows(ctx context.Context, v *core.User, req frontend.RowRequest) ([]frontend.Row, error) {
	if s.d.PluginLibraries == nil {
		return []frontend.Row{}, nil
	}
	piv, err := s.pluginItem(ctx, v, req.Item)
	if err != nil {
		return nil, err
	}
	if piv.Title.MediaClass == core.MediaClassMusic && piv.Title.MediaRole == core.MediaRoleArtist {
		viewID := strconv.FormatInt(int64(piv.Library.ID), 10)
		if topRow, ok := s.synthesizeArtistTopTracks(ctx, v, req.Item, viewID, req.Limit); ok {
			return []frontend.Row{topRow}, nil
		}
	}
	return []frontend.Row{}, nil
}

func (s *Service) synthesizeArtistTopTracks(ctx context.Context, v *core.User, artistKey core.RatingKey, viewID string, reqLimit int) (frontend.Row, bool) {
	page, err := s.d.PluginLibraries.Children(ctx, artistKey, true, "", 2000)
	if err != nil || len(page.Items) == 0 {
		return frontend.Row{}, false
	}
	tracks := make([]core.Title, 0, len(page.Items))
	for _, it := range page.Items {
		if it.Key == 0 || it.Key == artistKey {
			continue
		}
		if it.MediaRole == core.MediaRoleTrack || (it.MediaRole == "" && it.Type == core.TypeEpisode) {
			tracks = append(tracks, it)
		}
	}
	if len(tracks) == 0 {
		return frontend.Row{}, false
	}
	filtered, err := s.filterPluginTitles(ctx, v, tracks, frontend.ItemQuery{})
	if err != nil || len(filtered) == 0 {
		return frontend.Row{}, false
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Rating != filtered[j].Rating {
			return filtered[i].Rating > filtered[j].Rating
		}
		if filtered[i].Popularity != filtered[j].Popularity {
			return filtered[i].Popularity > filtered[j].Popularity
		}
		ti := strings.ToLower(strings.TrimSpace(filtered[i].Title))
		tj := strings.ToLower(strings.TrimSpace(filtered[j].Title))
		if ti != tj {
			return ti < tj
		}
		return filtered[i].Key < filtered[j].Key
	})

	const defaultTopCap = 10
	capLimit := defaultTopCap
	if reqLimit > 0 && reqLimit < capLimit {
		capLimit = reqLimit
	}
	dedup := make([]core.Title, 0, min(len(filtered), capLimit))
	seenTitle := make(map[string]bool, capLimit)
	seenKey := make(map[core.RatingKey]bool, capLimit)
	for _, it := range filtered {
		if seenKey[it.Key] {
			continue
		}
		seenKey[it.Key] = true
		norm := strings.ToLower(strings.TrimSpace(it.Title))
		if norm != "" {
			if seenTitle[norm] {
				continue
			}
			seenTitle[norm] = true
		}
		dedup = append(dedup, it)
		if len(dedup) >= capLimit {
			break
		}
	}
	if len(dedup) == 0 {
		return frontend.Row{}, false
	}
	return frontend.Row{
		ID:      "plugin:" + viewID + ":detail:top",
		Title:   "Top Tracks",
		Purpose: frontend.RowRecommendation,
		Kind:    "detail",
		Style:   "shelf",
		Items:   dedup,
	}, true
}
