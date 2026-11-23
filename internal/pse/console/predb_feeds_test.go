package console

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func TestFeedsListValidateMaskAndRestoreKeys(t *testing.T) {
	fd := fieldByKey["PSE_PREDB_FEEDS"]
	raw := `[{"name":" Site A ","url":"https://a.example/rss","key":"cle-secrete-a"},{"url":"http://b.example/api?x=1"}]`
	norm, err := validate(fd, raw, nil)
	if err != nil || !strings.Contains(norm, `"name":"Site A"`) {
		t.Fatalf("%v %s", err, norm)
	}
	shown := display(fd, norm)
	if strings.Contains(shown, "cle-secrete-a") || !strings.Contains(shown, mask) || !strings.Contains(shown, "b.example") {
		t.Fatalf("la clé doit être masquée : %s", shown)
	}
	// La page renvoie la liste masquée plus un nouveau site : la clé du premier revient.
	back := restore(fd, strings.Replace(shown, `]`, `,{"url":"https://c.example/x","key":"nouvelle-cle"}]`, 1), norm)
	final, err := validate(fd, back, nil)
	if err != nil {
		t.Fatal(err)
	}
	var list []feedEntry
	_ = json.Unmarshal([]byte(final), &list)
	if len(list) != 3 || list[0].Key != "cle-secrete-a" || list[2].Key != "nouvelle-cle" || list[1].Key != "" {
		t.Fatalf("%+v", list)
	}
	// Un autre site ne récupère pas l'ancienne clé : il faut la ressaisir.
	changed := restore(fd, `[{"url":"https://autre.example/rss","key":"`+mask+`"}]`, norm)
	if _, err := validate(fd, changed, nil); err == nil || !strings.Contains(err.Error(), "ressaisissez") {
		t.Fatalf("clé non restituée à une autre adresse : %v", err)
	}
}

func TestFeedsListRejectsBadEntriesWithTheirNumber(t *testing.T) {
	fd := fieldByKey["PSE_PREDB_FEEDS"]
	for raw, want := range map[string]string{
		`[{"url":""}]`: "site 1 : adresse manquante",
		`[{"url":"https://a.example"},{"url":"ftp://x"}]`:           "site 2 : adresse http(s)",
		`[{"url":"https://u:p@a.example"}]`:                         "sans identifiants",
		`[{"url":"https://a.example"},{"url":"https://a.example"}]`: "site 2 : adresse déjà ajoutée",
		`pas du json`: "liste de sites invalide",
	} {
		if _, err := validate(fd, raw, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s : %v, attendu %q", raw, err, want)
		}
	}
	many := make([]string, 21)
	for i := range many {
		many[i] = `{"url":"https://s` + string(rune('a'+i)) + `.example"}`
	}
	if _, err := validate(fd, "["+strings.Join(many, ",")+"]", nil); err == nil || !strings.Contains(err.Error(), "20 sites") {
		t.Fatalf("plus de 20 sites : %v", err)
	}
	if out, err := validate(fd, "", nil); err != nil || out != "" {
		t.Fatalf("liste vide : %q %v", out, err)
	}
}

func TestProxyFieldsValidation(t *testing.T) {
	url := fieldByKey["PSE_PREDB_PROXY_URL"]
	for _, ok := range []string{"http://gluetun:8888", "https://mediaflow.example", "socks5://10.0.0.2:1080", "socks5h://h:1"} {
		if _, err := validate(url, ok, nil); err != nil {
			t.Errorf("%s refusée : %v", ok, err)
		}
	}
	for _, bad := range []string{"ftp://x", "http://u:p@h:1", "pas-une-adresse"} {
		if _, err := validate(url, bad, nil); err == nil {
			t.Errorf("%s acceptée", bad)
		}
	}
	typ := fieldByKey["PSE_PREDB_PROXY_TYPE"]
	for _, ok := range []string{"", "proxy", "mediaflow"} {
		if _, err := validate(typ, ok, nil); err != nil {
			t.Errorf("type %q refusé : %v", ok, err)
		}
	}
	if _, err := validate(typ, "vpn", nil); err == nil {
		t.Error("type inconnu accepté")
	}
}

// Le bouton Tester envoie les valeurs saisies (pas encore enregistrées) ; les clés masquées
// reviennent depuis les réglages enregistrés et le résultat ne contient jamais de clé.
func TestTestPreEndpointUsesDraftValuesAndRestoresMaskedKeys(t *testing.T) {
	restoreEnv := pseenv.Replace(map[string]string{"PSE_PREDB_FEEDS": `[{"name":"A","url":"https://a.example/rss","key":"cle-secrete-a"}]`})
	defer restoreEnv()
	var got map[string]string
	h := Handler(Deps{TestPre: func(_ context.Context, get func(string) string) any {
		got = map[string]string{}
		for _, k := range preTestKeys {
			got[k] = get(k)
		}
		got["PSE_PREDB"] = get("PSE_PREDB")
		return map[string]any{"runs": []any{}}
	}})
	body := `{"values":{"PSE_PREDB_FEEDS":"[{\"name\":\"A\",\"url\":\"https://a.example/rss\",\"key\":\"` + mask + `\"},{\"url\":\"https://b.example/x\"}]","PSE_PREDB_PROXY_TYPE":"mediaflow","PSE_PREDB_PROXY_URL":"https://relay.example","PSE_PREDB_PROXY_AUTH":"pw"}}`
	_, out := do(t, h, "POST", "/predb/test", body)
	if out["ok"] != true {
		t.Fatalf("%v", out)
	}
	var list []feedEntry
	_ = json.Unmarshal([]byte(got["PSE_PREDB_FEEDS"]), &list)
	if len(list) != 2 || list[0].Key != "cle-secrete-a" || got["PSE_PREDB_PROXY_TYPE"] != "mediaflow" || got["PSE_PREDB_PROXY_AUTH"] != "pw" || got["PSE_PREDB"] != "on" {
		t.Fatalf("valeurs transmises : %+v", got)
	}
	if strings.Contains(mustJSON(t, out), "cle-secrete-a") {
		t.Fatal("la clé ne doit pas apparaître dans la réponse")
	}
	// Un réglage invalide est signalé avec son nom, sans lancer le test.
	got = nil
	_, out = do(t, h, "POST", "/predb/test", `{"values":{"PSE_PREDB_PROXY_URL":"ftp://x"}}`)
	if out["ok"] != false || got != nil || !strings.Contains(out["message"].(string), "Adresse du proxy") {
		t.Fatalf("réglage invalide : %v", out)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFeedsKeyKeptWhenOnlyPathChanges(t *testing.T) {
	fd := fieldByKey["PSE_PREDB_FEEDS"]
	norm, err := validate(fd, `[{"name":"Mon site","url":"https://api.exemple.net","key":"cle-secrete"}]`, nil)
	if err != nil {
		t.Fatal(err)
	}
	back := restore(fd, `[{"name":"Mon site","url":"https://api.exemple.net/api/v1/releases","key":"`+mask+`"}]`, norm)
	final, err := validate(fd, back, nil)
	if err != nil {
		t.Fatal(err)
	}
	var list []feedEntry
	_ = json.Unmarshal([]byte(final), &list)
	if len(list) != 1 || list[0].Key != "cle-secrete" || list[0].URL != "https://api.exemple.net/api/v1/releases" {
		t.Fatalf("%+v", list)
	}
}
