package plex

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// liveManifestRefetches records which client address last received a Live TV
// session's start.m3u8 on an authenticated request. Plex for Samsung fetches
// that playlist with its token, then hands the URL to the TV's native player,
// which requests it again without credentials. authenticateLiveManifestRefetch
// admits that one refetch, the HLS counterpart of authenticateManifestRefetch.
type liveManifestRefetches struct {
	mu     sync.Mutex
	served map[string]liveManifestServed // live session id
	alias  map[string]string             // client session id -> live session id
}

type liveManifestServed struct {
	ip           netip.Addr
	at           time.Time
	userID       int64
	clientID     string
	aliases      []string
	allowRefetch bool
}

func (t *liveManifestRefetches) note(id string, e liveManifestServed, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.served == nil {
		t.served, t.alias = map[string]liveManifestServed{}, map[string]string{}
	}
	const liveStopAliasTTL = 24 * time.Hour
	for old, prev := range t.served {
		ttl := manifestRefetchWindow
		if !prev.allowRefetch {
			ttl = liveStopAliasTTL
		}
		if now.Sub(prev.at) > ttl {
			t.drop(old, prev)
		}
	}
	if prev, ok := t.served[id]; ok {
		if !e.allowRefetch && prev.allowRefetch {
			e.allowRefetch = true
			if !e.ip.IsValid() {
				e.ip = prev.ip
			}
		}
		for _, a := range prev.aliases {
			seen := false
			for _, cur := range e.aliases {
				if cur == a {
					seen = true
					break
				}
			}
			if !seen {
				e.aliases = append(e.aliases, a)
			}
		}
		t.drop(id, prev)
	}
	t.served[id] = e
	for _, a := range e.aliases {
		t.alias[a] = id
	}
}

func (t *liveManifestRefetches) dropByID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.served[id]; ok {
		t.drop(id, prev)
	}
}

// drop removes id; t.mu is held.
func (t *liveManifestRefetches) drop(id string, e liveManifestServed) {
	delete(t.served, id)
	for _, a := range e.aliases {
		if t.alias[a] == id {
			delete(t.alias, a)
		}
	}
}

// lookup resolves a live session id or a client session id the playlist was
// served under.
func (t *liveManifestRefetches) lookup(key string) (string, liveManifestServed, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.served[key]; ok {
		return key, e, true
	}
	if id, ok := t.alias[key]; ok {
		e, ok := t.served[id]
		return id, e, ok
	}
	return "", liveManifestServed{}, false
}

type liveManifestRefetchKey struct{}

// touch slides the refetch window for a live session whose playlist an
// admitted refetch just read: the native player keeps refetching a live
// playlist for as long as the viewer watches.
func (t *liveManifestRefetches) touch(id string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.served[id]; ok {
		e.at = at
		t.served[id] = e
	}
}

func liveManifestRefetchFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(liveManifestRefetchKey{}).(string)
	return id, ok
}

const liveStartPlaylistPath = "/video/:/transcode/universal/start.m3u8"

// liveSessionFromPath is the live session id of a universal ?path=
// /livetv/sessions/{id} value.
func liveSessionFromPath(path string) (string, bool) {
	id, ok := strings.CutPrefix(path, "/livetv/sessions/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

// noteLiveManifestServed records that an authenticated start.m3u8 is
// receiving live session id's playlist, and from which client address.
func (s *Server) noteLiveManifestServed(r *http.Request, id string, pr *Request) {
	if r.URL.Path != liveStartPlaylistPath || pr == nil || pr.User == nil || pr.Capability || pr.Token == "" || !pr.RemoteIP.IsValid() {
		return
	}
	var aliases []string
	for _, a := range []string{r.URL.Query().Get("session"), pr.SessionID} {
		if a != "" && a != id {
			aliases = append(aliases, a)
		}
	}
	s.liveManifests.note(id, liveManifestServed{ip: pr.RemoteIP, at: s.now(), userID: pr.User.ID, clientID: pr.ClientID, aliases: aliases, allowRefetch: true}, s.now())
}

// noteLiveSessionAlias records the client session id used on authenticated
// universal Live TV requests (start.mpd, decision, HTTP start) so a subsequent
// authenticated /video/:/transcode/universal/stop?session=... stops the live
// session cleanly without admitting credential-less manifest refetches.
func (s *Server) noteLiveSessionAlias(r *http.Request, id string, pr *Request) {
	if pr == nil || pr.User == nil || pr.Capability || pr.Token == "" {
		return
	}
	var aliases []string
	for _, a := range []string{r.URL.Query().Get("session"), pr.SessionID} {
		if a != "" && a != id {
			aliases = append(aliases, a)
		}
	}
	s.liveManifests.note(id, liveManifestServed{ip: pr.RemoteIP, at: s.now(), userID: pr.User.ID, clientID: pr.ClientID, aliases: aliases}, s.now())
}

// authenticateLiveManifestRefetch admits a native player's credential-less
// refetch of a Live TV start.m3u8. Only a playlist an authenticated request
// received from the same client address within manifestRefetchWindow
// qualifies, and the request can only read that session's playlist, whose
// segment URLs carry its stream grant. It never tunes, changes or stops
// playback.
func (s *Server) authenticateLiveManifestRefetch(r *http.Request, pr *Request) (*http.Request, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return nil, false
	}
	if r.URL.Path != liveStartPlaylistPath || pr == nil || pr.Token != "" || !pr.RemoteIP.IsValid() {
		return nil, false
	}
	q := r.URL.Query()
	key, fromPath := liveSessionFromPath(q.Get("path"))
	if !fromPath {
		if q.Get("path") != "" {
			return nil, false
		}
		key = q.Get("session")
		if key == "" {
			key = pr.SessionID
		}
	}
	if key == "" {
		return nil, false
	}
	id, e, ok := s.liveManifests.lookup(key)
	if !ok || !e.allowRefetch || (fromPath && id != key) || e.ip != pr.RemoteIP {
		return nil, false
	}
	if age := s.now().Sub(e.at); age < 0 || age > manifestRefetchWindow {
		return nil, false
	}
	u := s.activeViewer(r.Context(), e.userID)
	if u == nil {
		return nil, false
	}
	if policy, ok := s.policy().(frontend.UserPolicySource); ok {
		u = policy.UserPolicy(u)
	}
	if policy, ok := s.policy().(frontend.PlaybackAccessPolicy); ok && !policy.CanPlay(u, pr.ClientCaps(0).Remote, s.now()) {
		return nil, false
	}
	pr.User = u
	pr.Capability = true
	if pr.ClientID == "" {
		pr.ClientID = e.clientID
	}
	s.liveManifests.touch(id, s.now())
	return r.WithContext(context.WithValue(r.Context(), liveManifestRefetchKey{}, id)), true
}

// serveLiveManifestRefetch answers an admitted refetch with the running live
// session's playlist only.
func (s *Server) serveLiveManifestRefetch(w http.ResponseWriter, r *http.Request, id string) {
	if !s.liveTVEnabled() {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	s.svc().liveTV.ServePlaylist(w, r, id, FromContext(r.Context()).User)
}
