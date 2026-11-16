import { getLocale } from "../i18n";
export function indexerOrigin(ix: { kind?: string; url?: string; prowlarrImport?: unknown }) {
  if (ix.kind === "stremio") return { key: "stremio", label: "Stremio" };
  try {
    const u = new URL(ix.url || "");
    const jackett = /\/api\/v2\.0\/indexers\//i.test(u.pathname);
    if (ix.prowlarrImport || jackett) {
      const kind = jackett ? "Jackett" : "Prowlarr";
      // Do not expose credentials, query strings or private URL prefixes.
      const base = jackett
        ? u.pathname.split(/\/api\/v2\.0\/indexers\//i)[0]
        : u.pathname.replace(/\/\d+\/api\/?$/i, "");
      return { key: `${kind}:${u.origin}${base}`, label: `${kind} · ${u.host}` };
    }
  } catch {
    /* Missing/masked address: keep it in manual sources. */
  }
  return {
    key: "manual",
    label: getLocale().startsWith("fr")
      ? "Autres indexeurs / ajout manuel"
      : "Other indexers / manual setup",
  };
}
