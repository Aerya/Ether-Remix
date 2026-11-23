// Package logredact removes credentials from log output. Handler wraps the
// final slog handler, so every record is scrubbed whichever call site wrote
// it: a URL with ?X-Plex-Token=…, an error that quotes a request, a header
// dump or an attribute named "password". Call-site redaction stays useful
// (it keeps the shape of a value readable); this is the backstop.
//
// It recognises credentials by name (query parameters, JSON keys, header
// names and attribute keys, compared case-insensitively ignoring '-' and
// '_' and after URL-decoding, so %58-Plex-Token is X-Plex-Token), by
// position (user:password@ in URLs, Bearer/Basic credentials) and by the
// prefixes of Ether's own credentials. Anything else passes through
// unchanged.
package logredact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

// Mask replaces a removed credential.
const Mask = "[redacted]"

// sensitive holds normalised credential names (see normalise).
var sensitive = map[string]bool{
	"token": true, "xplextoken": true, "plextoken": true, "accesstoken": true, "refreshtoken": true,
	"authtoken": true, "idtoken": true, "sessiontoken": true, "xetheradmintoken": true, "etheradmintoken": true,
	"xetherrecoverytoken": true, "recoverytoken": true,
	"xetherstreamtoken": true, "streamtoken": true, "xetherreadtoken": true, "readtoken": true,
	"plugintoken": true, "sharetoken": true, "downloadtoken": true,
	"credential": true, "xetherplugincredential": true, "xetherbackuppassword": true, "xetherpick": true, "xembytoken": true, "xembyauthorization": true, "xmediabrowsertoken": true, "xroomtoken": true, "jointoken": true, "roomaccesstoken": true, "opentoken": true, "ticket": true, "code": true, "devicecode": true, "pincode": true, "codeverifier": true, "invite": true, "secrets": true, "tag": true, "imagetags": true, "backdropimagetags": true,
	"username": true, "password": true, "passwd": true, "pwd": true, "pw": true, "newpw": true, "currentpw": true, "currentpassword": true, "pass": true, "passkey": true, "authkey": true, "torrentpass": true,
	"secret": true, "clientsecret": true, "apikey": true, "xapikey": true, "apitoken": true,
	"authorization": true, "proxyauthorization": true, "cookie": true, "setcookie": true,
	"rsskey": true,
	"jwt":    true, "sig": true, "signature": true,
}

// normalise lowercases name and drops everything but letters and digits.
func normalise(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Sensitive reports whether a parameter, header, JSON key or attribute key
// names a credential. Percent-encoded names are decoded first.
func Sensitive(name string) bool {
	for range 3 { // %2558 is %58 is X
		dec, err := url.QueryUnescape(name)
		if err != nil || dec == name {
			break
		}
		name = dec
	}
	return sensitive[normalise(name)]
}

var (
	xtreamPath        = regexp.MustCompile(`(?i)(/(?:live|movie|series)/)[^/\s?#"']+/[^/\s?#"']+/`)
	trackerPathAfter  = regexp.MustCompile(`(?i)(/(?:announce|scrape)(?:\.php)?/)[0-9a-z]{32,}\b`)
	trackerPathBefore = regexp.MustCompile(`(?i)(/)[0-9a-z]{32,}(/(?:announce|scrape)(?:\.php)?(?:$|[/?#&"'\s;,)\]}]))`)

	// Magnet tracker/source parameters (tr, xs, as, ws, optionally numbered)
	// and percent-encoded URLs whose inner query or path may carry passkeys.
	magnetParam = regexp.MustCompile(`(?i)(^|[?&#;\s,(\[{])((?:tr|xs|as|ws)(?:\.[0-9]+)?)=((?:[^&#\s"'<>;,)\[\]{}]|\[redacted\])+)`)
	encodedURL  = regexp.MustCompile(`(?i)(?:(?:https?|udp|wss?)%3a%2f%2f|magnet%3a)(?:[^&#\s"'<>;,)\[\]{}]|\[redacted\])+`)

	// name=value after a query, form or log-text boundary. A value stops at
	// '?' so that url=https://host/?apikey=… is still seen as apikey=….
	param = regexp.MustCompile(`(^|[?&#;\s,(\[{])([^=&#\s"'<>?;,(){}\[\]]+)=("[^"?]*"|[^&#\s"'<>;,)\]}?]*)`)
	// "name": "value" in JSON.
	jsonImageTags = regexp.MustCompile(`(?i)("(?:ImageTags|BackdropImageTags)"\s*:\s*)(?:\{[^}]*\}|\[[^]]*\])`)
	jsonField     = regexp.MustCompile(`("([^"\\]{1,64})"\s*:\s*)"((?:[^"\\]|\\.)*)"`)
	// Header lines and Go's map[Name:[value]] dumps: cookies to the end of
	// the line, token headers and Authorization (in its canonical spelling,
	// so prose such as "authorization: failed" is left alone) one value.
	cookie      = regexp.MustCompile(`(?i)\b(cookie|set-cookie)(\s*:\s*\[?|:\[)([^\]\r\n]+)`)
	tokenHeader = regexp.MustCompile(`(?i)\b(x-plex-token|x-ether-admin-token|x-ether-recovery-token|x-ether-stream-token|x-ether-read-token|x-ether-plugin-credential|x-ether-backup-password|x-ether-pick|x-emby-token|x-mediabrowser-token|x-room-token|x-api-key)(\s*:\s*\[?|:\[)([^\s\],;"]+)`)
	authHeader  = regexp.MustCompile(`\b(Authorization|Proxy-Authorization)(: ?\[?|:\[)((?:[A-Za-z]+ )?[^\s\],;"]+)`)
	// scheme://user:password@host
	userinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]*):([^@/\s]+)@`)
	// Bearer/Basic credentials wherever they appear.
	scheme = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{6,}`)
	// Ether's own credentials: console sessions and cast delegations.
	own = regexp.MustCompile(`\bether-(admin|cast|recovery)-[A-Za-z0-9]+`)
	// Path capabilities and WebSocket subprotocol credentials have no key=value name.
	pathCapability   = regexp.MustCompile(`(?i)(/(?:stremio/image|m3u/image|xtream/image|api/v2/images|sessions/lazy|ether/api/addon-relay|auth/keys)/|/(?:sessions/media|src)/[^/\s?#"']+/)([^/\s?#"']+)`)
	appPath          = regexp.MustCompile(`(?i)(/(?:stremio|m3u)/)([^/\s?#"']+)`)
	opaqueCredential = regexp.MustCompile(`\b(?:eg1_|er1_|ek1_|ether-credential\.|silo\.ticket\.)[A-Za-z0-9_-]+`)
	// JSON Web Tokens by shape (header.payload.signature, base64url), so a
	// signed stream URL or a token under any name is masked.
	jwt = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{4,}\.eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]+`)
)

const maxNestedDepth = 3

// String returns s with credentials replaced by Mask.
func String(s string) string {
	return stringDepth(s, 0)
}

func stringDepth(s string, depth int) string {
	if s == "" {
		return s
	}
	s = replaceIf(xtreamPath, s, "${1}"+Mask+"/"+Mask+"/")
	s = replaceIf(trackerPathAfter, s, "${1}"+Mask)
	s = replaceIf(trackerPathBefore, s, "${1}"+Mask+"${2}")
	s = replaceIf(pathCapability, s, "${1}"+Mask)
	s = replaceFuncIf(appPath, s, func(m string) string {
		sub := appPath.FindStringSubmatch(m)
		switch strings.ToLower(sub[2]) {
		case "image", "sessions", "[redacted]":
			return m
		}
		return sub[1] + Mask
	})
	s = replaceIf(opaqueCredential, s, Mask)
	s = replaceIf(jsonImageTags, s, `${1}"`+Mask+`"`)
	s = replaceIf(jwt, s, Mask)
	s = maskValue(cookie, s)
	s = maskValue(tokenHeader, s)
	s = maskValue(authHeader, s)
	s = replaceIf(scheme, s, "${1} "+Mask)
	s = replaceIf(userinfo, s, "${1}:"+Mask+"@")
	s = replaceIf(own, s, "ether-${1}-"+Mask)
	if depth < maxNestedDepth && strings.Contains(s, "%") {
		s = replaceFuncIf(magnetParam, s, func(m string) string {
			sub := magnetParam.FindStringSubmatch(m)
			if clean, ok := scrubPercentURL(sub[3], depth+1); ok {
				return sub[1] + sub[2] + "=" + clean
			}
			return m
		})
		s = replaceFuncIf(encodedURL, s, func(m string) string {
			if clean, ok := scrubPercentURL(m, depth+1); ok {
				return clean
			}
			return m
		})
	}
	s = replaceFuncIf(param, s, func(m string) string {
		sub := param.FindStringSubmatch(m)
		if !Sensitive(sub[2]) || sub[3] == "" || masked(sub[3]) {
			return m
		}
		return sub[1] + sub[2] + "=" + Mask
	})
	s = replaceFuncIf(jsonField, s, func(m string) string {
		sub := jsonField.FindStringSubmatch(m)
		if !Sensitive(sub[2]) || sub[3] == "" {
			return m
		}
		return sub[1] + `"` + Mask + `"`
	})
	return s
}

func scrubPercentURL(raw string, depth int) (string, bool) {
	decoded, ok := unescapePercent(raw)
	if !ok {
		return raw, false
	}
	clean := stringDepth(decoded, depth)
	if clean == decoded {
		return raw, false
	}
	encoded := strings.ReplaceAll(url.QueryEscape(clean), "+", "%20")
	encoded = strings.ReplaceAll(encoded, "%5Bredacted%5D", Mask)
	return encoded, true
}

func unescapePercent(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for i := 0; i < len(s); {
		if i+2 < len(s) && s[i] == '%' && isHexDigit(s[i+1]) && isHexDigit(s[i+2]) {
			b.WriteByte(fromHex(s[i+1])<<4 | fromHex(s[i+2]))
			i += 3
			changed = true
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String(), changed
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func fromHex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

// maskValue replaces the third group of re's matches (name, separator,
// value) with Mask.
func maskValue(re *regexp.Regexp, s string) string {
	return replaceFuncIf(re, s, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if masked(sub[3]) {
			return m
		}
		return sub[1] + sub[2] + Mask
	})
}

// masked reports whether v is already (the start of) a Mask.
func masked(v string) bool {
	v = strings.Trim(v, `"'`)
	return v == Mask || v == strings.TrimSuffix(Mask, "]") || v == strings.Trim(Mask, "[]")
}

// Handler returns a slog.Handler that scrubs every record before next sees
// it: the message, attribute values (inside groups too) and attributes
// added with WithAttrs. Attributes whose key names a credential lose their
// value entirely.
func Handler(next slog.Handler) slog.Handler { return &handler{next: next} }

type handler struct {
	next        slog.Handler
	secretGroup bool
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, String(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(as))
	for i, a := range as {
		clean[i] = h.attr(a)
	}
	return &handler{next: h.next.WithAttrs(clean), secretGroup: h.secretGroup}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(name), secretGroup: h.secretGroup || Sensitive(name)}
}

func (h *handler) attr(a slog.Attr) slog.Attr {
	if h.secretGroup && credentialValue(a.Value.Resolve()) {
		return slog.String(a.Key, Mask)
	}
	return Attr(a)
}

// Attr scrubs one attribute.
func Attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if Sensitive(a.Key) && credentialValue(v) {
		return slog.String(a.Key, Mask)
	}
	if v.Kind() == slog.KindGroup {
		as := v.Group()
		clean := make([]slog.Attr, len(as))
		for i, g := range as {
			clean[i] = Attr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(clean...)}
	}
	if Sensitive(a.Key) && credentialValue(v) {
		return slog.String(a.Key, Mask)
	}
	switch v.Kind() {
	case slog.KindString:
		if s := String(v.String()); s != v.String() {
			return slog.String(a.Key, s)
		}
	case slog.KindAny:
		var text string
		switch x := v.Any().(type) {
		case error:
			text = x.Error()
		case *url.URL:
			text = x.String()
		case url.URL:
			text = x.String()
		case fmt.Stringer:
			text = x.String()
		case []byte:
			text = string(x)
		default:
			// Preserve structured fields, including full-width integers, while
			// recursively scrubbing credential keys and string values. Cyclic or
			// otherwise non-JSON values cannot be safely inspected: omit them.
			b, err := json.Marshal(v.Any())
			if err != nil {
				return slog.String(a.Key, Mask)
			}
			var decoded any
			dec := json.NewDecoder(strings.NewReader(string(b)))
			dec.UseNumber()
			if err := dec.Decode(&decoded); err != nil {
				return slog.String(a.Key, Mask)
			}
			if clean, changed := structured(decoded); changed {
				return slog.Any(a.Key, clean)
			}
			return slog.Attr{Key: a.Key, Value: v}
		}
		if s := String(text); s != text {
			return slog.String(a.Key, s)
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// credentialValue reports whether a value under a credential's name could
// hold the credential: text, not a count or a flag ("token=false").
func credentialValue(v slog.Value) bool {
	switch v.Kind() {
	case slog.KindBool, slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindDuration, slog.KindTime:
		return false
	case slog.KindString:
		return v.String() != ""
	}
	return true
}

func structured(value any) (any, bool) {
	changed := false
	switch x := value.(type) {
	case map[string]any:
		for key, item := range x {
			if Sensitive(key) {
				switch v := item.(type) {
				case nil, bool, json.Number:
				case string:
					if v != "" && v != Mask {
						x[key] = Mask
						changed = true
					}
				default:
					x[key] = Mask
					changed = true
				}
				continue
			}
			clean, c := structured(item)
			x[key] = clean
			changed = changed || c
		}
	case []any:
		for i, item := range x {
			clean, c := structured(item)
			x[i] = clean
			changed = changed || c
		}
	case string:
		clean := String(x)
		return clean, clean != x
	}
	return value, changed
}

// replaceIf and replaceFuncIf skip a pattern that does not occur: regexp's
// ReplaceAll copies the whole input even without a match, and String runs
// many patterns over every logged value.
func replaceIf(re *regexp.Regexp, s, repl string) string {
	if !re.MatchString(s) {
		return s
	}
	return re.ReplaceAllString(s, repl)
}

func replaceFuncIf(re *regexp.Regexp, s string, repl func(string) string) string {
	if !re.MatchString(s) {
		return s
	}
	return re.ReplaceAllStringFunc(s, repl)
}
