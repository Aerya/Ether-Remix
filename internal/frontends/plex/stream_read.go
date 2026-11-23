package plex

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// readCapParam is the minted playlist/segment read capability. It is
// never a client-chosen session id and must not appear in status,
// notifications, cluster messages, or request logs.
const readCapParam = "X-Ether-Read-Token"

type readCapability struct {
	sessionID string
	clientID  string
	userID    int64
}

type readCapabilityStore struct {
	mu        sync.Mutex
	byToken   map[string]readCapability
	bySession map[string]string
}

func newReadCapabilityStore() *readCapabilityStore {
	return &readCapabilityStore{byToken: map[string]readCapability{}, bySession: map[string]string{}}
}

func (c *readCapabilityStore) mint(sessionID, clientID string, userID int64) string {
	if c == nil || sessionID == "" {
		return ""
	}
	token := rand.Text()
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.bySession[sessionID]; ok {
		delete(c.byToken, old)
	}
	c.byToken[token] = readCapability{sessionID: sessionID, clientID: clientID, userID: userID}
	c.bySession[sessionID] = token
	return token
}

func (c *readCapabilityStore) revoke(sessionID string) {
	if c == nil || sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.bySession[sessionID]; ok {
		delete(c.byToken, old)
		delete(c.bySession, sessionID)
	}
}

func (c *readCapabilityStore) lookup(token string) (readCapability, bool) {
	if c == nil || token == "" {
		return readCapability{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cap, ok := c.byToken[token]
	return cap, ok
}

func (c *readCapabilityStore) token(sessionID string) string {
	if c == nil || sessionID == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bySession[sessionID]
}

func (s *Server) bindTranscodeStarted(entry *transcodeEntry, dec frontend.Decision, offset time.Duration) {
	entry.decision, entry.offset, entry.started, entry.updatedAt = dec, offset, true, s.now()
	s.transcodes.put(entry)
	s.mintReadCapability(entry.id, entry.clientID, entry.userID)
	s.retireSupersededTranscodes(entry)
	s.rebindSessionTranscode(entry)
}

func (s *Server) retireSupersededTranscodes(entry *transcodeEntry) {
	if entry == nil || entry.userID == 0 || entry.clientID == "" || entry.key == 0 {
		return
	}
	for _, otherID := range s.transcodes.forClient(entry.userID, entry.clientID, entry.key) {
		if otherID == entry.id {
			continue
		}
		other, ok := s.transcodes.get(otherID)
		if !ok {
			continue
		}
		samePlayback := entry.playbackID == "" || other.playbackID == "" || other.playbackID == entry.playbackID
		stale := other.playbackID != "" && s.playbackOwners.Stale(entry.userID, entry.clientID, entry.key, other.playbackID)
		if !samePlayback && !stale {
			continue
		}
		if other.started && s.transcodeEntryLive(other) && s.transcoder() != nil {
			s.stopPlexHLS(otherID)
		}
		s.dropTranscode(otherID)
	}
}

func (s *Server) rebindSessionTranscode(entry *transcodeEntry) {
	if entry == nil || entry.id == "" || entry.userID == 0 {
		return
	}
	matched := false
	for _, act := range s.sessions.list() {
		if act.user.ID != entry.userID || act.key != entry.key {
			continue
		}
		if entry.clientID != "" && act.client.ClientID != entry.clientID {
			continue
		}
		if entry.playbackID != "" && act.client.SessionID != "" && act.client.SessionID != entry.playbackID {
			continue
		}
		matched = true
		if act.transcodeID != entry.id {
			s.sessions.update(act.id, func(e *playEntry) {
				e.transcodeID = entry.id
			})
		}
	}
	if matched && s.cl != nil {
		s.cl.poke()
	}
}

func (s *Server) mintReadCapability(sessionID, clientID string, userID int64) string {
	return s.readCaps.mint(sessionID, clientID, userID)
}

func (s *Server) revokeReadCapability(sessionID string) {
	s.readCaps.revoke(sessionID)
}

func (s *Server) dropTranscode(id string) {
	if storage, ok := s.currentPlexEncoder().(frontend.EncoderBindingDeleter); ok {
		ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
		defer cancel()
		_ = storage.DeleteBinding(ctx, id)
	}

	s.revokeReadCapability(id)
	s.transcodes.retire(id, s.now())
	s.starts.Forget(id)
	cleared := false
	for _, act := range s.sessions.list() {
		if act.transcodeID == id {
			s.sessions.update(act.id, func(e *playEntry) {
				if e.transcodeID == id {
					e.transcodeID = ""
				}
			})
			cleared = true
		}
	}
	if cleared && s.cl != nil {
		s.cl.poke()
	}
}

func readCapURL(raw, token string) string {
	if token == "" {
		return raw
	}
	sep := "?"
	if strings.Contains(raw, "?") {
		sep = "&"
	}
	return raw + sep + readCapParam + "=" + token
}

func (s *Server) mediaURL(raw, sessionID, streamToken string) string {
	return readCapURL(streamGrantURL(raw, streamToken), s.readCaps.token(sessionID))
}

func readCapFrom(r *http.Request) string {
	return r.URL.Query().Get(readCapParam)
}
