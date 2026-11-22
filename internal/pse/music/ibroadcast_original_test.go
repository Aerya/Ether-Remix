package music

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestIBroadcastOriginalAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		originalStatus, fallbackStatus, calls int
		mime                                  string
	}{
		{"premium", 206, 206, 1, "audio/flac"},
		{"free", 403, 206, 2, "audio/mpeg"},
		{"both refused", 403, 403, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "")
			db := h.srv.o.Library.db
			ib := &IBroadcast{DB: db, Sealer: fakeSealer{}}
			if err := ib.save(t.Context(), ibCredentials{Access: "test-token", Refresh: "test-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(), UserID: 42, Server: "https://streaming.ibroadcast.com"}); err != nil {
				t.Fatal(err)
			}
			id := ibBase + 1
			raw, _ := json.Marshal(ibTrack{ID: id, FileID: 10, Path: "/128/a/b/123", MIME: "audio/flac"})
			if _, err := db.Exec("INSERT INTO pse_ibroadcast_tracks(id,data,account_id) VALUES(?,?,1)", id, string(raw)); err != nil {
				t.Fatal(err)
			}
			if err := mediaaccess.Create(t.Context(), db, "ibroadcast", 7); err != nil {
				t.Fatal(err)
			}
			if err := ReplaceOwned(t.Context(), db, "ibroadcast", []Track{{ID: id, Title: "FLAC"}}); err != nil {
				t.Fatal(err)
			}
			h.srv.o.Source = &OwnedSource{DB: db, IB: ib}
			calls := 0
			h.srv.o.Media = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				path, status := "/orig/a/b/123", tc.originalStatus
				if calls == 2 {
					path, status = "/128/a/b/123", tc.fallbackStatus
				}
				if r.URL.Path != path || r.Header.Get("Range") != "bytes=4-7" || r.URL.Query().Get("file_id") != "10" {
					t.Errorf("incorrect stream request path=%s range=%s", r.URL.Path, r.Header.Get("Range"))
				}
				header := http.Header{}
				body := "denied"
				if status == 206 {
					header.Set("Content-Range", "bytes 4-7/12")
					header.Set("Content-Type", "application/octet-stream")
					body = "0123"
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			p := url.Values{"u": {"alice"}, "p": {"secret"}, "v": {APIVersion}, "c": {"fixture"}, "id": {trackID(id)}}
			w := h.raw("stream", p, http.Header{"Range": {"bytes=4-7"}})
			if calls != tc.calls {
				t.Fatalf("requests=%d, want %d", calls, tc.calls)
			}
			if tc.mime != "" && (w.Code != 206 || w.Body.String() != "0123" || w.Header().Get("Content-Type") != tc.mime || w.Header().Get("Content-Range") != "bytes 4-7/12") {
				t.Fatalf("bad response: %d %v %s", w.Code, w.Header(), w.Body.String())
			}
			if tc.mime == "" && strings.Contains(w.Body.String(), "denied") {
				t.Fatal("provider error body leaked")
			}
		})
	}
}

func TestIBroadcastSkippedURLIgnoresSignatureAndExpiry(t *testing.T) {
	db := store.OpenTest(t)
	ib := &IBroadcast{DB: db, Sealer: fakeSealer{}}
	if err := ib.save(t.Context(), ibCredentials{Access: "new-token", ExpiresAt: time.Now().Add(time.Hour).Unix(), UserID: 42, Server: "https://streaming.ibroadcast.com"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ibTrack{FileID: 10, Path: "/128/a/b/123", MIME: "flac"})
	if _, err := db.Exec("INSERT INTO pse_ibroadcast_tracks(id,data,account_id) VALUES(1,?,1)", string(raw)); err != nil {
		t.Fatal(err)
	}
	ctx := WithSkippedStreams(t.Context(), "https://streaming.ibroadcast.com/orig/a/b/123?Expires=1&Signature=old")
	st, err := ib.Stream(ctx, 1, "LOSSLESS")
	if err != nil || st.Quality != "128" || st.Mime != "audio/mpeg" {
		t.Fatal(st.Quality, st.Mime, err)
	}
	ctx = WithSkippedStreams(ctx, st.URL)
	if _, err = ib.Stream(ctx, 1, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("refused streams retried", err)
	}
	// A new playback starts with the original again: no account-wide downgrade.
	st, err = ib.Stream(t.Context(), 1, "")
	if err != nil || st.Quality != "ORIGINAL" || st.Mime != "audio/flac" {
		t.Fatal(st.Quality, st.Mime, err)
	}
}

func TestIBroadcastOriginalFormats(t *testing.T) {
	for _, tc := range []struct{ input, mime, container, codec string }{
		{"FLAC", "audio/flac", "flac", "flac"},
		{"audio/x-flac", "audio/flac", "flac", "flac"},
		{"audio/mpeg", "audio/mpeg", "mp3", "mp3"},
		{"audio/mp4", "audio/mp4", "m4a", ""},
		{"alac", "audio/mp4", "m4a", "alac"},
		{"audio/ogg", "audio/ogg", "ogg", ""},
		{"audio/opus", "audio/ogg", "ogg", "opus"},
		{"audio/x-wav", "audio/wav", "wav", ""},
		{"audio/x-aiff", "audio/aiff", "aiff", ""},
		{"unknown", "application/octet-stream", "", ""},
	} {
		m, c, k := ibOriginalFormat(tc.input)
		if m != tc.mime || c != tc.container || k != tc.codec {
			t.Errorf("%s: %s %s %s", tc.input, m, c, k)
		}
	}
}
