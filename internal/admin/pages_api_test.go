package admin_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/personalize"
)

// fakePages is an in-memory RecommendationPages with the engine's
// revision and validation rules.
type fakePages struct {
	pmu   sync.Mutex
	saved map[core.PageKey]core.RowPage
}

var fakeLibraries = []core.PageLibrary{{ID: 12, Name: "Films", Experience: "watch", MediaType: core.TypeMovie, Caps: []string{},
	RowsTabs: []core.PageTab{{Key: "recommended", Label: "Recommended"}, {Key: "kids", Label: "Kids"}}}}

func (f *fakePages) Pages(context.Context) []core.RowPage {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	out := []core.RowPage{}
	for _, p := range f.saved {
		out = append(out, p)
	}
	return out
}
func (f *fakePages) Page(_ context.Context, key core.PageKey) (core.RowPage, bool) {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	if p, ok := f.saved[key]; ok {
		return p, true
	}
	return personalize.DefaultPage(key), false
}
func (f *fakePages) PageDocument(ctx context.Context, key core.PageKey) (core.PageDocument, error) {
	p, saved := f.Page(ctx, key)
	doc := core.PageDocument{Page: p, Saved: saved, ViewersWithChanges: []core.ViewerChanges{{ID: 2, Changes: 1}}}
	if id, _, ok := key.Library(); ok {
		for i := range fakeLibraries {
			if fakeLibraries[i].ID == id {
				doc.Library = &fakeLibraries[i]
			}
		}
	} else {
		doc.Libraries = fakeLibraries
	}
	return doc, nil
}
func (f *fakePages) SavePage(_ context.Context, p core.RowPage) (core.RowPage, error) {
	if p.Mode != core.PageCurated && p.Mode != core.PagePersonalized {
		return p, &personalize.ValidationError{Problems: []personalize.Problem{{Field: "page.mode", Message: "must be curated or personalized"}}}
	}
	f.pmu.Lock()
	defer f.pmu.Unlock()
	cur, had := f.saved[p.Key]
	if had && cur.Revision != p.Revision || !had && p.Revision != 0 {
		return p, personalize.ErrPageConflict
	}
	p.Revision++
	if f.saved == nil {
		f.saved = map[core.PageKey]core.RowPage{}
	}
	f.saved[p.Key] = p
	return p, nil
}
func (f *fakePages) ResetPage(_ context.Context, key core.PageKey) error {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	delete(f.saved, key)
	return nil
}
func (f *fakePages) PageSources(context.Context, core.PageKey) (core.PageSources, error) {
	return core.PageSources{Sources: []core.PageSource{}, Libraries: fakeLibraries}, nil
}
func (f *fakePages) PreviewPage(context.Context, *core.User, core.PageKey, core.PagePreviewRequest) (*core.PagePreview, error) {
	return &core.PagePreview{Rows: []core.HomeRow{}, Picks: []core.PreviewPick{}, NotShown: []core.NotShownRow{}}, nil
}
func (f *fakePages) PreviewPageRow(_ context.Context, _ *core.User, _ core.PageKey, req core.RowPreviewRequest) (*core.RowPreview, error) {
	return &core.RowPreview{Rows: []core.HomeRow{{HubID: "h", Title: req.Row.Name, Items: []core.Title{poster}}}, Total: 1}, nil
}
func (f *fakePages) PageImpact(context.Context, core.PageKey, core.RowPage, string, time.Time) (core.PageImpact, error) {
	return core.PageImpact{Viewers: 3, WithChanges: 1}, nil
}
func (f *fakePages) CollectionUsage(_ context.Context, id string, name func(core.PageKey) string) []core.PageUsage {
	k := core.LibraryPage(12, "kids")
	return []core.PageUsage{{Page: k, PageName: name(k), RowID: "rabc", RowName: id}}
}

func TestPagesAPI(t *testing.T) {
	h := personalizationHandler(t, &fakeEngine{})
	kids := "/ether/api/pages/" + admin.PageKeyPath(core.LibraryPage(12, "kids"))
	if kids != "/ether/api/pages/library:12~kids" {
		t.Fatalf("key path %s", kids)
	}

	rec := serve(h, req(http.MethodGet, "/ether/api/pages", ""))
	for _, want := range []string{`"key":"home","name":"Home"`, `"key":"library:12/kids","name":"Films › Kids"`, `"apps":["vela","silo","plex","mediaBrowser"]`} {
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("pages %d: missing %s in %s", rec.Code, want, rec.Body)
		}
	}
	// Both the "~" form and a percent-encoded ":" resolve the page.
	for _, path := range []string{kids, "/ether/api/pages/library%3A12~kids"} {
		rec = serve(h, req(http.MethodGet, path, ""))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"key":"library:12/kids"`) || rec.Header().Get("ETag") != "0" ||
			!strings.Contains(rec.Body.String(), `"name":"bob"`) {
			t.Fatalf("get %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := serve(h, req(http.MethodGet, "/ether/api/pages/library:99~recommended", "")); rec.Code != 404 {
		t.Fatalf("unknown library: %d", rec.Code)
	}
	if rec := serve(h, req(http.MethodGet, "/ether/api/pages/kitchen", "")); rec.Code != 404 {
		t.Fatalf("bad key: %d", rec.Code)
	}

	put := func(rev, body string) *http.Response {
		r := req(http.MethodPut, kids, body)
		if rev != "" {
			r.Header.Set("If-Match", rev)
		}
		return serve(h, r).Result()
	}
	body := `{"mode":"curated","picks":false,"length":{},"rows":[]}`
	if res := put("", body); res.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("no If-Match: %d", res.StatusCode)
	}
	if res := put(`"0"`, body); res.StatusCode != 200 || res.Header.Get("ETag") != "1" {
		t.Fatalf("save: %d %s", res.StatusCode, res.Header.Get("ETag"))
	}
	r := req(http.MethodPut, kids, body)
	r.Header.Set("If-Match", "0")
	rec = serve(h, r)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Someone else saved this page") {
		t.Fatalf("stale save: %d %s", rec.Code, rec.Body)
	}
	r = req(http.MethodPut, kids, `{"mode":"sideways","rows":[]}`)
	r.Header.Set("If-Match", "1")
	rec = serve(h, r)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"page.mode"`) {
		t.Fatalf("invalid save: %d %s", rec.Code, rec.Body)
	}

	if rec := serve(h, req(http.MethodPost, kids+"/preview", `{"device":"tv"}`)); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"summary"`) {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	rec = serve(h, req(http.MethodPost, kids+"/rows/preview", `{"row":{"id":"","name":"Picks","source":{"kind":"trending"}},"device":"fridge"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad device: %d %s", rec.Code, rec.Body)
	}
	rec = serve(h, req(http.MethodPost, kids+"/rows/preview", `{"row":{"id":"","name":"Picks","source":{"kind":"trending"}},"device":"tv"}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"title":"The Signal"`) || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("row preview: %d %s", rec.Code, rec.Body)
	}
	rec = serve(h, req(http.MethodGet, "/ether/api/collections/42/usage", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"pageName":"Films › Kids"`) {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}

	if rec := serve(h, req(http.MethodDelete, kids, "")); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"saved":false`) {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
}
