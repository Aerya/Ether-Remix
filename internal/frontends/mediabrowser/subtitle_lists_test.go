package mediabrowser

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Jellyfin Web posts "SubtitleStreamIndex":"-1" from its hidden subtitle
// menu when the detail page it showed listed no subtitle track. Only a reply
// that listed subtitles makes a later "-1" the viewer's choice; Ether's own
// later probe does not.
func TestSubtitleListShown(t *testing.T) {
	var s subtitleListShown
	v := &frontend.Viewer{ID: 7}
	key := frontend.RatingKey(42)
	now := time.Now()
	if s.shown(v, key, now) {
		t.Fatal("nothing sent yet, but shown")
	}
	unprobed := []any{map[string]any{"MediaStreams": []any{
		map[string]any{"Type": "Video"}, map[string]any{"Type": "Audio"},
	}}}
	s.note(v, key, unprobed, now)
	if s.shown(v, key, now) {
		t.Fatal("a source without subtitles counted as a list")
	}
	probed := []any{map[string]any{"MediaStreams": []map[string]any{
		{"Type": "Video"}, {"Type": "Audio"}, {"Type": "Subtitle", "Language": "eng"},
	}}}
	s.note(v, key, probed, now)
	if !s.shown(v, key, now) {
		t.Fatal("a source listing subtitles was not recorded")
	}
	if s.shown(&frontend.Viewer{ID: 8}, key, now) {
		t.Fatal("another viewer inherited the list")
	}
	if s.shown(v, key, now.Add(subtitleListTTL+time.Minute)) {
		t.Fatal("a stale list still counted")
	}
	// The detail page re-rendered before the probe: back to unlisted.
	s.note(v, key, unprobed, now)
	if s.shown(v, key, now) {
		t.Fatal("the latest reply listed no subtitles")
	}
}

// At the cap, expired entries go first, then the oldest; everyone else
// keeps what they were shown.
func TestSubtitleListShownCapEvictsOldest(t *testing.T) {
	var s subtitleListShown
	listed := []any{map[string]any{"MediaStreams": []any{map[string]any{"Type": "Subtitle"}}}}
	now := time.Now()
	for i := range subtitleListCap {
		s.note(&frontend.Viewer{ID: int64(i + 1)}, 42, listed, now.Add(time.Duration(i)*time.Second))
	}
	at := now.Add(subtitleListCap * time.Second)
	s.note(&frontend.Viewer{ID: 0x7fff}, 42, listed, at)
	if s.shown(&frontend.Viewer{ID: 1}, 42, at) {
		t.Fatal("the oldest entry survived the cap")
	}
	for _, id := range []int64{2, subtitleListCap, 0x7fff} {
		if !s.shown(&frontend.Viewer{ID: id}, 42, at) {
			t.Fatalf("viewer %d lost its list at the cap", id)
		}
	}
	// Expired entries make room before any live one goes.
	later := now.Add(subtitleListTTL + 2*time.Hour)
	s.note(&frontend.Viewer{ID: 0x8000}, 42, listed, later)
	if len(s.m) != 1 || !s.shown(&frontend.Viewer{ID: 0x8000}, 42, later) {
		t.Fatalf("expired entries kept: %d", len(s.m))
	}
}

// Jellyfin Web posts its menu value quoted; a number is an API client's
// deliberate choice and is never second-guessed.
func TestPlaybackBodyMarksMenuSubtitleIndex(t *testing.T) {
	for _, tc := range []struct {
		body   string
		quoted bool
	}{
		{`{"SubtitleStreamIndex":"-1"}`, true},
		{`{"SubtitleStreamIndex":-1}`, false},
		{`{"SubtitleStreamIndex":"3"}`, true},
		{`{}`, false},
	} {
		var p playbackRequest
		if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
			t.Fatal(err)
		}
		if p.SubtitleIndexQuoted != tc.quoted {
			t.Errorf("%s: quoted=%v want %v", tc.body, p.SubtitleIndexQuoted, tc.quoted)
		}
	}
}
