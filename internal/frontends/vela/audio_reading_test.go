package vela

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeKindStreams struct {
	openFn func(context.Context, *core.Viewer, core.RatingKey, []string) (*frontend.KindMedia, error)
}

func (f fakeKindStreams) OpenKind(ctx context.Context, viewer *core.Viewer, key core.RatingKey, accept []string) (*frontend.KindMedia, error) {
	return f.openFn(ctx, viewer, key, accept)
}

func (f fakeKindStreams) KindLeaves(context.Context, *core.Viewer, core.RatingKey) ([]frontend.KindLeaf, error) {
	return nil, nil
}

type readingExpFake struct {
	frontendtest.Experience
	reading map[core.RatingKey]core.ReadingProgress
	writes  int
}

func (r *readingExpFake) SetReadingProgress(_ context.Context, _ *frontend.Viewer, key frontend.RatingKey, progress frontend.ReadingProgress) error {
	r.writes++
	if r.reading == nil {
		r.reading = map[core.RatingKey]core.ReadingProgress{}
	}
	r.reading[key] = progress
	return nil
}

func sampleEPUB(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("application/epub+zip"))
	ch, err := zw.CreateHeader(&zip.FileHeader{Name: "OEBPS/ch1.xhtml", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ch.Write([]byte("<html><body>Chapter 1 of Dune</body></html>"))
	_ = zw.Close()
	return buf.Bytes()
}

func TestVelaCapabilitiesAudioAndReading(t *testing.T) {
	db := store.OpenTest(t)
	inst, err := (Frontend{DB: db, ReplayKey: bytes.Repeat([]byte{0x42}, 32)}).Mount(context.Background(), frontend.Host{
		Identity:   identityFake{},
		Experience: &readingExpFake{},
		Sessions:   frontendtest.Sessions{},
		KindStreams: fakeKindStreams{openFn: func(context.Context, *core.Viewer, core.RatingKey, []string) (*frontend.KindMedia, error) {
			return nil, core.ErrNotFound
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })

	req := httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil)
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("X-Vela-Device-Id", "dev1")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	features, _ := payload["features"].(map[string]any)
	audio, _ := features["audioPlayback"].(map[string]any)
	if audio == nil || audio["available"] != true {
		t.Fatalf("audioPlayback capability = %v (features=%v)", features["audioPlayback"], features)
	}
	reading, _ := features["reading"].(map[string]any)
	if reading == nil || reading["available"] != true {
		t.Fatalf("reading capability = %v", features["reading"])
	}
}

func TestVelaBookDetailCanPlayFalseRefusesPlaybackAndReadsViaGrant(t *testing.T) {
	db := store.OpenTest(t)
	bookKey := core.PluginKey(101)
	movieKey := core.MovieKey(202)
	epubBytes := sampleEPUB(t)

	var planCalls atomic.Int32
	var preparing atomic.Bool
	yes := true

	exp := &readingExpFake{}
	exp.ItemFunc = func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
		if req.Key == bookKey {
			st := frontend.WatchStatus{}
			if p, ok := exp.reading[bookKey]; ok {
				cp := p
				st.Reading = &cp
			}
			return frontend.ItemDetail{
				Title: frontend.Title{
					Key:        bookKey,
					Type:       frontend.TypeMovie,
					Title:      "The Way of Kings",
					MediaClass: core.MediaClassBook,
					MBItemType: "Book",
				},
				UserData: st,
				CanPlay:  &yes, // Even if upstream says true, Vela MUST force canPlay=false for books!
			}, nil
		}
		return frontend.ItemDetail{Title: frontend.Title{Key: movieKey, Type: frontend.TypeMovie, Title: "Film"}}, nil
	}

	sessions := frontendtest.Sessions{
		PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
			planCalls.Add(1)
			return frontend.PlayPlan{}, nil
		},
	}

	ks := fakeKindStreams{
		openFn: func(_ context.Context, _ *core.Viewer, key core.RatingKey, _ []string) (*frontend.KindMedia, error) {
			if preparing.Load() {
				return nil, &frontend.PreparingError{RetryAfter: 4 * time.Second}
			}
			if key == bookKey {
				return &frontend.KindMedia{
					ReaderAt: bytes.NewReader(epubBytes),
					Size:     int64(len(epubBytes)),
					Name:     "way-of-kings.epub",
					MIME:     "application/epub+zip",
				}, nil
			}
			return nil, core.ErrNotFound
		},
	}

	inst, err := (Frontend{DB: db, ReplayKey: bytes.Repeat([]byte{0x42}, 32)}).Mount(context.Background(), frontend.Host{
		Identity:       identityFake{},
		Experience:     exp,
		Sessions:       sessions,
		Editions:       &versionsEditionsFake{},
		VersionHandles: &versionsHandlesFake{},
		Capabilities:   frontendtest.Capabilities{},
		KindStreams:    ks,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })

	doAuth := func(method, target, body, idem string) *httptest.ResponseRecorder {
		t.Helper()
		var rbody io.Reader
		if body != "" {
			rbody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, rbody)
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("X-Vela-Device-Id", "dev1")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
		rec := httptest.NewRecorder()
		inst.Handler().ServeHTTP(rec, req)
		return rec
	}

	bookID := ids.RatingKeyGUID(bookKey)
	movieID := ids.RatingKeyGUID(movieKey)

	// 0. GET /vela/v0/titles/{bookID} MUST advertise canPlay: false AND canRead: true!
	detailRec := doAuth(http.MethodGet, "/vela/v0/titles/"+bookID, "", "")
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET book detail = (%d) %s", detailRec.Code, detailRec.Body.String())
	}
	var detailWire map[string]any
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detailWire); err != nil {
		t.Fatal(err)
	}
	if detailWire["canPlay"] != false || detailWire["canRead"] != true {
		t.Fatalf("book detail canPlay=%v canRead=%v, want false/true (body=%s)", detailWire["canPlay"], detailWire["canRead"], detailRec.Body.String())
	}

	// 1. POST /vela/v0/playback and GET /versions on a book are refused with 400 not_playable and clear reading message.
	pbRec := doAuth(http.MethodPost, "/vela/v0/playback", `{"idempotencyKey":"idem-pb-book","titleId":"`+bookID+`","client":{"device":{"id":"dev1"}}}`, "")
	if pbRec.Code != http.StatusBadRequest || !strings.Contains(pbRec.Body.String(), `"not_playable"`) || !strings.Contains(pbRec.Body.String(), "reading") {
		t.Fatalf("book playback = (%d) %s, want 400 not_playable mentioning reading", pbRec.Code, pbRec.Body.String())
	}
	if planCalls.Load() != 0 {
		t.Fatalf("book playback called Sessions.Plan %d times, want 0", planCalls.Load())
	}
	verRec := doAuth(http.MethodGet, "/vela/v0/titles/"+bookID+"/versions", "", "")
	if verRec.Code != http.StatusBadRequest || !strings.Contains(verRec.Body.String(), `"not_playable"`) {
		t.Fatalf("book versions = (%d) %s, want 400 not_playable", verRec.Code, verRec.Body.String())
	}

	// 2. Transient ErrPreparing on /read returns 503 + Retry-After and does NOT pin in vela_replays.
	preparing.Store(true)
	prepRec := doAuth(http.MethodPost, "/vela/v0/titles/"+bookID+"/read", `{"idempotencyKey":"idem-read-retry"}`, "")
	if prepRec.Code != http.StatusServiceUnavailable || prepRec.Header().Get("Retry-After") != "4" {
		t.Fatalf("preparing /read = (%d, Retry-After=%q) %s", prepRec.Code, prepRec.Header().Get("Retry-After"), prepRec.Body.String())
	}
	preparing.Store(false)

	// 3. Reusing the same idempotencyKey now succeeds and mints a ReadingGrant!
	readRec := doAuth(http.MethodPost, "/vela/v0/titles/"+bookID+"/read", `{"idempotencyKey":"idem-read-retry"}`, "")
	if readRec.Code != http.StatusOK {
		t.Fatalf("POST /read = (%d) %s", readRec.Code, readRec.Body.String())
	}
	var grant struct {
		URL        string `json:"url"`
		Format     string `json:"format"`
		MIMEType   string `json:"mimeType"`
		ByteLength int64  `json:"byteLength"`
		ExpiresAt  string `json:"expiresAt"`
	}
	if err := json.Unmarshal(readRec.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(grant.URL, "/vela/v0/read/") || grant.Format != "epub" || grant.MIMEType != "application/epub+zip" || grant.ByteLength != int64(len(epubBytes)) || grant.ExpiresAt == "" {
		t.Fatalf("unexpected ReadingGrant: %+v", grant)
	}

	// 4. GET /vela/v0/read/{ticket} works WITHOUT Bearer/Device headers, supports Range/206, and sets security headers!
	rangeReq := httptest.NewRequest(http.MethodGet, grant.URL, nil)
	rangeReq.Header.Set("Range", "bytes=0-9")
	rangeRec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent {
		t.Fatalf("GET read ticket range = %d, want 206", rangeRec.Code)
	}
	if rangeRec.Header().Get("Content-Type") != "application/epub+zip" ||
		rangeRec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rangeRec.Header().Get("Content-Security-Policy") != "sandbox" ||
		rangeRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read ticket headers = %v", rangeRec.Header())
	}
	if !bytes.Equal(rangeRec.Body.Bytes(), epubBytes[:10]) {
		t.Fatalf("range body mismatch: got %x want %x", rangeRec.Body.Bytes(), epubBytes[:10])
	}

	// HEAD also succeeds with exact MIME and matching strong ETag for If-Range resume.
	headReq := httptest.NewRequest(http.MethodHead, grant.URL, nil)
	headRec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(headRec, headReq)
	readETag := headRec.Header().Get("ETag")
	if headRec.Code != http.StatusOK || headRec.Header().Get("Content-Type") != "application/epub+zip" ||
		readETag == "" || strings.HasPrefix(readETag, "W/") || readETag != rangeRec.Header().Get("ETag") {
		t.Fatalf("HEAD read ticket = (%d, %q, etag=%q vs %q)", headRec.Code, headRec.Header().Get("Content-Type"), readETag, rangeRec.Header().Get("ETag"))
	}
	ifRangeReq := httptest.NewRequest(http.MethodGet, grant.URL, nil)
	ifRangeReq.Header.Set("Range", "bytes=10-19")
	ifRangeReq.Header.Set("If-Range", readETag)
	ifRangeRec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(ifRangeRec, ifRangeReq)
	if ifRangeRec.Code != http.StatusPartialContent || !bytes.Equal(ifRangeRec.Body.Bytes(), epubBytes[10:20]) {
		t.Fatalf("If-Range read ticket = (%d, len=%d)", ifRangeRec.Code, ifRangeRec.Body.Len())
	}

	// 5. Non-book title refused on /read with 400 not_readable.
	nonBookRec := doAuth(http.MethodPost, "/vela/v0/titles/"+movieID+"/read", `{"idempotencyKey":"idem-read-movie"}`, "")
	if nonBookRec.Code != http.StatusBadRequest || !strings.Contains(nonBookRec.Body.String(), `"not_readable"`) {
		t.Fatalf("non-book /read = (%d) %s", nonBookRec.Code, nonBookRec.Body.String())
	}

	// 6. POST /vela/v0/titles/{id}/reading-progress persists progress and surfaces in watchWire!
	progRec := doAuth(http.MethodPost, "/vela/v0/titles/"+bookID+"/reading-progress",
		`{"idempotencyKey":"idem-prog-1","locator":"epubcfi(/6/4!/4/2)","fraction":0.35,"label":"Chapter 2"}`, "")
	if progRec.Code != http.StatusOK || !strings.Contains(progRec.Body.String(), `"accepted":true`) {
		t.Fatalf("reading-progress = (%d) %s", progRec.Code, progRec.Body.String())
	}
	if exp.writes != 1 || exp.reading[bookKey].Fraction != 0.35 {
		t.Fatalf("exp.reading = %+v (writes=%d)", exp.reading, exp.writes)
	}

	wire := watchWire(frontend.WatchStatus{Reading: &core.ReadingProgress{Locator: "epubcfi(/6/4!/4/2)", Fraction: 0.35, Label: "Chapter 2"}})
	if wire["reading"] == nil {
		t.Fatalf("watchWire missing reading: %+v", wire)
	}
}

func TestVelaAudioPlaybackEmitsMediaAudioFileHTTPFallbackHLSAndM4BChapters(t *testing.T) {
	db := store.OpenTest(t)
	trackKey := core.PluginKey(303)
	m4bInfo := frontend.MediaInfo{
		Container:   "mp4",
		Duration:    45 * time.Minute,
		Size:        40 << 20,
		BitrateKbps: 128,
		Streams:     []frontend.StreamInfo{{Index: 0, Type: "audio", Codec: "aac", Channels: 2, Default: true}},
		Chapters: []core.Chapter{
			{Title: "Chapter 1", Start: 0, End: 15 * time.Minute},
			{Title: "Chapter 2", Start: 15 * time.Minute, End: 45 * time.Minute},
		},
	}

	var startedFormats []frontend.StreamFormat
	exp := &readingExpFake{}
	exp.ItemFunc = func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{
			Title: frontend.Title{
				Key:        trackKey,
				Type:       frontend.TypeMovie,
				Title:      "Dune Audiobook",
				MediaClass: core.MediaClassAudiobook,
			},
		}, nil
	}
	sessions := frontendtest.Sessions{
		PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
			return frontend.PlayPlan{
				ID:            "plan-audio",
				Media:         m4bInfo,
				Decision:      frontend.Decision{Kind: core.DecisionTranscode, AudioCodec: "aac", Container: "mp4"},
				HTTP:          true,
				HTTPContainer: "mp4",
				HLS:           true,
			}, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "sess-audio"}, nil
		},
		StartStreamFunc: func(_ context.Context, _ frontend.SessionAccess, _ string, req frontend.StreamRequest) (string, error) {
			startedFormats = append(startedFormats, req.Format)
			if req.Format == frontend.StreamHTTP {
				return "/sessions/media/sess-audio/g/stream.m4a", nil
			}
			return "/sessions/media/sess-audio/g/master.m3u8", nil
		},
	}

	inst, err := (Frontend{DB: db, ReplayKey: bytes.Repeat([]byte{0x42}, 32)}).Mount(context.Background(), frontend.Host{
		Identity:     identityFake{},
		Experience:   exp,
		Sessions:     sessions,
		Capabilities: frontendtest.Capabilities{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })

	req := httptest.NewRequest(http.MethodPost, "/vela/v0/playback", strings.NewReader(`{
		"idempotencyKey":"idem-audio-1",
		"titleId":"`+ids.RatingKeyGUID(trackKey)+`",
		"client":{"device":{"id":"dev1"},"platform":"web","hls":true,"audioCodecs":["aac","opus"],"transports":["file","hls"]}
	}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("X-Vela-Device-Id", "dev1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audio playback = (%d) %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["media"] != "audio" {
		t.Fatalf("media = %v, want audio; body=%s", resp["media"], rec.Body.String())
	}
	if _, hasVideo := resp["originalVideo"]; hasVideo {
		t.Fatalf("audio playback should omit originalVideo: %v", resp["originalVideo"])
	}
	paths, _ := resp["paths"].([]any)
	if len(paths) < 2 {
		t.Fatalf("audio paths = %+v, want primary file + fallback-hls", resp["paths"])
	}
	primary, _ := paths[0].(map[string]any)
	fallback, _ := paths[1].(map[string]any)
	if primary["transport"] != "file" || !strings.Contains(primary["url"].(string), "/stream.m4a") {
		t.Fatalf("primary audio path = %+v", primary)
	}
	if fallback["id"] != "fallback-hls" || fallback["transport"] != "hls" {
		t.Fatalf("fallback audio path = %+v", fallback)
	}
	chapters, _ := resp["chapters"].([]any)
	if len(chapters) != 2 {
		t.Fatalf("audio M4B chapters = %+v, want 2", resp["chapters"])
	}
	if len(startedFormats) != 1 || startedFormats[0] != frontend.StreamHTTP {
		t.Fatalf("startedFormats = %v, want [http]", startedFormats)
	}
}
