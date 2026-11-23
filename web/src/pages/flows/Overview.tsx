import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Button, EmptyState, useElementSize } from "@braedonsaunders/appkit-ui";
import {
  Activity,
  BarChart3,
  Clock3,
  Sparkles,
  TriangleAlert,
  Workflow,
  Zap,
  type LucideIcon,
} from "lucide-react";
import { useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import {
  RankedBars,
  StackedSeriesColumns,
  TimeBars,
  type StackSeries,
} from "../../components/charts";
import { Segmented } from "../../components/fields";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Kpi, KpiRow } from "../../components/Kpi";
import { getLocale, t, tn } from "../../i18n";
import { flowsApi } from "../../lib/flowsApi";
import { formatMinutes, formatNanos, formatNumber, relativeTime } from "../../lib/format";
import { useAsync, useInterval, useMediaQuery } from "../../lib/hooks";
import { useStreamEvent } from "../../lib/stream";
import { DescribeFlowDialog } from "./DescribeFlowDialog";
import { useCatalogue, type Catalogue } from "./shared";
import type { FlowSummary, NodeKind, RunStats, StatsRange } from "./types";
import { nodeKindLabel } from "./types";

const RANGES: StatsRange[] = ["24h", "7d", "30d"];

/** The overview's period, kept in the URL (?range=) like Activity's. */
export function useStatsRange(): [StatsRange, (r: StatsRange) => void] {
  const [params, setParams] = useSearchParams();
  const raw = params.get("range");
  const range = (RANGES as string[]).includes(raw ?? "") ? (raw as StatsRange) : "7d";
  return [
    range,
    (next) =>
      setParams(
        (prev) => {
          const p = new URLSearchParams(prev);
          p.set("range", next);
          return p;
        },
        { preventScrollReset: true, replace: true },
      ),
  ];
}

export function StatsRangePicker() {
  const [range, setRange] = useStatsRange();
  return (
    <Segmented
      ariaLabel={t("Period")}
      size="sm"
      value={range}
      onChange={setRange}
      options={[
        { value: "24h", label: t("Last 24 hours") },
        { value: "7d", label: tn(7, "Last {count} day", "Last {count} days") },
        { value: "30d", label: tn(30, "Last {count} day", "Last {count} days") },
      ]}
    />
  );
}

/** "850 ms", "4.2 s", "3 min 10 s", "2 h 5 min". */
export function formatRunDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  return ms < 3_600_000 ? formatNanos(ms * 1e6) : formatMinutes(ms / 60_000);
}

const OUTCOMES = (): StackSeries[] => [
  { key: "succeeded", label: t("Succeeded"), color: "viz-1" },
  { key: "failed", label: t("Failed"), color: "viz-3" },
  { key: "skipped", label: t("Skipped"), color: "viz-2" },
];

function bucketLabel(iso: string, range: StatsRange): string {
  const d = new Date(iso);
  const locale = getLocale();
  if (range === "24h") return d.toLocaleTimeString(locale, { hour: "numeric" });
  if (range === "7d") return d.toLocaleString(locale, { weekday: "short", hour: "numeric" });
  return d.toLocaleDateString(locale, { month: "short", day: "numeric" });
}

function periodPhrase(range: StatsRange): string {
  return range === "24h"
    ? t("in the last 24 hours")
    : range === "7d"
      ? tn(7, "in the last {count} day", "in the last {count} days")
      : tn(30, "in the last {count} day", "in the last {count} days");
}

export function triggerLabel(
  kind: NodeKind,
  eventType: string | undefined,
  catalogue: Catalogue | undefined,
): string {
  if (kind === "trigger.event" && eventType)
    return catalogue?.eventsByType.get(eventType)?.label ?? eventType;
  return nodeKindLabel(kind);
}

/**
 * Flows overview: the KPI row, then charts that fill the rest of the
 * viewport (desktop) — runs over time by outcome, top flows, the triggers
 * that fired most, run time, and the latest failures with their traces.
 */
export function FlowsOverview({
  flows,
  flowsError,
  onRetryFlows,
}: {
  flows: FlowSummary[] | undefined;
  flowsError: unknown;
  onRetryFlows: () => void;
}) {
  const navigate = useNavigate();
  const [range] = useStatsRange();
  const catalogue = useCatalogue().data;
  const stats = useAsync((signal) => flowsApi.stats(range, signal), [range]);
  const lastReload = useRef(0);
  useStreamEvent("flows.run", () => {
    // Runs report every step; refresh at most every ten seconds.
    if (Date.now() - lastReload.current < 10_000) return;
    lastReload.current = Date.now();
    void stats.reload();
  });
  useInterval(() => void stats.reload(), 60_000);
  const [describeOpen, setDescribeOpen] = useState(false);

  const list = flows ?? [];
  const kpis = useMemo(
    () => ({
      enabled: list.filter((f) => f.enabled).length,
      runs24h: list.reduce((s, f) => s + (f.runs24h ?? 0), 0),
      failures24h: list.reduce((s, f) => s + (f.failures24h ?? 0), 0),
      active: list.reduce((s, f) => s + (f.activeRuns ?? 0), 0),
    }),
    [list],
  );

  let body: ReactNode;
  if (flowsError && !flows) {
    body = <ErrorPanel error={flowsError} onRetry={onRetryFlows} />;
  } else if (flows && flows.length === 0) {
    body = (
      <OverviewEmpty
        icon={<Workflow />}
        title={t("No flows yet")}
        description={t(
          "Describe what should happen, or start from a template — the first flow takes a minute.",
        )}
        action={
          <div className="flex gap-2">
            <Button size="sm" onClick={() => setDescribeOpen(true)}>
              <Sparkles size={14} /> {t("Describe it")}
            </Button>
            <Button size="sm" variant="outline" onClick={() => navigate("/flows/templates")}>
              {t("Browse templates")}
            </Button>
          </div>
        }
      />
    );
  } else if (stats.error && !stats.data) {
    body = <ErrorPanel error={stats.error} onRetry={() => void stats.reload()} />;
  } else if (!stats.data || !flows) {
    body = (
      <div className="rounded-xl border border-border bg-surface p-5 lg:flex-1">
        <LoadingRows rows={5} />
      </div>
    );
  } else if (stats.data.totals.runs === 0) {
    body = (
      <OverviewEmpty
        icon={<BarChart3 />}
        title={t("No runs {period}", { period: periodPhrase(range) })}
        description={t(
          "Charts fill in as your flows fire. Test one from the editor to see a run and its trace right away.",
        )}
        action={
          <Button size="sm" variant="outline" onClick={() => navigate("/flows/list")}>
            {t("Open flows")}
          </Button>
        }
      />
    );
  } else {
    body = <OverviewCharts stats={stats.data} range={range} catalogue={catalogue} />;
  }

  return (
    <div className="flex flex-1 flex-col gap-4 lg:min-h-0">
      <KpiRow className="shrink-0">
        <Kpi
          label={t("Automations on")}
          value={kpis.enabled}
          detail={tn(list.length, "{count} flow in total", "{count} flows in total")}
          icon={<Workflow size={17} />}
          tone="primary"
        />
        <Kpi
          label={t("Runs in 24 h")}
          value={kpis.runs24h}
          detail={
            kpis.active
              ? tn(kpis.active, "{count} run in progress now", "{count} runs in progress now")
              : t("Nothing running right now")
          }
          icon={<Activity size={17} />}
          tone="info"
        />
        <Kpi
          label={t("Failures in 24 h")}
          value={kpis.failures24h}
          detail={
            kpis.failures24h ? t("Open Runs to see what went wrong") : t("Everything ran cleanly")
          }
          icon={<TriangleAlert size={17} />}
          tone={kpis.failures24h ? "danger" : "success"}
        />
        <Kpi
          label={t("Watching for")}
          value={t("Events, schedules & hooks")}
          detail={
            catalogue
              ? tn(
                  catalogue.events.length,
                  "{count} event type across the server",
                  "{count} event types across the server",
                )
              : undefined
          }
          icon={<Sparkles size={17} />}
          tone="success"
        />
      </KpiRow>
      {body}
      <DescribeFlowDialog
        open={describeOpen}
        onClose={() => setDescribeOpen(false)}
        onDraft={(draft) => navigate("/flows/list/new", { state: { draft } })}
      />
    </div>
  );
}

function OverviewEmpty(props: {
  icon: ReactNode;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="grid place-items-center rounded-xl border border-border bg-surface py-10 lg:min-h-[260px] lg:flex-1">
      <EmptyState {...props} />
    </div>
  );
}

/** Desktop windows too short for two rows of side panels (e.g. 1280×720). */
const SHORT_DESKTOP = "(min-width: 64rem) and (max-height: 859.98px)";

function OverviewCharts({
  stats,
  range,
  catalogue,
}: {
  stats: RunStats;
  range: StatsRange;
  catalogue: Catalogue | undefined;
}) {
  const short = useMediaQuery(SHORT_DESKTOP);
  const [topBy, setTopBy] = useState<"runs" | "failed">("runs");
  const [mostOf, setMostOf] = useState<"flows" | "triggers">("flows");
  const runs = (v: number) => formatNumber(v, 0);
  const rows = stats.buckets.map((b) => ({
    key: b.start,
    label: bucketLabel(b.start, range),
    values: [b.succeeded, b.failed, b.skipped],
  }));
  const byFailures = topBy === "failed" && !short;
  const topFlows = (
    byFailures
      ? stats.flows.filter((f) => f.failed > 0).sort((a, b) => b.failed - a.failed)
      : stats.flows
  ).map((f) => ({
    key: f.flowId,
    label: f.name || t("Deleted flow"),
    value: byFailures ? f.failed : f.runs,
    detail: byFailures
      ? tn(f.runs, "{count} run", "{count} runs")
      : f.failed
        ? tn(f.failed, "{count} failed", "{count} failed")
        : undefined,
  }));
  const triggers = stats.triggers.map((tr, i) => ({
    key: `${tr.kind}:${tr.eventType ?? ""}:${i}`,
    label: triggerLabel(tr.kind, tr.eventType, catalogue),
    value: tr.runs,
  }));
  const timed = stats.buckets.some((b) => b.avgDurationMs > 0);
  const noRunsYet = t("No runs {period}", { period: periodPhrase(range) });

  const runsPanel = (
    <DashboardPanel
      title={t("Runs over time")}
      icon={<BarChart3 size={16} />}
      className="h-80 lg:h-auto"
      actions={
        <span className="truncate text-xs tabular-nums text-fg-muted">
          {tn(stats.totals.runs, "{count} run", "{count} runs")}
          {stats.totals.active
            ? ` · ${t("{count} in progress", { count: stats.totals.active })}`
            : ""}
          {short && stats.totals.avgDurationMs > 0
            ? ` · ${t("avg {duration}", { duration: formatRunDuration(stats.totals.avgDurationMs) })}`
            : ""}
        </span>
      }
    >
      <StackedSeriesColumns
        fill
        ariaLabel={t("Runs over time by outcome")}
        data={rows}
        series={OUTCOMES()}
        valueFormat={runs}
      />
    </DashboardPanel>
  );

  const failuresPanel = (
    <DashboardPanel
      title={t("Recent failures")}
      icon={<TriangleAlert size={16} />}
      className="h-64 lg:h-auto"
      actions={
        stats.totals.failed ? (
          <Link
            to="/flows/runs?status=failed"
            className="shrink-0 text-xs font-medium text-primary hover:underline"
          >
            {t("All failures")}
          </Link>
        ) : null
      }
    >
      <RecentFailures stats={stats} range={range} />
    </DashboardPanel>
  );

  if (short) {
    // One row: runs, what ran most (flows or triggers), recent failures.
    return (
      <div className="grid min-h-[240px] flex-1 grid-cols-[minmax(0,1.45fr)_minmax(0,1fr)_minmax(0,1fr)] grid-rows-[minmax(0,1fr)] gap-4">
        {runsPanel}
        <DashboardPanel
          title={t("Ran most")}
          icon={mostOf === "flows" ? <Workflow size={16} /> : <Zap size={16} />}
          actions={
            <Segmented
              ariaLabel={t("Show")}
              size="sm"
              value={mostOf}
              onChange={setMostOf}
              options={[
                { value: "flows", label: t("Flows") },
                { value: "triggers", label: t("Triggers") },
              ]}
            />
          }
        >
          {mostOf === "flows" ? (
            <FitRanked
              data={topFlows}
              color="viz-4"
              ariaLabel={t("Flows by runs")}
              valueFormat={runs}
              empty={noRunsYet}
            />
          ) : (
            <FitRanked
              data={triggers}
              color="viz-5"
              ariaLabel={t("Triggers that fired most")}
              valueFormat={runs}
              empty={noRunsYet}
            />
          )}
        </DashboardPanel>
        {failuresPanel}
      </div>
    );
  }

  return (
    <div className="grid gap-4 lg:min-h-[340px] lg:flex-1 lg:grid-cols-[minmax(0,1.55fr)_minmax(0,1fr)_minmax(0,1fr)] lg:grid-rows-[minmax(0,1fr)]">
      {runsPanel}
      <div className="grid gap-4 lg:grid-rows-2">
        <DashboardPanel
          title={t("Top flows")}
          icon={<Workflow size={16} />}
          className="h-64 lg:h-auto"
          actions={
            <Segmented
              ariaLabel={t("Rank flows by")}
              size="sm"
              value={topBy}
              onChange={setTopBy}
              options={[
                { value: "runs", label: t("Runs") },
                { value: "failed", label: t("Failures") },
              ]}
            />
          }
        >
          <FitRanked
            data={topFlows}
            color={byFailures ? "viz-3" : "viz-4"}
            ariaLabel={byFailures ? t("Flows by failures") : t("Flows by runs")}
            valueFormat={runs}
            empty={
              byFailures ? t("No failures {period}", { period: periodPhrase(range) }) : noRunsYet
            }
          />
        </DashboardPanel>
        <DashboardPanel
          title={t("Top triggers")}
          icon={<Zap size={16} />}
          className="h-64 lg:h-auto"
        >
          <FitRanked
            data={triggers}
            color="viz-5"
            ariaLabel={t("Triggers that fired most")}
            valueFormat={(v) => tn(v, "{count} run", "{count} runs")}
            empty={noRunsYet}
          />
        </DashboardPanel>
      </div>
      <div className="grid gap-4 lg:grid-rows-2">
        <DashboardPanel
          title={t("Average run time")}
          icon={<Clock3 size={16} />}
          className="h-64 lg:h-auto"
          bodyClassName="flex min-h-0 flex-1 flex-col gap-2 p-4"
        >
          <div className="flex shrink-0 flex-wrap items-baseline gap-x-2">
            <span className="text-2xl font-semibold tabular-nums text-fg">
              {formatRunDuration(stats.totals.avgDurationMs)}
            </span>
            <span className="truncate text-xs text-fg-muted">
              {t("trigger to last step, waits included")}
            </span>
          </div>
          {timed ? (
            <TimeBars
              fill
              className="min-h-0 flex-1"
              ariaLabel={t("Average run time over time")}
              data={stats.buckets.map((b) => ({
                key: b.start,
                label: bucketLabel(b.start, range),
                value: b.avgDurationMs,
              }))}
              valueFormat={formatRunDuration}
              tickFormat={(v) => (v ? formatRunDuration(v) : "0")}
            />
          ) : null}
        </DashboardPanel>
        {failuresPanel}
      </div>
    </div>
  );
}

const RANK_ROW = 38;

/** Ranked bars cut to the rows that fit the panel, so nothing is clipped. */
function FitRanked({
  data,
  empty,
  ...rest
}: {
  data: { key: string; label: string; value: number; detail?: string }[];
  empty: string;
  ariaLabel: string;
  color: "viz-1" | "viz-2" | "viz-3" | "viz-4" | "viz-5";
  valueFormat: (v: number) => string;
}) {
  const [ref, size] = useElementSize<HTMLDivElement>();
  const fit = Math.max(1, Math.floor(((size.height || 200) + 10) / RANK_ROW));
  return (
    <div ref={ref} className="h-full min-h-0 overflow-hidden">
      <RankedBars
        {...rest}
        data={data.slice(0, fit)}
        empty={<p className="text-sm text-fg-muted">{empty}</p>}
      />
    </div>
  );
}

const FAIL_ROW = 50;

function RecentFailures({ stats, range }: { stats: RunStats; range: StatsRange }) {
  const [ref, size] = useElementSize<HTMLDivElement>();
  const fit = Math.max(1, Math.floor((size.height || 200) / FAIL_ROW));
  const items = stats.recentFailures.slice(0, fit);
  return (
    <div ref={ref} className="h-full min-h-0 overflow-hidden">
      {items.length === 0 ? (
        <p className="text-sm text-fg-muted">
          {t("No failures {period}", { period: periodPhrase(range) })}
        </p>
      ) : (
        <ul className="-my-1.5 divide-y divide-border">
          {items.map((run) => (
            <li key={run.id}>
              <Link
                to={`/flows/runs?run=${encodeURIComponent(run.id)}`}
                className="group flex items-start gap-2.5 py-1.5"
              >
                <FailureIcon icon={TriangleAlert} />
                <span className="min-w-0 flex-1">
                  <span className="flex items-baseline justify-between gap-2">
                    <span className="truncate text-sm font-medium text-fg group-hover:underline">
                      {run.flowName || t("Deleted flow")}
                    </span>
                    <span className="shrink-0 text-xs tabular-nums text-fg-subtle">
                      {relativeTime(run.startedAt)}
                    </span>
                  </span>
                  <span className="block truncate text-xs text-fg-muted" title={run.error}>
                    {run.error || t("Open the trace to see which step failed")}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function FailureIcon({ icon: Icon }: { icon: LucideIcon }) {
  return (
    <span
      aria-hidden
      className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-md bg-danger-subtle text-danger"
    >
      <Icon size={13} />
    </span>
  );
}
