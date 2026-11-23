import { t } from "../../../i18n";
import type { Rule, RuleField } from "../../../lib/contract";
import type { KindField, MediaKind } from "../api";

export type PluginFieldValueKind = "text" | "int" | "float" | "date" | "duration" | "bool";
export type NumericValueKind = "int" | "float" | "date" | "duration";
export type ChipCombineMode = "any" | "all" | "none";
export type NumericCompareOp = "is" | "isNot" | "lt" | "lte" | "gt" | "gte" | "between";

export const SHORT_VALUE_LIST_LIMIT = 40;

const DAY_SEC = 86_400;

export interface ParsedTextCondition {
  fieldKey: string;
  mode: ChipCombineMode;
  values: string[];
  /** True when the rule shape itself disambiguates the combine mode. */
  modeExplicit: boolean;
}

export interface ParsedNumericCondition {
  fieldKey: string;
  valueKind: NumericValueKind;
  op: NumericCompareOp;
  a: string;
  b: string;
}

export function pluginKindField(fieldKey: string, kind?: MediaKind): KindField | undefined {
  return kind?.fields?.find((f) => f.key === fieldKey);
}

export function pluginFieldKind(fieldKey: string, kind?: MediaKind): PluginFieldValueKind | null {
  if (!kind) return null;
  if (fieldKey === "year") return "int";
  if (fieldKey === "rating") return "float";
  if (fieldKey === "released") return "date";
  const fd = pluginKindField(fieldKey, kind);
  if (!fd) return null;
  switch (fd.type) {
    case "string":
    case "strings":
    case "people":
    case "enum":
      return "text";
    case "int":
      return "int";
    case "float":
      return "float";
    case "date":
      return "date";
    case "duration":
      return "duration";
    case "bool":
      return "bool";
    default:
      return null;
  }
}

export function pluginFieldMultiValued(fieldKey: string, kind?: MediaKind): boolean {
  const fd = pluginKindField(fieldKey, kind);
  return fd?.type === "strings" || fd?.type === "people";
}

export function pluginFieldDeclaredValues(fieldKey: string, kind?: MediaKind): string[] {
  const fd = pluginKindField(fieldKey, kind);
  if (!fd) return [];
  if (fd.type === "enum") return fd.enum ?? [];
  if (fd.type === "strings") return fd.values ?? [];
  return [];
}

function dedupeStrings(items: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of items) {
    if (!raw || !raw.trim()) continue;
    if (seen.has(raw)) continue;
    seen.add(raw);
    out.push(raw);
  }
  return out;
}

function extractStringList(value: unknown): string[] | null {
  if (value === "" || value === null || value === undefined) return [];
  if (typeof value === "string") return dedupeStrings([value]);
  if (Array.isArray(value)) {
    const out: string[] = [];
    for (const item of value) {
      if (typeof item !== "string") return null;
      out.push(item);
    }
    return dedupeStrings(out);
  }
  return null;
}

function extractSameTextFieldLeaves(
  children: Rule[],
  wantOp: "is" | "isNot",
  kind?: MediaKind,
  requireSingle = false,
): { fieldKey: string; values: string[] } | null {
  if (children.length === 0) return null;
  let fieldKey = "";
  const values: string[] = [];
  for (const child of children) {
    if (!child || typeof child !== "object") return null;
    if (!child.field.startsWith("browse:") || child.op !== wantOp) return null;
    const key = child.field.slice("browse:".length);
    if (pluginFieldKind(key, kind) !== "text") return null;
    if (!fieldKey) fieldKey = key;
    else if (fieldKey !== key) return null;
    const list = extractStringList(child.value);
    if (!list || list.length === 0) return null;
    if (requireSingle && list.length !== 1) return null;
    values.push(...list);
  }
  return { fieldKey, values: dedupeStrings(values) };
}

/**
 * Recognizes a text-field condition (either a direct `browse:<key>` rule or a
 * single-field `group` rule) so the editor can present chips + Any/All/None.
 */
export function parseTextCondition(rule: Rule, kind?: MediaKind): ParsedTextCondition | null {
  if (!kind) return null;

  if (rule.field.startsWith("browse:")) {
    const fieldKey = rule.field.slice("browse:".length);
    if (pluginFieldKind(fieldKey, kind) !== "text") return null;
    if (rule.op !== "is" && rule.op !== "isNot") return null;
    const values = extractStringList(rule.value);
    if (values === null) return null;
    const mode: ChipCombineMode = rule.op === "isNot" ? "none" : "any";
    return {
      fieldKey,
      mode,
      values,
      modeExplicit: rule.op === "isNot" || values.length >= 2,
    };
  }

  if (rule.field === "group" && Array.isArray(rule.value)) {
    const children = rule.value as Rule[];
    if (rule.op === "is" && children.length >= 2) {
      const allPos = extractSameTextFieldLeaves(children, "is", kind, true);
      if (allPos) {
        return {
          fieldKey: allPos.fieldKey,
          mode: "all",
          values: allPos.values,
          modeExplicit: true,
        };
      }
      const allNeg = extractSameTextFieldLeaves(children, "isNot", kind, false);
      if (allNeg) {
        return {
          fieldKey: allNeg.fieldKey,
          mode: "none",
          values: allNeg.values,
          modeExplicit: true,
        };
      }
    }
    if (rule.op === "in" && children.length >= 2) {
      const anyPos = extractSameTextFieldLeaves(children, "is", kind, false);
      if (anyPos) {
        return {
          fieldKey: anyPos.fieldKey,
          mode: "any",
          values: anyPos.values,
          modeExplicit: true,
        };
      }
    }
    if (rule.op === "isNot" && children.length === 1) {
      const only = children[0];
      if (only) {
        const single = extractSameTextFieldLeaves([only], "is", kind, false);
        if (single) {
          return {
            fieldKey: single.fieldKey,
            mode: "none",
            values: single.values,
            modeExplicit: true,
          };
        }
        if (only.field === "group" && only.op === "in" && Array.isArray(only.value)) {
          const nested = extractSameTextFieldLeaves(only.value as Rule[], "is", kind, false);
          if (nested) {
            return {
              fieldKey: nested.fieldKey,
              mode: "none",
              values: nested.values,
              modeExplicit: true,
            };
          }
        }
      }
    }
  }

  return null;
}

/**
 * Compiles one text-field chip condition into the engine's canonical Rule:
 *   - 0 values: incomplete `browse:<key>` (omitted from preview until picked)
 *   - 1 value:  `browse:<key>` with `is` or `isNot`
 *   - 2+ values:
 *       - Any of:  `browse:<key>` (`op: "is"`, `value: values`) — single IN EXISTS
 *       - All of:  `group` (`op: "is"`) of individual `browse:<key>` `is` leaves — AND
 *       - None of: `browse:<key>` (`op: "isNot"`, `value: values`) — NOT (IN EXISTS)
 */
export function buildTextCondition(
  fieldKey: string,
  mode: ChipCombineMode,
  rawValues: string[],
): Rule {
  const values = dedupeStrings(rawValues);
  const field = `browse:${fieldKey}` as RuleField;
  if (values.length === 0) {
    return { field, op: mode === "none" ? "isNot" : "is", value: "" };
  }
  if (values.length === 1) {
    return { field, op: mode === "none" ? "isNot" : "is", value: values[0]! };
  }
  if (mode === "all") {
    return {
      field: "group",
      op: "is",
      value: values.map((v) => ({ field, op: "is" as const, value: v })),
    };
  }
  return {
    field,
    op: mode === "none" ? "isNot" : "is",
    value: values,
  };
}

// ---- Numeric / Date / Duration helpers -------------------------------------

function dateStartUnix(iso: string): number | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(iso.trim())) return null;
  const ms = Date.parse(`${iso.trim()}T00:00:00Z`);
  return Number.isNaN(ms) ? null : Math.floor(ms / 1000);
}

function dateEndUnix(iso: string): number | null {
  const s = dateStartUnix(iso);
  return s === null ? null : s + DAY_SEC - 1;
}

function unixToIsoDate(sec: number): string {
  if (!Number.isFinite(sec)) return "";
  const d = new Date(sec * 1000);
  return Number.isNaN(d.getTime()) ? "" : d.toISOString().slice(0, 10);
}

function secondsToMinutesString(sec: number): string {
  if (!Number.isFinite(sec)) return "";
  const mins = sec / 60;
  return Number.isInteger(mins) ? String(mins) : String(Number(mins.toFixed(2)));
}

function formatDecodedScalar(raw: string, valueKind: NumericValueKind): string {
  const n = Number(raw.trim());
  if (!raw.trim() || !Number.isFinite(n)) return "";
  switch (valueKind) {
    case "duration":
      return secondsToMinutesString(n);
    case "date":
      return unixToIsoDate(n);
    case "int":
      return String(Math.trunc(n));
    case "float":
      return String(n);
  }
}

const LEGACY_DURATION_BUCKETS: Record<string, { op: NumericCompareOp; a: string; b: string }> = {
  "under-10m": { op: "lt", a: "10", b: "" },
  "10-30m": { op: "between", a: "10", b: "30" },
  "30-60m": { op: "between", a: "30", b: "60" },
  "1-2h": { op: "between", a: "60", b: "120" },
  "over-2h": { op: "gte", a: "120", b: "" },
};

export function parseNumericCondition(rule: Rule, kind?: MediaKind): ParsedNumericCondition | null {
  if (!kind || !rule.field.startsWith("browse:")) return null;
  const fieldKey = rule.field.slice("browse:".length);
  const vk = pluginFieldKind(fieldKey, kind);
  if (vk !== "int" && vk !== "float" && vk !== "date" && vk !== "duration") return null;

  const raw =
    typeof rule.value === "number"
      ? String(rule.value)
      : typeof rule.value === "string"
        ? rule.value.trim()
        : "";
  const baseOp: NumericCompareOp = rule.op === "isNot" ? "isNot" : "is";
  if (!raw) return { fieldKey, valueKind: vk, op: baseOp, a: "", b: "" };

  if (vk === "duration" && LEGACY_DURATION_BUCKETS[raw]) {
    const b = LEGACY_DURATION_BUCKETS[raw]!;
    return { fieldKey, valueKind: vk, op: b.op, a: b.a, b: b.b };
  }
  if (vk === "date" && /^\d{4}$/.test(raw)) {
    return { fieldKey, valueKind: vk, op: "between", a: `${raw}-01-01`, b: `${raw}-12-31` };
  }

  if (raw.includes("..")) {
    const idx = raw.indexOf("..");
    const left = raw.slice(0, idx).trim();
    const right = raw.slice(idx + 2).trim();
    if (left && !right) {
      if (left.startsWith(">") && !left.startsWith(">=")) {
        return {
          fieldKey,
          valueKind: vk,
          op: "gt",
          a: formatDecodedScalar(left.slice(1), vk),
          b: "",
        };
      }
      const cleanLeft = left.startsWith(">=") ? left.slice(2) : left;
      return { fieldKey, valueKind: vk, op: "gte", a: formatDecodedScalar(cleanLeft, vk), b: "" };
    }
    if (!left && right) {
      if (right.startsWith("<") && !right.startsWith("<=")) {
        return {
          fieldKey,
          valueKind: vk,
          op: "lt",
          a: formatDecodedScalar(right.slice(1), vk),
          b: "",
        };
      }
      const cleanRight = right.startsWith("<=") ? right.slice(2) : right;
      return { fieldKey, valueKind: vk, op: "lte", a: formatDecodedScalar(cleanRight, vk), b: "" };
    }
    if (left && right) {
      const cleanLeft = left.replace(/^>=?/, "").trim();
      const cleanRight = right.replace(/^<=?/, "").trim();
      const a = formatDecodedScalar(cleanLeft, vk);
      const b = formatDecodedScalar(cleanRight, vk);
      if (vk === "date" && a && a === b) {
        return { fieldKey, valueKind: vk, op: baseOp, a, b: "" };
      }
      return { fieldKey, valueKind: vk, op: "between", a, b };
    }
    return { fieldKey, valueKind: vk, op: baseOp, a: "", b: "" };
  }

  if (raw.startsWith("<=")) {
    return { fieldKey, valueKind: vk, op: "lte", a: formatDecodedScalar(raw.slice(2), vk), b: "" };
  }
  if (raw.startsWith(">=")) {
    return { fieldKey, valueKind: vk, op: "gte", a: formatDecodedScalar(raw.slice(2), vk), b: "" };
  }
  if (raw.startsWith("<")) {
    return { fieldKey, valueKind: vk, op: "lt", a: formatDecodedScalar(raw.slice(1), vk), b: "" };
  }
  if (raw.startsWith(">")) {
    return { fieldKey, valueKind: vk, op: "gt", a: formatDecodedScalar(raw.slice(1), vk), b: "" };
  }

  return { fieldKey, valueKind: vk, op: baseOp, a: formatDecodedScalar(raw, vk), b: "" };
}

function parseDisplayNumber(raw: string, valueKind: "int" | "float" | "duration"): number | null {
  const s = raw.trim();
  if (!s) return null;
  const n = Number(s);
  if (!Number.isFinite(n)) return null;
  if (valueKind === "int") return Math.trunc(n);
  if (valueKind === "duration") {
    if (n < 0) return null;
    return Math.round(n * 60);
  }
  return n;
}

export function buildNumericCondition(
  fieldKey: string,
  valueKind: NumericValueKind,
  op: NumericCompareOp,
  a: string,
  b: string,
): { rule: Rule; error?: string } {
  const field = `browse:${fieldKey}` as RuleField;
  const wireOp: Rule["op"] = op === "isNot" ? "isNot" : "is";
  const incomplete: Rule = { field, op: wireOp, value: "" };

  if (valueKind === "date") {
    if (op === "between") {
      const lo = dateStartUnix(a);
      const hi = dateEndUnix(b);
      if (lo === null || hi === null) return { rule: incomplete };
      if (lo > hi) return { rule: incomplete, error: t("Minimum must be at most maximum.") };
      return { rule: { field, op: "is", value: `${lo}..${hi}` } };
    }
    const start = dateStartUnix(a);
    const end = dateEndUnix(a);
    if (start === null || end === null) return { rule: incomplete };
    switch (op) {
      case "is":
      case "isNot":
        return { rule: { field, op: wireOp, value: `${start}..${end}` } };
      case "lt":
        return { rule: { field, op: "is", value: `<${start}` } };
      case "lte":
        return { rule: { field, op: "is", value: `..${end}` } };
      case "gt":
        return { rule: { field, op: "is", value: `>${end}` } };
      case "gte":
        return { rule: { field, op: "is", value: `${start}..` } };
    }
  }

  if (op === "between") {
    const lo = parseDisplayNumber(a, valueKind);
    const hi = parseDisplayNumber(b, valueKind);
    if (lo === null || hi === null) return { rule: incomplete };
    if (lo > hi) return { rule: incomplete, error: t("Minimum must be at most maximum.") };
    return { rule: { field, op: "is", value: `${lo}..${hi}` } };
  }

  const n = parseDisplayNumber(a, valueKind);
  if (n === null) return { rule: incomplete };
  switch (op) {
    case "is":
    case "isNot":
      return { rule: { field, op: wireOp, value: String(n) } };
    case "lt":
      return { rule: { field, op: "is", value: `<${n}` } };
    case "lte":
      return { rule: { field, op: "is", value: `..${n}` } };
    case "gt":
      return { rule: { field, op: "is", value: `>${n}` } };
    case "gte":
      return { rule: { field, op: "is", value: `${n}..` } };
  }
}

export function pluginFieldLabel(fieldKey: string, kind?: MediaKind): string {
  const declared = kind?.fields?.find((f) => f.key === fieldKey)?.label;
  if (declared) return declared;
  if (fieldKey === "year") return t("Year");
  if (fieldKey === "rating") return t("Rating");
  if (fieldKey === "released") return t("Released");
  return fieldKey;
}

/**
 * Formats a plugin text or numeric condition for rule list summaries.
 * Returns null when the rule is not a recognized plugin text/numeric condition.
 */
export function formatPluginCondition(rule: Rule, kind?: MediaKind): string | null {
  if (!kind) return null;

  const text = parseTextCondition(rule, kind);
  if (text && text.values.length > 0) {
    const field = pluginFieldLabel(text.fieldKey, kind);
    const value = text.values.join(", ");
    if (text.values.length === 1) {
      return text.mode === "none"
        ? t("{field} is not {value}", { field, value })
        : t("{field} is {value}", { field, value });
    }
    if (text.mode === "all") return t("{field} is all of {value}", { field, value });
    if (text.mode === "none") return t("{field} is none of {value}", { field, value });
    return t("{field} is any of {value}", { field, value });
  }

  const num = parseNumericCondition(rule, kind);
  if (num && num.a) {
    const field = pluginFieldLabel(num.fieldKey, kind);
    const unit = (v: string) => (num.valueKind === "duration" ? `${v} ${t("min")}` : v);
    switch (num.op) {
      case "is":
        return t("{field} is {value}", { field, value: unit(num.a) });
      case "isNot":
        return t("{field} is not {value}", { field, value: unit(num.a) });
      case "lt":
        return t("{field} is less than {value}", { field, value: unit(num.a) });
      case "lte":
        return t("{field} is at most {value}", { field, value: unit(num.a) });
      case "gt":
        return t("{field} is greater than {value}", { field, value: unit(num.a) });
      case "gte":
        return t("{field} is at least {value}", { field, value: unit(num.a) });
      case "between":
        if (num.b)
          return t("{field} between {min} and {max}", {
            field,
            min: unit(num.a),
            max: unit(num.b),
          });
        break;
    }
  }

  return null;
}
