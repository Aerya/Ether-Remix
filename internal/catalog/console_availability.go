package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("catalog", 39, `CREATE INDEX catalog_release_evidence_quality_cover ON catalog_release_evidence(policy,verified_until,height,hdr,dv,atmos,cached,cache_until,rating_key);`)
	store.Register("catalog", 37, `
CREATE TABLE catalog_release_evidence (
 rating_key INTEGER NOT NULL, release_id TEXT NOT NULL, source_id TEXT NOT NULL,
 info_hash TEXT NOT NULL DEFAULT '', policy TEXT NOT NULL, verified_until INTEGER NOT NULL DEFAULT 0,
 cache_until INTEGER NOT NULL DEFAULT 0, cached INTEGER NOT NULL DEFAULT -1,
 height INTEGER NOT NULL DEFAULT 0, hdr INTEGER NOT NULL DEFAULT 0,
 dv INTEGER NOT NULL DEFAULT 0, atmos INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(rating_key,release_id,source_id)
);
CREATE INDEX catalog_release_evidence_playable ON catalog_release_evidence(policy,verified_until,rating_key);
CREATE INDEX catalog_release_evidence_cache ON catalog_release_evidence(policy,cached,cache_until,rating_key);
CREATE INDEX catalog_release_evidence_expired ON catalog_release_evidence(verified_until,cache_until,rating_key,release_id,source_id);
CREATE INDEX catalog_release_evidence_title ON catalog_release_evidence(rating_key,policy,verified_until,height,hdr,dv,atmos);
`)
	store.Register("catalog", 61, `
CREATE TABLE catalog_release_audio_langs (
 rating_key BIGINT NOT NULL,
 release_id TEXT NOT NULL,
 source_id TEXT NOT NULL,
 policy TEXT NOT NULL,
 verified_until BIGINT NOT NULL DEFAULT 0,
 lang TEXT NOT NULL,
 PRIMARY KEY(rating_key, release_id, source_id, lang)
);
CREATE INDEX catalog_release_audio_langs_cover ON catalog_release_audio_langs(policy, verified_until, lang, rating_key);
CREATE INDEX catalog_release_audio_langs_release ON catalog_release_audio_langs(release_id);
CREATE INDEX IF NOT EXISTS catalog_titles_type_browse_language_released ON catalog_titles(type, browsable, adult, language, released_ts DESC, key ASC);
CREATE INDEX IF NOT EXISTS catalog_titles_type_browse_content_rating ON catalog_titles(type, browsable, adult, content_rating, released_ts DESC, key ASC);
CREATE INDEX IF NOT EXISTS catalog_titles_type_browse_rating_adult ON catalog_titles(type, browsable, adult, rating DESC, released_ts DESC, key ASC);
CREATE INDEX IF NOT EXISTS catalog_titles_type_browse_added_adult ON catalog_titles(type, browsable, adult, added_at DESC, key ASC);
`)
}

func normalizeEvidenceAudioLanguage(raw string) string {
	canon := langtag.CanonicalBCP47(raw)
	if canon == "" {
		return ""
	}
	return langtag.Normalize(canon)
}

// SetConsoleAvailabilityPolicy binds the same cached policy stamp used by the
// availability service. A changed source/quality configuration retracts evidence
// immediately, without rewriting every title or parsing release payloads.
func (c *Catalog) SetConsoleAvailabilityPolicy(policy func() string) {
	c.consoleEvidencePolicy.Store(&policy)
}
func (c *Catalog) evidencePolicy() string {
	if p := c.consoleEvidencePolicy.Load(); p != nil {
		return (*p)()
	}
	return ""
}

// RecordConsoleReleaseEvidence stores normalized observations on ingestion or
// verification. cached=-1 means unknown; addon cache hints never enter here.
func (c *Catalog) RecordConsoleReleaseEvidence(ctx context.Context, key core.RatingKey, r core.Release, source, policy string, verifiedUntil, cacheUntil int64, cached int) error {
	if key == 0 || r.ID == "" {
		return nil
	}
	hdr, dv, atmos := 0, 0, 0
	if r.Info.HDR10 {
		hdr = 1
	}
	if r.Info.DolbyVision {
		dv = 1
	}
	if r.Info.Atmos {
		atmos = 1
	}
	// Unverified observations without a definitive cache check carry no console
	// signal; update or clear any prior row for that release without inserting
	// an already-expired tombstone on every feed item.
	if verifiedUntil <= 0 && cacheUntil <= 0 && cached < 0 {
		res, err := c.db.ExecContext(ctx, `UPDATE catalog_release_evidence SET
			cache_until=CASE WHEN policy=? AND info_hash=? THEN cache_until ELSE 0 END,
			cached=CASE WHEN policy=? AND info_hash=? THEN cached ELSE -1 END,
			info_hash=?,policy=?,verified_until=0,height=?,hdr=?,dv=?,atmos=?
			WHERE rating_key=? AND release_id=? AND source_id=?`,
			policy, r.InfoHash, policy, r.InfoHash, r.InfoHash, policy, r.Info.Height, hdr, dv, atmos, int64(key), r.ID, source)
		if err != nil {
			return err
		}
		_, _ = c.db.ExecContext(ctx, `DELETE FROM catalog_release_audio_langs WHERE rating_key=? AND release_id=? AND source_id=?`, int64(key), r.ID, source)
		if n, _ := res.RowsAffected(); n > 0 {
			_, _ = c.db.ExecContext(ctx, `DELETE FROM catalog_release_evidence WHERE rating_key=? AND release_id=? AND source_id=? AND verified_until<=0 AND cache_until<=0`, int64(key), r.ID, source)
			c.writeGen.Add(1)
		}
		return nil
	}
	_, err := c.db.ExecContext(ctx, `INSERT INTO catalog_release_evidence(rating_key,release_id,source_id,info_hash,policy,verified_until,cache_until,cached,height,hdr,dv,atmos) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(rating_key,release_id,source_id) DO UPDATE SET info_hash=excluded.info_hash,policy=excluded.policy,verified_until=excluded.verified_until,cache_until=CASE WHEN excluded.cached<0 AND catalog_release_evidence.policy=excluded.policy AND catalog_release_evidence.info_hash=excluded.info_hash THEN catalog_release_evidence.cache_until ELSE excluded.cache_until END,cached=CASE WHEN excluded.cached<0 AND catalog_release_evidence.policy=excluded.policy AND catalog_release_evidence.info_hash=excluded.info_hash THEN catalog_release_evidence.cached ELSE excluded.cached END,height=excluded.height,hdr=excluded.hdr,dv=excluded.dv,atmos=excluded.atmos`, int64(key), r.ID, source, r.InfoHash, policy, verifiedUntil, cacheUntil, cached, r.Info.Height, hdr, dv, atmos)
	if err != nil {
		return err
	}
	_, _ = c.db.ExecContext(ctx, `DELETE FROM catalog_release_audio_langs WHERE rating_key=? AND release_id=? AND source_id=?`, int64(key), r.ID, source)
	if verifiedUntil > 0 && len(r.Info.Languages) > 0 {
		seen := make(map[string]struct{}, len(r.Info.Languages))
		for _, rawLang := range r.Info.Languages {
			norm := normalizeEvidenceAudioLanguage(rawLang)
			if norm == "" {
				continue
			}
			if _, dup := seen[norm]; dup {
				continue
			}
			seen[norm] = struct{}{}
			_, _ = c.db.ExecContext(ctx, `INSERT INTO catalog_release_audio_langs(rating_key,release_id,source_id,policy,verified_until,lang) VALUES(?,?,?,?,?,?) ON CONFLICT(rating_key,release_id,source_id,lang) DO UPDATE SET policy=excluded.policy,verified_until=excluded.verified_until`, int64(key), r.ID, source, policy, verifiedUntil, norm)
		}
	}
	c.writeGen.Add(1)
	return nil
}

func (c *Catalog) InvalidateConsoleReleaseEvidence(ctx context.Context, id string) {
	_, _ = c.db.ExecContext(ctx, `DELETE FROM catalog_release_audio_langs WHERE release_id=?`, id)
	_, _ = c.db.ExecContext(ctx, `DELETE FROM catalog_release_evidence WHERE release_id=?`, id)
	c.writeGen.Add(1)
}

func (c *Catalog) consoleAvailabilityConditions(f core.Filter) ([]string, []any) {
	if f.Playable == "" && f.DebridCached == "" && len(f.Qualities) == 0 && len(f.EvidenceResolutions) == 0 && len(f.EvidenceHDR) == 0 && len(f.EvidenceAudioLanguages) == 0 {
		return nil, nil
	}
	var conds []string
	var args []any
	now := c.now().Unix()
	policy := c.evidencePolicy()
	if f.Playable == "no" || f.DebridCached == "no" {
		return []string{"1=0"}, nil
	}
	hasVerifiedSubquery := len(f.Qualities) > 0 || len(f.EvidenceResolutions) > 0 || len(f.EvidenceHDR) > 0 || len(f.EvidenceAudioLanguages) > 0
	playable := `t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.verified_until>?)`
	if f.Playable != "" && !(f.Playable == "yes" && hasVerifiedSubquery) {
		if f.Playable == "unknown" {
			playable = "NOT (" + playable + ")"
		}
		conds = append(conds, playable)
		args = append(args, policy, now)
	}
	if f.DebridCached != "" && !(f.DebridCached == "yes" && len(f.Qualities) > 0) {
		sql := `t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.cached=1 AND ev.cache_until>?)`
		if f.DebridCached == "unknown" {
			sql = `t.key NOT IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.cached>=0 AND ev.cache_until>?)`
		}
		conds = append(conds, sql)
		args = append(args, policy, now)
	}
	if len(f.Qualities) > 0 {
		q := []string{}
		for _, quality := range f.Qualities {
			switch quality {
			case "4k":
				q = append(q, "ev.height>=2160")
			case "hdr":
				q = append(q, "(ev.hdr=1 OR ev.dv=1)")
			case "dv":
				q = append(q, "ev.dv=1")
			case "atmos":
				q = append(q, "ev.atmos=1")
			default:
				return []string{"1=0"}, nil
			}
		}
		if f.DebridCached == "yes" {
			q = append(q, "ev.cached=1 AND ev.cache_until>?")
		}
		conds = append(conds, fmt.Sprintf(`t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.verified_until>? AND %s)`, strings.Join(q, " AND ")))
		args = append(args, policy, now)
		if f.DebridCached == "yes" {
			args = append(args, now)
		}
	}
	if len(f.EvidenceResolutions) > 0 {
		var parts []string
		for _, res := range f.EvidenceResolutions {
			switch strings.ToLower(strings.TrimSpace(res)) {
			case "4k":
				parts = append(parts, "ev.height>=2160")
			case "1080p":
				parts = append(parts, "(ev.height>=1080 AND ev.height<2160)")
			case "720p":
				parts = append(parts, "(ev.height>=720 AND ev.height<1080)")
			case "sd":
				parts = append(parts, "(ev.height>0 AND ev.height<720)")
			default:
				return []string{"1=0"}, nil
			}
		}
		conds = append(conds, fmt.Sprintf(`t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.verified_until>? AND (%s))`, strings.Join(parts, " OR ")))
		args = append(args, policy, now)
	}
	if len(f.EvidenceHDR) > 0 {
		var parts []string
		for _, h := range f.EvidenceHDR {
			switch strings.ToLower(strings.TrimSpace(h)) {
			case "dv":
				parts = append(parts, "ev.dv=1")
			case "hdr10":
				parts = append(parts, "(ev.hdr=1 AND ev.dv=0)")
			case "sdr":
				parts = append(parts, "(ev.height>0 AND ev.hdr=0 AND ev.dv=0)")
			default:
				return []string{"1=0"}, nil
			}
		}
		conds = append(conds, fmt.Sprintf(`t.key IN (SELECT ev.rating_key FROM catalog_release_evidence ev WHERE ev.policy=? AND ev.verified_until>? AND (%s))`, strings.Join(parts, " OR ")))
		args = append(args, policy, now)
	}
	if len(f.EvidenceAudioLanguages) > 0 {
		var langs []any
		seen := make(map[string]struct{}, len(f.EvidenceAudioLanguages))
		for _, raw := range f.EvidenceAudioLanguages {
			norm := normalizeEvidenceAudioLanguage(raw)
			if norm == "" {
				return []string{"1=0"}, nil
			}
			if _, dup := seen[norm]; !dup {
				seen[norm] = struct{}{}
				langs = append(langs, norm)
			}
		}
		conds = append(conds, fmt.Sprintf(`t.key IN (SELECT al.rating_key FROM catalog_release_audio_langs al WHERE al.policy=? AND al.verified_until>? AND al.lang IN (%s))`, placeholders(len(langs))))
		args = append(args, policy, now)
		args = append(args, langs...)
	}
	return conds, args
}

// PruneConsoleReleaseEvidence drops at most one fixed-size expired chunk per
// monitor cycle. It never scans the title library or blocks a browse request.
func (c *Catalog) PruneConsoleReleaseEvidence(ctx context.Context, now int64) error {
	_, _ = c.db.ExecContext(ctx, `DELETE FROM catalog_release_audio_langs WHERE (rating_key,release_id,source_id,lang) IN (SELECT rating_key,release_id,source_id,lang FROM catalog_release_audio_langs WHERE verified_until<=? ORDER BY verified_until,rating_key,release_id,source_id,lang LIMIT 500)`, now)
	result, err := c.db.ExecContext(ctx, `DELETE FROM catalog_release_evidence WHERE (rating_key,release_id,source_id) IN (SELECT rating_key,release_id,source_id FROM catalog_release_evidence WHERE verified_until<=? AND cache_until<=? ORDER BY verified_until,rating_key,release_id,source_id LIMIT 500)`, now, now)
	if err == nil {
		if n, _ := result.RowsAffected(); n > 0 {
			c.writeGen.Add(1)
		}
	}
	return err
}

// Prune drains expired console release evidence in bounded batches during
// daily maintenance.
func (c *Catalog) Prune(ctx context.Context) (int64, error) {
	now := c.now().Unix()
	_, _ = store.PruneBatched(ctx, c.db, `DELETE FROM catalog_release_audio_langs WHERE (rating_key,release_id,source_id,lang) IN (SELECT rating_key,release_id,source_id,lang FROM catalog_release_audio_langs WHERE verified_until<=? ORDER BY verified_until,rating_key,release_id,source_id,lang LIMIT ?)`, now)
	n, err := store.PruneBatched(ctx, c.db, `DELETE FROM catalog_release_evidence WHERE (rating_key,release_id,source_id) IN (SELECT rating_key,release_id,source_id FROM catalog_release_evidence WHERE verified_until<=? AND cache_until<=? ORDER BY verified_until,rating_key,release_id,source_id LIMIT ?)`, now, now)
	if n > 0 {
		c.writeGen.Add(1)
	}
	return n, err
}
