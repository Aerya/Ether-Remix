package alko

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

const searchLimit = 200

// Search renvoie les fichiers vérifiés d'un film ou d'un épisode, depuis le
// miroir local : aucun appel réseau. Les releases sont celles de la
// publication (releasedb.AlkoRelease) : ID et GUID « sfr-alko:<playback_id> »,
// lien AllDebrid déchiffré au dernier moment, ni magnet ni empreinte.
func (s *Service) Search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	if req.IDs.TMDB <= 0 || (req.Type != core.TypeMovie && req.Type != core.TypeEpisode) {
		return nil, nil
	}
	kind := "movie"
	if req.Type == core.TypeEpisode {
		kind = "episode"
	}
	rows, err := s.st().forSearch(ctx, kind, req.IDs.TMDB, req.Season, req.Episode, searchLimit)
	if err != nil {
		return nil, err
	}
	out := make([]core.Release, 0, len(rows))
	for _, r := range rows {
		url, err := s.openURL(r.Sealed)
		if err != nil {
			continue
		}
		if rel, ok := releasedb.AlkoRelease(releasedb.AlkoFile{ID: r.ID, Kind: kind, Name: r.Filename, URL: url, Size: r.Size, TMDB: r.TMDB}, s.Settings.LangFR); ok {
			out = append(out, rel)
		}
	}
	return out, nil
}
