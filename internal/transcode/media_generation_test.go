package transcode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// A public session ID, offset, and numeric slot can all repeat. A delayed
// client request must still never read a replacement generation's bytes.
func TestHLSMediaGeneration(t *testing.T) {
	for _, container := range []string{ContainerMPEGTS, ContainerFMP4, ContainerMatroska} {
		t.Run(container, func(t *testing.T) {
			fr := newFakeRunner()
			m := newTestManager(t, fr, config.Transcode{})
			request := remuxRequest(t, "generation", 2*time.Minute)
			request.Decision.Container = container
			request.LazyStart = true
			var previous []string
			for _, offset := range []time.Duration{0, time.Minute, time.Minute, 0} {
				request.StartOffset = offset
				s := startSession(t, m, request)
				playlist := httptest.NewRecorder()
				s.ServePlaylist(playlist, httptest.NewRequest("GET", "/base/index.m3u8", nil))
				name := s.segName(0)
				init := s.scopeMediaName(initSegmentName(container))
				if !strings.Contains(playlist.Body.String(), name) {
					t.Fatal("missing media URI")
				}
				if hasInitSegment(container) && !strings.Contains(playlist.Body.String(), `URI="`+init+`"`) {
					t.Fatal("missing scoped init URI")
				}
				for _, uri := range append(previous, "00000."+segmentExt(container), initSegmentName(container)) {
					n, ok := ParseSegmentName(uri)
					if !ok {
						t.Fatalf("unparseable URI %q", uri)
					}
					w := httptest.NewRecorder()
					r := httptest.NewRequest("GET", "/base/"+uri, nil)
					r.Header.Set("Range", "bytes=0-15")
					s.ServeRungSegment(w, r, "base", n)
					if w.Code != http.StatusGone {
						t.Fatalf("obsolete URI served: %d", w.Code)
					}
				}
				for _, n := range []int{0, InitSegment} {
					if n == InitSegment && !hasInitSegment(container) {
						continue
					}
					uri := name
					if n == InitSegment {
						uri = init
					}
					parsed, ok := ParseSegmentName(uri)
					if !ok || parsed != n {
						t.Fatalf("URI index = %d, %v", parsed, ok)
					}
					w := httptest.NewRecorder()
					s.ServeRungSegment(w, httptest.NewRequest("GET", "/base/"+uri, nil), "base", n)
					if w.Code != http.StatusOK {
						t.Fatalf("current resource: %d %s", w.Code, w.Body.String())
					}
				}
				previous = append(previous, name)
				if hasInitSegment(container) {
					previous = append(previous, init)
				}
			}
		})
	}
}
