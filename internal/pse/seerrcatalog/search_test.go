package seerrcatalog

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestDelegatedSearchAuthenticatedBoundedAndReusable(t *testing.T) {
	s, _, _ := fixture(t)
	if err := s.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.Enrich = func(_ context.Context, req core.SearchRequest) core.SearchRequest {
		req.Title = "La Matrice"
		req.OriginalTitle = "The Matrix"
		return req
	}
	calls := 0
	s.Search = func(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
		calls++
		if core.IndexerPurpose(ctx) != core.IndexerPurposeAvailability {
			t.Fatal("quota purpose lost")
		}
		if _, ok := s.Lookup(ctx, req); ok {
			t.Fatal("circular SeerrCatalog reuse")
		}
		good := "The.Matrix.1999.VOSTFR.1080p.WEB-DL"
		return []core.Release{
			{Name: good, Info: releaseparse.Parse(good), InfoHash: strings.Repeat("a", 40), Indexer: "test"},
			{Name: good, Info: releaseparse.Parse(good), Protocol: core.ProtocolHTTP, StreamURL: "https://stream.test/video", StreamHeaders: map[string]string{"Authorization": "private"}},
			{Name: good, Info: releaseparse.Parse(good), TitleIDs: core.IDs{TMDB: 999}, InfoHash: strings.Repeat("c", 40)},
			{Name: good, Info: releaseparse.Parse(good), NZBURL: "https://indexer.test/private.nzb"},
			{Name: "Wrong.2024.1080p", Info: releaseparse.Parse("Wrong.2024.1080p"), InfoHash: strings.Repeat("b", 40)},
		}, nil
	}
	input := `{"media":{"type":"movie","tmdbId":603,"imdbId":"tt0133093","title":"The Matrix","year":1999}}`
	send := func(method, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, SearchPath, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := send("POST", input, "bad"); w.Code != 401 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	if w := send("GET", "", strings.Repeat("a", 64)); w.Code != 200 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	w := send("POST", input, strings.Repeat("a", 64))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var data struct {
		Streams    []SearchStream `json:"streams"`
		Conclusive bool           `json:"conclusive"`
		Ignored    int            `json:"ignored"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Streams) != 2 || !data.Conclusive || data.Ignored != 1 {
		t.Fatalf("%+v", data)
	}
	if strings.Contains(w.Body.String(), "private.nzb") {
		t.Fatal("NZB exported as video")
	}
	if w := send("POST", `{"media":{"type":"series"}}`, strings.Repeat("a", 64)); w.Code != 400 {
		t.Fatal(w.Code)
	}
	s.searchSlots <- struct{}{}
	s.searchSlots <- struct{}{}
	if w := send("POST", input, strings.Repeat("a", 64)); w.Code != 429 {
		t.Fatal(w.Code)
	}
}

func TestDelegatedSearchReportsIncompleteCoverage(t *testing.T) {
	s, _, _ := fixture(t)
	s.Search = func(ctx context.Context, _ core.SearchRequest) ([]core.Release, error) {
		core.NoteSearchIncomplete(ctx)
		return nil, nil
	}
	r := httptest.NewRequest("POST", SearchPath, strings.NewReader(`{"media":{"type":"movie","tmdbId":603,"imdbId":"tt0133093","title":"The Matrix"}}`))
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"conclusive":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
