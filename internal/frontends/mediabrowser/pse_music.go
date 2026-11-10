package mediabrowser

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// Bibliothèque « Musique » de PSE pour les clients Jellyfin et Emby (voir
// docs/pse/FORK.fr.md). Le traitement est dans internal/pse/music (par
// frontend.PSEMusic) ; ce
// fichier ajoute la bibliothèque aux vues et intercepte, avant le routeur
// d'Ether, les seules requêtes qui visent la musique.

const pseMusicMarker = "50534d55" // préfixe des GUID musique

func pseMusicViewID() string {
	id, _ := ids.GUID(ids.View, frontend.PSEMusicViewNumber)
	return id
}

// pseMusicFolder renvoie la bibliothèque Musique, ou ok=false si la musique
// est coupée.
func (i *instance) pseMusicFolder() (map[string]any, bool) {
	if frontend.CurrentPSEMusic() == nil {
		return nil, false
	}
	viewID := pseMusicViewID()
	name := "Musique"
	item := map[string]any{"Id": viewID, "Name": name, "Type": "CollectionFolder", "CollectionType": "music", "IsFolder": true, "ServerId": i.info.ID, "SortName": name, "DisplayPreferencesId": viewID, "CanDelete": false, "CanDownload": false, "ProviderIds": map[string]string{}}
	if i.protocol == "emby" {
		i.embyViewDTO(item)
	}
	if i.protocol == "jellyfin" {
		i.jellyfinCollectionFolder(item, viewID)
		item["ParentId"] = jellyfinRootID()
	}
	return item, true
}

// pseMusic enveloppe le routeur d'Ether.
func (i *instance) pseMusic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv := frontend.CurrentPSEMusic()
		if srv == nil || !i.pseMusicCandidate(r) {
			next.ServeHTTP(w, r)
			return
		}
		var viewer int64
		if v, _, err := i.authenticate(r); err == nil && v != nil {
			viewer = v.ID
		}
		j := frontend.PSEMusicJellyfin{Protocol: string(i.protocol), ServerID: i.info.ID, ViewID: pseMusicViewID(), Folder: func() map[string]any {
			item, _ := i.pseMusicFolder()
			return item
		}}
		if srv.ServeJellyfin(w, r, viewer, j) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// pseMusicCandidate écarte sans authentification les requêtes qui ne
// peuvent pas concerner la musique.
func (i *instance) pseMusicCandidate(r *http.Request) bool {
	path := strings.ToLower(r.URL.Path)
	query := strings.ToLower(r.URL.RawQuery)
	if strings.Contains(path, pseMusicMarker) || strings.Contains(query, pseMusicMarker) {
		return true
	}
	if strings.Contains(path, pseMusicViewID()) || strings.Contains(query, pseMusicViewID()) {
		return true
	}
	if strings.HasPrefix(path, "/artists") {
		return true
	}
	for k, vs := range r.URL.Query() {
		if strings.EqualFold(k, "IncludeItemTypes") && len(vs) > 0 {
			only := vs[0] != ""
			for _, t := range strings.Split(vs[0], ",") {
				switch strings.ToLower(strings.TrimSpace(t)) {
				case "audio", "musicalbum", "musicartist":
				default:
					only = false
				}
			}
			if only {
				return true
			}
		}
	}
	if r.Method == http.MethodPost && strings.HasPrefix(path, "/sessions/playing") && r.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		r.Body = rewound(raw, r.Body)
		return err == nil && bytes.Contains(bytes.ToLower(raw), []byte(pseMusicMarker))
	}
	return false
}

// rewound rend à Ether un corps déjà lu en partie.
func rewound(head []byte, rest io.ReadCloser) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), rest), rest}
}

// psePlaylists ajoute aux playlists d'Ether les playlists musicales
// importées du spectateur (Deezer, Last.fm), sur la première page.
func (i *instance) psePlaylists(r *http.Request, viewer *frontend.Viewer, items []any, total, offset int) ([]any, int) {
	m := frontend.CurrentPSEMusic()
	if m == nil || viewer == nil || offset > 0 {
		return items, total
	}
	extra := m.JellyfinPlaylists(r.Context(), viewer.ID, frontend.PSEMusicJellyfin{Protocol: string(i.protocol), ServerID: i.info.ID, ViewID: pseMusicViewID()})
	return append(items, extra...), total + len(extra)
}
