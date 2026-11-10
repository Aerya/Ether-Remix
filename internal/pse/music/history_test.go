package music

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMusicHistoryPeriodViewerPaginationAndExistingRows(t *testing.T) {
	h := newHarness(t, "")
	l := h.srv.o.Library
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 105; i++ {
		if err := l.recordPlay(context.Background(), 7, Track{ID: 100, Title: "Track", Duration: 201}); err != nil {
			t.Fatal(err)
		}
	}
	_ = l.recordPlay(context.Background(), 8, Track{ID: 101, Title: "Other viewer"})
	_, err := l.db.Exec("UPDATE pse_music_plays SET played_at=? WHERE id=1", now.AddDate(-1, 0, 0).Unix())
	if err != nil {
		t.Fatal(err)
	}
	handler := HistoryHandler(l)
	get := func(query string) HistoryPage {
		r := httptest.NewRequest("GET", "/music/history"+query, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var out HistoryPage
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	page := get("?user=7&days=30")
	if page.Total != 104 || len(page.Plays) != 100 || page.Plays[0].ID != 105 {
		t.Fatalf("page: %+v", page)
	}
	page = get("?user=7&days=30&offset=100")
	if len(page.Plays) != 4 || page.Plays[3].ID != 2 {
		t.Fatalf("suite: %+v", page)
	}
	page = get("?user=8&days=30")
	if page.Total != 1 || page.Plays[0].Title != "Other viewer" {
		t.Fatal(page)
	}
	page = get("?year=2025&user=7")
	if page.Total != 1 || page.Plays[0].ID != 1 {
		t.Fatal(page)
	}
	page = get("?user=999")
	if page.Total != 0 || len(page.Plays) != 0 {
		t.Fatal(page)
	}
}
