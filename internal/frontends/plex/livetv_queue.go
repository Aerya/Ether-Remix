package plex

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

func (s *Server) livePlayQueue(pq playQueue, info frontend.LiveTVSessionInfo) *model.MediaContainer {
	md := s.liveSessionMetadata(info)
	md.PlayQueueItemID = pq.Items[0].ID
	return &model.MediaContainer{
		Size: 1, Identifier: model.LibraryIdentifier, Metadata: []model.Metadata{md},
		Extra: []model.Attr{
			model.A("playQueueID", pq.ID),
			model.A("playQueueSelectedItemID", pq.Items[0].ID),
			model.A("playQueueSelectedItemOffset", 0),
			model.A("playQueueShuffled", false),
			model.A("playQueueSourceURI", pq.SourceURI),
			model.A("playQueueTotalCount", 1),
			model.A("playQueueVersion", pq.Version),
		},
	}
}

func (s *Server) writeLivePlayQueue(w http.ResponseWriter, r *http.Request, pq playQueue) {
	if !s.liveTVEnabled() {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	info, err := s.svc().liveTV.Session(r.Context(), pq.LiveSession, FromContext(r.Context()).User)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.write(w, r, s.livePlayQueue(pq, info))
}

// Live channel progress is not watch history for a catalogue movie/episode.
// Its opaque session key must not go through the numeric rating-key parser.
func (s *Server) handleLiveTimeline(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	key := q.Get("key")
	id := ""
	explicitLive := false
	if strings.HasPrefix(key, "/livetv/sessions/") {
		id = strings.TrimPrefix(key, "/livetv/sessions/")
		explicitLive = true
	} else if key == "" {
		if rk := q.Get("ratingKey"); rk != "" {
			if _, err := strconv.ParseInt(rk, 10, 64); err != nil {
				id = rk
			}
		}
	}
	if id == "" {
		return false
	}
	if !s.liveTVEnabled() {
		if explicitLive {
			writeHTTPError(w, http.StatusNotFound)
			return true
		}
		return false
	}
	pr := FromContext(r.Context())
	if _, err := s.svc().liveTV.Session(r.Context(), id, pr.User); err != nil {
		if explicitLive {
			s.writeErr(w, r, err)
			return true
		}
		return false
	}
	if q.Get("state") == "stopped" {
		if err := s.svc().liveTV.Stop(r.Context(), id, pr.User); err != nil {
			s.writeErr(w, r, err)
			return true
		}
	}
	s.writeEmpty(w, r)
	return true
}
