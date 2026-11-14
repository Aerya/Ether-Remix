# Source XDCC (via XDCC Grabscher)

Option de **Sources → XDCC**, désactivée par défaut. Remix cherche les films et les épisodes proposés par les bots XDCC des réseaux IRC que suit votre [XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher) (fork de CaptainEpix, version Mono/Docker). Il les télécharge ensuite à la demande, et peut les lire pendant le transfert.

> **Les fichiers sont écrits sur votre disque.** Contrairement aux comptes debrid, rien n'est lu « à distance » : Grabscher télécharge le fichier chez lui, Remix le lit dans son dossier. Gardez cette source désactivée si votre serveur ne doit stocker aucun média.

## Comment ça marche

1. **Recherche.** Remix envoie à Grabscher le titre, l'année (film) ou `S01E02` (épisode). Seuls les noms de fichiers qui correspondent au titre, à l'année ou à l'épisode sont gardés, puis triés par Ether avec vos filtres et profils habituels. Les bots hors ligne, ignorés ou en échec récent sont écartés.
2. **Lecture.** Quand une release XDCC est choisie, Remix demande le paquet au bot via Grabscher et attend jusqu'à 14 secondes.
3. **Streaming.** Si le tampon (8 Mo par défaut) arrive dans ce délai, la lecture démarre sur le fichier en cours de transfert et avance au rythme du bot. Sinon, le spectateur reçoit un message (« le transfert commence, relancez dans un instant », « en attente du bot », « bot hors ligne »…) et Ether essaie les autres sources de la liste.
4. **Fin.** Une fois complet, le fichier reste dans le dossier de Grabscher : cache temporaire ou fichier gardé, selon le mode.

Grabscher est un **service à part**. Remix n'ajoute aucun conteneur obligatoire : sans Grabscher, la source reste simplement inactive.

## Installation avec Docker Compose

Remix doit **voir les dossiers de Grabscher** : le dossier de téléchargement (`dl`) et, pour le streaming, le dossier temporaire (`tmp`). Le plus simple est de les monter en lecture seule dans Remix.

```yaml
services:
  ether-remix:
    image: ghcr.io/aerya/ether-remix:latest
    volumes:
      - ./config:/config
      - ./cache:/cache
      # Dossiers de Grabscher, en lecture seule
      - ./xdcc-grabscher/.config/XG/dl:/xdcc/dl:ro
      - ./xdcc-grabscher/.config/XG/tmp:/xdcc/tmp:ro

  xdcc-grabscher:
    image: ghcr.io/captainepix/xdcc-grabscher:latest
    container_name: xdcc-grabscher
    restart: unless-stopped
    environment:
      # Un dossier par catégorie : dl/remix (gardés) et dl/remix-cache (cache)
      - XG_CATEGORY_FOLDERS=remix,remix-cache
    volumes:
      - ./xdcc-grabscher:/config   # réglages, base et téléchargements
    ports:
      # Interface web pour le réglage, sur cette machine seulement.
      # Jamais "5556:5556" vers Internet.
      - "127.0.0.1:5556:5556"
```

- **Un seul volume pour Grabscher** : `dl` et `tmp` sont alors sur le même disque, et le déplacement d'un fichier terminé est instantané.
- **Droits** : Grabscher tourne avec l'utilisateur 99:100. Le dossier `./xdcc-grabscher` doit lui être accessible en écriture, et ses fichiers lisibles par l'utilisateur de Remix.
- **Lecture seule** : Remix ne supprime jamais rien lui-même. Les effacements du cache passent par Grabscher.

Dans la console Remix, **Sources → XDCC** :

| Réglage | Valeur pour cet exemple |
|---|---|
| Adresse de Grabscher | `http://xdcc-grabscher:5556` |
| Clé d'API de Grabscher | créée dans Grabscher, voir plus bas |
| Correspondance des dossiers (avancé) | `/config/.config/XG=/xdcc` |

Le bouton **Tester** vérifie la connexion, la clé, les serveurs IRC connectés et la visibilité des deux dossiers.

### Réglage de Grabscher

1. Ouvrez `http://127.0.0.1:5556` sur la machine (ou par un tunnel SSH). Mot de passe par défaut : `xgisgreat`, à changer.
2. Ajoutez vos serveurs IRC et vos canaux XDCC.
3. Dans **engrenage › Api Keys**, créez une clé **et activez-la** : une nouvelle clé y est désactivée.
4. Collez la clé dans Remix. Elle n'est jamais réaffichée, jamais envoyée au navigateur ni dans une adresse.

### Grabscher derrière un VPN (gluetun)

Grabscher peut partager le réseau d'un conteneur gluetun (`network_mode: "service:gluetun"`) pour que l'IRC passe par le VPN.

- Remix joint alors Grabscher par le nom du conteneur gluetun : `http://gluetun:5556`. Selon la configuration de son pare-feu, il faut autoriser le port 5556 depuis le réseau Docker.
- Le port 5556 ne doit jamais être ouvert côté VPN.

### DCC passif

La plupart des bots ouvrent eux-mêmes un port. Certains n'offrent que le **DCC passif** : Grabscher doit alors ouvrir un port joignable depuis Internet. C'est le seul port à exposer, jamais le 5556.

- Sans VPN : redirigez quelques ports sur votre box (ex. `50000-50004`), publiez-les sur le conteneur et indiquez `XG_PASSIVE_DCC_PORTS=50000-50004`.
- Avec gluetun : il faut un fournisseur qui redirige un port (AirVPN le permet). Port fixe : `XG_PASSIVE_DCC_PORTS=<port>` et ce port dans `FIREWALL_VPN_INPUT_PORTS` de gluetun. Port changeant : partagez le fichier de port de gluetun et indiquez `XG_PASSIVE_DCC_PORTS_FILE`.
- Sans DCC passif, ces bots sont signalés en échec et écartés des recherches.

Détails : [README de Grabscher](https://github.com/CaptainEpix/xdcc-grabscher#passive-dcc).

## Les trois modes

| Mode | Lecture | Fichier |
|---|---|---|
| **Streaming** (défaut) | pendant le transfert | cache temporaire (`dl/remix-cache`), effacé après la durée de conservation |
| **Streaming et fichier gardé** | pendant le transfert | gardé (`dl/remix`), jamais effacé par Remix |
| **Téléchargement seulement** | une fois le fichier complet | gardé (`dl/remix`) |

Le streaming exige que Remix voie le dossier `tmp`. Sinon, la console l'indique et la lecture attend la fin du transfert. Elle ne propose jamais un streaming qui ne marche pas.

Depuis la console, une recherche manuelle permet de lancer un téléchargement gardé. **Garder** protège un fichier du cache contre le nettoyage, sans le déplacer. Si la suppression a déjà eu lieu, une erreur est affichée. Le début d’une lecture et la conservation sont coordonnés avec le nettoyage.

Avant un nouveau téléchargement, Remix libère si nécessaire les fichiers terminés les plus anciens qui ne sont ni gardés, ni lus récemment, ni dans une bibliothèque. Les limites restent appliquées lorsque plusieurs spectateurs lancent une lecture simultanément.

## Réglages

| Réglage | Variable | Défaut | Rôle |
|---|---|---|---|
| Activer | `PSE_XDCC` | off | Nécessite l'adresse et la clé. |
| Adresse de Grabscher | `PSE_XDCC_URL` | | Adresse interne (Docker ou réseau local), sans identifiants. |
| Clé d'API | `PSE_XDCC_KEY` | | Clé activée dans Grabscher. |
| Mode | `PSE_XDCC_MODE` | `stream` | `stream`, `both` ou `download`. |
| Téléchargements simultanés | `PSE_XDCC_MAX_ACTIVE` | 2 | Au-delà, la lecture explique qu'il faut attendre. |
| Taille du cache | `PSE_XDCC_CACHE_GB` | 50 | En Go. Au-delà, les plus anciens non lus sont effacés. 0 : pas de plafond. |
| Conservation du cache | `PSE_XDCC_CACHE_HOURS` | 48 | Heures depuis la dernière lecture. |
| Place libre minimum | `PSE_XDCC_MIN_FREE` | 20 | En Go. Sous ce seuil, aucun téléchargement. 0 : pas de contrôle. |
| Lancements par jour | `PSE_XDCC_MAX_DAY` | 20 | Plafond appliqué aux lectures ; les lancements manuels peuvent le dépasser. |
| Tampon avant lecture | `PSE_XDCC_BUFFER_MB` | 8 | Mo reçus avant d'ouvrir la lecture. |
| Attente maximale d'un bot lent | `PSE_XDCC_WAIT` | `10s` | De 2 à 12 s, voir « Streaming ». |
| Correspondance des dossiers | `PSE_XDCC_PATH_MAP` | | `dossier Grabscher=dossier Remix`, séparés par `;`. |
| Dossier temporaire | `PSE_XDCC_TEMP_DIR` | | Vide : le `tmp` voisin du dossier de téléchargement. |
| Résultats par recherche | `PSE_XDCC_MAX_RESULTS` | 50 | Paquets lus par requête. |
| Recherches par jour | `PSE_XDCC_SEARCH_DAILY` | 3000 | Plafond de la source dans Ether. |
| Bots ignorés | `PSE_XDCC_BOTS_EXCLUDE` | | Noms séparés par des virgules. |

## Ce qui n'est jamais effacé

Le nettoyage du cache ne touche qu'aux fichiers que Remix a lui-même demandés en mode Streaming. Il épargne toujours :

- les fichiers gardés (modes « gardé » et « téléchargement », lancements manuels, bouton **Garder**) ;
- un fichier en cours de lecture ;
- tout fichier rangé dans une bibliothèque locale de Remix ;
- tout ce que Grabscher a téléchargé sans Remix.

Si Remix ne peut pas lire la liste des bibliothèques, il n'efface rien.

## Indexation en arrière-plan

XDCC n'est interrogé par l'indexation que s'il est coché dans **Indexation en arrière-plan → Sources importées**. Ses recherches suivent le plafond de la source.

Un paquet indexé ne suffit pas à rendre un titre « lisible » : il ne compte que s'il a été revu sur un bot en ligne, sans échec récent, dans les 48 dernières heures. Il n'est jamais marqué « en cache » : il se télécharge d'abord. Même compté, il peut attendre dans la file du bot.

## États dans la console

| État | Sens |
|---|---|
| Disponible | Paquet trouvé sur un bot en ligne. |
| Bot hors ligne | Bot absent du réseau IRC. |
| En attente | Demandé, le bot n'envoie pas encore (file d'attente, places prises). |
| En cours | Transfert en cours, avec vitesse et progression. |
| Prêt à lire (en cours) | Assez d'octets reçus pour lire pendant le transfert. |
| Prêt à lire | Fichier complet. |
| Échec | Bot muet, refus, DCC passif impossible… avec la raison donnée par Grabscher. |

Un même paquet n'est jamais demandé deux fois : plusieurs spectateurs partagent le même transfert.

## Streaming : ce qui marche, ce qui ne marche pas

XDCC envoie un fichier **dans l'ordre, du début à la fin**. Grabscher ne sait pas demander une autre position au bot. Remix le respecte, sans inventer d'octets :

- **Lecture du début** : elle démarre dès le tampon reçu et suit le transfert.
- **Retour en arrière** : immédiat, dans la partie déjà reçue.
- **Saut en avant dans la partie reçue** : immédiat.
- **Saut au-delà de la partie reçue** : Remix attend au plus le délai réglé (10 s par défaut) si les octets arrivent à temps. Sinon il répond tout de suite « pas encore disponible ». Le lecteur peut réessayer, la suite du film n'est pas bloquée.
- **Formats** : MKV et MP4 « optimisés web » se lisent pendant le transfert. Un MP4 dont l'index est à la fin, ou un AVI, ne se lit qu'une fois complet ; le spectateur en est averti.
- **Vitesse** : si le bot envoie moins vite que le débit du film, la lecture s'arrête pour charger. Rien ne peut l'éviter, sauf attendre la fin du transfert.

### Résultats mesurés (labo de faux bots, aucun bot public)

Avec le labo fourni par Grabscher (`tools/lab`), un bot à 2 Mo/s et un vrai MKV H.264/AAC de 2 minutes :

- lecture possible **4,3 s** après le clic (8,5 Mo reçus sur 47) ;
- ffprobe et ffmpeg (le moteur de Jellyfin et d'Emby) lisent et décodent pendant le transfert ;
- un saut à 100 s alors que 18 % étaient reçus : refus net, pas de fausses images ;
- lecture complète par le moteur d'Ether : octets identiques à l'original ;
- MP4 avec index à la fin : message « ne se lit qu'une fois complet », puis lecture normale ;
- bot muet : échec expliqué après le délai de Grabscher ; bot qui refuse : échec expliqué ; ces paquets ne sont plus proposés ensuite.

## Limites connues

- **Ordre des sources.** Ether trie d'abord par qualité. Une release XDCC meilleure qu'une release debrid peut passer devant : son téléchargement démarre, puis Ether lit la release debrid suivante si le bot n'est pas prêt. Pour l'éviter, placez XDCC après vos autres sources dans l'ordre des sources d'Ether, ou réservez-le aux titres introuvables ailleurs.
- **Redémarrage.** Les liens de lecture locaux changent à chaque démarrage de Remix. Relancez la lecture ; le transfert, lui, continue chez Grabscher.
- **Recherche par nom seulement.** Les paquets XDCC n'ont qu'un nom de fichier : pas d'identifiant IMDb ou TMDB. Les packs de saison et la numérotation absolue des animés ne sont pas reconnus.
- **Aucune garantie de lecture immédiate.** Les bots ont leurs files d'attente et leurs limites ; Remix les affiche mais ne peut pas les contourner.

## Tester avec le labo

Les tests d'intégration réels sont ignorés par défaut. Démarrez le labo de Grabscher (`tools/lab/run.sh`, bots IRC simulés, sans Internet), puis :

```bash
XDCC_LAB_URL=http://127.0.0.1:15556 XDCC_LAB_KEY=<clé du labo> XDCC_LAB_MAP=/state=<dossier hôte> go test ./internal/pse/xdcc/ -run TestLab -v
```

`XDCC_LAB_MEDIA` (dossier des vidéos servies par le bot) ajoute la comparaison octet par octet. Le scénario, l'adaptation du faux bot (il sert de vraies vidéos) et toutes les commandes sont dans [`internal/pse/xdcc/testdata/lab`](../../internal/pse/xdcc/testdata/lab/README.md).

## Crédits

[XDCC Grabscher](https://github.com/CaptainEpix/xdcc-grabscher), fork Mono/Docker de CaptainEpix du projet XG de Lars Formella. Remix n'en embarque aucun code et l'utilise par ses API.
