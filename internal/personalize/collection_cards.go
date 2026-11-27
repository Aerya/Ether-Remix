package personalize

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

type collectionCardSource interface {
	CollectionCardHeaders(context.Context, *core.User, []core.RatingKey) (map[core.RatingKey]*core.Collection, error)
	FindCollectionCardKeys(context.Context, string, int) ([]core.RatingKey, error)
}

// CollectionCardHubs resolves only rows requested by the effective Home layout.
// The caller applies the same final placement rules and row budget as other hubs.
func (e *Engine) CollectionCardHubs(ctx context.Context, user *core.User, hc core.HubContext, limit int) []core.Hub {
	source, ok := e.opts.Collections.(collectionCardSource)
	if !ok || user == nil {
		return nil
	}
	if hc.Now.IsZero() {
		hc.Now = e.now().In(e.loc)
	}
	pl := e.planFor(ctx, user.ID, surfHome)
	if !pl.listed[SourceCollectionCards] {
		return nil
	}
	type selection struct {
		rule *rule
		keys []core.RatingKey
	}
	var rows []selection
	var keys []core.RatingKey
	for _, r := range pl.rules {
		if r.Source != SourceCollectionCards || !r.active(user.ID, hc) {
			continue
		}
		var rowKeys []core.RatingKey
		if match := strings.TrimSpace(r.row.Source.Param("match", "")); match != "" {
			rowKeys, _ = source.FindCollectionCardKeys(ctx, match, core.MaxCollectionCards)
		} else {
			for id := range strings.SplitSeq(r.row.Source.Param("collections", ""), ",") {
				if key, err := core.ParseRatingKey(id); err == nil && key.Kind() == core.KindSmart && !key.IsEphemeral() {
					rowKeys = append(rowKeys, key)
				}
			}
		}
		rows = append(rows, selection{r, rowKeys})
		keys = append(keys, rowKeys...)
	}
	if len(keys) == 0 {
		return nil
	}
	collections, err := source.CollectionCardHeaders(ctx, user, keys)
	if err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 12
	}
	limit = min(limit, core.MaxCollectionCards)
	var hubs []core.Hub
	for _, row := range rows {
		name := strings.TrimSpace(row.rule.Name)
		if name == "" {
			name = e.sourceTitle(SourceCollectionCards)
		}
		hub := core.Hub{ID: "home." + row.rule.RowID, Title: name, Type: "collection", Style: "shelf", Promoted: true}
		seen := map[core.RatingKey]bool{}
		for _, key := range row.keys {
			c := collections[key]
			if c == nil || c.OwnerUserID != 0 || c.ChildCount == 0 || seen[key] {
				continue
			}
			seen[key] = true
			hub.Collections = append(hub.Collections, *c)
		}
		if len(hub.Collections) == 0 {
			continue
		}
		hub.More = len(hub.Collections) > limit
		hub.Collections = hub.Collections[:min(limit, len(hub.Collections))]
		hub.ItemCount = len(hub.Collections)
		hubs = append(hubs, hub)
	}
	return hubs
}
