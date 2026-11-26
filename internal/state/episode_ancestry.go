package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type ancestryReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func childAncestry(ctx context.Context, db ancestryReader, key core.RatingKey) (show, parent core.RatingKey, err error) {
	if !key.Allocated() {
		return core.ShowKey(key.TMDB()), key.Parent(), nil
	}
	err = db.QueryRowContext(ctx, `SELECT show_key,parent_key FROM catalog_allocated_child WHERE key=?`, key).Scan(&show, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func (s *State) allocatedAncestry(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]core.RatingKey, error) {
	out := map[core.RatingKey]core.RatingKey{}
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		if key.Allocated() {
			args = append(args, key)
		}
	}
	for batch := range store.Chunks(args, 200) {
		rows, err := s.db.QueryContext(ctx, `SELECT key,show_key FROM catalog_allocated_child WHERE key IN (`+strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")+`)`, batch...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, show core.RatingKey
			if err := rows.Scan(&key, &show); err != nil {
				rows.Close()
				return nil, err
			}
			out[key] = show
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
