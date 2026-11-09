# Contributions à Ether-Remix

- Rédiger les titres, descriptions et commentaires des pull requests en français.
- Toute PR ajoutant une fonctionnalité ou corrigeant un problème important doit, avant publication :
  - actualiser les README français (`README.md`, prioritaire) et anglais (`README.en.md`) ainsi que les guides concernés ; décrire les fonctionnalités de façon concise ;
  - ajouter une entrée datée dans `web/src/pse/RemixUpdates.tsx`, avec un identifiant inédit et `version: "current"` pour la nouvelle note ; figer la note précédente à sa version Docker réellement publiée. Une seule note doit porter `current`. Conserver les identifiants et versions historiques afin de préserver les repères de lecture ;
  - vérifier la version affichée en bas à gauche de la console. Les images officielles injectent automatiquement `<version Ether>-remix.<numéro de build>.<tentative>` via `.github/workflows/pse-docker.yml` ; conserver cette progression et ne pas figer une version dans l’interface ;
  - conserver les deux liens vers les dépôts GitHub Ether d’origine et Ether-Remix dans le pied de menu.
- Régénérer `internal/admin/dist` après une modification de l’interface et exécuter les vérifications adaptées.
- Ne jamais publier les archives, notes de coordination privées ou secrets dans le dépôt.
