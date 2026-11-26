package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/service"
	"github.com/braedonsaunders/ether/internal/updater"
)

func testUpdateApp(t *testing.T) *App {
	t.Helper()
	return &App{
		opts: Options{DataDir: t.TempDir(), Version: "2.0.0"},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestConfirmUpdateStartupRollbackRequestsRestart(t *testing.T) {
	a := testUpdateApp(t)
	target := filepath.Join(t.TempDir(), "ether")
	for p, body := range map[string]string{target: "new", target + ".old": "old"} {
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dir := a.updatesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker, _ := json.Marshal(updater.Pending{From: "1.0.0", To: "2.0.0", Target: target, Attempts: updater.DefaultMaxStartAttempts})
	if err := os.WriteFile(updater.PendingPath(dir), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	err := a.confirmUpdateStartup()
	if !errors.Is(err, service.ErrRestartRequested) {
		t.Fatalf("err = %v, want ErrRestartRequested", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "old" {
		t.Fatalf("binary not rolled back: %q", b)
	}
}

func TestConfirmUpdateStartupNoMarker(t *testing.T) {
	a := testUpdateApp(t)
	if err := a.confirmUpdateStartup(); err != nil {
		t.Fatal(err)
	}
	if a.runResult() != nil {
		t.Fatal("runResult without restart should be nil")
	}
}

type staticChecker struct{ upd *updater.Update }

func (s staticChecker) Check(context.Context) (*updater.Update, error) { return s.upd, nil }
func (s staticChecker) Download(context.Context, *updater.Update, string, updater.ProgressFunc) (string, error) {
	return "", errors.New("not used")
}

func TestUpdatesHandlerAndRestart(t *testing.T) {
	a := testUpdateApp(t)
	a.updates = &updateState{}
	newMgr := func(selfApply bool, why string) {
		a.updates.mgr = updater.NewManager(updater.ManagerOptions{
			NewClient: func(config.Updates) (updater.Checker, error) { return staticChecker{}, nil },
			Config: func() config.Updates {
				return config.Updates{Enabled: true, Channel: "stable", MaintenanceStart: "02:00", MaintenanceEnd: "04:30"}
			},
			CurrentVersion: "2.0.0",
			Dir:            a.updatesDir(),
			Target:         filepath.Join(t.TempDir(), "ether"),
			SelfApply:      selfApply,
			NotifyReason:   why,
			Now:            func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) },
			Logger:         a.log,
		})
	}
	newMgr(true, "")
	h := a.updatesHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var st updater.ManagerStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	if st.Current != "2.0.0" || st.Capability != updater.CapabilitySelfApply || st.Window.Start != "02:00" || st.Window.End != "04:30" || st.State != updater.StateIdle {
		t.Fatalf("status = %+v", st)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/apply", strings.NewReader(`{"force":true}`)))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"force":true`) || !strings.Contains(rec.Body.String(), `"requested":true`) {
		t.Fatalf("POST apply: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/apply", strings.NewReader(`{bad`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec.Code)
	}

	newMgr(false, "running in a container; pull the new image instead")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/apply", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "container") {
		t.Fatalf("notify-only apply: %d %s", rec.Code, rec.Body)
	}

	// A restart request cancels Run and makes it report ErrRestartRequested.
	ctx, cancel := context.WithCancel(context.Background())
	a.bindRestart(ctx, cancel)
	a.requestRestart("updated 1.0.0 → 2.0.0")
	if ctx.Err() == nil {
		t.Fatal("Run context not cancelled")
	}
	if err := a.runResult(); !errors.Is(err, service.ErrRestartRequested) || !strings.Contains(err.Error(), "2.0.0") {
		t.Fatalf("runResult = %v", err)
	}
}

func TestUpdateNotesOwnerDismissal(t *testing.T) {
	version := updater.Version
	updater.Version = "0.3.21"
	t.Cleanup(func() { updater.Version = version })
	a := setupTestAppOwner(t)
	const path = "/api/updates/notes"
	unauthenticated := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://localhost/ether"+path, nil)
	r.RemoteAddr = "127.0.0.1:1000"
	a.Handler().ServeHTTP(unauthenticated, r)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated notes status = %d", unauthenticated.Code)
	}

	get := func() updateNotes {
		t.Helper()
		r := doAdminAPI(t, a, http.MethodGet, path+"?version=v0.3.21", "")
		var notes updateNotes
		if err := json.Unmarshal(r.Body.Bytes(), &notes); err != nil || r.Code != http.StatusOK {
			t.Fatalf("GET notes: status=%d, error=%v", r.Code, err)
		}
		return notes
	}
	if notes := get(); !notes.Unseen || !strings.HasPrefix(notes.Markdown, "# Ether v0.3.21") {
		t.Fatal("expected unseen, embedded release notes")
	}
	if r := doAdminAPI(t, a, http.MethodPost, path+"/dismiss", `{"version":"0.3.20"}`); r.Code != http.StatusConflict {
		t.Fatalf("stale dismissal status = %d", r.Code)
	}
	if r := doAdminAPI(t, a, http.MethodPost, path+"/dismiss", `{"version":"0.3.21"}`); r.Code != http.StatusNoContent {
		t.Fatalf("dismissal status = %d", r.Code)
	}
	if notes := get(); notes.Unseen || notes.Markdown == "" {
		t.Fatal("dismissed notes should still be available on demand")
	}
	if err := a.users.SetOwner(t.Context(), plextv.Account{ID: 43, Username: "next-owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	if !get().Unseen {
		t.Fatal("a different owner should have their own dismissal")
	}
}
