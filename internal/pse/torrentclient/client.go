// Package torrentclient relie Ether-Remix au client BitTorrent du
// propriétaire (qBittorrent, rTorrent/ruTorrent), en option et désactivé
// par défaut : Remix y envoie les releases torrent trouvées sur Prowlarr ou
// Jackett, les lit une fois téléchargées depuis le dossier partagé, laisse
// le client les partager selon les limites de ratio et de durée, puis
// prévient cross-seed. Remix ne retire jamais un torrent du client.
package torrentclient

import (
	"context"
	"errors"
	"strings"
	"time"
)

// File est un fichier d'un torrent ; Path est le chemin absolu vu par le client.
type File struct {
	Path string
	Size int64
}

// Torrent est l'état d'un torrent sur le client.
type Torrent struct {
	Hash     string // empreinte v1, hexadécimal minuscule
	Name     string
	Category string
	Done     bool // téléchargement terminé (en partage ou arrêté)
	Active   bool // en cours (téléchargement ou partage)
	Ratio    float64
	Seeding  time.Duration // durée de partage, si le client la donne
	Finished time.Time     // fin du téléchargement, si le client la donne
	Notified bool          // cross-seed déjà prévenu
	// Root est le dossier qui contient les chemins relatifs des fichiers.
	Root string
}

// AddOptions décrit un ajout.
type AddOptions struct {
	Magnet   string
	Category string
	SavePath string        // dossier côté client ("" : celui du client)
	Ratio    float64       // limite de ratio (0 : réglage du client)
	SeedTime time.Duration // durée de partage minimale (0 : réglage du client)
}

// Client est un client BitTorrent.
type Client interface {
	Kind() string
	// Version vérifie l'accès et renvoie la version du client.
	Version(ctx context.Context) (string, error)
	Add(ctx context.Context, o AddOptions) error
	// Get renvoie le torrent, ou nil s'il n'est pas sur le client.
	Get(ctx context.Context, hash string) (*Torrent, error)
	// Lookup renvoie, parmi hashes, ceux présents sur le client.
	Lookup(ctx context.Context, hashes []string) (map[string]Torrent, error)
	// Files liste les fichiers d'un torrent présent.
	Files(ctx context.Context, t Torrent) ([]File, error)
	// List renvoie les torrents de la catégorie.
	List(ctx context.Context, category string) ([]Torrent, error)
	// MarkNotified retient sur le client que cross-seed a été prévenu.
	MarkNotified(ctx context.Context, hash string) error
	// Stop arrête le partage (limites atteintes) sans rien supprimer ;
	// ErrUnsupported quand le client applique lui-même ses limites.
	Stop(ctx context.Context, hash string) error
}

// ErrUnsupported : opération inutile pour ce client.
var ErrUnsupported = errors.New("torrentclient: opération non prise en charge")

// ErrAuth : identifiants refusés par le client.
var ErrAuth = errors.New("torrentclient: identifiants refusés")

// NotifiedTag marque les torrents déjà envoyés à cross-seed.
const NotifiedTag = "remix-xseed"

func normHash(h string) string { return strings.ToLower(strings.TrimSpace(h)) }

func validHash(h string) bool {
	if len(h) != 40 {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
