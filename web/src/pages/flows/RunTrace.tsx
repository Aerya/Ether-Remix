import { Badge, cn } from "@braedonsaunders/appkit-ui";
import { Check, ChevronRight, Minus, X } from "lucide-react";
import { t } from "../../i18n";
import { formatDateTime, relativeTime } from "../../lib/format";
import type { Catalogue } from "./shared";
import { categoryTone, formatValue, iconForNode, RunStatusBadge } from "./shared";
import type { ConditionTrace, Graph, Run, RunStep } from "./types";
import { nodeKindLabel, operatorLabel, outputHandlesForNode } from "./types";

function TraceRows({ trace, catalogue }: { trace: ConditionTrace; catalogue?: Catalogue }) {
  const schemaFor = (field: string) => {
    if (!catalogue) return undefined;
    for (const ev of catalogue.events) {
      const hit = ev.fields.find((f) => f.name === field);
      if (hit) return hit;
    }
    return undefined;
  };
  const labelFor = (field: string) => schemaFor(field)?.label ?? field;
  const valueLabel = (field: string, v: unknown) => {
    const hit = schemaFor(field);
    if (hit?.enum && typeof v === "string") return hit.enum.find((o) => o.value === v)?.label ?? v;
    return formatValue(v);
  };
  return (
    <div className="space-y-1">
      {(trace.rules ?? []).map((r, i) => (
        <div
          key={i}
          className={cn(
            "flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-md px-2 py-1.5 text-xs",
            r.matched ? "bg-success-subtle/60" : "bg-danger-subtle/60",
          )}
        >
          {r.matched ? (
            <Check size={13} className="text-success" />
          ) : (
            <X size={13} className="text-danger" />
          )}
          <span className="font-medium text-fg">{labelFor(r.field)}</span>
          <span className="text-fg-muted">{operatorLabel(r.op)}</span>
          {r.expected !== undefined ? (
            <span className="font-medium text-fg">{valueLabel(r.field, r.expected)}</span>
          ) : null}
          <span className="ml-auto text-fg-muted">
            {t("was")}{" "}
            <span className="font-medium text-fg">
              {r.found ? valueLabel(r.field, r.actual) : t("not set")}
            </span>
          </span>
        </div>
      ))}
      {(trace.groups ?? []).map((g, i) => (
        <div key={i} className="rounded-md border border-border px-2 py-1.5">
          <p className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
            {g.op === "or" ? t("Any of these") : t("All of these")}
          </p>
          <TraceRows trace={g} catalogue={catalogue} />
        </div>
      ))}
    </div>
  );
}

function OutputRows({ output }: { output?: Record<string, unknown> }) {
  if (!output) return null;
  const entries = Object.entries(output).filter(
    ([k]) => k !== "trace" && k !== "matched" && k !== "actionType" && k !== "dryRun",
  );
  if (entries.length === 0) return null;
  return (
    <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
      {entries.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-fg-subtle">{k}</dt>
          <dd className="min-w-0 break-words text-fg">{formatValue(v)}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Step-by-step trace of one run: branch taken, condition checks, outputs, errors. */
export function StepTrace({
  run,
  steps,
  graph,
  catalogue,
}: {
  run: Run;
  steps: RunStep[];
  graph?: Graph;
  catalogue?: Catalogue;
}) {
  const labelOf = (step: RunStep) =>
    graph?.nodes.find((n) => n.id === step.nodeId)?.label || nodeKindLabel(step.nodeKind);
  const actionOf = (step: RunStep) =>
    String(
      (
        graph?.nodes.find((n) => n.id === step.nodeId)?.config as
          Record<string, unknown> | undefined
      )?.actionType ??
        step.output?.actionType ??
        "",
    );
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <RunStatusBadge status={run.status} />
        {run.dryRun ? <Badge variant="secondary">{t("Test run — nothing was sent")}</Badge> : null}
        <span className="text-fg-muted">
          {run.eventType
            ? `${catalogue?.eventsByType.get(run.eventType)?.label ?? run.eventType} · `
            : ""}
          {formatDateTime(run.startedAt)} ({relativeTime(run.startedAt)})
        </span>
        {run.wakeAt ? (
          <span className="text-fg-muted">
            {t("Wakes {time}", { time: relativeTime(run.wakeAt) })}
          </span>
        ) : null}
      </div>
      {run.error ? (
        <p className="rounded-lg bg-danger-subtle px-3 py-2 text-sm text-danger">{run.error}</p>
      ) : null}
      <ol className="relative space-y-3">
        {steps.map((step, idx) => {
          const Icon = iconForNode(step.nodeKind, actionOf(step) || undefined);
          const tone = categoryTone(step.nodeKind);
          const trace = (step.output as { trace?: ConditionTrace } | undefined)?.trace;
          return (
            <li key={`${step.seq}-${step.nodeId}`} className="relative">
              {idx < steps.length - 1 ? (
                <span
                  aria-hidden
                  className="absolute left-[17px] top-10 h-[calc(100%-1.5rem)] w-px bg-border"
                />
              ) : null}
              <div className="flex gap-3">
                <span
                  className={cn(
                    "z-10 grid size-9 shrink-0 place-items-center rounded-full border border-border bg-surface",
                    tone.chip,
                  )}
                >
                  <Icon size={16} />
                </span>
                <div className="min-w-0 flex-1 rounded-xl border border-border bg-surface px-3.5 py-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="text-sm font-medium text-fg">{labelOf(step)}</p>
                    <span className="text-xs text-fg-subtle">{nodeKindLabel(step.nodeKind)}</span>
                    <span className="ml-auto flex items-center gap-2">
                      {step.branch ? (
                        <Badge variant="outline" className="gap-0.5">
                          <ChevronRight size={11} />{" "}
                          {(() => {
                            const node = graph?.nodes.find((n) => n.id === step.nodeId);
                            return node
                              ? (outputHandlesForNode(node).find((h) => h.id === step.branch)
                                  ?.label ?? step.branch)
                              : step.branch;
                          })()}
                        </Badge>
                      ) : null}
                      {step.status === "completed" ? (
                        <Check size={14} className="text-success" />
                      ) : step.status === "failed" ? (
                        <X size={14} className="text-danger" />
                      ) : step.status === "skipped" ? (
                        <Minus size={14} className="text-fg-subtle" />
                      ) : (
                        <span className="size-2 animate-pulse rounded-full bg-primary" />
                      )}
                      <span className="text-xs tabular-nums text-fg-subtle">
                        {step.durationMs} ms
                      </span>
                    </span>
                  </div>
                  {trace ? (
                    <div className="mt-2">
                      <TraceRows trace={trace} catalogue={catalogue} />
                    </div>
                  ) : null}
                  <OutputRows output={step.output} />
                  {step.error ? <p className="mt-2 text-xs text-danger">{step.error}</p> : null}
                </div>
              </div>
            </li>
          );
        })}
        {steps.length === 0 ? (
          <p className="text-sm text-fg-muted">{t("No steps ran yet.")}</p>
        ) : null}
      </ol>
    </div>
  );
}
