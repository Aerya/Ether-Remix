import { Button, Select, toast } from "@braedonsaunders/appkit-ui";
import { FlaskConical, RotateCcw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { Field } from "../../components/fields";
import { LoadingRows, MutationError } from "../../components/feedback";
import { t } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { flowsApi } from "../../lib/flowsApi";
import { StepTrace } from "./RunTrace";
import type { Catalogue } from "./shared";
import { formatValue } from "./shared";
import type { Graph, TestRunResult } from "./types";

/** Flatten a sample payload into readable `a.b = value` rows for the preview. */
function sampleRows(sample: Record<string, unknown>, prefix = ""): [string, unknown][] {
  const rows: [string, unknown][] = [];
  for (const [k, v] of Object.entries(sample)) {
    if (k === "__eventType") continue;
    const path = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object" && !Array.isArray(v))
      rows.push(...sampleRows(v as Record<string, unknown>, path));
    else rows.push([path, v]);
  }
  return rows;
}

/**
 * Dry-run drawer: pick a sample event, run the flow against it, and read the
 * step-by-step trace — which branch each step took and how every condition
 * rule scored. Nothing is sent; side-effecting actions are simulated.
 */
export function TestRunDrawer({
  open,
  onClose,
  flowId,
  graph,
  catalogue,
}: {
  open: boolean;
  onClose: () => void;
  flowId?: string;
  graph: Graph;
  catalogue: Catalogue;
}) {
  const triggerEvents = useMemo(() => {
    const out = new Set<string>();
    for (const n of graph.nodes) {
      if (n.kind === "trigger.event")
        for (const e of (n.config?.events as string[] | undefined) ?? [])
          if (!e.endsWith(".*")) out.add(e);
      if (n.kind === "trigger.manual" && n.config?.sampleEventType)
        out.add(String(n.config.sampleEventType));
    }
    return [...out];
  }, [graph]);
  const [eventType, setEventType] = useState(triggerEvents[0] ?? "playback.session.started");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState<TestRunResult | null>(null);

  useEffect(() => {
    if (open) {
      setEventType(triggerEvents[0] ?? "playback.session.started");
      setResult(null);
      setError(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const schema = catalogue.eventsByType.get(eventType);
  const rows = schema?.sample ? sampleRows(schema.sample).slice(0, 14) : [];

  const run = async () => {
    setBusy(true);
    setError(null);
    try {
      const req = { dryRun: true, graph, event: { type: eventType } };
      const res = flowId ? await flowsApi.testRun(flowId, req) : await flowsApi.testDraft(req);
      setResult(res);
    } catch (e) {
      setError(e);
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer open={open} onClose={onClose} title={t("Test run")} size="xl">
      <div className="space-y-5">
        <p className="text-sm text-fg-muted">
          {t(
            "Runs the flow against a realistic sample event. Messages aren't sent and streams aren't stopped — you just see exactly what would happen.",
          )}
        </p>
        <Field label={t("Pretend this happened")}>
          <Select
            aria-label={t("Sample event")}
            value={eventType}
            onChange={(e) => {
              setEventType(e.target.value);
              setResult(null);
            }}
          >
            {(triggerEvents.length > 0 ? triggerEvents : catalogue.events.map((e) => e.type)).map(
              (type) => (
                <option key={type} value={type}>
                  {catalogue.eventsByType.get(type)?.label ?? type}
                </option>
              ),
            )}
          </Select>
        </Field>
        {rows.length > 0 ? (
          <div className="rounded-xl border border-border bg-bg-subtle/60 px-3.5 py-3">
            <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-fg-subtle">
              {t("The sample moment")}
            </p>
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
              {rows.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="font-mono text-fg-subtle">{k}</dt>
                  <dd className="min-w-0 truncate text-fg" title={formatValue(v)}>
                    {formatValue(v)}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        ) : null}
        <Button onClick={() => void run()} disabled={busy}>
          {result ? <RotateCcw size={15} /> : <FlaskConical size={15} />}
          {result ? t("Run again") : t("Run the test")}
        </Button>
        <MutationError error={error} />
        {busy ? (
          <LoadingRows rows={3} />
        ) : result ? (
          <StepTrace run={result.run} steps={result.steps} graph={graph} catalogue={catalogue} />
        ) : null}
      </div>
    </Drawer>
  );
}
