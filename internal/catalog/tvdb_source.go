package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tvdb"
)

// TVDBSourceTitles resolves a bounded upstream catalogue prefix into durable
// identities and lightweight rows. Details are fetched when a title is opened.
func (c *Catalog) TVDBSourceTitles(ctx context.Context, limit int) ([]core.Title, int, error) {
	source, ok := c.MetadataSources().Get(core.MetadataTVDB).(*tvdb.Client)
	if !ok || !source.Configured() {
		return nil, 0, tvdb.ErrKeyRequired
	}
	hits, err := source.SeriesPrefix(ctx, limit)
	if err != nil {
		return nil, 0, err
	}
	titles := make([]core.Title, 0, len(hits))
	for _, hit := range hits {
		title, err := c.SourceSummary(ctx, hit)
		if err != nil {
			return nil, len(hits), err
		}
		titles = append(titles, title)
	}
	return titles, len(hits), nil
}
