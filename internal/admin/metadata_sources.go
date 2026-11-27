package admin

import (
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type metadataSourceCard struct {
	ID         core.MetadataSourceID `json:"id"`
	Name       string                `json:"name"`
	KeySource  string                `json:"keySource"`
	Enabled    bool                  `json:"enabled"`
	Configured bool                  `json:"configured"`
	Movies     bool                  `json:"movies"`
	Shows      bool                  `json:"shows"`
}

func (h *Handler) handleMetadataSources(w http.ResponseWriter, _ *http.Request) {
	registry := h.deps.MetadataSources
	if registry == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", http.StatusText(http.StatusServiceUnavailable))
		return
	}
	cfg := h.deps.Config.Get().Catalog
	tmdbKeySource := "none"
	switch {
	case strings.TrimSpace(cfg.TMDBKey) != "":
		tmdbKeySource = "owner"
	case strings.TrimSpace(tmdb.BundledKey) != "":
		tmdbKeySource = "built-in"
	}
	tvdbKeySource := "none"
	if strings.TrimSpace(cfg.TVDBKey) != "" {
		tvdbKeySource = "owner"
	}
	cards := []metadataSourceCard{
		{ID: core.MetadataTMDB, Name: "TMDB", KeySource: tmdbKeySource, Enabled: !cfg.TMDBDisabled, Movies: true, Shows: true},
		{ID: core.MetadataTVDB, Name: "TheTVDB", KeySource: tvdbKeySource, Enabled: tvdbKeySource == "owner", Movies: true, Shows: true},
	}
	for i := range cards {
		if source := registry.Get(cards[i].ID); source != nil {
			caps := source.Capabilities()
			cards[i].Configured = cards[i].Enabled && source.Configured()
			cards[i].Movies, cards[i].Shows = caps.Movies, caps.Shows
		}
	}
	for _, source := range registry.Configured() {
		if source.ID() == core.MetadataTMDB || source.ID() == core.MetadataTVDB {
			continue
		}
		caps := source.Capabilities()
		cards = append(cards, metadataSourceCard{
			ID: source.ID(), Name: string(source.ID()), KeySource: "none", Enabled: true, Configured: true, Movies: caps.Movies, Shows: caps.Shows,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": cards})
}
