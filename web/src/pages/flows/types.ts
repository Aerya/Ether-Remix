import { t } from "../../i18n";

export type FieldType =
  | "string"
  | "int"
  | "float"
  | "bool"
  | "duration_ms"
  | "timestamp"
  | "enum"
  | "string_list"
  | "object";

export interface EnumOption {
  value: string;
  label?: string;
}

export interface FieldSchema {
  name: string;
  type: FieldType;
  label: string;
  description?: string;
  unit?: string;
  enum?: EnumOption[];
  pii?: boolean;
  secret?: boolean;
  filterable?: boolean;
  example?: unknown;
}

export interface EventSchema {
  type: string;
  version: number;
  domain: string;
  label: string;
  description?: string;
  owner: string;
  critical?: boolean;
  deprecated?: boolean;
  replacedBy?: string;
  legacyNames?: string[];
  fields: FieldSchema[];
  sample?: Record<string, unknown>;
}

/**
 * Canonical console editor picker IDs for ParamSchema.picker.
 * Keep in sync with Picker* constants in internal/flows/registry.go and
 * FlowActionParamMeta.picker in sdk/typescript/src/types.ts
 * (enforced by TestPickerConstantsMatchWeb in internal/flows).
 */
export const FLOW_PICKER = {
  channel: "channel",
  viewer: "viewer",
  library: "library",
  source: "source",
  event: "event",
  plugin: "plugin",
} as const;

export type FlowPicker = (typeof FLOW_PICKER)[keyof typeof FLOW_PICKER];

export interface ParamSchema {
  key: string;
  type: FieldType;
  label: string;
  description?: string;
  required?: boolean;
  secret?: boolean;
  template?: boolean;
  default?: unknown;
  enum?: EnumOption[];
  unit?: string;
  picker?: FlowPicker;
}

export interface ActionSpec {
  type: string;
  owner: string;
  group: string;
  label: string;
  description?: string;
  icon?: string;
  idempotent?: boolean;
  sideEffect?: boolean;
  params?: ParamSchema[];
  outputs?: FieldSchema[];
}

export type TriggerNodeKind =
  "trigger.event" | "trigger.schedule" | "trigger.webhook" | "trigger.manual";

export type LogicNodeKind =
  "logic.switch" | "logic.delay" | "logic.wait_event" | "logic.window" | "logic.dedupe";

export type NodeKind = TriggerNodeKind | "condition" | LogicNodeKind | "action";

export type NodeCategory = "trigger" | "condition" | "logic" | "action";

export type LogicOp = "and" | "or";

export type Operator =
  | "eq"
  | "neq"
  | "gt"
  | "gte"
  | "lt"
  | "lte"
  | "contains"
  | "not_contains"
  | "starts_with"
  | "ends_with"
  | "matches"
  | "in"
  | "not_in"
  | "exists"
  | "not_exists"
  | "is_true"
  | "is_false"
  | "contains_any"
  | "contains_all";

export interface ConditionRule {
  field: string;
  op: Operator;
  value?: unknown;
  caseSensitive?: boolean;
}

export interface ConditionGroup {
  op?: LogicOp;
  negate?: boolean;
  rules?: ConditionRule[];
  groups?: ConditionGroup[];
}

export interface Position {
  x: number;
  y: number;
}

export interface FlowNode {
  id: string;
  kind: NodeKind;
  label?: string;
  position: Position;
  config?: Record<string, unknown>;
  disabled?: boolean;
}

export interface FlowEdge {
  id: string;
  source: string;
  target: string;
  sourceHandle?: string;
}

export interface Graph {
  schemaVersion: number;
  nodes: FlowNode[];
  edges: FlowEdge[];
}

export interface FlowLimits {
  rateLimitPerMin?: number;
  maxSteps?: number;
  concurrency?: number;
  orderedByKey?: boolean;
  autoDisableAfter?: number;
  maxRunDurationSec?: number;
}

export type RunStatus =
  "pending" | "running" | "waiting" | "completed" | "failed" | "cancelled" | "throttled";

export type StepStatus = "pending" | "running" | "completed" | "failed" | "skipped" | "uncertain";

export interface Flow {
  id: string;
  name: string;
  description?: string;
  enabled: boolean;
  disabledReason?: string;
  version: number;
  graph: Graph;
  limits?: FlowLimits;
  consecutiveFailures?: number;
  templateId?: string;
  tags?: string[];
  createdAt: string;
  updatedAt: string;
  lastRunAt?: string;
  lastStatus?: RunStatus;
  runs24h?: number;
  failures24h?: number;
  activeRuns?: number;
  triggerSummary?: string;
  webhookUrl?: string;
  webhookConfigured?: boolean;
}

/**
 * One row of GET /api/flows. The list carries no graph (that stays on
 * GET /api/flows/{id}); `trigger` names what starts the flow.
 */
export interface FlowSummary {
  id: string;
  name: string;
  description?: string;
  enabled: boolean;
  disabledReason?: string;
  version: number;
  tags?: string[];
  templateId?: string;
  consecutiveFailures?: number;
  trigger?: { kinds: NodeKind[]; events?: string[] };
  lastRunAt?: string;
  lastStatus?: RunStatus;
  runs24h?: number;
  failures24h?: number;
  activeRuns?: number;
  createdAt: string;
  updatedAt: string;
}

/** Aggregates for the Flows overview (GET /api/flows/stats). */
export type StatsRange = "24h" | "7d" | "30d";

export interface RunStatsBucket {
  start: string;
  succeeded: number;
  failed: number;
  skipped: number;
  active: number;
  avgDurationMs: number;
}

export interface RunStats {
  since: string;
  bucketMs: number;
  totals: {
    runs: number;
    succeeded: number;
    failed: number;
    skipped: number;
    active: number;
    avgDurationMs: number;
  };
  buckets: RunStatsBucket[];
  flows: { flowId: string; name: string; runs: number; failed: number; avgDurationMs: number }[];
  triggers: { kind: NodeKind; eventType?: string; runs: number }[];
  recentFailures: Run[];
}

export interface EventTriggerConfig {
  events: string[];
  filter?: ConditionGroup;
}

export interface ScheduleTriggerConfig {
  cron?: string;
  every?: string;
  timezone?: string;
}

export interface WebhookTriggerConfig {
  secretHeader?: string;
  requireAuth?: boolean;
  allowedMethods?: string[];
}

export interface ManualTriggerConfig {
  inputs?: ParamSchema[];
  sampleEventType?: string;
}

export interface ConditionNodeConfig {
  condition: ConditionGroup;
}

export interface SwitchCase {
  key: string;
  label?: string;
  condition: ConditionGroup;
}

export interface SwitchNodeConfig {
  cases: SwitchCase[];
  matchAll?: boolean;
}

export interface DelayNodeConfig {
  duration?: string;
  durationField?: string;
  until?: string;
}

export interface WaitEventNodeConfig {
  eventTypes: string[];
  matchCorrelation?: boolean;
  filter?: ConditionGroup;
  timeout: string;
}

export interface WindowNodeConfig {
  keyTemplate?: string;
  threshold: number;
  window: string;
  resetOnReach?: boolean;
}

export interface DedupeNodeConfig {
  keyTemplate?: string;
  cooldown: string;
  maxPerWindow?: number;
}

export interface ActionNodeConfig {
  actionType: string;
  params?: Record<string, unknown>;
  continueOnError?: boolean;
  idempotencyKey?: string;
  timeout?: string;
}

export interface ValidationIssue {
  code: string;
  path?: string;
  nodeId?: string;
  edgeId?: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors?: ValidationIssue[];
  warnings?: ValidationIssue[];
}

export interface RuleTrace {
  field: string;
  op: Operator;
  expected?: unknown;
  actual?: unknown;
  found: boolean;
  matched: boolean;
}

export interface ConditionTrace {
  op: LogicOp;
  negate?: boolean;
  matched: boolean;
  rules?: RuleTrace[];
  groups?: ConditionTrace[];
}

export interface Run {
  id: string;
  flowId: string;
  flowName?: string;
  flowVersion: number;
  triggerKind: NodeKind;
  eventId?: string;
  eventType?: string;
  correlationKey?: string;
  status: RunStatus;
  attempt: number;
  dryRun?: boolean;
  trigger?: Record<string, unknown>;
  context?: Record<string, unknown>;
  error?: string;
  stepCount: number;
  flowDepth?: number;
  startedAt: string;
  updatedAt: string;
  finishedAt?: string;
  wakeAt?: string;
}

export interface RunStep {
  id: number;
  runId: string;
  seq: number;
  nodeId: string;
  nodeKind: NodeKind;
  status: StepStatus;
  idempotencyKey?: string;
  branch?: string;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  error?: string;
  startedAt: string;
  finishedAt?: string;
  durationMs: number;
}

export interface TestRunRequest {
  flowId?: string;
  graph?: Graph;
  dryRun: boolean;
  event?: {
    type: string;
    payload?: Record<string, unknown>;
    correlation?: Record<string, unknown>;
  };
  inputs?: Record<string, unknown>;
  trigger?: NodeKind;
}

export interface TestRunResult {
  run: Run;
  steps: RunStep[];
}

export interface FlowTemplate {
  id: string;
  name: string;
  description: string;
  category: string;
  icon?: string;
  notifyPreset?: string;
  params?: ParamSchema[];
  graph: Graph;
  limits?: FlowLimits;
}

export interface OutputHandleSpec {
  id: string;
  label: string;
  tone: "primary" | "success" | "muted" | "warning" | "danger" | "violet";
}

export function isTriggerKind(kind: NodeKind): kind is TriggerNodeKind {
  return (
    kind === "trigger.event" ||
    kind === "trigger.schedule" ||
    kind === "trigger.webhook" ||
    kind === "trigger.manual"
  );
}

export function nodeCategory(kind: NodeKind): NodeCategory {
  if (isTriggerKind(kind)) return "trigger";
  if (kind === "condition") return "condition";
  if (kind === "action") return "action";
  return "logic";
}

export function nodeKindLabel(kind: NodeKind): string {
  switch (kind) {
    case "trigger.event":
      return t("Event trigger");
    case "trigger.schedule":
      return t("Schedule");
    case "trigger.webhook":
      return t("Incoming webhook");
    case "trigger.manual":
      return t("Manual trigger");
    case "condition":
      return t("Condition");
    case "logic.switch":
      return t("Switch branches");
    case "logic.delay":
      return t("Delay");
    case "logic.wait_event":
      return t("Wait for event");
    case "logic.window":
      return t("Count in window");
    case "logic.dedupe":
      return t("Cooldown / dedupe");
    case "action":
      return t("Action");
  }
}

export function outputHandlesForNode(node: FlowNode): OutputHandleSpec[] {
  switch (node.kind) {
    case "trigger.event":
    case "trigger.schedule":
    case "trigger.webhook":
    case "trigger.manual":
    case "logic.delay":
      return [{ id: "next", label: t("Next"), tone: "primary" }];
    case "condition":
      return [
        { id: "true", label: t("Matches"), tone: "success" },
        { id: "false", label: t("Otherwise"), tone: "muted" },
      ];
    case "logic.switch": {
      const cfg = (node.config ?? {}) as Partial<SwitchNodeConfig>;
      const cases = Array.isArray(cfg.cases) ? cfg.cases : [];
      const handles: OutputHandleSpec[] = cases.map((c) => ({
        id: c.key,
        label: c.label || c.key,
        tone: "violet",
      }));
      handles.push({ id: "default", label: t("Default"), tone: "muted" });
      return handles;
    }
    case "logic.wait_event":
      return [
        { id: "matched", label: t("Matched"), tone: "success" },
        { id: "timeout", label: t("Timed out"), tone: "warning" },
      ];
    case "logic.window":
      return [
        { id: "reached", label: t("Reached"), tone: "warning" },
        { id: "below", label: t("Below"), tone: "muted" },
      ];
    case "logic.dedupe":
      return [
        { id: "pass", label: t("Allow"), tone: "success" },
        { id: "suppressed", label: t("Cooldown"), tone: "muted" },
      ];
    case "action":
      return [
        { id: "next", label: t("Next"), tone: "primary" },
        { id: "error", label: t("On error"), tone: "danger" },
      ];
  }
}

export function defaultHandleForNode(node: FlowNode): string {
  return outputHandlesForNode(node)[0]?.id ?? "next";
}

export function handleToneColor(tone: OutputHandleSpec["tone"]): string {
  switch (tone) {
    case "success":
      return "var(--color-success)";
    case "warning":
      return "var(--color-warning)";
    case "danger":
      return "var(--color-danger)";
    case "violet":
      return "var(--color-viz-2)";
    case "muted":
      return "var(--color-fg-subtle)";
    case "primary":
    default:
      return "var(--color-primary)";
  }
}

/** Operators allowed for each schema FieldType (mirrors internal/flows/conditions.go). */
export function operatorsForFieldType(ft: FieldType): Operator[] {
  switch (ft) {
    case "string":
      return [
        "eq",
        "neq",
        "contains",
        "not_contains",
        "starts_with",
        "ends_with",
        "matches",
        "in",
        "not_in",
        "exists",
        "not_exists",
      ];
    case "enum":
      return ["eq", "neq", "in", "not_in", "exists", "not_exists"];
    case "int":
    case "float":
    case "duration_ms":
    case "timestamp":
      return ["eq", "neq", "gt", "gte", "lt", "lte", "in", "not_in", "exists", "not_exists"];
    case "bool":
      return ["is_true", "is_false", "eq", "neq", "exists", "not_exists"];
    case "string_list":
      return ["contains", "not_contains", "contains_any", "contains_all", "exists", "not_exists"];
    case "object":
      return ["exists", "not_exists"];
    default:
      return ["eq", "neq", "exists", "not_exists"];
  }
}

export function operatorRequiresValue(op: Operator): boolean {
  return op !== "exists" && op !== "not_exists" && op !== "is_true" && op !== "is_false";
}

export function operatorLabel(op: Operator): string {
  switch (op) {
    case "eq":
      return t("is");
    case "neq":
      return t("is not");
    case "gt":
      return t("is greater than");
    case "gte":
      return t("is at least");
    case "lt":
      return t("is less than");
    case "lte":
      return t("is at most");
    case "contains":
      return t("contains");
    case "not_contains":
      return t("does not contain");
    case "starts_with":
      return t("starts with");
    case "ends_with":
      return t("ends with");
    case "matches":
      return t("matches pattern");
    case "in":
      return t("is one of");
    case "not_in":
      return t("is none of");
    case "exists":
      return t("is set");
    case "not_exists":
      return t("is empty");
    case "is_true":
      return t("is true");
    case "is_false":
      return t("is false");
    case "contains_any":
      return t("contains any of");
    case "contains_all":
      return t("contains all of");
  }
}

export function domainLabel(domain: string): string {
  if (domain.startsWith("plugin:")) {
    return t("Plugin · {id}", { id: domain.slice("plugin:".length) });
  }
  switch (domain) {
    case "playback":
      return t("Playback");
    case "livetv":
      return t("Live TV");
    case "resolver":
      return t("Releases & pins");
    case "indexer":
      return t("Indexers");
    case "provider":
      return t("Providers");
    case "vault":
      return t("Vault");
    case "cache":
      return t("Cache");
    case "library":
      return t("Library");
    case "viewer":
      return t("Viewers");
    case "auth":
      return t("Auth & devices");
    case "plugin":
      return t("Plugins");
    case "cluster":
      return t("Cluster");
    case "system":
      return t("System");
    case "flows":
      return t("Flows");
    default:
      return domain;
  }
}

export function actionGroupLabel(group: string): string {
  switch (group) {
    case "notifications":
      return t("Notifications");
    case "playback":
      return t("Playback");
    case "home":
      return t("Smart home");
    case "viewers":
      return t("Viewers");
    case "library":
      return t("Library");
    case "sources":
      return t("Sources");
    case "resolver":
      return t("Releases & pins");
    case "http":
      return t("HTTP & webhooks");
    case "flow":
      return t("Flow variables");
    case "plugin":
      return t("Plugins");
    default:
      return group;
  }
}

export function matchEventPattern(pattern: string, eventType: string): boolean {
  if (!pattern || !eventType) return false;
  if (pattern === "*") return true;
  if (pattern.endsWith(".*")) return eventType.startsWith(pattern.slice(0, -1));
  if (pattern.endsWith("/*")) return eventType.startsWith(pattern.slice(0, -1));
  if (pattern.endsWith(".") || pattern.endsWith("/")) return eventType.startsWith(pattern);
  return pattern === eventType;
}

/** Resolve all event schemas that flow into a given node from upstream triggers or wait_event nodes. */
export function upstreamEventSchemas(
  graph: Graph,
  nodeId: string,
  allEvents: EventSchema[],
): EventSchema[] {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const incoming = new Map<string, string[]>();
  for (const e of graph.edges) {
    const list = incoming.get(e.target) ?? [];
    list.push(e.source);
    incoming.set(e.target, list);
  }
  const visited = new Set<string>([nodeId]);
  const queue = [nodeId];
  const patterns = new Set<string>();

  while (queue.length > 0) {
    const cur = queue.shift()!;
    const n = byId.get(cur);
    if (n) {
      if (n.kind === "trigger.event") {
        const evs = (n.config as Partial<EventTriggerConfig> | undefined)?.events ?? [];
        for (const p of evs) patterns.add(p);
      } else if (n.kind === "trigger.manual") {
        const sample = (n.config as Partial<ManualTriggerConfig> | undefined)?.sampleEventType;
        if (sample) patterns.add(sample);
      } else if (n.kind === "logic.wait_event" && cur !== nodeId) {
        const evs = (n.config as Partial<WaitEventNodeConfig> | undefined)?.eventTypes ?? [];
        for (const p of evs) patterns.add(p);
      }
    }
    for (const prev of incoming.get(cur) ?? []) {
      if (!visited.has(prev)) {
        visited.add(prev);
        queue.push(prev);
      }
    }
  }

  // Fallback: if this node is currently disconnected, include all trigger.event nodes in the graph
  // so condition/template editing stays immediately helpful while wiring.
  if (patterns.size === 0) {
    for (const n of graph.nodes) {
      if (n.kind === "trigger.event") {
        const evs = (n.config as Partial<EventTriggerConfig> | undefined)?.events ?? [];
        for (const p of evs) patterns.add(p);
      }
    }
  }

  const matched: EventSchema[] = [];
  const seen = new Set<string>();
  for (const p of patterns) {
    for (const ev of allEvents) {
      if (matchEventPattern(p, ev.type) && !seen.has(ev.type)) {
        seen.add(ev.type);
        matched.push(ev);
      }
    }
  }
  return matched;
}

export interface PickerFieldGroup {
  id: string;
  label: string;
  fields: (FieldSchema & { path: string })[];
}

function fieldSubgroupLabel(prefix: string): string {
  switch (prefix) {
    case "source":
      return t("Source media");
    case "stream":
      return t("Active stream");
    case "viewer":
      return t("Viewer");
    case "client":
      return t("Client app");
    case "network":
      return t("Network");
    case "server":
      return t("Server counts");
    default:
      return t("Event payload");
  }
}

/** Group fields from upstream event schemas + event metadata + correlation + prior steps for pickers. */
export function buildFieldGroups(
  graph: Graph,
  nodeId: string,
  allEvents: EventSchema[],
  allActions: ActionSpec[],
  options?: { filterableOnly?: boolean },
): PickerFieldGroup[] {
  const schemas = upstreamEventSchemas(graph, nodeId, allEvents);
  const byGroup = new Map<string, Map<string, FieldSchema & { path: string }>>();

  const add = (groupKey: string, field: FieldSchema & { path: string }) => {
    if (options?.filterableOnly && field.filterable === false) return;
    let map = byGroup.get(groupKey);
    if (!map) {
      map = new Map();
      byGroup.set(groupKey, map);
    }
    if (!map.has(field.path)) map.set(field.path, field);
  };

  for (const schema of schemas) {
    for (const f of schema.fields) {
      const dot = f.name.indexOf(".");
      const prefix = dot > 0 ? f.name.slice(0, dot) : "payload";
      add(prefix, { ...f, path: f.name });
    }
  }

  const groups: PickerFieldGroup[] = [];
  const preferredOrder = ["payload", "stream", "source", "viewer", "client", "network", "server"];
  const orderedKeys = [
    ...preferredOrder.filter((k) => byGroup.has(k)),
    ...[...byGroup.keys()].filter((k) => !preferredOrder.includes(k)),
  ];
  for (const key of orderedKeys) {
    groups.push({
      id: key,
      label: fieldSubgroupLabel(key),
      fields: [...byGroup.get(key)!.values()],
    });
  }

  // Prior action/logic step outputs reachable upstream.
  const actionMap = new Map(allActions.map((a) => [a.type, a]));
  const stepFields: (FieldSchema & { path: string })[] = [];
  for (const n of graph.nodes) {
    if (n.id === nodeId) continue;
    if (n.kind === "action") {
      const actionType = String(n.config?.actionType ?? "");
      const spec = actionMap.get(actionType);
      for (const out of spec?.outputs ?? []) {
        stepFields.push({
          ...out,
          label: `${n.label || spec?.label || n.id} · ${out.label}`,
          path: `steps.${n.id}.output.${out.name}`,
        });
      }
    } else if (n.kind === "logic.window") {
      stepFields.push({
        name: "count",
        path: `steps.${n.id}.count`,
        type: "int",
        label: `${n.label || n.id} · ${t("Match count")}`,
        filterable: true,
      });
    }
  }
  if (stepFields.length > 0) {
    groups.push({ id: "steps", label: t("Prior step outputs"), fields: stepFields });
  }

  // Event envelope metadata & correlation.
  groups.push({
    id: "event",
    label: t("Event details"),
    fields: [
      {
        name: "event.type",
        path: "event.type",
        type: "string",
        label: t("Event type"),
        filterable: true,
        example: "playback.session.started",
      },
      {
        name: "event.node",
        path: "event.node",
        type: "string",
        label: t("Origin node"),
        filterable: true,
        example: "node-1",
      },
      {
        name: "event.occurredAt",
        path: "event.occurredAt",
        type: "timestamp",
        label: t("Occurred at"),
        filterable: true,
      },
      {
        name: "correlation.sessionId",
        path: "correlation.sessionId",
        type: "string",
        label: t("Session ID"),
        filterable: true,
      },
      {
        name: "correlation.viewerId",
        path: "correlation.viewerId",
        type: "int",
        label: t("Viewer ID"),
        filterable: true,
      },
    ],
  });

  // Flow variables set by flow.set_variable nodes.
  const varFields: (FieldSchema & { path: string })[] = [];
  for (const n of graph.nodes) {
    if (n.kind === "action" && n.config?.actionType === "flow.set_variable") {
      const key = String(
        (n.config?.params as Record<string, unknown> | undefined)?.key ?? "",
      ).trim();
      if (key) {
        varFields.push({
          name: `vars.${key}`,
          path: `vars.${key}`,
          type: "string",
          label: t("Variable: {key}", { key }),
          filterable: true,
        });
      }
    }
  }
  if (varFields.length > 0) {
    groups.push({ id: "vars", label: t("Flow variables"), fields: varFields });
  }

  return groups;
}

/** Topological left-to-right auto-layout for a Flow DAG. */
export function autoLayoutGraph(graph: Graph): Graph {
  if (graph.nodes.length === 0) return graph;
  const incomingCount = new Map<string, number>();
  const outgoing = new Map<string, FlowEdge[]>();
  for (const n of graph.nodes) {
    incomingCount.set(n.id, 0);
    outgoing.set(n.id, []);
  }
  for (const e of graph.edges) {
    if (incomingCount.has(e.target) && outgoing.has(e.source)) {
      incomingCount.set(e.target, (incomingCount.get(e.target) ?? 0) + 1);
      outgoing.get(e.source)!.push(e);
    }
  }

  // Assign columns via longest path from roots.
  const col = new Map<string, number>();
  const queue: string[] = [];
  for (const n of graph.nodes) {
    if ((incomingCount.get(n.id) ?? 0) === 0 || isTriggerKind(n.kind)) {
      col.set(n.id, 0);
      queue.push(n.id);
    }
  }
  let guard = 0;
  while (queue.length > 0 && guard < graph.nodes.length * 4) {
    guard++;
    const cur = queue.shift()!;
    const curCol = col.get(cur) ?? 0;
    for (const edge of outgoing.get(cur) ?? []) {
      const nextCol = curCol + 1;
      if (nextCol > (col.get(edge.target) ?? -1)) {
        col.set(edge.target, nextCol);
        queue.push(edge.target);
      }
    }
  }
  for (const n of graph.nodes) {
    if (!col.has(n.id)) col.set(n.id, 0);
  }

  const columns = new Map<number, FlowNode[]>();
  for (const n of graph.nodes) {
    const c = col.get(n.id) ?? 0;
    const list = columns.get(c) ?? [];
    list.push(n);
    columns.set(c, list);
  }

  const colSpacing = 336;
  const rowSpacing = 176;
  const startX = 64;
  const startY = 80;
  const nextPositions = new Map<string, Position>();

  for (const [c, nodes] of [...columns.entries()].sort((a, b) => a[0] - b[0])) {
    const totalH = (nodes.length - 1) * rowSpacing;
    const baseY = Math.max(startY, 220 - totalH / 2);
    nodes.forEach((n, idx) => {
      nextPositions.set(n.id, {
        x: Math.round((startX + c * colSpacing) / 16) * 16,
        y: Math.round((baseY + idx * rowSpacing) / 16) * 16,
      });
    });
  }

  return {
    ...graph,
    nodes: graph.nodes.map((n) => ({
      ...n,
      position: nextPositions.get(n.id) ?? n.position,
    })),
  };
}

/** Check whether adding an edge from source -> target would create a cycle. */
export function wouldCreateCycle(graph: Graph, source: string, target: string): boolean {
  if (source === target) return true;
  const adj = new Map<string, string[]>();
  for (const e of graph.edges) {
    const list = adj.get(e.source) ?? [];
    list.push(e.target);
    adj.set(e.source, list);
  }
  const visited = new Set<string>();
  const stack = [target];
  while (stack.length > 0) {
    const cur = stack.pop()!;
    if (cur === source) return true;
    if (visited.has(cur)) continue;
    visited.add(cur);
    for (const next of adj.get(cur) ?? []) stack.push(next);
  }
  return false;
}

/** Human-friendly short summary of one node's configuration for canvas cards. */
export function summarizeNode(
  node: FlowNode,
  eventsByType: Map<string, EventSchema>,
  actionsByType: Map<string, ActionSpec>,
): string {
  const cfg = (node.config ?? {}) as Record<string, unknown>;
  switch (node.kind) {
    case "trigger.event": {
      const evs = Array.isArray(cfg.events) ? (cfg.events as string[]) : [];
      if (evs.length === 0) return t("Choose events…");
      const labels = evs.slice(0, 2).map((e) => eventsByType.get(e)?.label ?? e);
      return evs.length > 2
        ? t("{list} +{more}", { list: labels.join(", "), more: evs.length - 2 })
        : labels.join(", ");
    }
    case "trigger.schedule": {
      if (typeof cfg.every === "string" && cfg.every)
        return t("Every {interval}", { interval: cfg.every });
      if (typeof cfg.cron === "string" && cfg.cron) return describeCron(cfg.cron);
      return t("Set schedule…");
    }
    case "trigger.webhook":
      return cfg.requireAuth ? t("Authenticated POST hook") : t("Incoming HTTP hook");
    case "trigger.manual": {
      const inputs = Array.isArray(cfg.inputs) ? cfg.inputs.length : 0;
      return inputs > 0 ? t("On demand · {count} inputs", { count: inputs }) : t("Run on demand");
    }
    case "condition": {
      const cond = cfg.condition as ConditionGroup | undefined;
      const count = countConditionRules(cond);
      if (count === 0) return t("Add condition rules…");
      const op = cond?.op === "or" ? t("ANY") : t("ALL");
      return t("{count} rules · match {op}", { count, op });
    }
    case "logic.switch": {
      const cases = Array.isArray(cfg.cases) ? (cfg.cases as SwitchCase[]) : [];
      if (cases.length === 0) return t("Add branch cases…");
      return t("{count} branches + default", { count: cases.length });
    }
    case "logic.delay": {
      if (typeof cfg.duration === "string" && cfg.duration)
        return t("Wait {duration}", { duration: cfg.duration });
      if (typeof cfg.durationField === "string" && cfg.durationField)
        return t("Wait from {field}", { field: cfg.durationField });
      if (typeof cfg.until === "string" && cfg.until) return t("Wait until date");
      return t("Set wait time…");
    }
    case "logic.wait_event": {
      const evs = Array.isArray(cfg.eventTypes) ? (cfg.eventTypes as string[]) : [];
      const timeout = typeof cfg.timeout === "string" ? cfg.timeout : "";
      if (evs.length === 0) return t("Choose follow-up event…");
      const first = eventsByType.get(evs[0]!)?.label ?? evs[0]!;
      return timeout ? t("{event} · timeout {timeout}", { event: first, timeout }) : first;
    }
    case "logic.window": {
      const th = Number(cfg.threshold || 0);
      const win = String(cfg.window || "");
      return th && win
        ? t("{count}× within {window}", { count: th, window: win })
        : t("Set threshold…");
    }
    case "logic.dedupe": {
      const cd = String(cfg.cooldown || "");
      const max = Number(cfg.maxPerWindow || 1);
      return cd ? t("At most {max} per {cooldown}", { max, cooldown: cd }) : t("Set cooldown…");
    }
    case "action": {
      const actionType = String(cfg.actionType ?? "");
      const spec = actionsByType.get(actionType);
      if (!actionType) return t("Choose an action…");
      return spec?.label ?? actionType;
    }
  }
}

export function countConditionRules(group?: ConditionGroup): number {
  if (!group) return 0;
  let total = group.rules?.length ?? 0;
  for (const sub of group.groups ?? []) total += countConditionRules(sub);
  return total;
}

/** Friendly description for common 5-field cron schedules. */
export function describeCron(cron: string): string {
  const trimmed = cron.trim();
  if (trimmed === "@hourly") return t("Every hour");
  if (trimmed === "@daily" || trimmed === "@midnight") return t("Every day at 00:00");
  if (trimmed === "@weekly") return t("Every Sunday at 00:00");
  if (trimmed === "@monthly") return t("First of every month");
  const parts = trimmed.split(/\s+/);
  if (parts.length !== 5) return trimmed;
  const [min, hr, dom, mon, dow] = parts;
  const time = `${hr!.padStart(2, "0")}:${min!.padStart(2, "0")}`;
  if (dom === "*" && mon === "*" && dow === "*" && /^\d+$/.test(min!) && /^\d+$/.test(hr!)) {
    return t("Daily at {time}", { time });
  }
  const dayNames: Record<string, string> = {
    "0": t("Sunday"),
    "1": t("Monday"),
    "2": t("Tuesday"),
    "3": t("Wednesday"),
    "4": t("Thursday"),
    "5": t("Friday"),
    "6": t("Saturday"),
    "7": t("Sunday"),
  };
  if (dom === "*" && mon === "*" && dayNames[dow!] && /^\d+$/.test(min!) && /^\d+$/.test(hr!)) {
    return t("Every {day} at {time}", { day: dayNames[dow!], time });
  }
  return trimmed;
}

/** Default empty config for a newly added node kind. */
export function defaultConfigForNode(kind: NodeKind, actionType?: string): Record<string, unknown> {
  switch (kind) {
    case "trigger.event":
      return { events: ["playback.session.started"] };
    case "trigger.schedule":
      return { every: "1h", timezone: "UTC" };
    case "trigger.webhook":
      return { requireAuth: true, allowedMethods: ["POST"] };
    case "trigger.manual":
      return { inputs: [], sampleEventType: "playback.session.started" };
    case "condition":
      return {
        condition: {
          op: "and",
          rules: [{ field: "stream.videoDecision", op: "eq", value: "transcode" }],
        },
      };
    case "logic.switch":
      return {
        cases: [
          {
            key: "case_1",
            label: t("Case 1"),
            condition: {
              op: "and",
              rules: [{ field: "event.type", op: "eq", value: "playback.session.started" }],
            },
          },
        ],
        matchAll: false,
      };
    case "logic.delay":
      return { duration: "5m" };
    case "logic.wait_event":
      return {
        eventTypes: ["playback.session.resumed", "playback.session.stopped"],
        matchCorrelation: true,
        timeout: "15m",
      };
    case "logic.window":
      return {
        keyTemplate: "{{ payload.sessionId }}",
        threshold: 3,
        window: "2m",
        resetOnReach: true,
      };
    case "logic.dedupe":
      return {
        keyTemplate: "{{ payload.viewer.id }}",
        cooldown: "30m",
        maxPerWindow: 1,
      };
    case "action":
      return {
        actionType: actionType || "notify.send",
        params:
          actionType === "playback.stop_session"
            ? {
                sessionId: "{{ payload.sessionId }}",
                message: t("Playback was stopped by a server automation."),
              }
            : actionType === "mqtt.publish"
              ? { topic: "home/cinema/state", payload: "playing", qos: 0, retain: false }
              : actionType === "http.webhook"
                ? {
                    method: "POST",
                    url: "https://example.invalid/hook",
                    contentType: "application/json",
                  }
                : actionType === "viewer.restrict"
                  ? { viewerId: "{{ payload.viewer.id }}", remoteDisabled: true }
                  : actionType === "library.refresh"
                    ? { libraryId: "1" }
                    : actionType === "digest.generate"
                      ? { window: "7d", groupEpisodes: true }
                      : actionType === "library.export"
                        ? { scope: "library", format: "csv", metadataLevel: 2, mediaInfoLevel: 2 }
                        : actionType === "flow.set_variable"
                          ? { key: "reason", value: "{{ payload.error }}" }
                          : actionType === "notify.send" || !actionType
                            ? {
                                channelIds: ["*"],
                                title: "{{ payload.titleName }}",
                                message: "{{ payload.viewer.username }} · {{ event.type }}",
                              }
                            : {},
      };
  }
}

/** Map legacy internal/notify event kinds to canonical Flows event types (docs/design/flows.md §3.2). */
export const LEGACY_NOTIFY_TO_CANONICAL: Record<string, string> = {
  "server.started": "system.server.started",
  "server.stopping": "system.server.stopping",
  "server.crash": "system.server.crashed",
  "provider.outage": "provider.service.outage",
  "provider.recovered": "provider.service.recovered",
  "indexer.outage": "indexer.service.outage",
  "indexer.recovered": "indexer.service.recovered",
  "indexer.budget": "indexer.budget.exhausted",
  "cache.full": "cache.budget.full",
  "cache.recovered": "cache.budget.recovered",
  "playback.start": "playback.session.started",
  "playback.stop": "playback.session.stopped",
  "playback.error": "playback.session.failed",
  "playback.failover": "playback.source.failover",
  "cert.renewed": "auth.certificate.renewed",
  "update.available": "system.update.available",
  "update.installed": "system.update.installed",
  "update.rolledback": "system.update.rolled_back",
  "backup.failed": "system.backup.failed",
  "user.joined": "viewer.account.joined",
  "title.available": "library.title.available",
  "newsletter.weekly": "flows.digest.scheduled",
};

/** Resolve a dotted path inside a plain JS object (for client-side live template previews). */
export function resolvePreviewPath(path: string, sample: Record<string, unknown>): unknown {
  const clean = path.trim();
  if (!clean) return undefined;
  const normalized = clean.startsWith("payload.")
    ? clean.slice("payload.".length)
    : clean.startsWith("event.payload.")
      ? clean.slice("event.payload.".length)
      : clean;
  if (normalized === "event.type") return sample.__eventType ?? "playback.session.started";
  if (normalized === "event.node") return "node-1";
  if (normalized === "event.occurredAt") return new Date().toISOString();
  const parts = normalized.split(".");
  let cur: unknown = sample;
  for (const part of parts) {
    if (!cur || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[part];
  }
  return cur;
}

/** Lightweight client-side preview renderer for `{{ expr }}` templates in the inspector. */
export function previewTemplate(tmpl: string, sample: Record<string, unknown>): string {
  if (!tmpl.includes("{{")) return tmpl;
  // Strip simple {{#if ...}}...{{#else}}...{{/if}} blocks gracefully for live preview.
  let out = tmpl.replace(
    /\{\{#if\s+([^}]+)\}\}([\s\S]*?)(?:\{\{#else\}\}([\s\S]*?))?\{\{\/if\}\}/g,
    (_m, cond: string, thenPart: string, elsePart = "") => {
      const eq = /^([^\s]+)\s*(==|!=)\s*["']([^"']+)["']$/.exec(cond.trim());
      if (eq) {
        const val = String(resolvePreviewPath(eq[1]!, sample) ?? "");
        const hit =
          eq[2] === "=="
            ? val.toLowerCase() === eq[3]!.toLowerCase()
            : val.toLowerCase() !== eq[3]!.toLowerCase();
        return hit ? thenPart : elsePart;
      }
      const val = resolvePreviewPath(cond.trim(), sample);
      return val ? thenPart : elsePart;
    },
  );
  out = out.replace(/\{\{\s*([^#\/][^}]*?)\s*\}\}/g, (_m, expr: string) => {
    const [rawPath, ...filters] = expr.split("|").map((s) => s.trim());
    let val = resolvePreviewPath(rawPath ?? "", sample);
    for (const f of filters) {
      if (f === "upper") val = String(val ?? "").toUpperCase();
      else if (f === "lower") val = String(val ?? "").toLowerCase();
      else if (f === "trim") val = String(val ?? "").trim();
      else if (f.startsWith("default ")) {
        if (val === undefined || val === null || val === "") {
          val = f.slice(8).replace(/^["']|["']$/g, "");
        }
      } else if (f.startsWith("join")) {
        const sep =
          f
            .slice(4)
            .trim()
            .replace(/^["']|["']$/g, "") || ", ";
        if (Array.isArray(val)) val = val.join(sep);
      } else if (f === "duration" && typeof val === "number") {
        const sec = Math.round(val / 1000);
        val = sec >= 60 ? `${Math.floor(sec / 60)}m ${sec % 60}s` : `${sec}s`;
      } else if (f === "bytes" && typeof val === "number") {
        val = val >= 1e9 ? `${(val / 1e9).toFixed(1)} GB` : `${Math.round(val / 1e6)} MB`;
      } else if (f.startsWith("percent") && typeof val === "number") {
        val = `${Math.round(val * 100)}%`;
      }
    }
    if (val === undefined || val === null) return "";
    if (Array.isArray(val)) return val.join(", ");
    if (typeof val === "object") return JSON.stringify(val);
    return String(val);
  });
  return out;
}

/** Quick client-side template syntax validation matching internal/flows/template.go rules. */
export function validateTemplateSyntax(tmpl: string): string | null {
  if (!tmpl.includes("{{") && !tmpl.includes("}}")) return null;
  let rest = tmpl;
  const stack: string[] = [];
  const allowedFilters = new Set([
    "upper",
    "lower",
    "title",
    "trim",
    "bytes",
    "duration",
    "json",
    "first",
    "last",
    "count",
    "default",
    "join",
    "truncate",
    "slice",
    "percent",
  ]);
  while (rest.length > 0) {
    const open = rest.indexOf("{{");
    const close = rest.indexOf("}}");
    if (close >= 0 && (open < 0 || close < open)) {
      return t("Unmatched closing braces }}");
    }
    if (open < 0) break;
    const after = rest.slice(open + 2);
    const end = after.indexOf("}}");
    if (end < 0) return t("Unclosed template expression {{");
    const tag = after.slice(0, end).trim();
    if (!tag) return t("Empty template expression");
    if (tag === "#else") {
      if (stack.length === 0) return t("Unexpected {{#else}} outside conditional");
    } else if (tag === "/if") {
      if (stack.pop() !== "if") return t("Unexpected {{/if}}");
    } else if (tag === "/unless") {
      if (stack.pop() !== "unless") return t("Unexpected {{/unless}}");
    } else if (tag.startsWith("#if ")) {
      stack.push("if");
    } else if (tag.startsWith("#unless ")) {
      stack.push("unless");
    } else if (tag.startsWith("#") || tag.startsWith("/")) {
      return t("Unknown template block “{tag}”", { tag });
    } else {
      const parts = tag.split("|").map((s) => s.trim());
      if (!parts[0]) return t("Missing field path in expression");
      for (const f of parts.slice(1)) {
        const name = f.split(/\s+/)[0] ?? "";
        if (!allowedFilters.has(name)) {
          return t("Unknown template filter “{name}”", { name });
        }
      }
    }
    rest = after.slice(end + 2);
  }
  if (stack.length > 0) {
    return t("Unclosed conditional block");
  }
  return null;
}
