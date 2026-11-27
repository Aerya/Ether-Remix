# Découvertes musicales Digarr

Dans **Contenus → Musique → Playlists → Digarr**, choisissez un spectateur pour consulter ses recommandations en attente. **Retrouver dans Remix** recherche des albums dans le catalogue Deezer : artiste complet concordant et, pour une recommandation d'album, titre complet concordant après normalisation de la casse et des espaces. Les éditions différentes ne sont pas assimilées automatiquement. Une recommandation d'artiste propose les albums correspondants parmi les 50 premiers résultats ; ce n'est pas une discographie exhaustive.

Choisissez ensuite **Créer une playlist de cet album**. Remix relit la recommandation et vérifie l'album avant de créer une nouvelle playlist personnelle, accessible dans Plex/Plexamp, Jellyfin et Subsonic. Aucun fichier audio n'est téléchargé par cette opération. La présence au catalogue ne garantit pas la lecture : celle-ci utilise les sources musicales configurées. Les fichiers locaux et iBroadcast ne sont pas utilisés pour cette recherche.

## Configuration facultative

Installez et configurez séparément [Digarr](https://github.com/iuliandita/digarr). Dans le compte Digarr de chaque utilisateur, créez un jeton personnel de portée **read** dans **Settings → Account → API tokens**. Configurez sur le serveur Remix :

- `PSE_DIGARR_URL` : URL HTTP(S) de votre instance de confiance, sans identifiants, paramètres ni fragment. Un préfixe de chemin est accepté.
- `PSE_DIGARR_TOKENS` : objet JSON associant les identifiants des spectateurs Remix à leurs jetons personnels Digarr, par exemple `{"7":"<jeton personnel du compte associé>","8":"<autre jeton personnel>"}`. Les numéros sont affichés dans le sélecteur Digarr de la console. Ne partagez pas un jeton entre spectateurs souhaitant des recommandations distinctes.

Redémarrez Remix après modification. Aucun jeton de remplacement commun n'est utilisé pour un spectateur sans configuration. Les jetons restent dans l'environnement serveur et ne sont jamais retournés par l'API de ce connecteur. Protégez le fichier d'environnement et utilisez HTTPS hors d'un réseau privé de confiance. Cette page appartient à la console d'administration : son administrateur peut sélectionner les spectateurs, comme pour les autres playlists.

## Fonctionnement et limites

- Lecture seule côté Digarr, via son API v1 (contrat documenté pour 2.3.0). Aucune validation, aucun rejet, scan, téléchargement Lidarr/slskd ou changement des réglages Digarr n'est déclenché. Lancez et gérez vos découvertes dans Digarr, puis actualisez la page Remix.
- Pagination par 20 recommandations, jusqu'à l'offset 10 000. Les recommandations et leurs explications viennent de Digarr ; elles peuvent être inexactes.
- La recherche n'utilise pas les identifiants MusicBrainz comme identifiants Deezer. Une correspondance textuelle exacte peut encore concerner des homonymes : vérifiez l'album proposé avant import.
- Les redirections HTTP sont refusées, chaque appel Digarr est limité à 20 secondes et sa réponse à 1 Mio. Les erreurs amont et jetons ne sont pas recopiés dans les messages.
- Une erreur de recherche ou un album incomplet ne crée pas de playlist et ne modifie pas les playlists existantes. Chaque import réussi crée une nouvelle playlist ; après une interruption de connexion, vérifiez vos playlists avant de réessayer pour éviter un doublon.
- Pas de synchronisation automatique des recommandations ou de retour des écoutes vers Digarr dans ce connecteur. Il complète les [playlists programmées et AudioMuse](MUSIC-FLOWS.fr.md).

Les contrats HTTP, l'isolation des jetons, les erreurs et l'import sont testés avec des services simulés. La connexion à une instance Digarr réelle reste à valider.
