package favorites

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// TitleMembership reports which keys are in the viewer's title favourites
// without re-running title policy. Callers must already have established that
// the viewer may see every key (frontend adapters do, through the library's
// visibility filter); it exists because HasTitles' per-title revalidation
// loads every title and costs several queries per key on a list page.
func (s *Service) TitleMembership(ctx context.Context, viewerID int64, keys []core.RatingKey) (map[core.RatingKey]bool, error) {
	out := make(map[core.RatingKey]bool, len(keys))
	if viewerID <= 0 {
		return nil, ErrForbidden
	}
	for start := 0; start < len(keys); start += 400 {
		chunk := keys[start:min(start+400, len(keys))]
		args := make([]any, 0, len(chunk)+2)
		args = append(args, viewerID, string(KindTitle))
		marks := make([]string, len(chunk))
		for i, key := range chunk {
			marks[i] = "?"
			args = append(args, key.String())
		}
		rows, err := s.o.DB.QueryContext(ctx, `SELECT subject_id FROM favorites_items WHERE viewer_id=? AND kind=? AND subject_id IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if key, err := core.ParseRatingKey(id); err == nil {
				out[key] = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
