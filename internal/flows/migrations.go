package flows

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	store.Register("flows", 1, flowsMigration1)
}

const flowsMigration1 = `
CREATE TABLE flows_definitions (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 0,
	disabled_reason TEXT NOT NULL DEFAULT '',
	version INTEGER NOT NULL DEFAULT 1,
	graph_json TEXT NOT NULL,
	limits_json TEXT NOT NULL DEFAULT '{}',
	trigger_events_json TEXT NOT NULL DEFAULT '[]',
	tags_json TEXT NOT NULL DEFAULT '[]',
	template_id TEXT NOT NULL DEFAULT '',
	consecutive_failures INTEGER NOT NULL DEFAULT 0,
	webhook_token_hash TEXT NOT NULL DEFAULT '',
	created_at_ms INTEGER NOT NULL,
	updated_at_ms INTEGER NOT NULL,
	last_run_at_ms INTEGER NOT NULL DEFAULT 0,
	last_status TEXT NOT NULL DEFAULT ''
);
CREATE INDEX flows_definitions_enabled ON flows_definitions(enabled);
CREATE INDEX flows_definitions_webhook ON flows_definitions(webhook_token_hash) WHERE webhook_token_hash <> '';

CREATE TABLE flows_runs (
	id TEXT PRIMARY KEY,
	flow_id TEXT NOT NULL REFERENCES flows_definitions(id) ON DELETE CASCADE,
	flow_version INTEGER NOT NULL DEFAULT 1,
	trigger_kind TEXT NOT NULL,
	event_id TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL DEFAULT '',
	correlation_key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	attempt INTEGER NOT NULL DEFAULT 1,
	dry_run INTEGER NOT NULL DEFAULT 0,
	trigger_json TEXT NOT NULL DEFAULT '{}',
	context_json TEXT NOT NULL DEFAULT '{}',
	error TEXT NOT NULL DEFAULT '',
	step_count INTEGER NOT NULL DEFAULT 0,
	flow_depth INTEGER NOT NULL DEFAULT 0,
	started_at_ms INTEGER NOT NULL,
	updated_at_ms INTEGER NOT NULL,
	finished_at_ms INTEGER NOT NULL DEFAULT 0,
	wake_at_ms INTEGER NOT NULL DEFAULT 0,
	lease_owner TEXT NOT NULL DEFAULT '',
	lease_until_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX flows_runs_flow_started ON flows_runs(flow_id, started_at_ms DESC);
CREATE INDEX flows_runs_started ON flows_runs(started_at_ms DESC);
CREATE INDEX flows_runs_due ON flows_runs(status, wake_at_ms) WHERE status IN ('pending', 'waiting');
CREATE INDEX flows_runs_lease ON flows_runs(status, lease_until_ms) WHERE status = 'running';
CREATE INDEX flows_runs_corr ON flows_runs(flow_id, correlation_key, started_at_ms) WHERE status IN ('pending', 'running', 'waiting');

CREATE TABLE flows_run_steps (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id TEXT NOT NULL REFERENCES flows_runs(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	node_id TEXT NOT NULL,
	node_kind TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'pending',
	idempotency_key TEXT NOT NULL DEFAULT '',
	branch TEXT NOT NULL DEFAULT '',
	input_json TEXT NOT NULL DEFAULT '{}',
	output_json TEXT NOT NULL DEFAULT '{}',
	error TEXT NOT NULL DEFAULT '',
	started_at_ms INTEGER NOT NULL,
	finished_at_ms INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	UNIQUE(run_id, seq)
);
CREATE INDEX flows_run_steps_run ON flows_run_steps(run_id, seq);

CREATE TABLE flows_waiters (
	id TEXT PRIMARY KEY,
	run_id TEXT NOT NULL REFERENCES flows_runs(id) ON DELETE CASCADE,
	flow_id TEXT NOT NULL REFERENCES flows_definitions(id) ON DELETE CASCADE,
	node_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	correlation_key TEXT NOT NULL DEFAULT '',
	filter_json TEXT NOT NULL DEFAULT '',
	timeout_at_ms INTEGER NOT NULL,
	created_at_ms INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'waiting'
);
CREATE INDEX flows_waiters_match ON flows_waiters(status, event_type, correlation_key) WHERE status = 'waiting';
CREATE INDEX flows_waiters_timeout ON flows_waiters(status, timeout_at_ms) WHERE status = 'waiting';

CREATE TABLE flows_counters (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	flow_id TEXT NOT NULL REFERENCES flows_definitions(id) ON DELETE CASCADE,
	node_id TEXT NOT NULL,
	group_key TEXT NOT NULL,
	at_ms INTEGER NOT NULL
);
CREATE INDEX flows_counters_lookup ON flows_counters(flow_id, node_id, group_key, at_ms);
CREATE INDEX flows_counters_at ON flows_counters(at_ms);

CREATE TABLE flows_dedupe (
	flow_id TEXT NOT NULL REFERENCES flows_definitions(id) ON DELETE CASCADE,
	node_id TEXT NOT NULL,
	dedupe_key TEXT NOT NULL,
	hits INTEGER NOT NULL DEFAULT 1,
	first_at_ms INTEGER NOT NULL,
	last_at_ms INTEGER NOT NULL,
	expires_at_ms INTEGER NOT NULL,
	PRIMARY KEY(flow_id, node_id, dedupe_key)
);
CREATE INDEX flows_dedupe_expires ON flows_dedupe(expires_at_ms);

CREATE TABLE flows_schedules (
	flow_id TEXT NOT NULL REFERENCES flows_definitions(id) ON DELETE CASCADE,
	node_id TEXT NOT NULL,
	next_run_ms INTEGER NOT NULL DEFAULT 0,
	last_run_ms INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY(flow_id, node_id)
);
CREATE INDEX flows_schedules_next ON flows_schedules(next_run_ms);
`
