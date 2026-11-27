package plex

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/plextimeline"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

func (s *Server) timelineWatch(ctx context.Context, pr *Request, key frontend.RatingKey, offset, duration time.Duration, status string, r *http.Request) (context.Context, *frontend.TimelinePersistenceResult, error) {
	if suppressTimelineWatch(ctx) {
		return frontend.WithTimelineStored(ctx), nil, nil
	}
	state := s.state()
	if _, supported := state.(frontend.WatchMutationWriter); !supported {
		return ctx, nil, nil
	}
	writer := timeline.Writer{State: state, Store: &s.timelineStore, Now: s.now}
	var offline *int
	if r.URL.Query().Get("offline") == "1" {
		n, err := strconv.Atoi(r.URL.Query().Get("viewCount"))
		if err != nil || n < 0 {
			n = -1
		}
		offline = &n
	}
	var restoreContinue func(context.Context, int64, frontend.RatingKey) error
	if d := s.discovery(); d != nil && d.Personal != nil {
		restoreContinue = d.Personal.RestoreContinue
	}
	return frontend.StoreTimeline(ctx, writer, s.now, restoreContinue, pr.User.ID, key, offset, duration, status, r.URL.Query().Get("playQueueItemID"), offline)
}
func timelineWatchAttrs(mc *model.MediaContainer, result *frontend.TimelinePersistenceResult) {
	if result == nil {
		return
	}
	offset := int64(-1)
	if result.ViewOffset > 0 {
		offset = result.ViewOffset.Milliseconds()
	}
	mc.Extra = append(mc.Extra, model.A("playbackState", result.PlaybackState), model.A("viewOffset", offset), model.A("viewCount", result.ViewCount), model.A("skipCount", 0))
}

// storeOfflineTimeline handles a downloaded file independently of live
// playback, including when the device has already started another stream.
func (s *Server) storeOfflineTimeline(w http.ResponseWriter, r *http.Request, pr *Request, key frontend.RatingKey, title *frontend.Title, received time.Time) {
	q := r.URL.Query()
	updated, err := strconv.ParseInt(q.Get("updated"), 10, 64)
	if err != nil || updated <= 0 {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	st, ok := s.state().(frontend.OfflineProgressState)
	if !ok {
		s.writeEmpty(w, r)
		return
	}
	duration := msParam(q.Get("duration"))
	if duration <= 0 && title != nil {
		duration = title.Runtime
	}
	count, _ := strconv.Atoi(q.Get("viewCount"))
	if count <= 0 && !q.Has("time") {
		s.writeEmpty(w, r)
		return
	}
	observed := received
	if updated <= received.Unix() {
		observed = time.Unix(updated, 0)
	}
	result, err := st.ApplyOfflineProgress(r.Context(), pr.User.ID, key, frontend.OfflineProgress{
		Position: msParam(q.Get("time")), Duration: duration, Played: count > 0,
		ObservedAt: observed, ReceivedAt: received,
	})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	mc := &model.MediaContainer{}
	timelineWatchAttrs(mc, &result)
	s.write(w, r, mc)
}
