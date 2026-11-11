# Sous-titres externes avec SubMaker

Cette intégration facultative est désactivée par défaut. Elle utilise une instance [publique](https://submaker.elfhosted.com/configure) ou [auto-hébergée](https://github.com/xtremexq/StremioSubMaker). Aucun fournisseur ni moteur de traduction n’est préconfiguré par Remix.

## Configuration

1. Configurez vos fournisseurs de sous-titres et votre moteur de traduction dans SubMaker. Copiez son **manifeste personnel**, de forme `https://submaker.example/addon/<configuration>/manifest.json`. Une adresse `/configure` seule ne suffit pas. Ce manifeste contient des informations privées : ne le publiez pas.
2. Dans **Remix → Sources → Stremio → SubMaker**, activez l’intégration, collez le manifeste et choisissez les langues source/cible (anglais/français par défaut). Enregistrez les réglages de la page, puis testez le manifeste. Ce test ne déclenche aucune traduction.
3. Autorisez le périmètre global ou ajoutez des exceptions pour les bibliothèques/collections, puis **Enregistrer le périmètre**. La collection prime sur la bibliothèque, puis sur le global ; à priorité égale, la première règle correspondante gagne. Un titre SeerrCatalog sans appartenance connue utilise le global.
4. Pour [SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog), configurez aussi la recherche via Remix et sa clé dédiée. Dans les filtres utilisateur SeerrCatalog, activez les sous-titres externes et choisissez la même langue cible que Remix. Le partage des disponibilités garde sa propre activation et sa propre clé.

## Recherche et lecture

Remix recherche d’abord un sous-titre dans la langue cible, puis dans la langue source. Le nom du fichier de sous-titres doit correspondre exactement au nom normalisé de la release (extension, ponctuation et casse neutralisées). Une simple correspondance de titre ne suffit pas. Certains fournisseurs n’exposent pas ce nom : leurs résultats ne sont alors pas validés automatiquement.

Si nécessaire, Remix demande à SubMaker de traduire le SRT. Il conserve le résultat uniquement après réception complète et vérification de la structure, du nombre de répliques et des timecodes. Ces contrôles ne prouvent pas la qualité linguistique ni la synchronisation réelle avec la vidéo : une vérification à la lecture reste nécessaire. Les résultats traduits portent la mention IA/AI.

Un seul travail s’exécute à la fois ; chaque recherche prépare au maximum deux releases. Le budget persistant est de 10 tentatives par jour UTC par défaut (réglable de 1 à 100), avec une heure avant nouvelle tentative après échec. Chaque SRT est limité à 512 Kio ; la préparation s’arrête lorsque le cache atteint 512 Mio. Les limites et éventuels frais du fournisseur restent applicables. Le cache contient du texte et les noms des releases, pas les fichiers vidéo.

À la lecture dans Remix, seul le SRT associé à la release sélectionnée est ajouté aux pistes. Si sa préparation commence pendant la lecture, relancez celle-ci après la fin du travail. Les filtres de qualité/audio restent actifs : un profil imposant une piste audio française peut donc exclure une VO malgré ses sous-titres. L’indexation d’une série prépare son épisode représentatif ; elle ne valide pas tous ses épisodes.

À la lecture, une release dont le SRT est déjà prêt passe devant les releases de même niveau : même qualité, même langue audio acceptée et même score de formats personnalisés. Elle l’emporte alors sur une release d’un indexeur prioritaire, avec plus de seeders ou un fichier plus gros. Une préférence explicite pour Usenet ou le debrid reste appliquée avant elle. Une release de meilleure qualité, dans une langue mieux acceptée ou avec un meilleur score reste prioritaire, même sans SRT ; SubMaker la prépare ensuite pour la lecture suivante. La préférence ne joue que si SubMaker est activé et que le titre fait partie du périmètre autorisé. Ether peut encore préférer une release équivalente déjà en cache chez le debrid ou une source plus rapide, afin que la lecture démarre. La vérification se fait en une seule requête par lecture ; le motif apparaît dans le détail du classement.

SeerrCatalog reçoit le SRT via la recherche authentifiée, sans recevoir le manifeste SubMaker. Une traduction en attente ne compte pas comme release disponible ; les titres concernés sont revérifiés après cinq minutes. Deux releases distinctes répondant aux critères valident le média, une seule conserve le parcours J+1/J+3 et la validation manuelle. Une disponibilité acquise reste définitive jusqu’à suppression. Le filtre de résolution et le nombre d’addons exigés restent appliqués.

SeerrCatalog expose les SRT enregistrés par la ressource Stremio `subtitles`, uniquement pour un média disponible et le nom exact de la vidéo. Le client doit transmettre `filename` ; sans ce paramètre, aucun sous-titre n’est proposé. Après mise à jour, réinstallez le manifeste si le client conserve l’ancienne liste de ressources. Supprimer le média retire l’accès aux sous-titres correspondants.

## Validation avant activation réelle

Les tests automatisés utilisent un fournisseur local simulé : cache, budgets, erreurs, release/épisode, disponibilité et distribution Stremio. Un essai réel a validé la traduction anglais → français de deux répliques avec timecodes conservés et la récupération d’un SRT français de 1 263 répliques par le connecteur Remix. La lecture vidéo synchronisée et le parcours complet jusqu’à SeerrCatalog restent à valider avant mise en production. Aucune clé ni configuration n’est nécessaire pour examiner les PR en brouillon.
