package plex

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// Bibliothèque « Musique » de PSE pour les clients Plex (Plexamp, Plex Web,
// applications Plex) — voir docs/pse/FORK.fr.md. Le traitement est dans
// internal/pse/music/plex.go ; ce fichier ajoute la section et passe à la
// musique, après l'authentification d'Ether, les requêtes qu'elle reconnaît.

// pseMusicDirectory est la section ajoutée à /library/sections.
func (s *Server) pseMusicDirectory() (model.Directory, bool) {
	if frontend.CurrentPSEMusic() == nil {
		return model.Directory{}, false
	}
	now := s.now().Unix()
	return model.Directory{
		AllowSync: model.Ptr(false), Filters: model.Ptr(true), Refreshing: model.Ptr(false),
		Key: strconv.Itoa(frontend.PSEMusicSection), Type: "artist", Title: "Musique",
		Agent: "tv.plex.agents.music", Scanner: "Plex Music", Language: "fr-FR", UUID: "50534d55-0000-4000-8000-000000005053",
		UpdatedAt: now, CreatedAt: now, ScannedAt: now, Content: model.Ptr(true), IsDirectory: model.Ptr(true),
		ContentChangedAt: now, Hidden: model.Ptr(0),
		Location: []model.Location{{ID: frontend.PSEMusicSection, Path: "/ether/musique"}},
	}, true
}

// pseMusicProvider ajoute la section Musique à /media/providers, la liste
// des bibliothèques lue par Plex Web et les applications Plex récentes.
func (s *Server) pseMusicProvider(dirs []model.ProviderDirectory) []model.ProviderDirectory {
	d, ok := s.pseMusicDirectory()
	if !ok {
		return dirs
	}
	id := d.Key
	return append(dirs, model.ProviderDirectory{
		Agent: d.Agent, Language: d.Language, Refreshing: model.Ptr(false), Scanner: d.Scanner, UUID: d.UUID,
		ID: id, Key: "/library/sections/" + id, HubKey: "/hubs/sections/" + id, Type: d.Type, Title: d.Title,
		UpdatedAt: d.UpdatedAt, ScannedAt: d.ScannedAt,
		Pivot: []model.Pivot{
			{ID: "recommended", Key: "/hubs/sections/" + id, Type: "hub", Title: "Recommended", Context: "content.discover", Symbol: "star"},
			{ID: "library", Key: "/library/sections/" + id + "/all?type=8", Type: "list", Title: "Library", Context: "content.library", Symbol: "library"},
			{ID: "playlists", Key: "/playlists?type=15&sectionID=" + id + "&playlistType=audio", Type: "list", Title: "Playlists", Context: "content.playlists", Symbol: "playlist"},
		},
	})
}

// pseProviderTypes annonce l'audio dans /media/providers quand la musique
// est active.
func (s *Server) pseProviderTypes(types string) string {
	if frontend.CurrentPSEMusic() == nil {
		return types
	}
	return types + ",audio"
}

func (s *Server) pseMusic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := frontend.CurrentPSEMusic()
		pr := FromContext(r.Context())
		if m == nil || pr.User == nil || pr.Capability {
			next.ServeHTTP(w, r)
			return
		}
		env := frontend.PSEMusicPlex{Write: s.write, Start: pr.Start, Size: pr.Size, HasPage: pr.HasPage}
		if !m.ServePlex(w, r, pr.User.ID, env) {
			next.ServeHTTP(w, r)
		}
	})
}

// pseMusicScore estime la pertinence d'un résultat musical, sur l'échelle
// des scores de recherche d'Ether : titre exact, début du titre, sinon
// faible.
func pseMusicScore(title, query string) float64 {
	t, q := strings.ToLower(strings.TrimSpace(title)), strings.ToLower(strings.TrimSpace(query))
	switch {
	case q == "":
		return 0
	case t == q:
		return 1
	case strings.HasPrefix(t, q):
		return 0.6
	case strings.Contains(t, q):
		return 0.4
	}
	return 0.2
}

type pseMusicHit struct {
	md    model.Metadata
	score float64
}

// pseMusicSearch interroge la musique pour la recherche globale, par type
// (artist, album, track), meilleurs résultats d'abord.
func (s *Server) pseMusicSearch(ctx context.Context, pr *Request, query string, limit int) map[string][]pseMusicHit {
	m := frontend.CurrentPSEMusic()
	if m == nil || pr.User == nil || pr.Capability {
		return nil
	}
	ar, al, tr := m.PlexSearch(ctx, pr.User.ID, query, limit)
	out := map[string][]pseMusicHit{}
	for typ, list := range map[string][]any{"artist": ar, "album": al, "track": tr} {
		for i, v := range list {
			md, ok := v.(model.Metadata)
			if !ok {
				continue
			}
			score := pseMusicScore(md.Title, query) * (1 - float64(i)/float64(len(list)+1)/2)
			out[typ] = append(out[typ], pseMusicHit{md, score})
		}
	}
	return out
}

// pseMusicFillSearchHubs remplit les hubs Artists, Albums et Tracks, que
// la recherche globale renvoie vides (comme PMS sans bibliothèque musicale).
func (s *Server) pseMusicFillSearchHubs(ctx context.Context, pr *Request, query string, limit int, hubs []model.Hub, tops []float64) {
	hits := s.pseMusicSearch(ctx, pr, query, limit)
	if hits == nil {
		return
	}
	for i := range hubs {
		list := hits[hubs[i].HubIdentifier]
		if len(list) == 0 || len(hubs[i].Metadata) > 0 {
			continue
		}
		top := 0.0
		for _, hit := range list {
			hit.md.Score = strconv.FormatFloat(hit.score, 'f', 5, 64)
			hubs[i].Metadata = append(hubs[i].Metadata, hit.md)
			top = max(top, hit.score)
		}
		if len(hubs[i].Metadata) > limit {
			hubs[i].Metadata, hubs[i].More = hubs[i].Metadata[:limit], true
		}
		hubs[i].Size = len(hubs[i].Metadata)
		if i < len(tops) {
			tops[i] = max(tops[i], top)
		}
	}
}

// pseMusicSearchResults complète /library/search quand la musique est
// demandée (searchTypes vide ou contenant music).
func (s *Server) pseMusicSearchResults(ctx context.Context, pr *Request, query, searchTypes string, limit int) ([]model.SearchResult, []float64) {
	if searchTypes != "" && !strings.Contains(searchTypes, "music") {
		return nil, nil
	}
	hits := s.pseMusicSearch(ctx, pr, query, limit)
	var out []model.SearchResult
	var raw []float64
	for _, typ := range []string{"artist", "album", "track"} {
		for _, hit := range hits[typ] {
			md := hit.md
			md.LibrarySectionID, md.LibrarySectionKey = 0, ""
			out = append(out, model.SearchResult{Score: float64(int(hit.score*10000)) / 10000, Metadata: &md})
			raw = append(raw, hit.score)
		}
	}
	return out, raw
}

// psePlaylists ajoute aux playlists d'Ether les playlists musicales
// importées du spectateur (Deezer, Last.fm), sur la première page d'une
// liste sans type ou audio.
func (s *Server) psePlaylists(r *http.Request, pr *Request, mc *model.MediaContainer) {
	m := frontend.CurrentPSEMusic()
	if m == nil || pr == nil || pr.User == nil || pr.Capability {
		return
	}
	q := r.URL.Query()
	if pt := q.Get("playlistType"); pt != "" && !strings.Contains(pt, "audio") || q.Get("smart") == "1" {
		return
	}
	if pr.HasPage && pr.Start > 0 {
		return
	}
	extra := m.PlexPlaylists(r.Context(), pr.User.ID)
	for _, v := range extra {
		if md, ok := v.(model.Metadata); ok {
			mc.Metadata = append(mc.Metadata, md)
		}
	}
	if mc.TotalSize != nil {
		mc.TotalSize = model.Ptr(*mc.TotalSize + len(extra))
	}
}
