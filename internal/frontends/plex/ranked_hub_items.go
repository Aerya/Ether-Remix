package plex

import (
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// Plex clients page and refresh a Home row through /hubs/items. A Top 10 row
// the Home painted ranked announces a wide coverArt display; when the same
// row is fetched again through /hubs/items it must keep that painting.
// Otherwise the client keeps the wide frame from the Home response, finds no
// coverArt image on the refreshed items and draws black tiles.
//
// The Home layout decides ranking per viewer (Top 10 families default on, an
// owner policy can turn it off), and that decision is only known while the
// Home is painted. The painted state is therefore remembered per viewer and
// row, so /hubs/items follows exactly what the Home showed.

const (
	rankedHubRetention = 6 * time.Hour
	rankedHubMaxKeys   = 4096
)

func rankedHubKey(pr *Request, id string) string {
	if pr == nil || pr.User == nil || id == "" {
		return ""
	}
	return strconv.FormatInt(pr.User.ID, 10) + "\x00" + id
}

// noteRankedHubs records which painted Home rows are ranked for this viewer.
// A row painted unranked clears an earlier ranked state.
func (s *Server) noteRankedHubs(pr *Request, hubs []model.Hub) {
	if len(hubs) == 0 || pr == nil || pr.User == nil {
		return
	}
	now := s.now()
	s.rankedHubMu.Lock()
	defer s.rankedHubMu.Unlock()
	if s.rankedHubs == nil || len(s.rankedHubs) >= rankedHubMaxKeys {
		fresh := make(map[string]time.Time, len(s.rankedHubs))
		for key, expires := range s.rankedHubs {
			if now.Before(expires) && len(fresh) < rankedHubMaxKeys/2 {
				fresh[key] = expires
			}
		}
		s.rankedHubs = fresh
	}
	for _, h := range hubs {
		key := rankedHubKey(pr, h.HubIdentifier)
		if key == "" {
			continue
		}
		if h.RankedTop10 {
			s.rankedHubs[key] = now.Add(rankedHubRetention)
		} else {
			delete(s.rankedHubs, key)
		}
	}
}

// rankedHub reports whether the viewer's Home last painted row id ranked.
func (s *Server) rankedHub(pr *Request, id string) bool {
	key := rankedHubKey(pr, id)
	if key == "" {
		return false
	}
	s.rankedHubMu.Lock()
	defer s.rankedHubMu.Unlock()
	expires, ok := s.rankedHubs[key]
	if ok && !s.now().Before(expires) {
		delete(s.rankedHubs, key)
		return false
	}
	return ok
}

// limitRankedItems keeps the ten ranked positions of a full row, before the
// requested page is cut from it.
func limitRankedItems(items []model.Metadata) []model.Metadata {
	return items[:min(len(items), 10)]
}

// paintRankedHubPage paints one /hubs/items page of a ranked row the way the
// Home painted the row: numbered artwork from the page's absolute position
// and the wide coverArt display.
func paintRankedHubPage(mc *model.MediaContainer, start int) {
	mc.Metadata = append([]model.Metadata(nil), mc.Metadata...)
	rankedMetadataOffset(mc.Metadata, start)
	typ := "movie"
	if len(mc.Metadata) > 0 && mc.Metadata[0].Type != "" {
		typ = mc.Metadata[0].Type
	}
	mc.Meta = &model.Meta{DisplayImage: []model.DisplayImage{{Type: typ, ImageType: "coverArt"}}}
}
