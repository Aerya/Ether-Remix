import { Badge, cn } from "@braedonsaunders/appkit-ui";
import {
  Bell,
  CalendarClock,
  Clock3,
  Download,
  GitBranch,
  Hand,
  History,
  Hourglass,
  Lightbulb,
  Newspaper,
  OctagonX,
  Pin,
  PinOff,
  Play,
  RadioTower,
  RefreshCw,
  Repeat2,
  ShieldAlert,
  ShieldCheck,
  Shuffle,
  Variable,
  Webhook,
  Zap,
  type LucideIcon,
} from "lucide-react";
import { t } from "../../i18n";
import { flowsApi } from "../../lib/flowsApi";
import { useAsync } from "../../lib/hooks";
import type { ActionSpec, EventSchema, FlowSummary, NodeKind, RunStatus } from "./types";
import { nodeCategory, nodeKindLabel } from "./types";

export const KIND_ICONS: Record<NodeKind, LucideIcon> = {
  "trigger.event": Zap,
  "trigger.schedule": CalendarClock,
  "trigger.webhook": Webhook,
  "trigger.manual": Hand,
  condition: GitBranch,
  "logic.switch": Shuffle,
  "logic.delay": Hourglass,
  "logic.wait_event": Clock3,
  "logic.window": History,
  "logic.dedupe": Repeat2,
  action: Play,
};

export const ACTION_ICONS: Record<string, LucideIcon> = {
  "notify.send": Bell,
  "playback.stop_session": OctagonX,
  "http.webhook": Webhook,
  "mqtt.publish": Lightbulb,
  "viewer.restrict": ShieldAlert,
  "viewer.unrestrict": ShieldCheck,
  "library.refresh": RefreshCw,
  "library.export": Download,
  "digest.generate": Newspaper,
  "source.refresh": RadioTower,
  "release.pin": Pin,
  "release.unpin": PinOff,
  "flow.set_variable": Variable,
};

export function iconForNode(kind: NodeKind, actionType?: string): LucideIcon {
  if (kind === "action" && actionType && ACTION_ICONS[actionType]) return ACTION_ICONS[actionType];
  return KIND_ICONS[kind];
}

/** Category accent used on node cards, palette rows and traces. */
export function categoryTone(kind: NodeKind): { chip: string; ring: string; dot: string } {
  switch (nodeCategory(kind)) {
    case "trigger":
      return {
        chip: "bg-primary-subtle text-primary",
        ring: "border-primary/40",
        dot: "bg-primary",
      };
    case "condition":
      return {
        chip: "bg-warning-subtle text-warning",
        ring: "border-warning/40",
        dot: "bg-warning",
      };
    case "logic":
      return { chip: "bg-info-subtle text-info", ring: "border-info/40", dot: "bg-info" };
    case "action":
      return {
        chip: "bg-success-subtle text-success",
        ring: "border-success/40",
        dot: "bg-success",
      };
  }
}

export function runStatusLabel(status: RunStatus): string {
  switch (status) {
    case "pending":
      return t("Queued");
    case "running":
      return t("Running");
    case "waiting":
      return t("Waiting");
    case "completed":
      return t("Completed");
    case "failed":
      return t("Failed");
    case "cancelled":
      return t("Cancelled");
    case "throttled":
      return t("Throttled");
  }
}

export function RunStatusBadge({ status }: { status: RunStatus }) {
  const variant =
    status === "completed"
      ? "success"
      : status === "failed"
        ? "destructive"
        : status === "running" || status === "waiting"
          ? "default"
          : "outline";
  return (
    <Badge variant={variant as never} className={cn(status === "running" && "animate-pulse")}>
      {runStatusLabel(status)}
    </Badge>
  );
}

export interface Catalogue {
  events: EventSchema[];
  actions: ActionSpec[];
  eventsByType: Map<string, EventSchema>;
  actionsByType: Map<string, ActionSpec>;
}

let catalogueCache: Catalogue | null = null;

/** Event + action catalogue from the API, cached for the session. */
export function useCatalogue() {
  return useAsync<Catalogue>(async () => {
    if (catalogueCache) return catalogueCache;
    const [events, actions] = await Promise.all([
      flowsApi.eventCatalogue(),
      flowsApi.actionCatalogue(),
    ]);
    catalogueCache = {
      events,
      actions,
      eventsByType: new Map(events.map((e) => [e.type, e])),
      actionsByType: new Map(actions.map((a) => [a.type, a])),
    };
    return catalogueCache;
  }, []);
}

/** Readable rendering of any trace value (never a raw JSON dump for scalars). */
export function formatValue(v: unknown): string {
  if (v === undefined || v === null) return "—";
  if (typeof v === "boolean") return v ? t("Yes") : t("No");
  if (Array.isArray(v)) return v.map((x) => String(x)).join(", ") || "—";
  if (typeof v === "object") {
    const entries = Object.entries(v as Record<string, unknown>);
    if (entries.length === 0) return "—";
    return entries.map(([k, x]) => `${k}: ${formatValue(x)}`).join(" · ");
  }
  return String(v);
}

export function newNodeId(prefix = "n"): string {
  return `${prefix}${Math.random().toString(36).slice(2, 8)}`;
}

/**
 * What starts a flow, from its list row: event names from the catalogue
 * ("Playback started · Playback paused"), or the trigger kind.
 */
export function triggerSummary(flow: FlowSummary, catalogue: Catalogue | undefined): string {
  const parts: string[] = [];
  const events = flow.trigger?.events ?? [];
  const labels = events.map((e) => catalogue?.eventsByType.get(e)?.label ?? e);
  if (labels.length > 3)
    parts.push(
      t("{first} and {count} more", {
        first: labels.slice(0, 2).join(" · "),
        count: labels.length - 2,
      }),
    );
  else if (labels.length) parts.push(labels.join(" · "));
  for (const kind of flow.trigger?.kinds ?? []) {
    if (kind === "trigger.event") continue;
    parts.push(nodeKindLabel(kind));
  }
  return parts.join(" · ");
}
