package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// blockingSearchCatalog holds the search for "fj" until a newer search
// from the same client supersedes it.
type blockingSearchCatalog struct {
	*fakeCatalog
	started chan struct{}
}

func (c *blockingSearchCatalog) Search(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error) {
	if strings.EqualFold(query, "fj") {
		c.started <- struct{}{}
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}
	return c.fakeCatalog.Search(ctx, query, types, limit)
}

type blockingLiveTVSearch struct {
	frontend.LiveTV
	started chan struct{}
}

func (b *blockingLiveTVSearch) SearchLiveTV(ctx context.Context, viewer *frontend.User, query string, limit int) (frontend.LiveSearchResults, error) {
	if strings.EqualFold(query, "f") {
		b.started <- struct{}{}
		<-ctx.Done()
		return frontend.LiveSearchResults{}, context.Cause(ctx)
	}
	return frontend.LiveSearchResults{}, nil
}

// A search the client's next keystroke superseded answers in the
// endpoint's own no-results shape. For /hubs/search that is PMS's 17 empty
// hubs, never a container without hubs, which PMS never sends (W373).
func TestSupersededSearchAnswersNoResultsShape(t *testing.T) {
	for _, path := range []string{"/hubs/search", "/library/search"} {
		for _, accept := range []string{"", "application/json"} {
			bc := &blockingSearchCatalog{started: make(chan struct{}, 1)}
			f := newFixture(func(d *testServerDeps) {
				bc.fakeCatalog = d.Catalog.(*fakeCatalog)
				d.Catalog = bc
			})
			search := func(query string) *httptest.ResponseRecorder {
				hdr := map[string]string{"X-Plex-Client-Identifier": "roku-1", "X-Plex-Platform": "Roku"}
				if accept != "" {
					hdr["Accept"] = accept
				}
				return f.request(t, http.MethodGet, path+"?query="+query+"&limit=10", hdr)
			}
			done := make(chan *httptest.ResponseRecorder)
			go func() { done <- search("Fj") }()
			<-bc.started
			search("Fjo")
			superseded := <-done
			noResults := search("zzzz")
			if superseded.Code != http.StatusOK || superseded.Body.String() != noResults.Body.String() {
				t.Errorf("%s [%s]: superseded answer\n%d %s\nwant the no-results answer\n%s", path, accept, superseded.Code, superseded.Body.String(), noResults.Body.String())
			}
			f.srv.Close()
		}
	}
}

// EPG /library/search participates in the same per-(user, client) search turn
// as /library/search so typing "F" -> "Fi" cancels stale EPG searches and
// responds with 200 OK in the EPG provider's empty MediaContainer shape.
func TestSupersededLiveTVSearchAnswersNoResultsShape(t *testing.T) {
	for _, triggerPath := range []string{liveProviderPath + "/library/search", "/library/search"} {
		for _, accept := range []string{"", "application/json"} {
			f, service, _ := liveFixture(t)
			bl := &blockingLiveTVSearch{
				LiveTV:  service.Frontend(),
				started: make(chan struct{}, 1),
			}
			f.srv.setLiveTV(bl)
			req := func(path, query string) *httptest.ResponseRecorder {
				hdr := map[string]string{"X-Plex-Client-Identifier": "roku-65", "X-Plex-Platform": "Roku"}
				if accept != "" {
					hdr["Accept"] = accept
				}
				return f.request(t, http.MethodGet, path+"?includeCollections=1&query="+query+"&limit=100", hdr)
			}
			done := make(chan *httptest.ResponseRecorder)
			go func() { done <- req(liveProviderPath+"/library/search", "F") }()
			<-bl.started
			req(triggerPath, "Fi")
			superseded := <-done
			noResults := req(liveProviderPath+"/library/search", "zzzz")
			if superseded.Code != http.StatusOK || superseded.Body.String() != noResults.Body.String() {
				t.Errorf("trigger=%s [%s]: superseded EPG answer\n%d %s\nwant\n%s", triggerPath, accept, superseded.Code, superseded.Body.String(), noResults.Body.String())
			}
		}
	}
}
