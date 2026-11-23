package plex

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// nativeDeviceID derives a stable, synthetic device identity for a clientless
// media read. Native players (Samsung AVPlay, libmpv) may drop Plex headers on
// media URLs and carry only a token, so the request has no X-Plex-Client-Identifier.
// Such a read must still open an accounted session: the identity is keyed on
// the viewer, the credential and the network peer, so repeated range reads from
// the same player reuse one session instead of minting a new one per request,
// while a different viewer, token or address stays distinct.
func nativeDeviceID(viewer int64, token string, remoteIP string) string {
	sum := sha256.Sum256([]byte(token + "\x1f" + remoteIP + "\x1f" + strconv.FormatInt(viewer, 10)))
	return "native-" + hex.EncodeToString(sum[:8])
}

// nativePlatform names the platform of a clientless read from its User-Agent,
// so the session and status surfaces do not show an empty platform.
func nativePlatform(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "tizen"):
		return "Tizen"
	case strings.Contains(ua, "webos"):
		return "webOS"
	case strings.Contains(ua, "android"):
		return "Android"
	case strings.Contains(ua, "apple tv"), strings.Contains(ua, "tvos"):
		return "tvOS"
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"), strings.Contains(ua, "apple"):
		return "iOS"
	case strings.Contains(ua, "roku"):
		return "Roku"
	case ua == "":
		return ""
	}
	// The first product token is a coarse but honest fallback.
	first := userAgent
	if i := strings.IndexAny(first, " /"); i > 0 {
		first = first[:i]
	}
	if len(first) > 40 {
		first = first[:40]
	}
	return first
}

// adoptNativeDevice gives a clientless read a synthetic device identity so it
// can prepare and publish through the broker like any other client.
func (pr *Request) adoptNativeDevice(r *http.Request) {
	if pr.ClientID == "" {
		pr.ClientID = nativeDeviceID(pr.User.ID, pr.Token, pr.RemoteIP.String())
	}
	if pr.Product == "" {
		if strings.Contains(strings.ToLower(r.UserAgent()), "plezy") {
			pr.Product = "Plezy"
		} else {
			pr.Product = "Native player"
		}
	}
	if pr.Platform == "" {
		pr.Platform = nativePlatform(r.UserAgent())
	}
}

const (
	recentPartClientTTL = 15 * time.Minute
	recentPartTitleTTL  = 2 * time.Minute
	recentPartClientCap = 256
)

type recentClientProfile struct {
	clientID           string
	product            string
	version            string
	platform           string
	platformVersion    string
	device             string
	deviceName         string
	model              string
	vendor             string
	profileName        string
	profileExtra       string
	clientCapabilities string
	seenAt             time.Time
	ambiguousUntil     time.Time
}

func (p recentClientProfile) sameDevice(o recentClientProfile) bool {
	if p.clientID != "" && o.clientID != "" {
		return p.clientID == o.clientID
	}
	return strings.EqualFold(p.product, o.product) &&
		strings.EqualFold(p.platform, o.platform) &&
		strings.EqualFold(p.device, o.device)
}

func (p recentClientProfile) isAmbiguous(now time.Time) bool {
	return !p.ambiguousUntil.IsZero() && now.Before(p.ambiguousUntil)
}

func uaPlatformFamily(ua string) string {
	s := strings.ToLower(ua)
	switch {
	case strings.Contains(s, "tizen"):
		return "tizen"
	case strings.Contains(s, "webos"):
		return "webos"
	case strings.Contains(s, "roku"):
		return "roku"
	case strings.Contains(s, "android"):
		return "android"
	case strings.Contains(s, "applecoremedia"),
		strings.Contains(s, "cfnetwork"),
		strings.Contains(s, "apple tv"),
		strings.Contains(s, "tvos"),
		strings.Contains(s, "iphone"),
		strings.Contains(s, "ipad"),
		strings.Contains(s, "darwin"),
		strings.Contains(s, "mac os x"),
		strings.Contains(s, "macos"):
		return "apple"
	case strings.Contains(s, "windows"), strings.Contains(s, "win64"), strings.Contains(s, "win32"):
		return "windows"
	default:
		return ""
	}
}

func profilePlatformFamily(p recentClientProfile) string {
	s := strings.ToLower(p.platform + " " + p.device + " " + p.product)
	switch {
	case strings.Contains(s, "tizen"), strings.Contains(s, "samsung"):
		return "tizen"
	case strings.Contains(s, "webos"):
		return "webos"
	case strings.Contains(s, "roku"):
		return "roku"
	case strings.Contains(s, "android"):
		return "android"
	case strings.Contains(s, "tvos"),
		strings.Contains(s, "ios"),
		strings.Contains(s, "apple tv"),
		strings.Contains(s, "iphone"),
		strings.Contains(s, "ipad"),
		strings.Contains(s, "macos"),
		strings.Contains(s, "darwin"),
		strings.Contains(s, "osx"),
		strings.Contains(s, "mac "):
		return "apple"
	case strings.Contains(s, "windows"):
		return "windows"
	case strings.Contains(s, "linux"):
		return "linux"
	default:
		return ""
	}
}

func uaCompatibleWithProfile(ua string, p recentClientProfile) bool {
	uf := uaPlatformFamily(ua)
	if uf == "" {
		return true
	}
	pf := profilePlatformFamily(p)
	if pf == "" {
		return true
	}
	return uf == pf
}

type recentClientKey struct {
	userID   int64
	clientID string
}

type recentPeerKey struct {
	userID   int64
	remoteIP string
}

type recentTitlePeerKey struct {
	userID   int64
	remoteIP string
	key      frontend.RatingKey
}

type recentTitleKey struct {
	userID int64
	key    frontend.RatingKey
}

// recentPartClients remembers the client capability headers sent on recent
// authenticated control/metadata requests so a subsequent headerless direct-play
// /library/parts read from a native player (such as Plezy's libmpv loader when
// X-Plex-* headers are dropped) inherits the real client's playback capabilities
// instead of collapsing to the 1080p H.264 SDR "generic" fallback.
type recentPartClients struct {
	mu          sync.Mutex
	byClient    map[recentClientKey]recentClientProfile
	byPeer      map[recentPeerKey]recentClientProfile
	byTitlePeer map[recentTitlePeerKey]recentClientProfile
	byTitle     map[recentTitleKey]recentClientProfile
}

func recentProfileFrom(r *http.Request, pr *Request, now time.Time) (recentClientProfile, bool) {
	if pr == nil || pr.User == nil || pr.Capability || libraryToolClient(pr) {
		return recentClientProfile{}, false
	}
	product := strings.TrimSpace(pr.Product)
	if product == "" || product == "Native player" {
		return recentClientProfile{}, false
	}
	extra := pr.ProfileExtra
	platform := pr.Platform
	if r != nil && strings.HasPrefix(r.URL.Path, "/video/:/transcode/") {
		// Transcode decisions may attach per-session bitrate or output target
		// limitations (e.g. Plezy's non-Original presets) and query X-Plex-Platform=Generic;
		// never carry those onto a subsequent Original direct-play /library/parts read.
		extra = ""
		if strings.EqualFold(platform, "Generic") && r.Header.Get("X-Plex-Platform") == "" {
			platform = ""
		}
	}
	return recentClientProfile{
		clientID:           pr.ClientID,
		product:            product,
		version:            pr.Version,
		platform:           platform,
		platformVersion:    pr.PlatformVersion,
		device:             pr.Device,
		deviceName:         pr.DeviceName,
		model:              pr.Model,
		vendor:             pr.Vendor,
		profileName:        pr.ProfileName,
		profileExtra:       extra,
		clientCapabilities: pr.ClientCapabilities,
		seenAt:             now,
	}, true
}

func mergeRecentEntry(existing recentClientProfile, hasExisting bool, incoming recentClientProfile, ttl time.Duration, checkConflict bool) recentClientProfile {
	if hasExisting && !incoming.seenAt.IsZero() && !existing.seenAt.IsZero() && incoming.seenAt.Sub(existing.seenAt) <= ttl {
		if incoming.platform == "" && strings.EqualFold(existing.product, incoming.product) && existing.sameDevice(incoming) {
			incoming.platform = existing.platform
		}
		if incoming.profileExtra == "" && existing.profileExtra != "" && existing.sameDevice(incoming) {
			incoming.profileExtra = existing.profileExtra
		}
		if checkConflict {
			if existing.isAmbiguous(incoming.seenAt) {
				incoming.ambiguousUntil = existing.ambiguousUntil
			}
			if !existing.sameDevice(incoming) {
				until := incoming.seenAt.Add(ttl)
				if until.After(incoming.ambiguousUntil) {
					incoming.ambiguousUntil = until
				}
			}
		}
	}
	return incoming
}

func (c *recentPartClients) ensureLocked(now time.Time) {
	if c.byClient == nil {
		c.byClient = make(map[recentClientKey]recentClientProfile)
		c.byPeer = make(map[recentPeerKey]recentClientProfile)
		c.byTitlePeer = make(map[recentTitlePeerKey]recentClientProfile)
		c.byTitle = make(map[recentTitleKey]recentClientProfile)
		return
	}
	if len(c.byClient) >= recentPartClientCap || len(c.byPeer) >= recentPartClientCap || len(c.byTitlePeer) >= recentPartClientCap || len(c.byTitle) >= recentPartClientCap {
		c.pruneLocked(now)
	}
}

func (c *recentPartClients) pruneLocked(now time.Time) {
	for k, v := range c.byClient {
		if now.Sub(v.seenAt) > recentPartClientTTL {
			delete(c.byClient, k)
		}
	}
	for k, v := range c.byPeer {
		if now.Sub(v.seenAt) > recentPartClientTTL {
			delete(c.byPeer, k)
		}
	}
	for k, v := range c.byTitlePeer {
		if now.Sub(v.seenAt) > recentPartClientTTL {
			delete(c.byTitlePeer, k)
		}
	}
	for k, v := range c.byTitle {
		if now.Sub(v.seenAt) > recentPartTitleTTL {
			delete(c.byTitle, k)
		}
	}
	if len(c.byClient) >= recentPartClientCap {
		clear(c.byClient)
	}
	if len(c.byPeer) >= recentPartClientCap {
		clear(c.byPeer)
	}
	if len(c.byTitlePeer) >= recentPartClientCap {
		clear(c.byTitlePeer)
	}
	if len(c.byTitle) >= recentPartClientCap {
		clear(c.byTitle)
	}
}

func (c *recentPartClients) recordRequest(r *http.Request, pr *Request, now time.Time) {
	if c == nil {
		return
	}
	prof, ok := recentProfileFrom(r, pr, now)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked(now)
	if pr.ClientID != "" {
		ck := recentClientKey{userID: pr.User.ID, clientID: pr.ClientID}
		old, has := c.byClient[ck]
		c.byClient[ck] = mergeRecentEntry(old, has, prof, recentPartClientTTL, false)
	}
	if pr.RemoteIP.IsValid() {
		pk := recentPeerKey{userID: pr.User.ID, remoteIP: pr.RemoteIP.String()}
		old, has := c.byPeer[pk]
		c.byPeer[pk] = mergeRecentEntry(old, has, prof, recentPartClientTTL, true)
	}
}

func (c *recentPartClients) recordTitle(r *http.Request, pr *Request, key frontend.RatingKey, now time.Time) {
	if c == nil || key == 0 {
		return
	}
	prof, ok := recentProfileFrom(r, pr, now)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureLocked(now)
	if pr.RemoteIP.IsValid() {
		tpk := recentTitlePeerKey{userID: pr.User.ID, remoteIP: pr.RemoteIP.String(), key: key}
		old, has := c.byTitlePeer[tpk]
		c.byTitlePeer[tpk] = mergeRecentEntry(old, has, prof, recentPartClientTTL, true)
	}
	tk := recentTitleKey{userID: pr.User.ID, key: key}
	old, has := c.byTitle[tk]
	c.byTitle[tk] = mergeRecentEntry(old, has, prof, recentPartTitleTTL, true)
}

func (c *recentPartClients) lookupLocked(r *http.Request, pr *Request, key frontend.RatingKey, now time.Time) (recentClientProfile, bool) {
	if pr.ClientID != "" {
		if c.byClient != nil {
			if prof, ok := c.byClient[recentClientKey{userID: pr.User.ID, clientID: pr.ClientID}]; ok && now.Sub(prof.seenAt) <= recentPartClientTTL {
				return prof, true
			}
		}
		// Never cross-pollinate an explicit client identifier with another
		// device that happens to share an IP or title.
		return recentClientProfile{}, false
	}
	if r == nil {
		return recentClientProfile{}, false
	}
	ua := strings.TrimSpace(r.UserAgent())
	if ua == "" {
		return recentClientProfile{}, false
	}
	if key != 0 && pr.RemoteIP.IsValid() && c.byTitlePeer != nil {
		if prof, ok := c.byTitlePeer[recentTitlePeerKey{userID: pr.User.ID, remoteIP: pr.RemoteIP.String(), key: key}]; ok && !prof.isAmbiguous(now) && now.Sub(prof.seenAt) <= recentPartClientTTL && uaCompatibleWithProfile(ua, prof) {
			return prof, true
		}
	}
	if key != 0 && c.byTitle != nil {
		if prof, ok := c.byTitle[recentTitleKey{userID: pr.User.ID, key: key}]; ok && !prof.isAmbiguous(now) && now.Sub(prof.seenAt) <= recentPartTitleTTL && uaCompatibleWithProfile(ua, prof) {
			return prof, true
		}
	}
	if pr.RemoteIP.IsValid() && c.byPeer != nil {
		if prof, ok := c.byPeer[recentPeerKey{userID: pr.User.ID, remoteIP: pr.RemoteIP.String()}]; ok && !prof.isAmbiguous(now) && now.Sub(prof.seenAt) <= recentPartClientTTL && uaCompatibleWithProfile(ua, prof) {
			return prof, true
		}
	}
	return recentClientProfile{}, false
}

// apply populates omitted client capability fields on a /library/parts request
// from the viewer's recent pre-play metadata or control request, while leaving
// pr.ClientID and pr.wireIdentity() untouched so clientless session accounting
// and support diagnostics preserve their exact contracts.
func (c *recentPartClients) apply(r *http.Request, pr *Request, key frontend.RatingKey, now time.Time) bool {
	if c == nil || pr == nil || pr.User == nil || pr.Product != "" {
		return false
	}
	c.mu.Lock()
	prof, ok := c.lookupLocked(r, pr, key, now)
	c.mu.Unlock()
	if !ok {
		return false
	}
	pr.Product = cmp.Or(pr.Product, prof.product)
	pr.Version = cmp.Or(pr.Version, prof.version)
	pr.Platform = cmp.Or(pr.Platform, prof.platform)
	pr.PlatformVersion = cmp.Or(pr.PlatformVersion, prof.platformVersion)
	pr.Device = cmp.Or(pr.Device, prof.device)
	pr.DeviceName = cmp.Or(pr.DeviceName, prof.deviceName)
	pr.Model = cmp.Or(pr.Model, prof.model)
	pr.Vendor = cmp.Or(pr.Vendor, prof.vendor)
	pr.ProfileName = cmp.Or(pr.ProfileName, prof.profileName)
	pr.ProfileExtra = cmp.Or(pr.ProfileExtra, prof.profileExtra)
	pr.ClientCapabilities = cmp.Or(pr.ClientCapabilities, prof.clientCapabilities)
	return true
}
