package frontend

import (
	"bytes"
	"context"
	"net/http"
	"path"
	"strings"
	"time"
)

type catchUpPlaylistResponse struct {
	header http.Header
	status int
	bytes.Buffer
}

func (w *catchUpPlaylistResponse) Header() http.Header    { return w.header }
func (w *catchUpPlaylistResponse) WriteHeader(status int) { w.status = status }

// ServeCatchUpPlaylist routes the encoder's relative segment and init names
// through the requesting protocol's authenticated media endpoint.
func ServeCatchUpPlaylist(w http.ResponseWriter, r *http.Request, live LiveTV, id string, viewer *Viewer, childURL func(string) string) {
	out := &catchUpPlaylistResponse{header: make(http.Header), status: http.StatusOK}
	live.ServePlaylist(out, r, id, viewer)
	for key, values := range out.header {
		if key != "Content-Length" {
			w.Header()[key] = values
		}
	}
	w.WriteHeader(out.status)
	if r.Method == http.MethodHead {
		return
	}
	if out.status != http.StatusOK {
		_, _ = w.Write(out.Bytes())
		return
	}
	lines := strings.Split(out.String(), "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			before, tail, ok := strings.Cut(line, `URI="`)
			if name, after, closed := strings.Cut(tail, `"`); ok && closed && name == path.Base(name) {
				lines[i] = before + `URI="` + childURL(name) + `"` + after
			}
		case line != "" && !strings.HasPrefix(line, "#") && line == path.Base(line):
			lines[i] = childURL(line)
		}
	}
	_, _ = w.Write([]byte(strings.Join(lines, "\n")))
}

func OpenLibraryCatchUp(ctx context.Context, live LiveTV, archive LiveCatchUp, viewer *Viewer, req CatchUpRequest, caps ClientCaps, session string, position time.Duration) (CatchUpInfo, error) {
	if session == "" {
		session = caps.ClientID
	}
	var info CatchUpInfo
	reused := false
	if finder, ok := live.(CatchUpSessionFinder); ok && session != "" {
		info, reused = finder.FindCatchUp(ctx, viewer, req, caps, session)
	}
	if !reused {
		req.InitialOffset = position
		return archive.StartCatchUp(ctx, viewer, req, caps, session)
	}
	if position == info.Origin {
		return info, nil
	}
	return archive.SeekCatchUp(ctx, viewer, info.ID, position)
}
