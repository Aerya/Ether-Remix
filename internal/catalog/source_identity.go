package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("catalog", 80, `
CREATE TABLE catalog_identity (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 kind INTEGER NOT NULL,
 provider TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 key INTEGER NOT NULL,
 created INTEGER NOT NULL,
 UNIQUE(kind,provider,provider_id)
);
CREATE INDEX catalog_identity_key ON catalog_identity(key,seq);
`)
}

func init() {
	store.Register("catalog", 82, `
CREATE TABLE catalog_identity_alias (
 old_key INTEGER PRIMARY KEY,
 canonical_key INTEGER NOT NULL
);
CREATE TABLE catalog_title_detail_source (
 key INTEGER PRIMARY KEY,
 provider TEXT NOT NULL,
 fetched_at INTEGER NOT NULL
);`)
}

type sourceIdentityRekey func(context.Context, *sql.Tx, *core.Title, *core.Title, []core.TitleKeyRange) (func(), error)

func (c *Catalog) SetSourceIdentityRekey(rekey func(context.Context, *sql.Tx, *core.Title, *core.Title, []core.TitleKeyRange) (func(), error)) {
	if rekey == nil {
		c.sourceIdentityRekey.Store(nil)
		return
	}
	fn := sourceIdentityRekey(rekey)
	c.sourceIdentityRekey.Store(&fn)
}

func (c *Catalog) MetadataSources() *core.MetadataSources { return c.metadataSources }

type identityDB interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func sourceIdentity(ctx context.Context, db identityDB, kind core.KeyKind, source core.MetadataSourceID, id string) (core.RatingKey, error) {
	var key core.RatingKey
	err := db.QueryRowContext(ctx, `SELECT key FROM catalog_identity WHERE kind=? AND provider=? AND provider_id=?`, kind, source, id).Scan(&key)
	return key, err
}

// putSourceIdentity must run in a write transaction. The unique remote reference
// serializes concurrent allocation, and every committed row has a stable key.
func putSourceIdentity(ctx context.Context, tx *sql.Tx, kind core.KeyKind, source core.MetadataSourceID, id string, preferred core.RatingKey, now int64) (core.RatingKey, error) {
	var seq int64
	err := tx.QueryRowContext(ctx, `INSERT INTO catalog_identity(kind,provider,provider_id,key,created) VALUES (?,?,?,?,?)
 ON CONFLICT(kind,provider,provider_id) DO NOTHING RETURNING seq`, kind, source, id, preferred, now).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceIdentity(ctx, tx, kind, source, id)
	}
	if err != nil {
		return 0, err
	}
	if preferred != 0 {
		return preferred, nil
	}
	key, ok := core.AllocatedKey(kind, seq)
	if !ok {
		return 0, fmt.Errorf("catalog: allocated rating key range exhausted for kind %d", kind)
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_identity SET key=? WHERE seq=?`, key, seq)
	return key, err
}

// AllocateIdentity reserves a stable key for a provider item.
func (c *Catalog) AllocateIdentity(ctx context.Context, kind core.KeyKind, source core.MetadataSourceID, id string) (core.RatingKey, error) {
	if source == "" || strings.TrimSpace(id) == "" || kind < core.KindMovie || kind > core.KindEpisode {
		return 0, core.ErrNotFound
	}
	key, err := sourceIdentity(ctx, c.db, kind, source, id)
	if !errors.Is(err, sql.ErrNoRows) {
		return key, err
	}
	return c.saveSourceIdentity(ctx, kind, source, id, 0)
}

func (c *Catalog) saveSourceIdentity(ctx context.Context, kind core.KeyKind, source core.MetadataSourceID, id string, preferred core.RatingKey) (core.RatingKey, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	key, err := putSourceIdentity(ctx, tx, kind, source, id, preferred, c.now().Unix())
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return key, nil
}

func sourceTitleKind(typ core.MediaType) core.KeyKind {
	switch typ {
	case core.TypeMovie:
		return core.KindMovie
	case core.TypeShow:
		return core.KindShow
	}
	return 0
}

func (c *Catalog) sourceTitleKey(ctx context.Context, ref core.ProviderRef, ids core.IDs) (core.RatingKey, error) {
	kind := sourceTitleKind(ref.Type)
	if kind == 0 || ref.Source == "" || strings.TrimSpace(ref.ID) == "" {
		return 0, core.ErrNotFound
	}
	if ref.Source == core.MetadataTMDB {
		var err error
		ids.TMDB, err = tmdbRefID(ref)
		if err != nil {
			return 0, err
		}
	}
	if ref.Source == core.MetadataTVDB && ids.TVDB == 0 {
		ids.TVDB, _ = strconv.ParseInt(ref.ID, 10, 64)
	}
	canonical := core.RatingKey(0)
	if ids.TMDB > 0 {
		canonical = core.ShowKey(ids.TMDB)
		if kind == core.KindMovie {
			canonical = core.MovieKey(ids.TMDB)
		}
	}
	key, err := sourceIdentity(ctx, c.db, kind, ref.Source, ref.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		return key, err
	}
	if ref.Source == core.MetadataTMDB && ids.TVDB == 0 && ids.IMDB == "" {
		return canonical, nil
	}

	if canonical != 0 {
		err = c.db.QueryRowContext(ctx, `SELECT key FROM catalog_titles WHERE key=?`, canonical).Scan(&key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	if key == 0 {
		for _, external := range []struct {
			column  string
			value   any
			present bool
		}{
			{"tmdb_id", ids.TMDB, ids.TMDB > 0}, {"tvdb", ids.TVDB, ids.TVDB > 0}, {"imdb", ids.IMDB, ids.IMDB != ""}} {
			if !external.present {
				continue
			}
			var candidate core.RatingKey
			err = c.db.QueryRowContext(ctx, `SELECT MIN(key) FROM catalog_titles WHERE `+external.column+`=? AND type=? HAVING COUNT(*)=1`, external.value, ref.Type).Scan(&candidate)
			if err == nil {
				key = candidate
				break
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return 0, err
			}
		}
	}
	if key == 0 {
		key = canonical
	}
	return c.saveSourceIdentity(ctx, kind, ref.Source, ref.ID, key)
}

// canonicalSourceKey resolves published aliases while retaining the old key
// in the durable allocation namespace.
func (c *Catalog) canonicalSourceKey(ctx context.Context, key core.RatingKey) (core.RatingKey, error) {
	if !key.Allocated() || !key.IsTitleKey() {
		return key, nil
	}
	var canonical core.RatingKey
	err := c.db.QueryRowContext(ctx, `SELECT canonical_key FROM catalog_identity_alias WHERE old_key=?`, key).Scan(&canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return key, nil
	}
	return canonical, err
}

func (c *Catalog) reconcileTitleIdentity(ctx context.Context, t *core.Title) error {
	oldKey := t.Key
	if !oldKey.Allocated() {
		return nil
	}
	var candidate core.RatingKey
	if t.IDs.TMDB > 0 {
		candidate = core.MovieKey(t.IDs.TMDB)
		if t.Type == core.TypeShow {
			candidate = core.ShowKey(t.IDs.TMDB)
		}
	} else if t.IDs.IMDB != "" {
		// IMDb-only provider records can establish the same canonical
		// identity. Ambiguous matches leave the allocation in place.
		var id int64
		err := c.db.QueryRowContext(ctx, `SELECT MIN(tmdb_id) FROM catalog_titles
 WHERE imdb=? AND type=? AND tmdb_id>0 AND key=CASE WHEN type='show' THEN 2000000000000000 ELSE 1000000000000000 END+tmdb_id
 HAVING COUNT(*)=1`, t.IDs.IMDB, t.Type).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if id > 0 {
			candidate = core.MovieKey(id)
			if t.Type == core.TypeShow {
				candidate = core.ShowKey(id)
			}
		}
	}
	if candidate == 0 {
		return nil
	}
	row, err := c.loadByKey(ctx, candidate)
	if errors.Is(err, core.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !compatibleEpisodeIDs(t.IDs, row.t.IDs) {
		return nil
	}
	t.IDs = mergeSourceIDs(t.IDs, row.t.IDs)

	rekey := c.sourceIdentityRekey.Load()
	if rekey == nil {
		return fmt.Errorf("catalog: source identity rematch service unavailable")
	}
	old := *t
	ranges := []core.TitleKeyRange{{First: int64(oldKey), Last: int64(oldKey), Delta: int64(candidate - oldKey)}}
	if prior, err := c.loadByKey(ctx, oldKey); err == nil {
		old = prior.t
		ranges, err = c.EpisodeMatchRanges(ctx, oldKey, candidate)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, core.ErrNotFound) {
		return err
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range []core.RatingKey{min(oldKey, candidate), max(oldKey, candidate)} {
		if err := store.LockKey(ctx, store.DialectOf(c.db), tx, "title-match:"+key.String()); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_identity_alias(old_key,canonical_key) VALUES (?,?) ON CONFLICT(old_key) DO NOTHING`, oldKey, candidate)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.QueryRowContext(ctx, `SELECT canonical_key FROM catalog_identity_alias WHERE old_key=?`, oldKey).Scan(&t.Key)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_identity SET key=? WHERE key=?`, candidate, oldKey); err != nil {
		return err
	}
	if t.Type == core.TypeShow {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_allocated_child SET show_key=?,parent_key=CASE WHEN parent_key=? THEN ? ELSE parent_key END WHERE show_key=?`, candidate, oldKey, candidate, oldKey); err != nil {
			return err
		}
	}

	after, err := (*rekey)(ctx, tx, &old, &row.t, ranges)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET browsable=0 WHERE key=?`, oldKey); err != nil {
		return err
	}
	if err := c.commitOrderWrite(tx); err != nil {
		return err
	}
	t.Key = candidate
	if after != nil {
		after()
	}
	return nil
}

// SourceSummary assigns the identity of a hit without fetching or persisting title details.
func (c *Catalog) SourceSummary(ctx context.Context, hit core.MetadataHit) (core.Title, error) {
	if hit.Adult {
		return core.Title{}, core.ErrNotFound
	}
	key, err := c.sourceTitleKey(ctx, hit.Ref, hit.IDs)
	if err != nil {
		return core.Title{}, err
	}
	return core.Title{Key: key, Type: hit.Ref.Type, IDs: hit.IDs, Title: hit.Title, Summary: hit.Summary,
		PosterPath: hit.PosterPath, Year: hit.Year, Released: hit.Released, SortTitle: sortTitle(hit.Title)}, nil
}

// MaterializeSourceTitle fetches details for an explicitly opened provider reference.
func (c *Catalog) MaterializeSourceTitle(ctx context.Context, ref core.ProviderRef) (*core.Title, error) {
	source := c.metadataSources.Get(ref.Source)
	if source == nil || !source.Configured() {
		return nil, core.ErrUnsupported
	}
	t, err := source.Lookup(ctx, ref)
	if err != nil {
		return nil, err
	}
	if t == nil || t.Adult || t.Type != ref.Type {
		return nil, core.ErrNotFound
	}
	id, _ := strconv.ParseInt(ref.ID, 10, 64)
	switch ref.Source {
	case core.MetadataTMDB:
		if t.IDs.TMDB == 0 {
			t.IDs.TMDB = id
		}
	case core.MetadataTVDB:
		if t.IDs.TVDB == 0 {
			t.IDs.TVDB = id
		}
	}
	key, err := c.sourceTitleKey(ctx, ref, t.IDs)
	if err != nil {
		return nil, err
	}
	t.Key = key
	if err := c.reconcileTitleIdentity(ctx, t); err != nil {
		return nil, err
	}
	key = t.Key
	if old, err := c.loadByKey(ctx, key); err == nil {
		t.IDs = mergeSourceIDs(t.IDs, old.t.IDs)
	} else if !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	if err := c.storeSourceDetails(ctx, t, ref.Source, false); err != nil {
		return nil, err
	}

	return t, nil
}

func (c *Catalog) storeSourceDetails(ctx context.Context, t *core.Title, source core.MetadataSourceID, replace bool) error {
	now := c.now().Unix()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertOne(ctx, tx, *t, upsertOpts{browsable: t.IsReleased(c.now()), detail: true, replaceMetadata: replace}, now); err != nil {
		return err
	}
	if t.Key.Allocated() && t.IDs.TMDB > 0 {
		if _, err := putSourceIdentity(ctx, tx, t.Key.Kind(), core.MetadataTMDB, strconv.FormatInt(t.IDs.TMDB, 10), t.Key, now); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_title_detail_source(key,provider,fetched_at) VALUES (?,?,?)
 ON CONFLICT(key) DO UPDATE SET provider=excluded.provider,fetched_at=excluded.fetched_at`, t.Key, source, now)
	if err != nil {
		return err
	}
	return c.commitOrderWrite(tx)
}

func mergeSourceIDs(ids, more core.IDs) core.IDs {
	if ids.TMDB == 0 {
		ids.TMDB = more.TMDB
	}
	if ids.TVDB == 0 {
		ids.TVDB = more.TVDB
	}
	if ids.IMDB == "" {
		ids.IMDB = more.IMDB
	}
	return ids
}

func (c *Catalog) openSourceTitle(ctx context.Context, key core.RatingKey, typ core.MediaType) (*core.Title, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT provider,provider_id FROM catalog_identity WHERE key=? ORDER BY seq`, key)
	if err != nil {
		return nil, err
	}
	var refs []core.ProviderRef
	for rows.Next() {
		ref := core.ProviderRef{Type: typ}
		if err := rows.Scan(&ref.Source, &ref.ID); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if source := c.metadataSources.Get(ref.Source); source != nil && source.Configured() {
			return c.MaterializeSourceTitle(ctx, ref)
		}
	}
	return nil, core.ErrNotFound
}
