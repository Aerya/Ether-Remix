# Règles du fork PSE

« Ether-Remix » est le nom public du fork. Le code garde le préfixe historique `pse` (paquets, variables `PSE_*`, marqueurs `PSE-HOOK`) pour ne casser ni les configurations ni la synchronisation.

Objectif : intégrer chaque jour Ether upstream **sans casser les ajouts de PSE**, et sans résolution de conflit automatique sur le code.

## Où placer le code

| Type | Emplacement propre au fork |
| --- | --- |
| Go | `internal/pse/<fonction>/` (un paquet par ajout) |
| Go importé par un frontend | `internal/frontend/kit/<fonction>/` : `TestFrontendImports` interdit aux frontends tout autre paquet interne (ex. `scrobview`) |
| Interface web | `web/src/pse/` |
| Traductions PSE | `web/src/pse/i18n/` (catalogues complémentaires, jamais dans `web/src/i18n/locales/*.json`) |
| Workflows | `.github/workflows/pse-*.yml`, fichiers annexes dans `.github/pse/` |
| Documentation | `README.md`, `README.fr.md`, `docs/pse/` |

Les fichiers d'Ether ne sont modifiés que pour des **points d'accroche** : quelques lignes qui appellent le code PSE (enregistrement d'une route, d'un service, d'un filtre de catalogue…). Chaque point d'accroche est marqué :

```go
// PSE-HOOK: <nom de l'ajout> — voir docs/pse/FORK.fr.md
```

`git grep PSE-HOOK` liste ainsi tout ce qui peut entrer en conflit avec upstream. Points d'accroche actuels :

| Fichier d'Ether | Ajout |
| --- | --- |
| `internal/notify/notify.go` (`deliver`) | notifications FR (`internal/notify/pse_localize.go`) |
| `internal/app/build_integrations.go` | indexeur de releases (`internal/app/pse_indexer.go`), API Subsonic (`internal/app/pse_music.go`) |
| `internal/catalog/browse.go` (`browseWhere`) | catalogue lisible uniquement et en français (`internal/catalog/pse_hooks.go`) |
| `internal/app/build_personalization.go` (`FilterAvailable`), `internal/app/collections.go` (`collectionOptions`), `internal/catalog/lists.go` (`Similar`), `internal/collections/collections.go` (`serve` renommé `serveMembers`, franchises) | filtre français et lisible des rangées d'accueil, des collections et des titres similaires (`internal/catalog/pse_discovery.go`, `internal/collections/pse_keys.go`) |
| `internal/catalog/browse_tally.go` (`tallyTotal`) | décompte précalculé ignoré quand les filtres Ether-Remix s'appliquent (`pseFiltersCatalog`) |
| `internal/core/interfaces.go` (`Filter.PSEUnplayable`, `Filter.PSEThemed`) | parcours de l'indexeur PSE hors catalogue « lisible uniquement » (`internal/app/pse_indexer.go`) ; filtre VF des bibliothèques à thème malgré `IncludeKeys` (`internal/catalog/pse_hooks.go`) |
| `internal/catalog/sync.go` (tâche de fond) | dates de sortie pour le catalogue en français (`internal/catalog/pse_hooks.go`) |
| `internal/frontends/mediabrowser/browse.go` (vues, `/Items`, dossier) | bibliothèques Vus et notés, `AnyProviderIdEquals` (`internal/frontends/mediabrowser/pse_scrob.go`) |
| `internal/frontends/mediabrowser/frontend.go` (`Handler`, routeur enveloppé) | bibliothèque Musique des clients Jellyfin (`internal/frontends/mediabrowser/pse_music.go`, `internal/pse/music/jellyfin.go`) |
| `internal/frontends/plex/routes.go` (middleware après `authenticate`), `internal/frontends/plex/handlers_library.go` (`handleSections`), `internal/frontends/plex/handlers_server.go` (`handleProviders`), `internal/frontends/plex/handlers_hubs.go` (`handleHubSearch`, `handleLibrarySearch`) | section Musique des clients Plex (`internal/frontends/plex/pse_music.go`, `internal/pse/music/plex.go`) ; liaison sans import de `internal/pse` : `internal/frontend/pse_music.go` |
| `internal/frontends/plex/playlist_queries.go` (`writePlaylistListing`), `internal/frontends/mediabrowser/playlists.go` (`writePlaylists`) | playlists Deezer et Last.fm ajoutées aux playlists d'Ether (`internal/frontends/plex/pse_music.go`, `internal/frontends/mediabrowser/pse_music.go`, `internal/pse/music/playlists*.go`) |
| `internal/frontends/mediabrowser/userdata.go` (`mutationRoutes`) | `POST …/UserData` (`internal/frontends/mediabrowser/pse_scrob.go`) |
| `internal/app/extensions.go` (`extensionHandlers`) | API de la page PSE `/ether/api/pse/` (`internal/app/pse_console.go`, `internal/pse/console`) |
| `internal/app/extend_builtins.go` | entrée « PSE » du menu (`internal/app/pse_descriptors.go`) |
| `web/src/App.tsx` (import, `BUILTIN_PAGES` avec onglets `/:tab?`, icône `Languages` dans l'import lucide et `NAV_ICONS`) | page Ether-Remix (`web/src/pse/PsePage.tsx`), en tête du menu sous la Vue d'ensemble (`internal/app/pse_descriptors.go`) |
| `web/src/App.tsx` (pied de la barre latérale) | numéro de version cliquable vers le dépôt GitHub (`web/src/pse/VersionLink.tsx`) |
| `web/src/pages/Home.tsx` (import, colonne de `ActivityPanel`) | bloc Musique de la Vue d'ensemble (`web/src/pse/MusicActivity.tsx`, `internal/pse/music/plays.go`) |
| `web/src/pages/settings/ConnectedServices.tsx` (import, groupe « Suivi du visionnage ») | carte Scrob et son panneau latéral (`web/src/pse/ScrobCard.tsx`) ; envoi des lectures : `internal/pse/scrobpush`, `internal/app/pse_scrob.go` |
| `web/src/pages/sources/Sources.tsx` (onglet, routage de la galerie) et `web/src/pages/sources/Indexers.tsx` (prop `view`, liste filtrée, ordre fusionné) | onglet Stremio de Sources (`web/src/pse/stremioTab.ts`) ; test Ether adapté : `web/src/test/indexer-stremio.test.tsx` |
| `internal/collections/builtins.go` (`refreshInterval`), `internal/collections/spec.go` (validation de `refresh`), `web/src/lib/contract.ts` (type `Refresh`) | cadence libre « Nh » des collections, de 1 à 168 h (`internal/collections/pse_refresh.go`, `web/src/pse/refresh.ts`) |
| `web/src/pages/collections/SettingsPanels.tsx` (champ Actualiser), `web/src/pages/collections/Editor.tsx` et `web/src/pages/collections/CollectionDetail.tsx` (libellé de cadence) | choix « Toutes les N h » (`web/src/pse/RefreshField.tsx`) |
| `web/src/pages/collections/Gallery.tsx` (import, en-tête) | bouton « Importer un addon » : une collection par catalogue d'addon Stremio (`web/src/pse/AddonCatalogImport.tsx`) |
| `internal/frontends/plex/server.go` (`sectionByID`, `canSeeSection`), `internal/frontends/plex/handlers_library.go` (`handleSections`, `parseBrowse`), `internal/frontends/plex/handlers_server.go` (`handleProviders`), `internal/frontends/plex/handlers_hubs.go` (`buildSectionHubs`), `internal/frontends/plex/handlers_metadata.go` (`sectionCollections`) | bibliothèques à thème des clients Plex, sections 5101 à 5199 (`internal/frontends/plex/pse_themes.go`, `internal/pse/themes`, `internal/collections/pse_keys.go`) ; liaison sans import de `internal/pse` : `internal/frontend/pse_themes.go` ; titres prioritaires de l'indexeur : `internal/app/pse_themes.go` |
| `internal/frontends/plex/convert.go` (`metadata`, résumé), `internal/frontends/mediabrowser/browse.go` (`itemDTO`) et `jellyfin_browse.go` (`Overview`), `internal/overlays/presentation.go` (badges) | titres risqués affichés avec moins de releases que le minimum : « ⚠ 1 source » dans le résumé et badge « 1 SOURCE » (`internal/pse/releasedb/risk.go`) ; liaison sans import de `internal/pse` : `internal/frontend/pse_risk.go` |
| `internal/artwork/providers.go` (`GetPoster`, `posterVersion`), `internal/frontends/plex/discovery_features.go` (`serveDiscoveryPoster`), `internal/frontends/plex/images.go` (`serveItemImage`), `internal/artwork/service/read.go` (`readEntry`), `internal/frontends/mediabrowser/frontend.go` (`Handler`) et `images.go` (`image`) | affiches plus rapides : témoin « vient du fournisseur », renouvellement réglable (`PSE_POSTER_REFRESH_HOURS`) et redimensionnement gardé en mémoire (`internal/artwork/pse_posters.go`, alias `internal/frontend/pse_posters.go`) ; en-tête de cache et taille des affiches TMDB côté Plex (`internal/frontends/plex/pse_posters.go`) ; taille demandée et images sans attente d'empreinte côté Jellyfin/Emby (`internal/frontends/mediabrowser/pse_posters.go`) ; choix de largeur (`internal/frontend/kit/images/pse_size.go`) |
| `internal/catalog/pse_hooks.go` (`pseCatalogWhere`), `internal/catalog/pse_discovery.go`, `internal/collections/pse_keys.go` (`serve`), `internal/app/collections.go` (`WithPSEDiscovery`), `internal/frontends/plex/pse_themes.go` (`pseThemeFilter`) | exceptions de langue (VOSTFR, Toutes langues, collections d'exception, repli SeerrCatalog) limitées à leur bibliothèque ou collection : `core.Filter.PSELangFree`, `core.WithPSELangFree` |
| `internal/frontends/plex/handlers_library.go` (`metaType`, `applySortParam`), `internal/frontends/mediabrowser/browse.go` (`items`), `internal/frontends/mediabrowser/preferences.go` (`writeDisplayPreferences`) | tri par défaut par date de sortie (`PSE_LIBRARY_SORT`) ; `internal/frontends/plex/pse_sort_test.go` fixe `title` pour les tests d'Ether |
| `internal/app/pse_seerrcatalog.go` (`Changed`), `internal/app/pse_themes.go` (`runPSELangExempt`) | collections SeerrCatalog et SeerrCatalog · VOSTFR (`internal/pse/themes/seerr.go`) |
| `internal/debrid/direct.go` (`DirectPayload`, après le contrôle « lecture seulement »), `internal/core/local_download.go` (`LocalDownloadMessage`), `internal/app/build_sources.go` (`Backend` du gestionnaire d'indexeurs), `internal/app/stremio_addons.go` (`addonIndexers`) | source XDCC via XDCC Grabscher : préparation de la release avant la sonde (`internal/debrid/pse_xdcc.go`), messages au spectateur et contrôle de disponibilité (`internal/core/pse_ondemand.go`), indexeur virtuel et API de la console (`internal/app/pse_xdcc.go`, `internal/pse/xdcc`) |
| `internal/app/build_sources.go`, `internal/app/stremio_addons.go`, `internal/debrid/pse_xdcc.go`, `internal/pse/releasedb/{freshness,indexer}.go`, `internal/catalog/pse_tier_sources.go` | Source FTP / SFTP : indexeur virtuel, registre de préparation avant sonde partagé avec XDCC, disponibilité après copie et conservation des tables du fork. Configuration et API : `internal/app/pse_ftp.go`, `internal/pse/ftp`, `internal/pse/console/fields_ftp.go`. |
| `.github/workflows/pr-image-build.yml` (`paths-ignore`) | pas d'image d'aperçu pour une PR qui ne touche que la documentation (`*.md`, `docs/`, `LICENSE`) ; `pr-image-publish.yml` ne tourne qu'après un build réussi, il est donc ignoré aussi |

Les réglages PSE passent tous par `internal/pse/pseenv` : la valeur enregistrée dans la console (table `pse_settings`) l'emporte sur la variable d'environnement. Un nouvel ajout lit ses réglages avec `pseenv.Get` et les déclare dans `internal/pse/console/fields.go`.

## Synchronisation automatique

Chaque jour à 05:17 UTC, le workflow compare Ether à la révision publique enregistrée dans `.github/pse/upstream-base`. Il fusionne à trois voies le fork et Ether depuis cette révision, sans importer l'ancien historique privé ni ajouter les commits Ether comme parents. La fusion passe par des commits temporaires qui partagent cette révision : Git suit ainsi les fichiers et dossiers déplacés par Ether. Un fichier Remix ajouté dans un dossier qu'Ether déplace suit ce dossier, et une modification Remix d'un fichier déplacé est reportée au nouvel emplacement.

Les trois README, `.github/`, `docs/pse/`, `internal/pse/` et `web/src/pse/` restent propres à Ether-Remix. Les changements des workflows Ether nécessitent une revue manuelle. L'interface embarquée est reconstruite depuis les sources fusionnées.

En cas de conflit, une issue est ouverte et main reste inchangée. Elle liste les fichiers en conflit, le type de chaque conflit (contenu, fichier supprimé par Ether mais modifié par Remix, etc.) et le lien vers l'exécution. Sinon, une branche temporaire est publiée, puis les contrôles Go et web s'exécutent sur son commit exact. Seul ce commit validé peut être intégré ; si main a avancé entre-temps, la publication est refusée. Les images amd64 et arm64 sont ensuite reconstruites.

Le `GITHUB_TOKEN` suffit : aucun jeton personnel ni secret de synchronisation n'est requis, car les workflows upstream sont exclus du delta.

## Résolution manuelle d'un conflit

Un conflit empêche volontairement la fusion ; relancer la même révision ne le résout pas. Intégrer le delta complet, conserver les ajouts du fork, puis avancer `upstream-base` uniquement avec la résolution validée. Les compilations et tests restent nécessaires même si Git ne signale plus de conflit : une interface déplacée peut demander d’adapter les raccordements Remix.

Depuis un clone propre :

```bash
git fetch origin
git fetch https://github.com/braedonsaunders/ether.git main
upstream_sha=$(git rev-parse FETCH_HEAD)
git switch -c sync/manuel origin/main
python3 .github/pse/apply-upstream.py "$upstream_sha"
# En cas de conflit (code 2), les fichiers contiennent les marqueurs <<<<<<< ||||||| >>>>>>>.
# Les résoudre en préservant les ajouts Ether-Remix ; git diff --name-only --diff-filter=U les liste.
git add -A
printf '%s\n' "$upstream_sha" > .github/pse/upstream-base
git add .github/pse/upstream-base
cd web && pnpm install --frozen-lockfile && pnpm build && cd ..
git add -A internal/admin/dist
git commit -m "Synchroniser Ether et reconstruire l’interface"
git push origin sync/manuel
# Ouvrir une PR et attendre les contrôles avant fusion.
```

## Workflows d'Ether désactivés

`ci.yml`, `api-drift.yml`, `plex-parity.yml`, `reference-drift.yml`, `refresh-update-feed.yml` et `release.yml` restent présents (pour éviter les conflits) mais sont désactivés dans les réglages Actions du dépôt : ils publient les releases d'Ether ou consomment beaucoup de minutes (matrice Windows/macOS) sur un dépôt privé.

## Fichiers Markdown

Le `.gitignore` d'Ether ignore les fichiers `*.md` non listés. Ajouter une nouvelle documentation PSE avec `git add -f`.

## Navigation Remix et synchronisation

La navigation et les catégories résident dans `web/src/pse/navigation.tsx`, `RemixSources.tsx` et `PsePage.tsx`. Les composants natifs Sources sont importés avec leurs API/configurations existantes. Les pages Sources et Collections d’Ether restent identiques à upstream ; l’import de catalogues se fait depuis Contenus et bibliothèques. Les anciennes routes Sources servent de redirections.

Le script de synchronisation fusionne les conflits de traductions JSON par clé uniquement lorsque les modifications sont indépendantes. Deux traductions différentes du même message restent un conflit bloquant ; le point de référence n’avance pas. L’interface et les catalogues sont régénérés avant les contrôles. Les consignes `AGENTS.md`, les fichiers Remix et les workflows restent protégés. Le retard historique de 775 commits a été intégré depuis `6d9bb9f17adb661540fdf292fee42ac0bb8ac353`.

L’adaptateur `internal/catalog/pse_tier_sources.go` classe les tables du fork pour la migration de catalogue d’Ether : les exceptions de langue restent protégées, les caches d’indexation ne retiennent pas tout le catalogue. Cet adaptateur est protégé du delta automatique.

### Blocs repliables des sources

Les onglets Stremio, Indexeurs et Indexation activent le contexte `CollapsibleSections` pour leurs blocs de réglages et listes. Chaque titre est un `summary` natif, utilisable à la souris et au clavier. Le contenu reste monté pour conserver les saisies. Les nouveaux blocs sont ouverts par défaut ; StreamFusion et Priorité de recherche conservent leur ouverture manuelle. Les autres onglets gardent leur présentation habituelle.

## Recherche déléguée SeerrCatalog

La console regroupe les deux fonctions sous **Sources → Stremio → SeerrCatalog** : **Disponibilités vers Remix** pour importer les contrôles, **Recherche via Remix** pour autoriser les recherches. Les clés et activations restent indépendantes. Le premier test se lance dans Remix ; le second depuis **SeerrCatalog → Paramètres → Intégration Ether Remix → Recherche via Remix**. Les identifiants de configuration existants sont conservés.

L’import des disponibilités accepte une fiche déjà enregistrée même si elle est masquée par le catalogue (titre à venir ou adulte). Cela évite qu’un seul titre bloque tout le lot, sans modifier les filtres de visibilité ni les règles de lecture. Une fiche réellement absente ou une erreur de stockage continue de préserver le dernier lot valide.

`GET /api/integrations/seerrcatalog/v1/search` teste la disponibilité du connecteur ; `POST` reçoit `{media: {type, tmdbId, imdbId, title, year}, season, episode}` et renvoie `{schema: "seerrcatalog.search.v1", streams, conclusive, ignored}`. Les deux utilisent `Authorization: Bearer <clé dédiée aux recherches>` et exigent `PSE_SEERRCATALOG_SEARCH=on` avec `PSE_SEERRCATALOG_SEARCH_TOKEN` (32 caractères minimum). Ils sont indépendants de `PSE_SEERRCATALOG`, de son URL et de sa clé de partage. Après mise à jour des deux applications, renseigner la nouvelle clé : aucun repli vers l’ancienne clé de partage n’est autorisé. Deux recherches simultanées au maximum ; délai de 75 secondes et budgets d’indexation existants. La réponse ne transforme ni lien NZB ni lien de téléchargement torrent en vidéo. Le champ `ignored` compte les releases sans descripteur réutilisable.

La recherche appelle les indexeurs configurés, avec vérification du titre/année ou de ses identifiants, et de l’épisode pour les séries. Les résultats SeerrCatalog retenus sont explicitement exclus de cette requête afin d’éviter une boucle de validation. Le client conserve ses critères de langue et décide de la disponibilité. Aucune collection ni exception globale de langue n’est créée. Un résultat incomplet doit être retenté, sans conclure à l’absence de sources.

## Musique dans Activité

Les observations musicales sont ajoutées à `/status/sessions`, utilisé par Activité → En cours et la Vue d’ensemble. Les timelines Plex et les rapports Jellyfin/Emby transmettent lecture, pause, position et arrêt ; Subsonic transmet le début via `scrobble?submission=false`. Les sessions sont isolées par spectateur, protocole et appareil. Les jetons et URL audio ne sont pas conservés. Aucun bouton d’arrêt vidéo n’est proposé pour ces observations musicales.

Les sessions Plex/Jellyfin/Emby expirent après deux minutes sans rapport. Subsonic n’ayant généralement pas de signal d’arrêt, la session expire après la durée du morceau plus trente secondes (cinq minutes si elle est inconnue). Sans signal de lecture du client, aucune session n’est inventée à partir d’un téléchargement ou d’un préchargement. Les observations en mémoire disparaissent au redémarrage ou à la reconfiguration de la source.

Activité → Historique présente aussi les lignes existantes de `pse_music_plays` dans une section musicale dédiée, via l’API propriétaire `/pse/music/history`. Période, année et spectateur filtrent cette section, paginée par cent écoutes. La durée affichée est celle du morceau, pas une mesure de temps écouté ; les indicateurs vidéo restent séparés. Les soumissions Subsonic alimentent l’historique ; Plex et Jellyfin/Emby le font à l’arrêt après au moins trente secondes, comme auparavant. Une simple notification de début ne crée pas d’historique.

Points d’accroche : `internal/app/admin.go`, `internal/app/pse_console.go`, `internal/frontend/pse_music.go`, `internal/frontends/mediabrowser/pse_music.go`, `web/src/lib/health.ts`, `web/src/components/SessionArtwork.tsx`, `web/src/pages/activity/Activity.tsx` et `NowPlaying.tsx`.

## Sélection Concerts

Le modèle Concerts exige le genre TMDB Musique (10402) et au moins un mot-clé de captation : `concert film` (156205), `music concert` (304615) ou `live concert` (318730). Le mot-clé générique `concert` (6029), qui peut décrire une scène de fiction, est retiré. Les sorties vidéo sont incluses. La pertinence prime sur l’exhaustivité : les captations mal renseignées peuvent manquer, et les erreurs de mots-clés TMDB restent possibles.

Au démarrage, les trois anciennes requêtes standard des collections créées par le modèle sont remplacées et leurs membres reconstruits en arrière-plan. Identifiants, noms et autres réglages sont conservés, y compris après renommage. Une requête personnalisée ou une collection personnelle n’est pas migrée. Les titres disparaissent de Concerts après reconstruction ; aucun média n’est supprimé.

Médias possédés : `internal/pse/localmedia` confine les ouvertures aux racines autorisées ; `pse_owned_media.go` publie les collections/bibliothèques et pilote les scans. Le catalogue conserve les clés locales et les expose sans recherche distante ; resolver/playback délèguent au fichier local. `music/owned.go` partage les métadonnées sans fusionner les historiques des spectateurs ; `music/ibroadcast.go` gère OAuth et les liens temporaires. Les workflows de synchronisation et la version du pied de menu sont inchangés.

La révision Ether `16053c6572ff7c53d4ad5452458e4ec9af6573a4` est intégrée : le catalogue conserve les contrôles des médias locaux, les vues Scrob coexistent avec les collections Jellyfin/Emby, le résolveur conserve SubMaker et les profils de qualité en complément des contrôles d’identité des épisodes. La génération des identifiants Jellyfin/Emby conserve le champ `userId` nécessaire à Scrob.

Les collections Jellyfin/Emby utilisent le bit 60 pour leur vue virtuelle dans Remix. Le bit 61 + 1 et + 2 reste réservé aux vues Scrob existantes ; ne pas reprendre la constante upstream sans vérifier ces collisions. Le test `TestPSEScrobViewIDsDoNotCollideWithEther` protège cette séparation.

## Configuration rapide JukeBox

`internal/pse/jukebox` fournit les trois listes de démarrage, le catalogue public borné et mis en cache, et la création réentrante des collections/bibliothèques. `internal/app/pse_console.go` branche `/pse/jukebox/` sur l’API propriétaire existante. Le composant `web/src/pse/JukeboxQuickSetup.tsx` est accessible depuis Contenus et Bibliothèques ; le point d’accroche dans `web/src/pages/libraries/Libraries.tsx` le propose aussi dans les bibliothèques communes à tous les protocoles. Aucune modification des sources de lecture ni des règles de langue.
