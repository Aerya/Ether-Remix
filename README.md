> [!WARNING]
> **BÊTA — Ether-Remix est encore en développement actif. Des bugs et des changements incompatibles peuvent survenir.**

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

| Fonction | Ce qu’elle permet |
| --- | --- |
| **Catalogue VF et qualité** | Films, séries et animés selon les pays, langues (VFF, VFQ, VFi, MULTi…), résolution, codec et cache choisis. Exceptions par collection/bibliothèque et **VOSTFR à la demande** avec recherche prioritaire. Mode **lisible uniquement** selon un minimum de releases ; règles détaillées ci-dessous. |
| **Indexation et sources** | Addons Stremio et indexeurs Prowlarr, Jackett, NZBHydra2 : recherche en arrière-plan, budgets quotidiens, pause pendant la lecture et priorités par collection/bibliothèque. Sources Ether et musicales réunies, tri par catégorie/contenu, clés debrid affichables ponctuellement. |
| **Bibliothèques et collections** | Bibliothèques Plex par service, studio, anime, documentaire, concert ou spectacle ; collections TMDB et catalogues Stremio importés, rangées personnalisées, modèles et exceptions de langue. Actualisation réglable de 1 à 168 h. Animés dans **Contenus et bibliothèques → Animes**, avec critères FR/MULTi et addons libres par manifest, aux priorités partagées. |
| **SeerrCatalog** | Récupération des demandes disponibles, fiches TMDB et sources déjà contrôlées, sans recherche en double ; repli vers les sources habituelles à la lecture. Intégration facultative, décrite ci-dessous. |
| **Navigation et statistiques** | Badges d’affiches groupés dans Plex/Jellyfin, état de lecture par spectateur, accès direct au catalogue lisible et compteurs exacts. Comparaison TMDB, historique jour/semaine/mois/année, valeurs par période ou cumulées, filtres films/séries/animes et source, suivi en direct et journal d’activité. |
| **Musique** | Catalogue Deezer public dans Plex/Plexamp, Jellyfin et Subsonic ; favoris et historique par utilisateur, sans bibliothèque de fichiers locaux. Sources, repli automatique et clients détaillés ci-dessous. |
| **Playlists** | Découverte Deezer, connexion Spotify personnelle, imports Deezer/Spotify/ListenBrainz par lien ou CSV/XSPF, avec aperçu et suivi des correspondances. Synchronisation des profils Deezer publics, Last.fm et ListenBrainz ; playlists personnelles éditables via Subsonic. |
| **Découverte musicale** | Favoris, écoutes récentes, titres les plus écoutés, tops par pays, découvertes et nouveautés des artistes suivis ; genres, artistes similaires, radios et mix selon le client. |
| **Suivi et notifications** | Événements vidéo vers Scrob, bibliothèques « Vus et notés » pour importer l’historique sans parcourir le catalogue ; Last.fm par utilisateur pour la musique et statistiques dans la Vue d’ensemble. Onglet **Notifications et suivi** dédié. |
| **Interface** | Six onglets Remix, langues et qualité regroupées, sources musicales par famille, blocs de sources et priorités repliables sans perdre la saisie. Six palettes — Ether, Nord, Catppuccin, Solarized, Gruvbox, Rosé Pine — en clair/sombre, avec aperçu immédiat et mémorisation par navigateur dans **Apparence**. |
| **Mises à jour** | Nouveautés non lues dans la console ; synchronisation quotidienne avec Ether, contrôles avant fusion, signalement des échecs et images Docker `amd64`/`arm64`. |
Les compatibilités Plex, Jellyfin, Emby, Infuse, Swiftfin, Silo et Stremio, ainsi que le streaming vidéo debrid/Usenet, proviennent d’**Ether**. Ether-Remix complète ces fonctions.

Dans **Sources → Stremio, Indexeurs et Indexation**, cliquez sur le titre d’un bloc pour le replier/déplier.

## SeerrCatalog : des demandes aux contenus disponibles

[SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog) transforme les demandes Seerr en catalogues Stremio et transmet à Remix ses contrôles de disponibilité et ses sources, sans recherche en double.

1. Dans SeerrCatalog, ouvrez **Paramètres → Intégration Ether Remix**, générez une clé et activez l’export.
2. Dans Remix, ouvrez **Sources → Stremio → SeerrCatalog → Ether Remix**, saisissez l’adresse de base (ex. `https://seerrcatalog.example`, sans `/api/…`) et la clé, activez puis enregistrez. **Tester la connexion** vérifie le flux sans importer ; la synchronisation s’effectue chaque minute.
3. Les titres déjà validés sont exportés automatiquement par la nouvelle version de SeerrCatalog, sans relancer leurs recherches.

Les disponibilités de SeerrCatalog passent le filtre **« lisible uniquement »** sans nouvelle interrogation des addons. Les autres filtres et règles de lecture restent actifs. L’import récupère les fiches TMDB, sans créer de collection.

Disponibilités valables **26 h**, revérifiées après 24 h avec réveil horaire. Liens et en-têtes réutilisés au maximum **30 min** ; hash torrent jusqu’à expiration de l’observation. Source périmée, refusée par les règles du spectateur ou défaillante : retour à la recherche habituelle. Les URL peuvent expirer plus tôt ou dépendre de l’IP. Pour les séries, seuls les épisodes contrôlés fournissent des sources réutilisables.

La synchronisation paginée rattrape modifications et suppressions après coupure ; un import incomplet conserve les données valides. Les sources sensibles restent en mémoire, hors API de catalogue, et sont relues au redémarrage. Désactiver l’intégration retire sa confiance. La clé partage les sources de **tous les utilisateurs SeerrCatalog** : instance Remix de confiance et HTTPS hors réseau privé requis.

Variables équivalentes : `PSE_SEERRCATALOG=on`, `PSE_SEERRCATALOG_URL=http://seerrcatalog:7000`, `PSE_SEERRCATALOG_TOKEN=…`. L’option est désactivée par défaut.

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
