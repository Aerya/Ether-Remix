package pluginitems

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	ExpansionStateRunning = "running"
	ExpansionStateDone    = "done"
	ExpansionStateError   = "error"
)

// FacetExpansion is one durable row in plugin_facet_expansions recording the
// latest on-demand expansion of a (plugin, kind, facet, value) slice.
type FacetExpansion struct {
	PluginID   string
	KindKey    string
	LibraryID  core.LibraryID
	Facet      string
	ValueKey   string
	State      string
	StartedAt  time.Time
	FinishedAt time.Time
	ItemsAdded int64
	Error      string
	// Outdated: the expansion ran under an earlier declaration of the kind
	// (an older plugin), so it no longer counts as fresh.
	Outdated bool
}

// IsFresh reports whether e completed successfully within ttl of now, under
// the kind's current declaration.
func (e FacetExpansion) IsFresh(now time.Time, ttl time.Duration) bool {
	if e.State != ExpansionStateDone || e.FinishedAt.IsZero() || ttl <= 0 || e.Outdated {
		return false
	}
	return now.Sub(e.FinishedAt) < ttl
}

// IsInErrorCooldown reports whether e recorded a failure within cooldown of
// now, suppressing immediate re-launch and repeated first-open stalls for a
// slice whose upstream expand route is failing.
func (e FacetExpansion) IsInErrorCooldown(now time.Time, cooldown time.Duration) bool {
	if e.State != ExpansionStateError || e.FinishedAt.IsZero() || cooldown <= 0 {
		return false
	}
	return now.Sub(e.FinishedAt) < cooldown
}

// PersonFacetMatch identifies one (field, display name) credit for a synthetic
// plugin person ID within a library.
type PersonFacetMatch struct {
	Field string
	Name  string
}

// GetFacetExpansion looks up the durable expansion record for
// (pluginID, kindKey, facet, valueKey).
func (s *Service) GetFacetExpansion(ctx context.Context, pluginID, kindKey, facet, valueKey string) (FacetExpansion, bool, error) {
	if s == nil || s.db == nil {
		return FacetExpansion{}, false, nil
	}
	var (
		out        FacetExpansion
		libID      int64
		startedAt  int64
		finishedAt int64
		current    int
	)
	err := s.db.QueryRowContext(ctx, `
SELECT e.plugin_id, e.kind_key, e.library_id, e.facet, e.value_key, e.state, e.started_at, e.finished_at, e.items_added, e.error,
	CASE WHEN e.decl_hash = COALESCE((SELECT k.decl_hash FROM plugin_kinds k WHERE k.plugin_id = e.plugin_id AND k.kind_key = e.kind_key), '') THEN 1 ELSE 0 END
FROM plugin_facet_expansions e
WHERE e.plugin_id = ? AND e.kind_key = ? AND e.facet = ? AND e.value_key = ?`,
		pluginID, kindKey, facet, valueKey,
	).Scan(&out.PluginID, &out.KindKey, &libID, &out.Facet, &out.ValueKey, &out.State, &startedAt, &finishedAt, &out.ItemsAdded, &out.Error, &current)
	out.Outdated = current == 0
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FacetExpansion{}, false, nil
		}
		return FacetExpansion{}, false, err
	}
	out.LibraryID = core.LibraryID(libID)
	if startedAt > 0 {
		out.StartedAt = time.Unix(startedAt, 0).UTC()
	}
	if finishedAt > 0 {
		out.FinishedAt = time.Unix(finishedAt, 0).UTC()
	}
	return out, true, nil
}

// MarkFacetExpansionRunning upserts a running expansion record for
// (pluginID, kindKey, facet, valueKey).
func (s *Service) MarkFacetExpansionRunning(ctx context.Context, pluginID, kindKey string, libraryID core.LibraryID, facet, valueKey string) error {
	if s == nil || s.db == nil {
		return nil
	}
	nowUnix := s.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO plugin_facet_expansions(plugin_id, kind_key, library_id, facet, value_key, state, started_at, finished_at, items_added, error, decl_hash)
VALUES(?, ?, ?, ?, ?, ?, ?, 0, 0, '', COALESCE((SELECT decl_hash FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?), ''))
ON CONFLICT(plugin_id, kind_key, facet, value_key) DO UPDATE SET
	library_id  = excluded.library_id,
	state       = excluded.state,
	started_at  = excluded.started_at,
	finished_at = 0,
	items_added = 0,
	error       = '',
	decl_hash   = excluded.decl_hash`,
		pluginID, kindKey, int64(libraryID), facet, valueKey, ExpansionStateRunning, nowUnix, pluginID, kindKey,
	)
	return err
}

// FinishFacetExpansion records completion or failure of a background facet
// expansion in plugin_facet_expansions.
func (s *Service) FinishFacetExpansion(ctx context.Context, pluginID, kindKey string, libraryID core.LibraryID, facet, valueKey, state string, itemsAdded int, errMsg string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if len(errMsg) > 1024 {
		errMsg = errMsg[:1024]
	}
	nowUnix := s.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO plugin_facet_expansions(plugin_id, kind_key, library_id, facet, value_key, state, started_at, finished_at, items_added, error, decl_hash)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT decl_hash FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?), ''))
ON CONFLICT(plugin_id, kind_key, facet, value_key) DO UPDATE SET
	library_id  = excluded.library_id,
	state       = excluded.state,
	finished_at = excluded.finished_at,
	items_added = excluded.items_added,
	error       = excluded.error`,
		pluginID, kindKey, int64(libraryID), facet, valueKey, state, nowUnix, nowUnix, int64(itemsAdded), errMsg, pluginID, kindKey,
	)
	return err
}

// CountKindItems returns the stored item count for (pluginID, kindKey).
func (s *Service) CountKindItems(ctx context.Context, pluginID, kindKey string) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	return s.countKindItems(ctx, pluginID, kindKey)
}

// LookupPersonFacetByName resolves a people-field bucket label within libraryID
// to its synthetic plugin person ID and canonical display name when present.
func (s *Service) LookupPersonFacetByName(ctx context.Context, libraryID core.LibraryID, field, name string) (personID int64, canonicalName string, ok bool, err error) {
	if s == nil || s.db == nil || libraryID <= 0 || field == "" || strings.TrimSpace(name) == "" {
		return 0, "", false, nil
	}
	trimmed := strings.TrimSpace(name)
	if libTally, tErr := s.libraryTally(ctx, libraryID); tErr == nil && libTally.active {
		if libTally.libTotal == 0 {
			return 0, "", false, nil
		}
		if candPID := core.PluginPersonID(trimmed); core.IsPluginPersonID(candPID) {
			err = s.db.QueryRowContext(ctx, `
SELECT v_int, label
FROM plugin_item_person_tally
WHERE library_id = ? AND level = '' AND field = ? AND v_int = ?`,
				int64(libraryID), field, candPID).Scan(&personID, &canonicalName)
			if err == nil && core.IsPluginPersonID(personID) && canonicalName != "" {
				return personID, canonicalName, true, nil
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, "", false, err
			}
		}
		isPG := store.DialectOf(s.db) == store.Postgres
		if folded := FoldFieldText(trimmed); folded != "" {
			orderBy := `label_lower ASC, label ASC, v_int ASC`
			if isPG {
				orderBy = `left(label_lower, 256) ASC, label_lower ASC, label ASC, v_int ASC`
			}
			err = s.db.QueryRowContext(ctx, `
SELECT v_int, label
FROM plugin_item_person_tally
WHERE library_id = ? AND level = '' AND field = ? AND label_fold = ?
ORDER BY `+orderBy+`
LIMIT 1`, int64(libraryID), field, folded).Scan(&personID, &canonicalName)
			if err == nil && core.IsPluginPersonID(personID) && canonicalName != "" {
				return personID, canonicalName, true, nil
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return 0, "", false, err
			}
		}
		return 0, "", false, nil
	}

	isPG := store.DialectOf(s.db) == store.Postgres
	libCheck := `i.library_id + 0 = ?`
	if isPG {
		libCheck = `i.library_id = ?`
	}
	if isPG {
		err = s.db.QueryRowContext(ctx, `
SELECT f.v_int, f.v_text
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND left(f.v_text, 256) = ? AND f.v_text = ? AND f.v_int > 0 AND `+libCheck+`
LIMIT 1`, field, pgTextPrefix(trimmed), trimmed, int64(libraryID)).Scan(&personID, &canonicalName)
	} else {
		err = s.db.QueryRowContext(ctx, `
SELECT f.v_int, f.v_text
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_text = ? AND f.v_int > 0 AND `+libCheck+`
LIMIT 1`, field, trimmed, int64(libraryID)).Scan(&personID, &canonicalName)
	}
	if err == nil && core.IsPluginPersonID(personID) {
		return personID, canonicalName, true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, err
	}
	folded := FoldFieldText(trimmed)
	if folded == "" {
		return 0, "", false, nil
	}
	if isPG {
		err = s.db.QueryRowContext(ctx, `
SELECT f.v_int, f.v_text
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND left(f.v_fold, 256) = ? AND f.v_fold = ? AND f.v_int > 0 AND `+libCheck+`
LIMIT 1`, field, pgTextPrefix(folded), folded, int64(libraryID)).Scan(&personID, &canonicalName)
	} else {
		err = s.db.QueryRowContext(ctx, `
SELECT f.v_int, f.v_text
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE f.field = ? AND f.v_fold = ? AND f.v_int > 0 AND `+libCheck+`
LIMIT 1`, field, folded, int64(libraryID)).Scan(&personID, &canonicalName)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if !core.IsPluginPersonID(personID) {
		return 0, "", false, nil
	}
	return personID, canonicalName, true, nil
}

// LookupPersonFacetsByID returns the distinct (field, name) pairs among fields
// crediting personID in libraryID.
func (s *Service) LookupPersonFacetsByID(ctx context.Context, libraryID core.LibraryID, fields []string, personID int64) ([]PersonFacetMatch, error) {
	if s == nil || s.db == nil || libraryID <= 0 || len(fields) == 0 || !core.IsPluginPersonID(personID) {
		return nil, nil
	}
	placeholders := make([]string, len(fields))
	if libTally, tErr := s.libraryTally(ctx, libraryID); tErr == nil && libTally.active {
		if libTally.libTotal == 0 {
			return nil, nil
		}
		args := make([]any, 0, 2+len(fields))
		args = append(args, int64(libraryID), personID)
		for i, fk := range fields {
			placeholders[i] = "?"
			args = append(args, fk)
		}
		rows, err := s.db.QueryContext(ctx, `
SELECT field, label
FROM plugin_item_person_tally
WHERE library_id = ? AND level = '' AND v_int = ? AND field IN (`+strings.Join(placeholders, ",")+`)
ORDER BY field ASC`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []PersonFacetMatch
		for rows.Next() {
			var m PersonFacetMatch
			if err := rows.Scan(&m.Field, &m.Name); err != nil {
				return nil, err
			}
			m.Name = strings.TrimSpace(m.Name)
			if m.Field != "" && m.Name != "" {
				out = append(out, m)
			}
		}
		return out, rows.Err()
	}

	libCheck := `i.library_id + 0 = ?`
	if store.DialectOf(s.db) == store.Postgres {
		libCheck = `i.library_id = ?`
	}
	args := make([]any, 0, 2+len(fields))
	args = append(args, int64(libraryID), personID)
	for i, fk := range fields {
		placeholders[i] = "?"
		args = append(args, fk)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT f.field, MIN(f.v_text)
FROM plugin_item_fields f
JOIN plugin_items i ON i.item_ref = f.item_ref
WHERE `+libCheck+` AND f.v_int = ? AND f.v_int <> 0 AND f.field IN (`+strings.Join(placeholders, ",")+`) AND f.v_text <> ''
GROUP BY f.field
ORDER BY f.field ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PersonFacetMatch
	for rows.Next() {
		var m PersonFacetMatch
		if err := rows.Scan(&m.Field, &m.Name); err != nil {
			return nil, err
		}
		m.Name = strings.TrimSpace(m.Name)
		if m.Field != "" && m.Name != "" {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// ChildrenFreshness checks in O(1) via the (library_id, parent_ref) index
// whether parentRef currently has at least one stored child row and returns
// a sample freshness timestamp (the newer of seen_at and updated_at).
func (s *Service) ChildrenFreshness(ctx context.Context, libraryID core.LibraryID, parentRef core.ItemRef) (bool, time.Time, error) {
	if s == nil || s.db == nil || libraryID <= 0 || strings.TrimSpace(string(parentRef)) == "" {
		return false, time.Time{}, nil
	}
	var seenAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
SELECT seen_at, updated_at
FROM plugin_items
WHERE library_id = ? AND parent_ref = ?
LIMIT 1`, int64(libraryID), string(parentRef)).Scan(&seenAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, err
	}
	ts := max(seenAt, updatedAt)
	if ts <= 0 {
		return true, time.Time{}, nil
	}
	return true, time.Unix(ts, 0).UTC(), nil
}

// ChildrenCountAndFreshness returns the stored direct child count and newest
// seen/updated timestamp for parentRef using the (library_id, parent_ref)
// index. Used to detect partially populated containers whose ChildCount
// exceeds the currently stored children.
func (s *Service) ChildrenCountAndFreshness(ctx context.Context, libraryID core.LibraryID, parentRef core.ItemRef) (int, time.Time, error) {
	if s == nil || s.db == nil || libraryID <= 0 || strings.TrimSpace(string(parentRef)) == "" {
		return 0, time.Time{}, nil
	}
	var count int
	var maxSeen, maxUpdated int64
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(MAX(seen_at), 0), COALESCE(MAX(updated_at), 0)
FROM plugin_items
WHERE library_id = ? AND parent_ref = ?`, int64(libraryID), string(parentRef)).Scan(&count, &maxSeen, &maxUpdated)
	if err != nil {
		return 0, time.Time{}, err
	}
	ts := max(maxSeen, maxUpdated)
	if ts <= 0 {
		return count, time.Time{}, nil
	}
	return count, time.Unix(ts, 0).UTC(), nil
}
