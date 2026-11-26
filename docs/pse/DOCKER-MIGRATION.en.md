# Remix-Media et Docker

Existing installations may keep `ghcr.io/aerya/ether-remix:latest`. The [compatibility mirror](https://github.com/Aerya/remix-docker-mirror) copies releases from `ghcr.io/remix-media/ether-remix:latest` approximately every 30 minutes, subject to GitHub Actions scheduling. It also supports manual dispatch. Historical versions remain at their old address.

To switch voluntarily, change only the `image:` line in Dockge-Enhanced to `ghcr.io/remix-media/ether-remix:latest`, pull the image and recreate the container. Keep all volumes, environment variables, ports and paths unchanged; no reinstallation or data deletion is required.

GitHub redirects old repository URLs, but GHCR does not redirect images. The mirror copies amd64 and arm64 manifests without rebuilding. A mirror outage delays updates at the historical address without removing the existing image.
