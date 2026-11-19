package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/frenchcat"
	"github.com/braedonsaunders/ether/internal/pse/quality"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestReferenceSelectionRejectsUntaggedAndPrunesOldLinks(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: store.OpenTest(t), cfg: cfg, log: slog.Default()}
	t.Cleanup(frenchcat.Override(frenchcat.Settings{Enabled: true, Releases: []string{"french", "multi", "vostfr"}, Types: map[core.MediaType]bool{core.TypeMovie: true, core.TypeShow: true}}))
	t.Cleanup(releasedb.OverridePlayable(releasedb.NewPlayable(1, "min=720", false)))
	key := core.MovieKey(42)
	var rs []core.Release
	for _, name := range []string{
		"Son.Yemek.2026.2160p.AMZN.WEB-DL.DDP5.1.DoVi.HDR.H.265-TURG",
		"Son.Yemek.2026.1080p.AMZN.WEB-DL.DDP5.1.H.264-TURG",
		"Long.Haired.Businessmen.2026.1080p.WEB-DL.HEVC.x265.5.1-BONE",
		"Film.2026.FRENCH.1080p.WEB-DL",
		"Film.2026.MULTi.1080p.WEB-DL",
		"Film.2026.VOSTFR.1080p.WEB-DL",
		"Film.2026.FRENCH.480p.WEB-DL",
	} {
		rs = append(rs, core.Release{ID: name, Name: name, Protocol: core.ProtocolHTTP, StreamURL: "https://source.test/private", Info: releaseparse.Parse(name)})
	}
	// Simulate a database created by the old unconditional retention path.
	old := a.releaseLibrary()
	old.Accept = nil
	if err = old.Save(ctx, key, rs); err != nil {
		t.Fatal(err)
	}
	if got := a.releaseLibrary().Candidates(ctx, key); len(got) != 3 {
		t.Fatalf("candidates=%d", len(got))
	}
	w := httptest.NewRecorder()
	a.pseReleaseLibraryAPI(w, httptest.NewRequest("GET", "/release-library?browse=1&mode=releases&media="+key.String(), nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "source.test/private") {
		t.Fatal("locator exposed")
	}
	var browse struct {
		Items []browseItem `json:"items"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &browse); err != nil {
		t.Fatal(err)
	}
	excluded := 0
	for _, item := range browse.Items {
		if item.Release != nil && item.Release.State == "excluded" {
			excluded++
		}
	}
	if excluded != 4 {
		t.Fatalf("old rejected rows shown as ready: %d excluded", excluded)
	}
	n, err := a.releaseLibrary().PruneSelection(ctx)
	if err != nil || n != 4 {
		t.Fatalf("pruned=%d err=%v", n, err)
	}
	// A subsequent search must not bring rejected locators back.
	if err = a.releaseLibrary().Save(ctx, key, rs); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.db.QueryRow(`SELECT COUNT(*) FROM pse_release_library`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("stored=%d err=%v", count, err)
	}
	// Explicit language exception keeps unknown audio, but not bad resolution.
	if _, err = a.db.Exec(`INSERT INTO pse_lang_exempt(title_key) VALUES(?)`, int64(key)); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.acceptReference(ctx, key, rs[0]); err != nil || !ok {
		t.Fatal("language exception", ok, err)
	}
	if ok, err := a.acceptReference(ctx, key, rs[6]); err != nil || ok {
		t.Fatal("exception bypassed quality", ok, err)
	}
}

func TestReferenceSelectionUsesScopedProfiles(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	a := &App{db: db, log: slog.Default()}
	q, err := quality.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	required := func(value string) []core.ReleaseRule {
		return []core.ReleaseRule{{ID: value, Name: value, Action: "require", Match: "any", Conditions: []core.ReleaseCondition{{Field: "remix_language", Operator: "equals", Value: value}}}}
	}
	if err = q.Save(ctx, quality.Config{Mode: "profiles", Profiles: []quality.Profile{
		{ID: "fr", Name: "French", Scope: "global", Rules: required("French")},
		{ID: "en", Name: "English exception", Scope: "collection", Target: "en", Replace: true, Rules: required("English")},
	}}); err != nil {
		t.Fatal(err)
	}
	key := core.MovieKey(42)
	q.SetMembers(map[string][]core.RatingKey{"collection:en": {key}})
	english := core.Release{Name: "Film.2026.ENGLISH.1080p", Info: releaseparse.Parse("Film.2026.ENGLISH.1080p")}
	if ok, err := a.acceptReference(ctx, key, english); err != nil || !ok {
		t.Fatal("scoped override", ok, err)
	}
	if ok, err := a.acceptReference(ctx, core.MovieKey(43), english); err != nil || ok {
		t.Fatal("global French requirement", ok, err)
	}
	if ok, err := a.acceptReference(ctx, key, core.Release{Name: "Film.2026.1080p"}); err != nil || ok {
		t.Fatal("unknown language accepted", ok, err)
	}
}
