package silo

import (
	"bytes"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// The Silo app authorizes server HLS by URL shape, not by host. Its media
// scope (PlaybackMediaAuthorization.Scope) admits exactly two kinds of URL
// under a session:
//
//	/api/v2/playback/transcode/{session}/master.m3u8[?silo_rendition=…]
//	/api/v2/playback/transcode/{session}/segment/{name}[?seek=…]
//
// Any other URL gets no credentials, and the app fails the item with
// "resource unavailable" before a single variant is fetched. The session
// layer names its renditions {rung}/index.m3u8 and {rung}/{segment}, so
// every VOD remux or transcode failed in the app: tvOS fetched master.m3u8
// twice and gave up, and the replan ladder ended in "No compatible playback
// route remains for this stream".
//
// siloHLSName folds a session resource path into one segment/ name: "/"
// becomes "~", which never occurs in rung ids or segment names and is a
// character the app accepts in a path component. siloHLSResource reverses it.
const siloHLSDir = "segment/"

func siloHLSName(resource string) string {
	return strings.ReplaceAll(resource, "/", "~")
}

func siloHLSResource(resource string) string {
	name, ok := strings.CutPrefix(resource, siloHLSDir)
	if !ok || !strings.Contains(name, "~") || strings.Contains(name, "/") {
		return resource
	}
	return strings.ReplaceAll(name, "~", "/")
}

// siloHLSRewrite rewrites the URIs of a playlist served for resource so that
// every relative URI lands inside the app's scope. A URI that is already
// in scope, absolute, or carries a query is left alone.
// requested is the path the app fetched (relative URIs resolve against it);
// resource is the session path it maps to.
func siloHLSRewrite(body []byte, requested, resource string) []byte {
	dir := path.Dir(resource)
	if dir == "." {
		dir = ""
	}
	inSegmentDir := strings.HasPrefix(requested, siloHLSDir)
	rewrite := func(uri string) string {
		if uri == "" || strings.ContainsAny(uri, "?#:") || strings.HasPrefix(uri, "/") {
			return uri
		}
		logical := path.Clean(path.Join(dir, uri))
		if strings.HasPrefix(logical, "../") || logical == ".." {
			return uri
		}
		if name, ok := strings.CutPrefix(logical, siloHLSDir); ok && !strings.Contains(name, "/") {
			// Already segment/{name}: keep the client's own spelling.
			return uri
		}
		if inSegmentDir {
			return siloHLSName(logical)
		}
		return siloHLSDir + siloHLSName(logical)
	}
	lines := bytes.Split(body, []byte("\n"))
	for n, raw := range lines {
		line := strings.TrimRight(string(raw), "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "#"):
			start := strings.Index(line, `URI="`)
			if start < 0 {
				continue
			}
			start += len(`URI="`)
			end := strings.IndexByte(line[start:], '"')
			if end < 0 {
				continue
			}
			line = line[:start] + rewrite(line[start:start+end]) + line[start+end:]
		default:
			line = rewrite(trimmed)
		}
		lines[n] = []byte(line)
	}
	return bytes.Join(lines, []byte("\n"))
}

// serveSiloHLS serves a session resource through the app's URL scope:
// segment/ names are mapped back to session paths, and playlists are
// rewritten on the way out.
func serveSiloHLS(w http.ResponseWriter, r *http.Request, requested string, serve func(http.ResponseWriter, *http.Request, string)) {
	resource := siloHLSResource(requested)
	if !strings.HasSuffix(resource, ".m3u8") {
		serve(w, r, resource)
		return
	}
	capture := &captureWriter{header: http.Header{}, status: http.StatusOK}
	serve(capture, r, resource)
	for k, v := range capture.header {
		w.Header()[k] = v
	}
	if capture.status != http.StatusOK || r.Method == http.MethodHead {
		w.Header().Del("Content-Length")
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
		return
	}
	body := siloHLSRewrite(capture.body.Bytes(), requested, resource)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
