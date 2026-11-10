import { Button } from "@braedonsaunders/appkit-ui";
import { Sparkles, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const SEEN_KEY = "ether-remix.updates.seen"; // Ancien repère, conservé pour la migration.
const READ_KEY = "ether-remix.updates.read";
interface ReadProgress {
  version: string;
  ids: string[];
}

// Les nombres de build se comparent numériquement (remix.10 > remix.9).
export function compareUpdateVersions(a: string, b: string): number | null {
  const parse = (value: string) => {
    const match = /^v?(\d+)\.(\d+)\.(\d+)-remix\.(\d+)\.(\d+)$/.exec(value);
    return match ? match.slice(1).map(Number) : null;
  };
  const av = parse(a),
    bv = parse(b);
  if (!av || !bv) return null;
  for (let i = 0; i < av.length; i++) if (av[i] !== bv[i]) return av[i]! > bv[i]! ? 1 : -1;
  return 0;
}
// Ajouter les nouveautés ici à chaque livraison, de la plus récente à la plus ancienne.
export const REMIX_UPDATES = [
  {
    id: "2026-11-10-scrob-slow-connection",
    version: "current",
    date: "2026-11-10",
    title: ["Scrob : connexions lentes", "Scrob: slow connections"],
    items: [[
      "Le test du webhook et les envois de lecture attendent désormais jusqu’à 60 secondes au lieu de 15 pour les connexions lentes.",
      "Webhook tests and playback events now wait up to 60 seconds instead of 15 on slow connections.",
    ]],
  },
  {
    id: "2026-11-10-scrob-single-connection",
    version: "0.2.37-remix.34.1",
    date: "2026-11-10",
    title: ["Scrob : une seule connexion Jellyfin", "Scrob: one Jellyfin connection"],
    items: [
      [
        "Le parcours explique la connexion Jellyfin unique et le choix des sens de synchronisation dans Scrob. Le webhook temps réel et les bibliothèques limitées à l’historique sont présentés comme facultatifs. Le panneau distingue un accès Jellyfin observé d’une synchronisation terminée et explique les erreurs de connexion liées au proxy.",
        "Setup now explains one Jellyfin connection, with sync directions chosen in Scrob. Real-time webhooks and history-only libraries are explicitly optional. The panel distinguishes observed Jellyfin access from completed sync and explains proxy-related connection errors.",
      ],
    ],
  },
  {
    id: "2026-11-10-seerrcatalog-independent-search-auth",
    version: "0.2.37-remix.33.1",
    date: "2026-11-10",
    title: ["SeerrCatalog : recherches indépendantes", "SeerrCatalog: independent searches"],
    items: [
      [
        "La recherche complémentaire possède maintenant sa propre clé et son propre interrupteur dans Sources → Stremio → Recherches pour SeerrCatalog. Le partage des disponibilités peut rester désactivé. Après mise à jour des deux applications, générez une clé dans SeerrCatalog → Recherche complémentaire et copiez-la dans ce nouveau bloc.",
        "Complementary search now has its own token and switch under Sources → Stremio → Searches for SeerrCatalog. Availability sharing can remain disabled. After updating both applications, generate a token in SeerrCatalog → Complementary search and copy it into this new section.",
      ],
    ],
  },
  {
    id: "2026-11-10-statistics-dashboard",
    version: "0.2.37-remix.32.1",
    date: "2026-11-10",
    title: ["Les statistiques vous accueillent", "Statistics welcome you"],
    items: [
      [
        "Le menu Remix ouvre directement les Statistiques. Des anneaux colorés montrent la progression des recherches pour les films, séries et animes ; l’historique adopte des barres fines dégradées. Les filtres et valeurs cumulées restent disponibles.",
        "The Remix menu now opens Statistics. Coloured rings show search progress for movies, shows and anime, while history uses slim gradient bars. Filters and cumulative values remain available.",
      ],
    ],
  },
  {
    id: "2026-11-10-scrob-refresh-status",
    version: "0.2.37-remix.31.1",
    date: "2026-11-10",
    title: [
      "Scrob : état actualisé et rafraîchissement Jellyfin",
      "Scrob: live status and Jellyfin refresh",
    ],
    items: [
      [
        "Le dernier accès Jellyfin et l’état du webhook sont distingués et actualisés sans F5. La demande de rafraîchissement du catalogue virtuel ne renvoie plus une erreur 404. Les listes d’épisodes sans tri utilisent la pagination rapide au lieu de parcourir toutes les séries. Pull reste l’action d’import dans Scrob.",
        "Latest Jellyfin access and webhook status are distinguished and refreshed without reloading. Virtual catalogue refresh requests no longer return 404. Episode lists without an explicit sort use fast pagination instead of walking every series. Pull remains the action that imports into Scrob.",
      ],
    ],
  },
  {
    id: "2026-11-10-scrob-bidirectional",
    version: "0.2.37-remix.30.1",
    date: "2026-11-10",
    title: ["Scrob dans les deux sens", "Bidirectional Scrob"],
    items: [
      [
        "Créez les identifiants Jellyfin de chaque spectateur depuis les réglages Scrob. Le retour des statuts vus et des notes retrouve précisément les épisodes par série, saison et numéro. L’aide distingue la connexion complète du simple webhook.",
        "Create each viewer’s Jellyfin credentials from Scrob settings. Incoming watched status and ratings resolve the exact episode by series, season and number. Setup help distinguishes a full connection from a webhook alone.",
      ],
    ],
  },
  {
    id: "2026-11-10-seerrcatalog-delegated-search",
    version: "0.2.37-remix.29.1",
    date: "2026-11-10",
    title: ["SeerrCatalog : recherche complémentaire", "SeerrCatalog: complementary search"],
    items: [
      [
        "SeerrCatalog peut demander à Remix de compléter une recherche sans résultat dans les langues principales, via ses indexeurs dont Prowlarr. La clé partagée protège ce retour ; les budgets et les règles de validation SeerrCatalog restent appliqués. À activer et tester dans les paramètres SeerrCatalog.",
        "SeerrCatalog can ask Remix to complement searches with no primary-language results, using its indexers including Prowlarr. The shared token protects this response; budgets and SeerrCatalog confirmation rules still apply. Enable and test it in SeerrCatalog settings.",
      ],
    ],
  },
  {
    id: "2026-11-10-risky-titles",
    version: "0.2.37-remix.28.1",
    date: "2026-11-10",
    title: ["Titres à une seule release", "Titles with a single release"],
    items: [
      [
        "Nouvelle option dans le catalogue lisible : afficher aussi les titres qui n’ont pas le minimum de releases, en prévenant les spectateurs par « ⚠ 1 source » dans le résumé, un badge « 1 SOURCE » sur les affiches Ether, ou les deux.",
        "New playable-catalogue option: also show titles below the minimum number of releases, warning viewers with “⚠ 1 source” in the summary, a “1 SOURCE” badge on Ether posters, or both.",
      ],
      [
        "Les réglages d’indexation et les statistiques précisent que le catalogue local est limité par Ether (Masquer les titres obscurs), pas par Remix.",
        "Indexing settings and statistics now explain that Ether, not Remix, limits the local catalogue (Hide obscure titles).",
      ],
    ],
  },
  {
    id: "2026-11-10-seerrcatalog-permanent-availability",
    version: "0.2.37-remix.27.1",
    date: "2026-11-10",
    title: ["SeerrCatalog : disponibilité confirmée", "SeerrCatalog: confirmed availability"],
    items: [
      [
        "Une disponibilité confirmée par SeerrCatalog reste acquise jusqu’au retrait du titre ou à la désactivation de l’intégration. Les liens de lecture expirent toujours après 30 minutes ; aucune nouvelle recherche d’indexation n’est nécessaire.",
        "Availability confirmed by SeerrCatalog persists until the title is removed or the integration is disabled. Playback links still expire after 30 minutes; no new indexing search is needed.",
      ],
    ],
  },
  {
    id: "2026-11-10-indexer-whole-catalogue",
    version: "0.2.37-remix.26.1",
    date: "2026-11-10",
    title: ["L’indexation parcourt tout le catalogue", "Indexing covers the whole catalogue"],
    items: [
      [
        "Avec le catalogue en français, les titres étrangers sans VF connue n’étaient jamais cherchés : surtout des séries. L’indexation les parcourt désormais pour leur trouver une VF.",
        "With the French catalogue, foreign titles without a known French release were never searched, mostly TV shows. Indexing now goes through them to find one.",
      ],
    ],
  },
  {
    id: "2026-11-10-playlists-palettes",
    version: "0.2.37-remix.25.1",
    date: "2026-11-10",
    title: ["Des thèmes plus doux et vos playlists", "Softer themes and your playlists"],
    items: [
      [
        "Six palettes au choix dans Apparence, chacune en clair ou sombre, avec aperçu immédiat et mémorisation par navigateur.",
        "Six palettes in Appearance, each with light and dark modes, instant previews and browser-local preferences.",
      ],
      [
        "Playlists : découverte Deezer, connexion Spotify personnelle, imports par lien ou fichier CSV/XSPF. Aperçu avant validation, choix du spectateur et suivi des titres retrouvés ou manquants.",
        "Playlists: Deezer discovery, personal Spotify connection, link and CSV/XSPF imports. Preview before confirming, viewer selection and matched/unmatched track progress.",
      ],
      [
        "Les imports créent des copies personnelles sans remplacer les autres playlists. Les profils Deezer publics, Last.fm et ListenBrainz conservent leur synchronisation automatique.",
        "Imports create personal copies without replacing other playlists. Public Deezer profiles, Last.fm and ListenBrainz keep their automatic sync.",
      ],
    ],
  },
  {
    id: "2026-11-10-search-queues-indexing",
    version: "0.2.37-remix.24.1",
    date: "2026-11-10",
    title: ["Une recherche lance l’indexation", "Searching starts indexing"],
    items: [
      [
        "Une recherche dans Plex ou Jellyfin fait passer en tête de file les 5 premiers titres trouvés, s’ils n’ont jamais été cherchés ou n’avaient rien donné il y a plus de 6 h.",
        "A Plex or Jellyfin search moves the first 5 titles found to the front of the indexing queue, if they were never searched or found nothing more than 6 h ago.",
      ],
      [
        "Le README explique quels titres sont affichés et dans quel ordre ils sont cherchés.",
        "The README explains which titles are shown and in which order they are searched.",
      ],
    ],
  },
  {
    id: "2026-11-09-seerrcatalog-connection-test",
    version: "0.2.37-remix.23.1",
    date: "2026-11-09",
    title: ["Tester la connexion à SeerrCatalog", "Test the SeerrCatalog connection"],
    items: [
      [
        "Un bouton vérifie la clé et le flux avec les réglages enregistrés. Les champs adresse et clé sont alignés ; seule l’adresse de base est nécessaire.",
        "A button checks the token and feed using saved settings. Address and token fields are aligned; only the base address is required.",
      ],
    ],
  },
  {
    id: "2026-11-09-collapsible-source-blocks",
    version: "0.2.37-remix.22.1",
    date: "2026-11-09",
    title: ["Des blocs repliables dans les sources", "Collapsible source sections"],
    items: [
      [
        "Dans Sources → Stremio, Indexeurs et Indexation, chaque bloc se replie et se déplie depuis son titre, sans perdre les réglages en cours de saisie.",
        "Under Sources → Stremio, Indexers and Indexing, each section can be collapsed or expanded from its title without losing unsaved settings.",
      ],
    ],
  },
  {
    id: "2026-11-09-seerrcatalog-integration",
    version: "0.2.37-remix.19.1",
    date: "2026-11-09",
    title: [
      "SeerrCatalog partage ses résultats avec Remix",
      "SeerrCatalog shares its results with Remix",
    ],
    items: [
      [
        "Dans Sources → Stremio, activez l’intégration facultative SeerrCatalog : disponibilités sans recherche en double, sources récentes réutilisées à la lecture, reprise des modifications et suppressions après une coupure.",
        "Under Sources → Stremio, enable the optional SeerrCatalog integration: availability without duplicate searches, recent streams reused for playback, and recovery of changes and deletions after outages.",
      ],
      [
        "Les deux interfaces et les README proposent les liens GitHub SeerrCatalog et Ether Remix, ainsi que les étapes de connexion.",
        "Both interfaces and READMEs include SeerrCatalog and Ether Remix GitHub links and connection instructions.",
      ],
    ],
  },
  {
    id: "2026-11-09-client-browsing",
    version: "0.2.37-remix.10.1",
    date: "2026-11-09",
    title: ["Navigation plus rapide dans Plex et Jellyfin", "Faster browsing in Plex and Jellyfin"],
    items: [
      [
        "Les badges des affiches sont préparés par page : moins de lectures répétées des collections et des épisodes, avec les états propres à chaque spectateur.",
        "Poster badges are prepared per page, reducing repeated collection and episode reads while preserving each viewer’s state.",
      ],
      [
        "Le catalogue lisible uniquement recherche directement les titres éligibles pour accélérer les listes et leurs compteurs, avec les mêmes filtres et exceptions de langue.",
        "The playable-only catalogue looks up eligible titles directly to speed up lists and counts, preserving language filters and exceptions.",
      ],
    ],
  },
  {
    id: "2026-11-09-unread-notes",
    version: "0.2.37-remix.6.1",
    date: "2026-11-09",
    title: [
      "Des réglages plus clairs et uniquement les nouveautés non lues",
      "Clearer settings and only unread updates",
    ],
    items: [
      [
        "La popup affiche uniquement les nouveautés depuis votre dernière version lue. Les notes déjà consultées restent dans l’historique, accessible à la demande.",
        "The popup only shows updates since your last read version. Previously read notes remain available in the optional history.",
      ],
      [
        "Sources musicales regroupées : Eclipse, DMM / Real-Debrid avec ses comptes associés, puis les autres sources. Le diagnostic audio reste commun.",
        "Music sources grouped into Eclipse, DMM / Real-Debrid with its linked accounts, and other sources. Audio diagnostics remain shared.",
      ],
      [
        "Dans Debrid, affichez puis masquez la clé API du compte sélectionné. La clé reste masquée dans la configuration ordinaire.",
        "Under Debrid, show or hide the selected account’s API key. Normal configuration responses keep keys masked.",
      ],
      [
        "Les sources importées se filtrent et se trient aussi par contenus concernés. Priorité de recherche est repliable et l’encart générique sur les instances Stremio a été retiré.",
        "Imported sources can also be filtered and sorted by content. Search priority is collapsible and the generic Stremio instance notice has been removed.",
      ],
    ],
  },
  {
    id: "2026-11-09-iptv-editor",
    version: "0.2.37-remix.5.1",
    date: "2026-11-09",
    title: [
      "Sources Stremio simplifiées et IPTV corrigé",
      "Simpler Stremio sources and IPTV fixed",
    ],
    items: [
      [
        "Les fournisseurs IPTV s’ouvrent et s’enregistrent depuis Sources → IPTV ; la catégorie reste sélectionnée à la fermeture du formulaire.",
        "IPTV providers open and save under Sources → IPTV; the category stays selected when closing the editor.",
      ],
      [
        "Ajoutez librement vos addons dans Sources → Stremio avec leur manifest. Les suggestions de fournisseurs ont été retirées.",
        "Add any addon under Sources → Stremio using its manifest. Provider suggestions have been removed.",
      ],
    ],
  },
  {
    id: "2026-11-09-sources-sync",
    version: "0.2.37-remix.4.1",
    date: "2026-11-09",
    title: ["Sources réunies et navigation simplifiée", "Unified sources and simpler navigation"],
    items: [
      [
        "Un seul accès aux sources dans Remix : les comptes et addons Ether sont réutilisés, avec les sources musicales et l’indexation.",
        "One source entry in Remix reuses Ether accounts and addons, alongside music sources and background indexing.",
      ],
      [
        "Statistiques Animes : carte dédiée, historique par période ou cumulé et filtre par source, sans double comptage. Six onglets : Sources, Langues et qualité, VOSTFR à la demande, Contenus et bibliothèques, Statistiques, Notifications et suivi.",
        "Anime statistics: dedicated card, period or cumulative history and source filtering, without double counting. Six tabs: Sources, Languages & quality, On-demand subtitles, Content & libraries, Statistics, Notifications & tracking.",
      ],
      [
        "Sélection des sources d’indexation regroupée par catégorie, avec tri et indication des contenus concernés.",
        "Background indexing sources can be filtered and sorted by category, with their relevant content shown.",
      ],
      [
        "Les anciens liens restent utilisables. Les nouveautés d’Ether sont intégrées ; la synchronisation conserve mieux les traductions du fork.",
        "Existing links keep working. Ether changes are integrated; synchronization better preserves fork translations.",
      ],
    ],
  },
  {
    id: "2026-11-09-animes",
    version: "0.2.37-remix.3.1",
    date: "2026-11-09",
    title: ["Les animés rejoignent Ether-Remix", "Anime joins Ether-Remix"],
    items: [
      [
        "Les sources distinguent les instances publiques et proposent les liens d’auto-hébergement disponibles.",
        "Sources identify public instances and link to available self-hosting projects.",
      ],
      [
        "Onglet À la demande : les exceptions VOSTFR sont séparées du catalogue FR/MULTi. Chaque ajout lance une recherche prioritaire si l’indexation est active.",
        "On demand tab: subtitled exceptions are separate from the French/MULTi catalogue. Each addition queues a priority search when indexing is enabled.",
      ],
      [
        "Onglet Animes : bibliothèques et collections TMDB, avec vos critères de langue FR/MULTi.",
        "Anime tab: TMDB libraries and collections, respecting your language criteria.",
      ],
      [
        "Ajout libre des addons Stremio, avec priorité et sources partagées.",
        "Add any Stremio addon, with shared sources and priorities.",
      ],
      [
        "Ce panneau présente les nouveautés après chaque livraison et reste accessible avec le bouton Nouveautés.",
        "This panel presents changes after each release and remains available from the What's new button.",
      ],
    ],
  },
  {
    id: "2026-11-09-remix",
    version: "0.2.37-remix.3.1",
    date: "2026-11-09",
    title: ["Accent devient Ether-Remix", "Accent becomes Ether-Remix"],
    items: [
      [
        "Nouvelle identité, nouveaux logos et image Docker publique pour amd64 et arm64.",
        "New identity, new logos and public Docker image for amd64 and arm64.",
      ],
      [
        "Stats : comparaison TMDB, historique par jour, semaine, mois et année, par média et source, en valeurs simples ou cumulées.",
        "Stats: TMDB comparison, daily, weekly, monthly and yearly history by media and source, with period totals or cumulative values.",
      ],
      [
        "Sources musicales communautaires, DMM avec Real-Debrid, playlists et API Subsonic.",
        "Community music sources, DMM with Real-Debrid, playlists and Subsonic API.",
      ],
    ],
  },
] as const;

type Release = (typeof REMIX_UPDATES)[number];
const noteVersion = (release: Release, running: string) =>
  release.version === "current" ? running.replace(/^v/, "") : release.version;
function availableNotes(version: string): Release[] {
  return REMIX_UPDATES.filter(
    (release) => (compareUpdateVersions(noteVersion(release, version), version) ?? 0) <= 0,
  );
}
function loadProgress(version: string): ReadProgress {
  const raw = localStorage.getItem(READ_KEY);
  if (raw) {
    try {
      const value = JSON.parse(raw);
      if (
        typeof value?.version === "string" &&
        Array.isArray(value.ids) &&
        value.ids.every((id: unknown) => typeof id === "string")
      ) {
        if (compareUpdateVersions(value.version, value.version) !== 0) {
          value.version = REMIX_UPDATES.filter(
            (r) => r.version !== "current" && value.ids.includes(r.id),
          ).reduce(
            (latest, r) =>
              (compareUpdateVersions(r.version, latest) ?? 1) > 0 ? r.version : latest,
            "",
          );
        }
        return value;
      }
    } catch {
      /* Reprendre l’ancien repère si la nouvelle valeur est invalide. */
    }
  }
  const legacy = localStorage.getItem(SEEN_KEY);
  const release = REMIX_UPDATES.find((r) => r.id === legacy);
  if (release)
    return {
      version: noteVersion(release, version),
      ids: REMIX_UPDATES.slice(REMIX_UPDATES.indexOf(release)).map((r) => r.id),
    };
  if (legacy && compareUpdateVersions(legacy, legacy) === 0) return { version: legacy, ids: [] };
  // Un ancien identifiant retiré ne doit pas ressusciter tout l’historique.
  return { version: "", ids: legacy ? REMIX_UPDATES.slice(1).map((r) => r.id) : [] };
}
function unreadNotes(version: string, read: ReadProgress): Release[] {
  return availableNotes(version).filter(
    (release) =>
      !read.ids.includes(release.id) &&
      (compareUpdateVersions(noteVersion(release, version), read.version) ?? 1) > 0,
  );
}

export function RemixUpdates({ version }: { version: string }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false);
  const [shown, setShown] = useState<Release[]>([]);
  const [history, setHistory] = useState(false);
  const [read, setRead] = useState<ReadProgress>({ version: "", ids: [] });
  useEffect(() => {
    try {
      const progress = loadProgress(version);
      const notes = unreadNotes(version, progress);
      setRead(progress);
      setShown(notes);
      setHistory(false);
      setOpen(notes.length > 0);
    } catch {
      /* L’accès manuel reste disponible si le stockage est bloqué. */
    }
  }, [version]);
  useEffect(() => {
    if (open && !dialog.current?.open) dialog.current?.showModal();
    else if (!open && dialog.current?.open) dialog.current.close();
  }, [open]);
  const openUnread = () => {
    let progress = read;
    try {
      progress = loadProgress(version);
    } catch {
      /* Stockage facultatif. */
    }
    setRead(progress);
    setShown(unreadNotes(version, progress));
    setHistory(false);
    setOpen(true);
  };
  const close = () => {
    if (shown.length) {
      let previous = read;
      try {
        previous = loadProgress(version);
      } catch {
        /* Stockage facultatif. */
      }
      const progress = {
        version:
          compareUpdateVersions(version, version) !== 0 ||
          (compareUpdateVersions(previous.version, version) ?? -1) > 0
            ? previous.version
            : version.replace(/^v/, ""),
        ids: [...new Set([...previous.ids, ...shown.map((r) => r.id)])],
      };
      setRead(progress);
      try {
        localStorage.setItem(READ_KEY, JSON.stringify(progress));
        if ((compareUpdateVersions(progress.version, version) ?? 0) <= 0)
          localStorage.setItem(SEEN_KEY, REMIX_UPDATES[0].id);
      } catch {
        /* Stockage facultatif. */
      }
    }
    setOpen(false);
    dialog.current?.close();
  };
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={openUnread}
        aria-label={L("Nouveautés Ether-Remix", "Ether-Remix what's new")}
      >
        <Sparkles size={16} aria-hidden />{" "}
        <span className="hidden sm:inline">{L("Nouveautés", "What's new")}</span>
      </Button>
      <dialog
        ref={dialog}
        aria-labelledby="remix-updates-title"
        onCancel={(event) => {
          event.preventDefault();
          close();
        }}
        onClose={() => setOpen(false)}
        className="m-auto w-[calc(100%-2rem)] max-w-2xl max-h-[85dvh] overflow-y-auto rounded-2xl border border-border bg-bg p-0 text-fg shadow-xl backdrop:bg-black/60"
      >
        <div className="flex items-center justify-between gap-4 border-b border-border p-5">
          <h2 id="remix-updates-title" className="text-lg font-semibold">
            {L("Nouveautés Ether-Remix", "What's new in Ether-Remix")}
          </h2>
          <Button variant="ghost" size="sm" onClick={close} aria-label={L("Fermer", "Close")}>
            <X size={18} />
          </Button>
        </div>
        <div className="space-y-6 p-5">
          {shown.length === 0 ? (
            <p className="text-sm text-fg-muted">
              {L(
                "Vous avez déjà lu toutes les nouveautés de cette version.",
                "You have already read all updates for this version.",
              )}
            </p>
          ) : null}
          {shown.map((release) => (
            <section key={release.id}>
              <time className="text-xs text-fg-muted" dateTime={release.date}>
                {new Date(`${release.date}T12:00:00`).toLocaleDateString(getLocale(), {
                  dateStyle: "long",
                })}{" "}
                · v{noteVersion(release, version)}
              </time>
              <h3 className="mt-1 font-semibold">{L(release.title[0], release.title[1])}</h3>
              <ul className="mt-2 list-disc space-y-2 pl-5 text-sm text-fg-muted">
                {release.items.map((item) => (
                  <li key={item[1]}>{L(item[0], item[1])}</li>
                ))}
              </ul>
            </section>
          ))}
        </div>
        <div className="flex flex-wrap justify-between gap-3 border-t border-border p-4">
          <Button
            variant="ghost"
            onClick={() => {
              if (history) openUnread();
              else {
                setShown(availableNotes(version));
                setHistory(true);
              }
            }}
          >
            {history
              ? L("Nouveautés non lues", "Unread updates")
              : L("Historique complet", "Full history")}
          </Button>
          <Button onClick={close}>{L("J’ai compris", "Got it")}</Button>
        </div>
      </dialog>
    </>
  );
}
