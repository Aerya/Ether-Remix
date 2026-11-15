# AllDebrid, MediaFlow et Gluetun

## Installation

MediaFlow est un relais HTTP applicatif, pas un VPN. Pour un VPS, utilisez cette chaîne :

`Remix → MediaFlow → Gluetun → VPN accepté / tunnel domestique → AllDebrid`

Déployez la pile ci-dessous dans votre gestionnaire Docker. Placez le profil WireGuard dans `wg0.conf` (permissions `600`) et définissez `MEDIAFLOW_API_PASSWORD` dans le fichier `.env`. Pour un fournisseur VPN, adaptez Gluetun selon sa documentation. Le profil doit autoriser la route Internet ; ne publiez jamais sa clé privée.

```yaml
services:
  gluetun:
    image: qmcgaw/gluetun:latest
    restart: unless-stopped
    cap_add: [NET_ADMIN]
    devices: [/dev/net/tun:/dev/net/tun]
    volumes:
      - ./wg0.conf:/gluetun/wireguard/wg0.conf:ro
    environment:
      VPN_SERVICE_PROVIDER: custom
      VPN_TYPE: wireguard
      HTTPPROXY: "on"
      HTTPPROXY_LOG: "off"
      FIREWALL_INPUT_PORTS: "8888,8889"
    networks: [media]
  mediaflow:
    image: mhdzumair/mediaflow-proxy:latest
    restart: unless-stopped
    network_mode: service:gluetun
    depends_on:
      gluetun:
        condition: service_healthy
    environment:
      API_PASSWORD: ${MEDIAFLOW_API_PASSWORD:?Définissez un mot de passe}
      PORT: "8889"
networks:
  media:
    external: true
    name: media
```

Créez le réseau privé `media` et raccordez-y Remix. Aucun port n’est publié sur l’hôte : configurez `http://gluetun:8889` dans Remix. Le proxy HTTP Gluetun écoute sur `http://gluetun:8888` pour les outils sans prise en charge de MediaFlow. Pour une instance distante, utilisez HTTPS et une authentification ; ne rendez pas ce relais public sans protection. Adaptez le DNS de Gluetun à votre réseau, notamment pour conserver votre filtrage domestique.

L’option **Sources → Debrid → Utiliser MediaFlow pour AllDebrid** exige l’adresse et le mot de passe API du relais. Le mot de passe est masqué dans la console et protégé comme les autres identifiants. L’option reste propre à chaque nœud : configurez aussi les nœuds qui effectuent les téléchargements.

## Vérifications

1. Vérifiez l’IP de sortie de Gluetun puis celle de MediaFlow (`/proxy/ip`, authentification `api_password` dans l’en-tête). Elles doivent correspondre à la sortie choisie.
2. Consultez [les fournisseurs VPN proposés dans votre compte AllDebrid](https://alldebrid.fr/account/). [AirVPN — lien affilié](https://airvpn.org/?referred_by=483746) est une possibilité, sans garantie sur chaque serveur/IP.
3. Activez l’option dans Remix, puis utilisez **Tester** sur le compte AllDebrid. Vérifiez ensuite une lecture et un déplacement dans la vidéo.
4. Arrêtez temporairement MediaFlow : les requêtes AllDebrid de Remix doivent échouer sans sortir directement. Vérifiez aussi l’absence de sortie directe quand le tunnel Gluetun tombe.
5. Configurez les autres clients AllDebrid, workers et scripts de la machine sur la même sortie. Un proxy défini seulement dans Remix ne protège pas les autres applications.

## Limites

AllDebrid peut refuser les IP de serveurs/datacenters et certaines sorties VPN (dont WARP). Des accès assimilés à du partage peuvent entraîner une suspension ou un bannissement. MediaFlow ne garantit pas l’acceptation d’une IP ni le rétablissement d’un compte.

Remix relaie son API AllDebrid native et les flux identifiés AllDebrid (fournisseur ou domaines connus). Un addon externe qui contacte lui-même AllDebrid reste responsable de sa sortie. Les URL opaques d’un autre relais ne peuvent pas toujours être attribuées à AllDebrid.

MediaFlow est une instance de confiance : elle reçoit les destinations et identifiants nécessaires aux appels amont. Utilisez une version incluant `/proxy/forward` et les jetons chiffrés, gardez-la à jour et évitez la journalisation des URL/requêtes. Les jetons expirent après cinq minutes ; la lecture déjà ouverte continue. Aucun filtrage d’IP AllDebrid n’est nécessaire.

## English

Deploy MediaFlow behind Gluetun using the example above. Attach Remix to the private `media` network, set the relay URL to `http://gluetun:8889` and enter its API password under **Sources → Debrid**. Other tools can use Gluetun’s HTTP proxy on port 8888. Preserve your intended DNS settings. Do not expose the relay without authentication; use HTTPS for remote access.

Check both exit IPs, then test your account and playback. An unavailable relay must cause an error, never direct fallback. Configure every AllDebrid tool and background job on the same exit. Hosted addons make their own upstream requests; opaque third-party relay URLs cannot always be identified. MediaFlow requires a version supporting `/proxy/forward` and encrypted tokens. It is a trusted service receiving upstream credentials; avoid request/URL logging.

## Références

- [AllDebrid : problèmes de téléchargement](https://help.alldebrid.com/fr/faq/download-issue)
- [MediaFlow : relais des appels API](https://github.com/mhdzumair/mediaflow-proxy/blob/main/docs/usage/forward.md)
- [Gluetun : profil WireGuard personnalisé](https://github.com/qdm12/gluetun-wiki/blob/main/setup/providers/custom.md)
