/**
 * Meta types for the console descriptor kinds (mirror of
 * internal/extend/meta_console.go) and the small pure helpers the shell,
 * command search and Ask Ether share.
 */
import { BUILTIN, textOf, type Descriptor, type ExtendText } from "./extend";
import { searchTerms } from "../i18n";

export interface PageSectionMeta {
  key: string;
  label: ExtendText;
  keywords?: ExtendText;
  /** Where ⌘K lands when the section is not a tab of its own. */
  href?: string;
}

export interface PageMeta {
  route?: string;
  pluginRoute?: string;
  navGroup?: string;
  group?: string;
  icon?: string;
  inNav?: boolean;
  mobile?: boolean;
  exact?: boolean;
  keywords?: ExtendText;
  sections?: PageSectionMeta[];
}

export interface NavItemMeta {
  route: string;
  page?: string;
  pageKey?: string;
  mobile?: boolean;
  exact?: boolean;
}

export interface CommandMeta {
  route?: string;
  action?: string;
  keywords?: ExtendText;
  agentSuggestion?: boolean;
  routePatterns?: string[];
}

export interface IntegrationSwitchMeta {
  key: string;
  label: ExtendText;
  hint?: ExtendText;
}

/**
 * Search aliases for a descriptor text: the active language's terms plus the
 * English ones, as command search has always matched. Plugin keywords arrive
 * resolved for the active locale.
 */
export function searchTextOf(text?: ExtendText): string | undefined {
  if (!text) return undefined;
  if (text.source) return searchTerms(text.source);
  const resolved = textOf(text);
  return resolved || undefined;
}

/** The sidebar group holding the server's own pages. */
export const SYSTEM_NAV_GROUP = "system";

/**
 * The sidebar groups a plugin page may declare. Connect, Automate and System
 * are for built-in pages; a plugin's "server" lands in System.
 */
const PLUGIN_NAV_GROUPS: Record<string, string> = {
  library: "library",
  streaming: "streaming",
  server: SYSTEM_NAV_GROUP,
};

/**
 * The sidebar group for a plugin page descriptor, when it declares one.
 * Note that internal/plugins/descriptors.go sets top-level `d.group` to the
 * plugin's human name ("Ether Wrapped") while putting the manifest's sidebar
 * group on `meta.group` (or `meta.navGroup`).
 */
export function pluginNavGroup(d: Descriptor<PageMeta>): string | undefined {
  const candidate = d.meta?.navGroup || d.meta?.group || d.group;
  // Plugins never join the unlabelled top group above Overview.
  if (!candidate) return undefined;
  return PLUGIN_NAV_GROUPS[candidate] ?? SYSTEM_NAV_GROUP;
}

/** The console route a page descriptor opens. */
export function pageRoute(d: Descriptor<PageMeta>): string {
  if (d.owner !== BUILTIN) {
    return pluginPageRoute(d.owner, pluginLocalId(d));
  }
  return d.meta?.route || "/";
}

/** A plugin page's local id: the part after "plugin:<owner>/". */
export function pluginLocalId(d: Descriptor): string {
  const prefix = `plugin:${d.owner}/`;
  return d.id.startsWith(prefix) ? d.id.slice(prefix.length) : d.id;
}

export function pluginPageRoute(plugin: string, page: string): string {
  return `/p/${encodeURIComponent(plugin)}/${page.split("/").map(encodeURIComponent).join("/")}`;
}
