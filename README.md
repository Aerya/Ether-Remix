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

## Fonctionnalités

### ![](https://api.iconify.design/lucide/clapperboard.svg?color=%2314b8a6&width=24&height=24) Films, séries et animés selon vos préférences

- **Profils de qualité** : formats, codecs, résolutions, HDR/Dolby Vision, taille, langues et regex, globalement ou par bibliothèque/collection. Import des filtres compatibles AIOStreams depuis un export JSON, avec aperçu et modification avant enregistrement.
- **Catalogue orienté français** : choisissez vos langues et variantes — VFF, VFQ, VFi, MULTi… — et vos exigences de disponibilité en cache.
- **Catalogue « lisible uniquement »** : affichez les titres disposant d’un minimum de releases correspondant à vos critères. La recherche, les collections, la watchlist et la reprise de lecture restent accessibles.
- **Exceptions ciblées** : autorisez d’autres langues pour certaines collections ou bibliothèques, sans modifier vos préférences générales.
- **VOSTFR à la demande** : ajoutez les titres souhaités ; chaque ajout lance une recherche prioritaire lorsque l’indexation est active.
- **Animés** : créez leurs bibliothèques et collections à partir de TMDB, avec vos préférences FR/MULTi et les addons Stremio de votre choix.

### ![](https://api.iconify.design/lucide/radar.svg?color=%2314b8a6&width=24&height=24) Des sources réunies et une recherche anticipée

- **Configuration commune à Ether et Remix** : addons Stremio, indexeurs, services Usenet, comptes debrid, sources musicales et IPTV réunis dans un même espace.
- **Indexation en arrière-plan** : recherchez les releases via vos addons et indexeurs Prowlarr, Jackett ou NZBHydra2 avant le lancement d’une lecture.
- **Recherches maîtrisées** : budgets quotidiens par source, pause pendant la lecture, priorités par collection ou bibliothèque et recherche manuelle immédiate.
- **Recherche depuis Plex ou Jellyfin** : les premiers résultats jamais cherchés ou restés sans release passent en priorité, selon leur délai de nouvelle tentative.
- **Disponibilités SeerrCatalog** : importez les disponibilités confirmées et les sources récentes de [SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog), sans nouvelle recherche pour le catalogue « lisible uniquement ». La confirmation persiste jusqu’à suppression ; les sources de lecture périmées sont recherchées à nouveau si nécessaire.
- **Recherches pour SeerrCatalog** : autorisez les recherches complémentaires dans les sources Remix, notamment Prowlarr, avec une clé dédiée. Recherche et partage des disponibilités sont deux fonctions indépendantes, regroupées dans les réglages SeerrCatalog.

### ![](https://api.iconify.design/lucide/captions.svg?color=%2314b8a6&width=24&height=24) Sous-titres externes et traduction avec SubMaker

- **Instance au choix** : utilisez [SubMaker](https://github.com/xtremexq/StremioSubMaker) [public](https://submaker.elfhosted.com/configure) ou auto-hébergé avec un manifeste personnel ; l’intégration est désactivée par défaut.
- **Langues et périmètres** : choisissez les langues source/cible, anglais → français par défaut, et autorisez le traitement globalement ou par bibliothèque/collection.
- **Préparation contrôlée** : recherche d’un SRT correspondant à la release, traduction si nécessaire, file d’attente, cache et budget journalier. Seuls les résultats complets et validés sont retenus ; les traductions doivent conserver les timecodes et le nombre de répliques.
- **Lecture et SeerrCatalog** : ajoutez le SRT préparé aux pistes de la release correspondante et transmettez-le à SeerrCatalog par la recherche authentifiée. Une traduction en attente ne confirme pas la disponibilité ; une lecture déjà ouverte doit être relancée après préparation. [Configuration et limites](docs/pse/SUBMAKER.fr.md).

### ![](https://api.iconify.design/lucide/library.svg?color=%2314b8a6&width=24&height=24) Des bibliothèques et collections personnalisées

- Organisez vos bibliothèques Plex par **service, studio, animés, documentaires, concerts ou spectacles**.
- Composez leurs rangées avec des collections TMDB, des catalogues Stremio importés et des modèles prêts à l’emploi.
- Réglez la fréquence d’actualisation de chaque collection, de **1 à 168 heures**, indépendamment de la recherche de releases.
- Ajustez l’apparition des films avant ou après leur sortie numérique/physique avec un **décalage en jours**.
- Profitez d’une préparation groupée des badges d’affiches dans Plex et Jellyfin, avec l’état de lecture propre à chaque spectateur et des compteurs exacts.

### ![](https://api.iconify.design/lucide/headphones.svg?color=%2314b8a6&width=24&height=24) La musique dans vos applications

- Parcourez le **catalogue Deezer public** depuis Plex/Plexamp, les clients Jellyfin ou une application Subsonic : Symfonium, Feishin, Supersonic, DSub…
- Écoutez via **Audius, les addons Eclipse, DMM avec Real-Debrid ou vos comptes AllDebrid, Real-Debrid et TorBox**, selon les sources activées.
- Définissez l’ordre des sources : lorsqu’une source échoue, Remix essaie les suivantes. Le mode HiFi API externe s’utilise seulement lorsqu’aucune autre source musicale n’est active.
- Retrouvez vos **favoris et votre historique par utilisateur**, sans constituer une bibliothèque de fichiers musicaux locaux. La qualité disponible dépend des sources.

### ![](https://api.iconify.design/lucide/list-music.svg?color=%2314b8a6&width=24&height=24) Vos playlists et de nouvelles découvertes

- **Parcourez les playlists Deezer** et connectez votre compte Spotify pour retrouver vos playlists accessibles.
- **Importez par lien** depuis Deezer, Spotify ou ListenBrainz, ou à partir d’un fichier CSV/XSPF.
- Vérifiez l’aperçu, choisissez le spectateur destinataire et suivez les titres retrouvés ou manquants. Les imports créent des copies personnelles sans remplacer vos autres playlists.
- Synchronisez les profils Deezer publics, Last.fm et ListenBrainz ; gérez vos playlists personnelles depuis les clients Subsonic.
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

- Conservation des fonctionnalités et clients compatibles d’Ether : **Plex, Jellyfin, Emby, Infuse, Swiftfin, Silo et Stremio**, avec ses sources vidéo debrid et Usenet.
- Synchronisation quotidienne avec le projet d’origine, contrôles avant fusion et signalement des échecs.
- Images Docker pour **amd64 et arm64** et présentation des nouveautés dans la console.

## Démarrer

1. **Installer** `ghcr.io/aerya/ether-remix:latest` avec le [Compose fourni](docs/pse/GUIDE.fr.md#installation), via Dockge-Enhanced. L’image est publique, sans connexion à GHCR.
2. **Ouvrir la console** sur `http://<hôte>:32400/ether/` et suivre l’assistant. Le port se règle avec `ETHER_PORT`.
3. **Configurer les sources** debrid, Usenet, indexeurs et addons dans **Ether-Remix → Sources**, puis vos langues, contenus et bibliothèques dans les onglets correspondants.
4. **Connecter vos clients**. Pour Subsonic, utiliser l’adresse du serveur, un compte local avec mot de passe et l’authentification **legacy** ; préférer HTTPS pour un accès distant.

Vous migrez depuis Ether ? Le format de données est conservé : sauvegardez `/config`, arrêtez l’ancien serveur et réutilisez ses volumes `/config` et `/cache`.

## Documentation

| Besoin | Guide |
| --- | --- |
| Installation, réglages, sources musicales, playlists, Scrob et clients | [Guide de configuration](docs/pse/GUIDE.fr.md) |
| Visibilité des titres, priorités et délais de recherche | [Catalogue et indexation](docs/pse/CATALOGUE.fr.md) |
| Partage des disponibilités et recherches complémentaires | [Intégration SeerrCatalog](docs/pse/SEERRCATALOG.fr.md) |
| Instance publique ou auto-hébergée, langues et limites de traduction | [SubMaker](docs/pse/SUBMAKER.fr.md) |

## Origine et licence

Ether-Remix est un fork d’[Ether](https://github.com/braedonsaunders/ether) de Braedon Saunders, sous **GPL-3.0**.

Inspirations : [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) et [Silo Server](https://github.com/Silo-Server/silo-server). Intégration de suivi : [Scrob](https://github.com/Aerya/scrob). Les métadonnées vidéo utilisent [TMDB](https://www.themoviedb.org/), sans approbation ni certification de TMDB.

[Licence](LICENSE) · [Informations légales](LEGAL.md) · [Composants tiers](THIRD_PARTY_NOTICES.md)
