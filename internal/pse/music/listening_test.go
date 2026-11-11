package music

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListeningFullPeriodViewerTimezoneAndEmpty(t *testing.T) {
	h := newHarness(t, "")
	l := h.srv.o.Library
	now := time.Date(2026, 11, 11, 0, 30, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 105; i++ {
		if err := l.recordPlay(context.Background(), 7, Track{ID: 1, Title: "Song", Artist: Artist{Name: "Artist"}, Album: Album{Title: "Album"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.recordPlay(context.Background(), 8, Track{ID: 2, Title: "Other"}); err != nil {
		t.Fatal(err)
	}
	handler := HistoryHandler(l)
	get := func(query string) ListeningSummary {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/music/listening"+query, nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var s ListeningSummary
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := get("?days=7&user=7&tz=-120")
	if s.Plays != 105 || s.Tracks != 1 || s.Listeners != 1 || s.Artists != 1 || s.Titles[0].Plays != 105 {
		t.Fatalf("summary %+v", s)
	}
	found := false
	for _, c := range s.Heatmap {
		if c.Weekday == 2 && c.Hour == 22 && c.Plays == 105 {
			found = true
		}
	}
	if !found {
		t.Fatal("timezone not applied")
	}
	if s.Daily[len(s.Daily)-1].Label != "2026-11-10" {
		t.Fatal(s.Daily)
	}
	s = get("?year=2025")
	if s.Plays != 0 || len(s.Titles) != 0 || len(s.Daily) != 365 {
		t.Fatalf("empty year: plays=%d days=%d", s.Plays, len(s.Daily))
	}
	// UTC year has 365 days; negative offsets may span an additional local date.
	s = get("?user=8")
	if s.Plays != 1 || s.Listeners != 1 {
		t.Fatal(s)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/music/history?limit=500&user=7", nil))
	var page HistoryPage
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Plays) != 105 {
		t.Fatalf("history truncated at old limit: %d", len(page.Plays))
	}
}
