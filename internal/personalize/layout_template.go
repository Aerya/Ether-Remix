package personalize

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/layouts"
)

func (e *Engine) LayoutTemplatePart() layouts.Part {
	return layouts.Part{Name: "home", Capture: func(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT document FROM personalize_home_layout WHERE id=1`).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return json.RawMessage(`{"global":[],"viewers":[]}`), nil
		}
		return json.RawMessage(raw), err
	}, Restore: func(ctx context.Context, tx *sql.Tx, raw json.RawMessage) error {
		var d core.HomeLayout
		if err := json.Unmarshal(raw, &d); err != nil {
			return err
		}
		d, err := normalizeHomeLayout(d)
		if err != nil {
			return err
		}
		d.UpdatedAt = e.now()
		d.CollectionSchedules = nil
		b, err := json.Marshal(d)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO personalize_home_layout(id,document) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document`, string(b))
		return err
	}, Changed: func(ctx context.Context) {
		e.applyRemote(ctx, []string{"layout", "owner_rows"}, false)
		e.notify("layout")
		e.notify("owner_rows")
	}}
}
