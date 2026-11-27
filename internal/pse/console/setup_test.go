package console

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/latestfilter"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func setupBody(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func setupReview(t *testing.T, h http.Handler, values map[string]any) map[string]any {
	t.Helper()
	code, state := do(t, h, "GET", "/setup", "")
	if code != 200 {
		t.Fatalf("state: %d", code)
	}
	code, p := do(t, h, "POST", "/setup/preview", setupBody(t, map[string]any{"revision": state["revision"], "values": values}))
	if code != 200 {
		t.Fatalf("preview: %d %v", code, p)
	}
	return p
}
func setupApplyBody(t *testing.T, p map[string]any) string {
	return setupBody(t, map[string]any{"revision": p["revision"], "plan": p["plan"], "values": p["values"]})
}

func TestSetupPreviewDoesNotWriteAndApplyPreservesUnrelatedSettings(t *testing.T) {
	h, applied, stored := newHandler(t)
	pseenv.Replace(map[string]string{"PSE_SUBMAKER_MANIFEST": "private-secret", "PSE_MUSIC_CHARTS": "FR"})
	p := setupReview(t, h, map[string]any{"PSE_FRENCH": "on", "PSE_PLAYABLE": "on"})
	if *applied != 0 || len(stored()) != 0 {
		t.Fatal("preview wrote configuration")
	}
	if len(p["changes"].([]any)) != 2 {
		t.Fatal(p)
	}
	code, out := do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 200 {
		t.Fatalf("apply: %d %v", code, out)
	}
	if *applied != 1 || stored()["PSE_FRENCH"] != "on" || stored()["PSE_SUBMAKER_MANIFEST"] != "private-secret" || stored()["PSE_MUSIC_CHARTS"] != "FR" {
		t.Fatal("unrelated values lost")
	}
	if strings.Contains(setupBody(t, out), "private-secret") {
		t.Fatal("secret leaked")
	}
	// Same request cannot apply twice against an outdated baseline.
	code, _ = do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 409 || *applied != 1 {
		t.Fatal("replayed obsolete plan")
	}
}
func TestSetupRequiresMatchingReviewAndRevision(t *testing.T) {
	h, applied, _ := newHandler(t)
	p := setupReview(t, h, map[string]any{"PSE_FRENCH": "on"})
	p["values"] = map[string]any{"PSE_FRENCH": "off"}
	code, _ := do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 409 || *applied != 0 {
		t.Fatal("accepted changed plan")
	}
	p = setupReview(t, h, map[string]any{"PSE_FRENCH": "on"})
	code, _ = do(t, h, "PUT", "/", `{"values":{"PSE_LIBRARY_SORT":"title"}}`)
	if code != 200 {
		t.Fatal(code)
	}
	code, _ = do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 409 || *applied != 1 {
		t.Fatal("overwrote another editor")
	}
}
func TestSetupRejectsSecretsDownloadsUnknownAndMalformedRequests(t *testing.T) {
	h, applied, stored := newHandler(t)
	_, s := do(t, h, "GET", "/setup", "")
	for _, values := range []map[string]any{{"PSE_SUBMAKER_MANIFEST": "secret"}, {"PSE_TORRENT_DOWNLOAD": "on"}, {"UNKNOWN": "on"}, {"PSE_PLAYABLE_MIN": "0"}, {"PSE_FRENCH": "not-a-toggle"}} {
		code, _ := do(t, h, "POST", "/setup/preview", setupBody(t, map[string]any{"revision": s["revision"], "values": values}))
		if code != 422 {
			t.Fatalf("accepted %v: %d", values, code)
		}
	}
	for _, body := range []string{`{`, `{"revision":"x","values":{},"extra":true}`, `{"values":{}} {}`} {
		code, _ := do(t, h, "POST", "/setup/preview", body)
		if code != 400 {
			t.Fatalf("accepted malformed request %d", code)
		}
	}
	if *applied != 0 || len(stored()) != 0 {
		t.Fatal("invalid request wrote settings")
	}
}
func TestSetupUndoRestoresEnvironmentInheritance(t *testing.T) {
	t.Setenv("PSE_FRENCH", "off")
	h, _, stored := newHandler(t)
	p := setupReview(t, h, map[string]any{"PSE_FRENCH": "on"})
	code, result := do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 200 {
		t.Fatal(result)
	}
	undo := result["undo"].(map[string]any)
	if undo["PSE_FRENCH"] != nil {
		t.Fatal("undo manufactured override")
	}
	p = setupReview(t, h, undo)
	code, _ = do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 200 {
		t.Fatal(code)
	}
	if _, ok := stored()["PSE_FRENCH"]; ok {
		t.Fatal("environment was not restored")
	}
	if pseenv.Get("PSE_FRENCH") != "off" {
		t.Fatal("wrong inherited value")
	}
}
func TestSetupStateIsSecretFreeAndExplainsAnnouncements(t *testing.T) {
	h, _, _ := newHandler(t)
	pseenv.Replace(map[string]string{"PSE_SUBMAKER_MANIFEST": "do-not-leak", "PSE_PREDB": "on", "PSE_PLAYABLE": "on"})
	code, s := do(t, h, "GET", "/setup", "")
	if code != 200 {
		t.Fatal(code)
	}
	raw := setupBody(t, s)
	if strings.Contains(raw, "do-not-leak") || strings.Contains(raw, "PSE_SUBMAKER_MANIFEST") {
		t.Fatal("secret in state")
	}
	if !strings.Contains(raw, "announcements") {
		t.Fatal("missing pre warning")
	}
	for _, fd := range s["fields"].([]any) {
		if _, ok := fieldByKey[fd.(map[string]any)["key"].(string)]; !ok {
			t.Fatal("unknown schema field")
		}
	}
}

func TestSetupUnchangedValueDoesNotPinEnvironment(t *testing.T) {
	t.Setenv("PSE_FRENCH", "off")
	h, applied, stored := newHandler(t)
	p := setupReview(t, h, map[string]any{"PSE_FRENCH": "off"})
	if len(p["changes"].([]any)) != 0 || len(p["values"].(map[string]any)) != 0 {
		t.Fatal(p)
	}
	code, _ := do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 200 || *applied != 0 || len(stored()) != 0 {
		t.Fatal("no-op wrote settings")
	}
}

// The setup API must coexist with the newer dedicated Jellyfin/Emby editor.
func TestSetupPreservesLatestFiltersAndRejectsAnObsoletePreview(t *testing.T) {
	h, _, stored := newHandler(t)
	p := setupReview(t, h, map[string]any{"PSE_FRENCH": "on"})
	code, filters := do(t, h, "GET", "/jellyfin-latest", "")
	if code != 200 {
		t.Fatalf("latest editor: %d %v", code, filters)
	}
	code, out := do(t, h, "PUT", "/jellyfin-latest", `{"movie":{"exclude":[{"field":"genre","op":"in","value":["Animation"]}]},"show":{}}`)
	if code != 200 {
		t.Fatalf("save latest filters: %d %v", code, out)
	}
	before := stored()[latestfilter.Key]
	code, _ = do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 409 {
		t.Fatalf("stale setup accepted: %d", code)
	}
	p = setupReview(t, h, map[string]any{"PSE_FRENCH": "on"})
	code, _ = do(t, h, "POST", "/setup/apply", setupApplyBody(t, p))
	if code != 200 {
		t.Fatalf("fresh setup: %d", code)
	}
	if before == "" || stored()[latestfilter.Key] != before {
		t.Fatal("latest filters lost")
	}
}
