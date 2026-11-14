# Intégration SeerrCatalog

Dans **Sources → Stremio → SeerrCatalog**, une seule section regroupe **Disponibilités vers Remix** (import des résultats SeerrCatalog) et **Recherche via Remix** (recherches demandées par SeerrCatalog). Les deux fonctions restent indépendantes, avec une clé et une activation chacune. Le test du partage se fait dans Remix ; le test des recherches se fait dans SeerrCatalog. Prowlarr autonome se configure séparément dans SeerrCatalog.

1. Dans SeerrCatalog, ouvrez **Paramètres → Intégration Ether Remix**, générez une clé et activez l’export.
2. Dans Remix, ouvrez **Sources → Stremio → SeerrCatalog → Disponibilités vers Remix**, saisissez l’adresse de base (ex. `https://seerrcatalog.example`, sans `/api/…`) et la clé, activez puis enregistrez. **Tester la connexion** vérifie le flux sans importer ; la synchronisation s’effectue chaque minute.
3. Les titres déjà validés sont exportés automatiquement par la nouvelle version de SeerrCatalog, sans relancer leurs recherches.

Les disponibilités de SeerrCatalog passent le filtre **« lisible uniquement »** sans nouvelle interrogation des addons. Les titres masqués ou à venir ne bloquent pas la synchronisation des autres disponibilités. Les filtres du catalogue et les règles de lecture restent actifs. L’import récupère les fiches TMDB. Remix tient deux collections, en films et en séries : **SeerrCatalog** (tous les titres disponibles) et **SeerrCatalog · VOSTFR** (titres que SeerrCatalog n’a trouvés qu’en version de repli : VOSTFR ou VO avec sous-titres externes). La seconde est rangée dans la bibliothèque **VOSTFR**. Ces titres sans VF n’apparaissent jamais dans Films, Séries ou l’accueil ; l’indexeur de Remix continue de leur chercher une VF.

Les **disponibilités confirmées restent acquises jusqu’à suppression** du média dans SeerrCatalog. Les anciennes observations sans confirmation expirent après 26 h. Les URL et en-têtes de lecture sont réutilisés au maximum **30 min** ; un hash torrent conservé reste exploitable pour un média confirmé. Une source périmée, refusée par les règles du spectateur ou défaillante ramène à la recherche habituelle. Les URL peuvent expirer plus tôt ou dépendre de l’IP. Pour les séries, seuls les épisodes contrôlés fournissent des sources réutilisables.

La synchronisation paginée rattrape modifications et suppressions après coupure ; un import incomplet conserve les données valides. Les sources sensibles restent en mémoire, hors API de catalogue, et sont relues au redémarrage. Désactiver l’intégration retire sa confiance. La clé partage les sources de **tous les utilisateurs SeerrCatalog** : instance Remix de confiance et HTTPS hors réseau privé requis.

Variables équivalentes : `PSE_SEERRCATALOG=on`, `PSE_SEERRCATALOG_URL=http://seerrcatalog:7000`, `PSE_SEERRCATALOG_TOKEN=…`. L’option est désactivée par défaut.

## Recherche complémentaire

Dans **SeerrCatalog → Paramètres → Intégration Ether Remix → Recherche via Remix**, activez la recherche et renseignez l’adresse de base de Remix. Générez une **clé dédiée aux recherches** dans ce bloc, puis copiez-la dans **Remix → Sources → Stremio → SeerrCatalog → Recherche via Remix** et activez les recherches. Enregistrez des deux côtés.

Le partage des disponibilités possède sa propre clé et peut rester désactivé. Après mise à jour des deux applications, configurez cette nouvelle clé : l’ancienne clé de partage n’autorise plus les recherches. Lorsque les addons ne trouvent aucune release dans les langues principales et que le repli utilisateur est activé, SeerrCatalog demande à Remix une recherche dans ses sources configurées, notamment Prowlarr. Les tags principaux/de repli, la déduplication et les seuils restent appliqués par SeerrCatalog. Les contrôles J+1/J+3 et la disponibilité définitive sont conservés. Les recherches respectent les budgets de disponibilité et ne réutilisent pas circulairement les observations SeerrCatalog. Les torrents avec infoHash et les vrais flux HTTP peuvent être retournés ; les liens NZB ne sont jamais présentés comme des vidéos. Le test de connexion s’effectue depuis SeerrCatalog, sans lancer de recherche.

Pour les SRT externes et traduits, voir le [guide SubMaker](SUBMAKER.fr.md). Le [README SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog#intégration-ether-remix) décrit le contrat HTTP.
