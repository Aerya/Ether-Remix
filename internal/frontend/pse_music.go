package frontend

import (
	"context"
	"net/http"
	"sync/atomic"
)

// Liaison entre les frontaux (Jellyfin, Plex) et la musique de PSE
// (internal/pse/music), qu'ils n'importent pas directement — voir
// docs/pse/FORK.fr.md. L'application publie le serveur musique actif avec
// SetPSEMusic ; nil quand la musique est coupée.

// PSEMusicViewNumber est le numéro de vue de la bibliothèque Musique côté
// Jellyfin, hors des numéros de bibliothèques d'Ether (petits entiers) et
// des rangées personnelles (bit 62).
const PSEMusicViewNumber int64 = 1<<61 + 0x5053_4d55

// PSEMusicSection est le numéro de section de la bibliothèque Musique côté
// Plex.
const PSEMusicSection = 5053

// PSEMusicJellyfin décrit le serveur Jellyfin qui sert la musique.
type PSEMusicJellyfin struct {
	ServerID string
	ViewID   string                // GUID de la bibliothèque Musique
	Folder   func() map[string]any // objet de la bibliothèque, pour /Items/{ViewID}
}

// PSEMusicPlex décrit le serveur Plex qui sert la musique.
type PSEMusicPlex struct {
	// Write encode un MediaContainer (internal/plex/model) au format demandé.
	Write   func(w http.ResponseWriter, r *http.Request, v any)
	Start   int // fenêtre X-Plex-Container-Start/Size
	Size    int
	HasPage bool
}

// PSEMusic est implémenté par le serveur musique de PSE.
type PSEMusic interface {
	// ServeJellyfin et ServePlex renvoient false pour une requête qui ne
	// vise pas la musique. viewer vaut 0 sans identifiant valide.
	ServeJellyfin(w http.ResponseWriter, r *http.Request, viewer int64, j PSEMusicJellyfin) bool
	ServePlex(w http.ResponseWriter, r *http.Request, viewer int64, p PSEMusicPlex) bool
	// PlexSearch renvoie les artistes, albums et titres trouvés pour la
	// recherche globale de Plex, en éléments model.Metadata.
	PlexSearch(ctx context.Context, viewer int64, query string, limit int) (artists, albums, tracks []any)
	// JellyfinPlaylists et PlexPlaylists renvoient les playlists musicales
	// importées du spectateur (objets Jellyfin, éléments model.Metadata),
	// ajoutées aux playlists d'Ether.
	JellyfinPlaylists(ctx context.Context, viewer int64, j PSEMusicJellyfin) []any
	PlexPlaylists(ctx context.Context, viewer int64) []any
}

type pseMusicBox struct{ m PSEMusic }

var pseMusic atomic.Pointer[pseMusicBox]

// SetPSEMusic publie le serveur musique actif (nil pour le couper).
func SetPSEMusic(m PSEMusic) {
	if m == nil {
		pseMusic.Store(nil)
		return
	}
	pseMusic.Store(&pseMusicBox{m})
}

// CurrentPSEMusic renvoie le serveur musique actif, ou nil.
func CurrentPSEMusic() PSEMusic {
	if b := pseMusic.Load(); b != nil {
		return b.m
	}
	return nil
}
