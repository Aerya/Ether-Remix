package personalize

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"slices"
)

// PublishRemixRows offers an imported Xperience/Jukebox selection once on Home
// and its media pages. A later owner removal or Hide must remain respected.
func (e *Engine) PublishRemixRows(ctx context.Context, sp core.CollectionSpec) error {
	sections := []string{"home"}
	if sp.MediaType == "movie" || sp.MediaType == "mixed" {
		sections = append(sections, "movies")
	}
	if sp.MediaType == "show" || sp.MediaType == "mixed" {
		sections = append(sections, "shows")
	}
	offer := "remix:collection:" + sp.ID
	for _, section := range sections {
		key := e.pageKeyFor(ctx, section)
		page, _ := e.Page(ctx, key)
		if slices.Contains(page.Offered, offer) {
			continue
		}
		row, ok := e.legacyRow(key, legacyPolicy{Row: KindCollectionPrefix + sp.ID, Status: "always"})
		if !ok {
			continue
		}
		found := false
		for _, existing := range page.Rows {
			if rowSourceKey(existing) == rowSourceKey(row) {
				found = true
				break
			}
		}
		if !found {
			page.Rows = append(page.Rows, row)
		}
		page.Offered = append(page.Offered, offer)
		if _, err := e.SavePage(ctx, page); err != nil {
			return err
		}
	}
	return nil
}
