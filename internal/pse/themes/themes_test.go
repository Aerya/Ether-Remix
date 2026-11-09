package themes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeCols struct {
	specs   []core.CollectionSpec
	keys    map[string][]core.RatingKey
	created []core.CollectionSpec
}

func (f *fakeCols) ListSpecs(context.Context) ([]core.CollectionSpec, error) { return f.specs, nil }
func (f *fakeCols) PSEKeys(_ context.Context, id string, _ *core.User) []core.RatingKey {
	return f.keys[id]
}
func (f *fakeCols) CreateSpec(_ context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	sp.ID = strconv.Itoa(100 + len(f.specs))
	f.specs = append(f.specs, sp)
	f.created = append(f.created, sp)
	return &sp, nil
}

func newTest(t *testing.T) (*Service, *fakeCols) {
	t.Helper()
	cols := &fakeCols{
		specs: []core.CollectionSpec{
			{ID: "1", Name: "Netflix", MediaType: "movie", Sources: []core.CollectionSource{{Kind: "watchProvider", ID: 8}}},
			{ID: "2", Name: "Canal+", MediaType: "movie", Sources: []core.CollectionSource{{Kind: "watchProvider", ID: 381}}},
		},
		keys: map[string][]core.RatingKey{"1": {10, 11}, "2": {11, 12}},
	}
	s, err := New(context.Background(), store.OpenTest(t), cols)
	if err != nil {
		t.Fatal(err)
	}
	return s, cols
}

func TestLibrariesCRUD(t *testing.T) {
	ctx := context.Background()
	s, _ := newTest(t)
	if _, err := s.Create(ctx, Library{Name: "  ", Type: "movie", Collections: []string{"1"}}); err == nil {
		t.Fatal("nom vide accepté")
	}
	if _, err := s.Create(ctx, Library{Name: "X", Type: "movie", Collections: []string{"99"}}); err == nil {
		t.Fatal("collection inconnue acceptée")
	}
	a, err := s.Create(ctx, Library{Name: "Services", Type: "movie", Collections: []string{"1", "2", "1"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Create(ctx, Library{Name: "Canal", Type: "show", Collections: []string{"2"}})
	if a.Section() != 5101 || b.Section() != 5102 {
		t.Fatalf("sections %d %d", a.Section(), b.Section())
	}
	th := s.Themes()
	if len(th) != 2 || th[0].Title != "Services" || len(th[0].Rows) != 2 || th[0].Rows[1].Title != "Canal+" || th[1].Type != core.TypeShow {
		t.Fatalf("thèmes = %+v", th)
	}
	if got := s.AllKeys(ctx, core.TypeMovie); len(got) != 3 {
		t.Fatalf("titres prioritaires = %v", got)
	}
	if err := s.Move(ctx, b.ID, -1); err != nil {
		t.Fatal(err)
	}
	if th := s.Themes(); th[0].Title != "Canal" {
		t.Fatalf("ordre = %+v", th)
	}
	a.Name = "Plateformes"
	if _, err := s.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	libs := s.Libraries()
	if len(libs) != 1 || libs[0].Name != "Plateformes" {
		t.Fatalf("bibliothèques = %+v", libs)
	}
	// Le numéro libéré est réutilisé.
	c, _ := s.Create(ctx, Library{Name: "Encore", Type: "show", Collections: []string{"2"}})
	if c.Section() != 5102 {
		t.Fatalf("section réutilisée = %d", c.Section())
	}
}

func TestApplyPresetReusesCollections(t *testing.T) {
	ctx := context.Background()
	s, cols := newTest(t)
	l, warnings, err := s.ApplyPreset(ctx, cols, "services", "Mes services")
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	if l.Name != "Mes services" || l.Type != TypeMixed {
		t.Fatalf("bibliothèque = %+v", l)
	}
	// Films puis séries ; Netflix et Canal+ (films) existent déjà.
	if len(l.Collections) != 2*len(services) || l.Collections[0] != "1" {
		t.Fatalf("collections = %v", l.Collections)
	}
	if len(cols.created) != 2*len(services)-2 {
		t.Fatalf("%d collections créées", len(cols.created))
	}
	sp := cols.created[0]
	if !sp.Targets.CollectionsTab || sp.Targets.HomeRow || !sp.Targets.Users.All || sp.Sources[0].Kind != "watchProvider" || sp.Sources[0].ID == 0 {
		t.Fatalf("collection créée = %+v", sp)
	}
	if last := cols.created[len(cols.created)-1]; last.MediaType != "show" || last.Sources[0].MediaType != "show" {
		t.Fatalf("dernière collection = %+v", last)
	}
	if _, _, err := s.ApplyPreset(ctx, cols, "inconnu", ""); err == nil {
		t.Fatal("modèle inconnu accepté")
	}
	n := len(cols.created)
	l2, _, err := s.ApplyPreset(ctx, cols, "studios", " ")
	if err != nil || l2.Name != "Studios & labels" || l2.Type != TypeMixed {
		t.Fatal(l2, err)
	}
	// Les chaînes (HBO…) ne donnent qu'une collection Séries.
	hbo := 0
	for _, sp := range cols.created[n:] {
		if sp.Name == "HBO" {
			hbo++
			if sp.MediaType != "show" {
				t.Fatalf("HBO en %s", sp.MediaType)
			}
		}
	}
	if hbo != 1 {
		t.Fatalf("%d collections HBO", hbo)
	}
}

func TestMixedLibraryHasTwoPlexParts(t *testing.T) {
	ctx := context.Background()
	s, cols := newTest(t)
	cols.specs = append(cols.specs, core.CollectionSpec{ID: "3", Name: "Netflix", MediaType: "show"})
	cols.keys["3"] = []core.RatingKey{30}
	if err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, Library{Name: "Services", Type: TypeMixed, Collections: []string{"1", "3", "2"}})
	if err != nil {
		t.Fatal(err)
	}
	th := s.Themes()
	if len(th) != 2 {
		t.Fatalf("parties = %+v", th)
	}
	if th[0].Section != l.Section() || th[0].Type != core.TypeMovie || th[0].Title != "Services · Films" || len(th[0].Rows) != 2 {
		t.Fatalf("films = %+v", th[0])
	}
	if th[1].Section != 5201 || th[1].Type != core.TypeShow || th[1].Title != "Services · Séries" || len(th[1].Rows) != 1 || th[1].Rows[0].ID != "3" {
		t.Fatalf("séries = %+v", th[1])
	}
	if keys := s.AllKeys(ctx, core.TypeShow); len(keys) != 1 || keys[0] != 30 {
		t.Fatalf("titres séries = %v", keys)
	}
	// Sans collection de séries, la partie Séries disparaît.
	l.Collections = []string{"1"}
	if _, err := s.Update(ctx, l); err != nil {
		t.Fatal(err)
	}
	if th := s.Themes(); len(th) != 1 || th[0].Type != core.TypeMovie {
		t.Fatalf("parties = %+v", th)
	}
}

func TestAPI(t *testing.T) {
	s, cols := newTest(t)
	changed := 0
	h := s.Handler(cols, func() { changed++ })
	call := func(method, path, body string) (int, map[string]any) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var v map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return w.Code, v
	}
	code, v := call(http.MethodPost, "/", `{"name":"Services","type":"movie","collections":["1","2"]}`)
	if code != 200 || len(v["libraries"].([]any)) != 1 || changed != 1 {
		t.Fatalf("création : %d %v", code, v)
	}
	if code, _ := call(http.MethodPost, "/", `{"name":"X","type":"music","collections":["1"]}`); code != 400 {
		t.Fatalf("type invalide : %d", code)
	}
	code, v = call(http.MethodGet, "/", "")
	if code != 200 || len(v["collections"].([]any)) != 2 || len(v["presets"].([]any)) == 0 {
		t.Fatalf("lecture : %d %v", code, v)
	}
	lib := v["libraries"].([]any)[0].(map[string]any)
	if lib["section"].(float64) != 5101 {
		t.Fatalf("section = %v", lib["section"])
	}
	if code, _ := call(http.MethodPut, "/1", `{"name":"Plateformes","type":"movie","collections":["2"]}`); code != 200 {
		t.Fatalf("modification : %d", code)
	}
	if code, _ := call(http.MethodPut, "/7", `{"name":"Plateformes","type":"movie","collections":["2"]}`); code != 404 {
		t.Fatalf("bibliothèque inconnue : %d", code)
	}
	code, v = call(http.MethodPost, "/preset", `{"preset":"studios","name":"Mes studios"}`)
	if code != 200 || len(v["libraries"].([]any)) != 2 {
		t.Fatalf("modèle : %d %v", code, v)
	}
	if code, _ := call(http.MethodDelete, "/1", ""); code != 200 {
		t.Fatalf("suppression : %d", code)
	}
}

func TestLanguageExceptions(t *testing.T) {
	ctx := context.Background()
	s, cols := newTest(t)
	if keys := s.ExemptKeys(ctx); len(keys) != 0 {
		t.Fatalf("aucune exception attendue : %v", keys)
	}
	l, err := s.Create(ctx, Library{Name: "Concerts", Type: TypeMovie, Collections: []string{"1"}, AllLanguages: true})
	if err != nil || !l.AllLanguages {
		t.Fatal(l, err)
	}
	if err := s.Reload(ctx); err != nil || !s.Libraries()[0].AllLanguages {
		t.Fatal("drapeau « toutes langues » perdu", err)
	}
	if err := s.SetExceptions(ctx, []string{"99"}); err == nil {
		t.Fatal("collection inconnue acceptée")
	}
	if err := s.SetExceptions(ctx, []string{"2", "2"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Exceptions(); len(got) != 1 || got[0] != "2" {
		t.Fatalf("exceptions = %v", got)
	}
	// Collection 1 (bibliothèque) : 10, 11 ; collection 2 : 11, 12.
	if keys := s.ExemptKeys(ctx); len(keys) != 3 {
		t.Fatalf("titres exemptés = %v", keys)
	}
	l.AllLanguages = false
	if _, err := s.Update(ctx, l); err != nil {
		t.Fatal(err)
	}
	if keys := s.ExemptKeys(ctx); len(keys) != 2 || keys[0] != 11 || keys[1] != 12 {
		t.Fatalf("titres exemptés = %v", keys)
	}
	_ = cols
}

func TestConcertPresetIgnoresLanguage(t *testing.T) {
	s, cols := newTest(t)
	l, warnings, err := s.ApplyPreset(context.Background(), cols, "concerts", "")
	if err != nil || len(warnings) != 0 || !l.AllLanguages || l.Name != "Concerts" {
		t.Fatal(l, warnings, err)
	}
	if p := cols.created[0].Sources[0]; p.Kind != "tmdbDiscover" || p.Params["with_keywords"] == "" {
		t.Fatalf("source = %+v", p)
	}
}

func TestAnimeLibraryOriginSurvivesRenameAndReload(t *testing.T) {
	s, cols := newTest(t)
	ctx := context.Background()
	l, warnings, err := s.ApplyPreset(ctx, cols, "anime", "Mes animés")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("preset: %v %v", err, warnings)
	}
	if l.Preset != "anime" || l.AllLanguages || l.Type != TypeMixed || len(l.Collections) != 4 {
		t.Fatalf("library: %+v", l)
	}
	// Un ancien client peut ignorer le champ preset lors d’un renommage.
	l.Name = "Animation japonaise"
	l.Preset = ""
	if _, err := s.Update(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	got := s.Libraries()[0]
	if got.Preset != "anime" || got.Name != l.Name {
		t.Fatalf("origin lost: %+v", got)
	}
	before := len(cols.created)
	if _, _, err := s.ApplyPreset(ctx, cols, "anime", "Autres animés"); err != nil {
		t.Fatal(err)
	}
	if len(cols.created) != before {
		t.Fatal("collections dupliquées")
	}
}
