package plex

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Budget or quota failures end the part and both start routes with a bare
// 404 (Plezy shows "file unavailable"); decision and timeline keep the text.
func TestPSEPlaybackTerminalStatus(t *testing.T) {
	causes := []error{
		core.ErrBudgetExhausted,
		fmt.Errorf("indexer paused: %w", core.ErrBudgetExhausted),
		core.ErrDebridQuota,
		fmt.Errorf("%w: 1 releases failed: %w", core.ErrNotAvailable, core.ErrDebridQuota),
	}
	h := map[string]string{"X-Plex-Product": "Plezy", "X-Plex-Platform": "Android",
		"X-Plex-Client-Identifier": "plezy", "X-Plex-Session-Identifier": "attempt", "Accept": "application/json"}
	base := "?path=/library/metadata/1000000000000550&session=attempt&hasMDE=1&directPlay=1&mediaIndex=0&partIndex=0"
	for i, cause := range causes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if !psePartTerminal(cause) {
				t.Fatal("not terminal")
			}
			f := newFixture()
			defer f.srv.Close()
			f.srv.setPlayback(&failureBudgetPlayback{fakePlayback: f.playback, err: cause})
			want := playbackMessage(cause)
			d := f.request(t, "GET", "/video/:/transcode/universal/decision"+base, h)
			if d.Code != http.StatusOK || containerAttrs(t, d.Body.Bytes())["generalDecisionText"] != want {
				t.Fatalf("decision: %d %s", d.Code, d.Body.String())
			}
			for _, path := range []string{"/video/:/transcode/universal/start.m3u8" + base, "/video/:/transcode/universal/start.mpd" + base, "/library/parts/1000000000000550/1/file.mkv"} {
				r := f.request(t, "GET", path, h)
				if r.Code != http.StatusNotFound || r.Header().Get("Retry-After") != "" || r.Header().Get("Content-Type") != "" || r.Body.Len() != 0 {
					t.Fatalf("%s: %d %v %q", path, r.Code, r.Header(), r.Body.String())
				}
				tl := f.request(t, "GET", "/:/timeline?ratingKey=1000000000000550&state=buffering&time=0", h)
				if a := containerAttrs(t, tl.Body.Bytes()); a["terminationText"] != want {
					t.Fatalf("timeline after %s: %v", path, a)
				}
			}
		})
	}
}

func TestPSEPartTerminalScope(t *testing.T) {
	for _, err := range []error{core.ErrDebridAuth, core.ErrSourceUnavailable, core.ErrNotAvailable, nil} {
		if psePartTerminal(err) {
			t.Fatalf("%v must stay on the existing path", err)
		}
	}
}
