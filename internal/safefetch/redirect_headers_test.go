package safefetch

import (
	"net/http"
	"net/url"
	"testing"
)

func TestParseRedirectHeaderPatterns(t *testing.T) {
	valid := []struct {
		in   string
		want string
		wild bool
	}{
		{"cdn.example.net", "cdn.example.net", false},
		{"CDN.Example.Net:8443", "cdn.example.net:8443", false},
		{"127.0.0.1:50002", "127.0.0.1:50002", false},
		{"[::1]:8443", "[::1]:8443", false},
		{"*.example.org", "*.example.org", true},
		{"*.storage.example.org", "*.storage.example.org", true},
		{"*.myapp.github.io", "*.myapp.github.io", true},
	}
	for _, tc := range valid {
		p, err := ParseRedirectHeaderPattern(tc.in)
		if err != nil {
			t.Fatalf("ParseRedirectHeaderPattern(%q) unexpected error: %v", tc.in, err)
		}
		if p.Raw != tc.want || p.Wildcard != tc.wild {
			t.Fatalf("ParseRedirectHeaderPattern(%q) = %+v, want Raw=%q Wildcard=%v", tc.in, p, tc.want, tc.wild)
		}
	}

	invalid := []string{
		"",
		"*",
		"operator",
		"OPERATOR",
		"https://cdn.example.net",
		"cdn.example.net/path",
		"*.com",
		"*.co.uk",
		"*.github.io",
		"*.s3.amazonaws.com",
		"*.local",
		"*.localhost",
		"*.127.0.0.1",
		"*.example.org:443",
		"*.*.example.org",
		"sub.*.example.org",
		"*.example.org.",
	}
	for _, in := range invalid {
		if p, err := ParseRedirectHeaderPattern(in); err == nil {
			t.Fatalf("ParseRedirectHeaderPattern(%q) = %+v, want error", in, p)
		}
	}

	tooMany := make([]string, MaxRedirectHeaderPatterns+1)
	for i := range tooMany {
		tooMany[i] = "cdn.example.net"
	}
	if _, err := ParseRedirectHeaderPatterns(tooMany); err == nil {
		t.Fatalf("expected error for >%d patterns", MaxRedirectHeaderPatterns)
	}
}

func TestRedirectHeaderStateInvariants(t *testing.T) {
	mustURL := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	mustPatterns := func(raw ...string) []RedirectHeaderPattern {
		p, err := ParseRedirectHeaderPatterns(raw)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	makeInitialHeaders := func() http.Header {
		h := make(http.Header)
		h.Set("Authorization", "Bearer tok")
		h.Set("Cookie", "sid=secret")
		h.Set("X-Api-Key", "api-key")
		h.Set("Accept", "application/json")
		h.Set("User-Agent", "ether-test/1.0")
		return h
	}
	// simulateGoRedirect mimics net/http.Client's copyHeaders + Referer step
	// before CheckRedirect is called.
	simulateGoRedirect := func(initHeaders http.Header, prevURL, nextURL *url.URL, goDroppedSensitive bool) *http.Request {
		req := &http.Request{URL: nextURL, Header: initHeaders.Clone()}
		if goDroppedSensitive {
			for _, k := range restorableSensitiveHeaders {
				req.Header.Del(k)
			}
		}
		if prevURL != nil && !(prevURL.Scheme == "https" && nextURL.Scheme == "http") {
			req.Header.Set("Referer", prevURL.String())
		}
		return req
	}

	t.Run("strict same-site subdomain keeps credentials and strips auto referer", func(t *testing.T) {
		initU := mustURL("https://example.org/download/item/file?token=querysecret")
		initH := makeInitialHeaders()
		state := NewRedirectHeaderState(initU, RedirectHeaderOptions{InitialHeaders: initH})
		nextU := mustURL("https://node123.storage.example.org/items/item/file")
		next := simulateGoRedirect(initH, initU, nextU, false)

		state.Apply(next)

		if got := next.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("Authorization = %q, want kept", got)
		}
		if got := next.Header.Get("Cookie"); got != "sid=secret" {
			t.Fatalf("Cookie = %q, want kept", got)
		}
		if got := next.Header.Get("X-Api-Key"); got != "api-key" {
			t.Fatalf("X-Api-Key = %q, want kept", got)
		}
		if got := next.Header.Get("Referer"); got != "" {
			t.Fatalf("auto Referer leaked across host: %q", got)
		}
	})

	t.Run("explicit caller Referer preserved on trusted subdomain redirect", func(t *testing.T) {
		initU := mustURL("https://example.org/download/item")
		initH := makeInitialHeaders()
		initH.Set("Referer", "https://example.org/item")
		state := NewRedirectHeaderState(initU, RedirectHeaderOptions{InitialHeaders: initH})
		nextU := mustURL("https://node123.storage.example.org/file")
		next := simulateGoRedirect(initH, initU, nextU, false)

		state.Apply(next)

		if got := next.Header.Get("Referer"); got != "https://example.org/item" {
			t.Fatalf("explicit Referer = %q, want preserved", got)
		}
	})

	t.Run("https to http downgrade strips credentials even when explicitly listed", func(t *testing.T) {
		initU := mustURL("https://example.org/download")
		initH := makeInitialHeaders()
		state := NewRedirectHeaderState(initU, RedirectHeaderOptions{
			InitialHeaders: initH,
			Patterns:       mustPatterns("example.org", "*.example.org"),
		})
		nextU := mustURL("http://node123.storage.example.org/file")
		next := simulateGoRedirect(initH, initU, nextU, false)

		state.Apply(next)

		for _, k := range []string{"Authorization", "Cookie", "X-Api-Key", "Referer"} {
			if got := next.Header.Get(k); got != "" {
				t.Fatalf("downgrade kept %s=%q", k, got)
			}
		}
		if got := next.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("safe Accept header = %q, want kept", got)
		}
	})

	t.Run("http -> https -> http downgrade strips credentials", func(t *testing.T) {
		initU := mustURL("http://example.org/start")
		initH := makeInitialHeaders()
		state := NewRedirectHeaderState(initU, RedirectHeaderOptions{InitialHeaders: initH})

		hop1U := mustURL("https://example.org/mid")
		hop1 := simulateGoRedirect(initH, initU, hop1U, false)
		state.Apply(hop1)
		if hop1.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("http->https upgrade dropped Authorization")
		}

		hop2U := mustURL("http://example.org/end")
		hop2 := simulateGoRedirect(hop1.Header, hop1U, hop2U, false)
		state.Apply(hop2)
		if hop2.Header.Get("Authorization") != "" || hop2.Header.Get("Cookie") != "" {
			t.Fatalf("https->http after upgrade kept credentials: %v", hop2.Header)
		}
	})

	t.Run("public suffix isolation blocks github.io and s3.amazonaws.com", func(t *testing.T) {
		cases := [][2]string{
			{"https://github.io/a", "https://evil.github.io/b"},
			{"https://app1.github.io/a", "https://app2.github.io/b"},
			{"https://s3.amazonaws.com/a", "https://bucket.s3.amazonaws.com/b"},
			{"https://co.uk/a", "https://evil.co.uk/b"},
			{"https://nas.local/a", "https://sub.nas.local/b"},
			{"http://127.0.0.1:50001/a", "http://127.0.0.1:50002/b"},
			{"https://example.org/a", "https://node1.storage.example.org:8443/b"},
		}
		for _, tc := range cases {
			initU := mustURL(tc[0])
			initH := makeInitialHeaders()
			state := NewRedirectHeaderState(initU, RedirectHeaderOptions{InitialHeaders: initH})
			next := simulateGoRedirect(initH, initU, mustURL(tc[1]), false)
			state.Apply(next)
			if next.Header.Get("Authorization") != "" || next.Header.Get("Cookie") != "" || next.Header.Get("X-Api-Key") != "" {
				t.Fatalf("%s -> %s leaked credentials: %v", tc[0], tc[1], next.Header)
			}
		}
	})

	t.Run("sibling subdomain denied by default, restored with keepHeadersOnRedirect", func(t *testing.T) {
		initU := mustURL("https://api.example.org/download")
		initH := makeInitialHeaders()
		nextU := mustURL("https://node123.storage.example.org/file")

		// Default: stripped.
		defState := NewRedirectHeaderState(initU, RedirectHeaderOptions{InitialHeaders: initH})
		defNext := simulateGoRedirect(initH, initU, nextU, true)
		defState.Apply(defNext)
		if defNext.Header.Get("Authorization") != "" || defNext.Header.Get("X-Api-Key") != "" {
			t.Fatalf("sibling subdomain without opt-in kept headers: %v", defNext.Header)
		}

		// With explicit keepHeadersOnRedirect: Go-dropped Authorization & Cookie restored!
		optState := NewRedirectHeaderState(initU, RedirectHeaderOptions{
			InitialHeaders: initH,
			Patterns:       mustPatterns("*.storage.example.org"),
		})
		optNext := simulateGoRedirect(initH, initU, nextU, true)
		optState.Apply(optNext)
		if optNext.Header.Get("Authorization") != "Bearer tok" || optNext.Header.Get("Cookie") != "sid=secret" || optNext.Header.Get("X-Api-Key") != "api-key" {
			t.Fatalf("sibling subdomain with keepHeadersOnRedirect missing restored headers: %v", optNext.Header)
		}
	})

	t.Run("sticky confinement across A -> untrusted B -> A bounce", func(t *testing.T) {
		initU := mustURL("https://example.org/start")
		initH := makeInitialHeaders()
		state := NewRedirectHeaderState(initU, RedirectHeaderOptions{InitialHeaders: initH})

		hop1U := mustURL("https://thirdparty.net/bounce")
		hop1 := simulateGoRedirect(initH, initU, hop1U, true)
		state.Apply(hop1)
		if hop1.Header.Get("Authorization") != "" || hop1.Header.Get("X-Api-Key") != "" {
			t.Fatalf("third-party hop leaked headers: %v", hop1.Header)
		}

		// Bounce back to initial origin: must NOT restore headers.
		hop2U := mustURL("https://example.org/finish")
		hop2 := simulateGoRedirect(hop1.Header, hop1U, hop2U, true)
		state.Apply(hop2)
		if hop2.Header.Get("Authorization") != "" || hop2.Header.Get("Cookie") != "" || hop2.Header.Get("X-Api-Key") != "" || hop2.Header.Get("Referer") != "" {
			t.Fatalf("bounce back to initial origin restored headers: %v", hop2.Header)
		}
	})
}
