package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
)

func (s *Service) LayoutTemplatePart() layouts.Part {
	type item struct {
		ID            int64
		Name, Present string
		Sort, Hidden  int
	}
	return layouts.Part{Name: "libraries", Capture: func(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
		rows, e := tx.QueryContext(ctx, `SELECT id,name,present,sort,hidden FROM libraries ORDER BY id`)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []item{}
		for rows.Next() {
			var x item
			if e = rows.Scan(&x.ID, &x.Name, &x.Present, &x.Sort, &x.Hidden); e != nil {
				return nil, e
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
			result, e := tx.ExecContext(ctx, `UPDATE libraries SET name=?,present=?,sort=?,hidden=? WHERE id=?`, x.Name, x.Present, x.Sort, x.Hidden, x.ID)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return fmt.Errorf("bibliothèque supprimée : %s", x.Name)
			}
		}
		return nil
	}, Changed: func(context.Context) { s.changed() }}
}
