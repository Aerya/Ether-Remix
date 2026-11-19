# Guide de configuration d’Ether-Remix

[Présentation d’Ether-Remix](../../README.md) · [English](GUIDE.en.md)

**Sommaire**

- [Démarrage rapide](#nav-demarrage-rapide)
- [Installation](#nav-installation)
  - [Régler Ether-Remix depuis la console](#nav-regler-ether-remix-depuis-la-console)
  - [Connecter Scrob](#nav-connecter-scrob)
  - [Connecter un client musique (Subsonic)](#nav-connecter-un-client-musique-subsonic)
  - [Sources communautaires Eclipse](#nav-sources-communautaires-eclipse)
  - [Statistiques et suivi des sources](#nav-statistiques-et-suivi-des-sources)
  - [DMM avec Real-Debrid](#nav-dmm-avec-real-debrid)
  - [Historique et comparaison TMDB](#nav-historique-et-comparaison-tmdb)
- [Animes](#nav-animes)
  - [Exceptions VOSTFR à la demande](#nav-exceptions-vostfr-a-la-demande)
  - [Instances publiques ou auto-hébergement](#nav-instances-publiques-ou-auto-hebergement)
- [Organisation des réglages](#nav-organisation-des-reglages)
  - [Navigation et badges des affiches](#nav-navigation-et-badges-des-affiches)
- [Playlists et apparence](#nav-playlists-et-apparence)
- [Accueil Plex](#plex-home)
- [Seedstrem](#nav-seedstrem)
- [SubMaker](#nav-submaker)
  - [Historique commun et Écoute](#nav-historique-commun-et-ecoute)
- [Médias locaux et iBroadcast](#nav-medias-locaux-et-ibroadcast)
  - [Dossiers vidéo et audio](#nav-dossiers-video-et-audio)
  - [Santé des médias locaux](#nav-sante-des-medias-locaux)
  - [Compte iBroadcast](#nav-compte-ibroadcast)
- [Cache des recherches et pannes de sources](#nav-cache-des-recherches-et-pannes-de-sources)
  - [Navigation et configuration](#nav-navigation-et-configuration)
- [Cartes de navigation inspirées de Nuvio](#cartes-de-navigation-inspirées-de-nuvio)
- [Import Xperience](#import-xperience)
- [Configuration rapide JukeBox](#nav-configuration-rapide-jukebox)

<a id="nav-demarrage-rapide"></a>
## Démarrage rapide

Dans la console, ouvrez **Ether-Remix → Vue d’ensemble → Démarrage**. Cette vue s’ouvre par défaut tant que l’indexation est désactivée et reste accessible ensuite ; elle ne modifie pas les réglages automatiquement.

1. **Sources** : ajoutez, testez et activez vos fournisseurs. Un addon qui fournit ses propres liens de lecture n’exige pas une seconde clé débrideur pour les recherches.
2. **Indexation** : activez et enregistrez l’indexation en arrière-plan pour préparer le catalogue vidéo. Le mode Automatique utilise les addons Stremio activés ; passez en Personnalisé pour choisir vos sources, notamment les indexeurs Prowlarr/Jackett. Une sélection personnalisée vide ne lance aucune recherche. Respectez les quotas des fournisseurs.
3. **Lecture → Qualité** : vérifiez les critères et le seuil du catalogue lisible. Un catalogue vide peut simplement attendre des releases qui correspondent à vos réglages.
4. **Vue d’ensemble → Activité** : contrôlez les dernières recherches et le compteur de titres cherchés. L’indexation se met en pause pendant les lectures. Les statistiques d’utilisation sont distinctes.
5. **Contenus** : personnalisez les collections si besoin. Pour la musique et les fichiers locaux seuls, l’indexation vidéo n’est pas nécessaire ; configurez leurs sources et leur partage.
6. **Applications** : activez le protocole du client, utilisez les accès du spectateur puis essayez une lecture. Les accès Subsonic se trouvent aussi dans **Contenus → Musique**.

<a id="nav-installation"></a>

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

<a id="nav-regler-ether-remix-depuis-la-console"></a>

### Régler Ether-Remix depuis la console

Dans **Lecture → Transcodage**, « Qualités adaptatives en Direct Stream » permet de proposer des variantes réencodées aux connexions limitées ; ce réglage est désactivé par défaut pour les nouvelles configurations. Désactivez aussi « Démarrage accéléré » pour éviter la réduction de résolution des premiers segments lorsque la copie vidéo est possible. Les réglages existants restent inchangés ; ouvrez une nouvelle session de lecture pour appliquer vos choix. Dans Scrob, les boutons Copier récupèrent Server URL, Token et User ID sans devoir afficher le jeton.

Tous les ajouts se règlent dans la console, menu **Ether-Remix** (`/ether/remix`) : activation, options et état de chaque fonction (titres indexés, titres lisibles, bibliothèque musicale…). Une valeur enregistrée sur cette page remplace la variable d'environnement du même nom et s'applique sans redémarrer ; le bouton ↺ revient à la variable ou à la valeur par défaut. Les variables ci-dessous restent utilisables pour un déploiement entièrement décrit dans le `compose.yaml`.

Ether-Remix accepte toutes les variables d'environnement d'Ether (`ETHER_PORT`, `ETHER_DATA_DIR`, `PUID`/`PGID`…) : voir [docs/install.md](../install.md). Variables propres à Ether-Remix :

Les références issues des recherches et imports sont conservées automatiquement dans un index commun, consultable dans **Indexation → Releases**. **Rechercher automatiquement des releases** ajoute les recherches de fond, désactivées par défaut. L’ancien réglage `PSE_RELEASE_LIBRARY` est ignoré ; sa désactivation ne masque plus les références.

| Variable | Défaut | Effet |
| --- | --- | --- |
| `PSE_NOTIFY_LANGUAGE` | `fr` | Langue des notifications du propriétaire. `en` conserve les textes anglais d'Ether. |
| `PSE_INDEXER` | `off` | `on` recherche automatiquement des releases. `off` conserve l’index et son utilisation à la lecture. |
| `PSE_INDEXER_SOURCES` | *(tous les addons Stremio activés)* | Identifiants des indexeurs à interroger, séparés par des virgules. Seule façon d'inclure un indexeur Prowlarr/Jackett/NZBHydra2. |
| `PSE_INDEXER_DAILY` | `1000` | Requêtes maximum par source et par jour (UTC). |
| `PSE_INDEXER_PACE` | `10s` | Délai minimum entre deux titres (minimum `2s`). |
| `PSE_INDEXER_TYPES` | `movie,show` | Types indexés (`movie`, `show`). Une série est cherchée via son premier épisode. |
| `PSE_PLAYABLE` | `off` | `on` active le catalogue « lisible uniquement », avec la même portée que `PSE_FRENCH`. |
| `PSE_PLAYABLE_MIN` | `2` | Releases distinctes (même nom = une seule) nécessaires pour lister un titre. |
| `PSE_PLAYABLE_FILTER` | *(aucun)* | Releases prises en compte, ex. `lang=fr,multi;min=1080;codec=hevc,h264;cached=1`. Langues : `fr` (toute VF : FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF, VF2), `vff`, `vfq`/`qc`, `multi`, `en`, `vostfr` (sous-titres français annoncés), `vo` (aucun doublage annoncé)… Changer de filtre recalcule le catalogue au démarrage, sans réindexer. |
| `PSE_POSTER_REFRESH_HOURS` | `24` | Avec un fournisseur d'affiches (PostersPlus, XRDB, RPDB…), délai en heures (1 à 168) au bout duquel les applications reprennent ses affiches. Ether utilisait 6 heures. |
| `PSE_LIBRARY_SORT` | `release` | Tri des bibliothèques Films et Séries à l'ouverture : `release` (date de sortie, du plus récent au plus ancien) ou `title` (titre, A → Z). Un tri choisi dans l'application reste prioritaire. |
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

**Catalogues Stremio** : le bouton de synchronisation lance un travail en arrière-plan et affiche son état jusqu’à la fin. Un échec conserve le dernier import ; les autres catalogues peuvent encore être synchronisés. `PSE_CATALOGUE_SYNC_TIMEOUT` règle le budget par catalogue en secondes (180 par défaut, de 30 à 900), après l’attente dans la file. Les journaux séparent récupération et enregistrement.

**Titres des releases** : la comparaison tolère les accents et les apostrophes remplacées par des séparateurs. Une mention de partie ajoutée au titre français n’est acceptée que si un titre alternatif du catalogue confirme son numéro ; les contrôles d’année et d’identifiants restent appliqués.

<a id="nav-connecter-scrob"></a>

L’import complet depuis Plex Media Server alimente aussi l’activité récente avec les dates d’origine, sans doublons. Relancez « Récupérer tout l’historique depuis ce serveur » pour compléter un ancien import ; les états locaux plus récents restent prioritaires.

Les séries en cours restent dans « Continuer à regarder » même après plusieurs mois sans lecture, avec leur prochain épisode. Le tri suit le dernier visionnage ; Plex conserve une limite de 40 éléments et les masquages du spectateur. La sélection consulte uniquement l’historique du spectateur concerné, sans parcourir celui des autres comptes.

### Connecter Scrob

Le webhook relaie les débuts, pauses et arrêts effectifs, y compris le remplacement d’une lecture et le nettoyage d’une session inactive. L’identifiant reste stable pour chaque session et spectateur ; un arrêt répété n’est pas renvoyé et ne modifie pas l’historique.


1. Dans **Ether-Remix → Suivi → Scrob**, créez les identifiants Jellyfin du spectateur.
2. Dans Scrob → **Connections → Media Players → Add Connection**, choisissez **Jellyfin** et copiez **Server URL**, **Token** et **User ID**. Cliquez sur **Test**, puis **Add**. Le jeton est limité à ce spectateur et révocable dans ses appareils.
3. Choisissez les bibliothèques à synchroniser dans cette connexion. **Films** et **Séries** fonctionnent directement. L’option avancée Remix « Proposer des bibliothèques limitées à l’historique » ajoute **Vus et notés — Films/Séries** pour limiter l’import aux titres de l’historique.
4. Choisissez **SERVER → SCROB**, **SCROB → SERVER**, ou les deux, et les fréquences **Pull/Push** dans Scrob. Le retour vers Remix concerne les statuts vus/non vus et les notes, pas les positions de reprise. Il ne nécessite aucun interrupteur ni webhook dans Remix.
5. Facultatif : pour envoyer aussi les événements de lecture en temps réel, copiez le webhook de **cette même connexion** dans le panneau repliable Remix et activez « Envoi immédiat des événements de lecture » dans les options avancées. Sans webhook, la synchronisation programmée fonctionne. **Tester** dans ce panneau ne teste que le webhook. Les tests et les envois attendent jusqu’à 60 secondes sur une connexion lente ; une adresse inaccessible peut toujours échouer.

L’erreur Scrob **Failed to connect to Jellyfin or invalid User ID** peut également signaler un HTTP 403 du proxy ou du pare-feu avant que Remix reçoive le jeton. Vérifiez depuis le serveur Scrob les réponses de `/jellyfin/System/Info` et `/jellyfin/Users/{UserId}` avec le jeton, ainsi que les journaux du proxy. Une synchronisation peut parcourir beaucoup d’URLs : un système anti-exploration doit distinguer les lectures authentifiées réussies des tentatives d’accès refusées.

Le panneau actualise automatiquement le dernier accès Jellyfin du spectateur (Scrob ou autre client) et l’état du webhook, sans recharger la page ni effacer les champs en cours de saisie. Un accès API ne prouve pas la fin d’une synchronisation. **Trigger Library Scan** demande un rafraîchissement à Remix : son catalogue étant virtuel, la demande est acquittée immédiatement sans scan de fichiers ni réindexation globale. Les listes d’épisodes sans tri explicite utilisent le catalogue stocké et paginé lorsque l’ordre des épisodes n’est pas personnalisé. Utilisez **Pull** pour importer les données dans Scrob et **Push** pour les renvoyer à Remix.

<a id="nav-connecter-un-client-musique-subsonic"></a>

### Connecter un client musique (Subsonic)

1. Dans **Console → Ether-Remix → Sources → Streaming musical**, activez les comptes souhaités : **AllDebrid**, **Real-Debrid** et/ou **TorBox**, ou collez le lien manifest d’un addon Eclipse. Les clés et les indexeurs sont ceux déjà configurés dans Ether-Remix. **Tester** vérifie le compte puis cherche un lien (sans vérifier l’audio) ; **Tester les sources** ouvre les premiers octets audio pour constater le format. Avec plusieurs comptes d’un même service dans **Sources › Debrid**, ils sont essayés dans l’ordre de priorité.
2. Dans **Contenus → Musique → Clients musicaux**, l’administrateur crée un accès pour le spectateur. Recopiez l’adresse du serveur, l’identifiant `music-…` et le mot de passe musical dans **Subsonic / OpenSubsonic** dans Arpeggi ou votre client. Le mot de passe musical n’est affiché qu’à sa création ; il fonctionne avec l’authentification par jeton (`t`/`s`), y compris pour les spectateurs connectés à Remix par Plex.
3. Utilisez HTTPS hors du réseau local. Chaque spectateur dispose d’un accès musical, limité à ses droits ; le remplacer ou le révoquer coupe les prochaines requêtes de tous les clients qui l’utilisent. Le mot de passe principal reste indépendant. Les clients configurés avec un compte local peuvent continuer en mode **legacy**.

Les services debrid sont essayés après Eclipse, selon leur priorité dans **Sources → Debrid** (le compte actif le plus prioritaire détermine le rang du service, puis tous ses comptes actifs sont essayés dans leur ordre de priorité). Les changements de comptes ou de priorité renouvellent automatiquement les résolveurs et leurs caches. Audius conserve son placement avant les sources ou en repli. Les fichiers sont relayés sans stockage local ni conversion : FLAC/Hi-Res seulement si le fichier disponible l’est réellement.

TorBox impose l’ajout uniquement si déjà en cache. Real-Debrid ne propose plus de vérification préalable de cache : un nouvel ajout peut brièvement démarrer un téléchargement côté service avant son retrait s’il n’est pas prêt. Les torrents préexistants ne sont pas supprimés. Un album absent n’est pas téléchargé localement. La validation automatisée utilise des API simulées ; aucune lecture réelle Real-Debrid/TorBox ni disponibilité Hi-Res n’est certifiée par ces tests.

**Clients Jellyfin et Plex** (Jellyfin web, Finamp, Findroid, Streamyfin, Plexamp, Plex Web…) : rien à régler, une bibliothèque **Musique** apparaît à côté des autres dès qu’une source musicale est activée. Elle montre vos favoris et écoutes, puis les classements Deezer ; la recherche couvre tout le catalogue et la lecture est directe. La recherche globale de Plex montre aussi artistes, albums et titres.

**Lecture musicale Plex / Jellyfin** : le cache des albums et artistes est partagé sans erreur lors du passage d’un client à l’autre.

**Playlists Deezer et Last.fm** : dans **Console → Ether-Remix → Sources → Streaming musical**, collez pour chaque spectateur le lien de son profil Deezer (ses playlists publiques, dont Coups de cœur) ou des liens de playlists Deezer, y compris les liens de partage de l'application (`link.deezer.com`), et/ou son nom d'utilisateur Last.fm (titres aimés, top du mois, top de tous les temps, avec la clé API Last.fm). Les playlists apparaissent dans Plexamp, Plex Web, Finamp, Jellyfin et les clients Subsonic, en lecture seule, et sont réimportées toutes les 6 heures (`PSE_MUSIC_PLAYLISTS_HOURS`). Rien n'est téléchargé : seules les références des titres sont gardées. Pour Spotify et les imports manuels, voir [Playlists et apparence](#playlists-et-apparence).

**Playlists ListenBrainz** : même écran, nom d'utilisateur ListenBrainz (gratuit, sans clé API). Ether-Remix importe la dernière playlist de chaque série que ListenBrainz génère pour lui (Daily Jams, Weekly Jams, Weekly Exploration…) et ses playlists publiques, en respectant la limite de débit de ListenBrainz.

**Playlists personnelles et automatiques** : depuis un client Subsonic (Symfonium, Feishin, Supersonic, Tempo, DSub…), chaque spectateur crée, renomme, complète, réordonne et supprime ses propres playlists ; elles gardent les références des titres et ne dépendent d'aucun fournisseur audio. Quatre playlists automatiques s'y ajoutent, calculées à partir de ses favoris et de ses écoutes : **Titres favoris**, **Écoutés récemment**, **Les plus écoutés**, **Favoris peu écoutés**. Les playlists importées et automatiques restent en lecture seule. Plex et Jellyfin les affichent ; leur édition passe par un client Subsonic.

**Correspondance et repli** : Eclipse et Audius comparent les enregistrements par ISRC ou par titre, artiste et durée, avec rejet des versions live, remix, reprises, acoustiques ou karaoké non demandées. Les sources debrid recherchent l’album puis rapprochent les noms et numéros de pistes ; elles ne vérifient pas l’ISRC du fichier ni son master. Si un lien audio est expiré ou refusé, Ether-Remix passe à la source suivante (jusqu'à trois). Les liens audio visant la machine elle-même ou des adresses réservées sont refusés.

**Nouveautés et test des sources** : la playlist automatique **Nouveautés de vos artistes** réunit les albums sortis ces 4 derniers mois par les artistes favoris et les plus écoutés du spectateur (calculée en arrière-plan, mise à jour toutes les 6 h). Dans la console, Sources → Streaming musical, **Tester les sources audio** essaie chaque source réglée, seule, sur le même titre et affiche le format réellement envoyé (FLAC, MP3…), pas celui annoncé, ou la raison de l'échec. Il montre l'ordre réel de lecture, la raison de chaque source inactive et, pour chaque source, les étapes franchies : titre trouvé au catalogue, résultat de la source, lien obtenu, premiers octets vérifiés. La lecture complète n'est pas testée. **Réessayer cette source** relance une seule source, sans cache. Les liens d'addons ne sont affichés que par leur hôte ; aucune clé ni lien audio n'est affiché.

**Titres en VOSTFR à la demande** : dans **Ether-Remix → Lecture → VOSTFR**, cherchez un film ou une série et ajoutez-le. Il apparaît dans la bibliothèque **« VOSTFR »** de Plex (créée au premier ajout), mais pas dans Films ni Séries tant qu’aucune VF n’est trouvée.

**Découvrir de la musique** : chaque spectateur reçoit les playlists **Top France**, **Top Monde**… (pays au choix dans Console → Ether-Remix → Sources → Streaming musical) et **Découvertes**, renouvelées toutes les 12 heures. Les **styles** (Pop, Rap/Hip Hop, Rock, Électro, Jazz…) se parcourent dans Plex (Musique › Par style), Jellyfin (Genres) et Feishin (Genres), avec les albums, artistes et titres du moment. Sur la page d'un artiste : **artistes similaires** et radio « Dans le même esprit » (Plex), **Mix instantané** (Jellyfin), titres populaires et similaires (Feishin).

**Envoi des écoutes à Last.fm** : avec la clé API et le secret partagé Last.fm, chaque spectateur peut cliquer sur **Connecter Last.fm** dans **Console → Ether-Remix → Sources → Streaming musical**, autoriser Ether-Remix sur last.fm, puis **J'ai autorisé Ether-Remix**. Ses écoutes complètes sont ensuite envoyées toutes les 2 minutes (celles d'avant la liaison et de plus de 14 jours ne partent pas). La **Vue d'ensemble** affiche aussi un bloc **Musique** : écoutes, heures et auditeurs des 30 derniers jours, titres et artistes les plus écoutés.

**Arpeggi, Feishin et autres clients Subsonic** : utilisez les identifiants musicaux générés ci-dessus avec l’authentification par jeton. Si vous choisissez le mot de passe local du compte, activez **Enable legacy authentication**.

La bibliothèque se remplit avec les favoris (artistes, albums, titres) et les écoutes. La recherche interroge Deezer en direct ; à la lecture, Ether-Remix résout le titre auprès des sources activées et relaie le flux. Les playlists et les genres sont pris en charge. Les paroles et le transcodage audio ne le sont pas : le flux est relayé tel quel.

**Sources audio gratuites :** Audius a été validé en MP3 320 kbit/s sur un morceau complet à travers Ether-Remix. Aucune nouvelle source FLAC/Hi-Res sans compte n’est certifiée. Le diagnostic vérifie les premiers octets et indique profondeur/fréquence pour FLAC ; ce n’est pas un test de lecture complète. [Audit, sources exclues, configuration et limites](AUDIO-SOURCES.fr.md).

<a id="nav-sources-communautaires-eclipse"></a>

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


<a id="nav-statistiques-et-suivi-des-sources"></a>

### Statistiques et suivi des sources

**Ether-Remix → Stats** sépare le tableau de bord des réglages **Films & séries**. Il regroupe la couverture du catalogue, les graphiques de requêtes sur sept jours (jours UTC), les releases conservées par source, les quotas, erreurs, réponses partielles et durées moyennes.

L’activité de l’indexeur en arrière-plan est actualisée toutes les trois secondes : titre et source interrogés, durée en cours, puis résultat de la recherche. Le journal garde les 50 dernières recherches depuis le démarrage de l’indexeur ; il est réinitialisé lors d’un redémarrage ou d’un changement de réglages relançant l’indexeur. Les compteurs journaliers sont persistants ; les nouvelles mesures d’erreurs et de durées commencent à l’installation de cette version.

Les statistiques par source sont actualisées toutes les trente secondes. Les totaux détaillés du catalogue et des bibliothèques restent un instantané daté, recalculable avec **Actualiser**. Le suivi s’arrête lorsque l’onglet n’est plus affiché et se suspend quand la page est masquée. **Suspendre le suivi** fige l’affichage sans arrêter l’indexation.

Une recherche de titre peut consommer plusieurs requêtes. Les titres communs à plusieurs sources ou bibliothèques ne doivent pas être additionnés ; une release conservée n’est pas une garantie de lecture.


<a id="nav-dmm-avec-real-debrid"></a>

### DMM avec Real-Debrid

Par défaut, Remix interroge [l’instance publique DMM](https://debridmediamanager.com/albums). **Adresse de l’instance DMM** (`PSE_MUSIC_DMM_URL`) permet de choisir une instance privée accessible depuis le conteneur Remix. Saisissez l’adresse de base, sans `/albums` ni `/api/music`. Le [dépôt DMM](https://github.com/debridmediamanager/debrid-media-manager#setup) explique son installation ; votre instance doit fournir son catalogue et les API musicales `/api/music/search` et `/api/music/library`. Le catalogue public n’est pas copié automatiquement.

Dans **Sources → Debrid**, configurez au moins un compte Real-Debrid actif, puis activez **DMM · Real-Debrid** dans **Ether-Remix → Sources → Streaming musical**. Aucun lien manifest ni compte DMM n'est nécessaire. Le catalogue de navigation reste Deezer ; DMM fournit une recherche supplémentaire des albums à lire.

Cette source ne dépend pas des indexeurs. Elle se place au rang des comptes Real-Debrid, juste avant leur recherche par indexeurs ; les comptes sont essayés dans leur ordre de priorité. Les autres sources activées prennent le relais si l'album ou la piste ne peut pas être lu.

Ether-Remix ne transmet aucune clé à DMM : la recherche est publique, puis les magnets sont résolus directement auprès de Real-Debrid. Les pistes FLAC/MP3 sont sélectionnées ensemble pour retrouver le cache de DMM. Un album non prêt est retiré, avec la même limite Real-Debrid que pour la source par indexeurs : le service peut brièvement commencer un téléchargement avant son retrait. **Tester** cherche un lien ; le diagnostic des sources peut vérifier ses premiers octets audio.


<a id="nav-historique-et-comparaison-tmdb"></a>

### Historique et comparaison TMDB

Dans **Ether-Remix → Stats**, les totaux TMDB proviennent des exports d’identifiants officiels, téléchargés en arrière-plan chaque jour et conservés en cache avec leur date. Les exports adultes séparés sont exclus. Le premier relevé démarre environ une minute après le lancement du serveur. En cas de panne, le dernier relevé valide reste affiché. Le pourcentage compare la sélection navigable locale à ces listes, pas la disponibilité de flux audio/vidéo.

L’historique propose **Jour** (30 jours), **Semaine** (12 semaines), **Mois** (12 mois) et **Année** (10 années), en UTC. Choisissez les **ajouts au catalogue** ou les **recherches par source**, puis les films, séries, les deux ou le sous-ensemble Animes. Les animés restent inclus dans Films et Séries : les totaux ne les comptent pas deux fois. Le suivi des recherches d’animés commence avec cette version ; les périodes antérieures restent non mesurées. Le filtre source/indexeur s’applique aux recherches de releases. Le tableau détaillé affiche les valeurs par période et cumulées ; le cumul tient compte des mesures antérieures à la fenêtre affichée.

Les ajouts sont reconstruits depuis les dates d’entrée des médias encore conservés dans le catalogue local. Les titres sans date restent explicitement exclus. Le suivi des recherches par source est enregistré durablement à partir de cette mise à jour : une recherche correspond à un titre interrogé auprès d’une source, hors annulations et refus de quota. Un titre peut être recherché plusieurs fois. Les périodes non mesurées apparaissent comme telles.

Chaque ajout dans **Titres en VOSTFR à la demande** place aussi le titre en tête de file de recherche, sans attendre son délai normal. L’indexation doit être active ; les budgets par source et les pauses pendant les lectures restent respectés. Si la mise en file échoue, la sélection VOSTFR est conservée et la console l’indique.

Le bouton **Tester** de DMM utilise désormais un échantillon du catalogue DMM, indépendamment des classements Deezer. Il distingue les étapes catalogue, compte, cache et lien audio. Les logs `component=pse-music source=dmm` donnent les compteurs et les causes d’échec sans enregistrer de clé ni d’URL de lecture.

Les sources Xtream peuvent aussi importer les films et séries VOD du fournisseur : activez l’option VOD de la source pour les inclure.

Dans **Sources → IPTV**, le bouton **Modifier** ouvre le fournisseur sélectionné. La création, l’enregistrement et la fermeture de son formulaire conservent la catégorie IPTV. Les liens provenant de Télévision en direct ouvrent ce même éditeur.

Dans **Sources → Streaming musical**, les réglages sont regroupés par famille : **Eclipse** (manifests et registre), **DMM / Real-Debrid** (catalogue DMM, recherche par indexeurs et comptes partagés), puis **Autres sources** (AllDebrid, TorBox, Audius et HiFi API). Le diagnostic audio teste toutes les familles depuis un bloc distinct.

Dans **Sources → Debrid → Modifier**, **Afficher la clé API** révèle seulement celle du compte sélectionné au propriétaire connecté. **Masquer la clé API** ou la fermeture du formulaire retire cet affichage sans modifier la clé enregistrée.

Dans **Indexation**, **Sources importées** se filtre et se trie par catégorie, nom ou contenus concernés. **Titres à indexer en priorité** se déplie à la demande. « Recherchés » inclut les recherches sans résultat ; « Affichés » compte les titres respectant les critères du catalogue. Les compteurs sont actualisables et peuvent refléter le dernier instantané mis en cache.

<a id="nav-animes"></a>

## Animes

Dans **Ether-Remix → Contenus → Animes**, créez le modèle Animes pour obtenir les collections populaires et les mieux notées de TMDB, en films et séries. Une bibliothèque mixte apparaît dans Plex en deux sections Films et Séries. La catégorie Animes est conservée après renommage. Les bibliothèques Animes respectent vos filtres de langue, notamment FR/MULTi. La VO/VOSTFR reste un choix explicite, via les exceptions à la demande ou l’option Toutes langues.

Dans **Sources → Stremio**, utilisez **Ajouter un addon**, puis collez le lien `manifest.json` généré chez le fournisseur de votre choix. Aucune sélection de fournisseurs n’est imposée ou suggérée. Les sources, leur activation et leur priorité sont partagées avec Films & Séries. Pour la découverte, utilisez les collections TMDB, les listes AniList ou un addon proposant des catalogues.

Le bouton **Nouveautés**, dans l’en-tête de la console, permet de rouvrir les notes de livraison. La popup ne présente que les notes non lues depuis la dernière version consultée. Fermer ou valider mémorise la version et les notes lues dans ce navigateur ; une reconstruction Docker ne les réaffiche pas. L’ancien repère est repris automatiquement. **Historique complet** permet de consulter les notes précédentes à la demande. La version dans le menu est fournie par le serveur exécuté ; les images officielles incluent un numéro de build Remix automatiquement croissant. Le pied du menu mène aux dépôts Ether et Ether-Remix.

<a id="nav-exceptions-vostfr-a-la-demande"></a>

### Exceptions VOSTFR à la demande

**Ether-Remix → Lecture → VOSTFR → Titres en VOSTFR à la demande** regroupe les films, séries et animés explicitement autorisés sans VF. Chaque ajout lance une recherche prioritaire si l’indexation est active, dans les limites des quotas et des pauses de lecture. Ces titres figurent aussi dans la bibliothèque VOSTFR. Votre sélection existante est conservée.

<a id="nav-instances-publiques-ou-auto-hebergement"></a>

### Instances publiques ou auto-hébergement

Les sources musicales identifient les instances publiques et proposent les liens d’auto-hébergement disponibles : [UltraMax Music](https://github.com/PaRaN01a-hash/ultramax-music) et [HiFi API](https://github.com/binimum/hifi-api). Pour Stremio, collez le manifest de votre instance publique ou privée ; aucun fournisseur n’est recommandé. Votre manifest personnel reste votre configuration.

<a id="nav-organisation-des-reglages"></a>

## Organisation des réglages

Les indexeurs (Prowlarr, Jackett, NZBHydra2) trouvent les releases. Pour les lire, configurez la voie adaptée : compte débrideur, client BitTorrent ou addon Stremio comme Seedstrem. Seedstrem utilise son propre client torrent. Pour Usenet, Remix lit via son moteur NNTP et vos fournisseurs Usenet ; SABnzbd/NZBGet ne sont pas des clients externes intégrés.

- **Sources** : accès unique aux comptes, addons Stremio, indexeurs BitTorrent/Usenet, fournisseurs Usenet, IPTV et sources musicales. Les composants et configurations Ether sont conservés. Les indexeurs peuvent être filtrés par instance Prowlarr/Jackett ou par provenance manuelle. Leur configuration reste unique.
- **Indexation → Réglages** : activation, quotas, rythme et choix des sources : Automatique pour les addons Stremio activés, ou Personnalisé, y compris aucune source. Ce choix ne désactive pas les sources à la lecture. Les titres à indexer en priorité présentent les compteurs catalogue, recherchés, avec release et affichés par bibliothèque ou collection ; les groupes peuvent se recouper.
- **Vue d’ensemble** : démarrage rapide, activité de l’indexation Remix, tableaux de bord Sources et Utilisation hérités d’Ether.
- **Lecture → Priorités** : préférences entre sources à qualité équivalente. Ce classement n’impose pas des recherches réseau successives et ne classe pas universellement les fichiers locaux, clients torrent, XDCC et FTP. Le dernier recours se règle par addon Stremio dans Sources.
- **Lecture → Qualité** : catalogue français, langues de release, résolution, codecs, cache et exceptions par collection.
- **Lecture → VOSTFR** : sélection explicite de titres sans VF et recherche prioritaire.
- **Contenus** : films, séries, animés, musique, playlists et bibliothèques personnalisées. Les bibliothèques mixtes restent proposées dans Films et Séries.
- **Suivi** : notifications, Scrob et identifiants Last.fm.

Les anciennes adresses `/sources/...` et les anciens onglets Remix redirigent vers les nouveaux écrans. Il n’y a pas de copie des comptes ou manifests. Les catégories servent à organiser les sources ; elles ne garantissent pas qu’un titre précis existe chez un fournisseur.

<a id="nav-navigation-et-badges-des-affiches"></a>

### Navigation et badges des affiches

Plex et Jellyfin préparent les badges d’une page ensemble. Les nouveaux épisodes et les rotations de collections respectent l’état de chaque spectateur. Le catalogue « lisible uniquement » conserve des compteurs exacts, y compris les exceptions de langue.

**Affiches plus rapides** : avec un fournisseur d’affiches (PostersPlus, XRDB, RPDB…), Plex garde chaque affiche pendant un jour au lieu de la redemander au serveur à chaque affichage. Une affiche de repli TMDB, servie quand le fournisseur ne répond pas, reste revérifiée à chaque fois pour ne pas rester affichée après la panne. Les applications reprennent les affiches du fournisseur toutes les 24 heures ; ce délai se règle dans **Console → Ether-Remix → Visuels → Affiches** (`PSE_POSTER_REFRESH_HOURS`, de 1 à 168). Jellyfin et Emby reçoivent l’affiche à la taille demandée par l’application, et Jellyfin n’attend plus avant d’envoyer une image. Sans fournisseur, une télé qui demande une affiche de 600 px reçoit celle de 500 px plutôt que celle de 780 px, plus de deux fois plus lourde.

**Tri par date de sortie** : Films et Séries s’ouvrent du plus récent au plus ancien. Plex applique ce tri tant que vous n’en avez pas choisi un autre dans l’application. Jellyfin et Emby l’appliquent quand l’application ne demande pas de tri ; Jellyfin Web et Infuse gardent le leur : changez-le une fois dans l’application, elle s’en souvient.

**Films et Séries toujours en VF** : un titre sans VF (choisi en VOSTFR, rangé dans une bibliothèque « Toutes langues », dans une collection d’exception ou trouvé seulement en VOSTFR par SeerrCatalog) reste visible dans sa bibliothèque ou sa collection, mais jamais dans Films, Séries ou l’accueil.


<a id="nav-playlists-et-apparence"></a>

## Playlists et apparence

Dans **Contenus → Musique → Playlists** :

- **Découvrir** : tendances et recherche Deezer ; connexion Spotify et liste de vos playlists ; liens vers ListenBrainz, Soundplate et DailyPlaylists pour découvrir d’autres sélections.
- **Mes comptes** : profils Deezer publics, Last.fm et ListenBrainz synchronisés automatiquement (6 h par défaut). Les playlists Deezer privées ne sont pas accessibles par un simple lien de profil.
- **Importer** : lien de playlist Deezer, Spotify ou ListenBrainz, ou fichier CSV/XSPF (2 Mo maximum). CSV : colonnes `title` et `artist`, ou `Track Name` et `Artist Name(s)` pour Exportify ; séparateurs virgule ou point-virgule. Les URL audio des fichiers XSPF ne sont jamais téléchargées.

Choisissez le spectateur, vérifiez l’aperçu, puis validez. Une copie personnelle, éditable via Subsonic, est créée sans remplacer les autres playlists. Ces copies ne sont pas synchronisées automatiquement. L’import traite les 2 000 premiers éléments et affiche la progression ainsi que les titres non retrouvés dans le catalogue. La lecture dépend ensuite de vos sources musicales ; les métadonnées d’une playlist ne garantissent pas un flux audio disponible. Le suivi reste disponible une heure sur le serveur, mais ne survit pas à son redémarrage.

**Spotify** : dans Découvrir → Spotify, saisissez le **Client ID** de votre application. Ajoutez dans le tableau de bord Spotify l’URI de redirection exacte affichée par Remix, puis connectez le compte pour le spectateur choisi. HTTPS est requis hors boucle locale. Aucun Client Secret n’est demandé : connexion OAuth avec PKCE, jetons chiffrés sur le serveur, accès en lecture seule et bouton de déconnexion. En mode développement, le propriétaire de l’application doit avoir Premium et autoriser les utilisateurs dans Spotify Developers. L’accès aux titres peut être refusé pour les playlists dont vous n’êtes ni propriétaire ni collaborateur ; un lien trouvé dans un annuaire ne contourne pas cette limite.

**Apparence** : le bouton en forme de palette dans la barre supérieure propose Ether, Nord, Catppuccin, Solarized, Gruvbox et Rosé Pine. Choisissez séparément le mode clair ou sombre ; l’aperçu est immédiat et le choix est conservé dans ce navigateur. Les autres utilisateurs gardent leurs propres préférences.

<a id="nav-seedstrem"></a>

## Seedstrem

[Installer Seedstrem et connecter son manifeste à Remix](SEEDSTREM.fr.md) : Prowlarr, qBittorrent ou Deluge et lecture HTTP pendant le téléchargement.

<a id="nav-submaker"></a>

## SubMaker

[Configurer les sous-titres externes et la traduction facultative](SUBMAKER.fr.md), globalement ou par bibliothèque/collection, avec SeerrCatalog.

Pour les règles de visibilité et les délais de recherche, voir [Catalogue et indexation](CATALOGUE.fr.md). Pour connecter l’addon, voir [SeerrCatalog](SEERRCATALOG.fr.md).

<a id="nav-historique-commun-et-ecoute"></a>

### Historique commun et Écoute

**Nom affiché** : sur la fiche d’un utilisateur (**Spectateurs**), le crayon à côté de son nom permet de choisir le nom utilisé dans les statistiques, l’activité et les listes. Plex ne l’écrase pas et les lectures déjà enregistrées prennent le nouveau nom. La connexion ne change pas. Un champ vide rétablit le nom Plex.

**Activité → Historique** réunit films, séries et musique dans un tableau chronologique commun, avec recherche, tri, pagination et fiche de détail. La liste contient les 500 dernières lectures de la période sélectionnée. Les écoutes musicales gardent leurs métadonnées artiste/album ; la durée indiquée est celle du morceau, pas le temps réellement écouté. Les mesures de complétion et de durée de session restent explicitement réservées à la vidéo.

**Activité → Écoute** reprend les filtres période, année et utilisateur de Visionnage. Les compteurs, écoutes par jour, grille jour/heure et classements par morceau, artiste, album et auditeur portent sur toute la période, sans la limite de l’historique. Les graphiques utilisent le fuseau du navigateur. Les anciennes écoutes sont incluses, même si la source musicale est désactivée.

<a id="nav-medias-locaux-et-ibroadcast"></a>

## Médias locaux et iBroadcast

<a id="nav-dossiers-video-et-audio"></a>

### Dossiers vidéo et audio

Ajoutez vos dossiers dans les volumes de la stack Dockge-Enhanced, puis redémarrez le conteneur :

```yaml
volumes:
  - ./config:/config
  - ./cache:/cache
  - /chemin/hote/videos:/media/videos:ro
  - /chemin/hote/musique:/media/musique:ro
```

Dans **Sources → Fichiers locaux**, indiquez un nom unique, le chemin **dans le conteneur** (`/media/videos`, par exemple) et le type vidéo ou audio. Un dossier réseau doit être monté sur l’hôte avant de démarrer la stack. Le compte du conteneur doit pouvoir lire ses fichiers.

L’analyse se lance après ajout, au démarrage, chaque heure et avec **Analyser maintenant**. Le panneau affiche sa progression et ses erreurs. Les métadonnées inchangées sont réutilisées. Un dossier inaccessible conserve son index précédent. Les fichiers disparus restent également dans l’index après une analyse réussie : un montage vide ou partiellement déconnecté ne vide jamais la bibliothèque. Leur retour est pris en compte à la prochaine analyse. Retirer une source ne supprime jamais les fichiers. Les liens symboliques sont ignorés.

Les vidéos sont rapprochées **d’abord du catalogue Remix, puis de TMDB**. Un nom de film avec année (`Titre.2020.mkv`) ou un épisode explicite (`Série.S01E02.mkv`) permet une identification prudente : titre normalisé exact, année concordante et résultat unique. Un fichier identifié utilise la fiche existante, avec les saisons et épisodes correspondants, et fournit une lecture locale. Sans correspondance fiable, il garde son titre de fichier, sans fiche TMDB obligatoire. Chaque dossier alimente une bibliothèque et une collection ; les vidéos personnelles ne gonflent pas les compteurs TMDB.

Après la première analyse, ouvrez **Bibliothèques et collections** sous le dossier vidéo dans **Sources → Fichiers locaux**. Cochez les bibliothèques existantes qui doivent afficher sa collection, ou les collections personnalisées qu’il doit alimenter ; chaque changement est enregistré immédiatement. Vous pouvez ainsi regrouper plusieurs dossiers dans « Dessins animés » ou « Séries animées ». Les liens de création ouvrent les éditeurs de bibliothèques et de collections. La collection dédiée au dossier reste gérée par Remix. Les collections intégrées, celles d’autres spectateurs et les collections de sorties à venir ne sont pas modifiables ici.

Les sources et filtres existants des collections sont conservés, ainsi que leur fréquence d’actualisation. Les prochains fichiers sont pris en compte après analyse du dossier et actualisation de la collection. Une bibliothèque Films ne montre que les films, une bibliothèque Séries les séries identifiées ; une vidéo sans correspondance TMDB reste de type film. Le partage ne change pas : une destination alimentée par des dossiers privés est limitée aux spectateurs autorisés sur tous ces dossiers. Détacher la dernière collection d’une bibliothèque retire cette bibliothèque vide, sans supprimer les médias, et les scans ne la recréent pas. Une collection dont le dossier est l’unique source doit recevoir une autre source avant détachement ; retirer définitivement ce dossier désactive cette collection.

La musique locale rejoint la bibliothèque Musique et ses clients Plex/Plexamp, Jellyfin/Emby et Subsonic. Les tags fournissent titre, artiste, album, disque et piste ; à défaut, Remix utilise le nom du fichier et du dossier. Les fichiers sont servis dans leur format d’origine : la compatibilité audio dépend du client. Les fichiers locaux ne sont jamais envoyés à un fournisseur musical.

Formats analysés : MKV, MP4/M4V, AVI, MOV, WebM, TS/M2TS, MPEG/MPG, WMV, FLV, OGV, VOB, 3GP ; MP3, FLAC, M4A/AAC, Ogg/Opus, WAV, AIFF, APE, WMA et ALAC. Un fichier doit contenir un flux du type choisi et être lisible par `ffprobe`. L’image Docker Linux fournit cet outil. Les dossiers sont **privés par défaut**, réservés au propriétaire qui les ajoute. Dans **Privé · options de partage**, choisissez certains spectateurs ou tous, puis enregistrez. Seul l’administrateur peut ajouter des chemins du serveur.

<a id="nav-sante-des-medias-locaux"></a>

### Santé des médias locaux

Le diagnostic se charge à la première ouverture de la section dans **Sources → Fichiers locaux**. Replier puis rouvrir la section conserve le résultat ; utilisez **Actualiser le diagnostic** pour relancer la vérification.

Dans **Remix → Médias locaux**, actualisez le diagnostic pour vérifier les dossiers et fichiers déjà indexés, sans relancer `ffprobe`. Les erreurs distinguent chemin absent, permission refusée et autre indisponibilité. Un dossier indisponible rend ses fichiers non vérifiables ; un fichier absent dans un dossier lisible est signalé comme absent. Les modifications de taille/date demandent une nouvelle analyse.

Filtrez par dossier, état, nom ou chemin. Les vidéos sans correspondance restent des vidéos locales valides. Les doublons potentiels partagent une fiche identifiée ou un nom de fichier dans le même dossier source : ce ne sont pas des doublons certifiés, et des éditions différentes peuvent être signalées. Les fichiers sont triés du plus lourd au plus léger, par pages de 50.

Les tailles logiques sont regroupées par dossier source (une bibliothèque vidéo par dossier ; les dossiers audio rejoignent Musique). La taille accessible est vérifiée au moment du diagnostic ; la taille connue correspond au dernier index, fichiers absents compris. Les liens physiques peuvent être comptés plusieurs fois : ces chiffres ne représentent ni les blocs réellement occupés ni l’espace libre du disque. Aucun fichier n’est modifié ou supprimé. Les entrées absentes restent conservées jusqu’au retrait explicite de la source ; retirer puis ajouter la source permet de reconstruire son index, en reconfigurant son partage.

<a id="nav-compte-ibroadcast"></a>

### Compte iBroadcast

Dans **Sources → Streaming musical → iBroadcast**, ou dans votre **espace personnel** pour un compte spectateur, commencez par **Ajouter un compte** avec un nom :

1. Sur [iBroadcast](https://media.ibroadcast.com/), ouvrez le menu du compte → **Apps → developer**, puis créez une application privée.
2. Dans son édition, choisissez **Grant Type → Device Code**, enregistrez et copiez le **Client ID** dans Remix. Aucun Client Secret, mot de passe ou URL de retour n’est nécessaire.
3. Cliquez **Connecter iBroadcast**, ouvrez le lien et autorisez les accès au compte et à la bibliothèque **en lecture seule**. Le panneau se met à jour automatiquement.
4. L’import démarre après autorisation, puis chaque heure ; **Actualiser la bibliothèque** permet de le relancer.

Chaque compte iBroadcast est **privé par défaut**, appartient au spectateur qui le connecte et possède ses propres identifiants chiffrés. Plusieurs comptes peuvent être ajoutés. Le partage facultatif avec certains spectateurs ou tous se règle pour chaque compte ; les bibliothèques autorisées apparaissent ensemble dans Musique. Les favoris et historiques restent personnels. Activez **Importer mes playlists iBroadcast** pour les retrouver dans vos clients : leur ordre et les morceaux répétés sont conservés ; les pistes supprimées ou absentes sont ignorées. L’import est actualisé avec la bibliothèque et les playlists importées restent personnelles au propriétaire du compte. Cette intégration lit les pistes en **MP3 128 kb/s**, y compris avec un compte gratuit ; elle ne renvoie pas de modifications de playlists ou de tags vers iBroadcast. Les jetons sont chiffrés dans la base, renouvelés automatiquement et absents des réponses destinées aux clients. Déconnecter retire la bibliothèque de Remix ; pour révoquer également l’autorisation auprès du fournisseur, utilisez **Apps** sur iBroadcast.

<a id="nav-cache-des-recherches-et-pannes-de-sources"></a>

## Cache des recherches et pannes de sources

La durée « Mémoriser les recherches » du résolveur est un plafond strict (six heures par défaut). L’ancienneté du titre ne permet pas de le dépasser. Les réponses vides sont conservées moins longtemps : jusqu’à trois minutes pour un titre récent ou sans date, trente minutes pour un titre ancien, toujours dans le plafond configuré. Une recherche vide réussie remplace les anciens résultats.

Lors d’une panne temporaire (délai, connexion, erreur serveur ou suspension automatique après des pannes répétées), les résultats expirés depuis moins de 24 heures peuvent servir de candidats de secours. Ils ne sont ni renouvelés ni enregistrés comme une recherche fraîche complète. Seuls les descripteurs torrent/NZB sont repris ; les liens HTTP directs expirés sont exclus. Les indications de cache debrid sont retirées et le parcours normal valide la lecture. Les recherches de disponibilité en arrière-plan ne bénéficient pas de ce repli.

Ce mécanisme ne masque pas une erreur d’identifiants, un quota épuisé, une annulation ou une réponse vide valide. Désactiver le cache ou la source désactive également ce repli. Les changements d’adresse, d’identifiants ou de configuration de recherche invalident l’identité du cache. Les reprises sont signalées dans les journaux comme `using expired search candidates`.


<a id="nav-navigation-et-configuration"></a>

### Navigation et configuration

La console utilise `/ether/remix`. Les anciens favoris sont redirigés en conservant leurs paramètres. Les blocs de configuration sont repliés par défaut ; ouvrir ou fermer un bloc conserve les saisies. Sources → Streaming musical réunit Eclipse, DMM / Real-Debrid, iBroadcast et les autres fournisseurs. L’adresse Subsonic et les compteurs de bibliothèque sont dans Contenus → Musique. Pour Spotify, utilisez désormais `/ether/remix/contenus?content=musique` comme chemin de retour autorisé ; les retours déjà lancés avec l’ancien chemin restent acceptés.

## Cartes de navigation inspirées de Nuvio

**Packs de visuels** : prévisualisez un ensemble Nuvio Assets, Kometa, nichtlegacy, Kaptain, FR-Badges, UltraMax ou TVFlix/Nuvio, puis appliquez ses correspondances aux cartes sélectionnées. Les visuels personnalisés sont protégés par défaut, les cartes sans correspondance restent inchangées et les variantes ambiguës se choisissent dans le panneau d’ajustement paginé. Import possible depuis un fichier JSON (4 Mo maximum) ou son URL HTTPS publique : seuls les noms et couvertures sont repris, jamais les addons/catalogues. Les changements sont enregistrés avec « Appliquer les cartes à mes accueils ». La personnalisation à l’unité et la recherche par galerie restent disponibles.

**Affiches des films et séries** : BetterPosters dispose déjà de son intégration ; son configurateur est accessible depuis Services. Pictorium et TOP Posters ont leurs propres comptes facultatifs, via le modèle d’URL fourni par le service. Les paramètres privés restent côté serveur et un échec conserve le recours à TMDB. Pictorium peut être hébergé ailleurs : aucune installation locale n’est nécessaire pour utiliser une instance publique.

Un **style pour plusieurs cartes** peut être préparé depuis une même galerie : choisissez les cartes, recherchez leurs visuels, filtrez par créateur, ensemble ou dossier et sélectionnez les correspondances uniques en un clic. Vérifiez les variantes avant application groupée ; les cartes sans choix conservent leur visuel. Les métadonnées disponibles varient selon la source, et un même créateur ne garantit pas un style identique. Recherche limitée à 30 cartes par lot, sans enregistrement avant « Appliquer les cartes à mes accueils ».

Cette option est une **BÊTA facultative**, désactivée tant que vous ne l’appliquez pas. Dans **Ether-Remix → Visuels → Cartes**, ouvrez **Cartes inspirées de Nuvio → Configuration rapide**.

1. Partez de la **Sélection de démarrage**, ou composez votre sélection. **Studios et labels** recherche les sociétés de production TMDB par nom ou identifiant ; il ne s’agit pas d’un annuaire exhaustif des labels musicaux. **Genres** propose les genres de films TMDB, avec leur correspondance séries lorsqu’elle existe.
2. Dans **Services**, choisissez un pays : les services se chargent automatiquement. Les fournisseurs disponibles proviennent des listes films et séries TMDB / JustWatch. Ajoutez vos services puis changez de pays : la sélection reste conservée. Un code ISO à deux lettres permet de choisir un autre pays. Le pays indique la disponibilité du catalogue, pas l’origine du studio, et n’accorde aucun accès à un abonnement.
3. Pour une carte sélectionnée, ouvrez **Personnaliser une carte**. Les services du pays se chargent automatiquement. L’éditeur s’ouvre sous la carte ; Les galeries GitHub, ThePosterDB et ACdb permettent de rechercher et sélectionner un visuel directement dans Remix. MediUX couvre les galeries publiques de genres (recherche en anglais), pas les studios/services. Fanart.tv ne dispose pas de recherche de cartes intégrée ; l’import fichier/URL reste disponible.  Le choix apparaît immédiatement dans l’éditeur et l’aperçu. Le mode automatique utilise un logo TMDB ou une mosaïque ; les noms restent affichés sans logo. La galerie **[nichtlegacy / kometa-config-images](https://github.com/nichtlegacy/kometa-config-images)** propose ses images correspondantes, y compris des logos et des formats verticaux : choisissez une image adaptée à la carte. Sa liste est mise en cache six heures et une indisponibilité est signalée.
4. **Importer · fichier ou URL** accepte une URL directe d’image ou un fichier JPEG/PNG/WebP de 10 Mo maximum. Les fichiers sont réduits à 1 200 pixels et 256 Kio au maximum, puis conservés dans les données Remix ; les URL restent distantes et peuvent devenir indisponibles. Les liens [ThePosterDB](https://theposterdb.com/), [MediUX](https://mediux.pro/), [Fanart.tv](https://fanart.tv/) et [ACdb Art Department](https://acdb.tv/art-department) ouvrent ces sites pour choisir une image : les galeries intégrées sont décrites ci-dessus ; l’import manuel est une alternative. Respectez leurs conditions et les droits des créateurs. Ce réglage concerne les cartes, pas un remplacement global des affiches des films et séries.
5. Activez ou non **Regrouper les services par pays sur les accueils**, puis **Appliquer les cartes à mes accueils**. Jusqu’à 200 cartes peuvent être sélectionnées par opération. Les groupes remplacent les recommandations Plex et Jellyfin/Emby ; chaque carte ouvre sa collection. Films et séries sont séparés et les cartes remontent aussi dans les collections, sauf tri imposé par le client.

Les collections se remplissent en arrière-plan via TMDB, se renouvellent quotidiennement et contiennent au maximum 100 titres chacune. Un genre sans correspondance série ne crée qu’une collection films. Les collections vides sont masquées et les restrictions du spectateur restent appliquées. Les sources de lecture Remix restent nécessaires : cette configuration ne lance aucun téléchargement de média.

La réouverture recharge les cartes et les images enregistrées. Réappliquer ajoute les cartes manquantes et ne supprime pas les précédentes. Seuls les visuels explicitement modifiés ici sont remplacés, sur les collections films et séries de la carte ; les noms, règles et autres personnalisations existantes sont conservés. **Vider la sélection** ne supprime rien sur le serveur. Pour retirer une carte déjà créée, désactivez ou supprimez sa collection dans l’éditeur. **Rétablir l’accueil précédent** désactive le remplacement sans effacer les collections ni réécrire la disposition enregistrée.

Pour une collection existante, notamment importée depuis Xperience, utilisez **Public et destinations → Carte de navigation Remix**. Son contenu reste inchangé. **Aucune carte** la retire du groupe sans supprimer la collection.

Le préréglage est indépendant de Nuvio et ne copie ni ses fichiers ni ses paramètres. Aucun manifeste Xperience n’est nécessaire. Les clients décident de la forme finale : Plex peut conserver un cadre vertical et Jellyfin préfixer les rangées par « Derniers ajouts ». Actualisez le client après enregistrement ; le renouvellement de ses caches peut retarder les nouvelles images. Les essais sur vos clients restent nécessaires pendant cette bêta. Les autres protocoles bénéficient des collections sans remplacement de leur accueil.

## Import Xperience

Dans **Ether-Remix → Contenus** (`/ether/remix/contenus`), ouvrez **Importer un manifeste** dans le bloc **Importer Xperience**. Créez votre configuration sur [xperience-app.com](https://xperience-app.com/) et collez son lien personnel `manifest.json` (HTTPS ou `stremio://`). **Afficher les catalogues** prépare la sélection sans installer de source ni créer de bibliothèque.

Choisissez jusqu’à **30 catalogues** par import : films et séries restent séparés, y compris les rubriques Animation. Recherchez une rubrique, renommez-la et réglez ses paramètres ; **Sans filtre supplémentaire** correspond à `genre=None` lorsqu’il est proposé par Xperience. Les catalogues de recherche sont exclus. Choisissez **Bibliothèques et collections**, **Collections seulement** ou **Bibliothèques seulement**. Ce dernier mode utilise des collections de gestion sans publication dans l’onglet Collections ni rangées d’accueil.

Les nouvelles collections gardent le classement Xperience, sans rotation aléatoire, et se renouvellent selon l’intervalle choisi à l’import (24 heures par défaut, de 1 à 168 heures). Le maximum est réglable de **1 à 2 000 titres par catalogue**, 500 par défaut ; ce n’est pas un import illimité. Les bibliothèques sont publiées dans les clients compatibles (Plex, Jellyfin, Emby, Stremio, Xtream et Silo). Le remplissage se fait en arrière-plan. Les identifiants sont rapprochés du catalogue Remix ; certains titres peuvent manquer. Les filtres de langue, de disponibilité et les accès des spectateurs restent appliqués. Cette actualisation ne fige pas les autres rangées de l’accueil.

Xperience fournit des listes, pas des fichiers ni des sources de lecture : vos sources Remix restent nécessaires, sans fichiers `.strm`. Le manifeste personnel est enregistré dans la configuration privée du module Stremio ; les collections ne contiennent que sa référence et les paramètres choisis. Ne partagez pas ce lien. L’importeur installe ou réutilise le plugin Xperience dans Ether : il est nécessaire aux actualisations. La relecture du manifeste (catalogues disponibles et paramètres) possède un intervalle distinct, réglable de 1 à 168 heures, 1 heure par défaut. Un réimport applique cet intervalle à la source existante, mais conserve les horaires des collections déjà créées ; ceux-ci se règlent dans leur éditeur. Les nouveaux catalogues ne sont pas importés automatiquement.

La création depuis **Plugins → Catalogues** et **Accueil** reprend le type et les paramètres par défaut du catalogue. Une erreur de validation indique le champ refusé. Les collections supprimées ne réapparaissent plus dans les rangées à cause de leur historique ; les noms actuels sont utilisés. Désinstaller le plugin ne constitue pas une suppression de ses collections : elles restent enregistrées mais leur source devient indisponible. Un nouvel import des mêmes catalogues et paramètres réutilise les éléments existants, y compris après une création partielle, sans écraser les noms, limites, destinations ou autres personnalisations. Ajustez ces réglages depuis les collections et bibliothèques existantes. La limite choisie lors d’un réimport ne remplace donc pas celle d’une sélection déjà créée.

Les ajouts, renommages, masquages et suppressions de bibliothèques sont transmis à Plex sans redémarrer Ether. Rechargez le client Plex si sa liste reste en cache. Masquer les bibliothèques générales Movies / TV Shows ne désactive pas les bibliothèques Xperience : celles-ci gardent leur propre publication et leurs droits d’accès. Une collection vide n’est pas forcément encore en chargement : vérifiez aussi ses paramètres, les filtres et l’état de sa source.


<a id="nav-configuration-rapide-jukebox"></a>

## Configuration rapide JukeBox

Dans **Bibliothèques** ou **Ether-Remix → Contenus**, ouvrez **Configurer avec JukeBox**. Films, Séries et Animes sont précochés ; **Créer ma sélection** crée leurs collections et bibliothèques communes à Plex, Jellyfin, Emby, Stremio, Xtream et Silo. Pour des rangées automatiques sans nouvelle bibliothèque, choisissez **Collections / sélections automatiques seulement**. Les collections sont proposées dans l’onglet Collections, sur l’accueil et dans les sections compatibles avec ces rangées.

**Choisir parmi toutes les listes JukeBox** charge les listes publiques de [son profil MDBList](https://mdblist.com/lists/jukebox8786/). Recherchez par exemple « Documentaire », cochez vos listes et ajustez le type Films / Séries / Films et séries. Les trois propositions restent utilisables si le catalogue distant est indisponible. Une liste mixte crée deux collections et deux bibliothèques, une par type ; la proposition Films ne conserve que les films de la liste « Les plus piratés ».

Les collections s’actualisent chaque jour, ne retiennent que les titres sortis et les trient par date de sortie décroissante. Le tri général des bibliothèques doit rester sur **Date de sortie** (valeur par défaut) ; un tri explicitement enregistré dans un client reste prioritaire. Le remplissage initial se fait en arrière-plan. Les listes n’ajoutent aucune source de lecture, ne téléchargent pas les vidéos et ne garantissent pas une VF : vos sources, filtres de langue, droits des spectateurs et règles de disponibilité continuent de s’appliquer.

Un nouvel essai réutilise les éléments déjà créés, y compris après une erreur partielle, sans écraser vos modifications. Vous pouvez ensuite renommer ou masquer les bibliothèques et modifier les sources, la fréquence, le tri et les destinations des collections. Une bibliothèque supprimée peut être recréée en relançant la sélection ; supprimer une bibliothèque ne supprime pas sa collection.

<a id="plex-home"></a>
## Accueil Plex

Dans `/ether/home`, sélectionnez le même spectateur et le même type d’appareil que dans Plex : « Spectateur type » ne représente pas un compte personnalisé. Les réglages globaux et les remplacements du spectateur se cumulent. L’aperçu de « Reprendre » inclut les épisodes suivants ; « À suivre » est masqué lorsqu’il répète les mêmes épisodes. Les pages recommandées Films et Séries restent indépendantes de l’accueil.

Plex Web et les applications peuvent présenter les cartes différemment et conserver une ancienne réponse en cache. Après une modification, rechargez l’accueil du client. Les requêtes par bibliothèque ne doivent pas ajouter toutes les rangées Films puis Séries à l’accueil. Infuse conserve sa présentation spécifique par collections.
