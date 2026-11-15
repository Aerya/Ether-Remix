package egress

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func decodeMediaFlow(t *testing.T, token, password string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{' '}, 32)
	copy(key, []byte(password))
	block, _ := aes.NewCipher(key)
	body := append([]byte(nil), raw[16:]...)
	cipher.NewCBCDecrypter(block, raw[:16]).CryptBlocks(body, body)
	body = body[:len(body)-int(body[len(body)-1])]
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	return data
}
func TestMediaFlowAPIAndStream(t *testing.T) {
	previous := Current()
	t.Cleanup(func() { Set(previous) })
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "api", true: "stream"}[stream], func(t *testing.T) {
			hits := 0
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				want := "/proxy/forward"
				if stream {
					want = "/proxy/stream"
				}
				if r.URL.Path != "/relay"+want {
					t.Errorf("path %s", r.URL.Path)
				}
				if r.Header.Get("api_password") != "test-password" {
					t.Error("missing relay authentication")
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("upstream credential sent to relay headers")
				}
				data := decodeMediaFlow(t, r.URL.Query().Get("token"), "test-password")
				if data["h_accept-encoding"] != "identity" || r.Header.Get("Accept-Encoding") != "identity" {
					t.Error("relay must use identity encoding on both hops")
				}
				if data["d"] != "https://api.alldebrid.com/v4/user?agent=ether" || data["h_authorization"] != "Bearer upstream-secret" || data["h_range"] != "bytes=5-9" {
					t.Errorf("bad destination/headers: %v", data)
				}
				if data["exp"].(float64) < float64(time.Now().Unix()) {
					t.Error("expired token")
				}
				b, _ := io.ReadAll(r.Body)
				if !stream && string(b) != "body-data" {
					t.Errorf("body %q", b)
				}
				w.Header().Set("Content-Range", "bytes 5-9/10")
				w.WriteHeader(206)
				io.WriteString(w, "56789")
			}))
			defer relay.Close()
			Set(Policy{MediaFlow: config.MediaFlow{Enabled: true, URL: relay.URL + "/relay", Password: "test-password"}})
			method := "POST"
			if stream {
				method = "GET"
			}
			req, _ := http.NewRequest(method, "https://api.alldebrid.com/v4/user?agent=ether", strings.NewReader("body-data"))
			req.Header.Set("Authorization", "Bearer upstream-secret")
			req.Header.Set("Range", "bytes=5-9")
			client := &http.Client{Transport: WrapMediaFlow(http.DefaultTransport, stream)}
			defer client.CloseIdleConnections()
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			b, _ := io.ReadAll(res.Body)
			if res.StatusCode != 206 || string(b) != "56789" || res.Header.Get("Content-Range") != "bytes 5-9/10" || hits != 1 {
				t.Fatal("stream response changed")
			}
		})
	}
}
func TestMediaFlowNeverFallsBack(t *testing.T) {
	previous := Current()
	t.Cleanup(func() { Set(previous) })
	directHits := 0
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directHits++ }))
	defer direct.Close()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, direct.URL, 302) }))
	defer relay.Close()
	Set(Policy{MediaFlow: config.MediaFlow{Enabled: true, URL: relay.URL, Password: "test"}})
	client := Client(CapDebrid, time.Second)
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(WithAllDebrid(t.Context()), "GET", direct.URL, nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("relay redirect accepted")
	}
	relay.Close()
	if _, err := client.Do(req); err == nil {
		t.Fatal("unavailable relay accepted")
	}
	Set(Policy{MediaFlow: config.MediaFlow{Enabled: true}})
	if _, err := client.Do(req); err == nil {
		t.Fatal("invalid relay accepted")
	}
	if directHits != 0 {
		t.Fatal("direct fallback occurred")
	}
	// Unrelated services retain their original route even with a broken relay.
	res, err := client.Get(direct.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if directHits != 1 {
		t.Fatal("unrelated provider changed")
	}
}
func TestAllDebridDomainBoundaries(t *testing.T) {
	for host, want := range map[string]bool{"API.ALLDEBRID.COM.": true, "cdn.alld.io": true, "debrid.it": true, "alldebrid.com.attacker.test": false, "evilalld.io": false, "real-debrid.com": false} {
		if IsAllDebridHost(host) != want {
			t.Errorf("%s", host)
		}
	}
}
