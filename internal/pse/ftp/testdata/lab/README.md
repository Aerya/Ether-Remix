# Laboratoire FTP / SFTP

Essais locaux uniquement, sans compte ni média de production. `go`, `ffmpeg` et `ffprobe` sont requis. Les tests créent deux vidéos de 70 secondes (MKV et MP4 avec index en fin de fichier), puis les lisent via le moteur Ether à 0, 60, 30 et 5 secondes.

Depuis la racine du dépôt, le serveur SFTP intégré suffit pour un premier essai :

```sh
FTP_LAB=1 go test -count=1 -v -run TestLabStreamsRealVideosOverBuiltinSFTP ./internal/pse/ftp/
```

Pour OpenSSH et vsftpd réels, utilisez le fichier `compose.yml` de ce dossier dans une instance Dockge de laboratoire. Il publie uniquement sur `127.0.0.1` ; exécutez les tests sur le même hôte. N'utilisez pas une instance de production. Préparez un répertoire temporaire et un certificat jetable :

```sh
export FTP_LAB_WORK="$(mktemp -d)"
export FTP_LAB_DIR="$FTP_LAB_WORK/media"
export FTP_LAB_CERTS="$FTP_LAB_WORK/certs"
export FTP_LAB_USER=remix
export FTP_LAB_PASS="$(openssl rand -hex 16)"
mkdir -p "$FTP_LAB_DIR" "$FTP_LAB_CERTS"
chmod 755 "$FTP_LAB_WORK"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$FTP_LAB_CERTS/lab.key" -out "$FTP_LAB_CERTS/lab.crt" \
  -subj /CN=localhost
```

Transmettez ces variables à la stack de laboratoire sans les committer. Les conteneurs ont des montages média en lecture seule. Après démarrage :

```sh
export FTP_LAB=1
export FTP_LAB_SFTP=127.0.0.1:2222
export FTP_LAB_SFTP_ROOT=/upload
export FTP_LAB_FTPS=127.0.0.1:2121
export FTP_LAB_FTPS_ROOT=/srv/media
go test -count=1 -v -run 'TestLabStreamsOver' ./internal/pse/ftp/
```

Le compte vsftpd n'est pas chrooté : `/srv/media` est indispensable pour éviter de parcourir le conteneur entier. Les tests acceptent uniquement pour ce laboratoire l'empreinte qu'ils viennent de lire ; ce mécanisme ne remplace pas la vérification indépendante demandée à l'utilisateur dans la console.

À la fin, arrêtez et supprimez la stack de laboratoire dans Dockge, puis supprimez uniquement le répertoire temporaire créé pour cet essai. Ne conservez pas les comptes ni certificats jetables. Les temps mesurés sont locaux ; ils ne préjugent pas des performances d'un serveur distant.

## English

Run the built-in SFTP command above with Go, ffmpeg and ffprobe installed. For real OpenSSH and vsftpd, import `compose.yml` into a local Dockge lab, provide the generated temporary environment values, and run the second test command on the same host. Use `/upload` for SFTP and `/srv/media` for FTPS. Tests generate their own MKV/MP4 media and decode after four seeks through Ether. Remove the lab stack and its own temporary directory afterwards. Never use production credentials or infer remote-server performance from local timings.
