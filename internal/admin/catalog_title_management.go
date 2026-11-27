package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type CatalogTitleManagement interface {
	SearchMetadataMatches(context.Context, core.MetadataSourceID, core.MetadataSearch) ([]frontend.TitleMatchCandidate, error)
	ApplyTitleMatch(context.Context, core.RatingKey, core.ProviderRef) (core.RatingKey, error)
	RefreshCatalogTitle(context.Context, core.RatingKey) error
	StoredTitleEpisodeOrder(context.Context, core.RatingKey) (EpisodeOrderProvenance, error)
	TitleEpisodeOrder(context.Context, *core.Title) (*TitleEpisodeOrder, error)
	SetTitleEpisodeOrder(context.Context, core.RatingKey, core.EpisodeOrder) error
}

type EpisodeOrderOption struct {
	core.EpisodeOrder
	Label string `json:"label,omitempty"`
}

type EpisodeOrderGap struct {
	Order core.EpisodeOrder `json:"order"`
	Count int               `json:"count"`
}

type EpisodeOrderProvenance struct {
	Effective      core.EpisodeOrder  `json:"effective"`
	Override       *core.EpisodeOrder `json:"override,omitempty"`
	LibraryDefault core.EpisodeOrder  `json:"libraryDefault"`
	LibraryName    string             `json:"libraryName,omitempty"`
}

type TitleEpisodeOrder struct {
	EpisodeOrderProvenance
	Options []EpisodeOrderOption `json:"options"`
	Gap     *EpisodeOrderGap     `json:"gap,omitempty"`
}

func (h *Handler) handleCatalogTitleManagement(w http.ResponseWriter, r *http.Request) {
	key, err := core.ParseRatingKey(r.PathValue("key"))
	if err != nil || (key.Kind() != core.KindMovie && key.Kind() != core.KindShow) {
		writeError(w, http.StatusNotFound, "not_found", http.StatusText(http.StatusNotFound))
		return
	}
	mgmt := h.deps.TitleManagement
	if mgmt == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", http.StatusText(http.StatusServiceUnavailable))
		return
	}
	typ := core.TypeMovie
	if key.Kind() == core.KindShow {
		typ = core.TypeShow
	}
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		year, _ := strconv.Atoi(q.Get("year"))
		matches, err := mgmt.SearchMetadataMatches(r.Context(), core.MetadataSourceID(q.Get("source")),
			core.MetadataSearch{Type: typ, Title: q.Get("q"), Year: year})
		if err != nil {
			writeTitleManagementError(w, err)
			return
		}
		type matchCard struct {
			TitleCard
			Source     core.MetadataSourceID `json:"source"`
			ProviderID string                `json:"providerId"`
		}
		out := make([]matchCard, 0, len(matches))
		for _, hit := range matches {
			card := CardFor(hit.Title)
			if hit.Title.Key == 0 {
				card.Key = ""
			}
			out = append(out, matchCard{TitleCard: card, Source: hit.Ref.Source, ProviderID: hit.Ref.ID})
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPut:
		var body struct {
			Source     core.MetadataSourceID `json:"source"`
			ProviderID string                `json:"providerId"`
		}
		if decodeJSON(r, &body) != nil ||
			body.Source == "" || strings.TrimSpace(body.ProviderID) == "" {
			writeTitleManagementError(w, core.ErrUnsupported)
			return
		}
		next, err := mgmt.ApplyTitleMatch(r.Context(), key, core.ProviderRef{
			Source: body.Source, ID: strings.TrimSpace(body.ProviderID), Type: typ,
		})
		if err != nil {
			writeTitleManagementError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": next.String()})
	default:
		if err := mgmt.RefreshCatalogTitle(r.Context(), key); err != nil {
			writeTitleManagementError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) handleTitleEpisodeOrder(w http.ResponseWriter, r *http.Request) {
	key, err := core.ParseRatingKey(r.PathValue("key"))
	if err != nil || key.Kind() != core.KindShow {
		writeTitleManagementError(w, core.ErrNotFound)
		return
	}
	mgmt := h.deps.TitleManagement
	if mgmt == nil {
		writeTitleManagementError(w, core.ErrNotAvailable)
		return
	}
	var body struct {
		Order json.RawMessage `json:"order"`
	}
	var order core.EpisodeOrder
	if decodeJSON(r, &body) != nil || len(body.Order) == 0 ||
		(string(body.Order) != "null" && (json.Unmarshal(body.Order, &order) != nil || order.Source == "" || order.Kind == "")) {
		writeTitleManagementError(w, core.ErrUnsupported)
		return
	}
	if err := mgmt.SetTitleEpisodeOrder(r.Context(), key, order); err != nil {
		writeTitleManagementError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeTitleManagementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", http.StatusText(http.StatusNotFound))
	case errors.Is(err, core.ErrUnsupported):
		writeError(w, http.StatusBadRequest, "validation", http.StatusText(http.StatusBadRequest))
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable", http.StatusText(http.StatusServiceUnavailable))
	}
}
