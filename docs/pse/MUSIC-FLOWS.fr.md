# Playlists programmées et AudioMuse

Dans **Contenus → Musique → Playlists → Programmées**, choisissez un spectateur, un nom, un type, une cadence de 1 à 168 heures et une limite de 1 à 200 titres. Enregistrez, puis utilisez **Actualiser** pour produire la première liste immédiatement. Au maximum 20 programmations sont conservées par spectateur.

- **Mix personnel** alterne trois sélections : favoris, écoutes récentes et favoris écoutés au plus une fois. Chaque sélection explore jusqu'à 1 000 entrées accessibles et change à chaque période. Les doublons sont retirés. Un historique vide ne produit pas de mix ; les playlists automatiques Découvertes et Nouveautés existantes restent disponibles séparément.
- **Playlist liée** suit un lien HTTPS Deezer, Spotify ou ListenBrainz. Spotify utilise le compte déjà lié au spectateur. Chaque succès remplace le contenu généré dans l'ordre de la source, jusqu'à la limite choisie. Les titres non résolus sont comptés ; seuls artiste et titre complets concordants sont retenus lors d'une recherche. Aucun titre manquant n'est téléchargé. Une source dépassant la limite d'import du service est refusée plutôt que remplacée par une réponse tronquée.
- **AudioMuse** construit une liste de morceaux similaires à un titre local de départ, avec un service AudioMuse déjà configuré et une bibliothèque déjà analysée. Voir ci-dessous.

Les playlists générées gardent leur identifiant dans Plex/Plexamp, Jellyfin et Subsonic. La musique doit être activée pour les actualisations automatiques ; les échéances sont vérifiées toutes les dix minutes, après une minute d'attente au démarrage. Une erreur est réessayée à l'échéance suivante, ou immédiatement avec **Actualiser**. Les horodatages, nombres de titres retenus et introuvables et erreurs restent visibles dans la console après un redémarrage.

Une erreur ou un résultat entièrement vide conserve le dernier contenu réussi. Modifier une programmation pendant son actualisation empêche l'ancien résultat de l'écraser. **Suspendre** arrête la programmation et conserve la liste ; **Supprimer** retire la programmation et sa liste générée, sans toucher aux fichiers audio ni aux autres playlists. Les droits sur les sources locales sont vérifiés à la génération et lors de l'accès aux playlists.

## AudioMuse facultatif

Configurez ces variables dans l'environnement du serveur Remix, puis redémarrez-le :

| Variable | Valeur |
|---|---|
| `PSE_AUDIOMUSE_URL` | Adresse de votre instance AudioMuse, sans identifiants, paramètres ni fragment ; HTTP ou HTTPS. |
| `PSE_AUDIOMUSE_TOKEN` | Jeton API AudioMuse si l'authentification est activée. Conservé dans l'environnement serveur, jamais retourné à la console. |
| `PSE_AUDIOMUSE_SERVER` | Nom du serveur Remix tel qu'il est configuré dans AudioMuse. Obligatoire pour distinguer les bibliothèques. |

Utilisez une instance de confiance, HTTPS lorsque la connexion traverse un réseau non privé, et un compte musical Remix limité aux dossiers locaux utiles. Configurez AudioMuse pour indexer ce serveur via OpenSubsonic, puis effectuez son analyse depuis AudioMuse. Cette analyse externe peut lire les fichiers audio ; le connecteur Remix ne la déclenche jamais.

Dans la programmation, sélectionnez **AudioMuse**, puis un titre local autorisé et déjà analysé. Le sélecteur propose jusqu'à 1 000 titres locaux accessibles, classés par nom. Les comptes iBroadcast et les titres du catalogue distant ne servent pas de point de départ à ce connecteur.

Remix appelle uniquement `GET /api/similar_tracks` avec `item_id=tr-…`, `n`, `server` et `eliminate_duplicates=true`. Il refuse les redirections. Il ne sollicite ni l'analyse, ni la lecture, ni la création de playlists distante. Les résultats doivent reprendre les identifiants OpenSubsonic de **cette instance Remix**, ainsi que l'artiste et le titre attendus. Un identifiant Navidrome ou Jellyfin d'une autre bibliothèque n'est pas converti automatiquement. Les résultats interdits, ambigus ou absents sont écartés et comptés.

Le contrat HTTP, les refus, les limites de réponse et l'isolation entre spectateurs sont couverts par des tests simulés. Une analyse complète et la qualité des recommandations sur une instance AudioMuse réelle restent à valider ; un titre non analysé ou une API incompatible laisse la dernière playlist intacte.

Référence du contrat : [API de similarité AudioMuse](https://github.com/NeptuneHub/AudioMuse-AI/blob/main/app_ivf.py).
