import { Navigate, useLocation, useParams } from "react-router-dom";

export const REMIX_TABS = [
  { key: "sources", fr: "Sources", en: "Sources", features: ["indexer", "sfr", "seerrcatalog"] },
  { key: "priorites", fr: "Priorités", en: "Priorities", features: [] },
  {
    key: "langues",
    fr: "Langues et qualité",
    en: "Languages & quality",
    features: ["french", "playable"],
  },
  { key: "demande", fr: "VOSTFR à la demande", en: "On-demand subtitles", features: [] },
  {
    key: "contenus",
    fr: "Contenus et bibliothèques",
    en: "Content & libraries",
    features: ["music"],
  },
  { key: "statistiques", fr: "Statistiques", en: "Statistics", features: [] },
  {
    key: "suivi",
    fr: "Notifications et suivi",
    en: "Notifications & tracking",
    features: ["notify", "scrob"],
  },
] as const;

export function legacyRemixRoute(tab?: string): string | null {
  if (tab === "films") return "/remix/langues";
  if (tab === "animes") return "/remix/contenus?content=animes";
  if (tab === "musique") return "/remix/contenus?content=musique";
  if (tab === "bibliotheques") return "/remix/contenus?content=bibliotheques";
  return null;
}
export function sourcesHref(tab = "debrid", search = "") {
  const params = new URLSearchParams(search);
  if (["stats", "statistics", "usage"].includes(tab)) {
    params.set("view", tab === "usage" ? "usage" : "sources");
    return `/remix/statistiques?${params}`;
  }
  if (tab === "iptv" && params.has("source")) {
    params.set("iptvSource", params.get("source")!);
  }
  params.set(
    "source",
    tab === "indexers"
      ? "indexeurs"
      : ["debrid", "usenet", "iptv", "stremio"].includes(tab)
        ? tab
        : "debrid",
  );
  return `/remix/sources?${params}`;
}
export function RemixSourcesRedirect() {
  const { tab } = useParams();
  const location = useLocation();
  return <Navigate replace to={sourcesHref(tab, location.search) + location.hash} />;
}
