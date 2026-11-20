package releasedb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

const AssociationAuditVersion = 2

// AssociationRequests uses only local catalogue facts, never remote searches.
func AssociationRequests(ctx context.Context, db *sql.DB) (map[core.RatingKey]core.SearchRequest, error) {
	return AssociationRequestsFor(ctx, db, nil)
}

func AssociationRequestsFor(ctx context.Context, db *sql.DB, keys []core.RatingKey) (map[core.RatingKey]core.SearchRequest, error) {
	out := map[core.RatingKey]core.SearchRequest{}
	where := ""
	args := []any{}
	if keys != nil {
		raw, _ := json.Marshal(keys)
		where = " AND key IN (SELECT value FROM json_each(?))"
		args = append(args, string(raw))
	}
	rows, err := db.QueryContext(ctx, `SELECT key,type,tmdb_id,imdb,tvdb,title,original_title,year,language FROM catalog_titles WHERE type IN ('movie','show')`+where, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k core.RatingKey
		var q core.SearchRequest
		var lang string
		if err = rows.Scan(&k, &q.Type, &q.IDs.TMDB, &q.IDs.IMDB, &q.IDs.TVDB, &q.Title, &q.OriginalTitle, &q.Year, &lang); err != nil {
			break
		}
		q.Anime = lang == "ja"
		out[k] = q
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, `SELECT key,title FROM catalog_alt_titles WHERE 1=1`+where, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k core.RatingKey
		var name string
		if err = rows.Scan(&k, &name); err != nil {
			break
		}
		if q, ok := out[k]; ok {
			q.AlternativeTitles = append(q.AlternativeTitles, name)
			out[k] = q
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err == nil {
		for key, q := range out {
			out[key] = CheckSeriesIdentity(ctx, db, q)
		}
	}
	return out, err
}
func AssociationRequest(all map[core.RatingKey]core.SearchRequest, key core.RatingKey, season, episode int) (core.SearchRequest, bool) {
	if key.Kind() == core.KindEpisode {
		season, episode = key.Season(), key.Episode()
		key = core.ShowKey(key.TMDB())
	}
	q, ok := all[key]
	if q.Type == core.TypeShow && episode > 0 {
		q.Type = core.TypeEpisode
		q.Season = season
		q.Episode = episode
		if season == 1 {
			q.AbsoluteEpisode = episode
		}
	}
	return q, ok
}

type AssociationAudit struct {
	Checked     int                       `json:"checked"`
	Quarantined int                       `json:"quarantined"`
	Sources     map[string]map[string]int `json:"sources"`
}

// AuditAssociations can run read-only. Applying preserves every excluded row in
// a private quarantine table; no descriptor or private URL enters the report.
// Call with indexing stopped. Batches bound write transactions and memory.
func AuditAssociations(ctx context.Context, db *sql.DB, open func(string) (string, error), apply bool) (AssociationAudit, error) {
	report := AssociationAudit{Sources: map[string]map[string]int{}}
	requests, err := AssociationRequests(ctx, db)
	if err != nil {
		return report, err
	}
	if apply {
		if _, err = db.ExecContext(ctx, referenceBackfillSchema); err != nil {
			return report, err
		}
		if _, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO pse_reference_backfill(title_key) SELECT title_key FROM pse_releases UNION SELECT title_key FROM pse_index_titles UNION SELECT title_key FROM pse_release_library`); err != nil {
			return report, err
		}
	}
	for _, table := range []string{"pse_release_library", "pse_releases"} {
		if apply {
			if _, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+`_quarantine AS SELECT *, '' AS association_reason FROM `+table+` WHERE 0`); err != nil {
				return report, err
			}
		}
		var after int64
		for {
			query := `SELECT rowid,title_key,0,0,source_id,'',payload FROM pse_release_library WHERE rowid>? ORDER BY rowid LIMIT 500`
			if table == "pse_releases" {
				query = `SELECT rowid,title_key,season,episode,source_id,source_name,name FROM pse_releases WHERE rowid>? ORDER BY rowid LIMIT 500`
			}
			rows, e := db.QueryContext(ctx, query, after)
			if e != nil {
				return report, e
			}
			type rejected struct {
				id     int64
				reason string
			}
			var bad []rejected
			var stable []int64
			n := 0
			for rows.Next() {
				var id int64
				var key core.RatingKey
				var season, episode int
				var source, label, raw string
				if e = rows.Scan(&id, &key, &season, &episode, &source, &label, &raw); e != nil {
					break
				}
				after = id
				n++
				report.Checked++
				q, known := AssociationRequest(requests, key, season, episode)
				rel := core.Release{Name: raw}
				reason := ""
				if table == "pse_release_library" {
					plain, x := open(raw)
					if x != nil {
						reason = "unreadable_descriptor"
					} else if json.Unmarshal([]byte(plain), &rel) != nil {
						reason = "invalid_descriptor"
					}
					label = rel.Indexer
				}
				if reason == "" {
					if !known {
						reason = "missing_catalogue"
					} else {
						reason = releaseparse.AssociationReason(q, rel)
						if reason == "" && table == "pse_release_library" {
							reason = releaseparse.ReferenceReason(rel)
						}
					}
				}
				if label == "" {
					label = source
				}
				group := table + ":" + label
				if report.Sources[group] == nil {
					report.Sources[group] = map[string]int{}
				}
				status := reason
				if status == "" {
					status = "confirmed"
					if table == "pse_release_library" && releaseparse.StableReference(rel) {
						stable = append(stable, id)
					}
				}
				report.Sources[group][status]++
				if reason != "" {
					report.Quarantined++
					bad = append(bad, rejected{id, reason})
				}
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return report, e
			}
			if apply && len(stable) > 0 {
				raw, _ := json.Marshal(stable)
				if _, e = db.ExecContext(ctx, `UPDATE pse_release_library SET expires=? WHERE rowid IN (SELECT value FROM json_each(?))`, StableReferenceExpiry, string(raw)); e != nil {
					return report, e
				}
			}
			if apply && len(bad) > 0 {
				tx, e := db.BeginTx(ctx, nil)
				if e != nil {
					return report, e
				}
				for _, b := range bad {
					if _, e = tx.ExecContext(ctx, `INSERT INTO `+table+`_quarantine SELECT *,? FROM `+table+` WHERE rowid=?`, b.reason, b.id); e == nil {
						_, e = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid=?`, b.id)
					}
					if e != nil {
						break
					}
				}
				if e != nil {
					tx.Rollback()
					return report, e
				}
				if e = tx.Commit(); e != nil {
					return report, e
				}
			}
			if n == 0 {
				break
			}
		}
	}
	if apply {
		// Old positive visibility evidence must be evaluated under the new rule.
		for _, table := range []string{"resolver_availability", "availability_gate"} {
			var exists bool
			if e := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&exists); e != nil {
				return report, e
			}
			if exists {
				if _, e := db.ExecContext(ctx, `DELETE FROM `+table); e != nil {
					return report, e
				}
			}
		}
		// Derived availability must be rebuilt from the remaining confirmed rows.
		if _, err = db.ExecContext(ctx, `DELETE FROM pse_playable; DELETE FROM pse_state WHERE k='playable_signature'; UPDATE pse_index_titles SET found=(SELECT COUNT(*) FROM pse_releases r WHERE r.title_key=pse_index_titles.title_key)`); err != nil {
			return report, err
		}
		var totalQuarantined int
		if err = db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM pse_releases_quarantine)+(SELECT COUNT(*) FROM pse_release_library_quarantine)`).Scan(&totalQuarantined); err != nil {
			return report, err
		}
		_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS pse_association_audit(version INTEGER PRIMARY KEY,checked INTEGER NOT NULL,quarantined INTEGER NOT NULL); INSERT OR IGNORE INTO pse_association_audit VALUES(?, ?,?)`, AssociationAuditVersion, report.Checked+totalQuarantined-report.Quarantined, totalQuarantined)
		if err != nil {
			return report, fmt.Errorf("record association audit: %w", err)
		}
	}
	return report, nil
}
