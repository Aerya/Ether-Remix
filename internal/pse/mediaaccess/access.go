// Package mediaaccess holds the access policy shared by local folders and
// connected personal media accounts. Missing policies fail closed.
package mediaaccess

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/store"
)

var revision atomic.Uint64

func Revision() uint64 { return revision.Load() }
func Changed()         { revision.Add(1) }
func init() {
	store.Register("pse_media_access", 1, `
CREATE TABLE pse_media_sources(id TEXT PRIMARY KEY, owner_user_id INTEGER NOT NULL CHECK(owner_user_id>0), shared_all INTEGER NOT NULL DEFAULT 0, group_name TEXT NOT NULL DEFAULT '');
CREATE TABLE pse_media_shares(source_id TEXT NOT NULL REFERENCES pse_media_sources(id) ON DELETE CASCADE, viewer_id INTEGER NOT NULL CHECK(viewer_id>0), PRIMARY KEY(source_id,viewer_id));
`)
	store.RegisterViewerTable("pse_media_access", "pse_media_sources", "owner_user_id", store.ViewerMergeUnion)
	store.RegisterViewerTable("pse_media_access", "pse_media_shares", "viewer_id", store.ViewerMergeMove, "source_id")
}

type Policy struct {
	OwnerID   int64   `json:"ownerId"`
	SharedAll bool    `json:"sharedAll"`
	Viewers   []int64 `json:"viewers"`
	Group     string  `json:"group"`
}

// Create always starts private. The owner must come from authenticated server
// identity, never from a JSON field supplied by the client.
func Create(ctx context.Context, db *sql.DB, source string, owner int64) error {
	if source == "" || owner <= 0 {
		return errors.New("propriétaire authentifié requis")
	}
	_, err := db.ExecContext(ctx, "INSERT INTO pse_media_sources(id,owner_user_id) VALUES(?,?)", source, owner)
	return err
}

// Predicate uses only internally supplied SQL identifiers. Viewer identifiers
// are integers rendered by the server, not client-provided SQL fragments.
func Predicate(sourceColumn string, viewer int64) string {
	if viewer <= 0 {
		return "1=0"
	}
	v := strconv.FormatInt(viewer, 10)
	return "EXISTS (SELECT 1 FROM pse_media_sources mas WHERE mas.id=" + sourceColumn + " AND (mas.owner_user_id=" + v + " OR mas.shared_all=1 OR EXISTS (SELECT 1 FROM pse_media_shares msh WHERE msh.source_id=mas.id AND msh.viewer_id=" + v + ")))"
}

func Allowed(ctx context.Context, db *sql.DB, source string, viewer int64) bool {
	if viewer <= 0 {
		return false
	}
	var ok int
	err := db.QueryRowContext(ctx, "SELECT "+Predicate("?", viewer), source).Scan(&ok)
	return err == nil && ok == 1
}

// Update replaces sharing atomically. It cannot transfer ownership, and even a
// permitted reader may not grant access to other viewers.
func Update(ctx context.Context, db *sql.DB, source string, actor int64, p Policy) error {
	if actor <= 0 {
		return errors.New("propriétaire authentifié requis")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE pse_media_sources SET shared_all=?,group_name=? WHERE id=? AND owner_user_id=?", p.SharedAll, p.Group, source, actor)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("source introuvable ou accès refusé")
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM pse_media_shares WHERE source_id=?", source); err != nil {
		return err
	}
	for _, viewer := range p.Viewers {
		if viewer <= 0 {
			return errors.New("spectateur invalide")
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO pse_media_shares(source_id,viewer_id) VALUES(?,?)", source, viewer); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	Changed()
	return nil
}

func Read(ctx context.Context, db *sql.DB, source string) (Policy, error) {
	var p Policy
	if err := db.QueryRowContext(ctx, "SELECT owner_user_id,shared_all,group_name FROM pse_media_sources WHERE id=?", source).Scan(&p.OwnerID, &p.SharedAll, &p.Group); err != nil {
		return p, err
	}
	rows, err := db.QueryContext(ctx, "SELECT viewer_id FROM pse_media_shares WHERE source_id=? ORDER BY viewer_id", source)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Viewers = []int64{}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return p, err
		}
		p.Viewers = append(p.Viewers, v)
	}
	return p, rows.Err()
}
