package discovery

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http/httptest"
	"testing"
)

type fakeCollections struct {
	specs []core.CollectionSpec
	fail  int
}

func (f *fakeCollections) ListSpecs(context.Context) ([]core.CollectionSpec, error) {
	return f.specs, nil
}
func (f *fakeCollections) CreateSpec(_ context.Context, s core.CollectionSpec) (*core.CollectionSpec, error) {
	if f.fail > 0 && len(f.specs)+1 == f.fail {
		return nil, fmt.Errorf("failure")
	}
	s.ID = fmt.Sprint(len(f.specs) + 1)
	f.specs = append(f.specs, s)
	return &s, nil
}
func TestRetryRestoreAndPreserve(t *testing.T) {
	c := &fakeCollections{fail: 2}
	enabled := false
	s := New(c, func() bool { return enabled }, func(v bool) error { enabled = v; return nil })
	req := Request{Cards: []string{"a24", "action"}, Region: "FR"}
	if _, err := s.Apply(context.Background(), req); err == nil || enabled {
		t.Fatal("failed import enabled home", err)
	}
	c.fail = 0
	c.specs[0].Name = "My edited A24"
	for range 2 {
		if _, err := s.Apply(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.specs) != 4 || c.specs[0].Name != "My edited A24" || !enabled {
		t.Fatal(c.specs, enabled)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("DELETE", "/", nil))
	if w.Code != 200 || enabled || len(c.specs) != 4 {
		t.Fatal("restore changed collections", w.Code)
	}
	if _, e := s.Apply(context.Background(), Request{Cards: []string{"bad"}, Region: "FR"}); e == nil {
		t.Fatal("unknown card accepted")
	}
}
func TestAllPresetSpecsValidate(t *testing.T) {
	for n, c := range Cards {
		for _, typ := range []string{"movie", "show"} {
			sp := Spec(c, n, typ, "FR")
			collections.Normalize(&sp)
			if err := collections.Validate(&sp); err != nil {
				t.Fatalf("%s %s: %v", c.ID, typ, err)
			}
		}
	}
}

func (f *fakeCollections) UpdateSpec(_ context.Context, id string, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	for n := range f.specs {
		if f.specs[n].ID == id {
			sp.ID = id
			f.specs[n] = sp
			return &sp, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func TestCustomCardsCountriesAndArtworkPreserved(t *testing.T) {
	c := &fakeCollections{}
	s := New(c, func() bool { return true }, func(bool) error { return nil })
	card := Card{ID: "untrusted-template", Name: "A service", Group: "services", Source: "watchProvider", Entity: 123456, Accent: "#31566a"}
	req := Request{GroupCountries: true, Choices: []Choice{{Card: card, Region: "FR", Artwork: &core.CollectionArtwork{Mode: "custom", URL: "https://example.com/card.jpg"}}, {Card: card, Region: "US"}}}
	if _, err := s.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(c.specs) != 4 || c.specs[0].NavigationGroup != "services-FR" || c.specs[2].NavigationGroup != "services-US" {
		t.Fatal(c.specs)
	}
	if c.specs[0].TemplateID != "remix-navigation-services-123456-movie-FR" {
		t.Fatal(c.specs[0].TemplateID)
	}
	c.specs[0].Name = "Owner title"
	c.specs[0].Limit = 35
	req.Choices[0].Artwork = nil
	req.GroupCountries = false
	if _, err := s.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(c.specs) != 4 || c.specs[0].Name != "Owner title" || c.specs[0].Limit != 35 || c.specs[0].Artwork.URL != "https://example.com/card.jpg" || c.specs[0].NavigationGroup != "services" {
		t.Fatal(c.specs[0])
	}
	installed, err := s.installed(context.Background())
	if err != nil || len(installed) != 2 || installed[0].Artwork.URL != "https://example.com/card.jpg" {
		t.Fatal(installed, err)
	}
	req.Choices[0].Artwork = &core.CollectionArtwork{Mode: "auto"}
	if _, err = s.Apply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if c.specs[0].Artwork.Mode != "auto" || c.specs[0].Artwork.URL != "" {
		t.Fatal("reset artwork failed")
	}
}
func TestDynamicBatchRejectsBeforeAnyWrite(t *testing.T) {
	c := &fakeCollections{}
	s := New(c, func() bool { return false }, func(bool) error { t.Fatal("enabled invalid batch"); return nil })
	for _, bad := range []Choice{
		{Card: Card{Name: "Injected", Group: "studios", Source: "stremio", Entity: 1}, Region: "FR"},
		{Card: Cards[0], Region: "../"},
		{Card: Cards[0], Region: "FR", Artwork: &core.CollectionArtwork{Mode: "custom", URL: "data:image/svg+xml;base64,AAAA"}},
		{Card: Card{Name: "Fake genre", Group: "genres", Source: "genre", Entity: 99999}, Region: "FR"},
	} {
		if _, err := s.Apply(context.Background(), Request{Choices: []Choice{{Card: Cards[0], Region: "FR"}, bad}}); err == nil {
			t.Fatal("invalid choice accepted", bad)
		}
		if len(c.specs) != 0 {
			t.Fatal("partially created invalid batch")
		}
	}
}
func TestGenreMediaMapping(t *testing.T) {
	c := &fakeCollections{}
	s := New(c, func() bool { return false }, func(bool) error { return nil })
	if _, err := s.Apply(context.Background(), Request{Cards: []string{"adventure", "horror"}, Region: "FR"}); err != nil {
		t.Fatal(err)
	}
	if len(c.specs) != 3 {
		t.Fatal(c.specs)
	}
	for _, sp := range c.specs {
		if sp.MediaType == "show" && sp.Sources[0].Params["with_genres"] != "10759" {
			t.Fatal(sp)
		}
	}
}
