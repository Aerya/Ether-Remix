import { Badge, SearchSelect, SettingsSection, cn } from "@braedonsaunders/appkit-ui";
import {
  CheckCircle2,
  ChevronRight,
  Globe,
  Home,
  MonitorCheck,
  Plug,
  ShieldCheck,
} from "lucide-react";
import { Fragment, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Segmented } from "../../components/fields";
import { Section } from "../../components/layout";
import { useViewers, viewerName } from "../../components/pickers";
import { t } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api } from "../../lib/api";
import { formatNumber } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { fallbackOfRuleId, fallbackTitle, type RuleRef } from "../../lib/qualityRules";
import type {
  QualityAnswer,
  QualityLayer,
  QualitySummaryException,
  QualitySummaryRow,
} from "../../lib/types";
import {
  ExceptionPill,
  SummaryCellView,
  buildSummaryDeviceOptions,
  createRuleResolver,
  summaryKindLabel,
  type OpenRule,
  type RuleResolver,
} from "./qualitySummaryUi";
import { RuleDrawer } from "./RuleDrawer";
import { TitlePicker } from "./Search";

type Where = "home" | "away";

function resolutionName(h: number | undefined): string {
  if (!h) return t("Any resolution");
  return h === 2160 ? t("4K") : `${h}p`;
}

function rateName(kbps: number): string {
  return t("{rate} Mbps", { rate: formatNumber(kbps / 1000, 1) });
}

function explainKindLabel(kind: string | undefined): string {
  if (kind === "movie") return t("Movies");
  if (kind === "episode" || kind === "tv") return t("Episodes");
  return "";
}

/**
 * The device to check one play with, from the Device filter. A kind of device
 * ("Phones & tablets") covers several, so it has no single answer: null.
 */
function explainClientId(device: string): string | null {
  if (!device) return "any";
  if (device.startsWith("group:")) return null;
  if (device.startsWith("client:")) return device.slice("client:".length);
  return device;
}

/** The big line: "1080p · HDR off", plus bitrate/stereo caps when set. */
export function answerHeadline(a: QualityAnswer): string {
  if (a.blocked) return t("Can't play away from home");
  const hdr = a.dolbyVision ? t("HDR and Dolby Vision") : a.hdr ? t("HDR on") : t("HDR off");
  const parts = [resolutionName(a.maxHeight), hdr];
  if (a.bitrateKbps) parts.push(t("Up to {rate}", { rate: rateName(a.bitrateKbps) }));
  if (a.stereo) parts.push(t("Stereo"));
  return parts.join(" · ");
}

export function layerText(l: QualityLayer, _viewer: string, resolveRule?: RuleResolver): string {
  const ref = l.ruleId && resolveRule ? resolveRule(l.ruleId) : undefined;
  const n = ref?.number || l.rule || 0;
  switch (l.kind) {
    case "profile":
      if (l.source === "rule") {
        return n > 0
          ? t("Rule {n} · {profile}", { n, profile: l.profile ?? "" })
          : t("Rule · {profile}", { profile: l.profile ?? "" });
      }
      return [fallbackTitle(l.source === "defaultAway" ? "away" : "home"), l.profile]
        .filter(Boolean)
        .join(" · ");
    case "device":
      if (!l.decisive) return t("No limit");
      return (
        [
          l.maxHeight ? t("Up to {height}", { height: resolutionName(l.maxHeight) }) : "",
          l.hdr === false ? t("No HDR") : l.dolbyVision === false ? t("No Dolby Vision") : "",
        ]
          .filter(Boolean)
          .join(" · ") || t("No limit")
      );
    case "rule-limit": {
      if (l.ignored) return t("Device resolution is fixed");
      const limits = [
        l.maxHeight ? t("up to {height}", { height: resolutionName(l.maxHeight) }) : "",
        l.bitrateKbps ? rateName(l.bitrateKbps) : "",
        l.stereo ? t("Stereo") : "",
      ]
        .filter(Boolean)
        .join(" · ");
      if (!limits) return t("No limit");
      const row = fallbackOfRuleId(l.ruleId);
      if (row) return `${fallbackTitle(row)} · ${limits}`;
      return n > 0 ? t("Rule {n} · {limits}", { n, limits }) : limits;
    }
    case "remote-blocked":
      return t("Away playback disabled");
    case "android-lift":
      return n > 0 ? t("Rule {n} lifts the device limit", { n }) : t("Rule lifts the device limit");
  }
}

/** The rule or fallback row behind a layer, to open in the rule drawer; null for other layers. */
function layerRule(l: QualityLayer): RuleRef | null {
  if (l.kind === "device" || l.kind === "remote-blocked") return null;
  const row = fallbackOfRuleId(l.ruleId);
  if (row) return { fallback: row };
  if (l.kind === "profile" && l.source !== "rule") {
    return { fallback: l.source === "defaultAway" ? "away" : "home" };
  }
  return l.ruleId ? { rule: l.ruleId } : null;
}

function Answer({
  answer,
  viewer,
  resolveRule,
  onOpenRule,
}: {
  answer: QualityAnswer;
  viewer: string;
  resolveRule: RuleResolver;
  onOpenRule: OpenRule;
}) {
  const labels: Record<string, string> = {
    profile: t("Profile"),
    device: t("Device"),
    "rule-limit": t("Rule limit"),
    "android-lift": t("Rule"),
    "remote-blocked": t("Access"),
  };
  const kindText = explainKindLabel(answer.kind);

  return (
    <div className="space-y-5 border-t border-border p-5" aria-live="polite">
      <div className="flex items-center gap-4 rounded-xl border border-primary/20 bg-primary/5 p-5">
        <MonitorCheck size={36} className="shrink-0 text-primary" />
        <div className="space-y-2">
          <p className="text-xs font-medium text-fg-muted">{t("Available quality")}</p>
          {kindText || answer.profile ? (
            <div className="flex flex-wrap items-center gap-2" data-testid="quality-media">
              {kindText ? <Badge variant="secondary">{kindText}</Badge> : null}
              {answer.profile ? <Badge variant="outline">{answer.profile}</Badge> : null}
            </div>
          ) : null}
          <div data-testid="quality-headline" className="flex flex-wrap items-center gap-2">
            <span className="text-3xl font-semibold tracking-tight">
              {answer.blocked ? t("Can't play away from home") : resolutionName(answer.maxHeight)}
            </span>
            {!answer.blocked ? (
              <>
                <Badge variant="secondary">{answer.hdr ? t("HDR") : t("SDR")}</Badge>
                {answer.dolbyVision ? <Badge variant="secondary">{t("Dolby Vision")}</Badge> : null}
                {answer.bitrateKbps ? (
                  <Badge variant="outline">
                    {t("Up to {rate}", { rate: rateName(answer.bitrateKbps) })}
                  </Badge>
                ) : null}
                {answer.stereo ? <Badge variant="outline">{t("Stereo")}</Badge> : null}
              </>
            ) : null}
          </div>
        </div>
      </div>
      <ol className="flex flex-col gap-6 sm:flex-row" aria-label={t("What decided it")}>
        {answer.layers.map((original, i) => {
          const profile = answer.layers.find((step) => step.kind === "profile");
          const hdrCap =
            original.kind === "device" &&
            ((profile?.hdr === true && original.hdr === false) ||
              (profile?.dolbyVision === true && original.dolbyVision === false));
          const l = hdrCap
            ? {
                ...original,
                decisive: true,
                maxHeight: original.decisive ? original.maxHeight : undefined,
              }
            : original;
          return (
            <li
              key={`${l.kind}-${l.ruleId ?? i}`}
              className={cn(
                "relative min-w-0 flex-1 space-y-3 rounded-xl border border-border bg-surface p-4",
                l.ignored && "opacity-60",
                l.decisive && "border-primary/40",
              )}
            >
              {i > 0 ? (
                <ChevronRight
                  aria-hidden
                  size={18}
                  className="absolute -left-6 top-1/2 hidden text-fg-muted sm:block"
                />
              ) : null}
              <div className="flex items-center gap-2 text-sm font-medium">
                <span className="text-fg-muted">{i + 1}</span>
                {labels[l.kind] ?? t("Limit")}
              </div>
              <p className={cn("text-sm text-fg-muted", l.ignored && "line-through")}>
                {layerText(l, viewer, resolveRule)}
              </p>
              {l.decisive ? (
                <Badge variant="info">
                  <ShieldCheck size={12} />
                  {t("Sets the limit")}
                </Badge>
              ) : (
                <CheckCircle2
                  aria-label={t("No additional limit")}
                  size={16}
                  className="text-success"
                />
              )}
              {l.edited ? <Badge variant="secondary">{t("Edited")}</Badge> : null}
              <div>
                {(() => {
                  // A rule's limit row already sets one, binding or not: it is edited.
                  const label = l.decisive || l.kind === "rule-limit" ? t("Edit") : t("Set limit");
                  const ref = layerRule(l);
                  if (ref) {
                    return (
                      <button
                        type="button"
                        className="text-sm text-primary underline"
                        onClick={() => onOpenRule(ref)}
                      >
                        {label}
                      </button>
                    );
                  }
                  return (
                    <Link
                      className="text-sm text-primary underline"
                      to={l.kind === "remote-blocked" ? "/viewers" : "/playback/devices/apps"}
                    >
                      {label}
                    </Link>
                  );
                })()}
              </div>
            </li>
          );
        })}
      </ol>
      <p className="text-xs text-fg-muted">
        {t("Apps can ask for less in their own quality settings.")}
      </p>
    </div>
  );
}

function partitionExceptions(
  rows: QualitySummaryRow[],
  unfiltered: boolean,
  resolveRule: RuleResolver,
): {
  globalExceptions: QualitySummaryException[];
  globalRuleIds: Set<string>;
} {
  if (!unfiltered || rows.length === 0) {
    return { globalExceptions: [], globalRuleIds: new Set() };
  }
  const nonPluginRows = rows.filter((r) => !r.chosenByPlugin);
  const byId = new Map<string, QualitySummaryException>();

  for (const row of rows) {
    for (const ex of row.exceptions) {
      const ref = resolveRule(ex.ruleId);
      const isGlobal = ref.rule
        ? !ref.rule.libraries?.length
        : nonPluginRows.length > 0
          ? nonPluginRows.every((r) => r.exceptions.some((e) => e.ruleId === ex.ruleId))
          : rows.every((r) => r.exceptions.some((e) => e.ruleId === ex.ruleId));
      if (!isGlobal) continue;
      const prev = byId.get(ex.ruleId);
      if (!prev || (!prev.profile && ex.profile)) {
        byId.set(ex.ruleId, ex);
      }
    }
  }
  return {
    globalExceptions: [...byId.values()],
    globalRuleIds: new Set(byId.keys()),
  };
}

/**
 * "Who gets what": pick a viewer and a device once, then read what they get in
 * every library at home and away, and check one play layer by layer. Every
 * rule and fallback row named here opens in the rule drawer, in place.
 */
export function WhoGetsWhat() {
  const { locale } = useLocale();
  const { viewers, error: viewersError } = useViewers();
  const config = useAsync(() => api.getConfig(), []);
  const quality = config.data?.quality ?? null;
  const devices = useAsync((signal) => api.qualityDevices(signal), []);

  // The one question: who, on what. "" is Anyone / Any device.
  const [filterViewer, setFilterViewer] = useState("");
  const [filterDevice, setFilterDevice] = useState("");
  const summary = useAsync(
    (signal) =>
      api.qualitySummary(
        {
          ...(filterViewer ? { viewer: filterViewer } : {}),
          ...(filterDevice ? { device: filterDevice } : {}),
        },
        signal,
      ),
    [filterViewer, filterDevice],
  );

  const resolveRule = useMemo(
    () =>
      createRuleResolver({
        rules: quality?.rules,
        rows: summary.data?.rows,
        viewers,
      }),
    [quality?.rules, summary.data?.rows, viewers, locale],
  );

  const unfiltered = !filterViewer && !filterDevice;
  const { globalExceptions, globalRuleIds } = useMemo(
    () => partitionExceptions(summary.data?.rows ?? [], unfiltered, resolveRule),
    [summary.data?.rows, unfiltered, resolveRule],
  );

  // "Check one play": the same viewer and device, plus where and what.
  const [where, setWhere] = useState<Where>("home");
  const [title, setTitle] = useState<{ key: string; name: string } | null>(null);
  const [libraryId, setLibraryId] = useState(0);
  const [kind, setKind] = useState<"movie" | "episode">("movie");

  const viewerId = Number(filterViewer) || 0;
  const clientId = explainClientId(filterDevice);
  const remote = where === "away";
  // Any library, or one holding both movies and episodes, doesn't say which a
  // play is, and rules for only Movies or only Episodes need to know.
  const libraryRow = summary.data?.rows.find((r) => r.libraryId === libraryId);
  const needsKind =
    !title &&
    (libraryId === 0 ||
      !libraryRow ||
      (!libraryRow.chosenByPlugin &&
        (libraryRow.cells.length !== 1 || !libraryRow.cells[0]?.kind)));
  const answer = useAsync<QualityAnswer | null>(
    (signal) =>
      clientId
        ? api.explainQuality(
            {
              ...(viewerId ? { userId: viewerId } : {}),
              clientId,
              ...(title ? { titleKey: title.key } : {}),
              ...(!title && libraryId ? { libraryId } : {}),
              ...(needsKind ? { kind } : {}),
              remote,
            },
            signal,
          )
        : Promise.resolve(null),
    [viewerId, clientId, remote, title?.key, libraryId, needsKind ? kind : ""],
  );
  const viewer = viewers?.find((v) => v.id === viewerId);

  // The rule drawer loads the rule list itself, so it mounts on first use.
  const [rule, setRuleRef] = useState<RuleRef | null>(null);
  const [drawerUsed, setDrawerUsed] = useState(false);
  const setRule = (ref: RuleRef) => {
    setDrawerUsed(true);
    setRuleRef(ref);
  };

  const summaryError = summary.error ?? devices.error ?? viewersError;

  const summaryDeviceOptions = useMemo(
    () => buildSummaryDeviceOptions(devices.data),
    [devices.data, locale],
  );
  const deviceName = summaryDeviceOptions.find((o) => o.value === filterDevice)?.label ?? "";

  const summaryViewerOptions = useMemo(
    () => [
      { value: "", label: t("Anyone") },
      ...(viewers ?? []).map((v) => ({ value: String(v.id), label: viewerName(v) })),
    ],
    [viewers, locale],
  );

  const libraryOptions = useMemo(
    () => [
      { value: "0", label: t("Any library") },
      ...(summary.data?.rows ?? []).map((r) => ({
        value: String(r.libraryId),
        label: r.library,
      })),
    ],
    [summary.data?.rows, locale],
  );

  return (
    <div className="space-y-6">
      <div data-setting="who-gets-what">
        <Section
          title={t("Who gets what")}
          description={t(
            "Choose a viewer and a device to see what they get in each library, at home and away.",
          )}
          bodyClassName="p-0"
          actions={
            viewers && devices.data ? (
              <>
                <div className="w-36 sm:w-44">
                  <SearchSelect
                    {...searchSelectLabels()}
                    value={filterViewer}
                    onChange={setFilterViewer}
                    options={summaryViewerOptions}
                    ariaLabel={t("Filter by viewer")}
                  />
                </div>
                <div className="w-36 sm:w-48">
                  <SearchSelect
                    {...searchSelectLabels()}
                    value={filterDevice}
                    onChange={setFilterDevice}
                    options={summaryDeviceOptions}
                    ariaLabel={t("Filter by device")}
                  />
                </div>
              </>
            ) : null
          }
        >
          {summaryError ? (
            <div className="p-5">
              <ErrorPanel
                error={summaryError}
                onRetry={() => {
                  void summary.reload();
                  void devices.reload();
                }}
              />
            </div>
          ) : !summary.data ? (
            <LoadingRows rows={4} />
          ) : summary.data.rows.length === 0 ? (
            <p className="px-5 py-6 text-sm text-fg-muted">{t("No libraries yet.")}</p>
          ) : (
            <>
              {globalExceptions.length > 0 ? (
                <div
                  data-testid="global-exceptions"
                  className="space-y-2 border-b border-border px-5 py-3.5"
                >
                  <p className="text-xs text-fg-muted">
                    {t("Changes for some people or devices, in every library")}
                  </p>
                  <div className="flex flex-wrap gap-2">
                    {globalExceptions.map((ex) => (
                      <ExceptionPill
                        key={ex.ruleId}
                        exception={ex}
                        resolveRule={resolveRule}
                        onOpenRule={setRule}
                      />
                    ))}
                  </div>
                </div>
              ) : null}

              {/* Desktop summary table */}
              <div className="hidden sm:block">
                <table className="w-full border-collapse text-left text-sm">
                  <thead>
                    <tr className="border-b border-border text-xs font-medium text-fg-muted">
                      <th className="w-[34%] px-5 py-2.5 font-medium">{t("Library")}</th>
                      <th className="w-[33%] px-5 py-2.5 font-medium">
                        <span className="inline-flex items-center gap-1.5">
                          <Home size={13} aria-hidden />
                          {t("At home")}
                        </span>
                      </th>
                      <th className="w-[33%] px-5 py-2.5 font-medium">
                        <span className="inline-flex items-center gap-1.5">
                          <Globe size={13} aria-hidden />
                          {t("Away")}
                        </span>
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {summary.data.rows.map((row) => {
                      const rowExceptions = row.exceptions.filter(
                        (ex) => !globalRuleIds.has(ex.ruleId),
                      );
                      const split = row.cells.length > 1;
                      return (
                        <Fragment key={row.libraryId}>
                          {row.cells.map((pair, idx) => (
                            <tr key={`${row.libraryId}-${pair.kind || idx}`} className="align-top">
                              <td className="px-5 py-3.5">
                                <div className="flex flex-wrap items-baseline gap-1.5">
                                  <span
                                    className={cn(
                                      "font-medium",
                                      split && idx > 0 ? "text-fg-muted" : "text-fg",
                                    )}
                                  >
                                    {row.library}
                                  </span>
                                  {split && pair.kind ? (
                                    <span className="text-xs text-fg-muted">
                                      · {summaryKindLabel(pair.kind)}
                                    </span>
                                  ) : null}
                                </div>
                                {row.chosenByPlugin && idx === 0 ? (
                                  <p className="mt-1 flex items-center gap-1.5 text-xs text-fg-muted">
                                    <Plug size={12} aria-hidden />
                                    {t("Releases chosen by {plugin}", {
                                      plugin: row.plugin || t("plugin"),
                                    })}
                                  </p>
                                ) : null}
                              </td>
                              <td className="px-5 py-3.5">
                                <SummaryCellView
                                  cell={pair.home}
                                  kind={pair.kind}
                                  where="home"
                                  chosenByPlugin={row.chosenByPlugin}
                                  quality={quality}
                                  resolveRule={resolveRule}
                                  onOpenRule={setRule}
                                />
                              </td>
                              <td className="px-5 py-3.5">
                                <SummaryCellView
                                  cell={pair.away}
                                  kind={pair.kind}
                                  where="away"
                                  chosenByPlugin={row.chosenByPlugin}
                                  quality={quality}
                                  resolveRule={resolveRule}
                                  onOpenRule={setRule}
                                />
                              </td>
                            </tr>
                          ))}
                          {rowExceptions.length > 0 ? (
                            <tr className="bg-bg-subtle/40">
                              <td colSpan={3} className="px-5 py-2.5">
                                <div className="flex flex-wrap items-center gap-2">
                                  <span className="text-xs text-fg-muted">{t("Exceptions:")}</span>
                                  {rowExceptions.map((ex) => (
                                    <ExceptionPill
                                      key={ex.ruleId}
                                      exception={ex}
                                      resolveRule={resolveRule}
                                      onOpenRule={setRule}
                                    />
                                  ))}
                                </div>
                              </td>
                            </tr>
                          ) : null}
                        </Fragment>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {/* Mobile stacked cards */}
              <div className="divide-y divide-border sm:hidden">
                {summary.data.rows.map((row) => {
                  const rowExceptions = row.exceptions.filter(
                    (ex) => !globalRuleIds.has(ex.ruleId),
                  );
                  const split = row.cells.length > 1;
                  return (
                    <div key={row.libraryId} className="space-y-3 px-4 py-4">
                      <div>
                        <p className="font-medium text-fg">{row.library}</p>
                        {row.chosenByPlugin ? (
                          <p className="mt-0.5 flex items-center gap-1.5 text-xs text-fg-muted">
                            <Plug size={12} aria-hidden />
                            {t("Releases chosen by {plugin}", {
                              plugin: row.plugin || t("plugin"),
                            })}
                          </p>
                        ) : null}
                      </div>
                      {row.cells.map((pair, idx) => (
                        <div key={`${row.libraryId}-m-${pair.kind || idx}`} className="space-y-3">
                          {split && pair.kind ? (
                            <p className="text-xs font-medium text-fg-muted">
                              {summaryKindLabel(pair.kind)}
                            </p>
                          ) : null}
                          <div className="space-y-1">
                            <p className="text-xs text-fg-muted">{t("At home")}</p>
                            <SummaryCellView
                              cell={pair.home}
                              kind={pair.kind}
                              where="home"
                              chosenByPlugin={row.chosenByPlugin}
                              quality={quality}
                              resolveRule={resolveRule}
                              onOpenRule={setRule}
                            />
                          </div>
                          <div className="space-y-1">
                            <p className="text-xs text-fg-muted">{t("Away")}</p>
                            <SummaryCellView
                              cell={pair.away}
                              kind={pair.kind}
                              where="away"
                              chosenByPlugin={row.chosenByPlugin}
                              quality={quality}
                              resolveRule={resolveRule}
                              onOpenRule={setRule}
                            />
                          </div>
                        </div>
                      ))}
                      {rowExceptions.length > 0 ? (
                        <div className="flex flex-wrap gap-1.5">
                          {rowExceptions.map((ex) => (
                            <ExceptionPill
                              key={ex.ruleId}
                              exception={ex}
                              resolveRule={resolveRule}
                              onOpenRule={setRule}
                            />
                          ))}
                        </div>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </Section>
      </div>

      <SettingsSection
        title={t("Check one play")}
        description={
          clientId
            ? t("What {viewer} gets on {device}, and what decided it.", {
                viewer: viewer ? viewerName(viewer) : t("anyone"),
                device: deviceName && filterDevice ? deviceName : t("any device"),
              })
            : undefined
        }
      >
        {viewersError || devices.error ? (
          <ErrorPanel error={viewersError ?? devices.error} onRetry={() => void devices.reload()} />
        ) : !viewers || !devices.data ? (
          <LoadingRows rows={3} />
        ) : !clientId ? (
          <p className="px-5 py-4 text-sm text-fg-muted">
            {t("{device} covers several devices. Choose one device or app above to check a play.", {
              device: deviceName,
            })}
          </p>
        ) : (
          <>
            <div
              data-setting="check-quality"
              className="flex flex-wrap items-end gap-x-6 gap-y-4 px-5 py-4"
            >
              <div className="space-y-2">
                <p className="text-xs font-medium text-fg-muted">{t("Where")}</p>
                <Segmented
                  ariaLabel={t("Where")}
                  size="sm"
                  value={where}
                  onChange={setWhere}
                  options={[
                    { value: "home", label: t("At home") },
                    { value: "away", label: t("Away") },
                  ]}
                />
              </div>
              <div className="min-w-0 flex-1 basis-64 space-y-2">
                <p className="text-xs font-medium text-fg-muted">
                  {t("Movie or episode (optional)")}
                </p>
                {title ? (
                  <div className="flex h-9 items-center gap-2 text-sm">
                    <span>{title.name}</span>
                    <button
                      type="button"
                      className="text-primary underline"
                      onClick={() => setTitle(null)}
                    >
                      {t("Clear")}
                    </button>
                  </div>
                ) : (
                  <TitlePicker onPick={(key, name) => setTitle({ key, name })} />
                )}
              </div>
              {!title && libraryOptions.length > 1 ? (
                <div className="w-48 max-w-full space-y-2">
                  <p className="text-xs font-medium text-fg-muted">{t("Library")}</p>
                  <SearchSelect
                    {...searchSelectLabels()}
                    value={String(libraryId)}
                    onChange={(v) => setLibraryId(Number(v))}
                    options={libraryOptions}
                    ariaLabel={t("Library")}
                  />
                </div>
              ) : null}
              {needsKind ? (
                <div className="space-y-2">
                  <p className="text-xs font-medium text-fg-muted">{t("What")}</p>
                  <Segmented
                    ariaLabel={t("What")}
                    size="sm"
                    value={kind}
                    onChange={setKind}
                    options={[
                      { value: "movie", label: t("Movie") },
                      { value: "episode", label: t("Episode") },
                    ]}
                  />
                </div>
              ) : null}
            </div>
            {answer.error ? (
              <ErrorPanel error={answer.error} onRetry={() => void answer.reload()} />
            ) : answer.data ? (
              <Answer
                answer={answer.data}
                viewer={viewer ? viewerName(viewer) : t("This viewer")}
                resolveRule={resolveRule}
                onOpenRule={setRule}
              />
            ) : (
              <LoadingRows rows={2} />
            )}
          </>
        )}
      </SettingsSection>

      {drawerUsed ? (
        <RuleDrawer
          open={rule !== null}
          ruleId={rule && "rule" in rule ? rule.rule : undefined}
          fallback={rule && "fallback" in rule ? rule.fallback : undefined}
          onClose={() => setRuleRef(null)}
          onSaved={() => {
            void config.reload();
            void summary.reload();
            void answer.reload();
          }}
        />
      ) : null}
    </div>
  );
}
