package vela

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

const mediaCapWaitHeader = "X-Ether-Media-Cap-Wait-Ms"

type capWaitWriter struct {
	http.ResponseWriter
	measurement *frontend.MediaCapWait
	wrote       bool
	head        bool
}

func (w *capWaitWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *capWaitWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.wrote {
		return
	}
	w.wrote = true
	// Headers precede the body: report admission for the first upstream run
	// serving the initial source read only. A cached initial read reports zero;
	// future body reads cannot change the value already sent to the client.
	ms, known := w.measurement.Milliseconds()
	// Multipart boundaries commit headers before any source read. Its wait is
	// unknowable here; omit rather than reporting an invented zero.
	if !w.head && strings.HasPrefix(w.Header().Get("Content-Type"), "multipart/byteranges") {
		known = false
	}
	if known && (status == 200 || status == 206) {
		h := w.Header()
		value := strconv.FormatInt(ms, 10)
		h.Set(mediaCapWaitHeader, value)
		if h.Get("Access-Control-Allow-Origin") != "" || h.Get("Access-Control-Expose-Headers") != "" || h.Get("Access-Control-Allow-Credentials") != "" {
			exposed := false
			for _, line := range h.Values("Access-Control-Expose-Headers") {
				for _, name := range strings.Split(line, ",") {
					if strings.EqualFold(strings.TrimSpace(name), mediaCapWaitHeader) {
						exposed = true
					}
				}
			}
			if !exposed {
				h.Add("Access-Control-Expose-Headers", mediaCapWaitHeader)
			}
		}
		if len(h.Values("Server-Timing")) > 0 {
			h.Add("Server-Timing", "capwait;dur="+value)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *capWaitWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *capWaitWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
