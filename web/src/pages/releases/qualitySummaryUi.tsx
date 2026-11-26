import { Badge } from "@braedonsaunders/appkit-ui";
import { Fragment } from "react";
import { viewerName } from "../../components/pickers";
import { t, tx } from "../../i18n";
import type { Viewer } from "../../lib/contract";
import {
  fallbackOfRuleId,
  fallbackRule,
  fallbackTitle,
  mbpsText,
  resolutionName,
  ruleTitle,
  tidyLimits,
  type FallbackRow,
  type RuleNames,
  type RuleRef,
} from "../../lib/qualityRules";
import type {
  EtherConfig,
  QualityDevices,
  QualityLimits,
  QualityRule,
  QualitySummaryCell,
  QualitySummaryException,
  QualitySummaryRow,
} from "../../lib/types";
import { localizedRule } from "./RuleDrawer";

export interface ResolvedRuleRef {
  id: string;
  /** 1-based rule position in `config.quality.rules`, or 0 when unknown. */
  number: number;
  /** Human-readable rule name or condition summary; never a raw rule ID. */
  summary: string;
  rule?: QualityRule;
  /**
   * The libraries the rule names, when it is for only some. kind is set for
   * a library that holds only movies or only episodes.
   */
  libraries?: { id: number; name: string; kind?: "movie" | "episode" }[];
  /** Set when the id names a fallback row ("default" / "defaultAway"), not a rule. */
  fallback?: FallbackRow;
}

/** Opens a rule or a fallback row in the rule drawer, in place. */
export type OpenRule = (ref: RuleRef) => void;

function refOf(ruleId: string): RuleRef {
  const fallback = fallbackOfRuleId(ruleId);
  return fallback ? { fallback } : { rule: ruleId };
}

export type RuleResolver = (ruleId: string | undefined) => ResolvedRuleRef;

export function formatResolutionCap(height: number): string {
  return `≤ ${resolutionName(height)}`;
}

export function formatBitrateCap(kbps: number): string {
  return t("≤ {n} Mbps", { n: mbpsText(kbps) });
}

export function formatExceptionEffect(ex: QualitySummaryException): string {
  const parts: string[] = [];
  if (ex.profile) parts.push(ex.profile);
  if (ex.maxHeight > 0) parts.push(formatResolutionCap(ex.maxHeight));
  if (ex.maxBitrateKbps > 0) parts.push(formatBitrateCap(ex.maxBitrateKbps));
  if (ex.stereo) parts.push(t("Stereo"));
  return parts.join(" · ");
}

export function summaryKindLabel(kind: "" | "movie" | "episode"): string {
  if (kind === "movie") return t("Movies");
  if (kind === "episode") return t("Episodes");
  return "";
}

export function createRuleResolver(opts: {
  rules?: QualityRule[] | null;
  rows?: QualitySummaryRow[] | null;
  viewers?: Viewer[] | null;
  libraryName?: (id: number) => string;
}): RuleResolver {
  const list = opts.rules ?? [];
  const rowLibs = new Map<number, string>();
  const rowKinds = new Map<number, "movie" | "episode">();
  for (const r of opts.rows ?? []) {
    rowLibs.set(r.libraryId, r.library);
    // A row with one cell of a set kind is a movies-only or episodes-only library.
    const only = r.cells.length === 1 ? r.cells[0]!.kind : "";
    if (only) rowKinds.set(r.libraryId, only);
  }
  const names: RuleNames = {
    library: (id) => opts.libraryName?.(id) ?? rowLibs.get(id) ?? t("Unavailable library"),
    viewer: (id) => {
      const v = opts.viewers?.find((x) => x.id === id);
      return v ? viewerName(v) : t("Unavailable viewer");
    },
  };

  return (ruleId) => {
    if (!ruleId) return { id: "", number: 0, summary: t("Rule") };
    const fallback = fallbackOfRuleId(ruleId);
    if (fallback) return { id: ruleId, number: 0, summary: fallbackTitle(fallback), fallback };
    const directIdx = list.findIndex((r) => r.id === ruleId);
    if (directIdx >= 0) {
      const rule = list[directIdx]!;
      return {
        id: ruleId,
        number: directIdx + 1,
        summary: ruleTitle(localizedRule(rule), names),
        rule,
        libraries: rule.exceptLibraries
          ? undefined
          : rule.libraries?.map((id) => ({
              id,
              name: names.library(id),
              kind: rowKinds.get(id),
            })),
      };
    }
    const synthetic = /^r_(\d+)$/.exec(ruleId);
    if (synthetic) {
      const n = Number(synthetic[1]);
      const rule = n >= 1 && n <= list.length ? list[n - 1] : undefined;
      if (rule) {
        return {
          id: ruleId,
          number: n,
          summary: ruleTitle(localizedRule(rule), names),
          rule,
        };
      }
      return { id: ruleId, number: n, summary: t("Rule {n}", { n }) };
    }
    const viewerCap = /^r_viewer_(\d+)$/.exec(ruleId);
    if (viewerCap) {
      const uid = Number(viewerCap[1]);
      return { id: ruleId, number: 0, summary: names.viewer(uid) };
    }
    return { id: ruleId, number: 0, summary: t("Rule") };
  };
}

export function ruleBadgeLabel(ref: ResolvedRuleRef): string {
  if (ref.fallback) return fallbackTitle(ref.fallback);
  return ref.number > 0 ? t("Rule {n}", { n: ref.number }) : t("Rule");
}

function RuleTrigger({
  ruleId,
  label,
  summary,
  onOpenRule,
}: {
  ruleId: string;
  label: string;
  /** What the rule covers, shown on hover: "Phones & tablets · Away". */
  summary?: string;
  onOpenRule: OpenRule;
}) {
  return (
    <button
      type="button"
      title={summary && summary !== label ? summary : undefined}
      onClick={() => onOpenRule(refOf(ruleId))}
      className="text-left text-primary hover:underline"
    >
      {label}
    </button>
  );
}

export function LimitBadges({
  maxHeight,
  maxBitrateKbps,
  stereo,
}: {
  maxHeight: number;
  maxBitrateKbps: number;
  stereo: boolean;
}) {
  if (!maxHeight && !maxBitrateKbps && !stereo) return null;
  return (
    <>
      {maxHeight > 0 ? <Badge variant="outline">{formatResolutionCap(maxHeight)}</Badge> : null}
      {maxBitrateKbps > 0 ? (
        <Badge variant="outline">{formatBitrateCap(maxBitrateKbps)}</Badge>
      ) : null}
      {stereo ? <Badge variant="outline">{t("Stereo")}</Badge> : null}
    </>
  );
}

export function ExceptionPill({
  exception,
  resolveRule,
  onOpenRule,
}: {
  exception: QualitySummaryException;
  resolveRule: RuleResolver;
  onOpenRule: OpenRule;
}) {
  const ref = resolveRule(exception.ruleId);
  const effect = formatExceptionEffect(exception);
  return (
    <button
      type="button"
      onClick={() => onOpenRule(refOf(exception.ruleId))}
      className="inline-flex items-center gap-1.5 rounded-full border border-border bg-bg-subtle px-2.5 py-1 text-left text-xs transition-colors hover:border-primary/40 hover:bg-primary/5"
    >
      <span className="text-fg-muted">{ruleBadgeLabel(ref)}</span>
      <span className="text-fg">{effect ? `${ref.summary} → ${effect}` : ref.summary}</span>
    </button>
  );
}

function RuleLinks({
  ruleIds,
  resolveRule,
  onOpenRule,
}: {
  ruleIds: string[];
  resolveRule: RuleResolver;
  onOpenRule: OpenRule;
}) {
  return (
    <>
      {ruleIds.map((rid, idx) => (
        <Fragment key={rid}>
          {idx > 0 ? <span>,</span> : null}
          <RuleTrigger
            ruleId={rid}
            label={ruleBadgeLabel(resolveRule(rid))}
            summary={resolveRule(rid).summary}
            onOpenRule={onOpenRule}
          />
        </Fragment>
      ))}
    </>
  );
}

type Quality = EtherConfig["quality"];

/** The lower of two ceilings, where 0 is "none". */
function lower(a: number, b: number): number {
  return a && b ? Math.min(a, b) : a || b;
}

/** The limits behind a limit id: a rule's own, or a fallback row's. undefined when not known. */
function limitsOf(ref: ResolvedRuleRef, quality: Quality | null): QualityLimits | undefined {
  if (ref.rule) return tidyLimits(ref.rule);
  if (ref.fallback && quality) return tidyLimits(fallbackRule(quality, ref.fallback));
  return undefined;
}

/** Which of a cell's limit rules set the result ("binding"), and which were looser. */
export function cellLimitSources(
  cell: QualitySummaryCell,
  profileHeight: number,
  resolveRule: RuleResolver,
  quality: Quality | null,
): { binding: { ruleId: string; sets: string[] }[]; looser: string[]; known: boolean } {
  const binding: { ruleId: string; sets: string[] }[] = [];
  const looser: string[] = [];
  // A rule's resolution limit only shows when it is under the profile's own.
  const heightBinds = Boolean(cell.maxHeight) && (!profileHeight || cell.maxHeight < profileHeight);
  const done = { height: !heightBinds, bitrate: !cell.maxBitrateKbps, stereo: !cell.stereo };
  for (const ruleId of cell.limitRuleIds) {
    const limits = limitsOf(resolveRule(ruleId), quality);
    if (!limits) return { binding: [], looser: [], known: false };
    const sets: string[] = [];
    if (!done.height && limits.maxHeight === cell.maxHeight) {
      sets.push(resolutionName(cell.maxHeight));
      done.height = true;
    }
    if (!done.bitrate && limits.maxBitrateKbps === cell.maxBitrateKbps) {
      sets.push(t("{rate} Mbps", { rate: mbpsText(cell.maxBitrateKbps) }));
      done.bitrate = true;
    }
    if (!done.stereo && limits.stereoAudio) {
      sets.push(t("Stereo"));
      done.stereo = true;
    }
    if (sets.length) binding.push({ ruleId, sets });
    else looser.push(ruleId);
  }
  return { binding, looser, known: true };
}

export interface AlsoInEffect {
  ruleId: string;
  /** The other libraries a title must also be in. */
  libraries: string[];
  /** The profile the rule picks for those titles; "" when it only adds a limit. */
  profile: string;
  /** The limits it tightens: "≤ 720p", "≤ 8 Mbps", "Stereo". */
  limits: string[];
}

/**
 * The rules for another library that change what a title in both libraries
 * gets. The server lists every such rule that could match; this keeps only
 * the ones that change the result. A rule below the one that picks this
 * library's profile never picks one, a limit no stricter than the cell's
 * changes nothing, and an episode is never in a movies-only library.
 */
export function alsoInEffects(
  cell: QualitySummaryCell,
  chosenByPlugin: boolean,
  profileHeight: number,
  resolveRule: RuleResolver,
  kind: "" | "movie" | "episode" = "",
): AlsoInEffect[] {
  const pickedAt =
    cell.source === "rule" && cell.profileRuleId
      ? resolveRule(cell.profileRuleId).number || Infinity
      : Infinity;
  const height = lower(profileHeight, cell.maxHeight);
  /** Libraries an earlier rule already picks the profile for. */
  const claimed = new Set<number>();
  const out: AlsoInEffect[] = [];
  const refs = cell.partialRuleIds
    .map((id) => resolveRule(id))
    .filter((ref) => ref.rule && ref.number > 0)
    .sort((a, b) => a.number - b.number);
  for (const ref of refs) {
    const rule = ref.rule!;
    const libraries = (ref.libraries ?? []).filter((l) => !kind || !l.kind || l.kind === kind);
    if (!libraries.length) continue;
    const open = libraries.filter((l) => !claimed.has(l.id));
    const picks = !chosenByPlugin && Boolean(rule.profile) && ref.number < pickedAt;
    const wins = picks && open.length > 0 && rule.profile !== cell.profile;
    if (picks) for (const l of libraries) claimed.add(l.id);
    const limits: string[] = [];
    if (rule.maxHeight && (!height || rule.maxHeight < height)) {
      limits.push(formatResolutionCap(rule.maxHeight));
    }
    if (
      rule.maxBitrateKbps &&
      (!cell.maxBitrateKbps || rule.maxBitrateKbps < cell.maxBitrateKbps)
    ) {
      limits.push(formatBitrateCap(rule.maxBitrateKbps));
    }
    if (rule.stereoAudio && !cell.stereo) limits.push(t("Stereo"));
    if (!wins && !limits.length) continue;
    out.push({
      ruleId: ref.id,
      libraries: (wins ? open : libraries).map((l) => l.name),
      profile: wins ? (rule.profile ?? "") : "",
      limits,
    });
  }
  return out;
}

export function SummaryCellView({
  cell,
  kind = "",
  where,
  chosenByPlugin,
  quality,
  resolveRule,
  onOpenRule,
}: {
  cell: QualitySummaryCell;
  /** Movies or Episodes, when the cell is for only one of them. */
  kind?: "" | "movie" | "episode";
  /** The column the cell is in: names the fallback row when no rule picked the profile. */
  where: FallbackRow;
  chosenByPlugin: boolean;
  /** For the profile's own ceiling and the fallback rows' limits. */
  quality: Quality | null;
  resolveRule: RuleResolver;
  onOpenRule: OpenRule;
}) {
  const hasLimits = Boolean(cell.maxHeight || cell.maxBitrateKbps || cell.stereo);
  if (chosenByPlugin && !hasLimits) {
    return <span className="text-xs text-fg-muted">{t("No limits")}</span>;
  }

  const profileHeight = chosenByPlugin
    ? 0
    : (quality?.profiles?.find((p) => p.name === cell.profile)?.maxHeight ?? 0);
  // What plays, first: the profile's ceiling or a rule's limit, whichever is lower.
  const height = lower(profileHeight, cell.maxHeight);
  const sources = cellLimitSources(cell, profileHeight, resolveRule, quality);
  const alsoIn = alsoInEffects(cell, chosenByPlugin, profileHeight, resolveRule, kind);

  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-1.5" data-testid="cell-result">
        {height ? (
          <span className="text-sm font-medium text-fg">
            {t("Up to {height}", { height: resolutionName(height) })}
          </span>
        ) : chosenByPlugin ? null : (
          <span className="text-sm font-medium text-fg">{t("Any resolution")}</span>
        )}
        <LimitBadges maxHeight={0} maxBitrateKbps={cell.maxBitrateKbps} stereo={cell.stereo} />
      </div>
      {chosenByPlugin ? null : (
        <div className="flex flex-wrap items-center gap-1.5 text-xs" data-testid="cell-profile">
          <span className="text-fg-muted">{t("Profile:")}</span>
          <span className="text-fg">{cell.profile || "—"}</span>
          <span className="text-fg-muted">·</span>
          {cell.source === "rule" && cell.profileRuleId ? (
            <RuleTrigger
              ruleId={cell.profileRuleId}
              label={ruleBadgeLabel(resolveRule(cell.profileRuleId))}
              summary={resolveRule(cell.profileRuleId).summary}
              onOpenRule={onOpenRule}
            />
          ) : (
            <button
              type="button"
              onClick={() => onOpenRule({ fallback: where })}
              className="text-left text-fg-muted hover:text-fg hover:underline"
            >
              {where === "away" && cell.source !== "defaultAway"
                ? t("Everyone else, away: same as at home")
                : fallbackTitle(where)}
            </button>
          )}
        </div>
      )}
      {cell.limitRuleIds.length > 0 ? (
        <div
          className="flex flex-wrap items-center gap-1 text-xs text-fg-muted"
          data-testid="cell-limits"
        >
          {!sources.known ? (
            <>
              <span>{t("Limits from")}</span>
              <RuleLinks
                ruleIds={cell.limitRuleIds}
                resolveRule={resolveRule}
                onOpenRule={onOpenRule}
              />
            </>
          ) : (
            <>
              {sources.binding.length > 0 ? (
                <span>{sources.looser.length > 0 ? t("The strictest:") : t("Limit from")}</span>
              ) : null}
              {sources.binding.map(({ ruleId, sets }, idx) => (
                <Fragment key={ruleId}>
                  {idx > 0 ? <span>·</span> : null}
                  <span>
                    <RuleTrigger
                      ruleId={ruleId}
                      label={ruleBadgeLabel(resolveRule(ruleId))}
                      summary={resolveRule(ruleId).summary}
                      onOpenRule={onOpenRule}
                    />
                    , <span className="text-fg">{sets.join(", ")}</span>
                  </span>
                </Fragment>
              ))}
              {sources.looser.length > 0 ? (
                <>
                  {sources.binding.length > 0 ? <span>·</span> : null}
                  <span>{t("Looser:")}</span>
                  <RuleLinks
                    ruleIds={sources.looser}
                    resolveRule={resolveRule}
                    onOpenRule={onOpenRule}
                  />
                </>
              ) : null}
            </>
          )}
        </div>
      ) : null}
      {alsoIn.map((effect) => {
        const values = {
          library: effect.libraries.join(", "),
          rule: (
            <RuleTrigger
              ruleId={effect.ruleId}
              label={ruleBadgeLabel(resolveRule(effect.ruleId))}
              summary={resolveRule(effect.ruleId).summary}
              onOpenRule={onOpenRule}
            />
          ),
          result: [effect.profile, ...effect.limits].filter(Boolean).join(" · "),
        };
        return (
          <p key={effect.ruleId} className="text-xs text-fg-muted" data-testid="cell-also-in">
            {effect.profile
              ? tx("A title that is also in {library}: {rule} wins, so it gets {result}.", values)
              : tx("A title that is also in {library}: {rule} also limits it to {result}.", values)}
          </p>
        );
      })}
    </div>
  );
}

export function buildSummaryDeviceOptions(
  devices: QualityDevices | null | undefined,
): { value: string; label: string }[] {
  const out: { value: string; label: string }[] = [{ value: "", label: t("Any device") }];
  if (!devices) return out;
  const groupTranslations: Record<string, string> = {
    tv: t("TVs & streaming boxes"),
    mobile: t("Phones & tablets"),
    computer: t("Computers & browsers"),
  };
  for (const g of devices.groups ?? []) {
    out.push({ value: `group:${g.id}`, label: groupTranslations[g.id] ?? g.name });
  }
  for (const c of devices.clients ?? []) {
    out.push({
      value: `client:${c.id}`,
      label: c.viewer ? `${c.name} (${c.viewer})` : c.name,
    });
  }
  const apps = devices.apps ?? [];
  const appNameCounts = new Map<string, number>();
  for (const a of apps) {
    appNameCounts.set(a.name, (appNameCounts.get(a.name) ?? 0) + 1);
  }
  for (const a of apps) {
    const disambiguate = (appNameCounts.get(a.name) ?? 0) > 1 && a.protocol;
    out.push({
      value: `app:${a.id}`,
      label: disambiguate ? `${a.name} (${a.protocol})` : a.name,
    });
  }
  for (const dt of devices.types ?? []) {
    out.push({ value: `type:${dt.id}`, label: dt.name });
  }
  return out;
}
