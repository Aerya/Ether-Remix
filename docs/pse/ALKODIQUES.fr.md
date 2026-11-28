# Alkodiques par l'API de StreamFusion

Cette source facultative lit les fichiers AllDebrid **déjà vérifiés** (Alkodiques) de votre [StreamFusion Reborn](STREAMFUSION.fr.md) par son **API HTTP en lecture seule**, sans accès à PostgreSQL. Elle est **désactivée par défaut**. Elle est interrogée avant les autres sources et ne coûte qu'un déblocage de lien (`link/unlock`) à la lecture : aucun magnet n'est ajouté.

Prérequis : un StreamFusion Reborn qui expose l'API ALKO (`SFR_ALKO_ENABLED=true`, version récente) et un compte AllDebrid actif dans Remix. Sur un StreamFusion trop ancien, la console affiche « API absente ».

## Mise en place

1. Dans l'administration de StreamFusion, ouvrez `/admin/alko-lecture` : elle affiche l'**adresse interne** conseillée (`http://stream-fusion:8080`) et la **clé de lecture**, une seule fois. La clé change si `SECRET_API_KEY` change : il faut alors la recopier.
2. Remix et StreamFusion doivent partager un réseau Docker (voir [STREAMFUSION.fr.md](STREAMFUSION.fr.md), étape 2). L'adresse peut être privée : Remix ne la fait pas passer par le filtre des adresses publiques.
3. Dans **Ether-Remix → Sources → Alkodiques (API StreamFusion)**, renseignez l'adresse et la clé, enregistrez, puis **Tester la connexion**. Activez la source.

| Réglage | Défaut | Rôle |
| --- | --- | --- |
| `PSE_ALKO` | désactivé | Active la source. Exige une clé. |
| `PSE_ALKO_URL` | `http://stream-fusion:8080` | Adresse de l'API (http ou https, sans identifiants, paramètres ni fragment). |
| `PSE_ALKO_KEY` | — | Clé de lecture (en-tête `alko-key`). Masquée ; jamais journalisée. |
| `PSE_ALKO_LANG_FR` | oui | Ne garde que les fichiers FR, MULTi, VOSTFR ou aux sous-titres français, d'après le nom. |
| `PSE_ALKO_INTERVAL` | 10 min | Délai entre deux synchronisations (minimum 2 min). |

## Fonctionnement

- **Copie locale** : Remix garde le catalogue dans sa base (table `pse_alko_files`). La recherche à la lecture lit cette copie : **aucun appel réseau**. Environ 380 000 fichiers sont attendus ; la lecture complète transfère plusieurs centaines de Mo de JSON, **page par page** (1 000 fichiers), avec une mémoire bornée, par transactions de lots (environ 1,4 s par page, soit une dizaine de minutes pour 380 000 fichiers, quelques minutes de plus avec le catalogue « lisible uniquement » ; le nettoyage final et le rafraîchissement des observations se font par petites tranches, sans bloquer l'écriture). Elle s'interrompt pendant les lectures et reprend là où elle s'est arrêtée après un redémarrage. Les fichiers disparus de SFR sont retirés à la fin.
- **Suivi** : ensuite, Remix suit le journal des modifications de SFR. Pour chaque ligne du journal, il relit le fichier par identifiant (`playback_id`) : présent, il est mis à jour ; absent, il est supprimé. Il ne supprime jamais « à l'aveugle ». Le repère n'avance à `last_seq` que sur la dernière page et si le journal est valide.
- **Relecture complète** si SFR l'impose (HTTP 410, `purge_seq` au-delà du repère, `last_seq` inférieur au repère, époque différente) ou si le journal reste invalide plus de deux cycles (au plus une fois toutes les 6 heures). Changer l'adresse de l'API repart de zéro.
- **Publication** : seuls les titres **présents dans le catalogue** de Remix sont publiés, **tous les épisodes** compris (saison 0 comprise). Un titre qui entre au catalogue après la synchronisation est rattrapé : à chaque passage, Remix vérifie par lots de 500 les titres jamais vérifiés (au plus deux minutes par passage, puis chaque titre une fois tous les 30 jours).
- **Même fichier, même identifiant** : les releases gardent l'identifiant et le GUID `sfr-alko:<playback_id>` des Alkodiques PostgreSQL, donc la lecture, les priorités (`resolver.verifiedFiles`) et la [bibliothèque de références](RELEASE_LIBRARY.fr.md) fonctionnent comme avant. La lecture PostgreSQL des Alkodiques n'est **ignorée** qu'une fois la source active (activée, adresse valide, clé présente) **et** une première lecture complète réussie : tant que SFR répond 404 (trop ancien), que la clé est refusée ou que la première synchronisation n'est pas terminée, l'ancien chemin continue. À la fin de cette première lecture, Remix retire les anciennes lignes PostgreSQL des Alkodiques (et leurs références) pour éviter les doublons ; les torrents SFR par PostgreSQL ne changent pas.
- **Pistes audio** : le `media_info` de chaque fichier est décodé à la synchronisation (compact, dans la copie locale) et les langues des pistes audio sont retenues. Elles ne servent pas encore au filtre de langue, qui ne regarde que le nom du fichier. Un `media_info` absent ou illisible est ignoré sans erreur.

## Sécurité

La recherche de cette source n'est jamais mise en cache (le cache de recherche d'Ether écrirait les liens en clair). Le lien source (`source_url`) contient un jeton AllDebrid privé. Il est **chiffré au repos** avec la clé de configuration de Remix, n'apparaît dans aucun journal, aucune erreur ni la console, et n'est déchiffré qu'à la recherche. La clé de lecture est masquée dans la console. Les erreurs affichées sont génériques (clé refusée, API absente, vues indisponibles, injoignable) : jamais l'adresse, la clé ni un lien.

## Diagnostic

La carte affiche le nombre de fichiers (films et épisodes), le nombre de fichiers publiés pour votre catalogue, l'état du journal, la dernière synchronisation et la dernière erreur. **Synchroniser maintenant** déclenche un passage.

| Message | Cause |
| --- | --- |
| Clé refusée | Clé absente, fausse ou changée (`SECRET_API_KEY`). Recopiez-la depuis `/admin/alko-lecture`. |
| API absente | StreamFusion trop ancien, ou lecture ALKO désactivée. |
| Vues indisponibles | Migration ALKO non appliquée côté StreamFusion. |
| Injoignable | Adresse fausse ou réseau Docker non partagé. |
| Journal inactif ou en retard | SFR applique des lots sans journal : Remix relit tout périodiquement (au plus toutes les 6 heures). |
