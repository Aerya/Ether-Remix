import { ApiError } from "../../lib/api";
import type { ActionSpec, FlowTemplate, Graph, ValidationResult } from "./types";
import { defaultConfigForNode } from "./types";

/**
 * Params for a new action step: the editor's starter values for params the
 * action accepts, then the action's own defaults. Nothing the action does
 * not accept, because the server rejects unknown params and the inspector
 * only shows the ones the action declares.
 */
export function initialActionParams(
  spec: ActionSpec | undefined,
  actionType: string,
): Record<string, unknown> {
  const seeds = (defaultConfigForNode("action", actionType).params ?? {}) as Record<
    string,
    unknown
  >;
  if (!spec) return {};
  const out: Record<string, unknown> = {};
  for (const p of spec.params ?? []) {
    if (p.key in seeds) out[p.key] = seeds[p.key];
    else if (p.default !== undefined) out[p.key] = p.default;
  }
  return out;
}

/** Only the params `spec` accepts (used when a step switches to another action). */
export function knownParams(
  spec: ActionSpec | undefined,
  params: Record<string, unknown>,
): Record<string, unknown> {
  if (!spec) return {};
  const keys = new Set((spec.params ?? []).map((p) => p.key));
  return Object.fromEntries(Object.entries(params).filter(([k]) => keys.has(k)));
}

/**
 * Drop params an action does not accept from every action step. Drafts made
 * before the inspector filtered them (or by older Describe-it output) can
 * carry hidden leftovers the owner cannot see or remove. Returns the same
 * graph when nothing changes.
 */
export function pruneUnknownParams(graph: Graph, actionsByType: Map<string, ActionSpec>): Graph {
  let changed = false;
  const nodes = graph.nodes.map((n) => {
    if (n.kind !== "action") return n;
    const cfg = (n.config ?? {}) as Record<string, unknown>;
    const spec = actionsByType.get(String(cfg.actionType ?? ""));
    const params = cfg.params as Record<string, unknown> | undefined;
    if (!spec || !params) return n;
    const kept = knownParams(spec, params);
    if (Object.keys(kept).length === Object.keys(params).length) return n;
    changed = true;
    return { ...n, config: { ...cfg, params: kept } };
  });
  return changed ? { ...graph, nodes } : graph;
}

const PARAM_PLACEHOLDER = /\{\{\s*param\.([A-Za-z0-9_.-]+)\s*\}\}/g;

/**
 * A template's graph with its gallery placeholders ({{ param.key }}) filled
 * from the template's defaults, for "Preview on canvas". A placeholder with
 * no default becomes empty, so the step shows its picker and validation asks
 * for a value instead of saving a reference that renders empty at run time.
 */
export function fillTemplateDefaults(template: FlowTemplate): Graph {
  const defaults = new Map((template.params ?? []).map((p) => [p.key, p.default]));
  const fill = (v: unknown): unknown => {
    if (typeof v === "string") {
      const whole = /^\s*\{\{\s*param\.([A-Za-z0-9_.-]+)\s*\}\}\s*$/.exec(v);
      if (whole) return defaults.get(whole[1]!) ?? "";
      return v.replace(PARAM_PLACEHOLDER, (_, key: string) => {
        const d = defaults.get(key);
        return d === undefined || d === null ? "" : Array.isArray(d) ? d.join(", ") : String(d);
      });
    }
    if (Array.isArray(v)) return v.map(fill);
    if (v && typeof v === "object")
      return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, fill(x)]));
    return v;
  };
  return {
    ...template.graph,
    nodes: template.graph.nodes.map((n) => ({
      ...n,
      config: fill(n.config ?? {}) as Record<string, unknown>,
    })),
  };
}

/** The validation report a 400 from create/update carries, if any. */
export function validationFromError(err: unknown): ValidationResult | null {
  if (!(err instanceof ApiError) || err.status !== 400) return null;
  const body = err.body as { validation?: ValidationResult } | null | undefined;
  return body?.validation && Array.isArray(body.validation.errors) ? body.validation : null;
}
