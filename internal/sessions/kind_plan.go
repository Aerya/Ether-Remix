package sessions

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// planKind resolves a plugin kind item into a plan through the shared
// KindPlayback pipeline (spec §15.1). It mirrors the catalogue tail of Plan:
// resolve version handle, reserve, gate, prepare, validate exact version,
// decide, store.
func (b *Broker) planKind(ctx context.Context, req frontend.PlanRequest, v *core.Viewer) (frontend.PlayPlan, error) {
	b.stampViewerTranscoding(v, &req.Caps)
	in, err := b.deps.KindSource(ctx, v, req.Item)
	if err != nil || in == nil {
		return frontend.PlayPlan{}, core.ErrNotFound
	}
	if in.Title == nil || in.Title.Key != req.Item {
		return frontend.PlayPlan{}, core.ErrNotFound
	}
	if !in.Title.Released.IsZero() && in.Title.Released.After(b.deps.Now()) {
		return frontend.PlayPlan{}, core.ErrUnreleased
	}
	if req.Intent.Estimate && (req.Intent.AudioOrdinal != nil || req.Intent.SubtitleOrdinal != nil) {
		return frontend.PlayPlan{}, frontend.ErrTrackSourceUnavailable
	}
	if req.Intent.Estimate {
		// Detail/browse estimate for a plugin item must never invoke plugin
		// /resolve or ffprobe on the read path.
		info := b.deps.Playback.Estimate(ctx, in.Title, req.Caps)
		dec := core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: !info.AudioOnly(), AudioCopy: true, Container: info.Container}
		if b.deps.Transcoder != nil {
			dec = b.deps.Transcoder.Decide(info, req.Caps, req.Intent.Options)
		}
		return frontend.PlayPlan{
			Estimated:    true,
			Media:        publicMedia(info),
			Decision:     dec,
			DirectPlay:   req.Intent.Options.DirectPlay && dec.Kind == core.DecisionDirectPlay,
			DirectStream: req.Intent.Options.DirectStream && (dec.Kind == core.DecisionDirectPlay || dec.Kind == core.DecisionRemux),
			Transcoding:  b.deps.Transcoder != nil,
		}, nil
	}

	versionHandle := ""
	var chosen *core.ReleaseVariant
	if req.Intent.ExactVersion && req.ResolvedRelease != "" {
		if req.Version == "" || b.deps.DB == nil || len(req.ResolvedRelease) > 512 {
			return frontend.PlayPlan{}, core.ErrNotAvailable
		}
		h := &versionHandles{db: b.deps.DB, broker: b, protocol: req.Protocol}
		item, release, verr := h.lookupRow(ctx, frontend.VersionScope{Viewer: v, Device: req.Device, Protocol: req.Protocol, Caps: req.Caps}, req.Version)
		if verr != nil {
			return frontend.PlayPlan{}, verr
		}
		if item != req.Item || release != req.ResolvedRelease {
			return frontend.PlayPlan{}, core.ErrNotFound
		}
		versionHandle, req.Version = normalizeVersionHandle(req.Version), release
		chosen = &core.ReleaseVariant{ID: release, Auto: release == core.KindAutoVersionID}
	} else if req.Intent.ExactVersion {
		if req.Version == "" || b.deps.DB == nil {
			return frontend.PlayPlan{}, core.ErrNotAvailable
		}
		h := &versionHandles{db: b.deps.DB, broker: b, protocol: req.Protocol}
		scope := frontend.VersionScope{Viewer: v, Device: req.Device, Protocol: req.Protocol, Caps: req.Caps}
		item, release, verr := h.resolveRecord(ctx, scope, req.Version)
		if errors.Is(verr, core.ErrNotFound) {
			if parent, _, lerr := h.lookupRow(ctx, scope, req.Version); lerr == nil && parent == req.Item {
				if req.Protocol == "vela" {
					return frontend.PlayPlan{}, core.ErrVersionUnavailable
				}
				item, release, verr = parent, "", nil
				req.Intent.ExactVersion = false
			}
		}
		if verr != nil {
			return frontend.PlayPlan{}, verr
		}
		if item != req.Item {
			return frontend.PlayPlan{}, core.ErrNotFound
		}
		versionHandle, req.Version = normalizeVersionHandle(req.Version), release
	} else if req.Version != "" && b.deps.DB != nil {
		h := &versionHandles{db: b.deps.DB, broker: b, protocol: req.Protocol}
		scope := frontend.VersionScope{Viewer: v, Device: req.Device, Protocol: req.Protocol, Caps: req.Caps}
		if item, release, verr := h.resolveRecord(ctx, scope, req.Version); verr == nil && item == req.Item {
			versionHandle, req.Version = normalizeVersionHandle(req.Version), release
		}
	}
	if chosen == nil && req.Version != "" {
		if req.Version == core.KindAutoVersionID {
			chosen = &core.ReleaseVariant{ID: core.KindAutoVersionID, Auto: true}
		} else if b.deps.KindVersions != nil {
			vctx := core.WithIndexerPurpose(ctx, core.IndexerPurposePlayback)
			variants, verr := b.deps.KindVersions(vctx, v, req.Item)
			if verr != nil {
				return frontend.PlayPlan{}, verr
			}
			for j := range variants {
				if variants[j].ID == req.Version || variants[j].Release.ID == req.Version {
					chosen = &variants[j]
					break
				}
			}
			if chosen != nil && chosen.Availability == core.VersionUnavailable && req.Protocol == "vela" {
				return frontend.PlayPlan{}, core.ErrVersionUnavailable
			}
			if chosen == nil {
				return frontend.PlayPlan{}, core.ErrNotFound
			}
		} else {
			chosen = &core.ReleaseVariant{ID: req.Version}
		}
	}

	if !b.reservePlan(v.ID) {
		return frontend.PlayPlan{}, core.ErrStreamLimit
	}
	defer b.releasePlanReservation(v.ID)
	unlock, err := b.gates.Acquire(ctx, fmt.Sprintf("broker/owner/%d/%s", v.ID, req.Caps.ClientID))
	if err != nil {
		return frontend.PlayPlan{}, err
	}
	defer unlock()

	kindPlayback, ok := b.deps.Playback.(core.KindPlayback)
	if !ok {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}

	release := ""
	prepareExact := req.Intent.ExactVersion
	if chosen != nil {
		if req.Version == core.KindAutoVersionID || (chosen.Auto && chosen.ID == req.Version) {
			prepareExact = false
			req.Intent.ExactVersion = false
		} else {
			release = req.Version
		}
	}
	if release == "" && req.Version == "" && b.deps.KindRememberedRelease != nil {
		// Play/resume without a version: the viewer's remembered version,
		// as a preference (an unavailable source still falls back).
		release = b.deps.KindRememberedRelease(v.ID, req.Item)
	}
	if release != "" {
		in.PreferSourceID = release
	}
	in.Exact = prepareExact
	in.DeferSupersession = true

	var previous *core.PlaySession
	var existed bool
	if scoped, ok := b.deps.Playback.(core.SurfaceScopedPlayback); ok {
		previous, existed = scoped.SessionForSurface(string(req.Protocol), v.ID, req.Caps.ClientID, req.Item, release, prepareExact)
	} else {
		previous, existed = kindPlayback.SessionForKind(v.ID, req.Caps.ClientID, req.Item)
	}

	requested := b.deps.Now()
	ps, err := kindPlayback.PrepareKind(ctx, v, *in, req.Caps)
	if err != nil {
		if req.Intent.ExactVersion && req.Protocol == "vela" && errors.Is(err, core.ErrNotAvailable) && !errors.Is(err, core.ErrPreparing) {
			return frontend.PlayPlan{}, core.ErrVersionUnavailable
		}
		return frontend.PlayPlan{}, err
	}
	if ps == nil {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	existed = existed && previous != nil && previous.ID == ps.ID
	if req.Intent.ExactVersion && !ps.Resolution.MatchesVersion(release) {
		if !existed {
			b.deps.Playback.Stop(ps.ID)
		}
		if req.Protocol == "vela" {
			return frontend.PlayPlan{}, core.ErrVersionUnavailable
		}
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	b.acquirePlanSource(ps.ID, !existed)
	stored := false
	pinned := false
	defer func() {
		if !stored {
			b.releasePlanSource(ps.ID, pinned)
		}
	}()

	ps, pinned, err = b.pinRequestedTracks(ctx, &req, ps)
	if err != nil {
		return frontend.PlayPlan{}, err
	}
	if err := bindSourceTracks(&req.Intent, ps.Info); err != nil {
		return frontend.PlayPlan{}, err
	}

	if b.deps.SelectScopedTracks != nil || b.deps.SelectTracks != nil {
		var selected core.DecideOptions
		var terr error
		if b.deps.SelectScopedTracks != nil {
			selected, terr = b.deps.SelectScopedTracks(ctx, v, req.Device, in.Title, ps.Info, req.Intent.Options, req.Intent.AudioSet, req.Intent.SubtitleSet)
		} else {
			selected, terr = b.deps.SelectTracks(ctx, v, in.Title, ps.Info, req.Intent.Options, req.Intent.AudioSet, req.Intent.SubtitleSet)
		}
		if terr != nil {
			return frontend.PlayPlan{}, terr
		}
		req.Intent.Options = selected
	}
	dec, err := b.decideTracks(ps, &req)
	if err != nil {
		return frontend.PlayPlan{}, err
	}
	if req.Intent.AllowTranscoding != nil && !*req.Intent.AllowTranscoding && decisionEncodes(dec, ps.Info) {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	if (dec.Kind == core.DecisionDirectPlay && !req.Intent.Options.DirectPlay) || (dec.Kind == core.DecisionRemux && !req.Intent.Options.DirectStream) {
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	ps, pinned, err = b.pinOriginalRuntime(ctx, &req, ps, dec, pinned)
	if err != nil {
		return frontend.PlayPlan{}, err
	}
	id := rand.Text()
	etag := sourceRevision(ps)
	exactRelease := ""
	if (req.Intent.ExactVersion || versionHandle != "") && ps.Resolution != nil && ps.Resolution.Release.ID != "" {
		exactRelease = ps.Resolution.Release.ID
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return frontend.PlayPlan{}, core.ErrNotAvailable
	}
	b.plans[id] = brokerPlan{request: req, source: ps, decision: dec, created: b.deps.Now(), requested: requested, etag: etag, exactRelease: exactRelease, versionHandle: versionHandle, pinned: pinned}
	stored = true
	b.mu.Unlock()
	direct, remux, transcoding := b.planAlternatives(ps, req)
	hls, hlsContainer := b.hlsPlanOutput(ps, req, dec)
	httpOK, httpContainer := b.httpPlanOutput(ps, req, dec)
	relID := ""
	if ps.Resolution != nil {
		relID = ps.Resolution.Release.ID
	}
	return frontend.PlayPlan{ID: id, Media: publicMedia(ps.Info), Decision: dec, SelectedSubtitle: sideSubtitleStream(ps, req, dec), DirectPlay: direct, DirectStream: remux, Transcoding: transcoding, VideoTranscodingOff: req.Channel == "" && b.videoOff(req.Caps), HLS: hls, HLSContainer: hlsContainer, HTTP: httpOK, HTTPContainer: httpContainer, ETag: etag, VersionHandle: versionHandle, ReleaseID: relID}, nil
}
