package mediabrowser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
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
)

func TestMediaBrowserBookRefusalAudioTranscodeUniversalAndReadingPercentage(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	bookKey := core.PluginKey(401)
	trackKey := core.PluginKey(402)
	var planCalls atomic.Int32
	var startedFormat frontend.StreamFormat

	flacInfo := frontend.MediaInfo{
		Container:   "flac",
		Duration:    5 * time.Minute,
		Size:        30 << 20,
		BitrateKbps: 1000,
		Streams:     []frontend.StreamInfo{{Index: 0, Type: "audio", Codec: "flac", Channels: 2, Default: true}},
		Chapters:    []core.Chapter{{Title: "Part 1", Start: 0, End: 5 * time.Minute}},
	}

	exp := frontendtest.Experience{
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			if req.Key == bookKey {
				return frontend.ItemDetail{
					Title: frontend.Title{Key: bookKey, Type: frontend.TypeMovie, Title: "Dune", MediaClass: core.MediaClassBook, MBItemType: "Book"},
				}, nil
			}
			return frontend.ItemDetail{
				Title: frontend.Title{Key: trackKey, Type: frontend.TypeMovie, Title: "You Must Burn!", MediaClass: core.MediaClassMusic, MBItemType: "Audio"},
			}, nil
		},
	}

	sessions := frontendtest.Sessions{
		PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
			planCalls.Add(1)
			return frontend.PlayPlan{
				ID:            "plan-mb-audio",
				Media:         flacInfo,
				Decision:      frontend.Decision{Kind: core.DecisionTranscode, AudioCodec: "mp3", Container: "mp3", AudioBitrateKbps: 192},
				HTTP:          true,
				HTTPContainer: "mp3",
				HLS:           true,
			}, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "sess-mb-audio"}, nil
		},
		StartStreamFunc: func(_ context.Context, _ frontend.SessionAccess, _ string, req frontend.StreamRequest) (string, error) {
			startedFormat = req.Format
			return "/sessions/media/sess-mb-audio/g/stream.mp3", nil
		},
		ServeMediaFunc: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/mpeg")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ID3-mp3-audio"))
		},
	}

	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:     ident,
		Experience:   exp,
		Sessions:     sessions,
		Capabilities: frontendtest.Capabilities{},
		SurfaceURL:   func(context.Context, *http.Request) (string, error) { return "https://ether.example/jellyfin", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. PlaybackInfo on a Book returns ErrorCode: NoCompatibleStream without touching Sessions.Plan.
	bookPath := "/jellyfin/Items/" + ids.RatingKeyGUID(bookKey) + "/PlaybackInfo"
	req := httptest.NewRequest(http.MethodPost, bookPath, strings.NewReader(`{"IsPlayback":true}`))
	req.Header.Set("X-Emby-Token", "grant")
	rec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ErrorCode":"NoCompatibleStream"`) {
		t.Fatalf("book PlaybackInfo = (%d) %s", rec.Code, rec.Body.String())
	}
	if planCalls.Load() != 0 {
		t.Fatalf("book PlaybackInfo invoked Sessions.Plan %d times, want 0", planCalls.Load())
	}

	// 2. PlaybackInfo on audio-only FLAC needing transcode selects StreamHTTP + TranscodingSubProtocol="http".
	trackPath := "/jellyfin/Items/" + ids.RatingKeyGUID(trackKey) + "/PlaybackInfo"
	req2 := httptest.NewRequest(http.MethodPost, trackPath, strings.NewReader(`{"IsPlayback":true,"EnableDirectPlay":true,"EnableTranscoding":true}`))
	req2.Header.Set("X-Emby-Token", "grant")
	rec2 := httptest.NewRecorder()
	inst.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("audio PlaybackInfo = (%d) %s", rec2.Code, rec2.Body.String())
	}
	var pbResp struct {
		MediaSources []map[string]any `json:"MediaSources"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &pbResp); err != nil {
		t.Fatal(err)
	}
	if len(pbResp.MediaSources) != 1 {
		t.Fatalf("MediaSources = %+v", pbResp.MediaSources)
	}
	ms := pbResp.MediaSources[0]
	if startedFormat != frontend.StreamHTTP {
		t.Fatalf("startedFormat = %v, want http", startedFormat)
	}
	if ms["TranscodingSubProtocol"] != "http" || !strings.Contains(ms["TranscodingUrl"].(string), "/stream.mp3") {
		t.Fatalf("audio MediaSource = %+v", ms)
	}

	// 3. Standalone GET /Audio/{id}/universal streams audio without prior PlaybackInfo!
	univPath := "/jellyfin/Audio/" + ids.RatingKeyGUID(trackKey) + "/universal?api_key=grant&DeviceId=finamp-1&transcodingContainer=mp3&audioCodec=mp3&maxStreamingBitrate=192000"
	uReq := httptest.NewRequest(http.MethodGet, univPath, nil)
	uRec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(uRec, uReq)
	if uRec.Code != http.StatusOK || uRec.Header().Get("Content-Type") != "audio/mpeg" || uRec.Body.String() != "ID3-mp3-audio" {
		t.Fatalf("GET /Audio/{id}/universal = (%d, %q, %q)", uRec.Code, uRec.Header().Get("Content-Type"), uRec.Body.String())
	}

	// 4. userDataDTO projects Reading.Fraction into PlayedPercentage (0..100).
	ud := (&instance{}).userDataDTO(ids.RatingKeyGUID(bookKey), frontend.WatchStatus{
		Reading: &core.ReadingProgress{Locator: "epubcfi(/6/4)", Fraction: 0.42},
	})
	if pct, ok := ud["PlayedPercentage"].(float64); !ok || pct < 41.9 || pct > 42.1 {
		t.Fatalf("userDataDTO PlayedPercentage = %v, want 42", ud["PlayedPercentage"])
	}
}

type fakeBookKindStreams struct {
	openFn func(context.Context, *core.Viewer, core.RatingKey, []string) (*frontend.KindMedia, error)
}

func (f fakeBookKindStreams) OpenKind(ctx context.Context, viewer *core.Viewer, key core.RatingKey, accept []string) (*frontend.KindMedia, error) {
	return f.openFn(ctx, viewer, key, accept)
}

func (f fakeBookKindStreams) KindLeaves(context.Context, *core.Viewer, core.RatingKey) ([]frontend.KindLeaf, error) {
	return nil, nil
}

func sampleBookEPUB(t *testing.T) []byte {
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
	_, _ = ch.Write([]byte("<html><body>Dune</body></html>"))
	_ = zw.Close()
	return buf.Bytes()
}

func TestMediaBrowserBookDetailAndDownloadFile(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	userID, err := ids.GUID(ids.User, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	bookKey := core.PluginKey(501)
	pdfKey := core.PluginKey(502)
	unavailKey := core.PluginKey(503)
	movieKey := core.MovieKey(504)
	epubBytes := sampleBookEPUB(t)
	pdfBytes := []byte("%PDF-1.7\nhello world\n%%EOF\n")

	var preparing atomic.Bool
	yes, no := true, false

	exp := frontendtest.Experience{
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			switch req.Key {
			case bookKey:
				return frontend.ItemDetail{
					Title: frontend.Title{
						Key:          bookKey,
						Type:         frontend.TypeMovie,
						Title:        "Dune",
						MediaClass:   core.MediaClassBook,
						MBItemType:   "Book",
						MediaReading: &core.TitleReading{Format: "epub", Pages: 606},
					},
					CanPlay: &no,
					CanRead: &yes,
				}, nil
			case pdfKey:
				return frontend.ItemDetail{
					Title: frontend.Title{
						Key:          pdfKey,
						Type:         frontend.TypeMovie,
						Title:        "Manual",
						MediaClass:   core.MediaClassBook,
						MBItemType:   "Book",
						MediaReading: &core.TitleReading{Format: "pdf"},
					},
					CanPlay: &no,
					CanRead: &yes,
				}, nil
			case unavailKey:
				return frontend.ItemDetail{
					Title: frontend.Title{
						Key:          unavailKey,
						Type:         frontend.TypeMovie,
						Title:        "Missing Book",
						MediaClass:   core.MediaClassBook,
						MBItemType:   "Book",
						MediaReading: &core.TitleReading{Format: "epub"},
					},
					CanPlay:     &no,
					CanRead:     &no,
					Unavailable: &core.ItemUnavailable{Code: "removed"},
				}, nil
			default:
				return frontend.ItemDetail{
					Title: frontend.Title{Key: movieKey, Type: frontend.TypeMovie, Title: "Film"},
				}, nil
			}
		},
	}

	ks := fakeBookKindStreams{
		openFn: func(_ context.Context, _ *core.Viewer, key core.RatingKey, accept []string) (*frontend.KindMedia, error) {
			if preparing.Load() {
				return nil, &frontend.PreparingError{RetryAfter: 3 * time.Second}
			}
			switch key {
			case bookKey:
				return &frontend.KindMedia{
					ReaderAt: bytes.NewReader(epubBytes),
					Size:     int64(len(epubBytes)),
					Name:     "Dune.epub",
					MIME:     "application/epub+zip",
				}, nil
			case pdfKey:
				return &frontend.KindMedia{
					ReaderAt: bytes.NewReader(pdfBytes),
					Size:     int64(len(pdfBytes)),
					Name:     "Manual.pdf",
					MIME:     "application/pdf",
				}, nil
			default:
				return nil, core.ErrNotFound
			}
		},
	}

	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	host := frontend.Host{
		Identity:     ident,
		Experience:   exp,
		KindStreams:  ks,
		Capabilities: frontendtest.Capabilities{},
	}

	jf, err := (Jellyfin{MachineID: "jf"}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	em, err := (Emby{MachineID: "em"}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}

	bookID := ids.RatingKeyGUID(bookKey)
	pdfID := ids.RatingKeyGUID(pdfKey)
	unavailID := ids.RatingKeyGUID(unavailKey)
	movieID := ids.RatingKeyGUID(movieKey)

	// 1. Jellyfin detail exposes Path/Container/CanDownload/PlayAccess:Full without MediaSources.
	detailReq := httptest.NewRequest(http.MethodGet, "/Users/"+userID+"/Items/"+bookID+"?api_key=grant", nil)
	detailRec := httptest.NewRecorder()
	jf.Handler().ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("jellyfin book detail = (%d) %s", detailRec.Code, detailRec.Body.String())
	}
	var jfDTO map[string]any
	if err := json.Unmarshal(detailRec.Body.Bytes(), &jfDTO); err != nil {
		t.Fatal(err)
	}
	if jfDTO["Type"] != "Book" || jfDTO["MediaType"] != "Book" || jfDTO["Path"] != "/Books/Dune.epub" ||
		jfDTO["Container"] != "epub" || jfDTO["CanDownload"] != true || jfDTO["PlayAccess"] != "Full" {
		t.Fatalf("unexpected jellyfin book detail: %+v", jfDTO)
	}
	if _, hasSources := jfDTO["MediaSources"]; hasSources {
		t.Fatalf("book detail must not advertise MediaSources: %+v", jfDTO["MediaSources"])
	}

	// Emby detail also exposes MimeType and FileName for books.
	emReq := httptest.NewRequest(http.MethodGet, "/emby/Users/"+userID+"/Items/"+bookID+"?api_key=grant", nil)
	emRec := httptest.NewRecorder()
	em.Handler().ServeHTTP(emRec, emReq)
	if emRec.Code != http.StatusOK {
		t.Fatalf("emby book detail = (%d) %s", emRec.Code, emRec.Body.String())
	}
	var emDTO map[string]any
	if err := json.Unmarshal(emRec.Body.Bytes(), &emDTO); err != nil {
		t.Fatal(err)
	}
	if emDTO["Path"] != "/Books/Dune.epub" || emDTO["Container"] != "epub" || emDTO["CanDownload"] != true ||
		emDTO["MimeType"] != "application/epub+zip" || emDTO["FileName"] != "Dune.epub" {
		t.Fatalf("unexpected emby book detail: %+v", emDTO)
	}

	// Unavailable book omits Path/Container and sets CanDownload:false / PlayAccess:None.
	uReq := httptest.NewRequest(http.MethodGet, "/Users/"+userID+"/Items/"+unavailID+"?api_key=grant", nil)
	uRec := httptest.NewRecorder()
	jf.Handler().ServeHTTP(uRec, uReq)
	var uDTO map[string]any
	if err := json.Unmarshal(uRec.Body.Bytes(), &uDTO); err != nil {
		t.Fatal(err)
	}
	if uDTO["CanDownload"] != false || uDTO["PlayAccess"] != "None" {
		t.Fatalf("unavailable book detail flags = %+v", uDTO)
	}
	if _, hasPath := uDTO["Path"]; hasPath {
		t.Fatalf("unavailable book must omit Path: %+v", uDTO["Path"])
	}

	// 2. GET/HEAD /Items/{id}/Download and /Items/{id}/File serve EPUB & PDF with range + safety headers.
	dlReq := httptest.NewRequest(http.MethodGet, "/Items/"+bookID+"/Download?api_key=grant", nil)
	dlRec := httptest.NewRecorder()
	jf.Handler().ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK || dlRec.Header().Get("Content-Type") != "application/epub+zip" ||
		!strings.HasPrefix(dlRec.Header().Get("Content-Disposition"), "attachment;") ||
		dlRec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(dlRec.Header().Get("Content-Security-Policy"), "sandbox") ||
		dlRec.Header().Get("Cache-Control") != "no-store" ||
		!bytes.Equal(dlRec.Body.Bytes(), epubBytes) {
		t.Fatalf("GET /Download = (%d, headers=%v, len=%d)", dlRec.Code, dlRec.Header(), dlRec.Body.Len())
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "/emby/Items/"+pdfID+"/File?api_key=grant", nil)
	rangeReq.Header.Set("Range", "bytes=0-7")
	rangeRec := httptest.NewRecorder()
	em.Handler().ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusPartialContent || rangeRec.Header().Get("Content-Type") != "application/pdf" ||
		!strings.HasPrefix(rangeRec.Header().Get("Content-Disposition"), "inline;") ||
		!bytes.Equal(rangeRec.Body.Bytes(), pdfBytes[:8]) {
		t.Fatalf("Range GET /File = (%d, headers=%v, body=%q)", rangeRec.Code, rangeRec.Header(), rangeRec.Body.String())
	}

	headReq := httptest.NewRequest(http.MethodHead, "/Items/"+bookID+"/Download?api_key=grant", nil)
	headRec := httptest.NewRecorder()
	jf.Handler().ServeHTTP(headRec, headReq)
	etag := headRec.Header().Get("ETag")
	if headRec.Code != http.StatusOK || headRec.Header().Get("Content-Type") != "application/epub+zip" || headRec.Body.Len() != 0 ||
		etag == "" || strings.HasPrefix(etag, "W/") || etag != dlRec.Header().Get("ETag") {
		t.Fatalf("HEAD /Download = (%d, %q, etag=%q vs %q, len=%d)", headRec.Code, headRec.Header().Get("Content-Type"), etag, dlRec.Header().Get("ETag"), headRec.Body.Len())
	}
	resumeReq := httptest.NewRequest(http.MethodGet, "/Items/"+bookID+"/Download?api_key=grant", nil)
	resumeReq.Header.Set("Range", "bytes=4-11")
	resumeReq.Header.Set("If-Range", etag)
	resumeRec := httptest.NewRecorder()
	jf.Handler().ServeHTTP(resumeRec, resumeReq)
	if resumeRec.Code != http.StatusPartialContent || !bytes.Equal(resumeRec.Body.Bytes(), epubBytes[4:12]) {
		t.Fatalf("If-Range GET /Download = (%d, len=%d)", resumeRec.Code, resumeRec.Body.Len())
	}

	// 3. Preparing error maps to 503 + Retry-After; unauth -> 401; movie & unavailable book -> 404.
	preparing.Store(true)
	prepReq := httptest.NewRequest(http.MethodGet, "/Items/"+bookID+"/Download?api_key=grant", nil)
	prepRec := httptest.NewRecorder()
	jf.Handler().ServeHTTP(prepRec, prepReq)
	if prepRec.Code != http.StatusServiceUnavailable || prepRec.Header().Get("Retry-After") != "3" {
		t.Fatalf("preparing /Download = (%d, Retry-After=%q)", prepRec.Code, prepRec.Header().Get("Retry-After"))
	}
	preparing.Store(false)

	for _, tc := range []struct {
		name string
		req  *http.Request
		want int
	}{
		{"unauth download", httptest.NewRequest(http.MethodGet, "/Items/"+bookID+"/Download", nil), http.StatusUnauthorized},
		{"unauth file", httptest.NewRequest(http.MethodGet, "/Items/"+bookID+"/File", nil), http.StatusUnauthorized},
		{"movie download", httptest.NewRequest(http.MethodGet, "/Items/"+movieID+"/Download?api_key=grant", nil), http.StatusNotFound},
		{"unavailable book", httptest.NewRequest(http.MethodGet, "/Items/"+unavailID+"/Download?api_key=grant", nil), http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		jf.Handler().ServeHTTP(rec, tc.req)
		if rec.Code != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, rec.Code, tc.want)
		}
	}

	if cacheableBrowse(httptest.NewRequest(http.MethodGet, "/Items/"+bookID+"/File", nil)) {
		t.Fatal("cacheableBrowse(/Items/{id}/File) = true, want false")
	}
}
