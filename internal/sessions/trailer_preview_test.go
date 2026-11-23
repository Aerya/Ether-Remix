package sessions

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrailerPreviewPurposeIsolation(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()
	pb := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 0}
	progress := &fakeProgress{}
	timeline := 0
	fills := 0
	cold := make(chan struct{})
	path := filepath.Join(t.TempDir(), "trailer.mp4")
	if err := os.WriteFile(path, []byte("preview bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Playback: pb, Progress: progress, ActiveViewer: active, Events: events, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, Now: func() time.Time { return now }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) { timeline++ }, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}, PreviewSource: func(ctx context.Context, _ *core.Viewer, title *core.Title, _ string) (PreviewSource, error) {
		if title.Key == core.MovieKey(99) {
			select {
			case <-cold:
			case <-ctx.Done():
				return PreviewSource{}, ctx.Err()
			}
		}
		fills++
		return PreviewSource{MIMEType: "video/mp4", SourceGeneration: "internal-hash", Open: func() (*os.File, error) { return os.Open(path) }}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	req := PreviewRequest{Viewer: &core.Viewer{ID: 1}, Device: core.Device{ID: "device"}, Protocol: "jellyfin", Item: core.MovieKey(1), IdempotencyKey: "focus-1"}
	// Exercise the wrapper used by real adapters too.
	previews := BindSurface(b, "jellyfin").(frontend.TrailerPreviews)
	status := BindSurface(b, "jellyfin").(frontend.TrailerPreviewStatuses)
	if _, err := status.TrailerPreviewStatus(ctx, req); err != nil || fills != 0 || len(b.previewPreparations) != 0 {
		t.Fatal("status started preparation", err)
	}
	result, err := previews.TrailerPreview(ctx, req)
	if err != nil || result.State != "ready" {
		t.Fatal(result, err)
	}
	grant := result.Grant
	if grant.Purpose != "preview" || grant.SourceGeneration != 1 || grant.SeekGeneration != 1 {
		t.Fatal("wrong grant purpose/generations", grant)
	}
	retry, err := status.TrailerPreviewStatus(ctx, req)
	if err != nil || retry.Grant.SessionID != grant.SessionID || fills != 1 {
		t.Fatal("idempotent retry changed grant", retry, err)
	}
	access := frontend.SessionAccess{Viewer: req.Viewer, Device: req.Device, Protocol: req.Protocol}
	if err = b.Report(ctx, access, grant.SessionID, frontend.Timeline{Position: time.Minute, Duration: time.Minute, State: "playing"}); err != nil {
		t.Fatal(err)
	}
	if timeline != 0 || progress.calls != 0 || pb.prepares != 0 || pb.estimates != 0 {
		t.Fatal("preview reached library playback/progress", timeline, progress.calls, pb.prepares, pb.estimates)
	}
	serve := func(g *frontend.TrailerPreviewGrant, a *frontend.SessionAccess) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, g.URL, nil)
		if a == nil {
			b.ServeMedia(w, r)
		} else {
			b.ServeMediaAccess(w, r, *a)
		}
		return w.Code
	}
	if code := serve(grant, &access); code != 200 {
		t.Fatal("preview not playable", code)
	}
	wrong := access
	wrong.Viewer = &core.Viewer{ID: 2}
	if code := serve(grant, &wrong); code != 404 {
		t.Fatal("cross-viewer grant accepted", code)
	}
	if err = b.Stop(ctx, wrong, grant.SessionID); err != core.ErrUnauthorized {
		t.Fatal("cross-viewer stop accepted", err)
	}
	req.IdempotencyKey = "focus-2"
	req.Item = core.MovieKey(2)
	replacement, err := previews.TrailerPreview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if code := serve(grant, nil); code != 404 {
		t.Fatal("replaced grant survived", code)
	}
	list, err := b.Sessions(ctx, access)
	if err != nil || len(list) != 1 || list[0].Purpose != "preview" {
		t.Fatal("preview missing from stop-command scope", list, err)
	}
	if err = b.Command(ctx, req.Viewer, replacement.Grant.SessionID, frontend.Command{Kind: frontend.CommandStop}); err != nil {
		t.Fatal(err)
	}
	if code := serve(replacement.Grant, nil); code != 404 {
		t.Fatal("stopped grant survived", code)
	}
	req.IdempotencyKey = "focus-3"
	expired, err := previews.TrailerPreview(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	if code := serve(expired.Grant, nil); code != 404 {
		t.Fatal("expired grant survived", code)
	}
	// Cold focus must return promptly without promising a grant. A fresh poll
	// can issue a grant only after preparation completes and policy is rechecked.
	req.Item, req.IdempotencyKey = core.MovieKey(99), "cold-focus"
	preparing, err := previews.TrailerPreview(ctx, req)
	if err != nil || preparing.State != "preparing" || preparing.Grant != nil {
		t.Fatal("cold focus blocked or fabricated a grant", preparing, err)
	}
	if polled, err := status.TrailerPreviewStatus(ctx, req); err != nil || polled.State != "preparing" {
		t.Fatal("pending status changed", polled, err)
	}
	b.mu.Lock()
	done := b.previewPreparations[req.Viewer.ID].done
	b.mu.Unlock()
	close(cold)
	<-done
	ready, err := status.TrailerPreviewStatus(ctx, req)
	if err != nil || ready.State != "ready" || ready.Grant == nil {
		t.Fatal("prepared focus did not become ready", ready, err)
	}

	t.Run("full trailer playback", func(t *testing.T) {
		extra := core.ExtraKey(7)
		b.deps.ExtraLookup = func(key core.RatingKey) (frontend.Extra, bool) {
			return frontend.Extra{Key: extra, ParentKey: core.MovieKey(1), Kind: "trailer"}, key == extra
		}
		b.deps.ExtraSource = func(ctx context.Context, v *core.Viewer, title *core.Title, key core.RatingKey, lang string) (PreviewSource, error) {
			if title.Key != core.MovieKey(1) || key != extra {
				t.Fatal("wrong authorized parent/extra")
			}
			return b.deps.PreviewSource(ctx, v, title, lang)
		}
		extras := BindSurface(b, "jellyfin").(frontend.ExtraPlaybacks)
		fullReq := req
		fullReq.Item, fullReq.IdempotencyKey = extra, "explicit-play"
		full, err := extras.ExtraPlayback(ctx, fullReq)
		if err != nil || full.State != "ready" || full.Grant.Purpose != "playback" {
			t.Fatal(full, err)
		}
		again, err := extras.ExtraPlayback(ctx, fullReq)
		if err != nil || again.Grant.SessionID != full.Grant.SessionID {
			t.Fatal("retry duplicated full session", again, err)
		}
		req.Item, req.IdempotencyKey = core.MovieKey(2), "focus-during-playback"
		if _, err := previews.TrailerPreview(ctx, req); err != nil {
			t.Fatal(err)
		}
		now = now.Add(3 * time.Minute)
		if code := serve(full.Grant, &access); code != 200 {
			t.Fatal("full trailer was revoked by focus or preview expiry", code)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, full.Grant.URL, nil)
		r.Header.Set("Range", "bytes=0-3")
		b.ServeMediaAccess(w, r, access)
		if w.Code != 206 || w.Body.String() != "prev" {
			t.Fatal("trailer range seek failed", w.Code, w.Body.String())
		}
		for _, state := range []string{"playing", "paused", "playing", "ended"} {
			if err := b.Report(ctx, access, full.Grant.SessionID, frontend.Timeline{State: state, Position: time.Minute}); err != nil {
				t.Fatal(err)
			}
		}
		if timeline != 0 || progress.calls != 0 || pb.prepares != 0 || pb.estimates != 0 {
			t.Fatal("full trailer reached feature/history path")
		}
		if code := serve(full.Grant, &wrong); code != 404 {
			t.Fatal("full trailer crossed viewers", code)
		}
		if err := b.Stop(ctx, access, full.Grant.SessionID); err != nil {
			t.Fatal(err)
		}
		if code := serve(full.Grant, &access); code != 404 {
			t.Fatal("stopped full trailer survived", code)
		}
	})

}
