# Index des releases

Les recherches et imports conservent automatiquement leurs références dans un index commun, réutilisé à la lecture. Dans **Remix → Indexation**, le seul interrupteur **Rechercher automatiquement des releases** (`PSE_INDEXER`) contrôle les recherches de fond. Il reste désactivé par défaut ; l’arrêter n’efface pas l’index et ne désactive ni sa consultation ni sa réutilisation. L’ancien réglage `PSE_RELEASE_LIBRARY` est ignoré, quelle que soit sa valeur. Les sources, budgets et pauses déjà configurés sont conservés.

Les recherches réelles alimentent la bibliothèque, ainsi que l’import SFR pour les titres du catalogue. Les liens fichiers Alkodiques conservent le chemin AllDebrid vérifié, sans ajout de magnet. Les références et éventuels en-têtes privés sont chiffrés avec la clé de configuration de Remix et ne sont jamais envoyés au navigateur.

À la lecture, les références encore récentes sont proposées au moteur existant : langue, qualité, contraintes du lecteur et vérifications restent applicables. Le moteur peut réutiliser les alternatives conservées puis faire une recherche de secours si elles échouent ou ne conviennent pas. Le gain dépend du temps de résolution, de contrôle et de mise en tampon ; aucune lecture instantanée n’est garantie. Les recherches d’indexation et les recherches manuelles continuent à interroger les sources.

## Consulter les releases

Ouvrez une fiche dans le catalogue de la console, puis **Releases indexées**. La consultation reste possible lorsque la recherche automatique est arrêtée. Choisissez le champ et le sens du tri : résolution, taille, codec vidéo/audio, source, protocole, langues, cache annoncé, état de la référence ou dates. Une recherche textuelle filtre les releases sans interroger les sources. Le tri d’affichage ne modifie pas les critères de lecture.

Pour une série, choisissez une saison puis un épisode. La vue initiale correspond à S01E01, utilisé par l’indexation du catalogue ; les autres épisodes sont alimentés par leurs recherches. Une série indexée ne signifie pas que tous ses épisodes sont disponibles.

- **Publication** : date fournie par la source, inconnue si absente.
- **Première indexation / dernière observation** : dates de découverte et de rafraîchissement dans Remix.
- **Métadonnées seules** : informations déjà indexées sans référence exploitable conservée.
- **Référence retenue** : candidate réutilisable, pas une preuve de lecture réussie.
- **À actualiser / échec / source modifiée** : référence écartée de la réutilisation.

**Exploration de l’index** : dans **Indexation → Releases**, recherchez les médias (titre ou titre original) ou les releases (nom ou source) dans tout l’index. Filtrez par Films, Séries ou Animes, puis par aujourd’hui, cette semaine (depuis lundi), ce mois ou des dates personnalisées inclusives, dans votre fuseau horaire. Choisissez la première indexation ou la dernière observation. Les séries regroupent les épisodes ; un clic déplie leurs releases directement sous le média, sans quitter la liste ni perdre la recherche. Les releases se chargent à la première ouverture et restent conservées lors du repliage. Les animes utilisent les métadonnées du catalogue, lorsqu’elles sont connues. Les résultats sont paginés par 50 et triables par indexation ou observation. La vue globale des releases utilise Précédent/Suivant sans recompter tout l’index ; le total reste affiché pour les médias et pour les releases d’un média. Les lignes compactes dévoilent codecs, langues et dates au clic, sans tableau horizontal. La recherche ne contacte aucune source. À la première ouverture des releases, les anciennes références chiffrées sont préparées par lots, uniquement pour le média ouvert lorsqu’il est sélectionné ; seuls les champs affichables sont ajoutés à l’index de recherche.

## Conservation

Les URL HTTP ordinaires sont réutilisables pendant une heure ; les liens de téléchargement torrent/NZB pendant 24 heures. Les références stables (hash/magnet valide et lien fichier AllDebrid) sont conservées sans expiration d’âge, jusqu’à un échec ou un changement de configuration des sources. Elles passent toujours par les contrôles de lecture ; un hash conservé n’affirme pas que le fichier est en cache. Les changements de qualité ou de langue éliminent les liens rejetés ; élargir les critères nécessite de nouvelles recherches. Les références expirées sont supprimées après 60 jours sans observation lors d’une nouvelle collecte. La consultation ne prolonge pas leur validité.

Seules les releases renvoyées par les sources sont disponibles : les filtres appliqués par un addon ne peuvent pas être annulés dans Remix. Les anciennes métadonnées ne permettent pas de reconstruire rétroactivement les liens ; une nouvelle recherche ou un import est nécessaire. Arrêter la recherche automatique conserve la collecte lors des recherches à la lecture et des imports, ainsi que la réutilisation des références existantes.

## Suivi dans la console

La vue **Indexation → Releases** est toujours accessible. Les réglages, les sources et l’activité des recherches se trouvent dans **Indexation**. La collecte est continue, sans pourcentage global de complétion. Pour une série, seuls les épisodes réellement présents dans l’index sont affichés ; une recherche initiale S01E01 ne représente pas une saison complète.

Les compteurs, également présents dans **Vue d’ensemble**, distinguent les métadonnées des références conservées : disponibles, à actualiser, en échec ou liées à une source modifiée. Ces ensembles se recoupent et ne doivent pas être additionnés. Les compteurs se rafraîchissent toutes les 60 secondes lorsque la page est visible. Une référence retenue ne garantit pas la lecture.

Une erreur de rafraîchissement des compteurs conserve leurs dernières valeurs et ne ferme plus l’explorateur. Une erreur sur un média affiche un bouton Réessayer dans son bloc, sans effacer les autres résultats. Le chargement ne relance pas une requête encore en cours.

## Identité des médias et réparation de l’index

Une recherche par TMDB/IMDb ne prouve pas que la source a répondu pour le bon média. Même un identifiant déclaré concordant exige un nom correspondant à un titre complet du catalogue (original ou alternatif compris) et une année exacte pour un film. Une contradiction d’identifiants reste éliminatoire. Un épisode exige une correspondance de série et un numéro unique compatible ; sans identifiant déclaré concordant, l’année de la série doit aussi être connue. Les packs de saison et les noms ambigus sont refusés. Les résultats sans véritable référence de source sont refusés : un identifiant de résultat seul ne permet pas de lire. Le même contrôle protège les recherches normales, de secours et les références déjà enregistrées avant lecture.

Un nouvel audit, version 2, s’exécute même si le précédent avait terminé. Il vérifie les anciennes métadonnées et références chiffrées, y compris les imports StreamFusion : le rattachement historique TMDB ne sert jamais de preuve à lui-même. Les entrées non confirmées sont préservées dans les tables privées `pse_releases_quarantine` et `pse_release_library_quarantine`, hors recherche et réutilisation. « Non confirmé » ne signifie pas nécessairement « faux » : les preuves peuvent manquer. L’indexation et la réutilisation attendent la fin de cet audit ; les anciens compteurs et verdicts de disponibilité sont recalculés.

Les contenus déjà indexés sont ensuite repris progressivement pour récupérer les liens manquants, avec les budgets, pauses et sources habituels. Le compteur **Contenus à reprendre** suit cette file ; les échecs attendent un prochain passage et aucun lien n’est inventé depuis un nom. Les références stables confirmées sont réutilisées directement. L’import StreamFusion est relancé sous la nouvelle règle. Cette reprise dépend des réponses des sources et ne promet ni une référence pour chaque titre ni la découverte de tous les épisodes d’une série : l’indexation générale représente une série par S01E01, tandis que les épisodes déjà recherchés sont repris individuellement.

L’outil `go run ./cmd/ether-release-audit -db /chemin/copie.db -config /chemin/config-copie` fournit un audit en lecture seule. Utilisez une sauvegarde SQLite cohérente et une copie privée de `config.json` et `.credentials-key` (le chargement de configuration peut mettre à niveau cette copie). `-apply` met les entrées non confirmées en quarantaine : arrêter l’indexation/le serveur et conserver une sauvegarde préalable. Le rapport contient uniquement des compteurs par source et motif, jamais les URL de lecture ni les secrets.

La recherche globale de releases parcourt les noms sans imposer le tri chronologique à chaque lecture ; le parcours par catégorie utilise l’ordre des dates pour limiter les lignes lues.


## AllDebrid : références et quota d’ajout

Les liens fichiers AllDebrid identifiés sont débridés sans ajout de magnet. Les torrents déjà présents dans le compte sont retrouvés par leur hash puis réutilisés. Une référence torrent absente du compte nécessite encore un ajout : conserver son hash dans la bibliothèque ne suffit pas à produire un lien fichier. Le refus `MAGNET_TOO_MANY` ne suspend que les ajouts répétés, pas la lecture des fichiers déjà accessibles.

L’indexeur Remix conserve des références sans lancer lui-même de téléchargement ni débrider les fichiers. Une recherche auprès d’un addon distant reste toutefois exécutée par cet addon : celui-ci peut appeler AllDebrid pour vérifier son propre cache. Les budgets Remix ne contrôlent pas ces appels internes. L’import direct de la base StreamFusion et de ses liens Alkodiques évite cette recherche distante.

## Sélection avant conservation

Les liens doivent respecter les critères Remix actifs avant enregistrement et réutilisation : profils globaux ou spécifiques en mode profils ; filtres simples et langues du catalogue français en mode simple. Les exceptions explicites de langue restent appliquées sans annuler les critères de qualité. Une langue inconnue ne satisfait pas une exigence VF/MULTi/VOSTFR.

Le nettoyage des anciens liens démarre après actualisation des profils et exceptions, au démarrage, après modification et lors du passage périodique. Il ne supprime aucun fichier vidéo. Les observations sans lien restent des métadonnées de diagnostic ; elles ne prouvent pas la conformité ni la possibilité de lecture. Élargir les critères nécessite de nouvelles recherches pour retrouver les liens rejetés. « Référence retenue » ne remplace pas les contrôles du client et du profil de lecture.
