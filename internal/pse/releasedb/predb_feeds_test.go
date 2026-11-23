package releasedb

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const rssSample = `<rss><channel>
<item><title>Un.Film.2024.FRENCH.1080p.WEB.H264-GRP</title><description>Section: X264-HD | Files: 1</description></item>
<item><title>Sakamoto.Days.S02E07.VOSTFR.1080p.WEB.H264-ONi</title><description>Section: ANiME | Files: 1</description></item>
<item><title>Autre.2024.GERMAN.1080p.WEB.H264-GRP</title><description>Section: X264-HD | Files: 1</description></item>
<item><title>Un.Docu.2023.FRENCH.1080p.WEB.H264-GRP</title><description>Section: DOCU | Files: 1</description></item>
<item><title>Un.Album.FRENCH-WEB-2026-GRP</title><description>Section: MP3-WEB | Files: 1</description></item></channel></rss>`

func getFrom(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadPreDBSettingsFeedsProxyAndInterval(t *testing.T) {
	s := LoadPreDBSettingsWith(getFrom(map[string]string{
		"PSE_PREDB": "on",
		"PSE_PREDB_FEEDS": `[{"name":"Site A","url":"https://a.example/rss","key":" k1 "},{"url":"ftp://x"},
			{"url":"https://u:p@b.example/"},{"name":"Site B","url":"http://b.example/api"}]`,
		"PSE_PREDB_PROXY_TYPE": "mediaflow", "PSE_PREDB_PROXY_URL": "https://relay.example", "PSE_PREDB_PROXY_AUTH": "secret",
	}))
	if !s.Enabled || len(s.Feeds) != 2 || s.Feeds[0].Name != "Site A" || s.Feeds[0].Key != "k1" || s.Feeds[1].URL != "http://b.example/api" {
		t.Fatalf("flux personnels : %+v", s.Feeds)
	}
	if s.Proxy.Type != "mediaflow" || s.Proxy.Problem() != "" {
		t.Fatalf("relais : %+v %q", s.Proxy, s.Proxy.Problem())
	}
	if s.Every != time.Minute {
		t.Fatalf("intervalle par défaut : %v", s.Every)
	}
	if s2 := LoadPreDBSettingsWith(getFrom(map[string]string{"PSE_PREDB_INTERVAL": "5s"})); s2.Every != 30*time.Second {
		t.Fatalf("minimum 30 s : %v", s2.Every)
	}
	legacy := LoadPreDBSettingsWith(getFrom(map[string]string{"PSE_PREDB_PRIVATE_URL": "https://a.example/x https://b.example/y", "PSE_PREDB_PRIVATE_KEY": "abc"}))
	if len(legacy.Feeds) != 2 || legacy.Feeds[1].Key != "abc" {
		t.Fatalf("ancien réglage : %+v", legacy.Feeds)
	}
	for _, p := range []PreProxy{{Type: "proxy", URL: ""}, {Type: "proxy", URL: "ftp://x:1"}, {Type: "proxy", URL: "http://u:p@x:1"}, {Type: "mediaflow", URL: "https://r.example"}} {
		if p.Problem() == "" {
			t.Errorf("proxy invalide accepté : %+v", p)
		}
	}
	for _, p := range []PreProxy{{}, {Type: "proxy", URL: "http://gluetun:8888"}, {Type: "proxy", URL: "socks5://10.0.0.2:1080"}} {
		if p.Problem() != "" {
			t.Errorf("proxy valide refusé : %+v %q", p, p.Problem())
		}
	}
}

func TestPreAcceptKeepsAnimeAndDocumentariesButNotMusic(t *testing.T) {
	langs := []string{"fr", "multi", "vostfr"}
	for _, c := range []struct {
		name, section string
		want          bool
	}{
		{"Sakamoto.Days.S02E07.VOSTFR.1080p.WEB.H264-ONi", "ANiME", true},
		{"Un.Docu.2023.FRENCH.1080p.WEB.H264-GRP", "DOCU", true},
		{"Un.Film.2024.MULTi.1080p.BLURAY.x264-GRP", "BLURAY", true},
		{"Un.Album.FRENCH-WEB-2026-GRP", "MP3-WEB", false},
		{"Devils.Crest.S01E04.GERMAN.DL.ANiME.720p.WEB.h264-SAUERKRAUT", "ANiME", false},
		{"Un.Jeu.FRENCH.2024-GRP", "GAMES", false},
	} {
		if _, ok := PreAccept(c.name, c.section, langs); ok != c.want {
			t.Errorf("%s [%s] : %v, attendu %v", c.name, c.section, ok, c.want)
		}
	}
}

func TestParsePreFeedReadsPredbAPIJSONWithUnixTime(t *testing.T) {
	items, err := ParsePreFeed([]byte(`{"status":"success","results":2,"data":[
		{"id":1,"pretime":1795238359,"release":"Sakamoto.Days.S02E07.VOSTFR.1080p.WEB.H264-ONi","section":"ANiME"},
		{"id":2,"pretime":1795238300,"release":"Un.Film.2024.FRENCH.1080p.WEB.H264-GRP","section":"X264-HD"}]}`))
	if err != nil || len(items) != 2 || items[0].Section != "ANiME" || items[0].At.Unix() != 1795238359 {
		t.Fatalf("%v %+v", err, items)
	}
}

// Un proxy HTTP reçoit la requête avec les identifiants du proxy, et les flux publics comme privés y passent.
func TestFetchPreThroughProxyUsesCredentialsAndAlsoForPrivateFeeds(t *testing.T) {
	var proxyAuth, proxied string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") == "" {
			w.Header().Set("Proxy-Authenticate", `Basic realm="p"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		proxyAuth, proxied = r.Header.Get("Proxy-Authorization"), r.URL.String()
		w.Write([]byte(rssSample))
	}))
	defer proxy.Close()
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(rssSample)) }))
	defer direct.Close()
	oldValidate := preValidateTarget
	preValidateTarget = func(context.Context, string) error { return nil }
	defer func() { preValidateTarget = oldValidate }()
	oldClient := preClient
	preClient = direct.Client()
	defer func() { preClient = oldClient }()

	cfg := PreDBSettings{Langs: []string{"fr", "multi", "vostfr"}, Proxy: PreProxy{Type: "proxy", URL: proxy.URL, Auth: "bob:pa ss"}}
	items, err := fetchPre(context.Background(), preFeed{url: "http://pre.example/feed", public: true}, cfg)
	if err != nil || len(items) != 5 {
		t.Fatalf("via le proxy : %v %d", err, len(items))
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("bob:pa ss"))
	if proxyAuth != want || proxied != "http://pre.example/feed" {
		t.Fatalf("identifiants %q, adresse %q", proxyAuth, proxied)
	}
	// Sans identifiants, le proxy refuse : message clair.
	cfg.Proxy.Auth = ""
	if _, err := fetchPre(context.Background(), preFeed{url: "http://pre.example/feed", public: true}, cfg); err == nil || !strings.Contains(err.Error(), "407") {
		t.Fatalf("407 attendu : %v", err)
	}
	// Un flux privé passe aussi par le proxy.
	cfg.Proxy.Auth = "bob:pa ss"
	proxied = ""
	if _, err := fetchPre(context.Background(), preFeed{url: "http://prive.example/feed", key: testKey}, cfg); err != nil || proxied != "http://prive.example/feed" {
		t.Fatalf("flux privé : %v, proxy %q", err, proxied)
	}
	// Proxy injoignable.
	cfg.Proxy = PreProxy{Type: "proxy", URL: "http://127.0.0.1:1"}
	if _, err := fetchPre(context.Background(), preFeed{url: "http://pre.example/feed", public: true}, cfg); err == nil || err.Error() != "proxy inaccessible" {
		t.Fatalf("proxy injoignable : %v", err)
	}
}

func decryptMediaFlowToken(t *testing.T, token, password string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 32 {
		t.Fatalf("jeton illisible : %v", err)
	}
	key := bytes.Repeat([]byte{' '}, 32)
	copy(key, password)
	block, _ := aes.NewCipher(key)
	body := raw[16:]
	cipher.NewCBCDecrypter(block, raw[:16]).CryptBlocks(body, body)
	body = body[:len(body)-int(body[len(body)-1])]
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("jeton invalide : %v", err)
	}
	return data
}

// Le relais MediaFlow reçoit l'adresse du flux dans un jeton chiffré et le mot de passe en en-tête.
func TestFetchPreThroughMediaFlowRelay(t *testing.T) {
	var gotPath, gotTarget, gotPassword, gotKey string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api_password") != "s3cret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		data := decryptMediaFlowToken(t, r.URL.Query().Get("token"), "s3cret")
		gotPath, gotPassword = r.URL.Path, r.Header.Get("api_password")
		gotTarget, _ = data["d"].(string)
		gotKey, _ = data["h_x-api-key"].(string)
		w.Write([]byte(rssSample))
	}))
	defer relay.Close()
	oldValidate := preValidateTarget
	preValidateTarget = func(context.Context, string) error { return nil }
	defer func() { preValidateTarget = oldValidate }()

	cfg := PreDBSettings{Langs: []string{"fr"}, Proxy: PreProxy{Type: "mediaflow", URL: relay.URL, Auth: "s3cret"}}
	items, err := fetchPre(context.Background(), preFeed{url: "https://predb.me/?cats=movies&rss=1", public: true}, cfg)
	if err != nil || len(items) != 5 {
		t.Fatalf("via MediaFlow : %v %d", err, len(items))
	}
	if gotTarget != "https://predb.me/?cats=movies&rss=1" || gotPath != "/proxy/stream" || gotPassword != "s3cret" {
		t.Fatalf("cible %q, chemin %q", gotTarget, gotPath)
	}
	// Les API (api.…) passent par /proxy/forward, comme pour AllDebrid.
	if _, err := fetchPre(context.Background(), preFeed{url: "https://api.predb.net/?type=section&q=ANiME", public: true}, cfg); err != nil || gotPath != "/proxy/forward" {
		t.Fatalf("API : %v %q", err, gotPath)
	}
	// Un flux privé passe par le relais, avec sa clé transmise dans le jeton.
	if _, err := fetchPre(context.Background(), preFeed{url: "https://api.exemple.net/api/v1/releases", key: testKey}, cfg); err != nil || gotKey != testKey || gotTarget != "https://api.exemple.net/api/v1/releases" {
		t.Fatalf("flux privé via MediaFlow : %v clé %q cible %q", err, gotKey, gotTarget)
	}
	cfg.Proxy.Auth = "mauvais"
	if _, err := fetchPre(context.Background(), preFeed{url: "https://predb.me/?cats=tv&rss=1", public: true}, cfg); err == nil || err.Error() != "relais refusé (401)" {
		t.Fatalf("mot de passe refusé : %v", err)
	}
}

// Le test des flux lit chaque flux maintenant et renvoie le détail, sans rien enregistrer.
func TestTestPreFeedsReportsCountsSamplesAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ko" {
			http.Error(w, "x", http.StatusForbidden)
			return
		}
		w.Write([]byte(rssSample))
	}))
	defer srv.Close()
	oldClient := preClient
	preClient = srv.Client()
	defer func() { preClient = oldClient }()
	cfg := PreDBSettings{Langs: []string{"fr", "vostfr"}, Feeds: []PreFeedConf{{Name: "Mon site", URL: srv.URL + "/ok", Key: testKey}, {URL: srv.URL + "/ko"}}}
	res := TestPreFeeds(context.Background(), cfg)
	if len(res.Runs) != 2 || res.Proxy != "" {
		t.Fatalf("%+v", res)
	}
	ok, ko := res.Runs[0], res.Runs[1]
	if ok.State != "ok" || ok.Label != "Mon site" || ok.Fetched != 5 || ok.Kept != 3 || len(ok.Sample) != 3 || ok.Feed != "perso:1" {
		t.Fatalf("flux lisible : %+v", ok)
	}
	if ko.State != "error" || ko.Error != "flux refusé (403)" || ko.Feed != "perso:2" {
		t.Fatalf("flux refusé : %+v", ko)
	}
	if strings.Contains(string(mustJSON(t, res)), srv.URL) {
		t.Fatal("l'adresse d'un flux personnel ne doit jamais apparaître dans le résultat")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
