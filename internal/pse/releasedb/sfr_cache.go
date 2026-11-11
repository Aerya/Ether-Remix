package releasedb

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_releases", 10, `
ALTER TABLE pse_releases ADD COLUMN cache_until INTEGER NOT NULL DEFAULT 0;
CREATE INDEX pse_release_cache_expiry ON pse_releases(cache_until) WHERE cache_until>0 AND debrid_cached<>'';
UPDATE pse_releases SET debrid_cached='' WHERE source_id='sfr-db';
DELETE FROM pse_state WHERE k='playable_signature';
UPDATE pse_index_titles SET next_due=0 WHERE title_key IN (SELECT title_key FROM pse_releases WHERE source_id='sfr-db');
`)
}

func (p *pgxSFR) attachCache(ctx context.Context, rs []sfrRow) {
	if !p.cacheAvailable || len(rs) == 0 {
		return
	}
	hashes := make([]string, 0, len(rs))
	for _, r := range rs {
		hashes = append(hashes, strings.ToLower(r.Hash))
	}
	rows, err := p.conn.Query(ctx, `SELECT info_hash,service,expires_at::bigint FROM public.debrid_cache WHERE info_hash=ANY($1::text[]) AND expires_at > extract(epoch from now())`, hashes)
	if err != nil {
		return
	} // Unknown is safer than claiming availability.
	defer rows.Close()
	type proof struct {
		services []string
		until    int64
	}
	proofs := map[string]proof{}
	for rows.Next() {
		var hash, service string
		var until int64
		if rows.Scan(&hash, &service, &until) != nil {
			return
		}
		service = strings.ToLower(service)
		switch service {
		case "alldebrid", "realdebrid", "torbox", "premiumize":
		default:
			continue
		}
		v := proofs[hash]
		v.services = append(v.services, service)
		if v.until == 0 || until < v.until {
			v.until = until
		}
		proofs[hash] = v
	}
	if rows.Err() != nil {
		return
	}
	for i := range rs {
		v := proofs[strings.ToLower(rs[i].Hash)]
		rs[i].Cached = v.services
		rs[i].CacheUntil = v.until
	}
}

// Expire proofs even between imports. Materialized catalogue counts follow
// within one minute; direct reads discard expired hints immediately.
func (s *Service) expireSFRCache(ctx context.Context) error {
	cutoff := s.d.Now().Unix()
	rows, err := s.st.db.QueryContext(ctx, `SELECT DISTINCT title_key FROM pse_releases WHERE cache_until>0 AND cache_until<=? AND debrid_cached<>''`, cutoff)
	if err != nil {
		return err
	}
	var keys []core.RatingKey
	for rows.Next() {
		var k int64
		if err = rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, core.RatingKey(k))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	_, err = s.st.db.ExecContext(ctx, `UPDATE pse_releases SET debrid_cached='' WHERE cache_until>0 AND cache_until<=? AND debrid_cached<>''`, cutoff)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err = s.st.RefreshPlayable(ctx, k, Playable()); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) watchSFRCache(ctx context.Context) {
	for {
		_ = s.expireSFRCache(ctx)
		if !sleepCtx(ctx, time.Minute) {
			return
		}
	}
}
