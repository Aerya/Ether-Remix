export type Copy = readonly [string, string];
export type Content = "movie" | "show" | "anime" | "music";
export type Usage = "stream" | "download" | "local";
export interface SetupChoices {
  contents: Content[];
  usages: Usage[];
  discovery: string[];
  sources: string[];
  clients: string[];
  subtitles: boolean;
  original: boolean;
}
export const initialChoices: SetupChoices = {
  contents: ["movie", "show"],
  usages: ["stream"],
  discovery: ["catalogue"],
  sources: [],
  clients: ["plex"],
  subtitles: false,
  original: false,
};
export const contentOptions: { id: Content; label: Copy; help: Copy }[] = [
  {
    id: "movie",
    label: ["Films", "Movies"],
    help: [
      "Cinéma, documentaires, concerts et vidéos personnelles.",
      "Cinema, documentaries, concerts and personal videos.",
    ],
  },
  {
    id: "show",
    label: ["Séries", "Shows"],
    help: ["Épisodes et saisons.", "Episodes and seasons."],
  },
  {
    id: "anime",
    label: ["Animés", "Anime"],
    help: [
      "Une sélection dédiée de films et séries.",
      "A dedicated selection of movies and shows.",
    ],
  },
  {
    id: "music",
    label: ["Musique", "Music"],
    help: ["Albums, morceaux et playlists.", "Albums, tracks and playlists."],
  },
];
export const usageOptions: { id: Usage; label: Copy; help: Copy }[] = [
  {
    id: "stream",
    label: ["Lire à la demande", "Play on demand"],
    help: [
      "Chercher une source à la lecture. Du cache ou une préparation peut rester nécessaire.",
      "Find a source at playback. Cache or preparation may still be needed.",
    ],
  },
  {
    id: "download",
    label: ["Télécharger aussi", "Also download"],
    help: [
      "Conserver les fichiers, avec du stockage et parfois une attente avant lecture.",
      "Keep files, using storage and sometimes waiting before playback.",
    ],
  },
  {
    id: "local",
    label: ["Utiliser mes fichiers", "Use my files"],
    help: [
      "Médias accessibles à Remix, même sans correspondance TMDB.",
      "Media accessible to Remix, even without a TMDB match.",
    ],
  },
];
export interface SetupSource {
  id: string;
  label: Copy;
  help: Copy;
  href: string;
  usages: Usage[];
  music?: boolean;
}
export const setupSources: SetupSource[] = [
  {
    id: "debrid",
    label: ["Debrid intégré", "Built-in debrid"],
    help: [
      "Votre compte et des sources compatibles. Disponibilité en cache selon le fournisseur.",
      "Your account and compatible sources. Cache availability depends on the provider.",
    ],
    href: "/remix/sources?source=debrid",
    usages: ["stream"],
  },
  {
    id: "usenet",
    label: ["Usenet intégré", "Built-in Usenet"],
    help: [
      "Fournisseur NNTP et indexeurs NZB requis. Cache, préparation et démarrage variables.",
      "NNTP provider and NZB indexers required. Cache, preparation and startup time vary.",
    ],
    href: "/remix/sources?source=usenet",
    usages: ["stream"],
  },
  {
    id: "stremio",
    label: ["Addons Stremio", "Stremio add-ons"],
    help: [
      "Catalogue, recherche ou lecture selon l’addon ; ses comptes et son fonctionnement restent distincts.",
      "Catalogue, search or playback depending on the add-on; accounts and behaviour remain separate.",
    ],
    href: "/remix/sources?source=stremio",
    usages: ["stream"],
  },
  {
    id: "torrent",
    label: ["Client BitTorrent direct", "Direct BitTorrent client"],
    help: [
      "qBittorrent ou rTorrent. Dossier partagé ; lecture à 100 %. Régler stockage, ratio et durée de partage.",
      "qBittorrent or rTorrent. Shared folder; playback at 100%. Configure storage, ratio and seeding time.",
    ],
    href: "/remix/sources?source=torrent",
    usages: ["download"],
  },
  {
    id: "seedstrem",
    label: ["Seedstrem", "Seedstrem"],
    help: [
      "Remix reçoit un flux HTTP. Téléchargement, stockage et partage restent côté Seedstrem/client distant.",
      "Remix receives HTTP streams. Downloading, storage and seeding remain with Seedstrem/the remote client.",
    ],
    href: "/remix/sources?source=stremio",
    usages: ["stream", "download"],
  },
  {
    id: "local",
    label: ["Dossiers locaux ou montés", "Local or mounted folders"],
    help: [
      "Dossiers visibles par Remix. Les fichiers originaux sont conservés.",
      "Folders visible to Remix. Original files are preserved.",
    ],
    href: "/remix/sources?source=local",
    usages: ["local"],
  },
  {
    id: "ftp",
    label: ["FTP / SFTP", "FTP / SFTP"],
    help: [
      "Vos fichiers distants ; lecture distante possible avec cache.",
      "Your remote files; remote playback with cache is supported.",
    ],
    href: "/remix/sources?source=ftp",
    usages: ["stream", "local"],
  },
  {
    id: "xdcc",
    label: ["XDCC / Grabscher", "XDCC / Grabscher"],
    help: [
      "Service externe et stockage. Télécharger avant lecture.",
      "External service and storage. Download before playback.",
    ],
    href: "/remix/sources?source=xdcc",
    usages: ["download"],
  },
  {
    id: "amule",
    label: ["aMule", "aMule"],
    help: [
      "amuleapi et dossier partagé. Lecture après réception complète.",
      "amuleapi and shared folder. Playback after complete download.",
    ],
    href: "/remix/sources?source=amule",
    usages: ["download"],
  },
  {
    id: "slskd",
    label: ["Soulseek / slskd", "Soulseek / slskd"],
    help: [
      "Service slskd et dossier partagé. Vidéo et musique après réception complète.",
      "slskd service and shared folder. Video and music after complete download.",
    ],
    href: "/remix/sources?source=slskd",
    usages: ["download"],
  },
  {
    id: "music",
    label: ["Fournisseurs musicaux", "Music providers"],
    help: [
      "Sources qui lisent les morceaux ; le catalogue seul ne garantit pas la lecture.",
      "Sources that play tracks; the catalogue alone does not guarantee playback.",
    ],
    href: "/remix/sources?source=musique",
    usages: ["stream"],
    music: true,
  },
  {
    id: "ibroadcast",
    label: ["iBroadcast", "iBroadcast"],
    help: [
      "Votre compte et votre bibliothèque musicale. Privés par défaut.",
      "Your account and music library. Private by default.",
    ],
    href: "/remix/sources?source=local",
    usages: ["stream", "local"],
    music: true,
  },
];
export const discoveryOptions: {
  id: string;
  label: Copy;
  help: Copy;
  href: string;
  music?: boolean;
}[] = [
  {
    id: "catalogue",
    label: ["Catalogue Remix", "Remix catalogue"],
    help: [
      "Nouveautés et sélections intégrées ; aucune nouvelle source de lecture.",
      "Built-in releases and selections; not a new playback source.",
    ],
    href: "/remix/contenus",
  },
  {
    id: "jukebox",
    label: ["JukeBox / MDBList", "JukeBox / MDBList"],
    help: [
      "Listes à personnaliser en collections et bibliothèques.",
      "Lists to customise as collections and libraries.",
    ],
    href: "/remix/contenus?content=bibliotheques",
  },
  {
    id: "addons",
    label: ["Catalogues d’addons / Xperience", "Add-on catalogues / Xperience"],
    help: [
      "Importer les sélections des addons, séparément de leurs sources de lecture.",
      "Import add-on selections separately from their playback sources.",
    ],
    href: "/remix/contenus?content=bibliotheques",
  },
  {
    id: "seerr",
    label: ["SeerrCatalog", "SeerrCatalog"],
    help: [
      "Catalogue, partage des disponibilités et recherche via Remix sont trois fonctions différentes.",
      "Catalogue, availability sharing and search through Remix are three different functions.",
    ],
    href: "/remix/sources?source=stremio",
  },
  {
    id: "rss",
    label: ["RSS Catalog", "RSS Catalog"],
    help: [
      "Importer le catalogue de l’addon RSS Catalog. Distinct des annonces pre au format RSS.",
      "Import the RSS Catalog add-on catalogue. Separate from RSS pre announcements.",
    ],
    href: "/remix/contenus?content=bibliotheques",
  },
  {
    id: "pre",
    label: ["Annonces pre · bêta", "Pre announcements · beta"],
    help: [
      "Annonces RSS/API sans lien de lecture : disponibilité non prouvée.",
      "RSS/API announcements without playback links: availability is not proven.",
    ],
    href: "/remix/indexation?view=pre",
  },
  {
    id: "sfr",
    label: ["StreamFusion Reborn", "StreamFusion Reborn"],
    help: [
      "Import des références d’une base StreamFusion existante ; configurer le chemin et les sources de lecture.",
      "Import references from an existing StreamFusion database; configure its path and playback sources.",
    ],
    href: "/remix/sources?source=stremio#pse-sfr",
  },
  {
    id: "music-playlists",
    label: ["Playlists et mixes programmés", "Playlists and scheduled mixes"],
    help: [
      "Imports Deezer, Spotify, ListenBrainz ou Last.fm et mises à jour programmées. Lecture selon vos sources musicales.",
      "Deezer, Spotify, ListenBrainz or Last.fm imports and scheduled updates. Playback depends on your music sources.",
    ],
    href: "/remix/contenus?content=musique",
    music: true,
  },
  {
    id: "audiomuse",
    label: ["AudioMuse", "AudioMuse"],
    help: [
      "Service externe facultatif : similarités entre morceaux locaux déjà analysés, pour les mixes personnels.",
      "Optional external service: similarities between analysed local tracks for personal mixes.",
    ],
    href: "/remix/contenus?content=musique",
    music: true,
  },
  {
    id: "digarr",
    label: ["Digarr", "Digarr"],
    help: [
      "Service externe et jeton personnel en lecture seule par spectateur ; recommandations d’artistes et albums, pas une source audio.",
      "External service and personal read-only token per viewer; artist and album recommendations, not an audio source.",
    ],
    href: "/remix/contenus?content=musique",
    music: true,
  },
];
export const clientOptions: { id: string; label: Copy; help: Copy }[] = [
  {
    id: "plex",
    label: ["Plex / Plexamp", "Plex / Plexamp"],
    help: [
      "Accueil et bibliothèques par spectateur et appareil.",
      "Home and libraries per viewer and device.",
    ],
  },
  {
    id: "jellyfin",
    label: ["Jellyfin / Infuse", "Jellyfin / Infuse"],
    help: [
      "Bibliothèques et filtres des derniers ajouts Films et Séries.",
      "Libraries and latest movie/show filters.",
    ],
  },
  {
    id: "emby",
    label: ["Emby", "Emby"],
    help: [
      "Clients compatibles avec le serveur Emby de Remix.",
      "Clients compatible with Remix’s Emby server.",
    ],
  },
  {
    id: "stremio",
    label: ["Stremio", "Stremio"],
    help: [
      "Lien personnel HTTPS : catalogues ou fichiers seuls, affichage et versions par qualité.",
      "Personal HTTPS link: catalogues or files only, display and versions per quality.",
    ],
  },
  {
    id: "nuvio",
    label: ["Nuvio", "Nuvio"],
    help: [
      "Lien Stremio et compte lié par spectateur pour synchroniser reprise et historique.",
      "Stremio link and per-viewer linked account to sync resume and history.",
    ],
  },
  {
    id: "subsonic",
    label: ["Clients musicaux Subsonic", "Subsonic music clients"],
    help: [
      "Symfonium, Feishin et autres clients avec accès musical personnel.",
      "Symfonium, Feishin and other clients with personal music access.",
    ],
  },
  {
    id: "other",
    label: ["Autres applications", "Other apps"],
    help: [
      "Silo, WebDAV, IPTV et DLNA : accès à activer selon votre application.",
      "Silo, WebDAV, IPTV and DLNA: enable access for your app.",
    ],
  },
];
export function visibleDiscoveryOptions(c: SetupChoices) {
  return discoveryOptions.filter((o) => !o.music || c.contents.includes("music"));
}
export function visibleSetupSources(c: SetupChoices) {
  return setupSources.filter(
    (s) => (!s.music || c.contents.includes("music")) && s.usages.some((u) => c.usages.includes(u)),
  );
}
const pick = <T extends string>(value: unknown, allowed: readonly T[], fallback: T[]): T[] =>
  Array.isArray(value)
    ? [...new Set(value.filter((v): v is T => typeof v === "string" && allowed.includes(v as T)))]
    : fallback;
/** Stored answers are preferences only; never trust stored API bodies or URLs. */
export function restoreChoices(value: unknown): SetupChoices {
  const v = value && typeof value === "object" ? (value as Partial<SetupChoices>) : {};
  return {
    contents: pick(
      v.contents,
      contentOptions.map((x) => x.id),
      initialChoices.contents,
    ),
    usages: pick(
      v.usages,
      usageOptions.map((x) => x.id),
      initialChoices.usages,
    ),
    discovery: pick(
      v.discovery,
      discoveryOptions.map((x) => x.id),
      initialChoices.discovery,
    ),
    sources: pick(
      v.sources,
      setupSources.map((x) => x.id),
      [],
    ),
    clients: pick(
      v.clients,
      clientOptions.map((o) => o.id),
      initialChoices.clients,
    ),
    original: v.original === true,
    subtitles: v.subtitles === true,
  };
}
