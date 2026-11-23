import {
  Badge,
  Button,
  EmptyState,
  Input,
  Select,
  Switch,
  cn,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  Activity,
  Bell,
  FlaskConical,
  Gauge,
  Lightbulb,
  Newspaper,
  OctagonX,
  Pencil,
  PinOff,
  Play,
  Plus,
  Search,
  Siren,
  Smartphone,
  Sparkles,
  Timer,
  Trash2,
  Workflow,
  type LucideIcon,
} from "lucide-react";
import { useMemo, useRef, useState, type RefObject } from "react";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useOutlet,
  useOutletContext,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { Field, Segmented } from "../../components/fields";
import { ErrorPanel, LoadingRows, MutationError } from "../../components/feedback";
import { DomainPage, Section } from "../../components/layout";
import { localized, t, tn } from "../../i18n";
import { apiFetch, errorMessage } from "../../lib/api";
import { flowsApi } from "../../lib/flowsApi";
import { relativeTime } from "../../lib/format";
import { useAsync, useInterval } from "../../lib/hooks";
import { useStreamEvent } from "../../lib/stream";
import type { NotificationsData } from "../notifications/types";
import { DescribeFlowDialog } from "./DescribeFlowDialog";
import { FlowEditorDrawer } from "./Editor";
import { FlowsOverview, StatsRangePicker } from "./Overview";
import { fillTemplateDefaults } from "./params";
import { StepTrace } from "./RunTrace";
import { RunStatusBadge, triggerSummary, useCatalogue } from "./shared";
import { TestRunDrawer } from "./TestRunDrawer";
import {
  FLOW_PICKER,
  type Flow,
  type FlowSummary,
  type FlowTemplate,
  type ParamSchema,
  type Run,
  type RunStatus,
} from "./types";

const TABS = localized(
  () =>
    [
      { key: "overview", label: t("Overview") },
      { key: "list", label: t("Flows") },
      { key: "templates", label: t("Templates") },
      { key: "runs", label: t("Runs") },
    ] as const,
);

type TabKey = (typeof TABS)[number]["key"];

const TEMPLATE_ICONS: Record<string, LucideIcon> = {
  OctagonX,
  Timer,
  Gauge,
  Bell,
  Newspaper,
  Lightbulb,
  Smartphone,
  PinOff,
  Siren,
};

/**
 * Flows home (overview / flows / templates / runs) with the canvas editor
 * opening as a large URL-backed drawer over the flows list (/flows/list/new
 * and /flows/list/:id). Older links still land: /flows/flows goes to the
 * table and /flows/<flow id> opens that flow in the drawer.
 */
export default function Flows() {
  return (
    <Routes>
      <Route index element={<FlowsHome tab="overview" />} />
      <Route path="list" element={<FlowsHome tab="list" />}>
        <Route path=":id" element={<FlowEditorRoute />} />
      </Route>
      <Route path="templates" element={<FlowsHome tab="templates" />} />
      <Route path="runs" element={<FlowsHome tab="runs" />} />
      <Route path="flows" element={<Navigate to="/flows/list" replace />} />
      <Route path="*" element={<FlowsFallback />} />
    </Routes>
  );
}

function FlowsFallback() {
  const params = useParams();
  const first = (params["*"] ?? "").split("/").filter(Boolean)[0];
  if (first?.startsWith("flow_") || first?.startsWith("flow-")) {
    return <Navigate to={`/flows/list/${encodeURIComponent(first)}`} replace />;
  }
  return <Navigate to="/flows" replace />;
}

interface FlowListOutletContext {
  close: () => void;
  onChanged: () => void;
  promotedIdRef: RefObject<string | null>;
}

function FlowEditorRoute() {
  const { close, onChanged, promotedIdRef } = useOutletContext<FlowListOutletContext>();
  const { id } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const flowId = id === "new" ? undefined : id;

  // Keep the same editor instance when a brand-new flow gets its first saved id,
  // so the URL switches without closing or remounting the drawer.
  const sessionKeyRef = useRef<string>(flowId ? `edit:${flowId}` : `new:${location.key}`);
  const prevNewLocationKeyRef = useRef<string | null>(flowId ? null : location.key);
  if (flowId) {
    prevNewLocationKeyRef.current = null;
    if (promotedIdRef.current !== flowId) {
      promotedIdRef.current = null;
      sessionKeyRef.current = `edit:${flowId}`;
    }
  } else if (prevNewLocationKeyRef.current !== location.key) {
    promotedIdRef.current = null;
    prevNewLocationKeyRef.current = location.key;
    sessionKeyRef.current = `new:${location.key}`;
  }

  return (
    <FlowEditorDrawer
      key={sessionKeyRef.current}
      flowId={flowId}
      onClose={close}
      onSaved={(saved, created) => {
        onChanged();
        if (created) {
          promotedIdRef.current = saved.id;
          navigate(`/flows/list/${encodeURIComponent(saved.id)}`, { replace: true });
        }
      }}
      onDeleted={() => {
        onChanged();
        close();
      }}
    />
  );
}

function FlowsHome({ tab }: { tab: TabKey }) {
  const location = useLocation();
  const navigate = useNavigate();
  const promotedIdRef = useRef<string | null>(null);
  const editing = tab === "list" && /^\/flows\/list\/.+/.test(location.pathname.replace(/\/$/, ""));
  const browseSearch = useRef(editing ? "" : location.search);
  if (tab === "list" && !editing) browseSearch.current = location.search;

  // Overview and the table share one list request; keep it mounted behind the editor drawer.
  const flowsState = useFlowsList(tab === "overview" || tab === "list", !editing);
  const outlet = useOutlet({
    close: () => navigate(`/flows/list${browseSearch.current}`),
    onChanged: () => void flowsState.reload(),
    promotedIdRef,
  } satisfies FlowListOutletContext);

  return (
    <>
      <div
        aria-hidden={editing ? true : undefined}
        inert={editing}
        className="flex min-h-0 flex-1 flex-col"
      >
        <DomainPage
          title={t("Flows")}
          description={t(
            "Automations for your whole server — when something happens, check a few things, then act. Flows react off to the side; they never sit in the way of playback.",
          )}
          base="/flows"
          tabs={TABS}
          active={tab}
          tabHref={(key) => (key === "overview" ? "/flows" : `/flows/${key}`)}
          tabActions={tab === "overview" ? <StatsRangePicker /> : undefined}
          fit={tab === "overview" || tab === "list"}
        >
          {tab === "overview" ? (
            <FlowsOverview
              flows={flowsState.data}
              flowsError={flowsState.error}
              onRetryFlows={() => void flowsState.reload()}
            />
          ) : tab === "list" ? (
            <FlowListTab flowsState={flowsState} />
          ) : tab === "templates" ? (
            <TemplatesTab />
          ) : (
            <RunsTab />
          )}
        </DomainPage>
      </div>
      {outlet}
    </>
  );
}

function useFlowsList(active: boolean, poll = active) {
  const state = useAsync(async () => (active ? flowsApi.listFlows() : undefined), [active]);
  useStreamEvent("flows.changed", () => void state.reload());
  useStreamEvent("flows.run", () => void state.reload());
  useInterval(() => void state.reload(), active && poll ? 30_000 : null);
  return state;
}

type FlowFilter = "all" | "on" | "off" | "attention";

function needsAttention(f: FlowSummary): boolean {
  return Boolean(f.disabledReason) || (f.failures24h ?? 0) > 0 || f.lastStatus === "failed";
}

function FlowListTab({ flowsState }: { flowsState: ReturnType<typeof useFlowsList> }) {
  const navigate = useNavigate();
  const catalogueState = useCatalogue();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<FlowFilter>("all");
  const [describeOpen, setDescribeOpen] = useState(false);
  const [testFlow, setTestFlow] = useState<Flow | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const flows = flowsState.data ?? [];
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return flows.filter(
      (f) =>
        (filter === "all" ||
          (filter === "on" && f.enabled) ||
          (filter === "off" && !f.enabled) ||
          (filter === "attention" && needsAttention(f))) &&
        (!q || f.name.toLowerCase().includes(q) || (f.description ?? "").toLowerCase().includes(q)),
    );
  }, [flows, query, filter]);

  const act = async (flow: FlowSummary, fn: () => Promise<void>, ok?: string) => {
    setBusyId(flow.id);
    try {
      await fn();
      if (ok) toast.success(ok);
      await flowsState.reload();
    } catch (e) {
      toast.error(errorMessage(e));
      await flowsState.reload();
    } finally {
      setBusyId(null);
    }
  };

  // The list carries no graph; a test run needs the whole flow.
  const openTest = async (flow: FlowSummary) => {
    setBusyId(flow.id);
    try {
      setTestFlow(await flowsApi.getFlow(flow.id));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusyId(null);
    }
  };

  const attention = flows.filter(needsAttention).length;

  return (
    <>
      <Section
        title={t("Flows")}
        className="flex min-h-[360px] flex-1 flex-col"
        actions={
          <>
            <label className="flex w-56 max-w-full items-center gap-2 rounded-lg border border-border px-2.5 py-1.5">
              <Search size={14} className="shrink-0 text-fg-subtle" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t("Search flows…")}
                aria-label={t("Search flows")}
                className="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-fg-subtle"
              />
            </label>
            <Segmented
              ariaLabel={t("Show")}
              size="sm"
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all", label: t("All") },
                { value: "on", label: t("On") },
                { value: "off", label: t("Off") },
                {
                  value: "attention",
                  label: attention
                    ? t("Needs attention ({count})", { count: attention })
                    : t("Needs attention"),
                },
              ]}
            />
            <Button variant="outline" size="sm" onClick={() => setDescribeOpen(true)}>
              <Sparkles size={14} /> {t("Describe it")}
            </Button>
            <Button size="sm" onClick={() => navigate("/flows/list/new")}>
              <Plus size={14} /> {t("New flow")}
            </Button>
          </>
        }
        bodyClassName={cn(
          "min-h-0 flex-1 overflow-y-auto",
          visible.length ? "divide-y divide-border" : undefined,
        )}
      >
        {flowsState.error && !flowsState.data ? (
          <div className="px-5 py-4">
            <ErrorPanel error={flowsState.error} onRetry={() => void flowsState.reload()} />
          </div>
        ) : !flowsState.data ? (
          <div className="px-5 py-4">
            <LoadingRows rows={4} />
          </div>
        ) : visible.length === 0 ? (
          <EmptyState
            icon={<Workflow />}
            title={flows.length ? t("No flows match") : t("No flows yet")}
            description={
              flows.length
                ? t("Try a different search or filter.")
                : t(
                    "Describe what should happen, or start from a template — the first flow takes a minute.",
                  )
            }
            action={
              flows.length ? undefined : (
                <div className="flex gap-2">
                  <Button size="sm" onClick={() => setDescribeOpen(true)}>
                    <Sparkles size={14} /> {t("Describe it")}
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => navigate("/flows/templates")}>
                    {t("Browse templates")}
                  </Button>
                </div>
              )
            }
          />
        ) : (
          visible.map((flow) => {
            const trigger = triggerSummary(flow, catalogueState.data);
            return (
              <div key={flow.id} className="flex flex-wrap items-center gap-3 px-5 py-4">
                <span
                  aria-hidden
                  className={cn(
                    "size-2.5 shrink-0 rounded-full",
                    !flow.enabled
                      ? "bg-fg-subtle/40"
                      : flow.lastStatus === "failed"
                        ? "bg-danger"
                        : flow.lastStatus === "waiting" || flow.lastStatus === "running"
                          ? "animate-pulse bg-primary"
                          : "bg-success",
                  )}
                />
                <div className="min-w-0 flex-1 basis-56">
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      onClick={() => navigate(`/flows/list/${encodeURIComponent(flow.id)}`)}
                      className="truncate text-left font-medium text-fg hover:underline"
                    >
                      {flow.name}
                    </button>
                    {flow.lastStatus ? <RunStatusBadge status={flow.lastStatus} /> : null}
                    {flow.disabledReason && flow.disabledReason !== "disabled by owner" ? (
                      <Badge variant="destructive">{t("Turned itself off")}</Badge>
                    ) : null}
                  </div>
                  <p className="mt-0.5 truncate text-sm text-fg-muted" title={flow.description}>
                    {flow.description || trigger}
                  </p>
                  <p className="mt-1 flex flex-wrap gap-x-3 text-xs text-fg-subtle">
                    {trigger ? <span>{trigger}</span> : null}
                    <span>
                      {flow.lastRunAt
                        ? t("last run {when}", { when: relativeTime(flow.lastRunAt) })
                        : t("never run yet")}
                    </span>
                    <span>
                      {tn(flow.runs24h ?? 0, "{count} run in 24 h", "{count} runs in 24 h")}
                    </span>
                    {(flow.failures24h ?? 0) > 0 ? (
                      <span className="font-medium text-danger">
                        {tn(flow.failures24h ?? 0, "{count} failure", "{count} failures")}
                      </span>
                    ) : null}
                    {(flow.activeRuns ?? 0) > 0 ? (
                      <span className="font-medium text-primary">
                        {t("{count} in progress", { count: flow.activeRuns ?? 0 })}
                      </span>
                    ) : null}
                  </p>
                  {flow.disabledReason && flow.disabledReason !== "disabled by owner" ? (
                    <p className="mt-1 text-xs text-danger">{flow.disabledReason}</p>
                  ) : null}
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-2">
                  <label className="flex items-center gap-1.5 text-xs text-fg-muted">
                    <Switch
                      aria-label={t("Enable {name}", { name: flow.name })}
                      checked={flow.enabled}
                      disabled={busyId === flow.id}
                      onChange={(e) =>
                        void act(flow, async () => {
                          if (e.target.checked) await flowsApi.enableFlow(flow.id);
                          else await flowsApi.disableFlow(flow.id);
                        })
                      }
                    />
                    {flow.enabled ? t("On") : t("Off")}
                  </label>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={!catalogueState.data || busyId === flow.id}
                    onClick={() => void openTest(flow)}
                  >
                    <FlaskConical size={13} /> {t("Test")}
                  </Button>
                  {flow.trigger?.kinds.includes("trigger.manual") ? (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busyId === flow.id || !flow.enabled}
                      onClick={() =>
                        void act(
                          flow,
                          async () => {
                            await flowsApi.runManual(flow.id);
                          },
                          t("Flow started"),
                        )
                      }
                    >
                      <Play size={13} /> {t("Run now")}
                    </Button>
                  ) : null}
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => navigate(`/flows/list/${encodeURIComponent(flow.id)}`)}
                  >
                    <Pencil size={13} /> {t("Edit")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={t("Delete {name}", { name: flow.name })}
                    disabled={busyId === flow.id}
                    onClick={async () => {
                      const ok = await confirmDialog({
                        title: t("Delete this flow?"),
                        message: t(
                          "“{name}” and its run history will be removed. Active runs are cancelled.",
                          { name: flow.name },
                        ),
                        confirmLabel: t("Delete"),
                        tone: "danger",
                      });
                      if (ok)
                        void act(
                          flow,
                          async () => {
                            await flowsApi.deleteFlow(flow.id);
                          },
                          t("Flow deleted"),
                        );
                    }}
                  >
                    <Trash2 size={14} />
                  </Button>
                </div>
              </div>
            );
          })
        )}
      </Section>

      <DescribeFlowDialog
        open={describeOpen}
        onClose={() => setDescribeOpen(false)}
        onDraft={(draft) => navigate("/flows/list/new", { state: { draft } })}
      />
      {testFlow && catalogueState.data ? (
        <TestRunDrawer
          open
          onClose={() => setTestFlow(null)}
          flowId={testFlow.id}
          graph={testFlow.graph}
          catalogue={catalogueState.data}
        />
      ) : null}
    </>
  );
}

function TemplatesTab() {
  const navigate = useNavigate();
  const templatesState = useAsync(() => flowsApi.listTemplates(), []);
  const [category, setCategory] = useState("all");
  const [using, setUsing] = useState<FlowTemplate | null>(null);
  const templates = templatesState.data ?? [];
  const categories = useMemo(() => [...new Set(templates.map((x) => x.category))], [templates]);
  const visible = category === "all" ? templates : templates.filter((x) => x.category === category);

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap gap-1.5" role="group" aria-label={t("Template categories")}>
        {["all", ...categories].map((c) => (
          <button
            key={c}
            type="button"
            aria-pressed={category === c}
            onClick={() => setCategory(c)}
            className={cn(
              "rounded-full border px-3 py-1 text-sm transition-colors",
              category === c
                ? "border-primary bg-primary-subtle text-primary"
                : "border-border text-fg-muted hover:text-fg",
            )}
          >
            {c === "all" ? t("Everything") : c}
          </button>
        ))}
      </div>
      {templatesState.error && !templatesState.data ? (
        <ErrorPanel error={templatesState.error} onRetry={() => void templatesState.reload()} />
      ) : !templatesState.data ? (
        <LoadingRows rows={4} />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {visible.map((tpl) => {
            const Icon = (tpl.icon && TEMPLATE_ICONS[tpl.icon]) || Workflow;
            return (
              <article
                key={tpl.id}
                className="flex flex-col rounded-xl border border-border bg-surface p-4 shadow-xs transition-shadow hover:shadow"
              >
                <div className="flex items-start gap-3">
                  <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-primary-subtle text-primary">
                    <Icon size={18} />
                  </span>
                  <div className="min-w-0">
                    <h3 className="font-medium text-fg">{tpl.name}</h3>
                    <p className="text-xs text-fg-subtle">
                      {tpl.category} · {tn(tpl.graph.nodes.length, "{count} step", "{count} steps")}
                    </p>
                  </div>
                </div>
                <p className="mt-2.5 flex-1 text-sm leading-relaxed text-fg-muted">
                  {tpl.description}
                </p>
                <div className="mt-3 flex items-center gap-2">
                  <Button size="sm" onClick={() => setUsing(tpl)}>
                    {t("Use this template")}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() =>
                      navigate("/flows/list/new", {
                        state: {
                          draft: {
                            name: tpl.name,
                            description: tpl.description,
                            graph: fillTemplateDefaults(tpl),
                          },
                        },
                      })
                    }
                  >
                    {t("Preview on canvas")}
                  </Button>
                </div>
              </article>
            );
          })}
        </div>
      )}
      {using ? <UseTemplateDrawer template={using} onClose={() => setUsing(null)} /> : null}
    </div>
  );
}

/** The value a template param starts with in the drawer. */
function initialTemplateValue(p: ParamSchema): unknown {
  if (p.picker === FLOW_PICKER.channel && p.type === "string_list") return [];
  return p.default ?? (p.type === "bool" ? false : "");
}

/** A required param with neither a value nor a server-side default. */
function missingTemplateValue(p: ParamSchema, v: unknown): boolean {
  if (!p.required || (p.default !== undefined && p.default !== null)) return false;
  return v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0);
}

export function UseTemplateDrawer({
  template,
  onClose,
}: {
  template: FlowTemplate;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const params = template.params ?? [];
  const [name, setName] = useState(template.name);
  const [enabled, setEnabled] = useState(true);
  const [values, setValues] = useState<Record<string, unknown>>(() =>
    Object.fromEntries(params.map((p) => [p.key, initialTemplateValue(p)])),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const notices = useAsync(
    () => apiFetch<NotificationsData>("/notifications/").catch(() => null),
    [],
  );
  const channels = notices.data?.channels ?? [];
  const set = (key: string, v: unknown) => setValues((prev) => ({ ...prev, [key]: v }));
  const missing = params.filter((p) => missingTemplateValue(p, values[p.key]));

  const create = async () => {
    setBusy(true);
    setError(null);
    try {
      const out: Record<string, unknown> = {};
      for (const p of params) {
        const v = values[p.key];
        if (p.picker === FLOW_PICKER.channel && p.type === "string_list") {
          // An empty selection means every enabled channel.
          out[p.key] = Array.isArray(v) && v.length > 0 ? v : ["*"];
        } else if (v !== "" && v !== undefined) {
          out[p.key] = v;
        }
      }
      const flow = await flowsApi.createFromTemplate(template.id, {
        name: name.trim() || template.name,
        enabled,
        params: params.length ? out : undefined,
      });
      toast.success(t("Flow created from template"));
      onClose();
      navigate(`/flows/list/${encodeURIComponent(flow.id)}`);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer
      open
      onClose={onClose}
      title={template.name}
      footer={
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t("Cancel")}
          </Button>
          <Button disabled={busy || missing.length > 0} onClick={() => void create()}>
            {busy ? t("Creating…") : t("Create & open in editor")}
          </Button>
        </div>
      }
    >
      <div className="space-y-4">
        <p className="text-sm text-fg-muted">{template.description}</p>
        <Field label={t("Name")}>
          <Input
            aria-label={t("Flow name")}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        {params.map((p) => (
          <TemplateParamField
            key={p.key}
            param={p}
            value={values[p.key]}
            onChange={(v) => set(p.key, v)}
            channels={channels}
            channelsLoaded={Boolean(notices.data) || !notices.loading}
          />
        ))}
        <label className="flex items-center justify-between gap-3">
          <span className="text-sm text-fg">{t("Turn on right away")}</span>
          <Switch
            aria-label={t("Enabled")}
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
        </label>
        <MutationError error={error} />
      </div>
    </Drawer>
  );
}

function TemplateParamField({
  param: p,
  value,
  onChange,
  channels,
  channelsLoaded,
}: {
  param: ParamSchema;
  value: unknown;
  onChange: (v: unknown) => void;
  channels: NotificationsData["channels"];
  channelsLoaded: boolean;
}) {
  const label = p.label + (missingTemplateValue(p, "") ? " *" : "");
  if (p.picker === FLOW_PICKER.channel && p.type === "string_list") {
    const selected = Array.isArray(value) ? value.map(String) : [];
    return (
      <Field
        label={t("Send to")}
        hint={t("Leave empty to use all enabled channels. You can change this per step later.")}
      >
        <div className="flex flex-wrap gap-1">
          {channels.map((c) => (
            <button
              key={c.id}
              type="button"
              aria-pressed={selected.includes(c.id)}
              onClick={() =>
                onChange(
                  selected.includes(c.id)
                    ? selected.filter((x) => x !== c.id)
                    : [...selected, c.id],
                )
              }
              className={cn(
                "rounded-full border px-2.5 py-1 text-xs transition-colors",
                selected.includes(c.id)
                  ? "border-primary bg-primary-subtle text-primary"
                  : "border-border text-fg-muted hover:text-fg",
              )}
            >
              {c.name || c.kind}
            </button>
          ))}
          {channelsLoaded && channels.length === 0 ? (
            <span className="text-sm text-fg-muted">
              {t("No notification channels yet — the flow will use all channels once you add one.")}
            </span>
          ) : null}
        </div>
      </Field>
    );
  }
  if (p.picker === FLOW_PICKER.channel) {
    // One channel, e.g. the MQTT broker a lighting flow publishes to.
    const mqtt = channels.filter((c) => c.kind === "mqtt");
    const options = p.key === "brokerId" && mqtt.length > 0 ? mqtt : channels;
    return (
      <Field
        label={label}
        hint={
          channelsLoaded && options.length === 0
            ? p.key === "brokerId"
              ? t("Add an MQTT broker under Notifications first, then come back.")
              : t("Add a notification channel under Notifications first, then come back.")
            : p.description
        }
      >
        <Select
          aria-label={p.label}
          value={String(value ?? "")}
          onChange={(e) => onChange(e.target.value)}
          disabled={options.length === 0}
        >
          <option value="">{t("Choose…")}</option>
          {options.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name || c.kind}
            </option>
          ))}
        </Select>
      </Field>
    );
  }
  if (p.type === "bool") {
    return (
      <label className="flex items-center justify-between gap-3">
        <span className="text-sm text-fg">{p.label}</span>
        <Switch
          aria-label={p.label}
          checked={value === true}
          onChange={(e) => onChange(e.target.checked)}
        />
      </label>
    );
  }
  if (p.type === "enum" && p.enum) {
    return (
      <Field label={label} hint={p.description}>
        <Select
          aria-label={p.label}
          value={String(value ?? "")}
          onChange={(e) => onChange(e.target.value)}
        >
          {p.enum.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label ?? o.value}
            </option>
          ))}
        </Select>
      </Field>
    );
  }
  if (p.type === "int" || p.type === "float" || p.type === "duration_ms") {
    return (
      <Field label={label} hint={p.description}>
        <Input
          aria-label={p.label}
          type="number"
          value={value === undefined || value === null ? "" : String(value)}
          onChange={(e) => onChange(e.target.value === "" ? "" : Number(e.target.value))}
          className="w-40"
        />
      </Field>
    );
  }
  return (
    <Field label={label} hint={p.description}>
      <Input
        aria-label={p.label}
        value={Array.isArray(value) ? value.join(", ") : String(value ?? "")}
        onChange={(e) =>
          onChange(
            p.type === "string_list"
              ? e.target.value
                  .split(",")
                  .map((x) => x.trim())
                  .filter(Boolean)
              : e.target.value,
          )
        }
      />
    </Field>
  );
}

const RUN_FILTERS: { key: RunStatus | ""; label: () => string }[] = [
  { key: "", label: () => t("All runs") },
  { key: "running", label: () => t("Running") },
  { key: "waiting", label: () => t("Waiting") },
  { key: "completed", label: () => t("Completed") },
  { key: "failed", label: () => t("Failed") },
  { key: "cancelled", label: () => t("Cancelled") },
];

function RunsTab() {
  // ?status= and ?run= let the overview (and Ask Ether) link straight to
  // failures or to one run's trace.
  const [search, setSearch] = useSearchParams();
  const fromUrl = search.get("status") ?? "";
  const [status, setStatus] = useState<RunStatus | "">(
    RUN_FILTERS.some((f) => f.key === fromUrl) ? (fromUrl as RunStatus) : "",
  );
  const [flowId, setFlowId] = useState("");
  const openRun = search.get("run");
  const setOpenRun = (id: string | null) =>
    setSearch(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (id) next.set("run", id);
        else next.delete("run");
        return next;
      },
      { replace: true, preventScrollReset: true },
    );
  const flowsState = useAsync(() => flowsApi.listFlows(), []);
  const runsState = useAsync(
    () => flowsApi.listRuns({ status, flowId: flowId || undefined, limit: 60 }),
    [status, flowId],
  );
  useStreamEvent("flows.run", () => void runsState.reload());
  useInterval(() => void runsState.reload(), 15_000);

  const runs = runsState.data?.runs ?? [];
  const eventLabel = useEventLabels();

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1.5" role="group" aria-label={t("Filter by status")}>
          {RUN_FILTERS.map((f) => (
            <button
              key={f.key || "all"}
              type="button"
              aria-pressed={status === f.key}
              onClick={() => setStatus(f.key)}
              className={cn(
                "rounded-full border px-3 py-1 text-sm transition-colors",
                status === f.key
                  ? "border-primary bg-primary-subtle text-primary"
                  : "border-border text-fg-muted hover:text-fg",
              )}
            >
              {f.label()}
            </button>
          ))}
        </div>
        <Select
          aria-label={t("Filter by flow")}
          value={flowId}
          onChange={(e) => setFlowId(e.target.value)}
          className="ml-auto w-56 max-w-full"
        >
          <option value="">{t("Every flow")}</option>
          {(flowsState.data ?? []).map((f) => (
            <option key={f.id} value={f.id}>
              {f.name}
            </option>
          ))}
        </Select>
      </div>

      <Section bodyClassName={runs.length ? "divide-y divide-border" : undefined}>
        {runsState.error && !runsState.data ? (
          <div className="px-5 py-4">
            <ErrorPanel error={runsState.error} onRetry={() => void runsState.reload()} />
          </div>
        ) : !runsState.data ? (
          <div className="px-5 py-4">
            <LoadingRows rows={5} />
          </div>
        ) : runs.length === 0 ? (
          <EmptyState
            icon={<Activity />}
            title={t("No runs yet")}
            description={t(
              "Runs appear here the moment a flow fires — test one from the editor to see a trace right away.",
            )}
          />
        ) : (
          runs.map((run) => (
            <button
              key={run.id}
              type="button"
              onClick={() => setOpenRun(run.id)}
              className="flex w-full flex-wrap items-center gap-3 px-5 py-3.5 text-left transition-colors hover:bg-surface-hover"
            >
              <RunStatusBadge status={run.status} />
              <span className="min-w-0 flex-1 basis-48">
                <span className="block truncate font-medium text-fg">
                  {run.flowName ?? run.flowId}
                </span>
                <span className="block text-xs text-fg-muted">
                  {run.eventType ? eventLabel(run.eventType) : t("Manual run")}
                  {run.dryRun ? ` · ${t("test")}` : ""}
                  {run.error ? ` · ${run.error}` : ""}
                </span>
              </span>
              <span className="text-xs text-fg-subtle">
                {tn(run.stepCount, "{count} step", "{count} steps")}
              </span>
              <span className="text-sm tabular-nums text-fg-muted">
                {relativeTime(run.startedAt)}
              </span>
            </button>
          ))
        )}
      </Section>
      {openRun ? (
        <RunDetailDrawer
          runId={openRun}
          onClose={() => setOpenRun(null)}
          onChanged={() => void runsState.reload()}
        />
      ) : null}
    </div>
  );
}

function useEventLabels() {
  const catalogue = useCatalogue();
  return (type: string) => catalogue.data?.eventsByType.get(type)?.label ?? type;
}

function RunDetailDrawer({
  runId,
  onClose,
  onChanged,
}: {
  runId: string;
  onClose: () => void;
  onChanged: () => void;
}) {
  const catalogueState = useCatalogue();
  const detail = useAsync(async () => {
    const d = await flowsApi.getRun(runId);
    const flow = await flowsApi.getFlow(d.run.flowId).catch(() => null);
    return { ...d, flow };
  }, [runId]);
  useStreamEvent("flows.run", () => void detail.reload());
  const run: Run | undefined = detail.data?.run;
  const canCancel =
    run && (run.status === "running" || run.status === "waiting" || run.status === "pending");

  return (
    <Drawer
      open
      onClose={onClose}
      title={run?.flowName ?? t("Run")}
      size="xl"
      footer={
        canCancel ? (
          <div className="flex justify-end">
            <Button
              variant="outline"
              onClick={async () => {
                try {
                  await flowsApi.cancelRun(runId);
                  toast.success(t("Run cancelled"));
                  onChanged();
                  void detail.reload();
                } catch (e) {
                  toast.error(errorMessage(e));
                }
              }}
            >
              {t("Cancel this run")}
            </Button>
          </div>
        ) : undefined
      }
    >
      {detail.error && !detail.data ? (
        <ErrorPanel error={detail.error} onRetry={() => void detail.reload()} />
      ) : !detail.data ? (
        <LoadingRows rows={4} />
      ) : (
        <StepTrace
          run={detail.data.run}
          steps={detail.data.steps}
          graph={detail.data.flow?.graph}
          catalogue={catalogueState.data}
        />
      )}
    </Drawer>
  );
}
