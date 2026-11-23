package plex

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Samsung's native DASH player re-requests start.mpd with only ?session=
// after the Plex app fetched it with a token (GitHub #483). Only the same
// client address, for a started session it just received, may read that
// manifest again; nothing else becomes reachable without credentials.
func TestSamsungNativeManifestRefetch(t *testing.T) {
	f, _ := dashFixture()
	defer f.srv.Close()
	headers := []string{"X-Plex-Product", "Plex for Samsung", "X-Plex-Platform", "Tizen", "X-Plex-Client-Profile-Extra", "add-transcode-target(type=videoProfile&context=streaming&protocol=dash&container=mp4&videoCodec=h264&audioCodec=aac)"}
	startPath := "/video/:/transcode/universal/start.mpd?" + dashQuery
	f.get(t, decisionPath(startPath), headers...)
	if rec := f.get(t, startPath, headers...); rec.Code != 200 {
		t.Fatalf("authenticated start %d", rec.Code)
	}
	native := func(target, ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = ip + ":40000"
		req.Header.Set("User-Agent", "SamsungDASH/2.0 (;;;;;)")
		rec := httptest.NewRecorder()
		f.srv.ServeHTTP(rec, req)
		return rec
	}
	if rec := native("/video/:/transcode/universal/start.mpd?session=web-1", "192.168.1.50"); rec.Code != 200 || rec.Body.String() != `<MPD session="web-1"/>` {
		t.Fatalf("same-IP refetch: %d %q", rec.Code, rec.Body.String())
	}
	if rec := native("/video/:/transcode/universal/start.mpd?session=web-1", "192.168.1.77"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other-IP refetch: %d", rec.Code)
	}
	if rec := native("/video/:/transcode/universal/start.mpd?session=nope", "192.168.1.50"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown session: %d", rec.Code)
	}
	if rec := native("/video/:/transcode/universal/decision?session=web-1", "192.168.1.50"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("decision without token: %d", rec.Code)
	}
}
