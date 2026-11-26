import {
  Alert,
  AlertDescription,
  Badge,
  Input,
  MultiSelect,
  SearchSelect,
  SettingsSection,
  confirmDialog,
  type MultiSelectOption,
} from "@braedonsaunders/appkit-ui";
import { Globe, Home, Lock, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Segmented } from "../../components/fields";
import { useViewers, viewerName } from "../../components/pickers";
import { SettingsRow } from "../../components/SettingsRow";
import { localized, t } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import {
  awaySameAsHome,
  defaultRulePosition,
  deviceFromKey,
  deviceKey,
  deviceLabel,
  fallbackRule,
  fallbackTitle,
  matchesSummary,
  mbps,
  ruleHasLimits,
  ruleLimits,
  ruleOrigin,
  ruleStanding,
  ruleTitle,
  tidyLimits,
  tidyRule,
  type FallbackRow,
  type RuleNames,
  type RuleRef,
} from "../../lib/qualityRules";
import type { DeviceMatch, QualityDevices, QualityRule } from "../../lib/types";
import { librariesApi, type Library } from "../libraries/api";
import { RecordDrawer, TextRow, recordErrors, type Errors } from "../sources/form";
import { useQuality, type Quality } from "./useQuality";

type Mode = "any" | "only" | "except";
type ListKey = "libraries" | "viewers" | "devices";

/** Which rule the drawer edits: a position in the list, or one past the end for a new rule. */
export interface RuleTarget {
  index: number;
  rule: QualityRule;
  /**
   * Where a new rule starts in the list (0 is the top). Left out, it starts
   * above the first catch-all rule: see defaultRulePosition.
   */
  at?: number;
  /** Opened from a library's page: the Library condition starts as only that library. */
  lockLibrary?: number;
  /** Opened from a viewer's page: the Who condition starts as only that viewer. */
  lockViewer?: number;
  /**
   * Set when the drawer edits one of the two rows fixed at the bottom of the
   * list. Their conditions can't change; what they give is edited like any rule.
   */
  fallback?: FallbackRow;
}

/** The drawer target for a rule in the list, a fallback row, or a new rule. */
export function ruleTarget(quality: Quality, ref: RuleRef): RuleTarget | null {
  const rules = quality.rules ?? [];
  if ("fallback" in ref) {
    return { index: -1, rule: fallbackRule(quality, ref.fallback), fallback: ref.fallback };
  }
  if ("rule" in ref) {
    const index = rules.findIndex((r) => r.id === ref.rule);
    return index >= 0 ? { index, rule: rules[index]! } : null;
  }
  return { index: rules.length, rule: { id: "", ...ref.create } };
}

const GROUP_NAMES = localized((): Record<string, string> => ({
  tv: t("TVs & streaming boxes"),
  mobile: t("Phones & tablets"),
  computer: t("Computers & browsers"),
}));

/** The name a device match is shown with; device kinds are translated here. */
export function deviceMatchName(m: DeviceMatch): string {
  return (m.group && GROUP_NAMES[m.group]) || deviceLabel(m);
}

/** Names for a rule's libraries and viewers, with plain words for ones that are gone. */
export function useRuleNames(): RuleNames & { libraries: Library[]; ready: boolean } {
  const { locale } = useLocale();
  const libraries = useAsync((signal) => librariesApi.list(signal), []);
  const { viewers } = useViewers();
  return useMemo(
    () => ({
      libraries: libraries.data ?? [],
      ready: libraries.data !== undefined && viewers !== null,
      library: (id: number) =>
        libraries.data?.find((l) => l.id === id)?.name ??
        (libraries.data ? t("Unavailable library") : "…"),
      viewer: (id: number) => {
        const v = viewers?.find((x) => x.id === id);
        return v ? viewerName(v) : viewers ? t("Unavailable viewer") : "…";
      },
    }),
    [libraries.data, viewers, locale],
  );
}

/** Rule names with device kinds translated, for summaries and chips. */
export function localizedRule(r: QualityRule): QualityRule {
  if (!r.devices?.some((m) => m.group)) return r;
  return { ...r, devices: r.devices.map((m) => ({ ...m, label: deviceMatchName(m) })) };
}

const RESOLUTIONS = localized(() => [
  { value: "0", label: t("No limit") },
  { value: "2160", label: "4K" },
  { value: "1080", label: "1080p" },
  { value: "720", label: "720p" },
  { value: "480", label: "480p" },
]);

const NO_CHANGE = "";

function modeOf(list: unknown[] | null | undefined, except: boolean | undefined): Mode {
  return list?.length ? (except ? "except" : "only") : "any";
}

function multiSelectLabels() {
  return {
    searchPlaceholder: t("Search…"),
    noMatchesLabel: t("No matches"),
    clearLabel: t("Clear"),
    selectAllLabel: t("Select all"),
  };
}

/** Options for the Device picker: kinds, seen devices, apps, then device types. */
function deviceOptions(devices: QualityDevices | null, picked: DeviceMatch[]): MultiSelectOption[] {
  const out: MultiSelectOption[] = [];
  const kinds = t("Kinds");
  for (const g of devices?.groups ?? []) {
    out.push({
      value: `group:${g.id}`,
      label: GROUP_NAMES[g.id] ?? g.name,
      description: g.aliases?.join(", "),
      group: kinds,
    });
  }
  const apps = devices?.apps ?? [];
  const types = devices?.types ?? [];
  const yours = t("Your devices");
  for (const c of devices?.clients ?? []) {
    const app = apps.find((a) => a.id === c.app)?.name;
    const seen = c.lastSeen ? relativeTime(c.lastSeen) : "";
    out.push({
      value: `client:${c.id}`,
      label: c.name,
      description: [app, c.viewer, seen].filter(Boolean).join(" · "),
      group: yours,
    });
  }
  const repeated = new Set(
    apps.filter((a, i) => apps.findIndex((b) => b.name === a.name) !== i).map((a) => a.name),
  );
  const appsGroup = t("Apps");
  for (const a of apps) {
    const protocol = a.protocol ? a.protocol[0]!.toUpperCase() + a.protocol.slice(1) : "";
    out.push({
      value: `app:${a.id}`,
      label: repeated.has(a.name) && protocol ? `${a.name} (${protocol})` : a.name,
      // The devices it runs on, which the search also matches ("iPad").
      description: a.aliases?.join(", "),
      group: appsGroup,
    });
  }
  const typesGroup = t("Device types");
  for (const d of types) {
    out.push({
      value: `type:${d.id}`,
      label: d.name,
      description: d.aliases?.join(", "),
      group: typesGroup,
    });
  }
  // Matches the lists above don't offer (a "name contains" match from an
  // older rule, a device no longer seen) stay shown so they can be removed.
  const other = t("Already in this rule");
  for (const m of picked) {
    const key = deviceKey(m);
    if (key && !out.some((o) => o.value === key)) {
      out.push({ value: key, label: deviceMatchName(m), group: other });
    }
  }
  return out;
}

/**
 * The rule drawer: when a rule applies (library, who, device, where, what)
 * and what it does (a profile, limits, or both). Saves the whole rule list.
 * The two fallback rows at the bottom of the list open here too: the same
 * outcome section, with their fixed condition spelled out instead of pickers.
 */
export function RuleEditor({
  quality,
  commit,
  target,
  onClose,
  onSaved,
  onOpenRule,
}: {
  quality: Quality;
  commit: (next: Quality, message: string) => Promise<void>;
  target: RuleTarget | null;
  onClose: () => void;
  onSaved?: () => void;
  /** Jump to another rule from the "comes first" line. */
  onOpenRule?: (index: number) => void;
}) {
  const names = useRuleNames();
  const { viewers } = useViewers();
  const devices = useAsync((signal) => api.qualityDevices(signal), []);
  const [draft, setDraft] = useState<QualityRule | null>(null);
  const [bitrateText, setBitrateText] = useState("");
  const [modes, setModes] = useState<Record<ListKey, Mode>>({
    libraries: "any",
    viewers: "any",
    devices: "any",
  });
  const [locked, setLocked] = useState({ library: false, viewer: false });
  /** Where the rule sits among the other rules once saved; 0 is the top. */
  const [at, setAt] = useState(0);
  /** The away fallback row only: away plays get exactly what they get at home. */
  const [sameAsHome, setSameAsHome] = useState(false);
  const [errors, setErrors] = useState<Errors>({});
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!target) return;
    const rule = structuredClone(target.rule);
    setDraft(rule);
    setBitrateText(rule.maxBitrateKbps ? String(mbps(rule.maxBitrateKbps)) : "");
    setModes({
      libraries: modeOf(rule.libraries, rule.exceptLibraries),
      viewers: modeOf(rule.viewers, rule.exceptViewers),
      devices: modeOf(rule.devices, rule.exceptDevices),
    });
    setLocked({
      library:
        target.lockLibrary !== undefined &&
        !rule.exceptLibraries &&
        rule.libraries?.length === 1 &&
        rule.libraries[0] === target.lockLibrary,
      viewer:
        target.lockViewer !== undefined &&
        !rule.exceptViewers &&
        rule.viewers?.length === 1 &&
        rule.viewers[0] === target.lockViewer,
    });
    setSameAsHome(target.fallback === "away" && awaySameAsHome(quality));
    const list = quality.rules ?? [];
    setAt(
      target.index < list.length
        ? Math.max(0, target.index)
        : Math.min(target.at ?? defaultRulePosition(list), list.length),
    );
    setErrors({});
    setError(null);
    // Keyed on the target alone: the drawer keeps its draft while the list reloads.
  }, [target]);

  const rules = quality.rules ?? [];
  const profiles = quality.profiles ?? [];
  const index = target?.index ?? rules.length;
  const fallback = target?.fallback;
  const isNew = !fallback && index >= rules.length;
  /** Every rule but this one, in order: `at` is a position among these. */
  const others = isNew || fallback ? rules : rules.filter((_, j) => j !== index);
  /** The number a rule in `others` has in the list as it is now. */
  const numberOf = (i: number) => (isNew || fallback || i < index ? i + 1 : i + 2);
  const set = (patch: Partial<QualityRule>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const setMode = (key: ListKey, mode: Mode) => {
    setModes((m) => ({ ...m, [key]: mode }));
    setErrors((e) => ({ ...e, [key]: "" }));
  };

  /** The draft as it would be saved: a condition set to "any" drops its list. */
  function settled(d: QualityRule): QualityRule {
    return tidyRule({
      ...d,
      libraries: modes.libraries === "any" ? [] : d.libraries,
      exceptLibraries: modes.libraries === "except",
      viewers: modes.viewers === "any" ? [] : d.viewers,
      exceptViewers: modes.viewers === "except",
      devices: modes.devices === "any" ? [] : d.devices,
      exceptDevices: modes.devices === "except",
    });
  }

  async function saveFallback(row: FallbackRow) {
    if (!draft) return;
    const own = row === "home" || !sameAsHome;
    if (own && !draft.profile) {
      setErrors({ profile: t("Choose a profile.") });
      return;
    }
    const limits = own ? tidyLimits(draft) : {};
    setSaving(true);
    setError(null);
    setErrors({});
    try {
      // An empty limits object clears the row's limits (the save is a patch).
      await commit(
        row === "home"
          ? { ...quality, default: draft.profile ?? "", defaultLimits: limits }
          : { ...quality, defaultAway: own ? draft.profile : "", defaultAwayLimits: limits },
        t("Rule saved"),
      );
      onSaved?.();
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }

  async function save() {
    if (!draft) return;
    if (fallback) return saveFallback(fallback);
    const next: Errors = {};
    if (modes.libraries !== "any" && !draft.libraries?.length) {
      next.libraries = t("Choose at least one library, or pick Any library.");
    }
    if (modes.viewers !== "any" && !draft.viewers?.length) {
      next.viewers = t("Choose at least one viewer, or pick Anyone.");
    }
    if (modes.devices !== "any" && !draft.devices?.length) {
      next.devices = t("Choose at least one device, or pick Any device.");
    }
    const rule = settled(draft);
    if (!rule.profile && !ruleHasLimits(rule)) {
      next.profile = t("Choose a profile or set at least one limit.");
    }
    if (Object.keys(next).length) {
      setErrors(next);
      return;
    }
    const list = [...others.slice(0, at), rule, ...others.slice(at)];
    setSaving(true);
    setError(null);
    setErrors({});
    try {
      await commit({ ...quality, rules: list }, isNew ? t("Rule added") : t("Rule saved"));
      onSaved?.();
      onClose();
    } catch (e) {
      setError(e);
      setErrors(recordErrors(e, `quality.rules[${at}].`));
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    const ok = await confirmDialog({
      title: t("Remove rule?"),
      message: t("Plays it matched follow the rules below it, then the defaults."),
      confirmLabel: t("Remove"),
      tone: "danger",
    });
    if (!ok) return;
    setSaving(true);
    setError(null);
    try {
      await commit({ ...quality, rules: rules.filter((_, j) => j !== index) }, t("Rule removed"));
      onSaved?.();
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }

  const lockedChip = (label: string, widen: string, onWiden: () => void) => (
    <Badge variant="info" className="gap-1 py-1">
      {label}
      <button type="button" aria-label={widen} onClick={onWiden} className="rounded-full">
        <X size={12} />
      </button>
    </Badge>
  );

  const modeRow = (
    key: ListKey,
    title: string,
    options: { value: Mode; label: string }[],
    picker: React.ReactNode,
  ) => (
    <SettingsRow
      stacked
      title={title}
      description={errors[key] ? <span className="text-danger">{errors[key]}</span> : undefined}
      control={
        <div className="space-y-2">
          <Segmented
            ariaLabel={title}
            size="sm"
            value={modes[key]}
            options={options}
            onChange={(mode) => setMode(key, mode)}
          />
          {modes[key] === "any" ? null : picker}
        </div>
      }
    />
  );

  const preview = draft ? localizedRule(settled(draft)) : null;
  const standing =
    preview && !fallback
      ? ruleStanding(others.slice(0, at), preview, devices.data ?? null)
      : { shadowedBy: [], overlaps: [] };
  const home = fallbackRule(quality, "home");
  const nameOf = (i: number) => ruleTitle(localizedRule(others[i]!), names);

  /** "Insert before rule N" and "Open rule N" for a rule that decides first. */
  const firstActions = (i: number, open: boolean) => (
    <span className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1">
      <button
        type="button"
        className="text-xs font-medium text-primary underline"
        onClick={() => setAt(i)}
      >
        {isNew
          ? t("Insert before rule {n}", { n: numberOf(i) })
          : t("Move before rule {n}", { n: numberOf(i) })}
      </button>
      {open && onOpenRule ? (
        <button
          type="button"
          className="text-xs font-medium text-primary underline"
          onClick={() => onOpenRule(rules.indexOf(others[i]!))}
        >
          {t("Open rule {n}", { n: numberOf(i) })}
        </button>
      ) : null}
    </span>
  );

  const drawerSummary = fallback ? undefined : preview ? (
    <div className="space-y-2">
      <div
        role="status"
        className="rounded-lg border border-border bg-bg-subtle/50 px-3.5 py-2.5 text-sm text-fg-muted"
      >
        <span className="mr-2 font-medium text-fg">{t("Matches")}</span>
        <span>{matchesSummary(preview, names)}</span>
      </div>
      {standing.shadowedBy.length ? (
        <Alert variant="warning" data-testid="rule-shadowed">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {standing.shadowedBy.length === 1
                ? t(
                    "Rule {n} “{name}” comes first for all of these, so this rule never picks a profile. Its limits still apply.",
                    { n: numberOf(standing.shadowedBy[0]!), name: nameOf(standing.shadowedBy[0]!) },
                  )
                : t(
                    "Rules {numbers} come first for all of these, so this rule never picks a profile. Its limits still apply.",
                    { numbers: standing.shadowedBy.map(numberOf).join(", ") },
                  )}
            </span>
            {firstActions(standing.shadowedBy[0]!, standing.shadowedBy.length === 1)}
          </AlertDescription>
        </Alert>
      ) : null}
      {standing.overlaps.map((i) => (
        <Alert key={others[i]!.id || i} variant="warning">
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {t("Rule {n} “{name}” comes first: those plays get {profile}.", {
                n: numberOf(i),
                name: nameOf(i),
                profile: others[i]!.profile ?? "",
              })}
            </span>
            {firstActions(i, true)}
          </AlertDescription>
        </Alert>
      ))}
    </div>
  ) : undefined;

  const positionOptions = others.length
    ? Array.from({ length: others.length + 1 }, (_, i) => ({
        value: String(i),
        label:
          i === 0
            ? t("Top")
            : i === others.length
              ? t("Bottom")
              : t("Above rule {n}: {name}", { n: numberOf(i), name: nameOf(i) }),
      }))
    : [];

  return (
    <RecordDrawer
      open={target !== null}
      onClose={() => !saving && onClose()}
      title={
        fallback
          ? fallbackTitle(fallback)
          : isNew
            ? t("New rule")
            : preview
              ? ruleTitle(preview, names)
              : t("Rule {n}", { n: index + 1 })
      }
      description={target && !fallback ? ruleOrigin(target.rule) || undefined : undefined}
      summary={drawerSummary}
      onSave={() => void save()}
      saving={saving}
      saveLabel={t("Save rule")}
      onRemove={isNew || fallback ? undefined : () => void remove()}
      removeLabel={t("Remove rule")}
      error={error}
    >
      {draft ? (
        <>
          {fallback ? (
            <SettingsSection title={t("When")}>
              <div className="flex items-start gap-3 px-5 py-4">
                <Lock aria-hidden size={16} className="mt-0.5 shrink-0 text-fg-muted" />
                <div className="space-y-1">
                  <p className="text-sm font-medium text-fg">
                    {fallback === "home"
                      ? t("Applies to everything no rule above caught, at home.")
                      : t("Applies to everything no rule above caught, away from home.")}
                  </p>
                  <p className="text-sm text-fg-muted">
                    {t(
                      "This is always the last row, so it can't be narrowed. To treat a library, a person or a device differently, add a rule above it.",
                    )}
                  </p>
                </div>
              </div>
            </SettingsSection>
          ) : (
            <>
              <SettingsSection title={t("Rule")}>
                <TextRow
                  label={t("Name")}
                  help={t("Optional")}
                  value={draft.name ?? ""}
                  placeholder={t("e.g. Living room in 4K")}
                  error={errors.name}
                  onChange={(v) => set({ name: v })}
                />
                {positionOptions.length ? (
                  <SettingsRow
                    title={t("Position")}
                    description={t("Rules are checked from the top.")}
                    control={
                      <div className="w-72 max-w-full">
                        <SearchSelect
                          {...searchSelectLabels()}
                          value={String(at)}
                          onChange={(v) => setAt(Number(v))}
                          options={positionOptions}
                          ariaLabel={t("Position")}
                        />
                      </div>
                    }
                  />
                ) : null}
              </SettingsSection>

              <SettingsSection title={t("When")}>
                {locked.library && target?.lockLibrary !== undefined ? (
                  <SettingsRow
                    title={t("Library")}
                    control={lockedChip(
                      t("Only {name}", { name: names.library(target.lockLibrary) }),
                      t("Choose other libraries"),
                      () => setLocked((l) => ({ ...l, library: false })),
                    )}
                  />
                ) : (
                  modeRow(
                    "libraries",
                    t("Library"),
                    [
                      { value: "any", label: t("Any library") },
                      { value: "only", label: t("Only…") },
                      { value: "except", label: t("All except…") },
                    ],
                    <MultiSelect
                      {...multiSelectLabels()}
                      ariaLabel={t("Libraries")}
                      placeholder={t("Choose libraries")}
                      value={(draft.libraries ?? []).map(String)}
                      onChange={(v) => set({ libraries: v.map(Number) })}
                      options={[
                        ...names.libraries.map((l) => ({
                          value: String(l.id),
                          label: l.name,
                          description:
                            l.source === "plugin" && l.pluginName
                              ? t("Releases chosen by {plugin}; only limits apply", {
                                  plugin: l.pluginName,
                                })
                              : undefined,
                        })),
                        ...(draft.libraries ?? [])
                          .filter((id) => names.ready && !names.libraries.some((l) => l.id === id))
                          .map((id) => ({ value: String(id), label: t("Unavailable library") })),
                      ]}
                    />,
                  )
                )}
                {locked.viewer && target?.lockViewer !== undefined ? (
                  <SettingsRow
                    title={t("Who")}
                    control={lockedChip(
                      t("Only {name}", { name: names.viewer(target.lockViewer) }),
                      t("Choose other viewers"),
                      () => setLocked((l) => ({ ...l, viewer: false })),
                    )}
                  />
                ) : (
                  modeRow(
                    "viewers",
                    t("Who"),
                    [
                      { value: "any", label: t("Anyone") },
                      { value: "only", label: t("Only…") },
                      { value: "except", label: t("Everyone except…") },
                    ],
                    <MultiSelect
                      {...multiSelectLabels()}
                      ariaLabel={t("Viewers")}
                      placeholder={t("Choose viewers")}
                      value={(draft.viewers ?? []).map(String)}
                      onChange={(v) => set({ viewers: v.map(Number) })}
                      options={[
                        ...(viewers ?? []).map((v) => ({
                          value: String(v.id),
                          label: viewerName(v),
                        })),
                        ...(draft.viewers ?? [])
                          .filter((id) => viewers && !viewers.some((v) => v.id === id))
                          .map((id) => ({ value: String(id), label: t("Unavailable viewer") })),
                      ]}
                    />,
                  )
                )}
                {modeRow(
                  "devices",
                  t("Device"),
                  [
                    { value: "any", label: t("Any device") },
                    { value: "only", label: t("Only…") },
                    { value: "except", label: t("Any except…") },
                  ],
                  (() => {
                    const options = deviceOptions(devices.data ?? null, draft.devices ?? []);
                    return (
                      <MultiSelect
                        {...multiSelectLabels()}
                        searchPlaceholder={t("Search devices, apps, kinds")}
                        ariaLabel={t("Devices")}
                        placeholder={t("Choose devices or apps")}
                        value={(draft.devices ?? []).map(deviceKey)}
                        onChange={(keys) =>
                          set({
                            devices: keys.map(
                              (key) =>
                                (draft.devices ?? []).find((m) => deviceKey(m) === key) ??
                                deviceFromKey(
                                  key,
                                  options.find((o) => o.value === key)?.label ?? "",
                                ),
                            ),
                          })
                        }
                        options={options}
                      />
                    );
                  })(),
                )}
                <SettingsRow
                  title={t("Where")}
                  control={
                    <Segmented
                      ariaLabel={t("Where")}
                      size="sm"
                      value={draft.where ?? ""}
                      options={[
                        { value: "", label: t("Anywhere"), icon: <Globe size={12} aria-hidden /> },
                        {
                          value: "home",
                          label: t("At home"),
                          icon: <Home size={12} aria-hidden />,
                        },
                        { value: "away", label: t("Away"), icon: <Globe size={12} aria-hidden /> },
                      ]}
                      onChange={(where) => set({ where })}
                    />
                  }
                />
                <SettingsRow
                  title={t("What")}
                  control={
                    <Segmented
                      ariaLabel={t("What")}
                      size="sm"
                      value={draft.kind ?? ""}
                      options={[
                        { value: "", label: t("Movies and episodes") },
                        { value: "movie", label: t("Movies") },
                        { value: "episode", label: t("Episodes") },
                      ]}
                      onChange={(kind) => set({ kind })}
                    />
                  }
                />
              </SettingsSection>
            </>
          )}

          <SettingsSection title={t("Then")}>
            {fallback === "away" ? (
              <SettingsRow
                stacked
                title={t("Away from home")}
                control={
                  <div className="space-y-2">
                    <Segmented
                      ariaLabel={t("Away from home")}
                      size="sm"
                      value={sameAsHome ? "same" : "own"}
                      options={[
                        { value: "same", label: t("Same as at home") },
                        { value: "own", label: t("Its own profile and limits") },
                      ]}
                      onChange={(v) => {
                        setSameAsHome(v === "same");
                        setErrors({});
                      }}
                    />
                    {sameAsHome ? (
                      <p className="flex flex-wrap items-center gap-1.5 text-sm text-fg-muted">
                        <span>{t("At home that is")}</span>
                        <span className="font-medium text-fg">{home.profile}</span>
                        {ruleLimits(home).map((limit) => (
                          <Badge key={limit} variant="secondary">
                            {limit}
                          </Badge>
                        ))}
                      </p>
                    ) : null}
                  </div>
                }
              />
            ) : null}
            {fallback === "away" && sameAsHome ? null : (
              <>
                <SettingsRow
                  title={t("Profile")}
                  description={
                    errors.profile ? (
                      <span className="text-danger">{errors.profile}</span>
                    ) : (
                      t("Ranks the releases")
                    )
                  }
                  control={
                    <div className="w-72 max-w-full">
                      <SearchSelect
                        {...searchSelectLabels()}
                        value={draft.profile ?? NO_CHANGE}
                        onChange={(v) => {
                          set({ profile: v });
                          setErrors((e) => ({ ...e, profile: "" }));
                        }}
                        options={[
                          ...(fallback
                            ? []
                            : [
                                {
                                  value: NO_CHANGE,
                                  label: t("No change: only apply the limits below"),
                                },
                              ]),
                          ...profiles.map((p) => ({ value: p.name, label: p.name })),
                          ...(draft.profile && !profiles.some((p) => p.name === draft.profile)
                            ? [{ value: draft.profile, label: draft.profile }]
                            : []),
                        ]}
                        ariaLabel={t("Profile")}
                      />
                    </div>
                  }
                />
                <SettingsRow
                  title={t("Max resolution")}
                  description={
                    errors.maxHeight ? (
                      <span className="text-danger">{errors.maxHeight}</span>
                    ) : undefined
                  }
                  control={
                    <Segmented
                      ariaLabel={t("Max resolution")}
                      size="sm"
                      value={String(draft.maxHeight ?? 0)}
                      options={RESOLUTIONS}
                      onChange={(v) => set({ maxHeight: Number(v) || undefined })}
                    />
                  }
                />
                <SettingsRow
                  title={t("Max bitrate")}
                  description={
                    errors.maxBitrateKbps ? (
                      <span className="text-danger">{errors.maxBitrateKbps}</span>
                    ) : undefined
                  }
                  control={
                    <span className="flex items-center gap-2">
                      <Input
                        aria-label={t("Max bitrate")}
                        type="number"
                        inputMode="decimal"
                        min={0}
                        step="any"
                        placeholder={t("No limit")}
                        value={bitrateText}
                        className="w-24 tabular-nums"
                        onChange={(e) => {
                          const raw = e.currentTarget.value;
                          setBitrateText(raw);
                          if (raw.trim() === "") {
                            set({ maxBitrateKbps: undefined });
                            return;
                          }
                          const n = e.currentTarget.valueAsNumber;
                          if (Number.isFinite(n)) {
                            set({ maxBitrateKbps: n > 0 ? Math.round(n * 1000) : undefined });
                          }
                        }}
                        onBlur={() => {
                          setBitrateText(
                            draft.maxBitrateKbps ? String(mbps(draft.maxBitrateKbps)) : "",
                          );
                        }}
                      />
                      <span className="text-sm text-fg-muted">Mbps</span>
                    </span>
                  }
                />
                <SettingsRow
                  title={t("Audio")}
                  control={
                    <Segmented
                      ariaLabel={t("Audio")}
                      size="sm"
                      value={draft.stereoAudio ? "stereo" : ""}
                      options={[
                        { value: "", label: t("As released") },
                        { value: "stereo", label: t("Stereo") },
                      ]}
                      onChange={(v) => set({ stereoAudio: v === "stereo" || undefined })}
                    />
                  }
                />
              </>
            )}
          </SettingsSection>
        </>
      ) : null}
    </RecordDrawer>
  );
}

/**
 * The rule drawer for other pages (a library's or a viewer's): it loads the
 * rule list itself. Pass ruleId to edit a rule, or leave it out for a new one.
 */
export function RuleDrawer({
  open,
  ruleId,
  fallback,
  preset,
  lockLibrary,
  lockViewer,
  onClose,
  onSaved,
}: {
  open: boolean;
  ruleId?: string;
  /** Edit one of the two fallback rows instead of a rule. */
  fallback?: FallbackRow;
  /** Starting values of a new rule. */
  preset?: Partial<QualityRule>;
  lockLibrary?: number;
  lockViewer?: number;
  onClose: () => void;
  onSaved?: () => void;
}) {
  const { quality, commit } = useQuality();
  const target = useMemo<RuleTarget | null>(() => {
    if (!open || !quality) return null;
    if (fallback) return ruleTarget(quality, { fallback });
    const rules = quality.rules ?? [];
    const at = ruleId ? rules.findIndex((r) => r.id === ruleId) : -1;
    if (at >= 0) return { index: at, rule: rules[at]!, lockLibrary, lockViewer };
    return {
      index: rules.length,
      rule: {
        id: "",
        ...(lockLibrary !== undefined ? { libraries: [lockLibrary] } : {}),
        ...(lockViewer !== undefined ? { viewers: [lockViewer] } : {}),
        ...preset,
      },
      lockLibrary,
      lockViewer,
    };
    // Keyed on what was asked for, so the drawer keeps its rule while the list reloads.
  }, [open, ruleId, fallback, lockLibrary, lockViewer, quality === null]);
  if (!quality) return null;
  return (
    <RuleEditor
      quality={quality}
      commit={commit}
      target={target}
      onClose={onClose}
      onSaved={onSaved}
    />
  );
}
