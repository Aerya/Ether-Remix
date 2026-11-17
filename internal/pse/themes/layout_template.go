package themes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
)

// Only presentation is captured. Sharing, language policy, roots and accounts
// always remain current. Removed entities are not silently recreated.
func (s *Service) LayoutTemplatePart() layouts.Part {
	type item struct {
		ID                      int64
		Name, Type, Collections string
		Position                int
	}
	return layouts.Part{Name: "themeLibraries", Capture: func(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
		rows, e := tx.QueryContext(ctx, `SELECT id,name,type,collections,position FROM pse_theme_libraries ORDER BY position,id`)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []item{}
		for rows.Next() {
			var x item
			if e = rows.Scan(&x.ID, &x.Name, &x.Type, &x.Collections, &x.Position); e != nil {
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
			var ids []string
			if e := json.Unmarshal([]byte(x.Collections), &ids); e != nil {
				return e
			}
			for _, id := range ids {
				var n int
				e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM collections_specs WHERE CAST(id AS TEXT)=? OR slug=?`, id, id).Scan(&n)
				if e != nil {
					return e
				}
				if n == 0 {
					return fmt.Errorf("collection supprimée : %s", id)
				}
			}
			result, e := tx.ExecContext(ctx, `UPDATE pse_theme_libraries SET name=?,type=?,collections=?,position=? WHERE id=?`, x.Name, x.Type, x.Collections, x.Position, x.ID)
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
	}, Changed: func(ctx context.Context) { _ = s.Reload(ctx) }}
}
