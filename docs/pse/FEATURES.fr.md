# Fonctionnalités détaillées

[Retour au README](../../README.md) · [Guide de configuration](GUIDE.fr.md)

## Fonctionnalités Remix

<a id="nav-films-series-et-animes-selon-vos-preferences"></a>

### Films, séries et animés selon vos préférences

- **Qualité en Direct Stream** : les nouvelles configurations ne proposent plus de variantes réencodées par défaut lorsque la vidéo peut être copiée. Dans Lecture → Transcodage, réactivez-les pour les connexions limitées ou désactivez aussi le démarrage accéléré pour préserver l’image dès le début. Les réglages enregistrés restent conservés.
- **Profils de qualité** : formats, codecs, résolutions, HDR/Dolby Vision, taille, langues et regex, globalement ou par bibliothèque/collection. Mode profils ou critères simples, sans cumul ; formats détaillés, résolution inconnue, langue originale et cache debrid annoncé. Import AIOStreams par JSON ou manifest/source enregistrée avec le mot de passe de configuration, aperçu et modification avant enregistrement (variantes non reproduites).
- **Catalogue orienté français** : choisissez vos langues et variantes — VFF, VFQ, VFi, MULTi… — et vos exigences de disponibilité en cache. Un film pas encore disponible en français n’apparaît ni sur l’accueil (Tendances comprises) ni dans les collections.
- **Catalogue « lisible uniquement »** : affichez les titres disposant d’un minimum de releases correspondant à vos critères. La règle vaut aussi pour l’accueil et les collections ; la recherche, la watchlist, la reprise de lecture et les collections de sorties à venir restent complètes.
- **Exceptions ciblées** : autorisez d’autres langues pour certaines collections ou bibliothèques, sans modifier vos préférences générales. Ces titres sans VF n’apparaissent que dans leur bibliothèque ou leur collection, jamais dans Films, Séries ou l’accueil.
- **Tri par date de sortie** : Films et Séries s’ouvrent du plus récent au plus ancien dans Plex, Jellyfin et Emby (réglage `PSE_LIBRARY_SORT`). Un tri choisi dans l’application reste prioritaire.
- **VOSTFR à la demande** : ajoutez les titres souhaités ; chaque ajout lance une recherche prioritaire lorsque l’indexation est active. En option, SubMaker prépare leurs sous-titres français, et chaque titre affiche l’état de ses sous-titres.
- **Animés** : créez leurs bibliothèques et collections à partir de TMDB, avec vos préférences FR/MULTi et les addons Stremio de votre choix.
- **Identification des releases** : rapprochement des titres accentués, apostrophes et titres alternatifs ; les numéros de partie doivent être corroborés par le catalogue.

<a id="nav-vos-videos-locales-films-et-videos-personnelles"></a>

### Vos vidéos locales, films et vidéos personnelles

- **Santé des médias locaux** : section dans **Sources → Fichiers locaux**, chargée à sa première ouverture, pour les diagnostics des dossiers, fichiers absents/non identifiés, doublons potentiels et tailles par dossier. Les entrées restent au catalogue si un montage devient vide ou inaccessible ; aucun fichier supprimé.
- **Dossiers de l’hôte ou montages réseau** : exposez-les par des volumes Docker en lecture seule. Analyse à la demande et chaque heure, sans déplacer ni supprimer vos fichiers.
- **Rapprochement avec le catalogue** : recherche dans les fiches déjà présentes dans Remix, puis TMDB ; films et épisodes identifiés réutilisent leurs fiches. Sans correspondance fiable, les vidéos personnelles restent affichées telles quelles.
- **Bibliothèques et collections par dossier** : rattachez vos dossiers vidéo aux bibliothèques et collections personnalisées depuis Sources → Fichiers locaux. Privés par défaut, avec partage facultatif ; les destinations respectent les accès des dossiers. [Configuration et formats](GUIDE.fr.md#médias-locaux-et-ibroadcast).

<a id="nav-des-sources-reunies-et-une-recherche-anticipee"></a>

### Des sources réunies et une recherche anticipée

Les indexeurs (Prowlarr, Jackett, NZBHydra2) trouvent les releases. Pour les lire, configurez la voie adaptée : compte débrideur, client BitTorrent ou addon Stremio comme Seedstrem. Seedstrem utilise son propre client torrent. Pour Usenet, Remix lit via son moteur NNTP et vos fournisseurs Usenet ; SABnzbd/NZBGet ne sont pas des clients externes intégrés.

- **Démarrage rapide** : la vue **Vue d’ensemble → Démarrage** guide les premiers réglages — sources, indexation vidéo, langues et qualité, statistiques et clients. Un rappel signale lorsque la recherche automatique est désactivée.
- **Console Remix** : réglages regroupés dans `/ether/remix`, blocs repliables fermés par défaut ; iBroadcast rejoint les fournisseurs musicaux et les informations Subsonic restent dans Contenus → Musique.
- **IPTV et VOD Xtream** : chaînes en direct, guide EPG et import des films et séries du fournisseur.
- **Configuration commune à Ether et Remix** : addons Stremio, indexeurs, services Usenet, comptes debrid, sources musicales et IPTV réunis dans un même espace.
- **Seedstrem auto-hébergé** : ajoutez son manifeste depuis Sources → Stremio ; la liste des modules s’ouvre après enregistrement pour le retrouver et le modifier. Recherche Prowlarr et lecture pendant le téléchargement via qBittorrent ou Deluge ; les fichiers restent sur l’hôte Seedstrem, sans volume à monter dans Remix. Téléchargement réservé à la lecture, avec secours optionnel après échec des autres sources et recherches conservées pour l’indexation. [Installation et raccordement](SEEDSTREM.fr.md).
- **Catalogues Stremio** : synchronisation manuelle en arrière-plan, état visible et conservation du dernier import en cas d’échec.
- **Indexation en arrière-plan** : recherchez les releases via vos addons et indexeurs Prowlarr, Jackett ou NZBHydra2 avant le lancement d’une lecture.
- **Panne temporaire d’un indexeur** : réutilisez ses résultats expirés depuis moins de 24 heures, sans prolonger leur validité ni contourner les vérifications de lecture. Les erreurs d’accès et les quotas restent respectés.
- **Recherches maîtrisées** : budgets quotidiens par source, pause pendant la lecture, priorités par collection ou bibliothèque et recherche manuelle immédiate.
- **Recherche depuis Plex ou Jellyfin** : les premiers résultats jamais cherchés ou restés sans release passent en priorité, selon leur délai de nouvelle tentative.
- **Disponibilités SeerrCatalog** : importez les disponibilités confirmées et les sources récentes de [SeerrCatalog](https://github.com/Aerya/Stremio-Seerr-Catalog), sans nouvelle recherche pour le catalogue « lisible uniquement ». La confirmation persiste jusqu’à suppression ; les sources de lecture périmées sont recherchées à nouveau si nécessaire. Un titre trouvé seulement en VOSTFR (repli de SeerrCatalog) va dans la bibliothèque **VOSTFR** et les collections **SeerrCatalog**, jamais dans Films ni Séries.
- **Recherches pour SeerrCatalog** : autorisez les recherches complémentaires dans les sources Remix, notamment Prowlarr, avec une clé dédiée. Recherche et partage des disponibilités sont deux fonctions indépendantes, regroupées dans les réglages SeerrCatalog.
- **Client torrent en option** : envoyez les releases de Prowlarr ou Jackett à un à trois clients qBittorrent ou rTorrent/ruTorrent, avec ratio et durée de partage par client et par tracker, cross-seed et alerte de place libre. Le client peut être distant, mais Remix doit voir son dossier de téléchargement (même machine ou partage NFS/SMB) pour lire les fichiers. Désactivé par défaut ; les comptes debrid restent en cache uniquement. [Détails](TORRENT.fr.md)
- **Source XDCC en option** : cherchez films et épisodes chez les bots XDCC suivis par votre [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher), puis lisez-les pendant le transfert, en téléchargement seulement, ou les deux. États des bots et des transferts, cache libéré avant un nouveau transfert, limites respectées lors des lectures simultanées et fichiers gardés protégés du nettoyage. Grabscher reste un service à part, jamais exposé à Internet. Désactivé par défaut : les fichiers sont écrits sur le disque. [Mise en place](XDCC.fr.md)
- **Source FTP / SFTP en option** : films et épisodes d’un serveur distant, index local et lecture directe avec déplacements dans la vidéo. SFTP, FTPS et FTP ; copie complète ou streaming avec copie gardée en option. Désactivée par défaut, lecture distante sans copie complète comme mode initial (cache habituel d’Ether conservé). [Configuration et stockage](FTP.fr.md)
- **Base StreamFusion Reborn** : importez les releases françaises, MULTi ou VOSTFR et recherchez directement films, épisodes et packs dans votre base en lecture seule. Les fichiers Alkodiques déjà vérifiés sont importés et lus via leurs liens AllDebrid, sans ajout de magnet ; un compte AllDebrid configuré reste nécessaire. Pour les torrents ordinaires, les preuves de cache debrid expirent ; la lecture revérifie la disponibilité et conserve le recours aux autres sources. Les titres ajoutés plus tard au catalogue sont aussi rattrapés. Ils passent devant les autres sources s’ils respectent le profil (réglage `resolver.verifiedFiles` : `first` par défaut, `tie` ou `off`). Quand AllDebrid annonce son quota journalier atteint, le compte est mis en pause 1 heure puis testé par une seule requête (un essai par heure et par nœud en cluster) ; les secours de lecture qui dépendent du debrid sont écartés quand aucun compte n’est utilisable (quota ou clé refusée sur tous les fournisseurs) et le nombre de tentatives est plafonné (`resolver.failoverAttempts`, de 1 à 10, 4 par défaut). La page Debrid affiche alors « Quota atteint » avec l’heure du prochain essai, et efface la dernière erreur au premier succès. [Mise en place](STREAMFUSION.fr.md). Une absence de cache explicitement constatée sur un nouvel ajout AllDebrid évite de répéter la même demande sur ses autres comptes ; les erreurs propres à un compte et les anciens torrents incomplets conservent les comptes de secours.

**Index des releases** : les recherches et imports conservent automatiquement les références chiffrées pour les réutiliser à la lecture et en secours. Seule la recherche automatique de fond se règle dans Remix → Indexation ; son arrêt conserve l’index et sa réutilisation. Chaque fiche du catalogue propose les releases indexées, triables par résolution, taille, codecs, source, langues et dates. Les fichiers Alkodiques gardent leur lecture AllDebrid sans magnet. [Fonctionnement et limites](RELEASE_LIBRARY.fr.md). Les noms et années doivent correspondre même si la source fournit le bon identifiant ; les réponses ambiguës sont refusées. Un nouvel audit et une file de reprise récupèrent progressivement les références des contenus déjà indexés.

**Indexation → Releases** : recherche globale de médias et de releases, catégories Films/Séries/Animes, périodes et dates personnalisées, séries regroupées et releases dépliées sous chaque média, sans changer de vue. Chargement ciblé et conservation de la liste lors des rafraîchissements ou erreurs. [Guide](RELEASE_LIBRARY.fr.md).

La vue **Indexation → Releases** est toujours accessible. Sources, réglages et activité sont regroupés dans **Indexation**. Pour les séries, la recherche initiale couvre S01E01 ; les autres épisodes sont conservés au fil des recherches.

<a id="alldebrid-sur-un-vps--mediaflow-et-gluetun"></a>
<a id="nav-alldebrid-sur-un-vps-mediaflow-et-gluetun"></a>


Les refus AllDebrid `MAGNET_TOO_MANY` suspendent uniquement les nouveaux ajouts après des refus répétés : les torrents déjà présents et les liens fichiers restent utilisables. La bibliothèque de releases ne transforme pas un hash inconnu en lien de fichier.

**Renouvellement du lien pendant la lecture** : par défaut, quand le serveur média d’un debrid tombe en panne pendant une lecture (5xx, coupure, 429), Remix demande un nouveau lien, voire un autre fournisseur. Sur un compte partagé, ces demandes entament le quota de liens et peuvent bloquer les autres utilisateurs. Dans **Sources → Debrid**, choisissez **Garder le lien et réessayer** (`debrid.playbackRelink` = `"replay"`) : Remix réessaie le lien existant lors des erreurs du serveur média, sans demander systématiquement un nouveau lien. Un lien réellement mort (401, 403, 404, 410, redirection) reste renouvelé ; dans ce mode, au plus une fois toutes les 30 secondes par fichier. Le changement s’applique sans redémarrage.

### AllDebrid sur un VPS : MediaFlow et Gluetun

AllDebrid peut bloquer les IP de VPS/serveurs, de datacenters et certaines sorties VPN, notamment **Cloudflare WARP**. Des accès depuis plusieurs pays ou assimilés à du partage peuvent entraîner une suspension, voire un bannissement du compte.

Dans **Sources → Debrid** (`/ether/remix/sources?source=debrid`), activez **Utiliser MediaFlow pour AllDebrid**, puis renseignez l’adresse et le mot de passe API de votre instance. Les appels API et fichiers AllDebrid récupérés par Remix passent alors par MediaFlow, sans repli direct en cas de panne. L’option est désactivée par défaut et nécessite une instance MediaFlow existante ; elle n’installe aucun conteneur.

Sur un VPS, faites sortir **MediaFlow via Gluetun**, connecté à un VPN accepté ou à votre tunnel domestique. MediaFlow seul conserve l’IP du serveur. Vérifiez les fournisseurs VPN proposés dans [votre compte AllDebrid](https://alldebrid.fr/account/). [AirVPN est une possibilité — lien affilié](https://airvpn.org/?referred_by=483746) ; sa présence dans la liste ne garantit pas l’acceptation de toutes ses IP.

Appliquez **la même sortie à tous les outils de cette machine** accédant à AllDebrid, y compris les workers, planificateurs et scripts de nettoyage. Pour un outil sans intégration MediaFlow, utilisez le proxy Gluetun associé. Les appels effectués par des addons hébergés ailleurs ne sont pas contrôlés par Remix. [Installation et vérifications](../alldebrid-mediaflow.md). Le panneau propose un lien vers le Compose MediaFlow + Gluetun et un bouton de test AllDebrid via la connexion enregistrée.

**Fichiers Compose prêts à utiliser :** [Ether-Remix](../../docker-compose.example.yml) · [MediaFlow + Gluetun](../../docker-compose.mediaflow-gluetun.yml) · [Prérequis et raccordement](../alldebrid-mediaflow.md).

Un proxy HTTP/HTTPS/SOCKS5 existant (par exemple SSDV2 Proxy Manager) peut aussi être utilisé directement dans **Réglages → Général → Proxy pour les sources**, sans MediaFlow. Collez son URL complète : les identifiants sont répartis dans les champs séparés, puis enregistrez. Remix et le proxy doivent partager un réseau accessible. Activez les routes Debrid et, pour les flux issus des addons, Stremio.

**Proxys par source** : chaque compte débrideur peut utiliser son propre proxy HTTP/HTTPS/SOCKS5 pour ses appels API et la lecture vidéo ou musicale. Dans **Sources → Stremio**, choisissez séparément le proxy des requêtes (manifeste, recherches) et celui des flux HTTP. Les blocs acceptent les URL complètes avec identifiants. Un proxy spécifique prend priorité sur MediaFlow et le proxy global, sans repli direct ; désactivé, il conserve le comportement global. Les torrents résolus par Remix utilisent le proxy du compte débrideur.

<a id="nav-sous-titres-externes-et-traduction-avec-submaker"></a>

### Sous-titres externes et traduction avec SubMaker

- **Instance au choix** : utilisez [SubMaker](https://github.com/xtremexq/StremioSubMaker) [public](https://submaker.elfhosted.com/configure) ou auto-hébergé avec un manifeste personnel ; l’intégration est désactivée par défaut.
- **Langues et périmètres** : choisissez les langues source/cible, anglais → français par défaut, et autorisez le traitement globalement ou par bibliothèque/collection.
- **Préparation contrôlée** : recherche d’un SRT correspondant à la release, traduction si nécessaire, file d’attente, cache et budget journalier. Seuls les résultats complets et validés sont retenus ; les traductions doivent conserver les timecodes et le nombre de répliques.
- **Lecture et SeerrCatalog** : ajoutez le SRT préparé aux pistes de la release correspondante et transmettez-le à SeerrCatalog par la recherche authentifiée. Une traduction en attente ne confirme pas la disponibilité ; une lecture déjà ouverte doit être relancée après préparation.
- **Release déjà sous-titrée en priorité** : à qualité, langue audio et score de formats égaux, la lecture choisit la release dont le SRT est déjà prêt plutôt qu’une autre ayant plus de seeders ou un fichier plus gros. [Configuration et limites](SUBMAKER.fr.md).

<a id="nav-des-bibliotheques-et-collections-personnalisees"></a>

### Des bibliothèques et collections personnalisées

Les **modèles de disposition** enregistrent l’accueil, les bibliothèques et la présentation des collections : sauvegardez l’existant, dupliquez A/B/C et réappliquez une variante avec retour en arrière. Les bibliothèques à thème Plex peuvent combiner dossiers locaux et sources indexées et cibler tous les utilisateurs ou une sélection ; les contenus privés restent filtrés par spectateur.

**Cartes de navigation inspirées de Nuvio — BÊTA facultative** : Les services du pays se chargent automatiquement. L’éditeur s’ouvre sous la carte ; Les galeries GitHub, ThePosterDB et ACdb permettent de rechercher et sélectionner un visuel directement dans Remix. MediUX couvre les galeries publiques de genres (recherche en anglais), pas les studios/services. Fanart.tv ne dispose pas de recherche de cartes intégrée ; l’import fichier/URL reste disponible. recherchez des studios et labels sur TMDB, choisissez des services de plusieurs pays et des genres. Prévisualisez chaque carte avec un logo automatique, une image de la galerie GitHub ou un fichier/une URL importés. Les services peuvent former des groupes par pays sur les accueils Plex et Jellyfin/Emby ; le rendu dépend du client et l’accueil précédent reste restaurable. [Mode d’emploi](GUIDE.fr.md#cartes-de-navigation-inspirées-de-nuvio).

**Packs de visuels** : prévisualisez un ensemble Nuvio Assets, Kometa, nichtlegacy, Kaptain, FR-Badges, UltraMax ou TVFlix/Nuvio, puis appliquez ses correspondances aux cartes sélectionnées. Les visuels personnalisés sont protégés par défaut, les cartes sans correspondance restent inchangées et les variantes ambiguës se choisissent dans le panneau d’ajustement paginé. Import possible depuis un fichier JSON (4 Mo maximum) ou son URL HTTPS publique : seuls les noms et couvertures sont repris, jamais les addons/catalogues. Les changements sont enregistrés avec « Appliquer les cartes à mes accueils ». La personnalisation à l’unité et la recherche par galerie restent disponibles.

**Affiches des films et séries** : BetterPosters dispose déjà de son intégration ; son configurateur est accessible depuis Services. Pictorium et TOP Posters ont leurs propres comptes facultatifs, via le modèle d’URL fourni par le service. Les paramètres privés restent côté serveur et un échec conserve le recours à TMDB. Pictorium peut être hébergé ailleurs : aucune installation locale n’est nécessaire pour utiliser une instance publique.

Un **style pour plusieurs cartes** peut être préparé depuis une même galerie : choisissez les cartes, recherchez leurs visuels, filtrez par créateur, ensemble ou dossier et sélectionnez les correspondances uniques en un clic. Vérifiez les variantes avant application groupée ; les cartes sans choix conservent leur visuel. Les métadonnées disponibles varient selon la source, et un même créateur ne garantit pas un style identique. Recherche limitée à 30 cartes par lot, sans enregistrement avant « Appliquer les cartes à mes accueils ».

- **Import Xperience** : dans Contenus, collez votre manifeste [Xperience](https://xperience-app.com/), choisissez les catalogues et leurs paramètres, puis créez des bibliothèques, des collections ou les deux. Classement de la source et filtres Remix conservés ; intervalles distincts du manifeste et des nouvelles collections réglables de 1 à 168 heures. Remplissage en arrière-plan ; les bibliothèques Plex se mettent à jour sans redémarrer Ether. [Mode d’emploi](GUIDE.fr.md#import-xperience).
- **Configuration rapide française** : créez en une action vos bibliothèques Films, Séries et Animes depuis les trois listes JukeBox proposées, ou choisissez ses autres listes (documentaires, studios…). Collections seules possibles, mise à jour quotidienne et tri par date de sortie décroissante. Bibliothèques publiées dans Plex, Jellyfin et Emby ; vos sources et critères de langue restent appliqués. [Mode d’emploi](GUIDE.fr.md#configuration-rapide-jukebox).
- Organisez vos bibliothèques Plex par **service, studio, animés, documentaires, concerts ou spectacles**. Les concerts combinent des mots-clés de captation et le genre Musique pour limiter les fictions.
- Composez leurs rangées avec des collections TMDB, des catalogues Stremio importés et des modèles prêts à l’emploi.
- Réglez la fréquence d’actualisation de chaque collection, de **1 à 168 heures**, indépendamment de la recherche de releases.
- Ajustez l’apparition des films avant ou après leur sortie numérique/physique avec un **décalage en jours**.
- Profitez d’une préparation groupée des badges d’affiches dans Plex et Jellyfin, avec l’état de lecture propre à chaque spectateur et des compteurs exacts.
- Affichez les affiches plus vite : avec un fournisseur d’affiches (PostersPlus, XRDB, RPDB…), Plex garde chaque affiche au lieu de la redemander à chaque affichage, et vous réglez le délai de renouvellement. Jellyfin et Emby reçoivent l’affiche à la taille demandée, sans attente. Les grandes résolutions demandées pour les vues détaillées sont conservées.

<a id="nav-la-musique-dans-vos-applications"></a>

### La musique dans vos applications

- **Lecture musicale Plex / Jellyfin** : le cache des albums et artistes est partagé sans erreur lors du passage d’un client à l’autre.
- Parcourez le **catalogue Deezer public** depuis Plex/Plexamp, les clients Jellyfin ou une application Subsonic : Symfonium, Feishin, Supersonic, DSub…
- Écoutez via **Audius, les addons Eclipse, DMM avec Real-Debrid ou vos comptes AllDebrid, Real-Debrid et TorBox**, selon les sources activées.
- Instance DMM publique : [catalogue](https://debridmediamanager.com/albums). L’adresse est configurable dans Sources → Streaming musical → DMM / Real-Debrid pour utiliser une instance auto-hébergée disposant de son catalogue musical et de son API.
- **Musique locale** : lisez vos fichiers dans leur format d’origine, avec artistes, albums et tags, depuis des dossiers de l’hôte ou des montages réseau exposés par des volumes Docker en lecture seule. Analyse à la demande et chaque heure, sans déplacer ni supprimer les fichiers.
- **iBroadcast** : chaque spectateur peut connecter plusieurs comptes par code OAuth et retrouver leurs bibliothèques. Accès en lecture seule, lecture MP3 128 kb/s ; import des playlists en option.
- **Accès privés et partage** : dossiers musicaux et comptes iBroadcast sont privés par défaut, partageables avec certains spectateurs ou tous. Les sources autorisées apparaissent ensemble dans Musique. [Configuration et formats](GUIDE.fr.md#médias-locaux-et-ibroadcast).
- Définissez l’ordre des sources : lorsqu’une source échoue, Remix essaie les suivantes. Le mode HiFi API externe s’utilise seulement lorsqu’aucune autre source musicale n’est active.
- Retrouvez vos **favoris et votre historique par utilisateur**. La qualité disponible dépend des sources.
- **Activité musicale** : lectures en cours signalées par Plex, Jellyfin/Emby et Subsonic ; historique chronologique commun aux vidéos et à la musique ; onglet **Écoute** avec tendances quotidiennes, horaires et classements par morceau, artiste, album et auditeur, filtrables par période et utilisateur.

<a id="nav-vos-playlists-et-de-nouvelles-decouvertes"></a>

#### Vos playlists et de nouvelles découvertes

- **Parcourez les playlists Deezer** et connectez votre compte Spotify pour retrouver vos playlists accessibles.
- **Importez par lien** depuis Deezer, Spotify ou ListenBrainz, ou à partir d’un fichier CSV/XSPF.
- Vérifiez l’aperçu, choisissez le spectateur destinataire et suivez les titres retrouvés ou manquants. Les imports créent des copies personnelles sans remplacer vos autres playlists.
- Synchronisez les profils Deezer publics, Last.fm et ListenBrainz ; gérez vos playlists personnelles depuis les clients Subsonic.
- **Playlists iBroadcast** : activez leur import pour chaque compte connecté ; ordre des titres et répétitions conservés, actualisation sans doublons, sans modification chez iBroadcast.
- Explorez les **tops par pays, écoutes récentes, titres les plus écoutés et nouveautés des artistes suivis**. Genres, artistes similaires, radios et mix sont proposés selon le client.

<a id="nav-statistiques-et-suivi"></a>

### Statistiques et suivi

- **Index des releases** : les statistiques affichent les références conservées et leur état ; la vue **Indexation → Releases** donne accès aux contenus et recherches en cours.
- Remix s’ouvre sur les statistiques : anneaux de progression colorés, historique à barres fines, recherches, erreurs, quotas, durées et résultats par source en direct.
- Consultez l’historique **par jour, semaine, mois ou année**, en valeurs par période ou cumulées, avec filtres films, séries, animés et sources.
- Comparez le catalogue local aux totaux TMDB et retrouvez les opérations dans le journal d’activité.
- Les séries en cours restent dans « Continuer à regarder » même après plusieurs mois sans lecture, avec leur prochain épisode. Le tri suit le dernier visionnage ; Plex conserve une limite de 40 éléments et les masquages du spectateur. La sélection consulte uniquement l’historique du spectateur concerné, sans parcourir celui des autres comptes.
- L’import complet depuis Plex Media Server alimente aussi l’activité récente avec les dates d’origine, sans doublons. Relancez « Récupérer tout l’historique depuis ce serveur » pour compléter un ancien import ; les états locaux plus récents restent prioritaires.
- Connectez Remix à **Scrob comme un serveur Jellyfin** et choisissez dans Scrob le sens et la fréquence des synchronisations : historique de visionnage et notes, dans un sens ou les deux. Le webhook pour les événements en temps réel (délai maximal de **60 secondes**) et les bibliothèques « Vus et notés » sont facultatifs. Les arrêts et changements de lecture sont relayés par session, sans doublons.
- Associez **Last.fm** à chaque utilisateur pour ses écoutes musicales et retrouvez les statistiques dans la Vue d’ensemble.

<a id="nav-une-interface-adaptee-a-vos-habitudes"></a>

### Une interface adaptée à vos habitudes

- Sept onglets : **Vue d’ensemble** (démarrage, activité, sources, utilisation), **Sources** (avec la santé des médias locaux), **Indexation** (réglages et releases), **Lecture** (priorités, qualité, VOSTFR), **Contenus**, **Visuels** (cartes Nuvio, packs, renouvellement des affiches) et **Suivi**. Les anciens liens et favoris sont redirigés.
- Indexation automatique des addons Stremio activés ou sélection personnalisée, même vide ; filtres par instance Prowlarr/Jackett et compteurs catalogue, recherchés, avec release et affichés pour chaque bibliothèque ou collection prioritaire.
- Sources regroupées par famille, tri par catégorie ou contenu, clés debrid affichables ponctuellement et blocs repliables sans perdre la saisie. Réglez les palettes dans **Apparence**.
- Six palettes : **Ether, Nord, Catppuccin, Solarized, Gruvbox et Rosé Pine**, chacune en clair et sombre, avec aperçu immédiat et mémorisation par navigateur.

<a id="nav-la-base-ether-enrichie-par-remix"></a>

### La base Ether, enrichie par Remix

- **Nom affiché** : renommez un utilisateur dans la console pour les statistiques et l’activité, même avec un compte Plex. La connexion ne change pas.
- Synchronisation quotidienne avec le projet d’origine, contrôles avant fusion et signalement des échecs. Les bibliothèques par sources de catalogue, les améliorations IPTV et le classement des releases d’Ether coexistent avec les filtres et sources Remix.
- Images Docker pour **amd64 et arm64** et présentation des nouveautés dans la console.

## Applications compatibles

| Accès                                                 | Applications                                                                                                                                                                                    |
| ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Plex**                                              | Applications officielles : Apple TV, iOS, Android, Android TV / Shield, Fire TV, Roku, PlayStation, Xbox, TV connectées (LG, Samsung, VIDAA), ordinateur et Web. Kodi via Plex for Kodi (PM4K). |
| **Jellyfin**                                          | Infuse, Swiftfin, Findroid, Streamyfin, Jellyfin pour Android TV, JellyCon (Kodi), Jellyfin pour Roku.                                                                                          |
| **Emby**                                              | Applications Emby (Android TV, iOS, Android, Roku, TV connectées) et Infuse. Emby for TV 2.x sur Apple TV n’est pas pris en charge.                                                             |
| **Silo**                                              | Apple TV, iOS, macOS, Android, Android TV.                                                                                                                                                      |
| **Stremio**                                           | Toutes les applications Stremio, via une extension personnelle. Une adresse HTTPS est nécessaire.                                                                                               |
| **Fichiers (WebDAV)**                                 | Infuse, VLC, Kodi, rclone.                                                                                                                                                                      |
| **M3U & XMLTV**                                       | Channels DVR, TiviMate et lecteurs IPTV.                                                                                                                                                        |
| **Xtream**                                            | TiviMate, Televizo, IPTV Smarters… Désactivé par défaut.                                                                                                                                        |
| **DLNA**                                              | TV et lecteurs du réseau local.                                                                                                                                                                 |
| **Musique** _(Remix)_                                 | Plex et Plexamp, clients Jellyfin (Finamp…), applications Subsonic : Symfonium, Feishin, Supersonic, DSub…                                                                                      |

Les accès s’activent dans **Console → Applications**. Chaque spectateur trouve ses liens et mots de passe d’application sur sa page **Votre compte**.
