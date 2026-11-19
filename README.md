<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>

# Ether-Remix

**Films, séries, animés et musique dans vos applications habituelles.**

Ether-Remix est un serveur multimédia à la demande, basé sur [Ether](https://github.com/braedonsaunders/ether). Il réunit vos sources distantes et vos fichiers locaux, avec un catalogue orienté français, des bibliothèques personnalisées et un suivi par spectateur. Vous fournissez les sources et les comptes nécessaires à la lecture.

**Français · [English](README.en.md)** · Docker **amd64 / arm64** · **GPL-3.0**

[Installation](#demarrer) · [Fonctionnalités](#fonctionnalites) · [Applications](#applications) · [Documentation](#documentation)

## Ether, en bref

Ether est un serveur multimédia **à la demande**. Il présente un catalogue issu de TMDB dans les applications que vous utilisez déjà, sans rien télécharger à l’avance. Son étendue dépend des réglages du serveur. Quand vous lancez un titre, il choisit la meilleure release sur vos comptes debrid et vos fournisseurs Usenet, vérifie qu’elle fonctionne, puis la lit en flux. Ce fonctionnement ne nécessite ni Sonarr, ni Radarr, ni client de téléchargement, ni montage à scanner. Il peut tourner à côté d’un Plex, Jellyfin ou Emby existant.

Ether s’adapte à chaque application : c’est un serveur Plex pour les applications Plex, un serveur Jellyfin pour Infuse ou Swiftfin, et ainsi de suite. Chaque spectateur a son compte et son accueil personnalisé, et retrouve sa progression d’une application à l’autre. Les limites de flux et le contrôle parental s’appliquent partout.

L’accueil Plex se personnalise dans `/ether/home` : choisissez le spectateur et le type d’appareil pour comparer l’aperçu au client. Les recommandations propres à Films et Séries restent dans leurs pages ; elles ne sont plus ajoutées en bloc à l’accueil par les requêtes par bibliothèque de Plex.

## Remix, en bref

Les **modèles de disposition** enregistrent l’accueil, les bibliothèques et la présentation des collections : sauvegardez l’existant, dupliquez A/B/C et réappliquez une variante avec retour en arrière. Les bibliothèques à thème Plex peuvent combiner dossiers locaux et sources indexées et cibler tous les utilisateurs ou une sélection ; les contenus privés restent filtrés par spectateur. [Guide des dispositions et du partage](docs/pse/FEATURES.fr.md#nav-des-bibliotheques-et-collections-personnalisees).

Ether-Remix reprend le serveur et les applications compatibles d’Ether, avec un catalogue orienté français et des réglages de langue et de qualité. Selon vos choix, l’accueil et les collections privilégient les titres en VF/MULTi ou disposant de releases correspondant à vos critères. La recherche automatique prépare les releases à l’avance sans télécharger les vidéos ; les recherches et imports alimentent le même index, réutilisé à la lecture.

Vos **vidéos locales**, films, épisodes ou vidéos personnelles, rejoignent vos bibliothèques depuis des dossiers de l’hôte ou des montages réseau. Remix cherche une correspondance dans son catalogue puis TMDB et conserve une présentation locale si aucun résultat fiable ne convient. La vue **Sources → Fichiers locaux** signale les fichiers absents, les problèmes de lecture et les doublons potentiels, sans supprimer vos médias.

Les **sources facultatives** complètent le debrid et Usenet : client torrent, XDCC via Grabscher et serveur FTP / SFTP. Torrent et XDCC écrivent des fichiers sur disque ; FTP / SFTP permet aussi une lecture distante sans copie complète, avec le cache habituel d’Ether. Ces sources sont désactivées par défaut.

Côté **musique**, réunissez le catalogue Deezer, vos fichiers audio et vos comptes iBroadcast dans Plexamp, les clients Jellyfin ou les applications Subsonic. Les sources musicales et les comptes debrid activés assurent la lecture ; les playlists se parcourent, s’importent et se gèrent par utilisateur. Dossiers locaux et comptes iBroadcast sont privés par défaut, avec partage facultatif.

La configuration rapide **JukeBox** aide à créer vos bibliothèques et collections. Les sous-titres avec SubMaker, le suivi Scrob et Last.fm, les statistiques et la console en français complètent ces usages.

<a id="nav-fonctionnalites-remix"></a>
<a id="fonctionnalites"></a>

<a id="nav-films-series-et-animes-selon-vos-preferences"></a>
<a id="nav-vos-videos-locales-films-et-videos-personnelles"></a>
<a id="alldebrid-sur-un-vps--mediaflow-et-gluetun"></a>
<a id="nav-alldebrid-sur-un-vps-mediaflow-et-gluetun"></a>
<a id="nav-sous-titres-externes-et-traduction-avec-submaker"></a>
<a id="nav-des-bibliotheques-et-collections-personnalisees"></a>
<a id="nav-la-musique-dans-vos-applications"></a>
<a id="nav-vos-playlists-et-de-nouvelles-decouvertes"></a>
<a id="nav-statistiques-et-suivi"></a>
<a id="nav-une-interface-adaptee-a-vos-habitudes"></a>
<a id="nav-la-base-ether-enrichie-par-remix"></a>

## Ce que fait Remix

Le [détail des fonctionnalités](docs/pse/FEATURES.fr.md) précise les options, les sources et les limites de chaque usage.

| Usage | Fonctionnalités |
| --- | --- |
| Films, séries et animés | Préférences VF/MULTi, qualité, catalogue filtré, exceptions VOSTFR et sous-titres SubMaker. |
| Sources vidéo | Debrid, Usenet, addons Stremio, Prowlarr/Jackett/NZBHydra2, base StreamFusion Reborn et SeerrCatalog. Clients torrent, XDCC et FTP/SFTP en option. |
| Fichiers locaux | Vidéos et musique depuis vos dossiers ou montages réseau, sans déplacer ni supprimer les originaux. |
| Bibliothèques et accueil | Collections, configuration rapide JukeBox, import Xperience, modèles de disposition, affiches et cartes personnalisées. Le partage respecte les droits de chaque spectateur. |
| Musique | Catalogue Deezer, sources musicales activées, fichiers locaux et iBroadcast ; accès depuis Plexamp, Jellyfin ou Subsonic, avec playlists personnelles. |
| Suivi | Progression et historique par spectateur, activité vidéo et musicale, Scrob, Last.fm et statistiques des sources. |

<a id="nav-des-sources-reunies-et-une-recherche-anticipee"></a>

## Un seul index de releases

Les recherches et imports conservent dans un index commun les références conformes aux critères Remix actifs. Les liens hors critères sont refusés ; les anciens liens sont nettoyés après actualisation des profils et exceptions. À la lecture, Remix propose les références encore utilisables, applique les critères de langue et de qualité, puis recherche d’autres sources si nécessaire. L’identité du film ou de l’épisode reste vérifiée ; une référence enregistrée ne garantit pas sa disponibilité.

Dans **Remix → Indexation**, un seul interrupteur, **Rechercher automatiquement des releases**, prépare les contenus du catalogue en arrière-plan. Les sources se choisissent sur la même page ; cadence et budgets sont dans les réglages avancés. Désactiver cette recherche conserve l’index, sa consultation et son utilisation à la lecture.

La recherche de fond ne télécharge ni ne débride les fichiers. Un addon distant peut néanmoins effectuer ses propres appels. Pour les séries, le passage initial couvre **S01E01** : les autres épisodes sont enregistrés au fil des recherches, sans garantie de saison complète. [Fonctionnement de l’index](docs/pse/RELEASE_LIBRARY.fr.md).

Les fichiers AllDebrid vérifiés peuvent être prioritaires lorsqu’ils respectent le profil. Les quotas suspendent les comptes concernés, les tentatives de secours sont limitées et le renouvellement des liens peut être réglé pour éviter des appels inutiles. [AllDebrid et proxys](docs/alldebrid-mediaflow.md).

<a id="nav-applications-compatibles"></a>
<a id="applications"></a>

## Applications

| Accès | Exemples de clients |
| --- | --- |
| Plex | Applications Plex, Plexamp et Kodi avec PM4K. |
| Jellyfin / Emby | Infuse, Swiftfin, Findroid, Streamyfin et clients TV compatibles. Emby for TV 2.x sur Apple TV n’est pas pris en charge. |
| Silo | Clients Silo sur ordinateur, mobile et TV. |
| Stremio | Extension personnelle ; adresse HTTPS nécessaire. |
| WebDAV | Infuse, VLC, Kodi et rclone. |
| IPTV / réseau local | M3U, XMLTV, Xtream et DLNA, selon les accès activés. |
| Subsonic | Symfonium, Feishin, Supersonic, DSub et autres clients compatibles. |

Activez les accès dans **Console → Applications**. Chaque spectateur retrouve ses liens et accès personnels dans **Votre compte**. Les fonctions disponibles dépendent du client.

<a id="nav-demarrer"></a>
<a id="demarrer"></a>

## Démarrer

1. Installez l’image publique `ghcr.io/aerya/ether-remix:latest` avec le [Compose du guide](docs/pse/GUIDE.fr.md#installation). Les volumes `/config` et `/cache` doivent être persistants.
2. Ouvrez `http://<hôte>:32400/ether/` et suivez l’assistant. Le port se règle avec `ETHER_PORT`.
3. Ajoutez vos comptes et fournisseurs dans **Remix → Sources**, puis choisissez vos langues et votre qualité dans **Lecture**.
4. Activez la recherche automatique dans **Indexation** si vous souhaitez préparer le catalogue à l’avance. Consultez les résultats dans **Releases**.
5. Activez vos applications et connectez vos spectateurs. Pour les clients Subsonic à jeton, créez un accès musical dans **Contenus → Musique → Clients musicaux**.

Pour migrer depuis Ether, sauvegardez `/config`, arrêtez l’ancien serveur et réutilisez ses volumes. Utilisez HTTPS pour les accès distants. Les sources torrent et XDCC peuvent télécharger sur disque ; FTP/SFTP propose aussi la lecture distante sans copie complète. Consultez leur guide avant activation.

<a id="nav-documentation"></a>
<a id="documentation"></a>

## Documentation

| Besoin | Guide |
| --- | --- |
| Installation et configuration | [Guide utilisateur](docs/pse/GUIDE.fr.md) |
| Catalogue, filtres et priorités | [Catalogue](docs/pse/CATALOGUE.fr.md) |
| Références, conservation et épisodes indexés | [Index des releases](docs/pse/RELEASE_LIBRARY.fr.md) |
| Bibliothèques, accueil et partage | [Dispositions](docs/pse/FEATURES.fr.md#nav-des-bibliotheques-et-collections-personnalisees) |
| Import StreamFusion Reborn | [StreamFusion](docs/pse/STREAMFUSION.fr.md) |
| Disponibilités SeerrCatalog | [SeerrCatalog](docs/pse/SEERRCATALOG.fr.md) |
| AllDebrid, MediaFlow et proxys | [Réseau](docs/alldebrid-mediaflow.md) |
| Sous-titres et traduction | [SubMaker](docs/pse/SUBMAKER.fr.md) |
| Sources facultatives | [Torrent](docs/pse/TORRENT.fr.md) · [XDCC](docs/pse/XDCC.fr.md) · [FTP/SFTP](docs/pse/FTP.fr.md) |
| Musique locale et comptes iBroadcast | [Médias locaux](docs/pse/GUIDE.fr.md#médias-locaux-et-ibroadcast) |

Les nouveautés détaillées sont présentées dans la console. Les guides décrivent les réglages et leurs limites ; ce README présente les usages principaux.

<a id="nav-origine-et-licence"></a>

## Origine et licence

Ether-Remix est un fork d’[Ether](https://github.com/braedonsaunders/ether) de Braedon Saunders, sous **GPL-3.0**.

Merci à **[JukeBox](https://mdblist.com/lists/jukebox8786/)** pour ses listes MDBList, utilisées dans la configuration rapide et les sélections automatiques.

Inspirations : [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) et [Silo Server](https://github.com/Silo-Server/silo-server). Intégration de suivi : [Scrob](https://github.com/Aerya/scrob). Source XDCC : [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) de CaptainEpix. Source FTP / SFTP : [pkg/sftp](https://github.com/pkg/sftp), [jlaffaye/ftp](https://github.com/jlaffaye/ftp). Les métadonnées vidéo utilisent [TMDB](https://www.themoviedb.org/), sans approbation ni certification de TMDB.

[Licence](LICENSE) · [Informations légales](LEGAL.md) · [Composants tiers](THIRD_PARTY_NOTICES.md)
