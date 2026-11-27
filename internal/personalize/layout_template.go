package personalize

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
)

func (e *Engine) LayoutTemplatePart() layouts.Part {
	return layouts.Part{Name: "home", PrepareRestore: e.prepareLegacyTemplate, Capture: func(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
		rows, err := tx.QueryContext(ctx, `SELECT document, revision FROM personalize_pages ORDER BY page`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		pages := []core.RowPage{}
		for rows.Next() {
			var raw string
			var revision int64
			if err := rows.Scan(&raw, &revision); err != nil {
				return nil, err
			}
			var p core.RowPage
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				return nil, err
			}
			p.Revision = revision
			pages = append(pages, p)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Pages []core.RowPage `json:"pages"`
		}{pages})
	}, Restore: func(ctx context.Context, tx *sql.Tx, raw json.RawMessage) error {
		var doc struct {
			Pages *[]core.RowPage `json:"pages"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		var pages []core.RowPage
		if doc.Pages != nil {
			pages = *doc.Pages
		} else {
			var err error
			pages, err = e.convertLegacyLayout(ctx, string(raw), nil)
			if err != nil {
				return err
			}
		}
		// Normalize without querying the store while its write transaction is held.
		for _, p := range pages {
			if err := p.ValidateStructure(); err != nil {
				return err
			}
			p.UpdatedAt = e.now()
			b, err := json.Marshal(p)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO personalize_pages(page,document,revision) VALUES(?,?,1) ON CONFLICT(page) DO UPDATE SET document=excluded.document,revision=personalize_pages.revision+1`, string(p.Key), string(b))
			if err != nil {
				return err
			}
		}
		return nil
	}, Changed: func(ctx context.Context) {
		e.applyRemote(ctx, []string{"layout", "owner_rows"}, false)
		e.notify("layout")
		e.notify("owner_rows")
	}}
}
