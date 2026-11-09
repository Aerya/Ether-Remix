import { getLocale } from "../i18n";

/**
 * Onglet « Stremio » de Sources (PSE) : les addons Stremio restent des
 * indexeurs d'Ether (`kind: "stremio"`, même section de configuration et
 * même ordre de recherche), mais s'affichent dans leur propre onglet au lieu
 * d'être mêlés aux sites et aux URL Newznab/Torznab. Voir docs/pse/FORK.fr.md.
 */

/** Ce qu'affiche une liste d'indexeurs : tout (Ether), sans Stremio, ou Stremio seul. */
export type PseIndexerView = "all" | "indexers" | "stremio" | "bittorrent" | "usenet-indexers";

export function pseShowsIndexer(
  view: PseIndexerView,
  ix: { kind?: string; protocol?: string },
): boolean {
  if (view === "all") return true;
  if (view === "bittorrent") return ix.kind !== "stremio" && ix.protocol === "torrent";
  if (view === "usenet-indexers") return ix.kind !== "stremio" && ix.protocol !== "torrent";
  return (ix.kind === "stremio") === (view === "stremio");
}

/**
 * Replace les éléments visibles réordonnés (`next`) aux emplacements qu'ils
 * occupaient dans la liste complète (`slots`), sans bouger les indexeurs
 * masqués par le filtre.
 */
export function pseMergeOrder<T>(all: T[], slots: number[], next: T[]): T[] {
  const out = all.slice();
  slots.forEach((at, k) => {
    if (next[k] !== undefined) out[at] = next[k];
  });
  return out;
}

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export const pseStremioText = {
  description: () =>
    L(
      "Addons Stremio interrogés quand quelqu'un lance une lecture, dans l'ordre de recherche des sources. Collez l'URL du manifest de l'addon de votre choix.",
      "Stremio addons queried when someone presses Play, in the sources' search order. Paste the manifest URL of your chosen addon.",
    ),
  emptyTitle: () => L("Aucun addon Stremio", "No Stremio addons"),
  emptyDescription: () =>
    L(
      "Ajoutez l'URL d'un addon Stremio pour qu'Ether y cherche des flux.",
      "Add a Stremio addon URL so Ether can find streams there.",
    ),
  add: () => L("Ajouter un addon", "Add an addon"),
  indexersEmpty: () =>
    L(
      "Ajoutez un site ou un indexeur Newznab ou Torznab pour qu'Ether trouve des releases. Les addons Stremio ont leur propre onglet.",
      "Add a site or a Newznab or Torznab indexer so Ether can find releases. Stremio addons have their own tab.",
    ),
};
