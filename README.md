<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>

# Ether-Remix

**Films, séries, animés et musique dans vos applications habituelles.**

Ether-Remix réunit vos sources distantes et vos fichiers locaux dans un serveur multimédia à la demande, basé sur [Ether](https://github.com/braedonsaunders/ether). Il ajoute un catalogue orienté français, des bibliothèques personnalisées, la musique et un suivi par spectateur.

**Français · [English](README.en.md)** · Docker **amd64 / arm64** · **GPL-3.0** · Bêta : bugs et changements incompatibles possibles

[Fonctionnalités](#fonctionnalites) · [Sources](#sources) · [Applications](#applications) · [Installation](#demarrer) · [Documentation](#documentation)

Vous fournissez les sources et comptes nécessaires. Pour le debrid et Usenet, Remix cherche une release conforme à vos critères puis la lit en flux, sans téléchargement préalable obligatoire ni Sonarr/Radarr, client de téléchargement ou montage à scanner. Il peut coexister avec un Plex, Jellyfin ou Emby existant. Les sources facultatives ont leurs propres besoins de stockage, précisés ci-dessous. La présence d’un titre au catalogue ne garantit pas sa lecture.

<a id="nav-fonctionnalites-remix"></a>
<a id="fonctionnalites"></a>
<a id="nav-films-series-et-animes-selon-vos-preferences"></a>
<a id="nav-vos-videos-locales-films-et-videos-personnelles"></a>
<a id="nav-sous-titres-externes-et-traduction-avec-submaker"></a>
<a id="nav-des-bibliotheques-et-collections-personnalisees"></a>
<a id="nav-la-musique-dans-vos-applications"></a>
<a id="nav-vos-playlists-et-de-nouvelles-decouvertes"></a>
<a id="nav-statistiques-et-suivi"></a>
<a id="nav-une-interface-adaptee-a-vos-habitudes"></a>
<a id="nav-la-base-ether-enrichie-par-remix"></a>
<a id="ether-en-bref"></a>
<a id="remix-en-bref"></a>
<a id="ce-que-fait-remix"></a>

## Ce que vous pouvez faire

| Usage | Possibilités |
| --- | --- |
| **Films, séries et animés** | Catalogue TMDB, préférences VF/MULTi, qualité, exceptions VOSTFR, sous-titres et traduction SubMaker. |
| **Musique** | Catalogue Deezer, fichiers locaux et iBroadcast ; favoris, historique, playlists personnelles, imports et mixes programmés. |
| **Bibliothèques et accueil** | Collections à thème, configuration JukeBox, import Xperience, modèles de disposition, affiches et cartes personnalisées. |
| **Sources et lecture** | Priorités configurables, recherche anticipée et index commun des releases ; contrôle de l’identité du média, des langues et de la qualité. |
| **Spectateurs et suivi** | Accès privés ou partagés, contrôle parental et limites de flux ; reprise entre applications, nouveaux épisodes dans « Continuer à regarder », Scrob, Last.fm et statistiques. |
| **Console** | Interface française, assistant de configuration en huit étapes avec aperçu avant application, modes Simple/Expert, diagnostics et réglages d’apparence. |

Dans **Recommandations**, composez l’accueil et les rangées des bibliothèques pour vos spectateurs. Les modèles permettent de sauvegarder et réappliquer une disposition. Le partage s’applique aussi aux bibliothèques Remix dans Plex, Stremio et Jellyfin ; Jellyfin propose des exclusions pour ses derniers ajouts. [Fonctionnalités détaillées](docs/pse/FEATURES.fr.md).

Les dossiers locaux et comptes iBroadcast sont privés par défaut, avec partage facultatif. Remix signale les fichiers absents ou problématiques sans supprimer les originaux. Les listes légères évitent les enrichissements pendant le défilement ; les détails sont chargés à l’ouverture d’une fiche ou à la lecture.

<a id="alldebrid-sur-un-vps--mediaflow-et-gluetun"></a>
<a id="nav-alldebrid-sur-un-vps-mediaflow-et-gluetun"></a>
<a id="nav-des-sources-reunies-et-une-recherche-anticipee"></a>
<a id="sources"></a>
<a id="un-seul-index-de-releases"></a>

## Sources et intégrations

Les sources de catalogue décrivent les contenus ; une source de lecture fournit leur accès. Choisissez uniquement les intégrations utiles à votre installation.

| Source ou service | Rôle et prérequis |
| --- | --- |
| **Debrid / Usenet** | Lecture à la demande avec vos comptes et fournisseurs ; priorités, quotas et solutions de repli configurables. |
| **Prowlarr, Jackett, NZBHydra2 / addons Stremio** | Recherche de releases et, selon l’addon, accès aux flux. Une voie de lecture compatible reste nécessaire. Un seul proxy par addon couvre requêtes et lecture. |
| **StreamFusion Reborn / SeerrCatalog / Pre** | Import ou enrichissement du catalogue et des références. SeerrCatalog nécessite son service externe ; Pre, en bêta, fournit des annonces, pas des liens de lecture. |
| **Alkodiques (API StreamFusion)** | Fichiers AllDebrid déjà vérifiés de votre StreamFusion, lus par son API en lecture seule (copie locale tenue à jour, tous les épisodes des titres du catalogue, un seul déblocage de lien). Compte AllDebrid et clé de lecture StreamFusion requis. Facultatif. [Guide](docs/pse/ALKODIQUES.fr.md). |
| **Client torrent / Seedstrem / XDCC Grabscher** | Téléchargement sur disque ; Seedstrem et Grabscher nécessitent leurs services externes. Sources facultatives. |
| **FTP / SFTP** | Lecture distante possible sans copie complète, avec cache ; serveur et accès requis. Facultatif. |
| **aMule / Soulseek (slskd)** | Recherche vidéo et musicale, transferts manuels ou à la lecture ; lecture après réception complète. Services externes et dossier partagé requis. |
| **Fichiers locaux / iBroadcast** | Vos vidéos et fichiers audio depuis des dossiers ou montages réseau ; bibliothèques musicales iBroadcast avec vos comptes. |
| **Sources musicales** | Catalogue Deezer et lecture via les sources activées : notamment Eclipse, Audius, DMM/debrid et OpenDeezer avec votre propre compte Deezer. |
| **Deezer / Spotify / ListenBrainz / Last.fm** | Parcours, import ou synchronisation de playlists selon le service ; suivi de playlists et mixes personnels programmés. |
| **AudioMuse / Digarr** | AudioMuse suggère des morceaux locaux déjà analysés ; Digarr recommande artistes/albums et permet de créer des playlists depuis les albums retrouvés. Services externes facultatifs, sans garantie de disponibilité audio. |

Les connecteurs torrent, XDCC, FTP/SFTP, aMule et slskd sont désactivés par défaut. Les **priorités** permettent de préférer Usenet ou BitTorrent et de limiter le recours aux débrideurs. La **recherche automatique** prépare l’index sans télécharger ni débrider les fichiers ; les addons distants peuvent effectuer leurs propres appels. Pour les séries, l’indexation initiale couvre S01E01, pas une saison complète. [Priorités](docs/pse/QUICK_SOURCES.fr.md) · [Index et limites](docs/pse/RELEASE_LIBRARY.fr.md).

<a id="nav-applications-compatibles"></a>
<a id="applications"></a>

## Applications compatibles

| Accès | Exemples et particularités |
| --- | --- |
| **Plex** | Applications Plex, Plexamp et Kodi avec PM4K. |
| **Jellyfin / Emby** | Infuse, Swiftfin, Findroid, Streamyfin et clients TV compatibles. Emby for TV 2.x sur Apple TV non pris en charge. |
| **Silo** | Clients ordinateur, mobile et TV. |
| **Stremio** | Extension personnelle HTTPS, avec catalogues ou fichiers seuls ; choix des versions et qualités. |
| **Nuvio** | Liaison par spectateur, installation du lien Stremio, reprise et historique synchronisés dans les deux sens. |
| **WebDAV** | Infuse, VLC, Kodi et rclone. |
| **IPTV / réseau local** | M3U, XMLTV, Xtream et DLNA, selon les accès activés. |
| **Subsonic** | Symfonium, Feishin, Supersonic, DSub et autres clients musicaux compatibles. |

Activez les accès dans **Console → Applications**. Chaque spectateur retrouve ses liens dans **Votre compte**. Les fonctions et l’affichage dépendent du client. [Compatibilité et réglages Stremio/Nuvio](docs/pse/FEATURES.fr.md#applications-compatibles).

<a id="nav-demarrer"></a>
<a id="demarrer"></a>
<a id="démarrer"></a>

## Installation rapide

1. Installez `ghcr.io/remix-media/ether-remix:latest` avec le [Compose du guide](docs/pse/GUIDE.fr.md#installation). Rendez `/config` et `/cache` persistants.
2. Ouvrez `http://<hôte>:32400/ether/` et suivez l’assistant. Le port est configurable avec `ETHER_PORT`.
3. Dans **Remix → Sources**, ajoutez vos comptes et fournisseurs ; réglez langues, qualité et priorités dans **Lecture**.
4. Si souhaité, activez **Rechercher automatiquement des releases** dans **Indexation**, puis consultez **Releases**. Sa désactivation conserve l’index et son usage à la lecture.
5. Activez les applications et connectez vos spectateurs. Pour Subsonic à jeton : **Contenus → Musique → Clients musicaux**. Le mode **Expert** affiche les réglages supplémentaires ; **Diagnostic** aide à comprendre une lecture impossible.

Pour migrer depuis Ether, sauvegardez `/config`, arrêtez l’ancien serveur et réutilisez ses volumes. Utilisez HTTPS pour les accès distants. L’ancienne image `ghcr.io/aerya/ether-remix:latest` reste maintenue par un miroir : les installations existantes peuvent la conserver. [Migration Docker](docs/pse/DOCKER-MIGRATION.fr.md).

<a id="nav-documentation"></a>
<a id="documentation"></a>

## Documentation par besoin

| Besoin | Guides |
| --- | --- |
| Installation, console et médias locaux | [Guide utilisateur](docs/pse/GUIDE.fr.md) · [Assistant de configuration](docs/pse/SETUP.fr.md) · [Dossiers et iBroadcast](docs/pse/GUIDE.fr.md#médias-locaux-et-ibroadcast) |
| Fonctions, bibliothèques, partage et clients | [Fonctionnalités détaillées](docs/pse/FEATURES.fr.md) |
| Catalogue, recherche et priorités | [Catalogue](docs/pse/CATALOGUE.fr.md) · [Index des releases](docs/pse/RELEASE_LIBRARY.fr.md) · [Priorités](docs/pse/QUICK_SOURCES.fr.md) |
| Enrichissement du catalogue | [StreamFusion](docs/pse/STREAMFUSION.fr.md) · [SeerrCatalog](docs/pse/SEERRCATALOG.fr.md) · [Pre](docs/pse/PRE.fr.md) |
| Téléchargement et sources distantes | [Torrent](docs/pse/TORRENT.fr.md) · [Seedstrem](docs/pse/SEEDSTREM.fr.md) · [XDCC](docs/pse/XDCC.fr.md) · [FTP/SFTP](docs/pse/FTP.fr.md) · [aMule/slskd](docs/pse/PEER.fr.md) |
| Musique, playlists et découverte | [Sources audio](docs/pse/AUDIO-SOURCES.fr.md) · [Playlists/AudioMuse](docs/pse/MUSIC-FLOWS.fr.md) · [Digarr](docs/pse/DIGARR.fr.md) |
| Réseau et sous-titres | [AllDebrid/MediaFlow/proxys](docs/alldebrid-mediaflow.md) · [SubMaker](docs/pse/SUBMAKER.fr.md) |

Les nouveautés détaillées sont dans la console. Les guides conservent les réglages, prérequis et limites de chaque fonction.

<a id="nav-origine-et-licence"></a>
<a id="origine-et-licence"></a>

## Origine et contribution

Ether-Remix est un fork d’[Ether](https://github.com/braedonsaunders/ether) de Braedon Saunders, sous **GPL-3.0**.

Merci à **[JukeBox](https://mdblist.com/lists/jukebox8786/)** pour ses listes MDBList, utilisées dans la configuration rapide et les sélections automatiques.

Inspirations : [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) et [Silo Server](https://github.com/Silo-Server/silo-server). Intégration de suivi : [Scrob](https://github.com/Aerya/scrob). Source XDCC : [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) de CaptainEpix. Source FTP / SFTP : [pkg/sftp](https://github.com/pkg/sftp), [jlaffaye/ftp](https://github.com/jlaffaye/ftp). Les métadonnées vidéo utilisent [TMDB](https://www.themoviedb.org/), sans approbation ni certification de TMDB.

[Licence](LICENSE) · [Informations légales](LEGAL.md) · [Composants tiers](THIRD_PARTY_NOTICES.md)

Les contributions passent par une PR vers `main`, avec CI et détection des secrets obligatoires. Les PR de `kesurof/Ether-Remix` ouvertes par `kesurof` fusionnent automatiquement après validation. [Règles de contribution et synchronisation](docs/pse/FORK.fr.md).

Le logo Remix-Media identifie la console ; le numéro de version et le lien Remix du pied de menu ouvrent ce dépôt, à côté du crédit Ether.
