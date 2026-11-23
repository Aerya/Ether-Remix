package app

import (
	"bytes"
	"context"
	"github.com/braedonsaunders/ether/internal/frontend/kit/mediasafety"
	"github.com/braedonsaunders/ether/internal/transcode"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/clientreg"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/livetv"
)

var liveTagURI = regexp.MustCompile(`URI="([^"]+)"`)

type sessionLive struct {
	service *livetv.Service
	app     *App
	cached  func(context.Context, core.RatingKey) (*core.Title, error)
}

func (l sessionLive) Allowed(ctx context.Context, viewer *core.Viewer, id string) error {
	return l.service.AllowedChannelCached(ctx, viewer, id, l.cached)
}
func (l sessionLive) DirectAllowed(ctx context.Context, viewer *core.Viewer, target *frontend.DirectLiveTarget) error {
	if target == nil || target.LibraryID <= 0 || l.app == nil {
		return core.ErrNotAvailable
	}
	svc := l.app.pluginLiveService()
	if svc == nil {
		return core.ErrNotAvailable
	}
	_, _, err := svc.AllowViewer(ctx, viewer, target.LibraryID, target.Tab)
	return err
}
func (l sessionLive) TuneDirect(ctx context.Context, viewer *core.Viewer, target *frontend.DirectLiveTarget, caps core.ClientCaps, clientSession string) (string, *core.MediaInfo, error) {
	if target == nil || target.URL == "" || l.service == nil {
		return "", nil, core.ErrNotAvailable
	}
	surface := core.LiveApp(ctx)
	libID := target.LibraryID
	tab := target.Tab
	var allowCheck func(context.Context, *core.User) error
	var resolve func(context.Context) (livetv.DirectStreamResult, error)
	if l.app != nil {
		allowCheck = func(checkCtx context.Context, u *core.User) error {
			svc := l.app.pluginLiveService()
			if svc == nil {
				return core.ErrNotAvailable
			}
			_, _, err := svc.AllowViewer(core.WithLiveApp(checkCtx, surface), u, libID, tab)
			return err
		}
		if libID > 0 && target.ItemID != "" && viewer != nil {
			itemID := target.ItemID
			viewerCopy := *viewer
			resolve = func(resolveCtx context.Context) (livetv.DirectStreamResult, error) {
				svc := l.app.pluginLiveService()
				if svc == nil {
					return livetv.DirectStreamResult{}, core.ErrNotAvailable
				}
				lib, _, err := svc.AllowViewer(core.WithLiveApp(resolveCtx, surface), &viewerCopy, libID, tab)
				if err != nil {
					return livetv.DirectStreamResult{}, err
				}
				stream, err := svc.Resolve(resolveCtx, lib, itemID)
				if err != nil {
					return livetv.DirectStreamResult{}, err
				}
				return livetv.DirectStreamResult{URL: stream.URL, Headers: stream.Headers}, nil
			}
		}
	}
	info, err := l.service.TuneDirectStream(ctx, viewer, livetv.DirectStream{
		ID:         target.ItemID,
		Title:      target.Title,
		URL:        target.URL,
		Headers:    target.Headers,
		AllowCheck: allowCheck,
		Resolve:    resolve,
	}, liveTVClientCaps(caps), clientSession)
	return info.ID, info.MediaInfo, err
}
func (l sessionLive) Tune(ctx context.Context, viewer *core.Viewer, id string, caps core.ClientCaps, clientSession string) (string, error) {
	info, err := l.service.TuneWithSession(ctx, viewer, id, liveTVClientCaps(caps), clientSession)
	return info.ID, err
}
func (l sessionLive) TuneMedia(ctx context.Context, viewer *core.Viewer, id string, caps core.ClientCaps, clientSession string) (string, *core.MediaInfo, error) {
	info, err := l.service.TuneWithSession(ctx, viewer, id, liveTVClientCaps(caps), clientSession)
	return info.ID, info.MediaInfo, err
}
func (l sessionLive) Stop(ctx context.Context, viewer *core.Viewer, id string) error {
	return l.service.Stop(ctx, id, viewer)
}

func normalizeLiveTail(tail string) string {
	if rest, ok := strings.CutPrefix(tail, "resource-"); ok && rest != "" && !strings.Contains(rest, "/") {
		return "resource/" + rest
	}
	if rest, ok := strings.CutPrefix(tail, "relay-"); ok && rest != "" && !strings.Contains(rest, "/") {
		return "relay/" + rest
	}
	return tail
}

func (l sessionLive) Serve(w http.ResponseWriter, r *http.Request, viewer *core.Viewer, id, tail, base string) {
	mediasafety.SetHeaders(w)
	// Live TV's Plex adapter can propagate Plex credentials into playlists.
	// Scoped broker paths carry the grant already, so strip credential inputs.
	clean := r.Clone(r.Context())
	clean.Header = r.Header.Clone()
	clean.Header.Del("X-Plex-Token")
	clean.URL.RawQuery = ""
	if offset := r.URL.Query().Get("offset"); offset != "" {
		clean.URL.RawQuery = url.Values{"offset": []string{offset}}.Encode()
	}
	if normalizeLiveTail(tail) == "stream.ts" {
		// Continuous MPEG-TS for IPTV apps that ask for ".ts". Timelines that
		// are not MPEG-TS fall back to the HLS playlist beside it.
		if !l.service.ServeContinuous(w, clean, id, viewer) {
			http.Redirect(w, clean, "index.m3u8", http.StatusTemporaryRedirect)
		}
		return
	}
	sessionPrefix := "/livetv/sessions/" + id + "/0/"
	p := &livePlaylistWriter{
		ResponseWriter: w,
		from:           []byte(sessionPrefix),
		to:             []byte(base),
		flatSegments:   strings.HasSuffix(base, "/segment/"),
		sessionPrefix:  sessionPrefix,
		base:           base,
	}
	defer p.finish()
	tail = normalizeLiveTail(tail)
	parts := strings.Split(tail, "/")
	switch {
	case tail == "index.m3u8":
		l.service.ServePlaylist(p, clean, id, viewer)
	case len(parts) == 2 && parts[0] == "resource" && parts[1] != "":
		l.service.ServeResource(p, clean, id, parts[1], viewer)
	case len(parts) == 2 && parts[0] == "relay":
		n, ok := transcode.ParseSegmentName(parts[1])
		if !ok {
			http.NotFound(p, clean)
			return
		}
		l.service.ServeRelaySegment(p, clean, id, n, viewer)
	default:
		http.NotFound(p, clean)
	}
}

type livePlaylistWriter struct {
	http.ResponseWriter
	from, to          []byte
	flatSegments      bool
	sessionPrefix     string
	base              string
	buf               bytes.Buffer
	decided, playlist bool
	status            int
}

func (w *livePlaylistWriter) decide() {
	if !w.decided {
		w.decided = true
		w.playlist = strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "mpegurl")
		if w.playlist {
			w.Header().Del("Content-Length")
		}
	}
}
func (w *livePlaylistWriter) WriteHeader(status int) {
	w.decide()
	if w.playlist {
		w.status = status
	} else {
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *livePlaylistWriter) Write(p []byte) (int, error) {
	w.decide()
	if w.playlist {
		return w.buf.Write(p)
	}
	return w.ResponseWriter.Write(p)
}
func (w *livePlaylistWriter) Flush() {
	if !w.playlist {
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush()
		}
	}
}
func (w *livePlaylistWriter) finish() {
	if w.playlist {
		if w.status != 0 {
			w.ResponseWriter.WriteHeader(w.status)
		}
		if w.flatSegments {
			_, _ = w.ResponseWriter.Write(rewriteSiloLivePlaylist(w.buf.Bytes(), w.sessionPrefix, w.base))
			return
		}
		_, _ = w.ResponseWriter.Write(bytes.ReplaceAll(w.buf.Bytes(), w.from, w.to))
	}
}

func rewriteSiloLivePlaylist(body []byte, sessionPrefix, base string) []byte {
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if !strings.HasPrefix(trim, "#") {
			lines[i] = rewriteSiloLiveURI(trim, sessionPrefix, base)
			continue
		}
		lines[i] = liveTagURI.ReplaceAllStringFunc(line, func(m string) string {
			parts := liveTagURI.FindStringSubmatch(m)
			if len(parts) < 2 {
				return m
			}
			return `URI="` + rewriteSiloLiveURI(parts[1], sessionPrefix, base) + `"`
		})
	}
	return []byte(strings.Join(lines, "\n"))
}

func rewriteSiloLiveURI(raw, sessionPrefix, base string) string {
	rel := strings.TrimSpace(raw)
	rel = strings.TrimPrefix(rel, sessionPrefix)
	rel, _, _ = strings.Cut(rel, "?")
	switch {
	case rel == "index.m3u8":
		return base + "index.m3u8"
	case strings.HasPrefix(rel, "resource/"):
		key := strings.TrimPrefix(rel, "resource/")
		if key != "" && !strings.Contains(key, "/") {
			return base + "resource-" + key
		}
	case strings.HasPrefix(rel, "relay/"):
		seg := strings.TrimPrefix(rel, "relay/")
		if seg != "" && !strings.Contains(seg, "/") {
			return base + "relay-" + seg
		}

	}
	return strings.ReplaceAll(raw, sessionPrefix, base)
}

// liveTVClientCaps supplies frontend delivery requirements to the neutral host.
func liveTVClientCaps(caps core.ClientCaps) core.ClientCaps {
	policy := frontendHostPolicy(caps.Protocol)
	if policy.ProbeLiveInput {
		caps.ProbeLiveInput = true
	}
	if policy.BestEffortLiveStart {
		caps.BestEffortLiveStart = true
	}
	caps.ProbeLiveWithoutCodecs = caps.ProbeLiveWithoutCodecs || clientreg.HasQuirk(clientreg.Identity{Product: caps.Product, Platform: caps.Platform, Device: caps.Device, Version: caps.Version}, "probe-hls-without-codecs")
	return caps
}

func (l sessionLive) LiveWindow(ctx context.Context, id string) (frontend.LiveWindow, error) {
	return l.service.LiveWindow(ctx, id)
}
