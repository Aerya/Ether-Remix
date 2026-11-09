package releasedb

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSourceHistorySurvivesActivityRetentionAndSeparatesMedia(t *testing.T) {
	e := newFreshEnv(t)
	src := source{id: "a", name: "A"}
	for _, v := range []struct {
		typ core.MediaType
		err error
	}{
		{core.TypeMovie, nil}, {core.TypeShow, nil}, {core.TypeMovie, core.ErrBudgetExhausted}, {core.TypeShow, context.Canceled},
	} {
		event := e.s.beginSearch(core.Title{Type: v.typ}, src)
		e.s.finishSearch(e.ctx, event, 0, v.err)
	}
	var movies, shows, denied int
	if err := e.s.st.db.QueryRow("SELECT searches,denied FROM pse_index_source_history WHERE source_id='a' AND media_type='movie'").Scan(&movies, &denied); err != nil {
		t.Fatal(err)
	}
	if err := e.s.st.db.QueryRow("SELECT searches FROM pse_index_source_history WHERE source_id='a' AND media_type='show'").Scan(&shows); err != nil {
		t.Fatal(err)
	}
	if movies != 1 || shows != 1 || denied != 1 {
		t.Fatal(movies, shows, denied)
	}
	e.now = e.now.Add(400 * 24 * time.Hour)
	e.s.maintain(e.ctx)
	var n int
	if err := e.s.st.db.QueryRow("SELECT COUNT(*) FROM pse_index_source_history").Scan(&n); err != nil || n != 2 {
		t.Fatal("long history pruned", n, err)
	}
}

func TestAnimeHistoryTracksSubsetAndStartWithoutBackfilling(t *testing.T) {
	e := newFreshEnv(t)
	if _, err := e.s.st.db.Exec(`INSERT INTO pse_index_source_history(day,source_id,media_type,searches) VALUES('2020-01-01','a','show',100)`); err != nil {
		t.Fatal(err)
	}
	for _, title := range []core.Title{{Type: core.TypeShow, Language: "ja", Genres: []string{"Animation"}}, {Type: core.TypeShow, Language: "en", Genres: []string{"Animation"}}} {
		event := e.s.beginSearch(title, source{id: "a"})
		e.s.finishSearch(e.ctx, event, 0, nil)
	}
	var total, anime int
	var since string
	if err := e.s.st.db.QueryRow(`SELECT SUM(searches),SUM(anime_searches),MIN(NULLIF(anime_since,'')) FROM pse_index_source_history`).Scan(&total, &anime, &since); err != nil {
		t.Fatal(err)
	}
	if total != 102 || anime != 1 || since != e.now.UTC().Format("2006-01-02") {
		t.Fatal(total, anime, since)
	}
}
