package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/personalize"
)

// Recommendations pages owner API (design §7). Every route is owner-only.
//
// Page keys in URLs: a key's "/" is written as "~", so "library:12/recommended"
// is /api/pages/library:12~recommended (":" is a legal path character; a
// client that percent-encodes it as %3A works too). Tab keys never contain
// "~", so the mapping is exact both ways. Home is /api/pages/home.

// RecommendationPages is the engine behind /api/pages (internal/personalize.Engine).
type RecommendationPages interface {
	Pages(ctx context.Context) []core.RowPage
	Page(ctx context.Context, key core.PageKey) (core.RowPage, bool)
	PageDocument(ctx context.Context, key core.PageKey) (core.PageDocument, error)
	// SavePage returns personalize.ErrPageConflict when the revision is
	// stale and *personalize.ValidationError for an invalid page.
	SavePage(ctx context.Context, p core.RowPage) (core.RowPage, error)
	ResetPage(ctx context.Context, key core.PageKey) error
	PageSources(ctx context.Context, key core.PageKey) (core.PageSources, error)
	// PreviewPage, PreviewPageRow and PageImpact take a nil viewer for the
	// typical viewer.
	PreviewPage(ctx context.Context, viewer *core.User, key core.PageKey, req core.PagePreviewRequest) (*core.PagePreview, error)
	PreviewPageRow(ctx context.Context, viewer *core.User, key core.PageKey, req core.RowPreviewRequest) (*core.RowPreview, error)
	PageImpact(ctx context.Context, key core.PageKey, draft core.RowPage, device string, at time.Time) (core.PageImpact, error)
	CollectionUsage(ctx context.Context, collectionID string, pageName func(core.PageKey) string) []core.PageUsage
}

// PageKeyPath is the URL form of a page key ("/" → "~").
func PageKeyPath(k core.PageKey) string { return strings.ReplaceAll(string(k), "/", "~") }

func pageKeyFromPath(s string) core.PageKey { return core.PageKey(strings.ReplaceAll(s, "~", "/")) }

func (h *Handler) pageRoutes(api func(pattern string, fn http.HandlerFunc)) {
	api("GET /api/pages", h.handleListPages)
	api("GET /api/pages/{key}", h.handleGetPage)
	api("PUT /api/pages/{key}", h.handlePutPage)
	api("DELETE /api/pages/{key}", h.handleResetPage)
	api("GET /api/pages/{key}/sources", h.handlePageSources)
	api("POST /api/pages/{key}/preview", h.handlePagePreview)
	api("POST /api/pages/{key}/rows/preview", h.handlePageRowPreview)
	api("POST /api/pages/{key}/impact", h.handlePageImpact)
	api("GET /api/collections/{id}/usage", h.handleCollectionUsage)
	api("GET /api/row-sources/facets/{library}/{facet}", h.handleRowSourceFacets)
}

// pagesEngine returns the engine, or writes 503 when there is none.
func (h *Handler) pagesEngine(w http.ResponseWriter) (RecommendationPages, bool) {
	e := h.deps.Personalization
	if e == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "Recommendations are not available yet")
		return nil, false
	}
	return e, true
}

// pageKey reads and validates the {key} path value.
func pageKey(w http.ResponseWriter, r *http.Request) (core.PageKey, bool) {
	k := pageKeyFromPath(r.PathValue("key"))
	if !k.Valid() {
		writeError(w, http.StatusNotFound, "not_found", "no such page")
		return "", false
	}
	return k, true
}

// writePageError maps the engine's page errors onto the API.
func writePageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, personalize.ErrPageConflict):
		writeError(w, http.StatusConflict, "conflict", "Someone else saved this page. Reload it to see their changes.")
	default:
		writeEngineError(w, err)
	}
}

// pageSummaries lists Home and every rows tab of every library, in
// library and tab order.
func pageSummaries(ctx context.Context, e RecommendationPages) ([]core.PageSummary, error) {
	home, err := e.PageDocument(ctx, core.PageHome)
	if err != nil {
		return nil, err
	}
	out := []core.PageSummary{summary(home.Page, home.Saved, "Home", 0, "", "", []string{"vela", "plex", "mediaBrowser", "silo"})}
	for _, l := range home.Libraries {
		for i, t := range l.RowsTabs {
			key := core.LibraryPage(l.ID, t.Key)
			p, saved := e.Page(ctx, key)
			// The first rows tab is what Plex, Jellyfin, Emby and Silo
			// show as the library's Recommended page; the others are
			// Vela-only tabs.
			apps := []string{"vela"}
			if i == 0 {
				apps = append(apps, "silo")
				if l.MediaType != "" {
					apps = append(apps, "plex", "mediaBrowser")
				}
			}
			out = append(out, summary(p, saved, l.Name+" › "+t.Label, l.ID, t.Key, l.Experience, apps))
		}
	}
	return out, nil
}

func summary(p core.RowPage, saved bool, name string, lib core.LibraryID, tab, experience string, apps []string) core.PageSummary {
	return core.PageSummary{Key: p.Key, Name: name, Library: lib, Tab: tab, Experience: experience,
		Mode: p.Mode, RowCount: len(p.Rows), Saved: saved, Apps: apps}
}

func (h *Handler) handleListPages(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	out, err := pageSummaries(r.Context(), e)
	if err != nil {
		writePageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) writePageDocument(w http.ResponseWriter, r *http.Request, e RecommendationPages, key core.PageKey) {
	doc, err := e.PageDocument(r.Context(), key)
	if err != nil {
		writePageError(w, err)
		return
	}
	if _, _, ok := key.Library(); ok && doc.Library == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such library")
		return
	}
	h.nameViewerChanges(r.Context(), doc.ViewersWithChanges)
	w.Header().Set("ETag", strconv.FormatInt(doc.Page.Revision, 10))
	writeJSON(w, http.StatusOK, doc)
}

// nameViewerChanges fills viewer names (the engine knows only ids).
func (h *Handler) nameViewerChanges(ctx context.Context, vs []core.ViewerChanges) {
	if len(vs) == 0 {
		return
	}
	users, err := h.storedViewers(ctx)
	if err != nil {
		return
	}
	names := make(map[int64]string, len(users))
	for _, u := range users {
		names[u.ID] = u.Title
		if names[u.ID] == "" {
			names[u.ID] = u.Username
		}
	}
	for i := range vs {
		if vs[i].Name == "" {
			vs[i].Name = names[vs[i].ID]
		}
	}
}

func (h *Handler) handleGetPage(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	key, ok := pageKey(w, r)
	if !ok {
		return
	}
	h.writePageDocument(w, r, e, key)
}

// handlePutPage saves the whole page. If-Match carries the revision the
// page was loaded at (0 for a page never saved).
func (h *Handler) handlePutPage(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	key, ok := pageKey(w, r)
	if !ok {
		return
	}
	match := strings.Trim(strings.TrimPrefix(strings.TrimSpace(r.Header.Get("If-Match")), "W/"), `"`)
	rev, err := strconv.ParseInt(match, 10, 64)
	if err != nil || rev < 0 {
		writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match must carry the page revision it was loaded at")
		return
	}
	var p core.RowPage
	if err := decodeJSON(r, &p); err != nil {
		writeValidationError(w, []FieldError{{Field: "page", Message: err.Error()}})
		return
	}
	p.Key, p.Revision = key, rev
	if _, err := e.SavePage(r.Context(), p); err != nil {
		writePageError(w, err)
		return
	}
	h.writePageDocument(w, r, e, key)
}

func (h *Handler) handleResetPage(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	key, ok := pageKey(w, r)
	if !ok {
		return
	}
	if err := e.ResetPage(r.Context(), key); err != nil {
		writePageError(w, err)
		return
	}
	h.writePageDocument(w, r, e, key)
}

func (h *Handler) handlePageSources(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	key, ok := pageKey(w, r)
	if !ok {
		return
	}
	out, err := e.PageSources(r.Context(), key)
	if err != nil {
		writePageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// previewViewer resolves a preview's viewer: nil for 0 (the typical viewer).
func (h *Handler) previewViewer(w http.ResponseWriter, r *http.Request, id int64) (*core.User, bool) {
	if id == 0 {
		return nil, true
	}
	users, err := h.storedViewers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "users_failed", err.Error())
		return nil, false
	}
	for i := range users {
		if users[i].ID == id {
			return &users[i].User, true
		}
	}
	writeValidationError(w, []FieldError{{Field: "viewer", Message: "no such viewer"}})
	return nil, false
}

// previewMoment checks a preview's device and defaults its moment to now.
func previewMoment(w http.ResponseWriter, device *string, at *time.Time) bool {
	*device = strings.ToLower(strings.TrimSpace(*device))
	switch *device {
	case "":
		*device = core.DeviceTV
	case core.DeviceTV, core.DeviceMobile, core.DeviceWeb, core.DeviceDesktop:
	default:
		writeValidationError(w, []FieldError{{Field: "device", Message: "must be tv, mobile, web or desktop"}})
		return false
	}
	if at.IsZero() {
		*at = time.Now()
	}
	return true
}

func (h *Handler) handlePagePreview(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	key, ok := pageKey(w, r)
	if !ok {
		return
	}
	var body core.PagePreviewRequest
	if err := decodeJSON(r, &body); err != nil {
		writeValidationError(w, []FieldError{{Field: "body", Message: err.Error()}})
		return
	}
	if !previewMoment(w, &body.Device, &body.At) {
		return
	}
	if body.Count < 0 || body.Count > 100 {
		writeValidationError(w, []FieldError{{Field: "count", Message: "must be 1-100"}})
		return
	}
	if body.Page != nil {
		body.Page.Key = key
	}
	viewer, ok := h.previewViewer(w, r, body.Viewer)
	if !ok {
		return
	}
	out, err := e.PreviewPage(r.Context(), viewer, key, body)
	if err != nil {
		writePageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": out.Summary, "rows": previewRows(out.Rows), "picks": nonNil(out.Picks), "notShown": nonNil(out.NotShown),
		"access": out.Access, "generatedAt": out.GeneratedAt.UTC(),
		"context": map[string]any{"device": out.Context.Device, "daypart": out.Context.Daypart, "weekend": out.Context.Weekend},
	})
}

func (h *Handler) handlePageRowPreview(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	key, ok := pageKey(w, r)
	if !ok {
		return
	}
	var body core.RowPreviewRequest
	if err := decodeJSON(r, &body); err != nil {
		writeValidationError(w, []FieldError{{Field: "body", Message: err.Error()}})
		return
	}
	if !previewMoment(w, &body.Device, &body.At) {
		return
	}
	if body.Count < 0 || body.Count > 40 {
		writeValidationError(w, []FieldError{{Field: "count", Message: "must be 1-40"}})
		return
	}
	viewer, ok := h.previewViewer(w, r, body.Viewer)
	if !ok {
		return
	}
	out, err := e.PreviewPageRow(r.Context(), viewer, key, body)
	if err != nil {
		writePageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": previewRows(out.Rows), "total": out.Total})
}

// handlePageImpact takes {page, device?, at?} and answers "Affects N
// viewers · M have changes".
func (h *Handler) handlePageImpact(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	key, ok := pageKey(w, r)
	if !ok {
		return
	}
	var body core.PagePreviewRequest
	if err := decodeJSON(r, &body); err != nil {
		writeValidationError(w, []FieldError{{Field: "body", Message: err.Error()}})
		return
	}
	if body.Page == nil {
		writeValidationError(w, []FieldError{{Field: "page", Message: "the draft page is required"}})
		return
	}
	if !previewMoment(w, &body.Device, &body.At) {
		return
	}
	body.Page.Key = key
	out, err := e.PageImpact(r.Context(), key, *body.Page, body.Device, body.At)
	if err != nil {
		writePageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCollectionUsage is Collection Studio's "Used on".
func (h *Handler) handleCollectionUsage(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	names := map[core.PageKey]string{}
	if sums, err := pageSummaries(r.Context(), e); err == nil {
		for _, s := range sums {
			names[s.Key] = s.Name
		}
	}
	uses := e.CollectionUsage(r.Context(), r.PathValue("id"), func(k core.PageKey) string {
		if n := names[k]; n != "" {
			return n
		}
		return "A page that no longer exists"
	})
	writeJSON(w, http.StatusOK, map[string]any{"uses": uses})
}

// handleRowSourceFacets lists a library's values of a facet for a row
// parameter picker: {"values": [FacetPick], "next": offset?, "pending"}.
// Values come from the catalogue's cached facet snapshot (never a count
// over the library on the request), filtered by ?q= and paged by
// ?offset=&limit= (limit ≤ 200, default 50).
func (h *Handler) handleRowSourceFacets(w http.ResponseWriter, r *http.Request) {
	e, ok := h.pagesEngine(w)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("library"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found", "no such library")
		return
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// A library page's document names its library (in memory).
	doc, err := e.PageDocument(r.Context(), core.PageHome)
	if err != nil {
		writePageError(w, err)
		return
	}
	var lib *core.PageLibrary
	for i := range doc.Libraries {
		if doc.Libraries[i].ID == core.LibraryID(id) {
			lib = &doc.Libraries[i]
		}
	}
	if lib == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such library")
		return
	}
	out := map[string]any{"values": []core.FacetPick{}}
	facets, _ := h.deps.CatalogHelper.(interface {
		ConsoleFacets(context.Context, core.MediaType, string) ([]core.FacetValue, bool, error)
	})
	// Only catalogue libraries have catalogue facets; plugin libraries'
	// declared facets are not offered as parameters yet.
	if lib.MediaType == "" || facets == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	vals, pending, err := facets.ConsoleFacets(r.Context(), lib.MediaType, r.PathValue("facet"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no such facet")
		return
	}
	if r.PathValue("facet") == "genre" {
		// Ether's anime classification is a genre choice too.
		vals = append([]core.FacetValue{{Key: personalize.GenreAnime, Title: "Anime"}}, vals...)
	}
	needle := strings.ToLower(strings.TrimSpace(q.Get("q")))
	picks := make([]core.FacetPick, 0, limit)
	seen := 0
	for _, v := range vals {
		label := v.Title
		if label == "" {
			label = v.Key
		}
		if needle != "" && !strings.Contains(strings.ToLower(label), needle) && !strings.Contains(strings.ToLower(v.Key), needle) {
			continue
		}
		seen++
		if seen <= offset {
			continue
		}
		if len(picks) == limit {
			out["next"] = offset + limit
			break
		}
		picks = append(picks, core.FacetPick{Value: v.Key, Label: label, Count: v.Count})
	}
	out["values"] = picks
	if pending {
		out["pending"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// previewRow is a served row as the console draws it: titles as
// TitleCards with browser-loadable images.
type previewRow struct {
	HubID       string               `json:"hubId"`
	RowID       string               `json:"rowId,omitempty"`
	Library     core.LibraryID       `json:"library,omitempty"`
	Mode        string               `json:"mode,omitempty"`
	Why         string               `json:"why,omitempty"`
	Note        string               `json:"note,omitempty"`
	Look        core.RowLook         `json:"look"`
	Personal    bool                 `json:"personal"`
	Flags       []string             `json:"flags,omitempty"`
	Ghost       bool                 `json:"ghost,omitempty"`
	Present     core.RowPresentation `json:"present"`
	Title       string               `json:"title"`
	Kind        string               `json:"kind"`
	Source      string               `json:"source"`
	Items       []homeTitleCard      `json:"items"`
	ItemReasons map[string]string    `json:"itemReasons,omitempty"`
}

func previewRows(rows []core.HomeRow) []previewRow {
	out := make([]previewRow, 0, len(rows))
	for _, row := range rows {
		pr := previewRow{HubID: row.HubID, RowID: row.RowID, Library: row.Library, Mode: row.Mode, Why: row.Why, Note: row.Note,
			Look: row.Look, Personal: row.Personal, Flags: row.Flags, Ghost: row.Ghost, Present: row.Present, Title: row.Title,
			Kind: row.Kind, Source: row.Source, Items: make([]homeTitleCard, 0, len(row.Items))}
		for _, t := range row.Items {
			pr.Items = append(pr.Items, homeCard(t))
		}
		for _, c := range row.Hub.Collections {
			poster := adminImageURL(c.PosterPath, 342)
			if c.PosterPath != "" && !validImagePath(c.PosterPath) {
				poster = "/ether/api/collections/" + strconv.FormatInt(c.Key.TMDB(), 10) + "/artwork?card=1&v=" + strconv.FormatInt(c.UpdatedAt.Unix(), 10)
			}
			backdrop := poster
			if validImagePath(c.BackdropPath) {
				backdrop = adminImageURL(c.BackdropPath, 780)
			}
			pr.Items = append(pr.Items, homeTitleCard{Key: c.Key.String(), Type: "collection", Title: c.Title,
				Summary: c.Summary, Poster: poster, Backdrop: backdrop})
		}
		if len(row.ItemReasons) > 0 {
			pr.ItemReasons = make(map[string]string, len(row.ItemReasons))
			for k, v := range row.ItemReasons {
				pr.ItemReasons[k.String()] = v
			}
		}
		out = append(out, pr)
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
