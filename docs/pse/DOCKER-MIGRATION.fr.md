# Remix-Media et Docker

Les installations existantes peuvent conserver `ghcr.io/aerya/ether-remix:latest`. Le [miroir de compatibilité](https://github.com/Aerya/remix-docker-mirror) copie les publications de `ghcr.io/remix-media/ether-remix:latest` toutes les 30 minutes environ, selon la disponibilité de GitHub Actions. Il peut aussi être lancé manuellement. Les versions historiques restent à leur ancienne adresse.

Pour changer volontairement, remplacez seulement la ligne `image:` dans Dockge-Enhanced par `ghcr.io/remix-media/ether-remix:latest`, puis récupérez l’image et recréez le conteneur. Conservez exactement les volumes, variables, ports et chemins actuels ; aucune réinstallation ni suppression de données.

Le transfert GitHub conserve les redirections des anciennes URL du code, mais GHCR ne redirige pas les images. Le miroir copie les manifestes amd64 et arm64 sans reconstruire l’image. Une panne du miroir retarde les mises à jour de l’ancienne adresse, sans retirer l’image existante.
