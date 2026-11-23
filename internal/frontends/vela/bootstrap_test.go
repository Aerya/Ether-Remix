package vela

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/viewerpolicy"
)

type idleBootstrapExperience struct {
	frontendtest.Experience
	*experience.RowViewService
	db            *sql.DB
	staleBackfill bool
	revisionReads int
	churn         int
}

func (f *idleBootstrapExperience) HomeRevision(ctx context.Context, _ *frontend.Viewer) (frontend.HomeRevision, bool, error) {
	f.revisionReads++
	epoch, err := viewerpolicy.Read(ctx, f.db)
	if f.churn > 0 {
		epoch += int64(f.churn)
		f.churn--
	}
	return frontend.HomeRevision{Viewer: "idle", Policy: epoch}, true, err
}
func (f *idleBootstrapExperience) ResolveRowView(ctx context.Context, q frontend.RowViewPageRequest) (frontend.RowViewPage, error) {
	if q.Offset > 0 && f.staleBackfill {
		f.staleBackfill = false
		return frontend.RowViewPage{}, frontend.ErrRowViewStale
	}
	return f.RowViewService.ResolveRowView(ctx, q)
}

type idleBootstrapImages struct{ frontend.ImageGrants }

func (idleBootstrapImages) SignPage(_ context.Context, q frontend.ImagePageRequest) ([]frontend.ImageBatchItem, error) {
	var out []frontend.ImageBatchItem
	for _, title := range q.Titles {
		out = append(out, frontend.ImageBatchItem{Item: title.Key, Images: []frontend.GrantedImage{{Secret: "image", Image: frontend.Image{Kind: frontend.ImageBackdrop, Path: fmt.Sprint(title.Key), Tag: "backdrop"}}}})
	}
	return out, nil
}

// An idle bootstrap reuses an immutable, authorized visit and its receipts.
// Exercise real persistence/issuance, stale manifests and hero backfill through
// the mounted HTTP handler; fake only identity, title source and artwork.
func TestVelaIdleBootstrap(t *testing.T) {
	db := store.OpenTest(t)
	active := func(_ context.Context, id int64) (*core.User, error) {
		return &core.User{ID: id, Username: "viewer"}, nil
	}
	receipts, err := experience.NewReceipts(experience.ReceiptOptions{DB: db, TTL: 30 * time.Minute, MaxPerDevice: 100, Active: active,
		Allowed: func(context.Context, *core.User, core.Device, core.Protocol, experience.ReceiptSubject) bool {
			return true
		},
		AllowedAction: func(context.Context, *core.User, core.Device, core.Protocol, experience.ReceiptSubject, frontend.RowInteractionKind) bool {
			return true
		},
		Record: func(context.Context, *core.User, core.Device, core.Protocol, experience.ReceiptSubject, frontend.RowInteractionKind) error {
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	builds := 0
	valid := true
	views, err := experience.NewRowViewService(experience.RowViewOptions{DB: db, TTL: 15 * time.Minute, Active: active, Receipts: receipts,
		Build: func(_ context.Context, q frontend.RowViewRequest) ([]experience.RowViewSnapshot, error) {
			builds++
			snap := experience.RowViewSnapshot{Row: frontend.Row{ID: "trending", Kind: "trending", Title: "Trending", Purpose: frontend.RowRecommendation}, Placement: q.Placement}
			for n := 1; n <= 30; n++ {
				snap.Entries = append(snap.Entries, core.NeutralEntryRef{Kind: "title", ID: core.MovieKey(int64(n)).String()})
			}
			return []experience.RowViewSnapshot{snap}, nil
		},
		ValidRow: func(context.Context, *core.User, core.Device, core.Protocol, experience.RowViewSnapshot) bool {
			return valid
		},
		ResolveEntry: func(_ context.Context, _ *core.User, _ core.Device, _ core.Protocol, ref core.NeutralEntryRef) (frontend.RowEntry, error) {
			key, err := core.ParseRatingKey(ref.ID)
			return frontend.RowEntry{Kind: frontend.RowEntryTitle, Title: &core.Title{Key: key, Title: fmt.Sprint(key)}}, err
		}})
	if err != nil {
		t.Fatal(err)
	}
	// A live manifest referencing an expired row used to reject a new listing.
	if _, err = views.ListRowViews(context.Background(), frontend.RowViewRequest{Viewer: &core.User{ID: 1}, Device: core.Device{ID: "device"}, Protocol: "vela", Placement: frontend.PlacementHome}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE experience_row_views SET expires=0`); err != nil {
		t.Fatal(err)
	}
	source := &idleBootstrapExperience{RowViewService: views, db: db, staleBackfill: true}
	mounted, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(context.Background(), frontend.Host{Identity: identityFake{}, Experience: source, Sessions: frontendtest.Sessions{}, ImageGrants: idleBootstrapImages{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(context.Background()) })
	inst := mounted.(*instance)
	get := func() ([]byte, *store.QueryStats) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("X-Vela-Device-Id", "device")
		ctx, stats := store.WithQueryStats(req.Context())
		w := httptest.NewRecorder()
		mounted.Handler().ServeHTTP(w, req.WithContext(ctx))
		if w.Code != 200 {
			t.Fatalf("bootstrap %d: %s", w.Code, w.Body.String())
		}
		var wire struct{ Heroes []json.RawMessage }
		if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil || len(wire.Heroes) != 6 {
			t.Fatalf("heroes: %s, %v", w.Body, err)
		}
		return append([]byte(nil), w.Body.Bytes()...), stats
	}
	first, _ := get()
	second, warm := get()
	if string(first) != string(second) {
		t.Fatal("idle hero, receipts or cursors changed")
	}
	if warm.Writes() != 0 {
		t.Fatalf("idle writes=%d", warm.Writes())
	}
	// Wire-grant refresh must also reuse live page receipts rather than fill
	// the device quota. The response can carry newly sealed image/cursor grants.
	inst.bootstrapMu.Lock()
	for _, entry := range inst.bootstraps {
		entry.expires = time.Now().Add(-time.Second)
	}
	inst.bootstrapMu.Unlock()
	_, renewed := get()
	if renewed.Writes() != 0 {
		t.Fatalf("page revisit wrote %d statements", renewed.Writes())
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM experience_receipts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 49 {
		t.Fatalf("idle receipt count=%d, want 21 before stale backfill + 28 on rebuilt visit", count)
	}
	if builds != 3 {
		t.Fatalf("manifest builds=%d want seed, stale replacement, backfill replacement", builds)
	}
	// A durable policy change must invalidate even an otherwise live wire body.
	valid = false
	if _, err = db.Exec(`UPDATE viewer_policy_epoch SET generation=generation+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Vela-Device-Id", "device")
	denied := httptest.NewRecorder()
	mounted.Handler().ServeHTTP(denied, req)
	// A revoked row is dropped from Home; nothing it carried may be served.
	if body := denied.Body.String(); strings.Contains(body, `"heroes"`) || (denied.Code == 200 && !strings.Contains(body, `"rows":[]`)) {
		t.Fatal("cached Home survived a policy revocation", denied.Code, body)
	}

	// A moving revision is retried locally; continuous churn has a bounded,
	// explicit client retry contract and never publishes a mixed snapshot.
	valid = true
	for _, churn := range []int{2, 100} {
		inst.bootstraps = nil
		source.churn, source.revisionReads = churn, 0
		out := httptest.NewRecorder()
		mounted.Handler().ServeHTTP(out, req)
		if churn == 2 && out.Code != 200 {
			t.Fatalf("settling revision: %d %s", out.Code, out.Body)
		}
		if churn == 100 && (out.Code != 409 || out.Header().Get("Retry-After") != "1" || !strings.Contains(out.Body.String(), `"code":"bootstrap_changed"`) || !strings.Contains(out.Body.String(), `"allowed":true`)) {
			t.Fatalf("continuous revision: %d %s", out.Code, out.Body)
		}
		if source.revisionReads > 2*bootstrapAttempts {
			t.Fatalf("unbounded assembly: %d revision reads", source.revisionReads)
		}
	}
	source.churn = 0

	// Unexpected failures must give the owner one useful, sanitized WARN.
	valid = true
	source.UserDataFunc = func(context.Context, *frontend.Viewer, []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
		return nil, fmt.Errorf("bootstrap data: %w", errors.New("failed https://example.invalid/private?token=hidden token=hidden Bearer token"))
	}
	var logs bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(prior)
	failed := httptest.NewRecorder()
	mounted.Handler().ServeHTTP(failed, req)
	if failed.Code != 503 {
		t.Fatalf("unexpected failure status=%d", failed.Code)
	}
	line := logs.String()
	if strings.Count(line, `"level":"WARN"`) != 1 || !strings.Contains(line, `GET /vela/v0/bootstrap`) || !strings.Contains(line, `error_class`) || !strings.Contains(line, `error_chain`) {
		t.Fatalf("missing request warning: %s", line)
	}
	if strings.Contains(line, "https://") || strings.Contains(line, "example.invalid") || strings.Contains(line, "hidden") || strings.Contains(line, "Bearer token") {
		t.Fatalf("unsafe warning: %s", line)
	}
}
