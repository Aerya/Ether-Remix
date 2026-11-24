# aMule et Soulseek (slskd)

Ces sources facultatives recherchent des films, épisodes et morceaux depuis Remix, puis lancent le téléchargement lors d’une lecture. Elles sont désactivées par défaut. La lecture commence **après réception complète** ; une première demande peut indiquer que le téléchargement est en cours. Relancez la lecture quand il est terminé. Les recherches et les vérifications de disponibilité ne lancent pas de transfert.

## Configuration

Dans **Remix → Sources**, activez le mode **Expert**, puis ouvrez **aMule** ou **Soulseek (slskd)**. Enregistrez les réglages avant d’utiliser **Tester la connexion**.

- **aMule** : utilisez une version fournissant l’API officielle `amuleapi` (`/api/v1`), activée auprès de votre daemon, et son mot de passe administrateur. L’adresse est celle d’`amuleapi`, généralement `http://amule:4713`, pas le port EC 4712 ni l’ancienne interface amuleweb. Le dépôt historique a déménagé vers [amule-org/amule](https://github.com/amule-org/amule). Voir le [démarrage d’amuleapi](https://github.com/amule-org/amule/blob/master/docs/QUICKSTART-AMULEAPI.md). La recherche utilise les serveurs eD2k (`global`) ; le daemon doit être connecté et configuré indépendamment.
- **slskd** : renseignez son adresse de base, généralement `http://slskd:5030`, et une clé API autorisant lecture et écriture. La version installée doit fournir `POST /api/v0/transfers/downloads/batches` avec `options.destination`. Les fichiers Remix sont placés sous `remix/<identifiant>/` dans le dossier de téléchargements de slskd. La connexion Soulseek et le partage se configurent dans slskd. Voir sa [configuration officielle](https://github.com/slskd/slskd/blob/master/docs/config.md).
- Montez le dossier des téléchargements terminés dans Remix, idéalement en lecture seule. Renseignez son chemin vu par Remix. Pour aMule, indiquez également le chemin du même dossier côté daemon s’il diffère. Les sous-dossiers sont conservés ; aucun chemin extérieur au montage n’est accepté.

Les clients restent des services externes ; le connecteur ne les installe pas. Les secrets sont masqués dans la console et envoyés dans un en-tête ou un corps de requête, jamais dans l’adresse. Les redirections HTTP ne sont pas suivies.

| Variable | Rôle |
|---|---|
| `PSE_AMULE`, `PSE_SLSKD` | `on` pour activer ; `off` par défaut |
| `PSE_AMULE_URL`, `PSE_SLSKD_URL` | Adresse de base sans suffixe `/api` |
| `PSE_AMULE_KEY` | Mot de passe administrateur amuleapi |
| `PSE_SLSKD_KEY` | Clé API slskd |
| `PSE_AMULE_ROOT`, `PSE_SLSKD_ROOT` | Dossier terminé monté dans Remix |
| `PSE_AMULE_REMOTE_ROOT` | Même dossier côté aMule ; vide = même chemin |

## Recherche, lecture et transferts

Les deux sources rejoignent les indexeurs vidéo avec les filtres et budgets d’Ether. Un film doit annoncer le bon titre et une année compatible ; un épisode doit annoncer le bon titre et les numéros attendus. Les résultats incertains sont écartés.

Pour la musique, le catalogue Deezer sert à rechercher artistes, albums et morceaux ; aMule et slskd sont des sources de lecture de secours après les sources déjà configurées. L’artiste, l’album et le titre doivent être identifiables dans le nom ou le chemin distant, sans substitution de version (live, remix…). Un fichier au nom insuffisant n’est pas associé automatiquement.

Le panneau permet aussi une recherche manuelle, un lancement explicite et l’actualisation des transferts. Il conserve les tâches dans la base Remix pour ne pas relancer un téléchargement après redémarrage. Limites par connecteur : deux transferts actifs, vingt lancements sur 24 heures. Les fichiers restent conservés ; l’annulation, le partage et le nettoyage se font dans le client externe. Aucun effacement automatique n’est effectué.

Le montage doit contenir le fichier complet et de la taille annoncée. La lecture vidéo passe par un serveur HTTP local à jetons avec prise en charge des plages d’octets ; la musique ouvre le fichier directement dans le montage. Les liens symboliques ne peuvent pas sortir de ce dossier. Pour retrouver aussi les fichiers hors du catalogue à la demande, ajoutez le montage à une bibliothèque locale, avec les droits de partage voulus.

Les versions anciennes d’aMule sans `amuleapi` et de slskd sans API de lots ne sont pas compatibles. Aucun streaming d’un fichier incomplet, installation automatique de client ou changement de configuration réseau n’est réalisé.

Les reprises musicales utilisent en priorité la tâche déjà enregistrée, sans nouvelle recherche ni lancement d’une seconde copie. Les titres numériques (par exemple « 1979 ») sont conservés lors du retrait d’un numéro de piste.
