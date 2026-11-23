package personalize

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

// The high end of the SmartKey payload namespace is reserved for virtual
// discovery collections; ordinary SQLite smart-collection IDs start at one.
const reviewCollectionBase int64 = 900_000_000_000_000

func reviewCollectionKey(year int, typ core.MediaType) core.RatingKey {
	kind := int64(1)
	if typ == core.TypeShow {
		kind = 2
	}
	return core.SmartKey(reviewCollectionBase + int64(year)*10 + kind)
}
func reviewCollectionParts(key core.RatingKey) (int, core.MediaType, bool) {
	if key.Kind() != core.KindSmart {
		return 0, "", false
	}
	id := int64(key-core.SmartKey(0)) - reviewCollectionBase
	year := int(id / 10)
	if year < 1900 || year > 9999 {
		return 0, "", false
	}
	switch id % 10 {
	case 1:
		return year, core.TypeMovie, true
	case 2:
		return year, core.TypeShow, true
	}
	return 0, "", false
}

type reviewCollections struct {
	d    *Discovery
	base core.Collections
}

// ReviewCollections retains the collection engine as the authority for owner
// collections and adds virtual collections assembled from the viewer's history.
func (d *Discovery) ReviewCollections(base core.Collections) core.Collections {
	return &reviewCollections{d: d, base: base}
}
func (c *reviewCollections) review(ctx context.Context, u *core.User, year int, typ core.MediaType) (*core.Collection, []core.Title, error) {
	if u == nil {
		return nil, nil, core.ErrUnauthorized
	}
	hub, e := c.d.YearInReview(ctx, u, year, 10000)
	if e != nil {
		return nil, nil, e
	}
	items := []core.Title{}
	for _, t := range hub.Items {
		if t.Type == typ {
			items = append(items, t)
		}
	}
	col := &core.Collection{Key: reviewCollectionKey(year, typ), Title: hub.Title, Summary: "Titles you completed this year, based on your private watch history.", Kind: "user", Type: typ, OwnerUserID: u.ID, ChildCount: len(items)}
	if len(items) > 0 {
		col.PosterPath = items[0].PosterPath
		col.BackdropPath = items[0].BackdropPath
		col.UpdatedAt = time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return col, items, nil
}
func (c *reviewCollections) List(ctx context.Context, u *core.User, typ core.MediaType) ([]core.Collection, error) {
	var out []core.Collection
	if c.base != nil {
		var e error
		out, e = c.base.List(ctx, u, typ)
		if e != nil {
			return nil, e
		}
	}
	return c.withReviews(ctx, u, typ, out), nil
}

// ListOwned forwards the engine's listing without derived franchises (Plex
// collection search), plus the viewer's Year in Review collections.
func (c *reviewCollections) ListOwned(ctx context.Context, u *core.User, typ core.MediaType) ([]core.Collection, error) {
	var out []core.Collection
	if c.base != nil {
		var e error
		if owned, ok := c.base.(core.OwnedCollectionLister); ok {
			out, e = owned.ListOwned(ctx, u, typ)
		} else {
			out, e = c.base.List(ctx, u, typ)
		}
		if e != nil {
			return nil, e
		}
	}
	return c.withReviews(ctx, u, typ, out), nil
}

// ListComplete forwards the engine's complete listing (errors included, never
// a partial result) and adds the viewer's Year in Review collections, so
// consumers that advertise exact totals (Jellyfin BoxSets) see every
// collection. Without it the wrapper hid ListComplete and those consumers
// answered with an empty list (#454).
func (c *reviewCollections) ListComplete(ctx context.Context, u *core.User, typ core.MediaType) ([]core.Collection, error) {
	var out []core.Collection
	if c.base != nil {
		complete, ok := c.base.(interface {
			ListComplete(context.Context, *core.User, core.MediaType) ([]core.Collection, error)
		})
		if !ok {
			return nil, core.ErrNotAvailable
		}
		var e error
		out, e = complete.ListComplete(ctx, u, typ)
		if e != nil {
			return nil, e
		}
	}
	return c.withReviews(ctx, u, typ, out), nil
}

// ListOwnedComplete forwards the engine's non-franchise listing plus Year in
// Review collections and reports whether TMDB franchises belong to typ, so
// page-first collection endpoints never materialize every TMDB franchise.
func (c *reviewCollections) ListOwnedComplete(ctx context.Context, u *core.User, typ core.MediaType) ([]core.Collection, bool, error) {
	var out []core.Collection
	var includeFranchises bool
	if c.base != nil {
		owned, ok := c.base.(interface {
			ListOwnedComplete(context.Context, *core.User, core.MediaType) ([]core.Collection, bool, error)
		})
		if !ok {
			return nil, false, core.ErrNotAvailable
		}
		var err error
		out, includeFranchises, err = owned.ListOwnedComplete(ctx, u, typ)
		if err != nil {
			return nil, false, err
		}
	}
	return c.withReviews(ctx, u, typ, out), includeFranchises, nil
}

// ListPage forwards page-first collection paging (Plex /collections,
// /collection, /all?type=18) and appends the viewer's Year in Review
// collections after the base listing without materializing every franchise.
func (c *reviewCollections) ListPage(ctx context.Context, u *core.User, typ core.MediaType, offset, limit int) ([]core.Collection, int, error) {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 500
	}
	reviews := c.withReviews(ctx, u, typ, nil)
	if c.base == nil {
		if limit == 0 || offset >= len(reviews) {
			return nil, len(reviews), nil
		}
		end := min(offset+limit, len(reviews))
		return reviews[offset:end], len(reviews), nil
	}
	if pager, ok := c.base.(interface {
		ListPage(context.Context, *core.User, core.MediaType, int, int) ([]core.Collection, int, error)
	}); ok {
		page, baseTotal, err := pager.ListPage(ctx, u, typ, offset, limit)
		if err != nil {
			return nil, 0, err
		}
		total := baseTotal + len(reviews)
		if limit > 0 && len(page) < limit && len(reviews) > 0 {
			start := max(0, offset-baseTotal)
			if start < len(reviews) {
				rem := limit - len(page)
				end := min(start+rem, len(reviews))
				page = append(page, reviews[start:end]...)
			}
		}
		return page, total, nil
	}
	out, err := c.base.List(ctx, u, typ)
	if err != nil {
		return nil, 0, err
	}
	out = append(out, reviews...)
	total := len(out)
	if limit == 0 || offset >= total {
		return nil, total, nil
	}
	end := min(offset+limit, total)
	return out[offset:end], total, nil
}

// FindVisible forwards existence probes to the underlying engine before
// checking Year in Review collections, avoiding full franchise materialization.
func (c *reviewCollections) FindVisible(ctx context.Context, u *core.User, typ core.MediaType, accept func(core.Collection) (bool, error)) (bool, error) {
	if c.base != nil {
		if finder, ok := c.base.(interface {
			FindVisible(context.Context, *core.User, core.MediaType, func(core.Collection) (bool, error)) (bool, error)
		}); ok {
			found, err := finder.FindVisible(ctx, u, typ, accept)
			if err != nil || found {
				return found, err
			}
		}
	}
	for _, rev := range c.withReviews(ctx, u, typ, nil) {
		if accept == nil {
			return true, nil
		}
		yes, err := accept(rev)
		if err != nil || yes {
			return yes, err
		}
	}
	return false, nil
}

func (c *reviewCollections) withReviews(ctx context.Context, u *core.User, typ core.MediaType, out []core.Collection) []core.Collection {
	year := c.d.now().Year()
	if c.d.now().Month() < time.December {
		year--
	}
	types := []core.MediaType{typ}
	if typ == "" {
		types = []core.MediaType{core.TypeMovie, core.TypeShow}
	}
	for _, t := range types {
		if t != core.TypeMovie && t != core.TypeShow {
			continue
		}
		col, items, e := c.review(ctx, u, year, t)
		if e == nil && len(items) > 0 {
			out = append(out, *col)
		}
	}
	return out
}
func (c *reviewCollections) Get(ctx context.Context, u *core.User, key core.RatingKey) (*core.Collection, error) {
	if year, typ, ok := reviewCollectionParts(key); ok {
		col, _, e := c.review(ctx, u, year, typ)
		return col, e
	}
	if c.base == nil {
		return nil, core.ErrNotFound
	}
	return c.base.Get(ctx, u, key)
}
func (c *reviewCollections) Items(ctx context.Context, u *core.User, key core.RatingKey, offset, limit int) (core.Page, error) {
	if year, typ, ok := reviewCollectionParts(key); ok {
		_, items, e := c.review(ctx, u, year, typ)
		if e != nil {
			return core.Page{}, e
		}
		total := len(items)
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		if limit <= 0 {
			limit = total
		}
		end := offset + limit
		if end < offset || end > total {
			end = total
		}
		return core.Page{Items: items[offset:end], Total: total}, nil
	}
	if c.base == nil {
		return core.Page{}, core.ErrNotFound
	}
	return c.base.Items(ctx, u, key, offset, limit)
}
func (c *reviewCollections) CheckMembers(ctx context.Context, u *core.User, key core.RatingKey, want []core.RatingKey, scanLimit int) (*core.Collection, bool, error) {
	if len(want) == 0 || scanLimit <= 0 {
		return nil, false, nil
	}
	if year, typ, ok := reviewCollectionParts(key); ok {
		col, items, e := c.review(ctx, u, year, typ)
		if e != nil {
			return nil, false, e
		}
		wantSet := make(map[core.RatingKey]bool, len(want))
		for _, k := range want {
			wantSet[k] = true
		}
		for _, it := range items[:min(len(items), scanLimit)] {
			delete(wantSet, it.Key)
		}
		return col, len(wantSet) == 0, nil
	}
	if c.base == nil {
		return nil, false, core.ErrNotFound
	}
	if checker, ok := c.base.(interface {
		CheckMembers(context.Context, *core.User, core.RatingKey, []core.RatingKey, int) (*core.Collection, bool, error)
	}); ok {
		return checker.CheckMembers(ctx, u, key, want, scanLimit)
	}
	col, err := c.base.Get(ctx, u, key)
	if err != nil {
		return nil, false, err
	}
	wantSet := make(map[core.RatingKey]bool, len(want))
	for _, k := range want {
		wantSet[k] = true
	}
	for offset := 0; offset < scanLimit; {
		page, err := c.base.Items(ctx, u, key, offset, 100)
		if err != nil {
			return col, false, err
		}
		for _, it := range page.Items {
			delete(wantSet, it.Key)
		}
		if len(wantSet) == 0 {
			return col, true, nil
		}
		if len(page.Items) == 0 {
			return col, false, nil
		}
		offset += len(page.Items)
		if offset >= page.Total {
			return col, false, nil
		}
	}
	return col, false, nil
}
func (c *reviewCollections) CheckMembersBatch(ctx context.Context, u *core.User, keys []core.RatingKey, want map[core.RatingKey][]core.RatingKey, scanLimit int) (map[core.RatingKey]*core.Collection, map[core.RatingKey]bool, error) {
	cols := make(map[core.RatingKey]*core.Collection, len(keys))
	matched := make(map[core.RatingKey]bool, len(keys))
	var baseKeys []core.RatingKey
	for _, key := range keys {
		if year, typ, ok := reviewCollectionParts(key); ok {
			col, items, e := c.review(ctx, u, year, typ)
			if e != nil || col == nil {
				continue
			}
			cols[key] = col
			w := want[key]
			if len(w) > 0 && scanLimit > 0 {
				wantSet := make(map[core.RatingKey]bool, len(w))
				for _, k := range w {
					wantSet[k] = true
				}
				for _, it := range items[:min(len(items), scanLimit)] {
					delete(wantSet, it.Key)
				}
				matched[key] = len(wantSet) == 0
			}
			continue
		}
		baseKeys = append(baseKeys, key)
	}
	if len(baseKeys) == 0 || c.base == nil {
		return cols, matched, nil
	}
	if batch, ok := c.base.(interface {
		CheckMembersBatch(context.Context, *core.User, []core.RatingKey, map[core.RatingKey][]core.RatingKey, int) (map[core.RatingKey]*core.Collection, map[core.RatingKey]bool, error)
	}); ok {
		bcols, bmatched, err := batch.CheckMembersBatch(ctx, u, baseKeys, want, scanLimit)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range bcols {
			cols[k] = v
		}
		for k, v := range bmatched {
			matched[k] = v
		}
		return cols, matched, nil
	}
	for _, key := range baseKeys {
		w := want[key]
		if len(w) > 0 && scanLimit > 0 {
			col, ok, err := c.CheckMembers(ctx, u, key, w, scanLimit)
			if err == nil && col != nil {
				cols[key] = col
				matched[key] = ok
			}
		} else if col, err := c.base.Get(ctx, u, key); err == nil && col != nil {
			cols[key] = col
		}
	}
	return cols, matched, nil
}
func (c *reviewCollections) CreateSmart(ctx context.Context, col core.Collection) (*core.Collection, error) {
	if c.base == nil {
		return nil, core.ErrNotFound
	}
	return c.base.CreateSmart(ctx, col)
}
func (c *reviewCollections) DeleteSmart(ctx context.Context, key core.RatingKey) error {
	if _, _, ok := reviewCollectionParts(key); ok {
		return core.ErrNotFound
	}
	if c.base == nil {
		return core.ErrNotFound
	}
	return c.base.DeleteSmart(ctx, key)
}
func (c *reviewCollections) CollectionArtwork(ctx context.Context, u *core.User, key core.RatingKey, element string, width int) ([]byte, string, bool, error) {
	if _, _, ok := reviewCollectionParts(key); ok {
		return nil, "", false, nil
	}
	if source, ok := c.base.(core.CollectionArtworkSource); ok {
		return source.CollectionArtwork(ctx, u, key, element, width)
	}
	return nil, "", false, nil
}
