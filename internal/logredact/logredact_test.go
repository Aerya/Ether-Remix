package logredact

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"
)

const secret = "s3cr3tVALUE42"

func TestStringRemovesCredentials(t *testing.T) {
	for _, in := range []string{
		"GET /library/sections?X-Plex-Token=" + secret + "&type=1",
		"GET /library/sections?%58-Plex-Token=" + secret,
		"GET /library/sections?x%2Dplex%2Dtoken=" + secret,
		"GET /library/sections?%2558-Plex-Token=" + secret,
		"GET /library/sections?X_PLEX_TOKEN=" + secret,
		`Get "https://indexer.example/api?t=search&apikey=` + secret + `&q=x": dial tcp: i/o timeout`,
		"https://api.themoviedb.org/3/movie/1?api_key=" + secret,
		"nntps://user:" + secret + "@news.example:563",
		"https://user:" + secret + "@debrid.example/api",
		"Authorization: Bearer " + secret,
		"request headers map[Authorization:[Bearer " + secret + "] Accept:[*/*]]",
		"Authorization: " + secret,
		"authorization=Basic " + secret,
		"X-Plex-Token: " + secret,
		"map[X-Plex-Token:[" + secret + "]]",
		"Cookie: session=" + secret + "; theme=dark",
		`{"username":"owner","password":"` + secret + `","port":563}`,
		`{"apiKey": "` + secret + `"}`,
		"login failed token=" + secret,
		"exchanged for ether-admin-" + secret,
		"delegated ether-cast-" + secret,
		"recovery session ether-recovery-" + secret,
		"X-Ether-Recovery-Token: " + secret,
		"map[X-Ether-Recovery-Token:[" + secret + "]]",
		"rss https://tracker.example/rss?passkey=" + secret,
		"url=https://x.example/?apikey=" + secret,
		`time=2026-10-30T09:00:00Z level=WARN msg=fetch url="https://x.example/api?t=search&apikey=` + secret + `"`,
	} {
		out := String(in)
		if strings.Contains(out, secret) {
			t.Errorf("leaked: %q -> %q", in, out)
		}
		if !strings.Contains(out, Mask) {
			t.Errorf("no mask: %q -> %q", in, out)
		}
	}
}

func TestStringLeavesBenignTextAlone(t *testing.T) {
	for _, in := range []string{
		"listening on :32400",
		"GET /library/metadata/4000013990010003?includeGuids=1&X-Plex-Product=Plex%20Web",
		"https://api.themoviedb.org/3/tv/1399?language=en-US&append_to_response=credits",
		"token=",
		"tokens=12 remaining=3",
		"tokenRejected=false",
		"plex.tv authorization: unauthorized",
		"https://news.example:563 user=owner",
		"segment cache over budget: used=10 budget=5",
		"cookie banner dismissed",
		"Bearer",
		`{"title":"Breaking: the key=value story"}`,
		"",
	} {
		if out := String(in); out != in {
			t.Errorf("changed benign text: %q -> %q", in, out)
		}
	}
}

func TestStringIsIdempotent(t *testing.T) {
	in := "GET /x?X-Plex-Token=" + secret + " Authorization: Bearer " + secret
	once := String(in)
	if twice := String(once); twice != once {
		t.Fatalf("%q then %q", once, twice)
	}
}

type stringer string

func (s stringer) String() string { return string(s) }

func TestHandlerScrubsEveryPart(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	u, _ := url.Parse("https://indexer.example/api?apikey=" + secret)
	log = log.With("password", secret, slog.Group("conn", "url", "nntp://u:"+secret+"@h"))
	log.WithGroup("req").Info("request to https://x.example/?token="+secret+" failed",
		"err", errors.New(`Get "https://x.example/?X-Plex-Token=`+secret+`": EOF`),
		"wrapped", fmt.Errorf("retry: %w", errors.New("Authorization: Bearer "+secret)),
		"url", u,
		"stringer", stringer("ether-admin-"+secret),
		"body", []byte(`{"token":"`+secret+`"}`),
		"X-Plex-Token", secret,
		slog.Group("headers", "Authorization", "Bearer "+secret, "Accept", "*/*"),
		"token", false,
		"tokens", 3,
		"elapsed", time.Second,
		"path", "/library/sections",
	)
	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("leaked:\n%s", out)
	}
	for _, keep := range []string{"req.token=false", "req.tokens=3", "req.elapsed=1s", "req.path=/library/sections", "req.headers.Accept=*/*", "conn.url=nntp://u:[redacted]@h"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q:\n%s", keep, out)
		}
	}
}

func TestHandlerKeepsLevelsAndBenignAttrs(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	log.Info("dropped")
	log.Warn("kept", "count", 2, "title", "The Show S01E03")
	if out := buf.String(); strings.Contains(out, "dropped") || !strings.Contains(out, `msg=kept count=2 title="The Show S01E03"`) {
		t.Fatalf("%s", out)
	}
}

func TestEtherCredentialNames(t *testing.T) {
	for _, name := range []string{"X-Ether-Stream-Token", "X-Ether-Read-Token", "stream_token", "read_token", "X-Plex-Token", "api_key", "X-Ether-Admin-Token", "X-Ether-Recovery-Token", "plugin_token", "share_token", "download_token"} {
		for _, input := range []string{"/path?" + name + "=credential123", name + "=credential123", `{"` + name + `":"credential123"}`} {
			if got := String(input); strings.Contains(got, "credential123") {
				t.Errorf("%q leaked: %q", input, got)
			}
		}
	}
}

func TestCapabilityShapesAreRedacted(t *testing.T) {
	const secret = "sensitiveValue_12345"
	inputs := []string{
		"/stremio/" + secret + "/manifest.json", "/m3u/" + secret + "/playlist.m3u",
		"/stremio/image/" + secret, "/m3u/image/" + secret, "/silo/api/v2/images/" + secret,
		"/ether/api/addon-relay/" + secret + "/configure", "/src/session/" + secret,
		"/sessions/lazy/" + secret + "/file", "/sessions/media/session/" + secret + "/file",
		"X-Ether-Plugin-Credential: " + secret, "X-Ether-Backup-Password: " + secret, "X-Ether-Pick: " + secret,
		"X-Emby-Token: " + secret, "X-MediaBrowser-Token: " + secret, "X-Room-Token: " + secret,
		"Authorization: MediaBrowser Client=TV, Token=\"" + secret + "\"",
		"Sec-WebSocket-Protocol: ether-credential." + secret,
		"Sec-WebSocket-Protocol: silo.ticket." + secret,
		"credential=eg1_" + secret, "refresh_token=er1_" + secret,
		"?tag=" + secret, `{"ImageTags":{"Primary":"` + secret + `"}}`, `{"BackdropImageTags":["` + secret + `"]}`, `{"device_code":"` + secret + `"}`, "?OpenToken=" + secret, "?code=" + secret, "?code_verifier=" + secret,
		"#invite=" + secret, `{"join_token":"` + secret + `","room_access_token":"` + secret + `","ticket":"` + secret + `"}`,
	}
	for _, input := range inputs {
		if got := String(input); strings.Contains(got, secret) {
			t.Errorf("%q leaked: %q", input, got)
		}
	}
}

func TestMagnetAndTrackerCredentials(t *testing.T) {
	const (
		infoHash = "0123456789abcdef0123456789abcdef01234567"
		hexKey   = "deadbeefcafebabe0123456789abcdefdeadbeef"
		host     = "tracker.example.com"
	)
	cases := []struct {
		name      string
		in        string
		leak      []string
		keepHosts []string
	}{
		{
			name:      "encoded tr passkey",
			in:        "magnet:?xt=urn:btih:" + infoHash + "&dn=Movie.2024&tr=" + url.QueryEscape("https://"+host+":8443/announce?passkey="+secret+"&compact=1"),
			leak:      []string{secret},
			keepHosts: []string{host, infoHash},
		},
		{
			name:      "encoded tr pass and numbered xs authkey torrent_pass",
			in:        "magnet:?xt=urn:btih:" + infoHash + "&tr.0=" + url.QueryEscape("https://"+host+"/announce?pass="+secret) + "&xs.0=" + url.QueryEscape("https://"+host+"/download.php?id=9&authkey="+secret+"&torrent_pass="+hexKey),
			leak:      []string{secret, hexKey},
			keepHosts: []string{host, infoHash},
		},
		{
			name:      "encoded path passkeys after and before announce",
			in:        "magnet:?xt=urn:btih:" + infoHash + "&tr=" + url.QueryEscape("https://"+host+"/announce/"+hexKey) + "&tr=" + url.QueryEscape("https://b."+host+"/"+hexKey+"/announce"),
			leak:      []string{hexKey},
			keepHosts: []string{host, "b." + host, infoHash},
		},
		{
			name:      "plain error string and pass query key",
			in:        `Get "https://` + host + `/rss?uid=7&pass=` + secret + `": EOF; announce https://` + host + `/announce/` + hexKey,
			leak:      []string{secret, hexKey},
			keepHosts: []string{host},
		},
		{
			name:      "double encoded magnet and pre-masked support bundle host",
			in:        "form magnets%5B%5D=" + url.QueryEscape("magnet:?xt=urn:btih:"+infoHash+"&tr="+url.QueryEscape("https://"+host+"/announce?torrent_pass="+secret)) + " bundle=magnet:?xt=urn:btih:" + infoHash + "&tr=https%3A%2F%2F[redacted]%2Fannounce%3Fpasskey%3D" + secret,
			leak:      []string{secret},
			keepHosts: []string{host, infoHash},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := String(tc.in)
			for _, bad := range tc.leak {
				if strings.Contains(out, bad) {
					t.Fatalf("leaked %q in %q", bad, out)
				}
			}
			if !strings.Contains(out, Mask) {
				t.Fatalf("missing mask in %q", out)
			}
			for _, keep := range tc.keepHosts {
				if !strings.Contains(out, keep) {
					t.Fatalf("lost %q in %q", keep, out)
				}
			}
			if twice := String(out); twice != out {
				t.Fatalf("not idempotent:\n once: %s\ntwice: %s", out, twice)
			}
		})
	}

	benign := "magnet:?xt=urn:btih:" + infoHash + "&dn=Basic%20Instinct%201992%201080p&tr=" + url.QueryEscape("udp://tracker.opentrackr.org:1337/announce")
	if got := String(benign); got != benign {
		t.Fatalf("benign magnet modified:\n in: %s\ngot: %s", benign, got)
	}
}

func TestStringRemovesAPIKeysAndMediaBrowserPasswords(t *testing.T) {
	apiKey := "ek1_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123456"
	for _, in := range []string{
		"X-Emby-Token: " + apiKey,
		"X-MediaBrowser-Token: " + apiKey,
		`Authorization: MediaBrowser Token="` + apiKey + `"`,
		"GET /System/Info?api_key=" + apiKey,
		"DELETE /Auth/Keys/" + apiKey,
		"POST /Auth/Keys/" + apiKey + "/Delete",
		"DELETE /Auth/Keys/" + secret,
		`{"AccessToken":"` + apiKey + `"}`,
		`{"NewPw":"` + secret + `","CurrentPw":"` + secret + `"}`,
		`{"CurrentPassword":"` + secret + `","Pw":"` + secret + `"}`,
	} {
		out := String(in)
		if strings.Contains(out, apiKey) || strings.Contains(out, secret) {
			t.Errorf("leaked credential: %q -> %q", in, out)
		}
		if !strings.Contains(out, Mask) {
			t.Errorf("missing mask: %q -> %q", in, out)
		}
	}
}
