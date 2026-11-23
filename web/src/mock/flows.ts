/**
 * Flows for the mock backend: the event/action catalogue, starter templates,
 * a handful of household flows with run history, graph validation and a
 * step-by-step test-run simulator. Mirrors docs/design/flows.md §7 so the
 * console behaves the same against this fake and the live Go API (F4).
 * Development and tests only; never shipped.
 */
import {
  autoLayoutGraph,
  FLOW_PICKER,
  LEGACY_NOTIFY_TO_CANONICAL,
  validateTemplateSyntax,
  wouldCreateCycle,
  type ActionSpec,
  type ConditionGroup,
  type ConditionRule,
  type ConditionTrace,
  type EventSchema,
  type FieldSchema,
  type Flow,
  type FlowNode,
  type FlowSummary,
  type FlowTemplate,
  type Graph,
  type Run,
  type RunStats,
  type RunStep,
  type ValidationIssue,
  type ValidationResult,
} from "../pages/flows/types";

const H = 3_600_000;
const M = 60_000;
const iso = (msAgo: number) => new Date(Date.now() - msAgo).toISOString();

function f(
  name: string,
  type: FieldSchema["type"],
  label: string,
  extra: Partial<FieldSchema> = {},
): FieldSchema {
  return { name, type, label, filterable: true, ...extra };
}

/** The shared playback context (docs/design/flows.md §3.3), flattened. */
const PLAYBACK_FIELDS: FieldSchema[] = [
  f("sessionId", "string", "Session ID", { example: "sess_9f2a" }),
  f("playId", "string", "Play ID", { example: "play_8c11" }),
  f("titleKey", "string", "Title key", { example: "movie:603" }),
  f("titleName", "string", "Title", { example: "The Matrix" }),
  f("showTitle", "string", "Show", { example: "Night Desk" }),
  f("season", "int", "Season", { example: 3 }),
  f("episode", "int", "Episode", { example: 7 }),
  f("mediaType", "enum", "Kind", {
    enum: [
      { value: "movie", label: "Movie" },
      { value: "episode", label: "Episode" },
      { value: "live", label: "Live TV" },
      { value: "trailer", label: "Trailer" },
    ],
    example: "movie",
  }),
  f("year", "int", "Year", { example: 1999 }),
  f("libraryName", "string", "Library", { example: "Movies" }),
  f("contentRating", "string", "Content rating", { example: "R" }),
  f("genres", "string_list", "Genres", { example: ["Sci-Fi", "Action"] }),
  f("durationMs", "duration_ms", "Runtime", { unit: "ms", example: 8_160_000 }),
  f("posterUrl", "string", "Poster URL", { filterable: false }),
  f("source.videoCodec", "string", "Source video codec", { example: "hevc" }),
  f("source.videoHeight", "int", "Source height", { unit: "px", example: 2160 }),
  f("source.videoDynamicRange", "enum", "Source dynamic range", {
    enum: [
      { value: "sdr", label: "SDR" },
      { value: "hdr10", label: "HDR10" },
      { value: "dv", label: "Dolby Vision" },
      { value: "hlg", label: "HLG" },
    ],
    example: "dv",
  }),
  f("source.audioCodec", "string", "Source audio codec", { example: "truehd" }),
  f("source.bitrateKbps", "int", "Source bitrate", { unit: "kbps", example: 42_000 }),
  f("stream.delivery", "enum", "Delivery", {
    enum: [
      { value: "direct_play", label: "Direct play" },
      { value: "remux", label: "Remux" },
      { value: "transcode", label: "Transcode" },
    ],
    example: "transcode",
  }),
  f("stream.videoDecision", "enum", "Video decision", {
    enum: [
      { value: "direct_play", label: "Direct play" },
      { value: "copy", label: "Copy" },
      { value: "transcode", label: "Transcode" },
    ],
    example: "transcode",
  }),
  f("stream.audioDecision", "enum", "Audio decision", {
    enum: [
      { value: "direct_play", label: "Direct play" },
      { value: "copy", label: "Copy" },
      { value: "transcode", label: "Transcode" },
    ],
    example: "copy",
  }),
  f("stream.videoHeight", "int", "Stream height", { unit: "px", example: 1080 }),
  f("stream.bitrateKbps", "int", "Stream bitrate", { unit: "kbps", example: 12_000 }),
  f("stream.hwEncode", "bool", "Hardware encode", { example: true }),
  f("stream.transcodeSpeed", "float", "Transcode speed", { unit: "×", example: 2.4 }),
  f("viewer.id", "int", "Viewer ID", { example: 7 }),
  f("viewer.username", "string", "Viewer", { pii: true, example: "alex" }),
  f("viewer.role", "string", "Viewer role", { example: "friend" }),
  f("viewer.tags", "string_list", "Viewer tags", { example: ["family"] }),
  f("viewer.activeStreams", "int", "Viewer's active streams", { example: 2 }),
  f("viewer.activeTranscodes", "int", "Viewer's active transcodes", { example: 1 }),
  f("client.name", "string", "Client", { example: "Living Room Apple TV" }),
  f("client.platform", "string", "Platform", { example: "tvOS" }),
  f("client.protocol", "enum", "App protocol", {
    enum: [
      { value: "vela", label: "Vela" },
      { value: "plex", label: "Plex" },
      { value: "jellyfin", label: "Jellyfin" },
      { value: "dlna", label: "DLNA" },
    ],
    example: "plex",
  }),
  f("network.location", "enum", "Network location", {
    enum: [
      { value: "lan", label: "At home (LAN)" },
      { value: "wan", label: "Away from home (WAN)" },
    ],
    example: "wan",
  }),
  f("network.country", "string", "Country", { example: "US" }),
  f("server.activeStreams", "int", "Active streams (server)", { example: 4 }),
  f("server.activeTranscodes", "int", "Active transcodes (server)", { example: 2 }),
];

const PLAYBACK_SAMPLE: Record<string, unknown> = {
  sessionId: "sess_9f2a",
  playId: "play_8c11",
  titleKey: "movie:603",
  titleName: "The Matrix",
  mediaType: "movie",
  year: 1999,
  libraryName: "Movies",
  contentRating: "R",
  genres: ["Sci-Fi", "Action"],
  durationMs: 8_160_000,
  source: {
    videoCodec: "hevc",
    videoHeight: 2160,
    videoDynamicRange: "dv",
    audioCodec: "truehd",
    bitrateKbps: 42_000,
  },
  stream: {
    delivery: "transcode",
    videoDecision: "transcode",
    audioDecision: "copy",
    videoHeight: 1080,
    bitrateKbps: 12_000,
    hwEncode: true,
    transcodeSpeed: 2.4,
  },
  viewer: {
    id: 7,
    username: "alex",
    role: "friend",
    tags: ["family"],
    activeStreams: 2,
    activeTranscodes: 1,
  },
  client: { name: "Alex's iPhone", platform: "iOS", protocol: "plex" },
  network: { location: "wan", country: "US" },
  server: { activeStreams: 4, activeTranscodes: 2 },
};

function ev(
  type: string,
  label: string,
  description: string,
  fields: FieldSchema[],
  sample: Record<string, unknown>,
  extra: Partial<EventSchema> = {},
): EventSchema {
  return {
    type,
    version: 1,
    domain: type.split(".")[0]!,
    label,
    description,
    owner: "ether",
    fields,
    sample: { __eventType: type, ...sample },
    ...extra,
  };
}

export const FLOW_EVENTS: EventSchema[] = [
  ev(
    "playback.session.started",
    "Playback started",
    "A viewer started playing a title.",
    PLAYBACK_FIELDS,
    PLAYBACK_SAMPLE,
  ),
  ev(
    "playback.session.paused",
    "Playback paused",
    "A viewer paused playback.",
    [
      ...PLAYBACK_FIELDS,
      f("positionMs", "duration_ms", "Position", { unit: "ms", example: 1_800_000 }),
    ],
    { ...PLAYBACK_SAMPLE, positionMs: 1_800_000 },
  ),
  ev(
    "playback.session.resumed",
    "Playback resumed",
    "A viewer resumed after pausing.",
    [
      ...PLAYBACK_FIELDS,
      f("positionMs", "duration_ms", "Position", { unit: "ms" }),
      f("pausedForMs", "duration_ms", "Paused for", { unit: "ms", example: 300_000 }),
    ],
    { ...PLAYBACK_SAMPLE, positionMs: 1_800_000, pausedForMs: 300_000 },
  ),
  ev(
    "playback.session.stopped",
    "Playback stopped",
    "A playback session ended.",
    [
      ...PLAYBACK_FIELDS,
      f("watchedRatio", "float", "Watched ratio", { example: 0.92 }),
      f("stopReason", "string", "Stop reason", { example: "complete" }),
    ],
    { ...PLAYBACK_SAMPLE, watchedRatio: 0.92, stopReason: "complete" },
  ),
  ev(
    "playback.session.completed",
    "Watched to the end",
    "A stream crossed the watched threshold.",
    [...PLAYBACK_FIELDS, f("watchedRatio", "float", "Watched ratio", { example: 0.95 })],
    { ...PLAYBACK_SAMPLE, watchedRatio: 0.95 },
  ),
  ev(
    "playback.session.failed",
    "Playback failed",
    "A session could not play.",
    [
      ...PLAYBACK_FIELDS,
      f("stage", "string", "Failure stage", { example: "resolve" }),
      f("error", "string", "Error", {
        example: "every release failed (3 tried): missing articles",
      }),
    ],
    {
      ...PLAYBACK_SAMPLE,
      stage: "resolve",
      error: "every release failed (3 tried): missing articles",
    },
    { critical: true },
  ),
  ev(
    "playback.stream.stalled",
    "Stream stalled (buffering)",
    "A stream ran out of buffered media.",
    [
      ...PLAYBACK_FIELDS,
      f("stallCount", "int", "Stalls so far", { example: 3 }),
      f("bufferMs", "duration_ms", "Buffer left", { unit: "ms", example: 0 }),
    ],
    { ...PLAYBACK_SAMPLE, stallCount: 3, bufferMs: 0 },
  ),
  ev(
    "playback.stream.decision",
    "Stream decision made",
    "Ether chose direct play, remux or transcode.",
    [...PLAYBACK_FIELDS, f("reason", "string", "Reason", { example: "client bitrate cap" })],
    { ...PLAYBACK_SAMPLE, reason: "client bitrate cap" },
  ),
  ev(
    "playback.marker.entered",
    "Marker entered",
    "Playback entered an intro, credits or recap marker.",
    [
      ...PLAYBACK_FIELDS,
      f("markerKind", "enum", "Marker kind", {
        enum: [
          { value: "intro" },
          { value: "credits" },
          { value: "commercial" },
          { value: "recap" },
        ],
        example: "credits",
      }),
    ],
    { ...PLAYBACK_SAMPLE, markerKind: "credits" },
  ),
  ev(
    "playback.concurrency.exceeded",
    "Stream limit exceeded",
    "A viewer went over their concurrent stream limit.",
    [
      ...PLAYBACK_FIELDS,
      f("limit", "int", "Stream limit", { example: 2 }),
      f("distinctActiveIPs", "int", "Distinct active IPs", { example: 3 }),
    ],
    { ...PLAYBACK_SAMPLE, limit: 2, distinctActiveIPs: 3 },
    { critical: true },
  ),
  ev(
    "livetv.input.stalled",
    "Live TV input stalled",
    "A channel's input stopped delivering data.",
    [
      f("channelName", "string", "Channel", { example: "News 24" }),
      f("dataAgeMs", "duration_ms", "Data age", { unit: "ms", example: 45_000 }),
      f("reconnects", "int", "Reconnects", { example: 2 }),
    ],
    { channelName: "News 24", dataAgeMs: 45_000, reconnects: 2 },
  ),
  ev(
    "livetv.input.lost",
    "Live TV input lost",
    "A channel's input is gone.",
    [
      f("channelName", "string", "Channel", { example: "News 24" }),
      f("error", "string", "Error", { example: "connection reset" }),
      f("hasBackup", "bool", "Has backup source", { example: false }),
    ],
    { channelName: "News 24", error: "connection reset", hasBackup: false },
    { critical: true },
  ),
  ev(
    "livetv.guide.failed",
    "Guide refresh failed",
    "A guide source could not refresh.",
    [
      f("sourceName", "string", "Guide source", { example: "Main XMLTV" }),
      f("error", "string", "Error", { example: "HTTP 503" }),
      f("staleHours", "float", "Stale for", { unit: "h", example: 26 }),
    ],
    { sourceName: "Main XMLTV", error: "HTTP 503", staleHours: 26 },
  ),
  ev(
    "resolver.search.empty",
    "No releases found",
    "A title search returned nothing playable.",
    [
      f("titleName", "string", "Title", { example: "Obscure Film" }),
      f("indexersQueried", "int", "Indexers queried", { example: 6 }),
      f("topRejection", "string", "Top rejection", { example: "no seeders" }),
    ],
    { titleName: "Obscure Film", indexersQueried: 6, topRejection: "no seeders" },
  ),
  ev(
    "resolver.pin.unhealthy",
    "Pinned release unhealthy",
    "A pinned release stopped being playable.",
    [
      f("titleName", "string", "Title", { example: "Arrival" }),
      f("status", "string", "Status", { example: "dead" }),
      f("reason", "string", "Reason", { example: "missing articles" }),
    ],
    { titleName: "Arrival", status: "dead", reason: "missing articles" },
    { critical: true },
  ),
  ev(
    "indexer.service.outage",
    "Indexer outage",
    "An indexer stopped answering.",
    [
      f("indexerName", "string", "Indexer", { example: "NZBgeek" }),
      f("consecutiveFailures", "int", "Failures in a row", { example: 5 }),
      f("error", "string", "Error", { example: "timeout" }),
    ],
    { indexerName: "NZBgeek", consecutiveFailures: 5, error: "timeout" },
    { critical: true },
  ),
  ev(
    "indexer.budget.exhausted",
    "Indexer budget used up",
    "An indexer hit its API budget.",
    [
      f("indexerName", "string", "Indexer", { example: "NZBgeek" }),
      f("used", "int", "Used", { example: 5000 }),
      f("limit", "int", "Limit", { example: 5000 }),
    ],
    { indexerName: "NZBgeek", used: 5000, limit: 5000 },
  ),
  ev(
    "provider.service.outage",
    "Provider outage",
    "A provider stopped answering.",
    [
      f("providerName", "string", "Provider", { example: "Real-Debrid" }),
      f("error", "string", "Error", { example: "HTTP 502" }),
    ],
    { providerName: "Real-Debrid", error: "HTTP 502" },
    { critical: true },
  ),
  ev(
    "provider.quota.exhausted",
    "Provider quota used up",
    "A provider hit its quota.",
    [
      f("providerName", "string", "Provider", { example: "TorBox" }),
      f("used", "int", "Used", { example: 100 }),
      f("limit", "int", "Limit", { example: 100 }),
    ],
    { providerName: "TorBox", used: 100, limit: 100 },
    { critical: true },
  ),
  ev(
    "cache.budget.full",
    "Cache full",
    "A cache reached its disk budget.",
    [
      f("cacheKind", "string", "Cache", { example: "media" }),
      f("percentUsed", "float", "Budget used", { unit: "%", example: 99.5 }),
    ],
    { cacheKind: "media", percentUsed: 99.5 },
    { critical: true },
  ),
  ev(
    "library.title.added",
    "Title added",
    "A new title appeared in a library.",
    [
      f("libraryName", "string", "Library", { example: "Movies" }),
      f("titleName", "string", "Title", { example: "Dune: Part Two" }),
      f("mediaType", "enum", "Kind", {
        enum: [{ value: "movie" }, { value: "episode" }],
        example: "movie",
      }),
      f("year", "int", "Year", { example: 2024 }),
      f("posterUrl", "string", "Poster URL", { filterable: false }),
    ],
    { libraryName: "Movies", titleName: "Dune: Part Two", mediaType: "movie", year: 2024 },
  ),
  ev(
    "library.batch.added",
    "Recently added (batched)",
    "Several titles arrived together.",
    [
      f("libraryName", "string", "Library", { example: "TV Shows" }),
      f("showTitle", "string", "Show", { example: "Night Desk" }),
      f("itemCount", "int", "Items", { example: 8 }),
      f("posterUrl", "string", "Poster URL", { filterable: false }),
    ],
    { libraryName: "TV Shows", showTitle: "Night Desk", itemCount: 8 },
  ),
  ev(
    "library.episode.aired",
    "Episode aired",
    "A followed show aired a new episode.",
    [
      f("showTitle", "string", "Show", { example: "Night Desk" }),
      f("episodeTitle", "string", "Episode", { example: "The Long Shift" }),
      f("season", "int", "Season", { example: 3 }),
      f("episode", "int", "Episode number", { example: 8 }),
      f("followerCount", "int", "Followers", { example: 3 }),
    ],
    {
      showTitle: "Night Desk",
      episodeTitle: "The Long Shift",
      season: 3,
      episode: 8,
      followerCount: 3,
    },
  ),
  ev(
    "library.sync.failed",
    "Library sync failed",
    "A library could not sync.",
    [
      f("libraryName", "string", "Library", { example: "Movies" }),
      f("error", "string", "Error", { example: "TMDB timeout" }),
    ],
    { libraryName: "Movies", error: "TMDB timeout" },
  ),
  ev(
    "viewer.account.joined",
    "Viewer joined",
    "A new viewer account was created.",
    [
      f("viewerId", "int", "Viewer ID", { example: 12 }),
      f("username", "string", "Viewer", { pii: true, example: "sam" }),
      f("role", "string", "Role", { example: "friend" }),
    ],
    { viewerId: 12, username: "sam", role: "friend" },
  ),
  ev(
    "auth.login.failed",
    "Sign-in failed",
    "Someone failed to sign in.",
    [
      f("username", "string", "Username", { pii: true, example: "alex" }),
      f("reason", "string", "Reason", { example: "bad password" }),
      f("remoteIP", "string", "IP address", { pii: true, example: "203.0.113.9" }),
    ],
    { username: "alex", reason: "bad password", remoteIP: "203.0.113.9" },
  ),
  ev(
    "auth.device.registered",
    "New device",
    "A viewer signed in on a new device.",
    [
      f("name", "string", "Device", { example: "Sam's Pixel" }),
      f("platform", "string", "Platform", { example: "Android" }),
      f("username", "string", "Viewer", { pii: true, example: "sam" }),
      f("isFirstSeenForViewer", "bool", "First device for viewer", { example: false }),
    ],
    { name: "Sam's Pixel", platform: "Android", username: "sam", isFirstSeenForViewer: false },
  ),
  ev(
    "auth.session.new_ip",
    "Sign-in from a new address",
    "A viewer appeared from an IP address they have not used before.",
    [
      f("username", "string", "Viewer", { pii: true, example: "alex" }),
      f("country", "string", "Country", { example: "CA" }),
      f("city", "string", "City", { example: "Toronto" }),
      f("distinctActiveIPs", "int", "Distinct active IPs", { example: 2 }),
    ],
    { username: "alex", country: "CA", city: "Toronto", distinctActiveIPs: 2 },
  ),
  ev(
    "plugin.health.degraded",
    "Plugin unhealthy",
    "A plugin keeps failing.",
    [
      f("name", "string", "Plugin", { example: "Trakt Sync" }),
      f("consecutiveFailures", "int", "Failures in a row", { example: 4 }),
      f("lastError", "string", "Last error", { example: "rate limited" }),
    ],
    { name: "Trakt Sync", consecutiveFailures: 4, lastError: "rate limited" },
    { critical: true },
  ),
  ev(
    "system.server.started",
    "Server started",
    "Ether finished starting.",
    [
      f("version", "string", "Version", { example: "0.3.17" }),
      f("startupMs", "duration_ms", "Startup time", { unit: "ms", example: 4200 }),
    ],
    { version: "0.3.17", startupMs: 4200 },
  ),
  ev(
    "system.update.available",
    "Update available",
    "A new Ether version can be installed.",
    [
      f("currentVersion", "string", "Current version", { example: "0.3.16" }),
      f("newVersion", "string", "New version", { example: "0.3.17" }),
      f("critical", "bool", "Critical update", { example: false }),
    ],
    { currentVersion: "0.3.16", newVersion: "0.3.17", critical: false },
  ),
  ev(
    "system.backup.failed",
    "Backup failed",
    "A scheduled backup did not complete.",
    [
      f("kind", "string", "Backup kind", { example: "nightly" }),
      f("error", "string", "Error", { example: "disk full" }),
      f("consecutiveFailures", "int", "Failures in a row", { example: 2 }),
    ],
    { kind: "nightly", error: "disk full", consecutiveFailures: 2 },
    { critical: true },
  ),
  ev(
    "system.disk.low",
    "Disk running low",
    "A volume is nearly full.",
    [
      f("path", "string", "Path", { example: "/data" }),
      f("percentUsed", "float", "Used", { unit: "%", example: 94 }),
    ],
    { path: "/data", percentUsed: 94 },
    { critical: true },
  ),
  ev(
    "system.health.changed",
    "Server health changed",
    "Overall server health moved.",
    [
      f("fromStatus", "string", "Was", { example: "ok" }),
      f("toStatus", "string", "Now", { example: "degraded" }),
      f("summary", "string", "Summary", { example: "Indexer outage" }),
    ],
    { fromStatus: "ok", toStatus: "degraded", summary: "Indexer outage" },
  ),
  ev(
    "flows.run.failed",
    "Flow run failed",
    "A flow run ended in failure.",
    [
      f("flowName", "string", "Flow", { example: "Weekly digest" }),
      f("error", "string", "Error", { example: "webhook timeout" }),
      f("consecutiveFailures", "int", "Failures in a row", { example: 3 }),
    ],
    { flowName: "Weekly digest", error: "webhook timeout", consecutiveFailures: 3 },
  ),
  ev(
    "flows.digest.scheduled",
    "Digest window closed",
    "A digest period ended and is ready to summarize.",
    [
      f("period", "string", "Period", { example: "weekly" }),
      f("totalPlays", "int", "Total plays", { example: 142 }),
      f("uniqueViewers", "int", "Viewers", { example: 6 }),
    ],
    { period: "weekly", totalPlays: 142, uniqueViewers: 6 },
  ),
];

export const FLOW_ACTIONS: ActionSpec[] = [
  {
    type: "notify.send",
    owner: "ether",
    group: "notifications",
    label: "Send a notification",
    description: "Deliver a message through your notification channels.",
    icon: "Bell",
    sideEffect: true,
    params: [
      { key: "channelIds", type: "string_list", label: "Channels", picker: FLOW_PICKER.channel },
      { key: "title", type: "string", label: "Title", template: true },
      { key: "message", type: "string", label: "Message", required: true, template: true },
      { key: "posterUrl", type: "string", label: "Poster URL", template: true },
      {
        key: "priority",
        type: "enum",
        label: "Priority",
        enum: [
          { value: "low", label: "Low" },
          { value: "normal", label: "Normal" },
          { value: "high", label: "High" },
        ],
      },
      { key: "bypassDigest", type: "bool", label: "Send immediately (skip digest batching)" },
    ],
    outputs: [f("delivered", "int", "Channels delivered")],
  },
  {
    type: "playback.stop_session",
    owner: "ether",
    group: "playback",
    label: "Stop a playback session",
    description: "End a stream with a message shown to the viewer.",
    icon: "OctagonX",
    idempotent: true,
    sideEffect: true,
    params: [
      { key: "sessionId", type: "string", label: "Session ID", template: true },
      {
        key: "selector",
        type: "enum",
        label: "Or stop…",
        enum: [
          { value: "lowest_priority_transcode", label: "Lowest-priority transcode" },
          { value: "oldest_paused", label: "Oldest paused stream" },
          { value: "viewer_oldest", label: "Viewer's oldest stream" },
        ],
      },
      { key: "excludeSessionId", type: "string", label: "Never stop this session", template: true },
      { key: "message", type: "string", label: "Message to the viewer", template: true },
    ],
    outputs: [f("stoppedSessionId", "string", "Stopped session")],
  },
  {
    type: "http.webhook",
    owner: "ether",
    group: "http",
    label: "Call a webhook (HTTP)",
    description: "Send an HTTP request to another service.",
    icon: "Webhook",
    sideEffect: true,
    params: [
      {
        key: "method",
        type: "enum",
        label: "Method",
        required: true,
        enum: [
          { value: "GET" },
          { value: "POST" },
          { value: "PUT" },
          { value: "PATCH" },
          { value: "DELETE" },
        ],
        default: "POST",
      },
      { key: "url", type: "string", label: "URL", required: true, template: true },
      { key: "body", type: "string", label: "Body", template: true },
      { key: "contentType", type: "string", label: "Content type", default: "application/json" },
    ],
    outputs: [f("statusCode", "int", "HTTP status"), f("responseBody", "string", "Response body")],
  },
  {
    type: "mqtt.publish",
    owner: "ether",
    group: "home",
    label: "Publish to MQTT",
    description: "For Home Assistant scenes, lighting and presence.",
    icon: "Lightbulb",
    idempotent: true,
    sideEffect: true,
    params: [
      { key: "topic", type: "string", label: "Topic", required: true, template: true },
      { key: "payload", type: "string", label: "Payload", required: true, template: true },
      { key: "retain", type: "bool", label: "Retain message" },
    ],
    outputs: [],
  },
  {
    type: "viewer.restrict",
    owner: "ether",
    group: "viewers",
    label: "Restrict a viewer",
    description: "Tighten what a viewer can do, optionally for a limited time.",
    icon: "ShieldAlert",
    idempotent: true,
    sideEffect: true,
    params: [
      { key: "viewerId", type: "string", label: "Viewer ID", required: true, template: true },
      { key: "remoteDisabled", type: "bool", label: "Block remote (away-from-home) playback" },
      { key: "maxBitrateKbps", type: "int", label: "Bitrate cap", unit: "kbps" },
      { key: "streamLimit", type: "int", label: "Concurrent stream limit" },
      { key: "disableAccount", type: "bool", label: "Disable the account" },
      { key: "revertAfter", type: "string", label: "Lift automatically after (e.g. 24h)" },
    ],
    outputs: [],
  },
  {
    type: "viewer.unrestrict",
    owner: "ether",
    group: "viewers",
    label: "Lift viewer restrictions",
    description: "Remove restrictions a flow applied earlier.",
    icon: "ShieldCheck",
    idempotent: true,
    sideEffect: true,
    params: [
      { key: "viewerId", type: "string", label: "Viewer ID", required: true, template: true },
    ],
    outputs: [],
  },
  {
    type: "library.refresh",
    owner: "ether",
    group: "library",
    label: "Refresh a library",
    description: "Re-sync a library or rebuild a collection.",
    icon: "RefreshCw",
    idempotent: true,
    sideEffect: true,
    params: [
      { key: "libraryId", type: "string", label: "Library", picker: FLOW_PICKER.library },
      { key: "collectionId", type: "string", label: "Collection" },
    ],
    outputs: [],
  },
  {
    type: "library.export",
    owner: "ether",
    group: "library",
    label: "Export library data",
    description: "Write a CSV, JSON, XML or M3U export.",
    icon: "Download",
    idempotent: true,
    params: [
      {
        key: "scope",
        type: "enum",
        label: "Scope",
        enum: [
          { value: "server", label: "Whole server" },
          { value: "library", label: "One library" },
          { value: "collection", label: "One collection" },
        ],
      },
      { key: "libraryId", type: "string", label: "Library", picker: FLOW_PICKER.library },
      {
        key: "format",
        type: "enum",
        label: "Format",
        enum: [{ value: "csv" }, { value: "json" }, { value: "xml" }, { value: "m3u" }],
      },
      { key: "metadataLevel", type: "int", label: "Metadata detail (0–3)" },
      { key: "mediaInfoLevel", type: "int", label: "Media info detail (0–3)" },
    ],
    outputs: [f("downloadUrl", "string", "Download URL"), f("itemCount", "int", "Items exported")],
  },
  {
    type: "digest.generate",
    owner: "ether",
    group: "notifications",
    label: "Build a digest",
    description: "Summarize a time window (recently added, activity) for a message.",
    icon: "Newspaper",
    idempotent: true,
    params: [
      { key: "window", type: "string", label: "Window (e.g. 7d)", required: true, default: "7d" },
      { key: "groupEpisodes", type: "bool", label: "Group episodes by show" },
    ],
    outputs: [
      f("html", "string", "Digest HTML"),
      f("text", "string", "Digest text"),
      f("itemCount", "int", "Items included"),
    ],
  },
  {
    type: "source.refresh",
    owner: "ether",
    group: "sources",
    label: "Refresh a source",
    description: "Refresh a Live TV guide, playlist or watchlist source.",
    icon: "RadioTower",
    idempotent: true,
    sideEffect: true,
    params: [
      {
        key: "sourceKind",
        type: "enum",
        label: "Source kind",
        enum: [
          { value: "livetv_guide", label: "Live TV guide" },
          { value: "livetv_playlist", label: "Live TV playlist" },
          { value: "watchlist", label: "Watchlist" },
        ],
      },
      { key: "sourceId", type: "string", label: "Source ID", template: true },
    ],
    outputs: [],
  },
  {
    type: "release.pin",
    owner: "ether",
    group: "resolver",
    label: "Pin a release",
    description: "Always use one release for a title.",
    icon: "Pin",
    idempotent: true,
    sideEffect: true,
    params: [
      { key: "titleKey", type: "string", label: "Title key", required: true, template: true },
      { key: "releaseId", type: "string", label: "Release ID", required: true, template: true },
      { key: "note", type: "string", label: "Note", template: true },
    ],
    outputs: [],
  },
  {
    type: "release.unpin",
    owner: "ether",
    group: "resolver",
    label: "Unpin a release",
    description: "Let Ether choose releases for a title again.",
    icon: "PinOff",
    idempotent: true,
    sideEffect: true,
    params: [
      { key: "titleKey", type: "string", label: "Title key", required: true, template: true },
    ],
    outputs: [],
  },
  {
    type: "flow.set_variable",
    owner: "ether",
    group: "flow",
    label: "Set a variable",
    description: "Remember a value for later steps in this run.",
    icon: "Variable",
    idempotent: true,
    params: [
      { key: "key", type: "string", label: "Variable name", required: true },
      { key: "value", type: "string", label: "Value", template: true },
    ],
    outputs: [],
  },
];

let uid = 0;
function node(kind: FlowNode["kind"], label: string, config: Record<string, unknown>): FlowNode {
  uid += 1;
  return { id: `n${uid}`, kind, label, position: { x: 0, y: 0 }, config };
}
function graphOf(nodes: FlowNode[], links: [FlowNode, string, FlowNode][]): Graph {
  return autoLayoutGraph({
    schemaVersion: 1,
    nodes,
    edges: links.map(([s, handle, tg], i) => ({
      id: `e${i + 1}-${s.id}-${tg.id}`,
      source: s.id,
      target: tg.id,
      sourceHandle: handle,
    })),
  });
}
const NOTIFY_PARAM = [
  {
    key: "channelIds",
    type: "string_list" as const,
    label: "Send to channels",
    default: ["*"],
    picker: FLOW_PICKER.channel,
  },
];

function templateGraphs() {
  uid = 0;
  const t1 = node("trigger.event", "Playback starts or decision changes", {
    events: ["playback.session.started", "playback.stream.decision"],
  });
  const c1 = node("condition", "Remote 4K video transcode", {
    condition: {
      op: "and",
      rules: [
        { field: "stream.videoDecision", op: "eq", value: "transcode" },
        { field: "source.videoHeight", op: "gte", value: 2160 },
        { field: "network.location", op: "eq", value: "wan" },
        { field: "viewer.tags", op: "not_contains", value: "vip" },
      ],
    },
  });
  const a1 = node("action", "Stop the stream", {
    actionType: "playback.stop_session",
    params: {
      sessionId: "{{ payload.sessionId }}",
      message:
        "4K video can't be transcoded away from home. Pick a lower quality or watch at home.",
    },
  });
  const g1 = graphOf(
    [t1, c1, a1],
    [
      [t1, "next", c1],
      [c1, "true", a1],
    ],
  );

  const t2 = node("trigger.event", "Playback paused", { events: ["playback.session.paused"] });
  const w2 = node("logic.wait_event", "Still paused after 15 minutes?", {
    eventTypes: ["playback.session.resumed", "playback.session.stopped"],
    matchCorrelation: true,
    timeout: "15m",
  });
  const a2 = node("action", "Stop the idle stream", {
    actionType: "playback.stop_session",
    params: {
      sessionId: "{{ payload.sessionId }}",
      message: "This stream was paused for a while, so it was stopped to free the server.",
    },
  });
  const g2 = graphOf(
    [t2, w2, a2],
    [
      [t2, "next", w2],
      [w2, "timeout", a2],
    ],
  );

  const t3 = node("trigger.event", "A stream buffers", { events: ["playback.stream.stalled"] });
  const w3 = node("logic.window", "3 stalls in 2 minutes", {
    keyTemplate: "{{ payload.sessionId }}",
    threshold: 3,
    window: "2m",
    resetOnReach: true,
  });
  const a3 = node("action", "Stop lowest-priority transcode", {
    actionType: "playback.stop_session",
    params: {
      selector: "lowest_priority_transcode",
      excludeSessionId: "{{ payload.sessionId }}",
      message: "Stopped to relieve buffering elsewhere.",
    },
  });
  const g3 = graphOf(
    [t3, w3, a3],
    [
      [t3, "next", w3],
      [w3, "reached", a3],
    ],
  );

  const t4 = node("trigger.event", "Playback fails", { events: ["playback.session.failed"] });
  const d4 = node("logic.dedupe", "At most one alert per title each hour", {
    keyTemplate: "{{ payload.titleKey }}",
    cooldown: "1h",
    maxPerWindow: 1,
  });
  const a4 = node("action", "Notify me", {
    actionType: "notify.send",
    params: {
      channelIds: [],
      title: "Playback failed — {{ payload.titleName }}",
      message:
        "{{ payload.viewer.username }} could not play {{ payload.titleName }}: {{ payload.error }}",
      posterUrl: "{{ payload.posterUrl }}",
      priority: "high",
    },
  });
  const g4 = graphOf(
    [t4, d4, a4],
    [
      [t4, "next", d4],
      [d4, "pass", a4],
    ],
  );

  const t5 = node("trigger.schedule", "Friday at 5 pm", { cron: "0 17 * * 5", timezone: "UTC" });
  const dg5 = node("action", "Build this week's digest", {
    actionType: "digest.generate",
    params: { window: "7d", groupEpisodes: true },
  });
  const a5 = node("action", "Send the newsletter", {
    actionType: "notify.send",
    params: {
      channelIds: [],
      title: "New this week on Ether",
      message: "{{ steps." + dg5.id + ".output.text }}",
    },
  });
  const g5 = graphOf(
    [t5, dg5, a5],
    [
      [t5, "next", dg5],
      [dg5, "next", a5],
    ],
  );

  const t6 = node("trigger.event", "Movie night signals", {
    events: ["playback.session.started", "playback.session.paused", "playback.marker.entered"],
  });
  const c6 = node("condition", "Living Room Apple TV", {
    condition: {
      op: "and",
      rules: [{ field: "client.name", op: "eq", value: "Living Room Apple TV" }],
    },
  });
  const a6 = node("action", "Tell the lights", {
    actionType: "mqtt.publish",
    params: { topic: "home/cinema/event", payload: "{{ event.type }}", retain: false },
  });
  const g6 = graphOf(
    [t6, c6, a6],
    [
      [t6, "next", c6],
      [c6, "true", a6],
    ],
  );

  const t7 = node("trigger.event", "New device or address", {
    events: ["auth.device.registered", "auth.session.new_ip"],
  });
  const a7 = node("action", "Notify me", {
    actionType: "notify.send",
    params: {
      channelIds: [],
      title: "New sign-in to watch for",
      message: "A viewer signed in somewhere new. Check Activity if it wasn't them.",
      priority: "normal",
    },
  });
  const g7 = graphOf([t7, a7], [[t7, "next", a7]]);

  const t8 = node("trigger.event", "Dead pin or empty search", {
    events: ["resolver.pin.unhealthy"],
  });
  const a8 = node("action", "Unpin the dead release", {
    actionType: "release.unpin",
    params: { titleKey: "{{ payload.titleKey }}" },
  });
  const a8b = node("action", "Notify me", {
    actionType: "notify.send",
    params: {
      channelIds: [],
      title: "Dead pin removed",
      message:
        "{{ payload.titleName }} — {{ payload.reason }}. Ether will choose a fresh release next play.",
    },
  });
  const g8 = graphOf(
    [t8, a8, a8b],
    [
      [t8, "next", a8],
      [a8, "next", a8b],
    ],
  );

  const t9 = node("trigger.event", "Something critical breaks", {
    events: [
      "provider.service.outage",
      "indexer.service.outage",
      "cache.budget.full",
      "system.backup.failed",
      "system.disk.low",
    ],
  });
  const d9 = node("logic.dedupe", "Don't repeat the same alert for 30 minutes", {
    keyTemplate: "{{ event.type }}",
    cooldown: "30m",
    maxPerWindow: 1,
  });
  const a9 = node("action", "Notify me right away", {
    actionType: "notify.send",
    params: {
      channelIds: [],
      title: "Ether needs attention",
      message: "{{ event.type }} — open the console for details.",
      priority: "high",
      bypassDigest: true,
    },
  });
  const g9 = graphOf(
    [t9, d9, a9],
    [
      [t9, "next", d9],
      [d9, "pass", a9],
    ],
  );

  return { g1, g2, g3, g4, g5, g6, g7, g8, g9 };
}

export function flowTemplates(): FlowTemplate[] {
  const g = templateGraphs();
  return [
    {
      id: "stop-remote-4k-transcodes",
      name: "Stop remote 4K video transcodes",
      description:
        "Ends away-from-home streams that transcode 4K video, with a friendly message. Viewers tagged VIP are exempt.",
      category: "Playback",
      icon: "OctagonX",
      graph: g.g1,
    },
    {
      id: "stop-idle-paused",
      name: "Stop streams paused too long",
      description: "Waits 15 minutes after a pause; if nothing resumes, frees the stream.",
      category: "Playback",
      icon: "Timer",
      graph: g.g2,
    },
    {
      id: "relieve-buffering",
      name: "Relieve buffering automatically",
      description:
        "When one stream stalls three times in two minutes, stops the lowest-priority transcode — never the buffering one.",
      category: "Playback",
      icon: "Gauge",
      graph: g.g3,
    },
    {
      id: "playback-failure-alerts",
      name: "Playback failure alerts",
      description:
        "Tells you when a viewer cannot play something — at most once per title each hour, so one bad release can't flood you.",
      category: "Notifications",
      icon: "Bell",
      notifyPreset: "playback",
      params: NOTIFY_PARAM,
      graph: g.g4,
    },
    {
      id: "weekly-new-this-week",
      name: "Weekly “new this week” newsletter",
      description:
        "Every Friday at 5 pm, builds a digest of the last seven days and sends it to your channels.",
      category: "Notifications",
      icon: "Newspaper",
      notifyPreset: "newsletter",
      params: NOTIFY_PARAM,
      graph: g.g5,
    },
    {
      id: "cinema-lights",
      name: "Cinema lights with MQTT",
      description:
        "Publishes play, pause and credits events for the living-room TV so Home Assistant can run the room.",
      category: "Smart home",
      icon: "Lightbulb",
      graph: g.g6,
    },
    {
      id: "new-device-alert",
      name: "New device & new address alerts",
      description: "Heads-up when a viewer signs in on a new device or from a new network address.",
      category: "Viewers",
      icon: "Smartphone",
      params: NOTIFY_PARAM,
      graph: g.g7,
    },
    {
      id: "auto-unpin-dead-pin",
      name: "Remove dead pins automatically",
      description:
        "When a pinned release dies, unpins it so the next play picks a healthy release, and tells you.",
      category: "Library",
      icon: "PinOff",
      params: NOTIFY_PARAM,
      graph: g.g8,
    },
    {
      id: "critical-alerts",
      name: "Critical server alerts",
      description:
        "Provider and indexer outages, a full cache, failed backups and low disk — sent immediately, never twice in 30 minutes.",
      category: "Notifications",
      icon: "Siren",
      notifyPreset: "critical",
      params: NOTIFY_PARAM,
      graph: g.g9,
    },
  ];
}

// ---------- Condition evaluation + test-run simulator ----------

function resolvePath(
  path: string,
  payload: Record<string, unknown>,
  eventType: string,
): { found: boolean; value: unknown } {
  const clean = path.startsWith("payload.") ? path.slice(8) : path;
  if (clean === "event.type") return { found: true, value: eventType };
  if (clean === "event.node") return { found: true, value: "node-1" };
  if (
    clean.startsWith("event.") ||
    clean.startsWith("correlation.") ||
    clean.startsWith("steps.") ||
    clean.startsWith("vars.") ||
    clean.startsWith("trigger.")
  ) {
    return { found: false, value: undefined };
  }
  let cur: unknown = payload;
  for (const part of clean.split(".")) {
    if (!cur || typeof cur !== "object" || !(part in (cur as Record<string, unknown>)))
      return { found: false, value: undefined };
    cur = (cur as Record<string, unknown>)[part];
  }
  return { found: true, value: cur };
}

function evalRule(
  rule: ConditionRule,
  payload: Record<string, unknown>,
  eventType: string,
): { matched: boolean; actual: unknown; found: boolean } {
  const { found, value: actual } = resolvePath(rule.field, payload, eventType);
  const want = rule.value;
  const s = (v: unknown) => (rule.caseSensitive ? String(v ?? "") : String(v ?? "").toLowerCase());
  const num = (v: unknown) => (typeof v === "number" ? v : Number(v));
  let matched = false;
  switch (rule.op) {
    case "exists":
      matched = found;
      break;
    case "not_exists":
      matched = !found;
      break;
    case "is_true":
      matched = actual === true;
      break;
    case "is_false":
      matched = actual === false;
      break;
    case "eq":
      matched =
        found &&
        (typeof actual === "number" && typeof want === "number"
          ? actual === want
          : s(actual) === s(want));
      break;
    case "neq":
      matched = !found || s(actual) !== s(want);
      break;
    case "gt":
      matched = num(actual) > num(want);
      break;
    case "gte":
      matched = num(actual) >= num(want);
      break;
    case "lt":
      matched = num(actual) < num(want);
      break;
    case "lte":
      matched = num(actual) <= num(want);
      break;
    case "contains":
      matched = Array.isArray(actual)
        ? actual.some((x) => s(x) === s(want))
        : s(actual).includes(s(want));
      break;
    case "not_contains":
      matched = Array.isArray(actual)
        ? !actual.some((x) => s(x) === s(want))
        : !s(actual).includes(s(want));
      break;
    case "starts_with":
      matched = s(actual).startsWith(s(want));
      break;
    case "ends_with":
      matched = s(actual).endsWith(s(want));
      break;
    case "matches":
      try {
        matched = new RegExp(String(want)).test(String(actual ?? ""));
      } catch {
        matched = false;
      }
      break;
    case "in":
      matched = Array.isArray(want) && want.some((x) => s(x) === s(actual));
      break;
    case "not_in":
      matched = Array.isArray(want) && !want.some((x) => s(x) === s(actual));
      break;
    case "contains_any":
      matched =
        Array.isArray(actual) &&
        Array.isArray(want) &&
        want.some((w) => actual.some((x) => s(x) === s(w)));
      break;
    case "contains_all":
      matched =
        Array.isArray(actual) &&
        Array.isArray(want) &&
        want.every((w) => actual.some((x) => s(x) === s(w)));
      break;
  }
  return { matched, actual, found };
}

function evalGroup(
  group: ConditionGroup | undefined,
  payload: Record<string, unknown>,
  eventType: string,
): ConditionTrace {
  const op = group?.op === "or" ? "or" : "and";
  const rules = (group?.rules ?? []).map((r) => {
    const e = evalRule(r, payload, eventType);
    return {
      field: r.field,
      op: r.op,
      expected: r.value,
      actual: e.actual,
      found: e.found,
      matched: e.matched,
    };
  });
  const groups = (group?.groups ?? []).map((g) => evalGroup(g, payload, eventType));
  const parts = [...rules.map((r) => r.matched), ...groups.map((g) => g.matched)];
  let matched =
    parts.length === 0 ? true : op === "or" ? parts.some(Boolean) : parts.every(Boolean);
  if (group?.negate) matched = !matched;
  return { op, negate: group?.negate || undefined, matched, rules, groups };
}

/** Walk the DAG from its triggers, following the branch each node returns. */
function simulate(
  graph: Graph,
  payload: Record<string, unknown>,
  eventType: string,
  runId: string,
): RunStep[] {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const out = new Map<string, { handle: string; target: string }[]>();
  for (const e of graph.edges) {
    const list = out.get(e.source) ?? [];
    list.push({ handle: e.sourceHandle ?? "next", target: e.target });
    out.set(e.source, list);
  }
  const steps: RunStep[] = [];
  const visited = new Set<string>();
  const queue = graph.nodes.filter((n) => n.kind.startsWith("trigger.")).map((n) => n.id);
  let seq = 0;
  while (queue.length > 0 && seq < 100) {
    const id = queue.shift()!;
    if (visited.has(id)) continue;
    visited.add(id);
    const n = byId.get(id)!;
    seq += 1;
    const cfg = (n.config ?? {}) as Record<string, unknown>;
    let branch = "next";
    let output: Record<string, unknown> = {};
    let durationMs = 2 + ((seq * 37) % 40);
    if (n.kind === "condition") {
      const trace = evalGroup(cfg.condition as ConditionGroup, payload, eventType);
      branch = trace.matched ? "true" : "false";
      output = { matched: trace.matched, trace };
    } else if (n.kind === "logic.switch") {
      const cases = (cfg.cases as { key: string; condition: ConditionGroup }[] | undefined) ?? [];
      branch = "default";
      for (const c of cases) {
        const trace = evalGroup(c.condition, payload, eventType);
        if (trace.matched) {
          branch = c.key;
          output = { trace };
          break;
        }
      }
    } else if (n.kind === "logic.wait_event") {
      branch = "timeout";
      output = { waitedFor: cfg.eventTypes, timeout: cfg.timeout };
      durationMs = 0;
    } else if (n.kind === "logic.window") {
      const threshold = Number(cfg.threshold ?? 3);
      output = { count: threshold, threshold, window: cfg.window };
      branch = "reached";
    } else if (n.kind === "logic.dedupe") {
      branch = "pass";
      output = { cooldown: cfg.cooldown, suppressed: 0 };
    } else if (n.kind === "action") {
      const spec = FLOW_ACTIONS.find((a) => a.type === cfg.actionType);
      output = { actionType: cfg.actionType, dryRun: true };
      for (const o of spec?.outputs ?? [])
        output[o.name] =
          o.type === "int"
            ? 1
            : o.name === "html" || o.name === "text"
              ? "8 new this week — Dune: Part Two, Night Desk S3…"
              : "(sample)";
      durationMs = 18 + ((seq * 53) % 90);
    } else if (n.kind === "logic.delay") {
      output = { duration: cfg.duration ?? cfg.until ?? cfg.durationField };
      durationMs = 0;
    }
    const now = new Date().toISOString();
    steps.push({
      id: seq,
      runId,
      seq,
      nodeId: n.id,
      nodeKind: n.kind,
      status: n.disabled ? "skipped" : "completed",
      branch,
      input: { eventType },
      output,
      startedAt: now,
      finishedAt: now,
      durationMs,
    });
    if (!n.disabled) {
      for (const e of out.get(n.id) ?? []) {
        if (e.handle === branch || e.handle === "") queue.push(e.target);
      }
    }
  }
  return steps;
}

// ---------- Validation (mirrors the checks the console surfaces inline) ----------

export function validateFlowGraph(graph: Graph): ValidationResult {
  const errors: ValidationIssue[] = [];
  const warnings: ValidationIssue[] = [];
  const err = (code: string, message: string, nodeId?: string, edgeId?: string) =>
    errors.push({ code, message, nodeId, edgeId });
  const warn = (code: string, message: string, nodeId?: string) =>
    warnings.push({ code, message, nodeId });
  if (!graph || !Array.isArray(graph.nodes) || graph.nodes.length === 0) {
    err("empty_graph", "Add at least a trigger and one step to get started.");
    return { valid: false, errors, warnings };
  }
  const ids = new Set<string>();
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const eventTypes = new Set(FLOW_EVENTS.map((e) => e.type));
  const actionTypes = new Map(FLOW_ACTIONS.map((a) => [a.type, a]));
  let triggers = 0;
  for (const n of graph.nodes) {
    if (ids.has(n.id)) err("duplicate_node_id", `Two steps share the id “${n.id}”.`, n.id);
    ids.add(n.id);
    const cfg = (n.config ?? {}) as Record<string, unknown>;
    if (n.kind.startsWith("trigger.")) triggers += 1;
    if (n.kind === "trigger.event") {
      const evs = (cfg.events as string[] | undefined) ?? [];
      if (evs.length === 0) err("no_events", "Choose at least one event to listen for.", n.id);
      for (const e of evs)
        if (!e.endsWith(".*") && !eventTypes.has(e))
          warn(
            "unknown_event",
            `“${e}” is not in the event catalogue yet — it may come from a plugin.`,
            n.id,
          );
    }
    if (n.kind === "trigger.schedule" && !cfg.cron && !cfg.every)
      err("no_schedule", "Set a schedule — a repeating interval or specific times.", n.id);
    if (n.kind === "condition") {
      const cond = cfg.condition as ConditionGroup | undefined;
      if (!cond || ((cond.rules?.length ?? 0) === 0 && (cond.groups?.length ?? 0) === 0))
        err("empty_condition", "Add at least one rule, or remove this condition.", n.id);
      for (const r of cond?.rules ?? [])
        if (!r.field) err("missing_field", "A rule is missing its field.", n.id);
    }
    if (n.kind === "logic.wait_event" && !cfg.timeout)
      err("no_timeout", "Give the wait a timeout so runs can't wait forever.", n.id);
    if (n.kind === "logic.window" && (!cfg.threshold || !cfg.window))
      err("bad_window", "Set how many times, within what window.", n.id);
    if (n.kind === "logic.dedupe" && !cfg.cooldown)
      err("bad_cooldown", "Set a cooldown period.", n.id);
    if (n.kind === "logic.delay" && !cfg.duration && !cfg.until && !cfg.durationField)
      err("bad_delay", "Set how long to wait.", n.id);
    if (n.kind === "action") {
      const spec = actionTypes.get(String(cfg.actionType ?? ""));
      if (!spec) err("unknown_action", "Choose an action for this step.", n.id);
      else {
        const params = (cfg.params ?? {}) as Record<string, unknown>;
        for (const p of spec.params ?? []) {
          if (p.required && (params[p.key] === undefined || params[p.key] === ""))
            err("missing_param", `“${p.label}” is required.`, n.id);
          const v = params[p.key];
          if (p.template && typeof v === "string") {
            const terr = validateTemplateSyntax(v);
            if (terr) err("bad_template", `${p.label}: ${terr}`, n.id);
          }
        }
      }
    }
    if (!n.kind.startsWith("trigger.") && !graph.edges.some((e) => e.target === n.id))
      warn("unconnected", "This step isn't connected to the flow yet.", n.id);
  }
  if (triggers === 0)
    err(
      "no_trigger",
      "Every flow starts with a trigger — an event, a schedule, a webhook or a manual run.",
    );
  for (const e of graph.edges) {
    const target = byId.get(e.target);
    if (!byId.has(e.source) || !target) {
      err("dangling_edge", "A connection points at a step that no longer exists.", undefined, e.id);
      continue;
    }
    if (target.kind.startsWith("trigger."))
      err(
        "edge_into_trigger",
        "Triggers start a flow — nothing can connect into one.",
        target.id,
        e.id,
      );
    const without = { ...graph, edges: graph.edges.filter((x) => x.id !== e.id) };
    if (wouldCreateCycle(without, e.source, e.target))
      err("cycle", "This connection would loop the flow back on itself.", undefined, e.id);
  }
  return { valid: errors.length === 0, errors, warnings };
}

// ---------- Describe-it (prompt → draft) ----------

export function describeFlow(prompt: string): {
  name: string;
  description: string;
  graph: Graph;
  explanation: string;
} {
  const p = prompt.toLowerCase();
  const templates = flowTemplates();
  const pick = (id: string, why: string) => {
    const t = templates.find((x) => x.id === id)!;
    return {
      name: t.name,
      description: t.description,
      graph: structuredClone(t.graph),
      explanation: why,
    };
  };
  if (/transcod/.test(p) && /(away|remote|wan|4k|hdr|home)/.test(p))
    return pick(
      "stop-remote-4k-transcodes",
      "I listen for playback starting and for stream decisions, check that the video is transcoding from a 4K source for someone away from home (and not tagged VIP), then stop that stream with a polite message.",
    );
  if (/paus|idle|left.*on|forgot/.test(p))
    return pick(
      "stop-idle-paused",
      "When playback pauses I wait up to 15 minutes for resume or stop; if neither arrives, the idle stream is ended.",
    );
  if (/buffer|stall/.test(p))
    return pick(
      "relieve-buffering",
      "Three stalls from the same stream inside two minutes triggers relief: the lowest-priority transcode stops, never the buffering stream itself.",
    );
  if (/newsletter|weekly|digest|new this week|recently added/.test(p))
    return pick(
      "weekly-new-this-week",
      "A Friday 5 pm schedule builds a seven-day digest and sends it through your notification channels.",
    );
  if (/light|home assistant|mqtt|cinema|movie night/.test(p))
    return pick(
      "cinema-lights",
      "Play, pause and credits moments on the living-room TV are published to MQTT for your lighting scenes.",
    );
  if (/device|sign.?in|new ip|address|sharing|account/.test(p))
    return pick(
      "new-device-alert",
      "New devices and first-seen network addresses raise a heads-up so you can spot account sharing.",
    );
  if (/pin|dead release|unpin/.test(p))
    return pick(
      "auto-unpin-dead-pin",
      "An unhealthy pin is removed automatically and you're told, so the next play resolves fresh.",
    );
  if (/outage|critical|down|backup|disk|provider|indexer/.test(p))
    return pick(
      "critical-alerts",
      "Critical server events alert you immediately, deduplicated per event type for 30 minutes.",
    );
  if (/fail|error|can'?t play|broken/.test(p))
    return pick(
      "playback-failure-alerts",
      "Playback failures alert you with the title, viewer and error — capped at one alert per title each hour.",
    );
  // Generic fallback: notify on failure.
  uid = 900;
  const trig = node("trigger.event", "Something happens", { events: ["playback.session.failed"] });
  const act = node("action", "Notify me", {
    actionType: "notify.send",
    params: { channelIds: [], title: "{{ payload.titleName }}", message: prompt.slice(0, 140) },
  });
  return {
    name: prompt.length > 48 ? `${prompt.slice(0, 47)}…` : prompt || "Untitled flow",
    description: "Draft built from your description — review the trigger and message, then save.",
    graph: graphOf([trig, act], [[trig, "next", act]]),
    explanation:
      "I started from the closest everyday pattern — an event trigger feeding a notification. Tell me more (which viewers, which titles, how often) or adjust the steps directly.",
  };
}

// ---------- Stateful mock service ----------

export interface MockNotifyChannel {
  id: string;
  name?: string;
  kind: string;
  enabled: boolean;
  events: string[];
  digestMinutes?: number;
  perMinute?: number;
}

export interface FlowsMockOptions {
  llmEnabled?: () => boolean;
  channels?: MockNotifyChannel[];
}

export function createFlowsMock(opts: FlowsMockOptions = {}) {
  const templates = flowTemplates();
  const seed = (templateId: string, patch: Partial<Flow>): Flow => {
    const t = templates.find((x) => x.id === templateId)!;
    return {
      id: `flow-${templateId}`,
      name: t.name,
      description: t.description,
      enabled: true,
      version: 3,
      graph: structuredClone(t.graph),
      templateId,
      tags: [],
      createdAt: iso(21 * 24 * H),
      updatedAt: iso(2 * H),
      lastStatus: "completed",
      runs24h: 0,
      failures24h: 0,
      activeRuns: 0,
      ...patch,
    };
  };
  const flows: Flow[] = [
    seed("stop-remote-4k-transcodes", {
      lastRunAt: iso(38 * M),
      runs24h: 6,
      failures24h: 0,
      triggerSummary: "Playback started · Stream decision",
    }),
    seed("stop-idle-paused", {
      lastRunAt: iso(12 * M),
      lastStatus: "waiting",
      runs24h: 9,
      failures24h: 0,
      activeRuns: 1,
      triggerSummary: "Playback paused",
    }),
    seed("playback-failure-alerts", {
      lastRunAt: iso(74 * M),
      runs24h: 4,
      failures24h: 1,
      triggerSummary: "Playback failed",
    }),
    seed("weekly-new-this-week", {
      lastRunAt: iso(3 * 24 * H),
      runs24h: 0,
      triggerSummary: "Fridays at 17:00",
    }),
    seed("critical-alerts", {
      enabled: false,
      lastRunAt: iso(2 * 24 * H),
      runs24h: 0,
      triggerSummary: "5 critical events",
    }),
    seed("cinema-lights", {
      lastRunAt: iso(9 * M),
      lastStatus: "failed",
      runs24h: 31,
      failures24h: 3,
      triggerSummary: "Play · Pause · Credits",
    }),
  ];
  let runSeq = 1000;
  const runs: Run[] = [];
  const stepsByRun = new Map<string, RunStep[]>();
  const mkRun = (
    flow: Flow,
    agoMs: number,
    status: Run["status"],
    eventType: string,
    dryRun = false,
    graphOverride?: Graph,
  ): Run => {
    runSeq += 1;
    const id = `run_${runSeq.toString(16)}`;
    const schema = FLOW_EVENTS.find((e) => e.type === eventType);
    const payload = (schema?.sample ?? PLAYBACK_SAMPLE) as Record<string, unknown>;
    const steps = simulate(graphOverride ?? flow.graph, payload, eventType, id);
    if (status === "failed" && steps.length > 0) {
      const last = steps[steps.length - 1]!;
      last.status = "failed";
      last.error = "MQTT broker unreachable: dial tcp 10.0.0.51:1883: i/o timeout";
    }
    if (status === "waiting" && steps.length > 1) {
      steps.splice(2);
      const wait = steps[steps.length - 1]!;
      wait.status = "running";
    }
    stepsByRun.set(id, steps);
    const run: Run = {
      id,
      flowId: flow.id,
      flowName: flow.name,
      flowVersion: flow.version,
      triggerKind: "trigger.event",
      eventId: `evt_${runSeq.toString(16)}`,
      eventType,
      correlationKey: "sess_9f2a",
      status,
      attempt: 1,
      dryRun,
      error: status === "failed" ? "MQTT broker unreachable" : undefined,
      stepCount: steps.length,
      startedAt: iso(agoMs),
      updatedAt: iso(agoMs - 4000),
      finishedAt: status === "waiting" || status === "running" ? undefined : iso(agoMs - 4000),
      wakeAt: status === "waiting" ? new Date(Date.now() + 11 * M).toISOString() : undefined,
    };
    runs.unshift(run);
    return run;
  };
  mkRun(flows[1]!, 12 * M, "waiting", "playback.session.paused");
  mkRun(flows[5]!, 9 * M, "failed", "playback.session.started");
  mkRun(flows[0]!, 38 * M, "completed", "playback.stream.decision");
  mkRun(flows[2]!, 74 * M, "completed", "playback.session.failed");
  mkRun(flows[0]!, 3 * H, "completed", "playback.session.started");
  mkRun(flows[5]!, 4 * H, "completed", "playback.session.paused");
  mkRun(flows[2]!, 6 * H, "failed", "playback.session.failed");
  mkRun(flows[1]!, 8 * H, "completed", "playback.session.paused");
  mkRun(flows[3]!, 3 * 24 * H, "completed", "flows.digest.scheduled");
  mkRun(flows[4]!, 2 * 24 * H, "completed", "provider.service.outage");

  const json = (body: unknown, status = 200) => ({ status, body });
  const notFound = () =>
    json({ error: { code: "not_found", message: "That flow no longer exists." } }, 404);

  function triggerSummaryOf(graph: Graph): string {
    const trig = graph.nodes.find((n) => n.kind.startsWith("trigger."));
    if (!trig) return "No trigger";
    const cfg = (trig.config ?? {}) as Record<string, unknown>;
    if (trig.kind === "trigger.event") {
      const evs = (cfg.events as string[] | undefined) ?? [];
      return (
        evs
          .map((e) => FLOW_EVENTS.find((x) => x.type === e)?.label ?? e)
          .slice(0, 2)
          .join(" · ") || "No events"
      );
    }
    if (trig.kind === "trigger.schedule")
      return cfg.every ? `Every ${String(cfg.every)}` : String(cfg.cron ?? "Schedule");
    if (trig.kind === "trigger.webhook") return "Incoming webhook";
    return "Manual";
  }

  function applyTemplateParams(graph: Graph, params?: Record<string, unknown>): Graph {
    if (!params) return graph;
    const next = structuredClone(graph);
    for (const n of next.nodes) {
      if (n.kind !== "action") continue;
      const cfg = (n.config ?? {}) as Record<string, unknown>;
      const p = { ...((cfg.params ?? {}) as Record<string, unknown>) };
      for (const [k, v] of Object.entries(params)) p[k] = v;
      n.config = { ...cfg, params: p };
    }
    return next;
  }

  return {
    handle(
      method: string,
      seg: string[],
      q: URLSearchParams,
      body: unknown,
    ): { status: number; body: unknown } | null {
      if (seg[0] !== "flows") return null;
      const b = (body ?? {}) as Record<string, unknown>;

      if (method === "GET" && seg[1] === "catalogue" && seg[2] === "events")
        return json({ events: FLOW_EVENTS });
      if (method === "GET" && seg[1] === "catalogue" && seg[2] === "actions")
        return json({ actions: FLOW_ACTIONS });
      if (method === "GET" && seg[1] === "templates") return json({ templates });
      if (method === "POST" && seg[1] === "validate")
        return json(
          validateFlowGraph((b.graph ?? { schemaVersion: 1, nodes: [], edges: [] }) as Graph),
        );
      if (method === "POST" && seg[1] === "describe") {
        const prompt = String(b.prompt ?? "").trim();
        if (!prompt || prompt.length > 2000) {
          return json(
            {
              error: {
                code: "bad_request",
                message: "describe the flow you want in at most 2,000 characters",
              },
            },
            400,
          );
        }
        if (opts.llmEnabled && !opts.llmEnabled()) {
          return json(
            {
              error: {
                code: "ai_unavailable",
                message: "Configure AI in Integrations to describe flows in plain language.",
              },
            },
            409,
          );
        }
        return json(describeFlow(prompt));
      }
      if (method === "POST" && seg[1] === "test") {
        if (!b.graph) {
          return json({ error: { code: "bad_request", message: "graph is required" } }, 400);
        }
        const graph = b.graph as Graph;
        const v = validateFlowGraph(graph);
        if (!v.valid) {
          return json(
            {
              error: { code: "validation_failed", message: "flow definition is invalid" },
              validation: v,
            },
            400,
          );
        }
        const dryRun = b.dryRun !== false;
        const eventType = String(
          (b.event as Record<string, unknown> | undefined)?.type ?? "playback.session.started",
        );
        const schema = FLOW_EVENTS.find((e) => e.type === eventType);
        const payload = {
          ...((schema?.sample ?? PLAYBACK_SAMPLE) as Record<string, unknown>),
          ...(((b.event as Record<string, unknown> | undefined)?.payload ?? {}) as Record<
            string,
            unknown
          >),
        };
        runSeq += 1;
        const runId = `run_${runSeq.toString(16)}`;
        const steps = simulate(graph, payload, eventType, runId);
        const now = new Date().toISOString();
        const run: Run = {
          id: runId,
          flowId: "",
          flowName: "Draft",
          flowVersion: 1,
          triggerKind: "trigger.event",
          eventType,
          status: "completed",
          attempt: 1,
          dryRun,
          stepCount: steps.length,
          startedAt: now,
          updatedAt: now,
          finishedAt: now,
        };
        return json({ run, steps });
      }
      if (method === "POST" && seg[1] === "customize-notification") {
        const channelId = String(b.channelId ?? "").trim();
        if (!channelId) {
          return json({ error: { code: "bad_request", message: "channelId is required" } }, 400);
        }
        const ch = opts.channels?.find((c) => c.id === channelId);
        if (!ch) {
          return json(
            { error: { code: "not_found", message: "notification channel not found" } },
            404,
          );
        }
        const requestedRaw: string[] = [];
        if (typeof b.event === "string" && b.event.trim()) requestedRaw.push(b.event.trim());
        if (Array.isArray(b.events)) {
          for (const ev of b.events) {
            const s = String(ev ?? "").trim();
            if (s && !requestedRaw.includes(s)) requestedRaw.push(s);
          }
        }
        const channelHasWildcard = ch.events.includes("*");
        const requested = requestedRaw.length ? requestedRaw : [...ch.events];
        if (requested.length === 0) {
          return json(
            {
              error: {
                code: "cannot_customize",
                message:
                  "channel does not have any matching legacy notification rules to customize",
              },
            },
            409,
          );
        }
        const expandedRequested: string[] = [];
        for (const r of requested) {
          if (r === "*") {
            for (const k of Object.keys(LEGACY_NOTIFY_TO_CANONICAL)) {
              if (!expandedRequested.includes(k)) expandedRequested.push(k);
            }
            continue;
          }
          if (!channelHasWildcard && !ch.events.includes(r)) {
            return json(
              {
                error: {
                  code: "bad_request",
                  message: `channel "${ch.name ?? ch.id}" is not subscribed to legacy rule "${r}"`,
                },
              },
              400,
            );
          }
          if (!expandedRequested.includes(r)) expandedRequested.push(r);
        }
        const notes: string[] = [];
        const migrated: string[] = [];
        const canonicalEvents: string[] = [];
        let isNewsletter = false;
        let hasCrash = false;
        for (const k of expandedRequested) {
          if (k === "server.crash") {
            hasCrash = true;
            notes.push(
              "server.crash remains on the notification channel because it is delivered by the out-of-process crash watcher.",
            );
            continue;
          }
          if (k === "newsletter.weekly") {
            isNewsletter = true;
            migrated.push(k);
            continue;
          }
          const canonical = LEGACY_NOTIFY_TO_CANONICAL[k];
          if (!canonical) {
            return json(
              {
                error: { code: "bad_request", message: `unknown legacy notification event "${k}"` },
              },
              400,
            );
          }
          migrated.push(k);
          if (!canonicalEvents.includes(canonical)) canonicalEvents.push(canonical);
        }
        if (migrated.length === 0) {
          const msg = hasCrash
            ? "server.crash is delivered by the out-of-process crash watcher and cannot be moved exclusively into an in-process flow"
            : "channel does not have any matching legacy notification rules to customize";
          return json({ error: { code: "cannot_customize", message: msg } }, 409);
        }
        const removeLegacy = b.removeLegacyRule !== false;
        const sourceEvents = channelHasWildcard
          ? Object.keys(LEGACY_NOTIFY_TO_CANONICAL)
          : ch.events;
        let kept = removeLegacy
          ? sourceEvents.filter((k) => !migrated.includes(k))
          : [...ch.events];
        const chLabel = ch.name?.trim() || ch.id;
        let graph: Graph;
        if (isNewsletter && canonicalEvents.length === 0) {
          graph = autoLayoutGraph({
            schemaVersion: 1,
            nodes: [
              {
                id: "trigger_1",
                kind: "trigger.schedule",
                label: "Weekly schedule",
                position: { x: 0, y: 0 },
                config: { cron: "0 17 * * 5" },
              },
              {
                id: "digest_1",
                kind: "action",
                label: "Generate weekly digest",
                position: { x: 0, y: 0 },
                config: {
                  actionType: "digest.generate",
                  params: { window: "7d", groupEpisodes: true },
                },
              },
              {
                id: "notify_1",
                kind: "action",
                label: `Notify ${chLabel}`,
                position: { x: 0, y: 0 },
                config: {
                  actionType: "notify.send",
                  params: {
                    channelIds: [ch.id],
                    title: "Weekly Recently Added",
                    message: "{{ steps.digest_1.output.text }}",
                    html: "{{ steps.digest_1.output.html }}",
                    bypassDigest: true,
                  },
                },
              },
            ],
            edges: [
              { id: "e1", source: "trigger_1", sourceHandle: "next", target: "digest_1" },
              { id: "e2", source: "digest_1", sourceHandle: "next", target: "notify_1" },
            ],
          });
        } else {
          if (isNewsletter) {
            notes.push(
              "newsletter.weekly uses a weekly schedule and can be customized separately via the Weekly Recently Added template.",
            );
            const idx = migrated.indexOf("newsletter.weekly");
            if (idx >= 0) migrated.splice(idx, 1);
            if (removeLegacy && !kept.includes("newsletter.weekly"))
              kept = [...kept, "newsletter.weekly"];
          }
          graph = autoLayoutGraph({
            schemaVersion: 1,
            nodes: [
              {
                id: "trigger_1",
                kind: "trigger.event",
                label: "Notification events",
                position: { x: 0, y: 0 },
                config: { events: canonicalEvents },
              },
              {
                id: "notify_1",
                kind: "action",
                label: `Notify ${chLabel}`,
                position: { x: 0, y: 0 },
                config: {
                  actionType: "notify.send",
                  params: {
                    channelIds: [ch.id],
                    title: "Ether alert: {{ event.type }}",
                    message: "Event {{ event.type }} on {{ event.node }}.",
                    bypassDigest: (ch.digestMinutes ?? 0) <= 0,
                  },
                },
              },
            ],
            edges: [{ id: "e1", source: "trigger_1", sourceHandle: "next", target: "notify_1" }],
          });
        }
        if (removeLegacy) ch.events = kept;
        const name =
          String(b.name ?? "").trim() ||
          (migrated.length === 1 ? `${chLabel}: ${migrated[0]}` : `${chLabel} notifications`);
        const description =
          String(b.description ?? "").trim() ||
          `Customized from notification channel "${chLabel}" (${migrated.join(", ")}).`;
        const flow: Flow = {
          id: `flow-${Date.now().toString(36)}`,
          name,
          description,
          enabled: typeof b.enabled === "boolean" ? b.enabled : ch.enabled,
          version: 1,
          graph,
          tags: [],
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
          runs24h: 0,
          failures24h: 0,
          activeRuns: 0,
          triggerSummary: triggerSummaryOf(graph),
        };
        flows.unshift(flow);
        return json({ flow, channelId: ch.id, migrated, kept, notes }, 201);
      }
      if (method === "POST" && seg[1] === "from-template") {
        const t = templates.find((x) => x.id === decodeURIComponent(seg[2] ?? ""));
        if (!t) return notFound();
        const flow: Flow = {
          id: `flow-${Date.now().toString(36)}`,
          name: String(b.name ?? t.name),
          description: t.description,
          enabled: Boolean(b.enabled ?? true),
          version: 1,
          graph: applyTemplateParams(
            structuredClone(t.graph),
            b.params as Record<string, unknown> | undefined,
          ),
          templateId: t.id,
          tags: [],
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
          runs24h: 0,
          failures24h: 0,
          activeRuns: 0,
          triggerSummary: triggerSummaryOf(t.graph),
        };
        flows.unshift(flow);
        return json({ flow }, 201);
      }
      if (seg[1] === "runs") {
        if (method === "GET" && seg.length === 2) {
          const flowId = q.get("flowId") ?? "";
          const status = q.get("status") ?? "";
          const limit = Number(q.get("limit") ?? 50);
          return json({
            runs: runs
              .filter((r) => (!flowId || r.flowId === flowId) && (!status || r.status === status))
              .sort((a, b) => b.startedAt.localeCompare(a.startedAt))
              .slice(0, limit),
          });
        }
        const run = runs.find((r) => r.id === seg[2]);
        if (method === "GET" && seg.length === 3)
          return run ? json({ run, steps: stepsByRun.get(run.id) ?? [] }) : notFound();
        if (method === "POST" && seg[3] === "cancel" && run) {
          run.status = "cancelled";
          run.finishedAt = new Date().toISOString();
          return json(run);
        }
        return notFound();
      }
      if (method === "GET" && seg[1] === "stats" && seg.length === 2) {
        const range = q.get("range") === "24h" || q.get("range") === "30d" ? q.get("range")! : "7d";
        return json({ range, stats: mockRunStats(range as "24h" | "7d" | "30d", flows, runs) });
      }
      if (seg.length === 1) {
        // The live list carries summaries, never graphs.
        if (method === "GET") return json({ flows: flows.map(flowSummaryOf) });
        if (method === "POST") {
          const graph = b.graph as Graph;
          const v = validateFlowGraph(graph);
          if (!v.valid)
            return json(
              {
                error: {
                  code: "invalid_flow",
                  message: v.errors?.[0]?.message ?? "This flow needs a fix before it can run.",
                },
                validation: v,
              },
              400,
            );
          const flow: Flow = {
            id: String(b.id ?? `flow-${Date.now().toString(36)}`),
            name: String(b.name ?? "Untitled flow"),
            description: String(b.description ?? ""),
            enabled: Boolean(b.enabled ?? true),
            version: 1,
            graph,
            limits: b.limits as Flow["limits"],
            tags: (b.tags as string[]) ?? [],
            createdAt: new Date().toISOString(),
            updatedAt: new Date().toISOString(),
            runs24h: 0,
            failures24h: 0,
            activeRuns: 0,
            triggerSummary: triggerSummaryOf(graph),
          };
          flows.unshift(flow);
          return json({ flow }, 201);
        }
      }
      const flow = flows.find((x) => x.id === decodeURIComponent(seg[1] ?? ""));
      if (!flow) return notFound();
      if (method === "GET" && seg.length === 2) return json({ flow });
      if (method === "GET" && seg[2] === "runs")
        return json({
          runs: runs
            .filter((r) => r.flowId === flow.id)
            .sort((a, b) => b.startedAt.localeCompare(a.startedAt))
            .slice(0, Number(q.get("limit") ?? 50)),
        });
      if (method === "PUT" && seg.length === 2) {
        const graph = b.graph as Graph;
        const v = validateFlowGraph(graph);
        if (!v.valid)
          return json(
            {
              error: {
                code: "invalid_flow",
                message: v.errors?.[0]?.message ?? "This flow needs a fix before it can run.",
              },
              validation: v,
            },
            400,
          );
        Object.assign(flow, {
          name: String(b.name ?? flow.name),
          description: String(b.description ?? flow.description ?? ""),
          enabled: Boolean(b.enabled ?? flow.enabled),
          graph,
          limits: b.limits ?? flow.limits,
          version: flow.version + 1,
          updatedAt: new Date().toISOString(),
          triggerSummary: triggerSummaryOf(graph),
          disabledReason: undefined,
        });
        return json({ flow });
      }
      if (method === "DELETE" && seg.length === 2) {
        flows.splice(flows.indexOf(flow), 1);
        return json({ ok: true });
      }
      if (method === "POST" && seg[2] === "enable") {
        flow.enabled = true;
        flow.disabledReason = undefined;
        flow.consecutiveFailures = 0;
        return json({ flow });
      }
      if (method === "POST" && seg[2] === "disable") {
        flow.enabled = false;
        return json({ flow });
      }
      if (method === "POST" && seg[2] === "webhook-token")
        return json({
          token: `flw_${Date.now().toString(36)}`,
          webhookUrl: `/ether/api/flows/hooks/${flow.id}`,
        });
      if (method === "POST" && seg[2] === "test") {
        const graph = (b.graph as Graph | undefined) ?? flow.graph;
        const eventType = String(
          (b.event as Record<string, unknown> | undefined)?.type ?? "playback.session.started",
        );
        const schema = FLOW_EVENTS.find((e) => e.type === eventType);
        const payload = {
          ...((schema?.sample ?? PLAYBACK_SAMPLE) as Record<string, unknown>),
          ...(((b.event as Record<string, unknown> | undefined)?.payload ?? {}) as Record<
            string,
            unknown
          >),
        };
        const probe: Flow = { ...flow, graph };
        const run = mkRun(probe, 0, "completed", eventType, true, graph);
        // Re-simulate with the caller's payload for an honest trace.
        const steps = simulate(graph, payload, eventType, run.id);
        stepsByRun.set(run.id, steps);
        run.stepCount = steps.length;
        return json({ run, steps });
      }
      if (method === "POST" && seg[2] === "run") {
        const run = mkRun(flow, 0, "completed", "playback.session.started");
        flow.lastRunAt = run.startedAt;
        flow.lastStatus = "completed";
        flow.runs24h = (flow.runs24h ?? 0) + 1;
        return json({ run });
      }
      return notFound();
    },
  };
}

/** A flow as GET /api/flows lists it (internal/admin flowSummary). */
function flowSummaryOf(f: Flow): FlowSummary {
  const kinds = [
    ...new Set(
      f.graph.nodes.filter((n) => n.kind.startsWith("trigger.") && !n.disabled).map((n) => n.kind),
    ),
  ].sort();
  const events = [
    ...new Set(
      f.graph.nodes
        .filter((n) => n.kind === "trigger.event" && !n.disabled)
        .flatMap((n) => (n.config as { events?: string[] } | undefined)?.events ?? []),
    ),
  ];
  return {
    id: f.id,
    name: f.name,
    description: f.description,
    enabled: f.enabled,
    disabledReason: f.disabledReason,
    version: f.version,
    tags: f.tags,
    templateId: f.templateId,
    consecutiveFailures: f.consecutiveFailures,
    trigger: { kinds, events },
    lastRunAt: f.lastRunAt,
    lastStatus: f.lastStatus,
    runs24h: f.runs24h ?? 0,
    failures24h: f.failures24h ?? 0,
    activeRuns: f.activeRuns ?? 0,
    createdAt: f.createdAt,
    updatedAt: f.updatedAt,
  };
}

/**
 * Overview aggregates for mock mode: a deterministic, realistic history
 * (busier evenings, the lighting flow failing now and then) shaped like
 * GET /api/flows/stats.
 */
function mockRunStats(range: "24h" | "7d" | "30d", flows: Flow[], runs: Run[]): RunStats {
  const bucketMs = range === "24h" ? H : range === "7d" ? 6 * H : 24 * H;
  const n = range === "24h" ? 24 : range === "7d" ? 28 : 30;
  const now = new Date();
  const end = new Date(now);
  if (range === "30d") end.setHours(0, 0, 0, 0);
  else if (range === "7d") end.setHours(Math.floor(now.getHours() / 6) * 6, 0, 0, 0);
  else end.setMinutes(0, 0, 0);
  const since = end.getTime() - (n - 1) * bucketMs;
  let seed = 7;
  const rand = () => {
    seed = (seed * 16807) % 2147483647;
    return seed / 2147483647;
  };
  const weights = flows.map((f) => (f.enabled ? Math.max(1, f.runs24h ?? 1) : 0.2));
  const perFlow = flows.map((f) => ({
    flowId: f.id,
    name: f.name,
    runs: 0,
    failed: 0,
    dur: 0,
    timed: 0,
  }));
  const buckets = Array.from({ length: n }, (_, i) => {
    const start = since + i * bucketMs;
    const hour = new Date(start).getHours();
    const evening = range === "30d" ? 1 : hour >= 17 && hour <= 23 ? 1.8 : hour < 7 ? 0.3 : 1;
    const scale = range === "24h" ? 0.4 : range === "7d" ? 1.6 : 5;
    const b = {
      start: new Date(start).toISOString(),
      succeeded: 0,
      failed: 0,
      skipped: 0,
      active: 0,
      avgDurationMs: 0,
    };
    let dur = 0;
    let timed = 0;
    flows.forEach((f, k) => {
      const count = Math.round(weights[k]! * evening * scale * (0.5 + rand()));
      for (let c = 0; c < count; c++) {
        const failRate = f.id.includes("cinema") ? 0.12 : f.id.includes("failure") ? 0.05 : 0.01;
        const r = rand();
        const ms = f.id.includes("idle") ? 600_000 + rand() * 900_000 : 400 + rand() * 4_000;
        perFlow[k]!.runs++;
        if (r < failRate) {
          b.failed++;
          perFlow[k]!.failed++;
        } else if (r < failRate + 0.04) b.skipped++;
        else b.succeeded++;
        if (r >= failRate && r < failRate + 0.04) continue;
        dur += ms;
        timed++;
        perFlow[k]!.dur += ms;
        perFlow[k]!.timed++;
      }
    });
    b.avgDurationMs = timed ? Math.round(dur / timed) : 0;
    return b;
  });
  if (n)
    buckets[n - 1]!.active = runs.filter(
      (r) => r.status === "waiting" || r.status === "running",
    ).length;
  const totals = buckets.reduce(
    (t, b) => ({
      runs: t.runs + b.succeeded + b.failed + b.skipped + b.active,
      succeeded: t.succeeded + b.succeeded,
      failed: t.failed + b.failed,
      skipped: t.skipped + b.skipped,
      active: t.active + b.active,
      avgDurationMs: 0,
    }),
    { runs: 0, succeeded: 0, failed: 0, skipped: 0, active: 0, avgDurationMs: 0 },
  );
  const timedAll = perFlow.reduce((s, f) => s + f.timed, 0);
  totals.avgDurationMs = timedAll
    ? Math.round(perFlow.reduce((s, f) => s + f.dur, 0) / timedAll)
    : 0;
  const triggerCounts = new Map<
    string,
    { kind: Run["triggerKind"]; eventType?: string; runs: number }
  >();
  flows.forEach((f, k) => {
    const sum = flowSummaryOf(f);
    const ev = sum.trigger?.events?.[0];
    const kind = (sum.trigger?.kinds[0] ?? "trigger.event") as Run["triggerKind"];
    const key = `${kind}:${ev ?? ""}`;
    const cur = triggerCounts.get(key) ?? {
      kind,
      eventType: kind === "trigger.event" ? ev : undefined,
      runs: 0,
    };
    cur.runs += perFlow[k]!.runs;
    triggerCounts.set(key, cur);
  });
  const failedNames = [
    "MQTT broker unreachable: dial tcp 10.0.0.51:1883: i/o timeout",
    "Discord webhook answered 429 Too Many Requests",
    "Viewer has no active session",
  ];
  const failing = flows.filter((_, k) => perFlow[k]!.failed > 0);
  const recentFailures: Run[] = failing.length
    ? Array.from({ length: Math.min(6, totals.failed) }, (_, i) => {
        const f = failing[i % failing.length]!;
        const at = new Date(now.getTime() - (i * 2 + 1) * 47 * M).toISOString();
        return {
          id: runs.find((r) => r.flowId === f.id && r.status === "failed")?.id ?? `run_fail_${i}`,
          flowId: f.id,
          flowName: f.name,
          flowVersion: f.version,
          triggerKind: "trigger.event",
          eventType: "playback.session.started",
          status: "failed",
          attempt: 1,
          error: failedNames[i % failedNames.length],
          stepCount: 3,
          startedAt: at,
          updatedAt: at,
          finishedAt: at,
        };
      })
    : [];
  return {
    since: new Date(since).toISOString(),
    bucketMs,
    totals,
    buckets,
    flows: perFlow
      .filter((f) => f.runs > 0)
      .map((f) => ({
        flowId: f.flowId,
        name: f.name,
        runs: f.runs,
        failed: f.failed,
        avgDurationMs: f.timed ? Math.round(f.dur / f.timed) : 0,
      }))
      .sort((a, b) => b.runs - a.runs),
    triggers: [...triggerCounts.values()]
      .filter((x) => x.runs > 0)
      .sort((a, b) => b.runs - a.runs)
      .slice(0, 8),
    recentFailures,
  };
}

export { evalGroup as _evalGroupForTests, simulate as _simulateForTests };
