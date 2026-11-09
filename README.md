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

Ether-Remix enrichit [Ether](https://github.com/braedonsaunders/ether) avec un catalogue orienté français, des bibliothèques personnalisées et une bibliothèque musicale partagée entre vos applications. Vous conservez le serveur, les sources debrid/Usenet et les clients compatibles d’Ether ; Ether-Remix ajoute les fonctions ci-dessous.

**🇫🇷 Français · [🇬🇧 English](README.en.md)**

<p align="center">
  <img src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-lightgrey" alt="amd64 et arm64">
  <img src="https://img.shields.io/badge/licence-GPL--3.0-green" alt="GPL-3.0">
</p>

## Fonctionnalités

| Apport | Fonctionnalités |
| --- | --- |
| **Un catalogue orienté VF** | Films et séries filtrés selon leur disponibilité française et les releases détectées : VFF, VFQ, VFi, MULTi… Pays et langues réglables, exceptions par collection ou bibliothèque, et sélection de titres en VOSTFR dans une bibliothèque dédiée. |
| **Un catalogue « lisible uniquement »** | Affiche les titres disposant d’un nombre minimum de releases indexées, selon vos critères de langue, résolution, codec et cache. La recherche, la watchlist et la reprise de lecture restent accessibles sans ce filtre. |
| **Une recherche de sources anticipée** | Indexation en arrière-plan des addons Stremio et des indexeurs Prowlarr, Jackett ou NZBHydra2, avec budgets quotidiens et pause pendant la lecture. |
| **Un tableau de bord dédié** | Onglet Stats avec comparaison TMDB, historique par jour, semaine, mois et année, valeurs par période ou cumulées, filtres films/séries et source, suivi en direct et journal d’activité. |
| **Des bibliothèques à votre image** | Bibliothèques Plex par service, studio, anime, documentaire, concert ou spectacle, avec rangées de collections et exceptions de langue. Import des catalogues Stremio en collections, modèles prêts à l’emploi et actualisation réglable de 1 à 168 heures. |
| **La musique dans vos applications** | Bibliothèque musicale dans Plex/Plexamp et les clients Jellyfin, plus une API Subsonic pour Symfonium, Feishin, Supersonic, DSub… Catalogue Deezer public, favoris et historique propres à chaque utilisateur. |
| **Des playlists et de la découverte** | Import Deezer, Last.fm et ListenBrainz ; playlists personnelles éditables via Subsonic ; favoris, écoutes récentes, titres les plus écoutés, tops par pays, découvertes et nouveautés des artistes suivis. Navigation par genre, artistes similaires, radios et mix selon le client. |
| **Le suivi avec Scrob et Last.fm** | Envoi des événements vidéo à Scrob et bibliothèques « Vus et notés » pour importer l’historique sans parcourir tout le catalogue. Liaison Last.fm par utilisateur pour les écoutes musicales, avec statistiques dans la Vue d’ensemble. |
| **Des réglages et notifications adaptés** | Page Ether-Remix pour activer et régler les fonctions sans redémarrer, onglet Stremio dédié, diagnostic des sources audio et notifications du propriétaire en français. |
| **Un fork qui suit Ether** | Synchronisation quotidienne avec contrôles avant fusion, signalement des échecs et images Docker `amd64`/`arm64`. Les fonctions et correctifs d’Ether restent la base du projet. |

Les compatibilités Plex, Jellyfin, Emby, Infuse, Swiftfin, Silo et Stremio, ainsi que le streaming vidéo debrid/Usenet, proviennent d’**Ether**. Ether-Remix complète ces fonctions ; il ne nécessite pas de constituer une bibliothèque de fichiers musicaux locaux.

## Les sources musicales

Les sources s’activent séparément dans **Console → Ether-Remix → Musique**. Les comptes debrid et les indexeurs déjà configurés sont réutilisés.

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

Dans **Bibliothèques**, **Priorité de recherche** permet de faire passer une collection ou une bibliothèque avant le reste : *Prioritaire* retente un titre sans release après 1 jour, *Maximale* après 6 h. **Chercher maintenant** ignore ces délais pour la sélection. Les budgets par source restent appliqués. À l’import, SeerrCatalog (mise à jour toutes les heures, priorité maximale) et RSS Catalog (toutes les 3 h, prioritaire) sont reconnus et préréglés ; ces réglages restent modifiables.

Cette priorité concerne la recherche de releases. Le rafraîchissement du contenu d’une collection suit sa propre cadence, réglable collection par collection (en direct, toutes les N heures, chaque jour, chaque semaine). Pour le lancer sans attendre, le bouton **Actualiser** de la même section relance la collection, ou toutes les collections d’une bibliothèque.

**Collections et bibliothèques**

Chaque collection a sa propre cadence : `live` (à la demande), `hourly`, `daily` (par défaut), `weekly` ou toutes les N heures, de 1 à 168. Certaines sources imposent un délai minimum. Les bibliothèques à thème se mettent à jour avec leurs collections.

## Démarrer

1. **Installer** `ghcr.io/aerya/ether-remix:latest` avec le [Compose fourni](docs/pse/GUIDE.fr.md#installation), via Dockge-Enhanced. L’image est publique, sans connexion à GHCR.
2. **Ouvrir la console** sur `http://<hôte>:32400/ether/` et suivre l’assistant. Le port se règle avec `ETHER_PORT`.
3. **Configurer les sources** debrid, Usenet, indexeurs et addons dans les pages d’Ether, puis activer les ajouts souhaités dans le menu **Ether-Remix**.
4. **Connecter vos clients**. Pour Subsonic, utiliser l’adresse du serveur, un compte local avec mot de passe et l’authentification **legacy** ; préférer HTTPS pour un accès distant.

Vous migrez depuis Ether ? Le format de données est conservé : sauvegardez `/config`, arrêtez l’ancien serveur et réutilisez ses volumes `/config` et `/cache`.

## Documentation

- [Guide français : installation, variables, Scrob, musique et clients](docs/pse/GUIDE.fr.md) · [English guide](docs/pse/GUIDE.en.md)

## Origine et licence

Ether-Remix est un fork d’[Ether](https://github.com/braedonsaunders/ether) de Braedon Saunders, sous **GPL-3.0**.

Inspirations : [Remux](https://github.com/lostb1t/remux), [Eclipse](https://eclipsemusic.app/), [Monochrome](https://monochrome.st/), [Polyfin](https://github.com/moodiness/polyfin) et [Silo Server](https://github.com/Silo-Server/silo-server). Intégration de suivi : [Scrob](https://github.com/Aerya/scrob). Les métadonnées vidéo utilisent [TMDB](https://www.themoviedb.org/), sans approbation ni certification de TMDB.

[Licence](LICENSE) · [Informations légales](LEGAL.md) · [Composants tiers](THIRD_PARTY_NOTICES.md)
