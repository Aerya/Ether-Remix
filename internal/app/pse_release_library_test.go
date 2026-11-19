package app

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseLibraryPlaybackGateAndPrivateProjection(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: store.OpenTest(t), cfg: cfg, log: slog.Default()}
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	req := core.SearchRequest{Type: core.TypeMovie, Title: "Film", Year: 2020, IDs: core.IDs{TMDB: 42}}
	r := core.Release{ID: "private-id", Name: "Film.2020.FRENCH.1080p", TitleIDs: core.IDs{TMDB: 42}, Indexer: "Source", IndexerID: "test", Protocol: core.ProtocolHTTP, StreamURL: "https://example.test/private-url", StreamHeaders: map[string]string{"Authorization": "private-header"}, GUID: "private-guid", StreamDebrid: "alldebrid"}
	a.rememberSearchReferences(context.Background(), req, []core.Release{r})
	play := core.WithIndexerPurpose(context.Background(), core.IndexerPurposePlayback)
	if rs, ok := a.retainedReleaseLibrary(play, req); !ok || len(rs) != 1 || rs[0].StreamDebrid != "alldebrid" {
		t.Fatal(rs, ok)
	}
	for _, purpose := range []string{core.IndexerPurposeAvailability, core.IndexerPurposeRescue, core.IndexerPurposeConsole} {
		if _, ok := a.retainedReleaseLibrary(core.WithIndexerPurpose(context.Background(), purpose), req); ok {
			t.Fatal("retained answered", purpose)
		}
	}
	skipped := core.WithIndexerSkip(play, map[string]bool{"test": true})
	if _, ok := a.retainedReleaseLibrary(skipped, req); ok {
		t.Fatal("skipped source returned")
	}
	w := httptest.NewRecorder()
	a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", "/release-library?key="+core.MovieKey(42).String(), nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{"private-url", "private-header", "private-guid", "private-id", "streamUrl", "streamHeaders"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private field exposed", secret)
		}
	}
	var out struct {
		Releases []libraryRow `json:"releases"`
	}
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Releases) != 1 {
		t.Fatal(w.Body.String())
	}
	qualityRestore := pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on", "PSE_PLAYABLE_FILTER": "height=720", "PSE_QUALITY": "on"})
	if _, ok := a.retainedReleaseLibrary(play, req); !ok {
		t.Fatal("quality changes must not discard source references")
	}
	qualityRestore()
	restore := pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "off"})
	defer restore()
	if _, ok := a.retainedReleaseLibrary(play, req); !ok {
		t.Fatal("legacy off setting must not disable reference reuse")
	}
}

func TestReleaseLibraryOverview(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: store.OpenTest(t), cfg: cfg, log: slog.Default()}
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	for i := int64(1); i <= 51; i++ {
		a.rememberSearchReferences(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Film", Year: 2020, IDs: core.IDs{TMDB: i}}, []core.Release{{ID: "private-id", Name: "Film.2020.1080p", Protocol: core.ProtocolHTTP, TitleIDs: core.IDs{TMDB: i}, Indexer: "Source", StreamURL: "https://private.example/secret"}})
	}
	for i, change := range []string{"expires=0", "failed=1", "source_signature='old'"} {
		if _, err = a.db.Exec("UPDATE pse_release_library SET "+change+" WHERE title_key=?", int64(core.MovieKey(int64(i+1)))); err != nil {
			t.Fatal(err)
		}
	}

	request := func(path string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, secret := range []string{"private-id", "private.example", "payload", "source_signature"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("secret exposed")
			}
		}
		var out map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := request("/release-library?overview=1")
	for key, want := range map[string]float64{"references": 51, "titles": 51, "ready": 48, "expired": 1, "failed": 1, "sourceChanged": 1} {
		if out[key] != want {
			t.Fatalf("%s=%v want %v", key, out[key], want)
		}
	}
	if len(out["items"].([]any)) != 50 {
		t.Fatal("first page")
	}
	if len(request("/release-library?overview=1&page=1")["items"].([]any)) != 1 {
		t.Fatal("second page")
	}
	restore := pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "off"})
	defer restore()
	if out = request("/release-library?overview=1"); out["enabled"] != true || len(out["items"].([]any)) != 50 {
		t.Fatal(out)
	}
}

func TestReleaseLibraryRefusesWrongSourceAnswersAndLegacyReuse(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: store.OpenTest(t), cfg: cfg, log: slog.Default()}
	t.Cleanup(pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "on"}))
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 48463}, Title: "Incident au Loch Ness", OriginalTitle: "Incident at Loch Ness", Year: 2004}
	good := core.Release{ID: "good", Name: "Incident.at.Loch.Ness.2004.1080p", Indexer: "source", Protocol: core.ProtocolHTTP, StreamURL: "https://source.test/file"}
	bad := core.Release{ID: "bad", Name: "Superman.2025.1080p", Indexer: "source", Protocol: core.ProtocolHTTP, StreamURL: "https://source.test/file", TitleIDs: req.IDs, TitleIDsEchoed: true}
	a.rememberSearchReferences(context.Background(), req, []core.Release{good, bad, {ID: "none", Name: "aucun_flux"}})
	entries, err := a.releaseLibrary().Entries(context.Background(), core.MovieKey(48463))
	if err != nil || len(entries) != 1 {
		t.Fatalf("%d %v", len(entries), err)
	}
	// Even an old bad descriptor already on disk cannot return as a candidate.
	a.rememberReleases(context.Background(), &core.Title{Key: core.MovieKey(48463)}, []core.Release{bad})
	got, ok := a.retainedReleaseLibrary(core.WithIndexerPurpose(context.Background(), core.IndexerPurposePlayback), req)
	if !ok || len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("%v %v", got, ok)
	}
	pseRuntimeOf(a).associationPending.Store(true)
	if _, ok = a.retainedReleaseLibrary(core.WithIndexerPurpose(context.Background(), core.IndexerPurposePlayback), req); ok {
		t.Fatal("library reused before audit completed")
	}
	w := httptest.NewRecorder()
	a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", "/release-library?browse=1&mode=media", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"auditPending":true`) || strings.Contains(w.Body.String(), "Superman") {
		t.Fatal(w.Body.String())
	}
}

func TestReleaseLibraryAuditKeepsLegacyCounterShape(t *testing.T) {
	a := &App{}
	pseRuntimeOf(a).associationPending.Store(true)
	w := httptest.NewRecorder()
	a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", "/release-library?overview=1&summary=1", nil))
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"references", "ready", "expired", "failed", "sourceChanged", "lastSeen", "metadata", "titles"} {
		if _, ok := out[key].(float64); !ok {
			t.Fatalf("missing numeric counter %s", key)
		}
	}
}
