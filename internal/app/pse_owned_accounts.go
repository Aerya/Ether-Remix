package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_owned_accounts", 1, `CREATE TABLE pse_ib_accounts(id INTEGER PRIMARY KEY AUTOINCREMENT,owner_user_id INTEGER NOT NULL,name TEXT NOT NULL,import_playlists INTEGER NOT NULL DEFAULT 0);`)
	store.RegisterViewerPurge("pse_owned_accounts", []string{"pse_ib_accounts"}, func(ctx context.Context, tx *sql.Tx, viewer int64) error {
		for _, query := range []string{
			"DELETE FROM pse_ibroadcast_tracks WHERE account_id IN (SELECT id FROM pse_ib_accounts WHERE owner_user_id=?)",
			"DELETE FROM pse_ibroadcast WHERE id IN (SELECT id FROM pse_ib_accounts WHERE owner_user_id=?)",
			"DELETE FROM pse_music_owned WHERE provider IN (SELECT 'ibroadcast:'||id FROM pse_ib_accounts WHERE owner_user_id=?)",
			"DELETE FROM pse_ib_accounts WHERE owner_user_id=?",
		} {
			if _, err := tx.ExecContext(ctx, query, viewer); err != nil {
				return err
			}
		}
		return nil
	})
	store.RegisterViewerMerge("pse_owned_accounts", []string{"pse_ib_accounts"}, func(ctx context.Context, tx *sql.Tx, from, to int64) error {
		_, err := tx.ExecContext(ctx, "UPDATE pse_ib_accounts SET owner_user_id=? WHERE owner_user_id=?", to, from)
		return err
	})
}

type ownedAccount struct {
	ID              int64              `json:"id"`
	Name            string             `json:"name"`
	ImportPlaylists bool               `json:"importPlaylists"`
	Access          mediaaccess.Policy `json:"access"`
}

func (a *App) ibAccounts(ctx context.Context, owner int64) ([]ownedAccount, error) {
	q := "SELECT id,name,import_playlists FROM pse_ib_accounts"
	args := []any{}
	if owner > 0 {
		q += " WHERE owner_user_id=?"
		args = append(args, owner)
	}
	rows, err := a.db.QueryContext(ctx, q+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	out := []ownedAccount{}
	for rows.Next() {
		var v ownedAccount
		if err = rows.Scan(&v.ID, &v.Name, &v.ImportPlaylists); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Access, err = mediaaccess.Read(ctx, a.db, fmt.Sprint("ibroadcast:", out[i].ID))
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (a *App) ibAccount(ctx context.Context, id int64) (*music.IBroadcast, error) {
	var owner int64
	var playlists bool
	if err := a.db.QueryRowContext(ctx, "SELECT owner_user_id,import_playlists FROM pse_ib_accounts WHERE id=?", id).Scan(&owner, &playlists); err != nil {
		return nil, err
	}
	rt := pseRuntimeOf(a)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.ibAccounts == nil {
		rt.ibAccounts = map[int64]*music.IBroadcast{}
	}
	ib := rt.ibAccounts[id]
	if ib == nil || ib.OwnerID != owner {
		ib = &music.IBroadcast{DB: a.db, Sealer: a.cfg, HTTP: egress.Client(egress.CapMetadata, 90*time.Second), AccountID: id, OwnerID: owner}
		rt.ibAccounts[id] = ib
	}
	ib.SetPlaylistImport(playlists)
	return ib, nil
}
func (a *App) ownedActor(r *http.Request) (context.Context, int64, bool) {
	if id := core.CatalogViewerID(r.Context()); id > 0 {
		return r.Context(), id, false
	}
	owner, err := a.users.Owner(r.Context())
	if err != nil || owner == nil {
		return r.Context(), 0, false
	}
	return core.WithCatalogViewer(r.Context(), &core.User{ID: owner.ID}), owner.ID, true
}
func (a *App) createIBAccount(ctx context.Context, owner int64, name string) (int64, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Reserve 1 for the pre-multi-account draft; old tokens cannot accidentally
	// become another user's account when upgrading a preview build.
	var id int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),1)+1 FROM pse_ib_accounts").Scan(&id); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO pse_ib_accounts(id,owner_user_id,name) VALUES(?,?,?)", id, owner, name); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO pse_media_sources(id,owner_user_id) VALUES(?,?)", "ibroadcast:"+strconv.FormatInt(id, 10), owner); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
