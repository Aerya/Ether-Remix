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
| `internal/frontends/mediabrowser/browse.go` (`collectionFolder`, `writeCollectionFolder`, `search`, `items`), `internal/frontends/mediabrowser/jellyfin_browse.go` (`attachLibraryParent`, `virtualFolders`, `libraryCollageGrant`), `internal/frontends/mediabrowser/page_cache.go` (`browseCacheKey`) | navigation, collections et recherche dans les bibliothèques Remix Jellyfin, avec présentation native des vues mixtes et invalidation du cache après modification. La liste des vues ne compte pas les titres Remix ; la recherche d'une vue Remix tient dans `pseJellyfinSearch` (`internal/frontends/mediabrowser/pse_jellyfin_themes.go`) |
| `internal/frontends/mediabrowser/frontend.go` (`Handler`, routeur enveloppé) | bibliothèque Musique des clients Jellyfin (`internal/frontends/mediabrowser/pse_music.go`, `internal/pse/music/jellyfin.go`) |
| `internal/frontends/plex/routes.go` (middleware après `authenticate`), `internal/frontends/plex/handlers_library.go` (`handleSections`), `internal/frontends/plex/handlers_server.go` (`handleProviders`), `internal/frontends/plex/handlers_hubs.go` (`handleHubSearch`, `handleLibrarySearch`) | section Musique des clients Plex (`internal/frontends/plex/pse_music.go`, `internal/pse/music/plex.go`) ; liaison sans import de `internal/pse` : `internal/frontend/pse_music.go` |
| `internal/frontends/plex/playlist_queries.go` (`writePlaylistListing`), `internal/frontends/mediabrowser/playlists.go` (`writePlaylists`) | playlists Deezer et Last.fm ajoutées aux playlists d'Ether (`internal/frontends/plex/pse_music.go`, `internal/frontends/mediabrowser/pse_music.go`, `internal/pse/music/playlists*.go`) |
| `internal/frontends/mediabrowser/userdata.go` (`mutationRoutes`) | `POST …/UserData` (`internal/frontends/mediabrowser/pse_scrob.go`) |
| `internal/app/extensions.go` (`extensionHandlers`) | API de la page PSE `/ether/api/pse/` (`internal/app/pse_console.go`, `internal/pse/console`) |
| `internal/app/extend_builtins.go` | entrées du groupe « Remix » du menu (`internal/app/pse_descriptors.go`) |
| `web/src/App.tsx` (import, `BUILTIN_PAGES` avec onglets `/:tab?`, icône `Languages` dans l'import lucide et `NAV_ICONS`) | page Ether-Remix (`web/src/pse/PsePage.tsx`), dans le groupe « Remix » du menu (`internal/app/pse_descriptors.go`) |
| `web/src/App.tsx` (groupe `remix` de `NAV_GROUPS`, `buildNavigation`, `linkRender`, `NAV_ICONS`, filtre `filterRemixNav`), `internal/app/pse_descriptors.go` | groupe « Remix » de la barre latérale, au-dessus de Bibliothèque : entrées déclarées dans `internal/app/pse_descriptors.go`, libellés bilingues et filtre simple / expert dans `web/src/pse/navigation.tsx` et `web/src/pse/RemixNavigation.tsx` ; un test vérifie que les listes Go et TypeScript concordent |
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
| `internal/frontends/plex/home_cache.go` (`homeEntry.psePolicy`, `serveHome`, `storeHome`), `internal/frontends/plex/home_cache_test.go` (un test d'Ether) | écrans immédiats (`PSE_INSTANT_SCREENS`) : un écran Plex déjà construit est servi tout de suite puis reconstruit en arrière-plan, même après un visionnage, un changement de disposition ou au-delà de 30 min (jusqu'à 12 h) ; jamais pour un spectateur restreint ni après un changement de droits ou de réglages (`internal/frontends/plex/pse_instant.go`, `internal/frontend/kit/instantscreens`) ; `home_cache_test.go` règle `off` pour le test du contrat d'Ether |
| `internal/catalog/get.go` (`Get`, `Grandchildren`), `internal/catalog/year_correct.go` (`kickMovieYears`), `internal/catalog/browse.go` (`Browse`, appels de `kickMovieYears`), `internal/catalog/mirror_titles.go` (`titles` : réalisateurs seuls), `internal/collections/collections.go` (`build` : construction toujours complète, `internal/collections/pse_listonly.go`), `internal/frontends/plex/handlers_library.go` (`sectionList`), `internal/frontends/plex/handlers_metadata.go` (`handleCollectionItems`), `internal/frontends/plex/home_cache.go` (`serveHome`, `homeBuildRequest`, `warmKey`), `internal/frontends/plex/handlers_hubs.go` (`warmBrowseLink`, `buildSectionHubs` : « Ajouts récents » sans comptage du catalogue), `internal/frontends/plex/convert.go` (`renderTitles`, `metadata`), `internal/frontends/mediabrowser/browse.go` (`items`) et `internal/frontends/mediabrowser/library_activity.go` (`libraryActivity`), `internal/frontends/stremio/catalog.go` (`catalog`), `internal/frontends/plex/enrich_test.go` (deux tests d'Ether) | listes légères (`PSE_LIGHT_LISTS`) : une bibliothèque, une rangée, une collection ou un catalogue ne lit que la base, sans fiche TMDB, écriture, préchauffage ni appel debrid ; marqueur `internal/pse/listonly`, alias `internal/frontend/pse_listonly.go`, lecture seule du catalogue `internal/catalog/pse_listonly.go`, Plex `internal/frontends/plex/pse_listonly.go`, Jellyfin/Emby `internal/frontends/mediabrowser/pse_listonly.go` ; `internal/frontends/plex/enrich_test.go` règle `off` pour les deux tests du comportement d'Ether |
| `internal/app/pse_seerrcatalog.go` (`Changed`), `internal/app/pse_themes.go` (`runPSELangExempt`) | collections SeerrCatalog et SeerrCatalog · VOSTFR (`internal/pse/themes/seerr.go`) |
| `internal/debrid/direct.go` (`DirectPayload`, après le contrôle « lecture seulement »), `internal/core/local_download.go` (`LocalDownloadMessage`), `internal/app/build_sources.go` (`Backend` du gestionnaire d'indexeurs), `internal/app/stremio_addons.go` (`addonIndexers`) | source XDCC via XDCC Grabscher : préparation de la release avant la sonde (`internal/debrid/pse_xdcc.go`), messages au spectateur et contrôle de disponibilité (`internal/core/pse_ondemand.go`), indexeur virtuel et API de la console (`internal/app/pse_xdcc.go`, `internal/pse/xdcc`) |
| `internal/app/build_sources.go`, `internal/app/stremio_addons.go`, `internal/debrid/pse_xdcc.go`, `internal/pse/releasedb/{freshness,indexer}.go`, `internal/catalog/pse_tier_sources.go` | Source FTP / SFTP : indexeur virtuel, registre de préparation avant sonde partagé avec XDCC, disponibilité après copie et conservation des tables du fork. Configuration et API : `internal/app/pse_ftp.go`, `internal/pse/ftp`, `internal/pse/console/fields_ftp.go`. |
| `internal/frontends/plex/playback.go` (`writePartErr`, `writeStartErr`) | limite d'indexeur ou quota debrid : la part, `start.m3u8` et `start.mpd` répondent 404 sans corps ni `Retry-After`, au lieu de 502, pour que Plezy affiche « fichier indisponible » (`internal/frontends/plex/pse_playback_status.go`) |
| `internal/newznab/rate_limit.go` (`rateLimitState`, `recordRateLimit`), `internal/newznab/client.go` (`Search`, `SearchRescue`, `get`, recherche d'addon, `TestWithOptions`), `internal/newznab/search_stream.go`, `internal/newznab/store.go` (`reserve`), `internal/newznab/manager.go` (`Search`), `internal/core/local_download.go` (`LocalDownloadMessage`), `internal/frontends/vela/frontend.go` (`fail`) | pause d'un indexeur imposée par le service distinguée du budget journalier (`core.IndexerThrottledError`, `core.IndexerDailyLimitError`), pause qui s'allonge de 15 min à 24 h sans Retry-After, messages en français : `internal/newznab/pse_throttle.go`, `internal/core/pse_indexer_throttle.go`, `internal/frontends/vela/pse_throttle.go` ; alias sans import de `core` : `internal/frontend/pse_indexer_throttle.go`. Le palier est en mémoire : après un redémarrage la pause en cours est conservée (table `newznab_rate_limit`) mais le palier repart de 15 min. Le commentaire d'Ether `rate_limit.go` (« pause modeste ») ne correspond plus au comportement ; il n'est pas modifié. |
| `internal/experience/continue.go` (champ `PSE`, `InProgress` et son tri, `appendNext`, `inProgressBatchMode`), `internal/state/continue_series.go` (plafond de lecture), `internal/app/frontend_experience.go` (`NewContinue`), `internal/app/frontend_experience_rows.go` (`experienceContinueKind`) | « Continuer à regarder » : plafond de séries examinées (200 par défaut, jusqu’à 1000), période du dernier visionnage, nouveaux épisodes et nouvelles saisons (sortis après le dernier visionnage) placés en tête puis classés à leur date de sortie, quantités films et séries (`internal/experience/pse_continue.go`, `internal/app/pse_continue.go`, `internal/pse/continuewatch` ; tables `pse_continue_next*` classées dans `internal/catalog/pse_tier_sources.go`). Réglages `PSE_CONTINUE_*` dans Lecture → Continuer à regarder de la console. `Continue.PSE` à nil = comportement d’Ether ; sur Plex (`ShowOnDeck`), au plus 100 séries examinées car `Prepare` charge tous leurs épisodes. Lecture depuis la table Remix `pse_continue_next` (`internal/pse/continuewatch/next.go`, `internal/experience/pse_continue_next.go`) : un point d’accroche dans `appendNext` (`pseAppendFromTable`) ; tâche de mise à jour démarrée par `startPSEContinue` (`internal/app/pse_continue.go`, appelée depuis `internal/app/pse_music.go`). Table non remplie pour un spectateur : repli sur le calcul d’Ether. |
| `internal/experience/library_activity.go` (`latestAdded`, `Resume`) | ajouts récents et reprise des bibliothèques à thème autorisées dans Jellyfin (`internal/experience/pse_jellyfin_activity.go`) |
| `internal/experience/service.go` (`Views`, `LibraryItems`) | vues et navigation des bibliothèques et collections Remix dans Jellyfin (`internal/experience/pse_jellyfin_themes.go`) |
| `internal/experience/service.go` (`Facets`, `ResolveFacet`), `internal/experience/library_facets.go` (`FacetPage`, `FilterFacets`) | facettes de genre et de studio calculées sur les titres autorisés dans la vue Remix Jellyfin |
| `internal/experience/service.go` (`Search`), `internal/experience/served_types.go` (`servedItemKinds`) | recherche des titres et des épisodes dans les bibliothèques Remix autorisées dans Jellyfin, et recherche d'un profil limité aux thèmes dans l'union de ses thèmes (`internal/experience/pse_jellyfin_search.go`) |
| `internal/experience/continue.go` (`inProgressBatchMode`, filtre des sections), `internal/experience/library_activity.go` (`NextUp`) | « À suivre » Jellyfin : accepte les titres accordés par un thème Remix, contrôle parental conservé ; Plex et Emby inchangés (`internal/experience/pse_jellyfin_nextup.go`) |
| `internal/app/frontend_experience.go` (`newSurfaceExperience`), `internal/experience/service.go` (`ServiceDeps`) | applique les critères du catalogue aux membres des bibliothèques Remix Jellyfin, avec les exceptions de langue du thème |
| `internal/frontends/plex/handlers_hubs.go` (`searchTypes`) | recherche Plex : sections des bibliothèques Remix accordées au spectateur, sans doublon de type (`internal/frontends/plex/pse_themes.go`) |
| `internal/frontend/host_sessions_experience.go` (`Visibility.ThemeGrant`, `TitleAllowed`), `internal/experience/policy.go` (`BindVisibility`), `internal/frontends/plex/safety.go` (`visibility`), `internal/experience/plugin_libraries.go` (`sectionOrFilterAllowed`) | un titre refusé par les sections de base s'ouvre s'il appartient à une bibliothèque Remix explicitement accordée (`frontend.PSEThemeGrant`, `internal/frontend/pse_themes.go`) |
| `internal/admin/handlers.go` (`handleUpdateUser`) | normalisation des bibliothèques accordées d'un spectateur, marqueur 5100 et numéros Remix (`frontend.NormalizeAllowedLibraries`, `internal/frontend/pse_themes.go`) |
| `internal/app/frontend_runtime.go` (clé de cache WebDAV), `internal/identity/image_page_grant.go` (`pageImages`) | caches WebDAV et images de page invalidés quand les bibliothèques Remix changent (`frontend.PSEThemesGeneration`) |
| `internal/collections/collections.go` (`Service.builtHook`, `build`) | fonction appelée après chaque reconstruction réussie d'une collection, pour invalider les caches dérivés de ses membres (`internal/collections/pse_built_hook.go`) |
| `web/src/pages/viewers/ViewerAccess.tsx` (`ViewerLibraries`) | cases des bibliothèques Remix dans l'accès d'un spectateur restreint, marqueur 5100, enregistrement bloqué si leur lecture échoue (`web/src/pse/ViewerRemixLibraries.tsx`) |
| `.github/workflows/pr-image-build.yml` (`paths-ignore`) | pas d'image d'aperçu pour une PR qui ne touche que la documentation (`*.md`, `docs/`, `LICENSE`) ou la configuration GitHub (`.github/`) ; `pr-image-publish.yml` ne tourne qu'après un build réussi, il est donc ignoré aussi |

Les réglages PSE passent tous par `internal/pse/pseenv` : la valeur enregistrée dans la console (table `pse_settings`) l'emporte sur la variable d'environnement. Un nouvel ajout lit ses réglages avec `pseenv.Get` et les déclare dans `internal/pse/console/fields.go`.

## Synchronisation automatique

Chaque jour à 05:17 UTC, le workflow compare Ether à la révision publique enregistrée dans `.github/pse/upstream-base`. Il fusionne à trois voies le fork et Ether depuis cette révision, sans importer l'ancien historique privé ni ajouter les commits Ether comme parents. La fusion passe par des commits temporaires qui partagent cette révision : Git suit ainsi les fichiers et dossiers déplacés par Ether. Un fichier Remix ajouté dans un dossier qu'Ether déplace suit ce dossier, et une modification Remix d'un fichier déplacé est reportée au nouvel emplacement.

Les trois README, `.github/`, `docs/pse/`, `internal/pse/` et `web/src/pse/` restent propres à Ether-Remix. Les changements des workflows Ether nécessitent une revue manuelle. L'interface embarquée est reconstruite depuis les sources fusionnées.

En cas de conflit, une issue est ouverte et main reste inchangée. Elle liste les fichiers en conflit, le type de chaque conflit (contenu, fichier supprimé par Ether mais modifié par Remix, etc.) et le lien vers l'exécution. Sinon, une branche temporaire est publiée et les contrôles Go et web s'exécutent sur son commit exact. Une issue fournit le lien permettant de créer une PR vers main. La création et l'approbation de PR par les Actions restent désactivées, par choix du propriétaire. Une fois la PR créée manuellement, GitHub attend les contrôles obligatoires, dont Secrets, avant son intégration, sans push direct ni contournement. Les images amd64 et arm64 sont ensuite reconstruites.

Le `GITHUB_TOKEN` suffit pour préparer et vérifier la branche : aucun jeton personnel ni secret de synchronisation n'est requis, car les workflows upstream sont exclus du delta. La création de la PR et sa fusion restent manuelles pour la synchronisation upstream.

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

La CI du fork vérifie que `internal/admin/dist` correspond exactement à la compilation Linux. En cas d'écart, elle conserve pendant sept jours l'artefact `embedded-web-dist`, produit par le même commit, pour comparer les fichiers ou récupérer le bundle attendu. Vérifier le SHA de la PR avant de remplacer le dist ; le contrôle reste obligatoire après cette correction.

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

Les vues Jellyfin des bibliothèques Remix utilisent le numéro de section principal. Le bit 59 sépare les vues de rangées. La création d’un thème réserve les anciens numéros qui figurent encore dans les droits enregistrés des spectateurs. Un numéro libéré de tous les droits peut être réutilisé. Un favori de bibliothèque conservé par un profil ayant accès à tous les thèmes peut alors viser la nouvelle bibliothèque.

## Configuration rapide JukeBox

`internal/pse/jukebox` fournit les trois listes de démarrage, le catalogue public borné et mis en cache, et la création réentrante des collections/bibliothèques. `internal/app/pse_console.go` branche `/pse/jukebox/` sur l’API propriétaire existante. Le composant `web/src/pse/JukeboxQuickSetup.tsx` est accessible depuis Contenus et Bibliothèques ; le point d’accroche dans `web/src/pages/libraries/Libraries.tsx` le propose aussi dans les bibliothèques communes à tous les protocoles. Aucune modification des sources de lecture ni des règles de langue.


### Proxys spécifiques aux sources

`internal/config/source_proxy.go`, `internal/egress/source_proxy.go` et `web/src/pse/SourceProxyFields.tsx` portent le routage par compte et par addon. Préserver les appels depuis `app/outbound.go`, les constructeurs et lectures `debrid`, les requêtes `stremio`, la validation et la restauration des secrets `admin` lors des synchronisations amont. Les payloads ne persistent que l’identifiant de l’addon ; aucun identifiant de proxy n’y est copié. Un proxy spécifique précède MediaFlow et la politique globale. Les tests `source_proxy_test.go` vérifient routage, redirections, lecture et protection des secrets.

## Synchronisation du 15 novembre 2026

Révision Ether intégrée : `13d21d35fd15980c13c2bca15a26ddb7e5b8734a`. Conserver le filtrage de découverte des collections, les messages de téléchargement local, les traductions des rangées Plex, la signature des profils de qualité, la priorité SubMaker et le routage MediaFlow lors des prochaines fusions. Les bibliothèques upstream utilisent désormais la source `catalogue` ; les bibliothèques issues des collections Remix restent disponibles. Le repère upstream avance avec cette résolution, sans changement des workflows de synchronisation.

## Seedstrem

`internal/stremio/backend.go` conserve les routes Seedstrem `/stremio/play/{hash}?magnet=…` comme flux HTTP à la demande, au lieu de les transformer en candidats pour les comptes débrideurs. Le helper `seedstremPlay` de `internal/stremio/links.go` vérifie la cohérence du hash et du magnet et accepte un préfixe privé. Conserver ce cas avant la branche torrent lors des synchronisations Ether. `seedstrem_test.go` couvre le manifeste distant, les autres routes et l’absence d’ouverture du flux pendant les contrôles d’arrière-plan. La console réutilise le formulaire Stremio natif, sans nouvelle configuration parallèle.

### Seedstrem et dernier recours à la lecture

Les points d’accroche dans `internal/core/ondemand.go`, `internal/core/indexer_purpose.go` et `internal/debrid/direct.go` appellent `internal/core/pse_seedstrem.go` pour réserver l’ouverture des liens Seedstrem à une lecture réelle. `internal/resolver/resolver.go` conserve la configuration `Indexer.PlaybackLastResort`, sépare les vols de résolution et appelle `pse_last_resort.go` pour essayer les sources normales avant les secours. Les recherches d’indexation restent communes. Le réglage est exposé dans `web/src/pages/sources/Indexers.tsx` et `web/src/lib/types.ts`. Préserver ces accroches lors des synchronisations Ether.

### Navigation et sélection d’indexation

`web/src/pse/navigation.tsx` et `PsePage.tsx` organisent sept onglets (Vue d’ensemble, Sources, Indexation, Lecture, Contenus, Visuels, Suivi), découpés en vues `?view=`. Les anciens liens (`sources?source=indexation`, `priorites`, `statistiques`, `demarrage`, `releases`, `langues`, `demande`, `medias-locaux`) redirigent sans dupliquer la configuration Ether. `IndexerGroups` filtre les composants Ether par provenance (`prowlarrImport` ou chemin API Jackett) ; `IndexerList.originKey` est facultatif pour préserver ses usages amont.

`PSE_INDEXER_SOURCES` conserve sa sémantique historique : vide = addons Stremio activés, liste = sélection explicite. La valeur réservée `__none__` représente une sélection personnalisée vide, sans réactiver les addons automatiquement. Aucun appel de recherche ou téléchargement n’est déclenché par ce changement d’interface.

Les compteurs de priorité utilisent une seule réponse `/pse/indexer/details`, avec l’identifiant des bibliothèques ajouté aux lignes existantes. Ils réutilisent les agrégats et le cache des statistiques, sans requête supplémentaire par collection.

### Dispositions et partage par contribution

`internal/pse/layouts` stocke les modèles nommés et le retour en arrière. Chaque composant fournit sa capture/restauration de présentation dans `layout_template.go` ; les quatre parties sont appliquées dans une transaction, puis les caches sont invalidés. Les ACL ne font pas partie du modèle. Les bibliothèques à thème ajoutent `users_json` avec accès historique « all » et conservation des droits lors des mises à jour anciennes. Les membres des collections locales conservent leur provenance ; les permissions actuelles sont vérifiées avant tri/limite, sans cacher le résultat d’un spectateur dans le cache commun. La source `releaseSource` lit les observations indexées. Voir [le guide](LAYOUTS.fr.md).


### Configuration rapide des sources

`resolver.SourceOrder.Quick` persiste le mode, l’ordre et l’identifiant de l’instance Seedstrem. `internal/resolver/quick_sources.go` réutilise classement, filtres, validation et secours ; `internal/app/pse_quick_sources.go` borne les recherches du gestionnaire existant. Le contexte `core.WithQuickSource` limite le fan-out et empêche la VOD de rejoindre ces étapes. Le cache de recherche Alkodiques est séparé du cache SFR ordinaire. Les payloads torrent natifs gardent leur restriction de fournisseur durant le renouvellement. Aucun changement au parcours désactivé, à la musique ou à la résolution locale. Interface et guide : `QuickSources.tsx`, [FR](QUICK_SOURCES.fr.md) / [EN](QUICK_SOURCES.en.md).

## Fusion des contributions et secrets

`main` exige une PR, sans approbation humaine, et les contrôles **Ether-Remix CI** et **Secrets**. Les push directs, force pushes, suppressions et contournements sont interdits, administrateurs compris. Aucune signature ni revue CODEOWNERS n'est exigée.

Seules les PR non brouillon de `kesurof`, provenant exactement de `kesurof/Ether-Remix` et ciblant `main`, activent automatiquement la fusion squash. Les autres contributions conservent leur fonctionnement habituel. L'Action privilégiée ne lit que les métadonnées GitHub et son propre script sur `main` ; elle n'exécute jamais le code du fork. Un conflit, un contrôle absent ou une CI en échec bloque la fusion native GitHub. Après confirmation de la fusion, la publication Docker habituelle est déclenchée explicitement pour les changements applicatifs, sans déploiement de service. Une PR limitée à la documentation et à `.github/` ne construit ni ne publie d'image, avant ou après fusion.

Gitleaks examine les nouveaux commits de chaque PR, y compris les forks et la documentation. Les anciennes détections hors de ces commits ne bloquent pas la PR ; la configuration et les listes d'exclusion fournies par la PR sont ignorées. Aucun résultat contenant une valeur secrète n'est publié dans les logs ou les artefacts. Le secret scanning GitHub et sa push protection complètent ce contrôle ; les notifications de sécurité se règlent dans les préférences du compte GitHub.
