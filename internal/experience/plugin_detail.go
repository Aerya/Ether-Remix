package experience

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

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
	viewID := strconv.FormatInt(int64(piv.Library.ID), 10)
	rows := []frontend.Row{}
	if piv.Title.MediaClass == core.MediaClassMusic && piv.Title.MediaRole == core.MediaRoleArtist {
		if topRow, ok := s.synthesizeArtistTopTracks(ctx, v, req.Item, viewID, req.Limit); ok {
			rows = append(rows, topRow)
		}
	}
	if row, ok := s.pluginSimilarRow(ctx, v, piv, viewID); ok {
		rows = append(rows, row)
	}
	return rows, nil
}

// PluginRelations is the plugin catalogue's relation source (kinds that
// declare related): items similar to an item, or what plays after it.
type PluginRelations interface {
	RelatedAvailable(ctx context.Context, key core.RatingKey, purpose string) bool
	Related(ctx context.Context, key core.RatingKey, purpose string, limit int, wait time.Duration) (string, []core.Title, error)
}

// similarRowWait bounds how long a detail page waits for the plugin's similar
// items; a slower answer still lands in the memo and shows on the next open.
const similarRowWait = 2500 * time.Millisecond

func (s *Service) pluginSimilarRow(ctx context.Context, v *core.User, piv PluginItemView, viewID string) (frontend.Row, bool) {
	rel, ok := s.d.PluginLibraries.(PluginRelations)
	if !ok {
		return frontend.Row{}, false
	}
	label, titles, err := rel.Related(ctx, piv.Title.Key, "similar", 20, similarRowWait)
	if err != nil || len(titles) == 0 {
		return frontend.Row{}, false
	}
	visible, err := s.filterPluginTitles(ctx, v, titles, frontend.ItemQuery{})
	if err != nil || len(visible) == 0 {
		return frontend.Row{}, false
	}
	if label == "" {
		label = "More like this"
	}
	return frontend.Row{ID: "plugin:" + viewID + ":detail:related", Title: label, Purpose: frontend.RowRecommendation, Kind: "detail", Style: "shelf", Items: visible}, true
}

// PluginContinuation lists what plays after a playable plugin item when its
// queue ends (radio), from its kind plugin.
func (s *Service) PluginContinuation(ctx context.Context, viewer *core.Viewer, key core.RatingKey, limit int) ([]core.Title, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	rel, ok := s.d.PluginLibraries.(PluginRelations)
	if !ok || !key.IsPluginKey() {
		return nil, core.ErrNotFound
	}
	if _, err := s.pluginItem(core.WithNoContainerExpand(ctx), v, key); err != nil {
		return nil, err
	}
	_, titles, err := rel.Related(ctx, key, "continuation", limit, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return s.filterPluginTitles(ctx, v, titles, frontend.ItemQuery{})
}

// ContinuationAvailable reports whether any source answers continuation.
func (s *Service) ContinuationAvailable() bool {
	_, ok := s.d.PluginLibraries.(PluginRelations)
	return ok
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
