package sessions

import (
	"context"
	"errors"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// pinRequestedTracks binds a plan to the exact source revision when the
// client asked for a specific audio or subtitle stream, and pins that
// revision so the source cannot silently fail over under the client's track
// choice. It is shared by the catalogue plan and the plugin kind plan so both
// honour explicit stream-index selection with identical ETag and pinning
// semantics (spec §15.1). Requested positions the source cannot honour are
// dropped first (dropStaleTracks), so a stale id never pins or fails a start.
//
// It returns the current session snapshot (which may have been re-read under
// the pin) and whether a pin was taken; the caller must release it with
// releasePlanSource on a plan that is not stored. On error it returns the
// original ps unchanged (never nil) so the caller's deferred release stays
// valid.
func (b *Broker) pinRequestedTracks(ctx context.Context, req *frontend.PlanRequest, ps *core.PlaySession) (*core.PlaySession, bool, error) {
	if req.Protocol == "vela" && ps.Info.Estimated {
		return ps, false, frontend.ErrTrackSourceUnavailable
	}
	dropStaleTracks(&req.Intent, ps.Info)
	if req.Intent.AudioOrdinal != nil || req.Intent.SubtitleOrdinal != nil {
		if ps.Info.Estimated {
			return ps, false, frontend.ErrTrackSourceUnavailable
		}
		if req.ExpectedSourceETag == "" {
			req.ExpectedSourceETag = sourceRevision(ps)
			if req.ExpectedSourceETag == "" {
				return ps, false, frontend.ErrTrackSourceUnavailable
			}
		}
	}
	if req.ExpectedSourceETag == "" {
		return ps, false, nil
	}
	pins, ok := b.deps.Playback.(core.PlaybackRevisionPin)
	if !ok {
		if req.Intent.AudioOrdinal != nil || req.Intent.SubtitleOrdinal != nil {
			return ps, false, frontend.ErrTrackSourceUnavailable
		}
		return ps, false, frontend.ErrSourceGone
	}
	if err := pins.PinSourceRevision(ctx, ps.ID, req.ExpectedSourceETag); err != nil {
		if ctx.Err() != nil {
			return ps, false, ctx.Err()
		}
		if errors.Is(err, core.ErrSourceChanged) {
			return ps, false, frontend.ErrSourceGone
		}
		return ps, false, err
	}
	current, ok := b.deps.Playback.Session(ps.ID)
	if !ok || current == nil || sourceRevision(current) != req.ExpectedSourceETag {
		return ps, true, frontend.ErrSourceGone
	}
	return current, true, nil
}

// pinOriginalRuntime pins the opened source revision only when the admitted
// decision actually uses Vela's custom original-file runtime (and its optional
// sidecar audio rendition), leaving ordinary browser <video> direct play and
// HLS/MSE sessions free to fail over across releases.
func (b *Broker) pinOriginalRuntime(ctx context.Context, req *frontend.PlanRequest, ps *core.PlaySession, dec core.Decision, pinned bool) (*core.PlaySession, bool, error) {
	if pinned || dec.Reason != "original_runtime" {
		return ps, pinned, nil
	}
	if ps.Info.Estimated {
		return ps, false, frontend.ErrTrackSourceUnavailable
	}
	if req.ExpectedSourceETag == "" {
		req.ExpectedSourceETag = sourceRevision(ps)
		if req.ExpectedSourceETag == "" {
			return ps, false, frontend.ErrTrackSourceUnavailable
		}
	}
	return b.pinRequestedTracks(ctx, req, ps)
}

// decideTracks decides delivery. When a subtitle the viewer's rules chose
// (not one the client named) is what makes the start impossible, such as an
// image subtitle whose burn-in needs a video encode this server or request
// does not allow, it decides again without that subtitle and playback starts.
// A subtitle the client named keeps the clear refusal.
func (b *Broker) decideTracks(ps *core.PlaySession, req *frontend.PlanRequest) (core.Decision, error) {
	dec, err := b.decideProfile(ps, *req)
	named := req.Intent.SubtitleSet || (req.Intent.Hints != nil && req.Intent.Hints.ManualSubtitle)
	if named || req.Intent.Options.SubtitleStream < 0 {
		return dec, err
	}
	noEncode := (req.Channel == "" && b.videoOff(req.Caps)) || (req.Intent.AllowTranscoding != nil && !*req.Intent.AllowTranscoding)
	var incompatible *frontend.NoCompatiblePathError
	switch {
	case err == nil && !(noEncode && dec.BurnSubtitles && decisionEncodesVideo(dec, ps.Info)):
		return dec, nil
	case err != nil && !errors.Is(err, core.ErrVideoTranscodingDisabled) && !errors.As(err, &incompatible):
		return dec, err
	}
	retry := *req
	retry.Intent.Options.SubtitleStream = -1
	retry.Intent.Options.BurnSubtitles = false
	without, retryErr := b.decideProfile(ps, retry)
	if retryErr != nil {
		return dec, err
	}
	*req = retry
	return without, nil
}
