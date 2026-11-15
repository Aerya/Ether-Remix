package egress

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type allDebridContextKey struct{}

func WithAllDebrid(ctx context.Context) context.Context {
	return context.WithValue(ctx, allDebridContextKey{}, true)
}
func IsAllDebridHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, domain := range []string{"alldebrid.com", "alldebrid.fr", "alld.io", "debrid.it"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

type mediaFlowTransport struct {
	base     http.RoundTripper
	relay    *http.Transport
	stream   bool
	validate func(context.Context, string) error
}

// WrapMediaFlow preserves ordinary routing for other providers. The relay connection
// itself bypasses the source proxy: MediaFlow owns its own egress policy.
func WrapMediaFlow(base http.RoundTripper, stream bool) http.RoundTripper {
	relay := baseTransport()
	relay.Proxy = nil
	return &mediaFlowTransport{base: base, relay: relay, stream: stream}
}

// WrapGuardedMediaFlow retains the guarded base for ordinary addon links and
// validates relay destinations before passing them to the trusted MediaFlow server.
func WrapGuardedMediaFlow(base http.RoundTripper, validate func(context.Context, string) error) http.RoundTripper {
	t := WrapMediaFlow(base, true).(*mediaFlowTransport)
	t.validate = validate
	return t
}
func (t *mediaFlowTransport) CloseIdleConnections() {
	t.relay.CloseIdleConnections()
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

var errMediaFlow = errors.New("AllDebrid MediaFlow relay unavailable; direct fallback is disabled")

func (t *mediaFlowTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cfg := Current().MediaFlow
	forced, _ := req.Context().Value(allDebridContextKey{}).(bool)
	if !cfg.Enabled || (!forced && !IsAllDebridHost(req.URL.Hostname())) {
		return t.base.RoundTrip(req)
	}
	fail := func() (*http.Response, error) {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, errMediaFlow
	}
	if t.validate != nil {
		if err := t.validate(req.Context(), req.URL.String()); err != nil {
			return fail()
		}
	}
	if len(cfg.Problems()) != 0 {
		return fail()
	}
	root, _ := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.URL), "/"))
	endpoint := "/proxy/forward"
	if t.stream || ((req.Method == http.MethodGet || req.Method == http.MethodHead) && !strings.HasPrefix(strings.ToLower(req.URL.Hostname()), "api.")) {
		endpoint = "/proxy/stream"
	}
	root.Path += endpoint
	// Some MediaFlow versions decompress upstream API bodies while retaining
	// Content-Encoding. Request identity on both hops to avoid double decoding
	// and to preserve byte ranges for media.
	data := map[string]any{"d": req.URL.String(), "exp": time.Now().Add(5 * time.Minute).Unix(), "h_accept-encoding": "identity"}
	for name, values := range req.Header {
		switch strings.ToLower(name) {
		case "authorization", "content-type", "range", "if-range", "user-agent", "referer", "origin", "cookie", "accept":
			data["h_"+strings.ToLower(name)] = strings.Join(values, ", ")
		}
	}
	token, err := mediaFlowToken(data, cfg.Password)
	if err != nil {
		return fail()
	}
	root.RawQuery = url.Values{"token": {token}}.Encode()
	routed := req.Clone(req.Context())
	routed.URL = root
	routed.Host = ""
	routed.Header = make(http.Header)
	routed.Header.Set("api_password", cfg.Password)
	routed.Header.Set("Accept-Encoding", "identity")
	if ct := req.Header.Get("Content-Type"); ct != "" {
		routed.Header.Set("Content-Type", ct)
	}
	resp, err := t.relay.RoundTrip(routed)
	if err != nil {
		return nil, errMediaFlow
	}
	// Never let the outer client follow a redirect from the relay, including a
	// login redirect that could leak credentials or silently bypass the relay.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, errMediaFlow
	}
	resp.Request = req
	return resp, nil
}

// Matches MediaFlow EncryptionHandler: UTF-8 key space-padded/truncated to 32
// bytes, AES-CBC with random IV and PKCS#7, URL-safe base64 without padding.
// Tokens are short-lived credentials; use HTTPS when MediaFlow is remote.
func mediaFlowToken(data map[string]any, password string) (string, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	key := bytes.Repeat([]byte{' '}, 32)
	copy(key, []byte(password))
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	pad := aes.BlockSize - len(raw)%aes.BlockSize
	raw = append(raw, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, aes.BlockSize+len(raw))
	if _, err = io.ReadFull(rand.Reader, out[:aes.BlockSize]); err != nil {
		return "", err
	}
	cipher.NewCBCEncrypter(block, out[:aes.BlockSize]).CryptBlocks(out[aes.BlockSize:], raw)
	return base64.RawURLEncoding.EncodeToString(out), nil
}
