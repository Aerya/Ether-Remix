package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/store"
)

func newHandler(t *testing.T) (http.Handler, *int, func() map[string]string) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(pseenv.Replace(nil))
	applied := 0
	h := Handler(Deps{
		DB:    db,
		Apply: func(context.Context) { applied++ },
		Status: func(context.Context) map[string]any {
			return map[string]any{"french": map[string]any{"active": pseenv.Get("PSE_FRENCH") == "on"}}
		},
		Sources: func() []Option { return []Option{{"aio", Text{"AIOStreams", "AIOStreams"}}} },
		TestMusic: func(_ context.Context, key, base string) (string, error) {
			if strings.Contains(base, "down") {
				return "", errors.New("injoignable")
			}
			return "ok " + key, nil
		},
	})
	stored := func() map[string]string {
		m, err := readAll(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	return h, &applied, stored
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func field(t *testing.T, out map[string]any, key string) map[string]any {
	t.Helper()
	for _, f := range out["features"].([]any) {
		for _, fd := range f.(map[string]any)["fields"].([]any) {
			m := fd.(map[string]any)
			if m["key"] == key {
				return m
			}
		}
	}
	t.Fatalf("champ %s absent", key)
	return nil
}

func TestConsoleGetPut(t *testing.T) {
	t.Setenv("PSE_FRENCH_COUNTRIES", "FR,BE")
	h, applied, stored := newHandler(t)

	code, out := do(t, h, "GET", "/", "")
	if code != 200 {
		t.Fatalf("GET %d", code)
	}
	if f := field(t, out, "PSE_FRENCH"); f["value"] != "off" || f["source"] != "" {
		t.Fatalf("défaut: %v", f)
	}
	if f := field(t, out, "PSE_FRENCH_COUNTRIES"); f["value"] != "FR,BE" || f["source"] != "env" {
		t.Fatalf("variable d'environnement: %v", f)
	}
	if opts := field(t, out, "PSE_INDEXER_SOURCES")["options"].([]any); len(opts) != 1 {
		t.Fatalf("sources: %v", opts)
	}

	code, out = do(t, h, "PUT", "/", `{"values":{"PSE_FRENCH":"oui","PSE_FRENCH_COUNTRIES":"fr, ca","PSE_INDEXER_PACE":"30s","PSE_FRENCH_RELEASES":"MULTI,vfq"}}`)
	if code != 200 || *applied != 1 {
		t.Fatalf("PUT %d %v (apply %d)", code, out, *applied)
	}
	if f := field(t, out, "PSE_FRENCH"); f["value"] != "on" || f["source"] != "console" {
		t.Fatalf("enregistré: %v", f)
	}
	if pseenv.Get("PSE_FRENCH_COUNTRIES") != "FR,CA" || pseenv.Get("PSE_FRENCH_RELEASES") != "multi,vfq" {
		t.Fatalf("normalisation: %v", pseenv.Snapshot())
	}
	if s := out["features"].([]any)[1].(map[string]any)["status"].(map[string]any); s["active"] != true {
		t.Fatalf("état après enregistrement: %v", s)
	}
	if got := stored(); got["PSE_INDEXER_PACE"] != "30s" || len(got) != 4 {
		t.Fatalf("base: %v", got)
	}

	// null rétablit la variable d'environnement.
	code, _ = do(t, h, "PUT", "/", `{"values":{"PSE_FRENCH_COUNTRIES":null}}`)
	if code != 200 || pseenv.Get("PSE_FRENCH_COUNTRIES") != "FR,BE" || pseenv.Source("PSE_FRENCH_COUNTRIES") != "env" {
		t.Fatalf("réinitialisation: %d %q", code, pseenv.Get("PSE_FRENCH_COUNTRIES"))
	}
}

func TestConsoleValidation(t *testing.T) {
	h, applied, stored := newHandler(t)
	for _, body := range []string{
		`{"values":{"PSE_INCONNU":"on"}}`,
		`{"values":{"PSE_INDEXER_PACE":"1s"}}`,
		`{"values":{"PSE_PLAYABLE_MIN":"0"}}`,
		`{"values":{"PSE_FRENCH_COUNTRIES":"France"}}`,
		`{"values":{"PSE_FRENCH_RELEASES":"klingon"}}`,
		`{"values":{"PSE_MUSIC_HIFI_URL":"file:///etc/passwd"}}`,
		`{"values":{"PSE_MUSIC_QUALITY":"MP3"}}`,
		`{"values":{"PSE_INDEXER_SOURCES":"a b"}}`,
		`{"autre":1}`,
	} {
		code, out := do(t, h, "PUT", "/", body)
		if code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
			t.Fatalf("%s accepté: %d %v", body, code, out)
		}
	}
	if *applied != 0 || len(stored()) != 0 {
		t.Fatalf("rien ne doit être enregistré: %d %v", *applied, stored())
	}
}

func TestConsoleMasksCredentials(t *testing.T) {
	h, _, stored := newHandler(t)
	code, _ := do(t, h, "PUT", "/", `{"values":{"PSE_MUSIC_HIFI_URL":"https://moi:s3cret@hifi.example/"}}`)
	if code != 200 || stored()["PSE_MUSIC_HIFI_URL"] != "https://moi:s3cret@hifi.example" {
		t.Fatalf("enregistrement: %d %v", code, stored())
	}
	_, out := do(t, h, "GET", "/", "")
	shown := field(t, out, "PSE_MUSIC_HIFI_URL")["value"].(string)
	if strings.Contains(shown, "s3cret") || !strings.Contains(shown, mask) {
		t.Fatalf("mot de passe affiché: %q", shown)
	}
	// Renvoyer la valeur masquée conserve le mot de passe.
	body, _ := json.Marshal(map[string]any{"values": map[string]any{"PSE_MUSIC_HIFI_URL": shown, "PSE_SCROB_VIEWS": "on"}})
	if code, out := do(t, h, "PUT", "/", string(body)); code != 200 {
		t.Fatalf("PUT masqué: %d %v", code, out)
	}
	if stored()["PSE_MUSIC_HIFI_URL"] != "https://moi:s3cret@hifi.example" {
		t.Fatalf("mot de passe perdu: %v", stored())
	}
	// Changer d'hôte avec le masque impose de ressaisir le mot de passe.
	body, _ = json.Marshal(map[string]any{"values": map[string]any{"PSE_MUSIC_HIFI_URL": strings.Replace(shown, "hifi.example", "autre.example", 1)}})
	if code, _ := do(t, h, "PUT", "/", string(body)); code != http.StatusUnprocessableEntity {
		t.Fatalf("masque sur un autre hôte accepté: %d", code)
	}

	if _, out := do(t, h, "POST", "/music/test", `{"url":""}`); out["ok"] != true {
		t.Fatalf("test avec l'adresse enregistrée: %v", out)
	}
	if _, out := do(t, h, "POST", "/music/test", `{"url":"https://down.example"}`); out["ok"] != false {
		t.Fatalf("test en échec: %v", out)
	}
}

func TestConsoleMasksEclipseToken(t *testing.T) {
	h, _, stored := newHandler(t)
	const a, b = "https://addon.example/u/jeton123/manifest.json", "https://autre.example/x/y/manifest.json"
	body, _ := json.Marshal(map[string]any{"values": map[string]any{"PSE_MUSIC_ECLIPSE_URL": a + ",\n" + b + " " + a}})
	if code, out := do(t, h, "PUT", "/", string(body)); code != 200 {
		t.Fatalf("PUT: %d %v", code, out)
	}
	if got := stored()["PSE_MUSIC_ECLIPSE_URL"]; got != a+" "+b {
		t.Fatalf("enregistré: %q", got)
	}
	_, out := do(t, h, "GET", "/", "")
	shown := field(t, out, "PSE_MUSIC_ECLIPSE_URL")["value"].(string)
	if strings.Contains(shown, "jeton123") || shown != "https://addon.example/"+mask+" https://autre.example/"+mask {
		t.Fatalf("jeton affiché: %q", shown)
	}
	// Garder le premier lien masqué, remplacer le second.
	first := strings.Fields(shown)[0]
	body, _ = json.Marshal(map[string]any{"values": map[string]any{"PSE_MUSIC_ECLIPSE_URL": first + " https://neuf.example/m/manifest.json"}})
	if code, out := do(t, h, "PUT", "/", string(body)); code != 200 {
		t.Fatalf("PUT masqué: %d %v", code, out)
	}
	if got := stored()["PSE_MUSIC_ECLIPSE_URL"]; got != a+" https://neuf.example/m/manifest.json" {
		t.Fatalf("jeton perdu: %q", got)
	}
	// Un masque sur un hôte inconnu doit être ressaisi.
	body, _ = json.Marshal(map[string]any{"values": map[string]any{"PSE_MUSIC_ECLIPSE_URL": "https://inconnu.example/" + mask}})
	if code, _ := do(t, h, "PUT", "/", string(body)); code != http.StatusUnprocessableEntity {
		t.Fatalf("masque inconnu accepté: %d", code)
	}
	if _, out := do(t, h, "POST", "/music/test", `{"key":"PSE_MUSIC_ECLIPSE_URL","url":""}`); out["ok"] != true {
		t.Fatalf("test Eclipse: %v", out)
	}
	if _, out := do(t, h, "POST", "/music/test", `{"key":"PSE_MUSIC_ALLDEBRID"}`); out["ok"] != true || out["message"] != "ok PSE_MUSIC_ALLDEBRID" {
		t.Fatalf("test AllDebrid : %v", out)
	}
	for _, key := range []string{"PSE_MUSIC_DMM", "PSE_MUSIC_REALDEBRID", "PSE_MUSIC_TORBOX"} {
		if _, out := do(t, h, "POST", "/music/test", `{"key":"`+key+`"}`); out["ok"] != true || out["message"] != "ok "+key {
			t.Fatalf("test %s: %v", key, out)
		}
	}

	if _, out := do(t, h, "POST", "/music/test", `{"key":"PSE_FRENCH","url":"x"}`); out["ok"] != false {
		t.Fatalf("clé non testable acceptée: %v", out)
	}
}

func TestLoadOverrides(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Cleanup(pseenv.Replace(nil))
	if err := writeAll(context.Background(), db, map[string]string{"PSE_SCROB_VIEWS": "on", "PSE_OBSOLETE": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := LoadOverrides(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if pseenv.Get("PSE_SCROB_VIEWS") != "on" || pseenv.Source("PSE_SCROB_VIEWS") != "console" {
		t.Fatalf("rechargement: %v", pseenv.Snapshot())
	}
	if _, ok := pseenv.Lookup("PSE_OBSOLETE"); ok {
		t.Fatal("un réglage inconnu ne doit pas être chargé")
	}
}

func TestInternationalCatalogueChoices(t *testing.T) {
	for _, feature := range Features {
		for _, fd := range feature.Fields {
			switch fd.Key {
			case "PSE_CATALOG_ORIGINAL_LANGUAGES":
				if _, err := validate(fd, "ja,de,ar", nil); err != nil {
					t.Fatal(err)
				}
				if _, err := validate(fd, "none,de", nil); err == nil {
					t.Fatal("none must be exclusive")
				}
			case "PSE_FRENCH_RELEASES":
				if _, err := validate(fd, "german,spanish-latino,portuguese-brazil,arabic", nil); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
