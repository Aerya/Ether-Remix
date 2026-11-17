package collections

import (
	"context"
	"encoding/json"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
)

// authorizedMembers applies current folder permissions after shared evaluation
// and before personal sorting/limits. Never cache a viewer's filtered membership
// in the shared build. A title contributed by an independent public source stays
// visible without granting access to its private local file.
func (s *Service) authorizedMembers(ctx context.Context, row *specRow, keys []core.RatingKey, viewer int64) ([]core.RatingKey, error) {
	// Unmatched private videos never become public through a catalog/titles source.
	filtered := make([]core.RatingKey, 0, len(keys))
	viewerCtx := core.WithCatalogViewer(ctx, &core.User{ID: viewer})
	for _, key := range keys {
		if !localmedia.IsKey(key) || localmedia.Known(viewerCtx, s.db, key) {
			filtered = append(filtered, key)
		}
	}
	keys = filtered
	roots := []string{}
	for _, src := range row.spec.Sources {
		if src.Kind == "localFiles" {
			roots = append(roots, "local:"+src.Label)
		}
	}
	if len(roots) == 0 {
		return keys, nil
	}
	allowed := map[string]bool{"": true}
	all := true
	for _, root := range roots {
		allowed[root] = mediaaccess.Allowed(ctx, s.db, root, viewer)
		all = all && allowed[root]
	}
	if all {
		return keys, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT key,origins_json FROM collections_members WHERE spec_id=?", row.id)
	if err != nil {
		return nil, err
	}
	visible := map[core.RatingKey]bool{}
	for rows.Next() {
		var key core.RatingKey
		var raw string
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var origins []string
		// Legacy/malformed provenance fails closed until a background rebuild.
		if json.Unmarshal([]byte(raw), &origins) != nil {
			continue
		}
		for _, origin := range origins {
			if allowed[origin] {
				visible[key] = true
				break
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]core.RatingKey, 0, len(keys))
	for _, key := range keys {
		if visible[key] {
			out = append(out, key)
		}
	}
	return out, nil
}
