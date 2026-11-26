package mediabrowser

import (
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func (i *instance) writePersonalCollections(w http.ResponseWriter, r *http.Request, shelf personalShelf, q frontend.ItemQuery, latest bool) {
	allowed := len(q.Types) == 0 && len(q.UnservedTypes) == 0
	for _, typ := range q.UnservedTypes {
		allowed = allowed || strings.EqualFold(typ, "boxset")
	}
	var collections []frontend.Collection
	if allowed {
		for _, entry := range shelf.row.Entries {
			if entry.Kind == frontend.RowEntryCollection && entry.Collection != nil {
				collections = append(collections, *entry.Collection)
			}
		}
	}
	total := len(collections)
	start, end := min(max(q.Offset, 0), total), total
	if q.Limit > 0 {
		end = start + min(q.Limit, total-start)
	}
	page := collections[start:end]
	titles := make([]frontend.Title, len(page))
	for n, c := range page {
		titles[n] = frontend.Title{Key: c.Key, Type: "boxset"}
	}
	artRequest := r.WithContext(frontend.WithCollectionCardArtwork(r.Context()))
	images := i.gridListImages(withJellyfinListFiles(withBrowseMemo(artRequest)), titles)
	items := make([]any, 0, len(page))
	for _, c := range page {
		dto := i.boxSetDTO(c)
		for k, value := range images[c.Key] {
			dto[k] = value
		}
		items = append(items, dto)
	}
	if latest {
		i.replyItems(w, r, items)
		return
	}
	i.replyItems(w, r, i.queryResult(items, total, q.Offset))
}
