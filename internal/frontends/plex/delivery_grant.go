package plex

// Delivery grants (plex.delivery = "redirect"): the credential-free first hop.
//
// A redirected read names Ether's URL to the provider as its referrer, and for
// the players this feature exists for — native loaders that carry only a token
// on the URL — that URL is where the viewer's Plex token is written. Handing it
// straight to the provider would therefore put a standing server credential in a
// third party's request log. So a URL-authenticated read is redirected once to
// itself with an opaque grant in place of that token, and only the second hop
// leaves for the provider: what the edge can learn is a grant that is already
// spent.
//
// The grant is deliberately weak. It is single-use, it expires long before a
// playback would, it is bound to the file it was minted for, and it authorises
// nothing a viewer could not fetch a moment later with its own credential.

import (
	"crypto/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// deliveryGrantParam carries the minted delivery grant. Like the read
// capability, it must not appear in status, notifications or request logs.
const deliveryGrantParam = "X-Ether-Delivery-Token"

const (
	// deliveryGrantTTL bounds how long an unanswered first hop stays valid. A
	// player follows the redirect at once; the window only has to survive a
	// slow client start.
	deliveryGrantTTL = time.Minute
	// deliveryGrantMax bounds the store: a client that never follows the hop
	// leaves its grant behind, and abandoned hops must not grow without limit.
	deliveryGrantMax = 256
)

type deliveryGrant struct {
	userID  int64
	target  string // exact path and query issued in the first redirect
	expires time.Time
	seq     uint64
}

type deliveryGrantStore struct {
	mu      sync.Mutex
	byToken map[string]deliveryGrant
	seq     uint64
	now     func() time.Time
}

func newDeliveryGrantStore() *deliveryGrantStore {
	return &deliveryGrantStore{byToken: map[string]deliveryGrant{}, now: time.Now}
}

// mint issues a one-shot grant for one viewer and one part route. The client
// identifier is deliberately not part of the binding: the loaders this hop
// exists for send no client header, and a clientless part read adopts its
// synthetic identity per read, so the two hops need not name themselves
// identically.
func (g *deliveryGrantStore) mint(userID int64, r *http.Request) string {
	if g == nil || userID == 0 || r.URL.Path == "" {
		return ""
	}
	token := rand.Text()
	target := grantLocation(r, token)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.byToken == nil {
		g.byToken = map[string]deliveryGrant{}
	}
	now := g.now()
	for key, grant := range g.byToken {
		if !grant.expires.After(now) {
			delete(g.byToken, key)
		}
	}
	for len(g.byToken) >= deliveryGrantMax {
		oldest, oldestSeq := "", uint64(0)
		for key, grant := range g.byToken {
			if oldest == "" || grant.seq < oldestSeq {
				oldest, oldestSeq = key, grant.seq
			}
		}
		delete(g.byToken, oldest)
	}
	g.seq++
	g.byToken[token] = deliveryGrant{userID: userID, target: target, expires: now.Add(deliveryGrantTTL), seq: g.seq}
	return target
}

// take consumes a grant, reporting the viewer it was minted for when it was a
// live grant for this exact file. A spent, expired or misplaced token is not a
// credential: the request that repeats a spent hop has to come back with its
// own, and mints a fresh grant.
func (g *deliveryGrantStore) take(token, target string) (int64, bool) {
	if g == nil || token == "" {
		return 0, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	grant, ok := g.byToken[token]
	if !ok {
		return 0, false
	}
	delete(g.byToken, token)
	if grant.target != target || !grant.expires.After(g.now()) {
		return 0, false
	}
	return grant.userID, true
}

// authenticateDeliveryGrant authorises the second hop of a redirected part read
// by the grant Ether minted for it. The grant is consumed here, before the
// handler can answer with the provider's URL, so a referrer that repeats it
// discloses only a spent value.
func (s *Server) authenticateDeliveryGrant(r *http.Request, pr *Request) bool {
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/library/parts/") {
		return false
	}
	token := r.URL.Query().Get(deliveryGrantParam)
	if token == "" {
		return false
	}
	// A grant is the second hop's only credential. A request that also carries an
	// account credential is mixed: the account authorises the read, and the URL
	// the player is about to name to the provider would carry that credential
	// again — which is the disclosure this hop exists to prevent.
	if pr.Token != "" || requestHeaderCredential(r) {
		return false
	}
	userID, ok := s.deliveryGrants.take(token, r.URL.RequestURI())
	if !ok {
		return false
	}
	u := s.activeViewer(r.Context(), userID)
	if u == nil {
		return false
	}
	if policy, ok := s.policy().(frontend.UserPolicySource); ok {
		u = policy.UserPolicy(u)
	}
	if policy, ok := s.policy().(frontend.PlaybackAccessPolicy); ok && !policy.CanPlay(u, pr.ClientCaps(0).Remote, s.now()) {
		return false
	}
	pr.User = u
	pr.Capability = true
	pr.deliveryGrantHop = true
	return true
}

// requestHeaderCredential reports whether the request carries a Plex (or bearer)
// credential in a header rather than on its URL. Only the client knows whether it
// forwards such headers across origins, and the provider's edge is somebody
// else's host, so a read with a header credential keeps relaying — and no
// external redirect may answer it.
func requestHeaderCredential(r *http.Request) bool {
	return len(r.Header.Values("X-Plex-Token")) != 0 || len(r.Header.Values("Authorization")) != 0
}

// requestQueryCredential reports whether the URL carries the viewer's Plex
// credential under any of the spellings clients use.
func requestQueryCredential(q url.Values) bool {
	for key := range q {
		if strings.EqualFold(key, "X-Plex-Token") || strings.EqualFold(key, "Authorization") {
			return true
		}
	}
	return false
}

// plexRoutingIdentity is the one incoming query parameter a grant hop carries
// forward: it names the client without being a secret, and a part read that lost
// it would arrive as a different device.
const plexRoutingIdentity = "X-Plex-Client-Identifier"

// grantLocation builds the second hop from the path the client asked for plus its
// fresh grant. Everything else in the incoming query is left behind, every
// spelling of it: the URL built here is the URL the player will name to the
// provider as its referrer.
func grantLocation(r *http.Request, token string) string {
	query := r.URL.Query()
	forward := url.Values{}
	for _, value := range query[plexRoutingIdentity] {
		forward.Add(plexRoutingIdentity, value)
	}
	forward.Set(deliveryGrantParam, token)
	hop := url.URL{Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: forward.Encode()}
	return hop.String()
}
