package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// Selection provenance must survive the real Plex parser and timeline
// persistence: an echoed server choice is not a manual veto, but Off and
// explicit commands remain manual even when the server showed that choice.
func TestAutomaticSubtitleSelectionProvenance(t *testing.T) {
	for _, tc := range []struct {
		name            string
		selector        int
		command, manual bool
	}{
		{"server track echo", 0, false, false},
		{"different track", 1, false, true},
		{"explicit off", -1, false, true},
		{"same track command", 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			defer f.srv.Close()
			prefs := trackprefs.New(store.OpenTest(t))
			f.srv.setTrackPreferences(prefs)
			pr := &Request{User: &ownerUser, ClientID: "client-1", Product: "Plex for Android (TV)", Platform: "Android"}
			info := core.MediaInfo{Streams: []core.StreamInfo{
				{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
				{Index: 3, Type: "subtitle", Codec: "subrip", Language: "nl"},
			}}
			key := fightClub.Key
			streams := f.srv.issuedStreams(key, f.srv.playIdentityFor(pr, key), int64(key), info, selection{subtitle: 0, subtitleSet: true}, pr.User)
			id := int64(0)
			if tc.selector >= 0 {
				id = streams[tc.selector].ID
			}
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/video/:/transcode/universal/decision?path=/library/metadata/%s&subtitleStreamID=%d", key, id), nil)
			req = req.WithContext(withRequest(context.Background(), pr))
			play, err := f.srv.parsePlayRequest(req)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.command && play.manualSubtitle != tc.manual {
				t.Fatalf("parser manual=%v want %v", play.manualSubtitle, tc.manual)
			}
			path := "/:/timeline"
			if tc.command {
				path = "/library/parts/" + key.String()
			}
			req.URL.Path = path
			f.srv.rememberStreamRequest(req, pr.User, key)
			_, _, manual, err := prefs.AutomaticSubtitleRules(req.Context(), pr.User.ID, key, "en", trackprefs.Scope{})
			if err != nil || manual != tc.manual {
				t.Fatalf("remembered manual=%v want %v error=%v", manual, tc.manual, err)
			}
		})
	}
}
