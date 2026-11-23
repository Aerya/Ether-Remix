package playback

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

// MarkerSpan represents a cached intro, credits, commercial, or recap marker
// range used to emit playback.marker.entered / playback.marker.exited events.
type MarkerSpan struct {
	Kind    string
	StartMS int64
	EndMS   int64
}

func (m *Manager) publishDomainEvent(eventType string, payload any, opts ...events.EnvelopeOption) {
	if m == nil || m.deps.Events == nil {
		return
	}
	_ = m.deps.Events.PublishTyped(eventType, payload, opts...)
}

func normalizeDeliveryMode(k core.DecisionKind) string {
	switch k {
	case core.DecisionDirectPlay:
		return "direct_play"
	case core.DecisionRemux:
		return "remux"
	case core.DecisionTranscode:
		return "transcode"
	default:
		return ""
	}
}

func normalizeResolutionTier(height int) string {
	switch {
	case height >= 2000:
		return "4k"
	case height >= 1000:
		return "1080p"
	case height >= 700:
		return "720p"
	case height > 0:
		return "480p"
	default:
		return ""
	}
}

func streamDynamicRange(v *core.StreamInfo) string {
	if v == nil {
		return ""
	}
	if v.DolbyVision {
		return "dv"
	}
	switch strings.ToLower(v.ColorTransfer) {
	case "smpte2084":
		return "hdr10"
	case "arib-std-b67":
		return "hlg"
	default:
		return "sdr"
	}
}

func titleMediaType(t *core.Title, ps *core.PlaySession) string {
	if ps != nil && ps.LiveChannel != nil {
		return "live"
	}
	if t != nil {
		switch t.Type {
		case core.TypeMovie:
			return "movie"
		case core.TypeEpisode:
			return "episode"
		}
	}
	if ps != nil && ps.Key != 0 {
		switch ps.Key.Kind() {
		case core.KindMovie:
			return "movie"
		case core.KindEpisode:
			return "episode"
		}
	}
	return ""
}

func decisionStreamModes(ps *core.PlaySession, d *core.Decision) (delivery, videoDec, audioDec, subDec string) {
	var kind core.DecisionKind
	var videoCopy, audioCopy bool
	if d != nil && d.Kind != "" {
		kind = d.Kind
		videoCopy = d.VideoCopy
		audioCopy = d.AudioCopy
	} else if ps != nil {
		kind = ps.Decision
		videoCopy = ps.DecisionVideoCopy
		audioCopy = ps.DecisionAudioCopy
	}
	delivery = normalizeDeliveryMode(kind)
	switch kind {
	case core.DecisionDirectPlay:
		videoDec, audioDec = "direct_play", "direct_play"
	case core.DecisionRemux:
		videoDec = "copy"
		if audioCopy {
			audioDec = "copy"
		} else {
			audioDec = "transcode"
		}
	case core.DecisionTranscode:
		if videoCopy {
			videoDec = "copy"
		} else {
			videoDec = "transcode"
		}
		if audioCopy {
			audioDec = "copy"
		} else {
			audioDec = "transcode"
		}
	}
	subDec = "none"
	if d != nil && d.SubtitleStream >= 0 {
		if d.BurnSubtitles {
			subDec = "burn"
		} else {
			subDec = "sidecar"
		}
	}
	return delivery, videoDec, audioDec, subDec
}

// BaseSessionContext builds a PlaybackSessionContext from in-memory session,
// title, user, and active-session snapshots without performing any I/O.
func BaseSessionContext(ps *core.PlaySession, title *core.Title, user *core.User, all []*core.PlaySession) events.PlaybackSessionContext {
	var ctx events.PlaybackSessionContext

	if ps != nil {
		if ps.Key != 0 {
			ctx.TitleKey = ps.Key.String()
		}
		ctx.TitleName = ps.TitleName
		if ps.Info.Duration > 0 {
			ctx.DurationMS = ps.Info.Duration.Milliseconds()
		}
		ctx.Source.Container = ps.Info.Container
		ctx.Source.BitrateKbps = ps.Info.BitrateKbps
		ctx.Source.FileSizeBytes = ps.Info.Size
		if v := ps.Info.Video(); v != nil {
			ctx.Source.VideoCodec = v.Codec
			ctx.Source.VideoWidth = v.Width
			ctx.Source.VideoHeight = v.Height
			ctx.Source.VideoResolution = normalizeResolutionTier(v.Height)
			ctx.Source.VideoDynamicRange = streamDynamicRange(v)
		}
		for i := range ps.Info.Streams {
			st := &ps.Info.Streams[i]
			if st.Type == "audio" {
				ctx.Source.AudioCodec = st.Codec
				ctx.Source.AudioChannels = st.Channels
				if st.Default {
					break
				}
			}
		}

		// Stream context.
		dec := ps.DeliveryDecision
		delivery, vDec, aDec, sDec := decisionStreamModes(ps, dec)
		ctx.Stream.Delivery = delivery
		ctx.Stream.VideoDecision = vDec
		ctx.Stream.AudioDecision = aDec
		ctx.Stream.SubtitleDecision = sDec
		ctx.Stream.Container = ps.Info.Container
		if dec != nil {
			if dec.Container != "" {
				ctx.Stream.Container = dec.Container
			}
			if dec.VideoCodec != "" {
				ctx.Stream.VideoCodec = dec.VideoCodec
			} else if vDec == "direct_play" || vDec == "copy" {
				ctx.Stream.VideoCodec = ctx.Source.VideoCodec
			}
			if dec.MaxHeight > 0 {
				ctx.Stream.VideoHeight = dec.MaxHeight
			} else {
				ctx.Stream.VideoHeight = ctx.Source.VideoHeight
			}
			ctx.Stream.VideoResolution = normalizeResolutionTier(ctx.Stream.VideoHeight)
			if dec.ToneMap {
				ctx.Stream.VideoDynamicRange = "sdr"
			} else {
				ctx.Stream.VideoDynamicRange = ctx.Source.VideoDynamicRange
			}
			if dec.AudioCodec != "" {
				ctx.Stream.AudioCodec = dec.AudioCodec
			} else {
				ctx.Stream.AudioCodec = ctx.Source.AudioCodec
			}
			if dec.AudioChannels > 0 {
				ctx.Stream.AudioChannels = dec.AudioChannels
			} else {
				ctx.Stream.AudioChannels = ctx.Source.AudioChannels
			}
			if dec.BitrateKbps > 0 {
				ctx.Stream.BitrateKbps = dec.BitrateKbps
			} else {
				ctx.Stream.BitrateKbps = ctx.Source.BitrateKbps
			}
		} else {
			ctx.Stream.VideoCodec = ctx.Source.VideoCodec
			ctx.Stream.VideoHeight = ctx.Source.VideoHeight
			ctx.Stream.VideoResolution = ctx.Source.VideoResolution
			ctx.Stream.VideoDynamicRange = ctx.Source.VideoDynamicRange
			ctx.Stream.AudioCodec = ctx.Source.AudioCodec
			ctx.Stream.AudioChannels = ctx.Source.AudioChannels
			ctx.Stream.BitrateKbps = ctx.Source.BitrateKbps
		}
		if ps.SpeedBps > 0 {
			ctx.Stream.BandwidthKbps = int((ps.SpeedBps * 8) / 1000)
		} else {
			ctx.Stream.BandwidthKbps = ctx.Stream.BitrateKbps
		}
		if ps.Encoder != nil {
			ctx.Stream.TranscodeSpeed = ps.Encoder.Speed
			proc := strings.ToLower(ps.Encoder.Processor)
			if proc != "" && proc != "cpu" && proc != "software" && proc != "copy" {
				ctx.Stream.HWEncode = true
				ctx.Stream.HWDecode = true
			}
			if strings.EqualFold(ps.Encoder.Phase, "throttled") {
				ctx.Stream.Throttled = true
			}
		}

		// Viewer & client & network context.
		ctx.Viewer.ID = ps.UserID
		ctx.Viewer.Username = ps.Username
		ctx.Client.ID = ps.ClientID
		if ctx.Client.ID == "" {
			ctx.Client.ID = ps.Client.ClientID
		}
		ctx.Client.Name = ps.Client.DeviceName
		if ctx.Client.Name == "" {
			ctx.Client.Name = ps.Client.Device
		}
		ctx.Client.Platform = ps.Client.Platform
		ctx.Client.Product = ps.Client.Product
		ctx.Client.Version = ps.Client.Version
		ctx.Client.Protocol = string(ps.Client.Protocol)

		ctx.Network.RemoteIP = ps.Client.ClientIP
		if ps.Client.Remote {
			ctx.Network.Location = "wan"
		} else {
			ctx.Network.Location = "lan"
		}
	}

	if title != nil {
		if ctx.TitleKey == "" && title.Key != 0 {
			ctx.TitleKey = title.Key.String()
		}
		if title.Title != "" {
			ctx.TitleName = title.Title
		}
		ctx.ShowTitle = title.ShowTitle
		ctx.Season = title.Season
		ctx.Episode = title.Episode
		ctx.Year = title.Year
		if title.LibraryID > 0 {
			ctx.LibraryID = strconv.FormatInt(int64(title.LibraryID), 10)
		}
		ctx.ContentRating = title.ContentRating
		if len(title.Genres) > 0 {
			ctx.Genres = append([]string(nil), title.Genres...)
		}
		if len(title.Cast) > 0 {
			actors := make([]string, 0, min(len(title.Cast), 10))
			for i := 0; i < len(title.Cast) && i < 10; i++ {
				if title.Cast[i].Name != "" {
					actors = append(actors, title.Cast[i].Name)
				}
			}
			ctx.Actors = actors
		}
		if len(title.Directors) > 0 {
			dirs := make([]string, 0, len(title.Directors))
			for _, d := range title.Directors {
				if d.Name != "" {
					dirs = append(dirs, d.Name)
				}
			}
			ctx.Directors = dirs
		}
		if len(title.Studios) > 0 {
			ctx.Studio = title.Studios[0]
		}
		if ctx.DurationMS == 0 && title.Runtime > 0 {
			ctx.DurationMS = title.Runtime.Milliseconds()
		}
		if title.PosterPath != "" {
			ctx.PosterURL = title.PosterPath
		}
	}
	ctx.MediaType = titleMediaType(title, ps)

	if user != nil {
		if ctx.Viewer.ID == 0 {
			ctx.Viewer.ID = user.ID
		}
		if ctx.Viewer.Username == "" {
			ctx.Viewer.Username = user.Username
		}
		if user.IsOwner {
			ctx.Viewer.Role = "owner"
		} else {
			ctx.Viewer.Role = "viewer"
		}
	}

	// Aggregate live viewer + server counts from active snapshots.
	viewerIPs := map[string]struct{}{}
	for _, other := range all {
		if other == nil || other.PreparedOnly {
			continue
		}
		ctx.Server.ActiveStreams++
		isTranscode := other.Decision == core.DecisionTranscode && other.Info.Video() != nil && !other.DecisionVideoCopy
		if isTranscode {
			ctx.Server.ActiveTranscodes++
		} else if other.Decision == core.DecisionDirectPlay {
			ctx.Server.ActiveDirectPlays++
		}
		if other.DeliveryDecision != nil && other.DeliveryDecision.BitrateKbps > 0 {
			ctx.Server.TotalBandwidthKbps += other.DeliveryDecision.BitrateKbps
		} else if other.Info.BitrateKbps > 0 {
			ctx.Server.TotalBandwidthKbps += other.Info.BitrateKbps
		}
		if ctx.Viewer.ID > 0 && other.UserID == ctx.Viewer.ID {
			ctx.Viewer.ActiveStreams++
			if isTranscode {
				ctx.Viewer.ActiveTranscodes++
			}
			if ip := strings.TrimSpace(other.Client.ClientIP); ip != "" {
				viewerIPs[ip] = struct{}{}
			}
		}
	}
	ctx.Viewer.DistinctActiveIPs = len(viewerIPs)

	return ctx
}

func (m *Manager) buildSessionContext(s *session, snap *core.PlaySession) events.PlaybackSessionContext {
	var title *core.Title
	var user *core.User
	if s != nil {
		title = s.title
		u := s.user
		user = &u
	}
	all := m.Sessions()
	base := BaseSessionContext(snap, title, user, all)
	if m.deps.EnrichEventContext != nil {
		base = m.deps.EnrichEventContext(snap, title, base)
	}
	return base
}

func playbackCorrelation(s *session, snap *core.PlaySession) events.EnvelopeOption {
	var c events.Correlation
	if snap != nil {
		c.SessionID = snap.ID
		c.PlayID = snap.ID
		c.ViewerID = snap.UserID
		c.DeviceID = snap.ClientID
		if snap.Key != 0 {
			c.TitleID = snap.Key.String()
		}
	} else if s != nil {
		c.SessionID = s.id
		c.PlayID = s.id
		c.ViewerID = s.user.ID
		c.DeviceID = s.caps.ClientID
		if s.ident.key != 0 {
			c.TitleID = s.ident.key.String()
		}
	}
	return events.WithCorrelation(c)
}

func (m *Manager) emitSessionStarted(s *session, snap *core.PlaySession) {
	if m == nil || m.deps.Events == nil || s == nil || snap == nil {
		return
	}
	if s.isBackgroundOnly() {
		return
	}
	ctx := m.buildSessionContext(s, snap)
	m.publishDomainEvent(events.EventPlaybackSessionStarted, events.PlaybackSessionStartedPayload{
		SessionID:              snap.ID,
		PlayID:                 snap.ID,
		PlaybackSessionContext: ctx,
	}, playbackCorrelation(s, snap))
}

func (m *Manager) emitSessionStopped(s *session, snap *core.PlaySession) {
	if m == nil || m.deps.Events == nil || s == nil || snap == nil {
		return
	}
	if s.isBackgroundOnly() {
		return
	}
	s.mu.Lock()
	pos := s.viewerPosition
	failure := s.exp.failure
	viewerState := s.viewerState
	stoppedBySurface := s.stoppedBySurface
	s.mu.Unlock()

	posMS := pos.Milliseconds()
	if snap.ViewOffset != nil && *snap.ViewOffset > posMS {
		posMS = *snap.ViewOffset
	}
	ctx := m.buildSessionContext(s, snap)
	var ratio float64
	if ctx.DurationMS > 0 && posMS > 0 {
		ratio = float64(posMS) / float64(ctx.DurationMS)
		if ratio > 1 {
			ratio = 1
		}
	}
	stopReason := "session_closed"
	switch {
	case failure != "":
		stopReason = "failed_start"
	case viewerState == "stopped":
		stopReason = "client_stopped"
	case stoppedBySurface:
		stopReason = "surface_stopped"
	}
	m.publishDomainEvent(events.EventPlaybackSessionStopped, events.PlaybackSessionStoppedPayload{
		SessionID:              snap.ID,
		PlayID:                 snap.ID,
		PositionMS:             posMS,
		BytesServed:            snap.BytesServed,
		WatchedRatio:           ratio,
		StopReason:             stopReason,
		PlaybackSessionContext: ctx,
	}, playbackCorrelation(s, snap))
}

func (m *Manager) emitSessionFailed(s *session, snap *core.PlaySession, stage string, err error) {
	if m == nil || m.deps.Events == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, core.ErrStreamLimit) {
		return
	}
	var sessID, releaseID, indexer string
	if snap != nil {
		sessID = snap.ID
	} else if s != nil {
		sessID = s.id
	}
	if s != nil {
		if g := s.current(); g != nil && g.res != nil {
			releaseID = g.res.Release.ID
			indexer = g.res.Release.Indexer
		}
	}
	ctx := m.buildSessionContext(s, snap)
	m.publishDomainEvent(events.EventPlaybackSessionFailed, events.PlaybackSessionFailedPayload{
		SessionID:              sessID,
		PlayID:                 sessID,
		Stage:                  stage,
		Error:                  OwnerError(err),
		ReleaseID:              releaseID,
		Indexer:                indexer,
		PlaybackSessionContext: ctx,
	}, playbackCorrelation(s, snap))
}

func (m *Manager) emitSourceFailover(s *session, from, to *generation, down bool, reason string) {
	if m == nil || m.deps.Events == nil || s == nil {
		return
	}
	var fromID, fromIdx, toID, toIdx string
	if from != nil && from.res != nil {
		fromID = from.res.Release.ID
		fromIdx = from.res.Release.Indexer
	}
	if to != nil && to.res != nil {
		toID = to.res.Release.ID
		toIdx = to.res.Release.Indexer
	}
	s.mu.Lock()
	attempt := s.failovers
	s.mu.Unlock()
	if attempt <= 0 {
		attempt = 1
	}
	if reason == "" {
		if down {
			reason = "slow source down-failover"
		} else {
			reason = "source failover"
		}
	}
	titleKey := ""
	if s.ident.key != 0 {
		titleKey = s.ident.key.String()
	}
	m.publishDomainEvent(events.EventPlaybackSourceFailover, events.PlaybackSourceFailoverPayload{
		SessionID:     s.id,
		PlayID:        s.id,
		TitleKey:      titleKey,
		TitleName:     s.titleName,
		FromReleaseID: fromID,
		ToReleaseID:   toID,
		FromIndexer:   fromIdx,
		ToIndexer:     toIdx,
		Reason:        reason,
		Attempt:       attempt,
	}, playbackCorrelation(s, nil))
}

func (m *Manager) emitStreamStalled(s *session, stalls int, pos time.Duration) {
	if m == nil || m.deps.Events == nil || s == nil {
		return
	}
	snap := s.snapshot(m.now())
	var bufMS int64
	if snap.BufferedUntil > pos {
		bufMS = (snap.BufferedUntil - pos).Milliseconds()
	}
	ctx := m.buildSessionContext(s, snap)
	m.publishDomainEvent(events.EventPlaybackStreamStalled, events.PlaybackStreamStalledPayload{
		SessionID:              snap.ID,
		PlayID:                 snap.ID,
		StallCount:             stalls,
		PositionMS:             pos.Milliseconds(),
		BufferMS:               bufMS,
		PlaybackSessionContext: ctx,
	}, playbackCorrelation(s, snap))
}

func (m *Manager) emitStreamDecision(s *session, prevKind core.DecisionKind, d core.Decision) {
	if m == nil || m.deps.Events == nil || s == nil || d.Kind == "" {
		return
	}
	snap := s.snapshot(m.now())
	ctx := m.buildSessionContext(s, snap)
	delivery, vDec, aDec, sDec := decisionStreamModes(snap, &d)
	m.publishDomainEvent(events.EventPlaybackStreamDecision, events.PlaybackStreamDecisionPayload{
		SessionID:              snap.ID,
		PlayID:                 snap.ID,
		PreviousDelivery:       normalizeDeliveryMode(prevKind),
		Delivery:               delivery,
		VideoDecision:          vDec,
		AudioDecision:          aDec,
		SubtitleDecision:       sDec,
		MaxHeight:              d.MaxHeight,
		BitrateKbps:            d.BitrateKbps,
		HWDecode:               ctx.Stream.HWDecode,
		HWEncode:               ctx.Stream.HWEncode,
		Reason:                 d.Reason,
		PlaybackSessionContext: ctx,
	}, playbackCorrelation(s, snap))
}

func (m *Manager) emitStreamQualityChanged(s *session, fromH, toH, fromB, toB int, reason string) {
	if m == nil || m.deps.Events == nil || s == nil {
		return
	}
	m.publishDomainEvent(events.EventPlaybackStreamQualityChanged, events.PlaybackStreamQualityChangedPayload{
		SessionID:       s.id,
		PlayID:          s.id,
		FromHeight:      fromH,
		ToHeight:        toH,
		FromBitrateKbps: fromB,
		ToBitrateKbps:   toB,
		Reason:          reason,
	}, playbackCorrelation(s, nil))
}

func (m *Manager) emitConcurrencyExceeded(user *core.User, caps core.ClientCaps, key core.RatingKey, limit, active int) {
	if m == nil || m.deps.Events == nil || user == nil {
		return
	}
	var ps core.PlaySession
	ps.UserID = user.ID
	ps.Username = user.Username
	ps.ClientID = caps.ClientID
	ps.Client = caps
	ps.Key = key
	all := m.Sessions()
	ctx := BaseSessionContext(&ps, nil, user, all)
	if m.deps.EnrichEventContext != nil {
		ctx = m.deps.EnrichEventContext(&ps, nil, ctx)
	}
	if active < ctx.Viewer.ActiveStreams {
		active = ctx.Viewer.ActiveStreams
	}
	m.publishDomainEvent(events.EventPlaybackConcurrencyExceeded, events.PlaybackConcurrencyExceededPayload{
		Limit:                  limit,
		ActiveStreams:          active,
		ActiveTranscodes:       ctx.Viewer.ActiveTranscodes,
		DistinctActiveIPs:      ctx.Viewer.DistinctActiveIPs,
		PlaybackSessionContext: ctx,
	}, events.WithCorrelation(events.Correlation{
		ViewerID: user.ID,
		DeviceID: caps.ClientID,
		TitleID:  key.String(),
	}))
}

const seekDetectThreshold = 10 * time.Second

func (m *Manager) observeProgressDomainEvents(s *session, prevState string, prevPausedAt time.Time, prevPos time.Duration, prevViewerAt time.Time, newState string, newPos time.Duration, now time.Time) {
	if m == nil || m.deps.Events == nil || s == nil {
		return
	}
	posMS := max(0, newPos.Milliseconds())

	// 1. Paused / resumed transitions.
	if newState == "paused" && prevState != "paused" {
		snap := s.snapshot(now)
		ctx := m.buildSessionContext(s, snap)
		m.publishDomainEvent(events.EventPlaybackSessionPaused, events.PlaybackSessionPausedPayload{
			SessionID:              snap.ID,
			PlayID:                 snap.ID,
			PositionMS:             posMS,
			PlaybackSessionContext: ctx,
		}, playbackCorrelation(s, snap))
	} else if newState == "playing" && prevState == "paused" {
		snap := s.snapshot(now)
		ctx := m.buildSessionContext(s, snap)
		var pausedForMS int64
		if !prevPausedAt.IsZero() && now.After(prevPausedAt) {
			pausedForMS = now.Sub(prevPausedAt).Milliseconds()
		}
		m.publishDomainEvent(events.EventPlaybackSessionResumed, events.PlaybackSessionResumedPayload{
			SessionID:              snap.ID,
			PlayID:                 snap.ID,
			PositionMS:             posMS,
			PausedForMS:            pausedForMS,
			PlaybackSessionContext: ctx,
		}, playbackCorrelation(s, snap))
	}

	// 2. Seek detection (significant jump relative to expected linear advance).
	if !prevViewerAt.IsZero() && prevState != "stopped" && newState != "stopped" {
		expectedAdvance := time.Duration(0)
		if prevState != "paused" {
			if d := now.Sub(prevViewerAt); d > 0 {
				expectedAdvance = d
			}
		}
		delta := newPos - prevPos
		drift := newPos - (prevPos + expectedAdvance)
		if math.Abs(float64(delta)) >= float64(seekDetectThreshold) && math.Abs(float64(drift)) >= float64(seekDetectThreshold) {
			m.publishDomainEvent(events.EventPlaybackStreamSeeked, events.PlaybackStreamSeekedPayload{
				SessionID: s.id,
				PlayID:    s.id,
				FromMS:    max(0, prevPos.Milliseconds()),
				ToMS:      posMS,
				DeltaMS:   delta.Milliseconds(),
			}, playbackCorrelation(s, nil))
		}
	}

	// 3. Marker enter / exit tracking.
	if m.deps.CachedMarkers != nil && newState == "playing" {
		m.observeMarkerTransition(s, posMS, now)
	}
}

func (m *Manager) observeMarkerTransition(s *session, posMS int64, now time.Time) {
	s.mu.Lock()
	loaded := s.markersLoaded
	markers := s.markersCache
	markersDur := s.markersDuration
	key := s.ident.key
	g := s.cur
	s.mu.Unlock()

	if g != nil {
		info := g.infoSnapshot()
		if !loaded || markersDur != info.Duration {
			found := m.deps.CachedMarkers(key, info)
			s.mu.Lock()
			if s.markersDuration != info.Duration {
				s.markersDuration = info.Duration
				s.markersFirstCheck = time.Time{}
				s.markersLoaded = false
			}
			if len(found) > 0 {
				s.markersLoaded = true
				s.markersCache = found
			} else {
				if s.markersFirstCheck.IsZero() {
					s.markersFirstCheck = now
				}
				if now.Sub(s.markersFirstCheck) >= 20*time.Second {
					s.markersLoaded = true
				}
				s.markersCache = nil
			}
			markers = s.markersCache
			s.mu.Unlock()
		}
	}
	if len(markers) == 0 {
		return
	}

	var matched *MarkerSpan
	for i := range markers {
		mk := &markers[i]
		if mk.Kind != "" && posMS >= mk.StartMS && posMS < mk.EndMS {
			matched = mk
			break
		}
	}

	s.mu.Lock()
	prevKind := s.activeMarkerKind
	prevStart := s.activeMarkerStart
	prevEnd := s.activeMarkerEnd
	var nextKind string
	var nextStart, nextEnd int64
	if matched != nil {
		nextKind = matched.Kind
		nextStart = matched.StartMS
		nextEnd = matched.EndMS
	}
	changed := prevKind != nextKind || prevStart != nextStart || prevEnd != nextEnd
	if changed {
		s.activeMarkerKind = nextKind
		s.activeMarkerStart = nextStart
		s.activeMarkerEnd = nextEnd
	}
	s.mu.Unlock()

	if !changed {
		return
	}
	snap := s.snapshot(now)
	ctx := m.buildSessionContext(s, snap)
	corr := playbackCorrelation(s, snap)
	if prevKind != "" {
		m.publishDomainEvent(events.EventPlaybackMarkerExited, events.PlaybackMarkerExitedPayload{
			SessionID:              snap.ID,
			PlayID:                 snap.ID,
			MarkerKind:             prevKind,
			PositionMS:             posMS,
			PlaybackSessionContext: ctx,
		}, corr)
	}
	if nextKind != "" {
		m.publishDomainEvent(events.EventPlaybackMarkerEntered, events.PlaybackMarkerEnteredPayload{
			SessionID:              snap.ID,
			PlayID:                 snap.ID,
			MarkerKind:             nextKind,
			StartMS:                nextStart,
			EndMS:                  nextEnd,
			PositionMS:             posMS,
			PlaybackSessionContext: ctx,
		}, corr)
	}
}

func (s *session) isBackgroundOnly() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	bg := s.analysisSource || s.backgroundHold != nil
	s.mu.Unlock()
	return bg
}
