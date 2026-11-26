// Package xtreamplayback adapts Xtream's finite archive response to the shared
// archive and app connection services.
package xtreamplayback

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// ArchiveRange parses the path and query variants of the archive request.
// The status is zero for a complete, syntactically valid range.
func ArchiveRange(r *http.Request) (string, time.Time, time.Duration, int) {
	file, duration, start := r.PathValue("file"), r.PathValue("duration"), r.PathValue("start")
	if duration == "" {
		query := r.URL.Query()
		file, duration, start = query.Get("stream"), query.Get("duration"), query.Get("start")
		if file == "" {
			file = query.Get("stream_id")
		}
	}
	id, ext, hasExtension := strings.Cut(file, ".")
	if hasExtension && ext != "ts" && !(ext == "m3u8" && ArchiveHLS(r)) {
		return "", time.Time{}, 0, http.StatusNotFound
	}
	minutes, err := strconv.Atoi(duration)
	if err != nil || minutes <= 0 || minutes > 30*24*60 {
		return "", time.Time{}, 0, http.StatusBadRequest
	}
	at, err := archiveStart(start)
	if err != nil {
		return "", time.Time{}, 0, http.StatusBadRequest
	}
	return id, at, time.Duration(minutes) * time.Minute, 0
}

func archiveStart(text string) (time.Time, error) {
	if epoch, err := strconv.ParseInt(text, 10, 64); err == nil {
		return time.Unix(epoch, 0).UTC(), nil
	}
	for _, layout := range []string{"2006-01-02:15-04", "2006-01-02:15:04", "2006-01-02 15:04:05"} {
		if start, err := time.Parse(layout, text); err == nil {
			return start, nil
		}
	}
	return time.Time{}, frontend.ErrInvalidCatchUpRange
}

// ArchiveHLS selects the HLS form of the path-based archive endpoint.
func ArchiveHLS(r *http.Request) bool {
	return strings.HasSuffix(r.PathValue("file"), ".m3u8")
}

// ServeArchive streams an authorized target and renews its admitted app slot.
func ServeArchive(w http.ResponseWriter, r *http.Request, host frontend.Host, archive frontend.LiveCatchUp, credential string, req frontend.CatchUpRequest, admission frontend.PlanRequest) {
	ctx := r.Context()
	if renewal, ok := host.LazyStreams.(frontend.AppConnectionRenewal); ok {
		streamCtx, release, err := renewal.RenewAppConnection(r.WithContext(ctx), credential, admission)
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, frontend.ErrStreamLimit) {
				status = http.StatusTooManyRequests
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		defer release()
		ctx = streamCtx
	}
	live := host.LiveTV
	hls := ArchiveHLS(r)
	if hls && r.URL.Query().Has("session") {
		serveArchivePart(w, r.WithContext(ctx), live, req, admission)
		return
	}
	info, err := archive.StartCatchUp(ctx, admission.Viewer, req, admission.Caps, admission.Device.ID)
	if err != nil {
		var archive *frontend.CatchUpError
		status := http.StatusServiceUnavailable
		switch {
		case errors.Is(err, frontend.ErrNotFound), errors.Is(err, frontend.ErrUnauthorized):
			status = http.StatusNotFound
		case errors.Is(err, frontend.ErrStreamLimit):
			status = http.StatusTooManyRequests
		case errors.As(err, &archive):
			status = http.StatusConflict
			if archive.Code == "catchup_unavailable" {
				status = http.StatusBadGateway
			}
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	if hls {
		frontend.ServeCatchUpPlaylist(w, r.WithContext(ctx), live, info.ID, admission.Viewer, func(name string) string {
			// Relative to the authenticated path, so credentials need not be
			// copied into the playlist.
			return "?" + url.Values{"session": {info.ID}, "part": {name}}.Encode()
		})
		return
	}
	defer live.Stop(context.WithoutCancel(ctx), info.ID, admission.Viewer)
	archive.ServeCatchUpTS(w, r.WithContext(ctx), info.ID, admission.Viewer)
}

func serveArchivePart(w http.ResponseWriter, r *http.Request, live frontend.LiveTV, req frontend.CatchUpRequest, admission frontend.PlanRequest) {
	id, file := r.URL.Query().Get("session"), r.URL.Query().Get("part")
	if _, ok := frontend.ParseSegmentName(file); !ok || path.Base(file) != file {
		http.NotFound(w, r)
		return
	}
	reader, ok := live.(frontend.CatchUpSessionReader)
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, ok := reader.LibraryCatchUpSession(frontend.SessionAccess{Viewer: admission.Viewer, Device: admission.Device, Protocol: admission.Protocol}, id)
	if !ok || info.ChannelID != req.ChannelID || !info.Start.Equal(req.Start) || info.End.After(req.Start.Add(req.Duration)) {
		http.NotFound(w, r)
		return
	}
	part := r.Clone(r.Context())
	part.URL.Path, part.URL.RawPath = "/"+file, ""
	live.ServeResource(w, part, id, file, admission.Viewer)
}
