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
    id: "2026-11-14-ftp-source",
    version: "current",
    date: "2026-11-14",
    title: ["Source FTP / SFTP en option", "Optional FTP / SFTP source"],
    items: [
      [
        "Nouvelle source FTP / SFTP dans Sources → FTP / SFTP : Remix parcourt les dossiers choisis de votre serveur (seedbox, NAS distant), reconnaît films et épisodes, puis les propose comme les autres sources. Désactivée par défaut.",
        "New FTP / SFTP source under Sources → FTP / SFTP: Remix scans the chosen folders of your server (seedbox, remote NAS), recognises movies and episodes, then offers them like the other sources. Off by default.",
      ],
      [
        "Lecture directe depuis le serveur, sans copie complète (le cache habituel d’Ether reste applicable), avec avance et retour rapides partout dans la vidéo (y compris les MP4 dont l'index est à la fin). En SFTP, un saut se fait sur la même connexion ; en FTP, il en ouvre une nouvelle.",
        "Plays straight from the server without a complete copy (Ether’s usual cache still applies), with fast seeking anywhere in the video (including MP4 files whose index sits at the end). With SFTP a seek reuses the same connection; with FTP it opens a new one.",
      ],
      [
        "Modes avec téléchargement en option : copie complète avant lecture, ou lecture directe avec copie gardée. Remix n'efface que ses propres copies temporaires, jamais un fichier gardé ni une bibliothèque.",
        "Optional download modes: full copy before playback, or direct playback with a kept copy. Remix only deletes its own temporary copies, never a kept file or a library.",
      ],
      [
        "Mot de passe et phrase de passe jamais réaffichés ; clé SSH lue depuis un fichier monté. En SFTP, l'empreinte du serveur est obligatoire : « Tester la connexion » l'affiche pour que vous la vérifiiez. FTPS disponible pour le FTP chiffré.",
        "Password and passphrase are never shown again; the SSH key is read from a mounted file. With SFTP the server fingerprint is required: “Test connection” shows it so you can check it. FTPS is available for encrypted FTP.",
      ],
    ],
  },
  {
    id: "2026-11-14-jukebox-quick-setup",
    version: "0.2.37-remix.63.1",
    date: "2026-11-14",
    title: ["Configuration rapide JukeBox", "JukeBox quick setup"],
    items: [
      [
        "Créez vos bibliothèques Films, Séries et Animes depuis trois listes précochées, ou choisissez parmi les listes publiques JukeBox, dont les documentaires.",
        "Create Movies, Shows and Anime libraries from three preselected lists, or browse JukeBox’s public lists, including documentaries.",
      ],
      [
        "Bibliothèques pour Plex, Jellyfin et Emby, ou collections seules : actualisation quotidienne, titres sortis et tri par date de sortie décroissante. Vos critères de langue restent appliqués.",
        "Libraries for Plex, Jellyfin and Emby, or collections only: daily refresh, released titles and newest-release-first sorting. Your language settings still apply.",
      ],
      [
        "Un nouvel essai réutilise les éléments déjà créés sans écraser vos modifications. Merci à JukeBox pour ses listes MDBList !",
        "Retrying reuses existing entries without overwriting your edits. Thanks to JukeBox for the MDBList lists!",
      ],
    ],
  },
  {
    id: "2026-11-14-upstream-72",
    version: "0.2.37-remix.62.1",
    date: "2026-11-14",
    title: ["Nouveautés Ether intégrées", "Ether updates integrated"],
    items: [
      [
        "Améliorations des collections, de l’identification des épisodes et de la lecture. Les fonctions Remix restent disponibles : Scrob, médias locaux, SubMaker et profils de qualité.",
        "Improvements to collections, episode identification and playback. Remix features remain available: Scrob, local media, SubMaker and quality profiles.",
      ],
    ],
  },
  {
    id: "2026-11-14-xdcc-source",
    version: "0.2.37-remix.61.1",
    date: "2026-11-14",
    title: ["Source XDCC en option", "Optional XDCC source"],
    items: [
      [
        "Nouvelle source XDCC dans Sources → XDCC, via XDCC Grabscher installé à part. Désactivée par défaut : contrairement au debrid, les fichiers sont écrits sur votre disque.",
        "New XDCC source under Sources → XDCC, through a separately installed XDCC Grabscher. Off by default: unlike debrid, files are written to your disk.",
      ],
      [
        "Lecture pendant le transfert, téléchargement seulement, ou les deux. Le retour en arrière est immédiat ; un saut au-delà de la partie reçue est refusé sans fausses images.",
        "Play during the transfer, download only, or both. Seeking back is instant; a jump beyond the received part is refused, without fake frames.",
      ],
      [
        "La console affiche les bots et les transferts (disponible, bot hors ligne, en attente, en cours, prêt à lire, échec), permet une recherche manuelle et garde ou arrête un fichier. Le cache est plafonné ; un fichier gardé ou rangé dans une bibliothèque n'est jamais effacé.",
        "The console shows bots and transfers (available, bot offline, waiting, downloading, ready to play, failed), offers a manual search and keeps or stops a file. The cache is capped; a kept file or one inside a library is never deleted.",
      ],
      [
        "Les limites de téléchargement restent respectées lors des lectures simultanées. Le cache libère la place nécessaire au prochain fichier et protège les actions Garder et les lectures pendant le nettoyage.",
        "Download limits remain enforced for simultaneous playback starts. The cache makes room for the next file and protects Keep actions and playback during cleanup.",
      ],
    ],
  },
  {
    id: "2026-11-14-vf-libraries",
    version: "0.2.37-remix.60.1",
    date: "2026-11-14",
    title: ["Films et Séries toujours en VF", "Movies and Shows always in French"],
    items: [
      [
        "Un titre sans VF n'apparaît plus dans Films, Séries ni sur l'accueil, même s'il est choisi en VOSTFR, rangé dans une bibliothèque « Toutes langues » ou dans une collection d'exception. Il reste visible dans sa bibliothèque ou sa collection.",
        "A title without a French version no longer shows in Movies, Shows or the home screen, even when chosen as VOSTFR, placed in an “Any language” library or in an exception collection. It stays visible in its library or collection.",
      ],
      [
        "SeerrCatalog : un titre trouvé seulement en VOSTFR (ou en VO avec sous-titres) va dans la bibliothèque VOSTFR et la collection « SeerrCatalog · VOSTFR ». La collection « SeerrCatalog » regroupe tous ses titres disponibles. L'indexeur de Remix continue de leur chercher une VF.",
        "SeerrCatalog: a title found only with French subtitles (or original audio with subtitles) goes to the VOSTFR library and the “SeerrCatalog · VOSTFR” collection. The “SeerrCatalog” collection gathers all its available titles. The Remix indexer keeps looking for a French version.",
      ],
      [
        "Films et Séries s'ouvrent triés par date de sortie, du plus récent au plus ancien. Réglage dans Ether-Remix → Tri des bibliothèques. Un tri choisi dans l'application reste prioritaire.",
        "Movies and Shows open sorted by release date, newest first. Set it in Ether-Remix → Library sorting. A sort chosen in the app still wins.",
      ],
    ],
  },
  {
    id: "2026-11-14-posters-speed",
    version: "0.2.37-remix.59.1",
    date: "2026-11-14",
    title: ["Affiches plus rapides", "Faster posters"],
    items: [
      [
        "Avec un fournisseur d'affiches (PostersPlus, XRDB, RPDB…), Plex garde chaque affiche pendant un jour au lieu de la redemander au serveur à chaque affichage. L'affiche de repli TMDB, servie quand le fournisseur ne répond pas, reste revérifiée.",
        "With a poster provider (PostersPlus, XRDB, RPDB…), Plex keeps each poster for a day instead of asking the server again on every screen. The TMDB fallback poster, served when the provider does not answer, is still rechecked.",
      ],
      [
        "Les applications reprennent les affiches du fournisseur toutes les 24 heures au lieu de 6. Le délai se règle dans Ether-Remix → Contenus et bibliothèques → Affiches dans les applications.",
        "Apps fetch provider posters again every 24 hours instead of 6. Set the delay in Ether-Remix → Content & libraries → Posters in apps.",
      ],
      [
        "Jellyfin et Emby reçoivent l'affiche à la taille demandée par l'application : plus légère sur téléphone. Jellyfin n'attend plus avant d'envoyer une image.",
        "Jellyfin and Emby receive the poster at the size the app asked for: lighter on phones. Jellyfin no longer waits before sending an image.",
      ],
      [
        "Sans fournisseur, une télé qui demande une affiche de 600 px reçoit celle de 500 px plutôt que celle de 780 px, plus de deux fois plus lourde.",
        "Without a provider, a TV asking for a 600 px poster receives the 500 px one rather than the 780 px one, more than twice as heavy.",
      ],
    ],
  },
  {
    id: "2026-11-14-dmm-instance",
    version: "0.2.37-remix.58.1",
    date: "2026-11-14",
    title: ["Choix de l’instance DMM", "DMM instance selection"],
    items: [
      [
        "La source musicale DMM affiche les liens vers le catalogue public et l’auto-hébergement. Son adresse peut être remplacée par celle de votre instance compatible.",
        "The DMM music source links to the public catalogue and self-hosting instructions. Its address can be replaced with your compatible instance.",
      ],
    ],
  },
  {
    id: "2026-11-13-torrent-client",
    version: "0.2.37-remix.57.1",
    date: "2026-11-13",
    title: ["Client torrent en option", "Optional torrent client"],
    items: [
      [
        "Nouvelle source, désactivée par défaut : Remix envoie les releases torrent de Prowlarr ou Jackett à qBittorrent ou rTorrent / ruTorrent, puis les lit une fois téléchargées. Films, séries, animés et musique.",
        "New source, off by default: Remix sends torrent releases from Prowlarr or Jackett to qBittorrent or rTorrent / ruTorrent, then plays them once downloaded. Movies, shows, anime and music.",
      ],
      [
        "Jusqu'à trois clients, accès par identifiants ou clé d'API qBittorrent (5.2 et suivants). Ratio et durée de partage par client et par tracker, plafond d'ajouts par jour, cross-seed. Remix ne supprime jamais rien.",
        "Up to three clients, with credentials or a qBittorrent API key (5.2 and later). Ratio and seeding time per client and per tracker, a daily cap on additions, cross-seed. Remix never deletes anything.",
      ],
      [
        "Pendant le téléchargement, Plex, Jellyfin et Emby affichent l'avancement ; la console liste les téléchargements. Sous le seuil de place libre, plus aucun ajout et une alerte part par les notifications d'Ether.",
        "While downloading, Plex, Jellyfin and Emby show the progress; the console lists the downloads. Below the free-space threshold, no more additions and an alert goes out through Ether notifications.",
      ],
      [
        "Le client peut être distant, mais Remix doit voir son dossier de téléchargement (même machine ou partage NFS/SMB). Vos comptes debrid restent en « cache uniquement ». Réglages : Sources → Client torrent.",
        "The client can be remote, but Remix must see its download folder (same machine or NFS/SMB share). Your debrid accounts stay cached-only. Settings: Sources → Torrent client.",
      ],
    ],
  },
  {
    id: "2026-11-13-upstream-sync",
    version: "0.2.37-remix.56.1",
    date: "2026-11-13",
    title: ["Synchronisation Ether réparée", "Ether synchronization repaired"],
    items: [
      [
        "Les nouveautés d’Ether jusqu’au 12 novembre sont intégrées. La synchronisation suit désormais les déplacements de fichiers et détaille les conflits pour préserver les ajouts Remix.",
        "Ether updates through November 12 are integrated. Synchronization now tracks file moves and reports conflicts in detail to preserve Remix additions.",
      ],
    ],
  },
  {
    id: "2026-11-13-subsonic-token-access",
    version: "0.2.37-remix.54.1",
    date: "2026-11-13",
    title: ["Connexion des clients musicaux par jeton", "Token authentication for music clients"],
    items: [
      [
        "Arpeggi et les clients Subsonic utilisant des jetons peuvent se connecter avec un accès musical dédié par spectateur. Créez ou révoquez-le dans Contenus et bibliothèques → Musique → Clients musicaux, sans modifier le mot de passe de votre compte.",
        "Arpeggi and token-authenticated Subsonic clients can connect with dedicated music access per viewer. Create or revoke it under Content & libraries → Music → Music clients without changing your account password.",
      ],
    ],
  },
  {
    id: "2026-11-11-remix-settings-layout",
    version: "0.2.37-remix.53.1",
    date: "2026-11-11",
    title: ["Configuration Remix réorganisée", "Reorganized Remix settings"],
    items: [
      [
        "Les adresses passent à /ether/remix ; vos anciens liens restent utilisables. Les blocs de configuration sont repliés par défaut et leurs titres harmonisés.",
        "Addresses now use /ether/remix; old links still work. Configuration sections start collapsed and share consistent headings.",
      ],
      [
        "iBroadcast rejoint Eclipse et DMM dans les sources musicales. L’adresse Subsonic et les compteurs de bibliothèque sont regroupés dans Contenus et bibliothèques → Musique.",
        "iBroadcast joins Eclipse and DMM in music sources. The Subsonic address and library counts are grouped under Content & libraries → Music.",
      ],
    ],
  },
  {
    id: "2026-11-11-source-cache-resilience",
    version: "0.2.37-remix.52.1",
    date: "2026-11-11",
    title: [
      "Sources plus résilientes et recherche StreamFusion",
      "Resilient sources and StreamFusion search",
    ],
    items: [
      [
        "En cas de panne temporaire, Remix peut reprendre les anciens résultats torrent/NZB pendant 24 heures après leur expiration, avec les vérifications de lecture habituelles.",
        "During temporary outages, Remix can reuse torrent/NZB results for 24 hours after expiry, with normal playback checks.",
      ],
      [
        "La base StreamFusion fournit aussi des candidats par film, saison et épisode. Les preuves de cache debrid sont vérifiées par service et expirent ; un import ne suppose plus une disponibilité AllDebrid.",
        "The StreamFusion database also supplies movie, season and episode candidates. Debrid cache evidence is service-specific and expires; imports no longer assume AllDebrid availability.",
      ],
    ],
  },
  {
    id: "2026-11-11-vostfr-submaker",
    version: "0.2.37-remix.50.1",
    date: "2026-11-11",
    title: ["VOSTFR à la demande avec SubMaker", "On-demand VOSTFR with SubMaker"],
    items: [
      [
        "Dans l’onglet VOSTFR à la demande, une option prépare les sous-titres français des titres choisis avec SubMaker. Elle n’est disponible que si SubMaker est activé avec un manifeste valide.",
        "In the VOSTFR on demand tab, an option prepares French subtitles for the chosen titles with SubMaker. It is only available when SubMaker is enabled with a valid manifest.",
      ],
      [
        "Chaque titre affiche l’état de ses sous-titres : VF disponible, sous-titres annoncés, SRT prêt ou traduit, en préparation, ou rien pour l’instant.",
        "Each title shows its subtitle state: French audio, announced subtitles, SRT ready or translated, preparing, or nothing yet.",
      ],
      [
        "À la lecture, à qualité, langue et score de formats égaux, Remix choisit la release dont le SRT est déjà prêt. Une meilleure release reste prioritaire, même sans sous-titres.",
        "At playback, with equal quality, language and format score, Remix picks the release whose SRT is already ready. A better release still ranks first, even without subtitles.",
      ],
    ],
  },
  {
    id: "2026-11-11-local-media-ibroadcast",
    version: "0.2.37-remix.49.1",
    date: "2026-11-11",
    title: ["Vos médias locaux et iBroadcast", "Your local media and iBroadcast"],
    items: [
      [
        "Ajoutez des dossiers vidéo ou audio montés dans Docker : bibliothèques locales, vidéos personnelles sans TMDB, analyse automatique et lecture sans déplacer vos fichiers.",
        "Add video or audio folders mounted in Docker: Remix/TMDB matching, personal videos preserved without a match, automatic scanning and in-place playback.",
      ],
      [
        "Dossiers et comptes iBroadcast privés par défaut, partage avec certains spectateurs ou tous. Plusieurs comptes personnels et import facultatif des playlists iBroadcast.",
        "Private folders and iBroadcast accounts by default, with optional sharing. Multiple personal accounts and optional iBroadcast playlist imports.",
      ],
    ],
  },
  {
    id: "2026-11-11-display-name",
    version: "0.2.37-remix.48.1",
    date: "2026-11-11",
    title: ["Nom affiché des utilisateurs", "Viewer display names"],
    items: [
      [
        "Sur la fiche d’un utilisateur, le crayon à côté de son nom permet de choisir le nom affiché dans les statistiques, l’activité et les listes. Plex ne l’écrase plus, et les lectures passées prennent le nouveau nom.",
        "On a viewer’s page, the pencil next to their name sets the name shown in statistics, activity and lists. Plex no longer overwrites it, and past plays take the new name.",
      ],
      [
        "La connexion ne change pas. Un champ vide rétablit le nom Plex.",
        "Sign-in does not change. An empty field restores the Plex name.",
      ],
    ],
  },
  {
    id: "2026-11-11-french-rows",
    version: "0.2.37-remix.47.1",
    date: "2026-11-11",
    title: ["Accueil et collections en français", "French Home rows and collections"],
    items: [
      [
        "Le catalogue en français et « lisible uniquement » s’appliquent aussi aux rangées de l’accueil (Tendances, Top 10…), aux collections et aux titres similaires. Un film pas encore sorti en France n’y apparaît plus.",
        "The French and playable-only catalogues now also apply to Home rows (Trending, Top 10…), collections and similar titles. A movie not yet out in France no longer shows there.",
      ],
      [
        "Restent intactes : les collections de sorties à venir, la watchlist, la reprise de lecture et la recherche.",
        "Left alone: upcoming collections, the watchlist, Continue Watching and search.",
      ],
    ],
  },
  {
    id: "2026-11-11-sfr-late-titles",
    version: "0.2.37-remix.46.1",
    date: "2026-11-11",
    title: ["StreamFusion rattrape les titres tardifs", "StreamFusion catches up late titles"],
    items: [
      [
        "Un titre ajouté au catalogue après l’import de ses releases StreamFusion Reborn, par exemple via Tendances, les récupère au passage suivant et devient lisible. La recherche se fait par lots et s’interrompt pendant les lectures.",
        "A title added to the catalogue after its StreamFusion Reborn releases were imported now gets them on the next run and becomes playable. The lookup runs in batches and pauses during playback.",
      ],
    ],
  },
  {
    id: "2026-11-11-unified-history-listening",
    version: "0.2.37-remix.45.1",
    date: "2026-11-11",
    title: ["Un historique commun et un onglet Écoute", "Unified history and a Listening tab"],
    items: [
      [
        "Films, séries et musique partagent le même historique chronologique. Écoute regroupe les tendances et classements musicaux, filtrés par période, année et utilisateur.",
        "Movies, shows and music share one chronological history. Listening groups music trends and rankings, filtered by period, year and user.",
      ],
    ],
  },
  {
    id: "2026-11-10-music-activity",
    version: "0.2.37-remix.44.1",
    date: "2026-11-10",
    title: ["La musique rejoint Activité", "Music joins Activity"],
    items: [
      [
        "Les lectures signalées par Plex, Jellyfin/Emby et Subsonic apparaissent dans En cours. L’onglet Historique affiche aussi les écoutes musicales, avec filtres et pagination. Les sessions sans nouvelles expirent automatiquement.",
        "Playback reported by Plex, Jellyfin/Emby and Subsonic appears in Now playing. History also shows music listens, with filters and pagination. Stale sessions expire automatically.",
      ],
    ],
  },
  {
    id: "2026-11-10-quality-mode-import",
    version: "0.2.37-remix.43.1",
    date: "2026-11-10",
    title: ["Un seul mode de filtrage à la fois", "One filtering mode at a time"],
    items: [
      [
        "Les profils remplacent les critères simples du catalogue quand ils sont activés. Vos anciens critères sont conservés pour revenir au mode simple. Les langues sont mieux alignées.",
        "Enabled profiles replace simple catalogue criteria. Previous criteria are preserved when switching back. Language options are aligned.",
      ],
      [
        "Import AIOStreams depuis une source enregistrée ou un manifest, avec le mot de passe de configuration et aperçu. Formats détaillés, résolutions inconnues, langue originale et annonces de cache sont pris en charge. Les manifests avec variante importent les filtres de base.",
        "Import AIOStreams from a saved source or manifest with the configuration password and a preview. Detailed formats, unknown resolutions, original language and cache hints are supported. Variant manifests import base filters.",
      ],
    ],
  },
  {
    id: "2026-11-10-concerts-precision",
    version: "0.2.37-remix.42.1",
    date: "2026-11-10",
    title: ["Concerts : une sélection plus précise", "Concerts: a more focused selection"],
    items: [
      [
        "Mots-clés de captation et genre Musique pour réduire les fictions. Les anciennes collections standard sont corrigées automatiquement, sans écraser les requêtes personnalisées. Quelques concerts mal renseignés peuvent manquer.",
        "Recorded-performance keywords and the Music genre reduce fiction matches. Existing standard collections are upgraded automatically without overwriting custom queries. Some poorly tagged concerts may be missing.",
      ],
    ],
  },
  {
    id: "2026-11-10-submaker-external-subtitles",
    version: "0.2.37-remix.41.1",
    date: "2026-11-10",
    title: ["Sous-titres externes avec SubMaker", "External subtitles with SubMaker"],
    items: [
      [
        "Instance publique ou auto-hébergée, recherche et traduction facultatives par bibliothèque ou collection. Les sous-titres préparés restent liés à leur release et peuvent être transmis à SeerrCatalog.",
        "Public or self-hosted instance, optional subtitle search and translation per library or collection. Prepared subtitles stay bound to their release and can be sent to SeerrCatalog.",
      ],
    ],
  },
  {
    id: "2026-11-10-quality-profiles",
    version: "0.2.37-remix.40.1",
    date: "2026-11-10",
    title: ["Des filtres de qualité personnalisables", "Custom release quality filters"],
    items: [
      [
        "Langues et qualité : profils globaux, par Films/Séries, bibliothèque ou collection. Formats, codecs, résolution, HDR/DV, taille et regex, avec test de noms de releases.",
        "Languages and quality: global, Movies/Shows, library or collection profiles. Formats, codecs, resolution, HDR/DV, size and regex, with a release-name preview.",
      ],
      [
        "Import des filtres compatibles d’un export JSON AIOStreams, avertissements pour les règles non transposables et modification avant enregistrement.",
        "Import compatible filters from an AIOStreams JSON export, with unsupported-rule warnings and editing before saving.",
      ],
    ],
  },
  {
    id: "2026-11-10-seerrcatalog-hidden-titles",
    version: "0.2.37-remix.39.1",
    date: "2026-11-10",
    title: [
      "SeerrCatalog : import des disponibilités fiabilisé",
      "SeerrCatalog: reliable availability imports",
    ],
    items: [
      [
        "Un titre masqué ou à venir ne bloque plus l’import des disponibilités des autres titres. Les filtres du catalogue et les règles de lecture restent appliqués.",
        "A hidden or upcoming title no longer blocks availability imports for other titles. Catalogue filters and playback rules still apply.",
      ],
    ],
  },
  {
    id: "2026-11-10-seerrcatalog-settings-layout",
    version: "0.2.37-remix.38.1",
    date: "2026-11-10",
    title: ["SeerrCatalog : des réglages regroupés", "SeerrCatalog: grouped settings"],
    items: [
      [
        "Sources → Stremio réunit les disponibilités et la recherche via Remix dans une seule section SeerrCatalog. Chaque fonction conserve sa clé et son activation. Les explications et les étapes de connexion sont simplifiées.",
        "Sources → Stremio groups availability and search via Remix in one SeerrCatalog section. Each feature keeps its own token and switch. Connection instructions are shorter and clearer.",
      ],
    ],
  },
  {
    id: "2026-11-10-scrob-slow-connection",
    version: "0.2.37-remix.37.1",
    date: "2026-11-10",
    title: ["Scrob : connexions lentes", "Scrob: slow connections"],
    items: [
      [
        "Le test du webhook et les envois de lecture attendent désormais jusqu’à 60 secondes au lieu de 15 pour les connexions lentes.",
        "Webhook tests and playback events now wait up to 60 seconds instead of 15 on slow connections.",
      ],
    ],
  },
  {
    id: "2026-11-10-ether-sync-recovered",
    version: "0.2.37-remix.36.1",
    date: "2026-11-10",
    title: ["Mise à jour Ether et compatibilité Remix", "Ether update and Remix compatibility"],
    items: [
      [
        "Les nouveautés d’Ether sont intégrées, dont le décalage des sorties numériques/physiques et les corrections du guide TV. Les filtres Remix, les sections repliables et la préparation groupée des affiches sont conservés.",
        "Ether updates include the digital/physical release offset and TV guide fixes. Remix filters, collapsible sections and batched poster preparation are preserved.",
      ],
    ],
  },
  {
    id: "2026-11-10-stats-searches-readable",
    version: "0.2.37-remix.35.1",
    date: "2026-11-10",
    title: ["Statistiques plus lisibles", "Clearer statistics"],
    items: [
      [
        "Dernières recherches n'affiche plus les sources dont le quota Remix du jour est épuisé : elles ne sont pas interrogées et restent comptées dans Détail par source.",
        "Latest searches no longer lists sources whose daily Remix quota is used up: they are not queried and remain counted in Per-source details.",
      ],
      [
        "Détail par source est replié par défaut ; cliquez sur le titre pour l'ouvrir.",
        "Per-source details are collapsed by default; click the title to open them.",
      ],
      [
        "Le bloc sur le catalogue TMDB explique pourquoi Remix ne cherche pas dans tout TMDB.",
        "The TMDB catalog section explains why Remix doesn't search all of TMDB.",
      ],
    ],
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
