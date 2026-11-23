package uploadbudget

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/plextv"
)

const (
	pmsPollInterval      = 15 * time.Second
	pmsRequestTimeout    = 3 * time.Second
	pmsDiscoveryCacheTTL = 15 * time.Minute
	pmsStaleGrace        = 2 * time.Minute
	pmsMaxResponseBytes  = 4 << 20
)

// ServerTarget identifies one already-linked Plex Media Server whose active
// remote sessions share this internet connection.
type ServerTarget struct {
	MachineID string
	Name      string
	BaseURLs  []string // tried in order (local non-relay first)
	Token     string
	Source    string // "linked_pms" or "plex_account"
}

// ExternalServerStatus reports the latest polled remote load on one linked PMS.
type ExternalServerStatus struct {
	MachineID     string    `json:"machineId,omitempty"`
	Name          string    `json:"name"`
	Source        string    `json:"source"`
	RemoteStreams int       `json:"remoteStreams"`
	BandwidthKbps int       `json:"bandwidthKbps"`
	Reachable     bool      `json:"reachable"`
	LastChecked   time.Time `json:"lastChecked,omitzero"`
}

// ExternalUsage aggregates remote bandwidth across all linked sibling PMS
// servers sharing this connection.
type ExternalUsage struct {
	TotalKbps     int
	RemoteStreams int
	Servers       []ExternalServerStatus
	UpdatedAt     time.Time
}

// PMSPollerOptions configures discovery and polling of already-linked PMS
// servers. No new URL/token setting is ever added: servers come solely from
// existing Plex Media Server links and/or the owner's Plex account resources.
type PMSPollerOptions struct {
	HTTP             *http.Client
	ClientID         string
	Now              func() time.Time
	LinkedPMS        func(context.Context) ([]ServerTarget, error)
	AccountResources func(context.Context) (resources []plextv.Resource, selfMachineID string, selfPublicIP string, err error)
}

type cachedServerReading struct {
	status      ExternalServerStatus
	lastOKAt    time.Time
	workingBase string
}

// PMSPoller polls /status/sessions on already-linked Plex Media Servers,
// strictly on demand when Ether itself has active remote streams.
type PMSPoller struct {
	opts PMSPollerOptions

	mu           sync.Mutex
	discovered   []ServerTarget
	discoveredAt time.Time
	readings     map[string]*cachedServerReading
	lastPollAt   time.Time
	pollCount    int64
}

// NewPMSPoller constructs a sibling PMS poller.
func NewPMSPoller(opts PMSPollerOptions) *PMSPoller {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: pmsRequestTimeout}
	}
	return &PMSPoller{
		opts:     opts,
		readings: map[string]*cachedServerReading{},
	}
}

// PollCount returns how many /status/sessions poll cycles ran (useful in tests
// to verify zero polling when Ether has no remote streams).
func (p *PMSPoller) PollCount() int64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pollCount
}

// ClearIdle resets live external usage when Ether has no active remote streams.
func (p *PMSPoller) ClearIdle() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.readings {
		r.status.RemoteStreams = 0
		r.status.BandwidthKbps = 0
	}
}

// Snapshot returns the current cached external PMS usage without network I/O.
func (p *PMSPoller) Snapshot() ExternalUsage {
	if p == nil {
		return ExternalUsage{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	var out ExternalUsage
	out.UpdatedAt = p.lastPollAt
	for _, r := range p.readings {
		st := r.status
		if !r.lastOKAt.IsZero() && now.Sub(r.lastOKAt) > pmsStaleGrace {
			st.RemoteStreams = 0
			st.BandwidthKbps = 0
			st.Reachable = false
		}
		out.TotalKbps += st.BandwidthKbps
		out.RemoteStreams += st.RemoteStreams
		out.Servers = append(out.Servers, st)
	}
	return out
}

// PollIfDue queries linked PMS servers if at least pmsPollInterval has elapsed
// (or force is true). Callers must only invoke this when Ether itself has at
// least one active remote stream.
func (p *PMSPoller) PollIfDue(ctx context.Context, force bool) ExternalUsage {
	if p == nil {
		return ExternalUsage{}
	}
	now := p.opts.Now()
	p.mu.Lock()
	if !force && !p.lastPollAt.IsZero() && now.Sub(p.lastPollAt) < pmsPollInterval {
		p.mu.Unlock()
		return p.Snapshot()
	}
	p.lastPollAt = now
	p.mu.Unlock()

	targets := p.resolveTargets(ctx, now)
	if len(targets) == 0 {
		return p.Snapshot()
	}

	p.mu.Lock()
	p.pollCount++
	p.mu.Unlock()

	activeKeys := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		key := targetKey(target)
		activeKeys[key] = struct{}{}
		p.pollOne(ctx, key, target, now)
	}

	p.mu.Lock()
	for k := range p.readings {
		if _, ok := activeKeys[k]; !ok {
			delete(p.readings, k)
		}
	}
	p.mu.Unlock()

	return p.Snapshot()
}

func (p *PMSPoller) resolveTargets(ctx context.Context, now time.Time) []ServerTarget {
	byID := map[string]ServerTarget{}
	var order []string
	add := func(t ServerTarget) {
		if t.Token == "" || len(t.BaseURLs) == 0 {
			return
		}
		key := targetKey(t)
		if _, exists := byID[key]; exists {
			return
		}
		byID[key] = t
		order = append(order, key)
	}

	// 1. Existing linked PMS servers (e.g. Step Aside / watch sync) take
	// priority because they already store the owner's preferred URL and token.
	if p.opts.LinkedPMS != nil {
		if linked, err := p.opts.LinkedPMS(ctx); err == nil {
			for _, t := range linked {
				if t.Source == "" {
					t.Source = "linked_pms"
				}
				add(t)
			}
		}
	}

	// 2. Sibling owned servers on the owner's Plex account (cached 15 min).
	if p.opts.AccountResources != nil {
		p.mu.Lock()
		cached := p.discovered
		fresh := !p.discoveredAt.IsZero() && now.Sub(p.discoveredAt) < pmsDiscoveryCacheTTL
		p.mu.Unlock()

		if !fresh {
			dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			resources, selfID, selfPublicIP, err := p.opts.AccountResources(dctx)
			cancel()
			if err == nil {
				cached = siblingTargetsFromResources(resources, selfID, selfPublicIP)
				p.mu.Lock()
				p.discovered = cached
				p.discoveredAt = now
				p.mu.Unlock()
			}
		}
		for _, t := range cached {
			add(t)
		}
	}

	out := make([]ServerTarget, 0, len(order))
	for _, k := range order {
		out = append(out, byID[k])
	}
	return out
}

func targetKey(t ServerTarget) string {
	if id := strings.ToLower(strings.TrimSpace(t.MachineID)); id != "" {
		return id
	}
	if len(t.BaseURLs) > 0 {
		return strings.ToLower(strings.TrimSpace(t.BaseURLs[0]))
	}
	return strings.ToLower(strings.TrimSpace(t.Name))
}

// siblingTargetsFromResources picks owned Plex Media Servers on the same site
// / internet line as Ether, excluding Ether itself.
func siblingTargetsFromResources(resources []plextv.Resource, selfMachineID, selfPublicIP string) []ServerTarget {
	selfID := strings.ToLower(strings.TrimSpace(selfMachineID))
	selfPub := strings.TrimSpace(selfPublicIP)
	var out []ServerTarget
	for _, r := range resources {
		if !r.ProvidesServer() || !r.Owned || strings.TrimSpace(r.AccessToken) == "" {
			continue
		}
		if selfID != "" && strings.ToLower(strings.TrimSpace(r.ClientIdentifier)) == selfID {
			continue
		}
		sameWAN := selfPub != "" && strings.TrimSpace(r.PublicAddress) == selfPub
		var localURLs, remoteURLs []string
		for _, c := range r.Connections {
			if c.Relay {
				continue
			}
			u := connectionBaseURL(c)
			if u == "" {
				continue
			}
			if c.Local {
				localURLs = append(localURLs, u)
			} else if sameWAN || r.PublicAddressMatches {
				remoteURLs = append(remoteURLs, u)
			}
		}
		// Only count sibling servers reachable locally on the same LAN or
		// sharing Ether's public WAN IP so an off-site cloud PMS does not
		// consume this site's upload budget.
		if !sameWAN && !r.PublicAddressMatches && len(localURLs) == 0 {
			continue
		}
		urls := append(localURLs, remoteURLs...)
		if len(urls) == 0 {
			continue
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = "Plex Media Server"
		}
		out = append(out, ServerTarget{
			MachineID: r.ClientIdentifier,
			Name:      name,
			BaseURLs:  dedupeStrings(urls),
			Token:     r.AccessToken,
			Source:    "plex_account",
		})
	}
	return out
}

func connectionBaseURL(c plextv.ResourceConnection) string {
	if raw := strings.TrimSpace(c.URI); raw != "" {
		if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/")
		}
	}
	addr := strings.TrimSpace(c.Address)
	if addr == "" || c.Port <= 0 {
		return ""
	}
	scheme := strings.TrimSpace(c.Protocol)
	if scheme != "http" && scheme != "https" {
		scheme = "http"
	}
	if ip, err := netip.ParseAddr(addr); err == nil && ip.Is6() {
		return fmt.Sprintf("%s://[%s]:%d", scheme, addr, c.Port)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, addr, c.Port)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func (p *PMSPoller) pollOne(ctx context.Context, key string, target ServerTarget, now time.Time) {
	p.mu.Lock()
	prev := p.readings[key]
	working := ""
	if prev != nil {
		working = prev.workingBase
	}
	p.mu.Unlock()

	urls := target.BaseURLs
	if working != "" {
		ordered := make([]string, 0, len(urls))
		ordered = append(ordered, working)
		for _, u := range urls {
			if u != working {
				ordered = append(ordered, u)
			}
		}
		urls = ordered
	}

	var remoteStreams, bandwidthKbps int
	var succeededBase string
	var lastErr error
	for _, base := range urls {
		rctx, cancel := context.WithTimeout(ctx, pmsRequestTimeout)
		streams, kbps, err := p.fetchSessions(rctx, base, target.Token)
		cancel()
		if err == nil {
			remoteStreams, bandwidthKbps, succeededBase = streams, kbps, base
			lastErr = nil
			break
		}
		lastErr = err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.readings[key]
	if rec == nil {
		rec = &cachedServerReading{}
		p.readings[key] = rec
	}
	rec.status.MachineID = target.MachineID
	rec.status.Name = target.Name
	rec.status.Source = target.Source
	rec.status.LastChecked = now
	if lastErr == nil {
		rec.status.RemoteStreams = remoteStreams
		rec.status.BandwidthKbps = bandwidthKbps
		rec.status.Reachable = true
		rec.lastOKAt = now
		rec.workingBase = succeededBase
		return
	}
	// Keep the last valid reading during a brief transient hiccup, then drop
	// to zero after pmsStaleGrace so an offline PMS never starves Ether.
	rec.status.Reachable = false
	if rec.lastOKAt.IsZero() || now.Sub(rec.lastOKAt) > pmsStaleGrace {
		rec.status.RemoteStreams = 0
		rec.status.BandwidthKbps = 0
	}
}

func (p *PMSPoller) fetchSessions(ctx context.Context, baseURL, token string) (remoteStreams, bandwidthKbps int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/status/sessions", nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", token)
	if p.opts.ClientID != "" {
		req.Header.Set("X-Plex-Client-Identifier", p.opts.ClientID)
	}
	resp, err := p.opts.HTTP.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return 0, 0, fmt.Errorf("pms status/sessions: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, pmsMaxResponseBytes))
	if err != nil {
		return 0, 0, err
	}
	return parsePMSSessionsJSON(body)
}

type pmsFlexInt int

func (f *pmsFlexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		*f = pmsFlexInt(n)
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = pmsFlexInt(v)
		return nil
	}
	return nil
}

type pmsFlexBool bool

func (b *pmsFlexBool) UnmarshalJSON(raw []byte) error {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(string(raw)), `"`))
	*b = s == "true" || s == "1"
	return nil
}

type pmsSessionObj struct {
	Bandwidth pmsFlexInt `json:"bandwidth"`
	Location  string     `json:"location"`
}

type pmsPlayerObj struct {
	Local pmsFlexBool `json:"local"`
	State string      `json:"state"`
}

// decodeFirstObject handles Plex JSON fields that vary between a single object
// and a single-element array across PMS versions.
func decodeFirstObject[T any](raw json.RawMessage) (T, bool) {
	var zero T
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return zero, false
	}
	if strings.HasPrefix(s, "[") {
		var list []T
		if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
			return list[0], true
		}
		return zero, false
	}
	var one T
	if err := json.Unmarshal(raw, &one); err == nil {
		return one, true
	}
	return zero, false
}

// parsePMSSessionsJSON counts active WAN sessions and their aggregate kbps
// from a real Plex Media Server /status/sessions JSON payload.
func parsePMSSessionsJSON(body []byte) (remoteStreams, bandwidthKbps int, err error) {
	var env struct {
		MediaContainer struct {
			Metadata []struct {
				Session json.RawMessage `json:"Session"`
				Player  json.RawMessage `json:"Player"`
				Media   []struct {
					Bitrate pmsFlexInt `json:"bitrate"`
				} `json:"Media"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return 0, 0, err
	}
	for _, md := range env.MediaContainer.Metadata {
		sess, hasSess := decodeFirstObject[pmsSessionObj](md.Session)
		player, hasPlayer := decodeFirstObject[pmsPlayerObj](md.Player)
		if hasPlayer && strings.EqualFold(strings.TrimSpace(player.State), "stopped") {
			continue
		}
		loc := strings.ToLower(strings.TrimSpace(sess.Location))
		isRemote := false
		switch loc {
		case "wan":
			isRemote = true
		case "lan":
			isRemote = false
		default:
			if hasPlayer {
				isRemote = !bool(player.Local)
			}
		}
		if !isRemote {
			continue
		}
		bw := 0
		if hasSess && int(sess.Bandwidth) > 0 {
			bw = int(sess.Bandwidth)
		}
		if bw == 0 && len(md.Media) > 0 && int(md.Media[0].Bitrate) > 0 {
			bw = int(md.Media[0].Bitrate)
		}
		remoteStreams++
		bandwidthKbps += bw
	}
	return remoteStreams, bandwidthKbps, nil
}
