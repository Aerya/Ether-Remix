# Assistant de configuration Remix

## Tester la branche BETA

La version stable conserve l’ancien assistant. Ce parcours est réservé à la branche `BETA` et à l’image `ghcr.io/remix-media/ether-remix:beta` (amd64 et arm64). Le workflow BETA ne publie jamais `latest`. Dans Dockge-Enhanced, créez une instance de test avec un dossier de données distinct (ou une copie à froid de vos données) et un port différent. Deux instances ne doivent pas écrire dans la même base. Ouvrez `/ether/remix/assistant` pour commencer.


L’assistant remplace le démarrage rapide en quatre étapes. Ouvrez **Remix → Accueil → Relancer l’assistant**. Il reste accessible aux utilisateurs avancés et ne crée pas une seconde configuration effective.

## Le parcours

1. **Contenus** : films, séries, animés, musique. Ces intentions orientent les propositions ; décocher un contenu ne supprime ni ne masque une bibliothèque existante.
2. **Langues et disponibilité** : distinguer catalogue français, marqueurs de releases, critères de lecture et sous-titres. Les sections VO/VOSTFR et SubMaker renvoient aux éditeurs de règles ; elles ne sont pas créées automatiquement.
3. **Découverte** : catalogue, JukeBox, catalogues Stremio/Xperience, SeerrCatalog, RSS Catalog et annonces pre. Découvrir un titre ne garantit pas une source de lecture.
4. **Usages** : lecture à la demande, téléchargement ou fichiers existants, cumulables. Les permissions de téléchargement ne sont jamais activées simplement en cochant un usage.
5. **Connexions** : présenter les fournisseurs pertinents avec prérequis et limites. Les liens ouvrent un nouvel onglet pour configurer et tester les services. L’assistant ne conserve pas leurs secrets et ne considère pas un lien ouvert comme une connexion réussie.
6. **Bibliothèques et accueil** : intentions Plex/Jellyfin, accès aux applications et à l’accueil Plex, configuration JukeBox et éditeur de bibliothèques existants. Ces éditeurs ont leur propre enregistrement immédiat. Les bibliothèques à thème restent partagées entre les protocoles ; le choix de client ne crée pas de visibilité par client.
7. **Affiches** : accès aux fournisseurs, cartes de navigation et durée du cache. Les affiches des collections restent un chantier distinct ; aucun style nouveau n’est généré automatiquement.
8. **Examiner et appliquer** : comparer les valeurs globales, lire les avertissements, puis confirmer le lot. Les connexions, créations de collections et modifications déjà enregistrées dans d’autres éditeurs ne font pas partie de ce lot.

Les intentions non sensibles sont mémorisées dans ce navigateur. Les modifications globales non appliquées et le retour du dernier lot sont limités à la session du composant ; recharger la page les perd. Le retour prépare un nouvel aperçu, qui doit lui aussi être confirmé. Les diagnostics et un essai de lecture restent nécessaires.

## Chevauchements identifiés

| Options | Relation réelle |
|---|---|
| Langues et qualité / Lecture → Qualité | Deux accès au même éditeur, pas deux configurations. |
| Catalogue français / disponibilité | Filtres cumulés. Sortie en France et audio français sont différents. |
| Profils / critères simples | Les profils remplacent les critères simples ; le seuil minimum reste actif. Les préférences audio du lecteur s’ajoutent. |
| Règles globales / type / bibliothèque / collection | Portées distinctes : une règle locale ne doit pas être interprétée comme une préférence globale. Vérifier le mode de remplacement des règles. |
| VO, sous-titres et audio FR obligatoire | SubMaker ne rend pas compatible une release rejetée pour sa langue audio. Une traduction en attente n’est pas encore disponible. |
| Pre / catalogue lisible | Les annonces comptent actuellement dans les releases indexées sans fournir de lien. L’assistant avertit ; il ne modifie pas ce comportement historique. |
| RSS pre / addon RSS Catalog | Les premiers importent des annonces de releases ; le second fournit des listes de découverte. Ils ne sont pas interchangeables. |
| SeerrCatalog / connexion et partage Seerr | Plusieurs rôles distincts ; ne pas les supprimer comme doublons sans vérifier leurs usages. |
| Ordre rapide / priorités avancées | L’ordre rapide concerne certaines sources vidéo ; il ne représente pas tous les chemins locaux ou musicaux. |

## API commune et périmètre livré

Les routes authentifiées `GET /ether/api/pse/setup`, `POST /ether/api/pse/setup/preview` et `POST /ether/api/pse/setup/apply` lisent l’état public, calculent un aperçu puis appliquent un lot validé. Le client transmet `revision`, `values`, puis le `plan` de l’aperçu. Une révision périmée ou un plan différent produit un conflit, sans écriture. `null` restaure l’héritage de la variable d’environnement ; les réglages hors lot sont préservés.

Le périmètre est volontairement borné : `PSE_FRENCH`, `PSE_FRENCH_RELEASES`, `PSE_PLAYABLE`, `PSE_PLAYABLE_MIN`, `PSE_PLAYABLE_SINGLE`, `PSE_PLAYABLE_PENDING`, `PSE_INDEXER`, `PSE_PREDB`, `PSE_LIBRARY_SORT`, `PSE_POSTER_REFRESH_HOURS`. Aucun secret, manifeste, réglage réseau, activation de fournisseur ou permission de téléchargement n’est accepté.

Cette API est une base pour de futurs outils MCP. Aucun connecteur Claude, ChatGPT ou Codex n’est livré ici. Il reste à définir les permissions spécifiques, la gestion des secrets, les brouillons durables, la planification des bibliothèques et les règles par contenu/client. Les comptes et offres des fournisseurs d’IA seront à vérifier lors de cette intégration.
