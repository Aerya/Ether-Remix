# Feuille de route PSE

État au 5 novembre 2026, basé sur Ether `v0.3.4`. Chaque ajout est livré par une PR distincte, dans les emplacements définis par [FORK.fr.md](FORK.fr.md).

## Ce qu'Ether fournit déjà (non redéveloppé)

- **Interface en français** : console web traduite (6 600 messages, FR complet).
- **Clients Jellyfin** : Infuse, Swiftfin, Findroid, Streamyfin, Jellyfin Android TV, JellyCon, Roku.
- **Indexeurs** : Newznab/Torznab natifs, import Prowlarr, moteur Cardigann, addons Stremio comme sources.
- **Veille des sorties** (`internal/availability`) : flux des indexeurs lus toutes les 30 min, « preuves » de releases stockées en base, titres récents masqués tant qu'aucune release acceptable n'est vue, budget de requêtes par indexeur.
- **Langues des releases** : `VFF`, `VFQ`, `VFi`, `MULTi`, `VOSTFR`… reconnues (`internal/releaseparse`, `internal/langtag`), VOSTFR jamais comptée comme audio français.

Les ajouts PSE s'appuient sur ces briques au lieu de les dupliquer.

## 1. Notifications en français — ✅ fait

Paquet `internal/pse/notifyfr`, appliqué en un seul point juste avant l'envoi (`notify.Service.deliver`, repère `PSE-HOOK`) : tous les canaux (Discord, Slack, Telegram, Gotify, ntfy, webhook, e-mail) et le résumé hebdomadaire HTML. Textes fixes et modèles variables (version, nom du serveur, fournisseur) ; un texte inconnu (plugin, nouveau message upstream) part en anglais, sans perte. Les notifications push des spectateurs ne contiennent que des noms de séries et d'épisodes.

- Réglage : `PSE_NOTIFY_LANGUAGE` (`fr` par défaut, `en` pour l'anglais).
- Dérive upstream : un test relève les textes de notification d'Ether et signale ceux qui ne sont pas traduits, sans bloquer la synchronisation (bloquant avec `PSE_STRICT_I18N=1`).
- La file de notifications et les événements diffusés à la console et aux plugins restent en anglais : seule la livraison est traduite.

## 2. Catalogue en français — ✅ fait

Paquet `internal/pse/frenchcat`, condition SQL ajoutée avec celle de l'étape 5 dans `browseWhere` (même `PSE-HOOK`). Un titre est gardé si sa langue originale est le français, si TMDB connaît une sortie numérique (4) ou physique (5) passée dans un des pays `PSE_FRENCH_COUNTRIES` (défaut `FR`), ou si l'indexeur a trouvé une release dont la langue figure dans `PSE_FRENCH_RELEASES` (défaut `french,multi`). Les dates de sortie sont récupérées par la tâche d'Ether qui les remplit en mode « sorties numériques », activée aussi quand `PSE_FRENCH=on` (second `PSE-HOOK`, dans `sync.go`) : un film dont les dates ne sont pas encore connues reste masqué jusqu'à leur récupération (150 films toutes les 90 s).

Non retenu : dates de diffusion TV (type 6, non conservées par Ether), plateformes TMDB (appels supplémentaires), pays de production (stockés par nom, peu fiables), chaînes de diffusion des séries.

Description initiale :

Filtre de catalogue optionnel, combinant :
- les dates de sortie TMDB de type numérique (4) / physique (5) / TV (6) pour `FR` et `CA` ;
- les plateformes de diffusion TMDB pour `FR` / `CA` ;
- les releases réellement indexées (étapes 3–4) portant de l'audio `fr` (VFF, VFQ, VFi, TRUEFRENCH, MULTi) ;
- pour les séries, les chaînes et dates de diffusion françaises/québécoises.

Un titre est affiché si au moins un des critères activés est vérifié. Branchement prévu comme règle de catalogue (Catalog › Rules) pour hériter des mécanismes d'Ether.

## 3. Indexation AllDebrid via les addons Stremio — ✅ fait

L'API AllDebrid n'a plus de vérification de cache (`/magnet/instant` supprimé en 2024–2025) : vérifier une release directement oblige à l'ajouter au compte. PSE n'appelle donc **jamais** `magnet/upload` pour l'indexation. À la place, il interroge en arrière-plan les addons Stremio configurés (AIOStreams, Torrentio, Comet, StremThru…) qui indiquent déjà les releases en cache AllDebrid.

Test du 5 novembre 2026 sur une instance AIOStreams configurée pour AllDebrid : 8 sources pour un film, avec marqueur de cache (⚡, `[AD]`), nom de fichier complet, taille et résolution. Ces champs suffisent pour l'indexation.

- Stockage par release : titre (TMDB/IMDb, saison/épisode), nom de fichier, taille, résolution, codec vidéo/audio, HDR, langues audio/sous-titres (analyseur d'Ether), source, statut « en cache », première et dernière observation.
- Filtres d'indexation : langues, résolutions, codecs.
- Rythme : file de titres priorisée (récents, watchlists, populaires, puis fond de catalogue), débit plafonné par addon (réglable, prudent par défaut pour les instances publiques ; une instance AIOStreams auto-hébergée est recommandée).
- À vérifier avant activation : le nombre de magnets du compte AllDebrid avant/après une série de requêtes, pour confirmer que la vérification de cache de l'addon n'ajoute rien, même temporairement.
- L'URL d'un addon contient sa configuration (clés debrid) : saisie dans l'interface, stockée comme secret, jamais journalisée ni versionnée.

## 4. Indexation Prowlarr / Jackett / NZBHydra2 — ✅ fait

Extension de la veille d'Ether au-delà de la fenêtre de 60 jours : recherche par identifiant (IMDb/TMDB/TVDB) en arrière-plan sur tout le catalogue, mêmes filtres et même stockage que l'étape 3, en restant dans les budgets quotidiens par indexeur d'Ether (aucune requête au-delà de la part allouée au travail de fond).

### Réalisation (étapes 3 et 4)

Paquet `internal/pse/releasedb`, démarré depuis `internal/app/pse_indexer.go` (repère `PSE-HOOK` dans `build_integrations.go`).

- Tables `pse_releases`, `pse_index_titles`, `pse_index_budget`, `pse_index_cursor` (SQLite et PostgreSQL).
- Parcours du catalogue par popularité décroissante, films et séries en alternance ; un titre trouvé est revu après 14 jours, un titre sans résultat après 7 jours, un titre sorti depuis moins de 60 jours après 2 jours.
- Recherche par le gestionnaire d'indexeurs d'Ether, une source à la fois, en mode « arrière-plan » : seules les requêtes de recherche sont admises (aucun NZB téléchargé, aucun octet lu, aucune vérification debrid). Budget quotidien par source tenu par PSE en plus de celui d'Ether.
- Langues : noms du parseur d'Ether, complétés par `vfq` (VFQ, QC), `vff` (VFF, TRUEFRENCH) et `multi`.
- Cache : le marqueur de l'addon (`⚡`, `+`) donne `debrid_cached` ; une release `[AD]` sans éclair n'est pas comptée en cache.
- Filtres (`lang=fr,multi;min=1080;codec=hevc;cached=1`) appliqués à la lecture : rien n'est jeté à l'indexation, changer de filtre ne demande pas de réindexer.
- Toujours à faire par le propriétaire : comparer le nombre de magnets du compte AllDebrid avant/après une première série de requêtes.

## 5. Catalogue « lisible uniquement » — ✅ fait

Table `pse_playable` (nombre de releases distinctes respectant le filtre, par titre), tenue à jour à chaque indexation et recalculée au démarrage quand le filtre change. Une condition SQL est ajoutée à `browseWhere` du catalogue (repère `PSE-HOOK`, code dans `internal/catalog/pse_hooks.go`) : accueil, bibliothèques, facettes, recommandations, saisons et épisodes (via la série). Ne sont pas filtrés : listes par clé (watchlist, collections, reprise de lecture), explorateur de règles du propriétaire, recherche et accès direct à un titre. Réglages `PSE_PLAYABLE`, `PSE_PLAYABLE_MIN`, `PSE_PLAYABLE_FILTER`, `PSE_PLAYABLE_PENDING`. Les pages du catalogue sont mises en cache une minute par Ether : un titre nouvellement lisible apparaît dans la minute.

Description initiale :

Option : n'afficher que les titres ayant au moins *N* releases indexées distinctes (défaut : 2), respectant des filtres de langue, résolution et codec. Pour une série : par épisode, avec règle de visibilité de la saison/série. S'appuie sur la base des étapes 3–4 et sur les règles de visibilité d'Ether.

## 6. Scrob — ✅ fait

Scrob se connecte en Jellyfin. Appels vérifiés contre son code (`backend/core/jellyfin.py`) et rejoués dans un test du paquet `mediabrowser` :

- `Users/{id}/Views` : deux bibliothèques PSE « Vus et notés » (paquet `internal/frontend/kit/scrobview`, numéros de vue avec le bit 61) quand `PSE_SCROB_VIEWS=on` ;
- `Users/{id}/Items?ParentId=…&IncludeItemTypes=Movie|Series|Episode` : films vus ou notés, séries ayant un épisode vu/noté (ou notées), épisodes vus/notés ; pagination `StartIndex`/`Limit`, `ProviderIds`, `SeriesId`, `UserData` rendus par Ether. Historique lu via `state.ExportEntries`, titres via le miroir local du catalogue (aucun appel TMDB) ;
- `Items?AnyProviderIdEquals=Tmdb.123` (et `Imdb.tt…`) : absent d'Ether, ajouté ;
- `POST Users/{id}/Items/{id}/UserData` (`Rating` 0–10, `Played`, `IsFavorite`) : absent d'Ether, ajouté ;
- `PlayedItems` (vu/non vu), `System/Info`, `Users/{id}` : déjà fournis par Ether.

Non couvert : `Library/Refresh` (Scrob ignore l'échec), la connexion Plex de Scrob (utiliser la connexion Jellyfin).

## 7. Musique — ✅

- Choix de départ : pas de catalogue MusicBrainz, et d’abord une **API Subsonic** (`internal/pse/music`, montée sous `/rest/`), le protocole que gèrent les clients musique auto-hébergés. Les bibliothèques Musique de Plex et de Jellyfin ont été ajoutées ensuite, sur la même bibliothèque (voir plus bas).
- Source par défaut, **sans compte, comme Remux** : catalogue de l'API publique Deezer, lecture par un **addon Eclipse** (`PSE_MUSIC_ECLIPSE_URL`, lien `manifest.json` ; `GET /search?q=artiste titre` puis `GET /stream/{id}` → `{url, quality}`). Le meilleur résultat est choisi sur le titre, l'artiste et la durée ; plusieurs addons sont essayés dans l'ordre ; l'URL résolue est gardée 10 min (déplacements dans le morceau).
- **Lecture AllDebrid** (`PSE_MUSIC_ALLDEBRID=on`, sans DroppedNeedle ni stockage) : au lancement d'un titre, l'album (titre nettoyé des mentions d'édition) est cherché en catégorie audio 3000 sur Prowlarr (un appel) ou sur les indexeurs torrent d'Ether en Torznab ; les résultats qui nomment l'album et l'artiste passent en premier (FLAC, sources, discographies en dernier), un lien `.torrent` sans empreinte est lu en mémoire. Jusqu'à 6 torrents sont ouverts sur AllDebrid en mode cache seul (`debrid.Provider.Acquire`, `CachedOnly`, `RemoveUncached`) ; le fichier du titre est choisi sur son nom et son numéro (et disque), débridé et relayé. Fichiers d'album gardés 6 h en mémoire, absence confirmée 30 min, lien 50 min ; une panne (indexeurs, compte, limite de requêtes) n'est pas une absence : nouvel essai après 15 s, doublé à chaque échec, 5 min au plus. Si le titre manque dans le torrent ouvert, les candidats suivants sont vérifiés (dans la limite des 6). Real-Debrid et TorBox suivent le même chemin ; plusieurs comptes d'un même service sont essayés dans l'ordre de priorité, chacun avec ses caches. Eclipse, s'il est réglé, est essayé d'abord (`music.Resolvers`). Le bouton **Tester** du compte cherche un lien sur des albums du moment sans vérifier l'audio ; **Tester les sources** vérifie les premiers octets.
- Option avancée : une **HiFi API**, utilisée seulement quand aucune autre source n'est active, jamais en repli (`PSE_MUSIC_HIFI_URL`, format `/search/`, `/album/`, `/artist/`, `/info/`, `/track/` avec manifeste base64).
- Bibliothèque par utilisateur (table `pse_music_items`, purgée et fusionnée avec le compte) : favoris et écoutes, avec un instantané des métadonnées. `search3` vide renvoie la bibliothèque (synchronisation de Symfonium).
- Lecture : manifeste JSON → URL directe relayée avec prise en charge de `Range`. Manifeste DASH → repli sur la qualité inférieure.
- Authentification : comptes locaux d'Ether (`u`/`p` ou `enc:`), cache de 10 min, blocage après 10 échecs en 15 min. Jeton `t`/`s` refusé : les mots de passe sont hachés.
- Testé contre Deezer (réel) et un addon Eclipse simulé ; lecture réelle à valider avec le lien manifest de l'utilisateur.
- **Clients Jellyfin** (Jellyfin web, Finamp, Findroid, Streamyfin…, Emby aussi) : une bibliothèque **Musique** (`CollectionType: music`) à côté de celles d'Ether. Elle montre d'abord les favoris et écoutes du spectateur, puis les classements Deezer pour ne jamais être vide ; la recherche interroge tout le catalogue. Albums, artistes (`/Artists`, `/Artists/AlbumArtists`), titres, pochettes, favoris, lecture directe (`PlaybackInfo`, `/Audio/{id}/stream…`, sans transcodage) et écoutes (`/Sessions/Playing/Stopped` après 30 s) partagent la bibliothèque personnelle de l'API Subsonic. Les identifiants musique ont leur propre espace de GUID (`50534d55…`) ; une couche placée devant le routeur d'Ether (`internal/frontends/mediabrowser/pse_music.go`) ne prend que les requêtes qui visent la musique et les authentifie elle-même, tout le reste va à Ether sans changement.
- **Clients Plex** (Plexamp, Plex Web, applications Plex) : une section **Musique** (`type="artist"`, numéro 5053) au même contenu. Artistes, albums, titres (`/library/sections/5053/all?type=8|9|10`), hubs de section (écoutés récemment, albums favoris, albums, artistes et titres populaires), recherche (`/hubs/search` avec `sectionId=5053`), pochettes (`/library/metadata/{clé}/thumb`, `/photo/:/transcode`), lecture directe (`/library/parts/{clé}/0/file.flac`, `/music/:/transcode/universal/decision` répond « lecture directe »), files de lecture musicales (`/playQueues`), écoutes (`/:/timeline` arrêté après 30 s, `/:/scrobble`) et favoris (`/:/rate`). Les ratingKey musique utilisent des genres absents d'Ether (50 à 52 × 10^15). Un middleware placé après l'authentification d'Ether (`internal/plex/pse_music.go`) ne prend que les requêtes qui visent la musique.
- Les deux frontaux atteignent la musique par `internal/frontend/pse_music.go` (une interface publiée par l'application) : ils n'importent jamais `internal/pse/music` et les frontières de dépendances d'Ether sont respectées.
- Recherche globale de Plex (`/hubs/search` sans section, `/library/search` quand `searchTypes` est vide ou contient `music`) : les hubs Artists, Albums et Tracks, que PMS renvoie déjà vides, sont remplis ; un titre exact ou un début de titre fait passer son hub en tête, comme Ether classe les titres. Les recherches du catalogue tournent en parallèle, bornées à 2,5 s et gardées 2 minutes : elles ne ralentissent jamais la recherche d'Ether.
- **Playlists Deezer et Last.fm** (`internal/pse/music/playlists*.go`, tables `pse_music_accounts`, `pse_music_playlists`, `pse_music_matches`) : par spectateur, un profil Deezer (playlists publiques via `/user/{id}/playlists`, dont Coups de cœur) ou des playlists Deezer, et un compte Last.fm (`user.getLovedTracks`, `user.getTopTracks` du mois et de tous les temps, avec `PSE_LASTFM_API_KEY`). Les titres Last.fm sont retrouvés dans Deezer (artiste et titre, mentions « Remastered », « Live »… tolérées) et la correspondance est gardée en base ; une absence est réessayée après une semaine. Appels Deezer espacés de 150 ms. Réimport toutes les `PSE_MUSIC_PLAYLISTS_HOURS` (6 h), et à la demande depuis la console. Servies en lecture seule : Subsonic (`getPlaylists`, `getPlaylist`), Jellyfin (GUID musique de type 4, `/Playlists/{id}[/Items]`, `/Items?ParentId=`, ajoutées à la liste d'Ether), Plex (ratingKey 53 × 10^15, `/playlists?playlistType=audio`, `/playlists/{clé}/items`, pochette composite, files de lecture par `playlistID`, ajoutées à la liste sans type d'Ether). Spotify est disponible pour l’import de copies personnelles avec une application développeur et OAuth PKCE par spectateur, dans les limites de son API.
- Non couverts : musique sur l'accueil de Plex (Jellyfin l'affiche via `Items/Latest`), playlists créées depuis les clients, paroles, transcodage.

## 8. Catalogues Stremio en collections — ✅ fait

- Ether sait déjà faire d'un catalogue d'addon Stremio la source d'une collection (`plugin:<addon>/<catalogue>`, rôle `list_source`), une à une. PSE ajoute l'import groupé (`web/src/pse/AddonCatalogImport.tsx`) : une collection publiée par catalogue films ou séries coché, triée dans l'ordre de l'addon, limitée à N titres (100 par défaut).
- Cadence libre `Nh` (1 à 168) en plus de `live`, `hourly`, `daily` et `weekly`, acceptée par la validation d'Ether et lue par sa reconstruction en arrière-plan (`internal/collections/pse_refresh.go`). Toute valeur inconnue garde le comportement d'Ether.

## Ordre proposé

1 → 3 → 4 → 5 → 2 → 6 → 7 → 8. Le catalogue français (2) gagne en précision une fois la base de releases (3–4) alimentée.
