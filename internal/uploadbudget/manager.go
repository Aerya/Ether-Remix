package uploadbudget

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	// CapSourceUploadBudget marks a stream whose bitrate cap was chosen by the
	// shared remote upload budget.
	CapSourceUploadBudget = "upload_budget"

	reconcileTick = 5 * time.Second
)

// Options wires the upload budget coordinator into Ether's live runtime.
type Options struct {
	Logger *slog.Logger
	Now    func() time.Time

	// OverrideKbps reads transcode.remoteUploadBudgetKbps (0 = auto).
	OverrideKbps func() int
	// VideoTranscodingOff reports whether movies/shows forbid video encoding.
	VideoTranscodingOff func() bool

	// ActiveSessions snapshots currently active Ether VOD + Live TV sessions.
	ActiveSessions func() []*core.PlaySession
	// AdaptiveRungs returns a running HLS session's ladder rung bitrates (kbps,
	// high->low) and current budget cap, or ok=false when not an adaptive HLS
	// session.
	AdaptiveInfo func(sessionID string) (rungsKbps []int, currentCapKbps int, adaptive bool)
	// ApplySessionCap updates a running adaptive HLS remote stream's dynamic
	// upload budget cap (0 clears the cap to step back up).
	ApplySessionCap func(sessionID string, capKbps int) (applied bool, rungID string)

	// Poller optionally queries already-linked sibling PMS servers while Ether
	// has active remote streams.
	Poller *PMSPoller
}

// StatusSnapshot is the live upload budget status exposed to the owner console.
type StatusSnapshot struct {
	Enabled             bool                   `json:"enabled"`
	Source              string                 `json:"source"` // "override" | "measured" | "learning"
	OverrideKbps        int                    `json:"overrideKbps"`
	MeasuredKbps        int                    `json:"measuredKbps"`
	MeasuredSamples     int                    `json:"measuredSamples"`
	EffectiveKbps       int                    `json:"effectiveKbps"`
	UsableKbps          int                    `json:"usableKbps"`
	EtherRemoteKbps     int                    `json:"etherRemoteKbps"`
	EtherRemoteStreams  int                    `json:"etherRemoteStreams"`
	BudgetCappedStreams int                    `json:"budgetCappedStreams"`
	OtherPMSKbps        int                    `json:"otherPmsKbps"`
	OtherPMSStreams     int                    `json:"otherPmsStreams"`
	TotalUsedKbps       int                    `json:"totalUsedKbps"`
	OtherServers        []ExternalServerStatus `json:"otherServers,omitempty"`
}

type sessionCapRecord struct {
	capKbps    int
	fromBudget bool
	updatedAt  time.Time
}

// Manager coordinates passive upload capacity estimation, dynamic remote
// stream admission caps, adaptive HLS rung stepping with hysteresis, and
// conditional sibling PMS bandwidth polling.
type Manager struct {
	opts       Options
	log        *slog.Logger
	now        func() time.Time
	estimator  *Estimator
	hysteresis *HysteresisController
	poller     *PMSPoller

	mu            sync.Mutex
	capped        map[string]sessionCapRecord
	lastBufEvents map[string]int

	wake chan struct{}
}

// NewManager creates an upload budget coordinator. Call Run in a background
// goroutine to keep sibling PMS readings and adaptive rungs reconciled while
// remote streams are active.
func NewManager(opts Options) *Manager {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Manager{
		opts:          opts,
		log:           opts.Logger,
		now:           opts.Now,
		estimator:     NewEstimator(opts.Now),
		hysteresis:    NewHysteresisController(),
		poller:        opts.Poller,
		capped:        map[string]sessionCapRecord{},
		lastBufEvents: map[string]int{},
		wake:          make(chan struct{}, 1),
	}
}

// EffectiveCapacity returns the active upload capacity in kbps and its source
// ("override", "measured", or "learning"). Override always wins when > 0.
func (m *Manager) EffectiveCapacity() (capacityKbps int, source string, overrideKbps int, measuredKbps int, samples int) {
	if m == nil {
		return 0, "learning", 0, 0, 0
	}
	if m.opts.OverrideKbps != nil {
		overrideKbps = max(0, m.opts.OverrideKbps())
	}
	measuredKbps, samples = m.estimator.Estimate()
	if measuredKbps > 0 {
		if smoothKbps := m.activeSmoothRemoteKbps(); smoothKbps > 0 && UsableBudgetKbps(measuredKbps) < smoothKbps {
			measuredKbps = minCapacityForUsable(smoothKbps)
		}
	}
	if overrideKbps > 0 {
		return overrideKbps, "override", overrideKbps, measuredKbps, samples
	}
	if measuredKbps > 0 {
		return measuredKbps, "measured", 0, measuredKbps, samples
	}
	return 0, "learning", 0, 0, samples
}

// activeSmoothRemoteKbps sums the current playing bitrates of remote streams
// that are demonstrably playing smoothly right now (without buffering), so an
// automatic learned budget is never lowered below them.
func (m *Manager) activeSmoothRemoteKbps() int {
	if m == nil || m.opts.ActiveSessions == nil {
		return 0
	}
	now := m.now()
	total := 0
	for _, ps := range m.opts.ActiveSessions() {
		if !core.SessionActiveStream(ps) || !ps.Client.Remote {
			continue
		}
		if m.estimator.IsSessionStarved(ps.ID, now) {
			continue
		}
		if ps.Experience != nil && ps.Experience.BufferEvents > 0 {
			continue
		}
		smooth := m.estimator.IsSessionSmooth(ps.ID, now)
		if !smooth && ps.Experience != nil && ps.Experience.Pictured && !ps.Experience.PicturedAt.IsZero() && now.Sub(ps.Experience.PicturedAt) >= minSmoothSpan {
			smooth = true
		}
		if !smooth {
			continue
		}
		curKbps, _ := sessionBitrates(ps)
		if curKbps > 0 {
			total += curKbps
		}
	}
	return total
}

// NoteBufferEvents tracks a session's cumulative client BufferEvents count and
// returns true only when BufferEvents increased since the previous check.
func (m *Manager) NoteBufferEvents(sessionID string, bufferEvents int) bool {
	if m == nil || sessionID == "" || bufferEvents <= 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastBufEvents == nil {
		m.lastBufEvents = map[string]int{}
	}
	prev := m.lastBufEvents[sessionID]
	if bufferEvents > prev {
		m.lastBufEvents[sessionID] = bufferEvents
		return true
	}
	return false
}

// RemoteCapForNewStream computes the dynamic bitrate cap for a newly starting
// stream. LAN streams (!remote) are never affected. Direct-play-only streams
// (allowTranscoding == false or VideoTranscodingOff) are never forced to
// transcode.
func (m *Manager) RemoteCapForNewStream(ctx context.Context, sessionID string, remote bool, allowTranscoding bool, existingCapKbps int, demandKbps int) (capKbps int, fromBudget bool) {
	if m == nil || !remote {
		return existingCapKbps, false
	}
	if !allowTranscoding || (m.opts.VideoTranscodingOff != nil && m.opts.VideoTranscodingOff()) {
		return existingCapKbps, false
	}

	capacityKbps, _, _, _, _ := m.EffectiveCapacity()
	usableKbps := UsableBudgetKbps(capacityKbps)
	if usableKbps <= 0 {
		return existingCapKbps, false
	}

	// Trigger a non-blocking sibling PMS refresh when remote streams begin,
	// using any already-cached reading immediately so startup never blocks.
	externalKbps := 0
	if m.poller != nil {
		externalKbps = m.poller.Snapshot().TotalKbps
		m.OnSessionsChanged()
	}

	active := m.snapshotActiveStreams()
	budgetCap := AllocateForNewStream(usableKbps, externalKbps, active, sessionID, demandKbps)
	if budgetCap <= 0 {
		return existingCapKbps, false
	}
	if existingCapKbps > 0 && existingCapKbps <= budgetCap {
		return existingCapKbps, false
	}
	// Only attribute the cap to the shared upload budget if it actually
	// constrains this stream (or source demand isn't known yet).
	constrained := demandKbps <= 0 || demandKbps > budgetCap
	if sessionID != "" {
		m.RecordSessionCap(sessionID, budgetCap, constrained)
	}
	return budgetCap, constrained
}

// RecordSessionCap records or clears whether sessionID's active bitrate cap
// came from the shared upload budget.
func (m *Manager) RecordSessionCap(sessionID string, capKbps int, fromBudget bool) {
	if m == nil || sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !fromBudget || capKbps <= 0 {
		delete(m.capped, sessionID)
		return
	}
	m.capped[sessionID] = sessionCapRecord{
		capKbps:    capKbps,
		fromBudget: true,
		updatedAt:  m.now(),
	}
}

// SessionBudgetCap reports whether sessionID is currently capped by the shared
// upload budget and at what kbps ceiling.
func (m *Manager) SessionBudgetCap(sessionID string) (capKbps int, active bool) {
	if m == nil || sessionID == "" {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.capped[sessionID]
	if !ok || !rec.fromBudget || rec.capKbps <= 0 {
		return 0, false
	}
	return rec.capKbps, true
}

// ObserveDelivery records a passive remote delivery timing/backlog measurement
// into the upload capacity estimator, combining it with any concurrent remote
// Ether streams and sibling PMS WAN bandwidth.
func (m *Manager) ObserveDelivery(sessionID string, obs Observation) bool {
	if m == nil {
		return false
	}
	if obs.At.IsZero() {
		obs.At = m.now()
	}
	if obs.SessionID == "" {
		obs.SessionID = sessionID
	}
	streamRate := obs.StreamRateKbps
	if streamRate <= 0 && obs.Bytes > 0 && obs.Duration > 0 {
		streamRate = int(float64(obs.Bytes*8) / obs.Duration.Seconds() / 1000)
	}
	obs.StreamRateKbps = streamRate

	// Combine with other concurrently active remote streams so aggregate uplink
	// usage is measured rather than one stream's fraction of a shared pipe.
	otherKbps := 0
	concurrent := 1
	for _, s := range m.snapshotActiveStreams() {
		if !s.Remote || (sessionID != "" && s.ID == sessionID) {
			continue
		}
		concurrent++
		if s.BitrateKbps > 0 {
			otherKbps += s.BitrateKbps
		}
	}
	if m.poller != nil {
		ext := m.poller.Snapshot()
		otherKbps += ext.TotalKbps
		concurrent += ext.RemoteStreams
	}
	if obs.AggregateKbps < streamRate+otherKbps {
		obs.AggregateKbps = streamRate + otherKbps
	}
	if obs.ConcurrentStreams < concurrent {
		obs.ConcurrentStreams = concurrent
	}

	beforeCap, _, _, _, _ := m.EffectiveCapacity()
	recorded := m.estimator.Observe(obs)
	afterCap, _, _, _, _ := m.EffectiveCapacity()
	if beforeCap != afterCap {
		m.OnSessionsChanged()
	}
	return recorded
}

// OnSessionsChanged signals the background coordinator to reconcile active
// remote streams and conditional sibling PMS polling.
func (m *Manager) OnSessionsChanged() {
	if m == nil {
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run keeps sibling PMS polling (strictly while Ether has remote streams) and
// adaptive HLS rung adjustments synchronized until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(reconcileTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
			m.ReconcileOnce(ctx)
		case <-ticker.C:
			m.ReconcileOnce(ctx)
		}
	}
}

// ReconcileOnce performs one synchronous pass: polls linked PMS servers ONLY
// when Ether currently has active remote streams, prunes ended cap records,
// and applies hysteresis-guarded step-down / step-up adjustments to running
// adaptive remote HLS streams.
func (m *Manager) ReconcileOnce(ctx context.Context) []Adjustment {
	if m == nil {
		return nil
	}
	now := m.now()
	active := m.snapshotActiveStreams()

	remoteCount := 0
	liveIDs := make(map[string]struct{}, len(active))
	for _, s := range active {
		liveIDs[s.ID] = struct{}{}
		if s.Remote {
			remoteCount++
		}
	}

	// Prune cap attribution for ended sessions (keep brief 30s grace for
	// freshly admitted plans whose playback session is opening).
	m.mu.Lock()
	for id, rec := range m.capped {
		if _, ok := liveIDs[id]; !ok && now.Sub(rec.updatedAt) > 30*time.Second {
			delete(m.capped, id)
		}
	}
	for id := range m.lastBufEvents {
		if _, ok := liveIDs[id]; !ok {
			delete(m.lastBufEvents, id)
		}
	}
	m.mu.Unlock()

	externalKbps := 0
	if m.poller != nil {
		if remoteCount > 0 {
			ext := m.poller.PollIfDue(ctx, false)
			externalKbps = ext.TotalKbps
		} else {
			// Requirement: never poll sibling PMS while Ether has no remote streams.
			m.poller.ClearIdle()
		}
	}

	if remoteCount == 0 {
		// Still tick hysteresis with empty active list so state cleans up.
		_ = m.hysteresis.Reconcile(now, 0, 0, nil)
		return nil
	}

	if m.opts.VideoTranscodingOff != nil && m.opts.VideoTranscodingOff() {
		return nil
	}

	capacityKbps, _, _, _, _ := m.EffectiveCapacity()
	usableKbps := UsableBudgetKbps(capacityKbps)

	adjustments := m.hysteresis.Reconcile(now, usableKbps, externalKbps, active)
	if len(adjustments) == 0 || m.opts.ApplySessionCap == nil {
		return adjustments
	}

	applied := make([]Adjustment, 0, len(adjustments))
	for _, adj := range adjustments {
		ok, rungID := m.opts.ApplySessionCap(adj.SessionID, adj.CapKbps)
		if !ok {
			continue
		}
		m.RecordSessionCap(adj.SessionID, adj.CapKbps, adj.CapKbps > 0)
		m.log.Info("adjusted remote stream for shared upload budget",
			"session", adj.SessionID,
			"capKbps", adj.CapKbps,
			"stepDown", adj.StepDown,
			"rung", rungID,
			"usableKbps", usableKbps,
			"otherPmsKbps", externalKbps)
		applied = append(applied, adj)
	}
	return applied
}

// Status returns a fast, non-blocking snapshot of the upload budget and live
// remote usage across Ether and any linked sibling Plex Media Servers.
func (m *Manager) Status() StatusSnapshot {
	if m == nil {
		return StatusSnapshot{Source: "learning"}
	}
	effective, source, overrideKbps, measuredKbps, samples := m.EffectiveCapacity()
	usable := UsableBudgetKbps(effective)

	var etherKbps, etherStreams, budgetCapped int
	for _, s := range m.snapshotActiveStreams() {
		if !s.Remote {
			continue
		}
		etherStreams++
		etherKbps += effectiveStreamBitrate(s)
		if s.CurrentCapKbps > 0 {
			budgetCapped++
		}
	}

	var ext ExternalUsage
	if m.poller != nil && etherStreams > 0 {
		ext = m.poller.Snapshot()
	}

	return StatusSnapshot{
		Enabled:             effective > 0,
		Source:              source,
		OverrideKbps:        overrideKbps,
		MeasuredKbps:        measuredKbps,
		MeasuredSamples:     samples,
		EffectiveKbps:       effective,
		UsableKbps:          usable,
		EtherRemoteKbps:     etherKbps,
		EtherRemoteStreams:  etherStreams,
		BudgetCappedStreams: budgetCapped,
		OtherPMSKbps:        ext.TotalKbps,
		OtherPMSStreams:     ext.RemoteStreams,
		TotalUsedKbps:       etherKbps + ext.TotalKbps,
		OtherServers:        ext.Servers,
	}
}

func (m *Manager) snapshotActiveStreams() []ActiveStream {
	if m == nil || m.opts.ActiveSessions == nil {
		return nil
	}
	sessions := m.opts.ActiveSessions()
	if len(sessions) == 0 {
		return nil
	}
	out := make([]ActiveStream, 0, len(sessions))
	for _, ps := range sessions {
		if !core.SessionActiveStream(ps) {
			continue
		}
		isLive := ps.LiveChannel != nil
		curKbps, baseKbps := sessionBitrates(ps)
		directPlay := ps.Decision == core.DecisionDirectPlay

		var rungsKbps []int
		var currentCap int
		adaptive := false
		if !directPlay && !isLive && m.opts.AdaptiveInfo != nil {
			rungsKbps, currentCap, adaptive = m.opts.AdaptiveInfo(ps.ID)
			if len(rungsKbps) > 0 && rungsKbps[0] > 0 {
				baseKbps = rungsKbps[0]
			}
		}
		if currentCap == 0 {
			if capKbps, ok := m.SessionBudgetCap(ps.ID); ok {
				currentCap = capKbps
			}
		}
		out = append(out, ActiveStream{
			ID:              ps.ID,
			Remote:          ps.Client.Remote,
			Live:            isLive,
			DirectPlay:      directPlay,
			Adaptive:        adaptive,
			BitrateKbps:     curKbps,
			BaseBitrateKbps: baseKbps,
			CurrentCapKbps:  currentCap,
			RungKbps:        rungsKbps,
		})
	}
	return out
}

func sessionBitrates(ps *core.PlaySession) (currentKbps, baseKbps int) {
	if ps == nil {
		return 0, 0
	}
	src := ps.Info.BitrateKbps
	if src <= 0 && ps.Info.Size > 0 && ps.Info.Duration > 0 {
		src = int(float64(ps.Info.Size*8) / ps.Info.Duration.Seconds() / 1000)
	}
	baseKbps = src

	d := ps.DeliveryDecision
	if ps.Encoder != nil {
		d = &ps.Encoder.Decision
	}
	currentKbps = src
	if ps.Decision == core.DecisionTranscode || (ps.Decision == core.DecisionRemux && d != nil && !d.AudioCopy) {
		if d != nil && d.BitrateKbps > 0 {
			currentKbps = d.BitrateKbps
		}
	}
	if baseKbps <= 0 {
		baseKbps = currentKbps
	}
	if currentKbps <= 0 && ps.LiveChannel != nil {
		if ps.Client.MaxBitrateKbps > 0 {
			currentKbps = ps.Client.MaxBitrateKbps
		} else {
			currentKbps = DefaultLiveStreamKbps
		}
		if baseKbps <= 0 {
			baseKbps = currentKbps
		}
	}
	return currentKbps, baseKbps
}
