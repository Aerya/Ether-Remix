package stremio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Catalogues des bibliothèques Remix (bibliothèques à thème) : un catalogue
// par rangée, id ether-pse-<identifiant de la collection>. Voir
// docs/pse/FORK.fr.md.

const (
	pseCatalogPrefix = "ether-pse-"
	psePageSize      = 100
	pseMaxSkip       = 10000
)

// pseThemeRows est l'accès du service d'expérience aux rangées Remix : droits,
// contrôle parental et langue y sont appliqués comme pour Jellyfin.
type pseThemeRows interface {
	PSEThemeCatalogs(context.Context, *frontend.Viewer) ([]frontend.PSEThemeCatalog, error)
	PSEThemeCatalogTitles(ctx context.Context, viewer *frontend.Viewer, typ, rowID string) ([]frontend.Title, error)
}

func pseRowFromCatalog(catalogID string) string {
	row, ok := strings.CutPrefix(catalogID, pseCatalogPrefix)
	if !ok {
		return ""
	}
	return row
}

// pseThemeCatalogs liste les catalogues Remix du spectateur, vide si le
// service est coupé ou si aucune rangée n'a de titre visible.
func (i *instance) pseThemeCatalogs(r *http.Request, viewer *frontend.Viewer) []any {
	rows, ok := i.host.Experience.(pseThemeRows)
	if !ok {
		return nil
	}
	list, err := rows.PSEThemeCatalogs(r.Context(), viewer)
	if err != nil {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]any{
			"type": c.Type, "id": pseCatalogPrefix + c.ID, "name": c.Name,
			"extra": []any{map[string]any{"name": "skip"}},
		})
	}
	return out
}

// pseCatalog répond à /catalog/<type>/ether-pse-<rangée>[/skip=N].json.
func (i *instance) pseCatalog(w http.ResponseWriter, r *http.Request, credential string, viewer *frontend.Viewer, typ, rowID string) {
	rows, ok := i.host.Experience.(pseThemeRows)
	if !ok || (typ != "movie" && typ != "series") {
		http.NotFound(w, r)
		return
	}
	extra := r.URL.Query()
	if raw := r.PathValue("extra"); raw != "" {
		if !strings.HasSuffix(raw, ".json") {
			http.NotFound(w, r)
			return
		}
		var err error
		if extra, err = url.ParseQuery(strings.TrimSuffix(raw, ".json")); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	} else if !strings.HasSuffix(r.PathValue("id"), ".json") {
		http.NotFound(w, r)
		return
	}
	offset := 0
	if skip := extra.Get("skip"); skip != "" {
		var err error
		if offset, err = strconv.Atoi(skip); err != nil || offset < 0 || offset > pseMaxSkip {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
	}
	fresh, cacheKey := i.catalogCacheKey(viewer, typ, pseCatalogPrefix+rowID, extra, frontend.ItemQuery{Offset: offset})
	cacheKey += "|g" + strconv.FormatUint(frontend.PSEThemesGeneration(), 10)
	if body, ok := i.cachedCatalog(cacheKey, fresh); ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}
	r = frontend.PSEListRequest(r)
	titles, err := rows.PSEThemeCatalogTitles(r.Context(), viewer, typ, rowID)
	if errors.Is(err, frontend.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	if offset >= len(titles) {
		titles = nil
	} else {
		titles = titles[offset:min(len(titles), offset+psePageSize)]
	}
	device := deviceFor(credential)
	posters := i.artwork(r, credential, viewer, device, titles)
	refs := i.itemRefs(r, viewer, titles)
	metas := make([]map[string]any, 0, len(titles))
	for _, title := range titles {
		id := stremioMetaID(title, refs[title.Key])
		if id == "" {
			continue
		}
		meta := map[string]any{"id": id, "type": typ, "name": title.Title, "description": title.Summary}
		if title.Year > 0 {
			meta["releaseInfo"] = strconv.Itoa(title.Year)
		}
		if poster := posters[title.Key]; poster != "" {
			meta["poster"] = poster
		}
		metas = append(metas, meta)
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(map[string]any{"metas": metas}); err != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	i.storeCatalog(cacheKey, fresh, body.Bytes())
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body.Bytes())
}
