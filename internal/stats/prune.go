package stats

import "context"

// dailyRetentionDays bounds the per-day indexer, provider and debrid counters. The
// admin UI reads the current month; two years keeps year-over-year
// comparisons possible without letting the tables grow forever. Plays
// (stats_plays) are user history and are never pruned here; their
// stats_play_timeline rows are kept for as long as the stats_plays row exists.
const dailyRetentionDays = 730

// Prune deletes per-day indexer/provider counters older than
// dailyRetentionDays, plus orphaned timeline entries from unmaterialised
// sessions older than timelineOrphanMaxAge. It is meant to be called periodically.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	now := s.opts.Now().UTC()
	cutoff := now.AddDate(0, 0, -dailyRetentionDays).Format("2006-01-02")
	var total int64
	for _, q := range []string{
		`DELETE FROM stats_indexer_days WHERE day<?`,
		`DELETE FROM stats_provider_days WHERE day<?`,
		`DELETE FROM stats_debrid_days WHERE day<?`,
		`DELETE FROM stats_stream_days WHERE day<?`,
	} {
		res, err := s.db.ExecContext(ctx, q, cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM stats_play_timeline WHERE at_ms<? AND NOT EXISTS (SELECT 1 FROM stats_plays WHERE stats_plays.id=stats_play_timeline.play_id)`,
		now.Add(-timelineOrphanMaxAge).UnixMilli(),
	)
	if err != nil {
		return total, err
	}
	n, _ := res.RowsAffected()
	total += n
	return total, nil
}
