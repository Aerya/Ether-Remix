package events

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	store.Register("events", 1, eventsOutboxMigration1)
}

const eventsOutboxMigration1 = `
CREATE TABLE events_outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL UNIQUE,
	event_type TEXT NOT NULL,
	version INTEGER NOT NULL DEFAULT 1,
	occurred_at_ms INTEGER NOT NULL,
	node_id TEXT NOT NULL DEFAULT '',
	correlation_key TEXT NOT NULL DEFAULT '',
	envelope_json TEXT NOT NULL,
	created_at_ms INTEGER NOT NULL,
	acked_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX events_outbox_pending ON events_outbox(acked_at_ms, id) WHERE acked_at_ms = 0;
CREATE INDEX events_outbox_created ON events_outbox(created_at_ms);
`
