import { Button, Checkbox, Input, Select, Switch, cn } from "@braedonsaunders/appkit-ui";
import { EyeOff, Plus, Search, Trash2 } from "lucide-react";
import { useMemo, useState } from "react";
import { Field, NumberInput, Segmented } from "../../components/fields";
import { localized, t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi } from "../libraries/api";
import type { NotificationsData } from "../notifications/types";
import { ConditionBuilder } from "./ConditionBuilder";
import { knownParams } from "./params";
import type { Catalogue } from "./shared";
import { TemplateField } from "./TemplateField";
import type {
  ConditionGroup,
  EventSchema,
  FlowNode,
  ParamSchema,
  PickerFieldGroup,
  SwitchCase,
} from "./types";
import { FLOW_PICKER, actionGroupLabel, describeCron, domainLabel } from "./types";

const DURATION_PRESETS = localized(() => [
  { value: "5m", label: t("5 minutes") },
  { value: "15m", label: t("15 minutes") },
  { value: "30m", label: t("30 minutes") },
  { value: "1h", label: t("1 hour") },
  { value: "6h", label: t("6 hours") },
  { value: "24h", label: t("24 hours") },
  { value: "7d", label: t("7 days") },
]);

function DurationField({
  value,
  onChange,
  label,
  hint,
}: {
  value: string;
  onChange: (v: string) => void;
  label: string;
  hint?: string;
}) {
  return (
    <Field label={label} hint={hint}>
      <div className="space-y-1.5">
        <div className="flex flex-wrap gap-1">
          {DURATION_PRESETS.map((p) => (
            <button
              key={p.value}
              type="button"
              aria-pressed={value === p.value}
              onClick={() => onChange(p.value)}
              className={cn(
                "rounded-full border px-2 py-0.5 text-xs transition-colors",
                value === p.value
                  ? "border-primary bg-primary-subtle text-primary"
                  : "border-border text-fg-muted hover:text-fg",
              )}
            >
              {p.label}
            </button>
          ))}
        </div>
        <Input
          aria-label={label}
          value={value}
          placeholder={t("or type one: 45s, 20m, 3h, 2d")}
          onChange={(e) => onChange(e.target.value)}
          className="w-44 font-mono text-[13px]"
        />
      </div>
    </Field>
  );
}

export function EventPicker({
  events,
  selected,
  onChange,
  label,
}: {
  events: EventSchema[];
  selected: string[];
  onChange: (next: string[]) => void;
  label: string;
}) {
  const [query, setQuery] = useState("");
  const byDomain = useMemo(() => {
    const q = query.trim().toLowerCase();
    const map = new Map<string, EventSchema[]>();
    for (const ev of events) {
      if (q && !ev.label.toLowerCase().includes(q) && !ev.type.includes(q)) continue;
      const list = map.get(ev.domain) ?? [];
      list.push(ev);
      map.set(ev.domain, list);
    }
    return [...map.entries()];
  }, [events, query]);
  const toggle = (type: string, on: boolean) =>
    onChange(on ? [...selected, type] : selected.filter((x) => x !== type));
  return (
    <Field label={label}>
      <div className="overflow-hidden rounded-lg border border-border">
        <label className="flex items-center gap-2 border-b border-border px-2.5 py-2">
          <Search size={14} className="shrink-0 text-fg-subtle" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("Search events…")}
            aria-label={t("Search events")}
            className="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-fg-subtle"
          />
          <span className="text-xs text-fg-subtle">
            {t("{count} chosen", { count: selected.length })}
          </span>
        </label>
        <div className="max-h-72 overflow-y-auto">
          {byDomain.map(([domain, evs]) => (
            <div key={domain} className="py-1">
              <p className="px-3 py-1 text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
                {domainLabel(domain)}
              </p>
              {evs.map((ev) => (
                <label
                  key={ev.type}
                  className="flex cursor-pointer items-start gap-2.5 px-3 py-1.5 hover:bg-surface-hover"
                >
                  <Checkbox
                    className="mt-0.5"
                    checked={selected.includes(ev.type)}
                    onChange={(e) => toggle(ev.type, e.target.checked)}
                    aria-label={ev.label}
                  />
                  <span className="min-w-0">
                    <span className="block text-sm text-fg">
                      {ev.label}
                      {ev.critical ? (
                        <span className="ml-1.5 text-xs font-medium text-danger">
                          {t("critical")}
                        </span>
                      ) : null}
                    </span>
                    <span className="block truncate text-xs text-fg-subtle">
                      {ev.description ?? ev.type}
                    </span>
                  </span>
                </label>
              ))}
            </div>
          ))}
          {byDomain.length === 0 ? (
            <p className="px-3 py-4 text-sm text-fg-muted">{t("No events match.")}</p>
          ) : null}
        </div>
      </div>
    </Field>
  );
}

function ParamEditor({
  param,
  value,
  onChange,
  fieldGroups,
  sample,
  channels,
}: {
  param: ParamSchema;
  value: unknown;
  onChange: (v: unknown) => void;
  fieldGroups: PickerFieldGroup[];
  sample?: Record<string, unknown>;
  channels: { id: string; name: string; kind?: string }[];
}) {
  const label = param.label + (param.required ? " *" : "");
  if (param.picker === FLOW_PICKER.channel) {
    if (param.type === "string_list") {
      const selected = new Set(
        (Array.isArray(value) ? value : []).map(String).filter((id) => id !== "" && id !== "*"),
      );
      return (
        <Field
          label={label}
          hint={
            channels.length === 0
              ? t(
                  "No channels yet — add one under Notifications, or leave empty to use all enabled channels.",
                )
              : t("Leave empty to use all enabled channels.")
          }
        >
          <div className="flex flex-wrap gap-1">
            {channels.map((c) => (
              <button
                key={c.id}
                type="button"
                aria-pressed={selected.has(c.id)}
                onClick={() => {
                  const next = new Set(selected);
                  if (next.has(c.id)) next.delete(c.id);
                  else next.add(c.id);
                  onChange(next.size > 0 ? [...next] : ["*"]);
                }}
                className={cn(
                  "rounded-full border px-2.5 py-1 text-xs transition-colors",
                  selected.has(c.id)
                    ? "border-primary bg-primary-subtle text-primary"
                    : "border-border text-fg-muted hover:text-fg",
                )}
              >
                {c.name || c.id}
              </button>
            ))}
          </div>
        </Field>
      );
    }
    const strValue = String(value ?? "");
    const matching =
      param.key === "brokerId" && channels.some((c) => c.kind === "mqtt")
        ? channels.filter((c) => c.kind === "mqtt")
        : channels;
    if (matching.length > 0 && !strValue.includes("{{")) {
      return (
        <Field label={label} hint={param.description}>
          <Select
            aria-label={param.label}
            value={strValue}
            onChange={(e) => onChange(e.target.value)}
          >
            <option value="">{t("Choose…")}</option>
            {matching.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name || c.id}
              </option>
            ))}
          </Select>
        </Field>
      );
    }
  }
  if (
    param.picker === FLOW_PICKER.library &&
    (param.type === "string" || param.type === "string_list")
  ) {
    return <LibraryParam param={param} label={label} value={value} onChange={onChange} />;
  }
  if (param.type === "bool") {
    return (
      <label className="flex items-center justify-between gap-3 py-1">
        <span className="text-sm text-fg">{param.label}</span>
        <Switch
          aria-label={param.label}
          checked={value === true}
          onChange={(e) => onChange(e.target.checked)}
        />
      </label>
    );
  }
  if (param.type === "enum" && param.enum) {
    return (
      <Field label={label}>
        <Select
          aria-label={param.label}
          value={String(value ?? param.default ?? "")}
          onChange={(e) => onChange(e.target.value)}
        >
          <option value="">{t("Choose…")}</option>
          {param.enum.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label ?? o.value}
            </option>
          ))}
        </Select>
      </Field>
    );
  }
  if (param.type === "duration_ms") {
    return (
      <Field label={label} hint={param.description}>
        <span className="flex items-center gap-1.5">
          <NumberInput
            aria-label={param.label}
            value={Math.round(Number(value ?? param.default ?? 0) / 1000)}
            min={0}
            step={1}
            onChange={(n) => onChange(Math.max(0, Math.round(n)) * 1000)}
            className="w-32"
          />
          <span className="text-xs text-fg-subtle">{t("seconds")}</span>
        </span>
      </Field>
    );
  }
  if (param.type === "object") {
    return (
      <Field label={label} hint={param.description}>
        <KeyValueParam
          value={value}
          onChange={onChange}
          ariaLabel={param.label}
          fieldGroups={fieldGroups}
          sample={sample}
          template={Boolean(param.template)}
        />
      </Field>
    );
  }
  if (param.type === "string_list") {
    const list = Array.isArray(value) ? value.map(String) : [];
    return (
      <Field label={label} hint={param.description ?? t("Separate several with commas.")}>
        <ListInput
          aria-label={param.label}
          value={list}
          onChange={(next) => onChange(next.length ? next : undefined)}
        />
      </Field>
    );
  }
  if (param.type === "int" || param.type === "float") {
    return (
      <Field label={label}>
        <span className="flex items-center gap-1.5">
          <NumberInput
            aria-label={param.label}
            value={Number(value ?? 0)}
            onChange={(n) => onChange(param.type === "int" ? Math.round(n) : n)}
            step={param.type === "int" ? 1 : "any"}
            className="w-32"
          />
          {param.unit ? <span className="text-xs text-fg-subtle">{param.unit}</span> : null}
        </span>
      </Field>
    );
  }
  const text =
    typeof value === "string" ? value : value === undefined || value === null ? "" : String(value);
  if (param.template) {
    return (
      <Field label={label} hint={param.description}>
        <TemplateField
          value={text}
          onChange={onChange}
          fieldGroups={fieldGroups}
          sample={sample}
          multiline={param.key === "message" || param.key === "body" || text.length > 60}
          ariaLabel={param.label}
        />
      </Field>
    );
  }
  return (
    <Field label={label} hint={param.description}>
      <Input aria-label={param.label} value={text} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

let librariesCache: { id: string; name: string }[] | null = null;

/** A library param: the server's real libraries, one or several. */
function LibraryParam({
  param,
  label,
  value,
  onChange,
}: {
  param: ParamSchema;
  label: string;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const libs = useAsync(async () => {
    if (librariesCache) return librariesCache;
    const list = await librariesApi.list();
    librariesCache = list.map((l) => ({ id: String(l.id), name: l.name }));
    return librariesCache;
  }, []);
  const options = libs.data ?? [];
  if (param.type === "string_list") {
    const selected = new Set(Array.isArray(value) ? value.map(String) : []);
    return (
      <Field label={label} hint={param.description}>
        <div className="flex flex-wrap gap-1">
          {options.map((l) => (
            <button
              key={l.id}
              type="button"
              aria-pressed={selected.has(l.id)}
              onClick={() => {
                const next = new Set(selected);
                if (next.has(l.id)) next.delete(l.id);
                else next.add(l.id);
                onChange(next.size ? [...next] : undefined);
              }}
              className={cn(
                "rounded-full border px-2.5 py-1 text-xs transition-colors",
                selected.has(l.id)
                  ? "border-primary bg-primary-subtle text-primary"
                  : "border-border text-fg-muted hover:text-fg",
              )}
            >
              {l.name}
            </button>
          ))}
          {libs.data && options.length === 0 ? (
            <span className="text-sm text-fg-muted">{t("No libraries yet.")}</span>
          ) : null}
        </div>
      </Field>
    );
  }
  const current = String(value ?? "");
  return (
    <Field label={label} hint={param.description}>
      <Select aria-label={param.label} value={current} onChange={(e) => onChange(e.target.value)}>
        <option value="">{t("Choose…")}</option>
        {current && !options.some((l) => l.id === current) ? (
          <option value={current}>{current}</option>
        ) : null}
        {options.map((l) => (
          <option key={l.id} value={l.id}>
            {l.name}
          </option>
        ))}
      </Select>
    </Field>
  );
}

/**
 * Comma-separated text edited as a list; keeps the text while typing. The
 * inspector is keyed by step, so it starts fresh for each step.
 */
function ListInput({
  value,
  onChange,
  ...rest
}: {
  value: string[];
  onChange: (next: string[]) => void;
  "aria-label": string;
}) {
  const [text, setText] = useState(value.join(", "));
  const parsed = (s: string) =>
    s
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean);
  return (
    <Input
      {...rest}
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        onChange(parsed(e.target.value));
      }}
    />
  );
}

/**
 * An object param (embed fields, webhook headers, JSON body) as name/value
 * rows. Values are text (templates allowed); empty rows are dropped.
 */
function KeyValueParam({
  value,
  onChange,
  ariaLabel,
  fieldGroups,
  sample,
  template,
}: {
  value: unknown;
  onChange: (v: unknown) => void;
  ariaLabel: string;
  fieldGroups: PickerFieldGroup[];
  sample?: Record<string, unknown>;
  template: boolean;
}) {
  const fromValue = (): [string, string][] =>
    value && typeof value === "object" && !Array.isArray(value)
      ? Object.entries(value as Record<string, unknown>).map(([k, v]) => [
          k,
          typeof v === "string" ? v : JSON.stringify(v),
        ])
      : [];
  const [rows, setRows] = useState<[string, string][]>(() => {
    const r = fromValue();
    return r.length ? r : [["", ""]];
  });
  const commit = (next: [string, string][]) => {
    setRows(next);
    const obj: Record<string, string> = {};
    for (const [k, v] of next) if (k.trim()) obj[k.trim()] = v;
    onChange(Object.keys(obj).length ? obj : undefined);
  };
  return (
    <div className="space-y-1.5">
      {rows.map(([k, v], i) => (
        <div key={i} className="flex items-start gap-1.5">
          <Input
            aria-label={t("{field} name {n}", { field: ariaLabel, n: i + 1 })}
            value={k}
            placeholder={t("Name")}
            onChange={(e) => commit(rows.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))}
            className="w-28 shrink-0"
          />
          <div className="min-w-0 flex-1">
            {template ? (
              <TemplateField
                value={v}
                onChange={(next) => commit(rows.map((r, j) => (j === i ? [r[0], next] : r)))}
                fieldGroups={fieldGroups}
                sample={sample}
                ariaLabel={t("{field} value {n}", { field: ariaLabel, n: i + 1 })}
              />
            ) : (
              <Input
                aria-label={t("{field} value {n}", { field: ariaLabel, n: i + 1 })}
                value={v}
                placeholder={t("Value")}
                onChange={(e) => commit(rows.map((r, j) => (j === i ? [r[0], e.target.value] : r)))}
              />
            )}
          </div>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={t("Remove row")}
            onClick={() => commit(rows.length > 1 ? rows.filter((_, j) => j !== i) : [["", ""]])}
          >
            <Trash2 size={14} />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setRows([...rows, ["", ""]])}
      >
        <Plus size={13} /> {t("Add row")}
      </Button>
    </div>
  );
}

const SCHEDULE_PRESETS = localized(() => [
  { cron: "0 * * * *", label: t("Every hour") },
  { cron: "0 9 * * *", label: t("Daily at 09:00") },
  { cron: "0 17 * * 5", label: t("Fridays at 17:00") },
  { cron: "0 8 * * 1", label: t("Mondays at 08:00") },
  { cron: "0 6 1 * *", label: t("First of the month at 06:00") },
]);

/** Configuration form for the selected canvas node. */
export function NodeInspector({
  node,
  onChange,
  catalogue,
  fieldGroups,
  sample,
}: {
  node: FlowNode;
  onChange: (next: FlowNode) => void;
  catalogue: Catalogue;
  fieldGroups: PickerFieldGroup[];
  sample?: Record<string, unknown>;
}) {
  const cfg = (node.config ?? {}) as Record<string, unknown>;
  const setCfg = (patch: Record<string, unknown>) =>
    onChange({ ...node, config: { ...cfg, ...patch } });
  const notices = useAsync(
    () => apiFetch<NotificationsData>("/notifications/").catch(() => null),
    [],
  );
  const channels = (notices.data?.channels ?? []).map((c) => ({
    id: c.id,
    name: c.name || c.kind,
    kind: c.kind,
  }));

  return (
    <div className="space-y-4">
      <Field label={t("Step name")}>
        <Input
          aria-label={t("Step name")}
          value={node.label ?? ""}
          placeholder={t("Name this step")}
          onChange={(e) => onChange({ ...node, label: e.target.value })}
        />
      </Field>
      <label className="flex items-center justify-between gap-3">
        <span className="flex items-center gap-2 text-sm text-fg">
          <EyeOff size={14} className="text-fg-subtle" /> {t("Skip this step (disabled)")}
        </span>
        <Switch
          aria-label={t("Skip this step")}
          checked={Boolean(node.disabled)}
          onChange={(e) => onChange({ ...node, disabled: e.target.checked || undefined })}
        />
      </label>

      {node.kind === "trigger.event" ? (
        <>
          <EventPicker
            label={t("Run this flow when…")}
            events={catalogue.events}
            selected={(cfg.events as string[] | undefined) ?? []}
            onChange={(events) => setCfg({ events })}
          />
          <div>
            <p className="mb-1.5 text-sm font-medium text-fg">
              {t("…but only when (optional pre-filter)")}
            </p>
            <ConditionBuilder
              value={(cfg.filter as ConditionGroup | undefined) ?? { op: "and", rules: [] }}
              fieldGroups={fieldGroups}
              onChange={(filter) => setCfg({ filter })}
            />
          </div>
        </>
      ) : null}

      {node.kind === "trigger.schedule" ? (
        <>
          <Segmented
            ariaLabel={t("Schedule kind")}
            value={cfg.cron ? "times" : "interval"}
            options={[
              { value: "interval" as const, label: t("Repeat interval") },
              { value: "times" as const, label: t("Specific times") },
            ]}
            onChange={(mode) =>
              setCfg(
                mode === "interval"
                  ? { every: String(cfg.every ?? "1h"), cron: undefined }
                  : { cron: String(cfg.cron ?? "0 9 * * *"), every: undefined },
              )
            }
          />
          {cfg.cron ? (
            <Field label={t("When")} hint={describeCron(String(cfg.cron))}>
              <div className="space-y-1.5">
                <div className="flex flex-wrap gap-1">
                  {SCHEDULE_PRESETS.map((p) => (
                    <button
                      key={p.cron}
                      type="button"
                      aria-pressed={cfg.cron === p.cron}
                      onClick={() => setCfg({ cron: p.cron })}
                      className={cn(
                        "rounded-full border px-2 py-0.5 text-xs transition-colors",
                        cfg.cron === p.cron
                          ? "border-primary bg-primary-subtle text-primary"
                          : "border-border text-fg-muted hover:text-fg",
                      )}
                    >
                      {p.label}
                    </button>
                  ))}
                </div>
                <Input
                  aria-label={t("Cron expression")}
                  value={String(cfg.cron)}
                  onChange={(e) => setCfg({ cron: e.target.value })}
                  className="w-44 font-mono text-[13px]"
                />
                <p className="text-xs text-fg-subtle">
                  {t("Five fields: minute hour day month weekday.")}
                </p>
              </div>
            </Field>
          ) : (
            <DurationField
              label={t("Run every")}
              value={String(cfg.every ?? "1h")}
              onChange={(every) => setCfg({ every })}
            />
          )}
          <Field
            label={t("Time zone")}
            hint={t("IANA name, e.g. America/New_York. Blank uses the server's.")}
          >
            <Input
              aria-label={t("Time zone")}
              value={String(cfg.timezone ?? "")}
              placeholder="UTC"
              onChange={(e) => setCfg({ timezone: e.target.value })}
              className="w-52"
            />
          </Field>
        </>
      ) : null}

      {node.kind === "trigger.webhook" ? (
        <>
          <p className="text-sm text-fg-muted">
            {t(
              "Other tools can start this flow by calling its private URL. The URL is shown after you save; rotate it from the flow's settings if it ever leaks.",
            )}
          </p>
          <label className="flex items-center justify-between gap-3">
            <span className="text-sm text-fg">{t("Require the secret token")}</span>
            <Switch
              aria-label={t("Require the secret token")}
              checked={cfg.requireAuth !== false}
              onChange={(e) => setCfg({ requireAuth: e.target.checked })}
            />
          </label>
          <Field label={t("Secret header name")}>
            <Input
              aria-label={t("Secret header name")}
              value={String(cfg.secretHeader ?? "")}
              placeholder="X-Ether-Flow-Token"
              onChange={(e) => setCfg({ secretHeader: e.target.value })}
              className="font-mono text-[13px]"
            />
          </Field>
        </>
      ) : null}

      {node.kind === "trigger.manual" ? (
        <>
          <Field label={t("Sample event (for the field pickers)")}>
            <Select
              aria-label={t("Sample event")}
              value={String(cfg.sampleEventType ?? "playback.session.started")}
              onChange={(e) => setCfg({ sampleEventType: e.target.value })}
            >
              {catalogue.events.map((ev) => (
                <option key={ev.type} value={ev.type}>
                  {ev.label}
                </option>
              ))}
            </Select>
          </Field>
          <p className="text-sm text-fg-muted">
            {t("Manual flows run from the “Run now” button on the Flows page.")}
          </p>
        </>
      ) : null}

      {node.kind === "condition" ? (
        <ConditionBuilder
          value={(cfg.condition as ConditionGroup | undefined) ?? { op: "and", rules: [] }}
          fieldGroups={fieldGroups}
          onChange={(condition) => setCfg({ condition })}
        />
      ) : null}

      {node.kind === "logic.switch" ? (
        <SwitchCasesEditor cfg={cfg} setCfg={setCfg} fieldGroups={fieldGroups} />
      ) : null}

      {node.kind === "logic.delay" ? (
        <DurationField
          label={t("Wait for")}
          value={String(cfg.duration ?? "")}
          onChange={(duration) => setCfg({ duration })}
          hint={t("The run sleeps durably — restarts don't lose it.")}
        />
      ) : null}

      {node.kind === "logic.wait_event" ? (
        <>
          <EventPicker
            label={t("Wait for this to happen…")}
            events={catalogue.events}
            selected={(cfg.eventTypes as string[] | undefined) ?? []}
            onChange={(eventTypes) => setCfg({ eventTypes })}
          />
          <label className="flex items-center justify-between gap-3">
            <span className="text-sm text-fg">{t("Same session / title / viewer only")}</span>
            <Switch
              aria-label={t("Match the same session")}
              checked={cfg.matchCorrelation !== false}
              onChange={(e) => setCfg({ matchCorrelation: e.target.checked })}
            />
          </label>
          <DurationField
            label={t("Give up after")}
            value={String(cfg.timeout ?? "15m")}
            onChange={(timeout) => setCfg({ timeout })}
            hint={t("On timeout the run follows the “Timed out” connection.")}
          />
        </>
      ) : null}

      {node.kind === "logic.window" ? (
        <>
          <Field
            label={t("Count per")}
            hint={t("Usually the session, so each stream is counted on its own.")}
          >
            <TemplateField
              value={String(cfg.keyTemplate ?? "")}
              onChange={(keyTemplate) => setCfg({ keyTemplate })}
              fieldGroups={fieldGroups}
              sample={sample}
              ariaLabel={t("Count per")}
            />
          </Field>
          <Field label={t("This many times")}>
            <NumberInput
              aria-label={t("Threshold")}
              value={Number(cfg.threshold ?? 3)}
              min={1}
              step={1}
              onChange={(n) => setCfg({ threshold: Math.max(1, Math.round(n)) })}
              className="w-24"
            />
          </Field>
          <DurationField
            label={t("within")}
            value={String(cfg.window ?? "2m")}
            onChange={(window) => setCfg({ window })}
          />
          <label className="flex items-center justify-between gap-3">
            <span className="text-sm text-fg">{t("Start counting again once reached")}</span>
            <Switch
              aria-label={t("Reset after reaching")}
              checked={cfg.resetOnReach !== false}
              onChange={(e) => setCfg({ resetOnReach: e.target.checked })}
            />
          </label>
        </>
      ) : null}

      {node.kind === "logic.dedupe" ? (
        <>
          <Field label={t("Treat as duplicates when this matches")}>
            <TemplateField
              value={String(cfg.keyTemplate ?? "")}
              onChange={(keyTemplate) => setCfg({ keyTemplate })}
              fieldGroups={fieldGroups}
              sample={sample}
              ariaLabel={t("Duplicate key")}
            />
          </Field>
          <Field label={t("Allow at most")}>
            <span className="flex items-center gap-2 text-sm text-fg-muted">
              <NumberInput
                aria-label={t("Max per cooldown")}
                value={Number(cfg.maxPerWindow ?? 1)}
                min={1}
                step={1}
                onChange={(n) => setCfg({ maxPerWindow: Math.max(1, Math.round(n)) })}
                className="w-24"
              />
              {t("per cooldown")}
            </span>
          </Field>
          <DurationField
            label={t("Cooldown")}
            value={String(cfg.cooldown ?? "30m")}
            onChange={(cooldown) => setCfg({ cooldown })}
          />
        </>
      ) : null}

      {node.kind === "action" ? (
        <ActionEditor
          cfg={cfg}
          setCfg={setCfg}
          catalogue={catalogue}
          fieldGroups={fieldGroups}
          sample={sample}
          channels={channels}
        />
      ) : null}
    </div>
  );
}

function SwitchCasesEditor({
  cfg,
  setCfg,
  fieldGroups,
}: {
  cfg: Record<string, unknown>;
  setCfg: (patch: Record<string, unknown>) => void;
  fieldGroups: PickerFieldGroup[];
}) {
  const cases = (cfg.cases as SwitchCase[] | undefined) ?? [];
  const setCases = (next: SwitchCase[]) => setCfg({ cases: next });
  return (
    <div className="space-y-3">
      <label className="flex items-center justify-between gap-3">
        <span className="text-sm text-fg">{t("Follow every matching branch")}</span>
        <Switch
          aria-label={t("Match all branches")}
          checked={Boolean(cfg.matchAll)}
          onChange={(e) => setCfg({ matchAll: e.target.checked })}
        />
      </label>
      {cases.map((c, i) => (
        <div key={c.key} className="space-y-2 rounded-lg border border-border p-3">
          <div className="flex items-center gap-2">
            <Input
              aria-label={t("Branch name")}
              value={c.label ?? c.key}
              onChange={(e) =>
                setCases(cases.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)))
              }
              className="flex-1"
            />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={t("Remove branch")}
              onClick={() => setCases(cases.filter((_, j) => j !== i))}
            >
              <Trash2 size={14} />
            </Button>
          </div>
          <ConditionBuilder
            value={c.condition}
            fieldGroups={fieldGroups}
            onChange={(condition) =>
              setCases(cases.map((x, j) => (j === i ? { ...x, condition } : x)))
            }
          />
        </div>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          let n = cases.length + 1;
          while (cases.some((c) => c.key === `case_${n}`)) n += 1;
          setCases([
            ...cases,
            { key: `case_${n}`, label: t("Case {n}", { n }), condition: { op: "and", rules: [] } },
          ]);
        }}
      >
        <Plus size={13} /> {t("Add branch")}
      </Button>
      <p className="text-xs text-fg-subtle">{t("Anything else follows the Default connection.")}</p>
    </div>
  );
}

function ActionEditor({
  cfg,
  setCfg,
  catalogue,
  fieldGroups,
  sample,
  channels,
}: {
  cfg: Record<string, unknown>;
  setCfg: (patch: Record<string, unknown>) => void;
  catalogue: Catalogue;
  fieldGroups: PickerFieldGroup[];
  sample?: Record<string, unknown>;
  channels: { id: string; name: string; kind?: string }[];
}) {
  const actionType = String(cfg.actionType ?? "");
  const spec = catalogue.actionsByType.get(actionType);
  const params = (cfg.params ?? {}) as Record<string, unknown>;
  const groups = useMemo(() => {
    const map = new Map<string, typeof catalogue.actions>();
    for (const a of catalogue.actions) {
      const list = map.get(a.group) ?? [];
      list.push(a);
      map.set(a.group, list);
    }
    return [...map.entries()];
  }, [catalogue.actions]);
  return (
    <div className="space-y-4">
      <Field label={t("Do this")}>
        <Select
          aria-label={t("Action")}
          value={actionType}
          onChange={(e) => {
            const next = catalogue.actionsByType.get(e.target.value);
            const defaults: Record<string, unknown> = {};
            for (const p of next?.params ?? [])
              if (p.default !== undefined) defaults[p.key] = p.default;
            setCfg({
              actionType: e.target.value,
              params: { ...defaults, ...knownParams(next, params) },
            });
          }}
        >
          <option value="">{t("Choose an action…")}</option>
          {groups.map(([group, actions]) => (
            <optgroup key={group} label={actionGroupLabel(group)}>
              {actions.map((a) => (
                <option key={a.type} value={a.type}>
                  {a.label}
                </option>
              ))}
            </optgroup>
          ))}
        </Select>
      </Field>
      {spec?.description ? <p className="-mt-2 text-sm text-fg-muted">{spec.description}</p> : null}
      {(spec?.params ?? []).map((param) => (
        <ParamEditor
          key={param.key}
          param={param}
          value={params[param.key]}
          channels={channels}
          fieldGroups={fieldGroups}
          sample={sample}
          onChange={(v) => setCfg({ params: { ...params, [param.key]: v } })}
        />
      ))}
      <label className="flex items-center justify-between gap-3 border-t border-border pt-3">
        <span className="text-sm text-fg">{t("Keep going if this step fails")}</span>
        <Switch
          aria-label={t("Continue on error")}
          checked={Boolean(cfg.continueOnError)}
          onChange={(e) => setCfg({ continueOnError: e.target.checked || undefined })}
        />
      </label>
    </div>
  );
}
