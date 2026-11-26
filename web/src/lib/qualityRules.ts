import { t } from "../i18n";
import { formatNumber } from "./format";
import type { DeviceMatch, EtherConfig, QualityDevices, QualityLimits, QualityRule } from "./types";

type Quality = EtherConfig["quality"];

/** The names a rule's conditions are shown with. */
export interface RuleNames {
  library: (id: number) => string;
  viewer: (id: number) => string;
}

const DEVICE_FIELDS = ["group", "type", "app", "client", "contains"] as const;
type DeviceField = (typeof DEVICE_FIELDS)[number];

/** "<field>:<id>" for the one field a device match sets. */
export function deviceKey(m: DeviceMatch): string {
  for (const f of DEVICE_FIELDS) if (m[f]) return `${f}:${m[f]}`;
  return "";
}

/** The device match a picker value ("<field>:<id>") stands for. */
export function deviceFromKey(key: string, label: string): DeviceMatch {
  const at = key.indexOf(":");
  const field = key.slice(0, at) as DeviceField;
  return { [field]: key.slice(at + 1), label };
}

export function deviceLabel(m: DeviceMatch): string {
  if (m.label) return m.label;
  if (m.contains) return t("Name contains “{text}”", { text: m.contains });
  return m.group || m.type || m.app || m.client || "";
}

export const resolutionName = (height: number) => (height >= 2160 ? "4K" : `${height}p`);

/** Kbps as Mbps with at most one decimal. */
export const mbps = (kbps: number) => Math.round(kbps / 100) / 10;

/** Kbps as Mbps text with the active language's decimal separator: "4,9" in pt-BR. */
export const mbpsText = (kbps: number) => formatNumber(mbps(kbps), 1);

export function ruleHasLimits(r: QualityLimits): boolean {
  return Boolean(r.maxHeight || r.maxBitrateKbps || r.stereoAudio);
}

/** Only the limits that are set, as the server stores them. */
export function tidyLimits(l: QualityLimits | null | undefined): QualityLimits {
  const out: QualityLimits = {};
  if (l?.maxHeight) out.maxHeight = l.maxHeight;
  if (l?.maxBitrateKbps) out.maxBitrateKbps = l.maxBitrateKbps;
  if (l?.stereoAudio) out.stereoAudio = true;
  return out;
}

/** The limits a rule sets, as short chips: "≤ 1080p", "≤ 8 Mbps", "Stereo". */
export function ruleLimits(r: QualityLimits): string[] {
  return [
    r.maxHeight ? `≤ ${resolutionName(r.maxHeight)}` : "",
    r.maxBitrateKbps ? t("≤ {n} Mbps", { n: mbpsText(r.maxBitrateKbps) }) : "",
    r.stereoAudio ? t("Stereo") : "",
  ].filter(Boolean);
}

export type ConditionKind = "library" | "viewer" | "device" | "where" | "kind";

export interface RuleCondition {
  kind: ConditionKind;
  label: string;
}

/** A rule's conditions in plain words, one per kind it sets. */
export function ruleConditions(r: QualityRule, names: RuleNames): RuleCondition[] {
  const out: RuleCondition[] = [];
  const list = (labels: string[]) => labels.join(", ");
  if (r.libraries?.length) {
    const label = list(r.libraries.map(names.library));
    out.push({
      kind: "library",
      label: r.exceptLibraries ? t("All except {names}", { names: label }) : label,
    });
  }
  if (r.viewers?.length) {
    const label = list(r.viewers.map(names.viewer));
    out.push({
      kind: "viewer",
      label: r.exceptViewers ? t("Everyone except {names}", { names: label }) : label,
    });
  }
  if (r.devices?.length) {
    const label = list(r.devices.map(deviceLabel));
    out.push({
      kind: "device",
      label: r.exceptDevices ? t("Any except {names}", { names: label }) : label,
    });
  }
  if (r.where === "home") out.push({ kind: "where", label: t("At home") });
  if (r.where === "away") out.push({ kind: "where", label: t("Away") });
  if (r.kind === "movie") out.push({ kind: "kind", label: t("Movies") });
  if (r.kind === "episode") out.push({ kind: "kind", label: t("Episodes") });
  return out;
}

/** The rule's own name, or its conditions joined: "Anime · Phones & tablets · Away". */
export function ruleTitle(r: QualityRule, names: RuleNames): string {
  if (r.name?.trim()) return r.name.trim();
  const parts = ruleConditions(r, names).map((c) => c.label);
  return parts.length ? parts.join(" · ") : t("Everything");
}

/** Compact one-line summary for the rule drawer header: "Anime · anyone · any device · anywhere". */
export function matchesSummary(r: QualityRule, names: RuleNames): string {
  const list = (labels: string[]) => labels.join(", ");
  const parts: string[] = [];
  if (r.libraries?.length) {
    const label = list(r.libraries.map(names.library));
    parts.push(r.exceptLibraries ? t("all except {names}", { names: label }) : label);
  } else {
    parts.push(t("every library"));
  }
  if (r.viewers?.length) {
    const label = list(r.viewers.map(names.viewer));
    parts.push(r.exceptViewers ? t("everyone except {names}", { names: label }) : label);
  } else {
    parts.push(t("anyone"));
  }
  if (r.devices?.length) {
    const label = list(r.devices.map(deviceLabel));
    parts.push(
      r.exceptDevices
        ? t("any device except {names}", { names: label })
        : t("on {names}", { names: label }),
    );
  } else {
    parts.push(t("any device"));
  }
  if (r.where === "home") parts.push(t("at home"));
  else if (r.where === "away") parts.push(t("away"));
  else parts.push(t("anywhere"));
  if (r.kind === "movie") parts.push(t("movies"));
  else if (r.kind === "episode") parts.push(t("episodes"));
  return parts.join(" · ");
}

/** The device icon class for a rule's device condition. */
export function deviceIconGroup(
  matches: DeviceMatch[] | null | undefined,
  devices: QualityDevices | null,
): "tv" | "mobile" | "computer" | "any" {
  if (!matches?.length) return "any";
  const groups = new Set<string>();
  for (const m of matches) {
    if (m.group) {
      groups.add(m.group);
    } else if (m.type) {
      const g = devices?.types?.find((x) => x.id === m.type)?.group;
      if (g) groups.add(g);
      else return "any";
    } else if (m.client) {
      const g = devices?.clients?.find((x) => x.id === m.client)?.group;
      if (g) groups.add(g);
      else return "any";
    } else {
      return "any";
    }
  }
  if (groups.size === 1) {
    const only = [...groups][0];
    if (only === "tv" || only === "mobile" || only === "computer") return only;
  }
  return "any";
}

/**
 * Whether list condition a matches everything list condition b matches. An
 * empty list is "any". exclusive is true when a play has exactly one value
 * (one viewer), false when it can have several (a title in many libraries).
 */
function listCovers(
  a: string[],
  aExcept: boolean,
  b: string[],
  bExcept: boolean,
  covered: (value: string) => boolean,
  exclusive: boolean,
): boolean {
  if (!a.length) return true;
  if (!b.length) return false;
  if (!aExcept && !bExcept) return b.every(covered);
  if (aExcept && bExcept) return a.every((v) => b.includes(v));
  if (aExcept && !bExcept) return exclusive && !b.some((v) => a.includes(v));
  return false;
}

/** Whether device key `inner` is covered by device key `outer`. */
function deviceKeyWithin(outer: string, inner: string, devices: QualityDevices | null): boolean {
  if (outer === inner) return true;
  const at = inner.indexOf(":");
  const field = inner.slice(0, at);
  const id = inner.slice(at + 1);
  if (field === "type") {
    const type = devices?.types?.find((x) => x.id === id);
    return Boolean(type && outer === `group:${type.group}`);
  }
  if (field === "client") {
    const client = devices?.clients?.find((x) => x.id === id);
    return Boolean(
      client &&
      ((client.group && outer === `group:${client.group}`) ||
        (client.type && outer === `type:${client.type}`) ||
        (client.app && outer === `app:${client.app}`)),
    );
  }
  if (field === "app") {
    // An app is inside a device type or kind when every device it runs on is.
    const reach = deviceReach(inner, devices);
    const [of] = splitKey(outer);
    if (of === "type") return reach.types?.size === 1 && outer === `type:${[...reach.types][0]}`;
    if (of === "group")
      return reach.groups?.size === 1 && outer === `group:${[...reach.groups][0]}`;
  }
  return false;
}

function splitKey(key: string): [field: string, id: string] {
  const at = key.indexOf(":");
  return [key.slice(0, at), key.slice(at + 1)];
}

/**
 * Where a device condition can match: the apps, device types and kinds of
 * device it is limited to. null is "not limited" (or not known).
 */
interface DeviceReach {
  client?: string;
  apps: Set<string> | null;
  types: Set<string> | null;
  groups: Set<string> | null;
}

function groupsOfTypes(types: Set<string> | null, devices: QualityDevices | null) {
  if (!types) return null;
  const groups = new Set<string>();
  for (const id of types) {
    const group = devices?.types?.find((x) => x.id === id)?.group;
    if (!group) return null;
    groups.add(group);
  }
  return groups;
}

function deviceReach(key: string, devices: QualityDevices | null): DeviceReach {
  const [field, id] = splitKey(key);
  const any: DeviceReach = { apps: null, types: null, groups: null };
  if (field === "group") return { ...any, groups: new Set([id]) };
  if (field === "type") {
    const types = new Set([id]);
    return { ...any, types, groups: groupsOfTypes(types, devices) };
  }
  if (field === "app") {
    const runsOn = devices?.apps?.find((x) => x.id === id)?.devices;
    const types = runsOn?.length ? new Set(runsOn) : null;
    return { apps: new Set([id]), types, groups: groupsOfTypes(types, devices) };
  }
  if (field === "client") {
    const client = devices?.clients?.find((x) => x.id === id);
    return {
      client: id,
      apps: client?.app ? new Set([client.app]) : null,
      types: client?.type ? new Set([client.type]) : null,
      groups: client?.group ? new Set([client.group]) : null,
    };
  }
  return any;
}

/**
 * The catalogue entries a "name contains" match (from a 0.3.20 rule) stands
 * for: a device type it names, or the apps whose name contains it. Empty
 * when it names nothing the catalogue knows, so it may match anything.
 */
function containsStandsFor(text: string, devices: QualityDevices | null): string[] {
  const want = text.trim().toLowerCase();
  if (!want) return [];
  const type = devices?.types?.find(
    (x) => x.id.toLowerCase() === want || x.name.toLowerCase() === want,
  );
  const apps = (devices?.apps ?? [])
    .filter((x) => x.name.toLowerCase().includes(want))
    .map((x) => `app:${x.id}`);
  return type ? [`type:${type.id}`, ...apps] : apps;
}

function disjoint(a: Set<string> | null, b: Set<string> | null): boolean {
  return Boolean(a && b && ![...a].some((v) => b.has(v)));
}

/** Whether rule a matches every play rule b matches. Unsure counts as no. */
function covers(a: QualityRule, b: QualityRule, devices: QualityDevices | null): boolean {
  if (a.where && a.where !== b.where) return false;
  if (a.kind && a.kind !== b.kind) return false;
  const ids = (list: number[] | null | undefined) => (list ?? []).map(String);
  const aLibraries = ids(a.libraries);
  const aViewers = ids(a.viewers);
  if (
    !listCovers(
      aLibraries,
      Boolean(a.exceptLibraries),
      ids(b.libraries),
      Boolean(b.exceptLibraries),
      (v) => aLibraries.includes(v),
      false,
    ) ||
    !listCovers(
      aViewers,
      Boolean(a.exceptViewers),
      ids(b.viewers),
      Boolean(b.exceptViewers),
      (v) => aViewers.includes(v),
      true,
    )
  ) {
    return false;
  }
  const aDevices = (a.devices ?? []).map(deviceKey);
  const bDevices = (b.devices ?? []).map(deviceKey);
  const within = (key: string) => aDevices.some((outer) => deviceKeyWithin(outer, key, devices));
  const exactOnly = [...aDevices, ...bDevices].every((k) => k.startsWith("client:"));
  return listCovers(
    aDevices,
    Boolean(a.exceptDevices),
    bDevices,
    Boolean(b.exceptDevices),
    within,
    exactOnly,
  );
}

function listOverlaps(a: string[], aExcept: boolean, b: string[], bExcept: boolean): boolean {
  if (!a.length || !b.length) return true;
  if (!aExcept && !bExcept) return a.some((v) => b.includes(v));
  if (aExcept && bExcept) return true;
  if (aExcept && !bExcept) return b.some((v) => !a.includes(v));
  return a.some((v) => !b.includes(v));
}

/** Whether two device conditions can both match one device. Unsure counts as yes. */
function deviceKeyOverlaps(a: string, b: string, devices: QualityDevices | null): boolean {
  if (a === b) return true;
  for (const [text, other] of [
    [a, b],
    [b, a],
  ] as const) {
    if (!text.startsWith("contains:")) continue;
    const stands = containsStandsFor(text.slice("contains:".length), devices);
    if (!stands.length) return true;
    return stands.some((key) => deviceKeyOverlaps(key, other, devices));
  }
  const ra = deviceReach(a, devices);
  const rb = deviceReach(b, devices);
  if (ra.client && rb.client) return false;
  return !(
    disjoint(ra.apps, rb.apps) ||
    disjoint(ra.types, rb.types) ||
    disjoint(ra.groups, rb.groups)
  );
}

function devicesOverlap(
  a: string[],
  aExcept: boolean,
  b: string[],
  bExcept: boolean,
  devices: QualityDevices | null,
): boolean {
  if (!a.length || !b.length) return true;
  if (!aExcept && !bExcept) {
    return a.some((ak) => b.some((bk) => deviceKeyOverlaps(ak, bk, devices)));
  }
  if (aExcept && bExcept) return true;
  if (aExcept && !bExcept) {
    return b.some((bk) => !a.some((ak) => deviceKeyWithin(ak, bk, devices)));
  }
  return a.some((ak) => !b.some((bk) => deviceKeyWithin(bk, ak, devices)));
}

/** Whether rules a and b can match the same play. */
function rulesOverlap(a: QualityRule, b: QualityRule, devices: QualityDevices | null): boolean {
  if (a.where && b.where && a.where !== b.where) return false;
  if (a.kind && b.kind && a.kind !== b.kind) return false;
  const ids = (list: number[] | null | undefined) => (list ?? []).map(String);
  if (
    !listOverlaps(
      ids(a.libraries),
      Boolean(a.exceptLibraries),
      ids(b.libraries),
      Boolean(b.exceptLibraries),
    ) ||
    !listOverlaps(
      ids(a.viewers),
      Boolean(a.exceptViewers),
      ids(b.viewers),
      Boolean(b.exceptViewers),
    )
  ) {
    return false;
  }
  return devicesOverlap(
    (a.devices ?? []).map(deviceKey),
    Boolean(a.exceptDevices),
    (b.devices ?? []).map(deviceKey),
    Boolean(b.exceptDevices),
    devices,
  );
}

const WHERES = ["home", "away"] as const;
const KINDS = ["movie", "episode"] as const;

/**
 * The earlier rules that between them pick the profile for every play this
 * rule matches, as 0-based positions in `earlier`; empty when the rule can
 * still pick one. Several rules can do it together: a Movies rule and an
 * Episodes rule above leave nothing for a rule that covers both.
 */
function winners(
  earlier: QualityRule[],
  rule: QualityRule,
  devices: QualityDevices | null,
): number[] {
  const out = new Set<number>();
  for (const where of rule.where ? [rule.where] : WHERES) {
    for (const kind of rule.kind ? [rule.kind] : KINDS) {
      const part = { ...rule, where, kind };
      const first = earlier.findIndex((e) => Boolean(e.profile) && covers(e, part, devices));
      if (first < 0) return [];
      out.add(first);
    }
  }
  return [...out].sort((x, y) => x - y);
}

/** How a rule stands against the rules above it. Positions are 0-based in `earlier`. */
export interface RuleStanding {
  /**
   * The earlier rules that pick the profile for every play this rule
   * matches. Not empty: this rule never picks a profile (its limits still apply).
   */
  shadowedBy: number[];
  /** Earlier rules that pick a different profile for some of its plays. */
  overlaps: number[];
}

/**
 * Which rules above a rule decide before it does. Only rules that can match
 * the same plays and give a different result count: a rule for other
 * devices, or one that itself never gets to pick, is left out.
 */
export function ruleStanding(
  earlier: QualityRule[],
  rule: QualityRule | undefined,
  devices: QualityDevices | null,
): RuleStanding {
  if (!rule?.profile) return { shadowedBy: [], overlaps: [] };
  const shadowedBy = winners(earlier, rule, devices);
  // Above a rule that always wins, an overlap still matters once this rule moves up.
  const limit = shadowedBy.length ? shadowedBy[0]! : earlier.length;
  const overlaps: number[] = [];
  for (let i = 0; i < limit; i++) {
    const e = earlier[i]!;
    if (!e.profile || e.profile === rule.profile) continue;
    if (!rulesOverlap(e, rule, devices)) continue;
    if (winners(earlier.slice(0, i), e, devices).length) continue;
    overlaps.push(i);
  }
  return { shadowedBy, overlaps };
}

/** Whether a rule in the list never picks a profile because rules above it always do first. */
export function neverPicksProfile(
  rules: QualityRule[],
  index: number,
  devices: QualityDevices | null,
): boolean {
  const rule = rules[index];
  return Boolean(rule?.profile) && winners(rules.slice(0, index), rule!, devices).length > 0;
}

/**
 * Where a new rule goes: above the first rule that picks a profile for
 * anyone on any device (a library, Movies or Away rule). Below such a
 * catch-all a new rule would never pick a profile. With none, it goes last,
 * just above the two fallback rows.
 */
export function defaultRulePosition(rules: QualityRule[]): number {
  const broad = rules.findIndex((r) => r.profile && !r.viewers?.length && !r.devices?.length);
  return broad >= 0 ? broad : rules.length;
}

/** The two rows fixed at the bottom of the rule list. */
export type FallbackRow = "home" | "away";

/** A rule to open in the rule drawer: one in the list, a fallback row, or a new one. */
export type RuleRef =
  { rule: string } | { fallback: FallbackRow } | { create: Partial<QualityRule> };

export function fallbackTitle(row: FallbackRow): string {
  return row === "home" ? t("Everyone else, at home") : t("Everyone else, away");
}

/** Whether away plays that reach the bottom get exactly what they get at home. */
export function awaySameAsHome(quality: Quality): boolean {
  return !quality.defaultAway;
}

/**
 * What a fallback row gives, shaped like a rule. Away without a profile of
 * its own gives the at-home profile and limits.
 */
export function fallbackRule(quality: Quality, row: FallbackRow): QualityRule {
  const own = row === "away" && !awaySameAsHome(quality);
  return {
    id: "",
    where: row,
    profile: own ? quality.defaultAway : quality.default,
    ...tidyLimits(own ? quality.defaultAwayLimits : quality.defaultLimits),
  };
}

/** The limit-rule id the server reports when a fallback row's limits applied. */
export function fallbackOfRuleId(ruleId: string | undefined): FallbackRow | null {
  if (ruleId === "default") return "home";
  if (ruleId === "defaultAway") return "away";
  return null;
}

/** Where the upgrade from 0.3.20 took a rule from, in plain words; "" when it wasn't migrated. */
export function ruleOrigin(r: QualityRule): string {
  const from = r.from;
  if (!from) return "";
  const name = from.name?.trim() ?? "";
  switch (from.kind) {
    case "library":
      return name
        ? t("From your {name} library's profile", { name })
        : t("From a library's profile");
    case "awayLimit":
      return t("From your away-from-home limit");
    case "viewerLimit":
      return name ? t("From {name}'s Max quality", { name }) : t("From a viewer's Max quality");
    case "rule":
      return name ? t("From the “{name}” rule", { name }) : t("From one of your device rules");
    case "awayProfile":
      return t("From your away profile");
    case "tvDefault":
      return t("From your default profile for TV shows");
    default:
      return "";
  }
}

export interface ProfileUse {
  key: string;
  /** "Rule 2: Phones & tablets · Away", "Everyone else, at home". */
  label: string;
  open: RuleRef;
}

/** Every rule that picks a profile, in list order, then the fallback rows. */
export function profileUses(quality: Quality, name: string, names: RuleNames): ProfileUse[] {
  const out: ProfileUse[] = [];
  (quality.rules ?? []).forEach((r, i) => {
    if (r.profile !== name) return;
    out.push({
      key: r.id || `rule-${i}`,
      label: t("Rule {n}: {name}", { n: i + 1, name: ruleTitle(r, names) }),
      open: { rule: r.id },
    });
  });
  if (quality.default === name) {
    out.push({
      key: "default",
      label: awaySameAsHome(quality) ? t("Everyone else, at home and away") : fallbackTitle("home"),
      open: { fallback: "home" },
    });
  }
  if (quality.defaultAway === name) {
    out.push({ key: "away", label: fallbackTitle("away"), open: { fallback: "away" } });
  }
  return out;
}

/** quality with every reference to profile from pointing at to. */
export function withProfileRenamed(quality: Quality, from: string, to: string): Quality {
  return {
    ...quality,
    default: quality.default === from ? to : quality.default,
    defaultAway: quality.defaultAway === from ? to : quality.defaultAway,
    rules: (quality.rules ?? []).map((r) => (r.profile === from ? { ...r, profile: to } : r)),
  };
}

/** A rule with empty conditions dropped, as the server stores it. */
export function tidyRule(r: QualityRule): QualityRule {
  const out: QualityRule = { id: r.id };
  if (r.name?.trim()) out.name = r.name.trim();
  if (r.libraries?.length) {
    out.libraries = r.libraries;
    if (r.exceptLibraries) out.exceptLibraries = true;
  }
  if (r.viewers?.length) {
    out.viewers = r.viewers;
    if (r.exceptViewers) out.exceptViewers = true;
  }
  if (r.devices?.length) {
    out.devices = r.devices;
    if (r.exceptDevices) out.exceptDevices = true;
  }
  if (r.where) out.where = r.where;
  if (r.kind) out.kind = r.kind;
  if (r.profile) out.profile = r.profile;
  if (r.maxHeight) out.maxHeight = r.maxHeight;
  if (r.maxBitrateKbps) out.maxBitrateKbps = r.maxBitrateKbps;
  if (r.stereoAudio) out.stereoAudio = true;
  return out;
}
