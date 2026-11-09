# Sources audio gratuites : audit et validation

Vérifications réalisées le 8 novembre 2026, à partir de `e1c0871e7`.
Les services publics peuvent évoluer ; un dépôt disponible ne prouve pas que son service fonctionne.

## Résultat

**Audius est la nouvelle source retenue**, pour ses morceaux publics accessibles à l'API sans compte ni clé. Le catalogue principal reste Deezer : seuls les morceaux qui correspondent à un enregistrement de ce catalogue sont résolus. Il ne s'agit pas d'importer tout le catalogue Audius.

Aucune nouvelle source FLAC ou Hi-Res n'a été validée dans les contraintes de cet audit. Les connecteurs Eclipse et HiFi API existaient déjà ; leur présence ne garantit ni la disponibilité d'un service tiers ni ses conditions d'accès.

| Fournisseur / mécanisme | Catalogue | Streaming | FLAC | Hi-Res | Compte requis | État |
|---|---|---|---|---|---|---|
| Audius | Public, artistes participants | MP3 public vérifié | Non validé | Non validé | Non pour les titres retenus | Ajouté, recherche et décodage intégral via Ether-Remix vérifiés |
| Deezer public | Déjà utilisé, recherche vérifiée | Aperçus uniquement via l'API publique | Non | Non | Non pour le catalogue | Conservé comme catalogue, pas ajouté comme source de morceaux complets |
| Eclipse générique | Recherche des addons | URL HTTP(S) directe | Selon addon | Selon addon | Selon addon | Déjà présent ; protocole testé avec fixtures, aucun nouveau service lossless certifié |
| HiFi API / Monochrome | TIDAL | Dépend de l'instance | Annoncé | Annoncé | Accès TIDAL sous-jacent soumis à conditions | Connecteur préexistant conservé ; aucune instance gratuite ajoutée |
| Tido | Non vérifié | Non vérifié | Non vérifié | Non vérifié | Restrictions client signalées | Adresse référencée par Remux : connexion expirée après 15 s depuis l'environnement de test |
| Nullbeat | Non vérifié | Non vérifié | Non vérifié | Non vérifié | Non vérifié | Même résultat : délai de connexion, pas une preuve de panne mondiale |
| Ultra MAX Music | Deezer | Via cookie ARL personnel | Compte HiFi | Non validé | Oui, cookie personnel | Exclu |
| LastWave lossless | Sources de son addon | URL personnelle | Annoncé | Annoncé | Liaison Discord ou Telegram | Exclu |
| BitChord / modules et addons | Selon source | Selon source | Selon module | Selon module | Certains addons exigent une preuve d'authenticité BitChord | Client et protocoles, pas un service audio autonome ; aucun addon supplémentaire validé pour Ether-Remix |
| YouTube Music | Public via son client | Pas d'accès audio séparé retenu | Non | Non | Variable | Exclu : aucun parcours officiel de relais audio seul répondant aux contraintes |
| Jamendo | API | API | Non validé | Non validé | Inscription développeur pour `client_id` | Exclu ; l'identifiant de démonstration n'est pas une solution de production |
| Internet Archive | Archives publiques | Selon fichier et droits | Possible selon item | Non validé | Variable selon item | Non intégré : aucune correspondance exacte avec les enregistrements Deezer validée pendant l'audit |
| AllDebrid | Indexeurs existants | Compte existant | Selon fichier | Selon fichier | Oui | Préexistant, hors critères gratuits ; inchangé |

## Configuration et lecture

Dans **Console → Ether-Remix → Musique**, `Audius (MP3 public)` propose :

- `off` (défaut) : aucune requête Audius ;
- `first` : Audius avant les addons Eclipse et AllDebrid ;
- `last` : Audius en repli, après ces sources. Ce choix est recommandé pour conserver la priorité aux sources lossless.

Même réglage par variable `PSE_MUSIC_AUDIUS`. Les manifests Eclipse restent dans `PSE_MUSIC_ECLIPSE_URL`, essayés dans l'ordre indiqué ; retirer un lien désactive cet addon. Il n'y a pas de nouveau classement automatique de tous les flux par résolution, ni de nouvel écran de gestion des addons.

Comme le mode Eclipse existant, le mode Audius utilise les identifiants Deezer et prend le pas sur le mode catalogue HiFi API : ne pas activer Audius pour convertir une bibliothèque reposant sur des identifiants TIDAL. HiFi API n'est pas un résolveur de repli du catalogue Deezer.

Les favoris, historiques et playlists du mode Deezer conservent leurs identifiants. Plex, Jellyfin et Subsonic utilisent le même relais audio. L'en-tête `X-Accent-Audio-Source` identifie Audius ou l'hôte de l'addon Eclipse sans chemin ni jeton. Les clients tiers ne sont pas obligés d'afficher cet en-tête ; aucun nouvel affichage de source dans Plexamp ou Symfonium n'est promis.

## Fiabilité et limites

Audius utilise l'API officielle `api.audius.co`, et non une instance communautaire imposée. Seuls les résultats déclarés disponibles, lisibles et non restreints sont retenus. Une absence de métadonnées d'accès est un refus. Sans ISRC commun, artiste identique et durée à cinq secondes près sont obligatoires, puis le contrôle existant de titre/version/durée s'applique. Un morceau absent ne devient pas arbitrairement un remix ou une reprise.

Les requêtes de recherche sont bornées à 12 secondes et 4 Mio. Une réponse 429 déclenche une pause d'au moins une minute (ou `Retry-After` s'il demande davantage). Les 401/403 ne déclenchent aucune extraction de cookie, authentification ni imitation d'un autre client. Le point d'entrée officiel `/tracks/{id}/stream` fournit une redirection fraîche ; les URL signées ne sont pas persistées ni journalisées par le résolveur.

Pour Eclipse, le cache respecte désormais `expiresAt` avec une marge de cinq secondes ; une URL déjà expirée est refusée. Sans expiration déclarée, la durée maximale préexistante de dix minutes subsiste. Le mot « lossless » seul ne permet plus d'inventer un type MIME FLAC.

Le diagnostic lit au plus 64 Kio et vérifie les signatures audio. Pour FLAC, STREAMINFO fournit profondeur et fréquence ; Hi-Res est affiché uniquement à partir de 24 bits et au-delà de 48 kHz. MP4/M4A et WAV sont reconnus comme conteneurs sans prétendre connaître leur codec ou leur caractère lossless. Un grand tag ID3 ou des métadonnées dépassant cette limite peuvent rendre le diagnostic non concluant. Une signature n'atteste pas l'origine du master ni l'absence de suréchantillonnage.

Le relais existant essaie au maximum trois URL après un refus HTTP. Un échec après l'envoi des premiers octets au client ne permet pas de remplacer silencieusement la suite du morceau. HLS/DASH et conversion de codecs ne sont pas ajoutés. Les protections existantes d'URL externes restent actives ; le diagnostic vérifie aussi les destinations des redirections. Cet audit ne constitue pas un durcissement global contre tous les scénarios DNS rebinding.

## Essais réels et tests simulés

**Réel :** recherche Deezer « Disclosure Waterfall », correspondance avec Audius, ouverture HTTP 206, signature MP3, décodage intégral par FFmpeg **à travers le relais d'Ether-Remix**, sans enregistrer le morceau. Mesure indépendante par FFprobe : MP3, 320 kbit/s, 48 kHz, 232,12 s. Aucun compte, clé ni cookie fourni. Ce résultat concerne ce morceau et cet instant, pas la totalité du catalogue. Aucun essai physique de Plexamp, Symfonium, Feishin ou Supersonic n'a été réalisé.

Reproduction explicite (réseau public et FFmpeg nécessaires) :

```sh
ACCENT_MUSIC_LIVE=1 go test ./internal/pse/music -run TestAudiusLive -v
```

**Simulé :** recherche et refus de morceaux restreints/incomplets, versions live et artistes incorrects, gestion 401/403/429/500 et JSON invalide, priorité, non-réutilisation d'une URL refusée, repli après un HTTP 403 audio, signatures/STREAMINFO et expiration. Les tests habituels de musique, console, Plex et Jellyfin couvrent les régressions des interfaces. Les essais réseau sont désactivés par défaut en CI.

## Sources de l'audit

- [Protocole Eclipse : recherche, flux, expiration et codecs](https://eclipsemusic.app/docs).
- [Audius : documentation API et accès en lecture](https://docs.audius.co/api/), [licence musicale](https://audius.org/open-music-license.pdf).
- [Monochrome : liste des instances](https://github.com/monochrome-music/monochrome/blob/main/public/instances.json), [conditions du lecteur TIDAL](https://developer.tidal.com/documentation/guidelines/guidelines-changelog).
- [Remux : connecteur Eclipse et adresses suggérées](https://github.com/lostb1t/remux/blob/main/crates/remux-server/src/addons/eclipse.rs), [Wesper : client à source externe](https://github.com/itsmeadarsh2008/wesper).
- [BitChord : registre des sources](https://github.com/kushagrasinghx/BitChord/blob/main/app/src/main/java/com/music/bitchord/data/sources/SourceRegistry.kt), [authentification des addons](https://github.com/kushagrasinghx/BitChord/blob/main/app/src/main/java/com/music/bitchord/data/sources/addon/AddonClient.kt).
- [Ultra MAX Music : ARL requis](https://github.com/PaRaN01a-hash/ultramax-music), [LastWave : conditions de l'addon lossless](https://lastwave.app/).
- [Jamendo : inscription pour client_id](https://developer.jamendo.com/v3.0/authentication), [YouTube : restrictions sur l'audio séparé](https://developers.google.com/youtube/terms/developer-policies).
- [Internet Archive : connecteur examiné](https://github.com/DancingMusic/MusicConnect-Archive).
