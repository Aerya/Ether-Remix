// Package discovery installs optional navigation collections, independently of Nuvio.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/collections"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/jukebox"
)

type Card struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Source string `json:"source"`
	Entity int64  `json:"entity"`
	Accent string `json:"accent"`
}

var Cards = []Card{
	{"a24", "A24", "studios", "tmdbCompany", 41077, "#354352"},
	{"pixar", "Pixar", "studios", "tmdbCompany", 3, "#247ba0"},
	{"marvel", "Marvel Studios", "studios", "tmdbCompany", 420, "#b51c21"},
	{"ghibli", "Studio Ghibli", "studios", "tmdbCompany", 10342, "#159fbe"},
	{"paramount", "Paramount", "studios", "tmdbCompany", 4, "#155c99"},
	{"warner", "Warner Bros.", "studios", "tmdbCompany", 174, "#264368"},
	{"universal", "Universal Pictures", "studios", "tmdbCompany", 33, "#263e65"},
	{"lucasfilm", "Lucasfilm", "studios", "tmdbCompany", 1, "#796b3f"},
	{"netflix", "Netflix", "services", "watchProvider", 8, "#b20710"},
	{"prime", "Prime Video", "services", "watchProvider", 119, "#146ca4"},
	{"disney", "Disney+", "services", "watchProvider", 337, "#16379a"},
	{"apple", "Apple TV", "services", "watchProvider", 350, "#676a77"},
	{"max", "HBO Max", "services", "watchProvider", 1899, "#582dac"},
	{"action", "Action", "genres", "genre", 28, "#a34825"},
	{"comedy", "Comédie", "genres", "genre", 35, "#9e7437"},
	{"drama", "Drame", "genres", "genre", 18, "#546480"},
	{"fantasy", "Fantastique", "genres", "genre", 14, "#574992"},
	{"crime", "Policier", "genres", "genre", 80, "#31566a"},
	{"scifi", "Science-fiction", "genres", "genre", 878, "#294c7b"},
	{"mystery", "Mystère", "genres", "genre", 9648, "#584763"},
	{"animation", "Animation", "genres", "genre", 16, "#206e71"},
	{"adventure", "Aventure", "genres", "genre", 12, "#436642"},
	{"documentary", "Documentaire", "genres", "genre", 99, "#41665f"},
	{"family", "Famille", "genres", "genre", 10751, "#546480"},
	{"history", "Histoire", "genres", "genre", 36, "#796b3f"},
	{"horror", "Horreur", "genres", "genre", 27, "#65354b"},
	{"music", "Musique", "genres", "genre", 10402, "#574992"},
	{"romance", "Romance", "genres", "genre", 10749, "#843a62"},
	{"thriller", "Thriller", "genres", "genre", 53, "#31566a"},
	{"war", "Guerre", "genres", "genre", 10752, "#546448"},
	{"western", "Western", "genres", "genre", 37, "#796b3f"},
}

type Service struct {
	cols    jukebox.Collections
	enabled func() bool
	set     func(bool) error
	mu      sync.Mutex
}

func New(cols jukebox.Collections, enabled func() bool, set func(bool) error) *Service {
	return &Service{cols: cols, enabled: enabled, set: set}
}

type Choice struct {
	Version      string                  `json:"version,omitempty"`
	CountryGroup bool                    `json:"countryGroup,omitempty"`
	CollectionID string                  `json:"collectionId,omitempty"`
	Card         Card                    `json:"card"`
	Region       string                  `json:"region"`
	Artwork      *core.CollectionArtwork `json:"artwork,omitempty"`
}

type Request struct {
	Choices        []Choice `json:"choices,omitempty"`
	GroupCountries bool     `json:"groupCountries"`
	Cards          []string `json:"cards"`
	Region         string   `json:"region"`
}

func Spec(card Card, order int, typ, region string) core.CollectionSpec {
	suffix := "Films"
	if typ == "show" {
		suffix = "Séries"
	}
	identity := "remix-navigation-" + card.ID + "-" + typ
	if card.Source == "watchProvider" {
		identity += "-" + region
	} else {
		region = ""
	}
	src := core.CollectionSource{Kind: card.Source, ID: card.Entity, Label: card.Name, Region: region}
	if card.Source == "genre" {
		id := card.Entity
		if typ == "show" {
			if id == 28 || id == 12 {
				id = 10759
			}
			if id == 14 || id == 878 {
				id = 10765
			}
		}
		src = core.CollectionSource{Kind: "tmdbDiscover", Params: map[string]string{"with_genres": fmt.Sprint(id)}}
	}
	return core.CollectionSpec{
		Name: card.Name + " · " + suffix, TemplateID: identity,
		Description: "Carte de navigation · " + card.Name, MediaType: typ,
		NavigationGroup: card.Group, NavigationOrder: order,
		Sources: []core.CollectionSource{src}, Match: "all", Rules: []core.CollectionRule{}, Exclude: []core.CollectionRule{},
		Sort: "popularity", SortDesc: true, Limit: 100, Refresh: "daily", ReleaseMode: "released", Enabled: true,
		Artwork: core.CollectionArtwork{Mode: "auto", Accent: card.Accent},
		Targets: core.CollectionTargets{CollectionsTab: true, Users: core.AllUsers()},
	}
}

// Apply is idempotent, preserves edited collections and enables the home only
// after every requested collection exists. A partial failure can be retried.
func (s *Service) Apply(ctx context.Context, req Request) ([]string, error) {
	choices := req.Choices
	if len(choices) == 0 {
		for _, id := range req.Cards {
			found := false
			for _, c := range Cards {
				if c.ID == id {
					choices = append(choices, Choice{Card: c, Region: req.Region})
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("carte inconnue")
			}
		}
	}
	if len(choices) == 0 || len(choices) > 200 {
		return nil, fmt.Errorf("choisissez entre 1 et 200 cartes")
	}
	// Validate the entire batch before creating anything. Client metadata cannot
	// inject arbitrary collection sources or template identities.
	for n := range choices {
		c := &choices[n]
		if err := validateChoice(c); err != nil {
			return nil, err
		}
		sp := Spec(c.Card, n, "movie", c.Region)
		if c.Artwork != nil {
			sp.Artwork = *c.Artwork
		}
		collections.Normalize(&sp)
		if err := collections.Validate(&sp); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return nil, err
	}
	result := []string{}
	for n, choice := range choices {
		c := choice.Card
		logo := ""
		if lookup, ok := s.cols.(interface {
			Lookup(context.Context, string, string, string) ([]core.LookupResult, error)
		}); ok && c.Source != "genre" && (choice.Artwork == nil || choice.Artwork.Mode == "auto") {
			kind := "company"
			if c.Source == "watchProvider" {
				kind = "watchProvider"
			}
			matches, _ := lookup.Lookup(ctx, kind, c.Name, choice.Region)
			for _, m := range matches {
				if m.ID == c.Entity {
					logo = m.ImagePath
					break
				}
			}
		}
		for k, typ := range []string{"movie", "show"} {
			if typ == "show" && c.Source == "genre" && (c.Entity == 36 || c.Entity == 27 || c.Entity == 10402 || c.Entity == 10749 || c.Entity == 53 || c.Entity == 10752) {
				continue
			}
			sp := Spec(c, n*2+k, typ, choice.Region)
			sp.Artwork.Logo = logo
			if c.Group == "services" {
				sp.Name = c.Name + " · " + choice.Region + " · " + map[string]string{"movie": "Films", "show": "Séries"}[typ]
				if req.GroupCountries {
					sp.NavigationGroup = "services-" + choice.Region
				}
			}
			if choice.Artwork != nil {
				sp.Artwork = *choice.Artwork
				if sp.Artwork.Mode == "auto" {
					sp.Artwork.Logo = logo
				}
			}
			found := ""
			for _, old := range existing {
				if old.OwnerUserID == 0 && old.TemplateID == sp.TemplateID {
					found = old.ID
					// Only fields explicitly edited here are changed; collection rules,
					// names, targets and manual artwork remain untouched otherwise.
					updated := old
					updated.NavigationGroup = sp.NavigationGroup
					if choice.Artwork != nil {
						updated.Artwork = sp.Artwork
					}
					if update, ok := s.cols.(interface {
						UpdateSpec(context.Context, string, core.CollectionSpec) (*core.CollectionSpec, error)
					}); ok && (old.NavigationGroup != updated.NavigationGroup || old.Artwork != updated.Artwork) {
						if _, e := update.UpdateSpec(ctx, old.ID, updated); e != nil {
							return result, e
						}
					}
					break
				}
			}
			if found == "" {
				created, e := s.cols.CreateSpec(ctx, sp)
				if e != nil {
					return result, e
				}
				found = created.ID
				existing = append(existing, *created)
			}
			result = append(result, found)
		}
	}
	if err = s.set(true); err != nil {
		return result, err
	}
	return result, nil
}
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/catalog" && r.Method == "GET" {
			s.catalog(w, r)
			return
		}
		if r.URL.Path == "/pack" && r.Method == "POST" {
			s.importPack(w, r)
			return
		}
		if r.URL.Path == "/gallery" && r.Method == "GET" {
			s.gallery(w, r)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "GET":
			choices, err := s.installed(r.Context())
			if err != nil {
				http.Error(w, "Lecture des cartes impossible", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": s.enabled(), "cards": Cards, "beta": true, "choices": choices})
		case "POST":
			var req Request
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req) != nil {
				http.Error(w, "Requête invalide", 400)
				return
			}
			ids, err := s.Apply(r.Context(), req)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "collections": ids})
		case "DELETE":
			s.mu.Lock()
			defer s.mu.Unlock()
			if err := s.set(false); err != nil {
				http.Error(w, "Enregistrement impossible", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false})
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			w.WriteHeader(405)
		}
	})
}

func validateChoice(c *Choice) error {
	if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(c.Region) {
		return fmt.Errorf("pays invalide")
	}
	if len(c.Card.Name) == 0 || len(c.Card.Name) > 180 || c.Card.Entity <= 0 {
		return fmt.Errorf("carte invalide")
	}
	expected := map[string]string{"studios": "tmdbCompany", "services": "watchProvider", "genres": "genre"}
	if expected[c.Card.Group] == "" || c.Card.Source != expected[c.Card.Group] {
		return fmt.Errorf("source de carte invalide")
	}
	if c.Card.Source == "genre" {
		found := false
		for _, card := range Cards {
			if card.Source == "genre" && card.Entity == c.Card.Entity {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("genre inconnu")
		}
	}
	// Preserve identities of the original preset when adding the same TMDB entity.
	c.Card.ID = c.Card.Group + "-" + strconv.FormatInt(c.Card.Entity, 10)
	for _, card := range Cards {
		if card.Source == c.Card.Source && card.Entity == c.Card.Entity {
			c.Card.ID = card.ID
			break
		}
	}
	return nil
}
func (s *Service) catalog(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	region := r.URL.Query().Get("region")
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if (kind != "company" && kind != "watchProvider") || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(region) || len(query) > 180 {
		http.Error(w, "Recherche invalide", 400)
		return
	}
	lookup, ok := s.cols.(interface {
		Lookup(context.Context, string, string, string) ([]core.LookupResult, error)
	})
	if !ok {
		http.Error(w, "Recherche indisponible", 503)
		return
	}
	matches, err := lookup.Lookup(r.Context(), kind, query, region)
	if err != nil {
		http.Error(w, "TMDB indisponible, réessayez", 502)
		return
	}
	out := []Card{}
	logos := map[string]string{}
	for _, m := range matches {
		c := Card{Name: m.Name, Entity: m.ID, Accent: "#31566a", Group: "studios", Source: "tmdbCompany"}
		if kind == "watchProvider" {
			c.Group = "services"
			c.Source = "watchProvider"
		}
		choice := Choice{Card: c, Region: region}
		if validateChoice(&choice) != nil {
			continue
		}
		out = append(out, choice.Card)
		logos[choice.Card.ID] = m.ImagePath
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"cards": out, "logos": logos})
}

func (s *Service) installed(ctx context.Context) ([]Choice, error) {
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return nil, err
	}
	out := []Choice{}
	seen := map[string]bool{}
	for _, sp := range specs {
		if sp.OwnerUserID != 0 || !strings.HasPrefix(sp.TemplateID, "remix-navigation-") || len(sp.Sources) != 1 {
			continue
		}
		src := sp.Sources[0]
		c := Card{Name: src.Label, Source: src.Kind, Entity: src.ID, Accent: sp.Artwork.Accent}
		switch src.Kind {
		case "tmdbCompany":
			c.Group = "studios"
		case "watchProvider":
			c.Group = "services"
		case "tmdbDiscover":
			c.Group = "genres"
			c.Source = "genre"
			for _, card := range Cards {
				if card.Group == "genres" && strings.HasPrefix(sp.TemplateID, "remix-navigation-"+card.ID+"-") {
					c = card
					break
				}
			}
		default:
			continue
		}
		region := src.Region
		if region == "" {
			region = "FR"
		}
		choice := Choice{Card: c, Region: region, CollectionID: sp.ID, CountryGroup: strings.HasPrefix(sp.NavigationGroup, "services-")}
		if sp.UpdatedAt != nil {
			choice.Version = strconv.FormatInt(sp.UpdatedAt.UnixNano(), 10)
		}
		if sp.LastBuiltAt != nil {
			choice.Version += "-" + strconv.FormatInt(sp.LastBuiltAt.UnixNano(), 10)
		}
		art := sp.Artwork
		choice.Artwork = &art
		if validateChoice(&choice) != nil {
			continue
		}
		key := choice.Card.ID
		if c.Group == "services" {
			key += "-" + region
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, choice)
	}
	return out, nil
}
