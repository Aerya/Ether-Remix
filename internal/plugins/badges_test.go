package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func parseAndValidateBadgeManifest(raw []byte) (Manifest, error) {
	m, err := ParseManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func posterBadgeManifestJSON(caps []string, sets []map[string]any, stringsMap map[string]any) []byte {
	etherBlock := map[string]any{
		"protocol":     1,
		"kind":         "extension",
		"summary":      "Marks dubbed releases and special cuts on posters",
		"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
		"capabilities": caps,
	}
	if stringsMap != nil {
		etherBlock["strings"] = stringsMap
	}
	if sets != nil {
		etherBlock["contributes"] = map[string]any{
			"posterBadges": sets,
		}
	}
	raw, _ := json.Marshal(map[string]any{
		"id":      "community.example.badges",
		"version": "1.0.0",
		"name":    "Poster Badges",
		"ether":   etherBlock,
	})
	return raw
}

func TestPosterBadgesManifestValidationAndDescriptors(t *testing.T) {
	t.Run("requires capability and contributes pair", func(t *testing.T) {
		_, err := parseAndValidateBadgeManifest(posterBadgeManifestJSON([]string{"poster_badges"}, nil, nil))
		if err == nil || !strings.Contains(err.Error(), "is required for poster_badges") {
			t.Fatalf("expected posterBadges required error, got %v", err)
		}

		_, err = parseAndValidateBadgeManifest(posterBadgeManifestJSON(
			[]string{},
			[]map[string]any{{"key": "dubs", "label": "Dubbed"}},
			nil,
		))
		if err == nil || !strings.Contains(err.Error(), "poster_badges is required for posterBadges") {
			t.Fatalf("expected poster_badges capability required error, got %v", err)
		}
	})

	t.Run("enforces max sets, key syntax, uniqueness and copy lint", func(t *testing.T) {
		fiveSets := []map[string]any{
			{"key": "s1", "label": "Set 1"},
			{"key": "s2", "label": "Set 2"},
			{"key": "s3", "label": "Set 3"},
			{"key": "s4", "label": "Set 4"},
			{"key": "s5", "label": "Set 5"},
		}
		_, err := parseAndValidateBadgeManifest(posterBadgeManifestJSON([]string{"poster_badges"}, fiveSets, nil))
		if err == nil || !strings.Contains(err.Error(), "at most 4 badge sets") {
			t.Fatalf("expected max 4 badge sets error, got %v", err)
		}

		for _, badKey := range []string{"", "Dubs", "1dubs", "dub-set", "a" + strings.Repeat("b", 32)} {
			_, err := parseAndValidateBadgeManifest(posterBadgeManifestJSON(
				[]string{"poster_badges"},
				[]map[string]any{{"key": badKey, "label": "Dubbed"}},
				nil,
			))
			if err == nil || !strings.Contains(err.Error(), "ether.contributes.posterBadges[0].key") {
				t.Fatalf("bad key %q accepted: %v", badKey, err)
			}
		}

		_, err = parseAndValidateBadgeManifest(posterBadgeManifestJSON(
			[]string{"poster_badges"},
			[]map[string]any{
				{"key": "dubs", "label": "Dubbed"},
				{"key": "dubs", "label": "Duplicate"},
			},
			nil,
		))
		if err == nil || !strings.Contains(err.Error(), "unique key") {
			t.Fatalf("duplicate key accepted: %v", err)
		}

		_, err = parseAndValidateBadgeManifest(posterBadgeManifestJSON(
			[]string{"poster_badges"},
			[]map[string]any{{"key": "dubs", "label": "Simply dubbed"}},
			nil,
		))
		if err == nil || !strings.Contains(err.Error(), "ether.contributes.posterBadges[0].label") {
			t.Fatalf("copy-lint label violation accepted: %v", err)
		}

		_, err = parseAndValidateBadgeManifest(posterBadgeManifestJSON(
			[]string{"poster_badges"},
			[]map[string]any{{"key": "dubs", "label": "Dubbed", "description": "Simply shows dubbed releases"}},
			nil,
		))
		if err == nil || !strings.Contains(err.Error(), "ether.contributes.posterBadges[0].description") {
			t.Fatalf("copy-lint description violation accepted: %v", err)
		}
	})

	t.Run("accepts localized sets, emits descriptors and permission copy", func(t *testing.T) {
		files := map[string][]byte{
			"dist/plugin.js": []byte("globalThis.handle = () => ({ status: 200, body: '{}' });"),
			"i18n/en.json":   []byte(`{"dubLabel":"Dubbed Audio","dubDesc":"Languages available on dubbed releases"}`),
		}
		raw := posterBadgeManifestJSON(
			[]string{"poster_badges"},
			[]map[string]any{
				{"key": "dubs", "label": "$t:dubLabel", "description": "$t:dubDesc"},
				{"key": "cuts", "label": "Special Cuts"},
			},
			map[string]any{"en": "i18n/en.json"},
		)
		man, err := parseAndValidateBadgeManifest(raw)
		if err != nil {
			t.Fatalf("valid manifest rejected: %v", err)
		}

		descs := DescriptorsFor(man)
		if len(descs) != 2 || descs[0].Kind != extend.KindPosterBadge {
			t.Fatalf("unexpected descriptors: %+v", descs)
		}
		meta0, err := extend.MetaOf[map[string]any](descs[0])
		if err != nil || meta0["layerKey"] != "plugin:community.example.badges:dubs" {
			t.Fatalf("descriptor meta = %+v (err=%v)", meta0, err)
		}

		sheet, _, _ := PermissionSheet(man)
		foundPerm := false
		for _, line := range sheet {
			if line == "Adds badges to library posters" {
				foundPerm = true
			}
		}
		if !foundPerm {
			t.Fatalf("PermissionSheet missing poster_badges copy: %v", sheet)
		}

		baseMan := man
		baseEther := *man.Ether
		baseEther.Capabilities = nil
		baseEther.Contributes.PosterBadges = nil
		baseMan.Ether = &baseEther
		diff := PermissionDiff(baseMan, man)
		if len(diff) != 1 || diff[0] != "Adds badges to library posters" {
			t.Fatalf("PermissionDiff = %v", diff)
		}

		env := newTestEnv(t)
		ctx := context.Background()
		prev, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: files})
		if err != nil {
			t.Fatal(err)
		}
		rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
		if err != nil {
			t.Fatal(err)
		}
		sets := env.m.ActivePosterBadgeSets(ctx)
		if len(sets) != 2 || sets[0].Label != "Dubbed Audio" || sets[0].Description != "Languages available on dubbed releases" || sets[0].LayerKey != "plugin:"+rec.ID+":dubs" {
			t.Fatalf("ActivePosterBadgeSets = %+v", sets)
		}
	})
}

func TestHostBadgesBoundsQuotaLifecycleAndUninstall(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	files := map[string][]byte{
		"dist/plugin.js": []byte("globalThis.handle = () => ({ status: 200, body: '{}' });"),
	}
	raw := posterBadgeManifestJSON(
		[]string{"poster_badges"},
		[]map[string]any{
			{"key": "dubs", "label": "Dubbed Audio", "description": "Shows dubbed audio languages"},
			{"key": "cuts", "label": "Special Cuts"},
		},
		nil,
	)
	prev, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	h := env.m.HostFor(rec.ID)

	// Default usage reports 0 titles and 200,000 quota.
	usage, err := h.BadgesUsage(ctx)
	if err != nil || usage.Titles != 0 || usage.QuotaTitles != PosterBadgeQuotaTitles {
		t.Fatalf("initial usage = %+v, err = %v", usage, err)
	}

	// Set key validation: undeclared or malformed set keys fail validation.
	for _, badSet := range []string{"undeclared", "UPPER", "bad-key"} {
		err := h.BadgesSet(ctx, badSet, []BadgeEntry{{Title: BadgeTitleRef{Type: "movie", TMDB: 100}, Labels: []string{"DUB"}}})
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("setKey %q: want *ValidationError, got %v", badSet, err)
		}
		if !errors.As(h.BadgesClear(ctx, badSet), &ve) {
			t.Fatalf("clear setKey %q: want *ValidationError", badSet)
		}
	}

	// Entry count > 500 fails validation.
	tooMany := make([]BadgeEntry, MaxBadgeEntriesPerCall+1)
	for i := range tooMany {
		tooMany[i] = BadgeEntry{Title: BadgeTitleRef{Type: "movie", TMDB: int64(i + 1)}, Labels: []string{"DUB"}}
	}
	var ve *ValidationError
	if err := h.BadgesSet(ctx, "dubs", tooMany); !errors.As(err, &ve) {
		t.Fatalf(">500 entries: want *ValidationError, got %v", err)
	}

	// Title ref & label bounds validation.
	invalidEntries := [][]BadgeEntry{
		{{Title: BadgeTitleRef{Type: "episode", TMDB: 10}, Labels: []string{"DUB"}}},
		{{Title: BadgeTitleRef{Type: "movie"}, Labels: []string{"DUB"}}},
		{{Title: BadgeTitleRef{Type: "movie", TMDB: -5}, Labels: []string{"DUB"}}},
		{{Title: BadgeTitleRef{Type: "movie", IMDB: "nm0000123"}, Labels: []string{"DUB"}}},
		{{Title: BadgeTitleRef{Type: "movie", TMDB: 10}, Labels: []string{"A", "B", "C", "D"}}},
		{{Title: BadgeTitleRef{Type: "movie", TMDB: 10}, Labels: []string{""}}},
		{{Title: BadgeTitleRef{Type: "movie", TMDB: 10}, Labels: []string{"   "}}},
		{{Title: BadgeTitleRef{Type: "movie", TMDB: 10}, Labels: []string{"12345678901234567"}}},
		{{Title: BadgeTitleRef{Type: "movie", TMDB: 10}, Labels: []string{"DUB!"}}},
		{{Title: BadgeTitleRef{Type: "movie", TMDB: 10}, Labels: []string{"4K_HDR"}}},
	}
	for i, batch := range invalidEntries {
		if err := h.BadgesSet(ctx, "dubs", batch); !errors.As(err, &ve) {
			t.Fatalf("invalid batch %d (%+v): want *ValidationError, got %v", i, batch, err)
		}
	}

	// Valid label charset & ASCII uppercasing + deduplication.
	env.m.SetResolveBadgeTitles(func(_ context.Context, refs []BadgeTitleRef) ([]string, error) {
		out := make([]string, len(refs))
		for i, r := range refs {
			if r.Type == "movie" && r.TMDB == 603 {
				out[i] = "1000603"
			}
		}
		return out, nil
	})
	if err := h.BadgesSet(ctx, "dubs", []BadgeEntry{
		{
			Title:  BadgeTitleRef{Type: "movie", TMDB: 603, IMDB: "TT0133093"},
			Labels: []string{"en dub", "jp+en / 5.1", "director's & co-"},
		},
	}); err != nil {
		t.Fatalf("valid BadgesSet failed: %v", err)
	}

	var storedJSON string
	if err := env.m.store.db.QueryRowContext(ctx,
		`SELECT labels_json FROM plugin_poster_badges WHERE plugin_id=? AND set_key='dubs' AND title_key='1000603'`,
		rec.ID,
	).Scan(&storedJSON); err != nil {
		t.Fatalf("stored row missing: %v", err)
	}
	var storedLabels []string
	if err := json.Unmarshal([]byte(storedJSON), &storedLabels); err != nil || !reflect.DeepEqual(storedLabels, []string{"EN DUB", "JP+EN / 5.1", "DIRECTOR'S & CO-"}) {
		t.Fatalf("storedLabels = %v (raw=%s, err=%v)", storedLabels, storedJSON, err)
	}

	// Quota enforcement across sets (distinct title_key per plugin).
	env.m.SetBadgeQuotaForTest(3)
	if err := h.BadgesSet(ctx, "dubs", []BadgeEntry{
		{Title: BadgeTitleRef{Type: "show", TMDB: 2001}, Labels: []string{"en dub", "EN DUB"}},
		{Title: BadgeTitleRef{Type: "movie", IMDB: "tt9999999"}, Labels: []string{"FR DUB"}},
	}); err != nil {
		t.Fatalf("filling quota to 3 failed: %v", err)
	}
	// Adding a second set on an already-badged title does NOT increase distinct title count.
	if err := h.BadgesSet(ctx, "cuts", []BadgeEntry{
		{Title: BadgeTitleRef{Type: "movie", TMDB: 603}, Labels: []string{"DIRECTOR'S CUT"}},
	}); err != nil {
		t.Fatalf("second set on existing title failed: %v", err)
	}
	usage, err = h.BadgesUsage(ctx)
	if err != nil || usage.Titles != 3 || usage.QuotaTitles != 3 {
		t.Fatalf("usage at quota = %+v, err = %v", usage, err)
	}

	// Pushing a 4th distinct title fails with ErrQuotaExceeded and rolls back.
	err = h.BadgesSet(ctx, "dubs", []BadgeEntry{
		{Title: BadgeTitleRef{Type: "movie", TMDB: 9999}, Labels: []string{"DE DUB"}},
	})
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("4th distinct title: want ErrQuotaExceeded, got %v", err)
	}
	usage, _ = h.BadgesUsage(ctx)
	if usage.Titles != 3 {
		t.Fatalf("quota rollback left Titles = %d, want 3", usage.Titles)
	}

	// Empty labels deletes that title's badges in that set, freeing quota when no other set badges it.
	if err := h.BadgesSet(ctx, "dubs", []BadgeEntry{
		{Title: BadgeTitleRef{Type: "movie", IMDB: "tt9999999"}, Labels: []string{}},
	}); err != nil {
		t.Fatalf("delete via empty labels failed: %v", err)
	}
	usage, _ = h.BadgesUsage(ctx)
	if usage.Titles != 2 {
		t.Fatalf("usage after empty-labels delete = %d, want 2", usage.Titles)
	}

	// BadgesClear clears only the named set ("dubs"), keeping "cuts" on title 1000603.
	if err := h.BadgesClear(ctx, "dubs"); err != nil {
		t.Fatalf("BadgesClear(dubs): %v", err)
	}
	usage, _ = h.BadgesUsage(ctx)
	if usage.Titles != 1 {
		t.Fatalf("usage after clearing dubs = %d, want 1 (cuts remains)", usage.Titles)
	}

	// Revoking poster_badges denies host calls and removes active sets without deleting rows.
	if err := env.m.SetGrants(ctx, rec.ID, Grants{Revoked: []string{string(CapPosterBadges)}}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(h.BadgesSet(ctx, "cuts", []BadgeEntry{{Title: BadgeTitleRef{Type: "movie", TMDB: 603}, Labels: []string{"IMAX"}}}), ErrPermissionDenied) {
		t.Fatal("expected ErrPermissionDenied when poster_badges grant is revoked")
	}
	if len(env.m.ActivePosterBadgeSets(ctx)) != 0 {
		t.Fatal("revoked plugin remained in ActivePosterBadgeSets")
	}
	if err := env.m.SetGrants(ctx, rec.ID, Grants{}); err != nil {
		t.Fatal(err)
	}
	if len(env.m.ActivePosterBadgeSets(ctx)) != 2 {
		t.Fatal("restored grant did not restore ActivePosterBadgeSets")
	}

	// Disabling plugin hides ActivePosterBadgeSets while preserving stored rows; re-enabling restores them.
	if err := env.m.SetEnabled(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(env.m.ActivePosterBadgeSets(ctx)) != 0 {
		t.Fatal("disabled plugin remained in ActivePosterBadgeSets")
	}
	var countWhileDisabled int
	if err := env.m.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_poster_badges WHERE plugin_id=?`, rec.ID).Scan(&countWhileDisabled); err != nil || countWhileDisabled != 1 {
		t.Fatalf("rows while disabled = %d (err=%v), want 1", countWhileDisabled, err)
	}
	if err := env.m.SetEnabled(ctx, rec.ID, true); err != nil {
		t.Fatal(err)
	}
	if len(env.m.ActivePosterBadgeSets(ctx)) != 2 {
		t.Fatal("re-enabled plugin missing from ActivePosterBadgeSets")
	}

	// Uninstall (Remove) deletes all plugin_poster_badges rows for the plugin.
	if err := env.m.Remove(ctx, rec.ID, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(env.m.ActivePosterBadgeSets(ctx)) != 0 {
		t.Fatal("removed plugin remained in ActivePosterBadgeSets")
	}
	var countAfterRemove int
	if err := env.m.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_poster_badges WHERE plugin_id=?`, rec.ID).Scan(&countAfterRemove); err != nil || countAfterRemove != 0 {
		t.Fatalf("rows after uninstall = %d (err=%v), want 0", countAfterRemove, err)
	}
}

func TestHostedWasmBadgesPreludeAndQuotaFault(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })
	env.m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))
	env.m.SetBadgeQuotaForTest(2)

	script := `
globalThis.handle = function(req) {
  if (req.path === "/push") {
    ether.badges.set("dubs", [
      { title: { type: "movie", tmdb: 101 }, labels: ["en dub", "dual audio"] },
      { title: { type: "show", imdb: "tt0944947" }, labels: ["4k remaster"] }
    ]);
    return { status: 200, body: JSON.stringify(ether.badges.usage()) };
  }
  if (req.path === "/overflow") {
    try {
      ether.badges.set("dubs", [{ title: { type: "movie", tmdb: 303 }, labels: ["dub"] }]);
      return { status: 500, body: "unexpectedly succeeded" };
    } catch (e) {
      return { status: 200, body: JSON.stringify({ code: e.code || "", message: String(e.message || e) }) };
    }
  }
  if (req.path === "/clear") {
    ether.badges.clear("dubs");
    return { status: 200, body: JSON.stringify(ether.badges.usage()) };
  }
  return { status: 200, body: "{}" };
};
`
	files := map[string][]byte{"dist/plugin.js": []byte(script)}
	raw := posterBadgeManifestJSON(
		[]string{"poster_badges"},
		[]map[string]any{{"key": "dubs", "label": "Dubbed Audio"}},
		nil,
	)
	prev, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := env.m.Call(ctx, rec.ID, Request{Method: "POST", Path: "/push"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("/push: resp=%+v err=%v", resp, err)
	}
	var bodyStr string
	if err := json.Unmarshal(resp.Body, &bodyStr); err != nil {
		bodyStr = string(resp.Body)
	}
	var usage BadgeUsage
	if err := json.Unmarshal([]byte(bodyStr), &usage); err != nil || usage.Titles != 2 || usage.QuotaTitles != 2 {
		t.Fatalf("/push usage = %s (%+v, err=%v)", bodyStr, usage, err)
	}

	resp, err = env.m.Call(ctx, rec.ID, Request{Method: "POST", Path: "/overflow"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("/overflow: resp=%+v err=%v", resp, err)
	}
	if err := json.Unmarshal(resp.Body, &bodyStr); err != nil {
		bodyStr = string(resp.Body)
	}
	if !strings.Contains(bodyStr, "quota_exceeded") {
		t.Fatalf("expected quota_exceeded fault in guest, got %s", bodyStr)
	}

	resp, err = env.m.Call(ctx, rec.ID, Request{Method: "POST", Path: "/clear"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("/clear: resp=%+v err=%v", resp, err)
	}
	if err := json.Unmarshal(resp.Body, &bodyStr); err != nil {
		bodyStr = string(resp.Body)
	}
	if err := json.Unmarshal([]byte(bodyStr), &usage); err != nil || usage.Titles != 0 {
		t.Fatalf("/clear usage = %s (%+v, err=%v)", bodyStr, usage, err)
	}

	// Ensure kit/Studio sandbox helper validates cleanly as well.
	norm, err := ValidateBadgeSetCall(rec.Manifest, "dubs", []BadgeEntry{
		{Title: BadgeTitleRef{Type: "movie", TMDB: 55}, Labels: []string{"imax 1.43"}},
	})
	if err != nil || len(norm) != 1 || norm[0].Labels[0] != "IMAX 1.43" {
		t.Fatalf("ValidateBadgeSetCall = %+v, err=%v", norm, err)
	}
	if err := ValidateBadgeClearCall(rec.Manifest, "undeclared"); err == nil {
		t.Fatal("ValidateBadgeClearCall accepted undeclared set")
	}
	_ = fmt.Sprint(rec.ID)
}
