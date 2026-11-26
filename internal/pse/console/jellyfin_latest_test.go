package console

import "testing"

func TestJellyfinLatestRules(t *testing.T) {
	h, applied, stored := newHandler(t)
	code, out := do(t, h, "GET", "/jellyfin-latest", "")
	if code != 200 || out["defaults"] == nil {
		t.Fatalf("GET %d %v", code, out)
	}
	body := `{"movie":{"exclude":[{"field":"genre","op":"in","value":["Animation"]}]},"show":{}}`
	code, out = do(t, h, "PUT", "/jellyfin-latest", body)
	if code != 200 || *applied != 1 {
		t.Fatalf("PUT %d %v", code, out)
	}
	if stored()["PSE_JELLYFIN_LATEST_FILTERS"] == "" {
		t.Fatal("non enregistré")
	}
	_, out = do(t, h, "GET", "/jellyfin-latest", "")
	if ex := out["movie"].(map[string]any)["exclude"].([]any); len(ex) != 1 {
		t.Fatalf("relecture %v", out)
	}
	if code, _ = do(t, h, "PUT", "/jellyfin-latest", `{"movie":{"exclude":[{"field":"nope","op":"in","value":["x"]}]}}`); code != 422 {
		t.Fatalf("règle invalide acceptée : %d", code)
	}
	if code, _ = do(t, h, "PUT", "/jellyfin-latest", `{"autre":1}`); code != 400 {
		t.Fatalf("champ inconnu accepté : %d", code)
	}
}
