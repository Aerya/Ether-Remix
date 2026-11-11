# Guide de configuration d’Ether-Remix

[Présentation d’Ether-Remix](../../README.md) · [English](GUIDE.en.md)

## Installation

L’image Docker est publique. Aucune connexion à GHCR ni aucun jeton GitHub ne sont nécessaires pour la télécharger.

```yaml
# compose.yaml — à déployer via Dockge-Enhanced
services:
  pse:
    image: ghcr.io/aerya/ether-remix:latest
    container_name: accent
    restart: unless-stopped
    network_mode: host            # découverte Plex sur le réseau local (GDM) et UPnP
    environment:
      - TZ=Europe/Paris
#     - ETHER_PORT=32450          # si un Plex Media Server utilise déjà 32400
    volumes:
      - ./config:/config          # config.json, base de données, identité
      - ./cache:/cache            # cache de segments
#   devices:
#     - /dev/dri:/dev/dri         # transcodage matériel Intel/AMD (facultatif)
```

Ouvrez **http://&lt;hôte&gt;:32400/ether/** (ou le port `ETHER_PORT`) et suivez l'assistant de premier démarrage.

> **Migration depuis Ether :** même format de données. Arrêtez Ether, faites pointer les volumes `/config` et `/cache` d'Ether-Remix vers les dossiers existants, puis démarrez Ether-Remix. Conservez d'abord une copie de `/config`.

> **Annonces de mise à jour :** la console peut signaler de nouvelles versions d'*Ether*. Dans un conteneur, Ether ne se met jamais à jour seul : ignorez l'annonce, les images Ether-Remix suivent la rolling release (mises à jour automatiques de Dockge-Enhanced ou `docker compose pull`).

### Régler Ether-Remix depuis la console

Tous les ajouts se règlent dans la console, menu **Ether-Remix** (`/ether/accent`) : activation, options et état de chaque fonction (titres indexés, titres lisibles, bibliothèque musicale…). Une valeur enregistrée sur cette page remplace la variable d'environnement du même nom et s'applique sans redémarrer ; le bouton ↺ revient à la variable ou à la valeur par défaut. Les variables ci-dessous restent utilisables pour un déploiement entièrement décrit dans le `compose.yaml`.

Ether-Remix accepte toutes les variables d'environnement d'Ether (`ETHER_PORT`, `ETHER_DATA_DIR`, `PUID`/`PGID`…) : voir [docs/install.md](../install.md). Variables propres à Ether-Remix :

| Variable | Défaut | Effet |
| --- | --- | --- |
| `PSE_NOTIFY_LANGUAGE` | `fr` | Langue des notifications du propriétaire. `en` conserve les textes anglais d'Ether. |
| `PSE_INDEXER` | `off` | `on` active l'indexation des releases en arrière-plan. |
| `PSE_INDEXER_SOURCES` | *(tous les addons Stremio activés)* | Identifiants des indexeurs à interroger, séparés par des virgules. Seule façon d'inclure un indexeur Prowlarr/Jackett/NZBHydra2. |
| `PSE_INDEXER_DAILY` | `1000` | Requêtes maximum par source et par jour (UTC). |
| `PSE_INDEXER_PACE` | `10s` | Délai minimum entre deux titres (minimum `2s`). |
| `PSE_INDEXER_TYPES` | `movie,show` | Types indexés (`movie`, `show`). Une série est cherchée via son premier épisode. |
| `PSE_PLAYABLE` | `off` | `on` active le catalogue « lisible uniquement », avec la même portée que `PSE_FRENCH`. |
| `PSE_PLAYABLE_MIN` | `2` | Releases distinctes (même nom = une seule) nécessaires pour lister un titre. |
| `PSE_PLAYABLE_FILTER` | *(aucun)* | Releases prises en compte, ex. `lang=fr,multi;min=1080;codec=hevc,h264;cached=1`. Langues : `fr` (toute VF : FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF, VF2), `vff`, `vfq`/`qc`, `multi`, `en`, `vostfr` (sous-titres français annoncés), `vo` (aucun doublage annoncé)… Changer de filtre recalcule le catalogue au démarrage, sans réindexer. |
| `PSE_FRENCH` | `off` | `on` active le catalogue en français : pages Films et Séries, rangées de l'accueil (dont Tendances), collections et titres similaires. Les collections de sorties à venir, la watchlist et la reprise de lecture restent intactes. |
| `PSE_FRENCH_COUNTRIES` | `FR` | Pays dont une sortie numérique ou physique compte (codes ISO). `FR,CA` ajoute le Canada, pas toujours doublé en français. |
| `PSE_FRENCH_RELEASES` | `french,multi` | Langues de release indexée qui comptent (`vfq` pour le québécois seul, `vff`…). |
| `PSE_FRENCH_TYPES` | `movie,show` | Types filtrés. `movie` laisse les séries intactes. |
| `PSE_SCROB_VIEWS` | `off` | `on` ajoute les bibliothèques « Vus et notés » (visibles aussi dans les autres applications Jellyfin/Emby). |
| `PSE_SCROB_PUSH` | `off` | `on` envoie les lectures aux webhooks Scrob réglés par spectateur (Services → Suivi du visionnage → Scrob). |
| `PSE_MUSIC_ALLDEBRID` | `off` | `on` : lecture depuis le compte AllDebrid configuré dans Ether. Au lancement d'un titre, l'album est cherché sur Prowlarr ou les indexeurs torrent d'Ether, ouvert sur AllDebrid seulement s'il y est déjà en cache, puis lu en flux. Rien n'est stocké sur disque ; un torrent hors cache est retiré aussitôt. |
| `PSE_MUSIC_DMM` | `off` | `on` : cherche les albums dans le catalogue public DMM et les lit via les comptes Real-Debrid existants, sans indexeur ni Prowlarr. |
| `PSE_MUSIC_REALDEBRID` | `off` | `on` : utilise le compte Real-Debrid existant et les indexeurs pour lire les pistes des albums disponibles. Sélection audio complète sur les nouveaux ajouts ; aucun changement de sélection des torrents déjà présents. |
| `PSE_MUSIC_TORBOX` | `off` | `on` : utilise le compte TorBox existant, avec ajout uniquement si déjà en cache et liens de lecture propres à chaque piste. |
| `PSE_MUSIC_AUDIUS` | `off` | Source MP3 publique sans compte ni clé : `first` avant les addons Eclipse, `last` en repli après Eclipse et les comptes debrid, `off` désactivée. Uniquement les enregistrements correspondant au catalogue Deezer. Prioritaire sur le mode HiFi API. |
| `PSE_MUSIC_ECLIPSE_URL` | *(vide)* | Lien `manifest.json` d'un addon Eclipse, généré sur le site de l'addon (comme pour Eclipse ou Remux). Plusieurs liens séparés par des espaces sont essayés dans l'ordre. Facultatif si une autre source musicale est activée. Le lien contient un jeton : la console le masque. |
| `PSE_MUSIC_HIFI_URL` | *(vide)* | Mode alternatif, utilisé seulement sans Eclipse, Audius ni source debrid musicale activée : URL d'une HiFi API (`https://…`, identifiants HTTP Basic possibles). |
| `PSE_MUSIC_QUALITY` | `LOSSLESS` | Qualité demandée à la HiFi API : `LOSSLESS`, `HI_RES_LOSSLESS`, `HIGH` ou `LOW`. Avec un addon Eclipse, la qualité se règle sur le site de l'addon. |
| `PSE_MUSIC_COVERS_URL` | `https://resources.tidal.com/images` | Origine des pochettes de la HiFi API. Avec un addon Eclipse, les pochettes viennent de Deezer. |
| `PSE_LASTFM_API_KEY` | *(vide)* | Clé API Last.fm (gratuite, last.fm/api/account/create) pour importer les titres aimés et les tops Last.fm des spectateurs. Masquée dans la console. |
| `PSE_LASTFM_SECRET` | *(vide)* | Secret partagé Last.fm (même page que la clé API). Avec la clé, permet d'envoyer les écoutes des spectateurs à Last.fm (scrobbling). Masqué dans la console. |
| `PSE_MUSIC_PLAYLISTS_HOURS` | `6` | Cadence de réimport des playlists Deezer et Last.fm, de 1 à 168 heures. |
| `PSE_MUSIC_CHARTS` | `FR,WW` | Tops Deezer ajoutés en playlists pour chaque spectateur : FR, WW (monde), US, UK, BE, CA, DE, ES, IT, BR, NL, NG. Vide : aucun. |
| `PSE_MUSIC_DISCOVER` | `on` | Playlist « Découvertes » par spectateur : une cinquantaine de titres d'artistes proches de ses écoutes et de ses playlists. |
| `PSE_PLAYABLE_PENDING` | `hide` | `show` garde visibles les titres pas encore indexés (pratique pendant la première indexation). |

### Connecter Scrob

1. Dans **Ether-Remix → Notifications et suivi → Scrob**, créez les identifiants Jellyfin du spectateur.
2. Dans Scrob → **Connections → Media Players → Add Connection**, choisissez **Jellyfin** et copiez **Server URL**, **Token** et **User ID**. Cliquez sur **Test**, puis **Add**. Le jeton est limité à ce spectateur et révocable dans ses appareils.
3. Choisissez les bibliothèques à synchroniser dans cette connexion. **Films** et **Séries** fonctionnent directement. L’option avancée Remix « Proposer des bibliothèques limitées à l’historique » ajoute **Vus et notés — Films/Séries** pour limiter l’import aux titres de l’historique.
4. Choisissez **SERVER → SCROB**, **SCROB → SERVER**, ou les deux, et les fréquences **Pull/Push** dans Scrob. Le retour vers Remix concerne les statuts vus/non vus et les notes, pas les positions de reprise. Il ne nécessite aucun interrupteur ni webhook dans Remix.
5. Facultatif : pour envoyer aussi les événements de lecture en temps réel, copiez le webhook de **cette même connexion** dans le panneau repliable Remix et activez « Envoi immédiat des événements de lecture » dans les options avancées. Sans webhook, la synchronisation programmée fonctionne. **Tester** dans ce panneau ne teste que le webhook. Les tests et les envois attendent jusqu’à 60 secondes sur une connexion lente ; une adresse inaccessible peut toujours échouer.

L’erreur Scrob **Failed to connect to Jellyfin or invalid User ID** peut également signaler un HTTP 403 du proxy ou du pare-feu avant que Remix reçoive le jeton. Vérifiez depuis le serveur Scrob les réponses de `/jellyfin/System/Info` et `/jellyfin/Users/{UserId}` avec le jeton, ainsi que les journaux du proxy. Une synchronisation peut parcourir beaucoup d’URLs : un système anti-exploration doit distinguer les lectures authentifiées réussies des tentatives d’accès refusées.

Le panneau actualise automatiquement le dernier accès Jellyfin du spectateur (Scrob ou autre client) et l’état du webhook, sans recharger la page ni effacer les champs en cours de saisie. Un accès API ne prouve pas la fin d’une synchronisation. **Trigger Library Scan** demande un rafraîchissement à Remix : son catalogue étant virtuel, la demande est acquittée immédiatement sans scan de fichiers ni réindexation globale. Les listes d’épisodes sans tri explicite utilisent le catalogue stocké et paginé lorsque l’ordre des épisodes n’est pas personnalisé. Utilisez **Pull** pour importer les données dans Scrob et **Push** pour les renvoyer à Remix.

### Connecter un client musique (Subsonic)

1. Dans **Console → Ether-Remix → Sources → Streaming musical**, activez les comptes souhaités : **AllDebrid**, **Real-Debrid** et/ou **TorBox**, ou collez le lien manifest d’un addon Eclipse. Les clés et les indexeurs sont ceux déjà configurés dans Ether-Remix. **Tester** vérifie le compte puis cherche un lien (sans vérifier l’audio) ; **Tester les sources** ouvre les premiers octets audio pour constater le format. Avec plusieurs comptes d’un même service dans **Sources › Debrid**, ils sont essayés dans l’ordre de priorité.
2. Dans le client, ajoutez un serveur **Subsonic / OpenSubsonic** : URL `http://<hôte>:32400` (ou le port `ETHER_PORT`), identifiants de votre compte Ether-Remix (comptes locaux à mot de passe).
3. Activez l'option **mot de passe en clair / authentification legacy** du client : Ether-Remix ne conserve que des hachages, l'authentification par jeton (`t`/`s`) est refusée (code 41). Utilisez HTTPS (proxy inverse) hors de votre réseau local.

Les services debrid sont essayés après Eclipse, selon leur priorité dans **Sources → Debrid** (le compte actif le plus prioritaire détermine le rang du service, puis tous ses comptes actifs sont essayés dans leur ordre de priorité). Les changements de comptes ou de priorité renouvellent automatiquement les résolveurs et leurs caches. Audius conserve son placement avant les sources ou en repli. Les fichiers sont relayés sans stockage local ni conversion : FLAC/Hi-Res seulement si le fichier disponible l’est réellement.

TorBox impose l’ajout uniquement si déjà en cache. Real-Debrid ne propose plus de vérification préalable de cache : un nouvel ajout peut brièvement démarrer un téléchargement côté service avant son retrait s’il n’est pas prêt. Les torrents préexistants ne sont pas supprimés. Un album absent n’est pas téléchargé localement. La validation automatisée utilise des API simulées ; aucune lecture réelle Real-Debrid/TorBox ni disponibilité Hi-Res n’est certifiée par ces tests.

**Clients Jellyfin et Plex** (Jellyfin web, Finamp, Findroid, Streamyfin, Plexamp, Plex Web…) : rien à régler, une bibliothèque **Musique** apparaît à côté des autres dès qu’une source musicale est activée. Elle montre vos favoris et écoutes, puis les classements Deezer ; la recherche couvre tout le catalogue et la lecture est directe. La recherche globale de Plex montre aussi artistes, albums et titres.

**Playlists Deezer et Last.fm** : dans **Console → Ether-Remix → Sources → Streaming musical**, collez pour chaque spectateur le lien de son profil Deezer (ses playlists publiques, dont Coups de cœur) ou des liens de playlists Deezer, y compris les liens de partage de l'application (`link.deezer.com`), et/ou son nom d'utilisateur Last.fm (titres aimés, top du mois, top de tous les temps, avec la clé API Last.fm). Les playlists apparaissent dans Plexamp, Plex Web, Finamp, Jellyfin et les clients Subsonic, en lecture seule, et sont réimportées toutes les 6 heures (`PSE_MUSIC_PLAYLISTS_HOURS`). Rien n'est téléchargé : seules les références des titres sont gardées. Pour Spotify et les imports manuels, voir [Playlists et apparence](#playlists-et-apparence).

**Playlists ListenBrainz** : même écran, nom d'utilisateur ListenBrainz (gratuit, sans clé API). Ether-Remix importe la dernière playlist de chaque série que ListenBrainz génère pour lui (Daily Jams, Weekly Jams, Weekly Exploration…) et ses playlists publiques, en respectant la limite de débit de ListenBrainz.

**Playlists personnelles et automatiques** : depuis un client Subsonic (Symfonium, Feishin, Supersonic, Tempo, DSub…), chaque spectateur crée, renomme, complète, réordonne et supprime ses propres playlists ; elles gardent les références des titres et ne dépendent d'aucun fournisseur audio. Quatre playlists automatiques s'y ajoutent, calculées à partir de ses favoris et de ses écoutes : **Titres favoris**, **Écoutés récemment**, **Les plus écoutés**, **Favoris peu écoutés**. Les playlists importées et automatiques restent en lecture seule. Plex et Jellyfin les affichent ; leur édition passe par un client Subsonic.

**Correspondance et repli** : Eclipse et Audius comparent les enregistrements par ISRC ou par titre, artiste et durée, avec rejet des versions live, remix, reprises, acoustiques ou karaoké non demandées. Les sources debrid recherchent l’album puis rapprochent les noms et numéros de pistes ; elles ne vérifient pas l’ISRC du fichier ni son master. Si un lien audio est expiré ou refusé, Ether-Remix passe à la source suivante (jusqu'à trois). Les liens audio visant la machine elle-même ou des adresses réservées sont refusés.

**Nouveautés et test des sources** : la playlist automatique **Nouveautés de vos artistes** réunit les albums sortis ces 4 derniers mois par les artistes favoris et les plus écoutés du spectateur (calculée en arrière-plan, mise à jour toutes les 6 h). Dans la console, Sources → Streaming musical, **Tester les sources audio** essaie chaque source réglée, seule, sur le même titre et affiche le format réellement envoyé (FLAC, MP3…), pas celui annoncé, ou la raison de l'échec. Il montre l'ordre réel de lecture, la raison de chaque source inactive et, pour chaque source, les étapes franchies : titre trouvé au catalogue, résultat de la source, lien obtenu, premiers octets vérifiés. La lecture complète n'est pas testée. **Réessayer cette source** relance une seule source, sans cache. Les liens d'addons ne sont affichés que par leur hôte ; aucune clé ni lien audio n'est affiché.

**Titres en VOSTFR à la demande** : dans **Ether-Remix → VOSTFR à la demande**, cherchez un film ou une série et ajoutez-le. Il apparaît dans Films et Séries même sans VF, et dans la bibliothèque **« VOSTFR »** de Plex et Jellyfin (créée au premier ajout).

**Découvrir de la musique** : chaque spectateur reçoit les playlists **Top France**, **Top Monde**… (pays au choix dans Console → Ether-Remix → Sources → Streaming musical) et **Découvertes**, renouvelées toutes les 12 heures. Les **styles** (Pop, Rap/Hip Hop, Rock, Électro, Jazz…) se parcourent dans Plex (Musique › Par style), Jellyfin (Genres) et Feishin (Genres), avec les albums, artistes et titres du moment. Sur la page d'un artiste : **artistes similaires** et radio « Dans le même esprit » (Plex), **Mix instantané** (Jellyfin), titres populaires et similaires (Feishin).

**Envoi des écoutes à Last.fm** : avec la clé API et le secret partagé Last.fm, chaque spectateur peut cliquer sur **Connecter Last.fm** dans **Console → Ether-Remix → Sources → Streaming musical**, autoriser Ether-Remix sur last.fm, puis **J'ai autorisé Ether-Remix**. Ses écoutes complètes sont ensuite envoyées toutes les 2 minutes (celles d'avant la liaison et de plus de 14 jours ne partent pas). La **Vue d'ensemble** affiche aussi un bloc **Musique** : écoutes, heures et auditeurs des 30 derniers jours, titres et artistes les plus écoutés.

**Feishin et autres clients Subsonic** : utilisez l'adresse d'Ether-Remix, votre nom d'utilisateur et un mot de passe local (Votre compte › Méthodes de connexion, y compris pour un compte connecté via Plex), et cochez **Enable legacy authentication** : Ether-Remix n'accepte pas l'authentification Subsonic par jeton.

La bibliothèque se remplit avec les favoris (artistes, albums, titres) et les écoutes. La recherche interroge Deezer en direct ; à la lecture, Ether-Remix résout le titre auprès des sources activées et relaie le flux. Les playlists et les genres sont pris en charge. Les paroles et le transcodage audio ne le sont pas : le flux est relayé tel quel.

**Sources audio gratuites :** Audius a été validé en MP3 320 kbit/s sur un morceau complet à travers Ether-Remix. Aucune nouvelle source FLAC/Hi-Res sans compte n’est certifiée. Le diagnostic vérifie les premiers octets et indique profondeur/fréquence pour FLAC ; ce n’est pas un test de lecture complète. [Audit, sources exclues, configuration et limites](AUDIO-SOURCES.fr.md).

### Sources communautaires Eclipse

Dans **Ether-Remix → Sources → Streaming musical → Sources communautaires Eclipse**, ouvrez **Configurer** pour le fournisseur souhaité. Générez son lien, collez le `manifest.json`, puis enregistrez. Un champ vide désactive la source. Pour Internet Archive, choisissez la catégorie **Music**. UltraMax et Deezer Ricky demandent votre compte Deezer sur leur propre site ; Ether-Remix conserve uniquement le lien généré, masqué dans la console.

| Fournisseur | Variable du manifest |
| --- | --- |
| Tido | `PSE_MUSIC_TIDO_URL` |
| Internet Archive | `PSE_MUSIC_INTERNETARCHIVE_URL` |
| UltraMax | `PSE_MUSIC_ULTRAMAX_URL` |
| Deezer Ricky | `PSE_MUSIC_DEEZER_RICKY_URL` |
| SoundCloud Eclipse | `PSE_MUSIC_SOUNDCLOUD_URL` |
| SoundCloud Ricky | `PSE_MUSIC_SOUNDCLOUD_RICKY_URL` |

Les sources dédiées sont essayées dans cet ordre, après les liens Eclipse libres et avant les comptes debrid. Le catalogue reste Deezer ; les titres absents ou sans correspondance fiable ne sont pas lus. **Tester** contrôle le manifest et le catalogue, **Tester les sources** vérifie les premiers octets audio. Ni un manifest valide ni la mention FLAC ne garantissent une lecture complète ou du Hi-Res.

**Suivi des adresses :** `PSE_MUSIC_ADDON_REGISTRY=on` par défaut, consultation du registre public Eclipse toutes les 6 h lorsque la musique est active. Le bouton d’actualisation fonctionne aussi si le suivi automatique est désactivé (limité à une tentative par minute). Les liens publics sont conservés en base pour les redémarrages et les pannes. Un changement par rapport à l’adresse fournie avec Ether-Remix est signalé : vérifiez le fournisseur avant de recréer votre lien. Aucun manifest personnel n’est modifié automatiquement. Les fournisseurs absents du registre gardent leur dernière adresse connue. Pas de surveillance Discord.


### Statistiques et suivi des sources

**Ether-Remix → Stats** sépare le tableau de bord des réglages **Films & séries**. Il regroupe la couverture du catalogue, les graphiques de requêtes sur sept jours (jours UTC), les releases conservées par source, les quotas, erreurs, réponses partielles et durées moyennes.

L’activité de l’indexeur en arrière-plan est actualisée toutes les trois secondes : titre et source interrogés, durée en cours, puis résultat de la recherche. Le journal garde les 50 dernières recherches depuis le démarrage de l’indexeur ; il est réinitialisé lors d’un redémarrage ou d’un changement de réglages relançant l’indexeur. Les compteurs journaliers sont persistants ; les nouvelles mesures d’erreurs et de durées commencent à l’installation de cette version.

Les statistiques par source sont actualisées toutes les trente secondes. Les totaux détaillés du catalogue et des bibliothèques restent un instantané daté, recalculable avec **Actualiser**. Le suivi s’arrête lorsque l’onglet n’est plus affiché et se suspend quand la page est masquée. **Suspendre le suivi** fige l’affichage sans arrêter l’indexation.

Une recherche de titre peut consommer plusieurs requêtes. Les titres communs à plusieurs sources ou bibliothèques ne doivent pas être additionnés ; une release conservée n’est pas une garantie de lecture.


### DMM avec Real-Debrid

Dans **Sources → Debrid**, configurez au moins un compte Real-Debrid actif, puis activez **DMM · Real-Debrid** dans **Ether-Remix → Sources → Streaming musical**. Aucun lien manifest ni compte DMM n'est nécessaire. Le catalogue de navigation reste Deezer ; DMM fournit une recherche supplémentaire des albums à lire.

Cette source ne dépend pas des indexeurs. Elle se place au rang des comptes Real-Debrid, juste avant leur recherche par indexeurs ; les comptes sont essayés dans leur ordre de priorité. Les autres sources activées prennent le relais si l'album ou la piste ne peut pas être lu.

Ether-Remix ne transmet aucune clé à DMM : la recherche est publique, puis les magnets sont résolus directement auprès de Real-Debrid. Les pistes FLAC/MP3 sont sélectionnées ensemble pour retrouver le cache de DMM. Un album non prêt est retiré, avec la même limite Real-Debrid que pour la source par indexeurs : le service peut brièvement commencer un téléchargement avant son retrait. **Tester** cherche un lien ; le diagnostic des sources peut vérifier ses premiers octets audio.


### Historique et comparaison TMDB

Dans **Ether-Remix → Stats**, les totaux TMDB proviennent des exports d’identifiants officiels, téléchargés en arrière-plan chaque jour et conservés en cache avec leur date. Les exports adultes séparés sont exclus. Le premier relevé démarre environ une minute après le lancement du serveur. En cas de panne, le dernier relevé valide reste affiché. Le pourcentage compare la sélection navigable locale à ces listes, pas la disponibilité de flux audio/vidéo.

L’historique propose **Jour** (30 jours), **Semaine** (12 semaines), **Mois** (12 mois) et **Année** (10 années), en UTC. Choisissez les **ajouts au catalogue** ou les **recherches par source**, puis les films, séries, les deux ou le sous-ensemble Animes. Les animés restent inclus dans Films et Séries : les totaux ne les comptent pas deux fois. Le suivi des recherches d’animés commence avec cette version ; les périodes antérieures restent non mesurées. Le filtre source/indexeur s’applique aux recherches de releases. Le tableau détaillé affiche les valeurs par période et cumulées ; le cumul tient compte des mesures antérieures à la fenêtre affichée.

Les ajouts sont reconstruits depuis les dates d’entrée des médias encore conservés dans le catalogue local. Les titres sans date restent explicitement exclus. Le suivi des recherches par source est enregistré durablement à partir de cette mise à jour : une recherche correspond à un titre interrogé auprès d’une source, hors annulations et refus de quota. Un titre peut être recherché plusieurs fois. Les périodes non mesurées apparaissent comme telles.

Chaque ajout dans **Titres en VOSTFR à la demande** place aussi le titre en tête de file de recherche, sans attendre son délai normal. L’indexation doit être active ; les budgets par source et les pauses pendant les lectures restent respectés. Si la mise en file échoue, la sélection VOSTFR est conservée et la console l’indique.

Le bouton **Tester** de DMM utilise désormais un échantillon du catalogue DMM, indépendamment des classements Deezer. Il distingue les étapes catalogue, compte, cache et lien audio. Les logs `component=pse-music source=dmm` donnent les compteurs et les causes d’échec sans enregistrer de clé ni d’URL de lecture.

Dans **Sources → IPTV**, le bouton **Modifier** ouvre le fournisseur sélectionné. La création, l’enregistrement et la fermeture de son formulaire conservent la catégorie IPTV. Les liens provenant de Télévision en direct ouvrent ce même éditeur.

Dans **Sources → Streaming musical**, les réglages sont regroupés par famille : **Eclipse** (manifests et registre), **DMM / Real-Debrid** (catalogue DMM, recherche par indexeurs et comptes partagés), puis **Autres sources** (AllDebrid, TorBox, Audius et HiFi API). Le diagnostic audio teste toutes les familles depuis un bloc distinct.

Dans **Sources → Debrid → Modifier**, **Afficher la clé API** révèle seulement celle du compte sélectionné au propriétaire connecté. **Masquer la clé API** ou la fermeture du formulaire retire cet affichage sans modifier la clé enregistrée.

Dans **Sources → Indexation**, **Sources importées** se filtre et se trie par catégorie, nom ou contenus concernés. **Priorité de recherche** se déplie à la demande.

## Animes

Dans **Ether-Remix → Contenus et bibliothèques → Animes**, créez le modèle Animes pour obtenir les collections populaires et les mieux notées de TMDB, en films et séries. Une bibliothèque mixte apparaît dans Plex en deux sections Films et Séries. La catégorie Animes est conservée après renommage. Les bibliothèques Animes respectent vos filtres de langue, notamment FR/MULTi. La VO/VOSTFR reste un choix explicite, via les exceptions à la demande ou l’option Toutes langues.

Dans **Sources → Stremio**, utilisez **Ajouter un addon**, puis collez le lien `manifest.json` généré chez le fournisseur de votre choix. Aucune sélection de fournisseurs n’est imposée ou suggérée. Les sources, leur activation et leur priorité sont partagées avec Films & Séries. Pour la découverte, utilisez les collections TMDB, les listes AniList ou un addon proposant des catalogues.

Le bouton **Nouveautés**, dans l’en-tête de la console, permet de rouvrir les notes de livraison. La popup ne présente que les notes non lues depuis la dernière version consultée. Fermer ou valider mémorise la version et les notes lues dans ce navigateur ; une reconstruction Docker ne les réaffiche pas. L’ancien repère est repris automatiquement. **Historique complet** permet de consulter les notes précédentes à la demande. La version dans le menu est fournie par le serveur exécuté ; les images officielles incluent un numéro de build Remix automatiquement croissant. Le pied du menu mène aux dépôts Ether et Ether-Remix.

### Exceptions VOSTFR à la demande

**Ether-Remix → VOSTFR à la demande → Titres en VOSTFR à la demande** regroupe les films, séries et animés explicitement autorisés sans VF. Chaque ajout lance une recherche prioritaire si l’indexation est active, dans les limites des quotas et des pauses de lecture. Ces titres figurent aussi dans la bibliothèque VOSTFR. Votre sélection existante est conservée.

### Instances publiques ou auto-hébergement

Les sources musicales identifient les instances publiques et proposent les liens d’auto-hébergement disponibles : [UltraMax Music](https://github.com/PaRaN01a-hash/ultramax-music) et [HiFi API](https://github.com/binimum/hifi-api). Pour Stremio, collez le manifest de votre instance publique ou privée ; aucun fournisseur n’est recommandé. Votre manifest personnel reste votre configuration.

## Organisation des réglages

- **Sources** : accès unique aux comptes, addons Stremio, indexeurs BitTorrent/Usenet, fournisseurs Usenet, IPTV et sources musicales. Les composants et configurations Ether sont conservés. La catégorie Indexation regroupe activation, quotas, rythme, sélection triable des sources et priorités des bibliothèques.
- **Langues et qualité** : catalogue français, langues de release, résolution, codecs, cache et exceptions par collection.
- **VOSTFR à la demande** : sélection explicite de titres sans VF et recherche prioritaire.
- **Contenus et bibliothèques** : films, séries, animés, musique, playlists et bibliothèques personnalisées. Les bibliothèques mixtes restent proposées dans Films et Séries.
- **Statistiques** : indexation Remix, tableaux de bord Sources et Utilisation hérités d’Ether.
- **Notifications et suivi** : notifications, Scrob et identifiants Last.fm.

Les anciennes adresses `/sources/...` et les anciens onglets Remix redirigent vers les nouveaux écrans. Il n’y a pas de copie des comptes ou manifests. Les catégories servent à organiser les sources ; elles ne garantissent pas qu’un titre précis existe chez un fournisseur.

### Navigation et badges des affiches

Plex et Jellyfin préparent les badges d’une page ensemble. Les nouveaux épisodes et les rotations de collections respectent l’état de chaque spectateur. Le catalogue « lisible uniquement » conserve des compteurs exacts, y compris les exceptions de langue.


## Playlists et apparence

Dans **Contenus et bibliothèques → Musique → Playlists** :

- **Découvrir** : tendances et recherche Deezer ; connexion Spotify et liste de vos playlists ; liens vers ListenBrainz, Soundplate et DailyPlaylists pour découvrir d’autres sélections.
- **Mes comptes** : profils Deezer publics, Last.fm et ListenBrainz synchronisés automatiquement (6 h par défaut). Les playlists Deezer privées ne sont pas accessibles par un simple lien de profil.
- **Importer** : lien de playlist Deezer, Spotify ou ListenBrainz, ou fichier CSV/XSPF (2 Mo maximum). CSV : colonnes `title` et `artist`, ou `Track Name` et `Artist Name(s)` pour Exportify ; séparateurs virgule ou point-virgule. Les URL audio des fichiers XSPF ne sont jamais téléchargées.

Choisissez le spectateur, vérifiez l’aperçu, puis validez. Une copie personnelle, éditable via Subsonic, est créée sans remplacer les autres playlists. Ces copies ne sont pas synchronisées automatiquement. L’import traite les 2 000 premiers éléments et affiche la progression ainsi que les titres non retrouvés dans le catalogue. La lecture dépend ensuite de vos sources musicales ; les métadonnées d’une playlist ne garantissent pas un flux audio disponible. Le suivi reste disponible une heure sur le serveur, mais ne survit pas à son redémarrage.

**Spotify** : dans Découvrir → Spotify, saisissez le **Client ID** de votre application. Ajoutez dans le tableau de bord Spotify l’URI de redirection exacte affichée par Remix, puis connectez le compte pour le spectateur choisi. HTTPS est requis hors boucle locale. Aucun Client Secret n’est demandé : connexion OAuth avec PKCE, jetons chiffrés sur le serveur, accès en lecture seule et bouton de déconnexion. En mode développement, le propriétaire de l’application doit avoir Premium et autoriser les utilisateurs dans Spotify Developers. L’accès aux titres peut être refusé pour les playlists dont vous n’êtes ni propriétaire ni collaborateur ; un lien trouvé dans un annuaire ne contourne pas cette limite.

**Apparence** : le bouton en forme de palette dans la barre supérieure propose Ether, Nord, Catppuccin, Solarized, Gruvbox et Rosé Pine. Choisissez séparément le mode clair ou sombre ; l’aperçu est immédiat et le choix est conservé dans ce navigateur. Les autres utilisateurs gardent leurs propres préférences.

## SubMaker

[Configurer les sous-titres externes et la traduction facultative](SUBMAKER.fr.md), globalement ou par bibliothèque/collection, avec SeerrCatalog.

Pour les règles de visibilité et les délais de recherche, voir [Catalogue et indexation](CATALOGUE.fr.md). Pour connecter l’addon, voir [SeerrCatalog](SEERRCATALOG.fr.md).

### Historique commun et Écoute

**Nom affiché** : sur la fiche d’un utilisateur (**Spectateurs**), le crayon à côté de son nom permet de choisir le nom utilisé dans les statistiques, l’activité et les listes. Plex ne l’écrase pas et les lectures déjà enregistrées prennent le nouveau nom. La connexion ne change pas. Un champ vide rétablit le nom Plex.

**Activité → Historique** réunit films, séries et musique dans un tableau chronologique commun, avec recherche, tri, pagination et fiche de détail. La liste contient les 500 dernières lectures de la période sélectionnée. Les écoutes musicales gardent leurs métadonnées artiste/album ; la durée indiquée est celle du morceau, pas le temps réellement écouté. Les mesures de complétion et de durée de session restent explicitement réservées à la vidéo.

**Activité → Écoute** reprend les filtres période, année et utilisateur de Visionnage. Les compteurs, écoutes par jour, grille jour/heure et classements par morceau, artiste, album et auditeur portent sur toute la période, sans la limite de l’historique. Les graphiques utilisent le fuseau du navigateur. Les anciennes écoutes sont incluses, même si la source musicale est désactivée.
