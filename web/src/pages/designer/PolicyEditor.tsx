import { Input, SearchSelect } from "@braedonsaunders/appkit-ui";
import type { ReactNode } from "react";
import { ChipGroup, Field, Segmented } from "../../components/fields";
import type { Daypart, LayoutWhen, RowPolicy } from "../../lib/contract";
import { t, tx } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { WEEKDAYS, formatHour } from "../../lib/format";
import { DAYPARTS, DEVICES, MONTHS, PRIORITIES, hasWhen } from "../../lib/layout";

/** An optional whole number (empty = unset). */
function OptNumber({
  value,
  onChange,
  label,
  min = 1,
  max = 50,
  placeholder,
  disabled,
}: {
  value: number | undefined;
  onChange: (n: number | undefined) => void;
  label: string;
  min?: number;
  max?: number;
  placeholder?: string;
  disabled?: boolean;
}) {
  return (
    <Input
      type="number"
      inputMode="numeric"
      min={min}
      max={max}
      aria-label={label}
      className="h-8 w-36 tabular-nums"
      disabled={disabled}
      value={value ?? ""}
      placeholder={placeholder ?? "–"}
      onChange={(e) => {
        const n = e.currentTarget.valueAsNumber;
        onChange(Number.isFinite(n) && n >= min ? Math.min(max, Math.round(n)) : undefined);
      }}
    />
  );
}

/** Where a row's schedule lives when it is not the policy's own (a collection's schedule). */
export interface ScheduleSource {
  when: LayoutWhen | undefined;
  onChange: (w: LayoutWhen | undefined) => void;
  /** One short line under the heading, e.g. whose schedule this is. */
  note: ReactNode;
}

/** Grouped plain-language settings for one row policy. */
export function PolicyEditor({
  policy,
  title,
  onChange,
  schedule,
}: {
  policy: RowPolicy;
  title: string;
  onChange: (p: RowPolicy) => void;
  schedule?: ScheduleSource;
}) {
  const when = (schedule ? schedule.when : policy.when) ?? {};
  const setWhen = (patch: Partial<LayoutWhen>) => {
    const next: LayoutWhen = { ...when, ...patch };
    for (const k of Object.keys(next) as (keyof LayoutWhen)[]) {
      const v = next[k];
      if (v === undefined || v === "" || (Array.isArray(v) && v.length === 0)) delete next[k];
    }
    const w = hasWhen(next) ? next : undefined;
    if (schedule) schedule.onChange(w);
    else onChange({ ...policy, when: w });
  };
  const hours = Array.from({ length: 25 }, (_, h) => ({
    value: String(h),
    label: h === 24 ? t("Midnight") : formatHour(h),
  }));
  return (
    <div className="space-y-5 text-xs">
      <section className="space-y-2" aria-label={t("Appearance")}>
        <div>
          <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">
            {t("Appearance")}
          </h3>
          <p className="mt-0.5 text-fg-muted">
            {t("How posters and titles appear when this row is shown.")}
          </p>
        </div>
        <div className="space-y-3.5 rounded-xl border border-border bg-surface p-3.5">
          <div className="space-y-1">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="font-medium text-fg">{t("Play trailer preview on focus")}</span>
              <Segmented
                ariaLabel={t("Play trailer preview on focus")}
                size="sm"
                value={
                  policy.previewOnFocus === undefined
                    ? "default"
                    : policy.previewOnFocus
                      ? "on"
                      : "off"
                }
                onChange={(v) =>
                  onChange({ ...policy, previewOnFocus: v === "default" ? undefined : v === "on" })
                }
                options={[
                  { value: "default", label: t("Default") },
                  { value: "on", label: t("On") },
                  { value: "off", label: t("Off") },
                ]}
              />
            </div>
            <p className="text-fg-muted">
              {t(
                "Vela and Moonfin can play the focused title's trailer. Other apps show wide artwork.",
              )}
            </p>
          </div>
          <div className="space-y-1 border-t border-border-subtle pt-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="font-medium text-fg">{t("Ranked (Top 10)")}</span>
              <Segmented
                ariaLabel={t("Ranked (Top 10)")}
                size="sm"
                value={
                  policy.rankedTop10 === undefined ? "default" : policy.rankedTop10 ? "on" : "off"
                }
                onChange={(v) =>
                  onChange({ ...policy, rankedTop10: v === "default" ? undefined : v === "on" })
                }
                options={[
                  { value: "default", label: t("Default") },
                  { value: "on", label: t("On") },
                  { value: "off", label: t("Off") },
                ]}
              />
            </div>
            <p className="text-fg-muted">{t("Number the first ten posters 1–10.")}</p>
          </div>
          <div className="space-y-1 border-t border-border-subtle pt-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="font-medium text-fg">{t("Titles other rows show")}</span>
              <Segmented
                ariaLabel={t("Titles other rows show")}
                size="sm"
                value={policy.showRepeats ? "keep" : "skip"}
                onChange={(v) =>
                  onChange({ ...policy, showRepeats: v === "keep" ? true : undefined })
                }
                options={[
                  { value: "skip", label: t("Skip") },
                  { value: "keep", label: t("Keep") },
                ]}
              />
            </div>
            <p className="text-fg-muted">
              {t(
                "Skip leaves out titles another row on the page already shows. Keep shows this row in full, like a Top 10 under Trending.",
              )}
            </p>
          </div>
        </div>
      </section>

      <section className="space-y-2" aria-label={t("Position and frequency")}>
        <div>
          <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">
            {t("Position & frequency")}
          </h3>
          <p className="mt-0.5 text-fg-muted">
            {t("Pin this row to a slot, bound where it can land, or cap how often it appears.")}
          </p>
        </div>
        <div className="space-y-3.5 rounded-xl border border-border bg-surface p-3.5">
          <div className="space-y-1.5">
            <p className="font-medium text-fg">{t("Priority")}</p>
            <Segmented
              ariaLabel={t("Priority for {title}", { title })}
              size="sm"
              value={String(policy.priority ?? 1)}
              onChange={(v) =>
                onChange({ ...policy, priority: Number(v) === 1 ? undefined : Number(v) })
              }
              options={PRIORITIES.map((p) => ({ value: String(p.value), label: p.label }))}
            />
          </div>
          <div className="grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2.5 border-t border-border-subtle pt-3">
            <span className="text-fg-muted">{t("Anchor to slot")}</span>
            <OptNumber
              label={t("Anchor slot for {title}", { title })}
              value={policy.slot}
              placeholder={t("None")}
              onChange={(n) =>
                onChange({
                  ...policy,
                  slot: n,
                  status: n ? "always" : policy.status,
                  minSlot: n ? undefined : policy.minSlot,
                  maxSlot: n ? undefined : policy.maxSlot,
                })
              }
            />
            <span className="text-fg-muted">{t("Never above slot")}</span>
            <OptNumber
              label={t("Never above slot for {title}", { title })}
              value={policy.minSlot}
              disabled={Boolean(policy.slot)}
              onChange={(n) => onChange({ ...policy, minSlot: n })}
            />
            <span className="text-fg-muted">{t("Never below slot")}</span>
            <OptNumber
              label={t("Never below slot for {title}", { title })}
              value={policy.maxSlot}
              disabled={Boolean(policy.slot)}
              onChange={(n) => onChange({ ...policy, maxSlot: n })}
            />
            <span className="text-fg-muted">{t("At most per page")}</span>
            <OptNumber
              label={t("Rows per page for {title}", { title })}
              value={policy.maxPerPage}
              max={10}
              placeholder={t("Default")}
              onChange={(n) => onChange({ ...policy, maxPerPage: n })}
            />
            <span className="text-fg-muted">{t("Days a week")}</span>
            <OptNumber
              label={t("Days a week for {title}", { title })}
              value={policy.maxDaysPerWeek}
              max={6}
              placeholder={t("Every day")}
              onChange={(n) => onChange({ ...policy, maxDaysPerWeek: n })}
            />
          </div>
        </div>
      </section>

      <fieldset className="space-y-2">
        <legend className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">
          {t("Schedule")}
        </legend>
        <p className="text-fg-muted">{t("Leave unselected to allow any day, time or device.")}</p>
        {schedule ? <p className="text-fg-muted">{schedule.note}</p> : null}
        <div className="space-y-3.5 rounded-xl border border-border bg-surface p-3.5">
          <Field label={t("Days")}>
            <ChipGroup
              ariaLabel={t("Days for {title}", { title })}
              size="sm"
              value={when.days ?? []}
              onChange={(d) => setWhen({ days: [...d].sort() })}
              options={WEEKDAYS.map((d, i) => ({ value: i, label: d }))}
            />
          </Field>
          <Field label={t("Times of day")}>
            <div className="space-y-2.5">
              <ChipGroup
                ariaLabel={t("Dayparts for {title}", { title })}
                size="sm"
                value={when.dayparts ?? []}
                onChange={(d) => setWhen({ dayparts: d as Daypart[] })}
                options={DAYPARTS}
              />
              <div className="grid grid-cols-2 gap-3">
                <Field label={t("From")}>
                  <SearchSelect
                    value={when.fromHour === undefined ? "" : String(when.fromHour)}
                    onChange={(v) => setWhen({ fromHour: v === "" ? undefined : Number(v) })}
                    options={[{ value: "", label: t("Any time") }, ...hours.slice(0, 24)]}
                    ariaLabel={t("From hour for {title}", { title })}
                    {...searchSelectLabels()}
                  />
                </Field>
                <Field label={t("Until")}>
                  <SearchSelect
                    value={when.toHour === undefined ? "" : String(when.toHour)}
                    onChange={(v) => setWhen({ toHour: v === "" ? undefined : Number(v) })}
                    options={[{ value: "", label: t("Any time") }, ...hours.slice(1)]}
                    ariaLabel={t("Until hour for {title}", { title })}
                    {...searchSelectLabels()}
                  />
                </Field>
              </div>
            </div>
          </Field>
          <Field label={t("Devices")}>
            <ChipGroup
              ariaLabel={t("Devices for {title}", { title })}
              size="sm"
              value={when.devices ?? []}
              onChange={(d) => setWhen({ devices: d })}
              options={DEVICES}
            />
          </Field>
          <Field label={t("Months")}>
            <ChipGroup
              ariaLabel={t("Months for {title}", { title })}
              size="sm"
              value={when.months ?? []}
              onChange={(m) => setWhen({ months: [...m].sort((a, b) => a - b) })}
              options={MONTHS.map((m, i) => ({ value: i + 1, label: m }))}
            />
          </Field>
          <Field label={t("Dates")}>
            <div className="flex flex-wrap items-center gap-2 text-fg-muted">
              {tx("Between {start} and {end}", {
                start: (
                  <Input
                    type="date"
                    aria-label={t("Start date for {title}", { title })}
                    className="h-8 w-40"
                    value={when.startDate ?? ""}
                    onChange={(e) => setWhen({ startDate: e.currentTarget.value || undefined })}
                  />
                ),
                end: (
                  <Input
                    type="date"
                    aria-label={t("End date for {title}", { title })}
                    className="h-8 w-40"
                    value={when.endDate ?? ""}
                    onChange={(e) => setWhen({ endDate: e.currentTarget.value || undefined })}
                  />
                ),
              })}
            </div>
          </Field>
        </div>
      </fieldset>
    </div>
  );
}
