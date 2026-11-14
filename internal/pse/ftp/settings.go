// Package ftp ajoute à Ether-Remix une source FTP / SFTP, en option et
// désactivée par défaut.
//
// Remix parcourt les dossiers choisis sur le serveur, garde la liste des
// vidéos dans sa base et la sert au gestionnaire d'indexeurs d'Ether comme
// une source de plus. À la lecture, un petit serveur HTTP de Remix, limité
// à 127.0.0.1 et protégé par un jeton éphémère, relit le fichier distant
// par plages : le moteur de lecture d'Ether le traite comme un flux debrid.
//
// Trois modes : « stream » (lecture directe, aucun média écrit sur disque),
// « download » (copie complète puis lecture de la copie) et « both »
// (lecture directe et copie gardée). Seuls les deux derniers écrivent sur
// le disque.
package ftp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Protocoles (PSE_FTP_PROTOCOL).
const (
	ProtoSFTP     = "sftp"          // SSH, port 22
	ProtoFTPS     = "ftps"          // FTP avec TLS explicite (AUTH TLS), port 21
	ProtoFTPSImpl = "ftps-implicit" // FTP avec TLS implicite, port 990
	ProtoFTP      = "ftp"           // FTP en clair : à éviter
)

// Modes (PSE_FTP_MODE).
const (
	// ModeStream : lecture directe depuis le serveur, rien n'est écrit.
	ModeStream = "stream"
	// ModeDownload : copie complète dans le dossier de téléchargement,
	// lecture depuis la copie ; copie temporaire (conservation, plafond).
	ModeDownload = "download"
	// ModeBoth : lecture directe et copie gardée en arrière-plan.
	ModeBoth = "both"
)

// Settings regroupe les réglages PSE_FTP_*.
type Settings struct {
	Enabled    bool
	Protocol   string
	Host       string
	Port       int
	User       string
	Password   string // PSE_FTP_PASSWORD : jamais affiché, jamais journalisé
	KeyFile    string // PSE_FTP_KEY_FILE : clé privée SSH (fichier monté)
	Passphrase string // PSE_FTP_KEY_PASSPHRASE
	// HostKey : PSE_FTP_HOST_KEY, empreinte SHA-256 de la clé de l'hôte SSH
	// (SHA256:…), ou du certificat TLS d'un serveur FTPS auto-signé (hex).
	HostKey string

	Roots     []string      // PSE_FTP_ROOTS : dossiers parcourus, séparés par « ; »
	Exclude   []string      // PSE_FTP_EXCLUDE : noms de dossiers ignorés
	Depth     int           // PSE_FTP_DEPTH : profondeur maximale
	ScanEvery time.Duration // PSE_FTP_SCAN_HOURS

	Mode        string
	DownloadDir string // PSE_FTP_DOWNLOAD_DIR

	Connections int           // PSE_FTP_CONNECTIONS : connexions simultanées au serveur
	MaxActive   int           // PSE_FTP_MAX_ACTIVE : copies simultanées
	CacheBytes  int64         // PSE_FTP_CACHE_GB
	Retention   time.Duration // PSE_FTP_CACHE_HOURS
	MinFree     int64         // PSE_FTP_MIN_FREE
	Daily       int           // PSE_FTP_SEARCH_DAILY
}

const (
	defaultDepth       = 6
	defaultScanHours   = 6
	defaultConnections = 4
	defaultMaxActive   = 1
	defaultCacheGB     = 50
	defaultRetention   = 48 * time.Hour
	defaultMinFreeGB   = 20
	defaultDaily       = 100000
)

// LoadSettings lit les réglages effectifs (console, sinon environnement).
// La source reste inactive tant qu'il manque l'hôte, l'utilisateur, un
// moyen de connexion ou, en SFTP, l'empreinte de l'hôte.
func LoadSettings() Settings {
	s := Settings{
		Enabled:     on(pseenv.Get("PSE_FTP")),
		Protocol:    NormalizeProtocol(pseenv.Get("PSE_FTP_PROTOCOL")),
		Host:        strings.TrimSpace(pseenv.Get("PSE_FTP_HOST")),
		Port:        intIn(pseenv.Get("PSE_FTP_PORT"), 0, 0, 65535),
		User:        strings.TrimSpace(pseenv.Get("PSE_FTP_USER")),
		Password:    pseenv.Get("PSE_FTP_PASSWORD"),
		KeyFile:     cleanAbs(pseenv.Get("PSE_FTP_KEY_FILE")),
		Passphrase:  pseenv.Get("PSE_FTP_KEY_PASSPHRASE"),
		HostKey:     strings.TrimSpace(pseenv.Get("PSE_FTP_HOST_KEY")),
		Depth:       intIn(pseenv.Get("PSE_FTP_DEPTH"), defaultDepth, 1, 20),
		ScanEvery:   time.Duration(intIn(pseenv.Get("PSE_FTP_SCAN_HOURS"), defaultScanHours, 1, 24*30)) * time.Hour,
		Mode:        NormalizeMode(pseenv.Get("PSE_FTP_MODE")),
		DownloadDir: cleanAbs(pseenv.Get("PSE_FTP_DOWNLOAD_DIR")),
		Connections: intIn(pseenv.Get("PSE_FTP_CONNECTIONS"), defaultConnections, 1, 16),
		MaxActive:   intIn(pseenv.Get("PSE_FTP_MAX_ACTIVE"), defaultMaxActive, 1, 10),
		CacheBytes:  int64(intIn(pseenv.Get("PSE_FTP_CACHE_GB"), defaultCacheGB, 0, 100000)) << 30,
		Retention:   time.Duration(intIn(pseenv.Get("PSE_FTP_CACHE_HOURS"), int(defaultRetention/time.Hour), 1, 24*365)) * time.Hour,
		MinFree:     int64(intIn(pseenv.Get("PSE_FTP_MIN_FREE"), defaultMinFreeGB, 0, 100000)) << 30,
		Daily:       intIn(pseenv.Get("PSE_FTP_SEARCH_DAILY"), defaultDaily, 100, 1000000),
	}
	s.Roots = ParseRoots(pseenv.Get("PSE_FTP_ROOTS"))
	for _, x := range strings.Split(pseenv.Get("PSE_FTP_EXCLUDE"), ",") {
		if x = strings.ToLower(strings.TrimSpace(x)); x != "" {
			s.Exclude = append(s.Exclude, x)
		}
	}
	if s.Mode != ModeStream && s.DownloadDir == "" {
		s.Mode = ModeStream // sans dossier, rien ne s'écrit
	}
	if s.Problem() != "" {
		s.Enabled = false
	}
	return s
}

// Problem explique ce qui manque pour activer la source ("" : complet).
func (s Settings) Problem() string {
	switch {
	case s.Host == "" || ValidateHost(s.Host) != nil:
		return "adresse du serveur manquante ou invalide"
	case s.User == "":
		return "utilisateur manquant"
	case s.Password == "" && (s.Protocol != ProtoSFTP || s.KeyFile == ""):
		if s.Protocol == ProtoSFTP {
			return "mot de passe ou clé SSH manquant"
		}
		return "mot de passe manquant"
	case s.Protocol == ProtoSFTP && NormalizeFingerprint(s.HostKey) == "":
		return "empreinte de l'hôte SSH manquante : utilisez « Tester la connexion »"
	}
	return ""
}

// Addr est l'adresse host:port du serveur.
func (s Settings) Addr() string {
	port := s.Port
	if port == 0 {
		switch s.Protocol {
		case ProtoSFTP:
			port = 22
		case ProtoFTPSImpl:
			port = 990
		default:
			port = 21
		}
	}
	return net.JoinHostPort(s.Host, strconv.Itoa(port))
}

// Server identifie le serveur et le compte (identifiants des fichiers).
func (s Settings) Server() string {
	return s.Protocol + "://" + s.User + "@" + strings.ToLower(s.Addr())
}

// Identity résume ce qui change les résultats ; aucun secret n'y entre,
// même haché.
func (s Settings) Identity() string {
	h := sha256.Sum256([]byte(s.Server() + "\x00" + strings.Join(s.Roots, ";") + "\x00" + s.Mode))
	return hex.EncodeToString(h[:8])
}

// Writes indique un mode qui écrit des médias sur le disque.
func (s Settings) Writes() bool { return s.Mode != ModeStream }

// FileID est l'identifiant stable d'un fichier du serveur.
func (s Settings) FileID(remote string) string {
	h := sha256.Sum256([]byte(s.Server() + "\x00" + remote))
	return hex.EncodeToString(h[:12])
}

// Excluded indique un dossier ignoré par le parcours.
func (s Settings) Excluded(name string) bool {
	n := strings.ToLower(name)
	if strings.HasPrefix(n, ".") || n == "@eadir" || n == "#recycle" || n == "$recycle.bin" {
		return true
	}
	for _, x := range s.Exclude {
		if n == x {
			return true
		}
	}
	return false
}

// NormalizeProtocol renvoie un protocole connu (SFTP par défaut).
func NormalizeProtocol(v string) string {
	switch p := strings.ToLower(strings.TrimSpace(v)); p {
	case ProtoSFTP, ProtoFTPS, ProtoFTPSImpl, ProtoFTP:
		return p
	}
	return ProtoSFTP
}

// NormalizeMode renvoie un mode connu (stream par défaut).
func NormalizeMode(v string) string {
	switch m := strings.ToLower(strings.TrimSpace(v)); m {
	case ModeStream, ModeDownload, ModeBoth:
		return m
	}
	return ModeStream
}

// ParseRoots lit « /films ; /series » ; vide : la racine du compte.
func ParseRoots(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(v, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		p = path.Clean(p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"/"}
	}
	return out
}

// ValidateHost vérifie un nom d'hôte ou une adresse IP, sans schéma, port,
// identifiants ni chemin.
func ValidateHost(v string) error {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 253 {
		return errors.New("nom d'hôte ou adresse IP attendu, ex. seedbox.exemple.net")
	}
	if net.ParseIP(strings.Trim(v, "[]")) != nil {
		return nil
	}
	for _, label := range strings.Split(v, ".") {
		if label == "" || len(label) > 63 {
			return errors.New("nom d'hôte invalide (sans « sftp:// », port ni chemin)")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return errors.New("nom d'hôte invalide (sans « sftp:// », port ni chemin)")
			}
		}
	}
	return nil
}

// NormalizeFingerprint met une empreinte SSH au format « SHA256:… », ou
// une empreinte de certificat en hexadécimal minuscule ; "" si invalide.
func NormalizeFingerprint(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(v, "SHA256:"); ok {
		if len(rest) < 40 || len(rest) > 64 || strings.ContainsAny(rest, " \t\r\n") {
			return ""
		}
		return "SHA256:" + rest
	}
	h := strings.ToLower(strings.ReplaceAll(v, ":", ""))
	if len(h) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(h); err != nil {
		return ""
	}
	return h
}

func on(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "1", "true", "yes", "oui":
		return true
	}
	return false
}

func intIn(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}

func cleanAbs(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || !strings.HasPrefix(v, "/") {
		return ""
	}
	return path.Clean(v)
}
