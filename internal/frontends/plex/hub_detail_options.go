package plex

import (
	"context"
	"net/url"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

type structuralProjectionKey struct{}

// applyHubDetailOptions projects requested structural blocks onto the rendered
// cards. Membership and paging have already been selected by the shelf engine.
func (s *Server) applyHubDetailOptions(ctx context.Context, mc *model.MediaContainer, q url.Values) {
	children, onDeck, related, extras := metadataOptionOn(q.Get("includeChildren")), metadataOptionOn(q.Get("includeOnDeck")), metadataOptionOn(q.Get("includeRelated")), metadataOptionOn(q.Get("includeExtras"))
	preferences := q.Get("includePreferences") == "1"
	if !children && !onDeck && !related && !extras && !preferences {
		return
	}
	ctx = context.WithValue(ctx, structuralProjectionKey{}, true)
	pr := FromContext(ctx)
	var missing []frontend.RatingKey
	seenMissing := map[frontend.RatingKey]bool{}
	for hi := range mc.Hub {
		for i := range mc.Hub[hi].Metadata {
			n, err := strconv.ParseInt(mc.Hub[hi].Metadata[i].RatingKey, 10, 64)
			if err != nil {
				continue
			}
			k := frontend.RatingKey(n)
			if titles := s.titleDetails(); titles != nil && titles.get(k) != nil {
				continue
			}
			if !seenMissing[k] {
				seenMissing[k] = true
				missing = append(missing, k)
			}
		}
	}
	if pre, ok := s.catalog().(frontend.StoredTitlePreloader); ok && len(missing) > 1 {
		pre.PreloadStored(ctx, missing)
	}
	type hubSlot struct {
		row   *model.Metadata
		title frontend.Title
	}
	var slots []hubSlot
	for hi := range mc.Hub {
		for i := range mc.Hub[hi].Metadata {
			row := &mc.Hub[hi].Metadata[i]
			n, err := strconv.ParseInt(row.RatingKey, 10, 64)
			if err != nil {
				continue
			}
			var title *frontend.Title
			if titles := s.titleDetails(); titles != nil {
				title = titles.get(frontend.RatingKey(n))
			}
			if title == nil {
				// Cache eviction must not erase a requested block. Production
				// adapters use a local point read without remote discovery.
				if stored, ok := s.catalog().(frontend.StoredTitleLookup); ok {
					title, _ = stored.StoredTitle(ctx, frontend.RatingKey(n))
				} else {
					title, _ = s.catalog().Get(ctx, frontend.RatingKey(n))
				}
			}
			if title == nil {
				continue
			}
			slots = append(slots, hubSlot{row: row, title: *title})
		}
	}
	var memo showEpisodes
	if (children || onDeck) && len(slots) > 1 {
		batch := make([]frontend.Title, len(slots))
		for i := range slots {
			batch[i] = slots[i].title
		}
		memo, _ = s.prefetchListEpisodes(ctx, pr, batch)
	}
	if memo == nil {
		memo = showEpisodes{}
	}
	extraTypes := q.Get("extraTypes")
	for i := range slots {
		title := &slots[i].title
		row := slots[i].row
		var proj model.Metadata
		s.projectDetailBlocks(ctx, pr, title, &proj, memo, children, onDeck, related, extras, extraTypes)
		row.Children, row.OnDeck, row.Related, row.Extras = proj.Children, proj.OnDeck, proj.Related, proj.Extras
		if preferences {
			row.Preferences = s.itemTrackPreferences(ctx, pr, title)
		}
		if extras {
			row.PrimaryExtraKey = proj.PrimaryExtraKey
		}
	}
}

// Inline OnDeck is a metadata object, so the client's element exclusions also
// apply to its children, including when it is nested in hub/detail structures.
func applyInlineOnDeckExclusions(mc *model.MediaContainer, excluded map[string]bool) {
	if len(excluded) == 0 {
		return
	}
	var visit func(*model.Metadata)
	visit = func(row *model.Metadata) {
		if row.OnDeck != nil {
			items := []model.Metadata{row.OnDeck.Metadata}
			applyExcludedElements(items, excluded)
			if excluded["Guid"] {
				items[0].GuidLead = nil
			}
			row.OnDeck.Metadata = items[0]
			visit(&row.OnDeck.Metadata)
		}
		if row.Children != nil {
			for i := range row.Children.Metadata {
				visit(&row.Children.Metadata[i])
			}
		}
		if row.Extras != nil {
			for i := range row.Extras.Metadata {
				visit(&row.Extras.Metadata[i])
			}
		}
		if row.Related != nil {
			for h := range row.Related.Hub {
				for i := range row.Related.Hub[h].Metadata {
					visit(&row.Related.Hub[h].Metadata[i])
				}
			}
		}
	}
	for i := range mc.Metadata {
		visit(&mc.Metadata[i])
	}
	for h := range mc.Hub {
		for i := range mc.Hub[h].Metadata {
			visit(&mc.Hub[h].Metadata[i])
		}
	}
}
