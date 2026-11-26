package vault

import (
	"context"
	"database/sql"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// RekeyTitle changes the owner match for vault releases in the caller’s transaction.
func RekeyTitle(ctx context.Context, tx *sql.Tx, old, next *core.Title) error {
	// Vault records without a TMDB ID have no stored provider identity.
	if old.IDs.TMDB <= 0 {
		return nil
	}
	// Vault content keys and pack offsets are independent of title identity.
	// Movie and TV TMDB IDs overlap, so constrain by the vault's numbering.
	const episodic = "(season<>0 OR episode<>0 OR EXISTS (SELECT 1 FROM vault_members m WHERE m.key=vault_releases.key AND m.episode>0))"
	numbering := "NOT " + episodic
	if old.Type == core.TypeShow {
		numbering = episodic
	}
	_, err := tx.ExecContext(ctx, `UPDATE vault_releases SET tmdb=?, title_key=?, year=?, owner_match=1 WHERE tmdb=? AND `+numbering,
		next.IDs.TMDB, releaseparse.NormalizeTitle(next.Title), next.Year, old.IDs.TMDB)
	return err
}
