package adapters

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// DuplicateTitles pages a plugin library's recordings with more than one
// copy, most copies first, as projected titles.
func (c *KindCatalog) DuplicateTitles(ctx context.Context, lib core.Library, offset, limit int) ([]experience.PluginDuplicateGroup, int, error) {
	if c == nil || c.items == nil || lib.Source != core.LibraryPlugin {
		return nil, 0, core.ErrNotFound
	}
	rec, err := c.items.GetKindByLibrary(ctx, lib.ID)
	if err != nil {
		return nil, 0, core.ErrNotFound
	}
	groups, total, err := c.items.Duplicates(ctx, lib.ID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]experience.PluginDuplicateGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, experience.PluginDuplicateGroup{Recording: g.Recording, Count: g.Count, Titles: c.projectItems(ctx, rec, lib, g.Items)})
	}
	return out, total, nil
}

// RecordingTitles lists every copy of the recording a plugin item carries
// in its library (itself included).
func (c *KindCatalog) RecordingTitles(ctx context.Context, key core.RatingKey) (core.Library, string, []core.Title, error) {
	if c == nil || c.items == nil || c.libs == nil || !key.IsPluginItemKey() {
		return core.Library{}, "", nil, core.ErrNotFound
	}
	item, libID, err := c.items.GetByKey(ctx, key)
	if err != nil || item.Recording == "" {
		return core.Library{}, "", nil, core.ErrNotFound
	}
	lib, err := c.libs.Get(ctx, libID)
	if err != nil {
		return core.Library{}, "", nil, core.ErrNotFound
	}
	rec, err := c.items.GetKindByLibrary(ctx, libID)
	if err != nil {
		return core.Library{}, "", nil, core.ErrNotFound
	}
	items, err := c.items.RecordingItems(ctx, libID, item.Recording)
	if err != nil {
		return core.Library{}, "", nil, err
	}
	return lib, item.Recording, c.projectItems(ctx, rec, lib, items), nil
}
