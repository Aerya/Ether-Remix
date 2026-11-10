# BÊTA — Ether-Remix

**Projet en développement actif : des bugs et des changements incompatibles peuvent survenir.**

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/ether-remix-signature-dark.svg">
    <img src=".github/assets/ether-remix-signature-light.svg" width="560" alt="Ether Remix">
  </picture>
</p>

# Ether-Remix

**Ether en VF, avec la musique.**

Fork d’[Ether](https://github.com/braedonsaunders/ether), Ether-Remix ajoute un catalogue orienté français, des bibliothèques personnalisées et la musique partagée entre vos applications, tout en conservant le serveur, les sources debrid/Usenet et les clients d’Ether.

**🇫🇷 Français · [🇬🇧 English](README.en.md)**

<p align="center">
  <img src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-lightgrey" alt="amd64 et arm64">
  <img src="https://img.shields.io/badge/licence-GPL--3.0-green" alt="GPL-3.0">
</p>

## Fonctionnalités

### ![](https://api.iconify.design/lucide/clapperboard.svg?color=%2314b8a6&width=24&height=24) Films, séries et animés selon vos préférences

- **Catalogue orienté français** : choisissez vos langues et variantes — VFF, VFQ, VFi, MULTi… — ainsi que vos critères de résolution, codec et disponibilité en cache.
- **Catalogue « lisible uniquement »** : affichez les titres disposant d’un minimum de releases correspondant à vos critères. La recherche, les collections, la watchlist et la reprise de lecture restent accessibles.
- **Exceptions ciblées** : autorisez d’autres langues pour certaines collections ou bibliothèques, sans modifier vos préférences générales.
- **VOSTFR à la demande** : ajoutez les titres souhaités ; chaque ajout lance une recherche prioritaire lorsque l’indexation est active.
- **Animés** : créez leurs bibliothèques et collections à partir de TMDB, avec vos préférences FR/MULTi et les addons Stremio de votre choix.

### ![](https://api.iconify.design/lucide/radar.svg?color=%2314b8a6&width=24&height=24) Des sources réunies et une recherche anticipée

- **Configuration commune à Ether et Remix** : addons Stremio, indexeurs, services Usenet, comptes debrid, sources musicales et IPTV réunis dans un même espace.
- **Indexation en arrière-plan** : recherchez les releases via vos addons et indexeurs Prowlarr, Jackett ou NZBHydra2 avant le lancement d’une lecture.
- **Recherches maîtrisées** : budgets quotidiens par source, pause pendant la lecture, priorités par collection ou bibliothèque et recherche manuelle immédiate.
- **Recherche depuis Plex ou Jellyfin** : les premiers résultats jamais cherchés ou restés sans release passent en priorité, selon leur délai de nouvelle tentative.
- **Intégration SeerrCatalog** : récupérez les disponibilités et sources déjà contrôlées pour vos demandes Seerr, sans doubler les recherches. En cas de besoin, la lecture revient aux sources habituelles.

### ![](https://api.iconify.design/lucide/library.svg?color=%2314b8a6&width=24&height=24) Des bibliothèques et collections personnalisées

- Organisez vos bibliothèques Plex par **service, studio, animés, documentaires, concerts ou spectacles**.
- Composez leurs rangées avec des collections TMDB, des catalogues Stremio importés et des modèles prêts à l’emploi.
- Réglez les exceptions de langue, les priorités de recherche et la fréquence d’actualisation de chaque collection, de **1 à 168 heures**.
- Ajustez l’apparition des films avant ou après leur sortie numérique/physique avec un **décalage en jours**.
- Profitez d’une préparation groupée des badges d’affiches dans Plex et Jellyfin, avec l’état de lecture propre à chaque spectateur et des compteurs exacts.

### ![](https://api.iconify.design/lucide/headphones.svg?color=%2314b8a6&width=24&height=24) La musique dans vos applications

- Parcourez le **catalogue Deezer public** depuis Plex/Plexamp, les clients Jellyfin ou une application Subsonic : Symfonium, Feishin, Supersonic, DSub…
- Écoutez via **Audius, les addons Eclipse, DMM avec Real-Debrid ou vos comptes AllDebrid, Real-Debrid et TorBox**, selon les sources activées.
- Définissez l’ordre des sources : lorsqu’une source échoue, Remix essaie les suivantes. Un mode HiFi API externe est également disponible séparément.
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
- Connectez Remix à **Scrob comme un serveur Jellyfin** et choisissez dans Scrob le sens et la fréquence des synchronisations : historique de visionnage et notes, dans un sens ou les deux. Le webhook pour les événements en temps réel et les bibliothèques « Vus et notés » sont facultatifs.
- Associez **Last.fm** à chaque utilisateur pour ses écoutes musicales et retrouvez les statistiques dans la Vue d’ensemble.

### ![](https://api.iconify.design/lucide/palette.svg?color=%2314b8a6&width=24&height=24) Une interface adaptée à vos habitudes

- Six onglets : **Sources**, **Langues et qualité**, **VOSTFR à la demande**, **Contenus et bibliothèques**, **Statistiques**, **Notifications et suivi**.
- Sources regroupées par famille, tri par catégorie ou contenu, clés debrid affichables ponctuellement et blocs repliables sans perdre la saisie. Réglez les palettes dans **Apparence**.
- Six palettes : **Ether, Nord, Catppuccin, Solarized, Gruvbox et Rosé Pine**, chacune en clair et sombre, avec aperçu immédiat et mémorisation par navigateur.

### ![](https://api.iconify.design/lucide/refresh-cw.svg?color=%2314b8a6&width=24&height=24) La base Ether, enrichie par Remix

- Conservation des fonctionnalités et clients compatibles d’Ether : **Plex, Jellyfin, Emby, Infuse, Swiftfin, Silo et Stremio**, avec ses sources vidéo debrid et Usenet.
- Synchronisation quotidienne avec le projet d’origine, contrôles avant fusion et signalement des échecs.
- Images Docker pour **amd64 et arm64** et présentation des nouveautés dans la console.

## SeerrCatalog : des demandes aux contenus disponibles

[SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog) transforme les demandes Seerr en catalogues Stremio et transmet à Remix ses contrôles de disponibilité et ses sources, sans recherche en double.

1. Dans SeerrCatalog, ouvrez **Paramètres → Intégration Ether Remix**, générez une clé et activez l’export.
2. Dans Remix, ouvrez **Sources → Stremio → SeerrCatalog → Ether Remix**, saisissez l’adresse de base (ex. `https://seerrcatalog.example`, sans `/api/…`) et la clé, activez puis enregistrez. **Tester la connexion** vérifie le flux sans importer ; la synchronisation s’effectue chaque minute.
3. Les titres déjà validés sont exportés automatiquement par la nouvelle version de SeerrCatalog, sans relancer leurs recherches.

Les disponibilités de SeerrCatalog passent le filtre **« lisible uniquement »** sans nouvelle interrogation des addons. Les autres filtres et règles de lecture restent actifs. L’import récupère les fiches TMDB, sans créer de collection.

Disponibilités valables **26 h**, revérifiées après 24 h avec réveil horaire. Liens et en-têtes réutilisés au maximum **30 min** ; hash torrent jusqu’à expiration de l’observation. Source périmée, refusée par les règles du spectateur ou défaillante : retour à la recherche habituelle. Les URL peuvent expirer plus tôt ou dépendre de l’IP. Pour les séries, seuls les épisodes contrôlés fournissent des sources réutilisables.

La synchronisation paginée rattrape modifications et suppressions après coupure ; un import incomplet conserve les données valides. Les sources sensibles restent en mémoire, hors API de catalogue, et sont relues au redémarrage. Désactiver l’intégration retire sa confiance. La clé partage les sources de **tous les utilisateurs SeerrCatalog** : instance Remix de confiance et HTTPS hors réseau privé requis.

Variables équivalentes : `PSE_SEERRCATALOG=on`, `PSE_SEERRCATALOG_URL=http://seerrcatalog:7000`, `PSE_SEERRCATALOG_TOKEN=…`. L’option est désactivée par défaut.

**Recherche complémentaire pour SeerrCatalog** : dans les paramètres SeerrCatalog, choisissez Ether Remix et renseignez son adresse de base. Générez une **clé dédiée aux recherches** dans ce bloc, puis copiez-la dans **Remix → Sources → Stremio → Recherches pour SeerrCatalog** et activez les recherches. Enregistrez des deux côtés. Le partage des disponibilités possède sa propre clé et peut rester désactivé. Après mise à jour des deux applications, configurez cette nouvelle clé : l’ancienne clé de partage n’autorise plus les recherches. Lorsque les addons ne trouvent aucune release dans les langues principales et que le repli utilisateur est activé, SeerrCatalog demande à Remix une recherche dans ses sources configurées, notamment Prowlarr. Les tags principaux/de repli, la déduplication et les seuils restent appliqués par SeerrCatalog. Les contrôles J+1/J+3 et la disponibilité définitive sont conservés. Les recherches respectent les budgets de disponibilité et ne réutilisent pas circulairement les observations SeerrCatalog. Les torrents avec infoHash et les vrais flux HTTP peuvent être retournés ; les liens NZB ne sont jamais présentés comme des vidéos. Le test de connexion s’effectue depuis SeerrCatalog, sans lancer de recherche.

## Les sources musicales

Les sources s’activent séparément dans **Console → Ether-Remix → Sources → Streaming musical**. Les comptes debrid et les indexeurs déjà configurés sont réutilisés.

| Source | Accès et rôle |
| --- | --- |
| **Audius** | MP3 public sans compte ni clé API, sur les titres correspondant au catalogue. Placé en premier ou en repli. |
| **Tido, Internet Archive, UltraMax, Deezer Ricky, SoundCloud (Eclipse et Ricky)** | Entrées dédiées avec lien de configuration et manifest personnel masqué. UltraMax et Deezer Ricky nécessitent un compte Deezer. Qualité et disponibilité selon le fournisseur. |
| **Autres addons Eclipse** | Un ou plusieurs liens `manifest.json`, essayés dans l’ordre. |
| **DMM · Real-Debrid** | Recherche dans le catalogue musical de Debrid Media Manager, puis lecture avec vos comptes Real-Debrid existants, sans Prowlarr. |
| **AllDebrid, Real-Debrid, TorBox** | Vos comptes existants, pour lire les pistes d’albums déjà en cache. Plusieurs comptes d’un même service sont essayés l’un après l’autre. |
| **HiFi API** | Mode à part, via une instance externe : utilisé **seulement** quand aucune autre source n’est active, jamais ajouté en repli. |

Les sources sont essayées automatiquement selon leur priorité, avec repli vers les suivantes. La console permet de consulter leur ordre et leur état. Les liens de configuration des fournisseurs sont actualisés depuis le registre public Eclipse.

Les flux sont relayés sans conversion audio ni stockage local. La disponibilité et la qualité audio dépendent des sources et des fichiers proposés.

### Clients musique

| Client | Accès |
| --- | --- |
| Plexamp, Plex Web, applications Plex | Section **Musique** de Plex |
| Finamp, Jellyfin web, Findroid, Streamyfin | Bibliothèque **Musique** Jellyfin |
| Symfonium, Feishin, Supersonic, Tempo, DSub… | API Subsonic, authentification **legacy** |

## Quels titres sont affichés et cherchés

Avec le catalogue **« lisible uniquement »**, Remix connaît tout le catalogue TMDB mais n’affiche un titre que s’il a assez de releases indexées (2 par défaut) qui respectent le filtre choisi, par exemple VF ou MULTi en cache debrid. Les titres **VOSTFR** et les exceptions de langue comptent leurs releases dans toutes les langues.

- **Listes filtrées** : accueil, Films, Séries, recommandations. La recherche, la watchlist, les collections et la reprise de lecture ne le sont pas : un titre choisi reste accessible, et sa lecture interroge les sources en direct.
- **Ordre de recherche** : popularité TMDB décroissante, en alternant films et séries. Passent avant : les titres VOSTFR et exceptions de langue, puis les collections et bibliothèques mises en priorité.
- **Recherche immédiate** : un ajout VOSTFR, le bouton **Chercher maintenant**, et une recherche dans Plex ou Jellyfin. Pour une recherche, les 5 premiers titres trouvés passent en tête de file s’ils n’ont jamais été cherchés, ou n’avaient rien donné il y a plus de 6 h.
- **Titres à risque** : l’option **Afficher aussi les titres à risque** garde les titres qui ont moins de releases que le minimum (au moins une). Plex et Jellyfin les signalent par « ⚠ 1 source » au début du résumé, par un badge « 1 SOURCE » sur les affiches Ether, ou les deux. Avec un autre outil d’affiches, choisissez le résumé.
- **Catalogue local** : Ether ne liste pas tout TMDB, seulement les titres assez connus et déjà sortis, selon **Catalogue → Masquer les titres obscurs**. C’est dans cet ensemble que Remix cherche des releases.

## Délais de rafraîchissement

**Métadonnées TMDB**

| Donnée | Fréquence |
| --- | --- |
| Catalogue complet | Toutes les 24 h (réglable) |
| Nouveautés et tendances | Toutes les 4 h |
| Fiches détaillées manquantes | Par lots, toutes les 90 s, en commençant par les sorties récentes et les titres populaires |
| Séries en cours | Fiche actualisée à chaque cycle du catalogue ; 1 h au maximum à l’affichage d’une saison active |

**Recherche de releases**

| Résultat de la dernière recherche | Nouvel essai après |
| --- | --- |
| Titre sorti il y a moins de 60 jours | 2 jours |
| Aucune release trouvée | 7 jours |
| Releases trouvées | 14 jours |
| Source en panne ou réponse partielle | 24 h |

Ces délais sont des minimums. La recherche dépend du budget quotidien de chaque source (1000 requêtes par défaut), de l’intervalle entre deux titres (10 s par défaut) et se met en pause pendant la lecture : sur un grand catalogue, un titre peut attendre plus longtemps. Une release non revue depuis 60 jours ne compte plus.

Dans **Sources → Indexation**, le bloc repliable **Priorité de recherche** permet de faire passer une collection ou une bibliothèque avant le reste : *Prioritaire* retente un titre sans release après 1 jour, *Maximale* après 6 h. **Chercher maintenant** ignore ces délais pour la sélection. Les budgets par source restent appliqués. À l’import, SeerrCatalog (mise à jour toutes les heures, priorité maximale) et RSS Catalog (toutes les 3 h, prioritaire) sont reconnus et préréglés ; ces réglages restent modifiables.

**Collections et bibliothèques**

Chaque collection a sa propre cadence : `live` (à la demande), `hourly`, `daily` (par défaut), `weekly` ou toutes les N heures, de 1 à 168. Certaines sources imposent un délai minimum. Les bibliothèques se mettent à jour avec leurs collections. Cette cadence est indépendante de la priorité de recherche de releases ; **Actualiser**, dans la même section, relance immédiatement une collection ou toutes celles d’une bibliothèque.

## Démarrer

1. **Installer** `ghcr.io/aerya/ether-remix:latest` avec le [Compose fourni](docs/pse/GUIDE.fr.md#installation), via Dockge-Enhanced. L’image est publique, sans connexion à GHCR.
2. **Ouvrir la console** sur `http://<hôte>:32400/ether/` et suivre l’assistant. Le port se règle avec `ETHER_PORT`.
3. **Configurer les sources** debrid, Usenet, indexeurs et addons dans **Ether-Remix → Sources**, puis vos langues, contenus et bibliothèques dans les onglets correspondants.
4. **Connecter vos clients**. Pour Subsonic, utiliser l’adresse du serveur, un compte local avec mot de passe et l’authentification **legacy** ; préférer HTTPS pour un accès distant.

Vous migrez depuis Ether ? Le format de données est conservé : sauvegardez `/config`, arrêtez l’ancien serveur et réutilisez ses volumes `/config` et `/cache`.

## Documentation

- [Guide français : installation, variables, Scrob, musique et clients](docs/pse/GUIDE.fr.md) · [English guide](docs/pse/GUIDE.en.md)

## Origine et licence

Ether-Remix est un fork d’[Ether](https://github.com/braedonsaunders/ether) de Braedon Saunders, sous **GPL-3.0**.

Inspirations : [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) et [Silo Server](https://github.com/Silo-Server/silo-server). Intégration de suivi : [Scrob](https://github.com/Aerya/scrob). Les métadonnées vidéo utilisent [TMDB](https://www.themoviedb.org/), sans approbation ni certification de TMDB.

[Licence](LICENSE) · [Informations légales](LEGAL.md) · [Composants tiers](THIRD_PARTY_NOTICES.md)
