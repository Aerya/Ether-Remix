package submaker

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// ReadyReleases reports which releases already have a prepared subtitle for
// req, with one query for the whole title. It follows Cached: same
// settings, same scope and the same release binding. A release whose hash
// is not known yet matches on its exact name.
func (s *Service) ReadyReleases(ctx context.Context, req core.SearchRequest, rels []core.Release) []bool {
	c := Load()
	if s == nil || s.DB == nil || len(rels) == 0 || !c.Enabled || !s.Allowed(ctx, req) {
		return nil
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id,release_name FROM remix_submaker_jobs WHERE title_key=? AND language=? AND state='ready'", Key(req), c.Target)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids, names := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var id, name string
		if rows.Scan(&id, &name) == nil {
			ids[id] = true
			names[Canonical(name)] = true
		}
	}
	if rows.Err() != nil || len(ids) == 0 {
		return nil
	}
	out := make([]bool, len(rels))
	for i, r := range rels {
		out[i] = ids[fingerprint(c, req, r)] || (r.InfoHash == "" && names[Canonical(r.Name)])
	}
	return out
}
