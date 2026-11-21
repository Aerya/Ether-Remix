# Pre (bêta)

Fonction **facultative, en bêta et désactivée par défaut**. Les flux publics et les formats d’API personnels n’ont pas encore été éprouvés en conditions réelles.

Une « pre » est l’annonce de la sortie d’une release. Remix lit ces annonces et garde celles en **FRENCH / VF**, **MULTi** et **VOSTFR / SUBFRENCH**. Il part du principe qu’une release annoncée est disponible, sous environ une heure, sur vos sources habituelles (debrid, BitTorrent, Usenet). Elle :

- compte comme **release indexée** (catalogue « lisible uniquement », priorités) ;
- ajoute son film ou sa série au catalogue **sans attendre** qu’il y arrive par TMDB.

Une pre ne contient **ni lien ni empreinte** : elle ne sert jamais à lire. La lecture passe toujours par vos sources.

## Activer

Dans la console : **Remix → Indexation → Pre (bêta)**, puis **Activer** et enregistrer. Rien n’est lu tant que l’option est désactivée.

| Réglage | Rôle |
| --- | --- |
| Activer | Interrupteur général. |
| Flux publics | [predb.net](https://api.predb.net/feed/) (flux général) et son [API animés](https://api.predb.net/?type=section&q=ANiME), puis predb.me : [films](https://predb.me/?cats=movies&rss=1), [séries](https://predb.me/?cats=tv&rss=1), [animés](https://predb.me/?cats=anime&rss=1). Décochez pour n’utiliser que vos propres sites. |
| Langues gardées | FRENCH / VF, MULTi, VOSTFR / SUBFRENCH. Les releases « autre langue + MULTi » et les corrections (FIX) sont toujours écartées. |
| Autres sites (flux RSS ou API) | Autant de sites que vous voulez (20 au plus) : un nom facultatif, l’adresse d’un flux RSS ou d’une API JSON, et une clé d’API facultative par site (envoyée en en-tête `X-API-Key` à ce seul site, jamais réaffichée, non renvoyée après une redirection vers un autre hôte). Adresses `http` ou `https` sans identifiants dedans. Dans les statistiques, ils s’appellent « Site 1 », « Site 2 »… (ou le nom donné). |
| Proxy pour les flux publics | Aucun (lecture directe), **Proxy HTTP/SOCKS** (`http://`, `https://`, `socks5://`) ou **Relais MediaFlow** (ex. `https://mediaflow.example`). Identifiants `utilisateur:mot de passe` du proxy, ou mot de passe API de MediaFlow. Ne s’applique qu’aux flux publics ; vos sites sont lus en direct. Utile quand le serveur est chez un hébergeur dont les adresses sont refusées par predb.me. |
| Délai avant prise en compte (avancé) | 1 h par défaut : une pre n’est comptée que ce temps après son annonce. `0s` : tout de suite. |
| Intervalle de lecture (avancé) | 1 min par défaut (le flux predb.net ne garde que les 20 dernières annonces), 30 s au minimum. |

## Fonctionnement

1. Chaque flux est lu à l’intervalle choisi ; les sections hors vidéo, les corrections et les langues non gardées sont écartées.
2. Une pre n’est prise en compte qu’après le délai choisi.
3. Son nom est rapproché d’un film ou d’une série : titre exact, année pour un film, aucun rapprochement si le résultat est ambigu. À défaut, Remix cherche dans TMDB et enregistre le média s’il manque.
4. Au plus 40 titres sont résolus par passage ; une pre sans média est retentée jusqu’à 6 fois puis abandonnée. Les pre traitées sont mémorisées 30 jours.

Les requêtes passent par le garde-fou réseau d’Ether (`safefetch`). L’état est affiché dans la console.

## Statistiques

**Remix → Statistiques → Résumé**, visible dès le mode Simple, affiche toujours les releases indexées au total et par source. Quand l’option est activée, un bloc **Pre (bêta)** s’y ajoute : les releases indexées au total, les releases issues des pre (avec leur part du total), les pre en attente et la date de la dernière lecture. Le détail des releases par source, dont les pre, est dans **Statistiques → Recherches** (« Releases conservées par source »). Un tableau détaille ensuite, **pour chaque flux de pre** (les cinq flux publics et vos autres sites, nommés « Site 1 », « Site 2 »… sans jamais afficher leur adresse), les pre lues sur 30 jours, les releases indexées, les pre en attente et celles dont le média est introuvable, avec un total. Une colonne **Dernière lecture** dit, pour chaque flux, s’il a répondu (nombre d’annonces reçues et retenues) ou l’erreur rencontrée (par exemple « flux refusé (403) », sans jamais afficher l’adresse), ce qui permet de savoir quel flux est inaccessible. Une pre vue par plusieurs flux est comptée pour le premier qui l’a vue ; les releases dont la pre a plus de 30 jours, ou lue avant ce suivi, sont regroupées dans une ligne « Plus anciennes ou flux inconnu ». Quand l’option est désactivée, un encart l’indique et renvoie vers **Indexation → Pre (bêta)** pour l’activer.

## Limites

- Une annonce ne prouve pas que la release est présente sur vos sources : le délai d’une heure est une estimation.
- Aucun lien n’est conservé : si aucune source ne propose la release, le titre n’est pas lisible.
- Les flux publics peuvent changer de format ou de disponibilité ; un format d’API personnel est lu de façon tolérante, sans garantie.
- **Depuis un serveur hébergé, les flux predb.me sont refusés** (erreur 403, « error code: 1005 » de Cloudflare, mesurée le 21/11/2026 depuis un serveur OneProvider) : predb.me bloque les adresses de serveurs. Le flux predb.net répond, mais ne contient que les 20 dernières annonces, toutes sections confondues : Remix le relit chaque minute et lit aussi l’API predb.net de la section animés, pour ne pas perdre d’annonces. Dans ce cas la colonne **Dernière lecture** indique « flux refusé (403) » pour predb.me et « OK · 20 annonces · 0 retenue » pour predb.net, et les compteurs restent à zéro. Les erreurs sont aussi écrites dans les journaux (`pse pre feed failed`, avec le flux concerné). Pour contourner ce blocage, renseignez un **proxy** ou un **relais MediaFlow** passant par une adresse résidentielle (réglage « Proxy pour les flux publics »), ou ajoutez un autre site auquel votre serveur a accès. Le bouton **Tester les flux** (même vue) lit chaque flux tout de suite avec les réglages affichés, même non enregistrés, et indique pour chacun le résultat, les annonces reçues et retenues et quelques exemples, sans rien enregistrer.
