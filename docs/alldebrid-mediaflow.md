# AllDebrid, MediaFlow et Gluetun

## Installation

MediaFlow est un relais HTTP applicatif, pas un VPN. Pour un VPS, utilisez cette chaîne :

`Remix → MediaFlow → Gluetun → VPN accepté / tunnel domestique → AllDebrid`

Téléchargez le [Compose MediaFlow + Gluetun](../docker-compose.mediaflow-gluetun.yml) et déployez-le comme une stack distincte dans Dockge-Enhanced. Pour une nouvelle installation de Remix, utilisez le [Compose Remix](../docker-compose.example.yml). Placez le profil WireGuard dans `wg0.conf` (permissions `600`) et définissez `MEDIAFLOW_API_PASSWORD` dans le fichier `.env`. Pour un fournisseur VPN, adaptez Gluetun selon sa documentation. Le profil doit autoriser la route Internet ; ne publiez jamais sa clé privée.

Dans le dossier de la stack MediaFlow + Gluetun, créez un fichier `.env` (ou utilisez l’onglet environnement de Dockge) :

```dotenv
MEDIAFLOW_API_PASSWORD=remplacez-par-un-long-mot-de-passe-unique
```

Le Compose place MediaFlow dans le réseau de Gluetun. MediaFlow écoute sur le port interne **8889**, et le proxy HTTP Gluetun sur **8888**. Aucun port n’est publié sur l’hôte. Le montage `wg0.conf` doit être un fichier existant, jamais un dossier. Déployez la stack après avoir créé le réseau `media` ci-dessous.

MediaFlow et Gluetun sont deux services externes : Remix ne les embarque pas. Si vous les avez déjà installés, réutilisez-les au lieu de créer une seconde instance.

Créez une seule fois le réseau privé avec `docker network create media`. Dans le Compose de Remix, ajoutez `media` aux réseaux du service existant, en conservant ses autres réseaux :

```yaml
services:
  remix: # utilisez le nom réel de votre service Remix
    networks: [default, media]
networks:
  default: {}
  media:
    external: true
    name: media
```

Redéployez la stack Remix dans votre gestionnaire Docker. La stack MediaFlow + Gluetun ne publie aucun port sur l’hôte : configurez `http://gluetun:8889` dans Remix. Le proxy HTTP Gluetun écoute sur `http://gluetun:8888` pour les outils sans prise en charge de MediaFlow. Pour une instance distante, utilisez HTTPS et une authentification ; ne rendez pas ce relais public sans protection. Adaptez le DNS de Gluetun à votre réseau, notamment pour conserver votre filtrage domestique.

L’option **Sources → Debrid → Utiliser MediaFlow pour AllDebrid** exige l’adresse et le mot de passe API du relais. Le mot de passe est masqué dans la console et protégé comme les autres identifiants. L’option reste propre à chaque nœud : configurez aussi les nœuds qui effectuent les téléchargements.

## Proxy existant (SSDV2, HTTP ou SOCKS5)

MediaFlow est facultatif si vous disposez déjà d’un proxy HTTP/HTTPS/SOCKS5 qui sort par votre VPN ou votre domicile. Dans **Réglages → Général → Proxy pour les sources**, collez l’URL complète fournie par votre gestionnaire. Remix sépare automatiquement l’adresse, le nom d’utilisateur et le mot de passe et ouvre **Plus d’options** pour vérification. Enregistrez puis testez la connexion. La saisie manuelle des trois champs reste disponible ; les identifiants ne sont pas conservés dans l’adresse.

Le conteneur Remix doit pouvoir joindre le proxy, notamment via un réseau Docker commun pour un nom de service interne. Activez **Services et flux Debrid**, ainsi que **Addons Stremio** pour les flux issus des addons. Sans proxy spécifique à la source, l’option MediaFlow AllDebrid, si activée, reste prioritaire pour les appels qu’elle prend en charge : laissez-la désactivée si vous choisissez uniquement le proxy SSDV2. Un Worker qui relaie seulement l’API n’assure pas le transport de la vidéo.

## Vérifications

1. Vérifiez l’IP de sortie de Gluetun puis celle de MediaFlow (`/proxy/ip`, authentification `api_password` dans l’en-tête). Elles doivent correspondre à la sortie choisie.
2. Consultez [les fournisseurs VPN proposés dans votre compte AllDebrid](https://alldebrid.fr/account/). [AirVPN — lien affilié](https://airvpn.org/?referred_by=483746) est une possibilité, sans garantie sur chaque serveur/IP.
3. Activez et enregistrez l’option dans Remix, puis utilisez **Tester AllDebrid via MediaFlow** dans ce même panneau (un compte AllDebrid actif est nécessaire). Le test appelle l’API du compte via le relais ; il ne certifie pas son IP de sortie. Vérifiez ensuite une lecture et un déplacement dans la vidéo.
4. Arrêtez temporairement MediaFlow : les requêtes AllDebrid de Remix doivent échouer sans sortir directement. Vérifiez aussi l’absence de sortie directe quand le tunnel Gluetun tombe.
5. Configurez les autres clients AllDebrid, workers et scripts de la machine sur la même sortie. Un proxy défini seulement dans Remix ne protège pas les autres applications.

## Limites

AllDebrid peut refuser les IP de serveurs/datacenters et certaines sorties VPN (dont WARP). Des accès assimilés à du partage peuvent entraîner une suspension ou un bannissement. MediaFlow ne garantit pas l’acceptation d’une IP ni le rétablissement d’un compte.

Remix relaie son API AllDebrid native et les flux identifiés AllDebrid (fournisseur ou domaines connus). Un addon externe qui contacte lui-même AllDebrid reste responsable de sa sortie. Les URL opaques d’un autre relais ne peuvent pas toujours être attribuées à AllDebrid.

MediaFlow est une instance de confiance : elle reçoit les destinations et identifiants nécessaires aux appels amont. Utilisez une version incluant `/proxy/forward` et les jetons chiffrés, gardez-la à jour et évitez la journalisation des URL/requêtes. Les jetons expirent après cinq minutes ; la lecture déjà ouverte continue. Aucun filtrage d’IP AllDebrid n’est nécessaire.

## English

MediaFlow is not bundled with Remix. Download the [Remix Compose](../docker-compose.example.yml) and the separate [MediaFlow + Gluetun Compose](../docker-compose.mediaflow-gluetun.yml). Before deployment, create the external network with `docker network create media`, provide an existing `wg0.conf` file and set `MEDIAFLOW_API_PASSWORD` in `.env`. Attach Remix to the private `media` network, set the relay URL to `http://gluetun:8889` and enter its API password under **Sources → Debrid**. Other tools can use Gluetun’s HTTP proxy on port 8888. Preserve your intended DNS settings. Do not expose the relay without authentication; use HTTPS for remote access.

Check both exit IPs, then test your account and playback. An unavailable relay must cause an error, never direct fallback. Configure every AllDebrid tool and background job on the same exit. Hosted addons make their own upstream requests; opaque third-party relay URLs cannot always be identified. MediaFlow requires a version supporting `/proxy/forward` and encrypted tokens. It is a trusted service receiving upstream credentials; avoid request/URL logging.

## Références

- [AllDebrid : problèmes de téléchargement](https://help.alldebrid.com/fr/faq/download-issue)
- [MediaFlow : relais des appels API](https://github.com/mhdzumair/mediaflow-proxy/blob/main/docs/usage/forward.md)
- [Gluetun : profil WireGuard personnalisé](https://github.com/qdm12/gluetun-wiki/blob/main/setup/providers/custom.md)


## Proxy par compte débrideur et addon Stremio

Dans **Sources → Debrid → Modifier**, dépliez **Proxy du compte · API et lecture**. Activez le proxy spécifique et collez son URL HTTP, HTTPS ou SOCKS5 : les identifiants sont répartis dans les champs dédiés. Enregistrez ; le bouton Tester du compte utilise aussi ce proxy. Un test réussi du compte confirme l’accès API, pas une lecture complète.

Dans **Sources → Stremio → Modifier**, deux blocs indépendants permettent de définir :

- **Proxy des requêtes** : récupération du manifeste, recherches et récupération des NZB de cet addon. Le bouton Tester de l’addon utilise cette route.
- **Proxy de lecture** : sondage et récupération des flux HTTP par Remix, y compris les redirections et les requêtes partielles de lecture. Pour un résultat torrent résolu par un compte débrideur Remix, c’est le proxy du compte qui s’applique.

Chaque bloc est replié par défaut. Désactivé, il hérite du routage actuel. Activé, son proxy prend priorité sur MediaFlow et le proxy général, même pour une destination locale ; les exceptions du proxy global ne s’appliquent pas. Une adresse absente, un proxy indisponible ou une authentification refusée ne déclenchent aucun repli direct. Le proxy doit pouvoir joindre les destinations voulues et Remix doit pouvoir joindre le proxy.

Les identifiants sont chiffrés dans la configuration et les mots de passe masqués dans l’API. Changer d’adresse ou d’utilisateur exige de ressaisir un mot de passe précédemment masqué. Les nouvelles lectures utilisent la configuration enregistrée ; un flux déjà ouvert peut terminer ses requêtes en cours avec l’ancien routage. Relancez la lecture après un changement de sortie, notamment si le fournisseur lie ses liens à l’IP.

La musique qui réutilise ces comptes natifs (dont DMM / Real-Debrid) suit également leur proxy.

Ces réglages concernent les comptes natifs et les manifestes ajoutés dans **Sources → Stremio**. Les plugins installés séparément, les comptes propres aux connecteurs musicaux et les appels exécutés par un addon distant ne sont pas reconfigurés. Le VPN reste géré par SSDV2, Gluetun ou votre service externe ; Remix n’installe pas de tunnel. Une URL de proxy HTTP/SOCKS5 et une URL d’API MediaFlow ne sont pas interchangeables.


## Priorité, quotas et reprises de lecture

Les références de fichiers Alkodiques vérifiées restent soumises au profil de lecture. `resolver.verifiedFiles` choisit leur priorité : `first` (par défaut), `tie` ou `off`. `resolver.failoverAttempts` limite les essais de lecture de 1 à 10 (4 par défaut).

Un quota journalier suspend le compte concerné pendant une heure ; les comptes de secours restent utilisables. Les refus AllDebrid `MAGNET_TOO_MANY` répétés suspendent seulement les nouveaux ajouts : les torrents existants et liens fichiers restent utilisables. Un hash inconnu ne devient pas un lien fichier.

Dans **Sources → Debrid**, **Garder le lien et réessayer** (`debrid.playbackRelink: "replay"`) conserve le lien lors des erreurs du serveur média. Les liens morts sont toujours renouvelés, au plus une fois toutes les 30 secondes par fichier dans ce mode. Le réglage s’applique sans redémarrage.

### Priority, quotas and playback recovery

Verified Alkodiques file references must still meet the playback profile. `resolver.verifiedFiles` selects their priority: `first` (default), `tie` or `off`. `resolver.failoverAttempts` caps playback attempts from 1 to 10 (default 4).

A daily quota pauses the affected account for one hour; backup accounts remain available. Repeated AllDebrid `MAGNET_TOO_MANY` refusals only pause new additions: existing torrents and file links remain usable. An unknown hash does not become a file link.

Under **Sources → Debrid**, **Keep the link and retry** (`debrid.playbackRelink: "replay"`) keeps the link during media-server errors. Dead links are still renewed, at most once every 30 seconds per file in this mode. The setting applies without a restart.
