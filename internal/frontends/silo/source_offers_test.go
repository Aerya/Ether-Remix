package silo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type testSourceOffers struct {
	issue   func(frontend.VersionScope, []frontend.SourceOfferTarget) ([]frontend.SourceOffer, error)
	resolve func(frontend.VersionScope, string) (frontend.SourceOffer, error)
}

func (s testSourceOffers) Issue(ctx context.Context, scope frontend.VersionScope, target frontend.SourceOfferTarget) (frontend.SourceOffer, error) {
	all, err := s.IssueBatch(ctx, scope, []frontend.SourceOfferTarget{target})
	if err != nil {
		return frontend.SourceOffer{}, err
	}
	return all[0], nil
}
func (s testSourceOffers) IssueBatch(_ context.Context, scope frontend.VersionScope, targets []frontend.SourceOfferTarget) ([]frontend.SourceOffer, error) {
	if s.issue != nil {
		return s.issue(scope, targets)
	}
	out := make([]frontend.SourceOffer, len(targets))
	for n, target := range targets {
		out[n] = frontend.SourceOffer{ID: strconv.FormatInt(101+int64(n), 10), Target: target, ExpiresAt: time.Now().Add(time.Minute)}
	}
	return out, nil
}
func (s testSourceOffers) Resolve(_ context.Context, scope frontend.VersionScope, id string) (frontend.SourceOffer, error) {
	if s.resolve != nil {
		return s.resolve(scope, id)
	}
	if id != "101" {
		return frontend.SourceOffer{}, frontend.ErrSourceGone
	}
	return frontend.SourceOffer{ID: id, Target: frontend.SourceOfferTarget{Item: 1000000000000001, VersionHandle: "handle"}}, nil
}
func (testSourceOffers) Revoke(context.Context, frontend.VersionScope, string) error { return nil }
func (h testVersionHandles) IssueBatch(ctx context.Context, scope frontend.VersionScope, key frontend.RatingKey, releases []string) (map[string]string, error) {
	out := map[string]string{}
	for _, release := range releases {
		handle, err := h.Issue(ctx, scope, key, release)
		if err != nil {
			return nil, err
		}
		out[release] = handle
	}
	return out, nil
}

type embeddedDetailSessions = frontend.Sessions

type testDetailSessions struct {
	embeddedDetailSessions
	detail func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error)
	match  func(context.Context, frontend.VersionScope, string, string) error
}

func (s testDetailSessions) DetailPlan(ctx context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
	return s.detail(ctx, req)
}
func (testDetailSessions) ResolveDetailSource(context.Context, frontend.VersionScope, string) (frontend.DetailSourceTarget, error) {
	return frontend.DetailSourceTarget{}, errors.New("not used")
}
func (s testDetailSessions) MatchDetailSession(ctx context.Context, scope frontend.VersionScope, handle, id string) error {
	if s.match != nil {
		return s.match(ctx, scope, handle, id)
	}
	return nil
}

func TestDetailSourceOfferFlowsThroughPlaybackAndSessionCheck(t *testing.T) {
	viewer := &frontend.Viewer{ID: 7}
	key := frontend.RatingKey(1000000000000001)
	detailCalls, plans, streams, stops, matches := 0, 0, 0, 0, 0
	var target frontend.SourceOfferTarget
	revoked, mismatch := false, false
	media := frontend.MediaInfo{Container: "mp4", Duration: time.Minute, Size: 123, Streams: []frontend.StreamInfo{{Type: "video", Codec: "h264", Height: 720}}}
	sessions := testDetailSessions{embeddedDetailSessions: frontendtest.Sessions{
		PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
			plans++
			if req.DetailSource != "detail" || req.ExpectedSourceETag != "generation" || req.Version != "" || req.Item != key || req.Intent.Estimate {
				t.Fatalf("wrong planned source %+v", req)
			}
			return frontend.PlayPlan{ID: "plan", ETag: "generation", Media: media, Decision: frontend.Decision{Kind: "directplay"}}, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "session"}, nil
		},
		StartStreamFunc: func(context.Context, frontend.SessionAccess, string, frontend.StreamRequest) (string, error) {
			streams++
			return "/sessions/media/session/grant/file", nil
		},
		StopFunc: func(context.Context, frontend.SessionAccess, string) error { stops++; return nil },
	}, detail: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
		detailCalls++
		if req.Intent.Estimate || req.Item != key || req.Device.ID != "tv" {
			t.Fatal(req)
		}
		return frontend.PlayPlan{Media: media, DetailSource: "detail", ETag: "generation"}, nil
	}, match: func(_ context.Context, scope frontend.VersionScope, handle, id string) error {
		matches++
		if scope.Viewer != viewer || scope.Device.ID != "tv" || handle != "detail" || id != "session" {
			t.Fatal("wrong match scope")
		}
		if mismatch {
			return frontend.ErrSourceGone
		}
		return nil
	}}
	offers := testSourceOffers{issue: func(scope frontend.VersionScope, targets []frontend.SourceOfferTarget) ([]frontend.SourceOffer, error) {
		if len(targets) != 1 || scope.Viewer != viewer || scope.Device.ID != "tv" {
			t.Fatal(targets)
		}
		target = targets[0]
		return []frontend.SourceOffer{{ID: "101", Target: target}}, nil
	}, resolve: func(scope frontend.VersionScope, id string) (frontend.SourceOffer, error) {
		if revoked || id != "101" {
			return frontend.SourceOffer{}, frontend.ErrSourceGone
		}
		return frontend.SourceOffer{ID: id, Target: target}, nil
	}}
	exp := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: frontend.Title{Key: key, Type: "movie", Released: time.Unix(10000, 0)}}, nil
	}}
	inst, err := (Frontend{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: testDeviceIdentity(frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}, viewer), Experience: exp, Sessions: sessions, SourceOffers: offers, Capabilities: frontendtest.Capabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer grant")
		r.Header.Set("X-Silo-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("GET", "/api/v2/catalog/items/"+ids.RatingKeyGUID(key), "")
	var body map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatalf("detail %d %s", w.Code, w.Body.String())
	}
	versions := body["versions"].([]any)
	if len(versions) != 0 || detailCalls != 0 {
		t.Fatalf("browse planned a source: %s calls=%d", w.Body.String(), detailCalls)
	}
	target = frontend.SourceOfferTarget{Item: key, DetailSource: "detail", ETag: "generation"}
	start := `{"protocol_version":3,"file_id":"101","profile_id":"7","installation_id":"` + inst.(*instance).installationID() + `","playback_attempt_id":"attempt"}`
	w = call("POST", "/api/v2/playback/start", start)
	if w.Code != 201 || detailCalls != 0 || plans != 1 || streams != 1 || matches != 2 {
		t.Fatalf("play %d %s counts=%d/%d/%d/%d", w.Code, w.Body.String(), detailCalls, plans, streams, matches)
	}
	mismatch = true
	w = call("POST", "/api/v2/playback/start", start)
	if w.Code != 503 || streams != 1 || stops != 1 {
		t.Fatalf("mismatch streamed %d %s", w.Code, w.Body.String())
	}
	revoked = true
	w = call("POST", "/api/v2/playback/start", start)
	if w.Code != 503 || plans != 2 || streams != 1 {
		t.Fatalf("revoked offer %d %s", w.Code, w.Body.String())
	}
}

func TestEstimatedAlternativeKeepsUnknownFacts(t *testing.T) {
	viewer := &frontend.Viewer{ID: 7}
	key := frontend.RatingKey(1000000000000001)
	media := frontend.MediaInfo{Estimated: true, Container: "mp4", Size: 123456, Duration: time.Hour, BitrateKbps: 9000, Streams: []frontend.StreamInfo{{Type: "video", Codec: "h264", Height: 1080}}}
	exp := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: frontend.Title{Key: key, Type: "movie"}, Versions: []frontend.Version{{ID: "cut", Media: media}}}, nil
	}}
	calls := 0
	sessions := frontendtest.Sessions{PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
		calls++
		return frontend.PlayPlan{}, nil
	}}
	inst, err := (Frontend{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}, Experience: exp, Sessions: sessions, VersionHandles: testVersionHandles{}, SourceOffers: testSourceOffers{}, Capabilities: frontendtest.Capabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/v2/watch/"+ids.RatingKeyGUID(key), nil)
	r.Header.Set("Authorization", "Bearer grant")
	r.Header.Set("X-Silo-Device-Id", "tv")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 || calls != 0 {
		t.Fatalf("detail resolved outside DetailPlanner: %d %s calls=%d", w.Code, w.Body.String(), calls)
	}
	for _, want := range []string{`"file_size":0`, `"duration_seconds":0`, `"bitrate":0`, `"codec_video":""`, `"resolution":""`, `"file_id":"101"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing unknown sentinel %s: %s", want, w.Body.String())
		}
	}
	for _, absent := range []string{`"video_tracks"`, `"audio_tracks"`, `"file_name"`, `"playback_variants"`} {
		if strings.Contains(w.Body.String(), absent) {
			t.Errorf("invented fact %s: %s", absent, w.Body.String())
		}
	}
}

func TestVersionsRouteProbesOneSource(t *testing.T) {
	viewer := &frontend.Viewer{ID: 7}
	key := frontend.RatingKey(1000000000000001)
	calls := 0
	known, progressive := true, false
	media := frontend.MediaInfo{Container: "mp4", Duration: time.Minute, Streams: []frontend.StreamInfo{{Type: "video", Codec: "h264", Height: 1080, Width: 1920, ProbedInterlaced: &progressive, DolbyVisionConfigurationPresent: &known, DolbyVisionBLCompatibilityIDPresent: &known, ColorTransfer: "bt709", HDR10Plus: &progressive}, {Type: "audio", Codec: "aac", Language: "eng", Channels: 2, Default: true}}}
	sessions := testDetailSessions{embeddedDetailSessions: frontendtest.Sessions{}, detail: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
		calls++
		if req.Intent.Estimate || req.Item != key {
			t.Fatal(req)
		}
		return frontend.PlayPlan{Media: media, DetailSource: "detail", ETag: "generation"}, nil
	}}
	exp := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: frontend.Title{Key: key, Type: "movie", Title: "Film"}}, nil
	}}
	inst, err := (Frontend{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: testDeviceIdentity(frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}, viewer), Experience: exp, Sessions: sessions, SourceOffers: testSourceOffers{}, Capabilities: frontendtest.Capabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer grant")
		r.Header.Set("X-Silo-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	item := call("/api/v2/catalog/items/" + ids.RatingKeyGUID(key))
	if item.Code != 200 || calls != 0 || !strings.Contains(item.Body.String(), `"versions":[]`) {
		t.Fatalf("item browse probed: %d calls=%d %s", item.Code, calls, item.Body.String())
	}
	versions := call("/api/v2/catalog/items/" + ids.RatingKeyGUID(key) + "/versions")
	if versions.Code != 200 || calls != 1 || !strings.Contains(versions.Body.String(), `"file_id"`) || !strings.Contains(versions.Body.String(), `"interlaced":false`) || !strings.Contains(versions.Body.String(), `"audio_tracks"`) || strings.Contains(versions.Body.String(), `"file_name"`) {
		t.Fatalf("versions probe: %d calls=%d %s", versions.Code, calls, versions.Body.String())
	}
	watch := call("/api/v2/watch/" + ids.RatingKeyGUID(key))
	if watch.Code != 200 || calls != 2 || !strings.Contains(watch.Body.String(), `"file_id"`) || !strings.Contains(watch.Body.String(), `"audio_tracks"`) || !strings.Contains(watch.Body.String(), `"duration_seconds"`) || strings.Contains(watch.Body.String(), `"versions":[]`) {
		t.Fatalf("watch probe: %d calls=%d %s", watch.Code, calls, watch.Body.String())
	}
}

type pluginSiloEditions struct {
	variants func(context.Context, frontend.ResolveRequest, int) ([]frontend.ReleaseVariant, error)
}

func (e pluginSiloEditions) Editions(context.Context, *frontend.Viewer, frontend.Device, frontend.RatingKey) ([]frontend.Version, error) {
	return nil, nil
}

func (e pluginSiloEditions) Variants(ctx context.Context, req frontend.ResolveRequest, max int) ([]frontend.ReleaseVariant, error) {
	return e.variants(ctx, req, max)
}

func (e pluginSiloEditions) Available(context.Context, frontend.RatingKey) (bool, bool) {
	return false, false
}

func (e pluginSiloEditions) RefillSearchCache(*frontend.Title) <-chan struct{} {
	return nil
}

func TestPluginItemVersionsWatchAndPlayback(t *testing.T) {
	viewer := &frontend.Viewer{ID: 7}
	key := core.PluginKey(9001)
	var variantCalls int
	var lastLocal bool
	var lastPurpose string
	var issuedTargets []frontend.SourceOfferTarget
	media1080 := frontend.MediaInfo{
		Container: "mp4",
		Duration:  90 * time.Minute,
		Streams: []frontend.StreamInfo{
			{Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Type: "audio", Codec: "aac", Language: "eng", Channels: 2, Default: true},
		},
	}
	editions := pluginSiloEditions{
		variants: func(ctx context.Context, req frontend.ResolveRequest, _ int) ([]frontend.ReleaseVariant, error) {
			variantCalls++
			lastLocal = core.LocalResponse(ctx)
			lastPurpose = core.IndexerPurpose(ctx)
			if req.Title == nil || req.Title.Key != key {
				t.Fatalf("unexpected ResolveRequest title: %+v", req.Title)
			}
			return []frontend.ReleaseVariant{
				{ID: core.KindAutoVersionID, Label: "Auto · 1080p", Auto: true, Info: media1080},
				{ID: "src-1080", Label: "1080p Feed", Release: frontend.Release{ID: "src-1080"}, Info: media1080},
				{ID: "src-1080", Label: "Duplicate 1080p Feed", Release: frontend.Release{ID: "src-1080"}, Info: media1080},
			}, nil
		},
	}
	var detailPlanCalls int
	var plannedReq frontend.PlanRequest
	dec := frontend.Decision{Kind: frontend.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: "mp4"}
	sess := frontend.Session{
		ID: "silo-plugin-sess", Item: key, Device: frontend.Device{ID: "tv"},
		Media: &media1080,
	}
	sessions := testDetailSessions{
		embeddedDetailSessions: frontendtest.Sessions{
			PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
				plannedReq = req
				return frontend.PlayPlan{ID: "plan-1", Media: media1080, Decision: dec, DirectPlay: true}, nil
			},
			OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
				return sess, nil
			},
			StartStreamFunc: func(context.Context, frontend.SessionAccess, string, frontend.StreamRequest) (string, error) {
				return "/sessions/media/" + sess.ID + "/scoped/direct", nil
			},
		},
		detail: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
			detailPlanCalls++
			return frontend.PlayPlan{}, errors.New("plugin key must not call DetailPlan")
		},
	}
	offeredByID := map[string]frontend.SourceOffer{}
	offers := testSourceOffers{
		issue: func(_ frontend.VersionScope, targets []frontend.SourceOfferTarget) ([]frontend.SourceOffer, error) {
			issuedTargets = append([]frontend.SourceOfferTarget(nil), targets...)
			out := make([]frontend.SourceOffer, len(targets))
			for idx, target := range targets {
				id := strconv.Itoa(500 + idx)
				out[idx] = frontend.SourceOffer{ID: id, Target: target}
				offeredByID[id] = out[idx]
			}
			return out, nil
		},
		resolve: func(_ frontend.VersionScope, id string) (frontend.SourceOffer, error) {
			if o, ok := offeredByID[id]; ok {
				return o, nil
			}
			return frontend.SourceOffer{}, frontend.ErrSourceGone
		},
	}
	exp := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: frontend.Title{Key: key, Type: "movie", Title: "Championship Final"}}, nil
	}}
	inst, err := (Frontend{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:       testDeviceIdentity(frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}, viewer),
		Experience:     exp,
		Editions:       editions,
		Sessions:       sessions,
		VersionHandles: testVersionHandles{},
		SourceOffers:   offers,
		Capabilities:   frontendtest.Capabilities{},
		IDs:            ids.Codecs{Allocator: fileIDs{}},
	})
	if err != nil {
		t.Fatal(err)
	}

	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer grant")
		r.Header.Set("X-Silo-Device-Id", "tv")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		return w
	}

	// 1. Item browse MUST NOT call Editions.Variants or DetailPlan.
	browse := call("GET", "/api/v2/catalog/items/"+ids.RatingKeyGUID(key), "")
	if browse.Code != 200 || variantCalls != 0 || detailPlanCalls != 0 || !strings.Contains(browse.Body.String(), `"versions":[]`) {
		t.Fatalf("item browse resolved plugin versions: code=%d variantCalls=%d detailCalls=%d body=%s", browse.Code, variantCalls, detailPlanCalls, browse.Body.String())
	}

	// 2. /versions resolves the plugin version menu with !LocalResponse and Playback purpose,
	//    deduplicates variant IDs, and issues VersionHandle offers (never DetailSource).
	versionsRes := call("GET", "/api/v2/catalog/items/"+ids.RatingKeyGUID(key)+"/versions", "")
	if versionsRes.Code != 200 || variantCalls != 1 || detailPlanCalls != 0 {
		t.Fatalf("/versions failed: code=%d variantCalls=%d detailCalls=%d body=%s", versionsRes.Code, variantCalls, detailPlanCalls, versionsRes.Body.String())
	}
	if lastLocal {
		t.Fatalf("pluginVersions passed LocalResponse=true; cold plugin items would return empty")
	}
	if lastPurpose != core.IndexerPurposePlayback {
		t.Fatalf("pluginVersions IndexerPurpose = %q, want %q", lastPurpose, core.IndexerPurposePlayback)
	}
	if len(issuedTargets) != 2 {
		t.Fatalf("issuedTargets = %+v, want 2 deduplicated version-handle targets", issuedTargets)
	}
	for _, tgt := range issuedTargets {
		if tgt.Item != key || tgt.VersionHandle == "" || tgt.DetailSource != "" {
			t.Fatalf("unexpected target %+v, want VersionHandle only", tgt)
		}
	}

	// 3. /watch returns those file_ids and POST /api/v2/playback/start plans via VersionHandle.
	watchRes := call("GET", "/api/v2/watch/"+ids.RatingKeyGUID(key), "")
	if watchRes.Code != 200 || variantCalls != 2 || detailPlanCalls != 0 {
		t.Fatalf("/watch failed: code=%d variantCalls=%d detailCalls=%d body=%s", watchRes.Code, variantCalls, detailPlanCalls, watchRes.Body.String())
	}
	var watchBody struct {
		Versions []struct {
			FileID string `json:"file_id"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(watchRes.Body.Bytes(), &watchBody); err != nil {
		t.Fatalf("decode /watch: %v", err)
	}
	if len(watchBody.Versions) != 2 {
		t.Fatalf("/watch versions = %+v, want 2", watchBody.Versions)
	}
	startBody := `{"protocol_version":3,"file_id":"` + watchBody.Versions[1].FileID + `","profile_id":"7","installation_id":"` + inst.(*instance).installationID() + `","playback_attempt_id":"att-1","client_capabilities":{"codecs_video":["h264"]}}`
	startRes := call("POST", "/api/v2/playback/start", startBody)
	if startRes.Code != 201 {
		t.Fatalf("POST /api/v2/playback/start status=%d body=%s", startRes.Code, startRes.Body.String())
	}
	if plannedReq.Item != key || !plannedReq.Intent.ExactVersion || plannedReq.Version != issuedTargets[1].VersionHandle || plannedReq.DetailSource != "" {
		t.Fatalf("Plan req = %+v, want Item=%v ExactVersion=true Version=%q DetailSource=\"\"", plannedReq, key, issuedTargets[1].VersionHandle)
	}
}
