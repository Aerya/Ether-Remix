package auth

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/braedonsaunders/ether/internal/core"
)

// RemixLibraryReservations returns section IDs still referenced by viewer limits
// inside the same transaction as creation of a new theme.
func RemixLibraryReservations(ctx context.Context, tx *sql.Tx) ([]core.LibraryID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT allowed_libraries, plex_sections FROM auth_users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sections []core.LibraryID
	for rows.Next() {
		var configured, shared string
		if err := rows.Scan(&configured, &shared); err != nil {
			return nil, err
		}
		for _, encoded := range []string{configured, shared} {
			var ids []core.LibraryID
			// Comme Ether (auth.go, lecture d'un spectateur) : une valeur illisible
			// est ignorée, elle ne bloque pas la création d'un thème.
			if err := json.Unmarshal([]byte(encoded), &ids); err != nil {
				continue
			}
			sections = append(sections, ids...)
		}
	}
	return sections, rows.Err()
}
