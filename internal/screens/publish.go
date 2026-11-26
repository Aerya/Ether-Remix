package screens

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// The owner's per-app switches for document screens and surfaces ("Show in
// apps"). A switch the owner has never touched follows the manifest's
// default; once set it is sticky across plugin updates and restarts.

func init() {
	store.Register("plugin_screen_publish", 1, `
CREATE TABLE plugin_screen_publish (
	plugin_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	item_id TEXT NOT NULL,
	frontend TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	updated_at BIGINT NOT NULL,
	PRIMARY KEY(plugin_id, kind, item_id, frontend)
);
`)
}

// PublishStore holds the switches in memory, backed by the database.
type PublishStore struct {
	db *sql.DB

	once   sync.Once
	mu     sync.RWMutex
	values map[publishKey]bool

	orderMu sync.Mutex
	order   map[string][]string
}

type publishKey struct {
	plugin, kind, id, frontend string
}

// NewPublishStore returns a store over db (nil keeps it in memory).
func NewPublishStore(db *sql.DB) *PublishStore {
	return &PublishStore{db: db, values: map[publishKey]bool{}}
}

func (p *PublishStore) load(ctx context.Context) {
	p.once.Do(func() {
		if p.db == nil {
			return
		}
		rows, err := p.db.QueryContext(ctx, `SELECT plugin_id, kind, item_id, frontend, enabled FROM plugin_screen_publish`)
		if err != nil {
			return
		}
		defer rows.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for rows.Next() {
			var k publishKey
			var on int
			if rows.Scan(&k.plugin, &k.kind, &k.id, &k.frontend, &on) == nil {
				p.values[k] = on == 1
			}
		}
	})
}

// Published reports the switch, or def when the owner never set it.
func (p *PublishStore) Published(ctx context.Context, pluginID, kind, id, frontendID string, def bool) bool {
	if p == nil {
		return def
	}
	p.load(ctx)
	p.mu.RLock()
	v, ok := p.values[publishKey{pluginID, kind, id, frontendID}]
	p.mu.RUnlock()
	if !ok {
		return def
	}
	return v
}

// Set records the owner's switch.
func (p *PublishStore) Set(ctx context.Context, pluginID, kind, id, frontendID string, enabled bool) error {
	p.load(ctx)
	if p.db != nil {
		on := 0
		if enabled {
			on = 1
		}
		if _, err := p.db.ExecContext(ctx, `INSERT INTO plugin_screen_publish(plugin_id,kind,item_id,frontend,enabled,updated_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(plugin_id,kind,item_id,frontend) DO UPDATE SET enabled=excluded.enabled, updated_at=excluded.updated_at`,
			pluginID, kind, id, frontendID, on, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.values[publishKey{pluginID, kind, id, frontendID}] = enabled
	p.mu.Unlock()
	return nil
}

func init() {
	store.Register("plugin_screen_overlay_order", 1, `
CREATE TABLE plugin_screen_overlay_order (
	frontend TEXT PRIMARY KEY,
	ids TEXT NOT NULL,
	updated_at BIGINT NOT NULL
);
`)
}

// OverlayOrder is the owner's order of player overlays per app, by surface
// wire id: when more than two overlays share an anchor, the first two in this
// order show. Unlisted overlays follow in install order.
func (p *PublishStore) OverlayOrder(ctx context.Context, frontendID string) []string {
	if p == nil || p.db == nil {
		return nil
	}
	p.orderMu.Lock()
	defer p.orderMu.Unlock()
	if p.order == nil {
		p.order = map[string][]string{}
		rows, err := p.db.QueryContext(ctx, `SELECT frontend, ids FROM plugin_screen_overlay_order`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var fe, raw string
				if rows.Scan(&fe, &raw) == nil {
					var ids []string
					if json.Unmarshal([]byte(raw), &ids) == nil {
						p.order[fe] = ids
					}
				}
			}
		}
	}
	return append([]string(nil), p.order[frontendID]...)
}

// SetOverlayOrder records the owner's order (at most 64 ids).
func (p *PublishStore) SetOverlayOrder(ctx context.Context, frontendID string, ids []string) error {
	if len(ids) > 64 {
		ids = ids[:64]
	}
	raw, _ := json.Marshal(ids)
	if p.db != nil {
		if _, err := p.db.ExecContext(ctx, `INSERT INTO plugin_screen_overlay_order(frontend, ids, updated_at) VALUES(?,?,?)
			ON CONFLICT(frontend) DO UPDATE SET ids=excluded.ids, updated_at=excluded.updated_at`, frontendID, string(raw), time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	p.orderMu.Lock()
	if p.order == nil {
		p.order = map[string][]string{}
	}
	p.order[frontendID] = append([]string(nil), ids...)
	p.orderMu.Unlock()
	return nil
}
