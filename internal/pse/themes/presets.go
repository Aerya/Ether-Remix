package themes

import (
	"context"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// Modèles prêts à l'emploi : une bibliothèque dont chaque rangée est une
// collection créée pour l'occasion (masquée de l'onglet Collections et de
// l'accueil, pour ne pas les encombrer).

// Entry est une rangée d'un modèle.
type Entry struct {
	Name   string
	Kind   string // source de collection d'Ether
	ID     int64  // identifiant TMDB (0 : résolu par le nom)
	Label  string
	Params map[string]string
	// Only limite la rangée à un type dans un modèle Films & séries (une
	// chaîne TMDB ne produit que des séries).
	Only string
}

// Preset est un modèle de bibliothèque.
type Preset struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Summary string   `json:"summary"`
	Entries []Entry  `json:"-"`
	Rows    []string `json:"rows"`
	// AllLanguages : la bibliothèque échappe au filtre de langue.
	AllLanguages bool `json:"allLanguages"`
}

func provider(name string, id int64) Entry {
	return Entry{Name: name, Kind: "watchProvider", ID: id, Label: name}
}

func company(name string, id int64) Entry {
	return Entry{Name: name, Kind: "tmdbCompany", ID: id, Label: name}
}

func network(name string, id int64) Entry {
	return Entry{Name: name, Kind: "tmdbNetwork", ID: id, Label: name, Only: TypeShow}
}

func discover(name string, params map[string]string) Entry {
	return Entry{Name: name, Kind: "tmdbDiscover", Params: params}
}

// Mots-clés TMDB (vérifiés sur themoviedb.org) ; « | » vaut « ou ».
const (
	kwConcerts   = "156205|6029|304615|318730" // concert film, concert, music concert, live concert
	kwStandUp    = "9716"                      // stand-up comedy
	kwOneManShow = "6656|193300"               // one-man show, one-woman show
	kwTheatre    = "246884|195729|220201"      // filmed theater, stage play, musical theater
)

func keywords(name, kw, sort string, extra ...string) Entry {
	p := map[string]string{"with_keywords": kw, "sort_by": sort}
	for i := 0; i+1 < len(extra); i += 2 {
		p[extra[i]] = extra[i+1]
	}
	return discover(name, p)
}

var services = []Entry{
	provider("Netflix", 8),
	provider("Amazon Prime Video", 119),
	provider("Disney Plus", 337),
	provider("Max", 1899),
	provider("Apple TV+", 350),
	provider("Canal+", 381),
	provider("Paramount Plus", 531),
	provider("Crunchyroll", 283),
}

var presets = []Preset{
	{ID: "services", Name: "Services", Type: TypeMixed, Summary: "Films et séries de chaque service de streaming (Netflix, Prime Video, Disney+…)", Entries: services},
	{ID: "studios", Name: "Studios & labels", Type: TypeMixed, Summary: "Une rangée par studio ou chaîne d'origine (Marvel, Pixar, Ghibli, HBO, BBC…), films et séries", Entries: []Entry{
		company("Marvel Studios", 420),
		company("Pixar", 3),
		company("Walt Disney Pictures", 2),
		company("Lucasfilm", 1),
		company("DC", 128064),
		company("Warner Bros. Pictures", 174),
		company("Universal Pictures", 33),
		company("DreamWorks Animation", 521),
		company("Illumination", 6704),
		company("Studio Ghibli", 10342),
		company("A24", 41077),
		company("Blumhouse", 3172),
		company("Legendary Pictures", 923),
		company("Gaumont", 9),
		network("HBO", 49),
		network("AMC", 174),
		network("FX", 88),
		network("BBC One", 4),
		network("Showtime", 67),
	}},
	{ID: "anime", Name: "Animes", Type: TypeMixed, Summary: "Films et séries d'animation japonaise", Entries: []Entry{
		discover("Animes populaires", map[string]string{"with_genres": "16", "with_original_language": "ja", "sort_by": "popularity.desc"}),
		discover("Animes les mieux notés", map[string]string{"with_genres": "16", "with_original_language": "ja", "sort_by": "vote_average.desc", "vote_count.gte": "200"}),
	}},
	{ID: "docs", Name: "Documentaires", Type: TypeMixed, Summary: "Documentaires et séries documentaires : populaires, les mieux notés, français", Entries: []Entry{
		discover("Documentaires populaires", map[string]string{"with_genres": "99", "sort_by": "popularity.desc"}),
		discover("Documentaires les mieux notés", map[string]string{"with_genres": "99", "sort_by": "vote_average.desc", "vote_count.gte": "50"}),
		discover("Documentaires français", map[string]string{"with_genres": "99", "with_original_language": "fr", "sort_by": "popularity.desc"}),
	}},
	{ID: "concerts", Name: "Concerts", Type: TypeMovie, AllLanguages: true, Summary: "Concerts filmés, toutes langues", Entries: []Entry{
		keywords("Concerts populaires", kwConcerts, "popularity.desc"),
		keywords("Concerts les mieux notés", kwConcerts, "vote_average.desc", "vote_count.gte", "20"),
		keywords("Concerts récents", kwConcerts, "primary_release_date.desc", "vote_count.gte", "5"),
	}},
	{ID: "spectacles", Name: "Spectacles", Type: TypeMovie, AllLanguages: true, Summary: "Stand-up, one-man-shows et théâtre filmé, toutes langues", Entries: []Entry{
		keywords("Stand-up", kwStandUp, "popularity.desc"),
		keywords("Stand-up français", kwStandUp, "popularity.desc", "with_original_language", "fr"),
		keywords("One-man-shows", kwOneManShow, "popularity.desc"),
		keywords("Théâtre filmé", kwTheatre, "popularity.desc"),
	}},
}

// Presets renvoie les modèles proposés dans la console.
func Presets() []Preset {
	out := make([]Preset, len(presets))
	for i, p := range presets {
		p.Rows = nil
		for _, e := range p.Entries {
			p.Rows = append(p.Rows, e.Name)
		}
		out[i] = p
	}
	return out
}

func presetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Creator crée une collection d'Ether.
type Creator interface {
	CreateSpec(ctx context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error)
}

// types renvoie les types de collection d'un modèle.
func (p Preset) types() []string {
	if p.Type == TypeMixed {
		return []string{TypeMovie, TypeShow}
	}
	return []string{p.Type}
}

// presetDescription marque les collections créées par un modèle.
const presetDescription = "Créée par Ether-Remix pour une bibliothèque à thème."

// specFor décrit la collection d'une rangée de modèle.
func specFor(e Entry, typ string) core.CollectionSpec {
	return core.CollectionSpec{
		Name:        e.Name,
		Description: presetDescription,
		MediaType:   typ,
		Sources:     []core.CollectionSource{{Kind: e.Kind, ID: e.ID, Label: e.Label, Params: e.Params, MediaType: typ}},
		Match:       "all",
		Rules:       []core.CollectionRule{},
		Exclude:     []core.CollectionRule{},
		Sort:        "source",
		Refresh:     "daily",
		Artwork:     core.CollectionArtwork{Mode: "auto"},
		Targets:     core.CollectionTargets{CollectionsTab: true, Users: core.AllUsers()},
		Enabled:     true,
	}
}

// entryKey reconnaît une collection déjà créée : même type, même source et
// même nom (le Netflix de Services n'est pas la chaîne Netflix).
func entryKey(typ, kind string, id int64, name string) string {
	return fmt.Sprintf("%s/%s/%d/%s", typ, kind, id, strings.ToLower(name))
}

// ApplyPreset crée (ou retrouve) les collections d'un modèle puis la
// bibliothèque, nommée name (le nom du modèle si vide). Une collection déjà
// présente sous le même nom et le même type est réutilisée.
func (s *Service) ApplyPreset(ctx context.Context, c Creator, id, name string) (Library, []string, error) {
	p, ok := presetByID(id)
	if !ok {
		return Library{}, nil, invalid("modèle %q inconnu", id)
	}
	if strings.TrimSpace(name) == "" {
		name = p.Name
	}
	existing := map[string]string{}
	if s.cols != nil {
		specs, err := s.cols.ListSpecs(ctx)
		if err != nil {
			return Library{}, nil, err
		}
		for _, sp := range specs {
			if sp.OwnerUserID == 0 && len(sp.Sources) == 1 {
				existing[entryKey(sp.MediaType, sp.Sources[0].Kind, sp.Sources[0].ID, sp.Name)] = sp.ID
			}
		}
	}
	var cols, warnings []string
	for _, typ := range p.types() {
		for _, e := range p.Entries {
			if e.Only != "" && e.Only != typ {
				continue
			}
			if id := existing[entryKey(typ, e.Kind, e.ID, e.Name)]; id != "" {
				cols = append(cols, id)
				continue
			}
			sp, err := c.CreateSpec(ctx, specFor(e, typ))
			if err != nil {
				warnings = append(warnings, e.Name+" : "+err.Error())
				continue
			}
			cols = append(cols, sp.ID)
		}
	}
	if err := s.Reload(ctx); err != nil {
		return Library{}, warnings, err
	}
	l, err := s.Create(ctx, Library{Name: name, Type: p.Type, Collections: cols, AllLanguages: p.AllLanguages})
	return l, warnings, err
}

// PresetSpecs renvoie les collections que crée un modèle.
func PresetSpecs(id string) []core.CollectionSpec {
	p, ok := presetByID(id)
	if !ok {
		return nil
	}
	out := make([]core.CollectionSpec, 0, len(p.Entries)*2)
	for _, typ := range p.types() {
		for _, e := range p.Entries {
			if e.Only != "" && e.Only != typ {
				continue
			}
			out = append(out, specFor(e, typ))
		}
	}
	return out
}
