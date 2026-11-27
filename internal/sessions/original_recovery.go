package sessions

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func (b *Broker) recoverOriginal(ctx context.Context, observed *brokerSession, viewer *core.Viewer, explicit bool) {
	recovery, ok := b.deps.Playback.(core.OriginalRecovery)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	unlock, err := b.gates.Acquire(ctx, ownerGateKey(observed.public.ViewerID, observed.clientID))
	if err != nil {
		return
	}
	defer unlock()
	b.mu.Lock()
	live := b.sessions[observed.public.ID]
	if b.closed || live == nil || live.grant != observed.grant || live.playID != observed.playID || live.format != observed.format || live.originalFailure {
		b.mu.Unlock()
		return
	}
	s := *live
	b.mu.Unlock()
	viewer, err = b.confirmCurrent(ctx, viewer, s.caps)
	if err != nil || !b.currentItemPolicy(ctx, viewer, &s) {
		return
	}
	before, ok := b.deps.Playback.Session(s.playID)
	if !ok || before == nil || before.Resolution == nil {
		return
	}
	revision := sourceRevision(before)
	if s.sourcePinned && revision != s.sourceETag {
		return
	}
	entry, ok := b.bindings.Get(s.public.ID)
	if !ok {
		return
	}
	explicit = explicit || (entry.Requested.ExactVersion && !s.autoRelease)
	position := s.streamPosition
	if s.originalPosition != nil {
		position = *s.originalPosition
	} else if before.ViewOffset != nil {
		position = time.Duration(*before.ViewOffset) * time.Millisecond
	}
	caps := s.caps
	b.stampViewerTranscoding(viewer, &caps)
	req := frontend.PlanRequest{Protocol: s.public.Protocol, Viewer: viewer, Device: s.public.Device, Item: s.public.Item, Caps: caps, Intent: entry.Requested}
	req.Intent.AllowTranscoding = s.allowTranscoding
	audio, subtitle := s.decision.AudioStream, s.opts.SubtitleStream
	if s.originalAudio != nil {
		audio = *s.originalAudio
	}
	if s.originalSubtitle != nil {
		subtitle = *s.originalSubtitle
	}
	type candidatePlan struct {
		decision core.Decision
		options  core.DecideOptions
	}
	plans := make(map[string]candidatePlan)
	ps, at, err := recovery.ReplaceLostOriginal(ctx, s.playID, revision, position, explicit || s.originalSubstituted, func(ps *core.PlaySession) float64 {
		candidate := req
		candidate.Intent.Options = s.opts
		audioTrack, audioScore := matchRecoveryTrack(before.Info, ps.Info, "audio", audio)
		subtitleTrack, subtitleScore := matchRecoveryTrack(before.Info, ps.Info, "subtitle", subtitle)
		candidate.Intent.Options.AudioStream, candidate.Intent.Options.SubtitleStream = audioTrack, subtitleTrack
		candidate.Intent.SubtitleSet = candidate.Intent.Options.SubtitleStream >= 0
		dec, _, err := b.admitWithClientCap(ps, &candidate, s.clientCap, b.admitPlanDecision)
		if err != nil || dec.Kind != core.DecisionDirectPlay {
			return 0
		}
		plans[ps.Resolution.Release.ID] = candidatePlan{decision: dec, options: candidate.Intent.Options}
		return (1 + 2*audioScore + subtitleScore) / 4
	})
	if errors.Is(err, core.ErrVersionUnreadable) || errors.Is(err, context.DeadlineExceeded) {
		b.mu.Lock()
		if live := b.sessions[s.public.ID]; live != nil && live.grant == s.grant {
			live.originalFailure = true
		}
		b.mu.Unlock()
		b.publish(context.WithoutCancel(ctx), sessionEvent("SessionOriginalChanged", s.public, nil))
		return
	}
	if err != nil {
		return
	}

	// The playback manager already fences the old revision; rotate the grant
	// with the public plan so no old URL can acquire the new byte coordinates.
	plan := plans[ps.Resolution.Release.ID]
	grant := rand.Text()
	media := publicMedia(ps.Info)
	path := "/sessions/media/" + s.public.ID + "/" + grant + "/file"
	_ = b.retireAudio(ctx, &s)
	if b.deps.Transcoder != nil {
		b.transcodeStop(s.public.ID)
	}
	b.mu.Lock()
	live = b.sessions[s.public.ID]
	if live == nil || b.closed {
		b.mu.Unlock()
		return
	}
	hold := live.trickplay
	live.trickplay, live.trickplayStarted = nil, false
	live.audio = nil
	live.grant, live.sourceETag = grant, sourceRevision(ps)
	live.exactRelease = ps.Resolution.Release.ID
	live.opts, live.decision = plan.options, plan.decision
	live.public.MediaPath, live.public.Media, live.public.PlayMethod = path, &media, "DirectPlay"
	live.format = frontend.StreamDirect
	live.streamPosition, live.originalPosition, live.originalResumeAt = at, &at, at
	live.originalAudio, live.originalSubtitle = nil, nil
	live.originalReplaced, live.originalAwaitingProgress, live.originalSubstituted = true, true, true
	live.autoSubtitleIdentity = new(int)
	b.mu.Unlock()
	if hold != nil {
		hold.Close(true)
	}
	entry.Info, entry.Decision, entry.Opts = ps.Info, plan.decision, plan.options
	entry.Requested.Options = plan.options
	b.bindings.Put(entry)
	b.publish(context.WithoutCancel(ctx), sessionEvent("SessionOriginalChanged", s.public, nil))
	if err := b.saveNeutralBinding(ctx, s.public.ID, false); err != nil && b.deps.OnEventError != nil {
		b.deps.OnEventError(err)
	}
}

func matchRecoveryTrack(old, next core.MediaInfo, kind string, index int) (int, float64) {
	var want *core.StreamInfo
	for i := range old.Streams {
		track := &old.Streams[i]
		if track.Type == kind && track.Index == index {
			want = track
			break
		}
	}
	if want == nil {
		return -1, 1
	}
	best, bestScore := -1, -1
	var fit float64
	role := func(value *bool) bool { return value != nil && *value }
	for _, track := range next.Streams {
		if track.Type != kind {
			continue
		}
		sameLanguage := frontend.LanguageMatchTier(track.Language, want.Language) > 0 ||
			(track.Language == "" && want.Language == "")
		sameRole := track.Forced == want.Forced && role(track.Commentary) == role(want.Commentary) &&
			role(track.VisualImpaired) == role(want.VisualImpaired) && role(track.Dub) == role(want.Dub) &&
			role(track.HearingImpaired) == role(want.HearingImpaired)
		if kind == "subtitle" && !sameLanguage {
			continue
		}
		score := 0
		if sameLanguage {
			score += 8
		}
		if sameRole {
			score += 4
		}
		if track.Codec == want.Codec {
			score += 2
		}
		if track.Default {
			score++
		}
		if score > bestScore {
			best, bestScore = track.Index, score
			fit = float64(score&12) / 12
		}
	}
	return best, fit
}

func (b *Broker) OriginalPlaybackState(ctx context.Context, a frontend.SessionAccess, id string) (frontend.OriginalPlaybackState, error) {
	s, _, err := b.accessSession(ctx, a, id, false)
	if err != nil {
		return frontend.OriginalPlaybackState{}, err
	}
	out := frontend.OriginalPlaybackState{Unavailable: s.originalFailure}
	if s.originalReplaced && s.public.Media != nil && s.format == frontend.StreamDirect {
		out.Media, out.URL, out.Revision = *s.public.Media, s.public.MediaPath, s.sourceETag
		out.ReleaseID, out.Decision, out.Position = s.exactRelease, s.decision, s.originalResumeAt
		if ps, ok := b.deps.Playback.Session(s.playID); ok && ps != nil {
			out.Origin = ps.Origin
		}
	}
	return out, nil
}

// OriginalRange applies file-grant recovery to a direct read for an owned
// session. The request must identify the replacement to use its byte coordinates.
func (b *Broker) OriginalRange(ctx context.Context, a frontend.SessionAccess, id, release, sourceRevision string) core.OriginalRange {
	if _, ok := b.deps.Playback.(core.OriginalRecovery); !ok {
		return core.OriginalRange{}
	}
	s, v, err := b.access(ctx, a, id)
	if err != nil {
		return core.OriginalRange{}
	}
	named := release != "" && release != core.KindAutoVersionID
	revision := ""
	if s.originalReplaced && (sourceRevision == s.sourceETag || (named && release == s.exactRelease)) {
		revision = s.sourceETag
	}
	return core.OriginalRange{Revision: revision, Lost: func() {
		b.recoverOriginal(context.WithoutCancel(ctx), s, v, named)
	}}
}
