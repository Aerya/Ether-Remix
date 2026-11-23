package plex

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// TestAccountProjectsEffectiveSubtitlePreferences covers #617: /accounts
// projects the layered (effective) subtitle and audio preferences, not only
// a viewer's own saved row.
func TestAccountProjectsEffectiveSubtitlePreferences(t *testing.T) {
	db := store.OpenTest(t)
	prefs := trackprefs.New(db)
	user := core.User{ID: 7, Username: "ada", SubtitleLanguages: []string{"de"}}
	plain := testServer(Deps{})
	plain.setTestAuth(&fakeAuth{users: map[string]core.User{"t": user}})
	withPrefs := testServer(Deps{})
	withPrefs.setTestAuth(plain.testAuth())
	withPrefs.setTrackPreferences(prefs)
	body := func(s *Server) string {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleAccounts(w, httptest.NewRequest("GET", "/accounts", nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}

	// No preference at any layer: the document is unchanged.
	untouched := body(plain)
	if got := body(withPrefs); got != untouched || !strings.Contains(got, `subtitleMode="0"`) || !strings.Contains(got, `defaultSubtitleLanguage="de"`) {
		t.Fatalf("no-preference account changed\nplain:\n%s\nprefs:\n%s", untouched, got)
	}

	// Inherited household preference (user 0): Always on, English, plus an
	// audio language, projects without any viewer row.
	shared := trackprefs.Default()
	shared.SubtitleMode = "always"
	shared.SubtitleLanguages = []string{"en"}
	shared.AudioLanguages = []string{"ja"}
	if err := prefs.PutRules(context.Background(), 0, shared); err != nil {
		t.Fatal(err)
	}
	inherited := body(withPrefs)
	if !strings.Contains(inherited, `subtitleMode="2"`) || !strings.Contains(inherited, `defaultSubtitleLanguage="en"`) || !strings.Contains(inherited, `defaultAudioLanguage="ja"`) {
		t.Fatalf("inherited household preference not projected\n%s", inherited)
	}

	// An explicit viewer override wins over the household layer.
	rules := trackprefs.Default()
	rules.SubtitleMode = "foreign-only"
	rules.SubtitleLanguages = []string{"fr", "en"}
	rules.AudioLanguages = []string{"fr"}
	if err := prefs.PutRules(context.Background(), user.ID, rules); err != nil {
		t.Fatal(err)
	}
	changed := body(withPrefs)
	if !strings.Contains(changed, `subtitleMode="1"`) || !strings.Contains(changed, `defaultSubtitleLanguage="fr"`) || !strings.Contains(changed, `defaultAudioLanguage="fr"`) {
		t.Fatalf("explicit viewer override not projected\n%s", changed)
	}
}

func TestPlexSelectionHonoursSubtitleMode(t *testing.T) {
	db := store.OpenTest(t)
	prefs := trackprefs.New(db)
	rules := trackprefs.Default()
	rules.AudioLanguages = []string{"ja"}
	rules.SubtitleLanguages = []string{"en"}
	rules.SubtitleMode = "foreign-only"
	if err := prefs.PutRules(context.Background(), 7, rules); err != nil {
		t.Fatal(err)
	}
	srv := testServer(Deps{})
	srv.setTrackPreferences(prefs)
	info := core.MediaInfo{Streams: []core.StreamInfo{{Type: "audio", Language: "ja"}, {Type: "audio", Language: "en"}, {Type: "subtitle", Language: "en", Forced: true}, {Type: "subtitle", Language: "en"}}}
	got := srv.preferredSelection(context.Background(), &core.User{ID: 7}, core.MovieKey(1), info, "ja", selection{})
	if !got.subtitleSet || got.subtitle != 3 || got.audio != 0 {
		t.Fatalf("%+v", got)
	}
	rules.SubtitleMode = "off"
	if err := prefs.PutRules(context.Background(), 7, rules); err != nil {
		t.Fatal(err)
	}
	got = srv.preferredSelection(context.Background(), &core.User{ID: 7}, core.MovieKey(1), info, "ja", selection{})
	if !got.subtitleSet || got.subtitle != -1 {
		t.Fatalf("off %+v", got)
	}
}
