package vela

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

type multiviewPlaybackRequest struct {
	GroupID    string `json:"groupId"`
	Tile       *int   `json:"tile"`
	TileCount  int    `json:"tileCount,omitempty"`
	TileHeight int    `json:"tileHeight,omitempty"`
	MaxHeight  int    `json:"maxHeight,omitempty"`
}

func roundMultiviewTileHeight(h int) int {
	switch {
	case h <= 0:
		return 0
	case h <= 480:
		return 480
	case h <= 720:
		return 720
	case h <= 1080:
		return 1080
	default:
		return 2160
	}
}

func parseMultiviewPlayback(req *multiviewPlaybackRequest) (*frontend.MultiviewTarget, bool) {
	if req == nil || req.GroupID == "" || req.Tile == nil {
		return nil, false
	}
	tile := *req.Tile
	if tile < 0 || tile >= frontend.MaxMultiviewTiles {
		return nil, false
	}
	if req.TileCount < 0 || req.TileCount > frontend.MaxMultiviewTiles {
		return nil, false
	}
	if req.TileHeight < 0 || req.TileHeight > 16384 || req.MaxHeight < 0 || req.MaxHeight > 16384 {
		return nil, false
	}
	height := req.MaxHeight
	if req.TileHeight > 0 && (height == 0 || req.TileHeight < height) {
		height = req.TileHeight
	}
	return &frontend.MultiviewTarget{
		GroupID:   req.GroupID,
		Tile:      tile,
		TileCount: req.TileCount,
		MaxHeight: height,
	}, true
}

func (i *instance) multiviewAvailable() bool {
	if _, live := i.host.LiveTV.(frontend.LiveGuidePager); !live {
		return false
	}
	mv, ok := i.host.Sessions.(frontend.MultiviewSessions)
	return ok && mv.MultiviewAvailable()
}

func sendProviderConnectionCap(w http.ResponseWriter, capErr *frontend.ProviderConnectionCapError) {
	send(w, http.StatusConflict, object{
		"error": object{
			"class":   "capacity",
			"code":    "provider_connection_cap",
			"message": capErr.Error(),
			"details": object{
				"provider": capErr.Provider,
				"max":      capErr.Max,
				"inUse":    capErr.InUse,
			},
			"retry": object{"allowed": false},
		},
	})
}

func parseMultiviewDeviceID(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true
	}
	if trimmed[0] == '"' {
		var id string
		if json.Unmarshal(trimmed, &id) != nil {
			return "", false
		}
		return strings.TrimSpace(id), true
	}
	var d device
	if json.Unmarshal(trimmed, &d) != nil {
		return "", false
	}
	return strings.TrimSpace(d.ID), true
}

func (i *instance) createMultiviewGroup(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	mv, ok := i.host.Sessions.(frontend.MultiviewSessions)
	if !ok || !i.multiviewAvailable() {
		i.unavailable(w, r)
		return
	}
	var b struct {
		Device json.RawMessage `json:"device"`
	}
	if body(r, &b) != nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	deviceID, valid := parseMultiviewDeviceID(b.Device)
	if !valid {
		problem(w, 400, "request", "invalid_device")
		return
	}
	reqDev := requestDevice(r)
	if reqDev.ID == "" {
		problem(w, 400, "request", "device_required")
		return
	}
	if deviceID != "" && deviceID != reqDev.ID {
		problem(w, 400, "request", "invalid_device")
		return
	}
	group, err := mv.CreateMultiviewGroup(r.Context(), i.access(r, v))
	if err != nil {
		fail(w, err, r)
		return
	}
	send(w, http.StatusOK, object{
		"groupId":   group.GroupID,
		"maxTiles":  group.MaxTiles,
		"expiresAt": group.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (i *instance) deleteMultiviewGroup(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	mv, ok := i.host.Sessions.(frontend.MultiviewSessions)
	if !ok || !i.multiviewAvailable() {
		i.unavailable(w, r)
		return
	}
	groupID := r.PathValue("groupId")
	if groupID == "" {
		problem(w, 404, "not-found", "not_found")
		return
	}
	if err := mv.DeleteMultiviewGroup(r.Context(), i.access(r, v), groupID); err != nil {
		fail(w, err, r)
		return
	}
	i.endMultiviewSessions(v.ID, requestDevice(r).ID, groupID)
	w.WriteHeader(http.StatusNoContent)
}

func (i *instance) endMultiviewSessions(viewerID int64, deviceID, groupID string) {
	if groupID == "" {
		return
	}
	i.mu.Lock()
	var victims []*playSession
	for _, s := range i.sessions {
		if s.viewer == viewerID && s.device == deviceID && s.multiviewGroup == groupID {
			victims = append(victims, s)
		}
	}
	i.mu.Unlock()
	now := time.Now()
	for _, s := range victims {
		s.mu.Lock()
		if s.ended.IsZero() {
			s.ended = now
			s.clearDisconnectTimerLocked()
			if s.plan != nil {
				s.plan["state"] = "ended"
			}
			if s.signal != nil {
				s.publish("ended")
			}
		}
		s.mu.Unlock()
	}
}

// replanLiveSession replans an active live session (for example upgrading a
// multiview tile's height or restoring full device height when maxHeight is
// omitted/zero) through the shared StartStream replan path. It reports whether
// the command succeeded and whether the plan changed. Caller holds s.mu and
// s.commandMu.
func (i *instance) replanLiveSession(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, s *playSession, id string, maxHeight *int) (ok bool, changed bool) {
	if _, live := s.plan["live"]; !live {
		i.unavailable(w, r)
		return false, false
	}
	if maxHeight != nil && (*maxHeight < 0 || *maxHeight > 16384) {
		problem(w, 400, "request", "invalid_request")
		return false, false
	}
	targetHeight := 0
	if maxHeight != nil {
		targetHeight = *maxHeight
	}
	oldStart, oldEdge, hadOldWindow := liveWindowBounds(s.plan["live"])
	oldPos := s.position
	hadObserved := s.observedSource == s.source

	var url string
	var err error
	if !s.port(w, func() {
		url, err = i.host.Sessions.StartStream(r.Context(), i.access(r, v), id, frontend.StreamRequest{
			Format:     frontend.StreamHLS,
			LazyStart:  true,
			Continuous: true,
			MaxHeight:  &targetHeight,
		})
	}) {
		return false, false
	}
	if err != nil {
		fail(w, err, r)
		return false, false
	}
	paths, _ := s.plan["paths"].([]object)
	path := selectedPlaybackPath(s.plan, paths)
	prevURL, _ := path["url"].(string)
	if url == prevURL {
		return true, false
	}
	if len(path) > 0 {
		path["url"] = url
		path["readiness"] = "ready"
	}
	if mvDoc, ok := s.plan["multiview"].(object); ok {
		effective := targetHeight
		if effective == 0 {
			effective = s.declaredCaps.MaxHeight
		} else {
			effective = roundMultiviewTileHeight(effective)
			if s.declaredCaps.MaxHeight > 0 && s.declaredCaps.MaxHeight < effective {
				effective = s.declaredCaps.MaxHeight
			}
		}
		if effective > 0 {
			mvDoc["maxHeight"] = effective
		} else {
			delete(mvDoc, "maxHeight")
		}
		delete(mvDoc, "focused")
	}
	s.retainDiagnostics()
	s.source++
	s.seek++
	if !s.portSnapshot(w, func(copy *playSession) { i.refreshOutput(r.Context(), id, copy) }) {
		return false, false
	}
	s.position = rebaseLivePosition(s.plan["live"], oldPos, oldStart, oldEdge, hadOldWindow && hadObserved)
	return true, true
}
