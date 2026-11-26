package collections

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// pageRows serves every enabled collection as a page row (LayoutRow), the
// way a Recommendations page that places them all would fetch them.
func pageRows(ctx context.Context, s *Service, user *core.User) ([]core.CollectionRow, error) {
	rows, err := s.loadRows(ctx)
	if err != nil {
		return nil, err
	}
	var out []core.CollectionRow
	for _, r := range rows {
		if !r.enabled {
			continue
		}
		cr, err := s.LayoutRow(ctx, user, core.HubContext{}, r.publicID())
		if err != nil || cr == nil {
			continue
		}
		out = append(out, *cr)
	}
	return out, nil
}
