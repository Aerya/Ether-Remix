package livetv

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("livetv", 4, `CREATE TABLE IF NOT EXISTS livetv_numbers (source_id TEXT NOT NULL, channel_id TEXT NOT NULL, group_name TEXT NOT NULL, number TEXT NOT NULL, seen_ms BIGINT NOT NULL, PRIMARY KEY(source_id,channel_id));`)
}

// assignImportedNumbers reserves numbers for seven days after a channel disappears.
// With lineupUnique (plugin sources), a new channel whose number another
// source already uses takes the next free number above it instead, so a
// plugin's 101 never lands on top of a playlist's 101.
func assignImportedNumbers(ctx context.Context, tx *sql.Tx, source string, cs []Channel, old map[string]Channel, all []Channel, now time.Time, lineupUnique bool) error {
	cutoff := now.Add(-7 * 24 * time.Hour).UnixMilli()
	if _, err := tx.ExecContext(ctx, `DELETE FROM livetv_numbers WHERE seen_ms < ?`, cutoff); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT source_id, channel_id, group_name, number FROM livetv_numbers`)
	if err != nil {
		return err
	}
	type reservation struct{ group, number string }
	saved := map[string]reservation{}
	used := map[string]map[string]bool{}
	reserve := func(group, number string) {
		if number == "" {
			return
		}
		if used[group] == nil {
			used[group] = map[string]bool{}
		}
		used[group][number] = true
	}
	for rows.Next() {
		var sourceID, id, group, number string
		if err := rows.Scan(&sourceID, &id, &group, &number); err != nil {
			rows.Close()
			return err
		}
		if sourceID == source {
			saved[id] = reservation{group, number}
		}
		reserve(group, number)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	taken := map[string]bool{}
	for _, c := range all {
		if c.SourceID != source {
			reserve(c.Group, c.Number)
			taken[c.Number] = true
		}
	}
	for _, c := range cs {
		if previous, ok := old[c.ID]; ok {
			reserve(c.Group, previous.Number)
		} else if !c.numberMissing {
			reserve(c.Group, c.Number)
		}
	}
	if lineupUnique {
		// Keep every provider number that is free first, so only the
		// colliding channels move and the others keep their numbers.
		free := map[string]bool{}
		for _, c := range cs {
			if _, ok := old[c.ID]; ok {
				taken[old[c.ID].Number] = true
			} else if !c.numberMissing && !taken[c.Number] {
				free[c.Number] = true
			}
		}
		for n := range free {
			taken[n] = true
		}
		for i := range cs {
			if _, ok := old[cs[i].ID]; !ok && free[cs[i].Number] {
				delete(free, cs[i].Number)
				cs[i].numberKept = true
			}
		}
	}
	for i := range cs {
		c := &cs[i]
		if previous, ok := old[c.ID]; ok {
			c.Number = previous.Number
		} else if lineupUnique && !c.numberMissing && !c.numberKept && taken[c.Number] {
			if v, ok := saved[c.ID]; ok && !taken[v.number] {
				c.Number = v.number
			} else {
				n, err := strconv.Atoi(c.Number)
				if err != nil {
					n = 0
				}
				for n++; taken[strconv.Itoa(n)]; n++ {
				}
				c.Number = strconv.Itoa(n)
			}
		} else if c.Number == "" || c.numberMissing {
			if v, ok := saved[c.ID]; ok && v.group == c.Group {
				c.Number = v.number
			} else {
				for n := 1; ; n++ {
					value := fmt.Sprint(n)
					if !used[c.Group][value] {
						c.Number = value
						break
					}
				}
			}
		}
		reserve(c.Group, c.Number)
		taken[c.Number] = true
		_, err := tx.ExecContext(ctx, `INSERT INTO livetv_numbers(source_id,channel_id,group_name,number,seen_ms) VALUES(?,?,?,?,?) ON CONFLICT(source_id,channel_id) DO UPDATE SET group_name=excluded.group_name,number=excluded.number,seen_ms=excluded.seen_ms`, source, c.ID, c.Group, c.Number, now.UnixMilli())
		if err != nil {
			return err
		}
	}
	return nil
}
