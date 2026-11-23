package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A cold page stores its minimum provider payload in one short transaction.
// Reuse the ordinary upsert's column/rating precedence contract, but bind many
// rows per statement and batch facet/credit maintenance instead of upsertOne's
// per-title side work. The background enrichment path still uses small chunks.
func (c *Catalog) writeLocalPage(ctx context.Context, items []upsertItem) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(ctx, detailWriteBudget)
	defer cancel()
	now := c.now().Unix()
	template := titleUpsertSQL()
	begin := strings.Index(template, "\nVALUES ")
	end := strings.Index(template, "\nON CONFLICT")
	if begin < 0 || end <= begin {
		return fmt.Errorf("catalog: malformed titleUpsertSQL")
	}
	columns := strings.Count(template[begin:end], "?")
	type group struct {
		rows []preparedTitleUpsert
		tail []any
	}
	groups := map[string]*group{}
	joins := &joinBatch{}
	for _, item := range items {
		o := item.o
		o.region = c.config().Region
		row, e := prepareTitleUpsert(item.t, o, now)
		if e != nil {
			return e
		}
		key := fmt.Sprint(row.args[columns:])
		g := groups[key]
		if g == nil {
			g = &group{tail: row.args[columns:]}
			groups[key] = g
		}
		g.rows = append(g.rows, row)
		if e := joins.add(ctx, tx, row.t, now); e != nil {
			return e
		}
	}
	for _, g := range groups {
		// ~45 parameters per row: 500 stays below both engines' bind limits.
		for start := 0; start < len(g.rows); start += 500 {
			part := g.rows[start:min(start+500, len(g.rows))]
			args := make([]any, 0, len(part)*columns+len(g.tail))
			for _, row := range part {
				args = append(args, row.args[:columns]...)
			}
			args = append(args, g.tail...)
			q := template[:begin] + "\nVALUES " + valueRows(len(part), columns) + template[end:]
			if _, err := tx.ExecContext(ctx, q, args...); err != nil {
				return err
			}
		}
	}
	if err := joins.flush(ctx, tx, now); err != nil {
		return err
	}
	keys := make([]core.RatingKey, len(items))
	for i, item := range items {
		keys[i] = item.t.Key
		if item.t.Type == core.TypeShow {
			if err := updateReleaseNames(ctx, tx, int64(item.t.Key), item.t.Type); err != nil {
				return err
			}
		}
	}
	membership, args := c.keyMembership("catalog_titles.key", false, keys)
	released := map[core.RatingKey]time.Time{}
	rows, err := tx.QueryContext(ctx, `UPDATE catalog_titles SET released_ts=(SELECT earliest_ts FROM catalog_episode_release WHERE key=catalog_titles.key),browsable=1 WHERE `+membership+` AND EXISTS(SELECT 1 FROM catalog_episode_release e WHERE e.key=catalog_titles.key AND (catalog_titles.released_ts=0 OR e.earliest_ts<catalog_titles.released_ts)) RETURNING key,released_ts`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, at int64
		if err := rows.Scan(&key, &at); err != nil {
			rows.Close()
			return err
		}
		released[core.RatingKey(key)] = time.Unix(at, 0)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	gen := c.writeGen.Add(1)
	titles := make([]core.Title, len(items))
	for i, item := range items {
		prepared, _ := prepareTitleUpsert(item.t, item.o, now)
		titles[i] = prepared.t
		if effective, ok := released[item.t.Key]; ok {
			titles[i].Released = effective
		}
	}
	// Own the freshly stored payload rather than reading it all back on the
	// same cold request. Parent numbering and date-added facts remain exact.
	if err := c.hydrateShows(ctx, titles); err != nil {
		return err
	}
	if err := c.applyHydratedAdded(ctx, titles); err != nil {
		return err
	}
	c.noteSearchWrites(titles)
	if c.writeGen.Load() == gen {
		c.cacheMu.Lock()
		for i, item := range items {
			var detailAt time.Time
			if item.o.detail {
				detailAt = time.Unix(now, 0)
			}
			delete(c.titleCache, item.t.Key)
			c.putSnapshotLocked(item.t.Key, gen, c.minuteBucket(), storedTitle{t: cloneTitle(titles[i]), detail: item.o.detail, detailAt: detailAt, fetchedAt: time.Unix(now, 0), browsable: item.o.browsable || !released[item.t.Key].IsZero(), metaKnown: true})
		}
		c.cacheMu.Unlock()
	}
	return nil
}
