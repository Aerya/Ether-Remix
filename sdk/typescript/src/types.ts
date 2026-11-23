/**
 * Ether Plugin Protocol v1 (EPP/1) types for @ether/plugin-sdk.
 * Mirrors internal/plugins/contract.go and internal/viewspec.
 */

export type MaybePromise<T> = T | Promise<T>;

export type PluginKind =
  | "source"
  | "integration"
  | "notification_channel"
  | "livetv_source"
  | "extension"
  | "content";

export type Capability =
  | "stream_source"
  | "debrid_provider"
  | "list_source"
  | "livetv_channels"
  | "subtitles"
  | "markers"
  | "notify"
  | "events"
  | "flow_actions"
  | "ranking_step"
  | "rule_condition"
  | "tools"
  | "ui"
  | "catalog_provider"
  | "kind_resolver"
  | "frontend_adapter"
  | "matcher"
  | "poster_badges"
  | "live_events";

export type LegacyEtherEventName =
  | "playback.started"
  | "playback.paused"
  | "playback.resumed"
  | "playback.stopped"
  | "playback.completed"
  | "playback.error"
  | "playback.failover"
  | "title.available"
  | "viewer.added"
  | "viewer.access_changed"
  | "collection.rebuilt"
  | "server.health_changed";

export type EtherEventName = LegacyEtherEventName | (string & {});

export type EventFieldType =
  | "string"
  | "int"
  | "float"
  | "bool"
  | "duration_ms"
  | "timestamp"
  | "enum"
  | "string_list"
  | "object";

export interface EventEnumOption {
  value: string;
  label?: string;
}

export interface EventFieldMeta {
  name: string;
  type: EventFieldType;
  label: string;
  description?: string;
  unit?: string;
  enum?: EventEnumOption[];
  pii?: boolean;
  secret?: boolean;
  filterable?: boolean;
  example?: unknown;
}

export interface PublishedEventMeta {
  key: string;
  label: string;
  description?: string;
  version?: number;
  fields?: EventFieldMeta[];
}

export interface FlowActionParamMeta {
  key: string;
  type: EventFieldType;
  label: string;
  description?: string;
  required?: boolean;
  secret?: boolean;
  template?: boolean;
  default?: unknown;
  enum?: EventEnumOption[];
  unit?: string;
  picker?: "channel" | "viewer" | "library" | "source" | "event" | "plugin";
}

export interface FlowActionMeta {
  key: string;
  label: string;
  description?: string;
  icon?: string;
  route: string;
  idempotent?: boolean;
  sideEffect?: boolean;
  timeout?: string;
  params?: FlowActionParamMeta[];
  outputs?: EventFieldMeta[];
}

export interface EventCorrelation {
  key?: string;
  sessionId?: string;
  playId?: string;
  titleId?: string;
  viewerId?: number;
  channelId?: string;
  sourceId?: string;
  pluginId?: string;
  deviceId?: string;
  libraryId?: string;
}

export interface FlowActionRequest {
  key: string;
  flowId: string;
  runId: string;
  nodeId: string;
  idempotencyKey: string;
  dryRun?: boolean;
  params: Record<string, unknown>;
}

export interface FlowActionResponse {
  output?: Record<string, unknown>;
  branch?: string;
}

export interface StremioCatalogDecl {
  type: string;
  id: string;
  name?: string;
  extra?: Array<{
    name: string;
    isRequired?: boolean;
    options?: string[];
    optionsLimit?: number;
  }>;
}

export interface StremioResourceDecl {
  name: string;
  types?: string[];
  idPrefixes?: string[];
}

export interface PluginAuthor {
  name?: string;
  url?: string;
}

/** Hosted/data requests are clamped to the owner's global or per-plugin ceilings. */
export interface RuntimeLimits {
  fetchBytes?: number;
  archiveMemberBytes?: number;
  archiveExpandedBytes?: number;
  archiveMembers?: number;
  maxItems?: number;
  memoryBytes?: number;
  readBytes?: number;
  deadline?: string;
  unitBudget?: number;
  /** Hosted WASM instance pool size (default 2; default owner ceiling 4, raisable up to 16). */
  poolSize?: number;
}
export interface RuntimeDecl {
  kind: "hosted" | "remote";
  entry?: string;
  module?: string;
  limits?: RuntimeLimits;
}

export interface HealthDecl {
  interval?: string;
}

export interface ScheduleEntry {
  every: string;
  call: string;
}

export interface ViewerPermissions {
  identity?: "none" | "granted_viewers" | "all";
  watch_history?: "none" | "granted_viewers" | "all";
  write_watch_state?: "none" | "granted_viewers";
}

export interface Permissions {
  network?: string[];
  catalog?: "none" | "read";
  viewers?: ViewerPermissions;
  collections?: "none" | "write";
  folders?: "none" | "read";
  secrets?: string[];
  /** Lets a kind resolver ask Ether to find bytes: search to look, acquire to open; add "+check" ("search+check" or "acquire+check") to also allow background usenet_check calls in jobs/schedules within the per-plugin daily allowance. */
  sources?: "none" | "search" | "search+check" | "acquire" | "acquire+check";
  /** Lets a plugin call ether.ai.complete on the owner's configured language model ("complete" for actions/jobs/sync/non-viewer routes; "complete+search" also allows bounded 20 s cached calls on interactive search routes). */
  ai?: "none" | "complete" | "complete+search";
  /** Lets a plugin probe container metadata via ether.media.probe from jobs, actions, and background sync (refused on viewer/client request paths). */
  media?: "none" | "probe";
}

export type SettingFieldKind =
  | "group"
  | "text"
  | "secret"
  | "duration"
  | "readonly"
  | "number"
  | "bool"
  | "select"
  | "list"
  | "bytes"
  | "hidden"
  | "multiSelect"
  | "orderedList"
  | "language"
  | "viewer"
  | "collection"
  | "folder"
  | "url"
  | "title";

export interface FieldOption {
  value: string;
  label: string;
  hint?: string;
}

export interface SettingField {
  key: string;
  kind: SettingFieldKind;
  label?: string;
  hint?: string;
  required?: boolean;
  default?: unknown;
  options?: FieldOption[];
  fields?: SettingField[];
  min?: number;
  max?: number;
  placeholder?: string;
  /** Search /ether/options/{key}?q=...; selected value persists verbatim. */
  search?: boolean;
}

export interface SettingsSchema {
  $ref?: string;
  fields?: SettingField[];
  groups?: Array<{
    key?: string;
    label?: string;
    hint?: string;
    advanced?: boolean;
    fields: SettingField[];
  }>;
}

export interface SourceMeta {
  class: "debrid" | "usenet" | "indexer" | "direct";
  abilities?: string[];
  limits?: Record<string, unknown>;
}

export interface ChannelsMeta {
  refresh?: string;
  test: true;
}

export interface SubtitlesMeta {
  languages?: "any" | string[];
  formats?: string[];
}

export type MarkerKind = "intro" | "credits" | "recap" | "preview";

export interface MarkersMeta {
  kinds: MarkerKind[];
}

export interface NotificationMeta {
  fields?: SettingField[] | SettingsSchema;
  sendTest: true;
  formats?: string[];
  maxLength?: number;
}

export interface EventsMeta {
  subscribe?: EtherEventName[];
  publish?: PublishedEventMeta[];
}

export interface IntegrationMeta {
  category: string;
  logo?: string;
  adds: string[];
  connect: "fields" | "device_code" | "none";
  test?: boolean;
}

export interface ListSourceMeta {
  key: string;
  label: string;
  hint?: string;
  catalog: string;
  params?: unknown;
  maxItems?: number;
  refresh?: string;
}

export interface RankingStepMeta {
  key: string;
  label: string;
  hint?: string;
  editor?: unknown;
  budget?: unknown;
}

export interface RuleConditionMeta {
  key: string;
  label: string;
  operators?: string[];
  editor?: unknown;
}

export interface PosterBadgeSetMeta {
  key: string;
  label: string;
  description?: string;
}

export interface BadgeTitleRef {
  type: "movie" | "show";
  tmdb?: number;
  imdb?: string;
}

export interface BadgeEntry {
  title: BadgeTitleRef;
  labels: string[];
}

export interface BadgeUsage {
  titles: number;
  quotaTitles: number;
}

export interface ToolMeta {
  name: string;
  title: string;
  description: string;
  inputSchema: Record<string, unknown>;
  readOnly?: boolean;
  destructive?: boolean;
  openWorld?: boolean;
}

export interface PageMeta {
  key: string;
  label: string;
  route: string;
  group?: string;
  icon?: string;
}

export type SlotKind =
  | "page"
  | "title.panel"
  | "viewer.tab"
  | "dashboard.panel"
  | "actionMenu.title"
  | "actionMenu.viewer"
  | "actionMenu.collection"
  | "actionMenu.release"
  | "command";

type ActionSlot =
  | "actionMenu.title"
  | "actionMenu.viewer"
  | "actionMenu.collection"
  | "actionMenu.release";
export type SlotMeta = { key: string; label: string } & (
  | { slot: ActionSlot; action: string; route?: never }
  | { slot: Exclude<SlotKind, ActionSlot>; route: string; action?: never }
);

export interface CommandMeta {
  key: string;
  label: string;
  action: string;
}

export interface Contributes {
  source?: SourceMeta;
  channels?: ChannelsMeta;
  subtitles?: SubtitlesMeta;
  markers?: MarkersMeta;
  notification?: NotificationMeta;
  events?: EventsMeta;
  flowActions?: FlowActionMeta[];
  integration?: IntegrationMeta;
  listSources?: ListSourceMeta[];
  rankingSteps?: RankingStepMeta[];
  ruleConditions?: RuleConditionMeta[];
  posterBadges?: PosterBadgeSetMeta[];
  tools?: ToolMeta[];
  pages?: PageMeta[];
  slots?: SlotMeta[];
  commands?: CommandMeta[];
  mediaKinds?: MediaKindMeta[];
  frontendAdapters?: FrontendAdapterMeta[];
  liveFeeds?: LiveFeedMeta[];
  setup?: SetupMeta;
  screens?: ScreenMeta[];
}

export type ScreenSectionType = "hero" | "events" | "table" | "schedule" | "leagues" | "entities";

export type ScreenEventState = "pre" | "in" | "post" | "delayed" | "postponed" | "canceled";

export type ScreenEventWindow = "today" | "week" | "recent";

export interface ScreenEventQuery {
  feed?: string;
  state?: ScreenEventState[];
  league?: string;
  favorites?: boolean;
  window?: ScreenEventWindow;
  category?: string;
}

export interface ScreenLeagueMeta {
  id: string;
  name: string;
  abbr?: string;
  logo?: string;
}

export interface ScreenSectionMeta {
  id?: string;
  type: ScreenSectionType;
  title?: string;
  feed?: string;
  league?: string;
  entityKind?: string;
  limit?: number;
  query?: ScreenEventQuery;
  leagues?: ScreenLeagueMeta[];
}

export interface ScreenMeta {
  id: string;
  title: string;
  icon?: string;
  feed?: string;
  sections: ScreenSectionMeta[];
  frontends?: string[];
}

export interface LiveFeedMeta {
  id: string;
  title: string;
  /** Minimum polling interval in seconds (>= 10). */
  minIntervalSec: number;
  entityKinds?: string[];
}

export type LiveEventState =
  | "pre"
  | "in"
  | "post"
  | "delayed"
  | "postponed"
  | "canceled";

export interface LiveEventLeague {
  id: string;
  name: string;
  abbr?: string;
  logo?: string;
}

export interface LiveEventStatus {
  state: LiveEventState;
  detail?: string;
  period?: number;
  clock?: string;
  close?: boolean;
}

export interface LiveEventCompetitor {
  id: string;
  name: string;
  short?: string;
  abbr?: string;
  logo?: string;
  color?: string;
  altColor?: string;
  score?: string;
  record?: string;
  rank?: number;
  home?: boolean;
  winner?: boolean;
  entityId?: string;
}

export interface LiveEventRankRow {
  pos: number;
  name: string;
  abbr?: string;
  detail?: string;
  secondary?: string;
  entityId?: string;
}

export interface LiveEventProbability {
  label?: string;
  value: number;
  competitorId?: string;
}

export interface LiveEventBroadcast {
  name: string;
  kind?: "tv" | "stream" | "radio";
  region?: string;
}

export interface LiveEventMatchHints {
  terms?: string[];
  callsigns?: string[];
}

export type LiveEventWatchTargetKind = "channel" | "addon" | "plugin";

/** Playable watch target attached by Ether; plugins do not emit it. */
export interface LiveEventWatchTarget {
  kind: LiveEventWatchTargetKind;
  channelId?: string;
  name: string;
  number?: string;
  logo?: string;
  live: boolean;
  confidence?: number;
  url?: string;
  /** Omitted when playable; false when the only matching channels are currently unhealthy. */
  available?: boolean;
}

export interface LiveEvent {
  id: string;
  feed: string;
  category?: string;
  league: LiveEventLeague;
  title?: string;
  /** RFC3339 timestamp. */
  start: string;
  status: LiveEventStatus;
  competitors: LiveEventCompetitor[];
  ranking?: LiveEventRankRow[];
  situation?: Record<string, string>;
  probability?: LiveEventProbability;
  venue?: string;
  broadcasts?: LiveEventBroadcast[];
  matchHints?: LiveEventMatchHints;
  /** Set by Ether; plugins leave omitted. */
  watch?: LiveEventWatchTarget[];
  /** Set by Ether; plugins leave omitted. */
  spoiler?: boolean;
  /** RFC3339 timestamp. */
  updatedAt?: string;
}

export interface LiveEventTable {
  title: string;
  columns: string[];
  rows: string[][];
}

export interface LiveEventPlay {
  clock?: string;
  period?: number;
  text: string;
  emphasis?: "score" | "key" | "normal";
  competitorId?: string;
}

export interface LiveEventProbabilityPoint {
  /** Either a 0..1 game fraction (preferred when known) or an RFC3339 timestamp. */
  t: number | string;
  value: number;
}

export interface LiveEventLink {
  label: string;
  url: string;
}

export interface LiveEventDetail extends LiveEvent {
  tables?: LiveEventTable[];
  plays?: LiveEventPlay[];
  probabilitySeries?: LiveEventProbabilityPoint[];
  links?: LiveEventLink[];
}

export interface LiveEventsResponse {
  events: LiveEvent[];
  nextPollSec?: number;
}

export interface LiveEventEntity {
  id: string;
  kind: string;
  name: string;
  abbr?: string;
  logo?: string;
  league?: string;
  color?: string;
}

export interface LiveEventEntitiesResponse {
  entities: LiveEventEntity[];
}

export interface LiveEventStandingsResponse {
  tables: LiveEventTable[];
}

export interface LiveEventsRequest {
  feed: string;
  from?: string;
  to?: string;
  league?: string;
  ids?: string[];
}

export interface LiveEventDetailRequest {
  feed: string;
  id: string;
}

export interface LiveEntitiesRequest {
  feed: string;
  kind?: string;
  league?: string;
  q?: string;
}

export interface LiveStandingsRequest {
  feed: string;
  league?: string;
}

/** A Jellyfin/Emby item type a level can present as. */
export type MediaClass = "video" | "music" | "audiobook" | "book" | "podcast" | "photo";

export type MediaRole =
  | "item"
  | "artist"
  | "album"
  | "track"
  | "series"
  | "book"
  | "audiobook"
  | "part"
  | "show"
  | "episode"
  | "collection";

export type MediaBrowserItemType =
  | "Movie" | "Series" | "Season" | "Episode" | "Video"
  | "MusicArtist" | "MusicAlbum" | "Audio" | "AudioBook"
  | "Book" | "PhotoAlbum" | "Photo";

/**
 * How a library is seen per protocol. Each app block overrides how that app
 * presents the library and may suggest `default: false` to start off. Omitted
 * apps are inferred by Ether from `media`, `levels[].role`, and level shape.
 */
export interface Presentations {
  adapterOnly?: boolean;
  vela?: { type?: "movies" | "shows"; default?: boolean };
  plex?: { base?: "movie" | "show" | "artist" | "photo"; default?: boolean };
  /**
   * Jellyfin/Emby. `itemTypes` maps each level key to its item type; any
   * unmapped level is inferred from the kind's media class, level roles, and
   * hierarchy shape.
   */
  mediaBrowser?: {
    collectionType?: string;
    itemTypes?: Record<string, MediaBrowserItemType>;
    default?: boolean;
  };
  stremio?: { type?: "movie" | "series" | "channel" | "tv"; default?: boolean };
  xtream?: { category?: string; default?: boolean };
  silo?: { type?: string; default?: boolean };
}

export interface KindLevel {
  key: string;
  label: string;
  /** Optional earlier level key; defaults to the previous level. */
  parent?: string;
  /** Optional media class override (defaults to the kind's media class). */
  media?: MediaClass;
  /** Free-text semantic role (e.g. "artist", "album", "track", "concert"). */
  role?: MediaRole | (string & {});
  playable?: boolean;
  container?: boolean;
}

export interface KindField {
  key: string;
  type: "string" | "int" | "float" | "bool" | "date" | "duration" | "enum" | "strings" | "people";
  label: string;
  indexed?: boolean;
  enum?: string[];
  values?: string[];
}

export interface KindArt {
  role: string;
  label: string;
}

export interface KindLibraryTabRule {
  field: string;
  op?: string;
  value?: unknown;
}

export interface KindLibraryTabFilter {
  match?: "all" | "any";
  rules?: KindLibraryTabRule[];
  exclude?: KindLibraryTabRule[];
}

export interface KindLibraryTab {
  key: string;
  label: string;
  kind: "recommended" | "library" | "collections" | "people" | "view" | "live";
  /** Optional declared hierarchy level key (library, view, collections or people tabs). */
  level?: string;
  /** Declared browse.facets entry (optional on collections; required people-type facet on people). */
  facet?: string;
  /** Bucket/person ordering: "count" (default on collections) or "name" (case-insensitive alphabetical, default on people). */
  order?: "count" | "name";
  /** Card aspect ratio on "live" (default "landscape") or "recommended" tabs. */
  card?: "poster" | "landscape" | "square";
  /** Presentation layout on "view" tabs: "grid" (default), "list", or "timeline". */
  layout?: "grid" | "list" | "timeline";
  /** Enables in-tab search on "collections", "people", "view", and "live" tabs (default true). */
  search?: boolean;
  /** Optional localizable search input placeholder (at most 60 characters; "$t:" supported) when search is enabled. */
  searchPlaceholder?: string;
  sort?: "title" | "added" | "released" | "year" | "rating" | "index" | "lastViewed";
  direction?: "asc" | "desc";
  /** Saved filter rules on "view" tabs (indexed fields and/or viewer-state rules). */
  filters?: KindLibraryTabFilter;
  /** Per-tab shelves on "recommended" tabs (overrides library.shelves when set). */
  shelves?: KindLibraryShelf[];
}

export interface KindLibraryShelf {
  /** Unique shelf key in this library, matching ^[a-z][a-z0-9-]{0,39}$. Stable row ID: "plugin:<viewID>:shelf:<key>". */
  key: string;
  /** Localizable shelf title (literal or "$t:<key>", at most 60 characters). */
  label: string;
  /** Optional declared hierarchy level key to populate the shelf from (defaults to the root level). */
  level?: string;
  /** Optional sort field (defaults to "released"). */
  sort?: "title" | "added" | "released" | "year" | "rating" | "index" | "lastViewed";
  /** Sort direction (defaults to "desc"). */
  direction?: "asc" | "desc";
  /** Single-facet filter shorthand; must name a declared and indexed field (or year/released/rating). */
  facet?: string;
  /** Single value for `facet`. */
  value?: string;
  /** OR values for `facet`. */
  values?: string[];
  /** Multi-facet map, TabFilter object, single rule, or rule array (including viewer-state rules); cannot be combined with `facet`. */
  filters?: Record<string, string[]> | KindLibraryTabFilter | KindLibraryTabRule | KindLibraryTabRule[];
}

export interface KindLibraryDecl {
  name: string;
  icon?: string;
  card?: "poster" | "landscape" | "square";
  /** How item release dates are rendered on cards and detail headers: "year" (default) or "date" (YYYY-MM-DD). */
  dateDisplay?: "year" | "date";
  /** Up to 50 sub-tabs shown inside the library; defaults to Recommended + Library. */
  tabs?: KindLibraryTab[];
  /** Up to 40 default Recommended shelves evaluated in order when present. */
  shelves?: KindLibraryShelf[];
}

/** On-demand expansion settings for declared browse facets and container levels. */
export interface KindBrowseExpand {
  /** Declared browse facets expanded on open (collection bucket or plugin person). */
  facets?: string[];
  /** Declared container levels (container: true) expanded when opened by a viewer. */
  levels?: string[];
  /** Freshness TTL in hours before an expanded slice or container may be refreshed (default 168). */
  ttlHours?: number;
}

/** One declared content kind; each kind becomes its own library. */
export interface MediaKindMeta {
  key: string;
  label: string;
  media?: MediaClass;
  library: KindLibraryDecl;
  levels: KindLevel[];
  fields?: KindField[];
  art?: KindArt[];
  present?: Presentations;
  browse: {
    sorts?: string[];
    facets?: string[];
    search: boolean;
    searchFields?: string[];
    /** Client searches also call /search live; results are upserted and merged. */
    liveSearch?: boolean;
    /** On-demand background slice expansion when a viewer opens a bucket or person. */
    expand?: KindBrowseExpand;
  };
  sync: { mode: "pull" | "push"; refresh?: string };
  cache?: { maxBytes?: number; ttl?: string };
  match?: { thresholdDefault?: number };
}

export interface FrontendAdapterMeta {
  key: string;
  label: string;
  protocolHint: "opds" | "subsonic" | "custom";
  pathPrefix: string;
  defaultPort?: number;
  clients?: string[];
  auth?: "ether_token" | "basic" | "none_lan";
  libraries?: string[];
  /** Optional adapter subpaths (such as "/search", "/rest/search3", or "/graphql") allowed to call ai.complete when permissions.ai is "complete+search". */
  searchPaths?: string[];
}

export interface SetupStep {
  key: string;
  label: string;
  hint?: string;
  fields: string[];
  test: string;
  required: boolean;
}

export interface SetupMeta {
  steps: SetupStep[];
}

/** The standardised plugin item envelope. */
export interface KindItem {
  unavailable?: { code: string; detail?: string };
  id: string;
  level: string;
  parentId?: string;
  index?: number;
  title: string;
  sortTitle?: string;
  summary?: string;
  year?: number;
  released?: number;
  rating?: number;
  playable?: boolean;
  childCount?: number;
  leafCount?: number;
  art?: Array<{ role: string; url: string; width?: number; height?: number }>;
  fields?: Record<string, unknown>;
  hints?: Record<string, unknown>;
  etag?: string;
}

export interface KindItemPart {
  index?: number;
  name?: string;
  mime?: string;
  size?: number;
  durationSec?: number;
  /** Optional zero-based file index inside a resolved multi-file release. */
  fileIndex?: number;
}

export interface KindPage {
  items: KindItem[];
  nextCursor?: string;
  total?: number;
}

export type KindBrowseTotalMode = "cheap" | "none" | "exact";

export interface KindBrowseRequest {
  kind: string;
  parentId?: string;
  level?: string;
  cursor?: string;
  limit?: number;
  sort?: string;
  desc?: boolean;
  /** Any of the listed values per field (OR within a field, AND across fields). */
  facets?: Record<string, string[]>;
  /** Every listed value per field must be present. */
  allFacets?: Record<string, string[]>;
  /** Items having any listed value per field are dropped. */
  excludeFacets?: Record<string, string[]>;
  query?: string;
  /**
   * Controls whether kinds.browse computes KindPage.total:
   * - "cheap" (default): return total only when O(1) (tallies, cached count, or single-page result)
   * - "none": never compute or return total
   * - "exact": compute an exact total (cached per library generation)
   */
  total?: KindBrowseTotalMode;
}

/** Query passed to kinds.browse from a frontend adapter (#615). */
export type KindBrowseQuery = Omit<KindBrowseRequest, "kind">;

export interface KindExpandRequest {
  kind: string;
  facet?: string;
  value?: string;
  valueId?: number;
  parentId?: string;
  level?: string;
  cursor?: string;
}

export interface KindExpandResponse {
  items?: KindItem[];
  cursor?: string;
  nextCursor?: string;
  job?: string;
}

export interface KindChangePage {
  upserts?: KindItem[];
  deletes?: string[];
  nextCursor?: string;
  complete: boolean;
}

export interface SourceQuery {
  kind:
    | "indexer_search"
    | "indexer_search_results"
    | "indexer_search_cancel"
    | "indexers"
    | "debrid_check"
    | "debrid_acquire"
    | "release_files"
    | "usenet_check";
  query?: string;
  /** Comma-separated Newznab category IDs or standard family names; with empty query, fetches recent releases. */
  category?: string;
  /** Quality profile name to score video release names against on indexer_search; empty uses default. */
  profile?: string;
  /** indexer_search only: when true, start the fan-out and return immediately with a searchId handle to poll via indexer_search_results or stop via indexer_search_cancel. */
  stream?: boolean;
  /** Streaming search handle returned by indexer_search with stream:true; required on indexer_search_results and indexer_search_cancel. */
  searchId?: string;
  /** indexer_search_results only: wait up to waitMs (capped at 10000 ms; 0 returns immediately) for at least one new arrival or completion. */
  waitMs?: number;
  /** indexer_search only: return once the fastest indexers produce results plus settleMs (default 250 ms), while slower indexers keep filling Ether's search cache. */
  firstResults?: boolean;
  /** indexer_search only: grace window in ms after the first matching result arrives (default 250 with firstResults; 0 returns immediately; capped at 30000). */
  settleMs?: number;
  /** indexer_search only: minimum deduplicated candidate count required before returning early. */
  minResults?: number;
  /** indexer_search only: indexer IDs or display names (case-insensitive) to exclude. Precedence over onlyIndexers. */
  skipIndexers?: string[];
  /** indexer_search only: restrict search to matching indexer IDs or display names (case-insensitive). */
  onlyIndexers?: string[];
  /** indexer_search only: per-call wait budget in ms, capped at Ether's 30000 ms ceiling (and any shorter parent deadline). */
  timeoutMs?: number;
  infoHashes?: string[];
  infoHash?: string;
  magnet?: string;
  /** Torrent download or details page link when an indexer did not supply infoHash or magnet. */
  torrentUrl?: string;
  /** Usenet NZB link for usenet_check when not passing a full candidate. */
  nzbUrl?: string;
  /** A candidate from indexer_search handed back for release_files or usenet_check, so an NZB or torrent link is resolved through its indexer. */
  candidate?: SourceCandidate;
  /** Optional torrent file count hint for release_files or debrid_acquire when passing a magnet or infoHash without a file list. */
  fileCount?: number;
}

export type IndexerSearchState = "answered" | "pending" | "timed_out" | "skipped" | "error";

export type IndexerKind = "newznab" | "torznab" | "builtin" | "stremio" | "zilean" | "addon";

/** Enabled indexer identity returned in the top-level pending list on early-returning and streaming search responses. */
export interface IndexerRef {
  indexerId: string;
  indexer: string;
}

/** Per-indexer search outcome entry (indexer_search / indexer_search_results / indexer_search_cancel) or enabled indexer summary (kind:"indexers"). */
export interface IndexerSearchStatus {
  indexerId: string;
  indexer: string;
  /** Outcome for this indexer on search/streaming queries ("pending" while still running in the background; "timed_out" when hitting the ceiling). */
  status?: IndexerSearchState;
  /** Raw release count returned by this indexer when status is "answered". */
  results?: number;
  /** Elapsed milliseconds until this indexer answered, failed, timed out, or the caller stopped waiting (0 for skipped). */
  ms?: number;
  /** Machine-readable skip reason when status is "skipped" automatically (for example "paused_backoff" while in circuit-breaker backoff after repeated failures/timeouts). */
  reason?: string;
  /** Sanitized short error code when status is "error". */
  error?: string;
  /** Indexer kind on kind:"indexers" responses. */
  kind?: IndexerKind | string;
  /** Median recent search duration in milliseconds on kind:"indexers" responses (omitted until searched). */
  medianMs?: number;
  /** Most recent search duration in milliseconds on kind:"indexers" responses (omitted until searched). */
  lastMs?: number;
  /** Sanitized short error code from the indexer's most recent failed search on kind:"indexers" responses. */
  lastError?: string;
}

/** Full response envelope from sources.resolve. */
export interface SourceResolveResult {
  candidates: SourceCandidate[];
  indexers?: IndexerSearchStatus[];
  /** Indexers still running in the background (early-returning indexer_search and streaming responses). */
  pending?: IndexerRef[];
  /** Streaming search handle returned on stream:true and echoed on indexer_search_results / indexer_search_cancel. */
  searchId?: string;
  /** True once every indexer in a streaming search has answered, failed, timed out, been skipped, or been cancelled. */
  done?: boolean;
}

/** One non-directory file inside an archive (RAR, 7z, ZIP) from a release_files query. */
export interface ReleaseMember {
  index: number;
  path: string;
  size: number;
  stored?: boolean;
}

/** Sampled Usenet article availability verdict for one NZB release (usenet_check). */
export interface UsenetCheckResult {
  available: boolean;
  checked: number;
  missing: number;
  /** True when served from Ether's bounded per-NZB cache (including a recorded playback failure). */
  cached?: boolean;
  /** True when a real playback attempt against this NZB failed with an article or release error. */
  playbackFailed?: boolean;
  /** Short diagnostic code when available is false (e.g. articles_missing, playback_article_missing). */
  reason?: string;
  /** Unix timestamp (seconds) when this verdict or playback failure was recorded. */
  checkedAt?: number;
}

/** One file inside a candidate release, from a release_files query. Index is the order Ether's file selection counts. */
export interface ReleaseFile {
  index: number;
  path: string;
  size: number;
  /** True when size is a segment-derived decoded estimate rather than an exact byte length. */
  sizeApproximate?: boolean;
  /** Inner non-directory files when this entry is the first volume of a readable Usenet archive. */
  members?: ReleaseMember[];
}

/** Structured track hint matched by disc/track number first, then normalised title tokens, failing on ambiguity. */
export interface TrackHint {
  disc?: number;
  number?: number;
  title?: string;
}

export interface DebridServiceRef {
  /** Configured debrid service ID in Ether. */
  id: string;
  /** Display name for the debrid service (owner account name or provider label). */
  name: string;
}

export interface SourceCandidate {
  infoHash?: string;
  magnet?: string;
  /** Key-less torrent download or details page link for torrent indexers (never placed in nzbUrl); Ether attaches same-origin indexer credentials at fetch time using indexerId. */
  torrentUrl?: string;
  /** Key-less Usenet NZB download link; Ether strips same-origin indexer API keys and re-attaches them at fetch time using indexerId so candidates are safe to cache in KV. */
  nzbUrl?: string;
  /** Zero-based index in the provider torrent file list. Omit for normal video selection. */
  fileIndex?: number;
  title?: string;
  size?: number;
  seeders?: number;
  cached?: boolean;
  /** Configured debrid services holding this torrent in acquisition order (or the service that served debrid_acquire); omitted when uncached. */
  cachedBy?: DebridServiceRef[];
  indexer?: string;
  indexerId?: string;
  /** Quality-profile score and parsed video facts; omitted on non-video candidates. */
  score?: number;
  resolution?: number;
  source?: string;
  codec?: string;
  /** Files inside this release; populated only by a release_files query. */
  files?: ReleaseFile[];
  /** Known file count of the torrent when reported by an indexer or release_files query. */
  fileCount?: number;
  /** Sampled Usenet article availability verdict; populated only by a usenet_check query. */
  usenetCheck?: UsenetCheckResult;
}

export interface KindMatchRequest {
  kind: string;
  item: KindItem;
  candidates: SourceCandidate[];
  settings?: Record<string, unknown>;
}

export interface KindMatchScore {
  candidateRef: string;
  score: number;
  reasons?: string[];
}

export interface PlayableSource {
  path?: string;
  id: string;
  label?: string;
  pattern: "range" | "download" | "direct" | "container" | "release" | "file";
  mime: string;
  /**
   * Optional quality-tier marker for interchangeable sources. Sources sharing
   * the same `tier` may be reordered by Ether's source routing (auto/usenet/
   * debrid, measured startup, and stall demotion) and may overtake each other
   * after the ranked open grace period. Distinct tiers keep the plugin's tier
   * order; omitting `tier` keeps the plugin's exact order.
   */
  tier?: number;
  size?: number;
  /** True when size is an approximate hint (e.g. from Usenet release_files) rather than an exact byte count. */
  sizeApproximate?: boolean;
  durationSec?: number;
  /**
   * Opts into open-time container duration verification when `durationSec > 0`:
   * Ether reads the container duration (reusing the durable `release_probes`
   * cache) and refuses the source as wrong content (trying the next candidate
   * and flagging the plugin answer) if the observed running time differs from
   * `durationSec` by more than this many seconds.
   */
  durationToleranceSec?: number;
  url?: string;
  headers?: Record<string, string>;
  /** Optional host or wildcard (`*.example.org`) patterns (within granted network hosts) that may keep credentials and headers across redirects. Never applies across HTTPS -> HTTP downgrades. */
  keepHeadersOnRedirect?: string[];
  blobRef?: string;
  sha256?: string;
  /** Hex digests checked against the served bytes (including a release file or container member); size is checked too. */
  sha1?: string;
  crc32?: string;
  maxBytes?: number;
  expiresAt?: number;
  container?: {
    format?: string;
    volumes: Array<{ url?: string; headers?: Record<string, string>; keepHeadersOnRedirect?: string[]; blobRef?: string; size: number }>;
    member?: string;
    memberIndex?: number;
    passwordSecret?: string;
  };
  release?: {
    nzbUrl?: string;
    magnet?: string;
    infoHash?: string;
    torrentUrl?: string;
    candidate?: SourceCandidate;
    /** Zero-based torrent file index; overrides candidate.fileIndex. Invalid selections fail. */
    fileIndex?: number;
    /** Exact torrent path or unique basename, including non-video files. */
    file?: string;
    /** Exact archive member path or unique basename when composing a release with container extraction. */
    fileHint?: string;
    /** Structured track hint ({disc?, number?, title?}) matched by disc/track number first, then title tokens. */
    track?: TrackHint;
    passwordSecret?: string;
    /** Require an already-cached debrid copy even when the owner turned "Cached torrents only" off. */
    cachedOnly?: boolean;
    /** Optional torrent file count hint when Ether has not seen the file list yet (e.g. a large collection magnet), so zip-only debrid caches (TorBox at 100+ files) are checked via cached-only add. */
    fileCount?: number;
  };
  parts?: KindItemPart[];
}

export interface KindResolveRequest {
  kind: string;
  libraryKey: string;
  item: KindItem;
  partIndex?: number;
  /** Optional PlayableSource.id chosen by a frontend adapter stream ticket (#661). */
  source?: string;
  client?: Record<string, unknown>;
}

/** Options for minting a scoped frontend adapter stream URL (#576, #661). */
export interface StreamUrlOptions {
  partIndex?: number;
  source?: string;
}

export interface PendingReply { pending: true; retryInMs?: number; job?: string; sources?: never; matches?: never }

export interface KindResolveResponse {
  pending?: boolean;
  retryInMs?: number;
  job?: string;
  sources: PlayableSource[];
  parts?: KindItemPart[];
  ttlSec?: number;
}

export interface KindLiveRequest {
  kind: string;
  tab?: string;
  q?: string;
  filter?: string;
  cursor?: string;
  limit?: number;
}

export interface KindLiveItem {
  id: string;
  title: string;
  subtitle?: string;
  badge?: string;
  viewers?: number;
  tags?: string[];
  thumb?: string;
  live?: boolean;
}

export interface KindLiveFilter {
  key: string;
  label: string;
}

export interface KindLivePage {
  items: KindLiveItem[];
  filters?: KindLiveFilter[];
  nextCursor?: string;
}

export interface KindLiveResolveRequest {
  kind: string;
  id: string;
}

export interface KindLiveResolveResponse {
  url: string;
  headers?: Record<string, string>;
  mime?: string;
  title?: string;
}

export interface SetupTestRequest {
  step: string;
  fields?: Record<string, unknown>;
}

export interface SetupTestResult {
  ok: boolean;
  detail?: string;
  fields?: Record<string, string>;
}

export interface EtherManifestBlock {
  protocol: 1;
  kind: PluginKind;
  capabilities?: Capability[];
  summary: string;
  author?: PluginAuthor;
  license?: string;
  repo?: string;
  icon?: string;
  runtime: RuntimeDecl;
  cluster?: "leader" | "every_node";
  health?: HealthDecl;
  permissions?: Permissions;
  settings?: SettingsSchema | Record<string, unknown>;
  strings?: Record<string, string | Record<string, string>>;
  schedule?: ScheduleEntry[];
  contributes?: Contributes;
}

export interface Manifest {
  id: string;
  version: string;
  name: string;
  description?: string;
  logo?: string;
  background?: string;
  types?: string[];
  catalogs?: StremioCatalogDecl[];
  resources?: Array<string | StremioResourceDecl>;
  idPrefixes?: string[];
  behaviorHints?: {
    adult?: boolean;
    p2p?: boolean;
    configurable?: boolean;
    configurationRequired?: boolean;
  };
  ether?: EtherManifestBlock;
}

// --- Route request and response payloads ---

export interface CatalogItem {
  id: string;
  type: string;
  name: string;
  poster?: string;
  posterShape?: "poster" | "landscape" | "square";
  background?: string;
  logo?: string;
  description?: string;
  releaseInfo?: string;
  imdbRating?: string;
  genres?: string[];
}

export interface CatalogRequest {
  type: string;
  id: string;
  extra: Record<string, string>;
  skip: number;
  search?: string;
  genre?: string;
}

export interface CatalogResponse {
  metas: CatalogItem[];
}

export interface MetaVideo {
  id: string;
  title: string;
  released?: string;
  season?: number;
  episode?: number;
  overview?: string;
  thumbnail?: string;
}

export interface MetaItem extends CatalogItem {
  runtime?: string;
  cast?: string[];
  director?: string[];
  videos?: MetaVideo[];
}

export interface MetaRequest {
  type: string;
  id: string;
}

export interface MetaResponse {
  meta: MetaItem;
}

export interface StreamEtherExt {
  id?: string;
  releaseName?: string;
  size?: number;
  languages?: string[];
  lazy?: boolean;
  file?: string;
}

export interface StreamItem {
  name?: string;
  title?: string;
  description?: string;
  url?: string;
  infoHash?: string;
  fileIdx?: number;
  ytId?: string;
  externalUrl?: string;
  behaviorHints?: {
    notWebReady?: boolean;
    bingeGroup?: string;
    countryWhitelist?: string[];
    filename?: string;
    videoSize?: number;
    videoHash?: string;
    proxyHeaders?: {
      request?: Record<string, string>;
      response?: Record<string, string>;
    };
  };
  ether?: StreamEtherExt;
}

export interface StreamRequest {
  type: string;
  id: string;
  extra: Record<string, string>;
}

export interface StreamResponse {
  streams: StreamItem[];
}

export interface OpenStreamRequest {
  streamId: string;
}

export interface OpenStreamResponse {
  url?: string;
  headers?: Record<string, string>;
  expiresAt?: string;
  file?: string;
}

export interface SubtitleItem {
  id: string;
  url: string;
  lang: string;
  label?: string;
  format?: string;
  hearingImpaired?: boolean;
}

export interface SubtitlesRequest {
  type: string;
  id: string;
  extra: Record<string, string>;
  pickToken?: string;
  videoHash?: string;
  videoSize?: number;
  filename?: string;
}

export interface SubtitlesResponse {
  subtitles: SubtitleItem[];
}

export interface MarkerItem {
  kind: MarkerKind;
  start: number;
  end: number;
  label?: string;
}

export interface MarkersRequest {
  type: string;
  id: string;
}

export interface MarkersResponse {
  markers: MarkerItem[];
}

export interface ChannelInput {
  url: string;
  name?: string;
  headers?: Record<string, string>;
  priority?: number;
}

export interface ChannelItem {
  id: string;
  name: string;
  number?: number;
  group?: string;
  logo?: string;
  guideId?: string;
  inputs: ChannelInput[];
}

export interface ChannelsResponse {
  channels: ChannelItem[];
}

export interface GuideRequest {
  channelId: string;
  from?: string;
  to?: string;
}

export interface ProgrammeItem {
  id?: string;
  channelId?: string;
  title: string;
  subtitle?: string;
  description?: string;
  start: string;
  stop: string;
  icon?: string;
  categories?: string[];
}

export interface GuideResponse {
  programmes: ProgrammeItem[];
}

export interface ConfigureResponse {
  errors?: Record<string, string>;
}

export interface TestResponse {
  ok: boolean;
  message?: string;
  details?: string[];
}

export type HealthStatus = "ok" | "attention" | "error";

export interface HealthResponse {
  status: HealthStatus;
  reason?: string;
}

export interface ViewSpecFieldRef {
  $: string;
}

export type ViewSpecValue = string | ViewSpecFieldRef;

export type ViewSpecBlock =
  | {
      kind: "page-header";
      title: ViewSpecValue;
      description?: ViewSpecValue;
      back?: { href: ViewSpecValue; label: ViewSpecValue };
      actions?: Array<{ widget: string; props?: Record<string, unknown> }>;
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "stat-tile";
      iconKey: ViewSpecValue;
      accent: ViewSpecValue;
      label: ViewSpecValue;
      value: ViewSpecValue;
      sub?: ViewSpecValue;
      tone?: "default" | "positive" | "warning" | "negative" | ViewSpecFieldRef;
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "panel";
      title: ViewSpecValue;
      iconKey?: ViewSpecValue;
      hint?: ViewSpecValue;
      blocks: ViewSpecBlock[];
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "text";
      content: ViewSpecValue;
      tone?:
        | "default"
        | "negative"
        | "positive"
        | "warning"
        | "muted"
        | "strong"
        | ViewSpecFieldRef;
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "heading";
      level: 2 | 3;
      content: ViewSpecValue;
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "grid";
      as?: "div" | "section";
      blocks: ViewSpecBlock[];
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "widget";
      widget:
        | "ether.posterGrid"
        | "ether.posterShelf"
        | "ether.titleCard"
        | "ether.liveChart"
        | "ether.settingsForm"
        | "ether.actionButton"
        | "ether.titleMatch"
        | "ether.healthStrip"
        | "ether.channelRow"
        | "ether.badge";
      props?: Record<string, unknown>;
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "frame";
      frame: "ether.section" | "ether.tabs" | "ether.drawer";
      props?: Record<string, unknown>;
      blocks: ViewSpecBlock[];
      when?: ViewSpecFieldRef;
    }
  | {
      kind: "repeat";
      items: ViewSpecFieldRef;
      itemKey: ViewSpecFieldRef;
      unwrapped?: boolean;
      empty?: { text: ViewSpecValue };
      blocks: ViewSpecBlock[];
      when?: ViewSpecFieldRef;
    }
  | Record<string, unknown>;

export interface ViewSpecDocument {
  specVersion: 1;
  layout: "list" | "detail" | "bare";
  route?: string;
  header: ViewSpecBlock[];
  body: ViewSpecBlock[];
}

export interface ViewRequest {
  page: string;
  query: Record<string, string>;
}

export interface ViewResponse {
  spec: ViewSpecDocument;
  data: Record<string, unknown>;
}

export interface ActionProposal {
  category: string;
  description: string;
  input?: Record<string, unknown>;
}

export interface ActionRequest<P = Record<string, unknown>> {
  action: string;
  input: P;
}

export interface ActionResponse {
  toast?: string;
  refresh?: boolean;
  open?: string;
  proposal?: ActionProposal;
}

export interface EtherEvent<P = Record<string, unknown>> {
  id: string;
  name: EtherEventName | string;
  at: string;
  viewerId?: number;
  payload?: P;
}

export interface EventResponse {
  ack: string[];
}

export interface ToolRequest<I = Record<string, unknown>> {
  name: string;
  input: I;
}

export type ToolResponse = Record<string, unknown>;

export interface JobRequest {
  name: string;
  continuation?: unknown;
}

export interface JobResponse {
  /** Presence asks Ether to call again after 0..300000 ms (minimum pause 100ms). */
  continueInMs?: number;
  /** Opaque JSON checkpoint forwarded as the next request's continuation. */
  continuation?: unknown;
  progress?: string;
  done?: boolean;
  failed?: string;
  ok?: boolean;
  [key: string]: unknown;
}

export interface NotifyRequest {
  title?: string;
  message: string;
  event?: string;
  settings?: Record<string, unknown>;
}

export interface NotifyResponse {
  ok: boolean;
  message?: string;
}

// --- Hot-path types (SPEC §5.2 & §5.8) ---

export interface ClientCaps {
  maxHeight?: number;
  hdr?: string[];
  codecs?: string[];
}

export interface CandidateFacts {
  releaseId: string;
  name: string;
  size?: number;
  protocol?: "usenet" | "torrent" | "direct" | string;
  sourceClass?: string;
  indexer?: string;
  resolution?: string | number;
  hdr?: string[];
  videoCodec?: string;
  audioCodecs?: string[];
  audioChannels?: string;
  languages?: string[];
  subtitles?: string[];
  releaseType?: string;
  group?: string;
  cached?: boolean;
  verified?: boolean;
  clientCaps?: ClientCaps;
}

export type RankKeyFn<C = Record<string, unknown>> = (
  candidate: CandidateFacts,
  config: C,
) => number;

export type CondMatchFn<V = unknown> = (
  candidate: CandidateFacts,
  operator: string,
  value: V,
) => boolean;

// --- Low-level ABI request/response ---

export interface RawRequest {
  pluginId?: string;
  method?: string;
  path: string;
  query?: Record<string, string | string[]>;
  headers?: Record<string, string | string[]>;
  body?: unknown;
  /** Exact request bytes, base64-encoded (frontend adapter routes). A non-JSON body arrives only here. */
  bodyBase64?: string;
  contentType?: string;
  locale?: string;
}

/** Host-served cached bytes for an authenticated frontend adapter response. */
export interface FrontendBlob {
  libraryKey: string;
  ref: string;
  memberIndex?: number;
}

export interface RawResponse {
  blob?: FrontendBlob;
  status: number;
  headers?: Record<string, string>;
  body?: unknown;
}

export class PluginHttpError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "PluginHttpError";
    this.status = status;
    this.code = code;
  }
}
