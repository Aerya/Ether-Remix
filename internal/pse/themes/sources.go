package themes

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"strings"
)

type SourceChoice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Collection string `json:"collection,omitempty"`
}

// SetSources is wired once by the application before serving the console.
func (s *Service) SetSources(fn func(context.Context) ([]SourceChoice, error)) { s.sourceChoices = fn }
func (s *Service) sources(ctx context.Context) ([]SourceChoice, error) {
	if s.sourceChoices == nil {
		return []SourceChoice{}, nil
	}
	return s.sourceChoices(ctx)
}

func (s *Service) attachSources(ctx context.Context, l Library) (Library, error) {
	if len(l.SourceIDs) == 0 {
		return l, nil
	}
	if len(l.SourceIDs) > 60 {
		return l, invalid("60 sources au plus")
	}
	choices, err := s.sources(ctx)
	if err != nil {
		return l, err
	}
	known := map[string]SourceChoice{}
	for _, c := range choices {
		known[c.ID] = c
	}
	for _, id := range l.SourceIDs {
		if _, ok := known[id]; !ok {
			return l, invalid("source inconnue ou désactivée")
		}
	}
	creator, ok := s.cols.(Creator)
	if !ok {
		return l, invalid("création des collections indisponible")
	}
	specs, err := s.cols.ListSpecs(ctx)
	if err != nil {
		return l, err
	}
	existing := map[string]string{}
	for _, sp := range specs {
		existing[sp.TemplateID] = sp.ID
	}
	for _, id := range l.SourceIDs {
		choice := known[id]
		collection := choice.Collection
		if collection == "" {
			marker := "remix-source-library-" + id
			collection = existing[marker]
			if collection == "" {
				sp := core.CollectionSpec{Name: choice.Name, MediaType: TypeMixed, Sources: []core.CollectionSource{{Kind: "releaseSource", Label: strings.TrimPrefix(id, "indexer:")}}, Match: "all", Sort: "source", Refresh: "live", Enabled: true, LocalOnly: true, TemplateID: marker, Targets: core.CollectionTargets{Users: core.AllUsers()}}
				made, e := creator.CreateSpec(ctx, sp)
				if e != nil {
					return l, e
				}
				collection = made.ID
				existing[marker] = collection
			}
		}
		l.Collections = append(l.Collections, collection)
	}
	l.SourceIDs = nil
	return l, nil
}
