package vela

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestMediaCapWait(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		known, upstream, cors, timing bool
		want                          string
	}{
		{"wait", true, true, true, true, "37"},
		{"cached", true, false, true, false, "0"},
		{"no cors", true, true, false, false, "37"},
		{"unknown", false, false, true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			if tc.cors {
				recorder.Header().Set("Access-Control-Allow-Origin", "*")
				recorder.Header().Set("Access-Control-Expose-Headers", "Content-Range")
			}
			if tc.timing {
				recorder.Header().Set("Server-Timing", "other;dur=2")
			}
			measurement := new(core.MediaCapWait)
			if tc.known {
				measurement.MarkKnown()
			}
			if tc.upstream {
				measurement.BeginRead()
				measurement.FirstUpstream(37 * time.Millisecond)
				measurement.FirstUpstream(time.Second)
			}
			writer := &capWaitWriter{ResponseWriter: recorder, measurement: measurement}
			writer.WriteHeader(http.StatusPartialContent)
			_, _ = writer.Write([]byte("media"))
			header := recorder.Result().Header // inspect committed headers, not mutable map
			if got := header.Get(mediaCapWaitHeader); got != tc.want {
				t.Fatalf("header %q, want %q", got, tc.want)
			}
			exposed := strings.Contains(strings.Join(header.Values("Access-Control-Expose-Headers"), ","), mediaCapWaitHeader)
			if exposed != (tc.cors && tc.known) {
				t.Fatalf("exposure %t", exposed)
			}
			timing := strings.Join(header.Values("Server-Timing"), ",")
			if got := strings.Contains(timing, "capwait;dur="+tc.want); got != (tc.known && tc.timing) {
				t.Fatalf("timing %q", timing)
			}
			if !tc.timing && timing != "" {
				t.Fatal("invented Server-Timing")
			}
		})
	}
	t.Run("multipart is unknown before reads", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		recorder.Header().Set("Content-Type", "multipart/byteranges; boundary=test")
		measurement := new(core.MediaCapWait)
		measurement.MarkKnown()
		writer := &capWaitWriter{ResponseWriter: recorder, measurement: measurement}
		writer.WriteHeader(http.StatusPartialContent)
		if recorder.Result().Header.Get(mediaCapWaitHeader) != "" {
			t.Fatal("invented a multipart measurement")
		}
	})

}
