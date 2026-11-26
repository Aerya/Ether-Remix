import type { CollectionSpec } from "../../lib/contract";
import { blankSpec, normalizeSpec } from "../../lib/rules";

/** Each new collection gets its own local-draft id, carried in the URL so reloads restore it. */
export function newCollectionHref(params: Record<string, string> = {}): string {
  const q = new URLSearchParams({
    ...params,
    draft: `new-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 5)}`,
  });
  return `/collections/new?${q}`;
}

/**
 * An existing collection in Collection Studio; `back` (a Recommendations
 * link) is where closing or saving returns to.
 */
export function editCollectionHref(id: string, back?: string): string {
  const q = back ? `?${new URLSearchParams({ back })}` : "";
  return `/collections/${encodeURIComponent(id)}${q}`;
}

/** Where Collection Studio returns to: a Recommendations page, or nowhere. */
export function studioBackLink(search: URLSearchParams): string | null {
  const back = search.get("back");
  return back?.startsWith("/recommendations?") ? back : null;
}

/** Carry the exact catalogue selected in a plugin drawer into its new collection. */
export function pluginCollectionHref(kind: string, label: string): string {
  return newCollectionHref({ pluginSource: kind, name: label });
}

/** True when a draft has no name, rules, artwork or source changes beyond blankSpec(). */
export function isBlankCollectionSpec(spec: CollectionSpec): boolean {
  const normalized = normalizeSpec(spec);
  const base = blankSpec(normalized.mediaType);
  return JSON.stringify(normalized) === JSON.stringify(base);
}
