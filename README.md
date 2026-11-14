# BÊTA — Ether-Remix

**Projet en développement actif : des bugs et des changements incompatibles peuvent survenir.**

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>


**Ether en VF, avec la musique.**

Fork d’[Ether](https://github.com/braedonsaunders/ether), Ether-Remix ajoute un catalogue orienté français, des bibliothèques personnalisées et la musique partagée entre vos applications, tout en conservant le serveur, les sources debrid/Usenet et les clients d’Ether.

**🇫🇷 Français · [🇬🇧 English](README.en.md)**

<p align="center">
  <img src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-lightgrey" alt="amd64 et arm64">
  <img src="https://img.shields.io/badge/licence-GPL--3.0-green" alt="GPL-3.0">
</p>

## Ether, en bref

Ether est un serveur multimédia **à la demande**. Il présente tout le catalogue TMDB dans les applications que vous utilisez déjà, sans rien télécharger à l’avance. Quand vous lancez un titre, il choisit la meilleure release sur vos comptes debrid et vos fournisseurs Usenet, vérifie qu’elle fonctionne, puis la lit en flux. Pas de Sonarr, de Radarr, de client de téléchargement ni de montage à scanner. Il peut tourner à côté d’un Plex, Jellyfin ou Emby existant.

Ether s’adapte à chaque application : c’est un serveur Plex pour les applications Plex, un serveur Jellyfin pour Infuse ou Swiftfin, et ainsi de suite. Chaque spectateur a son compte et son accueil personnalisé, et retrouve sa progression d’une application à l’autre. Les limites de flux et le contrôle parental s’appliquent partout.

## Remix, en bref

Ether-Remix est un fork d’Ether pensé pour un public francophone. Il garde tout le fonctionnement d’Ether et y ajoute un catalogue en français : l’accueil et les collections n’affichent que les titres disponibles en VF ou MULTi, ou lisibles selon vos critères. Une indexation en arrière-plan cherche les releases à l’avance (addons, indexeurs, SeerrCatalog, StreamFusion Reborn), pour qu’elles soient prêtes au moment de la lecture.

Vos **vidéos locales**, films, épisodes ou vidéos personnelles, rejoignent aussi vos bibliothèques : Remix cherche une correspondance dans son catalogue puis TMDB et conserve une présentation locale si aucun résultat fiable ne convient.

Côté **musique**, réunissez le catalogue Deezer, vos fichiers audio et vos comptes iBroadcast dans Plexamp, les clients Jellyfin ou les applications Subsonic. Les sources musicales gratuites et les comptes debrid complètent la lecture en streaming ; les playlists peuvent être parcourues, importées et gérées par utilisateur. Dossiers locaux et comptes iBroadcast sont privés par défaut, avec partage facultatif.

Des bibliothèques et collections personnalisées, des sous-titres traduits avec SubMaker, des statistiques détaillées et une console en français complètent ces usages.

## Applications compatibles

| Accès | Applications |
| --- | --- |
| **Plex** | Applications officielles : Apple TV, iOS, Android, Android TV / Shield, Fire TV, Roku, PlayStation, Xbox, TV connectées (LG, Samsung, VIDAA), ordinateur et Web. Kodi via Plex for Kodi (PM4K). |
| **Jellyfin** | Infuse, Swiftfin, Findroid, Streamyfin, Jellyfin pour Android TV, JellyCon (Kodi), Jellyfin pour Roku. |
| **Emby** | Applications Emby (Android TV, iOS, Android, Roku, TV connectées) et Infuse. Emby for TV 2.x sur Apple TV n’est pas pris en charge. |
| **Silo** | Apple TV, iOS, macOS, Android, Android TV. |
| **Stremio** | Toutes les applications Stremio, via une extension personnelle. Une adresse HTTPS est nécessaire. |
| **Fichiers (WebDAV)** | Infuse, VLC, Kodi, rclone. |
| **M3U & XMLTV** | Channels DVR, TiviMate et lecteurs IPTV. |
| **Xtream** | TiviMate, Televizo, IPTV Smarters… Désactivé par défaut. |
| **DLNA** | TV et lecteurs du réseau local. |
| **Musique** *(Remix)* | Plex et Plexamp, clients Jellyfin (Finamp…), applications Subsonic : Symfonium, Feishin, Supersonic, DSub… |

Les accès s’activent dans **Console → Applications**. Chaque spectateur trouve ses liens et mots de passe d’application sur sa page **Votre compte**.

**Mise à jour du 13 novembre 2026 :** intégration d’Ether jusqu’à `5b8ef628` et correction de la synchronisation automatique : les déplacements de fichiers sont suivis et les conflits détaillés pour préserver les ajouts Remix.

## Fonctionnalités Remix

### ![](https://api.iconify.design/lucide/clapperboard.svg?color=%2314b8a6&width=24&height=24) Films, séries et animés selon vos préférences

- **Profils de qualité** : formats, codecs, résolutions, HDR/Dolby Vision, taille, langues et regex, globalement ou par bibliothèque/collection. Mode profils ou critères simples, sans cumul ; formats détaillés, résolution inconnue, langue originale et cache debrid annoncé. Import AIOStreams par JSON ou manifest/source enregistrée avec le mot de passe de configuration, aperçu et modification avant enregistrement (variantes non reproduites).
- **Catalogue orienté français** : choisissez vos langues et variantes — VFF, VFQ, VFi, MULTi… — et vos exigences de disponibilité en cache. Un film pas encore disponible en français n’apparaît ni sur l’accueil (Tendances comprises) ni dans les collections.
- **Catalogue « lisible uniquement »** : affichez les titres disposant d’un minimum de releases correspondant à vos critères. La règle vaut aussi pour l’accueil et les collections ; la recherche, la watchlist, la reprise de lecture et les collections de sorties à venir restent complètes.
- **Exceptions ciblées** : autorisez d’autres langues pour certaines collections ou bibliothèques, sans modifier vos préférences générales.
- **VOSTFR à la demande** : ajoutez les titres souhaités ; chaque ajout lance une recherche prioritaire lorsque l’indexation est active. En option, SubMaker prépare leurs sous-titres français, et chaque titre affiche l’état de ses sous-titres.
- **Animés** : créez leurs bibliothèques et collections à partir de TMDB, avec vos préférences FR/MULTi et les addons Stremio de votre choix.

### ![](https://api.iconify.design/lucide/folder-open.svg?color=%2314b8a6&width=24&height=24) Vos vidéos locales, films et vidéos personnelles

- **Dossiers de l’hôte ou montages réseau** : exposez-les par des volumes Docker en lecture seule. Analyse à la demande et chaque heure, sans déplacer ni supprimer vos fichiers.
- **Rapprochement avec le catalogue** : recherche dans les fiches déjà présentes dans Remix, puis TMDB ; films et épisodes identifiés réutilisent leurs fiches. Sans correspondance fiable, les vidéos personnelles restent affichées telles quelles.
- **Bibliothèques et collections par dossier**, privées par défaut ; partage facultatif avec certains spectateurs ou tous. [Configuration et formats](docs/pse/GUIDE.fr.md#médias-locaux-et-ibroadcast).

### ![](https://api.iconify.design/lucide/radar.svg?color=%2314b8a6&width=24&height=24) Des sources réunies et une recherche anticipée

- **Console Remix** : réglages regroupés dans `/ether/remix`, blocs repliables fermés par défaut ; iBroadcast rejoint les fournisseurs musicaux et les informations Subsonic restent dans Contenus → Musique.
- **Configuration commune à Ether et Remix** : addons Stremio, indexeurs, services Usenet, comptes debrid, sources musicales et IPTV réunis dans un même espace.
- **Indexation en arrière-plan** : recherchez les releases via vos addons et indexeurs Prowlarr, Jackett ou NZBHydra2 avant le lancement d’une lecture.
- **Panne temporaire d’un indexeur** : réutilisez ses résultats expirés depuis moins de 24 heures, sans prolonger leur validité ni contourner les vérifications de lecture. Les erreurs d’accès et les quotas restent respectés.
- **Recherches maîtrisées** : budgets quotidiens par source, pause pendant la lecture, priorités par collection ou bibliothèque et recherche manuelle immédiate.
- **Recherche depuis Plex ou Jellyfin** : les premiers résultats jamais cherchés ou restés sans release passent en priorité, selon leur délai de nouvelle tentative.
- **Disponibilités SeerrCatalog** : importez les disponibilités confirmées et les sources récentes de [SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog), sans nouvelle recherche pour le catalogue « lisible uniquement ». La confirmation persiste jusqu’à suppression ; les sources de lecture périmées sont recherchées à nouveau si nécessaire.
- **Recherches pour SeerrCatalog** : autorisez les recherches complémentaires dans les sources Remix, notamment Prowlarr, avec une clé dédiée. Recherche et partage des disponibilités sont deux fonctions indépendantes, regroupées dans les réglages SeerrCatalog.
- **Client torrent en option** : envoyez les releases de Prowlarr ou Jackett à un à trois clients qBittorrent ou rTorrent/ruTorrent, avec ratio et durée de partage par client et par tracker, cross-seed et alerte de place libre. Le client peut être distant, mais Remix doit voir son dossier de téléchargement (même machine ou partage NFS/SMB) pour lire les fichiers. Désactivé par défaut ; les comptes debrid restent en cache uniquement. [Détails](docs/pse/TORRENT.fr.md)
- **Base StreamFusion Reborn** : importez les releases françaises, MULTi ou VOSTFR et recherchez directement films, épisodes et packs dans votre base en lecture seule. Les preuves de cache debrid expirent ; la lecture revérifie la disponibilité et conserve le recours aux autres sources. Les titres ajoutés plus tard au catalogue sont aussi rattrapés. [Mise en place](docs/pse/STREAMFUSION.fr.md).

### ![](https://api.iconify.design/lucide/captions.svg?color=%2314b8a6&width=24&height=24) Sous-titres externes et traduction avec SubMaker

- **Instance au choix** : utilisez [SubMaker](https://github.com/xtremexq/StremioSubMaker) [public](https://submaker.elfhosted.com/configure) ou auto-hébergé avec un manifeste personnel ; l’intégration est désactivée par défaut.
- **Langues et périmètres** : choisissez les langues source/cible, anglais → français par défaut, et autorisez le traitement globalement ou par bibliothèque/collection.
- **Préparation contrôlée** : recherche d’un SRT correspondant à la release, traduction si nécessaire, file d’attente, cache et budget journalier. Seuls les résultats complets et validés sont retenus ; les traductions doivent conserver les timecodes et le nombre de répliques.
- **Lecture et SeerrCatalog** : ajoutez le SRT préparé aux pistes de la release correspondante et transmettez-le à SeerrCatalog par la recherche authentifiée. Une traduction en attente ne confirme pas la disponibilité ; une lecture déjà ouverte doit être relancée après préparation.
- **Release déjà sous-titrée en priorité** : à qualité, langue audio et score de formats égaux, la lecture choisit la release dont le SRT est déjà prêt plutôt qu’une autre ayant plus de seeders ou un fichier plus gros. [Configuration et limites](docs/pse/SUBMAKER.fr.md).

### ![](https://api.iconify.design/lucide/library.svg?color=%2314b8a6&width=24&height=24) Des bibliothèques et collections personnalisées

- Organisez vos bibliothèques Plex par **service, studio, animés, documentaires, concerts ou spectacles**. Les concerts combinent des mots-clés de captation et le genre Musique pour limiter les fictions.
- Composez leurs rangées avec des collections TMDB, des catalogues Stremio importés et des modèles prêts à l’emploi.
- Réglez la fréquence d’actualisation de chaque collection, de **1 à 168 heures**, indépendamment de la recherche de releases.
- Ajustez l’apparition des films avant ou après leur sortie numérique/physique avec un **décalage en jours**.
- Profitez d’une préparation groupée des badges d’affiches dans Plex et Jellyfin, avec l’état de lecture propre à chaque spectateur et des compteurs exacts.
- Affichez les affiches plus vite : avec un fournisseur d’affiches (PostersPlus, XRDB, RPDB…), Plex garde chaque affiche au lieu de la redemander à chaque affichage, et vous réglez le délai de renouvellement. Jellyfin et Emby reçoivent l’affiche à la taille demandée, sans attente. Les grandes résolutions demandées pour les vues détaillées sont conservées.

### ![](https://api.iconify.design/lucide/headphones.svg?color=%2314b8a6&width=24&height=24) La musique dans vos applications

- Parcourez le **catalogue Deezer public** depuis Plex/Plexamp, les clients Jellyfin ou une application Subsonic : Symfonium, Feishin, Supersonic, DSub…
- Écoutez via **Audius, les addons Eclipse, DMM avec Real-Debrid ou vos comptes AllDebrid, Real-Debrid et TorBox**, selon les sources activées.
- Instance DMM publique : [catalogue](https://debridmediamanager.com/albums). L’adresse est configurable dans Sources → Streaming musical → DMM / Real-Debrid pour utiliser une instance auto-hébergée disposant de son catalogue musical et de son API.
- **Musique locale** : lisez vos fichiers dans leur format d’origine, avec artistes, albums et tags, depuis des dossiers de l’hôte ou des montages réseau exposés par des volumes Docker en lecture seule. Analyse à la demande et chaque heure, sans déplacer ni supprimer les fichiers.
- **iBroadcast** : chaque spectateur peut connecter plusieurs comptes par code OAuth et retrouver leurs bibliothèques. Accès en lecture seule, lecture MP3 128 kb/s ; import des playlists en option.
- **Accès privés et partage** : dossiers musicaux et comptes iBroadcast sont privés par défaut, partageables avec certains spectateurs ou tous. Les sources autorisées apparaissent ensemble dans Musique. [Configuration et formats](docs/pse/GUIDE.fr.md#médias-locaux-et-ibroadcast).
- Définissez l’ordre des sources : lorsqu’une source échoue, Remix essaie les suivantes. Le mode HiFi API externe s’utilise seulement lorsqu’aucune autre source musicale n’est active.
- Retrouvez vos **favoris et votre historique par utilisateur**. La qualité disponible dépend des sources.
- **Activité musicale** : lectures en cours signalées par Plex, Jellyfin/Emby et Subsonic ; historique chronologique commun aux vidéos et à la musique ; onglet **Écoute** avec tendances quotidiennes, horaires et classements par morceau, artiste, album et auditeur, filtrables par période et utilisateur.

#### ![](https://api.iconify.design/lucide/list-music.svg?color=%2314b8a6&width=24&height=24) Vos playlists et de nouvelles découvertes

- **Parcourez les playlists Deezer** et connectez votre compte Spotify pour retrouver vos playlists accessibles.
- **Importez par lien** depuis Deezer, Spotify ou ListenBrainz, ou à partir d’un fichier CSV/XSPF.
- Vérifiez l’aperçu, choisissez le spectateur destinataire et suivez les titres retrouvés ou manquants. Les imports créent des copies personnelles sans remplacer vos autres playlists.
- Synchronisez les profils Deezer publics, Last.fm et ListenBrainz ; gérez vos playlists personnelles depuis les clients Subsonic.
- **Playlists iBroadcast** : activez leur import pour chaque compte connecté ; ordre des titres et répétitions conservés, actualisation sans doublons, sans modification chez iBroadcast.
- Explorez les **tops par pays, écoutes récentes, titres les plus écoutés et nouveautés des artistes suivis**. Genres, artistes similaires, radios et mix sont proposés selon le client.

### ![](https://api.iconify.design/lucide/chart-no-axes-combined.svg?color=%2314b8a6&width=24&height=24) Statistiques et suivi

- Remix s’ouvre sur les statistiques : anneaux de progression colorés, historique à barres fines, recherches, erreurs, quotas, durées et résultats par source en direct.
- Consultez l’historique **par jour, semaine, mois ou année**, en valeurs par période ou cumulées, avec filtres films, séries, animés et sources.
- Comparez le catalogue local aux totaux TMDB et retrouvez les opérations dans le journal d’activité.
- Connectez Remix à **Scrob comme un serveur Jellyfin** et choisissez dans Scrob le sens et la fréquence des synchronisations : historique de visionnage et notes, dans un sens ou les deux. Le webhook pour les événements en temps réel (délai maximal de **60 secondes**) et les bibliothèques « Vus et notés » sont facultatifs.
- Associez **Last.fm** à chaque utilisateur pour ses écoutes musicales et retrouvez les statistiques dans la Vue d’ensemble.

### ![](https://api.iconify.design/lucide/palette.svg?color=%2314b8a6&width=24&height=24) Une interface adaptée à vos habitudes

- Six onglets : **Sources**, **Langues et qualité**, **VOSTFR à la demande**, **Contenus et bibliothèques**, **Statistiques**, **Notifications et suivi**.
- Sources regroupées par famille, tri par catégorie ou contenu, clés debrid affichables ponctuellement et blocs repliables sans perdre la saisie. Réglez les palettes dans **Apparence**.
- Six palettes : **Ether, Nord, Catppuccin, Solarized, Gruvbox et Rosé Pine**, chacune en clair et sombre, avec aperçu immédiat et mémorisation par navigateur.

### ![](https://api.iconify.design/lucide/refresh-cw.svg?color=%2314b8a6&width=24&height=24) La base Ether, enrichie par Remix

- **Nom affiché** : renommez un utilisateur dans la console pour les statistiques et l’activité, même avec un compte Plex. La connexion ne change pas.
- Synchronisation quotidienne avec le projet d’origine, contrôles avant fusion et signalement des échecs.
- Images Docker pour **amd64 et arm64** et présentation des nouveautés dans la console.

## Démarrer

1. **Installer** `ghcr.io/aerya/ether-remix:latest` avec le [Compose fourni](docs/pse/GUIDE.fr.md#installation), via Dockge-Enhanced. L’image est publique, sans connexion à GHCR.
2. **Ouvrir la console** sur `http://<hôte>:32400/ether/` et suivre l’assistant. Le port se règle avec `ETHER_PORT`.
3. **Configurer les sources** debrid, Usenet, indexeurs et addons dans **Ether-Remix → Sources**, puis vos langues, contenus et bibliothèques dans les onglets correspondants.
4. **Connecter vos clients**. Pour Arpeggi et les clients Subsonic à jeton, créer un accès dans **Contenus et bibliothèques → Musique → Clients musicaux** : adresse du serveur, identifiant et mot de passe musicaux dédiés, révocables par spectateur. Les comptes locaux restent utilisables en mode **legacy**. Utiliser HTTPS à distance.

Vous migrez depuis Ether ? Le format de données est conservé : sauvegardez `/config`, arrêtez l’ancien serveur et réutilisez ses volumes `/config` et `/cache`.

## Documentation

| Besoin | Guide |
| --- | --- |
| Installation, réglages, sources musicales, playlists, Scrob et clients | [Guide de configuration](docs/pse/GUIDE.fr.md) |
| Visibilité des titres, priorités et délais de recherche | [Catalogue et indexation](docs/pse/CATALOGUE.fr.md) |
| Partage des disponibilités et recherches complémentaires | [Intégration SeerrCatalog](docs/pse/SEERRCATALOG.fr.md) |
| Import en lecture seule depuis une base StreamFusion Reborn | [StreamFusion Reborn](docs/pse/STREAMFUSION.fr.md) |
| Instance publique ou auto-hébergée, langues et limites de traduction | [SubMaker](docs/pse/SUBMAKER.fr.md) |

## Origine et licence

Ether-Remix est un fork d’[Ether](https://github.com/braedonsaunders/ether) de Braedon Saunders, sous **GPL-3.0**.

Inspirations : [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) et [Silo Server](https://github.com/Silo-Server/silo-server). Intégration de suivi : [Scrob](https://github.com/Aerya/scrob). Les métadonnées vidéo utilisent [TMDB](https://www.themoviedb.org/), sans approbation ni certification de TMDB.

[Licence](LICENSE) · [Informations légales](LEGAL.md) · [Composants tiers](THIRD_PARTY_NOTICES.md)
