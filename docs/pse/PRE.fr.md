# Pré (bêta)

Fonction **facultative, en bêta et désactivée par défaut**. Les flux publics et les formats d’API personnels n’ont pas encore été éprouvés en conditions réelles.

Une « pré » est l’annonce de la sortie d’une release. Remix lit ces annonces et garde celles en **FRENCH / VF**, **MULTi** et **VOSTFR / SUBFRENCH**. Il part du principe qu’une release annoncée est disponible, sous environ une heure, sur vos sources habituelles (debrid, BitTorrent, Usenet). Elle :

- compte comme **release indexée** (catalogue « lisible uniquement », priorités) ;
- ajoute son film ou sa série au catalogue **sans attendre** qu’il y arrive par TMDB.

Une pré ne contient **ni lien ni empreinte** : elle ne sert jamais à lire. La lecture passe toujours par vos sources.

## Activer

Dans la console : **Remix → Indexation → Pré (bêta)**, puis **Activer** et enregistrer. Rien n’est lu tant que l’option est désactivée.

| Réglage | Rôle |
| --- | --- |
| Activer | Interrupteur général. |
| Flux publics | predb.net et predb.me (films, séries, animés). Décochez pour n’utiliser que vos flux personnels. |
| Langues gardées | FRENCH / VF, MULTi, VOSTFR / SUBFRENCH. Les releases « autre langue + MULTi » et les corrections (FIX) sont toujours écartées. |
| Flux ou API personnels | Vos propres flux RSS ou un point d’API JSON auquel vous avez accès, séparés par des espaces. Adresses `http` ou `https` sans identifiants dedans ; jamais réaffichées. |
| Clé d’API personnelle | Envoyée en en-tête `X-API-Key` aux seules adresses personnelles, jamais dans une adresse, jamais réaffichée. Elle n’est pas renvoyée en cas de redirection vers un autre hôte. |
| Délai avant prise en compte (avancé) | 1 h par défaut : une pré n’est comptée que ce temps après son annonce. `0s` : tout de suite. |
| Intervalle de lecture (avancé) | 10 min par défaut, 2 min au minimum. |

## Fonctionnement

1. Chaque flux est lu à l’intervalle choisi ; les sections hors vidéo, les corrections et les langues non gardées sont écartées.
2. Une pré n’est prise en compte qu’après le délai choisi.
3. Son nom est rapproché d’un film ou d’une série : titre exact, année pour un film, aucun rapprochement si le résultat est ambigu. À défaut, Remix cherche dans TMDB et enregistre le média s’il manque.
4. Au plus 40 titres sont résolus par passage ; une pré sans média est retentée jusqu’à 6 fois puis abandonnée. Les pré traitées sont mémorisées 30 jours.

Les requêtes passent par le garde-fou réseau d’Ether (`safefetch`). L’état est affiché dans la console.

## Limites

- Une annonce ne prouve pas que la release est présente sur vos sources : le délai d’une heure est une estimation.
- Aucun lien n’est conservé : si aucune source ne propose la release, le titre n’est pas lisible.
- Les flux publics peuvent changer de format ou de disponibilité ; un format d’API personnel est lu de façon tolérante, sans garantie.
