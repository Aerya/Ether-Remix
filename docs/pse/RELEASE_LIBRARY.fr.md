# Bibliothèque de releases — Bêta

Activez **Remix → Indexation → Bibliothèque de releases (Bêta)** pour conserver les références exploitables des résultats de recherche. L’option `PSE_RELEASE_LIBRARY` est **désactivée par défaut**. Elle ne lance pas automatiquement l’indexation : celle-ci conserve ses sources, budgets et pauses habituels.

Les recherches réelles alimentent la bibliothèque, ainsi que l’import SFR pour les titres du catalogue. Les liens fichiers Alkolites conservent le chemin AllDebrid vérifié, sans ajout de magnet. Les références et éventuels en-têtes privés sont chiffrés avec la clé de configuration de Remix et ne sont jamais envoyés au navigateur.

À la lecture, les références encore récentes sont proposées au moteur existant : langue, qualité, contraintes du lecteur et vérifications restent applicables. Le moteur peut réutiliser les alternatives conservées puis faire une recherche de secours si elles échouent ou ne conviennent pas. Le gain dépend du temps de résolution, de contrôle et de mise en tampon ; aucune lecture instantanée n’est garantie. Les recherches d’indexation et les recherches manuelles continuent à interroger les sources.

## Consulter les releases

Ouvrez une fiche dans le catalogue de la console, puis **Releases indexées · Bêta**. La consultation est possible même lorsque la réutilisation est désactivée. Cliquez sur un en-tête pour inverser le tri : résolution, taille, codec vidéo/audio, source, protocole, langues, cache annoncé, état de la référence ou dates. Une recherche textuelle filtre le tableau sans interroger les sources. Le tri d’affichage ne modifie pas les critères de lecture.

Pour une série, choisissez une saison puis un épisode. La vue initiale correspond à S01E01, utilisé par l’indexation du catalogue ; les autres épisodes sont alimentés par leurs recherches. Une série indexée ne signifie pas que tous ses épisodes sont disponibles.

- **Publication** : date fournie par la source, inconnue si absente.
- **Première indexation / dernière observation** : dates de découverte et de rafraîchissement dans Remix.
- **Métadonnées seules** : informations déjà indexées sans référence exploitable conservée.
- **Référence disponible** : candidate réutilisable, pas une preuve de lecture réussie.
- **À actualiser / échec / source modifiée** : référence écartée de la réutilisation.

## Conservation

Les URL HTTP ordinaires sont réutilisables pendant une heure ; les références torrent/NZB et liens fichiers AllDebrid pendant 24 heures. Une modification de configuration des sources invalide les références précédentes. Les changements de critères de qualité ou de langue conservent les références pour permettre un nouveau tri local. Les entrées restent consultables et sont supprimées après 60 jours sans observation lors d’une nouvelle collecte. La consultation et la lecture depuis la bibliothèque ne prolongent pas ces dates.

Seules les releases renvoyées par les sources sont disponibles : les filtres appliqués par un addon ne peuvent pas être annulés dans Remix. Les anciennes métadonnées ne permettent pas de reconstruire rétroactivement les liens ; une nouvelle recherche ou un import est nécessaire. Désactiver la bêta arrête la collecte et la réutilisation de ces références, sans supprimer les métadonnées existantes.

## Suivi dans la console

Activez la bêta dans **Vue d’ensemble → Démarrage** ou **Indexation**, puis enregistrez. La vue **Indexation → Releases** apparaît après enregistrement. Il liste les contenus et épisodes par dernière observation, par pages de 50 ; sélectionnez un contenu pour consulter ses releases triables. Les recherches en cours et récentes indiquent l’activité réelle. La collecte est continue, sans pourcentage global de complétion.

Les compteurs, également présents dans **Vue d’ensemble**, distinguent les métadonnées des références conservées : disponibles, à actualiser, en échec ou liées à une source modifiée. Ces ensembles se recoupent et ne doivent pas être additionnés. L’affichage se rafraîchit toutes les 15 secondes lorsque la page est visible. Une référence disponible ne garantit pas la lecture.
