package actions_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/flows/actions"
	"github.com/braedonsaunders/ether/internal/notify"
	"github.com/braedonsaunders/ether/internal/safefetch"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestRegisterAllBuiltinsAndSpecsValid(t *testing.T) {
	reg, err := actions.NewRegistry(actions.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	want := []string{
		"digest.generate",
		"flow.set_variable",
		"http.webhook",
		"library.export",
		"library.refresh",
		"mqtt.publish",
		"notify.send",
		"playback.stop_session",
		"release.pin",
		"release.unpin",
		"source.refresh",
		"viewer.restrict",
		"viewer.unrestrict",
	}
	specs := reg.List()
	if len(specs) != len(want) {
		t.Fatalf("registered %d actions, want %d", len(specs), len(want))
	}
	got := make(map[string]flows.ActionSpec, len(specs))
	for _, s := range specs {
		got[s.Type] = s
	}
	for _, w := range want {
		s, ok := got[w]
		if !ok {
			t.Fatalf("missing action %q", w)
		}
		if s.Owner != events.OwnerBuiltin {
			t.Fatalf("spec %s owner = %q, want builtin", w, s.Owner)
		}
	}
}

func TestNotifySendWithRealDiscordEmbed(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	fixed := time.Date(2026, 11, 19, 15, 30, 0, 0, time.UTC)
	nsvc := notify.New(db, notify.Options{Client: srv.Client(), Now: func() time.Time { return fixed }})
	if err := nsvc.Save(ctx, notify.Channel{
		ID:      "ops-discord",
		Name:    "Ops Discord",
		Kind:    "discord",
		Enabled: true,
		Secrets: map[string]string{"endpoint": srv.URL},
	}); err != nil {
		t.Fatal(err)
	}

	h := actions.NewNotifySend(actions.Deps{Notify: nsvc, Now: func() time.Time { return fixed }})

	// DryRun does not send HTTP request.
	dryRes, err := h.Execute(ctx, flows.ActionContext{DryRun: true}, map[string]any{
		"channelIds": []string{"ops-discord"},
		"title":      "Test dry",
		"message":    "No side effect",
	})
	if err != nil || dryRes.Output["dryRun"] != true || gotBody != nil {
		t.Fatalf("dryRun = %+v, err=%v, gotBody=%v", dryRes, err, gotBody)
	}

	// Real execution sends rich Discord embed.
	res, err := h.Execute(ctx, flows.ActionContext{
		Event: events.Envelope{Type: "playback.session.failed"},
	}, map[string]any{
		"channelIds":   []string{"ops-discord"},
		"title":        "Stream failed",
		"message":      "Could not start Dune",
		"color":        "#EF4444",
		"posterUrl":    "https://img.example.test/dune.jpg",
		"fields":       map[string]any{"Viewer": "alex", "Client": map[string]any{"value": "Apple TV", "inline": false}},
		"bypassDigest": true,
	})
	if err != nil {
		t.Fatalf("notify.send: %v", err)
	}
	if res.Output["sent"] != 1 || res.Output["failed"] != 0 {
		t.Fatalf("output = %+v", res.Output)
	}
	embeds, ok := gotBody["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("expected discord embed, got %#v", gotBody)
	}
}

type fakePlayback struct {
	sessions []actions.ActiveSession
	stopped  []struct{ id, msg string }
}

func (f *fakePlayback) ListSessions(context.Context) ([]actions.ActiveSession, error) {
	return append([]actions.ActiveSession(nil), f.sessions...), nil
}

func (f *fakePlayback) StopSession(_ context.Context, id, msg string) error {
	f.stopped = append(f.stopped, struct{ id, msg string }{id, msg})
	return nil
}

func TestPlaybackStopSessionSelectors(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	fp := &fakePlayback{
		sessions: []actions.ActiveSession{
			{
				ID: "s-vip-4k", ViewerID: "v-vip", ViewerTags: []string{"vip"}, TitleName: "VIP 4K",
				State: "playing", IsTranscode: true, VideoHeight: 2160, IsRemote: true, StartedAt: now.Add(-5 * time.Minute),
			},
			{
				ID: "s-lan-1080", ViewerID: "v-bob", TitleName: "LAN 1080p",
				State: "playing", IsTranscode: true, VideoHeight: 1080, IsRemote: false, StartedAt: now.Add(-20 * time.Minute),
			},
			{
				ID: "s-wan-4k", ViewerID: "v-eve", TitleName: "Remote 4K Transcode",
				State: "playing", IsTranscode: true, VideoHeight: 2160, IsRemote: true, StartedAt: now.Add(-10 * time.Minute),
			},
			{
				ID: "s-paused-old", ViewerID: "v-bob", TitleName: "Paused Old",
				State: "paused", IsTranscode: false, StartedAt: now.Add(-45 * time.Minute), PausedSince: now.Add(-30 * time.Minute),
			},
			{
				ID: "s-paused-new", ViewerID: "v-bob", TitleName: "Paused Fresh",
				State: "paused", IsTranscode: false, StartedAt: now.Add(-15 * time.Minute), PausedSince: now.Add(-2 * time.Minute),
			},
		},
	}

	h := actions.NewPlaybackStopSession(actions.Deps{Playback: fp, Now: func() time.Time { return now }})

	// 1. lowest_priority_transcode protects VIP tag and stops remote 4K transcode before LAN transcode.
	res, err := h.Execute(ctx, flows.ActionContext{}, map[string]any{
		"selector":          "lowest_priority_transcode",
		"excludeViewerTags": []string{"vip"},
		"message":           "Stopping remote 4K transcode to relieve load.",
	})
	if err != nil {
		t.Fatalf("lowest_priority_transcode: %v", err)
	}
	if res.Output["stopped"] != true || res.Output["sessionId"] != "s-wan-4k" {
		t.Fatalf("got %+v", res.Output)
	}
	if fp.stopped[0].id != "s-wan-4k" || fp.stopped[0].msg != "Stopping remote 4K transcode to relieve load." {
		t.Fatalf("stopped call = %+v", fp.stopped[0])
	}

	// 2. oldest_paused respects minPausedMs and picks s-paused-old.
	res, err = h.Execute(ctx, flows.ActionContext{}, map[string]any{
		"selector":    "oldest_paused",
		"minPausedMs": int64((15 * time.Minute).Milliseconds()),
		"message":     "Idle paused stream stopped.",
	})
	if err != nil || res.Output["sessionId"] != "s-paused-old" {
		t.Fatalf("oldest_paused = %+v, err=%v", res, err)
	}

	// 3. viewer_oldest picks v-bob's earliest started session (s-paused-old), unless excluded.
	res, err = h.Execute(ctx, flows.ActionContext{}, map[string]any{
		"selector":         "viewer_oldest",
		"viewerId":         "v-bob",
		"excludeSessionId": "s-paused-old",
	})
	if err != nil || res.Output["sessionId"] != "s-lan-1080" {
		t.Fatalf("viewer_oldest = %+v, err=%v", res, err)
	}

	// 4. DryRun identifies candidate without invoking StopSession.
	before := len(fp.stopped)
	dry, err := h.Execute(ctx, flows.ActionContext{DryRun: true}, map[string]any{
		"selector":  "explicit",
		"sessionId": "s-vip-4k",
	})
	if err != nil || dry.Output["dryRun"] != true || dry.Output["sessionId"] != "s-vip-4k" || len(fp.stopped) != before {
		t.Fatalf("dry = %+v, err=%v, stoppedCount=%d", dry, err, len(fp.stopped))
	}

	// 5. Missing explicit session is an idempotent no-op.
	miss, err := h.Execute(ctx, flows.ActionContext{}, map[string]any{
		"selector":  "explicit",
		"sessionId": "does-not-exist",
	})
	if err != nil || miss.Output["stopped"] != false || miss.Output["reason"] != "no_matching_session" {
		t.Fatalf("miss = %+v, err=%v", miss, err)
	}
}

func TestHTTPWebhookSSRFAndSignedPayload(t *testing.T) {
	ctx := context.Background()

	// 1. Default BuiltinHTTPClient blocks loopback (SSRF guard).
	var gotSig, gotIdem, gotAuth, gotCustom, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Ether-Signature")
		gotIdem = r.Header.Get("X-Ether-Idempotency-Key")
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Scene")
		b := make([]byte, 1024)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(strings.Repeat("x", 6000)))
	}))
	defer srv.Close()

	ssrfAction := actions.NewHTTPWebhook(actions.Deps{})
	_, err := ssrfAction.Execute(ctx, flows.ActionContext{}, map[string]any{
		"url":  srv.URL,
		"body": `{"hello":"world"}`,
	})
	if !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("expected safefetch.ErrBlocked for loopback, got %v", err)
	}

	// 2. With injected client, verifies signed body, headers, and 4 KiB response cap.
	h := actions.NewHTTPWebhook(actions.Deps{HTTPClient: srv.Client()})
	secret := "top-secret-key"
	bodyJSON := `{"scene":"cinema","brightness":10}`
	res, err := h.Execute(ctx, flows.ActionContext{
		FlowID:         "flw_1",
		RunID:          "run_1",
		IdempotencyKey: "idem-123",
	}, map[string]any{
		"url":         srv.URL,
		"method":      "POST",
		"headers":     map[string]any{"X-Scene": "lights-dim"},
		"body":        bodyJSON,
		"secret":      secret,
		"bearerToken": "tok-abc",
	})
	if err != nil {
		t.Fatalf("http.webhook: %v", err)
	}
	if res.Output["statusCode"] != 202 || res.Output["ok"] != true {
		t.Fatalf("output = %+v", res.Output)
	}
	if len(res.Output["body"].(string)) != 4096 {
		t.Fatalf("captured body len = %d, want 4096 cap", len(res.Output["body"].(string)))
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(bodyJSON))
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != wantSig || gotIdem != "idem-123" || gotAuth != "Bearer tok-abc" || gotCustom != "lights-dim" || gotBody != bodyJSON {
		t.Fatalf("webhook headers/body mismatch: sig=%q idem=%q auth=%q custom=%q body=%q", gotSig, gotIdem, gotAuth, gotCustom, gotBody)
	}
}

type fakeNotifier struct {
	brokerID string
	topic    string
	payload  string
	qos      byte
	retain   bool
}

func (f *fakeNotifier) DeliverToChannels(context.Context, []string, notify.Event, notify.DeliverOptions) ([]notify.DeliverResult, error) {
	return nil, nil
}
func (f *fakeNotifier) PublishMQTT(_ context.Context, brokerID, topic string, payload []byte, qos byte, retain bool) error {
	f.brokerID = brokerID
	f.topic = topic
	f.payload = string(payload)
	f.qos = qos
	f.retain = retain
	return nil
}

func TestMQTTPublishAction(t *testing.T) {
	ctx := context.Background()
	fn := &fakeNotifier{}
	h := actions.NewMQTTPublish(actions.Deps{Notify: fn})

	if _, err := h.Execute(ctx, flows.ActionContext{}, map[string]any{
		"brokerId": "ha",
		"topic":    "home/#",
		"payload":  "on",
	}); err == nil {
		t.Fatal("expected wildcard topic error")
	}

	res, err := h.Execute(ctx, flows.ActionContext{}, map[string]any{
		"brokerId": "ha",
		"topic":    "home/cinema/scene",
		"payload":  `{"state":"credits"}`,
		"qos":      1,
		"retain":   true,
	})
	if err != nil {
		t.Fatalf("mqtt.publish: %v", err)
	}
	if res.Output["published"] != true || fn.brokerID != "ha" || fn.topic != "home/cinema/scene" || fn.qos != 1 || !fn.retain {
		t.Fatalf("unexpected mqtt publish: res=%+v fn=%+v", res, fn)
	}
}

type fakeViewers struct {
	policies map[string]actions.ViewerPolicySnapshot
}

func (f *fakeViewers) GetViewerPolicy(_ context.Context, viewerID string) (actions.ViewerPolicySnapshot, error) {
	p, ok := f.policies[viewerID]
	if !ok {
		return actions.ViewerPolicySnapshot{}, errors.New("viewer not found")
	}
	return p, nil
}

func (f *fakeViewers) ApplyViewerPolicy(_ context.Context, viewerID string, patch actions.ViewerRestrictionPatch) error {
	p, ok := f.policies[viewerID]
	if !ok {
		return errors.New("viewer not found")
	}
	if patch.RemoteDisabled != nil {
		p.RemoteDisabled = *patch.RemoteDisabled
	}
	if patch.MaxBitrateKbps != nil {
		p.MaxBitrateKbps = *patch.MaxBitrateKbps
	}
	if patch.StreamLimit != nil {
		p.StreamLimit = *patch.StreamLimit
	}
	if patch.AccountEnabled != nil {
		p.AccountEnabled = *patch.AccountEnabled
	}
	f.policies[viewerID] = p
	return nil
}

func TestViewerRestrictUnrestrictAndDBRevertDue(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	rstore := actions.NewRestrictionStore(db, clock)
	fv := &fakeViewers{
		policies: map[string]actions.ViewerPolicySnapshot{
			"u1": {ViewerID: "u1", RemoteDisabled: false, MaxBitrateKbps: 20000, StreamLimit: 3, AccountEnabled: true},
		},
	}
	deps := actions.Deps{Viewers: fv, Restrictions: rstore, Now: clock}
	restrict := actions.NewViewerRestrict(deps)
	unrestrict := actions.NewViewerUnrestrict(deps)

	// 1. Apply first temporary restriction for 30m.
	res, err := restrict.Execute(ctx, flows.ActionContext{}, map[string]any{
		"viewerId":       "u1",
		"remoteDisabled": true,
		"maxBitrateKbps": 4000,
		"streamLimit":    1,
		"revertAfter":    "30m",
		"reason":         "Account sharing alert",
	})
	if err != nil {
		t.Fatalf("viewer.restrict: %v", err)
	}
	if res.Output["restricted"] != true || fv.policies["u1"].MaxBitrateKbps != 4000 || !fv.policies["u1"].RemoteDisabled || fv.policies["u1"].StreamLimit != 1 {
		t.Fatalf("after restrict: res=%+v policy=%+v", res, fv.policies["u1"])
	}

	// 2. Apply overlapping restriction (disableAccount); original baseline (20000 kbps, 3 streams) must be preserved!
	if _, err := restrict.Execute(ctx, flows.ActionContext{}, map[string]any{
		"viewerId":       "u1",
		"disableAccount": true,
		"revertAfter":    "1h",
	}); err != nil {
		t.Fatalf("second restrict: %v", err)
	}
	if fv.policies["u1"].AccountEnabled {
		t.Fatal("expected account disabled")
	}

	// 3. Before deadline, RevertDue does nothing.
	now = now.Add(30 * time.Minute)
	n, err := rstore.RevertDue(ctx, fv)
	if err != nil || n != 0 {
		t.Fatalf("early RevertDue = %d, %v", n, err)
	}

	// 4. After deadline, RevertDue restores exact original baseline.
	now = now.Add(31 * time.Minute)
	n, err = rstore.RevertDue(ctx, fv)
	if err != nil || n != 1 {
		t.Fatalf("due RevertDue = %d, %v", n, err)
	}
	got := fv.policies["u1"]
	if got.RemoteDisabled || got.MaxBitrateKbps != 20000 || got.StreamLimit != 3 || !got.AccountEnabled {
		t.Fatalf("restored policy = %+v, want original baseline", got)
	}

	// 5. Explicit viewer.unrestrict also restores a saved baseline.
	if _, err := restrict.Execute(ctx, flows.ActionContext{}, map[string]any{
		"viewerId":       "u1",
		"maxBitrateKbps": 2000,
	}); err != nil {
		t.Fatal(err)
	}
	unres, err := unrestrict.Execute(ctx, flows.ActionContext{}, map[string]any{"viewerId": "u1"})
	if err != nil || unres.Output["unrestricted"] != true || unres.Output["restoredFromSnapshot"] != true {
		t.Fatalf("unrestrict = %+v, err=%v", unres, err)
	}
	if fv.policies["u1"].MaxBitrateKbps != 20000 {
		t.Fatalf("policy after unrestrict = %+v", fv.policies["u1"])
	}
}

type fakeLibraries struct {
	refreshedLib  string
	refreshedColl string
	items         []actions.ExportItem
}

func (f *fakeLibraries) RefreshLibrary(_ context.Context, id string, _ bool) (actions.RefreshResult, error) {
	f.refreshedLib = id
	return actions.RefreshResult{Target: id, Kind: "library", Items: 42}, nil
}

func (f *fakeLibraries) RefreshCollection(_ context.Context, id string) (actions.RefreshResult, error) {
	f.refreshedColl = id
	return actions.RefreshResult{Target: id, Kind: "collection", Items: 12}, nil
}

func (f *fakeLibraries) ListExportItems(context.Context, actions.ExportQuery) ([]actions.ExportItem, error) {
	return f.items, nil
}

func TestLibraryRefreshAndExportFormats(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sink := &actions.FileExportSink{
		Dir:             dir,
		BaseDownloadURL: "/api/flows/exports",
		MaxFiles:        10,
	}
	fl := &fakeLibraries{
		items: []actions.ExportItem{
			{
				Key: "movie:tmdb:550", Title: "Fight Club", MediaType: "movie", Year: 1999,
				LibraryID: "1", LibraryName: "Movies", ContentRating: "R", Studio: "Fox",
				Rating: 8.4, Genres: []string{"Drama", "Thriller"},
				Directors: []string{"David Fincher"}, Actors: []string{"Brad Pitt", "Edward Norton"},
				DurationMS: 8340000, Container: "mkv", VideoCodec: "hevc", VideoWidth: 3840, VideoHeight: 2160,
				DynamicRange: "hdr10", AudioCodec: "truehd", AudioChannels: 8, BitrateKbps: 48000, FileSizeBytes: 52000000000,
				PosterURL: "https://img.example.test/fc.jpg", PlayURL: "https://ether.example.test/play/550",
			},
			{
				Key: "episode:tmdb:95396:2:1", Title: "Hello, Ms. Cobel", ShowTitle: "Severance",
				Season: 2, Episode: 1, MediaType: "episode", Year: 2025,
				LibraryID: "2", LibraryName: "Shows", DurationMS: 3300000,
			},
		},
	}

	fixed := time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)
	deps := actions.Deps{Libraries: fl, Exports: sink, Now: func() time.Time { return fixed }}

	// Refresh library & collection
	ref := actions.NewLibraryRefresh(deps)
	r1, err := ref.Execute(ctx, flows.ActionContext{}, map[string]any{"libraryId": "1", "fullSync": true})
	if err != nil || r1.Output["refreshed"] != true || fl.refreshedLib != "1" {
		t.Fatalf("refresh library: %+v, err=%v", r1, err)
	}
	r2, err := ref.Execute(ctx, flows.ActionContext{}, map[string]any{"collectionId": "col-4k"})
	if err != nil || r2.Output["kind"] != "collection" || fl.refreshedColl != "col-4k" {
		t.Fatalf("refresh collection: %+v, err=%v", r2, err)
	}

	// Export CSV, JSON, XML, M3U (including path traversal neutralization)
	exp := actions.NewLibraryExport(deps)
	for _, format := range []string{"csv", "json", "xml", "m3u"} {
		res, err := exp.Execute(ctx, flows.ActionContext{}, map[string]any{
			"scope":          "library",
			"targetId":       "1",
			"format":         format,
			"metadataLevel":  2,
			"mediaInfoLevel": 2,
			"artworkLevel":   1,
			"filename":       "../../sneaky-export",
		})
		if err != nil {
			t.Fatalf("export %s: %v", format, err)
		}
		p := res.Output["exportPath"].(string)
		if filepath.Dir(p) != dir {
			t.Fatalf("export escaped dir: %s", p)
		}
		if res.Output["itemCount"] != 2 || !strings.HasPrefix(res.Output["downloadUrl"].(string), "/api/flows/exports/") {
			t.Fatalf("export %s output = %+v", format, res.Output)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		switch format {
		case "csv":
			rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
			if err != nil || len(rows) != 3 {
				t.Fatalf("csv rows = %d, err=%v", len(rows), err)
			}
		case "json":
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil || int(doc["itemCount"].(float64)) != 2 {
				t.Fatalf("json invalid: %v %s", err, string(raw))
			}
		case "xml":
			var doc struct {
				ItemCount int `xml:"itemCount,attr"`
			}
			if err := xml.Unmarshal(raw, &doc); err != nil || doc.ItemCount != 2 {
				t.Fatalf("xml invalid: %v %s", err, string(raw))
			}
		case "m3u":
			s := string(raw)
			if !strings.HasPrefix(s, "#EXTM3U\n") || !strings.Contains(s, "Fight Club (1999)") || !strings.Contains(s, "Severance - S02E01") {
				t.Fatalf("m3u unexpected:\n%s", s)
			}
		}
	}
}

type fakeDigests struct {
	items []actions.DigestItem
	act   actions.DigestActivitySummary
}

func (f *fakeDigests) RecentlyAdded(context.Context, actions.DigestQuery) ([]actions.DigestItem, error) {
	return f.items, nil
}
func (f *fakeDigests) ActivitySummary(context.Context, time.Time, time.Time) (actions.DigestActivitySummary, error) {
	return f.act, nil
}

func TestDigestGenerateGroupsEpisodesAndEscapesHTML(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC)
	sink := &actions.FileExportSink{Dir: t.TempDir(), BaseDownloadURL: "/api/flows/exports"}
	fd := &fakeDigests{
		items: []actions.DigestItem{
			{
				TitleKey: "movie:1", Title: "Dune: Part Two <script>alert(1)</script>", MediaType: "movie",
				Year: 2024, Rating: 8.6, Genres: []string{"Sci-Fi"}, PosterURL: "https://img.example.test/dune2.jpg",
				AddedAt: now.Add(-24 * time.Hour),
			},
			{ShowKey: "show:sev", ShowTitle: "Severance", Season: 2, Episode: 1, MediaType: "episode", AddedAt: now.Add(-48 * time.Hour)},
			{ShowKey: "show:sev", ShowTitle: "Severance", Season: 2, Episode: 2, MediaType: "episode", AddedAt: now.Add(-47 * time.Hour)},
			{ShowKey: "show:sev", ShowTitle: "Severance", Season: 2, Episode: 3, MediaType: "episode", AddedAt: now.Add(-46 * time.Hour)},
			{ShowKey: "show:sev", ShowTitle: "Severance", Season: 2, Episode: 5, MediaType: "episode", AddedAt: now.Add(-45 * time.Hour)},
		},
		act: actions.DigestActivitySummary{
			TotalPlays:    48,
			UniqueViewers: 6,
			HoursWatched:  61.5,
			TopTitles:     []actions.DigestTopTitle{{Title: "Severance", Plays: 18}},
		},
	}

	h := actions.NewDigestGenerate(actions.Deps{Digests: fd, Exports: sink, Now: func() time.Time { return now }})
	res, err := h.Execute(ctx, flows.ActionContext{}, map[string]any{
		"window":          "7d",
		"kind":            "combined",
		"groupEpisodes":   true,
		"includeActivity": true,
		"saveShareable":   true,
	})
	if err != nil {
		t.Fatalf("digest.generate: %v", err)
	}
	if res.Output["movieCount"] != 1 || res.Output["showCount"] != 1 || res.Output["episodeCount"] != 4 || res.Output["totalPlays"] != 48 {
		t.Fatalf("counts = %+v", res.Output)
	}
	text := res.Output["text"].(string)
	html := res.Output["html"].(string)
	if !strings.Contains(text, "Season 2 (E01–E03, E05 · 4 episodes)") {
		t.Fatalf("expected grouped episode range in text:\n%s", text)
	}
	if strings.Contains(html, "<script>alert(1)</script>") || !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("expected HTML escaping in digest:\n%s", html)
	}
	if !strings.HasPrefix(res.Output["shareUrl"].(string), "/api/flows/exports/digest_") {
		t.Fatalf("shareUrl = %v", res.Output["shareUrl"])
	}
}

type fakeSources struct {
	kind, id string
}

func (f *fakeSources) RefreshSource(_ context.Context, kind, id string) (actions.RefreshResult, error) {
	f.kind, f.id = kind, id
	return actions.RefreshResult{Target: id, Kind: kind, Items: 99}, nil
}

type fakeReleases struct {
	pinnedTitle, pinnedRelease, note, unpinnedTitle string
}

func (f *fakeReleases) PinRelease(_ context.Context, titleKey, releaseID, note string) (actions.PinResult, error) {
	f.pinnedTitle, f.pinnedRelease, f.note = titleKey, releaseID, note
	return actions.PinResult{TitleKey: titleKey, ReleaseID: releaseID, ReleaseName: "Movie.2024.2160p.REMUX"}, nil
}
func (f *fakeReleases) UnpinRelease(_ context.Context, titleKey string) error {
	f.unpinnedTitle = titleKey
	return nil
}

func TestSourceRefreshReleasePinUnpinAndSetVariable(t *testing.T) {
	ctx := context.Background()
	fs := &fakeSources{}
	fr := &fakeReleases{}
	deps := actions.Deps{Sources: fs, Releases: fr}

	// source.refresh
	sr := actions.NewSourceRefresh(deps)
	if _, err := sr.Execute(ctx, flows.ActionContext{}, map[string]any{"sourceKind": "livetv_source"}); err == nil {
		t.Fatal("expected missing sourceId error for livetv_source")
	}
	res, err := sr.Execute(ctx, flows.ActionContext{}, map[string]any{"sourceKind": "livetv_guide"})
	if err != nil || res.Output["refreshed"] != true || fs.kind != "livetv_guide" {
		t.Fatalf("source.refresh = %+v, err=%v", res, err)
	}

	// release.pin & release.unpin
	pin := actions.NewReleasePin(deps)
	pres, err := pin.Execute(ctx, flows.ActionContext{}, map[string]any{
		"titleKey":  "movie:tmdb:550",
		"releaseId": "rel_42",
		"note":      "Preferred remux",
	})
	if err != nil || pres.Output["pinned"] != true || fr.pinnedRelease != "rel_42" || fr.note != "Preferred remux" {
		t.Fatalf("release.pin = %+v, err=%v", pres, err)
	}
	unpin := actions.NewReleaseUnpin(deps)
	ures, err := unpin.Execute(ctx, flows.ActionContext{
		Event: events.Envelope{Correlation: events.Correlation{TitleID: "movie:tmdb:550"}},
	}, map[string]any{})
	if err != nil || ures.Output["unpinned"] != true || fr.unpinnedTitle != "movie:tmdb:550" {
		t.Fatalf("release.unpin = %+v, err=%v", ures, err)
	}

	// flow.set_variable (runs even in DryRun)
	vars := map[string]any{}
	sv := actions.NewFlowSetVariable(deps)
	vres, err := sv.Execute(ctx, flows.ActionContext{
		DryRun: true,
		SetVar: func(k string, v any) { vars[k] = v },
	}, map[string]any{
		"key":       "max_retries",
		"value":     "3",
		"valueType": "int",
	})
	if err != nil || vres.Output["value"] != int64(3) || vars["max_retries"] != int64(3) {
		t.Fatalf("flow.set_variable = %+v, vars=%+v, err=%v", vres, vars, err)
	}
	if _, err := sv.Execute(ctx, flows.ActionContext{}, map[string]any{"key": "123-bad", "value": "x"}); err == nil {
		t.Fatal("expected invalid variable key error")
	}
}
