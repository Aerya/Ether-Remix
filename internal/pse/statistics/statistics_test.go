package statistics

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/catalog"
	_ "github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/store"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTMDBTotalsAreDatedPersistentAndPreservedAfterFailure(t *testing.T) {
	now := time.Date(2026, 11, 9, 7, 30, 0, 0, time.UTC)
	db := store.OpenTest(t)
	var s Service
	if err := s.Load(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return now }
	calls := 0
	fail := false
	s.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.HasPrefix(r.URL.String(), "https://files.tmdb.org/p/exports/") {
			t.Fatal("wrong host")
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected credentials")
		}
		if fail && strings.Contains(r.URL.Path, "tv_series") {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("private upstream"))}, nil
		}
		if !strings.Contains(r.URL.Path, exportDate(now).Format("01_02_2006")) {
			t.Error("wrong export date", r.URL.Path)
		}
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		fmt.Fprintln(w, `{"id":1}`)
		if strings.Contains(r.URL.Path, "tv_series") {
			fmt.Fprintln(w, `{"id":2}`)
		}
		_ = w.Close()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b.Bytes()))}, nil
	})}
	s.Refresh(t.Context())
	got := s.Snapshot()
	if got.Movies != 1 || got.Shows != 2 || got.ExportDate != "2026-11-08" {
		t.Fatal(got)
	}
	s.Refresh(t.Context())
	if calls != 2 {
		t.Fatal("repeated download", calls)
	}
	var restored Service
	if err := restored.Load(t.Context(), db); err != nil || restored.Snapshot().Movies != 1 {
		t.Fatal(restored.Snapshot(), err)
	}
	now = now.Add(26 * time.Hour)
	fail = true
	s.Refresh(t.Context())
	got = s.Snapshot()
	if got.Movies != 1 || got.Shows != 2 || got.ExportDate != "2026-11-08" || got.Error == "" {
		t.Fatal("partial replacement", got)
	}
	if strings.Contains(got.Error, "private upstream") {
		t.Fatal("raw error exposed")
	}
}

func TestTMDBCounterRejectsInvalidAndTruncatedExports(t *testing.T) {
	for _, body := range []string{`{"id":0}`, "not JSON", ""} {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		_, _ = w.Write([]byte(body))
		_ = w.Close()
		s := Service{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b.Bytes()))}, nil
		})}}
		if _, err := s.count(t.Context(), "movie", time.Now()); err == nil {
			t.Fatal("invalid export accepted", body)
		}
	}
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte("{\"id\":1}\n"))
	_ = w.Close()
	truncated := b.Bytes()[:b.Len()-4]
	s := Service{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(truncated))}, nil
	})}}
	if _, err := s.count(t.Context(), "movie", time.Now()); err == nil {
		t.Fatal("truncated gzip accepted")
	}
}

func TestTMDBSnapshotDoesNotWaitForNetwork(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := Service{Now: func() time.Time { return time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC) }}
	s.HTTP = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	done := make(chan struct{})
	go func() { s.Refresh(context.Background()); close(done) }()
	<-entered
	if got := s.Snapshot(); got.CheckedAt == 0 {
		t.Error("refresh not started")
	}
	close(release)
	<-done
}

func TestHistoryUTCPeriodsTypesSourcesAndLifetimeCumulative(t *testing.T) {
	db := store.OpenTest(t)
	var s Service
	if err := s.Load(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }
	// Un ajout bien avant la fenêtre doit rester dans le cumul.
	for i, row := range []struct{ date, kind string }{{"2010-01-01", "movie"}, {"2026-11-08", "movie"}, {"2026-11-09", "show"}, {"", "show"}} {
		at := int64(0)
		if row.date != "" {
			d, _ := time.Parse("2006-01-02", row.date)
			at = d.Unix()
		}
		if _, err := db.Exec("INSERT INTO catalog_titles(key,type,title,browsable,added_at) VALUES(?,?,?,1,?)", i+1, row.kind, "Title", at); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		day, id, kind string
		n             int
	}{
		{"2010-01-01", "a", "movie", 7}, {"2026-11-08", "a", "movie", 2}, {"2026-11-09", "a", "show", 3}, {"2026-11-09", "b", "movie", 5},
	} {
		if _, err := db.Exec("INSERT INTO pse_index_source_history(day,source_id,media_type,searches) VALUES(?,?,?,?)", row.day, row.id, row.kind, row.n); err != nil {
			t.Fatal(err)
		}
	}
	for _, period := range []string{"day", "week", "month", "year"} {
		t.Run(period, func(t *testing.T) {
			got, err := s.History(t.Context(), period, "a", false)
			if err != nil {
				t.Fatal(err)
			}
			last := got.History[len(got.History)-1]
			if last.CumulativeMovies != 2 || last.CumulativeShows != 1 || last.CumulativeMovieSearches != 9 || last.CumulativeShowSearches != 3 || got.Undated != 1 {
				t.Fatalf("%+v", got)
			}
			if period == "day" && (last.Movies != 0 || last.Shows != 1 || last.MovieSearches != 0 || last.ShowSearches != 3) {
				t.Fatal(last)
			}
			if period == "week" && last.Start != "2026-11-09" {
				t.Fatal("week does not start Monday", last)
			}
			if period == "year" && got.History[0].CumulativeMovies != 1 {
				t.Fatal("lifetime baseline lost")
			}
		})
	}
	got, err := s.History(t.Context(), "day", "", false)
	if err != nil || got.History[len(got.History)-1].CumulativeMovieSearches != 14 {
		t.Fatal(got, err)
	}
	if _, err = s.History(t.Context(), "invalid", "", false); err == nil {
		t.Fatal("invalid period accepted")
	}
}

func TestAnimeHistoryFiltersCatalogueAndSourcesAcrossPeriods(t *testing.T) {
	db := store.OpenTest(t)
	var s Service
	if err := s.Load(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }
	for _, q := range []string{
		`INSERT INTO catalog_titles(key,type,title,browsable,language,added_at) VALUES(1,'movie','Anime film',1,'ja',1794182400),(2,'show','Anime show',1,'en',1794182400),(3,'movie','Cartoon',1,'en',1794182400)`,
		`INSERT INTO catalog_genres(key,genre) VALUES(1,'Animation'),(3,'Animation')`,
		`INSERT INTO catalog_keywords(key,keyword) VALUES(2,'ANIME')`,
		`INSERT INTO pse_index_source_history(day,source_id,media_type,searches,anime_searches,anime_since) VALUES('2026-11-09','a','movie',10,2,'2026-11-09'),('2026-11-09','b','show',8,3,'2026-11-09'),('2020-01-01','a','show',50,0,'')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, period := range []string{"day", "week", "month", "year"} {
		got, err := s.History(t.Context(), period, "a", true)
		if err != nil {
			t.Fatal(err)
		}
		b := got.History[len(got.History)-1]
		if b.CumulativeMovies != 1 || b.CumulativeShows != 1 || b.CumulativeMovieSearches != 2 || b.CumulativeShowSearches != 0 || got.SearchTrackingSince != "2026-11-09" {
			t.Fatalf("%s: %+v", period, got)
		}
	}
	all, err := s.History(t.Context(), "day", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if b := all.History[len(all.History)-1]; b.CumulativeShowSearches != 3 {
		t.Fatal(b)
	}
}
