package admin

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// LayoutLibrary describes a library whose Recommended tab has its own
// "library:<id>" layout page.
type LayoutLibrary struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	Type       string `json:"type"`       // "movie" | "show" | "plugin"
	Section    string `json:"section"`    // "library:<id>"
	Customized bool   `json:"customized"` // true when Global contains section == "library:<id>"
}

// ViewerLibraryPage pairs a viewer id with that viewer's override page for a
// library's Recommended layout.
type ViewerLibraryPage struct {
	UserID int64           `json:"userId"`
	Page   core.LayoutPage `json:"page"`
}

type candidateRowPreviewer interface {
	PreviewCandidateRows(ctx context.Context, user *core.User, hc core.HubContext, section string, defaultMaxRows int, d core.HomeLayout, rows []core.HomeRow) (*core.HomePreview, error)
}

// collectionScheduler is the Collection Studio capability the Home
// designer uses for collection rows: a collection owns its row schedule,
// and the layout document carries it as collectionSchedules.
type collectionScheduler interface {
	CollectionSchedules(ctx context.Context) (map[string]*core.CollectionSchedule, error)
	ValidateSchedule(sc *core.CollectionSchedule) error
	SetSchedule(ctx context.Context, id string, sc *core.CollectionSchedule) error
}

func (h *Handler) scheduler() collectionScheduler {
	s, _ := h.deps.Collections.(collectionScheduler)
	return s
}

func layoutPageFor(ps []core.LayoutPage, section string) *core.LayoutPage {
	for i := range ps {
		if ps[i].Section == section {
			return &ps[i]
		}
	}
	return nil
}

func cloneLayoutPage(p core.LayoutPage) core.LayoutPage {
	raw, _ := json.Marshal(p)
	var out core.LayoutPage
	_ = json.Unmarshal(raw, &out)
	if out.Rows == nil {
		out.Rows = []core.RowPolicy{}
	}
	return out
}

func (h *Handler) layoutLibraries(ctx context.Context, d core.HomeLayout) ([]LayoutLibrary, error) {
	if h.deps.LayoutLibraries == nil {
		return []LayoutLibrary{}, nil
	}
	libs, err := h.deps.LayoutLibraries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LayoutLibrary, 0, len(libs))
	for _, l := range libs {
		if l.Section == "" && l.ID > 0 {
			l.Section = "library:" + strconv.Itoa(l.ID)
		}
		l.Customized = layoutPageFor(d.Global, l.Section) != nil
		out = append(out, l)
	}
	return out, nil
}

func (h *Handler) resolveLayoutLibrary(w http.ResponseWriter, r *http.Request, d core.HomeLayout) (LayoutLibrary, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_id", "library id must be a positive number")
		return LayoutLibrary{}, false
	}
	libs, err := h.layoutLibraries(r.Context(), d)
	if err != nil {
		h.writeServiceError(w, err)
		return LayoutLibrary{}, false
	}
	for _, l := range libs {
		if l.ID == id {
			return l, true
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "no such library")
	return LayoutLibrary{}, false
}

// writeLayout answers with the saved Home layout document (with every
// collection's schedule), the engine's per-section row defaults, the rows the
// document can target, and the libraries eligible for per-library Recommended
// layouts.
func (h *Handler) writeLayout(w http.ResponseWriter, r *http.Request, d core.HomeLayout) {
	avail, err := h.deps.Personalization.AvailableRows(r.Context())
	if err != nil {
		writeEngineError(w, err)
		return
	}
	libs, err := h.layoutLibraries(r.Context(), d)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	d.CollectionSchedules = nil
	if s := h.scheduler(); s != nil {
		if d.CollectionSchedules, err = s.CollectionSchedules(r.Context()); err != nil {
			h.writeServiceError(w, err)
			return
		}
	}
	if avail == nil {
		avail = []core.RowDescriptor{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"layout":        d,
		"defaults":      map[string]any{"maxRows": h.deps.Personalization.LayoutDefaults()},
		"availableRows": avail,
		"libraries":     libs,
	})
}

func (h *Handler) defaultOwnerViewer(ctx context.Context) *core.User {
	if users, err := h.storedViewers(ctx); err == nil {
		for _, v := range users {
			if v.IsOwner {
				copy := v.User
				return &copy
			}
		}
		if len(users) > 0 {
			copy := users[0].User
			return &copy
		}
	}
	return &core.User{ID: 1, IsOwner: true}
}

// writeLibraryLayout answers with a single library's Recommended layout page,
// viewer overrides, filtered row candidates, defaults, and collection schedules
// so the library's Tabs page edits that library's own Recommended rows directly.
func (h *Handler) writeLibraryLayout(w http.ResponseWriter, r *http.Request, lib LayoutLibrary, d core.HomeLayout) {
	schedules := map[string]*core.CollectionSchedule{}
	if s := h.scheduler(); s != nil {
		if loaded, serr := s.CollectionSchedules(r.Context()); serr != nil {
			h.writeServiceError(w, serr)
			return
		} else if loaded != nil {
			schedules = loaded
		}
	}
	d.CollectionSchedules = schedules

	customPage := layoutPageFor(d.Global, lib.Section)
	lib.Customized = customPage != nil

	effective := core.LayoutPage{Section: lib.Section, Rows: []core.RowPolicy{}}
	if customPage != nil {
		effective = cloneLayoutPage(*customPage)
	}

	viewerPages := []ViewerLibraryPage{}
	for _, vl := range d.Viewers {
		if vp := layoutPageFor(vl.Pages, lib.Section); vp != nil {
			cp := cloneLayoutPage(*vp)
			cp.Section = lib.Section
			viewerPages = append(viewerPages, ViewerLibraryPage{UserID: vl.UserID, Page: cp})
		}
	}

	var candidates []core.RowDescriptor
	var maxRows int
	if lib.Type == "plugin" {
		maxRows = 8
		if h.deps.PluginLibraryRows != nil {
			descs, _, err := h.deps.PluginLibraryRows(r.Context(), h.defaultOwnerViewer(r.Context()), lib.ID)
			if err != nil && !errors.Is(err, core.ErrNotFound) {
				h.writeServiceError(w, err)
				return
			}
			candidates = descs
		}
		if candidates == nil {
			candidates = []core.RowDescriptor{}
		}
		if pluginDescriptorsHaveDeclaredShelves(candidates) && len(candidates) > maxRows {
			maxRows = len(candidates)
		}
	} else {
		avail, err := h.deps.Personalization.AvailableRows(r.Context())
		if err != nil {
			writeEngineError(w, err)
			return
		}
		surface := "movies"
		if lib.Type == string(core.TypeShow) {
			surface = "shows"
		}
		candidates = make([]core.RowDescriptor, 0, len(avail))
		for _, row := range avail {
			if slices.Contains(row.Sections, surface) || slices.Contains(row.Sections, lib.Section) {
				candidates = append(candidates, row)
			}
		}
		defaults := h.deps.Personalization.LayoutDefaults()
		maxRows = defaults["library"]
		if maxRows <= 0 {
			maxRows = 16
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"library":             lib,
		"customized":          lib.Customized,
		"effectivePage":       effective,
		"viewerPages":         viewerPages,
		"defaults":            map[string]any{"maxRows": maxRows},
		"availableRows":       candidates,
		"collectionSchedules": schedules,
		"layout":              d,
	})
}

func (h *Handler) validateAndApplySchedules(w http.ResponseWriter, r *http.Request, schedules map[string]*core.CollectionSchedule, prefix string) (collectionScheduler, bool) {
	sched := h.scheduler()
	if len(schedules) == 0 {
		return sched, true
	}
	if sched == nil {
		writeValidationError(w, []FieldError{{Field: prefix, Message: "collections are unavailable"}})
		return nil, false
	}
	var fields []FieldError
	for _, id := range slices.Sorted(maps.Keys(schedules)) {
		var invalid interface{ InvalidFields() map[string]string }
		if err := sched.ValidateSchedule(schedules[id]); errors.As(err, &invalid) {
			for _, f := range slices.Sorted(maps.Keys(invalid.InvalidFields())) {
				fields = append(fields, FieldError{Field: prefix + "." + id + "." + f, Message: invalid.InvalidFields()[f]})
			}
		} else if err != nil {
			h.writeServiceError(w, err)
			return nil, false
		}
	}
	if len(fields) > 0 {
		writeValidationError(w, fields)
		return nil, false
	}
	return sched, true
}

func (h *Handler) commitSchedules(w http.ResponseWriter, r *http.Request, sched collectionScheduler, schedules map[string]*core.CollectionSchedule) bool {
	if len(schedules) == 0 || sched == nil {
		return true
	}
	for _, id := range slices.Sorted(maps.Keys(schedules)) {
		// A collection deleted since the designer loaded has no schedule to keep.
		if err := sched.SetSchedule(r.Context(), id, schedules[id]); err != nil && !errors.Is(err, core.ErrNotFound) {
			h.writeServiceError(w, err)
			return false
		}
	}
	h.invalidateOwnerRows()
	return true
}

func (h *Handler) handleGetLayout(w http.ResponseWriter, r *http.Request) {
	d, err := h.deps.Personalization.HomeLayout(r.Context())
	if err != nil {
		writeEngineError(w, err)
		return
	}
	h.writeLayout(w, r, d)
}

func (h *Handler) handlePutLayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Layout *core.HomeLayout `json:"layout"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	if body.Layout == nil {
		writeValidationError(w, []FieldError{{Field: "layout", Message: "is required"}})
		return
	}
	// Edited collection schedules (null clears one) are saved to their
	// collections once the layout itself is valid and saved.
	schedules := body.Layout.CollectionSchedules
	body.Layout.CollectionSchedules = nil
	sched, ok := h.validateAndApplySchedules(w, r, schedules, "layout.collectionSchedules")
	if !ok {
		return
	}
	d, err := h.deps.Personalization.SetHomeLayout(r.Context(), *body.Layout)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if !h.commitSchedules(w, r, sched, schedules) {
		return
	}
	h.writeLayout(w, r, d)
}

func (h *Handler) handleGetLibraryLayout(w http.ResponseWriter, r *http.Request) {
	d, err := h.deps.Personalization.HomeLayout(r.Context())
	if err != nil {
		writeEngineError(w, err)
		return
	}
	lib, ok := h.resolveLayoutLibrary(w, r, d)
	if !ok {
		return
	}
	h.writeLibraryLayout(w, r, lib, d)
}

func (h *Handler) handlePutLibraryLayout(w http.ResponseWriter, r *http.Request) {
	d, err := h.deps.Personalization.HomeLayout(r.Context())
	if err != nil {
		writeEngineError(w, err)
		return
	}
	lib, ok := h.resolveLayoutLibrary(w, r, d)
	if !ok {
		return
	}
	var body struct {
		Page                *core.LayoutPage                    `json:"page"`
		ViewerPages         *[]ViewerLibraryPage                `json:"viewerPages"`
		CollectionSchedules map[string]*core.CollectionSchedule `json:"collectionSchedules"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return
	}
	if body.Page == nil {
		writeValidationError(w, []FieldError{{Field: "page", Message: "is required"}})
		return
	}
	sched, ok := h.validateAndApplySchedules(w, r, body.CollectionSchedules, "collectionSchedules")
	if !ok {
		return
	}
	page := cloneLayoutPage(*body.Page)
	if page.Section != "" && page.Section != lib.Section {
		writeValidationError(w, []FieldError{{Field: "page.section", Message: "must match " + lib.Section}})
		return
	}
	page.Section = lib.Section

	replaced := false
	for i := range d.Global {
		if d.Global[i].Section == lib.Section {
			d.Global[i] = page
			replaced = true
			break
		}
	}
	if !replaced {
		d.Global = append(d.Global, page)
	}

	if body.ViewerPages != nil {
		byUser := map[int64]core.LayoutPage{}
		for idx, vp := range *body.ViewerPages {
			if vp.UserID <= 0 {
				writeValidationError(w, []FieldError{{Field: "viewerPages[" + strconv.Itoa(idx) + "].userId", Message: "must be a positive viewer id"}})
				return
			}
			if _, dup := byUser[vp.UserID]; dup {
				writeValidationError(w, []FieldError{{Field: "viewerPages[" + strconv.Itoa(idx) + "].userId", Message: "duplicate viewer id"}})
				return
			}
			cp := cloneLayoutPage(vp.Page)
			cp.Section = lib.Section
			byUser[vp.UserID] = cp
		}
		keptViewers := make([]core.ViewerLayout, 0, len(d.Viewers)+len(byUser))
		seenUsers := map[int64]bool{}
		for _, vl := range d.Viewers {
			pages := make([]core.LayoutPage, 0, len(vl.Pages)+1)
			for _, p := range vl.Pages {
				if p.Section != lib.Section {
					pages = append(pages, p)
				}
			}
			if vp, exists := byUser[vl.UserID]; exists {
				pages = append(pages, vp)
				seenUsers[vl.UserID] = true
			}
			if len(pages) > 0 {
				vl.Pages = pages
				keptViewers = append(keptViewers, vl)
			}
		}
		for _, uid := range slices.Sorted(maps.Keys(byUser)) {
			if !seenUsers[uid] {
				keptViewers = append(keptViewers, core.ViewerLayout{
					UserID: uid,
					Pages:  []core.LayoutPage{byUser[uid]},
				})
			}
		}
		d.Viewers = keptViewers
	}

	d, err = h.deps.Personalization.SetHomeLayout(r.Context(), d)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	if !h.commitSchedules(w, r, sched, body.CollectionSchedules) {
		return
	}
	h.writeLibraryLayout(w, r, lib, d)
}

func (h *Handler) handleResetLibraryLayout(w http.ResponseWriter, r *http.Request) {
	d, err := h.deps.Personalization.HomeLayout(r.Context())
	if err != nil {
		writeEngineError(w, err)
		return
	}
	lib, ok := h.resolveLayoutLibrary(w, r, d)
	if !ok {
		return
	}
	global := make([]core.LayoutPage, 0, len(d.Global))
	for _, p := range d.Global {
		if p.Section != lib.Section {
			global = append(global, p)
		}
	}
	d.Global = global

	viewers := make([]core.ViewerLayout, 0, len(d.Viewers))
	for _, vl := range d.Viewers {
		pages := make([]core.LayoutPage, 0, len(vl.Pages))
		for _, p := range vl.Pages {
			if p.Section != lib.Section {
				pages = append(pages, p)
			}
		}
		if len(pages) > 0 {
			vl.Pages = pages
			viewers = append(viewers, vl)
		}
	}
	d.Viewers = viewers

	d, err = h.deps.Personalization.SetHomeLayout(r.Context(), d)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	h.writeLibraryLayout(w, r, lib, d)
}

type layoutPreviewBody struct {
	ViewerID            *int64                              `json:"viewerId"`
	Layout              *core.HomeLayout                    `json:"layout"`
	Page                *core.LayoutPage                    `json:"page"`
	ViewerPages         *[]ViewerLibraryPage                `json:"viewerPages"`
	CollectionSchedules map[string]*core.CollectionSchedule `json:"collectionSchedules"`
	Device              string                              `json:"device"`
	At                  string                              `json:"at"`
	Section             string                              `json:"section"`
	Count               int                                 `json:"count"`
}

func applyDraftPageOverlay(d *core.HomeLayout, section string, page *core.LayoutPage, viewerPages *[]ViewerLibraryPage, schedules map[string]*core.CollectionSchedule) {
	if page != nil {
		cp := cloneLayoutPage(*page)
		if cp.Section == "" {
			sec := strings.TrimSpace(section)
			if sec == "" {
				sec = "home"
			} else if id, err := strconv.Atoi(sec); err == nil && id > 0 {
				sec = "library:" + strconv.Itoa(id)
			}
			cp.Section = sec
		}
		replaced := false
		for i := range d.Global {
			if d.Global[i].Section == cp.Section {
				d.Global[i] = cp
				replaced = true
				break
			}
		}
		if !replaced {
			d.Global = append(d.Global, cp)
		}
		if viewerPages != nil {
			byUser := map[int64]core.LayoutPage{}
			for _, vp := range *viewerPages {
				if vp.UserID > 0 {
					vcp := cloneLayoutPage(vp.Page)
					vcp.Section = cp.Section
					byUser[vp.UserID] = vcp
				}
			}
			keptViewers := make([]core.ViewerLayout, 0, len(d.Viewers)+len(byUser))
			seen := map[int64]bool{}
			for _, vl := range d.Viewers {
				pages := make([]core.LayoutPage, 0, len(vl.Pages)+1)
				for _, p := range vl.Pages {
					if p.Section != cp.Section {
						pages = append(pages, p)
					}
				}
				if vp, ok := byUser[vl.UserID]; ok {
					pages = append(pages, vp)
					seen[vl.UserID] = true
				}
				if len(pages) > 0 {
					vl.Pages = pages
					keptViewers = append(keptViewers, vl)
				}
			}
			for _, uid := range slices.Sorted(maps.Keys(byUser)) {
				if !seen[uid] {
					keptViewers = append(keptViewers, core.ViewerLayout{UserID: uid, Pages: []core.LayoutPage{byUser[uid]}})
				}
			}
			d.Viewers = keptViewers
		}
	}
	if len(schedules) > 0 {
		if d.CollectionSchedules == nil {
			d.CollectionSchedules = map[string]*core.CollectionSchedule{}
		}
		for k, v := range schedules {
			d.CollectionSchedules[k] = v
		}
	}
}

func (h *Handler) previewSectionLayout(ctx context.Context, e Personalization, u *core.User, hc core.HubContext, section string, count int, d core.HomeLayout) (*core.HomePreview, error) {
	sec := strings.TrimSpace(section)
	if id, err := strconv.Atoi(sec); err == nil && id > 0 {
		sec = "library:" + strconv.Itoa(id)
	}
	if strings.HasPrefix(sec, "library:") && h.deps.LayoutLibraries != nil && h.deps.PluginLibraryRows != nil {
		if cp, ok := e.(candidateRowPreviewer); ok {
			if libs, err := h.layoutLibraries(ctx, d); err == nil {
				for _, lib := range libs {
					if lib.Section == sec && lib.Type == "plugin" {
						fetchUser := u
						if fetchUser == nil || fetchUser.ID <= 0 {
							fetchUser = h.defaultOwnerViewer(ctx)
						}
						_, rows, rErr := h.deps.PluginLibraryRows(ctx, fetchUser, lib.ID)
						if rErr != nil && !errors.Is(rErr, core.ErrNotFound) {
							return nil, rErr
						}
						if count > 0 {
							for i := range rows {
								if len(rows[i].Items) > count {
									rows[i].Items = rows[i].Items[:count]
									rows[i].Hub.Items = rows[i].Items
								}
							}
						}
						defaultMaxRows := 8
						if pluginHomeRowsHaveDeclaredShelves(rows) && len(rows) > defaultMaxRows {
							defaultMaxRows = len(rows)
						}
						return cp.PreviewCandidateRows(ctx, u, hc, sec, defaultMaxRows, d, rows)
					}
				}
			}
		}
	}
	return e.PreviewLayout(ctx, u, hc, section, count, d)
}

func pluginDescriptorsHaveDeclaredShelves(rows []core.RowDescriptor) bool {
	for _, r := range rows {
		if r.Kind == "shelf" || strings.Contains(r.HubID, ":shelf:") {
			return true
		}
	}
	return false
}

func pluginHomeRowsHaveDeclaredShelves(rows []core.HomeRow) bool {
	for _, r := range rows {
		if r.Kind == "shelf" || strings.Contains(r.HubID, ":shelf:") {
			return true
		}
	}
	return false
}

func (h *Handler) decodeLayoutPreview(w http.ResponseWriter, r *http.Request) (layoutPreviewBody, core.HubContext, Personalization, bool) {
	var b layoutPreviewBody
	var hc core.HubContext
	e := h.deps.Personalization
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, 400, "bad_json", err.Error())
		return b, hc, e, false
	}
	if b.Device == "" {
		b.Device = core.DeviceTV
	}
	switch b.Device {
	case core.DeviceTV, core.DeviceMobile, core.DeviceWeb, core.DeviceDesktop:
	default:
		writeValidationError(w, []FieldError{{Field: "device", Message: "must be tv, mobile, web or desktop"}})
		return b, hc, e, false
	}
	at := time.Now()
	if b.At != "" {
		var err error
		at, err = time.Parse(time.RFC3339, b.At)
		if err != nil {
			writeValidationError(w, []FieldError{{Field: "at", Message: "must be an RFC 3339 time"}})
			return b, hc, e, false
		}
	}
	if b.Count == 0 {
		b.Count = 12
	}
	if b.Count < 1 || b.Count > 100 {
		writeValidationError(w, []FieldError{{Field: "count", Message: "must be 1-100"}})
		return b, hc, e, false
	}
	if b.Layout == nil {
		d, err := e.HomeLayout(r.Context())
		if err != nil {
			writeEngineError(w, err)
			return b, hc, e, false
		}
		b.Layout = &d
	}
	applyDraftPageOverlay(b.Layout, b.Section, b.Page, b.ViewerPages, b.CollectionSchedules)
	hc = core.NewHubContext(at.Local(), "", "", "")
	hc.Device = b.Device
	return b, hc, e, true
}
func (h *Handler) handleLayoutPreview(w http.ResponseWriter, r *http.Request) {
	b, hc, e, ok := h.decodeLayoutPreview(w, r)
	if !ok {
		return
	}
	u := &core.User{IsOwner: true}
	var viewer any
	if b.ViewerID != nil {
		users, err := h.storedViewers(r.Context())
		if err != nil {
			writeEngineError(w, err)
			return
		}
		found := false
		for _, v := range users {
			if v.ID == *b.ViewerID {
				copy := v.User
				u = &copy
				found = true
				viewer = map[string]any{"id": v.ID, "title": toViewer(v, core.ViewerStats{}).Title}
				break
			}
		}
		if !found {
			writeError(w, 404, "not_found", "no such viewer")
			return
		}
	}
	p, err := h.previewSectionLayout(r.Context(), e, u, hc, b.Section, b.Count, *b.Layout)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	h.writeHomePreview(w, p, viewer)
}
func overrideSettings(d core.HomeLayout, id int64) int {
	n := 0
	for _, v := range d.Viewers {
		if v.UserID == id {
			for _, p := range v.Pages {
				n += len(p.Rows) + len(p.DeviceMaxRows)
				if p.MaxRows > 0 {
					n++
				}
			}
		}
	}
	return n
}
func (h *Handler) handleLayoutImpact(w http.ResponseWriter, r *http.Request) {
	b, hc, e, ok := h.decodeLayoutPreview(w, r)
	if !ok {
		return
	}
	users, err := h.storedViewers(r.Context())
	if err != nil {
		writeEngineError(w, err)
		return
	}
	saved, err := e.HomeLayout(r.Context())
	if err != nil {
		writeEngineError(w, err)
		return
	}
	results := []map[string]any{}
	overrides, changed := 0, 0
	for _, u := range users {
		old, err := h.previewSectionLayout(r.Context(), e, &u.User, hc, b.Section, b.Count, saved)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		next, err := h.previewSectionLayout(r.Context(), e, &u.User, hc, b.Section, b.Count, *b.Layout)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		before, after := map[string]int{}, map[string]int{}
		for _, row := range old.Rows {
			if !row.Hidden {
				before[row.HubID] = len(before) + 1
			}
		}
		for _, row := range next.Rows {
			if !row.Hidden {
				after[row.HubID] = len(after) + 1
			}
		}
		added, removed := []string{}, []string{}
		moved := 0
		for _, row := range next.Rows {
			if row.Hidden {
				continue
			}
			if pos := before[row.HubID]; pos == 0 {
				added = append(added, row.Title)
			} else if pos != after[row.HubID] {
				moved++
			}
		}
		for _, row := range old.Rows {
			if !row.Hidden && after[row.HubID] == 0 {
				removed = append(removed, row.Title)
			}
		}
		n := overrideSettings(*b.Layout, u.ID)
		if n > 0 {
			overrides++
		}
		diff := len(added)+len(removed)+moved > 0
		if diff {
			changed++
		}
		results = append(results, map[string]any{"viewerId": u.ID, "title": toViewer(u, core.ViewerStats{}).Title, "overrides": n, "changed": diff, "added": added, "removed": removed, "moved": moved})
	}
	writeJSON(w, 200, map[string]any{"viewers": results, "summary": map[string]int{"viewers": len(users), "withOverrides": overrides, "changed": changed}})
}
