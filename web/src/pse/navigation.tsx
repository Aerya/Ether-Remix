import { Navigate, useLocation, useParams } from "react-router-dom";
import { getLocale } from "../i18n";

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
  {
    key: "indexation",
    fr: "Indexation",
    en: "Indexing",
    // « predb » : section « Pre » (bêta).
    features: ["indexer", "predb"],
  },
  {
    key: "lecture",
    fr: "Lecture",
    en: "Playback",
    features: ["french", "playable", "submaker", "continuewatch", "lightlists"],
  },
  { key: "contenus", fr: "Contenus", en: "Content", features: ["music", "librarysort"] },
  { key: "visuels", fr: "Visuels", en: "Visuals", features: ["posters"] },
  { key: "suivi", fr: "Suivi", en: "Tracking", features: ["notify", "scrob"] },
] as const;

/**
 * Entrées du groupe « Remix » de la barre latérale, dans l’ordre d’affichage.
 * `mode` : « simple » (toujours), « expert » (réservée au mode Expert) ou
 * « simple-only » (masquée en mode Expert, où Lecture couvre la qualité).
 * Les mêmes entrées sont déclarées côté Go (internal/app/pse_descriptors.go) ;
 * un test vérifie qu’elles concordent.
 */
export const REMIX_NAV = [
  {
    id: "pse-accueil",
    tab: "accueil",
    fr: "Accueil",
    en: "Start",
    icon: "House",
    mode: "simple",
  },
  { id: "pse-sources", tab: "sources", fr: "Sources", en: "Sources", icon: "Plug", mode: "simple" },
  {
    id: "pse-indexation",
    tab: "indexation",
    fr: "Indexation",
    en: "Indexing",
    icon: "Search",
    mode: "expert",
  },
  {
    id: "pse-langues",
    tab: "langues",
    fr: "Langues et qualité",
    en: "Languages and quality",
    icon: "Languages",
    mode: "simple-only",
  },
  {
    id: "pse-lecture",
    tab: "lecture",
    fr: "Lecture",
    en: "Playback",
    icon: "MonitorPlay",
    mode: "expert",
  },
  {
    id: "pse-contenus",
    tab: "contenus",
    fr: "Contenus",
    en: "Content",
    icon: "Film",
    mode: "simple",
  },
  {
    id: "pse-visuels",
    tab: "visuels",
    fr: "Visuels",
    en: "Visuals",
    icon: "Palette",
    mode: "expert",
  },
  { id: "pse-suivi", tab: "suivi", fr: "Suivi", en: "Tracking", icon: "Eye", mode: "expert" },
  {
    id: "pse-apercu",
    tab: "apercu",
    fr: "Statistiques",
    en: "Statistics",
    icon: "Gauge",
    mode: "simple",
  },
  {
    id: "pse-diagnostic",
    tab: "diagnostic",
    fr: "Diagnostic",
    en: "Diagnostics",
    icon: "Stethoscope",
    mode: "simple",
  },
  {
    id: "pse-expert",
    tab: "expert",
    fr: "Expert",
    en: "Expert",
    icon: "SlidersHorizontal",
    mode: "simple",
  },
] as const;

export const remixHref = (tab: string) => `/remix/${tab}`;
/** Adresse de l’entrée qui bascule le mode Expert (rendue en interrupteur, pas en lien). */
export const REMIX_EXPERT_HREF = remixHref("expert");

/** Libellé d’une entrée Remix de la barre latérale, dans la langue de la console. */
export function pseNavLabel(id: string): string | null {
  const entry = REMIX_NAV.find((e) => e.id === id);
  return entry ? (getLocale().startsWith("fr") ? entry.fr : entry.en) : null;
}

/** Titre d’une page Remix, d’après l’entrée de la barre latérale. */
export function remixTitle(tab: string | undefined, fr: boolean): string | null {
  const entry = REMIX_NAV.find((e) => e.tab === tab);
  return entry ? (fr ? entry.fr : entry.en) : null;
}

/**
 * Une page est « simple » quand le mode simple suffit à l’atteindre depuis la barre
 * latérale ; sinon la barre affiche les entrées Expert sans changer le mode mémorisé.
 */
export function isSimpleRoute(tab: string | undefined, view: string | null): boolean {
  if (
    ["accueil", "sources", "langues", "contenus", "apercu", "assistant", "diagnostic"].includes(
      tab ?? "",
    )
  )
    return true;
  return tab === "lecture" && view === "qualite";
}

/** Retire de la barre latérale les entrées qui ne correspondent pas au mode affiché. */
export function filterRemixNav<G extends { items: { href: string }[] }>(
  groups: G[],
  expert: boolean,
): G[] {
  const modeOf = new Map<string, string>(REMIX_NAV.map((e) => [remixHref(e.tab), e.mode]));
  return groups.map((g) => ({
    ...g,
    items: g.items.filter((i) => {
      const mode = modeOf.get(i.href);
      if (mode === "expert") return expert;
      if (mode === "simple-only") return !expert;
      return true;
    }),
  }));
}

/** Vues secondaires des onglets regroupés, portées par `?view=`. */
export const REMIX_VIEWS = {
  apercu: [
    { key: "resume", fr: "Résumé", en: "Summary" },
    { key: "catalogue", fr: "Catalogue", en: "Catalogue" },
    { key: "recherches", fr: "Recherches", en: "Searches" },
    { key: "sources", fr: "Sources", en: "Sources" },
    { key: "usage", fr: "Utilisation", en: "Usage" },
  ],
  indexation: [
    { key: "reglages", fr: "Réglages", en: "Settings" },
    { key: "releases", fr: "Releases", en: "Releases" },
    { key: "pre", fr: "Pre (bêta)", en: "Pre (beta)" },
  ],
  lecture: [
    { key: "priorites", fr: "Priorités", en: "Priorities" },
    { key: "qualite", fr: "Qualité", en: "Quality" },
    { key: "vostfr", fr: "VOSTFR", en: "Non-dubbed titles" },
    { key: "soustitres", fr: "Sous-titres", en: "Subtitles" },
    { key: "continuer", fr: "Continuer à regarder", en: "Continue watching" },
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
  if (tab === "demarrage") return "/remix/accueil";
  if (tab === "releases") return keep("/remix/indexation", "releases");
  if (tab === "priorites") return "/remix/lecture";
  if (tab === "films") return keep("/remix/lecture", "qualite");
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
