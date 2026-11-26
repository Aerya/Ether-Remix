package libraries

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"strconv"
)

func init() {
	// listed and item_limit: how many items the last sync read and the
	// most it could, so a list cut off at its limit is never silent.
	const mig14 = `ALTER TABLE libraries_catalogue_state ADD COLUMN listed BIGINT NOT NULL DEFAULT 0;
ALTER TABLE libraries_catalogue_state ADD COLUMN item_limit BIGINT NOT NULL DEFAULT 0;`
	store.Register("libraries", 14, mig14)
	store.RegisterDialect("libraries", 14, store.Postgres, mig14)
	store.Register("libraries", 7, `ALTER TABLE libraries_catalogue_state ADD COLUMN last_error TEXT NOT NULL DEFAULT '';`)
	store.RegisterDialect("libraries", 7, store.Postgres, `ALTER TABLE libraries_catalogue_state ADD COLUMN last_error TEXT NOT NULL DEFAULT '';`)

	const migration = `ALTER TABLE libraries ADD COLUMN sources TEXT NOT NULL DEFAULT '[]';
CREATE TABLE libraries_catalogue_members (source_id TEXT NOT NULL, key BIGINT NOT NULL, PRIMARY KEY(source_id,key));
CREATE TABLE libraries_catalogue_state (source_id TEXT PRIMARY KEY, refreshed BIGINT NOT NULL);`
	store.Register("libraries", 5, migration)
	store.RegisterDialect("libraries", 5, store.Postgres, migration)
	store.Register("libraries", 6, `ALTER TABLE libraries_catalogue_state ADD COLUMN item_count BIGINT NOT NULL DEFAULT 0;`)
	store.RegisterDialect("libraries", 6, store.Postgres, `ALTER TABLE libraries_catalogue_state ADD COLUMN item_count BIGINT NOT NULL DEFAULT 0;`)
}

// SetSources records validated registry references. Validation against enabled
// declarations belongs to the application registry, outside the library store.
func (s *Service) SetSources(ctx context.Context, id core.LibraryID, sources []string) error {
	if len(sources) == 0 {
		return ErrInvalid
	}
	data, err := json.Marshal(sources)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE libraries SET sources=?, updated_at=? WHERE id=?`, string(data), s.now().Unix(), int64(id))
	if err == nil {
		s.changed()
	}
	return err
}

func defaultSources(lib core.Library) []string {
	if lib.Source == core.LibraryCollection {
		return []string{"collection:" + strconv.FormatInt(lib.CollectionID, 10)}
	}
	if len(lib.Sources) > 0 {
		return lib.Sources
	}
	if lib.Source == core.LibraryPlugin {
		return []string{"plugin:" + lib.PluginID + ":" + lib.KindKey}
	}
	typ := lib.MediaType
	if typ == core.TypeShow {
		return []string{"tmdb-tv"}
	}
	return []string{"tmdb-movies"}
}

// SourceMember checks persisted membership without network work in playback.
func (s *Service) SourceMember(ctx context.Context, lib core.Library, key core.RatingKey) (bool, error) {
	if lib.Source == core.LibraryCollection {
		return s.CollectionMember(ctx, lib.CollectionID, key)
	}
	for _, id := range lib.Sources {
		if id == "tmdb-movies" || id == "tmdb-tv" {
			return true, nil
		}
	}
	for _, id := range lib.Sources {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries_catalogue_members WHERE source_id=? AND key=?`, id, int64(key)).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return len(lib.Sources) == 0, nil
}

func (s *Service) SetCollection(ctx context.Context, id core.LibraryID, collectionID int64) error {
	lib, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if lib.Source != core.LibraryCollection || collectionID <= 0 {
		return ErrInvalid
	}
	data, _ := json.Marshal([]string{"collection:" + strconv.FormatInt(collectionID, 10)})
	_, err = s.db.ExecContext(ctx, `UPDATE libraries SET collection_id=?, sources=?, updated_at=? WHERE id=?`, collectionID, string(data), s.now().Unix(), int64(id))
	if err == nil {
		s.changed()
	}
	return err
}

// CreateOption sets an optional property on a newly created catalogue library.
type CreateOption func(*core.Library)

// WithMetadataSource persists the selected source as part of library creation.
func WithMetadataSource(source string) CreateOption {
	return func(lib *core.Library) { lib.MetadataSource = source }
}

// SetMetadataSource saves a validated source. Plugin kinds own their metadata.
func (s *Service) SetMetadataSource(ctx context.Context, id core.LibraryID, source string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE libraries SET metadata_source=?, updated_at=? WHERE id=? AND deleted=0 AND source<>'plugin'`, source, s.now().Unix(), int64(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return core.ErrNotFound
	}
	s.gen.Add(1)
	return nil
}
