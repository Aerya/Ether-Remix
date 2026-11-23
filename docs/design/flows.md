# Ether Flows — Architecture & Core Contracts (v0.3.17)

**Status:** Approved foundation (F0)  
**Packages:** `internal/events`, `internal/flows`  
**Migrations:** `events/1` (`events_outbox`), `flows/1` (`flows_*`)

Flows is Ether's server-wide, event-driven automation engine. Owners wire **triggers**, **conditions**, **logic nodes** (branch, delay, wait-for-event, sliding windows, dedupe/throttle), and **actions** into visual graphs in the console. Notifications, playback remediation, library refreshes, release pinning, viewer restrictions, outbound webhooks, and plugin extensions all share one typed event bus and one durable runtime.

---

## 1. Non-Negotiable Invariants

1. **Never on the play path.** Flows react asynchronously. `events.Bus.Publish` performs zero I/O and never blocks publishers; slow consumers drop with a counter. Pre-playback admission and filtering (safety policies, parental limits, bitrate caps, release ranking/rules) stay in `internal/safety`, `internal/viewerpolicy`, `internal/releasepolicy`, and `internal/resolver`.
2. **Leader-only execution in clusters.** Events are published on whichever node observes them, stamped with `node`. Flow evaluation, timers, waiters, counters, and actions run exclusively on the cluster leader inside `cluster.RunAsLeader("flows", ...)`.
3. **Typed schemas are the single source of truth.** Every built-in event has a Go payload struct registered in `events.Registry`; every action declares typed `ParamSchema` and `FieldSchema` outputs. The console editor, static validator, condition evaluator, and plugin SDK all read from the same registries.
4. **Durable and replay-safe.** Runs, steps, delays, event waiters, sliding windows, and dedupe keys live in SQLite (standalone) and PostgreSQL (cluster). Side-effecting actions receive deterministic idempotency keys and are never blindly re-run after an ambiguous crash unless explicitly marked `Idempotent: true`.
5. **Backwards-compatible notifications.** Existing `internal/notify` channels and event rules continue working unchanged; Flows wraps and extends them rather than breaking existing owner configuration.

---

## 2. Event Envelope & Delivery Semantics

### 2.1 Envelope (`internal/events.Envelope`)

```json
{
  "id": "evt_019a...",
  "type": "playback.session.failed",
  "version": 1,
  "occurredAt": "2026-11-19T23:15:02.418Z",
  "node": "node-east-1",
  "source": "ether",
  "correlation": {
    "sessionId": "sess_9f2a",
    "playId": "play_8c11",
    "titleId": "movie:603",
    "viewerId": 7
  },
  "meta": {
    "durable": false,
    "flowDepth": 0
  },
  "payload": {
    "titleName": "The Matrix",
    "username": "alex",
    "error": "every release failed (3 tried): missing articles",
    "delivery": "direct_play"
  }
}
```

| Field | Semantics |
| --- | --- |
| `id` | Globally unique random ID (`evt_<16 hex>`), generated at publish if empty. |
| `type` | Canonical event type (`domain.noun.verb`, or `plugin:<pluginId>/<noun>.<verb>`). |
| `version` | Positive schema version (starts at `1`). Additive payload fields stay in `v1`; breaking renames/retypes bump `version`. |
| `occurredAt` | UTC event timestamp (stamps `now` if omitted). |
| `node` | Origin node ID (`cluster.Self().ID`, or local node ID in standalone). |
| `source` | `"ether"` for built-in emitters, `"plugin:<pluginId>"` for plugin-published events. |
| `correlation` | Structured IDs (`sessionId`, `playId`, `titleId`, `viewerId`, `channelId`, `sourceId`, `pluginId`, `deviceId`, `libraryId`) plus optional explicit `key`. |
| `meta` | Runtime metadata: `durable`, `flowDepth` (recursion guard), `originRunId`, `dryRun`. |
| `payload` | JSON object matching the registered `EventSchema`. Secrets are redacted before persistence or API display. |

### 2.2 Delivery Semantics & Cluster Rules

- **In-process bus (`events.Bus`):**
  - Subscribers register exact types (`playback.session.failed`), prefix wildcards (`playback.*`, `livetv.input.*`, `plugin:com.acme/*`), or `"*"`.
  - Each subscriber owns a bounded channel (default 256). `Publish` does a non-blocking send; on full buffer it increments per-subscriber and global `Dropped` atomic counters, invokes `OnDrop`, and emits a rate-limited warning (never blocks the publisher).
  - Dry-run events (`meta.dryRun == true`) are ignored by normal subscribers unless `IncludeDryRun: true`.
- **Standalone (SQLite):**
  - The single process is always the leader. The flow runtime subscribes directly to `events.Bus`.
  - When an event matches an enabled flow trigger (passing any trigger pre-filter) or an active `flows_waiters` row, the runtime atomically commits the run/wake row before acknowledging ingress (at-least-once from that point forward).
  - Events marked `meta.durable == true` (e.g. backup failure, update rollback, plugin-published events) can also be persisted to `events_outbox` before bus dispatch so a crash mid-delivery replays on restart.
- **Clustered (PostgreSQL):**
  - Publishers emit locally to their node's `events.Bus`.
  - The leader maintains the active subscription set (union of event patterns referenced by enabled flows and active `flows_waiters`) and broadcasts changes over `clusterbus` (`flows:subs`).
  - Follower nodes subscribe a lightweight bridge to their local `events.Bus`: **only events matching the active subscription set (or `meta.durable == true`)** are inserted into `events_outbox`, followed by a tiny `clusterbus` wake ping (`flows:outbox`).
  - The leader drains `events_outbox` (`WHERE acked_at_ms = 0 ORDER BY id`), feeds envelopes into the flow runtime, and marks `acked_at_ms` once all matching runs/waiters are committed in a single transaction.
  - This avoids PostgreSQL's 8 KB `NOTIFY` payload limit, survives leader failover without losing events, and causes **zero DB writes** for event types no flow listens to.
- **Correlation ordering:**
  - Every envelope resolves an effective correlation key (`Correlation.EffectiveKey`: explicit `key`, else first present of `playId`, `sessionId`, `channelId`, `titleId`, `viewerId`, `sourceId`, `pluginId`, `deviceId`, `libraryId`, `node`, `id`).
  - When a flow sets `limits.orderedByKey: true`, runs sharing `(flow_id, correlation_key)` execute sequentially in `occurred_at_ms, id` order; distinct correlation keys execute concurrently up to `limits.concurrency`.

---

## 3. Event Catalogue

### 3.1 Naming, Schema & Versioning

- **Built-in naming:** `domain.noun.verb`, three lowercase snake_case segments (`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`).
- **Plugin naming:** `plugin:<pluginId>/<noun>.<verb>` (matching `internal/extend.PluginID`, avoiding ambiguity with dots inside plugin IDs like `com.example.sync`).
- **Typed Go registration:** Each built-in event defines a Go struct with `json` and `event` tags registered via `events.RegisterStruct[T](reg, meta)`. Supported `event` tag directives: `label=...`, `desc=...`, `unit=...`, `enum=a|b|c`, `pii`, `secret`, `duration_ms`, `example=...`, `nofilter`.
- **Field types:** `string`, `int`, `float`, `bool`, `duration_ms`, `timestamp`, `enum`, `string_list`, `object`.
- **PII & Secret flags:**
  - `Secret: true` fields are masked (`[redacted]`) before storing step snapshots, run logs, or sending to the console API, and cannot be interpolated into outbound unprivileged logs.
  - `PII: true` fields (e.g. username, email, IP address) are automatically stripped when forwarding events to plugins that lack `permissions.viewers.identity`.
- **Versioning:** Schemas start at `version: 1`. Additive fields keep `version: 1`. Removing or retyping fields creates `version: 2`, marks old fields/events `deprecated: true` with `replacedBy`, and triggers console validation warnings without breaking saved flows.

### 3.2 Inventory of Existing Signals & Legacy Mapping

All existing Ether event sources consolidate into this catalogue:

| Legacy Source | Legacy Name | Canonical Event Type |
| --- | --- | --- |
| `plugins` (`contract.go`, 12) | `playback.started` | `playback.session.started` |
| `plugins` | `playback.paused` | `playback.session.paused` |
| `plugins` | `playback.resumed` | `playback.session.resumed` |
| `plugins` | `playback.stopped` | `playback.session.stopped` |
| `plugins` | `playback.completed` | `playback.session.completed` |
| `plugins` | `playback.error` | `playback.session.failed` |
| `plugins` | `playback.failover` | `playback.source.failover` |
| `plugins` | `title.available` | `library.title.available` |
| `plugins` | `viewer.added` | `viewer.account.joined` |
| `plugins` | `viewer.access_changed` | `viewer.access.changed` |
| `plugins` | `collection.rebuilt` | `library.collection.rebuilt` |
| `plugins` | `server.health_changed` | `system.health.changed` |
| `notify` (`events.go`, 22 + viewer) | `server.started` / `server.stopping` / `server.crash` | `system.server.started` / `stopping` / `crashed` |
| `notify` | `provider.outage` / `provider.recovered` | `provider.service.outage` / `recovered` |
| `notify` | `indexer.outage` / `indexer.recovered` / `indexer.budget` | `indexer.service.outage` / `recovered` / `indexer.budget.exhausted` |
| `notify` | `cache.full` / `cache.recovered` | `cache.budget.full` / `recovered` |
| `notify` | `playback.start` / `stop` / `error` / `failover` | `playback.session.started` / `stopped` / `failed` / `playback.source.failover` |
| `notify` | `cert.renewed` | `auth.certificate.renewed` |
| `notify` | `update.available` / `installed` / `rolledback` | `system.update.available` / `installed` / `rolled_back` |
| `notify` | `backup.failed` | `system.backup.failed` |
| `notify` | `user.joined` | `viewer.account.joined` |
| `notify` | `title.available` | `library.title.available` |
| `notify` | `newsletter.weekly` | `flows.digest.scheduled` (template) |
| `notify` (`viewer_push.go`) | `viewer.episode` | `library.episode.aired` |
| `playback` timeline (`timeline.go`) | `started`, `resolve`, `candidate`, `decision`, `quality`, `stall`, `failover`, `error`, `stop` | Mapped across `playback.*` and `resolver.*` below |
| `livetv` (`reserve_health.go`, `source_history.go`) | `InputStalled`, `InputRecovered`, `InputFailed`, source cooldown/damage | Mapped across `livetv.*` below |
| `auth` / `identity` / `stats` | `identity_viewer_deactivations`, `identity_grant_revocations`, `stats_device_registry` | Mapped across `viewer.*` and `auth.*` below |

### 3.3 Normalized Shared Playback Context (~70 Filterable Fields)

Every `playback.session.*`, `playback.stream.*`, `playback.marker.*`, and `playback.concurrency.exceeded` event embeds a shared `PlaybackSessionContext` struct (flattened by `events.RegisterStruct[T]` into dot-path fields) so conditions and templates can filter across the full session without custom scripts:

- **Title / Media:** `titleKey`, `titleName`, `showTitle`, `season`, `episode`, `mediaType` (`movie|episode|live|trailer`), `year`, `libraryId`, `libraryName`, `contentRating`, `genres` (`string_list`), `labels` (`string_list`), `actors` (`string_list`), `directors` (`string_list`), `studio`, `durationMs`, `posterUrl`.
- **Source media (`source.*`):** `container`, `videoCodec`, `videoResolution` (`4k|1080p|720p|480p`), `videoWidth`, `videoHeight`, `videoDynamicRange` (`sdr|hdr10|dv|hlg`), `audioCodec`, `audioChannels`, `bitrateKbps`, `fileSizeBytes`.
- **Active stream (`stream.*`):** `delivery` (`direct_play|remux|transcode`), `videoDecision` (`direct_play|copy|transcode`), `audioDecision`, `subtitleDecision`, `container`, `videoCodec`, `videoResolution`, `videoHeight`, `videoDynamicRange`, `audioCodec`, `audioChannels`, `bitrateKbps`, `bandwidthKbps`, `hwDecode`, `hwEncode`, `transcodeSpeed`, `throttled`.
- **Viewer / Client / Network:** `viewer.id`, `viewer.username*`, `viewer.role`, `viewer.tags` (`string_list`, e.g. `vip`, `family`, `kids`), `viewer.activeStreams`, `viewer.activeTranscodes`, `viewer.distinctActiveIPs`, `client.id`, `client.name`, `client.platform`, `client.product`, `client.version`, `client.protocol` (`vela|plex|jellyfin|dlna`), `network.remoteIP*`, `network.location` (`lan|wan`), `network.country`, `network.city`, `network.asn`, `network.isRelay`.
- **Server live counts (`server.*`):** `activeStreams`, `activeTranscodes`, `activeDirectPlays`, `totalBandwidthKbps`.

### 3.4 Planned Event Catalogue (119 Events across 14 Domains)

#### `playback` (17)
1. `playback.session.started` — `{sessionId, playId, ...PlaybackSessionContext}`
2. `playback.session.paused` — `{sessionId, playId, positionMs, durationMs, ...PlaybackSessionContext}`
3. `playback.session.resumed` — `{sessionId, playId, positionMs, pausedForMs, ...PlaybackSessionContext}`
4. `playback.session.stopped` — `{sessionId, playId, positionMs, durationMs, bytesServed, watchedRatio, stopReason, ...PlaybackSessionContext}`
5. `playback.session.completed` — `{sessionId, playId, durationMs, watchedMs, watchedRatio, thresholdPercent, ...PlaybackSessionContext}` *(fires when a stream crosses the configured watched % threshold)*
6. `playback.session.failed` — `{sessionId, playId, stage, error, releaseId, indexer, ...PlaybackSessionContext}` *(critical)*
7. `playback.session.terminated` — `{sessionId, playId, viewerId, actor, reason, message}`
8. `playback.source.failover` — `{sessionId, playId, titleKey, titleName, fromReleaseId, toReleaseId, fromIndexer, toIndexer, reason, attempt}`
9. `playback.stream.stalled` — `{sessionId, playId, stallCount, positionMs, bufferMs, ...PlaybackSessionContext}` *(pair with `logic.window` for "N buffers in T")*
10. `playback.stream.decision` — `{sessionId, playId, previousDelivery, delivery, videoDecision, audioDecision, subtitleDecision, maxHeight, bitrateKbps, hwDecode, hwEncode, reason, ...PlaybackSessionContext}` *(also fires on mid-stream decision change)*
11. `playback.stream.quality_changed` — `{sessionId, playId, fromHeight, toHeight, fromBitrateKbps, toBitrateKbps, reason}`
12. `playback.stream.seeked` — `{sessionId, playId, fromMs, toMs, deltaMs}`
13. `playback.marker.entered` — `{sessionId, playId, markerKind (intro|credits|commercial|recap), startMs, endMs, positionMs, ...PlaybackSessionContext}` *(Home Assistant / MQTT lighting triggers)*
14. `playback.marker.exited` — `{sessionId, playId, markerKind (intro|credits|commercial|recap), positionMs, ...PlaybackSessionContext}`
15. `playback.concurrency.exceeded` — `{sessionId, playId, limit, activeStreams, activeTranscodes, distinctActiveIPs, ...PlaybackSessionContext}`
16. `playback.admission.denied` — `{titleKey, titleName, viewerId, username*, client, remote, policy, reason}`
17. `playback.transcode.fallback` — `{sessionId, playId, fromHWAccel, toEncoder, reason, activeTranscodes}`

#### `livetv` (12)
18. `livetv.channel.tuned` — `{channelId, channelName, channelNumber, sourceId, providerId, mode, viewerId, username*, client}`
19. `livetv.channel.stopped` — `{channelId, channelName, sourceId, viewerId, watchedMs, bytesServed, mode}`
20. `livetv.input.stalled` — `{channelId, channelName, sourceId, transport, dataAgeMs, reserveDepthMs, stallAfterMs, reconnects}`
21. `livetv.input.recovered` — `{channelId, channelName, sourceId, transport, downtimeMs, reconnects}`
22. `livetv.input.lost` — `{channelId, channelName, sourceId, transport, reconnects, error, hasBackup}` *(critical)*
23. `livetv.source.failover` — `{channelId, channelName, fromSourceId, toSourceId, reason, mode}`
24. `livetv.source.degraded` — `{channelId, channelName, sourceId, damagePerHour, stallsPerHour, maxKeyframeGapMs, mediaSec}`
25. `livetv.source.cooldown` — `{channelId, sourceId, failures, errorClass, retryAfter}`
26. `livetv.provider.auth_failed` — `{providerId, providerName, accountId, statusCode, error}` *(critical)*
27. `livetv.provider.limit_reached` — `{providerId, providerName, activeConnections, maxConnections, channelId}`
28. `livetv.guide.refreshed` — `{sourceId, sourceName, channels, programs, durationMs}`
29. `livetv.guide.failed` — `{sourceId, sourceName, error, staleHours}`

#### `resolver` (8)
30. `resolver.search.completed` — `{titleKey, titleName, mediaType, candidateCount, eligibleCount, winnerReleaseId, durationMs, cacheHit}`
31. `resolver.search.empty` — `{titleKey, titleName, mediaType, profileId, indexersQueried, rejectedCount, topRejection}`
32. `resolver.release.selected` — `{titleKey, releaseId, releaseName, indexer, protocol, sizeBytes, resolution, hdr, score, pinned}`
33. `resolver.release.blocklisted` — `{releaseId, releaseName, titleKey, indexer, reason, actor}`
34. `resolver.pin.created` — `{titleKey, titleName, releaseId, releaseName, note, actor}`
35. `resolver.pin.removed` — `{titleKey, titleName, releaseId, actor}`
36. `resolver.pin.unhealthy` — `{titleKey, titleName, releaseId, status, reason}` *(critical)*
37. `resolver.pin.rejected` — `{titleKey, titleName, releaseId, client, reason}`

#### `indexer` (6)
38. `indexer.request.failed` — `{indexerId, indexerName, kind, statusCode, error, latencyMs}`
39. `indexer.service.outage` — `{indexerId, indexerName, kind, consecutiveFailures, error}` *(critical)*
40. `indexer.service.recovered` — `{indexerId, indexerName, kind, outageDurationMs}`
41. `indexer.budget.warning` — `{indexerId, indexerName, metric, used, limit, percentUsed}`
42. `indexer.budget.exhausted` — `{indexerId, indexerName, metric, used, limit, resetsAt}`
43. `indexer.rate.limited` — `{indexerId, indexerName, retryAfterMs, statusCode}`

#### `provider` (8)
44. `provider.service.outage` — `{providerId, providerName, kind, error, backoffSec}` *(critical)*
45. `provider.service.recovered` — `{providerId, providerName, kind, outageDurationMs}`
46. `provider.auth.rejected` — `{providerId, providerName, kind, statusCode, error}` *(critical)*
47. `provider.quota.warning` — `{providerId, providerName, kind, metric, used, limit, percentUsed}`
48. `provider.quota.exhausted` — `{providerId, providerName, kind, metric, used, limit}` *(critical)*
49. `provider.debrid.cached` — `{providerId, providerName, infoHash, titleKey, titleName, sizeBytes, waitMs}`
50. `provider.debrid.failed` — `{providerId, providerName, infoHash, titleKey, error}`
51. `provider.connections.saturated` — `{providerId, providerName, activeConnections, maxConnections, waitingReaders}`

#### `vault` (5)
52. `vault.record.stored` — `{vaultKey, kind, titleKey, releaseName, sizeBytes, compressedBytes, packId}`
53. `vault.record.dead` — `{vaultKey, kind, titleKey, releaseName, reason}`
54. `vault.payload.missing` — `{vaultKey, packId, nodeId, titleKey}`
55. `vault.pack.compacted` — `{packsCompacted, bytesReclaimed, durationMs}`
56. `vault.quota.warning` — `{usedBytes, budgetBytes, percentUsed, recordCount}`

#### `cache` (5)
57. `cache.budget.full` — `{cacheKind, usedBytes, budgetBytes, percentUsed}` *(critical)*
58. `cache.budget.recovered` — `{cacheKind, usedBytes, budgetBytes, percentUsed}`
59. `cache.eviction.spike` — `{cacheKind, evictedBytes, evictedEntries, windowSec}`
60. `cache.prefetch.completed` — `{titleKey, titleName, kind, bytesWarmed, durationMs}`
61. `cache.prefetch.failed` — `{titleKey, titleName, kind, error}`

#### `library` (11)
62. `library.title.added` — `{libraryId, libraryName, titleKey, titleName, showTitle, season, episode, mediaType, year, genres, posterUrl}`
63. `library.batch.added` — `{libraryId, libraryName, mediaType, showKey, showTitle, season, episodeCount, itemCount, titleNames, windowSec, posterUrl}` *(debounced/grouped recently added)*
64. `library.title.available` — `{titleKey, titleName, mediaType, year, source, watchedByCount}`
65. `library.title.unavailable` — `{titleKey, titleName, mediaType, reason}`
66. `library.episode.aired` — `{showKey, showTitle, episodeKey, episodeTitle, season, episode, airDate, followerCount}`
67. `library.sync.started` — `{libraryId, libraryName, sourceKind, trigger}`
68. `library.sync.completed` — `{libraryId, libraryName, sourceKind, added, updated, removed, durationMs}`
69. `library.sync.failed` — `{libraryId, libraryName, sourceKind, error}`
70. `library.collection.rebuilt` — `{collectionId, collectionName, sourceKind, itemCount, added, removed, durationMs}`
71. `library.watchlist.synced` — `{provider, viewerId, added, removed, totalItems, durationMs}`
72. `library.metadata.failed` — `{provider, titleKey, mediaType, error}`

#### `viewer` (9)
73. `viewer.account.joined` — `{viewerId, username*, role, source, inviteId}`
74. `viewer.account.deactivated` — `{viewerId, username*, reason, actor}`
75. `viewer.account.merged` — `{fromViewerId, intoViewerId, actor}`
76. `viewer.access.changed` — `{viewerId, username*, streamLimit, maxContentRating, allowedLibraries, actor}`
77. `viewer.safety.updated` — `{viewerId, username*, remoteDisabled, maxBitrateKbps, bedtimeEnabled, blockedGenresCount, actor}`
78. `viewer.invite.created` — `{inviteId, label, maxUses, expiresAt, actor}`
79. `viewer.invite.accepted` — `{inviteId, viewerId, username*}`
80. `viewer.watch.scrobbled` — `{viewerId, username*, titleKey, titleName, mediaType, watched, source}`
81. `viewer.rating.submitted` — `{viewerId, username*, titleKey, titleName, rating}`

#### `auth` (9)
82. `auth.login.succeeded` — `{viewerId, username*, method, client, platform, remoteIP*}`
83. `auth.login.failed` — `{username*, method, reason, client, remoteIP*}`
84. `auth.grant.revoked` — `{viewerId, grantKind, reason, actor}`
85. `auth.device.registered` — `{deviceId, clientIdentifier, name, platform, viewerId, username*, remoteIP*, isFirstSeenForViewer}`
86. `auth.device.quickconnect_approved` — `{viewerId, deviceName, platform}`
87. `auth.session.new_ip` — `{viewerId, username*, remoteIP*, previousIPs, location, country, city, asn, org, client, platform, sessionId, distinctActiveIPs}` *(new-IP / account-sharing detection)*
88. `auth.token.expired` — `{provider, scope, accountId, viewerId, username*, error}` *(critical)*
89. `auth.rate.limited` — `{scope, remoteIP*, attempts, windowSec}` *(critical)*
90. `auth.certificate.renewed` — `{domains, issuer, expiresAt}`

#### `plugin` (8)
91. `plugin.package.installed` — `{pluginId, name, version, kind, runtimeKind, verified}`
92. `plugin.package.updated` — `{pluginId, name, fromVersion, toVersion, auto}`
93. `plugin.package.removed` — `{pluginId, name, version}`
94. `plugin.state.enabled` — `{pluginId, name, shadow}`
95. `plugin.state.disabled` — `{pluginId, name, reason}`
96. `plugin.health.degraded` — `{pluginId, name, status, restarts, consecutiveFailures, lastError}` *(critical)*
97. `plugin.health.recovered` — `{pluginId, name, downtimeMs}`
98. `plugin.quota.exceeded` — `{pluginId, name, quotaKind, used, limit, drops}`

#### `cluster` (6)
99. `cluster.node.joined` — `{nodeId, nodeName, version, roles, memberCount}`
100. `cluster.node.lost` — `{nodeId, nodeName, lastSeenMs, roles, memberCount}` *(critical)*
101. `cluster.node.draining` — `{nodeId, nodeName, reason, activeSessions}`
102. `cluster.leader.elected` — `{leaderNodeId, leaderName, epoch, previousLeaderId}`
103. `cluster.database.unavailable` — `{dialect, error}` *(critical)*
104. `cluster.database.recovered` — `{dialect, outageDurationMs, replayedWrites}`

#### `system` (11)
105. `system.server.started` — `{version, mode, nodeId, startupMs}`
106. `system.server.stopping` — `{version, reason, activeSessions}`
107. `system.server.crashed` — `{version, exitReason, watcher}` *(critical; out-of-process crash watcher)*
108. `system.health.changed` — `{fromStatus, toStatus, component, summary}` *(critical when degraded/down)*
109. `system.reachability.changed` — `{scope (internal|external|remote_access), fromStatus, toStatus, url, statusCode, latencyMs, error}` *(critical when down)*
110. `system.update.available` — `{currentVersion, newVersion, channel, critical}`
111. `system.update.installed` — `{fromVersion, toVersion, channel, auto}`
112. `system.update.rolled_back` — `{failedVersion, restoredVersion, reason}` *(critical)*
113. `system.backup.completed` — `{kind, sizeBytes, durationMs, destination, encrypted}`
114. `system.backup.failed` — `{kind, error, consecutiveFailures}` *(critical)*
115. `system.disk.low` — `{volume, path, freeBytes, totalBytes, percentUsed}` *(critical)*

#### `flows` (4)
116. `flows.run.failed` — `{flowId, flowName, runId, nodeId, error, consecutiveFailures}`
117. `flows.flow.auto_disabled` — `{flowId, flowName, consecutiveFailures, lastError}` *(critical)*
118. `flows.bus.dropped` — `{subscriber, eventType, droppedTotal, capacity}`
119. `flows.digest.scheduled` — `{period, windowStart, windowEnd, totalPlays, uniqueViewers}`

*\* Fields marked with `*` carry `PII: true`.*

---

## 4. Flow Model

### 4.1 Graph Representation (`internal/flows.Graph`)

A Flow contains a versioned DAG (`schemaVersion: 1`) of `nodes` and `edges`:

- **Nodes:** `{id, kind, label, position: {x, y}, config, disabled}`.
- **Edges:** `{id, source, target, sourceHandle}`.
- **DAG invariant:** Static validation rejects cycles, unknown node kinds, edges targeting triggers, duplicate IDs, and invalid handles.

### 4.2 Node Kinds & Output Handles

| Category | `kind` | Config | Output Handles |
| --- | --- | --- | --- |
| Trigger | `trigger.event` | `events: string[]` (exact/prefix), optional `filter: ConditionGroup` | `next` (or `""`) |
| Trigger | `trigger.schedule` | 5-field `cron` OR `every` duration (`>= 1m`), optional IANA `timezone` | `next` (or `""`) |
| Trigger | `trigger.webhook` | `secretHeader`, `requireAuth`, `allowedMethods` | `next` (or `""`) |
| Trigger | `trigger.manual` | optional typed `inputs: ParamSchema[]`, `sampleEventType` | `next` (or `""`) |
| Condition | `condition` | `condition: ConditionGroup` | `true`, `false` |
| Logic | `logic.switch` | ordered `cases: [{key, label, condition}]`, `matchAll: bool` | `<case.key>`, `default` |
| Logic | `logic.delay` | `duration` (<=30d) OR `durationField` OR `until` (RFC3339) | `next` (or `""`) |
| Logic | `logic.wait_event` | `eventTypes[]`, `matchCorrelation`, optional `filter`, `timeout` (<=30d) | `matched`, `timeout` |
| Logic | `logic.window` | `keyTemplate`, `threshold` (>=1), `window` (1s..7d), `resetOnReach` | `reached`, `below` |
| Logic | `logic.dedupe` | `keyTemplate`, `cooldown` (1s..30d), `maxPerWindow` (default 1) | `pass`, `suppressed` |
| Action | `action` | `actionType`, `params`, `continueOnError`, `idempotencyKey`, `timeout` | `next` (or `""`), `error` |

### 4.3 Conditions & Typed Operators

Conditions are recursive `ConditionGroup` trees (`op: "and" | "or"`, optional `negate: true`) containing `ConditionRule` leaves (`{field, op, value, caseSensitive}`).

- **Path resolution (`ResolvePath`):**
  - `payload.<field>` (or bare `<field>` shorthand) reads the trigger event payload.
  - `event.<meta>` reads envelope metadata (`id`, `type`, `version`, `occurredAt`, `node`, `source`).
  - `correlation.<id>` reads correlation fields (`sessionId`, `playId`, `titleId`, `viewerId`, ...).
  - `vars.<name>` reads run-scoped variables set by `flow.set_variable`.
  - `steps.<nodeId>.output.<field>` (or `.event.*`, `.matched`, `.count`) reads prior node outputs.
  - `trigger.<field>` reads trigger metadata (schedule time, webhook headers, manual inputs).
- **Operators by field type:**
  - `string` / `enum`: `eq`, `neq`, `contains`, `not_contains`, `starts_with`, `ends_with`, `matches` (RE2), `in`, `not_in`, `exists`, `not_exists`.
  - `int` / `float` / `duration_ms` / `timestamp`: `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `in`, `not_in`, `exists`, `not_exists`.
  - `bool`: `is_true`, `is_false`, `eq`, `neq`, `exists`, `not_exists`.
  - `string_list`: `contains`, `not_contains`, `contains_any`, `contains_all`, `exists`, `not_exists`.
  - `object`: `exists`, `not_exists`.

### 4.4 Built-In Actions (`ActionRegistry`)

| `actionType` | Group | Idempotent | Summary |
| --- | --- | --- | --- |
| `notify.send` | `notifications` | No | Deliver message through one or more `internal/notify` channels (Webhook, Discord with rich embeds/poster thumbnails/fields, Telegram, Slack, Pushover, ntfy, Gotify, SMTP Email, MQTT, plugin channels). Params include `channelIds`, `title`, `message`, `html`, `posterUrl`, `color`, `fields`, `priority`, `linkUrl`, `bypassDigest`. |
| `playback.stop_session` | `playback` | Yes | Terminate an active playback session with an owner-supplied client message — targeting either an explicit `sessionId` OR a `selector` (`lowest_priority_transcode`, `oldest_paused`, `viewer_oldest`, with optional `excludeSessionId` / `excludeViewerTags`). |
| `http.webhook` | `http` | Configurable | Send signed/authenticated outbound HTTP request (`method`, `url`, `headers`, `body`, `contentType`) via `internal/egress` + `safefetch` (SSRF-safe, capped response capture). |
| `mqtt.publish` | `home` | Yes | Publish a message to a configured MQTT broker (`brokerId`, `topic`, `payload`, `qos`, `retain`) for Home Assistant lighting, media-room scenes, and presence automations. |
| `viewer.restrict` | `viewers` | Yes | Apply or tighten a viewer restriction (`remoteDisabled`, `maxBitrateKbps`, `streamLimit`, `disableAccount`, optional `revertAfter`). |
| `viewer.unrestrict` | `viewers` | Yes | Lift restrictions applied to a viewer. |
| `library.refresh` | `library` | Yes | Trigger a library or collection refresh (`libraryId` or `collectionId`). |
| `library.export` | `library` | Yes | Export server, library, collection, playlist, or item metadata to `csv`, `json`, `xml`, or `m3u` with configurable `metadataLevel`, `mediaInfoLevel`, and `artworkLevel`; outputs `{exportPath, downloadUrl, itemCount, sizeBytes}`. |
| `digest.generate` | `notifications` | Yes | Build a "Recently Added" or activity newsletter digest for a time window (collapsing episodes into seasons & shows, HTML + plain text with poster links and optional shareable self-hosted URL); pipe output into `notify.send` or `http.webhook`. |
| `source.refresh` | `sources` | Yes | Trigger a Live TV guide/playlist, watchlist, or plugin source refresh (`sourceKind`, `sourceId`). |
| `release.pin` | `resolver` | Yes | Pin a specific release to a title (`titleKey`, `releaseId`, `note`). |
| `release.unpin` | `resolver` | Yes | Remove a title's pinned release (`titleKey`). |
| `flow.set_variable` | `flow` | Yes | Set or transform a variable in `run.vars` (`key`, `value`). |
| `plugin:<id>/<key>` | `plugin` | Declared | Call a plugin-contributed flow action route (including sandboxed custom scripts/automations with declared grants and timeouts rather than unrestricted host shell execution). |

### 4.5 Templating (`internal/flows/template.go`)

Action string parameters and logic keys (`keyTemplate`) support safe, non-Turing-complete `{{ expr }}` interpolation plus inline conditional blocks (zero loops, max nesting depth 8):

- **Paths:** Same `ResolvePath` rules (`{{ payload.titleName }}`, `{{ payload.stream.videoResolution }}`, `{{ correlation.viewerId }}`, `{{ vars.note }}`, `{{ steps.wait1.event.payload.error }}`).
- **Pipe filters:**
  - Text: `default "fallback"`, `upper`, `lower`, `title`, `trim`, `truncate 120`, `json`.
  - Lists: `slice 0 3` (e.g. `{{ payload.actors | slice 0 3 | join ", " }}`), `join ", "`, `first`, `last`, `count`.
  - Units: `bytes`, `duration`, `percent` (optional decimals `percent 1`).
- **Inline conditional blocks:** `{{#if <cond>}}...{{#else}}...{{/if}}` and `{{#unless <cond>}}...{{#else}}...{{/unless}}` where `<cond>` is a truthy path or comparison (`==`, `!=`, `>`, `>=`, `<`, `<=`, `contains`). One template cleanly formats movies, episodes, HDR, and transcode details across all 119 events:
  ```text
  {{#if payload.mediaType == "episode"}}{{ payload.showTitle }} S{{ payload.season }}E{{ payload.episode }}{{#else}}{{ payload.titleName }} ({{ payload.year }}){{/if}}{{#if payload.stream.videoDynamicRange != "sdr"}} [{{ payload.stream.videoDynamicRange | upper }}]{{/if}}{{#if payload.stream.delivery == "transcode"}} • Transcode{{/if}}
  ```
- Missing values render as `""` unless `default` is provided; standalone `"{{ path }}"` preserves its native Go type inside `RenderParams`; `Secret: true` fields are redacted in all stored step traces and logs.

### 4.6 Native Power-User Recipes (Tautulli & JBOPS Parity — Zero External Scripts)

Every workflow power users previously needed Tautulli + JBOPS Python scripts for ships as a first-class built-in Flow template:

1. **Block 4K/HDR remote transcodes (`kill_stream.py`):** `trigger.event(playback.session.started, playback.stream.decision)` -> `condition(stream.videoDecision == "transcode" AND source.videoHeight >= 2160 AND network.location == "wan" AND viewer.tags not_contains "vip")` -> `playback.stop_session`.
2. **Terminate idle paused streams (`kill_stream.py --paused`):** `trigger.event(playback.session.paused)` -> `logic.wait_event(events: [playback.session.resumed, playback.session.stopped], matchCorrelation: true, timeout: "15m")` -> `timeout` handle -> `playback.stop_session`.
3. **Relieve buffering by stopping lowest-priority transcode (`kill_else_if_buffering.py`):** `trigger.event(playback.stream.stalled)` -> `logic.window(key: "{{ payload.sessionId }}", threshold: 3, window: "2m")` -> `playback.stop_session(selector: "lowest_priority_transcode", excludeSessionId: "{{ payload.sessionId }}")`.
4. **Concurrent stream / account-sharing guard (`limiterr.py` + `notify_newip.py`):** `trigger.event(playback.concurrency.exceeded, auth.session.new_ip)` -> `logic.dedupe` -> `notify.send` + optional `playback.stop_session` or `viewer.restrict`.
5. **Home Assistant cinema lighting (`on_play`/`on_pause`/`on_credits`):** `trigger.event(playback.session.started, playback.session.paused, playback.marker.entered)` -> `logic.switch` (client == Living Room Apple TV) -> `mqtt.publish` / `http.webhook`.
6. **Weekly Recently Added newsletter (`newsletters.py`):** `trigger.schedule(cron: "0 17 * * 5")` -> `digest.generate(window: "7d", groupEpisodes: true)` -> `notify.send(html: "{{ steps.digest.output.html }}")`.
7. **Scheduled library metadata export (`exporter.py`):** `trigger.schedule` (or `trigger.manual`) -> `library.export(scope: "library", format: "csv", metadataLevel: 2, mediaInfoLevel: 2)` -> `notify.send`.

---

## 5. Durable Runtime

### 5.1 Storage Schema (`events/1`, `flows/1`)

All migrations use SQLite DDL mechanically translated to PostgreSQL via `store.TranslateDDL`:

- `events_outbox` — durable cross-node / critical event queue (`id`, `event_id`, `event_type`, `version`, `occurred_at_ms`, `node_id`, `correlation_key`, `envelope_json`, `created_at_ms`, `acked_at_ms`).
- `flows_definitions` — saved flows, version, enabled state, `disabled_reason`, JSON graph, pre-extracted trigger event types, limits, failure streak, sealed/hashed webhook token.
- `flows_runs` — durable run state (`pending | running | waiting | completed | failed | cancelled | throttled`), `correlation_key`, serialized `context_json` (`vars`, step outputs, cursor), `wake_at_ms`, lease columns (`lease_owner`, `lease_until_ms`), `step_count`, `flow_depth`.
- `flows_run_steps` — append-only per-node execution trace (`seq`, `node_id`, `node_kind`, `status`, `idempotency_key`, `branch`, redacted `input_json`, `output_json`, `error`, `duration_ms`).
- `flows_waiters` — active `logic.wait_event` suspensions indexed by `(status, event_type, correlation_key)` and `(status, timeout_at_ms)`.
- `flows_counters` — exact event timestamps for `logic.window` ("N times in T") indexed by `(flow_id, node_id, group_key, at_ms)`.
- `flows_dedupe` — cooldown / throttle state for `logic.dedupe` keyed by `(flow_id, node_id, dedupe_key)`.
- `flows_schedules` — wall-clock next/last run timestamps per `(flow_id, node_id)` for `trigger.schedule` (uses `internal/schedule` wall-clock semantics so sleep/resume does not fire a backlog storm).

### 5.2 Execution, Idempotency & Restart Recovery

- **Step checkpointing:** Before executing a node, the runtime writes a `flows_run_steps` row (`status='running'`, `idempotency_key = flows.IdempotencyKey(runID, nodeID, attempt)`). After completion, it updates the step row and advances `flows_runs` in one transaction.
- **Ambiguous crash handling:** On leader startup (or when another leader's lease expires after 60s), any `running` run is recovered:
  - Pure logic/condition/variable steps safely re-run from the last committed step.
  - A `running` side-effecting action step is retried **only if** `ActionSpec.Idempotent == true`; otherwise it is marked `uncertain` (terminal failure unless the node has an `"error"` edge) so Ether never double-sends an alert or double-calls a non-idempotent webhook.
- **Wait & delay resume:** A leader tick (every 1s, woken immediately on new waits/events) resumes `flows_runs` where `status='waiting' AND wake_at_ms <= now` and times out `flows_waiters` where `status='waiting' AND timeout_at_ms <= now`.
- **Execution order:** Runs traverse outgoing edges matching the step's returned handle (`"true"`, `"false"`, `"matched"`, `"timeout"`, `"next"`, etc.) in topological/BFS order. Multiple parallel branches from a single node are allowed when wired, bounded by `maxSteps`.

### 5.3 Limits, Loop Protection & Retention

| Guardrail | Default | Hard Ceiling | Behavior on Breach |
| --- | --- | --- | --- |
| Runs per minute (per flow) | `60` | `600` | Excess triggers dropped/recorded as `throttled` (sampled) |
| Max steps per run | `100` | `250` | Run aborts with `failed` (`step limit exceeded`) |
| Max run wall-clock age | `7d` | `30d` | Waiting run cancelled/timed out |
| Event recursion depth (`flowDepth`) | `3` | `3` | Flow-emitted events at depth >= 3 do not trigger new flows |
| Consecutive failure backoff | 5s -> 15m | 15m | Exponential cooldown after 5 consecutive failures |
| Auto-disable breaker | `10` fails | Configurable | Disables flow (`disabledReason`), emits `flows.flow.auto_disabled` + owner alert |
| Run history retention | `30d` / `10,000` runs | Configurable | Background retention sweep deletes expired finished runs (`ON DELETE CASCADE` clears steps) |

---

## 6. Plugin Extensions (EPP/1 Additive)

Plugins extend Flows through existing manifest & host patterns (`internal/plugins/contract.go`):

1. **Manifest `contributes.events`:** Extend existing `EventsMeta` additively:
   ```json
   "contributes": {
     "events": {
       "subscribe": ["playback.session.started"],
       "publish": [
         {
           "key": "sync.completed",
           "label": "Sync completed",
           "description": "Finished syncing external watchlists.",
           "version": 1,
           "fields": [
             { "name": "items", "type": "int", "label": "Items synced" }
           ]
         }
       ]
     },
     "flowActions": [
       {
         "key": "post_comment",
         "label": "Post comment",
         "description": "Posts a comment to the external tracker.",
         "route": "/ether/v1/flow-actions/post_comment",
         "idempotent": false,
         "timeout": "15s",
         "params": [
           { "key": "titleId", "type": "string", "label": "Title ID", "required": true, "template": true },
           { "key": "body", "type": "string", "label": "Comment", "required": true, "template": true }
         ],
         "outputs": [
           { "name": "commentId", "type": "string", "label": "Comment ID" }
         ]
       }
     ]
   }
   ```
   - Capability `CapEvents` allows subscribing and/or publishing; new capability `CapFlowActions` (`"flow_actions"`) gates `contributes.flowActions`.
   - On install/enable/update, the plugin host registers `plugin:<id>/<key>` entries atomically into `events.Registry` (`ReplacePlugin`) and `flows.ActionRegistry` (`ReplacePlugin`). Disable/remove drops them cleanly; flows referencing a missing plugin action surface a clear validation warning and fail cleanly if run.
2. **Publishing plugin events:** Add `Host.PublishEvent(ctx, key string, payload json.RawMessage, corr events.Correlation) error` (WASM/JS host import + remote `POST /ether/plugin-host/events/publish`). Enforces manifest declaration, validates payload against declared fields, rate-limits (default 30/min/plugin), namespaces to `plugin:<pluginId>/<key>`, stamps `source: "plugin:<pluginId>"`, and publishes to `events.Bus`.
3. **Executing plugin actions:** The flow runtime invokes `plugins.Manager.Call` (`POST <route>`) with JSON body `{flowId, runId, nodeId, idempotencyKey, dryRun, params}` and header `X-Ether-Idempotency-Key`. Respects plugin grants, timeout ceilings (max 60s), and shadow mode (records journal entry without external side-effect).
4. **Legacy plugin event subscribers:** `plugins.Manager` subscribes to `events.Bus`, translating canonical events back to legacy 12 names (`KnownEvents`) for existing EPP/1 plugins while allowing new plugins to subscribe directly to canonical `domain.noun.verb` names.

---

## 7. Console API

Mounted under `/api/flows` (owner-authenticated via `admin.Deps.Extensions["flows"]`, matching `/api/plugins` and `/api/notifications`). Incoming webhook triggers mount separately at `/api/flows/hooks/{token}` with token/HMAC authentication.

| Method & Path | Purpose |
| --- | --- |
| `GET /api/flows` | List flows (id, name, enabled, disabledReason, tags, trigger summary, lastRunAt, lastStatus, 24h run counts). |
| `POST /api/flows` | Create a flow; validates graph against live registries; returns created `Flow`. |
| `GET /api/flows/{id}` | Fetch full `Flow` definition + webhook URL metadata (token never echoed after creation). |
| `PUT /api/flows/{id}` | Update `Flow` (bumps `version`, re-validates, updates schedules & cluster subscription set). |
| `DELETE /api/flows/{id}` | Delete `Flow` and cancel active runs/waiters. |
| `POST /api/flows/{id}/enable` | Enable flow (clears `consecutiveFailures` and `disabledReason`). |
| `POST /api/flows/{id}/disable` | Disable flow (cancels scheduled next runs; optional `cancelActive: true`). |
| `POST /api/flows/validate` | Dry-validate a draft `Graph` + `Limits`; returns `ValidationResult` with per-node/edge errors & warnings. |
| `POST /api/flows/{id}/test` | Execute a `TestRun` (`dryRun: true` default, optional custom `event` payload or auto-generated schema sample); returns full step-by-step trace. |
| `POST /api/flows/{id}/run` | Manually invoke a flow with a `trigger.manual` node. |
| `POST /api/flows/{id}/webhook-token/rotate` | Generate & return a new incoming webhook secret token once (stored hashed). |
| `GET /api/flows/runs` | List recent runs across all flows (`status`, `flowId`, `eventType`, `cursor`, `limit`). |
| `GET /api/flows/{id}/runs` | List recent runs for one flow. |
| `GET /api/flows/runs/{runId}` | Fetch run detail plus ordered `steps[]` with redacted inputs, condition traces, outputs, and errors. |
| `POST /api/flows/runs/{runId}/cancel` | Cancel a `running` or `waiting` run. |
| `GET /api/flows/catalogue/events` | Full event catalogue grouped by domain (types, versions, labels, typed `fields[]`, valid operators per field type, sample payloads, plugin origin). |
| `GET /api/flows/catalogue/actions` | Full action catalogue (built-in + plugin `ActionSpec[]`, param schemas, pickers, output schemas). |
| `GET /api/flows/templates` | Curated starter flow templates (notification presets, failover alerts, auto-unpin dead pin, bedtime grace warning, new device alert, etc.). |
| `POST /api/flows/from-template/{templateId}` | Instantiate a template with owner-chosen parameters (e.g. target notification channel). |
| `POST /api/flows/hooks/{token}` | Incoming webhook trigger endpoint (token-authenticated, body <= 256 KiB, rate-limited). |

Live console updates stream over the existing admin SSE hub via `admin.StreamFlowsChanged` (`"flows.changed"`) and `admin.StreamFlowRun` (`"flows.run"`).

---

## 8. Notifications Integration

- **Zero-regression bridge:** An internal bridge subscribes to `events.Bus` and maps canonical events back to legacy `notify.Event` kinds for any `notify.Channel` that still has legacy `channel.Events` configured. Existing notification setups keep working identically on upgrade.
- **Unified delivery transport:** The `notify.send` Flow action calls `notify.Service.DeliverToChannels(ctx, channelIDs, ev, opts)` — reusing channel secret sealing, per-channel rate limits, digest batching, delivery history, and plugin notification channel kinds (`CapNotify`). Alongside existing Webhook, Discord, Slack, Telegram, Gotify, ntfy, and SMTP Email transports, Flows extends `notify` with rich Discord embeds (poster thumbnail, color, fields), Pushover, and MQTT broker transports.
- **Seamless upgrade path in Console:**
  - Every existing notification preset ("Critical alerts", "Playback issues", "Provider outages", "Updates", "Weekly newsletter") ships as a first-class Flow template.
  - On the Notifications page, an owner can click **"Customize as Flow"** on any rule to materialize it as a full editable Flow (e.g. adding "only if 3 errors in 10 min" or "ignore viewer X"), clearing that legacy rule from the channel atomically to avoid duplicate alerts.
- **Crash watcher exception:** `system.server.crashed` (`server.crash`) continues to be delivered directly by the out-of-process watcher (`internal/notify/monitor.go`) because Ether itself is not running when a crash occurs.
