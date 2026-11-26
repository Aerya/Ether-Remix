package vela

import (
	"net/http"
	"strings"
)

// Vela web may reach this server at another origin than the page it was
// opened from: an http LAN page probing the server's https connection, for
// example. The API and media reads therefore answer cross-origin requests,
// as the Plex routes do. Vela authenticates with a bearer token (media with
// URL grants), never cookies, so credentials are not allowed and any origin
// may read: a page without the token gets the same 401 as anyone else.
// The owner console API (/ether/api) is not served here and stays
// same-origin.

// velaCORSHeaders are the request headers Vela sends, besides X-Vela-*.
var velaCORSHeaders = map[string]bool{
	"accept":          true,
	"accept-language": true,
	"authorization":   true,
	"content-type":    true,
	"idempotency-key": true,
	"if-none-match":   true,
	"if-range":        true,
	"last-event-id":   true,
	"range":           true,
}

// velaCORSExpose lists the non-safelisted response headers Vela reads.
const velaCORSExpose = "ETag, Retry-After, Content-Range, Accept-Ranges, Content-Disposition, " +
	mediaCapWaitHeader + ", X-Ether-Media-Error, X-Ether-Trickplay-Fallback"

// cors marks a cross-origin response readable and answers browser preflights.
// It reports true once it has answered the request.
func cors(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") == "" {
		return false
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Expose-Headers", velaCORSExpose)
	if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
	if allowed := allowedCORSHeaders(r.Header.Values("Access-Control-Request-Headers")); allowed != "" {
		h.Set("Access-Control-Allow-Headers", allowed)
	}
	h.Set("Access-Control-Max-Age", "7200")
	w.WriteHeader(http.StatusNoContent)
	return true
}

// allowedCORSHeaders keeps the requested headers Vela may send. A request for
// any other header fails the preflight in the browser.
func allowedCORSHeaders(requested []string) string {
	var out []string
	for _, line := range requested {
		for _, name := range strings.Split(line, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if velaCORSHeaders[name] || strings.HasPrefix(name, "x-vela-") && len(name) > len("x-vela-") {
				out = append(out, name)
			}
		}
	}
	return strings.Join(out, ", ")
}
