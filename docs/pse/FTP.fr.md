# Source FTP / SFTP

Cette source facultative propose les films et épisodes présents sur un serveur distant dans les résultats de Remix. Elle est **désactivée par défaut**. Un seul compte serveur peut être configuré, avec plusieurs dossiers. Aucun service intermédiaire n'est nécessaire.

## Configuration

Dans **Ether-Remix → Sources → FTP / SFTP**, renseignez le protocole, le serveur (sans préfixe ni port), le port, le compte et les dossiers séparés par `;`, par exemple `/films ; /series`. Enregistrez les réglages avant d'utiliser **Tester la connexion**. Les boutons utilisent la configuration enregistrée.

- **SFTP** (par défaut, port 22) : connexion SSH avec mot de passe ou fichier de clé privée. L'empreinte `SHA256:…` du serveur est obligatoire. Le test affiche l'empreinte sans l'accepter automatiquement : comparez-la à celle fournie par votre hébergeur ou administrateur, puis enregistrez-la et testez à nouveau. Une empreinte différente bloque la connexion.
- **FTPS explicite** (port 21) ou **implicite** (990) : certificat TLS vérifié. Pour un certificat auto-signé, vérifiez puis enregistrez son empreinte SHA-256 affichée par le test.
- **FTP** (port 21) : connexion non chiffrée, y compris le mot de passe. Utilisez-la seulement sur un réseau de confiance.

Activez ensuite la source et lancez **Parcourir le serveur**. Le parcours périodique a lieu toutes les six heures par défaut. Les recherches consultent l'index local, sans parcourir le serveur à chaque lecture. Un dossier momentanément illisible conserve ses anciennes entrées.

## Lecture et stockage

| Mode | Comportement |
| --- | --- |
| `stream` — Streaming (par défaut) | Lecture distante par plages, avec déplacements dans la vidéo. Aucune copie complète du média dans le dossier de téléchargement ; le cache habituel du moteur Ether reste applicable. |
| `download` — Télécharger puis lire | Copie complète temporaire avant lecture. Si elle n'est pas encore prête, un message invite à relancer la lecture après sa fin. |
| `both` — Streaming et copie gardée | Lecture distante immédiate et copie conservée en arrière-plan lors d'une vraie lecture. |

Les modes avec copie nécessitent un dossier inscriptible. Sans dossier configuré, Remix revient au streaming. Les recherches et contrôles en arrière-plan ne lancent pas de copie. Le bouton de copie manuelle et le mode `both` créent des copies **gardées**, exclues du nettoyage et du plafond des copies temporaires : leur stockage doit être géré séparément.

Par défaut : une copie à la fois, cache temporaire de 50 Gio, conservation de 48 heures et minimum de 20 Gio libres. Le nettoyage retire les anciennes copies temporaires pour libérer la place demandée. Il protège les copies gardées, en lecture ou appartenant à une bibliothèque locale. Il n'efface jamais les fichiers du serveur distant. **Garder** conserve une copie ; **Arrêter** interrompt un transfert et retire son fichier partiel. Les transferts interrompus par l'arrêt de Remix peuvent reprendre, pour le même serveur et en mode avec téléchargement.

## Volumes Docker

Pour une connexion par mot de passe en streaming, aucun volume média supplémentaire n'est nécessaire. Pour une clé SSH, montez seulement le fichier requis en lecture seule, puis indiquez son chemin dans le conteneur :

```yaml
volumes:
  - /chemin/prive/id_ed25519:/config/ssh/id_ed25519:ro
```

Pour un mode avec téléchargement, ajoutez un dossier dédié et inscriptible :

```yaml
volumes:
  - /chemin/copies-remix:/downloads/ftp
```

Configurez alors `/downloads/ftp` comme dossier de téléchargement. Conservez les volumes existants du service. Mot de passe et phrase de passe ne sont pas réaffichés par la console ; protégez aussi les fichiers de configuration et sauvegardes qui les contiennent.

## Variables

Les réglages enregistrés dans la console priment sur l'environnement. Préfixe commun : `PSE_FTP`.

| Variable | Valeur ou défaut |
| --- | --- |
| `PSE_FTP` | `off` ; `on` pour activer |
| `PSE_FTP_PROTOCOL` | `sftp`, `ftps`, `ftps-implicit`, `ftp` |
| `PSE_FTP_HOST`, `PSE_FTP_PORT`, `PSE_FTP_USER` | Serveur, port (`0` = port du protocole), compte |
| `PSE_FTP_PASSWORD` | Mot de passe |
| `PSE_FTP_KEY_FILE`, `PSE_FTP_KEY_PASSPHRASE` | Fichier de clé SSH et phrase de passe facultative |
| `PSE_FTP_HOST_KEY` | Empreinte SSH ou certificat FTPS |
| `PSE_FTP_ROOTS`, `PSE_FTP_EXCLUDE` | Dossiers séparés par `;`, noms à ignorer séparés par `,` |
| `PSE_FTP_DEPTH`, `PSE_FTP_SCAN_HOURS` | `6`, `6` |
| `PSE_FTP_MODE`, `PSE_FTP_DOWNLOAD_DIR` | `stream`, dossier vide |
| `PSE_FTP_CONNECTIONS`, `PSE_FTP_MAX_ACTIVE` | `4` connexions FTP, `1` copie |
| `PSE_FTP_CACHE_GB`, `PSE_FTP_CACHE_HOURS`, `PSE_FTP_MIN_FREE` | `50`, `48`, `20` |
| `PSE_FTP_SEARCH_DAILY` | `100000` (environnement) |

## Limites et essais

Les noms des fichiers ou dossiers doivent permettre d'identifier les films et épisodes. Les archives, extraits et vidéos de moins de 32 Mio ne sont pas indexés. Le parcours est borné en profondeur et en nombre d'entrées. Le FTP doit accepter `REST` pour les déplacements dans la vidéo ; sans cette commande, utilisez la copie complète. Le débit et le temps de réponse du serveur influencent directement la lecture. Une disparition distante peut rester visible dans le cache des résultats jusqu'à son actualisation ; la lecture vérifie sa présence.

Les essais automatisés couvrent les plages HTTP, coupures SFTP, connexions FTP limitées, copies, nettoyage et protection des secrets. Le [laboratoire](../../internal/pse/ftp/testdata/lab/README.md) lit de vraies vidéos MKV et MP4 avec ffprobe/ffmpeg à travers le moteur Ether, y compris avec l'index MP4 en fin de fichier. Ces essais locaux ne constituent pas une validation sur une seedbox distante ni dans tous les lecteurs TV.
