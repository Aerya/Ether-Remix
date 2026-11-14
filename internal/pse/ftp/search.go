package ftp

import (
	"context"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/xdcc"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// GUIDPrefix marque les releases de la source FTP.
const GUIDPrefix = "ftp:"

// PlaceholderURL est le lien enregistré avec une release : il ne sert qu'à
// la reconnaître. La préparation le remplace par un lien local à jeton.
const PlaceholderURL = "http://127.0.0.1/ftp/"

// Catégories Newznab des résultats (films, séries).
const (
	movieCategory = 2000
	tvCategory    = 5000
)

const maxResults = 50

// IDOf renvoie l'identifiant de fichier d'une release FTP.
func IDOf(rel core.Release) (string, bool) {
	id, ok := strings.CutPrefix(rel.GUID, GUIDPrefix)
	if !ok || len(id) != 24 {
		return "", false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", false
		}
	}
	return id, true
}

// Matches applique les règles de la source XDCC : titre (ou titre
// d'origine, ou alternatif), année d'un film, saison et épisode d'un
// épisode. Un fichier sans année ou sans numéro est écarté.
func Matches(name string, req core.SearchRequest) (core.ReleaseInfo, bool) {
	return xdcc.Matches(name, req)
}

// Search répond au gestionnaire d'indexeurs d'Ether depuis l'index local :
// aucun appel au serveur, aucune lecture de fichier.
func (s *Service) Search(_ context.Context, req core.SearchRequest, _ func(context.Context, string) error) ([]core.Release, error) {
	if req.Type != core.TypeMovie && req.Type != core.TypeEpisode && req.Type != core.TypeShow {
		return nil, nil
	}
	s.mu.Lock()
	cands := make([]File, 0, 16)
	for _, f := range s.files {
		if !prefilter(f.info, req) {
			continue
		}
		cands = append(cands, f.File)
	}
	s.mu.Unlock()
	var out []core.Release
	for _, f := range cands {
		info, ok := Matches(f.Name, req)
		if !ok {
			continue
		}
		out = append(out, s.release(f, info, req.Type))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Size > out[j].Size })
	if len(out) > maxResults {
		out = out[:maxResults]
	}
	return out, nil
}

// prefilter écarte vite ce qui ne peut pas correspondre (année, épisode).
func prefilter(info core.ReleaseInfo, req core.SearchRequest) bool {
	switch req.Type {
	case core.TypeMovie:
		return info.Year > 0 && len(info.Episodes) == 0 && (req.Year == 0 || releaseparse.YearMatches(info.Year, req.Year))
	default:
		return len(info.Episodes) > 0 && (req.Season == 0 || containsInt(info.Seasons, req.Season))
	}
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// release construit le candidat d'Ether. Le lien enregistré n'est qu'un
// repère : la préparation donne le vrai lien local à la lecture.
func (s *Service) release(f File, info core.ReleaseInfo, typ core.MediaType) core.Release {
	cat := movieCategory
	if typ != core.TypeMovie {
		cat = tvCategory
	}
	return core.Release{
		GUID:         GUIDPrefix + f.ID,
		Name:         f.Name,
		Size:         f.Size,
		PubDate:      f.MTime,
		Category:     cat,
		Info:         info,
		Protocol:     core.ProtocolHTTP,
		StreamURL:    PlaceholderURL + f.ID,
		StreamOrigin: "http://127.0.0.1",
	}
}

// ManualResult est un résultat de recherche de la console.
type ManualResult struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Height int    `json:"height,omitempty"`
	Langs  string `json:"langs,omitempty"`
	State  string `json:"state"` // available, downloading, ready, failed
}

// ManualSearch cherche un texte libre dans l'index (console).
func (s *Service) ManualSearch(text string) []ManualResult {
	words := strings.Fields(releaseparse.NormalizeTitle(text))
	if len(words) == 0 {
		return nil
	}
	s.mu.Lock()
	var out []ManualResult
	for _, f := range s.files {
		hay := releaseparse.NormalizeTitle(f.Name + " " + path2words(f.Path))
		all := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		m := ManualResult{ID: f.ID, Name: f.Name, Path: f.Path, Size: f.Size, Height: f.info.Height, Langs: strings.Join(f.info.Languages, ", "), State: "available"}
		if t := s.tasks[f.ID]; t != nil {
			switch t.State {
			case StateDownloading, StateQueued:
				m.State = "downloading"
			case StateDone:
				m.State = "ready"
			case StateFailed:
				m.State = "failed"
			}
		}
		out = append(out, m)
		if len(out) >= 200 {
			break
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func path2words(p string) string {
	return strings.NewReplacer("/", " ", "_", " ", ".", " ").Replace(p)
}
