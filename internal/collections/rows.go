package collections

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// rowItems is how many titles a collection row carries per media type.
const rowItems = 24

// rowPageKeys caps keys for a Home or section shelf. Mixed collections keep up
// to rowItems movies and up to rowItems shows so neither section's shelf is
// starved when the other media type clusters near the front of the list.
func rowPageKeys(keys []core.RatingKey, mediaType string) ([]core.RatingKey, bool) {
	if mediaType != MediaMixed {
		if len(keys) <= rowItems {
			return keys, false
		}
		return keys[:rowItems], true
	}
	var movies, shows int
	var dropped bool
	out := make([]core.RatingKey, 0, min(len(keys), rowItems*2))
	for _, k := range keys {
		switch k.Kind() {
		case core.KindMovie:
			if movies >= rowItems {
				dropped = true
				continue
			}
			movies++
		case core.KindShow:
			if shows >= rowItems {
				dropped = true
				continue
			}
			shows++
		default:
			if len(out) >= rowItems*2 {
				dropped = true
				continue
			}
		}
		out = append(out, k)
	}
	return out, dropped
}

// homeIndexWait bounds how long a page waits for the catalogue index's
// first build before it serves a live row from its last build.
const homeIndexWait = 300 * time.Millisecond

// homeRowWait bounds how long a single collection row may block a page's
// row assembly before its build finishes in the background.
const homeRowWait = 500 * time.Millisecond

// LayoutRow serves a collection or plugin list a Recommendations page
// places as a row: a collection by public id, or a plugin list by
// core.PluginListRowID, read directly with no collection behind it.
// Private ownership and viewer content limits apply; the page row supplies
// placement and schedule.
func (s *Service) LayoutRow(ctx context.Context, user *core.User, hc core.HubContext, id string) (*core.CollectionRow, error) {
	if strings.HasPrefix(strings.TrimSpace(id), "plugin:") {
		return s.pluginListRow(ctx, user, id)
	}
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !visible(row, user) {
		return nil, core.ErrNotFound
	}
	rctx, rcancel := context.WithTimeout(ctx, homeRowWait)
	// A live row is evaluated on request, but a page does not wait out the
	// catalogue index's first build (it reads the whole catalogue): past
	// homeIndexWait it serves the row's last build until the index is ready.
	live := true
	if row.spec.Refresh == RefreshLive {
		live = s.idx.readyWithin(ctx, homeIndexWait) != nil
	}
	keys, err := s.serve(rctx, row, user, live)
	rcancel()
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	count := len(keys)
	page, dropped := rowPageKeys(keys, row.spec.MediaType)
	items, err := s.mirror.Titles(ctx, page)
	if err != nil {
		return nil, err
	}
	hub := core.Hub{ID: "collection." + row.publicID(), Title: row.spec.Name, Style: "shelf", PreviewOnFocus: row.spec.PreviewOnFocus, RankedTop10: row.spec.RankedTop10, Promoted: true, Items: items, More: dropped || count > len(items), Key: row.key(), Context: "hub.home.collection"}
	switch row.spec.MediaType {
	case MediaMovie:
		hub.Type = core.TypeMovie
	case MediaShow:
		hub.Type = core.TypeShow
	}
	return &core.CollectionRow{Hub: hub, CollectionID: row.publicID()}, nil
}
