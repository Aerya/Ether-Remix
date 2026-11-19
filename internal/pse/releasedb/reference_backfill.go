package releasedb

import (
	"context"
	"database/sql"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"time"
)

const referenceBackfillSchema = `CREATE TABLE IF NOT EXISTS pse_reference_backfill(title_key INTEGER PRIMARY KEY, next_due INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0); CREATE INDEX IF NOT EXISTS pse_reference_backfill_due ON pse_reference_backfill(next_due,title_key);`

func init() { store.Register("pse_reference_backfill", 1, referenceBackfillSchema) }

// Revisit old metadata through normal, budgeted searches. Never fabricate a
// locator from its name. Failed/missing sources remain queued for a later pass.
func (s *Service) nextReferenceBackfill(ctx context.Context, typ core.MediaType, now time.Time) (core.Title, bool, error) {
	// An empty/not-due queue needs no catalogue lookup. This also keeps
	// metadata-only indexer integrations independent from the catalogue.
	var due int
	err := s.st.db.QueryRowContext(ctx, `SELECT 1 FROM pse_reference_backfill WHERE next_due<=? LIMIT 1`, now.Unix()).Scan(&due)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Title{}, false, nil
	}
	if err != nil {
		return core.Title{}, false, err
	}
	for range 8 {
		var key core.RatingKey
		err := s.st.db.QueryRowContext(ctx, `SELECT b.title_key FROM pse_reference_backfill b JOIN catalog_titles c ON c.key=CASE WHEN b.title_key/1000000000000000=4 THEN 2000000000000000+(b.title_key%1000000000000000)/10000000 ELSE b.title_key END WHERE c.type=? AND b.next_due<=? ORDER BY b.next_due,b.title_key LIMIT 1`, typ, now.Unix()).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return core.Title{}, false, nil
		}
		if err != nil {
			return core.Title{}, false, err
		}

		catalogKey := key
		if key.Kind() == core.KindEpisode {
			catalogKey = core.ShowKey(key.TMDB())
		}
		requests, err := AssociationRequestsFor(ctx, s.st.db, []core.RatingKey{catalogKey})
		if err != nil {
			return core.Title{}, false, err
		}
		q, ok := AssociationRequest(requests, key, 0, 0)
		if !ok {
			return core.Title{}, false, nil
		}
		if _, err = s.st.db.ExecContext(ctx, `UPDATE pse_reference_backfill SET next_due=?,attempts=attempts+1 WHERE title_key=?`, now.Add(24*time.Hour).Unix(), key); err != nil {
			return core.Title{}, false, err
		}
		return core.Title{Key: key, Type: q.Type, IDs: q.IDs, Title: q.Title, OriginalTitle: q.OriginalTitle, Year: q.Year, Season: q.Season, Episode: q.Episode, AbsoluteEpisode: q.AbsoluteEpisode, AlternativeTitles: q.AlternativeTitles}, true, nil
	}
	return core.Title{}, false, nil
}
