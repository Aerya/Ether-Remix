# Catalogue et indexation

Avec le catalogue **« lisible uniquement »**, Remix n’affiche un titre de son catalogue local que s’il a assez de releases indexées (2 par défaut) qui respectent le filtre choisi, par exemple VF ou MULTi en cache debrid. Les titres **VOSTFR** et les exceptions de langue comptent leurs releases dans toutes les langues.

- **Listes filtrées** : accueil (Tendances, Top 10…), Films, Séries, recommandations, titres similaires, collections et franchises. La recherche, la watchlist, la reprise de lecture et les collections de sorties à venir ne le sont pas : un titre choisi reste accessible, et sa lecture interroge les sources en direct.
- **Ordre de recherche** : popularité TMDB décroissante, en alternant films et séries. Passent avant : les titres VOSTFR et exceptions de langue, puis les collections et bibliothèques mises en priorité.
- **Recherche immédiate** : un ajout VOSTFR, le bouton **Chercher maintenant**, et une recherche dans Plex ou Jellyfin. Pour une recherche, les 5 premiers titres trouvés passent en tête de file s’ils n’ont jamais été cherchés, ou n’avaient rien donné il y a plus de 6 h.
- **Titres à risque** : l’option **Afficher aussi les titres à risque** garde les titres qui ont moins de releases que le minimum (au moins une). Plex et Jellyfin les signalent par « ⚠ 1 source » au début du résumé, par un badge « 1 SOURCE » sur les affiches Ether, ou les deux. Avec un autre outil d’affiches, choisissez le résumé.
- **Catalogue local** : Ether ne liste pas tout TMDB, seulement les titres assez connus et déjà sortis, selon **Catalogue → Masquer les titres obscurs**. C’est dans cet ensemble que Remix cherche des releases.

Les disponibilités confirmées par [SeerrCatalog](SEERRCATALOG.fr.md) suivent ses critères et restent acquises jusqu’à suppression.

## Délais de rafraîchissement

**Métadonnées TMDB**

| Donnée | Fréquence |
| --- | --- |
| Catalogue complet | Toutes les 24 h (réglable) |
| Nouveautés et tendances | Toutes les 4 h |
| Fiches détaillées manquantes | Par lots, toutes les 90 s, en commençant par les sorties récentes et les titres populaires |
| Séries en cours | Fiche actualisée à chaque cycle du catalogue ; 1 h au maximum à l’affichage d’une saison active |

**Recherche de releases**

| Résultat de la dernière recherche | Nouvel essai après |
| --- | --- |
| Titre sorti il y a moins de 60 jours | 2 jours |
| Aucune release trouvée | 7 jours |
| Releases trouvées | 14 jours |
| Source en panne ou réponse partielle | 24 h |

Ces délais sont des minimums. La recherche dépend du budget quotidien de chaque source (1000 requêtes par défaut), de l’intervalle entre deux titres (10 s par défaut) et se met en pause pendant la lecture : sur un grand catalogue, un titre peut attendre plus longtemps. Une release non revue depuis 60 jours ne compte plus.

Dans **Sources → Indexation**, le bloc repliable **Priorité de recherche** permet de faire passer une collection ou une bibliothèque avant le reste : *Prioritaire* retente un titre sans release après 1 jour, *Maximale* après 6 h. **Chercher maintenant** ignore ces délais pour la sélection. Les budgets par source restent appliqués. À l’import, SeerrCatalog (mise à jour toutes les heures, priorité maximale) et RSS Catalog (toutes les 3 h, prioritaire) sont reconnus et préréglés ; ces réglages restent modifiables.

**Collections et bibliothèques**

Chaque collection a sa propre cadence : `live` (à la demande), `hourly`, `daily` (par défaut), `weekly` ou toutes les N heures, de 1 à 168. Certaines sources imposent un délai minimum. Les bibliothèques se mettent à jour avec leurs collections. Cette cadence est indépendante de la priorité de recherche de releases ; **Actualiser**, dans la même section, relance immédiatement une collection ou toutes celles d’une bibliothèque.
