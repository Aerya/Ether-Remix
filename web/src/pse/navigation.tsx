import { Navigate, useLocation, useParams } from "react-router-dom";

export const REMIX_TABS = [
  { key: "apercu", fr: "Vue d’ensemble", en: "Overview", features: [] },
  {
    key: "sources",
    fr: "Sources",
    en: "Sources",
    features: [
      "sfr",
      "seerrcatalog",
      "seerrcatalog_search",
      "torrent",
      "torrent2",
      "torrent3",
      "xdcc",
      "ftp",
    ],
  },
  { key: "indexation", fr: "Indexation", en: "Indexing", features: ["indexer"] },
  {
    key: "lecture",
    fr: "Lecture",
    en: "Playback",
    features: ["french", "playable", "submaker"],
  },
  { key: "contenus", fr: "Contenus", en: "Content", features: ["music", "librarysort"] },
  { key: "visuels", fr: "Visuels", en: "Visuals", features: ["posters"] },
  { key: "suivi", fr: "Suivi", en: "Tracking", features: ["notify", "scrob"] },
] as const;

/** Regroupement du mode Expert par intention, dans l’ordre où on s’en sert. */
export const EXPERT_GROUPS = [
  { fr: "Surveiller", en: "Monitor", tabs: ["apercu"] },
  { fr: "Trouver", en: "Find", tabs: ["sources", "indexation"] },
  { fr: "Lire", en: "Watch", tabs: ["lecture", "contenus"] },
  { fr: "Personnaliser", en: "Customize", tabs: ["visuels", "suivi"] },
] as const;

/** Onglets du mode simple : le parcours d’un débutant, dans l’ordre où on s’en sert. */
export const SIMPLE_TABS = [
  { key: "accueil", fr: "Accueil", en: "Home" },
  { key: "sources", fr: "Sources", en: "Sources" },
  { key: "langues", fr: "Langues et qualité", en: "Languages and quality" },
  { key: "bibliotheque", fr: "Bibliothèque", en: "Library" },
] as const;

/** Adresse d’un onglet du mode simple, qui réutilise les pages existantes. */
export function simpleTabHref(key: string): string {
  if (key === "langues") return "/remix/lecture?view=qualite";
  if (key === "bibliotheque") return "/remix/contenus";
  return `/remix/${key}`;
}

/**
 * Onglet simple correspondant à une adresse, `""` pour une page du mode simple
 * sans onglet (assistant, diagnostic), `null` pour une page réservée au mode expert.
 */
export function simpleTabOf(tab: string | undefined, view: string | null): string | null {
  if (tab === "accueil" || tab === "sources") return tab;
  if (tab === "contenus") return "bibliotheque";
  if (tab === "lecture" && view === "qualite") return "langues";
  if (tab === "assistant" || tab === "diagnostic") return "";
  return null;
}

/** Vues secondaires des onglets regroupés, portées par `?view=`. */
export const REMIX_VIEWS = {
  apercu: [
    { key: "demarrage", fr: "Démarrage", en: "Quick start" },
    { key: "indexation", fr: "Activité", en: "Activity" },
    { key: "sources", fr: "Sources", en: "Sources" },
    { key: "usage", fr: "Utilisation", en: "Usage" },
  ],
  indexation: [
    { key: "reglages", fr: "Réglages", en: "Settings" },
    { key: "releases", fr: "Releases", en: "Releases" },
  ],
  lecture: [
    { key: "priorites", fr: "Priorités", en: "Priorities" },
    { key: "qualite", fr: "Qualité", en: "Quality" },
    { key: "vostfr", fr: "VOSTFR", en: "Non-dubbed titles" },
    { key: "soustitres", fr: "Sous-titres", en: "Subtitles" },
  ],
  visuels: [
    { key: "cartes", fr: "Cartes", en: "Cards" },
    { key: "affiches", fr: "Affiches", en: "Posters" },
  ],
} as const;

/** Anciennes adresses d’onglets, conservées pour les favoris et liens externes. */
export function legacyRemixRoute(tab?: string, search = ""): string | null {
  const keep = (path: string, view?: string) => {
    const params = new URLSearchParams(search);
    if (view) params.set("view", view);
    const q = params.toString();
    return q ? `${path}?${q}` : path;
  };
  if (tab === "statistiques") return keep("/remix/apercu");
  if (tab === "demarrage") return keep("/remix/apercu", "demarrage");
  if (tab === "releases") return keep("/remix/indexation", "releases");
  if (tab === "priorites") return "/remix/lecture";
  if (tab === "films" || tab === "langues") return keep("/remix/lecture", "qualite");
  if (tab === "demande") return keep("/remix/lecture", "vostfr");
  if (tab === "medias-locaux") return "/remix/sources?source=local#local-media-health";
  if (tab === "animes") return "/remix/contenus?content=animes";
  if (tab === "musique") return "/remix/contenus?content=musique";
  if (tab === "bibliotheques") return "/remix/contenus?content=bibliotheques";
  return null;
}
export function sourcesHref(tab = "debrid", search = "") {
  const params = new URLSearchParams(search);
  if (["stats", "statistics", "usage"].includes(tab)) {
    params.set("view", tab === "usage" ? "usage" : "sources");
    return `/remix/apercu?${params}`;
  }
  if (tab === "iptv" && params.has("source")) {
    params.set("iptvSource", params.get("source")!);
  }
  params.set(
    "source",
    tab === "indexers"
      ? "indexeurs"
      : ["debrid", "usenet", "iptv", "stremio", "torrent", "xdcc", "ftp"].includes(tab)
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

export function visibleRemixViews(
  tab: keyof typeof REMIX_VIEWS,
): readonly { key: string; fr: string; en: string }[] {
  return REMIX_VIEWS[tab];
}
