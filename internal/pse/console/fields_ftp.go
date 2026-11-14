package console

import (
	"errors"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/pse/ftp"
)

// Source FTP / SFTP (Ether-Remix, PSE_FTP) : voir docs/pse/FTP.fr.md.

func ftpFeature() Feature {
	return Feature{
		ID:    "ftp",
		Title: tx("FTP / SFTP", "FTP / SFTP"),
		Description: tx("Lit les films et épisodes rangés sur un serveur FTP ou SFTP (seedbox, NAS distant). Remix parcourt les dossiers choisis, garde la liste des vidéos et les lit directement depuis le serveur, avec avance et retour rapides. En mode Streaming, rien n'est écrit sur le disque ; les modes avec téléchargement copient les fichiers dans un dossier dédié. Désactivé par défaut.",
			"Plays movies and episodes stored on an FTP or SFTP server (seedbox, remote NAS). Remix scans the chosen folders, keeps the list of videos and plays them straight from the server, with fast seeking. In Streaming mode nothing is written to disk; the download modes copy files to a dedicated folder. Off by default."),
		Fields: []Field{
			{Key: "PSE_FTP", Kind: KindToggle, Default: "off", Label: tx("Activer", "Enable"),
				Help: tx("Nécessite l'adresse, l'utilisateur, un mot de passe ou une clé SSH et, en SFTP, l'empreinte du serveur (bouton « Tester la connexion »).", "Needs the address, the user, a password or SSH key and, for SFTP, the server fingerprint (“Test connection” button).")},
			{Key: "PSE_FTP_PROTOCOL", Kind: KindSelect, Default: ftp.ProtoSFTP, Label: tx("Protocole", "Protocol"),
				Options: []Option{
					{ftp.ProtoSFTP, tx("SFTP (SSH) — recommandé", "SFTP (SSH) — recommended")},
					{ftp.ProtoFTPS, tx("FTPS (TLS explicite)", "FTPS (explicit TLS)")},
					{ftp.ProtoFTPSImpl, tx("FTPS (TLS implicite, port 990)", "FTPS (implicit TLS, port 990)")},
					{ftp.ProtoFTP, tx("FTP en clair (déconseillé)", "Plain FTP (not recommended)")},
				},
				Help: tx("SFTP permet d'avancer ou de reculer instantanément dans la vidéo. En FTP, chaque saut ouvre une nouvelle connexion (environ une demi-seconde). FTP en clair : mot de passe et fichiers circulent sans chiffrement.",
					"SFTP seeks instantly anywhere in the video. With FTP, each seek opens a new connection (about half a second). Plain FTP: password and files travel unencrypted.")},
			{Key: "PSE_FTP_HOST", Kind: KindFilter, Default: "", Placeholder: "seedbox.exemple.net", Label: tx("Serveur", "Server"),
				Help: tx("Nom d'hôte ou adresse IP, sans « sftp:// », port ni chemin.", "Host name or IP address, without “sftp://”, port or path.")},
			{Key: "PSE_FTP_PORT", Kind: KindNumber, Default: "0", Min: ptr(0), Max: ptr(65535), Label: tx("Port", "Port"),
				Help: tx("0 : port habituel du protocole (22 en SFTP, 21 en FTP/FTPS, 990 en FTPS implicite).", "0: the protocol's usual port (22 for SFTP, 21 for FTP/FTPS, 990 for implicit FTPS).")},
			{Key: "PSE_FTP_USER", Kind: KindFilter, Default: "", Label: tx("Utilisateur", "User")},
			{Key: "PSE_FTP_PASSWORD", Kind: KindSecret, Default: "", Label: tx("Mot de passe", "Password"),
				Help: tx("Jamais réaffiché, jamais écrit dans les journaux ni dans une adresse. Facultatif en SFTP avec une clé.", "Never shown again, never written to logs or put in an address. Optional for SFTP with a key.")},
			{Key: "PSE_FTP_KEY_FILE", Kind: KindFilter, Default: "", Placeholder: "/config/ssh/id_ed25519", Label: tx("Clé SSH (fichier)", "SSH key (file)"),
				Help: tx("SFTP seulement : chemin, dans le conteneur de Remix, d'une clé privée montée en lecture seule. La clé elle-même ne passe jamais par le navigateur.", "SFTP only: path, inside Remix's container, of a private key mounted read-only. The key itself never goes through the browser.")},
			{Key: "PSE_FTP_KEY_PASSPHRASE", Kind: KindSecret, Default: "", Label: tx("Phrase de passe de la clé", "Key passphrase"),
				Help: tx("Seulement si la clé SSH est protégée.", "Only if the SSH key is protected.")},
			{Key: "PSE_FTP_HOST_KEY", Kind: KindFilter, Default: "", Placeholder: "SHA256:…", Label: tx("Empreinte du serveur", "Server fingerprint"),
				Help: tx("SFTP : obligatoire. « Tester la connexion » affiche l'empreinte présentée par le serveur : vérifiez-la auprès de l'hébergeur, puis collez-la ici. Une empreinte qui change bloque la connexion. FTPS : facultatif, seulement pour un certificat auto-signé.",
					"SFTP: required. “Test connection” shows the fingerprint the server presents: check it with your host, then paste it here. A changed fingerprint blocks the connection. FTPS: optional, only for a self-signed certificate.")},
			{Key: "PSE_FTP_ROOTS", Kind: KindFilter, Default: "", Placeholder: "/films ; /series", Label: tx("Dossiers à parcourir", "Folders to scan"),
				Help: tx("Séparés par « ; ». Vide : tout le compte. Seules les vidéos de 32 Mo et plus sont retenues (pas les échantillons).", "Separated by “;”. Empty: the whole account. Only videos of 32 MB or more are kept (no samples).")},
			{Key: "PSE_FTP_MODE", Kind: KindSelect, Default: ftp.ModeStream, Label: tx("Mode", "Mode"),
				Options: []Option{
					{ftp.ModeStream, tx("Streaming (rien sur le disque)", "Streaming (nothing on disk)")},
					{ftp.ModeBoth, tx("Streaming et copie gardée", "Streaming and kept copy")},
					{ftp.ModeDownload, tx("Téléchargement seulement", "Download only")},
				},
				Help: tx("Streaming : lecture directe depuis le serveur, aucun média écrit (seul le cache de lecture habituel d'Ether sert, comme pour le debrid). Streaming et copie gardée : idem, avec une copie gardée dans le dossier de téléchargement. Téléchargement seulement : la lecture attend la copie complète, effacée après la durée de conservation. Les deux derniers modes exigent un dossier de téléchargement.",
					"Streaming: played straight from the server, no media written (only Ether's usual playback cache, as with debrid). Streaming and kept copy: same, with a copy kept in the download folder. Download only: playback waits for the full copy, deleted after the retention time. The last two modes need a download folder.")},
			{Key: "PSE_FTP_DOWNLOAD_DIR", Kind: KindFilter, Default: "", Placeholder: "/downloads/ftp", Label: tx("Dossier de téléchargement", "Download folder"),
				Help: tx("Dans le conteneur de Remix (volume Docker). Remix n'y efface que ses propres copies temporaires, jamais un fichier gardé ni une bibliothèque locale.", "Inside Remix's container (Docker volume). Remix only deletes its own temporary copies there, never a kept file or a local library.")},
			{Key: "PSE_FTP_SCAN_HOURS", Kind: KindNumber, Default: "6", Min: ptr(1), Max: ptr(24 * 30), Label: tx("Parcours du serveur (heures)", "Server scan (hours)"),
				Help: tx("Fréquence de mise à jour de la liste des vidéos. Un parcours ne lit que les listes de dossiers, jamais les vidéos.", "How often the video list is refreshed. A scan only reads folder listings, never the videos.")},
			{Key: "PSE_FTP_CONNECTIONS", Advanced: true, Kind: KindNumber, Default: "4", Min: ptr(1), Max: ptr(16), Label: tx("Connexions simultanées (FTP)", "Simultaneous connections (FTP)"),
				Help: tx("FTP/FTPS : connexions ouvertes au plus vers le serveur (beaucoup en autorisent 2 à 5 par compte). SFTP : une seule connexion suffit, ce réglage est ignoré.", "FTP/FTPS: maximum connections to the server (many allow 2 to 5 per account). SFTP: a single connection is enough, this setting is ignored.")},
			{Key: "PSE_FTP_DEPTH", Advanced: true, Kind: KindNumber, Default: "6", Min: ptr(1), Max: ptr(20), Label: tx("Profondeur des dossiers", "Folder depth"),
				Help: tx("Niveaux de sous-dossiers parcourus sous chaque dossier choisi.", "Sub-folder levels scanned under each chosen folder.")},
			{Key: "PSE_FTP_EXCLUDE", Advanced: true, Kind: KindFilter, Default: "", Placeholder: "incomplete,tmp", Label: tx("Dossiers ignorés", "Ignored folders"),
				Help: tx("Noms séparés par des virgules. Les dossiers cachés (« .… ») sont toujours ignorés.", "Comma-separated names. Hidden folders (“.…”) are always ignored.")},
			{Key: "PSE_FTP_MAX_ACTIVE", Advanced: true, Kind: KindNumber, Default: "1", Min: ptr(1), Max: ptr(10), Label: tx("Copies simultanées", "Simultaneous copies"),
				Help: tx("Modes avec téléchargement seulement.", "Download modes only.")},
			{Key: "PSE_FTP_CACHE_GB", Advanced: true, Kind: KindNumber, Default: "50", Min: ptr(0), Max: ptr(100000), Label: tx("Taille du cache des copies (Go)", "Copy cache size (GB)"),
				Help: tx("Copies temporaires du mode Téléchargement seulement. Au-delà, les plus anciennes non lues sont effacées. 0 : pas de plafond.", "Temporary copies of Download only mode. Beyond this, the oldest unread ones are deleted. 0: no cap.")},
			{Key: "PSE_FTP_CACHE_HOURS", Advanced: true, Kind: KindNumber, Default: "48", Min: ptr(1), Max: ptr(24 * 365), Label: tx("Conservation des copies (heures)", "Copy retention (hours)"),
				Help: tx("Depuis la dernière lecture. Une copie gardée, en cours de lecture ou rangée dans une bibliothèque locale n'est jamais effacée.", "Since the last playback. A kept copy, one being played, or one inside a local library is never deleted.")},
			{Key: "PSE_FTP_MIN_FREE", Advanced: true, Kind: KindNumber, Default: "20", Min: ptr(0), Max: ptr(100000), Label: tx("Place libre minimum (Go)", "Minimum free space (GB)"),
				Help: tx("Mesurée sur le dossier de téléchargement. Sous ce seuil, aucune copie n'est lancée. 0 : aucun contrôle.", "Measured on the download folder. Below this, no copy starts. 0: no check.")},
		},
	}
}

// validateFTP vérifie les réglages FTP particuliers ; handled est faux pour
// les autres clés.
func validateFTP(key, v string) (out string, handled bool, err error) {
	switch key {
	case "PSE_FTP_HOST":
		v = strings.TrimSpace(v)
		if v != "" {
			if err := ftp.ValidateHost(v); err != nil {
				return "", true, err
			}
		}
		return v, true, nil
	case "PSE_FTP_USER":
		if len(v) > 128 || strings.ContainsAny(v, "\r\n\t\x00") {
			return "", true, errors.New("nom d'utilisateur invalide")
		}
		return strings.TrimSpace(v), true, nil
	case "PSE_FTP_KEY_FILE", "PSE_FTP_DOWNLOAD_DIR":
		v = strings.TrimSpace(v)
		if v != "" && (!strings.HasPrefix(v, "/") || strings.Contains(v, "..")) {
			return "", true, errors.New("chemin absolu attendu, dans le conteneur de Remix")
		}
		if key == "PSE_FTP_DOWNLOAD_DIR" && v == "/" {
			return "", true, errors.New("choisissez un dossier dédié, pas la racine")
		}
		return v, true, nil
	case "PSE_FTP_HOST_KEY":
		v = strings.TrimSpace(v)
		if v != "" && ftp.NormalizeFingerprint(v) == "" {
			return "", true, errors.New("empreinte attendue : « SHA256:… » (SFTP) ou 64 caractères hexadécimaux (certificat FTPS)")
		}
		return ftp.NormalizeFingerprint(v), true, nil
	case "PSE_FTP_ROOTS":
		if len(v) > 2000 || strings.ContainsAny(v, "\r\n\x00") || strings.Contains(v, "..") {
			return "", true, errors.New("dossiers absolus séparés par « ; » attendus")
		}
		return strings.Join(ftp.ParseRoots(v), " ; "), true, nil
	case "PSE_FTP_EXCLUDE":
		if strings.ContainsAny(v, "\r\n\t/") {
			return "", true, errors.New("noms de dossiers séparés par des virgules attendus")
		}
		return v, true, nil
	case "PSE_FTP_PORT":
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 || n > 65535 {
			return "", true, errors.New("port de 0 à 65535 attendu")
		}
		return strconv.Itoa(n), true, nil
	}
	return "", false, nil
}
