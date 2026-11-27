package vela

import (
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (i *instance) duplicatesSource() (frontend.Duplicates, bool) {
	source, ok := i.host.Experience.(frontend.Duplicates)
	return source, ok
}

func (i *instance) duplicatesAvailable() bool {
	_, ok := i.duplicatesSource()
	return ok
}

// duplicateGroupWire is {recordingId, count, titles, preferredTitleId?}.
func (i *instance) duplicateGroupWire(r *http.Request, v *frontend.Viewer, token string, g frontend.DuplicateGroup) (object, error) {
	titles, err := i.titles(r, v, token, g.Titles)
	if err != nil {
		return nil, err
	}
	out := object{"recordingId": g.RecordingID, "count": g.Count, "titles": titles}
	if g.Preferred != 0 {
		out["preferredTitleId"] = ids.RatingKeyGUID(g.Preferred)
	}
	return out, nil
}

func libraryParam(r *http.Request) (frontend.LibraryID, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return frontend.LibraryID(id), err == nil && id > 0
}

// libraryDuplicates serves GET /vela/v0/libraries/{id}/duplicates?cursor=&limit=.
func (i *instance) libraryDuplicates(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.duplicatesSource()
	lib, okLib := libraryParam(r)
	if !ok || !okLib {
		i.unavailable(w, r)
		return
	}
	page, err := source.LibraryDuplicates(r.Context(), v, lib, r.URL.Query().Get("cursor"), limit(r))
	if err != nil {
		fail(w, err, r)
		return
	}
	items := make([]object, 0, len(page.Groups))
	for _, g := range page.Groups {
		wire, err := i.duplicateGroupWire(r, v, token, g)
		if err != nil {
			fail(w, err, r)
			return
		}
		items = append(items, wire)
	}
	out := object{"items": items, "total": page.Total, "continuation": "exhausted"}
	if page.Next != "" {
		out["continuation"], out["nextCursor"] = "more", page.Next
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, 200, out)
}

// titleRecording serves GET /vela/v0/titles/{id}/recording: every release of
// the recording a title carries.
func (i *instance) titleRecording(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.duplicatesSource()
	if !ok {
		i.unavailable(w, r)
		return
	}
	k, err := key(r)
	if err != nil {
		problem(w, 404, "not_found", "not_found")
		return
	}
	g, err := source.TitleRecording(r.Context(), v, k)
	if err != nil {
		fail(w, err, r)
		return
	}
	wire, err := i.duplicateGroupWire(r, v, token, g)
	if err != nil {
		fail(w, err, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, 200, wire)
}

// preferRecording serves POST /vela/v0/libraries/{id}/duplicates/{recordingId}
// {preferredTitleId, hideOtherIds?}: the viewer's preferred release.
func (i *instance) preferRecording(w http.ResponseWriter, r *http.Request) {
	v, token, err := i.viewer(r)
	if err != nil {
		fail(w, err, r)
		return
	}
	source, ok := i.duplicatesSource()
	lib, okLib := libraryParam(r)
	if !ok || !okLib {
		i.unavailable(w, r)
		return
	}
	var b struct {
		PreferredTitleID string   `json:"preferredTitleId"`
		HideOtherIDs     []string `json:"hideOtherIds"`
	}
	if body(r, &b) != nil || b.PreferredTitleID == "" {
		problem(w, 400, "request", "invalid_request")
		return
	}
	preferred, err := ids.ParseRatingKeyGUID(b.PreferredTitleID)
	if err != nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	hide, err := parsePlaylistTitleIDs(b.HideOtherIDs)
	if err != nil {
		problem(w, 400, "request", "invalid_request")
		return
	}
	g, err := source.PreferRecording(r.Context(), v, lib, r.PathValue("recordingId"), preferred, hide)
	if err != nil {
		fail(w, err, r)
		return
	}
	wire, err := i.duplicateGroupWire(r, v, token, g)
	if err != nil {
		fail(w, err, r)
		return
	}
	send(w, 200, wire)
}
