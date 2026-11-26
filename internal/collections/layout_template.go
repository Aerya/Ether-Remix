package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
	"time"
)

// Explicit allowlist: never restore source URLs, ownership or audience from a
// presentation template. The current spec supplies all non-visual fields.
var layoutFields = []string{"name", "description", "artwork", "rotate", "limit", "sort", "sortDesc", "rankedTop10", "navigationGroup", "navigationOrder", "previewOnFocus", "enabled", "browseInApps"}

func (s *Service) LayoutTemplatePart() layouts.Part {
	type item struct {
		ID      int64
		Fields  map[string]json.RawMessage
		Targets map[string]json.RawMessage
	}
	return layouts.Part{Name: "collections", Capture: func(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
		rows, e := tx.QueryContext(ctx, `SELECT id,spec_json FROM collections_specs WHERE owner_user_id=0 ORDER BY id`)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []item{}
		for rows.Next() {
			var id int64
			var raw string
			if e = rows.Scan(&id, &raw); e != nil {
				return nil, e
			}
			var sp map[string]json.RawMessage
			if e = json.Unmarshal([]byte(raw), &sp); e != nil {
				return nil, e
			}
			x := item{ID: id, Fields: map[string]json.RawMessage{}}
			for _, key := range layoutFields {
				if v, ok := sp[key]; ok {
					x.Fields[key] = v
				}
			}
			out = append(out, x)
		}
		if e = rows.Err(); e != nil {
			return nil, e
		}
		return json.Marshal(out)
	}, Restore: func(ctx context.Context, tx *sql.Tx, raw json.RawMessage) error {
		var items []item
		if e := json.Unmarshal(raw, &items); e != nil {
			return e
		}
		for _, x := range items {
			var current string
			if e := tx.QueryRowContext(ctx, `SELECT spec_json FROM collections_specs WHERE id=? AND owner_user_id=0`, x.ID).Scan(&current); e != nil {
				return fmt.Errorf("collection %d indisponible : %w", x.ID, e)
			}
			var sp map[string]json.RawMessage
			if e := json.Unmarshal([]byte(current), &sp); e != nil {
				return e
			}
			for _, key := range layoutFields {
				delete(sp, key)
				if v, ok := x.Fields[key]; ok {
					sp[key] = v
				}
			}
			// Old snapshots stored browse placement under targets.
			if v, ok := x.Targets["collectionsTab"]; ok {
				sp["browseInApps"] = v
			}
			b, e := json.Marshal(sp)
			if e != nil {
				return e
			}
			var enabled bool
			if v := sp["enabled"]; v != nil {
				if e = json.Unmarshal(v, &enabled); e != nil {
					return e
				}
			}
			en := 0
			if enabled {
				en = 1
			}
			_, e = tx.ExecContext(ctx, `UPDATE collections_specs SET spec_json=?,enabled=?,updated_at=?,build_hash='' WHERE id=?`, string(b), en, time.Now().Unix(), x.ID)
			if e != nil {
				return e
			}
		}
		return nil
	}, Changed: func(ctx context.Context) {
		s.applyRemote(ctx, nil, true)
		s.notify("index")
		rows, e := s.db.QueryContext(ctx, `SELECT id FROM collections_specs WHERE owner_user_id=0`)
		if e == nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					s.changed(id)
				}
			}
		}
	}}
}
