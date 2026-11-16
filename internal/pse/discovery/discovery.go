// Package discovery installs optional navigation collections, independently of Nuvio.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/jukebox"
)

type Card struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Source string `json:"-"`
	Entity int64  `json:"-"`
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

type Request struct {
	Cards  []string `json:"cards"`
	Region string   `json:"region"`
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
			if id == 28 {
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
	if len(req.Cards) == 0 || len(req.Cards) > len(Cards) || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(req.Region) {
		return nil, fmt.Errorf("choisissez des cartes et un pays à deux lettres (FR, US…)")
	}
	selected := map[string]bool{}
	for _, id := range req.Cards {
		found := false
		for _, c := range Cards {
			if c.ID == id {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("carte inconnue")
		}
		selected[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return nil, err
	}
	result := []string{}
	for n, c := range Cards {
		if !selected[c.ID] {
			continue
		}
		logo := ""
		if lookup, ok := s.cols.(interface {
			Lookup(context.Context, string, string, string) ([]core.LookupResult, error)
		}); ok && c.Source != "genre" {
			kind := "company"
			if c.Source == "watchProvider" {
				kind = "watchProvider"
			}
			matches, _ := lookup.Lookup(ctx, kind, c.Name, req.Region)
			for _, m := range matches {
				if m.ID == c.Entity {
					logo = m.ImagePath
					break
				}
			}
		}
		for k, typ := range []string{"movie", "show"} {
			sp := Spec(c, n*2+k, typ, req.Region)
			sp.Artwork.Logo = logo
			found := ""
			for _, old := range existing {
				if old.OwnerUserID == 0 && old.TemplateID == sp.TemplateID {
					found = old.ID
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
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": s.enabled(), "cards": Cards})
		case "POST":
			var req Request
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req) != nil {
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
