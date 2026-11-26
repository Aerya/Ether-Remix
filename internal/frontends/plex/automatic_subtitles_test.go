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

// A timeline report of the audio track the server marked selected repeats
// the server's pick; saving it would pin that language for the whole show.
// A different track, or the part-selection command, is the viewer's choice.
func TestAudioTimelineEchoIsNotRemembered(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selector int
		command  bool
		saved    bool
	}{
		{"server track echo", 0, false, false},
		{"different track", 1, false, true},
		{"same track command", 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			defer f.srv.Close()
			prefs := trackprefs.New(store.OpenTest(t))
			f.srv.setTrackPreferences(prefs)
			pr := &Request{User: &ownerUser, ClientID: "client-1", Product: "Plex for Android (TV)", Platform: "Android"}
			info := core.MediaInfo{Streams: []core.StreamInfo{
				{Index: 1, Type: "audio", Codec: "dts", Language: "ru"},
				{Index: 2, Type: "audio", Codec: "ac3", Language: "en"},
			}}
			key := fightClub.Key
			streams := f.srv.issuedStreams(key, f.srv.playIdentityFor(pr, key), int64(key), info, selection{audio: 0, audioSet: true, subtitle: -1}, pr.User)
			var audio []int64
			for _, st := range streams {
				if st.StreamType == 2 {
					audio = append(audio, st.ID)
				}
			}
			if len(audio) != 2 {
				t.Fatalf("issued audio streams = %v", audio)
			}
			path := "/:/timeline"
			if tc.command {
				path = "/library/parts/" + key.String()
			}
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("%s?audioStreamID=%d", path, audio[tc.selector]), nil)
			req = req.WithContext(withRequest(context.Background(), pr))
			f.srv.rememberStreamRequest(req, pr.User, key)
			saved, err := prefs.Memory(req.Context(), pr.User.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for _, c := range saved {
				if c.Kind == "audio" {
					got = true
				}
			}
			if got != tc.saved {
				t.Fatalf("audio remembered=%v want %v (%+v)", got, tc.saved, saved)
			}
		})
	}
}

// A client that only held the pre-playback estimate (no subtitle rows)
// reports None on the timeline because its menu is empty; that must not be
// saved as a manual Off. Once a list with subtitles was issued, None is the
// viewer's choice again.
func TestTimelineOffWithoutSubtitleListIsNotRemembered(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	prefs := trackprefs.New(store.OpenTest(t))
	f.srv.setTrackPreferences(prefs)
	pr := &Request{User: &ownerUser, ClientID: "client-1", Product: "Plex for Android (TV)", Platform: "Android"}
	key := fightClub.Key
	estimate := core.MediaInfo{Estimated: true, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264"},
		{Index: 1, Type: "audio", Codec: "aac"},
	}}
	f.srv.issuedStreams(key, f.srv.playIdentityFor(pr, key), int64(key), estimate, selection{audio: 1, audioSet: true, subtitle: -1}, pr.User)
	off := func() bool {
		req := httptest.NewRequest(http.MethodGet, "/:/timeline?subtitleStreamID=0", nil)
		req = req.WithContext(withRequest(context.Background(), pr))
		f.srv.rememberStreamRequest(req, pr.User, key)
		saved, err := prefs.Memory(req.Context(), pr.User.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range saved {
			if c.Kind == "subtitle" && c.Choice.Off {
				return true
			}
		}
		return false
	}
	if off() {
		t.Fatal("None from an empty estimated menu was saved as a manual Off")
	}
	probed := core.MediaInfo{Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264"},
		{Index: 1, Type: "audio", Codec: "aac", Language: "en"},
		{Index: 2, Type: "subtitle", Codec: "subrip", Language: "en"},
	}}
	f.srv.issuedStreams(key, f.srv.playIdentityFor(pr, key), int64(key), probed, selection{audio: 1, audioSet: true, subtitle: -1}, pr.User)
	if !off() {
		t.Fatal("None after a subtitle list was issued was not saved")
	}
}
