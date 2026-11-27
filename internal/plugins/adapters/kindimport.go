package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// ImportSupport answers which playlist links a plugin library's kind reads
// and whether it identifies tracks (file imports need that).
func (c *KindCatalog) ImportSupport(ctx context.Context, lib core.Library) ([]string, bool) {
	if c == nil || c.items == nil || lib.Source != core.LibraryPlugin {
		return nil, false
	}
	rec, err := c.items.GetKindByLibrary(ctx, lib.ID)
	if err != nil || rec.Decl.Import == nil || !rec.Decl.Import.Identify {
		return nil, false
	}
	return rec.Decl.Import.Links, true
}

// ImportLink asks a plugin library's kind to read a playlist link.
func (c *KindCatalog) ImportLink(ctx context.Context, lib core.Library, url string) (core.KindImportResponse, error) {
	var out core.KindImportResponse
	rec, err := c.items.GetKindByLibrary(ctx, lib.ID)
	if err != nil || rec.Decl.Import == nil || len(rec.Decl.Import.Links) == 0 || c.mgr == nil {
		return out, core.ErrNotAvailable
	}
	body, _ := json.Marshal(core.KindImportRequest{URL: url})
	resp, err := c.mgr.Call(ctx, rec.PluginID, plugins.Request{Method: "POST", Path: "/ether/v1/kinds/" + rec.KindKey + "/import", Body: body})
	if err != nil {
		return out, err
	}
	switch resp.Status {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusNotFound:
		return out, core.ErrNotFound
	default:
		return out, fmt.Errorf("plugin import: status %d", resp.Status)
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return out, err
	}
	return out, nil
}

// IdentifyTracks asks a plugin library's kind to find its items for tracks,
// stores the items it returns (like live search results) and answers each
// answered track as stored keys. Indexes the plugin did not answer are
// absent, to be asked again.
func (c *KindCatalog) IdentifyTracks(ctx context.Context, lib core.Library, tracks []core.ImportTrack) ([]core.ImportMatch, error) {
	rec, err := c.items.GetKindByLibrary(ctx, lib.ID)
	if err != nil || rec.Decl.Import == nil || !rec.Decl.Import.Identify || c.mgr == nil {
		return nil, core.ErrNotAvailable
	}
	body, _ := json.Marshal(core.KindIdentifyRequest{Tracks: tracks})
	resp, err := c.mgr.Call(ctx, rec.PluginID, plugins.Request{Method: "POST", Path: "/ether/v1/kinds/" + rec.KindKey + "/identify", Body: body})
	if err != nil {
		return nil, err
	}
	if resp.Status != http.StatusOK {
		return nil, fmt.Errorf("plugin identify: status %d", resp.Status)
	}
	var page core.KindIdentifyResponse
	if err := json.Unmarshal(resp.Body, &page); err != nil {
		return nil, err
	}
	if len(page.Items) > 0 {
		if _, err := c.items.UpsertPinnedItems(ctx, rec.PluginID, rec.KindKey, page.Items); err != nil {
			return nil, err
		}
	}
	keyOf := func(id string) core.RatingKey {
		if id == "" {
			return 0
		}
		it, _, err := c.items.GetByExternal(ctx, rec.PluginID, rec.KindKey, id)
		if err != nil {
			return 0
		}
		return it.Key
	}
	out := make([]core.ImportMatch, 0, len(page.Matches))
	for _, m := range page.Matches {
		if m.Index < 0 || m.Index >= len(tracks) {
			continue
		}
		im := core.ImportMatch{Index: m.Index, Key: keyOf(m.ID), By: m.By}
		for _, cand := range m.Candidates {
			if k := keyOf(cand); k != 0 {
				im.Candidates = append(im.Candidates, k)
			}
		}
		if len(im.Candidates) > 8 {
			im.Candidates = im.Candidates[:8]
		}
		out = append(out, im)
	}
	return out, nil
}
