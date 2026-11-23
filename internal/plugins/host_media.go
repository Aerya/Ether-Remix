package plugins

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

const (
	// MediaProbeTimeout is the hard per-call timeout enforced on every
	// media.probe host call.
	MediaProbeTimeout = 10 * time.Second
	// DefaultMediaProbePerMin is the per-plugin sliding-window rate limit for
	// media.probe host calls.
	DefaultMediaProbePerMin = 60
)

// MediaPermissionAllowsProbe reports whether a normalized permissions.media
// level permits media.probe host calls.
func MediaPermissionAllowsProbe(level string) bool {
	return normLevel(level) == "probe"
}

// MediaProbeAllowedPath reports whether a plugin call to path may invoke
// media.probe. Only jobs, actions, Flow actions, tools, event deliveries,
// setup/test, feed, background kind sync, Live TV sync, declared scheduled
// calls, and background calls are permitted; every interactive viewer or
// client request path (including /resolve, /search, /catalog, /stream, /meta,
// /subtitles, /view, /options, /health, /configure, and frontend adapter
// routes) is refused.
func MediaProbeAllowedPath(man Manifest, path string, background bool) bool {
	p, _, _ := strings.Cut(strings.TrimSpace(path), "?")
	if p == "" {
		return false
	}
	if isClientRequestPath(p) {
		return false
	}
	if background {
		return true
	}
	switch {
	case strings.HasPrefix(p, "/ether/action/"),
		strings.HasPrefix(p, "/ether/job/"),
		strings.HasPrefix(p, "/ether/v1/flow-actions/"),
		p == "/ether/event",
		strings.HasPrefix(p, "/ether/tool/"),
		strings.HasPrefix(p, "/ether/v1/setup/"),
		p == "/ether/test",
		strings.HasPrefix(p, "/ether/feed/"),
		wasm.KindSyncPath(p),
		wasm.LiveTVSyncPath(p):
		return true
	}
	man.Ether = man.EffectiveEther()
	if man.Ether != nil {
		for _, s := range man.Ether.Schedule {
			callPath, _, _ := strings.Cut(strings.TrimSpace(s.Call), "?")
			if callPath == p {
				return true
			}
		}
		for _, fa := range man.Ether.Contributes.FlowActions {
			route, _, _ := strings.Cut(strings.TrimSpace(fa.Route), "?")
			if route == p {
				return true
			}
		}
	}
	return false
}

// ValidateMediaProbeSource validates the PlayableSource argument passed to
// media.probe. It accepts the same source shape /resolve returns.
func ValidateMediaProbeSource(src core.PlayableSource) error {
	pattern := strings.ToLower(strings.TrimSpace(src.Pattern))
	hasBlob := strings.TrimSpace(src.BlobRef) != ""
	if pattern == "" {
		switch {
		case src.Container != nil:
			pattern = "container"
		case src.Release != nil:
			pattern = "release"
		case strings.TrimSpace(src.Path) != "" && strings.TrimSpace(src.URL) == "" && !hasBlob:
			pattern = "file"
		default:
			pattern = "range"
		}
	}
	switch pattern {
	case "file":
		if strings.TrimSpace(src.Path) == "" {
			return &ValidationError{Fields: []FieldError{{Field: "path", Message: "file source requires path"}}}
		}
	case "release":
		if src.Release == nil {
			return &ValidationError{Fields: []FieldError{{Field: "release", Message: "release source requires release"}}}
		}
		r := src.Release
		hasTarget := strings.TrimSpace(r.InfoHash) != "" ||
			strings.TrimSpace(r.Magnet) != "" ||
			strings.TrimSpace(r.TorrentURL) != "" ||
			strings.TrimSpace(r.NZBURL) != ""
		if !hasTarget && r.Candidate != nil {
			c := r.Candidate
			hasTarget = strings.TrimSpace(c.InfoHash) != "" ||
				strings.TrimSpace(c.Magnet) != "" ||
				strings.TrimSpace(c.TorrentURL) != "" ||
				strings.TrimSpace(c.NZBURL) != ""
		}
		if !hasTarget {
			return &ValidationError{Fields: []FieldError{{Field: "release", Message: "release source requires infoHash, magnet, torrentUrl, nzbUrl, or candidate"}}}
		}
	case "container":
		if src.Container == nil {
			return &ValidationError{Fields: []FieldError{{Field: "container", Message: "container source requires container"}}}
		}
		if len(src.Container.Volumes) == 0 {
			return &ValidationError{Fields: []FieldError{{Field: "container.volumes", Message: "container source requires at least one volume"}}}
		}
		for i, v := range src.Container.Volumes {
			if strings.TrimSpace(v.URL) == "" && strings.TrimSpace(v.BlobRef) == "" {
				return &ValidationError{Fields: []FieldError{{Field: fmt.Sprintf("container.volumes[%d]", i), Message: "container volume requires url or blobRef"}}}
			}
		}
	case "range", "download", "direct":
		if strings.TrimSpace(src.URL) == "" && !hasBlob {
			return &ValidationError{Fields: []FieldError{{Field: "url", Message: pattern + " source requires url or blobRef"}}}
		}
	default:
		return &ValidationError{Fields: []FieldError{{Field: "pattern", Message: fmt.Sprintf("unknown source pattern %q", src.Pattern)}}}
	}
	return nil
}

// SetMediaProbeRateLimit overrides the per-plugin media.probe requests-per-minute
// limit. Passing <= 0 restores DefaultMediaProbePerMin. Intended for tests.
func (m *Manager) SetMediaProbeRateLimit(limit int) {
	m.mu.Lock()
	m.mediaProbeLimit = limit
	m.mu.Unlock()
}

func (m *Manager) mediaProbePerMin() int {
	m.mu.Lock()
	limit := m.mediaProbeLimit
	m.mu.Unlock()
	if limit <= 0 {
		return DefaultMediaProbePerMin
	}
	return limit
}

// MediaProbe reads container metadata ({durationSec, container, width, height,
// sizeBytes}) for a PlayableSource, reusing the durable release_probes cache
// when available. Requires permissions.media: "probe" (checked live against
// grants), is restricted to jobs, actions, sync, and other non-viewer routes,
// is rate-limited per plugin, and is hard-bounded at MediaProbeTimeout (10 s).
func (h *host) MediaProbe(ctx context.Context, src core.PlayableSource) (core.MediaProbeResult, error) {
	r, g, err := h.record(ctx)
	if err != nil {
		return core.MediaProbeResult{}, err
	}
	var level string
	if r.Manifest.Ether != nil {
		level = normLevel(r.Manifest.Ether.Permissions.Media)
	}
	if err := h.deny(r, g, "media", level, MediaPermissionAllowsProbe); err != nil {
		return core.MediaProbeResult{}, err
	}

	if path := callPathFromContext(ctx); path != "" {
		if !MediaProbeAllowedPath(r.Manifest, path, isBackgroundCall(ctx) || wasm.IsBackgroundCall(ctx)) {
			h.noteDenied("media_client_path")
			return core.MediaProbeResult{}, fmt.Errorf("%w: media.probe cannot run on viewer or client request path %s", ErrPermissionDenied, path)
		}
	}

	if err := ValidateMediaProbeSource(src); err != nil {
		return core.MediaProbeResult{}, err
	}

	h.m.mu.Lock()
	backend := h.m.deps.Backends.MediaProbe
	h.m.mu.Unlock()
	if backend == nil {
		return core.MediaProbeResult{}, ErrUnavailable
	}

	limit := h.m.mediaProbePerMin()
	if !h.m.rateOK(h.m.mediaProbeTimes, h.plugin, limit, time.Minute) {
		return core.MediaProbeResult{}, fmt.Errorf("%w: media.probe exceeds %d requests/minute", ErrRateLimited, limit)
	}

	pctx, cancel := context.WithTimeout(ctx, MediaProbeTimeout)
	defer cancel()
	return backend(pctx, h.plugin, src)
}
