# Bibliothèque de releases — Bêta

Activez **Remix → Indexation → Bibliothèque de releases (Bêta)** pour conserver les références exploitables des résultats de recherche. L’option `PSE_RELEASE_LIBRARY` est **désactivée par défaut**. Elle ne lance pas automatiquement l’indexation : celle-ci conserve ses sources, budgets et pauses habituels.

Les recherches réelles alimentent la bibliothèque, ainsi que l’import SFR pour les titres du catalogue. Les liens fichiers Alkolites conservent le chemin AllDebrid vérifié, sans ajout de magnet. Les références et éventuels en-têtes privés sont chiffrés avec la clé de configuration de Remix et ne sont jamais envoyés au navigateur.

À la lecture, les références encore récentes sont proposées au moteur existant : langue, qualité, contraintes du lecteur et vérifications restent applicables. Le moteur peut réutiliser les alternatives conservées puis faire une recherche de secours si elles échouent ou ne conviennent pas. Le gain dépend du temps de résolution, de contrôle et de mise en tampon ; aucune lecture instantanée n’est garantie. Les recherches d’indexation et les recherches manuelles continuent à interroger les sources.

## Consulter les releases

Ouvrez une fiche dans le catalogue de la console, puis **Releases indexées · Bêta**. La consultation est possible même lorsque la réutilisation est désactivée. Choisissez le champ et le sens du tri : résolution, taille, codec vidéo/audio, source, protocole, langues, cache annoncé, état de la référence ou dates. Une recherche textuelle filtre les releases sans interroger les sources. Le tri d’affichage ne modifie pas les critères de lecture.

Pour une série, choisissez une saison puis un épisode. La vue initiale correspond à S01E01, utilisé par l’indexation du catalogue ; les autres épisodes sont alimentés par leurs recherches. Une série indexée ne signifie pas que tous ses épisodes sont disponibles.

- **Publication** : date fournie par la source, inconnue si absente.
- **Première indexation / dernière observation** : dates de découverte et de rafraîchissement dans Remix.
- **Métadonnées seules** : informations déjà indexées sans référence exploitable conservée.
- **Référence disponible** : candidate réutilisable, pas une preuve de lecture réussie.
- **À actualiser / échec / source modifiée** : référence écartée de la réutilisation.

**Exploration de l’index** : dans **Indexation → Releases**, recherchez les médias (titre ou titre original) ou les releases (nom ou source) dans tout l’index. Filtrez par Films, Séries ou Animes, puis par aujourd’hui, cette semaine (depuis lundi), ce mois ou des dates personnalisées inclusives, dans votre fuseau horaire. Choisissez la première indexation ou la dernière observation. Les séries regroupent les épisodes ; un clic déplie leurs releases directement sous le média, sans quitter la liste ni perdre la recherche. Les releases se chargent à la première ouverture et restent conservées lors du repliage. Les animes utilisent les métadonnées du catalogue, lorsqu’elles sont connues. Les résultats sont paginés par 50 et triables par indexation ou observation. La vue globale des releases utilise Précédent/Suivant sans recompter tout l’index ; le total reste affiché pour les médias et pour les releases d’un média. Les lignes compactes dévoilent codecs, langues et dates au clic, sans tableau horizontal. La recherche ne contacte aucune source. À la première ouverture des releases, les anciennes références chiffrées sont préparées par lots, uniquement pour le média ouvert lorsqu’il est sélectionné ; seuls les champs affichables sont ajoutés à l’index de recherche.

## Conservation

Les URL HTTP ordinaires sont réutilisables pendant une heure ; les références torrent/NZB et liens fichiers AllDebrid pendant 24 heures. Une modification de configuration des sources invalide les références précédentes. Les changements de critères de qualité ou de langue conservent les références pour permettre un nouveau tri local. Les entrées restent consultables et sont supprimées après 60 jours sans observation lors d’une nouvelle collecte. La consultation et la lecture depuis la bibliothèque ne prolongent pas ces dates.

Seules les releases renvoyées par les sources sont disponibles : les filtres appliqués par un addon ne peuvent pas être annulés dans Remix. Les anciennes métadonnées ne permettent pas de reconstruire rétroactivement les liens ; une nouvelle recherche ou un import est nécessaire. Désactiver la bêta arrête la collecte et la réutilisation de ces références, sans supprimer les métadonnées existantes.

## Suivi dans la console

Activez la bêta dans **Vue d’ensemble → Démarrage** ou **Indexation**, puis enregistrez. La vue **Indexation → Releases** apparaît après enregistrement. Elle permet de rechercher et parcourir les médias et releases par catégorie et période. Les recherches en cours et récentes indiquent l’activité réelle. La collecte est continue, sans pourcentage global de complétion.

Les compteurs, également présents dans **Vue d’ensemble**, distinguent les métadonnées des références conservées : disponibles, à actualiser, en échec ou liées à une source modifiée. Ces ensembles se recoupent et ne doivent pas être additionnés. Les compteurs se rafraîchissent toutes les 60 secondes lorsque la page est visible. Une référence disponible ne garantit pas la lecture.

Une erreur de rafraîchissement des compteurs conserve leurs dernières valeurs et ne ferme plus l’explorateur. Une erreur sur un média affiche un bouton Réessayer dans son bloc, sans effacer les autres résultats. Le chargement ne relance pas une requête encore en cours.

## Identité des médias et réparation de l’index

Une recherche par TMDB/IMDb ne suffit pas : une source peut ignorer la demande. Avant l’enregistrement, les identifiants réellement déclarés par la release sont comparés à ceux du média. Les identifiants simplement recopiés depuis la requête ne constituent pas une preuve. Sans identifiant concordant, il faut une correspondance de titre (y compris les titres originaux/alternatifs) et, pour un film, une année connue et compatible. Les contradictions d’identifiants, de type, d’année ou d’épisode et les faux résultats « aucun_flux » sont exclus. Les packs de la bonne saison restent admissibles ; cela ne garantit pas leur lecture.

Au premier démarrage après cette correction, une vérification locale parcourt les anciennes métadonnées et références chiffrées. L’indexation et la réutilisation de la bibliothèque attendent la fin ; le serveur reste disponible. Les entrées non confirmées sont déplacées, sans perte du descripteur original, vers `pse_releases_quarantine` et `pse_release_library_quarantine`, avec une raison. « Non confirmé » ne signifie pas nécessairement « faux » : des métadonnées manquantes peuvent empêcher la vérification. Les données importées directement de StreamFusion conservent leur rattachement TMDB typé. Les compteurs dérivés sont reconstruits et le nombre d’associations écartées apparaît dans la console.

L’outil `go run ./cmd/ether-release-audit -db /chemin/copie.db -config /chemin/config-copie` fournit un audit en lecture seule. Utilisez une sauvegarde SQLite cohérente et une copie privée de `config.json` et `.credentials-key` (le chargement de configuration peut mettre à niveau cette copie). `-apply` met les entrées non confirmées en quarantaine : arrêter l’indexation/le serveur et conserver une sauvegarde préalable. Le rapport contient uniquement des compteurs par source et motif, jamais les URL de lecture ni les secrets.

La recherche globale de releases parcourt les noms sans imposer le tri chronologique à chaque lecture ; le parcours par catégorie utilise l’ordre des dates pour limiter les lignes lues.
