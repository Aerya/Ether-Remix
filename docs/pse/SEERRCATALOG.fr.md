# Intégration SeerrCatalog


[SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog) transforme vos demandes Seerr en catalogues Stremio et contrôle leurs sources. Son intégration facultative avec [Ether Remix](https://github.com/Aerya/Ether-Remix) transmet ces résultats pour éviter de rechercher deux fois les mêmes contenus.

1. Dans SeerrCatalog, ouvrez **Paramètres → Intégration Ether Remix**, générez une clé et activez l’export.
2. Dans Remix, ouvrez **Sources → Stremio → SeerrCatalog → Ether Remix**, saisissez l’adresse de base de SeerrCatalog et la clé, activez puis enregistrez.
3. Facultatif : dans SeerrCatalog, indiquez l’adresse de Remix suivie de `/api/integrations/seerrcatalog/v1/events`. Sans notification, la synchronisation reste automatique chaque minute.
4. Revérifiez les anciens contenus dans SeerrCatalog pour créer leurs premiers résultats exportables.

Remix fait confiance aux critères de SeerrCatalog : les titres disponibles passent le filtre **« lisible uniquement »**, et leur contrôle d’indexation n’interroge pas à nouveau les addons. Les autres filtres de visibilité et les règles de lecture des spectateurs restent actifs. L’import récupère les fiches TMDB ; aucune collection n’est créée automatiquement.

SeerrCatalog revérifie les contenus exportés après 24 h, avec un réveil horaire. Les disponibilités sont valables **26 h** ; les liens de lecture et en-têtes sont réutilisés au maximum **30 min**, les hash torrent jusqu’à l’expiration de l’observation. À la lecture, une source périmée, refusée par les règles du spectateur ou défaillante ramène à la recherche habituelle. Une URL peut expirer plus tôt ou être liée à l’adresse IP du fournisseur. Pour une série, seuls les épisodes effectivement contrôlés fournissent des sources réutilisables.

La synchronisation complète et paginée rattrape aussi les modifications et suppressions après une coupure ; un import incomplet ne remplace pas les données valides précédentes. Les sources sensibles restent en mémoire dans Remix, hors des API de catalogue, et sont relues après redémarrage. Une désactivation retire la confiance accordée à l’intégration. La clé partage les sources de tous les utilisateurs SeerrCatalog : réservez-la à une instance Remix de confiance et utilisez HTTPS hors réseau privé.

Variables équivalentes : `PSE_SEERRCATALOG=on`, `PSE_SEERRCATALOG_URL=http://seerrcatalog:7000`, `PSE_SEERRCATALOG_TOKEN=…`. L’option est désactivée par défaut.


Le contrat HTTP complet est documenté dans le [README SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog#intégration-ether-remix).
