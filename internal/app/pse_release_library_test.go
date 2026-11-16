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
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{TMDB: 42}}
	r := core.Release{ID: "private-id", Name: "Film.FRENCH.1080p", Indexer: "Source", IndexerID: "test", Protocol: core.ProtocolHTTP, StreamURL: "https://example.test/private-url", StreamHeaders: map[string]string{"Authorization": "private-header"}, GUID: "private-guid", StreamDebrid: "alldebrid"}
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
	restore := pseenv.Replace(map[string]string{"PSE_RELEASE_LIBRARY": "off"})
	defer restore()
	if _, ok := a.retainedReleaseLibrary(play, req); ok {
		t.Fatal("disabled beta reused candidates")
	}
}
