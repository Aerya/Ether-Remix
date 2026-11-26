package collections

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Plugin list rows: a plugin list source or Stremio add-on catalogue placed
// directly on a Recommendations page. No collection backs it. The list is
// read in the background the first time a page asks for it and kept for
// the list's refresh interval; a viewer request serves what is kept and
// never waits on the add-on. Nothing reads a list nobody shows.

const (
	// listRowLimit is how many titles a plugin list row keeps.
	listRowLimit = 100
	// listRowRetry is how long a failed read waits before the next try.
	listRowRetry = 5 * time.Minute
	// listRowMax bounds the lists kept (distinct sources and parameters).
	listRowMax = 256
)

type listRowEntry struct {
	keys     []core.RatingKey
	media    string
	builtAt  time.Time
	failedAt time.Time
	building bool
	usedAt   time.Time
}

type listRows struct {
	mu      sync.Mutex
	entries map[string]*listRowEntry
}

func (s *Service) listRowCache() *listRows {
	s.listRowsOnce.Do(func() { s.listRows = &listRows{entries: map[string]*listRowEntry{}} })
	return s.listRows
}

// pluginListSpec is the ephemeral spec a plugin list row reads through.
func (s *Service) pluginListSpec(kind SourceKind, kindID string, params map[string]string) core.CollectionSpec {
	desc := kind.Describe()
	name := strings.TrimSpace(desc.Label.Resolved)
	if name == "" {
		name = kindID
	}
	mediaType := MediaMixed
	if len(desc.MediaTypes) == 1 {
		switch desc.MediaTypes[0] {
		case "movie":
			mediaType = MediaMovie
		case "show":
			mediaType = MediaShow
		}
	}
	sp := core.CollectionSpec{
		Name: name, MediaType: mediaType,
		Sources: []core.CollectionSource{{Kind: kindID, Params: params}},
		Match:   MatchAll, Sort: "source", Limit: listRowLimit, Refresh: RefreshDaily, Enabled: true,
	}
	Normalize(&sp)
	return sp
}

// pluginListRow serves a plugin list row for a viewer, or nil while the
// list has not been read yet.
func (s *Service) pluginListRow(ctx context.Context, user *core.User, id string) (*core.CollectionRow, error) {
	kindID, params, ok := core.ParsePluginListRowID(id)
	if !ok {
		return nil, core.ErrNotFound
	}
	kind, ok := s.kindRegistry().Lookup(kindID)
	if !ok {
		return nil, core.ErrNotFound
	}
	sig := core.PluginListRowID(kindID, params)
	ttl := time.Hour
	if timed, ok := kind.(interface{ MinRefresh() time.Duration }); ok && timed.MinRefresh() > ttl {
		ttl = timed.MinRefresh()
	}
	c := s.listRowCache()
	now := s.now()
	c.mu.Lock()
	e := c.entries[sig]
	if e == nil {
		if len(c.entries) >= listRowMax {
			c.evictOldestLocked()
		}
		e = &listRowEntry{}
		c.entries[sig] = e
	}
	e.usedAt = now
	stale := e.builtAt.IsZero() || now.Sub(e.builtAt) >= ttl
	if stale && !e.building && now.Sub(e.failedAt) >= listRowRetry {
		e.building = true
		sp := s.pluginListSpec(kind, kindID, params)
		go s.readPluginList(sig, sp)
	}
	keys, media := e.keys, e.media
	c.mu.Unlock()
	if len(keys) == 0 {
		return nil, nil
	}
	snap, err := s.snapshotForServe(ctx, keys, false)
	if err != nil {
		return nil, err
	}
	present := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if snap.entries[k] != nil {
			present = append(present, k)
		}
	}
	keys = s.filterForUser(ctx, present, personalRules{}, user, snap)
	if len(keys) == 0 {
		return nil, nil
	}
	count := len(keys)
	page, dropped := rowPageKeys(keys, media)
	items, err := s.mirror.Titles(ctx, page)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(kind.Describe().Label.Resolved)
	if title == "" {
		title = kindID
	}
	hub := core.Hub{ID: sig, Title: title, Style: "shelf", Promoted: true, Items: items, More: dropped || count > len(items), Context: "hub.home.collection"}
	switch media {
	case MediaMovie:
		hub.Type = core.TypeMovie
	case MediaShow:
		hub.Type = core.TypeShow
	}
	return &core.CollectionRow{Hub: hub, CollectionID: sig}, nil
}

// readPluginList reads one list in the background and keeps its titles.
func (s *Service) readPluginList(sig string, sp core.CollectionSpec) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := s.evaluate(ctx, sp, evalOptions{seed: sig, persist: true})
	c := s.listRowCache()
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[sig]
	if e == nil {
		return
	}
	e.building = false
	if err != nil {
		e.failedAt = s.now()
		s.log.Warn("collections: plugin list row", "source", sig, "err", err)
		return
	}
	keys := res.keys
	if len(keys) > listRowLimit {
		keys = keys[:listRowLimit]
	}
	e.keys, e.media, e.builtAt = keys, sp.MediaType, s.now()
	go s.notifyBuilt()
}

func (c *listRows) evictOldestLocked() {
	var oldest string
	var at time.Time
	for k, e := range c.entries {
		if !e.building && (oldest == "" || e.usedAt.Before(at)) {
			oldest, at = k, e.usedAt
		}
	}
	if oldest != "" {
		delete(c.entries, oldest)
	}
}

// purgeSpecs deletes the collections matching where, with their members.
func (s *Service) purgeSpecs(ctx context.Context, where string, args ...any) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM collections_specs WHERE `+where, args...)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM collections_members WHERE spec_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collections_specs WHERE id=?`, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.forgetRowMemo(ctx)
	for _, id := range ids {
		s.changed(id)
	}
	return nil
}
