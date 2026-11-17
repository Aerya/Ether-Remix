package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/pse/themes"
	"strings"
)

func (a *App) librarySourceChoices(ctx context.Context) ([]themes.SourceChoice, error) {
	out := []themes.SourceChoice{}
	if a.collections != nil {
		specs, err := a.collections.ListSpecs(ctx)
		if err != nil {
			return nil, err
		}
		for _, sp := range specs {
			if strings.HasPrefix(sp.TemplateID, "remix-local-") {
				out = append(out, themes.SourceChoice{ID: "local:" + strings.TrimPrefix(sp.TemplateID, "remix-local-"), Name: sp.Name, Kind: "local", Collection: sp.ID})
			}
		}
	}
	if a.cfg != nil {
		for _, ix := range a.cfg.Read().Indexers {
			if ix.Enabled && ix.ID != "" {
				out = append(out, themes.SourceChoice{ID: "indexer:" + ix.ID, Name: ix.Name, Kind: ix.Kind})
			}
		}
	}
	return out, nil
}
