package experience

import (
	"context"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// ArrangeNeutralHome returns candidate indices in the shared layout's order.
// Indices point at the caller's original sidecars; duplicate producer IDs use
// the last sidecar, matching the established Home layout behavior.
func ArrangeNeutralHome(ctx context.Context, source core.NeutralHomeLayoutSource, viewer *core.User, situation core.HomeSituation, section string, rows []core.NeutralHomeRow) ([]int, error) {
	indices := make(map[string]int, len(rows))
	for i, row := range rows {
		indices[row.ID] = i
	}
	ordered, err := source.FinalizeNeutralHome(ctx, viewer, situation, section, rows)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ordered))
	for _, row := range ordered {
		if i, ok := indices[row.ID]; ok {
			out = append(out, i)
		}
	}
	return out, nil
}

// LayoutCandidate is a row available to a layout engine. ItemCount includes
// non-title entries such as live channels or playlists.
type LayoutCandidate struct {
	ID        string
	Title     string
	Type      core.MediaType
	Purpose   core.RowPurpose
	ItemCount int
}

// ArrangeLayout returns indices into candidates in the engine's final order.
// Duplicate identifiers retain the last candidate, matching existing layout
// rendering. Unknown identifiers are ignored.
func ArrangeLayout(candidates []LayoutCandidate, finalize func([]LayoutCandidate) ([]string, error)) ([]int, error) {
	ids, err := finalize(candidates)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]int, len(candidates))
	for i, row := range candidates {
		byID[row.ID] = i
	}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if i, ok := byID[id]; ok {
			out = append(out, i)
		}
	}
	return out, nil
}

// LayoutRows applies shared Home or per-library placement to neutral frontend rows.
func LayoutRows(ctx context.Context, source core.NeutralHomeLayoutSource, _ []Library, req frontend.RowRequest, sit core.HomeSituation, rows []frontend.Row) ([]frontend.Row, error) {
	section := "home"
	if req.Placement == frontend.PlacementLibrary {
		if id, err := strconv.Atoi(req.ViewID); err == nil && id > 0 {
			sit.LibraryID = id
		}
		if sit.LibraryID > 0 {
			// "library:<id>" is the library's first rows tab.
			section = "library:" + strconv.Itoa(sit.LibraryID)
			if req.Tab != "" {
				section = string(core.LibraryPage(core.LibraryID(sit.LibraryID), req.Tab))
			}
		}
		if req.Tab != "" && section != "home" {
			// The rows tab's own page: "library:<id>/<tab>".
			section += "/" + req.Tab
		}
	}
	if req.Placement == frontend.PlacementDetail {
		return rows, nil
	}
	candidates := make([]core.NeutralHomeRow, 0, len(rows))
	indices := make(map[string]int, len(rows))
	for i, row := range rows {
		indices[row.ID] = i
		// Present carries the row's own defaults (a plugin shelf's declared
		// layout) into the arrangement.
		n := core.NeutralHomeRow{ID: row.ID, Title: row.Title, Kind: row.Kind, Purpose: row.Purpose, ItemCount: len(row.Entries), Always: row.Layout.Always, PreviewOnFocus: row.PreviewOnFocus, RankedTop10: row.RankedTop10, Present: row.Layout.Present}
		for _, entry := range row.Entries {
			if entry.Title != nil {
				n.Items = append(n.Items, *entry.Title)
				if n.Type == "" {
					n.Type = entry.Title.Type
				}
			}
		}
		candidates = append(candidates, n)
	}
	ordered, err := source.FinalizeNeutralHome(ctx, req.Viewer, sit, section, candidates)
	if err != nil {
		return nil, err
	}
	out := make([]frontend.Row, 0, len(ordered))
	for _, arranged := range ordered {
		index, ok := indices[arranged.ID]
		if !ok {
			if arranged.Present.Surface != nil {
				// A plugin surface row is a stub at its page position: no
				// titles, drawn by apps from the plugin's document.
				out = append(out, SurfaceRow(arranged, len(out)))
			}
			continue
		}
		row := rows[index]
		rank := len(out)
		row.Rank = rank
		row.Layout.Order = rank
		row.Layout.Style = row.Style
		row.Layout.StylePolicy = arranged.Style
		row.Layout.HideWatched = arranged.HideWatched
		row.Layout.Present = arranged.Present
		row.Layout.Always = arranged.Always
		row.PreviewOnFocus = arranged.PreviewOnFocus
		row.Layout.PreviewOnFocus = arranged.PreviewOnFocus
		row.Layout.PreviewOnFocusPolicy = arranged.PreviewOnFocusPolicy
		row.RankedTop10 = arranged.RankedTop10
		row.Layout.RankedTop10 = arranged.RankedTop10
		row.Layout.Present = arranged.Present
		out = append(out, row)
	}
	return out, nil
}

// SurfaceRow is the stub row for a page's plugin surface row. Only apps that
// draw plugin documents (Vela) keep it; the rest drop rows without entries.
func SurfaceRow(arranged core.NeutralHomeRow, rank int) frontend.Row {
	row := frontend.Row{ID: arranged.ID, Title: arranged.Title, Kind: "surface", Purpose: arranged.Purpose, Rank: rank}
	row.Layout.Order = rank
	row.Layout.Present = arranged.Present
	return row
}

// IsSurfaceRow reports a plugin surface stub row.
func IsSurfaceRow(row frontend.Row) bool { return row.Layout.Present.Surface != nil }
